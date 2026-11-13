// Package client spricht HTTPS mit dem tokenlore-Server und vertraut dabei der gepinnten Stamm-CA oder, ohne sie, den System-Zertifikaten.
package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
)

const (
	// maxResponse begrenzt jede gelesene Antwort; der Server schickt nie mehr als ein paar hundert Bytes.
	maxResponse      = 64 << 10
	handshakeTimeout = 10 * time.Second
	maxHeaderWait    = 30 * time.Second
)

// Version ist die Programmversion für den User-Agent; main setzt sie beim Start.
var Version = "dev"

// userAgent ist der Wert des Headers User-Agent aller Anfragen.
func userAgent() string { return "tokenlore/" + Version }

var dialer = &net.Dialer{Timeout: 10 * time.Second}

// noRedirect: Eine Weiterleitung könnte den Schlüssel an ein anderes Ziel tragen; sie wird nie verfolgt.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Client spricht mit genau einem Server und vertraut nur einer gepinnten Stamm-CA oder den System-Zertifikaten.
// Gepinnt ist die Stamm-CA, nicht das Blatt: Caddy erneuert Blatt und Zwischen-CA laufend.
type Client struct {
	base      *url.URL
	hc        *http.Client
	transport *http.Transport
	pinned    bool // true: nur gepinnte CA; false: System-Zertifikate
}

// New baut einen Client, dessen TLS nur caPEM als Stamm vertraut, mit normaler Ketten- und Hostnamen-Prüfung.
// Ist caPEM nil, gelten die System-Zertifikate (Server mit öffentlich beglaubigtem Zertifikat, z. B. Let's Encrypt).
// Proxy-Variablen werden ignoriert (Proxy = nil), Weiterleitungen nicht verfolgt.
func New(base *url.URL, caPEM []byte, timeout time.Duration) (*Client, error) {
	if base == nil {
		return nil, errors.New("Server-Adresse fehlt")
	}
	// Ohne Gesamtgrenze könnte ein stummer Server den Abgleich beliebig lange festhalten.
	if timeout <= 0 {
		return nil, errors.New("Zeitgrenze muss größer als 0 sein")
	}
	var pool *x509.CertPool // nil: System-Zertifikate
	if caPEM != nil {
		ca, err := firstCACert(caPEM)
		if err != nil {
			return nil, fmt.Errorf("gespeicherte CA unbrauchbar: %w", err)
		}
		pool = x509.NewCertPool()
		pool.AddCert(ca)
	}
	tr := &http.Transport{
		Proxy:               nil,
		TLSClientConfig:     &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: handshakeTimeout,
		DialContext:         dialer.DialContext,
		MaxIdleConns:        2,
		IdleConnTimeout:     30 * time.Second,
		// Zusätzlich zur Gesamtgrenze: Kopfzeilen müssen spätestens nach 30 s da sein.
		ResponseHeaderTimeout: min(timeout, maxHeaderWait),
	}
	return &Client{
		base:      base,
		pinned:    caPEM != nil,
		transport: tr,
		hc:        &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: noRedirect},
	}, nil
}

// PairResult ist die geprüfte Antwort auf eine Kopplung.
type PairResult struct {
	DeviceID                 int64
	DeviceKey, CAFingerprint string
	Repaired                 bool // ein bestehendes Gerät wurde neu gekoppelt
	// SignKeysRefused sind gesendete Signaturschlüssel, die der Server nicht übernommen hat, weil sie einem
	// anderen Gerät gehören. Nur Schlüssel, die in der Anfrage standen; leer, wenn keiner.
	SignKeysRefused []string
}

// SignKeys sind die öffentlichen Signaturschlüssel, die Pair meldet: der aktuelle und die früheren, ersetzten. Priv
// ist der private Teil des aktuellen; mit ihm bildet Pair den Besitznachweis. Er geht nie an den Server.
type SignKeys struct {
	Public   string
	Previous []string
	Priv     ed25519.PrivateKey
}

func (k SignKeys) validate() error {
	if !protocol.ValidSignPublicKey(k.Public) {
		return errors.New("Signaturschlüssel ungültig")
	}
	if len(k.Priv) != ed25519.PrivateKeySize || protocol.EncodeSignPublicKey(k.Priv.Public().(ed25519.PublicKey)) != k.Public {
		return errors.New("privater Signaturschlüssel fehlt oder passt nicht zum öffentlichen")
	}
	if len(k.Previous) > protocol.MaxSignKeysPrevious {
		return fmt.Errorf("mehr als %d frühere Signaturschlüssel", protocol.MaxSignKeysPrevious)
	}
	for _, p := range k.Previous {
		if !protocol.ValidSignPublicKey(p) {
			return errors.New("früherer Signaturschlüssel ungültig")
		}
	}
	return nil
}

// PairPredecessor sind die Felder zur Frage nach dem Vorgänger (protocol/doc.md, „Vorgänger“). Source ist
// predecessor_source, Answer predecessor_answer (AnswerSame, AnswerNew, AnswerLater); leer heißt, das Feld fehlt.
// Der Aufrufer setzt sie nur, wenn der Server das Merkmal FeaturePredecessor nennt.
type PairPredecessor struct {
	Source, Answer string
}

func (p PairPredecessor) validate() error {
	if p.Source != "" && !protocol.ServerIDPattern.MatchString(p.Source) {
		return errors.New("Kennung des Vorgängers ungültig")
	}
	switch p.Answer {
	case "", AnswerSame, AnswerNew, AnswerLater:
		return nil
	}
	return errors.New("Antwort auf die Frage nach dem Vorgänger ungültig")
}

// Pair tauscht den einmaligen Kopplungscode gegen Geräteschlüssel und CA-Fingerabdruck.
// deviceSource ist die Kennung dieses Geräts für den Server (siehe protocol/doc.md, „Koppeln“). nameExplicit heißt,
// der Name wurde mit --name gewählt: Nur dann ersetzt er beim Neukoppeln den Namen auf dem Server. Das Feld
// device_name_explicit geht nur als true mit. plugin_version ist Version; sign die Signaturschlüssel dieses Rechners,
// sign_public_keys_previous geht nur mit, wenn es frühere gibt. Ungültige Schlüssel gehen nie raus. serverID ist die
// server_id aus Info; der Besitznachweis gilt nur für diesen Server. pred trägt predecessor_source und
// predecessor_answer; fragt der Server nach dem Vorgänger, ist der Fehler ein *PredecessorQuestionError.
func (c *Client) Pair(ctx context.Context, code, name, goos, goarch, deviceSource, serverID string, nameExplicit bool, sign SignKeys, pred PairPredecessor) (PairResult, error) {
	if !protocol.ServerIDPattern.MatchString(serverID) {
		return PairResult{}, errors.New("server_id fehlt oder ist ungültig")
	}
	if err := sign.validate(); err != nil {
		return PairResult{}, err
	}
	if err := pred.validate(); err != nil {
		return PairResult{}, err
	}
	reqBody, err := json.Marshal(struct {
		Code          string   `json:"code"`
		DeviceName    string   `json:"device_name"`
		NameExplicit  bool     `json:"device_name_explicit,omitempty"`
		OS            string   `json:"os"`
		Arch          string   `json:"arch"`
		PluginVersion string   `json:"plugin_version"`
		DeviceSource  string   `json:"device_source"`
		SignPublic    string   `json:"sign_public_key"`
		SignProof     string   `json:"sign_public_key_proof"`
		SignPrevious  []string `json:"sign_public_keys_previous,omitempty"`
		PredSource    string   `json:"predecessor_source,omitempty"`
		PredAnswer    string   `json:"predecessor_answer,omitempty"`
	}{code, name, nameExplicit, goos, goarch, Version, deviceSource, sign.Public, protocol.SignPairProof(sign.Priv, serverID, code), sign.Previous,
		pred.Source, pred.Answer})
	if err != nil {
		return PairResult{}, err
	}
	status, hdr, body, tooBig, err := c.post(ctx, "/api/plugin/pair", "", reqBody)
	if err != nil {
		return PairResult{}, err
	}
	if status == http.StatusUnauthorized {
		return PairResult{}, ErrBadCode
	}
	if status == http.StatusConflict {
		if q, isQuestion := predecessorQuestion(body, tooBig); isQuestion {
			if q == nil {
				return PairResult{}, errors.New("Frage des Servers nach dem Vorgänger nicht lesbar")
			}
			return PairResult{}, q
		}
	}
	if status != http.StatusOK {
		err := mapStatus(status, hdr, body, tooBig, "")
		var rej *RejectedError
		if status == http.StatusConflict && errors.As(err, &rej) && rej.Msg == "source_taken" {
			return PairResult{}, ErrSourceTaken
		}
		return PairResult{}, err
	}
	if tooBig {
		return PairResult{}, errors.New("Antwort des Servers zu groß")
	}
	var r struct {
		DeviceID      int64    `json:"device_id"`
		DeviceKey     string   `json:"device_key"`
		CAFingerprint string   `json:"ca_fingerprint"`
		Repaired      bool     `json:"repaired"` // fehlt es (älterer Server), gilt false
		Refused       []string `json:"sign_keys_refused"`
	}
	// Die Fehlermeldungen nennen nie Inhalte der Antwort: Sie enthält den Schlüssel.
	if json.Unmarshal(body, &r) != nil {
		return PairResult{}, errors.New("Antwort auf die Kopplung nicht lesbar")
	}
	if r.DeviceID <= 0 {
		return PairResult{}, errors.New("Antwort auf die Kopplung: Geräte-ID ungültig")
	}
	if !protocol.DeviceKeyPattern.MatchString(r.DeviceKey) {
		return PairResult{}, errors.New("Antwort auf die Kopplung: Geräteschlüssel ungültig")
	}
	// Leer ist gültig: Ein Server ohne eigene CA (öffentliches Zertifikat) hat keinen Fingerabdruck.
	// Ob das reicht, entscheidet der Aufrufer (mit --ca muss er passen).
	fp := ""
	if r.CAFingerprint != "" {
		var err error
		if fp, err = NormalizeFingerprint(r.CAFingerprint); err != nil {
			return PairResult{}, fmt.Errorf("Antwort auf die Kopplung: %w", err)
		}
	}
	// Nur gesendete Schlüssel übernehmen: Was der Server sonst nennt, könnte beliebiger Text sein.
	var refused []string
	for _, k := range r.Refused {
		if (k == sign.Public || slices.Contains(sign.Previous, k)) && !slices.Contains(refused, k) {
			refused = append(refused, k)
		}
	}
	return PairResult{DeviceID: r.DeviceID, DeviceKey: r.DeviceKey, CAFingerprint: fp, Repaired: r.Repaired, SignKeysRefused: refused}, nil
}

// IngestResult ist die Antwort auf eine Lieferung (protocol/doc.md, „Liefern“).
type IngestResult struct {
	Requests, LegacyDays, Sessions, Accounts int
	FlaggedSessions                          []string
	// KnownRequests zählt die Anfragen des Pakets, die dieser Benutzer schon geliefert hatte; der Server ließ sie
	// unverändert.
	KnownRequests int
	// ElsewhereRequests zählt die Anfragen des Pakets, die schon einem anderen Benutzer gehören; der Server hat
	// sie nicht gespeichert.
	ElsewhereRequests int
	// LegacyRejectedDays sind die Vorgeschichte-Tage des Pakets, die der Server abgewiesen hat, weil ein anderes
	// Gerät dort genau dieselben Werte hat.
	LegacyRejectedDays []string
}

// Ingest liefert ein fertiges Paket (JSON) mit dem Geräteschlüssel aus.
func (c *Client) Ingest(ctx context.Context, key string, body []byte) (IngestResult, error) {
	// Prüfen statt senden: Ein kaputter Schlüssel könnte sonst Kopfzeilen einschleusen.
	if !protocol.DeviceKeyPattern.MatchString(key) {
		return IngestResult{}, errors.New("Geräteschlüssel hat ein ungültiges Format")
	}
	status, hdr, resp, tooBig, err := c.post(ctx, "/api/plugin/ingest", key, body)
	if err != nil {
		return IngestResult{}, err
	}
	if status == http.StatusUnauthorized {
		return IngestResult{}, &UnauthorizedError{Reason: unauthorizedReason(resp, tooBig)}
	}
	if status != http.StatusOK {
		return IngestResult{}, mapStatus(status, hdr, resp, tooBig, key)
	}
	if tooBig {
		return IngestResult{}, errors.New("Antwort des Servers zu groß")
	}
	var r struct {
		Requests           int      `json:"requests"`
		LegacyDays         int      `json:"legacy_days"`
		Sessions           int      `json:"sessions"`
		Accounts           int      `json:"accounts"`
		FlaggedSessions    []string `json:"flagged_sessions"`
		KnownRequests      int      `json:"known_requests"`
		ElsewhereRequests  int      `json:"elsewhere_requests"`
		LegacyRejectedDays []string `json:"legacy_rejected_days"`
	}
	if json.Unmarshal(resp, &r) != nil {
		return IngestResult{}, errors.New("Antwort auf die Lieferung nicht lesbar")
	}
	// Nur echte Sitzungs-IDs übernehmen. IDPattern passt auch auf einen Geräteschlüssel,
	// daher werden Einträge mit dem Schlüssel (oder seinem Zufallsteil) zusätzlich verworfen.
	secretPart := strings.TrimPrefix(key, "tl_dk_")
	flagged := make([]string, 0, len(r.FlaggedSessions))
	for _, s := range r.FlaggedSessions {
		if !protocol.IDPattern.MatchString(s) || strings.Contains(s, secretPart) || protocol.DeviceKeyPattern.MatchString(s) {
			continue
		}
		flagged = append(flagged, s)
	}
	// Abgewiesene Tage nur mit gültigem Datum übernehmen: Das Plugin merkt sie sich und zeigt sie an.
	var days []string
	for _, d := range r.LegacyRejectedDays {
		if validDay(d) {
			days = append(days, d)
		}
	}
	return IngestResult{Requests: r.Requests, LegacyDays: r.LegacyDays, Sessions: r.Sessions, Accounts: r.Accounts,
		FlaggedSessions: flagged, KnownRequests: max(r.KnownRequests, 0), ElsewhereRequests: max(r.ElsewhereRequests, 0),
		LegacyRejectedDays: days}, nil
}

// validDay prüft ein Datum JJJJ-MM-TT.
func validDay(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil && len(s) == 10
}

// post sendet body als JSON und liest höchstens maxResponse Bytes der Antwort.
// Fehler nennen nur die Server-Adresse, nie Schlüssel oder Antwortinhalt.
func (c *Client) post(ctx context.Context, path, key string, body []byte) (status int, hdr http.Header, resp []byte, tooBig bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String()+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, false, errors.New("Anfrage ungültig")
	}
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	r, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, nil, false, &TransportError{Err: err, Pinned: c.pinned}
	}
	defer r.Body.Close()
	resp, tooBig, err = readCapped(r.Body)
	if err != nil {
		return 0, nil, nil, false, &TransportError{Err: err, Pinned: c.pinned}
	}
	return r.StatusCode, r.Header, resp, tooBig, nil
}

// readCapped liest höchstens maxResponse Bytes; tooBig meldet, dass mehr gekommen wäre.
func readCapped(r io.Reader) (b []byte, tooBig bool, err error) {
	b, err = io.ReadAll(io.LimitReader(r, maxResponse+1))
	if err != nil {
		return nil, false, err
	}
	if len(b) > maxResponse {
		return nil, true, nil
	}
	return b, false, nil
}

// mapStatus übersetzt eine Fehlerantwort. Texte vom Server werden bereinigt; key wird daraus entfernt,
// falls der Server ihn zurückspiegelt (vor dem Kürzen, damit kein Teilstück übrig bleibt).
func mapStatus(status int, hdr http.Header, body []byte, tooBig bool, key string) error {
	switch {
	case status == http.StatusUpgradeRequired:
		// 426 bedeutet in diesem Protokoll immer plugin_outdated; auch ohne lesbaren Rumpf.
		return &OutdatedError{Min: outdatedMin(body, tooBig)}
	case status == http.StatusServiceUnavailable && oldDatabase(body, tooBig):
		// Kein vorübergehender Zustand: nicht wiederholen, sondern die Meldung des Servers zeigen.
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return &OldDatabaseError{Msg: scrub(e.Error, key)}
	case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable:
		return &RetryError{Status: status, After: retryAfter(hdr)}
	case status >= 500:
		return &ServerError{Status: status}
	case status >= 400:
		var e struct {
			Error  string `json:"error"`
			Path   string `json:"path"`
			Reason string `json:"reason"`
		}
		if !tooBig {
			_ = json.Unmarshal(body, &e) // kein JSON: Status allein reicht
		}
		// Nur bekannte Gründe zählen; den Wortlaut von error wertet das Plugin nicht aus.
		if e.Reason != ReasonSignKeyUnknown {
			e.Reason = ""
		}
		return &RejectedError{Status: status, Path: scrub(e.Path, key), Msg: scrub(e.Error, key), Reason: e.Reason}
	case status >= 300 && status < 400:
		return fmt.Errorf("Server leitet weiter (Status %d), Weiterleitungen werden nicht verfolgt", status)
	default:
		return fmt.Errorf("unerwartete Antwort des Servers (Status %d)", status)
	}
}

func scrub(s, key string) string {
	s = strip(s)
	if key != "" {
		s = strings.ReplaceAll(s, key, "[Schlüssel]")
		// Auch der zufällige Teil allein ist geheim.
		s = strings.ReplaceAll(s, strings.TrimPrefix(key, "tl_dk_"), "[Schlüssel]")
	}
	return truncate(s)
}

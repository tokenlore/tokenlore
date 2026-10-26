// Package client spricht HTTPS mit dem tokenlore-Server und vertraut dabei der gepinnten Stamm-CA oder, ohne sie, den System-Zertifikaten.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
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

var (
	dialer      = &net.Dialer{Timeout: 10 * time.Second}
	saltPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

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
	DeviceID                              int64
	DeviceKey, ProjectSalt, CAFingerprint string
}

// Pair tauscht den einmaligen Kopplungscode gegen Geräteschlüssel, Projekt-Salz und CA-Fingerabdruck.
func (c *Client) Pair(ctx context.Context, code, name, goos, goarch string) (PairResult, error) {
	reqBody, err := json.Marshal(struct {
		Code       string `json:"code"`
		DeviceName string `json:"device_name"`
		OS         string `json:"os"`
		Arch       string `json:"arch"`
	}{code, name, goos, goarch})
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
	if status != http.StatusOK {
		return PairResult{}, mapStatus(status, hdr, body, tooBig, "")
	}
	if tooBig {
		return PairResult{}, errors.New("Antwort des Servers zu groß")
	}
	var r struct {
		DeviceID      int64  `json:"device_id"`
		DeviceKey     string `json:"device_key"`
		ProjectSalt   string `json:"project_salt"`
		CAFingerprint string `json:"ca_fingerprint"`
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
	if !saltPattern.MatchString(r.ProjectSalt) {
		return PairResult{}, errors.New("Antwort auf die Kopplung: Projekt-Salz ungültig")
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
	return PairResult{DeviceID: r.DeviceID, DeviceKey: r.DeviceKey, ProjectSalt: r.ProjectSalt, CAFingerprint: fp}, nil
}

// IngestResult ist die Antwort auf eine Lieferung.
type IngestResult struct {
	Requests, Sessions, Accounts int
	FlaggedSessions              []string
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
		return IngestResult{}, ErrUnauthorized
	}
	if status != http.StatusOK {
		return IngestResult{}, mapStatus(status, hdr, resp, tooBig, key)
	}
	if tooBig {
		return IngestResult{}, errors.New("Antwort des Servers zu groß")
	}
	var r struct {
		Requests        int      `json:"requests"`
		Sessions        int      `json:"sessions"`
		Accounts        int      `json:"accounts"`
		FlaggedSessions []string `json:"flagged_sessions"`
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
	return IngestResult{Requests: r.Requests, Sessions: r.Sessions, Accounts: r.Accounts, FlaggedSessions: flagged}, nil
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
	case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable:
		return &RetryError{Status: status, After: retryAfter(hdr)}
	case status >= 500:
		return &ServerError{Status: status}
	case status >= 400:
		var e struct {
			Error string `json:"error"`
			Path  string `json:"path"`
		}
		if !tooBig {
			_ = json.Unmarshal(body, &e) // kein JSON: Status allein reicht
		}
		return &RejectedError{Status: status, Path: scrub(e.Path, key), Msg: scrub(e.Error, key)}
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

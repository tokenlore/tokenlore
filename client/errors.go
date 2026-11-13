package client

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
)

var (
	// ErrUnauthorized: Der Server kennt den Geräteschlüssel nicht (mehr), z. B. nach Widerruf.
	ErrUnauthorized = errors.New("Server lehnt den Geräteschlüssel ab")
	// ErrBadCode: Der Kopplungscode ist ungültig, abgelaufen oder schon benutzt.
	ErrBadCode = errors.New("Kopplungscode ungültig")
	// ErrSourceTaken: Der Code gilt für ein bestehendes Gerät, die Kennung dieses Rechners gehört aber schon einem
	// anderen Gerät desselben Benutzers (409 source_taken).
	ErrSourceTaken = errors.New("Kennung gehört einem anderen Gerät")
)

// Gründe im 401 beim Liefern (protocol/doc.md, „Liefern“).
const (
	ReasonRepaired = "repaired" // Gerät wurde anderswo neu gekoppelt, dieser Schlüssel ist gesperrt
	ReasonRevoked  = "revoked"  // Gerät oder Schlüssel in der Geräte-Ansicht gesperrt, oder Benutzer gesperrt
	ReasonUnknown  = "unknown"  // Schlüssel unbekannt
)

// UnauthorizedError: Der Server lehnt den Geräteschlüssel beim Liefern ab (401). Reason ist einer der Gründe oben,
// leer wenn der Server keinen (oder einen unbekannten) nennt. errors.Is(err, ErrUnauthorized) gilt.
type UnauthorizedError struct {
	Reason string
}

func (e *UnauthorizedError) Error() string { return ErrUnauthorized.Error() }

func (e *UnauthorizedError) Is(target error) bool { return target == ErrUnauthorized }

// unauthorizedReason liest den Grund aus dem Rumpf eines 401; nur die bekannten Werte zählen.
func unauthorizedReason(body []byte, tooBig bool) string {
	if tooBig {
		return ""
	}
	var e struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &e) // kein JSON: kein Grund
	switch e.Reason {
	case ReasonRepaired, ReasonRevoked, ReasonUnknown:
		return e.Reason
	}
	return ""
}

const (
	defaultRetryAfter = 5 * time.Second
	maxRetryAfter     = 60 * time.Second
)

// RetryError: Der Server ist ausgelastet oder kurz nicht bereit (429, 503 außer OldDatabaseError); später erneut versuchen.
type RetryError struct {
	Status int
	After  time.Duration
}

func (e *RetryError) Error() string {
	return fmt.Sprintf("Server vorübergehend nicht bereit (Status %d), neuer Versuch in %s", e.Status, e.After)
}

// ReasonSignKeyUnknown kennzeichnet ein 400 beim Liefern, weil eine Signatur zu keinem Signaturschlüssel des Geräts
// passt; meist hat ein anderes Gerät den Schlüssel beim Koppeln übernommen (protocol/doc.md, „Liefern“).
const ReasonSignKeyUnknown = "sign_key_unknown"

// RejectedError: Der Server lehnt die Anfrage ab (4xx). Path und Msg sind bereits mit Clean bereinigt. Reason ist ein
// bekannter Grund aus dem Rumpf (ReasonSignKeyUnknown), sonst leer.
type RejectedError struct {
	Status    int
	Path, Msg string
	Reason    string
}

func (e *RejectedError) Error() string {
	s := fmt.Sprintf("Server lehnt ab (Status %d)", e.Status)
	if e.Msg != "" {
		s += ": " + e.Msg
	}
	if e.Path != "" {
		s += " (" + e.Path + ")"
	}
	return s
}

// ReasonOldDatabase kennzeichnet ein 503, weil die Datenbank des Servers von einer älteren Version stammt
// (protocol/doc.md, „Alte Datenbank“).
const ReasonOldDatabase = "old_database"

// OldDatabaseText gilt, wenn der Server bei ReasonOldDatabase keine Meldung mitschickt.
const OldDatabaseText = "Die Datenbank des Servers stammt von einer älteren Version; der Betreiber muss tokenlore dort neu einrichten."

// OldDatabaseError: Der Server antwortet 503 mit reason "old_database". Das ist kein ausgelasteter Server; ein neuer
// Versuch hilft erst, wenn der Betreiber den Server neu eingerichtet hat. Msg ist die Meldung des Servers, bereinigt.
type OldDatabaseError struct {
	Msg string
}

func (e *OldDatabaseError) Error() string {
	if e.Msg == "" {
		return OldDatabaseText
	}
	return e.Msg
}

// oldDatabase meldet, ob der Rumpf eines 503 reason "old_database" nennt.
func oldDatabase(body []byte, tooBig bool) bool {
	if tooBig {
		return false
	}
	var e struct {
		Reason string `json:"reason"`
	}
	return json.Unmarshal(body, &e) == nil && e.Reason == ReasonOldDatabase
}

// ServerError: Fehler im Server (5xx außer 503).
type ServerError struct {
	Status int
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("Serverfehler (Status %d)", e.Status)
}

// retryAfter liest Retry-After in Sekunden. Fehlt der Wert oder ist er ungültig (auch das
// HTTP-Datumsformat), gilt 5 s; mehr als 60 s werden gekappt, damit ein Server tokenlore nicht beliebig lange stilllegt.
func retryAfter(h http.Header) time.Duration {
	n, err := strconv.Atoi(h.Get("Retry-After"))
	if err != nil || n < 1 {
		return defaultRetryAfter
	}
	if n >= int(maxRetryAfter/time.Second) {
		return maxRetryAfter
	}
	return time.Duration(n) * time.Second
}

// TransportError: Verbindung, TLS oder Lesen ist gescheitert. Der innere Text wird bereinigt und gekürzt:
// Go nennt z. B. die SANs des Gegenzertifikats ungequotet, und die bestimmt bei einem MITM der Angreifer.
// Pinned sagt, ob der Client nur einer gepinnten CA vertraute (sonst den System-Zertifikaten); davon hängt der Text
// bei einem Zertifikatsfehler ab.
type TransportError struct {
	Err    error
	Pinned bool
}

func (e *TransportError) Error() string {
	if IsCertError(e.Err) {
		return certMessage(e.Pinned)
	}
	return "Server nicht erreichbar: " + Clean(e.Err.Error())
}

func (e *TransportError) Unwrap() error { return e.Err }

// MsgCertMismatch ersetzt jeden TLS-Prüffehler. Go nennt darin Namen und Felder des Gegenzertifikats, und die
// bestimmt bei einem Angreifer im Netz er selbst; über /tokenlore:sync landete der Text sonst in Claudes Kontext.
// Das gilt für den Client mit gepinnter CA; MsgCertUntrusted für den mit System-Zertifikaten (Kopplung ohne --ca).
const MsgCertMismatch = "Zertifikat des Servers passt nicht zur gepinnten CA oder zum Namen"

// MsgCertUntrusted: Ohne gepinnte CA gibt es keine "gepinnte CA", die nicht passen könnte. Wer einen Server mit
// eigenem Zertifikat betreibt, koppelt mit --ca.
const MsgCertUntrusted = "Zertifikat des Servers ist nicht vertrauenswürdig oder passt nicht zum Namen. Bei eigenem Zertifikat mit --ca <fingerabdruck> koppeln"

func certMessage(pinned bool) string {
	if pinned {
		return MsgCertMismatch
	}
	return MsgCertUntrusted
}

// CertMessage liefert den festen Text zu einem TLS-Prüffehler, passend zur Vertrauensart des Clients, der ihn
// meldete. Ohne TransportError in der Kette (unbekannt, ob gepinnt) gilt die neutrale Meldung.
func CertMessage(err error) string {
	var te *TransportError
	return certMessage(errors.As(err, &te) && te.Pinned)
}

// IsCertError erkennt TLS-Prüffehler am Typ, nie am Text.
func IsCertError(err error) bool {
	var (
		cve *tls.CertificateVerificationError
		he  x509.HostnameError
		ua  x509.UnknownAuthorityError
		ci  x509.CertificateInvalidError
	)
	return errors.As(err, &cve) || errors.As(err, &he) || errors.As(err, &ua) || errors.As(err, &ci)
}

// OutdatedError: Der Server nimmt diese Plugin-Version nicht mehr an (426 plugin_outdated). Min ist die Mindestversion
// x.y.z, leer, wenn der Server keine gültige nennt.
type OutdatedError struct {
	Min string
}

func (e *OutdatedError) Error() string {
	if e.Min == "" {
		return "Plugin-Version zu alt für diesen Server"
	}
	return "Plugin-Version zu alt für diesen Server, nötig ist mindestens " + e.Min
}

// outdatedMin liest die Mindestversion aus dem Rumpf eines 426; nur x.y.z zählt.
func outdatedMin(body []byte, tooBig bool) string {
	if tooBig {
		return ""
	}
	var e struct {
		Min string `json:"min_plugin_version"`
	}
	_ = json.Unmarshal(body, &e) // kein JSON: keine Version
	if protocol.MinVersionPattern.MatchString(e.Min) {
		return e.Min
	}
	return ""
}

// Antworten auf die Frage nach dem Vorgänger (protocol/doc.md, „Vorgänger“).
const (
	AnswerSame  = "same"  // dieser Rechner ist das alte Gerät A
	AnswerNew   = "new"   // anderer Rechner, eigenes Gerät, nie wieder fragen
	AnswerLater = "later" // eigenes Gerät, die Webseite fragt weiter
)

// PredecessorDevice sind die Angaben des Servers zum Gerät A, von dem dieser Konfigurationsordner stammt. Name, OS und
// Arch sind bereinigt; LastSeenAt ist nil, wenn A nie geliefert hat oder der Server keinen gültigen Zeitpunkt nennt.
type PredecessorDevice struct {
	DeviceID     int64
	Name         string
	LastSeenAt   *time.Time
	OS, Arch     string
	Recent       bool // A hat in den letzten 24 Stunden geliefert und scheint in Gebrauch
	SignKeyMatch bool // ein gesendeter Signaturschlüssel gehört A
}

// PredecessorQuestionError: Der Server fragt vor dem Koppeln, ob dieser Rechner das Gerät A ist (409
// predecessor_question). Nichts ist geändert, der Code bleibt gültig. Recommended ist AnswerSame oder AnswerNew.
type PredecessorQuestionError struct {
	Predecessor PredecessorDevice
	Recommended string
}

func (e *PredecessorQuestionError) Error() string {
	return "Der Server fragt, ob dieser Rechner das Gerät „" + e.Predecessor.Name + "“ ist"
}

// predecessorQuestion liest den Rumpf eines 409. ok ist false, wenn er keine Frage nach dem Vorgänger ist oder die
// Angaben zu A unbrauchbar sind (keine Nummer, kein Name).
func predecessorQuestion(body []byte, tooBig bool) (q *PredecessorQuestionError, isQuestion bool) {
	if tooBig {
		return nil, false
	}
	var v struct {
		Error       string `json:"error"`
		Predecessor *struct {
			DeviceID     int64  `json:"device_id"`
			Name         string `json:"name"`
			LastSeenAt   string `json:"last_seen_at"`
			OS           string `json:"os"`
			Arch         string `json:"arch"`
			Recent       bool   `json:"recent"`
			SignKeyMatch bool   `json:"sign_key_match"`
		} `json:"predecessor"`
		Recommended string `json:"recommended"`
	}
	if json.Unmarshal(body, &v) != nil || v.Error != "predecessor_question" {
		return nil, false
	}
	p := v.Predecessor
	if p == nil || p.DeviceID <= 0 || Clean(p.Name) == "" {
		return nil, true
	}
	d := PredecessorDevice{DeviceID: p.DeviceID, Name: Clean(p.Name), OS: Clean(p.OS), Arch: Clean(p.Arch),
		Recent: p.Recent, SignKeyMatch: p.SignKeyMatch}
	if protocol.TSPattern.MatchString(p.LastSeenAt) {
		if t, err := time.Parse(time.RFC3339Nano, p.LastSeenAt); err == nil {
			d.LastSeenAt = &t
		}
	}
	// Die Empfehlung folgt laut Protokoll aus recent; nennt der Server keine gültige, gilt diese Regel.
	rec := v.Recommended
	if rec != AnswerSame && rec != AnswerNew {
		rec = AnswerSame
		if p.Recent {
			rec = AnswerNew
		}
	}
	return &PredecessorQuestionError{Predecessor: d, Recommended: rec}, true
}

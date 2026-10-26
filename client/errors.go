package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

var (
	// ErrUnauthorized: Der Server kennt den Geräteschlüssel nicht (mehr), z. B. nach Widerruf.
	ErrUnauthorized = errors.New("Server lehnt den Geräteschlüssel ab")
	// ErrBadCode: Der Kopplungscode ist ungültig, abgelaufen oder schon benutzt.
	ErrBadCode = errors.New("Kopplungscode ungültig")
)

const (
	defaultRetryAfter = 5 * time.Second
	maxRetryAfter     = 60 * time.Second
)

// RetryError: Der Server ist ausgelastet oder kurz nicht bereit (429/503); später erneut versuchen.
type RetryError struct {
	Status int
	After  time.Duration
}

func (e *RetryError) Error() string {
	return fmt.Sprintf("Server vorübergehend nicht bereit (Status %d), neuer Versuch in %s", e.Status, e.After)
}

// RejectedError: Der Server lehnt die Anfrage ab (4xx). Path und Msg sind bereits mit Clean bereinigt.
type RejectedError struct {
	Status    int
	Path, Msg string
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

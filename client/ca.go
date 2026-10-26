package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var errFingerprint = errors.New("CA-Fingerabdruck fehlt oder ist ungültig")

// ParseServerURL prüft die Server-Adresse: nur https, mit Host, ohne Benutzerteil, Query und Fragment.
// Ein Pfad ist erlaubt (Server unter /tokenlore oder Webspace ohne Umschreibregeln unter .../index.php).
// Die Pfadsegmente "." und ".." sind nicht erlaubt.
// Das Ergebnis endet nicht auf "/", damit Aufrufpfade (/api/plugin/...) einfach angehängt werden können.
func ParseServerURL(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, errors.New("Server-Adresse ungültig")
	}
	switch {
	case u.Scheme != "https":
		return nil, errors.New("Server-Adresse muss mit https:// beginnen")
	case u.Opaque != "" || u.Hostname() == "":
		return nil, errors.New("Server-Adresse ohne Host")
	case u.User != nil:
		return nil, errors.New("Server-Adresse darf keinen Benutzerteil enthalten")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "":
		return nil, errors.New("Server-Adresse darf keine Query und kein Fragment enthalten")
	case u.RawPath != "":
		// RawPath ist nur gesetzt, wenn die Schreibweise von der Standardkodierung abweicht (z. B. %2F).
		return nil, errors.New("Server-Adresse: Pfad ungültig")
	}
	// "." und ".." würden beim Anhängen der Aufrufpfade den Server-Pfad verlassen oder umdeuten.
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return nil, errors.New("Server-Adresse: Pfad ungültig")
		}
	}
	return &url.URL{Scheme: "https", Host: u.Host, Path: strings.TrimRight(u.Path, "/")}, nil
}

// Fingerprint liefert SHA-256 über die DER als Großbuchstaben-Hex mit ":" (wie CAFingerprint im Server).
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return formatFingerprint(sum[:])
}

func formatFingerprint(b []byte) string {
	h := strings.ToUpper(hex.EncodeToString(b))
	parts := make([]string, 0, len(b))
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// NormalizeFingerprint akzeptiert Groß-/Kleinschreibung mit oder ohne ":" und liefert das Format von Fingerprint.
// Ein leerer Fingerabdruck ist ein Fehler: Ohne ihn gäbe es nichts, wogegen die geholte CA geprüft wird.
func NormalizeFingerprint(s string) (string, error) {
	h := strings.ReplaceAll(s, ":", "")
	if len(h) != 2*sha256.Size {
		return "", errFingerprint
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return "", errFingerprint
	}
	return formatFingerprint(b), nil
}

// FetchCA holt das öffentliche CA-Zertifikat des Servers. Hier, und nur hier, wird das
// Serverzertifikat nicht geprüft: Die CA ist ja erst noch zu holen. Vertraut wird ihr erst,
// wenn der Aufrufer ihren Fingerabdruck mit dem vom Nutzer angegebenen verglichen hat.
func FetchCA(ctx context.Context, base *url.URL) (pemBytes []byte, cert *x509.Certificate, err error) {
	if base == nil {
		return nil, nil, errors.New("Server-Adresse fehlt")
	}
	tr := &http.Transport{
		Proxy:               nil,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: handshakeTimeout,
		DialContext:         dialer.DialContext,
		DisableKeepAlives:   true,
	}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: 30 * time.Second, CheckRedirect: noRedirect}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"/api/plugin/ca", nil)
	if err != nil {
		return nil, nil, errors.New("Anfrage ungültig")
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &TransportError{Err: err}
	}
	defer resp.Body.Close()
	body, tooBig, err := readCapped(resp.Body)
	if err != nil {
		return nil, nil, &TransportError{Err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("CA-Zertifikat nicht verfügbar (Status %d)", resp.StatusCode)
	}
	if tooBig {
		return nil, nil, errors.New("Antwort des Servers zu groß")
	}
	cert, err = firstCACert(body)
	if err != nil {
		return nil, nil, err
	}
	return encodeCert(cert), cert, nil
}

// firstCACert nimmt den ersten CERTIFICATE-Block; andere Blöcke (z. B. Schlüssel) werden übersprungen.
func firstCACert(data []byte) (*x509.Certificate, error) {
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return nil, errors.New("kein CA-Zertifikat in der Antwort")
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("CA-Zertifikat nicht lesbar")
		}
		if !isValidRoot(cert, time.Now()) {
			return nil, errNotRoot
		}
		return cert, nil
	}
}

var errNotRoot = errors.New("Zertifikat ist keine gültige Stamm-CA")

// isValidRoot verlangt eine selbstsignierte, jetzt gültige CA. Gepinnt wird nur die Stamm-CA:
// Eine gepinnte Zwischen-CA bräche, sobald Caddy sie erneuert.
func isValidRoot(cert *x509.Certificate, now time.Time) bool {
	return cert.BasicConstraintsValid && cert.IsCA &&
		bytes.Equal(cert.RawSubject, cert.RawIssuer) &&
		cert.CheckSignatureFrom(cert) == nil &&
		!now.Before(cert.NotBefore) && !now.After(cert.NotAfter)
}

func encodeCert(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

// ErrStoredCA: Die gespeicherte CA passt nicht zum gespeicherten Fingerabdruck (fehlt, verändert oder unbrauchbar).
// Der Aufrufer bricht ab, statt auf weniger Vertrauen auszuweichen.
var ErrStoredCA = errors.New("Gespeicherte CA passt nicht zum gespeicherten Fingerabdruck – Gerät bitte neu koppeln mit /tokenlore:connect")

// CheckStoredCA prüft, dass das erste Stammzertifikat in caPEM genau den Fingerabdruck fp hat (beide normalisiert).
// Es gibt nie still auf System-Zertifikate zurück: Wer gepinnt gekoppelt hat, bleibt gepinnt.
func CheckStoredCA(caPEM []byte, fp string) error {
	want, err := NormalizeFingerprint(fp)
	if err != nil {
		return ErrStoredCA
	}
	cert, err := firstCACert(caPEM)
	if err != nil {
		return fmt.Errorf("gespeicherte CA unbrauchbar: %w", err)
	}
	if Fingerprint(cert.Raw) != want {
		return ErrStoredCA
	}
	return nil
}

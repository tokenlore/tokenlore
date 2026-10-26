// Package testpki erzeugt kleine Zertifikatsketten (Stamm-CA, Zwischen-CA, Blatt) für Tests.
// Nur in Tests verwenden: Schlüssel liegen im Speicher und werden nie geschützt.
package testpki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// CA ist eine Stamm- oder Zwischen-CA samt Schlüssel.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	// chain enthält die DER dieser CA und ihrer Eltern bis ausschließlich zur Stamm-CA.
	chain [][]byte
}

// NewRoot erzeugt eine selbstsignierte Stamm-CA.
func NewRoot(t testing.TB) *CA {
	t.Helper()
	key := newKey(t)
	tmpl := caTemplate("tokenlore Test-Stamm-CA")
	cert := sign(t, tmpl, tmpl, &key.PublicKey, key)
	return &CA{Cert: cert, Key: key}
}

// NewRootValid erzeugt eine selbstsignierte Stamm-CA mit eigener Gültigkeit (z. B. abgelaufen).
func NewRootValid(t testing.TB, notBefore, notAfter time.Time) *CA {
	t.Helper()
	key := newKey(t)
	tmpl := caTemplate("tokenlore Test-Stamm-CA")
	tmpl.NotBefore, tmpl.NotAfter = notBefore, notAfter
	cert := sign(t, tmpl, tmpl, &key.PublicKey, key)
	return &CA{Cert: cert, Key: key}
}

// Intermediate erzeugt eine von ca signierte Zwischen-CA.
func (ca *CA) Intermediate(t testing.TB) *CA {
	t.Helper()
	key := newKey(t)
	cert := sign(t, caTemplate("tokenlore Test-Zwischen-CA"), ca.Cert, &key.PublicKey, ca.Key)
	chain := append([][]byte{cert.Raw}, ca.chain...)
	return &CA{Cert: cert, Key: key, chain: chain}
}

// Leaf erzeugt ein Serverzertifikat für hosts (IP-Adressen als IP-SAN), mit Kette bis ausschließlich zur Stamm-CA.
func (ca *CA) Leaf(t testing.TB, hosts ...string) tls.Certificate {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: serial(t),
		Subject:      pkix.Name{CommonName: "tokenlore Test-Blatt"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(12 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	cert := sign(t, tmpl, ca.Cert, &key.PublicKey, ca.Key)
	return tls.Certificate{
		Certificate: append([][]byte{cert.Raw}, ca.chain...),
		PrivateKey:  key,
		Leaf:        cert,
	}
}

// PEM liefert das Zertifikat der CA als PEM.
func (ca *CA) PEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw})
}

// DER liefert das Zertifikat der CA als DER.
func (ca *CA) DER() []byte {
	return ca.Cert.Raw
}

func caTemplate(cn string) *x509.Certificate {
	return &x509.Certificate{
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
}

func newKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func serial(t testing.TB) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func sign(t testing.TB, tmpl, parent *x509.Certificate, pub *ecdsa.PublicKey, signer *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	tmpl.SerialNumber = serial(t)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

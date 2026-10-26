package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/internal/testpki"
)

func TestParseServerURL(t *testing.T) {
	ok := map[string]string{
		"https://tokenlore.lan":       "https://tokenlore.lan",
		"https://tokenlore.lan:8443/": "https://tokenlore.lan:8443",
	}
	for in, want := range ok {
		u, err := ParseServerURL(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if u.String() != want {
			t.Errorf("%q: %q, erwartet %q", in, u.String(), want)
		}
	}
	for _, in := range []string{
		"http://tokenlore.lan", "https://user@x", "https://x?a=1",
		"https://x#frag", "https://x?", "https://:443", "tokenlore.lan", "",
	} {
		if _, err := ParseServerURL(in); err == nil {
			t.Errorf("%q: Fehler erwartet", in)
		}
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	want := Fingerprint([]byte("x"))
	if len(want) != 95 || strings.ToUpper(want) != want {
		t.Fatalf("Format: %q", want)
	}
	for _, in := range []string{want, strings.ToLower(want), hex.EncodeToString(sum[:]), strings.ToUpper(hex.EncodeToString(sum[:]))} {
		got, err := NormalizeFingerprint(in)
		if err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", hex.EncodeToString(sum[:31]), strings.Repeat("zz", 32), want + ":AB", " " + want} {
		_, err := NormalizeFingerprint(in)
		if err == nil || err.Error() != "CA-Fingerabdruck fehlt oder ist ungültig" {
			t.Errorf("%q: %v", in, err)
		}
	}
}

// caServer liefert body unter /api/plugin/ca mit einem beliebigen (ungeprüften) Zertifikat.
func caServer(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugin/ca" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.WriteHeader(status)
		w.Write(body)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := ParseServerURL(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestFetchCAReturnsOnlyCert(t *testing.T) {
	root := testpki.NewRoot(t)
	keyBlock := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("geheim")})
	body := append(append([]byte{}, keyBlock...), root.PEM()...)
	srv := caServer(t, 200, body)

	pemBytes, cert, err := FetchCA(context.Background(), mustURL(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pemBytes, root.PEM()) {
		t.Errorf("PEM:\n%s", pemBytes)
	}
	if bytes.Contains(pemBytes, []byte("PRIVATE")) {
		t.Error("Schlüsselblock in der Rückgabe")
	}
	sum := sha256.Sum256(root.DER())
	fp, _ := NormalizeFingerprint(hex.EncodeToString(sum[:]))
	if Fingerprint(cert.Raw) != fp {
		t.Errorf("Fingerabdruck %s, erwartet %s", Fingerprint(cert.Raw), fp)
	}
}

func TestFetchCARejectsLeaf(t *testing.T) {
	root := testpki.NewRoot(t)
	leaf := root.Leaf(t, "127.0.0.1")
	body := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]})
	srv := caServer(t, 200, body)
	if _, _, err := FetchCA(context.Background(), mustURL(t, srv.URL)); err == nil {
		t.Fatal("Blatt als CA angenommen")
	}
}

func TestFetchCARejectsHuge(t *testing.T) {
	root := testpki.NewRoot(t)
	body := append(root.PEM(), bytes.Repeat([]byte("x"), 70*1024)...)
	srv := caServer(t, 200, body)
	if _, _, err := FetchCA(context.Background(), mustURL(t, srv.URL)); err == nil {
		t.Fatal("70 KiB angenommen")
	}
}

func TestFetchCARejectsErrorStatus(t *testing.T) {
	srv := caServer(t, 503, []byte(`{"error":"CA nicht verfügbar"}`))
	if _, _, err := FetchCA(context.Background(), mustURL(t, srv.URL)); err == nil {
		t.Fatal("503 angenommen")
	}
}

func TestFetchCARejectsNoCert(t *testing.T) {
	srv := caServer(t, 200, []byte("kein PEM"))
	if _, _, err := FetchCA(context.Background(), mustURL(t, srv.URL)); err == nil {
		t.Fatal("Antwort ohne Zertifikat angenommen")
	}
}

func TestParseServerURLWithPath(t *testing.T) {
	ok := map[string]string{
		"https://h/sub/":                "https://h/sub",
		"https://h/sub":                 "https://h/sub",
		"https://h/sub/index.php":       "https://h/sub/index.php",
		"https://h:8443/a/b/index.php/": "https://h:8443/a/b/index.php",
		"https://h//":                   "https://h",
	}
	for in, want := range ok {
		u, err := ParseServerURL(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if u.String() != want {
			t.Errorf("%q: %q, erwartet %q", in, u.String(), want)
		}
	}
	for _, in := range []string{"https://h/sub%2Fx", "https://h/sub?a=1", "https://h/sub#f", "https://u@h/sub", "http://h/sub"} {
		if _, err := ParseServerURL(in); err == nil {
			t.Errorf("%q: Fehler erwartet", in)
		}
	}
}

func TestParseServerURLRejectsQuery(t *testing.T) {
	for _, in := range []string{"https://h/sub?a=1", "https://h/sub?", "https://h?x", "https://h/sub/index.php?x=1"} {
		_, err := ParseServerURL(in)
		if err == nil || err.Error() != "Server-Adresse darf keine Query und kein Fragment enthalten" {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestCheckStoredCA(t *testing.T) {
	root := testpki.NewRoot(t)
	fp := Fingerprint(root.DER())
	if err := CheckStoredCA(root.PEM(), strings.ToLower(strings.ReplaceAll(fp, ":", ""))); err != nil {
		t.Errorf("passende CA: %v", err)
	}
	other := Fingerprint(testpki.NewRoot(t).DER())
	for name, c := range map[string]struct {
		pem []byte
		fp  string
	}{"anderer Fingerabdruck": {root.PEM(), other}, "leere CA": {nil, fp}, "leerer Fingerabdruck": {root.PEM(), ""}} {
		if err := CheckStoredCA(c.pem, c.fp); err == nil {
			t.Errorf("%s: Fehler erwartet", name)
		}
	}
}

func TestParseServerURLRejectsDotSegments(t *testing.T) {
	for _, in := range []string{
		"https://h/.", "https://h/./", "https://h/..", "https://h/../", "https://h/sub/..", "https://h/sub/../x",
		"https://h/./sub", "https://h/sub/./x", "https://h/a/b/../../index.php", "https://h/sub/index.php/..",
	} {
		_, err := ParseServerURL(in)
		if err == nil || err.Error() != "Server-Adresse: Pfad ungültig" {
			t.Errorf("%q: %v", in, err)
		}
	}
	// Punkte in Namen sind weiterhin erlaubt.
	for _, in := range []string{"https://h/.well-known", "https://h/a..b", "https://h/sub/index.php", "https://h/v1.2/x"} {
		if _, err := ParseServerURL(in); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestFetchCASendsUserAgent(t *testing.T) {
	old := Version
	Version = "9.9.9"
	t.Cleanup(func() { Version = old })
	root := testpki.NewRoot(t)
	var gotUA string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Write(root.PEM())
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	if _, _, err := FetchCA(context.Background(), mustURL(t, srv.URL)); err != nil {
		t.Fatal(err)
	}
	if gotUA != "tokenlore/9.9.9" {
		t.Errorf("User-Agent %q", gotUA)
	}
}

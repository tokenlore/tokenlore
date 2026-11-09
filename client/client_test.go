package client

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tokenlore/tokenlore/internal/testpki"
	"github.com/tokenlore/tokenlore/protocol"
)

const testKey = "tl_dk_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// testSource ist eine erfundene device_source.
const testSource = "5555555555555555555555555555555555555555555555555555555555555555"

// Erfundene öffentliche Signaturschlüssel (je 32 Byte Base64).
const (
	testSignPub  = "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="
	testSignPrev = "O2onvM62pC1io6jQKm8Nc2UyFXcd4kOmOsBIoYtZ2ik="
)

// testSignPriv ist der private Teil zu testSignPub (Testschlüssel aus protocol/doc.md, Seed 00 01 … 1f).
var testSignPriv = ed25519.NewKeyFromSeed([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31})

var testSign = SignKeys{Public: testSignPub, Priv: testSignPriv}

// tlsServer startet einen HTTPS-Server mit der Kette cert (Blatt + Zwischen-CA).
func tlsServer(t *testing.T, ca *testpki.CA, hosts []string, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{ca.Leaf(t, hosts...)}}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func newClient(t *testing.T, srvURL string, root *testpki.CA, timeout time.Duration) *Client {
	t.Helper()
	c, err := New(mustURL(t, srvURL), root.PEM(), timeout)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func okIngest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"requests":3,"sessions":2,"accounts":1,"flagged_sessions":["s1"]}`)
}

func TestPinnedClientAcceptsChainToPinnedRoot(t *testing.T) {
	root := testpki.NewRoot(t)
	var gotAuth, gotType string
	srv := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugin/ingest" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		gotAuth, gotType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		okIngest(w, r)
	})
	c := newClient(t, srv.URL, root, 5*time.Second)
	res, err := c.Ingest(context.Background(), testKey, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Requests != 3 || res.Sessions != 2 || res.Accounts != 1 || len(res.FlaggedSessions) != 1 || res.FlaggedSessions[0] != "s1" {
		t.Errorf("Ergebnis %+v", res)
	}
	if gotAuth != "Bearer "+testKey || gotType != "application/json" {
		t.Errorf("Kopfzeilen: %q %q", gotAuth, gotType)
	}
}

func TestPinnedClientRejectsOtherRoot(t *testing.T) {
	root, other := testpki.NewRoot(t), testpki.NewRoot(t)
	var hit atomic.Bool
	srv := tlsServer(t, other.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		okIngest(w, r)
	})
	c := newClient(t, srv.URL, root, 5*time.Second)
	if _, err := c.Ingest(context.Background(), testKey, []byte(`{}`)); err == nil {
		t.Fatal("fremde Stamm-CA angenommen")
	}
	if hit.Load() {
		t.Fatal("Handler erreicht")
	}
}

func TestPinnedClientRejectsWrongHost(t *testing.T) {
	root := testpki.NewRoot(t)
	var hit atomic.Bool
	srv := tlsServer(t, root.Intermediate(t), []string{"example.invalid"}, func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		okIngest(w, r)
	})
	c := newClient(t, srv.URL, root, 5*time.Second)
	if _, err := c.Ingest(context.Background(), testKey, []byte(`{}`)); err == nil {
		t.Fatal("falscher Hostname angenommen")
	}
	if hit.Load() {
		t.Fatal("Handler erreicht")
	}
}

func TestPinnedClientAcceptsRenewedLeaf(t *testing.T) {
	root := testpki.NewRoot(t)
	srv1 := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, okIngest)
	srv2 := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, okIngest)
	for _, u := range []string{srv1.URL, srv2.URL} {
		c := newClient(t, u, root, 5*time.Second)
		if _, err := c.Ingest(context.Background(), testKey, []byte(`{}`)); err != nil {
			t.Fatalf("%s: %v", u, err)
		}
	}
}

func TestClientDoesNotFollowRedirect(t *testing.T) {
	root := testpki.NewRoot(t)
	var hit atomic.Bool
	srv2 := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		okIngest(w, r)
	})
	srv1 := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv2.URL+"/api/plugin/ingest", http.StatusFound)
	})
	c := newClient(t, srv1.URL, root, 5*time.Second)
	_, err := c.Ingest(context.Background(), testKey, []byte(`{}`))
	if err == nil {
		t.Fatal("Weiterleitung angenommen")
	}
	if hit.Load() {
		t.Fatal("zweiter Server hat eine Anfrage bekommen")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatal("Schlüssel im Fehler")
	}
	if _, err := c.Pair(context.Background(), "code", "n", "linux", "amd64", testSource, testServerID, false, testSign); err == nil || hit.Load() {
		t.Fatalf("Pair folgt Weiterleitung: %v", err)
	}
}

func TestClientIgnoresProxyEnv(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var conns atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			conn.Close()
		}
	}()
	proxy := "http://" + ln.Addr().String()
	t.Setenv("HTTPS_PROXY", proxy)
	t.Setenv("https_proxy", proxy)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	root := testpki.NewRoot(t)
	// Kein Loopback-Ziel: Für 127.0.0.1 nutzt Go den Proxy ohnehin nie. Der Name löst nicht auf,
	// ohne Proxy schlägt die Anfrage also fehl, ohne den lauschenden Port zu berühren.
	c := newClient(t, "https://tokenlore.invalid", root, 3*time.Second)
	if c.transport.Proxy != nil {
		t.Fatal("Transport.Proxy gesetzt")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.Ingest(ctx, testKey, []byte(`{}`)); err == nil {
		t.Fatal("Anfrage an nicht auflösbaren Namen gelungen")
	}
	if n := conns.Load(); n != 0 {
		t.Fatalf("Proxy bekam %d Verbindungen", n)
	}
}

func statusServer(t *testing.T, root *testpki.CA, status int, hdr map[string]string, body string) *httptest.Server {
	return tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

func TestIngestStatusMapping(t *testing.T) {
	root := testpki.NewRoot(t)
	ingest := func(status int, hdr map[string]string, body string) (IngestResult, error) {
		srv := statusServer(t, root, status, hdr, body)
		return newClient(t, srv.URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
	}

	res, err := ingest(200, nil, `{"requests":5,"sessions":4,"accounts":2,"flagged_sessions":[]}`)
	if err != nil || res.Requests != 5 || res.Sessions != 4 || res.Accounts != 2 {
		t.Errorf("200: %+v %v", res, err)
	}
	if _, err := ingest(401, nil, `{"error":"Schlüssel ungültig"}`); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("401: %v", err)
	}
	retry := func(status int, hdr map[string]string, want time.Duration) {
		_, err := ingest(status, hdr, `{"error":"später"}`)
		var re *RetryError
		if !errors.As(err, &re) || re.Status != status || re.After != want {
			t.Errorf("%d %v: %#v", status, hdr, err)
		}
	}
	retry(429, map[string]string{"Retry-After": "7"}, 7*time.Second)
	retry(503, nil, 5*time.Second)
	retry(429, map[string]string{"Retry-After": "99999"}, 60*time.Second)
	retry(503, map[string]string{"Retry-After": "Wed, 21 Oct 2015 07:28:00 GMT"}, 5*time.Second)
	retry(429, map[string]string{"Retry-After": "-3"}, 5*time.Second)

	_, err = ingest(400, nil, `{"error":"liegt in der Zukunft","path":"requests[3].ts"}`)
	var rej *RejectedError
	if !errors.As(err, &rej) || rej.Status != 400 || rej.Path != "requests[3].ts" || rej.Msg != "liegt in der Zukunft" {
		t.Errorf("400: %#v", err)
	}
	_, err = ingest(413, nil, `kein JSON`)
	if !errors.As(err, &rej) || rej.Status != 413 {
		t.Errorf("413: %#v", err)
	}
	_, err = ingest(500, nil, `{"error":"intern"}`)
	var se *ServerError
	if !errors.As(err, &se) || se.Status != 500 {
		t.Errorf("500: %#v", err)
	}
	if _, err := ingest(200, nil, `kein JSON`); err == nil {
		t.Error("200 mit kaputtem JSON angenommen")
	}
	if _, err := ingest(200, nil, `{"requests":1,"x":"`+strings.Repeat("a", 70*1024)+`"}`); err == nil {
		t.Error("200 mit 70 KiB angenommen")
	}
}

func TestRejectedMessageIsCleaned(t *testing.T) {
	root := testpki.NewRoot(t)
	msg := "a\\u001b[31mb‮c" + strings.Repeat("x", 500)
	srv := statusServer(t, root, 400, nil, `{"error":"`+msg+`","path":"p​"}`)
	_, err := newClient(t, srv.URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
	var rej *RejectedError
	if !errors.As(err, &rej) {
		t.Fatalf("%#v", err)
	}
	for _, s := range []string{rej.Msg, rej.Path, err.Error()} {
		if strings.ContainsRune(s, 0x1b) || protocol.HasSpoofRune(s) {
			t.Errorf("Steuer-/Formatzeichen in %q", s)
		}
	}
	if !strings.HasPrefix(rej.Msg, "a[31mbc") || utf8.RuneCountInString(rej.Msg) > 200 {
		t.Errorf("Meldung %q (%d Zeichen)", rej.Msg, utf8.RuneCountInString(rej.Msg))
	}
	if rej.Path != "p" {
		t.Errorf("Pfad %q", rej.Path)
	}
}

func TestClean(t *testing.T) {
	if got := Clean("a\tb\nc dä"); got != "abcdä" {
		t.Errorf("%q", got)
	}
	long := strings.Repeat("ä", 300)
	if got := Clean(long); utf8.RuneCountInString(got) != 200 || !utf8.ValidString(got) {
		t.Errorf("Länge %d", utf8.RuneCountInString(got))
	}
}

func TestIngestTimesOutOnSilentServer(t *testing.T) {
	root := testpki.NewRoot(t)
	done := make(chan struct{})
	srv := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
		<-done
	})
	// Läuft vor srv.Close (Cleanups in umgekehrter Reihenfolge), damit Close nicht hängt.
	t.Cleanup(func() { close(done) })
	c := newClient(t, srv.URL, root, 300*time.Millisecond)
	start := time.Now()
	_, err := c.Ingest(context.Background(), testKey, []byte(`{}`))
	if err == nil {
		t.Fatal("kein Fehler")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("dauerte %v", d)
	}
}

// Pair schickt plugin_version und die Signaturschlüssel mit; sign_public_keys_previous nur, wenn es welche gibt.
func TestPairSendsSignKeysAndVersion(t *testing.T) {
	root := testpki.NewRoot(t)
	good := `{"device_id":7,"device_key":"` + testKey + `","ca_fingerprint":""}`
	old := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = old })
	var sent string
	srv := pairServer(t, root, 200, good, &sent)
	c := newClient(t, srv.URL, root, 5*time.Second)
	if _, err := c.Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(sent), &body); err != nil {
		t.Fatal(err)
	}
	if body["plugin_version"] != "1.2.3" || body["sign_public_key"] != testSignPub {
		t.Fatalf("Anfrage %s", sent)
	}
	// Besitznachweis: Signatur über Vorsatz plus genau diesen Code, mit dem aktuellen Schlüssel.
	if proof, _ := body["sign_public_key_proof"].(string); !protocol.VerifyPairProof(testSignPub, testServerID, "c", proof) {
		t.Fatalf("Besitznachweis fehlt oder passt nicht: %s", sent)
	}
	if _, ok := body["sign_public_keys_previous"]; ok {
		t.Fatalf("leere Liste mitgeschickt: %s", sent)
	}
	if _, err := c.Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false,
		SignKeys{Public: testSignPub, Priv: testSignPriv, Previous: []string{testSignPrev}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sent, `"sign_public_keys_previous":["`+testSignPrev+`"]`) {
		t.Fatalf("Anfrage %s", sent)
	}
}

// Ungültige Schlüssel gehen nie raus: Pair bricht vor der Anfrage ab.
func TestPairRejectsBadSignKeys(t *testing.T) {
	root := testpki.NewRoot(t)
	var sent string
	srv := pairServer(t, root, 200, `{}`, &sent)
	c := newClient(t, srv.URL, root, 5*time.Second)
	many := make([]string, protocol.MaxSignKeysPrevious+1)
	for i := range many {
		many[i] = testSignPrev
	}
	for name, k := range map[string]SignKeys{
		"leer":               {},
		"kaputt":             {Public: "A6EHv", Priv: testSignPriv},
		"früherer kaputt":    {Public: testSignPub, Priv: testSignPriv, Previous: []string{"x"}},
		"zu viele frühere":   {Public: testSignPub, Priv: testSignPriv, Previous: many},
		"ohne privaten Teil": {Public: testSignPub},
		"fremder privater":   {Public: testSignPub, Priv: ed25519.NewKeyFromSeed(make([]byte, 32))},
		"privater zu kurz":   {Public: testSignPub, Priv: testSignPriv[:10]},
	} {
		if _, err := c.Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, k); err == nil {
			t.Errorf("%s: angenommen", name)
		}
	}
	for _, sid := range []string{"", strings.ToUpper(testServerID), testServerID[:63]} {
		if _, err := c.Pair(context.Background(), "c", "n", "linux", "amd64", testSource, sid, false, testSign); err == nil {
			t.Errorf("server_id %q: angenommen", sid)
		}
	}
	if sent != "" {
		t.Fatalf("Anfrage gesendet: %s", sent)
	}
}

// sign_keys_refused: übernommen werden nur Schlüssel, die das Plugin auch gesendet hat; fehlt das Feld, ist die Liste leer.
func TestPairSignKeysRefused(t *testing.T) {
	root := testpki.NewRoot(t)
	base := `{"device_id":7,"device_key":"` + testKey + `","ca_fingerprint":""`
	keys := SignKeys{Public: testSignPub, Priv: testSignPriv, Previous: []string{testSignPrev}}
	for body, want := range map[string][]string{
		base + `}`:                        nil,
		base + `,"sign_keys_refused":[]}`: nil,
		base + `,"sign_keys_refused":["` + testSignPrev + `"]}`:                       {testSignPrev},
		base + `,"sign_keys_refused":["` + testSignPrev + `","fremd\u001b[31m"]}`:     {testSignPrev},
		base + `,"sign_keys_refused":["` + testSignPub + `","` + testSignPrev + `"]}`: {testSignPub, testSignPrev},
	} {
		srv := pairServer(t, root, 200, body, nil)
		res, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, keys)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if !slices.Equal(res.SignKeysRefused, want) {
			t.Errorf("%s: %v", body, res.SignKeysRefused)
		}
	}
}

func pairServer(t *testing.T, root *testpki.CA, status int, body string, gotBody *string) *httptest.Server {
	return tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugin/pair" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if gotBody != nil {
			b, _ := io.ReadAll(r.Body)
			*gotBody = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
}

func TestPairValidatesResponse(t *testing.T) {
	root := testpki.NewRoot(t)
	fp := Fingerprint(root.DER())
	// Die Antwort enthält kein project_salt (protocol/doc.md); das Plugin verlangt keins.
	good := `{"device_id":7,"device_key":"` + testKey + `","ca_fingerprint":"` + fp + `"}`

	var sent string
	srv := pairServer(t, root, 200, good, &sent)
	res, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "ABCD-1234", "Laptop", "linux", "amd64", testSource, testServerID, false, testSign)
	if err != nil {
		t.Fatal(err)
	}
	if res.DeviceID != 7 || res.DeviceKey != testKey || res.CAFingerprint != fp || res.Repaired {
		t.Errorf("Ergebnis falsch: id=%d fp=%v", res.DeviceID, res.CAFingerprint == fp)
	}
	for _, want := range []string{`"code":"ABCD-1234"`, `"device_name":"Laptop"`, `"os":"linux"`, `"arch":"amd64"`, `"device_source":"` + testSource + `"`} {
		if !strings.Contains(sent, want) {
			t.Errorf("Anfrage ohne %s: %s", want, sent)
		}
	}
	if strings.Contains(sent, "device_name_explicit") {
		t.Errorf("device_name_explicit ohne ausdrücklichen Namen mitgeschickt: %s", sent)
	}
	if _, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "ABCD-1234", "Laptop", "linux", "amd64", testSource, testServerID, true, testSign); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sent, `"device_name_explicit":true`) {
		t.Errorf("Anfrage ohne device_name_explicit: %s", sent)
	}

	bad := map[string]string{
		"Schlüssel":  strings.Replace(good, testKey, "tl_dk_kurz", 1),
		"id 0":       strings.Replace(good, `"device_id":7`, `"device_id":0`, 1),
		"Fp kaputt":  strings.Replace(good, fp, "AB:CD", 1),
		"kein JSON":  "kein JSON",
		"id negativ": strings.Replace(good, `"device_id":7`, `"device_id":-1`, 1),
		"id fehlt":   strings.Replace(good, `"device_id":7,`, ``, 1),
	}
	for name, body := range bad {
		srv := pairServer(t, root, 200, body, nil)
		_, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign)
		if err == nil {
			t.Errorf("%s: angenommen", name)
		} else if strings.Contains(err.Error(), "tl_dk_") {
			t.Errorf("%s: Schlüssel im Fehler", name)
		}
	}

	for body, want := range map[string]bool{
		strings.Replace(good, "}", `,"repaired":true}`, 1):  true,
		strings.Replace(good, "}", `,"repaired":false}`, 1): false,
	} {
		srv := pairServer(t, root, 200, body, nil)
		res, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign)
		if err != nil || res.Repaired != want {
			t.Errorf("repaired %v: %+v %v", want, res.Repaired, err)
		}
	}

	srv = pairServer(t, root, 401, `{"error":"Code ungültig"}`, nil)
	if _, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign); !errors.Is(err, ErrBadCode) {
		t.Errorf("401: %v", err)
	}
	srv = pairServer(t, root, 429, `{"error":"zu viele"}`, nil)
	var re *RetryError
	if _, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign); !errors.As(err, &re) {
		t.Errorf("429: %v", err)
	}
}

func TestKeyNeverInError(t *testing.T) {
	root := testpki.NewRoot(t)
	cases := []struct {
		status int
		body   string
	}{
		{401, `{"error":"` + testKey + `"}`},
		{429, `{"error":"` + testKey + `"}`},
		{503, ``},
		{400, `{"error":"Schlüssel ` + testKey + ` falsch","path":"` + testKey + `"}`},
		{400, `{"error":"Schlüssel tl_dk_​` + testKey[6:] + `"}`},
		{404, `nicht gefunden ` + testKey},
		{500, `{"error":"` + testKey + `"}`},
		{302, ``},
		{200, `kein JSON ` + testKey},
	}
	for _, tc := range cases {
		srv := statusServer(t, root, tc.status, nil, tc.body)
		_, err := newClient(t, srv.URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
		if err == nil {
			t.Errorf("%d: kein Fehler", tc.status)
			continue
		}
		if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), testKey[6:]) {
			t.Errorf("%d: Schlüssel im Fehler: %q", tc.status, err.Error())
		}
	}
	// Verbindungsfehler (Server weg).
	srv := statusServer(t, root, 200, nil, `{}`)
	c := newClient(t, srv.URL, root, 2*time.Second)
	srv.Close()
	if _, err := c.Ingest(context.Background(), testKey, []byte(`{}`)); err == nil || strings.Contains(err.Error(), testKey) {
		t.Errorf("Verbindungsfehler: %v", err)
	}
	// Ungültiger Schlüssel darf weder gesendet noch angezeigt werden.
	bad := "tl_dk_\nX-Evil: 1"
	if _, err := c.Ingest(context.Background(), bad, []byte(`{}`)); err == nil || strings.Contains(err.Error(), bad) {
		t.Errorf("ungültiger Schlüssel: %v", err)
	}
}

func TestIngestRejectsMalformedKeyWithoutSending(t *testing.T) {
	root := testpki.NewRoot(t)
	var hit atomic.Bool
	srv := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
		okIngest(w, r)
	})
	c := newClient(t, srv.URL, root, 5*time.Second)
	for _, k := range []string{"", "tl_dk_kurz", testKey + "A", "Bearer " + testKey} {
		if _, err := c.Ingest(context.Background(), k, []byte(`{}`)); err == nil {
			t.Errorf("%q angenommen", k)
		}
	}
	if hit.Load() {
		t.Fatal("ungültiger Schlüssel wurde gesendet")
	}
}

func TestNewRejectsBadCA(t *testing.T) {
	root := testpki.NewRoot(t)
	u := mustURL(t, "https://tokenlore.lan")
	if _, err := New(u, []byte("kein PEM"), time.Second); err == nil {
		t.Error("kein PEM angenommen")
	}
	leaf := root.Leaf(t, "x")
	leafPEM := pemCert(leaf.Certificate[0])
	if _, err := New(u, leafPEM, time.Second); err == nil {
		t.Error("Blatt als Stamm-CA angenommen")
	}
	if _, err := New(nil, root.PEM(), time.Second); err == nil {
		t.Error("ohne URL angenommen")
	}
}

func pemCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestTransportErrorIsCleaned(t *testing.T) {
	root := testpki.NewRoot(t)
	// Ein MITM bestimmt die SANs seines Zertifikats; Go nennt sie ungequotet in der Hostnamen-Fehlermeldung.
	evil := "evil\x1b]0;PWNED\x07" + strings.Repeat("x", 40) + ".example"
	sans := []string{evil}
	for i := 0; i < 20; i++ {
		sans = append(sans, strings.Repeat("y", 50)+".example")
	}
	srv := tlsServer(t, root.Intermediate(t), sans, okIngest)
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	c := newClient(t, "https://localhost:"+port, root, 5*time.Second)
	_, err := c.Ingest(context.Background(), testKey, []byte(`{}`))
	if err == nil {
		t.Fatal("falscher Hostname angenommen")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\x1b\x07‮") || protocol.HasSpoofRune(msg) {
		t.Errorf("Steuerzeichen im Fehler: %q", msg)
	}
	if n := utf8.RuneCountInString(msg); n > 260 {
		t.Errorf("Fehler zu lang: %d Zeichen", n)
	}
	var te *TransportError
	if !errors.As(err, &te) {
		t.Errorf("kein TransportError: %T", err)
	}
	var he x509.HostnameError
	if !errors.As(err, &he) {
		t.Errorf("Unwrap verliert die Ursache: %v", err)
	}

	// Format-Zeichen (z. B. U+202E) kann ein IA5-SAN nicht tragen; direkt prüfen.
	inner := errors.New("a‮b\x1b[2J" + strings.Repeat("z", 500))
	te = &TransportError{Err: inner}
	if s := te.Error(); strings.ContainsAny(s, "‮\x1b") || utf8.RuneCountInString(strings.TrimPrefix(s, "Server nicht erreichbar: ")) > 200 {
		t.Errorf("%q", s)
	}
	if !errors.Is(te, inner) {
		t.Error("errors.Is verliert die Ursache")
	}
}

func TestFetchCATransportErrorIsCleaned(t *testing.T) {
	srv := caServer(t, 200, nil)
	u := mustURL(t, srv.URL)
	srv.Close()
	_, _, err := FetchCA(context.Background(), u)
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("kein TransportError: %T %v", err, err)
	}
}

func TestNewRejectsNonPositiveTimeout(t *testing.T) {
	root := testpki.NewRoot(t)
	u := mustURL(t, "https://tokenlore.lan")
	for _, d := range []time.Duration{0, -time.Second} {
		if _, err := New(u, root.PEM(), d); err == nil {
			t.Errorf("Timeout %v angenommen", d)
		}
	}
	c, err := New(u, root.PEM(), 60*time.Second)
	if err != nil || c.transport.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("ResponseHeaderTimeout: %v %v", err, c)
	}
	c, err = New(u, root.PEM(), 300*time.Millisecond)
	if err != nil || c.transport.ResponseHeaderTimeout != 300*time.Millisecond {
		t.Errorf("ResponseHeaderTimeout kurz: %v", err)
	}
}

func TestOnlyValidSelfSignedRootIsPinned(t *testing.T) {
	root := testpki.NewRoot(t)
	inter := root.Intermediate(t)
	expired := testpki.NewRootValid(t, time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour))
	future := testpki.NewRootValid(t, time.Now().Add(time.Hour), time.Now().Add(48*time.Hour))
	u := mustURL(t, "https://tokenlore.lan")
	for name, ca := range map[string]*testpki.CA{"Zwischen-CA": inter, "abgelaufen": expired, "noch nicht gültig": future} {
		if _, err := New(u, ca.PEM(), time.Second); err == nil {
			t.Errorf("New: %s angenommen", name)
		}
		srv := caServer(t, 200, ca.PEM())
		if _, _, err := FetchCA(context.Background(), mustURL(t, srv.URL)); err == nil {
			t.Errorf("FetchCA: %s angenommen", name)
		} else if !strings.Contains(err.Error(), "keine gültige Stamm-CA") {
			t.Errorf("FetchCA %s: Meldung %q", name, err)
		}
	}
}

func TestFlaggedSessionsFiltered(t *testing.T) {
	root := testpki.NewRoot(t)
	srv := statusServer(t, root, 200, nil,
		`{"requests":1,"sessions":1,"accounts":1,"flagged_sessions":["s-1","`+testKey+`","x`+testKey[6:]+`","mit Leerzeichen","a\u001b[31m",""]}`)
	res, err := newClient(t, srv.URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.FlaggedSessions) != 1 || res.FlaggedSessions[0] != "s-1" {
		t.Errorf("gefiltert: %d Einträge", len(res.FlaggedSessions))
	}
}

func TestNewWithoutCAUsesSystemRoots(t *testing.T) {
	root := testpki.NewRoot(t)
	srv := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, okIngest)
	u := mustURL(t, srv.URL)

	// Ohne CA gelten die System-Zertifikate; die Test-CA kennt dort niemand.
	c, err := New(u, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Ingest(context.Background(), testKey, []byte(`{}`))
	// Der Fehlertext ist absichtlich bereinigt (MsgCertMismatch); die Ursache bleibt über Unwrap prüfbar.
	var ua x509.UnknownAuthorityError
	if !errors.As(err, &ua) {
		t.Fatalf("unknown authority erwartet, bekam: %v", err)
	}
	if err.Error() != MsgCertUntrusted || !strings.Contains(err.Error(), "--ca <fingerabdruck>") || strings.Contains(err.Error(), "gepinnt") {
		t.Errorf("ohne CA neutrale Meldung erwartet, bekam: %q", err)
	}

	// Mit gepinnter CA klappt es.
	c, err = New(u, root.PEM(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Ingest(context.Background(), testKey, []byte(`{}`)); err != nil {
		t.Fatalf("mit CA: %v", err)
	}
}

func TestNewWithoutCAKeepsRedirectAndProxyRules(t *testing.T) {
	c, err := New(mustURL(t, "https://tokenlore.lan"), nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if c.transport.Proxy != nil {
		t.Error("Proxy gesetzt")
	}
	if c.transport.TLSClientConfig.RootCAs != nil {
		t.Error("RootCAs gesetzt: System-Zertifikate erwartet")
	}
	if c.transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("Prüfung abgeschaltet")
	}
}

func TestIngestPathUnderSubdir(t *testing.T) {
	for _, sub := range []string{"/sub", "/sub/index.php"} {
		root := testpki.NewRoot(t)
		var got string
		srv := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
			got = r.URL.Path
			okIngest(w, r)
		})
		base, err := ParseServerURL(srv.URL + sub + "/")
		if err != nil {
			t.Fatal(err)
		}
		c, err := New(base, root.PEM(), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Ingest(context.Background(), testKey, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if got != sub+"/api/plugin/ingest" {
			t.Errorf("Ingest-Pfad %q, erwartet %q", got, sub+"/api/plugin/ingest")
		}
		got = ""
		_, _ = c.Pair(context.Background(), "123456", "n", "linux", "amd64", testSource, testServerID, false, testSign)
		if got != sub+"/api/plugin/pair" {
			t.Errorf("Pair-Pfad %q, erwartet %q", got, sub+"/api/plugin/pair")
		}
	}
}

func TestFetchCAUnderSubdir(t *testing.T) {
	root := testpki.NewRoot(t)
	var got string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.Write(root.PEM())
	}))
	defer srv.Close()
	base, err := ParseServerURL(srv.URL + "/sub/index.php")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := FetchCA(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	if got != "/sub/index.php/api/plugin/ca" {
		t.Errorf("Pfad %q", got)
	}
}

func TestPairAcceptsEmptyFingerprint(t *testing.T) {
	root := testpki.NewRoot(t)
	body := `{"device_id":7,"device_key":"` + testKey + `","ca_fingerprint":""}`
	srv := pairServer(t, root, 200, body, nil)
	res, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign)
	if err != nil {
		t.Fatal(err)
	}
	if res.CAFingerprint != "" || res.DeviceID != 7 {
		t.Errorf("Ergebnis: %+v", res)
	}
}

// 409 source_taken wird zu ErrSourceTaken; ein anderer 409 bleibt eine gewöhnliche Ablehnung.
func TestPairSourceTaken(t *testing.T) {
	root := testpki.NewRoot(t)
	srv := pairServer(t, root, 409, `{"error":"source_taken"}`, nil)
	_, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign)
	if !errors.Is(err, ErrSourceTaken) {
		t.Fatalf("source_taken: %v", err)
	}
	other := pairServer(t, root, 409, `{"error":"anders"}`, nil)
	_, err = newClient(t, other.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign)
	if err == nil || errors.Is(err, ErrSourceTaken) {
		t.Fatalf("anderer 409: %v", err)
	}
}

// Mit gepinnter CA spricht die Meldung von der gepinnten CA, ohne sie (System-Zertifikate) neutral.
func TestCertErrorMessageDependsOnPinning(t *testing.T) {
	root := testpki.NewRoot(t)
	srv := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, okIngest)
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	// "localhost" steht nicht im Blatt-Zertifikat: Hostnamen-Fehler trotz vertrauter CA.
	c := newClient(t, "https://localhost:"+port, root, 5*time.Second)
	_, err := c.Ingest(context.Background(), testKey, []byte(`{}`))
	if err == nil || err.Error() != MsgCertMismatch {
		t.Fatalf("gepinnt: %v", err)
	}
	if !strings.Contains(MsgCertMismatch, "gepinnten CA") || strings.Contains(MsgCertUntrusted, "gepinnt") {
		t.Errorf("Texte vertauscht: %q / %q", MsgCertMismatch, MsgCertUntrusted)
	}
	if got := CertMessage(err); got != MsgCertMismatch {
		t.Errorf("CertMessage gepinnt: %q", got)
	}
	ce := &TransportError{Err: x509.UnknownAuthorityError{}}
	if got := CertMessage(ce); got != MsgCertUntrusted {
		t.Errorf("CertMessage ohne Pinning: %q", got)
	}
	// Auch eingewickelt bleibt die Art des Clients erkennbar.
	if got := CertMessage(fmt.Errorf("x: %w", &TransportError{Err: x509.UnknownAuthorityError{}, Pinned: true})); got != MsgCertMismatch {
		t.Errorf("CertMessage eingewickelt: %q", got)
	}
	// Ohne TransportError (kein Wissen über das Pinning): die neutrale Meldung.
	if got := CertMessage(x509.UnknownAuthorityError{}); got != MsgCertUntrusted {
		t.Errorf("CertMessage ohne TransportError: %q", got)
	}
}

func TestRequestsCarryUserAgent(t *testing.T) {
	old := Version
	Version = "9.9.9"
	t.Cleanup(func() { Version = old })
	root := testpki.NewRoot(t)

	var ingestUA, pairUA string
	srv := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/plugin/ingest":
			ingestUA = r.Header.Get("User-Agent")
			okIngest(w, r)
		case "/api/plugin/pair":
			pairUA = r.Header.Get("User-Agent")
			w.WriteHeader(403)
		default:
			http.NotFound(w, r)
		}
	})
	c := newClient(t, srv.URL, root, 5*time.Second)
	if _, err := c.Ingest(context.Background(), testKey, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	c.Pair(context.Background(), "ABCD-1234", "Laptop", "linux", "amd64", testSource, testServerID, false, testSign) // Antwort egal, nur der Header zählt
	if ingestUA != "tokenlore/9.9.9" || pairUA != "tokenlore/9.9.9" {
		t.Errorf("User-Agent: ingest %q, pair %q", ingestUA, pairUA)
	}
}

func TestIngestUnauthorizedReason(t *testing.T) {
	root := testpki.NewRoot(t)
	cases := map[string]string{
		`{"error":"unauthorized","reason":"repaired"}`: ReasonRepaired,
		`{"error":"unauthorized","reason":"revoked"}`:  ReasonRevoked,
		`{"error":"unauthorized","reason":"unknown"}`:  ReasonUnknown,
		`{"error":"unauthorized","reason":"anders"}`:   "",
		`{"error":"unauthorized"}`:                     "",
		`kein JSON`:                                    "",
		`{"error":"unauthorized","reason":7}`:          "",
	}
	for body, want := range cases {
		srv := statusServer(t, root, 401, nil, body)
		_, err := newClient(t, srv.URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
		var ue *UnauthorizedError
		if !errors.Is(err, ErrUnauthorized) || !errors.As(err, &ue) || ue.Reason != want {
			t.Errorf("%s: %#v", body, err)
		}
	}
}

// Hinweise in der Antwort: gültige Tage übernehmen, ungültige verwerfen, negative Anzahl wird 0.
func TestIngestNoticesParsed(t *testing.T) {
	root := testpki.NewRoot(t)
	ingest := func(status int, hdr map[string]string, body string) (IngestResult, error) {
		srv := statusServer(t, root, status, hdr, body)
		return newClient(t, srv.URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
	}
	res, err := ingest(200, nil, `{"requests":4,"legacy_days":2,"sessions":1,"accounts":1,"flagged_sessions":[],"elsewhere_requests":3,`+
		`"known_requests":2,"legacy_rejected_days":["2026-10-20","20.10.2026","2026-13-01",""]}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Requests != 4 || res.LegacyDays != 2 || res.ElsewhereRequests != 3 || res.KnownRequests != 2 ||
		len(res.LegacyRejectedDays) != 1 || res.LegacyRejectedDays[0] != "2026-10-20" {
		t.Fatalf("Antwort %+v", res)
	}
	res, err = ingest(200, nil, `{"requests":1,"sessions":1,"accounts":1,"elsewhere_requests":-5,"known_requests":-1}`)
	if err != nil || res.ElsewhereRequests != 0 || res.KnownRequests != 0 || res.LegacyRejectedDays != nil {
		t.Fatalf("ohne Treffer %+v %v", res, err)
	}
}

// 503 mit reason "old_database" (Server mit Datenbank einer älteren Version) wird OldDatabaseError mit der Meldung des
// Servers, bei Info, Pair und Ingest, ohne Wiederholung. Jedes andere 503 bleibt RetryError.
func TestOldDatabase(t *testing.T) {
	root := testpki.NewRoot(t)
	const msg = "Die Datenbank des Servers stammt von einer älteren Version; bitte neu einrichten."
	body := `{"error":"` + msg + `","reason":"old_database"}`
	check := func(name string, err error, want string) {
		t.Helper()
		var od *OldDatabaseError
		var re *RetryError
		if !errors.As(err, &od) || errors.As(err, &re) || err.Error() != want {
			t.Errorf("%s: %#v", name, err)
		}
	}
	_, err := newClient(t, statusServer(t, root, 503, nil, body).URL, root, 5*time.Second).Info(context.Background())
	check("Info", err, msg)
	_, err = newClient(t, pairServer(t, root, 503, body, nil).URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign)
	check("Pair", err, msg)
	_, err = newClient(t, statusServer(t, root, 503, nil, body).URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
	check("Ingest", err, msg)

	// Ohne Meldung: fester Text. Der Geräteschlüssel fliegt aus der Meldung.
	_, err = newClient(t, statusServer(t, root, 503, nil, `{"reason":"old_database"}`).URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
	check("ohne Meldung", err, OldDatabaseText)
	_, err = newClient(t, statusServer(t, root, 503, nil, `{"error":"`+testKey+`","reason":"old_database"}`).URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
	if err == nil || strings.Contains(err.Error(), testKey[6:]) {
		t.Errorf("Schlüssel in der Meldung: %v", err)
	}

	// Anderes 503, anderer Grund, falscher Status, zu großer Rumpf: kein OldDatabaseError.
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"503 ohne Grund":    {503, `{"error":"` + msg + `"}`},
		"503 anderer Grund": {503, `{"error":"x","reason":"busy"}`},
		"503 kein JSON":     {503, `old_database`},
		"500 mit dem Grund": {500, body},
		"503 Rumpf zu groß": {503, `{"reason":"old_database","x":"` + strings.Repeat("a", 70*1024) + `"}`},
	} {
		_, err := newClient(t, statusServer(t, root, tc.status, nil, tc.body).URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
		var od *OldDatabaseError
		if err == nil || errors.As(err, &od) {
			t.Errorf("%s: %#v", name, err)
		}
	}
}

// 400 mit reason "sign_key_unknown" (Signatur passt zu keinem Schlüssel des Geräts, Fund M12): RejectedError trägt den
// Grund, bereinigt; unbekannte Gründe und Gründe aus einem zu großen Rumpf bleiben leer.
func TestRejectedReason(t *testing.T) {
	root := testpki.NewRoot(t)
	for name, tc := range map[string]struct {
		body, want string
	}{
		"bekannt":   {`{"error":"Signatur passt zu keinem Schlüssel des Geräts","path":"requests[0].s","reason":"sign_key_unknown"}`, ReasonSignKeyUnknown},
		"ohne":      {`{"error":"x","path":"requests[0].s"}`, ""},
		"unbekannt": {`{"error":"x","reason":"was anderes"}`, ""},
		"zu groß":   {`{"reason":"sign_key_unknown","x":"` + strings.Repeat("a", 70*1024) + `"}`, ""},
	} {
		_, err := newClient(t, statusServer(t, root, 400, nil, tc.body).URL, root, 5*time.Second).Ingest(context.Background(), testKey, []byte(`{}`))
		var rej *RejectedError
		if !errors.As(err, &rej) || rej.Reason != tc.want {
			t.Errorf("%s: %#v", name, err)
		}
	}
}

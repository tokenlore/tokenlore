package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tokenlore/tokenlore/internal/testpki"
	"github.com/tokenlore/tokenlore/protocol"
)

const testKey = "tl_dk_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

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
	if _, err := c.Pair(context.Background(), "code", "n", "linux", "amd64"); err == nil || hit.Load() {
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
	salt := strings.Repeat("ab", 32)
	fp := Fingerprint(root.DER())
	good := `{"device_id":7,"device_key":"` + testKey + `","project_salt":"` + salt + `","ca_fingerprint":"` + fp + `"}`

	var sent string
	srv := pairServer(t, root, 200, good, &sent)
	res, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "ABCD-1234", "Laptop", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if res.DeviceID != 7 || res.DeviceKey != testKey || res.ProjectSalt != salt || res.CAFingerprint != fp {
		t.Errorf("Ergebnis falsch: id=%d salt=%v fp=%v", res.DeviceID, res.ProjectSalt == salt, res.CAFingerprint == fp)
	}
	for _, want := range []string{`"code":"ABCD-1234"`, `"device_name":"Laptop"`, `"os":"linux"`, `"arch":"amd64"`} {
		if !strings.Contains(sent, want) {
			t.Errorf("Anfrage ohne %s: %s", want, sent)
		}
	}

	bad := map[string]string{
		"Schlüssel":  strings.Replace(good, testKey, "tl_dk_kurz", 1),
		"Salz 63":    strings.Replace(good, salt, salt[:63], 1),
		"Salz groß":  strings.Replace(good, salt, strings.ToUpper(salt), 1),
		"id 0":       strings.Replace(good, `"device_id":7`, `"device_id":0`, 1),
		"Fp kaputt":  strings.Replace(good, fp, "AB:CD", 1),
		"kein JSON":  "kein JSON",
		"id negativ": strings.Replace(good, `"device_id":7`, `"device_id":-1`, 1),
		"id fehlt":   strings.Replace(good, `"device_id":7,`, ``, 1),
	}
	for name, body := range bad {
		srv := pairServer(t, root, 200, body, nil)
		_, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64")
		if err == nil {
			t.Errorf("%s: angenommen", name)
		} else if strings.Contains(err.Error(), "tl_dk_") {
			t.Errorf("%s: Schlüssel im Fehler", name)
		}
	}

	srv = pairServer(t, root, 401, `{"error":"Code ungültig"}`, nil)
	if _, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64"); !errors.Is(err, ErrBadCode) {
		t.Errorf("401: %v", err)
	}
	srv = pairServer(t, root, 429, `{"error":"zu viele"}`, nil)
	var re *RetryError
	if _, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64"); !errors.As(err, &re) {
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
		_, _ = c.Pair(context.Background(), "123456", "n", "linux", "amd64")
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
	salt := strings.Repeat("ab", 32)
	body := `{"device_id":7,"device_key":"` + testKey + `","project_salt":"` + salt + `","ca_fingerprint":""}`
	srv := pairServer(t, root, 200, body, nil)
	res, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if res.CAFingerprint != "" || res.DeviceID != 7 {
		t.Errorf("Ergebnis: %+v", res)
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
	c.Pair(context.Background(), "ABCD-1234", "Laptop", "linux", "amd64") // Antwort egal, nur der Header zählt
	if ingestUA != "tokenlore/9.9.9" || pairUA != "tokenlore/9.9.9" {
		t.Errorf("User-Agent: ingest %q, pair %q", ingestUA, pairUA)
	}
}

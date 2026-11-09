package client

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/internal/testpki"
)

const testServerID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// Info holt server_id und min_plugin_version per GET, mit User-Agent, und prüft beide Felder.
func TestInfo(t *testing.T) {
	root := testpki.NewRoot(t)
	var method, path, ua string
	srv := tlsServer(t, root.Intermediate(t), []string{"127.0.0.1"}, func(w http.ResponseWriter, r *http.Request) {
		method, path, ua = r.Method, r.URL.Path, r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"server_id":"` + testServerID + `","min_plugin_version":"1.2.3"}`))
	})
	info, err := newClient(t, srv.URL+"/sub", root, 5*time.Second).Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.ServerID != testServerID || info.MinPluginVersion != "1.2.3" {
		t.Fatalf("Info %+v", info)
	}
	if method != http.MethodGet || path != "/sub/api/plugin/info" || !strings.HasPrefix(ua, "tokenlore/") {
		t.Fatalf("Anfrage %s %s %q", method, path, ua)
	}
}

// Ohne gültige server_id koppelt das Plugin nicht; eine ungültige Mindestversion fällt nur weg.
func TestInfoValidates(t *testing.T) {
	root := testpki.NewRoot(t)
	bad := []string{
		`{"min_plugin_version":"1.2.3"}`,
		`{"server_id":"` + strings.ToUpper(testServerID) + `"}`,
		`{"server_id":"` + testServerID[:63] + `"}`,
		`{"server_id":7}`,
		`kein JSON`,
	}
	for _, body := range bad {
		srv := statusServer(t, root, 200, nil, body)
		if _, err := newClient(t, srv.URL, root, 5*time.Second).Info(context.Background()); err == nil {
			t.Errorf("%s: kein Fehler", body)
		}
	}
	srv := statusServer(t, root, 200, nil, `{"server_id":"`+testServerID+`","min_plugin_version":"1.2"}`)
	info, err := newClient(t, srv.URL, root, 5*time.Second).Info(context.Background())
	if err != nil || info.ServerID != testServerID || info.MinPluginVersion != "" {
		t.Fatalf("ungültige Mindestversion: %+v %v", info, err)
	}
}

// Fehlerantworten von Info: 429 mit Retry-After, 404 (Server ohne Info, zu alt) als Ablehnung.
func TestInfoErrors(t *testing.T) {
	root := testpki.NewRoot(t)
	srv := statusServer(t, root, 429, map[string]string{"Retry-After": "7"}, `{"error":"rate_limited"}`)
	_, err := newClient(t, srv.URL, root, 5*time.Second).Info(context.Background())
	var re *RetryError
	if !errors.As(err, &re) || re.After != 7*time.Second {
		t.Fatalf("429: %#v", err)
	}
	srv = statusServer(t, root, 404, nil, `{"error":"not_found"}`)
	_, err = newClient(t, srv.URL, root, 5*time.Second).Info(context.Background())
	var rej *RejectedError
	if !errors.As(err, &rej) || rej.Status != 404 {
		t.Fatalf("404: %#v", err)
	}
}

// 426 beim Koppeln und Liefern wird OutdatedError mit der Mindestversion; eine ungültige Version fällt weg.
func TestOutdated(t *testing.T) {
	root := testpki.NewRoot(t)
	cases := map[string]string{
		`{"error":"plugin_outdated","min_plugin_version":"2.10.0"}`:     "2.10.0",
		`{"error":"plugin_outdated","min_plugin_version":"2.10\u001b"}`: "",
		`{"error":"plugin_outdated"}`:                                   "",
		`kein JSON`:                                                     "",
	}
	for body, want := range cases {
		srv := statusServer(t, root, 426, nil, body)
		c := newClient(t, srv.URL, root, 5*time.Second)
		_, err := c.Pair(context.Background(), "123456", "n", "linux", "amd64", testSource, testServerID, false, testSign)
		var oe *OutdatedError
		if !errors.As(err, &oe) || oe.Min != want {
			t.Errorf("Pair %s: %#v", body, err)
		}
		_, err = c.Ingest(context.Background(), testKey, []byte(`{}`))
		if !errors.As(err, &oe) || oe.Min != want {
			t.Errorf("Ingest %s: %#v", body, err)
		}
	}
}

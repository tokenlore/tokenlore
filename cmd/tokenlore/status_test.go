package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

func TestStatusNotConnected(t *testing.T) {
	cfg, data := connectEnv(t)
	var out bytes.Buffer
	if code := runStatus(nil, &out, data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "Nicht verbunden") {
		t.Fatalf("Ausgabe:\n%s", out.String())
	}
}

func TestStatusShowsConnectionAndStatus(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	s := &state.Status{LastAttempt: time.Now(), LastError: "Serverfehler \x1b[31m(Status 500)", Warnings: []string{"w1", "w2\x07"}}
	s.Save(data)
	var out bytes.Buffer
	if code := runStatus(nil, &out, data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	o := out.String()
	for _, want := range []string{f.srv.URL, "Testgerät", "7", f.fingerprint(), "Datei", "Serverfehler", "Warnungen: 2", "E-Mail-Stufe: off", "Offen: 1 Anfrage in 1 Paket"} {
		if !strings.Contains(o, want) {
			t.Fatalf("Ausgabe ohne %q:\n%s", want, o)
		}
	}
	if strings.ContainsAny(o, "\x1b\x07") {
		t.Fatal("Steuerzeichen ausgegeben")
	}
}

func TestStatusShowsNextPackage(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	before, err := os.ReadFile(filepath.Join(data, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runStatus([]string{"--next"}, &out, data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	o := out.String()
	i := strings.Index(o, "{")
	if i < 0 {
		t.Fatalf("kein JSON:\n%s", o)
	}
	var b protocol.Batch
	if err := json.Unmarshal([]byte(o[i:]), &b); err != nil {
		t.Fatalf("JSON: %v\n%s", err, o[i:])
	}
	if b.V != protocol.Version || len(b.Requests) == 0 {
		t.Fatalf("Paket %+v", b)
	}
	if !strings.Contains(o[i:], "\n  ") {
		t.Fatal("JSON nicht eingerückt")
	}
	after, _ := os.ReadFile(filepath.Join(data, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("status hat state.json verändert")
	}
	if _, err := os.Stat(filepath.Join(data, "status.json")); !os.IsNotExist(err) {
		t.Fatal("status hat status.json geschrieben")
	}
}

func TestStatusNeverPrintsKey(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	var out bytes.Buffer
	runStatus([]string{"--next"}, &out, data, cfg)
	assertKeyAbsent(t, data, out.String())
}

// F7: Warnungen nennen Pfade und Projektordner. /tokenlore:status landet in Claudes Kontext,
// also standardmäßig nur die Anzahl; die Texte mit --warnings fürs Terminal.
func TestStatusWarningsOnlyOnRequest(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	s := &state.Status{Warnings: []string{"/pfad/zu/projektordner-a", "/pfad/zu/projektordner-b"}}
	s.Save(data)
	var out bytes.Buffer
	runStatus(nil, &out, data, cfg)
	if !strings.Contains(out.String(), "Warnungen: 2") || strings.Contains(out.String(), "projektordner") {
		t.Fatalf("Ausgabe:\n%s", out.String())
	}
	out.Reset()
	runStatus([]string{"--warnings"}, &out, data, cfg)
	if !strings.Contains(out.String(), "  - /pfad/zu/projektordner-a") {
		t.Fatalf("--warnings:\n%s", out.String())
	}
}

// F9: Nach vollständiger Lieferung ist nichts offen, auch wenn Split immer ein erstes Paket bildet.
func TestStatusNothingOpenAfterSync(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	testLock(t)
	if code, out, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
	var out bytes.Buffer
	if code := runStatus([]string{"--next"}, &out, data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "Kein Paket offen.") || strings.Contains(out.String(), "Offen:") || strings.Contains(out.String(), "{") {
		t.Fatalf("Ausgabe:\n%s", out.String())
	}
}

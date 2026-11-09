package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/deliver"
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
	for _, want := range []string{f.srv.URL, "Testgerät", "7", f.fingerprint(), "Datei", "Serverfehler", "Warnungen: 2", "E-Mail-Stufe: off", "Noch nicht im Archiv: 1 Anfrage"} {
		if !strings.Contains(o, want) {
			t.Fatalf("Ausgabe ohne %q:\n%s", want, o)
		}
	}
	if strings.ContainsAny(o, "\x1b\x07") {
		t.Fatal("Steuerzeichen ausgegeben")
	}
}

// Was archiviert, aber nicht geliefert ist, zeigt status als offenes Paket aus dem Archiv; es schreibt nichts.
func TestStatusShowsNextPackage(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	testLock(t)
	f.ingestCode = 500
	if code, _, _ := syncRun(data, cfg); code != 1 {
		t.Fatalf("sync Exit %d, erwartet Fehler", code)
	}
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
	if b.V != protocol.Version || len(b.Requests) == 0 || b.Requests[0].S == "" {
		t.Fatalf("Paket %+v", b)
	}
	if !strings.Contains(o, "Offen: 1 Anfrage in 1 Paket") {
		t.Fatalf("Ausgabe:\n%s", o)
	}
	if !strings.Contains(o[i:], "\n  ") {
		t.Fatal("JSON nicht eingerückt")
	}
	after, _ := os.ReadFile(filepath.Join(data, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("status hat state.json verändert")
	}
	beforeStat, _ := os.ReadFile(filepath.Join(data, "status.json"))
	runStatus([]string{"--next"}, &out, data, cfg)
	if afterStat, _ := os.ReadFile(filepath.Join(data, "status.json")); !bytes.Equal(beforeStat, afterStat) {
		t.Fatal("status hat status.json verändert")
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

// status nennt neue Felder mit Namen: die gemerkten aus dem Status und die aus dem, was offen ist; nie ihre Werte.
func TestStatusShowsNewFields(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	(&state.Status{NewFields: []string{"altesFeld"}}).Save(data)
	line := `{"type":"assistant","sessionId":"s2","requestId":"req_N","timestamp":"2026-10-23T21:30:00.000Z","neuesFeld":"GEHEIMER-WERT","message":{"id":"msg_N","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
	if err := os.WriteFile(filepath.Join(cfg, "projects", "P", "s2.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runStatus(nil, &out, data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "Neue Felder in den Sitzungsdateien (nicht übernommen): altesFeld, neuesFeld\n") ||
		strings.Contains(out.String(), "GEHEIM") {
		t.Fatalf("Ausgabe:\n%s", out.String())
	}
}

// status meldet, wenn der Abgleich das fehlende Archiv neu angelegt hat.
func TestStatusShowsArchiveRecreated(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	var out bytes.Buffer
	runStatus(nil, &out, data, cfg)
	if strings.Contains(out.String(), "Archiv fehlte") {
		t.Fatalf("Meldung ohne Anlass:\n%s", out.String())
	}
	(&state.Status{ArchiveRecreated: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}).Save(data)
	out.Reset()
	runStatus(nil, &out, data, cfg)
	if !strings.Contains(out.String(), "Hinweis: "+deliver.MsgArchiveRecreated+" ("+when(time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))+")\n") {
		t.Fatalf("Ausgabe:\n%s", out.String())
	}
}

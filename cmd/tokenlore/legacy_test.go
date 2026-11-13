package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

// legacyDates sind die Tage aller Vorgeschichte-Hüllen in den Paketen, in Reihenfolge.
func legacyDates(bs []protocol.Batch) []string {
	var out []string
	for _, b := range bs {
		for _, e := range b.LegacyDays {
			var d protocol.DayLine
			if json.Unmarshal([]byte(e.Z), &d) == nil {
				out = append(out, d.Date)
			}
		}
	}
	return out
}

// archivedMonth meldet, ob der Oktober im Archivordner dir steht, gepackt oder nicht (gepackt wird nach der Uhr).
func archivedMonth(dir string) bool {
	for _, n := range []string{"2026-10.jsonl", "2026-10.jsonl.gz"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}

// requestZ sind die Inhaltszeilen aller Anfrage-Hüllen in den Paketen.
func requestZ(bs []protocol.Batch) []string {
	var out []string
	for _, b := range bs {
		for _, e := range b.Requests {
			out = append(out, e.Z)
		}
	}
	return out
}

// newRequestLine ist eine weitere gültige Anfrage für s1.jsonl.
func newRequestLine(n int) string {
	return fmt.Sprintf(`{"type":"assistant","sessionId":"s1","requestId":"req_N%d","timestamp":"2026-10-23T22:00:00.000Z","version":"2.1.309","message":{"id":"msg_N%d","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}`+"\n", n, n)
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	restFile(t, path)
}

// restFile setzt die Änderungszeit eine Stunde zurück: Die Datei ruht, auch ihre letzte Anfrage gilt als fertig.
func restFile(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

const statsCacheWithNewDay = `{"version":2,"lastComputedDate":"2026-08-16",
"dailyActivity":[{"date":"2026-08-14","messageCount":120,"sessionCount":3,"toolCallCount":40},
{"date":"2026-08-15","messageCount":80,"sessionCount":2,"toolCallCount":25},
{"date":"2026-08-16","messageCount":5,"sessionCount":1,"toolCallCount":1}],
"dailyModelTokens":[{"date":"2026-08-16","tokensByModel":{"claude-opus-4-1":700}}]}`

// Kern des Wunsches: Archiv an Server 1 geliefert, dann Koppeln mit Server 2 → das ganze Archiv mit der Vorgeschichte
// geht an Server 2, jede Zeile genau einmal; die Lesestände bleiben.
func TestLegacyGoesToSecondServerAfterConnect(t *testing.T) {
	testLock(t)
	cfg, data := connectEnv(t)
	f1 := newFakeServer(t)
	mustConnect(t, cfg, data, f1)
	if got := legacyDates(f1.ingested()); strings.Join(got, ",") != "2026-08-14,2026-08-15" {
		t.Fatalf("Server 1: %v", got)
	}
	if !strings.Contains(f1.rawBodies[0], `\"complete_before\":\"2026-08-15\"`) {
		t.Fatalf("Server 1 ohne complete_before: %s", f1.rawBodies[0])
	}
	// Nichts Neues: kein zweites Senden.
	if code, out, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("sync Exit %d: %s %s", code, out, errOut)
	}
	if got := legacyDates(f1.ingested()); len(got) != 2 {
		t.Fatalf("Vorgeschichte an Server 1 erneut gesendet: %v", got)
	}
	st1, _ := state.Load(data)

	f2 := newFakeServer(t)
	mustConnect(t, cfg, data, f2)
	if got := legacyDates(f2.ingested()); strings.Join(got, ",") != "2026-08-14,2026-08-15" {
		t.Fatalf("Server 2 bekam nicht die ganze Vorgeschichte: %v", got)
	}
	if a, b := requestZ(f1.ingested()), requestZ(f2.ingested()); len(b) == 0 || strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Fatalf("Server 2 bekam andere Anfragen: %d gegen %d", len(b), len(a))
	}
	if st2, _ := state.Load(data); !reflect.DeepEqual(st1.Files, st2.Files) {
		t.Fatalf("Lesestände beim Koppeln verändert: %v → %v", st1.Files, st2.Files)
	}
}

// Die Vorgeschichte kommt einmal ins Archiv: Ändert sich stats-cache.json danach, geht nichts davon erneut.
func TestLegacyTakenOnce(t *testing.T) {
	testLock(t)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	n := len(f.ingested())
	if err := os.WriteFile(filepath.Join(cfg, "stats-cache.json"), []byte(statsCacheWithNewDay), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("sync Exit %d: %s %s", code, out, errOut)
	}
	if d := legacyDates(f.ingested()[n:]); len(d) != 0 {
		t.Fatalf("nach Änderung erneut gesendet: %v", d)
	}
}

// Nach der Lieferung stimmen Kennungsdatei und Kopie überein; die Datei trägt nur id, machine und predecessor.
func TestIdentityCopyAfterDelivery(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	id := identityFile(t, cfg)
	st, _ := state.Load(data)
	if st.Identity == nil || *st.Identity != id {
		t.Fatalf("Kopie %+v, Datei %+v", st.Identity, id)
	}
	b, _ := os.ReadFile(filepath.Join(cfg, "tokenlore-id"))
	if strings.Contains(string(b), "last_day") || strings.Contains(string(b), "branch_day") {
		t.Fatalf("Kennungsdatei %s", b)
	}
}

// Scheitert die Lieferung beim Koppeln, warten die Daten im Archiv und gehen mit dem nächsten Abgleich.
func TestFailedDeliveryWaitsInArchive(t *testing.T) {
	testLock(t)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.ingestCode = 500
	mustConnect(t, cfg, data, f)
	dir := state.ArchiveDir(data, identityFile(t, cfg).ID)
	if !archivedMonth(dir) {
		t.Fatal("Archiv fehlt")
	}
	if _, err := os.Stat(filepath.Join(dir, "geliefert.json")); !os.IsNotExist(err) {
		t.Fatal("Merker trotz Fehler")
	}
	f.ingestCode = 0
	n := len(f.ingested())
	if code, o, e := syncRun(data, cfg); code != 0 {
		t.Fatalf("sync Exit %d: %s %s", code, o, e)
	}
	if got := f.ingested()[n:]; requestCount(got) == 0 || len(legacyDates(got)) != 2 {
		t.Fatalf("nach dem Fehler nicht geliefert: %d Anfragen", requestCount(got))
	}
}

// Kennung geändert (nur der Datenordner kopiert): gar nichts geht an den Server, status und Sessionstart nennen die
// Schritte zum Neukoppeln. Nach dem Neukoppeln geht alles Offene.
func TestIdentityChangedDeliversNothing(t *testing.T) {
	testLock(t)
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	n := len(f.ingested())
	// Der Datenordner liegt jetzt auf einem anderen Rechner mit eigenem Claude-Ordner.
	setMachine(t, machineB)
	other := `{"id":"` + copyID + `","machine":"","predecessor":"","last_day":""}` + "\n"
	writeIdentityFile(t, cfg, other)
	appendTo(t, filepath.Join(cfg, "projects", "P", "s1.jsonl"), newRequestLine(1))
	if err := os.WriteFile(filepath.Join(cfg, "stats-cache.json"), []byte(statsCacheWithNewDay), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	runStatus(nil, &out, data, cfg)
	if !strings.Contains(out.String(), "Achtung: "+deliver.MsgOtherDevice) || !strings.Contains(out.String(), f.srv.URL+"/settings/add-device") ||
		strings.Count(out.String(), "So koppelst du neu") != 1 {
		t.Fatalf("status ohne Meldung:\n%s", out.String())
	}
	if msg := sessionStartNotice(data, cfg); !strings.Contains(msg, deliver.MsgOtherDevice) || !strings.Contains(msg, "So koppelst du neu") {
		t.Fatalf("Sessionstart ohne Meldung: %q", msg)
	}

	code, _, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(errOut, deliver.MsgOtherDevice) {
		t.Fatalf("sync Exit %d: %s", code, errOut)
	}
	if got := f.ingested()[n:]; len(got) != 0 {
		t.Fatalf("trotz fremder Kennung geliefert: %d Pakete", len(got))
	}
	// Die fremde Datei bleibt unangetastet.
	b, _ := os.ReadFile(filepath.Join(cfg, "tokenlore-id"))
	if string(b) != other {
		t.Fatalf("Kennungsdatei verändert: %s", b)
	}

	// Neu koppeln mit der neuen Kennung: Meldung weg, Altbestand und die neue Anfrage gehen. Die bisherige Verbindung
	// gehörte zur alten Kennung: kein Aufruf, ihr Gerät zu sperren.
	f2 := newFakeServer(t)
	if o := mustConnect(t, cfg, data, f2); !strings.Contains(o, "gehörte zu einer anderen Kennung") || strings.Contains(o, "bis du es dort sperrst") {
		t.Fatalf("Ausgabe:\n%s", o)
	}
	if !hasLegacy(f2.ingested()) || requestCount(f2.ingested()) == 0 {
		t.Fatal("nach Neukoppeln nicht alles geliefert")
	}
	if msg := sessionStartNotice(data, cfg); msg != "" {
		t.Fatalf("Meldung nach Neukoppeln: %q", msg)
	}
	out.Reset()
	runStatus(nil, &out, data, cfg)
	if strings.Contains(out.String(), "Achtung:") {
		t.Fatalf("status meldet nach Neukoppeln noch:\n%s", out.String())
	}
}

// Datenordner und Claude-Ordner auf einen anderen Rechner kopiert: Die Kennung passt, der Rechner nicht. Es geht
// nichts an den Server; connect macht daraus ohne Rückfrage ein eigenes Gerät, das danach liefert.
func TestOtherMachineDeliversNothing(t *testing.T) {
	testLock(t)
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	first := identityFile(t, cfg)
	n := len(f.ingested())
	appendTo(t, filepath.Join(cfg, "projects", "P", "s1.jsonl"), newRequestLine(2))

	setMachine(t, machineB)
	code, _, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(errOut, deliver.MsgOtherDevice) {
		t.Fatalf("sync Exit %d: %s", code, errOut)
	}
	if got := f.ingested()[n:]; len(got) != 0 {
		t.Fatalf("auf anderem Rechner geliefert: %d Pakete", len(got))
	}
	// Nach dem gesperrten Abgleich steht die Meldung als letzter Fehler, nicht noch einmal als Achtung; die Schritte einmal.
	var out bytes.Buffer
	runStatus(nil, &out, data, cfg)
	if s := out.String(); !strings.Contains(s, "Letzter Fehler: "+deliver.MsgOtherDevice) || strings.Contains(s, "Achtung:") ||
		strings.Count(s, "So koppelst du neu") != 1 {
		t.Fatalf("status:\n%s", s)
	}

	// Die kopierte Verbindung gehört dem ursprünglichen Rechner: connect fordert nicht auf, dessen Gerät zu sperren.
	o := mustConnect(t, cfg, data, f)
	if !strings.Contains(o, "dieser Rechner bekommt eine eigene Kennung") || !strings.Contains(o, "gehörte zu einer anderen Kennung") ||
		strings.Contains(o, "bis du es dort sperrst") {
		t.Fatalf("Ausgabe:\n%s", o)
	}
	id := identityFile(t, cfg)
	if id.ID == first.ID || id.Predecessor != first.ID || id.Machine != machineB {
		t.Fatalf("Kennung nach Koppeln %+v", id)
	}
	if requestCount(f.ingested()[n:]) == 0 {
		t.Fatal("eigenes Gerät liefert nicht")
	}
	if msg := sessionStartNotice(data, cfg); msg != "" {
		t.Fatalf("Meldung nach Neukoppeln: %q", msg)
	}
}

// Systemkennung nicht lesbar: Es wird geliefert, status warnt.
func TestNoMachineDeliversAndWarns(t *testing.T) {
	testLock(t)
	setMachine(t, "")
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	n := len(f.ingested())
	appendTo(t, filepath.Join(cfg, "projects", "P", "s1.jsonl"), newRequestLine(3))
	if code, o, e := syncRun(data, cfg); code != 0 {
		t.Fatalf("sync Exit %d: %s %s", code, o, e)
	}
	if requestCount(f.ingested()[n:]) == 0 {
		t.Fatal("ohne Systemkennung nicht geliefert")
	}
	var out bytes.Buffer
	runStatus(nil, &out, data, cfg)
	if !strings.Contains(out.String(), "Warnung: "+deliver.MsgNoMachine) || strings.Contains(out.String(), "Achtung:") {
		t.Fatalf("status:\n%s", out.String())
	}
}

// 401 mit Grund: Meldung in sync, status und Sessionstart; nach erfolgreicher Lieferung ist sie weg.
func TestUnauthorizedReasonMessages(t *testing.T) {
	for reason, want := range map[string]string{
		"repaired": deliver.MsgRepaired,
		"revoked":  deliver.MsgRevoked,
		"unknown":  deliver.MsgUnknownKey,
	} {
		t.Run(reason, func(t *testing.T) {
			testLock(t)
			f := newFakeServer(t)
			cfg, data := connectedEnv(t, f)
			f.ingestCode, f.ingestBody = 401, `{"error":"unauthorized","reason":"`+reason+`"}`
			code, _, errOut := syncRun(data, cfg)
			if code != 1 || !strings.Contains(errOut, want) {
				t.Fatalf("Exit %d: %s", code, errOut)
			}
			var out bytes.Buffer
			runStatus(nil, &out, data, cfg)
			if !strings.Contains(out.String(), "Letzter Fehler: "+want) {
				t.Fatalf("status:\n%s", out.String())
			}
			if msg := sessionStartNotice(data, cfg); !strings.Contains(msg, want) {
				t.Fatalf("Sessionstart: %q", msg)
			}
			// Danach scheitert etwas anderes: Der Sessionstart meldet weiter den abgelehnten Schlüssel.
			f.ingestCode, f.ingestBody = 500, ""
			syncRun(data, cfg)
			if msg := sessionStartNotice(data, cfg); !strings.Contains(msg, want) {
				t.Fatalf("Sessionstart nach anderem Fehler: %q", msg)
			}
			out.Reset()
			runStatus(nil, &out, data, cfg)
			if !strings.Contains(out.String(), "Schlüssel abgelehnt: "+want) {
				t.Fatalf("status nach anderem Fehler:\n%s", out.String())
			}
			f.ingestCode = 0
			if code, o, e := syncRun(data, cfg); code != 0 {
				t.Fatalf("sync Exit %d: %s %s", code, o, e)
			}
			if msg := sessionStartNotice(data, cfg); msg != "" {
				t.Fatalf("Meldung nach Erfolg: %q", msg)
			}
		})
	}
}

func TestRepairedMessageSaysWhatToDo(t *testing.T) {
	m := deliver.MsgRepaired
	if !strings.Contains(m, "Dieses Gerät wurde anderswo neu gekoppelt") || !strings.Contains(m, "neu") {
		t.Fatalf("Meldung %q", m)
	}
	steps := deliver.RepairSteps("https://tokenlore.example/")
	if !strings.Contains(steps, "1. Auf https://tokenlore.example/settings/add-device einen Code holen.") || !strings.Contains(steps, "2. Den Befehl") {
		t.Fatalf("Schritte %q", steps)
	}
	if s := deliver.RepairSteps(""); !strings.Contains(s, "„Gerät hinzufügen“") {
		t.Fatalf("Schritte ohne Adresse %q", s)
	}
	for _, s := range []string{deliver.MsgRepaired, deliver.MsgRevoked, deliver.MsgUnknownKey, deliver.MsgOtherDevice} {
		if n := len([]rune(s)); n > 200 {
			t.Errorf("%d Zeichen, Status kürzt auf 200: %q", n, s)
		}
	}
}

// Abgezweigter Rechner (kopierter Ordner): Er bekommt ein eigenes Archiv unter seiner neuen Kennung; das Archiv des
// Ursprungs liefert er nie.
func TestForkGetsOwnArchive(t *testing.T) {
	setMachine(t, machineB)
	cfg, data := connectEnv(t)
	writeIdentityFile(t, cfg, `{"id":"`+fixedID+`","machine":"`+machineA+`","predecessor":""}`)
	origin := state.ArchiveDir(data, fixedID)
	if err := os.MkdirAll(origin, 0o700); err != nil {
		t.Fatal(err)
	}
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	id := identityFile(t, cfg)
	if id.ID == fixedID {
		t.Fatal("keine eigene Kennung")
	}
	if !archivedMonth(state.ArchiveDir(data, id.ID)) {
		t.Fatal("eigenes Archiv fehlt")
	}
	if entries, _ := os.ReadDir(origin); len(entries) != 0 {
		t.Fatalf("Archiv des Ursprungs beschrieben: %v", entries)
	}
	if requestCount(f.ingested()) == 0 {
		t.Fatal("eigenes Gerät liefert nicht")
	}
}

// Der Sessionstart-Hook gibt die Meldung als JSON aus (systemMessage und additionalContext).
func TestSessionStartHookPrintsNotice(t *testing.T) {
	f := newFakeServer(t)
	_, data := connectedEnv(t, f)
	conn, _, _ := state.LoadConnection(data)
	conn.Identity = fixedID // gekoppelt mit einer Kennung, die es im Ordner nicht gibt
	if err := conn.Save(data); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=hook", "HELPER_OUT_FILE="+filepath.Join(t.TempDir(), "kind.txt"))
	cmd.Stdin = strings.NewReader(`{"session_id":"s1"}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		SystemMessage      string `json:"systemMessage"`
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("keine JSON-Ausgabe: %q", out)
	}
	if !strings.Contains(got.SystemMessage, deliver.MsgOtherDevice) || !strings.Contains(got.SystemMessage, "So koppelst du neu") ||
		got.HookSpecificOutput.HookEventName != "SessionStart" || got.HookSpecificOutput.AdditionalContext != got.SystemMessage {
		t.Fatalf("Ausgabe %+v", got)
	}
}

// Ohne Meldung bleibt die Ausgabe des Hooks leer.
func TestSessionStartNoticeEmpty(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	var out bytes.Buffer
	writeSessionNotice(&out, data, cfg)
	if out.Len() != 0 {
		t.Fatalf("Ausgabe %q", out.String())
	}
	writeSessionNotice(&out, t.TempDir(), cfg) // nicht verbunden
	if out.Len() != 0 {
		t.Fatalf("ohne Verbindung: %q", out.String())
	}
}

// Hinweise des Servers: bekannte Anfragen, Anfragen bei einem anderen Benutzer und abgewiesene Vorgeschichte-Tage
// stehen in status und beim Sessionstart, nur als Anzahl. Abgewiesene Tage stehen im Merker und gehen nicht erneut;
// nach einem neuen Koppeln geht die ganze Vorgeschichte wieder mit.
func TestServerNoticesAndRejectedDaysNotResent(t *testing.T) {
	testLock(t)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.mu.Lock()
	f.ingestExtra = map[string]any{
		"known_requests":       1,
		"elsewhere_requests":   2,
		"legacy_rejected_days": []string{"2026-08-14", "2026-08-15"},
	}
	f.mu.Unlock()
	mustConnect(t, cfg, data, f)

	var out bytes.Buffer
	runStatus(nil, &out, data, cfg)
	s := out.String()
	if !strings.Contains(s, "Hinweis: 1 Anfrage war schon auf dem Server") || !strings.Contains(s, "Anfragen lagen schon bei einem anderen Benutzer") ||
		!strings.Contains(s, "Hinweis: 2 Tage aus der Zeit vor dem Koppeln") || strings.Contains(s, "So koppelst du neu") {
		t.Fatalf("status:\n%s", s)
	}
	msg := sessionStartNotice(data, cfg)
	if !strings.Contains(msg, "anderen Benutzer") || !strings.Contains(msg, "2 Tage") || strings.Contains(msg, "So koppelst du neu") {
		t.Fatalf("Sessionstart: %q", msg)
	}
	dir := state.ArchiveDir(data, identityFile(t, cfg).ID)
	b, _ := os.ReadFile(filepath.Join(dir, "geliefert.json"))
	if !strings.Contains(string(b), `"2026-08-14"`) || !strings.Contains(string(b), `"2026-08-15"`) {
		t.Fatalf("geliefert.json: %s", b)
	}

	f.mu.Lock()
	f.ingestExtra = nil
	f.mu.Unlock()
	n := len(f.ingested())
	appendTo(t, filepath.Join(cfg, "projects", "P", "s1.jsonl"), newRequestLine(5))
	code, o, e := syncRun(data, cfg)
	if code != 0 || strings.Contains(o, "Hinweis:") {
		t.Fatalf("sync Exit %d: %s %s", code, o, e)
	}
	if d := legacyDates(f.ingested()[n:]); len(d) != 0 {
		t.Fatalf("Vorgeschichte erneut gesendet: %v", d)
	}
	out.Reset()
	runStatus(nil, &out, data, cfg)
	if strings.Contains(out.String(), "Hinweis:") {
		t.Fatalf("Hinweis nach Lieferung ohne Treffer:\n%s", out.String())
	}

	// Neu koppeln: Der Merker fällt weg, die ganze Vorgeschichte geht wieder mit.
	n = len(f.ingested())
	mustConnect(t, cfg, data, f)
	if d := strings.Join(legacyDates(f.ingested()[n:]), ","); d != "2026-08-14,2026-08-15" {
		t.Fatalf("nach Neukoppeln: %s", d)
	}
}

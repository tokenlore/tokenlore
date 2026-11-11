package deliver

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

func archDir(data string) string { return state.ArchiveDir(data, testIdentity) }

func testPub() string {
	return protocol.EncodeSignPublicKey(testSignKey.Public().(ed25519.PublicKey))
}

// archiveRequests zählt je Anfrage (message_id|request_id) die Hüllen über alle Archivdateien des Rechners, gepackte
// und Nachträge eingeschlossen, und prüft dabei jede Signatur.
func archiveRequests(t *testing.T, data string) map[string]int {
	t.Helper()
	dir := archDir(data)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := map[string]int{}
	for _, e := range entries {
		name := e.Name()
		if name == archive.AccountsFile || name == archive.LegacyFile ||
			!(strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.gz")) {
			continue
		}
		lines, bad, err := archive.ReadFile(filepath.Join(dir, name))
		if err != nil || bad != 0 {
			t.Fatalf("%s: %v, %d kaputt", name, err, bad)
		}
		for _, h := range lines {
			if !protocol.VerifyLine(testPub(), []byte(h.Z), h.S) {
				t.Fatalf("%s: Signatur passt nicht: %s", name, h.Z)
			}
			var l protocol.RequestLine
			if err := json.Unmarshal([]byte(h.Z), &l); err != nil || l.Kind != protocol.KindRequest {
				t.Fatalf("%s: keine Anfrage: %s", name, h.Z)
			}
			n[l.MessageID+"|"+l.RequestID]++
		}
	}
	return n
}

// onceEach prüft, dass jede Anfrage genau einmal im Archiv steht, und gibt die Zahl der Anfragen zurück.
func onceEach(t *testing.T, data string) int {
	t.Helper()
	n := archiveRequests(t, data)
	for k, c := range n {
		if c != 1 {
			t.Errorf("%s steht %d-mal im Archiv", k, c)
		}
	}
	return len(n)
}

func runAt(t *testing.T, cfg, data string, now time.Time) Report {
	t.Helper()
	d := deps(cfg, data, &fakeSender{}, &sleeper{})
	d.Now = func() time.Time { return now }
	rep, err := Run(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func dropMarks(t *testing.T, data string) {
	t.Helper()
	st, err := state.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	st.Files = map[string]state.FileMark{}
	if err := st.Save(data); err != nil {
		t.Fatal(err)
	}
}

func setMtime(t *testing.T, p string, m time.Time) {
	t.Helper()
	if err := os.Chtimes(p, m, m); err != nil {
		t.Fatal(err)
	}
}

// Der Abgleich legt das Archiv an: signierte Hüllen, Konten, Vorgeschichte, Merker archiv_angelegt, Rechte.
// Übergibt die Desktop-App ein Konto, kommen beide Konten mit ihrer Adresse in konten.jsonl: das der App und das aus
// .claude.json.
func TestRunFillsArchiveBothAccounts(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(1)})
	os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"acc-1","emailAddress":"erika@beispiel.de"}}`), 0o600)
	t.Setenv("CLAUDE_CODE_ACCOUNT_UUID", "acc-app")
	t.Setenv("CLAUDE_CODE_USER_EMAIL", "max@beispiel.de")
	runAt(t, cfg, data, fixedNow)

	accs, err := archive.ReadAccounts(archDir(data))
	want := []archive.Account{{AccountID: "acc-1", Email: "erika@beispiel.de"}, {AccountID: "acc-app", Email: "max@beispiel.de"}}
	if err != nil || len(accs) != 2 || accs[0] != want[0] || accs[1] != want[1] {
		t.Fatalf("Konten %+v %v", accs, err)
	}
}

func TestRunFillsArchive(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(3)})
	os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"acc-1","emailAddress":"erika@beispiel.de"}}`), 0o600)
	sc, _ := os.ReadFile("../source/claudecode/testdata/stats-cache.json")
	os.WriteFile(filepath.Join(cfg, "stats-cache.json"), sc, 0o600)
	runAt(t, cfg, data, fixedNow)

	if n := onceEach(t, data); n != 3 {
		t.Fatalf("%d Anfragen im Archiv", n)
	}
	accs, err := archive.ReadAccounts(archDir(data))
	if err != nil || len(accs) != 1 || accs[0] != (archive.Account{AccountID: "acc-1", Email: "erika@beispiel.de"}) {
		t.Fatalf("Konten %+v %v", accs, err)
	}
	legacy, _, err := archive.ReadFile(filepath.Join(archDir(data), archive.LegacyFile))
	if err != nil || len(legacy) != 2 {
		t.Fatalf("Vorgeschichte %d Zeilen, %v", len(legacy), err)
	}
	for _, h := range legacy {
		if !protocol.VerifyLine(testPub(), []byte(h.Z), h.S) || !strings.Contains(h.Z, `"kind":"day"`) {
			t.Fatalf("Vorgeschichte-Zeile %s", h.Z)
		}
	}
	st, _ := state.Load(data)
	if !st.ArchiveCreated {
		t.Fatal("Merker archiv_angelegt fehlt")
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{filepath.Join(data, "archiv"), archDir(data)} {
			if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o700 {
				t.Errorf("%s: %v %v", p, fi.Mode(), err)
			}
		}
		entries, _ := os.ReadDir(archDir(data))
		for _, e := range entries {
			if fi, _ := e.Info(); fi.Mode().Perm() != 0o600 {
				t.Errorf("%s: Rechte %v", e.Name(), fi.Mode().Perm())
			}
		}
	}
}

// Nichts doppelt: dieselbe Anfrage in zwei Läufen, in zwei Protokolldateien (Subagent, Fortsetzung) und nach
// Verwerfen der Lesestände steht je genau einmal im Archiv.
func TestArchiveNoDuplicates(t *testing.T) {
	reqs := manyRequests(4)
	cfg, data := env(t, map[string][]byte{"s1.jsonl": reqs, "s1-fortsetzung.jsonl": reqs})
	sub := filepath.Join(cfg, "projects", "P", "s1", "subagents", "a.jsonl")
	os.MkdirAll(filepath.Dir(sub), 0o700)
	os.WriteFile(sub, reqs, 0o600)
	setMtime(t, sub, fixedNow.Add(-time.Hour))

	runAt(t, cfg, data, fixedNow)
	if n := onceEach(t, data); n != 4 {
		t.Fatalf("erster Lauf: %d Anfragen", n)
	}
	runAt(t, cfg, data, fixedNow)
	dropMarks(t, data)
	runAt(t, cfg, data, fixedNow)
	if n := onceEach(t, data); n != 4 {
		t.Fatalf("nach drei Läufen: %d Anfragen", n)
	}
}

// Archiv gelöscht, Kennung und Schlüssel noch da: Das Archiv entsteht neu, alles Vorhandene steht wieder genau
// einmal darin, status nennt es.
func TestArchiveRecreatedAfterDelete(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(3)})
	runAt(t, cfg, data, fixedNow)
	if loadStatus(t, data).ArchiveRecreated.IsZero() == false {
		t.Fatal("erstes Anlegen als neu angelegt gemeldet")
	}
	if err := os.RemoveAll(filepath.Join(data, "archiv")); err != nil {
		t.Fatal(err)
	}
	runAt(t, cfg, data, fixedNow)
	if n := onceEach(t, data); n != 3 {
		t.Fatalf("%d Anfragen im neuen Archiv", n)
	}
	if !loadStatus(t, data).ArchiveRecreated.Equal(fixedNow) {
		t.Fatal("status nennt das neu angelegte Archiv nicht")
	}
	if _, err := os.Stat(filepath.Join(archDir(data), archive.LegacyFile)); err != nil {
		t.Fatalf("Vorgeschichte nicht neu übernommen: %v", err)
	}
	// Danach wieder ein gewöhnlicher Lauf: nichts doppelt.
	runAt(t, cfg, data, fixedNow)
	if n := onceEach(t, data); n != 3 {
		t.Fatalf("%d Anfragen", n)
	}
}

// Claude Code löscht ein Protokoll: Die Anfragen daraus bleiben im Archiv.
func TestArchiveKeepsDeletedProtocol(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(3)})
	runAt(t, cfg, data, fixedNow)
	before, _ := os.ReadFile(filepath.Join(archDir(data), "2026-10.jsonl"))
	os.Remove(filepath.Join(cfg, "projects", "P", "s1.jsonl"))
	dropMarks(t, data)
	runAt(t, cfg, data, fixedNow)
	after, _ := os.ReadFile(filepath.Join(archDir(data), "2026-10.jsonl"))
	if len(before) == 0 || string(before) != string(after) {
		t.Fatal("Archiv nach dem Löschen des Protokolls verändert")
	}
	if n := onceEach(t, data); n != 3 {
		t.Fatalf("%d Anfragen", n)
	}
}

// Nach dem Packen eines Monats: Dieselben Anfragen noch einmal gelesen stehen weder in der Monatsdatei noch im
// Nachtrag ein zweites Mal.
func TestArchiveNoDuplicatesAfterPacking(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(3)})
	nov := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	runAt(t, cfg, data, nov)
	dir := archDir(data)
	if _, err := os.Stat(filepath.Join(dir, archive.PackedFile("2026-10"))); err != nil {
		t.Fatalf("Oktober nicht gepackt: %v", err)
	}
	dropMarks(t, data)
	runAt(t, cfg, data, nov)
	if _, err := os.Stat(filepath.Join(dir, archive.NachtragFile("2026-10"))); !os.IsNotExist(err) {
		t.Fatal("Nachtrag mit schon gepackten Anfragen angelegt")
	}
	if n := onceEach(t, data); n != 3 {
		t.Fatalf("%d Anfragen", n)
	}
}

// Monatswechsel: Eine zurückgehaltene Anfrage vom 31. um 23:59 UTC in einer ruhenden Datei hält das Packen ihres
// Monats auf; nach 5 Minuten Ruhe kommt sie in die Monatsdatei, und erst dann wird gepackt.
func TestHeldRequestDelaysPacking(t *testing.T) {
	late := `{"type":"assistant","sessionId":"s2","requestId":"req_L","timestamp":"2026-10-31T23:59:00.000Z","message":{"id":"msg_L","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(2), "s2.jsonl": []byte(late)})
	p := filepath.Join(cfg, "projects", "P", "s2.jsonl")
	t0 := time.Date(2026, 11, 1, 0, 1, 0, 0, time.UTC)
	setMtime(t, p, t0.Add(-time.Minute))
	runAt(t, cfg, data, t0)
	dir := archDir(data)
	if _, err := os.Stat(filepath.Join(dir, archive.PackedFile("2026-10"))); !os.IsNotExist(err) {
		t.Fatal("Monat mit zurückgehaltener Anfrage gepackt")
	}
	if archiveRequests(t, data)["msg_L|req_L"] != 0 {
		t.Fatal("unfertige Anfrage archiviert")
	}
	runAt(t, cfg, data, t0.Add(5*time.Minute))
	if _, err := os.Stat(filepath.Join(dir, archive.NachtragFile("2026-10"))); !os.IsNotExist(err) {
		t.Fatal("Anfrage im Nachtrag statt in der Monatsdatei")
	}
	verifyPacked(t, dir, "2026-10")
	if n := onceEach(t, data); n != 3 {
		t.Fatalf("%d Anfragen", n)
	}
}

// Monatswechsel nach dem Packen: Eine Anfrage vom 31. um 23:59 UTC, die erst fertig wird, nachdem der Monat gepackt
// ist, steht im Nachtrag, genau einmal; die .sig passt zur .gz.
func TestLateRequestGoesToNachtrag(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(2)})
	nov := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	runAt(t, cfg, data, nov)
	dir := archDir(data)
	verifyPacked(t, dir, "2026-10")
	late := `{"type":"assistant","sessionId":"s2","requestId":"req_L","timestamp":"2026-10-31T23:59:00.000Z","message":{"id":"msg_L","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
	p := filepath.Join(cfg, "projects", "P", "s2.jsonl")
	os.WriteFile(p, []byte(late), 0o600)
	setMtime(t, p, nov.Add(-time.Hour))
	runAt(t, cfg, data, nov)
	dropMarks(t, data)
	runAt(t, cfg, data, nov)
	lines, _, err := archive.ReadFile(filepath.Join(dir, archive.NachtragFile("2026-10")))
	if err != nil || len(lines) != 1 || !strings.Contains(lines[0].Z, "msg_L") {
		t.Fatalf("Nachtrag %v %v", lines, err)
	}
	verifyPacked(t, dir, "2026-10")
	if n := onceEach(t, data); n != 3 {
		t.Fatalf("%d Anfragen", n)
	}
}

func verifyPacked(t *testing.T, dir, m string) {
	t.Helper()
	gz, err := os.ReadFile(filepath.Join(dir, archive.PackedFile(m)))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(filepath.Join(dir, archive.SigFile(m)))
	if err != nil || !protocol.VerifyMonth(testPub(), gz, strings.TrimSpace(string(sig))) {
		t.Fatalf("%s: .sig passt nicht zur .gz (%v)", m, err)
	}
	if _, err := os.Stat(filepath.Join(dir, archive.MonthFile(m))); !os.IsNotExist(err) {
		t.Fatalf("%s: Monatsdatei nach dem Packen noch da", m)
	}
}

// Keine Archivdatei enthält Inhalt, Pfade, Projekt oder unbekannte Felder; nur die Felder der festen Liste.
func TestArchiveHasNoContentOrPath(t *testing.T) {
	felder, err := os.ReadFile("../source/claudecode/testdata/felder.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	cfg, data := env(t, map[string][]byte{"s9.jsonl": felder})
	runAt(t, cfg, data, fixedNow)
	if n := onceEach(t, data); n == 0 {
		t.Fatal("nichts archiviert")
	}
	allowed := map[string]bool{}
	for _, k := range []string{"kind", "request_id", "message_id", "session_id", "uuid", "parent_uuid", "ts",
		"utc_offset_min", "model", "requested_model", "effort", "entrypoint", "cc_version", "stop_reason", "subagent",
		"tools", "account_id", "usage"} {
		allowed[k] = true
	}
	entries, _ := os.ReadDir(archDir(data))
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(archDir(data), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"GEHEIM", "geheim", "/home/", "zweig", "cwd", "gitBranch", "wireToolInputs",
			"wireIngestContext", "content", "neuesFeld", "projekt", cfg, data, `"P"`, "projects"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s enthält %q", e.Name(), bad)
			}
		}
		if !strings.HasSuffix(e.Name(), ".jsonl") || e.Name() == archive.AccountsFile || e.Name() == archive.LegacyFile {
			continue
		}
		lines, _, _ := archive.ReadFile(filepath.Join(archDir(data), e.Name()))
		for _, h := range lines {
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(h.Z), &m); err != nil {
				t.Fatal(err)
			}
			for k := range m {
				if !allowed[k] {
					t.Errorf("Feld %q nicht in der festen Liste", k)
				}
			}
		}
	}
}

// Ein Fehler beim Archiv bricht nur den Abgleich ab: nichts gesendet, Meldung im Status.
func TestArchiveErrorStopsSync(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(2)})
	if err := os.WriteFile(filepath.Join(data, "archiv"), []byte("kein Ordner"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{}
	_, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err == nil || len(s.bodies) != 0 {
		t.Fatalf("Fehler %v, %d Pakete", err, len(s.bodies))
	}
	if !strings.HasPrefix(loadStatus(t, data).LastError, "Archiv nicht geschrieben") {
		t.Fatalf("Status: %q", loadStatus(t, data).LastError)
	}
}

// Ohne Signaturschlüssel kein Archiv und keine Lieferung.
func TestRunWithoutSignKey(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(2)})
	s := &fakeSender{}
	d := deps(cfg, data, s, &sleeper{})
	d.SignKey = nil
	if _, err := Run(context.Background(), d); err == nil || err.Error() != MsgSignKeyMissing || len(s.bodies) != 0 {
		t.Fatalf("Fehler %v, %d Pakete", err, len(s.bodies))
	}
	if archive.Exists(archDir(data)) {
		t.Fatal("Archiv ohne Schlüssel angelegt")
	}
}

// Der Merker archiv_angelegt und die Lesestände stehen schon nach dem Archivieren fest, auch wenn die Lieferung
// scheitert: Was gelesen ist, steht im Archiv. Ein danach gelöschtes Archiv wird erkannt.
func TestArchiveCreatedKeptWhenDeliveryFails(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(2)})
	down := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.TransportError{Err: errors.New("aus")}
	}}
	if _, err := Run(context.Background(), deps(cfg, data, down, &sleeper{})); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if st, _ := state.Load(data); !st.ArchiveCreated || len(st.Files) != 1 {
		t.Fatalf("Merker %v, Lesestände %v", st.ArchiveCreated, st.Files)
	}
	os.RemoveAll(filepath.Join(data, "archiv"))
	Run(context.Background(), deps(cfg, data, down, &sleeper{}))
	if loadStatus(t, data).ArchiveRecreated.IsZero() {
		t.Fatal("neu angelegtes Archiv nicht gemeldet")
	}
	if n := onceEach(t, data); n != 2 {
		t.Fatalf("%d Anfragen", n)
	}
}

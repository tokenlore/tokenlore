package deliver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

// recorder nimmt jedes Paket an und merkt sich die Inhaltszeilen je Abgleich.
type recorder struct {
	fakeSender
	result func(b protocol.Batch) client.IngestResult
}

func newRecorder() *recorder {
	r := &recorder{}
	r.fn = func(_ int, body []byte) (client.IngestResult, error) {
		var b protocol.Batch
		if err := json.Unmarshal(body, &b); err != nil {
			return client.IngestResult{}, err
		}
		if r.result != nil {
			return r.result(b), nil
		}
		return client.IngestResult{Requests: len(b.Requests)}, nil
	}
	return r
}

// runWith gleicht mit einem eigenen Sender zur Zeit now ab und gibt die gesendeten Inhaltszeilen dieses Laufs zurück.
func runWith(t *testing.T, cfg, data string, now time.Time, s *recorder) []string {
	t.Helper()
	before := len(s.bodies)
	d := deps(cfg, data, s, &sleeper{})
	d.Now = func() time.Time { return now }
	if _, err := Run(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return sentZ(t, s.bodies[before:])
}

// onceIn prüft, dass jede Inhaltszeile in zs höchstens einmal vorkommt, und gibt ihre Zahl zurück.
func onceIn(t *testing.T, zs []string) int {
	t.Helper()
	seen := map[string]bool{}
	for _, z := range zs {
		if seen[z] {
			t.Fatalf("in einer Lieferung doppelt: %s", z)
		}
		seen[z] = true
	}
	return len(seen)
}

// archiveLineCounts zählt die Zeilen jeder nicht leeren Archivdatei (gepackte ungepackt gezählt), mit dem Namen aus
// dem Merker.
func archiveLineCounts(t *testing.T, data string) map[string]int {
	t.Helper()
	srcs, err := archive.Sources(archDir(data))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, s := range srcs {
		lines, err := archive.ReadLines(s.Path)
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) > 0 {
			out[s.Name] = len(lines)
		}
	}
	return out
}

// markerMatchesArchive prüft, dass geliefert.json für jede Archivdatei genau ihre Zeilenzahl nennt.
func markerMatchesArchive(t *testing.T, data string) {
	t.Helper()
	want := archiveLineCounts(t, data)
	got := delivered(t, data).Lines
	if len(got) != len(want) {
		t.Fatalf("Merker %v, Archiv %v", got, want)
	}
	for name, n := range want {
		if got[name] != n {
			t.Fatalf("Merker %s = %d, Archiv hat %d Zeilen (alle: %v / %v)", name, got[name], n, got, want)
		}
	}
}

// Neukoppeln setzt nur den Merker zurück: Die nächste Lieferung enthält das ganze Archiv, jede Zeile genau einmal;
// danach stimmt der Merker wieder, und ein weiterer Lauf sendet nichts.
func TestResetDeliveredSendsArchiveOnce(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(5)})
	s := newRecorder()
	first := runWith(t, cfg, data, fixedNow, s)
	if onceIn(t, first) != 5 {
		t.Fatalf("erste Lieferung %d", len(first))
	}
	markerMatchesArchive(t, data)
	stBefore, _ := os.ReadFile(filepath.Join(data, "state.json"))

	if err := archive.ResetDelivered(archDir(data)); err != nil {
		t.Fatal(err)
	}
	again := runWith(t, cfg, data, fixedNow, s)
	if onceIn(t, again) != 5 || !slices.Equal(again, first) {
		t.Fatalf("nach dem Zurücksetzen %d Zeilen, erwartet dieselben 5", len(again))
	}
	markerMatchesArchive(t, data)
	if stAfter, _ := os.ReadFile(filepath.Join(data, "state.json")); !bytes.Equal(stBefore, stAfter) {
		t.Fatal("Lesestände verändert")
	}
	if more := runWith(t, cfg, data, fixedNow, s); len(more) != 0 {
		t.Fatalf("dritter Lauf sendet %d Zeilen", len(more))
	}
}

// Protokoll gelöscht: Das Archiv behält die Anfragen; nach dem Zurücksetzen des Merkers enthält die Lieferung dieselben.
func TestDeletedProtocolDeliveredFromArchive(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(3)})
	s := newRecorder()
	first := runWith(t, cfg, data, fixedNow, s)
	if err := os.Remove(filepath.Join(cfg, "projects", "P", "s1.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := archive.ResetDelivered(archDir(data)); err != nil {
		t.Fatal(err)
	}
	again := runWith(t, cfg, data, fixedNow, s)
	if onceIn(t, again) != 3 || !slices.Equal(again, first) {
		t.Fatalf("Lieferung nach Löschen %d Zeilen, erwartet dieselben 3", len(again))
	}
}

// Archiv gelöscht: Es entsteht neu, mit ihm ein leerer Merker; die Lieferung enthält jede noch vorhandene Anfrage genau
// einmal, und der Merker stimmt danach.
func TestDeletedArchiveDeliveredOnce(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(4)})
	s := newRecorder()
	first := runWith(t, cfg, data, fixedNow, s)
	if err := os.RemoveAll(filepath.Join(data, "archiv")); err != nil {
		t.Fatal(err)
	}
	again := runWith(t, cfg, data, fixedNow, s)
	if onceIn(t, again) != 4 || !slices.Equal(again, first) {
		t.Fatalf("nach dem Neuanlegen %d Zeilen, erwartet dieselben 4", len(again))
	}
	markerMatchesArchive(t, data)
	if loadStatus(t, data).ArchiveRecreated.IsZero() {
		t.Fatal("Neuanlegen nicht gemeldet")
	}
}

// Packen eines Monats: Der Merker zählt im ungepackten Inhalt und gilt nach dem Packen unverändert; nichts geht doppelt.
// Eine spät fertige Anfrage des gepackten Monats kommt aus dem Nachtrag, genau einmal.
func TestPackingKeepsMarker(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(3)})
	s := newRecorder()
	if n := onceIn(t, runWith(t, cfg, data, fixedNow, s)); n != 3 {
		t.Fatalf("Oktober %d", n)
	}
	before := delivered(t, data).Lines["2026-10.jsonl"]

	nov := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if more := runWith(t, cfg, data, nov, s); len(more) != 0 {
		t.Fatalf("nach dem Packen %d Zeilen erneut gesendet", len(more))
	}
	verifyPacked(t, archDir(data), "2026-10")
	if after := delivered(t, data).Lines["2026-10.jsonl"]; after != before || after != 3 {
		t.Fatalf("Merker vor dem Packen %d, danach %d", before, after)
	}
	markerMatchesArchive(t, data)

	late := `{"type":"assistant","sessionId":"s2","requestId":"req_L","timestamp":"2026-10-31T23:59:00.000Z","message":{"id":"msg_L","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
	p := filepath.Join(cfg, "projects", "P", "s2.jsonl")
	if err := os.WriteFile(p, []byte(late), 0o600); err != nil {
		t.Fatal(err)
	}
	setMtime(t, p, nov.Add(-time.Hour))
	got := runWith(t, cfg, data, nov, s)
	if len(got) != 1 || !strings.Contains(got[0], "msg_L") {
		t.Fatalf("Nachtrag geliefert: %v", got)
	}
	if d := delivered(t, data); d.Lines["2026-10.nachtrag.jsonl"] != 1 || d.Lines["2026-10.jsonl"] != 3 {
		t.Fatalf("Merker %v", d.Lines)
	}
	if more := runWith(t, cfg, data, nov, s); len(more) != 0 {
		t.Fatalf("Nachtrag erneut gesendet: %v", more)
	}

	// Nach dem Zurücksetzen geht alles genau einmal: gepackter Monat und Nachtrag.
	if err := archive.ResetDelivered(archDir(data)); err != nil {
		t.Fatal(err)
	}
	if n := onceIn(t, runWith(t, cfg, data, nov, s)); n != 4 {
		t.Fatalf("nach dem Zurücksetzen %d Zeilen", n)
	}
	markerMatchesArchive(t, data)
}

// Signatur im Plugin: Eine von Hand geänderte Zeile geht nicht mit, die übrigen schon; sie zählt als erledigt und als
// beschädigt, taucht beim nächsten Lauf nicht wieder auf, und der Merker deckt die ganze Datei.
func TestTamperedLineSkipped(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(3)})
	failing := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.ServerError{Status: 500}
	}}
	if _, err := Run(context.Background(), deps(cfg, data, failing, &sleeper{})); err == nil {
		t.Fatal("Fehler erwartet")
	}
	p := filepath.Join(archDir(data), archive.MonthFile("2026-10"))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	lines[1] = strings.Replace(lines[1], `\"output_tokens\":2`, `\"output_tokens\":2000`, 1)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newRecorder()
	d := deps(cfg, data, s, &sleeper{})
	rep, err := Run(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	got := sentZ(t, s.bodies)
	if rep.Damaged != 1 || len(got) != 2 || strings.Contains(strings.Join(got, ""), `"output_tokens":2000`) {
		t.Fatalf("Bericht %+v, gesendet %v", rep, got)
	}
	if dl := delivered(t, data); dl.Damaged != 1 || dl.Lines["2026-10.jsonl"] != 3 {
		t.Fatalf("Merker %+v", dl)
	}
	if !strings.Contains(strings.Join(loadStatus(t, data).Warnings, "\n"), "1 beschädigte Zeile") {
		t.Fatalf("Warnungen %v", loadStatus(t, data).Warnings)
	}
	rep, err = Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err != nil || !rep.NothingNew || rep.Damaged != 0 {
		t.Fatalf("zweiter Lauf %+v %v", rep, err)
	}
	if dl := delivered(t, data); dl.Damaged != 1 {
		t.Fatalf("beschädigt doppelt gezählt: %+v", dl)
	}
}

// Nur beschädigte Zeilen offen: Es gibt nichts zu senden, die Zeilen gelten trotzdem als erledigt.
func TestOnlyDamagedLinesNothingNew(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(1)})
	s := newRecorder()
	runWith(t, cfg, data, fixedNow, s)
	p := filepath.Join(archDir(data), archive.MonthFile("2026-10"))
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"z":"{}","s":"AAAA"}` + "\n" + "kein json\n")
	f.Close()
	sent := len(s.bodies)
	rep, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err != nil || !rep.NothingNew || rep.Damaged != 2 || len(s.bodies) != sent {
		t.Fatalf("Bericht %+v, %v, Rümpfe %d", rep, err, len(s.bodies)-sent)
	}
	if dl := delivered(t, data); dl.Lines["2026-10.jsonl"] != 3 || dl.Damaged != 2 {
		t.Fatalf("Merker %+v", dl)
	}
}

// Zeilen, die mit einem früheren Signaturschlüssel signiert sind, gehen mit; Zeilen mit einem unbekannten Schlüssel
// werden ausgesondert, weil der Server sonst die ganze Lieferung abwiese.
func TestPreviousKeyAcceptedUnknownKeySkipped(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(1)})
	s := newRecorder()
	runWith(t, cfg, data, fixedNow, s)
	oldKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	foreign := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	z := func(msg string) []byte {
		return []byte(`{"kind":"request","request_id":"r` + msg + `","message_id":"m` + msg + `","session_id":"s1","ts":"2026-10-20T10:00:00Z","utc_offset_min":0,"model":"claude-opus-5-5","subagent":false,"usage":{"input_tokens":1}}`)
	}
	alt, _ := protocol.SealLine(oldKey, z("alt"))
	fremd, _ := protocol.SealLine(foreign, z("fremd"))
	p := filepath.Join(archDir(data), archive.MonthFile("2026-10"))
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.Write(append(append(alt, '\n'), append(fremd, '\n')...))
	f.Close()
	st, _ := state.Load(data)
	st.SignPublicKeysPrevious = []string{protocol.EncodeSignPublicKey(oldKey.Public().(ed25519.PublicKey))}
	if err := st.Save(data); err != nil {
		t.Fatal(err)
	}
	got := runWith(t, cfg, data, fixedNow, s)
	if len(got) != 1 || !strings.Contains(got[0], "malt") {
		t.Fatalf("gesendet %v", got)
	}
	if dl := delivered(t, data); dl.Damaged != 1 {
		t.Fatalf("Merker %+v", dl)
	}
}

// Sitzungen werden aus den Inhaltszeilen abgeleitet: je Sitzung einmal, Version und Konto aus der frühesten Anfrage,
// ohne Signatur; sie stehen im Paket ihrer ersten Anfrage.
func TestSessionsDerivedFromLines(t *testing.T) {
	lines := `{"type":"assistant","sessionId":"s1","requestId":"r2","timestamp":"2026-10-23T22:00:00.000Z","version":"2.2.0","message":{"id":"m2","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}
{"type":"user","sessionId":"s1","ownerAccountUuid":"acc-1"}
{"type":"assistant","sessionId":"s1","requestId":"r1","timestamp":"2026-10-23T21:00:00.000Z","version":"2.1.0","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}
{"type":"assistant","sessionId":"s2","requestId":"r3","timestamp":"2026-10-23T21:30:00.000Z","message":{"id":"m3","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}
`
	cfg, data := env(t, map[string][]byte{"s1.jsonl": []byte(lines)})
	s := newRecorder()
	runWith(t, cfg, data, fixedNow, s)
	var b protocol.Batch
	if err := json.Unmarshal(s.bodies[0], &b); err != nil {
		t.Fatal(err)
	}
	got := map[string]protocol.Session{}
	for _, ss := range b.Sessions {
		if _, dup := got[ss.SessionID]; dup {
			t.Fatalf("Sitzung doppelt: %+v", b.Sessions)
		}
		got[ss.SessionID] = ss
	}
	if len(got) != 2 || got["s1"].CCVersion != "2.1.0" || got["s1"].AccountID != "acc-1" || got["s2"].CCVersion != "" {
		t.Fatalf("Sitzungen %+v", b.Sessions)
	}
	var raw map[string]json.RawMessage
	json.Unmarshal(s.bodies[0], &raw)
	if bytes.Contains(raw["sessions"], []byte(`"s"`)) || bytes.Contains(raw["sessions"], []byte(`"z"`)) {
		t.Fatalf("Sitzung mit Hülle: %s", raw["sessions"])
	}
}

// Konto und E-Mail: Die Kontenliste kommt aus konten.jsonl; die Stufe entscheidet, was von der Adresse mitgeht.
// Die volle Adresse bleibt im Archiv. Eine Zeile ohne ownerAccountUuid trägt das angemeldete Konto.
func TestAccountsFromKontenWithEmailMode(t *testing.T) {
	for mode, want := range map[string]string{protocol.EmailOff: "", protocol.EmailShort: "ma…@beispiel.de", protocol.EmailFull: "max@beispiel.de"} {
		t.Run(mode, func(t *testing.T) {
			line := `{"type":"assistant","sessionId":"s9","requestId":"r1","timestamp":"2026-10-23T21:00:00.000Z","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
			cfg, data := env(t, map[string][]byte{"s9.jsonl": []byte(line)})
			if err := os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"acc-jetzt","emailAddress":"max@beispiel.de"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			st, _ := state.Load(data)
			st.EmailMode = mode
			st.Save(data)
			s := newRecorder()
			got := runWith(t, cfg, data, fixedNow, s)
			if len(got) != 1 || !strings.Contains(got[0], `"account_id":"acc-jetzt"`) {
				t.Fatalf("Zeile %v", got)
			}
			var b protocol.Batch
			json.Unmarshal(s.bodies[0], &b)
			if len(b.Accounts) != 1 || b.Accounts[0].AccountID != "acc-jetzt" || b.Accounts[0].EmailMode != mode || b.Accounts[0].Email != want {
				t.Fatalf("Konten %+v", b.Accounts)
			}
			accs, _ := archive.ReadAccounts(archDir(data))
			if len(accs) != 1 || accs[0].Email != "max@beispiel.de" {
				t.Fatalf("konten.jsonl %+v", accs)
			}
		})
	}
}

// Eine zu lange Adresse geht nicht mit; das Konto schon.
func TestAccountEmailTooLongDropped(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("a", 300) + "@b.de"
	if err := os.WriteFile(filepath.Join(dir, archive.AccountsFile), []byte(`{"account_id":"acc-1","email":"`+long+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	accs, err := deliveryAccounts(dir, protocol.EmailFull)
	if err != nil || len(accs) != 1 || accs[0].Email != "" || protocol.ValidateAccount(accs[0]) != nil {
		t.Fatalf("Konten %+v %v", accs, err)
	}
}

// Abgewiesene Vorgeschichte-Tage stehen im Merker und im Hinweis; geschickt werden sie wie alles andere nur einmal,
// nach dem Zurücksetzen wieder. Bereits bekannte Anfragen meldet der Hinweis mit Anzahl.
func TestRejectedDaysAndKnownRequests(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(2)})
	sc, err := os.ReadFile("../source/claudecode/testdata/stats-cache.json")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(cfg, "stats-cache.json"), sc, 0o600)
	s := newRecorder()
	s.result = func(b protocol.Batch) client.IngestResult {
		r := client.IngestResult{Requests: len(b.Requests), KnownRequests: len(b.Requests)}
		if len(b.LegacyDays) > 0 {
			r.LegacyRejectedDays = []string{"2026-08-14", "2026-01-01"} // 2026-01-01 stand nicht im Paket
		}
		return r
	}
	rep, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Known != 2 || rep.LegacyRejected != 1 || rep.Days != 2 {
		t.Fatalf("Bericht %+v", rep)
	}
	if dl := delivered(t, data); !slices.Equal(dl.RejectedDays, []string{"2026-08-14"}) {
		t.Fatalf("Merker %+v", dl)
	}
	stat := loadStatus(t, data)
	notices := strings.Join(Notices(stat), "\n")
	if !strings.Contains(notices, "2 Anfragen waren schon auf dem Server") || !strings.Contains(notices, "1 Tag aus der Zeit vor dem Koppeln") {
		t.Fatalf("Hinweise %q", notices)
	}
	archive.ResetDelivered(archDir(data))
	if got := runWith(t, cfg, data, fixedNow, s); len(got) != 4 {
		t.Fatalf("nach dem Zurücksetzen %d Zeilen", len(got))
	}
}

// 426: Die Lieferung scheitert, der Fehler bleibt als Status stehen, der Merker rückt nicht vor; die Daten warten
// im Archiv und gehen nach dem Aktualisieren alle raus.
func TestOutdatedPluginDataWaits(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(3)})
	outdated := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.OutdatedError{Min: "9.0.0"}
	}}
	for range 2 {
		if _, err := Run(context.Background(), deps(cfg, data, outdated, &sleeper{})); err == nil {
			t.Fatal("Fehler erwartet")
		}
		if stat := loadStatus(t, data); stat.LastError != OutdatedMessage("9.0.0") || !stat.LastSuccess.IsZero() {
			t.Fatalf("Status %+v", stat)
		}
		if _, err := os.Stat(filepath.Join(archDir(data), archive.DeliveredFile)); !os.IsNotExist(err) {
			t.Fatal("Merker trotz 426 geschrieben")
		}
	}
	s := newRecorder()
	if got := runWith(t, cfg, data, fixedNow, s); onceIn(t, got) != 3 {
		t.Fatalf("nach dem Aktualisieren %d Zeilen", len(got))
	}
}

// Geliefert wird nur aus dem eigenen Archivordner; der Ordner eines anderen Rechners bleibt unberührt.
func TestForeignArchiveFolderIgnored(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(1)})
	other := state.ArchiveDir(data, strings.Repeat("f", 32))
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	env, _ := protocol.SealLine(testSignKey, []byte(`{"kind":"request","request_id":"rx","message_id":"mx","session_id":"sx","ts":"2026-10-20T10:00:00Z","utc_offset_min":0,"model":"m","subagent":false,"usage":{}}`))
	os.WriteFile(filepath.Join(other, archive.MonthFile("2026-10")), append(env, '\n'), 0o600)
	s := newRecorder()
	got := runWith(t, cfg, data, fixedNow, s)
	if len(got) != 1 || strings.Contains(got[0], "mx") {
		t.Fatalf("gesendet %v", got)
	}
	if _, err := os.Stat(filepath.Join(other, archive.DeliveredFile)); !os.IsNotExist(err) {
		t.Fatal("fremder Ordner bekam einen Merker")
	}
}

// Ein unlesbarer Merker: Das Archiv geht noch einmal ganz, mit Warnung, und der Merker stimmt danach wieder.
func TestBrokenMarkerResendsAll(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(2)})
	s := newRecorder()
	runWith(t, cfg, data, fixedNow, s)
	os.WriteFile(filepath.Join(archDir(data), archive.DeliveredFile), []byte("{kaputt"), 0o600)
	if got := runWith(t, cfg, data, fixedNow, s); onceIn(t, got) != 2 {
		t.Fatalf("%d Zeilen", len(got))
	}
	if !strings.Contains(strings.Join(loadStatus(t, data).Warnings, "\n"), "unlesbar") {
		t.Fatal("keine Warnung")
	}
	markerMatchesArchive(t, data)
}

// Die eigene Prüfung ist so streng wie der Server: unbekannter Schlüssel in der Hülle oder der Inhaltszeile, Text nach
// dem Objekt oder die falsche Art (Tag in einer Monatsdatei) gelten als beschädigt.
func TestCheckLineStrict(t *testing.T) {
	keys := []string{testPub()}
	z := `{"kind":"request","request_id":"r","message_id":"m","session_id":"s","ts":"2026-10-20T10:00:00Z","utc_offset_min":0,"model":"m","subagent":false,"usage":{}}`
	good, _ := protocol.SealLine(testSignKey, []byte(z))
	if _, v := checkLine(good, false, keys, nil, fixedNow); v != lineOK {
		t.Fatal("gültige Zeile abgelehnt")
	}
	extraZ, _ := protocol.SealLine(testSignKey, []byte(strings.Replace(z, `"usage"`, `"cwd":"/x","usage"`, 1)))
	day, _ := protocol.SealLine(testSignKey, []byte(`{"kind":"day","date":"2026-08-14","models":{"m":1}}`))
	for name, raw := range map[string][]byte{
		"Schlüssel in der Hülle":        bytes.Replace(good, []byte(`{"z"`), []byte(`{"x":1,"z"`), 1),
		"Schlüssel in der Inhaltszeile": extraZ,
		"Text nach dem Objekt":          append(append([]byte{}, good...), []byte(` {}`)...),
		"Tag statt Anfrage":             day,
	} {
		if _, v := checkLine(raw, false, keys, nil, fixedNow); v != lineDamaged {
			t.Errorf("%s: angenommen", name)
		}
	}
	if _, v := checkLine(day, true, keys, nil, fixedNow); v != lineOK {
		t.Error("gültiger Tag abgelehnt")
	}
}

// Kontowechsel nach dem Sitzungsstart, bevor die erste Anfrage archiviert ist: Die Sitzung bleibt beim Startkonto,
// auch für spätere Anfragen. Fehlt sitzungen.jsonl, gilt wie zuvor das angemeldete Konto.
func TestAccountChangeAfterSessionStart(t *testing.T) {
	line := func(n string) string {
		return `{"type":"assistant","sessionId":"s9","requestId":"r` + n + `","timestamp":"2026-10-23T21:0` + n + `:00.000Z","message":{"id":"m` + n + `","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
	}
	for name, recorded := range map[string]bool{"mit Startkonto": true, "ohne Datei": false} {
		t.Run(name, func(t *testing.T) {
			cfg, data := env(t, map[string][]byte{"s9.jsonl": []byte(line("1"))})
			if recorded {
				if err := archive.RecordSessionStart(archDir(data), "s9", "acc-start", fixedNow.Add(-2*time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			// Danach meldet sich ein anderes Konto an.
			os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"acc-neu"}}`), 0o600)
			s := newRecorder()
			got := runWith(t, cfg, data, fixedNow, s)
			p := filepath.Join(cfg, "projects", "P", "s9.jsonl")
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
			f.WriteString(line("2"))
			f.Close()
			setMtime(t, p, fixedNow.Add(-time.Hour))
			got = append(got, runWith(t, cfg, data, fixedNow, s)...)
			want := "acc-neu"
			if recorded {
				want = "acc-start"
			}
			if len(got) != 2 {
				t.Fatalf("%d Zeilen", len(got))
			}
			for _, z := range got {
				if !strings.Contains(z, `"account_id":"`+want+`"`) {
					t.Fatalf("Konto nicht %s: %s", want, z)
				}
			}
		})
	}
}

// Archiv gelöscht, danach startet eine Sitzung (sitzungen.jsonl entsteht im leeren Ordner): Das Archiv gilt trotzdem
// als fehlend, alles Vorhandene kommt neu hinein, und die Sitzung behält ihr Startkonto.
func TestDeletedArchiveWithSessionStartOnly(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(2)})
	s := newRecorder()
	runWith(t, cfg, data, fixedNow, s)
	os.RemoveAll(filepath.Join(data, "archiv"))
	if err := archive.RecordSessionStart(archDir(data), "s-neu", "acc-start", fixedNow); err != nil {
		t.Fatal(err)
	}
	if got := runWith(t, cfg, data, fixedNow, s); onceIn(t, got) != 2 {
		t.Fatalf("%d Zeilen nach dem Neuanlegen", len(got))
	}
	if loadStatus(t, data).ArchiveRecreated.IsZero() {
		t.Fatal("Neuanlegen nicht gemeldet")
	}
}

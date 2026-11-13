package archive

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
)

var nov6 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	return ed25519.NewKeyFromSeed(seed)
}

func pubKey(t *testing.T) string {
	return protocol.EncodeSignPublicKey(testKey(t).Public().(ed25519.PublicKey))
}

func open(t *testing.T, dir string, now time.Time) *Archive {
	t.Helper()
	a, err := Open(dir, testKey(t), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func newDir(t *testing.T) string {
	return filepath.Join(t.TempDir(), "archiv", "rechner-0123456789abcdef0123456789abcdef")
}

// req baut eine gültige Inhaltszeile mit output_tokens out.
func req(msg, reqID, ts string, out int) protocol.RequestLine {
	tm, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		panic(err)
	}
	return protocol.RequestLine{Kind: protocol.KindRequest, RequestID: reqID, MessageID: msg, SessionID: "s1",
		TS: tm.UTC(), UTCOffsetMin: 60, Model: "claude-opus-5-5", AccountID: "acc-1",
		Usage: map[string]any{"input_tokens": json.Number("1"), "output_tokens": json.Number(itoa(out))}}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func add(t *testing.T, a *Archive, lines ...protocol.RequestLine) AddResult {
	t.Helper()
	for _, l := range lines {
		if err := protocol.ValidateRequestLine(l, nov6); err != nil {
			t.Fatalf("Testzeile ungültig: %v", err)
		}
	}
	r, err := a.AddRequests(lines, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// readAll liest die Hüllen einer Datei und prüft jede Signatur.
func readAll(t *testing.T, p string) []protocol.Envelope {
	t.Helper()
	lines, bad, err := ReadFile(p)
	if err != nil || bad != 0 {
		t.Fatalf("%s: err %v, kaputt %d", p, err, bad)
	}
	for _, h := range lines {
		if !protocol.VerifyLine(pubKey(t), []byte(h.Z), h.S) {
			t.Fatalf("Signatur passt nicht: %s", h.Z)
		}
	}
	return lines
}

// countKey zählt die Hüllen mit dieser Anfrage über alle Archivdateien (auch gepackte und Nachträge).
func countKey(t *testing.T, dir, msg, reqID string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if !(strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.gz")) || name == AccountsFile || name == LegacyFile {
			continue
		}
		for _, h := range readAll(t, filepath.Join(dir, name)) {
			var k requestKey
			if err := json.Unmarshal([]byte(h.Z), &k); err != nil {
				t.Fatal(err)
			}
			if k.MessageID == msg && k.RequestID == reqID {
				n++
			}
		}
	}
	return n
}

func TestAddRequestsSignedByUTCMonth(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	// 31.10. 23:30 UTC ist in Ortszeit +60 schon November; der Monat richtet sich nach UTC.
	r := add(t, a, req("msg_1", "req_1", "2026-10-31T23:30:00Z", 5), req("msg_2", "req_2", "2026-11-01T00:30:00Z", 5))
	if r.Added != 2 || r.Nachtrag != 0 || r.Late != 0 {
		t.Fatalf("Ergebnis %+v", r)
	}
	oct := readAll(t, filepath.Join(dir, "2026-10.jsonl"))
	nov := readAll(t, filepath.Join(dir, "2026-11.jsonl"))
	if len(oct) != 1 || len(nov) != 1 || !strings.Contains(oct[0].Z, `"message_id":"msg_1"`) {
		t.Fatalf("Oktober %v, November %v", oct, nov)
	}
	// Die Inhaltszeile ist genau MarshalLine der Zeile.
	want, _ := protocol.MarshalLine(req("msg_1", "req_1", "2026-10-31T23:30:00Z", 5))
	if oct[0].Z != string(want) {
		t.Fatalf("z %s, erwartet %s", oct[0].Z, want)
	}
	if runtime.GOOS != "windows" {
		for p, perm := range map[string]os.FileMode{dir: 0o700, filepath.Dir(dir): 0o700, filepath.Join(dir, "2026-10.jsonl"): 0o600} {
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != perm {
				t.Errorf("%s: Rechte %v, erwartet %v", p, fi.Mode().Perm(), perm)
			}
		}
	}
}

// Dieselbe Anfrage zweimal im selben Aufruf, in zwei Läufen und mit leicht anderem Zeitpunkt über die Monatsgrenze
// hinweg (zweite Protokolldatei) steht genau einmal im Archiv.
func TestAddRequestsDedup(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	l := req("msg_1", "req_1", "2026-10-31T23:59:59Z", 5)
	if r := add(t, a, l, l); r.Added != 1 {
		t.Fatalf("im selben Aufruf: %+v", r)
	}
	a = open(t, dir, nov6)
	if r := add(t, a, l); r.Added != 0 || r.Late != 0 {
		t.Fatalf("zweiter Lauf: %+v", r)
	}
	a = open(t, dir, nov6)
	other := req("msg_1", "req_1", "2026-11-01T00:00:01Z", 5)
	if r := add(t, a, other); r.Added != 0 {
		t.Fatalf("über die Monatsgrenze: %+v", r)
	}
	// Ohne requestId ist message.id allein der Schlüssel; mit anderer requestId ist es eine andere Anfrage.
	if r := add(t, a, req("msg_1", "req_2", "2026-10-31T23:59:59Z", 5)); r.Added != 1 {
		t.Fatalf("andere requestId: %+v", r)
	}
	if n := countKey(t, dir, "msg_1", "req_1"); n != 1 {
		t.Fatalf("%d Zeilen für msg_1|req_1", n)
	}
}

// Kommen nach dem Archivieren Zeilen mit anderen Zahlen, bleibt die Archivzeile und Late zählt den Fall.
func TestAddRequestsLateKeepsLine(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	add(t, a, req("msg_1", "req_1", "2026-11-02T10:00:00Z", 5))
	before, _ := os.ReadFile(filepath.Join(dir, "2026-11.jsonl"))
	a = open(t, dir, nov6)
	if r := add(t, a, req("msg_1", "req_1", "2026-11-02T10:00:00Z", 9)); r.Added != 0 || r.Late != 1 {
		t.Fatalf("Ergebnis %+v", r)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "2026-11.jsonl"))
	if !bytes.Equal(before, after) {
		t.Fatal("Archivzeile verändert")
	}
}

func gunzip(t *testing.T, p string) []byte {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Monatswechsel: Ein abgeschlossener Monat wird gepackt und signiert; eine Anfrage vom 31. um 23:59 UTC, die erst
// danach fertig wird, steht im Nachtrag, und zwar genau einmal.
func TestPackAndNachtrag(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	add(t, a, req("msg_1", "req_1", "2026-09-10T10:00:00Z", 5), req("msg_2", "req_2", "2026-10-30T10:00:00Z", 5),
		req("msg_3", "req_3", "2026-11-01T10:00:00Z", 5))
	raw, _ := os.ReadFile(filepath.Join(dir, "2026-10.jsonl"))
	// September hat noch eine zurückgehaltene Anfrage, der November läuft noch.
	packed, err := a.Pack([]string{"2026-09"})
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) != 1 || packed[0] != "2026-10" {
		t.Fatalf("gepackt %v", packed)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-10.jsonl")); !os.IsNotExist(err) {
		t.Fatal("Monatsdatei nach dem Packen noch da")
	}
	for _, m := range []string{"2026-09", "2026-11"} {
		if _, err := os.Stat(filepath.Join(dir, MonthFile(m))); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if got := gunzip(t, filepath.Join(dir, PackedFile("2026-10"))); !bytes.Equal(got, raw) {
		t.Fatal("gepackter Inhalt weicht ab")
	}
	gz, _ := os.ReadFile(filepath.Join(dir, PackedFile("2026-10")))
	sig, _ := os.ReadFile(filepath.Join(dir, SigFile("2026-10")))
	if !strings.HasSuffix(string(sig), "\n") || !protocol.VerifyMonth(pubKey(t), gz, strings.TrimSpace(string(sig))) {
		t.Fatalf("Signatur %q passt nicht zur .gz", sig)
	}

	late := req("msg_4", "req_4", "2026-10-31T23:59:00Z", 5)
	for run := range 3 {
		a = open(t, dir, nov6)
		r := add(t, a, late, req("msg_2", "req_2", "2026-10-30T10:00:00Z", 5))
		if want := map[bool]int{true: 1, false: 0}[run == 0]; r.Added != want || r.Nachtrag != want {
			t.Fatalf("Lauf %d: %+v", run, r)
		}
	}
	if n := len(readAll(t, filepath.Join(dir, NachtragFile("2026-10")))); n != 1 {
		t.Fatalf("Nachtrag hat %d Zeilen", n)
	}
	for _, k := range [][2]string{{"msg_2", "req_2"}, {"msg_4", "req_4"}} {
		if n := countKey(t, dir, k[0], k[1]); n != 1 {
			t.Fatalf("%s: %d Zeilen", k[0], n)
		}
	}
	// Der Nachtrag wird nie gepackt, ein gepackter Monat nicht erneut.
	a = open(t, dir, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	packed, err = a.Pack(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(packed, ",") != "2026-09,2026-11" {
		t.Fatalf("gepackt %v", packed)
	}
	if _, err := os.Stat(filepath.Join(dir, NachtragFile("2026-10"))); err != nil {
		t.Fatal(err)
	}
}

// Ein Abbruch hinterlässt eine halbe Zeile, eine .gz ohne Signatur und eine liegengebliebene Monatsdatei: Open räumt
// auf, und die abgeschnittene Anfrage wird neu geschrieben.
func TestOpenRepairs(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	add(t, a, req("msg_1", "req_1", "2026-11-02T10:00:00Z", 5), req("msg_9", "req_9", "2026-09-02T10:00:00Z", 5))
	if _, err := a.Pack(nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "2026-11.jsonl")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, b[:len(b)-10], 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, SigFile("2026-09")))
	os.WriteFile(filepath.Join(dir, MonthFile("2026-09")), []byte("rest\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "2026-11.jsonl.123.tmp"), []byte("x"), 0o600)

	a = open(t, dir, nov6)
	if fi, _ := os.Stat(p); fi.Size() != 0 {
		t.Fatalf("halbe Zeile nicht abgeschnitten: %d Byte", fi.Size())
	}
	gz, _ := os.ReadFile(filepath.Join(dir, PackedFile("2026-09")))
	sig, err := os.ReadFile(filepath.Join(dir, SigFile("2026-09")))
	if err != nil || !protocol.VerifyMonth(pubKey(t), gz, strings.TrimSpace(string(sig))) {
		t.Fatalf("Signatur nicht nachgeholt: %v", err)
	}
	for _, n := range []string{MonthFile("2026-09"), "2026-11.jsonl.123.tmp"} {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("%s nicht entfernt", n)
		}
	}
	if r := add(t, a, req("msg_1", "req_1", "2026-11-02T10:00:00Z", 5)); r.Added != 1 {
		t.Fatalf("abgeschnittene Anfrage nicht neu geschrieben: %+v", r)
	}
	if n := len(readAll(t, p)); n != 1 {
		t.Fatalf("%d Zeilen", n)
	}
}

func TestTruncatePartialKeepsCompleteFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	big := strings.Repeat("a", 70000) + "\n" + strings.Repeat("b", 70000)
	os.WriteFile(p, []byte(big), 0o600)
	if err := truncatePartial(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if len(b) != 70001 {
		t.Fatalf("Länge %d", len(b))
	}
	if err := truncatePartial(p); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(p); len(b2) != 70001 {
		t.Fatal("vollständige Datei verändert")
	}
}

func day(date string, tokens int64) protocol.DayLine {
	return protocol.DayLine{Kind: protocol.KindDay, Date: date, Models: map[string]int64{"claude-opus-5-5": tokens},
		Activity: &protocol.DayActivity{Messages: 1, Sessions: 1}, CompleteBefore: "2026-11-01"}
}

// Vorgeschichte: nur Tage ohne Einzelanfrage (Ortsdatum), einmal, ungültige Tage fallen weg.
func TestAddLegacyOnce(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	// 31.10. 23:30 UTC mit +60 ist der 1.11. Ortszeit: Der 1.11. hat eine Anfrage, der 31.10. nicht.
	add(t, a, req("msg_1", "req_1", "2026-10-31T23:30:00Z", 5))
	if _, err := a.Pack(nil); err != nil {
		t.Fatal(err)
	}
	add(t, a, req("msg_2", "req_2", "2026-10-20T10:00:00Z", 5)) // Nachtrag zählt auch
	if a.LegacyDone() {
		t.Fatal("Vorgeschichte schon übernommen")
	}
	w, dropped, err := a.AddLegacy([]protocol.DayLine{day("2026-10-31", 5), day("2026-11-01", 7), day("2026-10-20", 1),
		day("2023-01-01", 1)})
	if err != nil {
		t.Fatal(err)
	}
	if w != 1 || dropped != 1 {
		t.Fatalf("geschrieben %d, ausgelassen %d", w, dropped)
	}
	lines := readAll(t, filepath.Join(dir, LegacyFile))
	if len(lines) != 1 || !strings.Contains(lines[0].Z, `"date":"2026-10-31"`) {
		t.Fatalf("Vorgeschichte %v", lines)
	}
	if w, _, _ := a.AddLegacy([]protocol.DayLine{day("2026-10-15", 3)}); w != 0 || len(readAll(t, filepath.Join(dir, LegacyFile))) != 1 {
		t.Fatal("Vorgeschichte ein zweites Mal übernommen")
	}
	// Ohne Tage entsteht die Datei trotzdem.
	dir2 := newDir(t)
	a2 := open(t, dir2, nov6)
	if _, _, err := a2.AddLegacy(nil); err != nil || !a2.LegacyDone() {
		t.Fatalf("leere Vorgeschichte: %v", err)
	}
}

func TestAddAccounts(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	if err := a.AddAccounts([]string{"acc-2", "acc-1", "acc-1", "kein konto"}, "acc-1", "erika@beispiel.de"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddAccounts([]string{"acc-2"}, "acc-1", "erika@beispiel.de"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAccounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Account{{"acc-1", "erika@beispiel.de"}, {"acc-2", ""}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Konten %+v", got)
	}
	if err := a.AddAccounts(nil, "acc-1", "neu@beispiel.de"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddAccounts(nil, "acc-2", "kaputt\x01@beispiel.de"); err != nil {
		t.Fatal(err)
	}
	got, _ = ReadAccounts(dir)
	if got[0].Email != "neu@beispiel.de" || got[1].Email != "" {
		t.Fatalf("Konten %+v", got)
	}
	b, _ := os.ReadFile(filepath.Join(dir, AccountsFile))
	if strings.Count(string(b), "\n") != 2 {
		t.Fatalf("konten.jsonl:\n%s", b)
	}
}

func TestOpenWithoutKey(t *testing.T) {
	if _, err := Open(newDir(t), nil, nil); err == nil {
		t.Fatal("ohne Schlüssel geöffnet")
	}
}

// Der Schlüssel ist message_id und request_id zusammen: Gleiche request_id mit anderer message_id und gleiche
// message_id mit anderer request_id sind je eigene Anfragen; nur beide gleich ist dieselbe.
func TestAddRequestsKeyUsesBothIDs(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	ts := "2026-11-02T10:00:00Z"
	r := add(t, a, req("msg_1", "req_1", ts, 5), req("msg_2", "req_1", ts, 5), req("msg_1", "req_2", ts, 5))
	if r.Added != 3 || r.Late != 0 {
		t.Fatalf("erster Aufruf: %+v", r)
	}
	a = open(t, dir, nov6)
	if r := add(t, a, req("msg_3", "req_1", ts, 5), req("msg_1", "req_3", ts, 5), req("msg_2", "req_1", ts, 5)); r.Added != 2 || r.Late != 0 {
		t.Fatalf("zweiter Lauf: %+v", r)
	}
	for _, k := range [][2]string{{"msg_1", "req_1"}, {"msg_2", "req_1"}, {"msg_1", "req_2"}, {"msg_3", "req_1"}, {"msg_1", "req_3"}} {
		if n := countKey(t, dir, k[0], k[1]); n != 1 {
			t.Errorf("%s|%s: %d Zeilen", k[0], k[1], n)
		}
	}
}

// Nachbarmonat nach vorn: Die zuerst archivierte Kopie liegt kurz nach der Monatsgrenze, die zweite Kopie (andere
// Protokolldatei) kurz davor; sie steht trotzdem nur einmal im Archiv, auch nach dem Packen des früheren Monats.
func TestAddRequestsDedupForwardNeighbour(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	if r := add(t, a, req("msg_1", "req_1", "2026-11-01T00:00:10Z", 5)); r.Added != 1 {
		t.Fatalf("erste Kopie: %+v", r)
	}
	a = open(t, dir, nov6)
	if r := add(t, a, req("msg_1", "req_1", "2026-10-31T23:59:50Z", 5)); r.Added != 0 {
		t.Fatalf("zweite Kopie vor der Grenze: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, MonthFile("2026-10"))); !os.IsNotExist(err) {
		t.Fatal("Monatsdatei Oktober für eine schon archivierte Anfrage angelegt")
	}
	if n := countKey(t, dir, "msg_1", "req_1"); n != 1 {
		t.Fatalf("%d Zeilen", n)
	}
}

// Eine Zeile ohne Konto bekommt das Konto, das ihre Sitzung im Archiv schon hat (auch aus dem Vormonat und aus
// einem früheren Lauf), sonst das angemeldete Konto; ohne beides bleibt sie ohne Konto.
func TestAddRequestsAccountFromSessionOrFallback(t *testing.T) {
	dir := newDir(t)
	withSession := func(l protocol.RequestLine, sid, acc string) protocol.RequestLine {
		l.SessionID, l.AccountID = sid, acc
		return l
	}
	a := open(t, dir, nov6)
	add(t, a, withSession(req("m1", "r1", "2026-11-01T10:00:00Z", 1), "s1", "acc-1"))
	// neuer Lauf: Index leer, das Konto kommt aus der Datei
	a = open(t, dir, time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC))
	lines := []protocol.RequestLine{
		withSession(req("m2", "r2", "2026-11-02T10:00:00Z", 1), "s1", ""),
		withSession(req("m3", "r3", "2026-11-02T11:00:00Z", 1), "s2", ""),
		withSession(req("m4", "r4", "2026-12-01T10:00:00Z", 1), "s1", ""), // Vormonat
	}
	if _, err := a.AddRequests(lines, nil, "acc-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddRequests([]protocol.RequestLine{withSession(req("m5", "r5", "2026-11-03T10:00:00Z", 1), "s3", "")}, nil, ""); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range []string{"2026-11", "2026-12"} {
		for _, h := range readAll(t, filepath.Join(dir, MonthFile(m))) {
			var k requestKey
			if err := json.Unmarshal([]byte(h.Z), &k); err != nil {
				t.Fatal(err)
			}
			got[k.MessageID] = k.AccountID
		}
	}
	want := map[string]string{"m1": "acc-1", "m2": "acc-1", "m3": "acc-2", "m4": "acc-1", "m5": ""}
	for m, acc := range want {
		if got[m] != acc {
			t.Errorf("%s: Konto %q, want %q", m, got[m], acc)
		}
	}
}

// Konto vom Sitzungsstart: Wechselt das angemeldete Konto nach dem Start, bevor die erste Anfrage archiviert ist,
// bleibt die Sitzung beim Startkonto. Das Startkonto geht ownerAccountUuid vor (Desktop-App: die Sitzungsdatei nennt
// teils das Konto aus .claude.json); ohne Startkonto gilt ownerAccountUuid, ohne beides das Konto der Sitzung.
// Hat die Sitzung schon ein Konto im Archiv, gilt es für jede weitere Zeile, auch gegen ein anderes ownerAccountUuid.
func TestAddRequestsAccountFromSessionStart(t *testing.T) {
	dir := newDir(t)
	if err := RecordSessionStart(dir, "s-start", "acc-start", nov6); err != nil {
		t.Fatal(err)
	}
	if err := RecordSessionStart(dir, "s-start", "acc-spaeter", nov6); err != nil { // zweiter Eintrag zählt nicht
		t.Fatal(err)
	}
	RecordSessionStart(dir, "s-owner", "acc-start", nov6)
	l := func(msg, sid, acc string) protocol.RequestLine {
		r := req(msg, "r"+msg, "2026-11-02T10:00:00Z", 1)
		r.SessionID, r.AccountID = sid, acc
		return r
	}
	a := open(t, dir, nov6)
	if _, err := a.AddRequests([]protocol.RequestLine{
		l("m1", "s-start", ""), l("m2", "s-owner", "acc-owner"), l("m3", "s-ohne", ""), l("m6", "s-nurowner", "acc-owner"),
	}, nil, "acc-jetzt"); err != nil {
		t.Fatal(err)
	}
	a = open(t, dir, nov6)
	if _, err := a.AddRequests([]protocol.RequestLine{l("m4", "s-owner", "acc-anders"), l("m5", "s-start", "")}, nil, "acc-jetzt"); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, h := range readAll(t, filepath.Join(dir, MonthFile("2026-11"))) {
		var k requestKey
		json.Unmarshal([]byte(h.Z), &k)
		got[k.MessageID] = k.AccountID
	}
	want := map[string]string{"m1": "acc-start", "m2": "acc-start", "m3": "acc-jetzt", "m4": "acc-start", "m5": "acc-start", "m6": "acc-owner"}
	for m, acc := range want {
		if got[m] != acc {
			t.Errorf("%s: Konto %q, want %q", m, got[m], acc)
		}
	}
	if fi, err := os.Stat(filepath.Join(dir, SessionsFile)); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
		t.Fatalf("sitzungen.jsonl: %v %v", fi, err)
	}
	if RecordSessionStart(dir, "a b", "acc", nov6) == nil || RecordSessionStart(dir, "s", "", nov6) == nil {
		t.Fatal("ungültige Kennung geschrieben")
	}
}

// Schreiben ohne Abgleich (etwa ohne Kopplung): je Sitzung höchstens ein Eintrag, und die Grenzen gelten schon beim
// Schreiben: alte Einträge fallen weg, höchstens sessionMax bleiben (die neuesten, samt dem eben geschriebenen).
func TestRecordSessionStartBounded(t *testing.T) {
	dir := newDir(t)
	for i := range 5 {
		if err := RecordSessionStart(dir, "s-gleich", "acc-1", nov6.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	RecordSessionStart(dir, "s-gleich", "acc-2", nov6.Add(time.Hour))
	all, first, err := readSessionStarts(dir)
	if err != nil || len(all) != 1 || first["s-gleich"] != "acc-1" || !all[0].At.Equal(nov6) {
		t.Fatalf("gleiche Sitzung mehrfach: %+v %v", all, err)
	}

	RecordSessionStart(dir, "s-spaeter", "acc-1", nov6.Add(sessionKeep+time.Hour))
	all, first, _ = readSessionStarts(dir)
	if len(all) != 1 || first["s-spaeter"] != "acc-1" {
		t.Fatalf("alter Eintrag beim Schreiben nicht entfernt: %+v", all)
	}

	var b strings.Builder
	for i := range sessionMax {
		line, _ := protocol.MarshalLine(sessionStart{SessionID: "s" + itoa(i), AccountID: "acc", At: nov6.Add(time.Duration(i) * time.Second)})
		b.Write(line)
		b.WriteByte('\n')
	}
	os.WriteFile(filepath.Join(dir, SessionsFile), []byte(b.String()), 0o600)
	if err := RecordSessionStart(dir, "s-neu", "acc-neu", nov6.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	all, first, _ = readSessionStarts(dir)
	if len(all) != sessionMax || first["s0"] != "" || first["s1"] != "acc" || first["s-neu"] != "acc-neu" {
		t.Fatalf("%d Einträge, s0 %q, s-neu %q", len(all), first["s0"], first["s-neu"])
	}
	if fi, err := os.Stat(filepath.Join(dir, SessionsFile)); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
		t.Fatalf("sitzungen.jsonl nach dem Kürzen: %v %v", fi, err)
	}
}

// Aufräumen: alte Einträge und Doppelte fallen weg, höchstens sessionMax bleiben (die neuesten).
func TestPruneSessionStarts(t *testing.T) {
	dir := newDir(t)
	RecordSessionStart(dir, "alt", "acc", nov6.Add(-sessionKeep-time.Hour))
	RecordSessionStart(dir, "neu", "acc-1", nov6.Add(-time.Hour))
	RecordSessionStart(dir, "neu", "acc-2", nov6)
	a := open(t, dir, nov6)
	if err := a.PruneSessionStarts(); err != nil {
		t.Fatal(err)
	}
	all, first, err := readSessionStarts(dir)
	if err != nil || len(all) != 1 || first["neu"] != "acc-1" {
		t.Fatalf("%+v %v", all, err)
	}
	var b strings.Builder
	for i := range sessionMax + 5 {
		line, _ := protocol.MarshalLine(sessionStart{SessionID: "s" + itoa(i), AccountID: "acc", At: nov6.Add(time.Duration(i) * time.Second)})
		b.Write(line)
		b.WriteByte('\n')
	}
	os.WriteFile(filepath.Join(dir, SessionsFile), []byte(b.String()), 0o600)
	if err := a.PruneSessionStarts(); err != nil {
		t.Fatal(err)
	}
	all, first, _ = readSessionStarts(dir)
	if len(all) != sessionMax || first["s0"] != "" || first["s"+itoa(sessionMax+4)] != "acc" {
		t.Fatalf("%d Einträge", len(all))
	}
}

// errLine baut eine gültige Fehlerzeile.
func errLine(msg, reqID, ts string) protocol.ErrorLine {
	r := req(msg, reqID, ts, 0)
	status := int64(429)
	return protocol.ErrorLine{Kind: protocol.KindError, RequestID: reqID, MessageID: msg, SessionID: "s2",
		TS: r.TS, UTCOffsetMin: 60, APIErrorStatus: &status, Error: "rate_limit"}
}

// Fehlerzeilen stehen in derselben Monatsdatei wie Anfragen, je einmal, entdoppelt nur unter sich; ihr Konto setzt
// das Archiv wie bei Anfragen; für die Vorgeschichte zählt ihr Tag nicht als Tag mit Anfrage.
func TestAddRequestsErrorLines(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	add(t, a, req("msg_1", "req_1", "2026-11-03T10:00:00Z", 5))
	e := errLine("msg_1", "req_1", "2026-11-02T10:00:00Z") // derselbe Schlüssel wie die Anfrage: zählt trotzdem
	if err := protocol.ValidateErrorLine(e, nov6); err != nil {
		t.Fatal(err)
	}
	r, err := a.AddRequests(nil, []protocol.ErrorLine{e, e}, "acc-jetzt")
	if err != nil || r.Added != 1 {
		t.Fatalf("hinzugefügt %d, %v", r.Added, err)
	}
	// Neu geöffnet: Das Archiv kennt die Fehlerzeile aus der Datei und schreibt sie nicht noch einmal.
	if r, err := open(t, dir, nov6).AddRequests(nil, []protocol.ErrorLine{e}, ""); err != nil || r.Added != 0 {
		t.Fatalf("doppelt: hinzugefügt %d, %v", r.Added, err)
	}
	lines := readAll(t, filepath.Join(dir, MonthFile("2026-11")))
	if len(lines) != 2 || !strings.Contains(lines[1].Z, `"kind":"error"`) || !strings.Contains(lines[1].Z, `"account_id":"acc-jetzt"`) {
		t.Fatalf("Monatsdatei %v", lines)
	}
	if w, _, err := a.AddLegacy([]protocol.DayLine{day("2026-11-02", 5), day("2026-11-03", 5)}); err != nil || w != 1 {
		t.Fatalf("Vorgeschichte geschrieben %d, %v", w, err)
	}
}

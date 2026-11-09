package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

const outdatedBody = `{"error":"plugin_outdated","min_plugin_version":"9.9.9"}`

// Server-Umzug: Derselbe Server (gleiche server_id) unter einer anderen Adresse bekommt dieselbe device_source; der
// Server koppelt dasselbe Gerät neu, und connect meldet kein zurückbleibendes altes Gerät.
func TestConnectServerMoveSameDevice(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f, g := newFakeServer(t), newFakeServer(t)
	g.serverID = f.serverID
	f.repaired, g.repaired = true, true
	mustConnect(t, cfg, data, f)
	out := mustConnect(t, cfg, data, g)
	if sentSource(t, f) != sentSource(t, g) {
		t.Fatal("andere device_source nach dem Umzug")
	}
	if strings.Contains(out, "altes Gerät bleibt") || strings.Contains(out, "bisherige Verbindung") ||
		!strings.Contains(out, "Bestehendes Gerät neu gekoppelt") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
	conn, ok, err := state.LoadConnection(data)
	if err != nil || !ok || conn.ServerID != f.serverID || conn.ServerURL != g.srv.URL || conn.DeviceID != 7 {
		t.Fatalf("Verbindung %+v %v", conn, err)
	}
}

// Ein anderer Server (andere server_id) unter derselben Adresse ist ein anderer Server: andere device_source, und
// das bisherige Gerät bleibt dort stehen.
func TestConnectOtherServerIDSameAddress(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.repaired = true
	mustConnect(t, cfg, data, f)
	before := sentSource(t, f)
	f.serverID = randomHex(t, 32)
	out := mustConnect(t, cfg, data, f)
	if sentSource(t, f) == before {
		t.Fatal("gleiche device_source für eine andere server_id")
	}
	if !strings.Contains(out, "Das alte Gerät bleibt auf dem Server") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
}

// Ohne gültige Server-Kennung koppelt connect nicht: kein Pair, nichts gespeichert, eine verständliche Meldung.
func TestConnectNeedsServerInfo(t *testing.T) {
	cases := map[string]func(f *fakeServer){
		"404":      func(f *fakeServer) { f.infoCode = http.StatusNotFound },
		"kaputt":   func(f *fakeServer) { f.serverID = "XYZ" },
		"500":      func(f *fakeServer) { f.infoCode = http.StatusInternalServerError },
		"zu viele": func(f *fakeServer) { f.infoCode = http.StatusTooManyRequests },
	}
	for name, set := range cases {
		cfg, data := connectEnv(t)
		f := newFakeServer(t)
		set(f)
		code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
		if code != 1 || !strings.Contains(out, "Kopplung abgebrochen") || f.pairCalls.Load() != 0 {
			t.Fatalf("%s: Exit %d, %d Pair-Aufrufe: %s", name, code, f.pairCalls.Load(), out)
		}
		if name == "404" && !strings.Contains(out, "keine Server-Kennung") {
			t.Fatalf("404: %s", out)
		}
		assertNothingStored(t, data)
	}
}

// Mindestversion beim Koppeln: 426 → Meldung mit Version und Befehl, nichts gespeichert.
func TestConnectOutdated(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.pairCode, f.pairBody = http.StatusUpgradeRequired, outdatedBody
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 1 || !strings.Contains(out, deliver.OutdatedMessage("9.9.9")) || !strings.Contains(out, "Kopplungscode bleibt gültig") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	assertNothingStored(t, data)
}

// Mindestversion beim Liefern: sync, status und Sessionstart zeigen die Meldung, ohne Schritte zum Neukoppeln; die
// Daten warten. Nach dem Aktualisieren (Server nimmt wieder an) ist die Meldung weg.
func TestOutdatedShownEverywhere(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	f.ingestCode, f.ingestBody = http.StatusUpgradeRequired, outdatedBody
	testLock(t)
	want := deliver.OutdatedMessage("9.9.9")
	code, _, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(errOut, want) || strings.Contains(errOut, "So koppelst du neu") {
		t.Fatalf("sync: Exit %d: %s", code, errOut)
	}
	var out bytes.Buffer
	runStatus(nil, &out, data, cfg)
	if !strings.Contains(out.String(), want) || strings.Contains(out.String(), "So koppelst du neu") {
		t.Fatalf("status:\n%s", out.String())
	}
	if msg := sessionStartNotice(data, cfg); !strings.Contains(msg, want) || strings.Contains(msg, "So koppelst du neu") {
		t.Fatalf("Sessionstart: %q", msg)
	}

	f.ingestCode, f.ingestBody = 0, ""
	if code, _, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("sync nach dem Aktualisieren: Exit %d: %s", code, errOut)
	}
	if len(f.ingested()) == 0 {
		t.Fatal("nach dem Aktualisieren nichts geliefert")
	}
	if msg := sessionStartNotice(data, cfg); strings.Contains(msg, "aktualisieren") {
		t.Fatalf("Sessionstart nach dem Aktualisieren: %q", msg)
	}
}

// Server mit Datenbank einer älteren Version (503 mit reason "old_database"): connect, sync und status zeigen die
// Meldung des Servers statt „ausgelastet“. Dass jedes andere 503 „ausgelastet“ bleibt, prüfen TestOldDatabase (client)
// und TestRunGivesUpAfterThreeRetries (deliver).
func TestOldDatabaseShown(t *testing.T) {
	const msg = "Die Datenbank des Servers stammt von einer älteren Version; der Betreiber muss sie neu einrichten."
	body := `{"error":"` + msg + `","reason":"old_database"}`

	// Koppeln: schon die Server-Kennung scheitert.
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.infoCode, f.infoBody = http.StatusServiceUnavailable, body
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 1 || !strings.HasPrefix(out, msg+" – Kopplung abgebrochen.") || f.pairCalls.Load() != 0 {
		t.Fatalf("connect (Info): Exit %d: %s", code, out)
	}
	assertNothingStored(t, data)

	// Koppeln: Info klappt, Pair antwortet so.
	cfg, data = connectEnv(t)
	f = newFakeServer(t)
	f.pairCode, f.pairBody = http.StatusServiceUnavailable, body
	code, out = connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 1 || !strings.Contains(out, msg) || strings.Contains(out, "ausgelastet") {
		t.Fatalf("connect (Pair): Exit %d: %s", code, out)
	}
	assertNothingStored(t, data)

	// Liefern.
	f = newFakeServer(t)
	cfg, data = connectedEnv(t, f)
	f.ingestCode, f.ingestBody = http.StatusServiceUnavailable, body
	testLock(t)
	code, _, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(errOut, msg) || strings.Contains(errOut, "ausgelastet") {
		t.Fatalf("sync: Exit %d: %s", code, errOut)
	}
	var st bytes.Buffer
	runStatus(nil, &st, data, cfg)
	if !strings.Contains(st.String(), "Letzter Fehler: "+msg) {
		t.Fatalf("status:\n%s", st.String())
	}
	// Sitzungsstart wie bei 426 (Entscheidung M11 a): die Meldung, ohne Schritte zum Neukoppeln.
	if notice := sessionStartNotice(data, cfg); !strings.Contains(notice, msg) || strings.Contains(notice, "So koppelst du neu") {
		t.Fatalf("Sessionstart: %q", notice)
	}
	var hook bytes.Buffer
	writeSessionNotice(&hook, data, cfg)
	if !strings.Contains(hook.String(), `"systemMessage":"tokenlore: `+msg) {
		t.Fatalf("Hook-Ausgabe: %s", hook.String())
	}

	// Danach ein anderer Fehler (Server nicht bereit, „ausgelastet“): die Meldung der alten Datenbank ist weg.
	f.ingestCode, f.ingestBody = http.StatusServiceUnavailable, `{"error":"x"}`
	syncRun(data, cfg)
	if notice := sessionStartNotice(data, cfg); strings.Contains(notice, msg) {
		t.Fatalf("Sessionstart nach anderem Fehler: %q", notice)
	}
	// Der Betreiber hat neu eingerichtet, die Lieferung gelingt: keine Meldung mehr.
	f.ingestCode, f.ingestBody = http.StatusServiceUnavailable, body
	syncRun(data, cfg)
	f.ingestCode, f.ingestBody = 0, ""
	if code, _, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("sync danach: Exit %d: %s", code, errOut)
	}
	if notice := sessionStartNotice(data, cfg); strings.Contains(notice, msg) {
		t.Fatalf("Sessionstart nach Erfolg: %q", notice)
	}
}

// Fund M12: Hat ein anderes Gerät den Signaturschlüssel übernommen, antwortet der Server beim Liefern 400 mit reason
// "sign_key_unknown". sync, status und der Sitzungsstart zeigen dann deliver.MsgSignKeyTaken mit den Schritten zum
// Neukoppeln; nach einer gelungenen Lieferung ist die Meldung weg.
func TestSignKeyTakenShown(t *testing.T) {
	body := `{"error":"Signatur passt zu keinem Schlüssel des Geräts","path":"requests[0].s","reason":"sign_key_unknown"}`
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	f.ingestCode, f.ingestBody = http.StatusBadRequest, body
	testLock(t)
	code, _, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(errOut, deliver.MsgSignKeyTaken) || !strings.Contains(errOut, "So koppelst du neu") {
		t.Fatalf("sync: Exit %d: %s", code, errOut)
	}
	var st bytes.Buffer
	runStatus(nil, &st, data, cfg)
	if !strings.Contains(st.String(), "Letzter Fehler: "+deliver.MsgSignKeyTaken) || !strings.Contains(st.String(), "So koppelst du neu") {
		t.Fatalf("status:\n%s", st.String())
	}
	if notice := sessionStartNotice(data, cfg); !strings.Contains(notice, deliver.MsgSignKeyTaken) || !strings.Contains(notice, "So koppelst du neu") {
		t.Fatalf("Sessionstart: %q", notice)
	}
	var hook bytes.Buffer
	writeSessionNotice(&hook, data, cfg)
	if !strings.Contains(hook.String(), `"systemMessage":"tokenlore: `+deliver.MsgSignKeyTaken) {
		t.Fatalf("Hook-Ausgabe: %s", hook.String())
	}

	// Eine andere Ablehnung ohne den Grund bleibt die rohe Meldung, ohne Schritte.
	f.ingestBody = `{"error":"Signatur passt zu keinem Schlüssel des Geräts","path":"requests[0].s"}`
	syncRun(data, cfg)
	if notice := sessionStartNotice(data, cfg); strings.Contains(notice, deliver.MsgSignKeyTaken) || strings.Contains(notice, "So koppelst du neu") {
		t.Fatalf("Sessionstart ohne Grund: %q", notice)
	}

	// Neu gekoppelt, die Lieferung gelingt: keine Meldung mehr.
	f.ingestBody = body
	syncRun(data, cfg)
	f.ingestCode, f.ingestBody = 0, ""
	if code, _, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("sync danach: Exit %d: %s", code, errOut)
	}
	if notice := sessionStartNotice(data, cfg); strings.Contains(notice, deliver.MsgSignKeyTaken) {
		t.Fatalf("Sessionstart nach Erfolg: %q", notice)
	}
}

// Fall aus M13: Gerät F liefert, zwei weitere Anfragen sind noch offen. Ein anderes Gerät übernimmt Fs
// Signaturschlüssel; F meldet das Neukoppeln (M12). Dann verliert F auch sign.key und koppelt neu: Der alte Schlüssel
// bleibt beim anderen Gerät (sign_keys_refused). Die Zeilen damit gehen nicht mehr mit, neue kommen an, nichts doppelt;
// status sagt, dass ältere Zeilen nicht mehr an den Server gehen, und verlangt kein Neukoppeln.
func TestOldLinesOfTakenKeySkippedAfterRepair(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.verifySign = true
	testLock(t)
	mustConnect(t, cfg, data, f)
	st, _ := state.Load(data)
	k1 := st.SignPublicKey
	s1 := filepath.Join(cfg, "projects", "P", "s1.jsonl")
	appendTo(t, s1, newRequestLine(1)+newRequestLine(2))

	// Das andere Gerät übernimmt k1: F liefert nichts und soll neu koppeln.
	f.signRefused = []string{k1}
	if code, _, errOut := syncRun(data, cfg); code != 1 || !strings.Contains(errOut, deliver.MsgSignKeyTaken) {
		t.Fatalf("sync nach Übernahme: Exit %d: %s", code, errOut)
	}

	// F verliert sign.key und koppelt neu: neuer Schlüssel, k1 bleibt abgewiesen.
	if err := os.Remove(filepath.Join(data, "sign.key")); err != nil {
		t.Fatal(err)
	}
	out := mustConnect(t, cfg, data, f)
	// Was mit den älteren Zeilen geschieht, steht genau einmal da: in der Zeile zum ersten Abgleich mit ihrer Zahl,
	// wie bei sync, und nicht zusätzlich „prüft der Server weiter“ oder „0 Anfragen in 1 Paket“ (Fund M16).
	if !strings.Contains(out, "Erster Abgleich: Nichts Neues.\nHinweis: "+deliver.ForeignMessage(5)+"\n") ||
		strings.Count(out, "ältere Archivzeilen gehen nicht an den Server") != 1 || strings.Contains(out, "prüft der Server weiter") ||
		strings.Contains(out, "Ältere Zeilen damit") ||
		strings.Contains(out, "0 Anfragen") || strings.Contains(out, "fehlgeschlagen") {
		t.Fatalf("connect:\n%s", out)
	}
	st, _ = state.Load(data)
	if st.SignPublicKey == k1 || !slices.Equal(st.SignKeysRefused, []string{k1}) {
		t.Fatalf("Merkzettel: aktuell gleich %v, abgewiesen %v", st.SignPublicKey == k1, st.SignKeysRefused)
	}

	appendTo(t, s1, newRequestLine(3)+newRequestLine(4)+newRequestLine(5))
	code, sOut, errOut := syncRun(data, cfg)
	if code != 0 || !strings.Contains(sOut, "Abgleich fertig: 3 Anfragen") {
		t.Fatalf("sync: Exit %d: %s%s", code, sOut, errOut)
	}
	// Angenommen hat der Server jede Anfrage höchstens einmal; N1 und N2 (mit k1 signiert, nie geliefert) gar nicht.
	seen := map[string]int{}
	f.mu.Lock()
	for _, b := range f.batches {
		for _, e := range b.Requests {
			var l protocol.RequestLine
			json.Unmarshal([]byte(e.Z), &l)
			seen[l.MessageID]++
		}
	}
	f.mu.Unlock()
	for id, n := range seen {
		if n > 1 && (strings.HasPrefix(id, "msg_N")) {
			t.Fatalf("%s %d-mal angenommen", id, n)
		}
	}
	if seen["msg_N1"] != 0 || seen["msg_N2"] != 0 || seen["msg_N3"] != 1 || seen["msg_N4"] != 1 || seen["msg_N5"] != 1 {
		t.Fatalf("angenommen %v", seen)
	}

	var stat bytes.Buffer
	runStatus(nil, &stat, data, cfg)
	if !strings.Contains(stat.String(), deliver.ForeignMessage(5)) ||
		strings.Contains(stat.String(), "So koppelst du neu") || strings.Contains(stat.String(), deliver.MsgSignKeyTaken) {
		t.Fatalf("status:\n%s", stat.String())
	}
	if notice := sessionStartNotice(data, cfg); strings.Contains(notice, deliver.MsgSignKeyTaken) {
		t.Fatalf("Sessionstart: %q", notice)
	}
}

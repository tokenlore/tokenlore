package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// Erfundene Systemkennungen (schon als Hash) und Kennungen.
var (
	machineA = strings.Repeat("a", 64)
	machineB = strings.Repeat("b", 64)
	fixedID  = "0123456789abcdef0123456789abcdef"
	copyID   = "fedcba9876543210fedcba9876543210"
)

// setMachine stellt den Rechner nach, auf dem connect läuft; "" heißt Systemkennung nicht lesbar.
func setMachine(t *testing.T, h string) {
	t.Helper()
	old := machineHash
	machineHash = func() string { return h }
	t.Cleanup(func() { machineHash = old })
}

func identityFile(t *testing.T, cfg string) state.Identity {
	t.Helper()
	id, ok, err := claudecode.ReadIdentity(cfg)
	if err != nil || !ok {
		t.Fatalf("Kennungsdatei: ok=%v err=%v", ok, err)
	}
	return id
}

func writeIdentityFile(t *testing.T, cfg, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cfg, claudecode.IdentityFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// sentSource ist die device_source der letzten Kopplung am Testserver.
func sentSource(t *testing.T, f *fakeServer) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pairBodies) == 0 {
		t.Fatal("keine Kopplung")
	}
	var b struct {
		DeviceSource string `json:"device_source"`
	}
	if err := json.Unmarshal([]byte(f.pairBodies[len(f.pairBodies)-1]), &b); err != nil {
		t.Fatal(err)
	}
	return b.DeviceSource
}

func sourceFor(t *testing.T, id string, f *fakeServer) string {
	t.Helper()
	return claudecode.DeviceSource(id, f.serverID)
}

func mustConnect(t *testing.T, cfg, data string, f *fakeServer, extra ...string) string {
	t.Helper()
	args := append([]string{f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x"}, extra...)
	code, out := connect(t, data, cfg, args...)
	if code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	return out
}

func TestConnectFirstTimeCreatesIdentityAndCopy(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	id := identityFile(t, cfg)
	if id.Machine != machineA || id.Predecessor != "" {
		t.Fatalf("Kennungsdatei %+v", id)
	}
	st, _ := state.Load(data)
	if st.Identity == nil || *st.Identity != id {
		t.Fatalf("Kopie %+v, Datei %+v", st.Identity, id)
	}
	if got := sentSource(t, f); got != sourceFor(t, id.ID, f) {
		t.Fatalf("device_source %q", got)
	}
	conn, _, _ := state.LoadConnection(data)
	if conn.Identity != id.ID {
		t.Fatalf("Verbindung mit Kennung %q", conn.Identity)
	}
}

func TestConnectRestoresIdentityFromCopy(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	first := identityFile(t, cfg)
	os.Remove(filepath.Join(cfg, claudecode.IdentityFile))
	out := mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "aus dem Merkzettel wiederhergestellt") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
	if got := identityFile(t, cfg); got != first {
		t.Fatalf("wiederhergestellt %+v, want %+v", got, first)
	}
	if sentSource(t, f) != sourceFor(t, first.ID, f) {
		t.Fatal("andere device_source nach Wiederherstellung")
	}
}

// Kennungsdatei und Datenordner gelöscht: Derselbe Rechner bekommt dieselbe Kennung zurück (aus der Systemkennung
// abgeleitet) und meldet dieselbe device_source, bleibt auf dem Server also dasselbe Gerät. Ohne lesbare
// Systemkennung gibt es nichts abzuleiten; dann ist die Kennung zufällig und neu.
func TestConnectIdentityWhenFileAndCopyGone(t *testing.T) {
	for _, c := range []struct {
		machine string
		same    bool
	}{{machineA, true}, {"", false}} {
		setMachine(t, c.machine)
		cfg, data := connectEnv(t)
		f := newFakeServer(t)
		mustConnect(t, cfg, data, f)
		first := identityFile(t, cfg)
		os.Remove(filepath.Join(cfg, claudecode.IdentityFile))
		os.RemoveAll(data)
		mustConnect(t, cfg, data, f)
		got := identityFile(t, cfg)
		if (got.ID == first.ID) != c.same || (sentSource(t, f) == sourceFor(t, first.ID, f)) != c.same {
			t.Fatalf("Systemkennung %q: Kennung vorher %s, nachher %s", c.machine, first.ID, got.ID)
		}
		if got.Machine != c.machine {
			t.Fatalf("Systemkennung in der Datei %q", got.Machine)
		}
	}
}

func TestConnectFileWinsOverCopy(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	st, _ := state.Load(data)
	st.Identity = &state.Identity{ID: copyID, Machine: machineA}
	st.Save(data)
	writeIdentityFile(t, cfg, `{"id":"`+fixedID+`","machine":"`+machineA+`"}`)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	if sentSource(t, f) != sourceFor(t, fixedID, f) {
		t.Fatal("Kopie statt Datei benutzt")
	}
	st, _ = state.Load(data)
	if st.Identity.ID != fixedID {
		t.Fatalf("Kopie nicht nachgezogen: %+v", st.Identity)
	}
}

func TestConnectBrokenFileWithCopy(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	st, _ := state.Load(data)
	st.Identity = &state.Identity{ID: copyID, Machine: machineA}
	st.Save(data)
	writeIdentityFile(t, cfg, "kein JSON")
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	if got := identityFile(t, cfg); got.ID != copyID {
		t.Fatalf("Datei %+v", got)
	}
	if sentSource(t, f) != sourceFor(t, copyID, f) {
		t.Fatal("device_source nicht aus der Kopie")
	}
}

func TestConnectBrokenFileWithoutCopy(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	writeIdentityFile(t, cfg, `{"id":"zu kurz"}`)
	f := newFakeServer(t)
	out := mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "tokenlore-id.kaputt") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
	b, err := os.ReadFile(filepath.Join(cfg, claudecode.IdentityFile+".kaputt"))
	if err != nil || string(b) != `{"id":"zu kurz"}` {
		t.Fatalf(".kaputt %q %v", b, err)
	}
	id := identityFile(t, cfg)
	if sentSource(t, f) != sourceFor(t, id.ID, f) {
		t.Fatal("device_source nicht aus der neuen Kennung")
	}
}

func TestConnectNormalizesIdentity(t *testing.T) {
	setMachine(t, machineA)
	for _, content := range []string{
		`{"id":"` + strings.ToUpper(fixedID) + `"}`,
		"{\"id\":\" " + fixedID + "\\r\\n\"}\r\n",
	} {
		cfg, data := connectEnv(t)
		writeIdentityFile(t, cfg, content)
		f := newFakeServer(t)
		mustConnect(t, cfg, data, f)
		if sentSource(t, f) != sourceFor(t, fixedID, f) {
			t.Errorf("%q: andere device_source", content)
		}
	}
}

func TestConnectDeviceSourceDiffersPerServer(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f, g := newFakeServer(t), newFakeServer(t)
	mustConnect(t, cfg, data, f)
	mustConnect(t, cfg, data, g)
	if sentSource(t, f) == sentSource(t, g) {
		t.Fatal("gleiche device_source für zwei Server")
	}
	// Dieselbe Adresse mit "/" am Ende und großem Host ergibt dieselbe device_source.
	u, _ := url.Parse(f.srv.URL)
	other := "https://" + strings.ToUpper(u.Host) + "/"
	before := sentSource(t, f)
	code, out := connect(t, data, cfg, other, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if sentSource(t, f) != before {
		t.Fatal("andere device_source für dieselbe Adresse")
	}
}

// Kennungsdatei von einem anderen Rechner: connect fragt nicht, sondern macht diesen Rechner zu einem eigenen Gerät.
func TestConnectOtherMachineBecomesOwnDevice(t *testing.T) {
	cfg, data := connectEnv(t)
	writeIdentityFile(t, cfg, `{"id":"`+fixedID+`","machine":"`+machineA+`","predecessor":"","last_day":"2026-08-15"}`)
	setMachine(t, machineB)
	f := newFakeServer(t)
	out := mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "dieser Rechner bekommt eine eigene Kennung") || strings.Contains(out, "Rückfrage") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
	got := identityFile(t, cfg)
	// Ein last_day aus einer alten Datei fällt weg; der Abgleich schreibt die Kennungsdatei nie.
	if got.ID == fixedID || got.Predecessor != fixedID || got.Machine != machineB {
		t.Fatalf("eigenes Gerät: %+v", got)
	}
	if sentSource(t, f) != sourceFor(t, got.ID, f) {
		t.Fatal("eigenes Gerät mit alter device_source")
	}
	st, _ := state.Load(data)
	if st.Identity == nil || *st.Identity != got {
		t.Fatalf("Kopie %+v", st.Identity)
	}
	// Danach gehört die Datei zu diesem Rechner: erneutes Koppeln behält die Kennung.
	mustConnect(t, cfg, data, f)
	if identityFile(t, cfg).ID != got.ID {
		t.Fatal("Kennung beim zweiten Koppeln geändert")
	}
}

// Die Optionen --umzug und --zweiter-rechner gibt es nicht mehr.
func TestConnectMoveOptionsUnknown(t *testing.T) {
	for _, opt := range []string{"--umzug", "--zweiter-rechner"} {
		cfg, data := connectEnv(t)
		f := newFakeServer(t)
		code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), opt)
		if code != 2 || !strings.Contains(out, "Unbekannte Option") || !strings.Contains(out, "Aufruf: /tokenlore:connect") {
			t.Fatalf("%s: Exit %d: %s", opt, code, out)
		}
		assertNothingStored(t, data)
	}
}

func TestConnectMachineUnreadableSkipsDetection(t *testing.T) {
	cfg, data := connectEnv(t)
	writeIdentityFile(t, cfg, `{"id":"`+fixedID+`","machine":"`+machineA+`"}`)
	setMachine(t, "")
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	if got := identityFile(t, cfg); got.ID != fixedID || got.Machine != machineA {
		t.Fatalf("Kennungsdatei %+v", got)
	}
	if sentSource(t, f) != sourceFor(t, fixedID, f) {
		t.Fatal("andere device_source")
	}
}

func TestConnectMachineUnreadableFirstTime(t *testing.T) {
	setMachine(t, "")
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	if got := identityFile(t, cfg); got.Machine != "" {
		t.Fatalf("Kennungsdatei %+v", got)
	}
	// Ist die Systemkennung später lesbar, wird sie übernommen, ohne Rückfrage.
	setMachine(t, machineA)
	mustConnect(t, cfg, data, f)
	if got := identityFile(t, cfg); got.Machine != machineA {
		t.Fatalf("Systemkennung nicht übernommen: %+v", got)
	}
}

// Neue Kennung auf demselben Rechner (etwa ein anderer Claude-Ordner mit demselben Datenordner): Die alte Kennung lag
// nachweislich hier, ihr Gerät liefert nicht mehr und kann gesperrt werden. Kein Wort vom Rechner, von dem kopiert wurde.
func TestConnectNewIdentitySameMachine(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	writeIdentityFile(t, cfg, `{"id":"`+copyID+`","machine":"`+machineA+`","predecessor":"","last_day":""}`+"\n")
	out := mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "gehörte zu einer früheren Kennung dieses Rechners") || !strings.Contains(out, "kannst es auf dem Server sperren") ||
		strings.Contains(out, "von dem kopiert wurde") || strings.Contains(out, "bis du es dort sperrst") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
}

// Ohne lesbare Systemkennung ist unbekannt, wo die bisherige Kennung lag: vorsichtige Meldung, kein Aufruf zum
// Sperren, auch wenn die Kopie im Merkzettel ebenfalls keine Systemkennung hat.
func TestConnectNewIdentityWithoutMachineIsCautious(t *testing.T) {
	setMachine(t, "")
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	writeIdentityFile(t, cfg, `{"id":"`+copyID+`","machine":"","predecessor":"","last_day":""}`+"\n")
	out := mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "gehörte zu einer anderen Kennung") || strings.Contains(out, "kannst es auf dem Server sperren") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
}

// Passt die Kopie im Merkzettel nicht zur Kennung der bisherigen Verbindung, sagt ihre Systemkennung nichts über
// diese Verbindung: vorsichtige Meldung.
func TestConnectCopyOfOtherIdentityIsCautious(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	st, err := state.Load(data)
	if err != nil || st.Identity == nil {
		t.Fatalf("Merkzettel %v %+v", err, st)
	}
	st.Identity.ID = "11111111111111111111111111111111"
	if err := st.Save(data); err != nil {
		t.Fatal(err)
	}
	writeIdentityFile(t, cfg, `{"id":"`+copyID+`","machine":"`+machineA+`","predecessor":"","last_day":""}`+"\n")
	out := mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "gehörte zu einer anderen Kennung") || strings.Contains(out, "kannst es auf dem Server sperren") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
}

// Kaputte Kennungsdatei ohne Kopie im Merkzettel. War die bisherige Kennung zufällig (ältere Installation), entsteht die
// Kennung aus der Systemkennung neu und weicht ab: Das alte Gerät liefert von hier nicht mehr. War sie schon
// abgeleitet, kommt dieselbe zurück, und es gibt keine frühere Kennung.
// Geändert, weil eine fehlende oder kaputte Datei ohne Kopie nicht mehr zufällig neu entsteht (Entscheidung M2).
func TestConnectBrokenIdentityWithoutCopy(t *testing.T) {
	for _, random := range []bool{true, false} {
		setMachine(t, machineA)
		cfg, data := connectEnv(t)
		os.MkdirAll(cfg, 0o700)
		if random {
			writeIdentityFile(t, cfg, `{"id":"`+fixedID+`","machine":"`+machineA+`"}`)
		}
		f := newFakeServer(t)
		mustConnect(t, cfg, data, f)
		st, err := state.Load(data)
		if err != nil {
			t.Fatal(err)
		}
		st.Identity = nil
		if err := st.Save(data); err != nil {
			t.Fatal(err)
		}
		writeIdentityFile(t, cfg, "kaputt")
		out := mustConnect(t, cfg, data, f)
		derived := claudecode.DeriveIdentityID(machineA, cfg)
		if got := identityFile(t, cfg); got.ID != derived {
			t.Fatalf("Kennung %s, abgeleitet %s", got.ID, derived)
		}
		if !strings.Contains(out, "aus der Systemkennung dieses Rechners bestimmt") ||
			strings.Contains(out, "gehörte zu einer früheren Kennung dieses Rechners") != random {
			t.Fatalf("zufällig=%v, Ausgabe:\n%s", random, out)
		}
	}
}

// Andere Kennung, aber der Server koppelt dasselbe Gerät neu: kein altes Gerät, das stehen bliebe.
func TestConnectRepairedSameDeviceOtherIdentity(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	writeIdentityFile(t, cfg, `{"id":"`+copyID+`","machine":"`+machineA+`","predecessor":"","last_day":""}`+"\n")
	f.repaired = true
	out := mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "Bestehendes Gerät neu gekoppelt") || strings.Contains(out, "bisherige Verbindung") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
}

func TestConnectRepairedSameDevice(t *testing.T) {
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	out := mustConnect(t, cfg, data, f)
	if strings.Contains(out, "neu gekoppelt") {
		t.Fatalf("neues Gerät als neu gekoppelt gemeldet:\n%s", out)
	}
	f.repaired = true
	out = mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "Bestehendes Gerät neu gekoppelt") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
	if strings.Contains(out, "Das alte Gerät bleibt") {
		t.Fatalf("Hinweis auf altes Gerät, obwohl dasselbe neu gekoppelt wurde:\n%s", out)
	}
}

package claudecode

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/state"
)

// Nur erfundene Werte.
const (
	testID      = "0123456789abcdef0123456789abcdef"
	otherID     = "fedcba9876543210fedcba9876543210"
	testMachine = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherMach   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func writeID(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, IdentityFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readIDFile(t *testing.T, dir string) state.Identity {
	t.Helper()
	id, ok, err := ReadIdentity(dir)
	if err != nil || !ok {
		t.Fatalf("Kennungsdatei: ok=%v err=%v", ok, err)
	}
	return id
}

func TestParseIdentityFormats(t *testing.T) {
	full := `{"id":"` + testID + `","machine":"` + testMachine + `","predecessor":"` + otherID + `","last_day":"2026-08-15"}`
	cases := []struct {
		name, in string
		ok       bool
		want     state.Identity
	}{
		{"vollständig", full, true, state.Identity{ID: testID, Machine: testMachine, Predecessor: otherID}},
		// Alte Dateien tragen noch last_day und branch_day: lesbar, die Felder fallen weg.
		{"mit branch_day", `{"id":"` + testID + `","predecessor":"` + otherID + `","last_day":"2026-09-01","branch_day":"2026-08-15"}`, true,
			state.Identity{ID: testID, Predecessor: otherID}},
		{"branch_day ungültig", `{"id":"` + testID + `","branch_day":"2026-02-30"}`, true, state.Identity{ID: testID}},
		{"Zeilenumbruch am Ende", full + "\n", true, state.Identity{ID: testID, Machine: testMachine, Predecessor: otherID}},
		{"CRLF", strings.ReplaceAll(`{"id":"`+testID+"\",\r\n\"machine\":\"\"}\r\n", "\n", "\r\n"), true, state.Identity{ID: testID}},
		{"id mit CRLF im Wert", `{"id":"` + testID + `\r\n"}`, true, state.Identity{ID: testID}},
		{"id groß und mit Leerraum", `{"id":"  ` + strings.ToUpper(testID) + `\t "}`, true, state.Identity{ID: testID}},
		{"BOM", "\xef\xbb\xbf" + `{"id":"` + testID + `"}`, true, state.Identity{ID: testID}},
		{"fehlende Felder", `{"id":"` + testID + `"}`, true, state.Identity{ID: testID}},
		{"ungültige Nebenfelder", `{"id":"` + testID + `","machine":"xyz","predecessor":5,"last_day":"2026-02-30"}`, true, state.Identity{ID: testID}},
		{"leer", "", false, state.Identity{}},
		{"kein JSON", "0123456789abcdef0123456789abcdef", false, state.Identity{}},
		{"JSON-Liste", `["` + testID + `"]`, false, state.Identity{}},
		{"null", `null`, false, state.Identity{}},
		{"id zu kurz", `{"id":"0123456789abcdef"}`, false, state.Identity{}},
		{"id zu lang", `{"id":"` + testID + `00"}`, false, state.Identity{}},
		{"id kein Hex", `{"id":"0123456789abcdefg123456789abcdef"}`, false, state.Identity{}},
		{"id als Zahl", `{"id":12345}`, false, state.Identity{}},
		{"id fehlt", `{"machine":"` + testMachine + `"}`, false, state.Identity{}},
	}
	for _, c := range cases {
		got, ok := parseIdentity([]byte(c.in))
		if ok != c.ok || got != c.want {
			t.Errorf("%s: %+v ok=%v, want %+v ok=%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestEnsureIdentityFirstTimeCreatesFile(t *testing.T) {
	dir := t.TempDir()
	id, origin, err := EnsureIdentity(dir, nil, testMachine)
	if err != nil || origin != IdentityNew {
		t.Fatalf("origin %v err %v", origin, err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id.ID) || id.Machine != testMachine || id.Predecessor != "" {
		t.Fatalf("%+v", id)
	}
	if got := readIDFile(t, dir); got != id {
		t.Fatalf("Datei %+v, want %+v", got, id)
	}
	b, _ := os.ReadFile(filepath.Join(dir, IdentityFile))
	want := `{"id":"` + id.ID + `","machine":"` + testMachine + `","predecessor":""}` + "\n"
	if string(b) != want {
		t.Fatalf("Inhalt %q", b)
	}
	// Ein zweiter Aufruf ändert nichts.
	again, origin, err := EnsureIdentity(dir, &id, testMachine)
	if err != nil || origin != IdentityFromFile || again != id {
		t.Fatalf("zweiter Aufruf: %+v %v %v", again, origin, err)
	}
}

// Ohne Datei und Kopie leitet EnsureIdentity die Kennung aus der Systemkennung und dem Ordner ab: Wird die Datei
// gelöscht, kommt dieselbe Kennung zurück. Ein anderer Rechner oder ein anderer Ordner (etwa ein zweiter Benutzer
// desselben Rechners) bekommt eine andere. Die Systemkennung selbst steht nicht in der Kennung.
func TestEnsureIdentityDerivedFromMachine(t *testing.T) {
	dir := t.TempDir()
	first, origin, err := EnsureIdentity(dir, nil, testMachine)
	if err != nil || origin != IdentityNew {
		t.Fatal(origin, err)
	}
	if first.ID != DeriveIdentityID(testMachine, dir) || strings.Contains(testMachine, first.ID) {
		t.Fatalf("Kennung %s nicht abgeleitet", first.ID)
	}
	os.Remove(IdentityPath(dir))
	again, _, _ := EnsureIdentity(dir, nil, testMachine)
	if again != first {
		t.Fatalf("nach Löschen %+v, vorher %+v", again, first)
	}
	// Derselbe Ordner, anders geschrieben, ergibt dieselbe Kennung.
	if DeriveIdentityID(testMachine, dir+string(filepath.Separator)+".") != first.ID {
		t.Fatal("Schreibweise des Ordners ändert die Kennung")
	}
	// Derselbe Ordner über einen Symlink ergibt dieselbe Kennung wie über den echten Pfad.
	link := filepath.Join(t.TempDir(), "verweis")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip("Symlink nicht möglich:", err)
	}
	if DeriveIdentityID(testMachine, link) != first.ID {
		t.Fatal("Symlink auf denselben Ordner ändert die Kennung")
	}
	// Auflösen scheitert (Ordner fehlt): der bereinigte Pfad gilt, das Ergebnis ist stabil und nicht leer.
	gone := filepath.Join(t.TempDir(), "fehlt")
	if g := DeriveIdentityID(testMachine, gone); g == "" || g != DeriveIdentityID(testMachine, gone+string(filepath.Separator)+".") {
		t.Fatalf("fehlender Ordner: %q", g)
	}
	other, _, _ := EnsureIdentity(t.TempDir(), nil, testMachine)
	mach, _, _ := EnsureIdentity(t.TempDir(), nil, otherMach)
	if other.ID == first.ID || mach.ID == first.ID || other.ID == mach.ID {
		t.Fatalf("gleiche Kennung für anderen Ordner oder Rechner: %s %s %s", first.ID, other.ID, mach.ID)
	}
	if DeriveIdentityID("", dir) != "" {
		t.Fatal("ohne Systemkennung abgeleitet")
	}
	// Kaputte Datei ohne Kopie: ebenfalls abgeleitet, die kaputte bleibt daneben.
	writeID(t, dir, "kaputt")
	broken, origin, _ := EnsureIdentity(dir, nil, testMachine)
	if origin != IdentityNewForBroken || broken.ID != first.ID {
		t.Fatalf("kaputte Datei: %+v %v", broken, origin)
	}
}

func TestEnsureIdentityNewIDsDiffer(t *testing.T) {
	a, _, _ := EnsureIdentity(t.TempDir(), nil, "")
	b, _, _ := EnsureIdentity(t.TempDir(), nil, "")
	if a.ID == b.ID {
		t.Fatal("zweimal dieselbe neue Kennung")
	}
}

func TestEnsureIdentityRestoresFromCopy(t *testing.T) {
	dir := t.TempDir()
	cp := state.Identity{ID: testID, Machine: testMachine}
	id, origin, err := EnsureIdentity(dir, &cp, testMachine)
	if err != nil || origin != IdentityFromCopy || id != cp {
		t.Fatalf("%+v %v %v", id, origin, err)
	}
	if got := readIDFile(t, dir); got != cp {
		t.Fatalf("Datei %+v", got)
	}
}

func TestEnsureIdentityFileWinsOverCopy(t *testing.T) {
	dir := t.TempDir()
	writeID(t, dir, `{"id":"`+testID+`"}`)
	cp := state.Identity{ID: otherID}
	id, origin, err := EnsureIdentity(dir, &cp, testMachine)
	if err != nil || origin != IdentityFromFile || id.ID != testID {
		t.Fatalf("%+v %v %v", id, origin, err)
	}
}

func TestEnsureIdentityNormalizesFile(t *testing.T) {
	for _, content := range []string{
		`{"id":"` + strings.ToUpper(testID) + `"}`,
		`{"id":" ` + testID + ` "}`,
		"{\"id\":\"" + testID + "\"}\r\n",
		`{"id":"` + testID + `\r\n"}`,
	} {
		dir := t.TempDir()
		writeID(t, dir, content)
		id, origin, err := EnsureIdentity(dir, nil, testMachine)
		if err != nil || origin != IdentityFromFile || id.ID != testID {
			t.Errorf("%q: %+v %v %v", content, id, origin, err)
		}
		if DeviceSource(id.ID, testServerID) != DeviceSource(testID, testServerID) {
			t.Errorf("%q: andere device_source", content)
		}
	}
}

func TestEnsureIdentityBrokenWithCopy(t *testing.T) {
	dir := t.TempDir()
	writeID(t, dir, "kaputt")
	cp := state.Identity{ID: testID, Machine: testMachine}
	id, origin, err := EnsureIdentity(dir, &cp, testMachine)
	if err != nil || origin != IdentityFromCopy || id != cp {
		t.Fatalf("%+v %v %v", id, origin, err)
	}
	if got := readIDFile(t, dir); got != cp {
		t.Fatalf("Datei nicht neu geschrieben: %+v", got)
	}
}

func TestEnsureIdentityBrokenWithoutCopyKeepsBroken(t *testing.T) {
	for _, content := range []string{"", "kein json", `{"id":"0123"}`, `{"id":"` + testID[:31] + `g"}`} {
		dir := t.TempDir()
		writeID(t, dir, content)
		id, origin, err := EnsureIdentity(dir, nil, testMachine)
		if err != nil || origin != IdentityNewForBroken || id.ID == "" {
			t.Fatalf("%q: %+v %v %v", content, id, origin, err)
		}
		b, err := os.ReadFile(filepath.Join(dir, IdentityFile+".kaputt"))
		if err != nil || string(b) != content {
			t.Fatalf("%q: .kaputt %q %v", content, b, err)
		}
		if got := readIDFile(t, dir); got != id {
			t.Fatalf("%q: Datei %+v", content, got)
		}
	}
}

func TestEnsureIdentityBrokenTwiceKeepsBoth(t *testing.T) {
	dir := t.TempDir()
	writeID(t, dir, "erste")
	if _, _, err := EnsureIdentity(dir, nil, ""); err != nil {
		t.Fatal(err)
	}
	writeID(t, dir, "zweite")
	if _, _, err := EnsureIdentity(dir, nil, ""); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(dir, IdentityFile+".kaputt"))
	b, _ := os.ReadFile(filepath.Join(dir, IdentityFile+".kaputt.2"))
	if string(a) != "erste" || string(b) != "zweite" {
		t.Fatalf("%q %q", a, b)
	}
}

func TestEnsureIdentityInvalidCopyIgnored(t *testing.T) {
	dir := t.TempDir()
	cp := state.Identity{ID: "kaputt"}
	id, origin, err := EnsureIdentity(dir, &cp, "")
	if err != nil || origin != IdentityNew || id.ID == "kaputt" {
		t.Fatalf("%+v %v %v", id, origin, err)
	}
}

func TestMachineConflict(t *testing.T) {
	id := state.Identity{ID: testID, Machine: testMachine}
	if MachineConflict(id, testMachine) || MachineConflict(id, "") || MachineConflict(state.Identity{ID: testID}, otherMach) {
		t.Fatal("Konflikt ohne Abweichung")
	}
	if !MachineConflict(id, otherMach) {
		t.Fatal("Abweichung nicht erkannt")
	}
}

func TestBindAndForkIdentity(t *testing.T) {
	dir := t.TempDir()
	old := state.Identity{ID: testID, Machine: testMachine}
	bound, err := BindIdentity(dir, old, otherMach)
	if err != nil || bound.ID != testID || bound.Machine != otherMach {
		t.Fatalf("Binden %+v %v", bound, err)
	}
	if readIDFile(t, dir) != bound {
		t.Fatal("Binden nicht geschrieben")
	}
	forked, err := ForkIdentity(dir, old, otherMach)
	if err != nil || forked.ID == testID || forked.Predecessor != testID || forked.Machine != otherMach {
		t.Fatalf("eigenes Gerät %+v %v", forked, err)
	}
	if readIDFile(t, dir) != forked {
		t.Fatal("eigenes Gerät nicht geschrieben")
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestNormalizeServerAddress(t *testing.T) {
	cases := map[string]string{
		"https://x.de":                 "https://x.de",
		"https://X.de/":                "https://x.de",
		"https://x.de:443":             "https://x.de",
		"https://x.de:8443/":           "https://x.de:8443",
		"https://x.de/Tokenlore/":      "https://x.de/Tokenlore",
		"https://[::1]:443/":           "https://[::1]",
		"https://[::1]:8443":           "https://[::1]:8443",
		"https://x.de/a/index.php":     "https://x.de/a/index.php",
		"https://x.de/mit%20Leerzeich": "https://x.de/mit%20Leerzeich",
	}
	for in, want := range cases {
		if got := NormalizeServerAddress(mustURL(t, in)); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

// testServerID ist eine erfundene server_id aus GET /api/plugin/info.
const testServerID = "00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff00ff"

// device_source hängt nur an Kennung und server_id, nicht an der Adresse: Ein Umzug des Servers ändert sie nicht.
func TestDeviceSource(t *testing.T) {
	a := DeviceSource(testID, testServerID)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a) {
		t.Fatalf("Format %q", a)
	}
	if DeviceSource(testID, strings.Replace(testServerID, "00", "11", 1)) == a {
		t.Error("andere server_id, gleiche device_source")
	}
	if DeviceSource(otherID, testServerID) == a {
		t.Error("andere Kennung, gleiche device_source")
	}
	// Feststehender Wert: HMAC-SHA256(Schlüssel = Kennung als Text, Nachricht = server_id als Text).
	if a != wantDeviceSource {
		t.Errorf("device_source %s, want %s", a, wantDeviceSource)
	}
}

// wantDeviceSource ist unabhängig berechnet: printf '<testServerID>' | openssl dgst -sha256 -hmac <testID>.
const wantDeviceSource = "dddc24d31d6d35592983d024cbbb760a62ab847ed623e2a07fd35410e2858694"

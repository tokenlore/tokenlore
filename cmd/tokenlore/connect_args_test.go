package main

import (
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/internal/testpki"
)

// splitAt teilt s an den angegebenen Stellen, wie es ein Zeilenumbruch beim Kopieren aus PuTTY tut.
func splitAt(s string, at ...int) []string {
	var parts []string
	last := 0
	for _, i := range at {
		parts = append(parts, s[last:i])
		last = i
	}
	return append(parts, s[last:])
}

func TestConnectJoinsSplitFingerprint(t *testing.T) {
	for _, at := range [][]int{{10}, {3, 40}, {47}, {48}} {
		cfg, data := connectEnv(t)
		f := newFakeServer(t)
		args := append([]string{f.srv.URL, "123456", "--ca"}, splitAt(f.fingerprint(), at...)...)
		args = append(args, "--name", "x")
		code, out := connect(t, data, cfg, args...)
		if code != 0 || !strings.Contains(out, "Verbunden mit") {
			t.Fatalf("geteilt bei %v: Exit %d: %s", at, code, out)
		}
	}
}

func TestConnectSplitWrongFingerprintStillRefused(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	other := client.Fingerprint(testpki.NewRoot(t).DER())
	args := append([]string{f.srv.URL, "123456", "--ca"}, splitAt(other, 20)...)
	code, out := connect(t, data, cfg, args...)
	if code != 1 || !strings.Contains(out, "Fingerabdruck stimmt nicht") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	assertNothingStored(t, data)
	if f.pairCalls.Load() != 0 {
		t.Fatal("Pair aufgerufen")
	}
}

func TestConnectArgumentMessages(t *testing.T) {
	fp := strings.Repeat("AB:", 31) + "AB"
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"nichts", nil, "Es fehlen Server-Adresse und Kopplungscode."},
		{"ohne Code", []string{"https://x.lan", "--ca", fp}, "Es fehlt der Kopplungscode (6 Ziffern)."},
		{"zu viel", []string{"https://x.lan", "123456", "extra", "--ca", fp}, "Unerwartete Angabe nach dem Kopplungscode: „extra“"},
		{"unbekannte Option", []string{"https://x.lan", "123456", "--cert", fp}, "Unbekannte Option --cert"},
		{"Option ohne Wert", []string{"https://x.lan", "123456", "--ca"}, "Option --ca braucht einen Wert"},
		{"zu kurz", []string{"https://x.lan", "123456", "--ca", "AB:CD:EF"}, "CA-Fingerabdruck ungültig: 6 statt 64 Hex-Zeichen"},
		{"kein Hex", []string{"https://x.lan", "123456", "--ca", strings.Repeat("ZZ:", 31) + "ZZ"}, "CA-Fingerabdruck ungültig: nur 0–9, A–F und „:“ erlaubt"},
	}
	for _, c := range cases {
		cfg, data := connectEnv(t)
		code, out := connect(t, data, cfg, c.args...)
		if code == 0 || !strings.Contains(out, c.want) {
			t.Errorf("%s: Exit %d, Ausgabe ohne %q:\n%s", c.name, code, c.want, out)
		}
		if c.name != "zu kurz" && c.name != "kein Hex" && !strings.Contains(out, "Aufruf: /tokenlore:connect") {
			t.Errorf("%s: Aufrufzeile fehlt:\n%s", c.name, out)
		}
		if strings.Contains(out, "flag provided") || strings.Contains(out, "Usage") {
			t.Errorf("%s: englische flag-Meldung:\n%s", c.name, out)
		}
		assertNothingStored(t, data)
	}
}

func TestConnectEchoIsCleaned(t *testing.T) {
	cfg, data := connectEnv(t)
	code, out := connect(t, data, cfg, "https://x.lan", "123456", "ex\x1b[31mtra\u202e", "--ca", strings.Repeat("AB", 32))
	if code == 0 || strings.ContainsAny(out, "\x1b\u202e") {
		t.Fatalf("Exit %d, ungereinigte Ausgabe: %q", code, out)
	}
}

// Der Befehl `admin pair-code` existiert nicht mehr; Hilfe und Fehlertext verweisen auf die Webseite.
func TestFingerprintHelpPointsToWebpage(t *testing.T) {
	msg := fingerprintProblem("AB:CD:EF")
	if strings.Contains(msg, "pair-code") || !strings.Contains(msg, "Webseite") {
		t.Errorf("Fehlertext: %q", msg)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/state"
)

// noEnv ist eine Umgebung ohne Variablen: kein Konto der Desktop-App.
func noEnv(string) string { return "" }

// appEnv ist eine Umgebung, in der die Desktop-App das Konto acc-app mit Adresse übergibt.
func appEnv(k string) string {
	switch k {
	case "CLAUDE_CODE_ACCOUNT_UUID":
		return "acc-app"
	case "CLAUDE_CODE_USER_EMAIL":
		return "erika.app@beispiel.de"
	}
	return ""
}

func emailCfg(t *testing.T) string {
	t.Helper()
	cj := filepath.Join(t.TempDir(), ".claude.json")
	os.WriteFile(cj, []byte(`{"oauthAccount":{"accountUuid":"acc-9","emailAddress":"max@beispiel.de"}}`), 0o600)
	return cj
}

// Die Ausgabe ohne Argument liest Claude im Chat mit: Sie nennt Stufe und Konto, nie eine Adresse.
func TestEmailShowsNoAddress(t *testing.T) {
	data, cj := t.TempDir(), emailCfg(t)
	for _, tc := range []struct {
		name   string
		getenv func(string) string
		want   []string
		never  []string
	}{
		{"Terminal", noEnv, []string{"E-Mail-Stufe: Aus (off)", "acc-9", "aus der Anmeldung im Terminal", "Adresse bekannt"},
			[]string{"max@", "ma…", "beispiel.de"}},
		{"Desktop-App", appEnv, []string{"acc-app", "aus der Desktop-App", "Adresse bekannt"},
			[]string{"erika", "er…", "beispiel.de", "acc-9"}},
	} {
		var out bytes.Buffer
		if code := runEmail(nil, &out, data, cj, tc.getenv); code != 0 {
			t.Fatalf("%s: Exit %d: %s", tc.name, code, out.String())
		}
		for _, w := range append(tc.want, "Ändern mit: /tokenlore:email off|short|full") {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: ohne %q:\n%s", tc.name, w, out.String())
			}
		}
		for _, n := range tc.never {
			if strings.Contains(out.String(), n) {
				t.Errorf("%s: Ausgabe nennt %q:\n%s", tc.name, n, out.String())
			}
		}
	}
}

func TestEmailJSONForChoice(t *testing.T) {
	data, cj := t.TempDir(), emailCfg(t)
	var out bytes.Buffer
	if code := runEmail([]string{"full"}, &out, data, cj, appEnv); code != 0 {
		t.Fatalf("Exit %d", code)
	}
	out.Reset()
	if code := runEmail([]string{"--json"}, &out, data, cj, appEnv); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	var c emailChoice
	if err := json.Unmarshal(out.Bytes(), &c); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if c.Mode != "full" || c.Account == nil || *c.Account != (emailAccount{From: "app", Short: "er…@beispiel.de", Full: "erika.app@beispiel.de"}) {
		t.Fatalf("got %+v %+v", c, c.Account)
	}

	// Ohne Adresse fehlt das Konto.
	noMail := filepath.Join(t.TempDir(), ".claude.json")
	os.WriteFile(noMail, []byte(`{"oauthAccount":{"accountUuid":"acc-9"}}`), 0o600)
	out.Reset()
	runEmail([]string{"--json"}, &out, data, noMail, noEnv)
	if strings.TrimSpace(out.String()) != `{"mode":"full"}` {
		t.Fatalf("ohne Adresse: %s", out.String())
	}
}

func TestEmailSet(t *testing.T) {
	data, cj := t.TempDir(), emailCfg(t)
	var out bytes.Buffer
	if code := runEmail([]string{"short"}, &out, data, cj, noEnv); code != 0 {
		t.Fatalf("Exit %d", code)
	}
	if !strings.Contains(out.String(), "Kurz (short)") {
		t.Fatalf("Meldung: %s", out.String())
	}
	st, _ := state.Load(data)
	if st.EmailMode != "short" {
		t.Fatalf("EmailMode %q", st.EmailMode)
	}
	if code := runEmail([]string{"quatsch"}, &out, data, cj, noEnv); code != 2 {
		t.Fatalf("unbekannte Stufe: Exit %d, want 2", code)
	}
}

// runOut ruft run auf und gibt zurück, was auf stdout landet.
func runOut(t *testing.T, args ...string) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	code := run(args)
	os.Stdout = old
	w.Close()
	var b bytes.Buffer
	b.ReadFrom(r)
	return b.String(), code
}

// Aus einem Slash-Befehl (--args) ist --json verboten: Claude läse sonst die volle Adresse mit. Direkt aufgerufen,
// wie vom Auswahlfenster, liefert es die Adresse.
func TestEmailJSONNotFromChat(t *testing.T) {
	t.Setenv("TOKENLORE_DATA_DIR", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("CLAUDE_CODE_ACCOUNT_UUID", "acc-app")
	t.Setenv("CLAUDE_CODE_USER_EMAIL", "erika.app@beispiel.de")
	for _, args := range [][]string{{"email", "--args", "--json"}, {"email", "--args", "full --json"}} {
		out, code := runOut(t, args...)
		if code != 2 || strings.Contains(out, "beispiel.de") {
			t.Fatalf("%q: Exit %d, Ausgabe %q", args, code, out)
		}
	}
	out, code := runOut(t, "email", "--json")
	if code != 0 || !strings.Contains(out, "erika.app@beispiel.de") {
		t.Fatalf("direkt: Exit %d, Ausgabe %q", code, out)
	}
	out, code = runOut(t, "email", "--args", "")
	if code != 0 || strings.Contains(out, "beispiel.de") || !strings.Contains(out, "acc-app") {
		t.Fatalf("Anzeige im Chat: Exit %d, Ausgabe %q", code, out)
	}
}

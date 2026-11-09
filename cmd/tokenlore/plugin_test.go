package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	allowedRe = regexp.MustCompile(`(?m)^allowed-tools: Bash\((.*):\*\)$`)
	bangRe    = regexp.MustCompile("(?m)^!`(.*)`$")
	hintRe    = regexp.MustCompile(`(?m)^argument-hint: (.*)$`)
)

// F5: Jeder Befehl erlaubt nur seinen eigenen Unterbefehl. Mit einem gemeinsamen Muster dürfte ein Folgezug
// des Modells über /tokenlore:status auch connect oder disconnect ausführen. Das Muster muss Präfix der
// !-Zeile sein, sonst fragt Claude Code bei jedem Aufruf nach.
func TestPluginCommandsAllowOnlyTheirSubcommand(t *testing.T) {
	for _, x := range []string{"connect", "sync", "status", "email", "disconnect"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "plugin", "commands", x+".md"))
		if err != nil {
			t.Fatal(err)
		}
		s := strings.ReplaceAll(string(b), "\r\n", "\n")
		want := `sh "${CLAUDE_PLUGIN_ROOT}/bin/tokenlore" ` + x
		a := allowedRe.FindAllStringSubmatch(s, -1)
		if len(a) != 1 || a[0][1] != want {
			t.Fatalf("%s.md: allowed-tools %q, want Bash(%s:*)", x, a, want)
		}
		bang := bangRe.FindStringSubmatch(s)
		if bang == nil || !strings.HasPrefix(bang[1], want+" ") {
			t.Fatalf("%s.md: !-Zeile %q beginnt nicht mit dem Muster", x, bang)
		}
	}
}

// $ARGUMENTS setzt Claude Code ohne Escaping in die !-Zeile ein. In einfachen Anführungszeichen wertet die Shell
// darin nichts aus (kein `;`, kein `$(…)`); tokenlore zerlegt den Text selbst (pluginArgs).
func TestPluginCommandsQuoteTheirArguments(t *testing.T) {
	for _, x := range []string{"connect", "sync", "status", "email", "disconnect"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "plugin", "commands", x+".md"))
		if err != nil {
			t.Fatal(err)
		}
		s := strings.ReplaceAll(string(b), "\r\n", "\n")
		bang := bangRe.FindStringSubmatch(s)
		want := `sh "${CLAUDE_PLUGIN_ROOT}/bin/tokenlore" ` + x + ` --args '$ARGUMENTS'`
		if bang == nil || bang[1] != want {
			t.Fatalf("%s.md: !-Zeile %q, want %q", x, bang, want)
		}
		if strings.Count(s, "$ARGUMENTS") != 1 {
			t.Fatalf("%s.md: $ARGUMENTS darf nur in der !-Zeile stehen", x)
		}
	}
}

// F10: --background ist nur für den Hook gedacht (stumm, Ergebnis nur in status.json) und wird nicht angeboten.
func TestPluginSyncOffersNoBackground(t *testing.T) {
	for _, p := range []string{filepath.Join("..", "..", "plugin", "commands", "sync.md"), filepath.Join("..", "..", "plugin", "README.md")} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "--background") {
			t.Fatalf("%s bietet --background an", p)
		}
	}
	b, _ := os.ReadFile(filepath.Join("..", "..", "plugin", "commands", "sync.md"))
	if h := hintRe.FindStringSubmatch(strings.ReplaceAll(string(b), "\r\n", "\n")); h == nil || h[1] != `""` {
		t.Fatalf("argument-hint %q", h)
	}
}

// connect.md stellt keine Rückfrage mehr: Ein anderer Rechner wird immer ein eigenes Gerät.
func TestPluginConnectHasNoMoveOrSecondComputer(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "plugin", "commands", "connect.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, bad := range []string{"Rückfrage", "Umzug", "--umzug", "--zweiter-rechner", "--resend-legacy"} {
		if strings.Contains(s, bad) {
			t.Errorf("connect.md nennt %q", bad)
		}
	}
	if h := hintRe.FindStringSubmatch(strings.ReplaceAll(s, "\r\n", "\n")); h == nil || !strings.Contains(h[1], "[--name <gerätename>]") {
		t.Errorf("argument-hint %q", h)
	}
}

// Beide Hooks rufen über den Starter ihren Unterbefehl auf: Sessionstart und Sitzungsende.
func TestPluginHooks(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "plugin", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var h struct {
		Hooks map[string][]struct {
			Hooks []struct{ Type, Command string }
		}
	}
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	for event, sub := range map[string]string{"SessionStart": "session-start", "SessionEnd": "session-end"} {
		groups := h.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("%s: %+v", event, groups)
		}
		hk := groups[0].Hooks[0]
		if want := `sh "${CLAUDE_PLUGIN_ROOT}/bin/tokenlore" ` + sub; hk.Type != "command" || hk.Command != want {
			t.Fatalf("%s: %+v, want %q", event, hk, want)
		}
	}
}

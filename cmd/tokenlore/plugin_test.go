package main

import (
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

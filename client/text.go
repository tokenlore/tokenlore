package client

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tokenlore/tokenlore/protocol"
)

// maxTextRunes begrenzt Texte vom Server, bevor sie dem Nutzer gezeigt werden.
const maxTextRunes = 200

// Clean entfernt Steuer- und Formatzeichen aus einem Text vom Server und kürzt ihn auf 200 Zeichen.
// Der Server ist nicht vertrauenswürdig genug, um Terminal-Escapes oder Richtungszeichen auszugeben.
func Clean(s string) string {
	return truncate(strip(s))
}

func strip(s string) string {
	var b strings.Builder
	for _, r := range s {
		// Ungültiges UTF-8 kommt als RuneError an und fliegt ebenfalls raus.
		if r == utf8.RuneError || unicode.IsControl(r) || protocol.HasSpoofRune(string(r)) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func truncate(s string) string {
	n := 0
	for i := range s {
		if n == maxTextRunes {
			return s[:i]
		}
		n++
	}
	return s
}

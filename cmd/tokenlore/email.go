package main

import (
	"fmt"
	"io"

	"github.com/tokenlore/tokenlore/email"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// runEmail zeigt ohne Argument für jede Stufe genau das, was gesendet würde, und setzt mit Argument die Stufe.
func runEmail(args []string, out io.Writer, dataDir, claudeJSON string) int {
	st, err := state.Load(dataDir)
	if err != nil {
		fmt.Fprintf(out, "Merkzettel nicht lesbar: %v\n", err)
		return 1
	}
	if len(args) == 0 {
		acc, ok, err := claudecode.ReadCurrentAccount(claudeJSON)
		if err != nil || !ok || acc.Email == "" {
			fmt.Fprintln(out, "Kein angemeldetes Konto mit E-Mail-Adresse gefunden.")
		} else {
			fmt.Fprintln(out, "So würde die Adresse des angemeldeten Kontos gesendet:")
			fmt.Fprintf(out, "  off   → (nichts)\n  short → %s\n  full  → %s\n", email.Mask(acc.Email), acc.Email)
		}
		fmt.Fprintf(out, "Aktuell: %s\nÄndern mit: /tokenlore:email off|short|full\n", st.EmailMode)
		return 0
	}
	switch args[0] {
	case "off", "short", "full":
		st.EmailMode = args[0]
		if err := st.Save(dataDir); err != nil {
			fmt.Fprintf(out, "Speichern fehlgeschlagen: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "E-Mail-Stufe ist jetzt %s.\n", args[0])
		return 0
	default:
		fmt.Fprintf(out, "Unbekannte Stufe %q. Erlaubt: off, short, full.\n", args[0])
		return 2
	}
}

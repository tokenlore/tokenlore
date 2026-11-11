package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/tokenlore/tokenlore/email"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// emailLevels sind die Stufen mit ihren deutschen Namen.
var emailLevels = map[string]string{"off": "Aus", "short": "Kurz", "full": "Voll"}

// emailAccount ist das Konto dieser Sitzung in der Ausgabe von `email --json`.
type emailAccount struct {
	From  string `json:"from"`
	Short string `json:"short"`
	Full  string `json:"full"`
}

// emailChoice ist die Ausgabe von `email --json`. Sie liest nur das Auswahlfenster des Plugins; was ein Befehl im
// Chat ausgibt, liest Claude mit, deshalb nennt die Ausgabe ohne `--json` nie eine Adresse.
type emailChoice struct {
	Mode    string        `json:"mode"`
	Account *emailAccount `json:"account,omitempty"`
}

// runEmail zeigt ohne Argument die Stufe und das Konto dieser Sitzung, ohne Adresse, und setzt mit Argument die
// Stufe. `--json` gibt Stufe und Adresse des Kontos dieser Sitzung für das Auswahlfenster aus.
func runEmail(args []string, out io.Writer, dataDir, claudeJSON string, getenv func(string) string) int {
	st, err := state.Load(dataDir)
	if err != nil {
		fmt.Fprintf(out, "Merkzettel nicht lesbar: %v\n", err)
		return 1
	}
	mode := st.EmailMode
	if _, ok := emailLevels[mode]; !ok {
		mode = "off"
	}
	acc, accOK, accErr := claudecode.SessionAccount(claudeJSON, getenv)
	accOK = accOK && accErr == nil
	if len(args) == 0 {
		fmt.Fprintf(out, "E-Mail-Stufe: %s (%s)\n", emailLevels[mode], mode)
		if accOK {
			from := "aus der Anmeldung im Terminal"
			if acc.From == claudecode.FromApp {
				from = "aus der Desktop-App"
			}
			known := "Adresse bekannt"
			if email.Mask(acc.Email) == "" {
				known = "keine Adresse bekannt"
			}
			fmt.Fprintf(out, "Konto dieser Sitzung: %s… %s, %s\n", shortID(acc.AccountID), from, known)
		} else {
			fmt.Fprintln(out, "Kein angemeldetes Konto gefunden.")
		}
		fmt.Fprintln(out, "Ändern mit: /tokenlore:email off|short|full")
		return 0
	}
	switch args[0] {
	case "--json":
		c := emailChoice{Mode: mode}
		if short := email.Mask(acc.Email); accOK && short != "" {
			c.Account = &emailAccount{From: acc.From, Short: short, Full: acc.Email}
		}
		b, err := json.Marshal(c)
		if err != nil {
			fmt.Fprintln(out, err)
			return 1
		}
		fmt.Fprintln(out, string(b))
		return 0
	case "off", "short", "full":
		st.EmailMode = args[0]
		if err := st.Save(dataDir); err != nil {
			fmt.Fprintf(out, "Speichern fehlgeschlagen: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "E-Mail-Stufe ist %s (%s).\n", emailLevels[args[0]], args[0])
		return 0
	default:
		fmt.Fprintf(out, "Unbekannte Stufe %q. Erlaubt: off, short, full.\n", args[0])
		return 2
	}
}

// shortID kürzt eine Kontokennung auf ihre ersten 8 Zeichen.
func shortID(id string) string {
	if r := []rune(id); len(r) > 8 {
		return string(r[:8])
	}
	return id
}

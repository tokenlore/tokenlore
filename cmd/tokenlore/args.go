package main

import (
	"errors"
	"strings"
)

// argsFlag kennzeichnet die Argumente eines Plugin-Befehls: Die Befehlsdateien geben $ARGUMENTS als ein einziges
// Argument in einfachen Anführungszeichen weiter (`--args '$ARGUMENTS'`). So wertet die Shell darin nichts aus
// (kein `;`, kein `$(…)`), und tokenlore zerlegt den Text selbst. Nur ein einfaches Anführungszeichen im Text bricht
// die Klammer auf, denn Claude Code setzt $ARGUMENTS ohne Escaping ein. Diesen Fall erkennt pluginArgs und lehnt ihn ab.
// Was die Shell vorher daraus macht, liegt aber außerhalb von tokenlore.
const argsFlag = "--args"

var (
	errUnclosedQuote = errors.New("Ein Anführungszeichen ist nicht geschlossen.")
	// Ein einfaches Anführungszeichen beendet die Klammer der Befehlsdatei; was danach kommt, sieht die Shell einzeln.
	errSingleQuote = errors.New("Einfache Anführungszeichen (') sind in den Angaben nicht erlaubt. Für Leerzeichen im Namen doppelte nehmen: --name \"Mein PC\".")
)

// pluginArgs liefert die Argumente nach dem Befehlsnamen: bei `<befehl> --args <text>` den zerlegten Text,
// sonst die Argumente unverändert. Mehr als ein Text nach --args heißt: Ein einfaches Anführungszeichen hat die
// Klammer der Befehlsdatei aufgebrochen; dann wird nichts ausgeführt.
func pluginArgs(args []string) ([]string, error) {
	if len(args) == 0 || args[0] != argsFlag {
		return args, nil
	}
	if len(args) != 2 {
		return nil, errSingleQuote
	}
	return splitArgs(args[1])
}

// splitArgs zerlegt an Leerraum; doppelte Anführungszeichen fassen Leerraum zu einem Argument zusammen
// (`--name "Mein PC"`). Andere Sonderzeichen haben keine Bedeutung.
func splitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQuote, started := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote, started = !inQuote, true
		case !inQuote && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if inQuote {
		return nil, errUnclosedQuote
	}
	if started {
		out = append(out, cur.String())
	}
	return out, nil
}

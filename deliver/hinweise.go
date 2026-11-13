package deliver

import (
	"fmt"

	"github.com/tokenlore/tokenlore/state"
)

// MsgHeldForServer nennen sync, status und der Sessionstart, wenn die Lieferung vor Zeilen anhält, die der Server
// noch nicht annimmt (Merkmal request_details fehlt).
const MsgHeldForServer = "Dein tokenlore-Server kennt die neueren Angaben aus Claude Code (etwa API-Fehler) noch nicht. Deine Zahlen ab da warten hier. Lass den Server aktualisieren, dann geht der Rest beim nächsten Abgleich mit."

// Notices sind die Hinweise zum letzten Abgleich, für status und den Sessionstart. Die des Servers nennen nur
// Anzahlen: wem die Anfragen gehören oder welches Gerät die Tage hat, sagt der Server nie.
func Notices(stat *state.Status) []string {
	var out []string
	if stat.HeldForServer {
		out = append(out, MsgHeldForServer)
	}
	if n := stat.LastKnown; n > 0 {
		out = append(out, fmt.Sprintf("%s schon auf dem Server und %s nicht doppelt.",
			plural(n, "1 Anfrage war", fmt.Sprintf("%d Anfragen waren", n)), plural(n, "zählt", "zählen")))
	}
	if n := stat.LastElsewhere; n > 0 {
		out = append(out, fmt.Sprintf("%s schon bei einem anderen Benutzer und %s dort, nicht bei dir. Neue Arbeit auf diesem Rechner zählt normal.",
			plural(n, "1 Anfrage lag", fmt.Sprintf("%d Anfragen lagen", n)), plural(n, "zählt", "zählen")))
	}
	if n := stat.LastLegacyRejected; n > 0 {
		out = append(out, fmt.Sprintf("%s aus der Zeit vor dem Koppeln hat der Server nicht übernommen, weil ein anderes Gerät dort genau dieselben Zahlen hat. %s nicht erneut geschickt, bis du neu koppelst.",
			plural(n, "1 Tag", fmt.Sprintf("%d Tage", n)), plural(n, "Er wird", "Sie werden")))
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

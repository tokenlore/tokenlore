package deliver

import (
	"fmt"

	"github.com/tokenlore/tokenlore/state"
)

// Notices sind die Hinweise des Servers zum letzten Abgleich, für status und den Sessionstart. Sie nennen nur
// Anzahlen: wem die Anfragen gehören oder welches Gerät die Tage hat, sagt der Server nie.
func Notices(stat *state.Status) []string {
	var out []string
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

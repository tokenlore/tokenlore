// Paket deliver gleicht neue Nutzungszahlen mit dem Server ab: einlesen, ins Archiv schreiben und aus dem Archiv
// liefern, was laut geliefert.json noch offen ist.
package deliver

import (
	"context"
	"fmt"
	"time"

	"github.com/tokenlore/tokenlore/device"
	"github.com/tokenlore/tokenlore/source"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// Prepare liest die fertigen Anfragen ab den Leseständen in st. st bleibt unverändert; die neuen Lesestände stehen
// in ScanResult.Next. identity ist die Kennung, mit der gekoppelt wurde (leer = nicht prüfen). ended sind die
// Hauptdateien beendeter Sitzungen (claudecode.EndedRel), deren letzte Anfragen sofort fertig sind.
func Prepare(ctx context.Context, configDir, identity string, st *state.State, now func() time.Time, ended ...string) (source.ScanResult, error) {
	_, loc := device.LocalTZ()
	src := &claudecode.ClaudeCode{ConfigDir: configDir, Loc: loc, Now: now, PairedIdentity: identity, Ended: ended}
	res, err := src.Scan(ctx, st)
	if err != nil {
		return res, fmt.Errorf("Einlesen fehlgeschlagen: %w", err)
	}
	return res, nil
}

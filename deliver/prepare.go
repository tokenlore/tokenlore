// Paket deliver gleicht neue Nutzungszahlen mit dem Server ab: einlesen, aufteilen, senden,
// und den Merkzettel erst speichern, wenn alle Pakete angenommen sind.
package deliver

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/tokenlore/tokenlore/device"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/source"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// Prepare liest neue Nutzungszahlen ab dem Merkzettel st, setzt Version und Gerätedaten und teilt
// die Lieferung in Pakete. st bleibt unverändert; der neue Stand steht in ScanResult.Next.
// Ungültige Gerätedaten sind ein Fehler: dann wird nichts gesendet.
func Prepare(ctx context.Context, configDir, version string, st *state.State, now func() time.Time) (source.ScanResult, []protocol.Batch, error) {
	tzName, loc := device.LocalTZ()
	src := &claudecode.ClaudeCode{ConfigDir: configDir, Loc: loc, Now: now}
	res, err := src.Scan(ctx, st)
	if err != nil {
		return res, nil, fmt.Errorf("Einlesen fehlgeschlagen: %w", err)
	}
	res.Batch.V = protocol.Version
	res.Batch.Device = protocol.DeviceInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, TZ: tzName, PluginVersion: version}
	if err := protocol.ValidateDevice(res.Batch.Device); err != nil {
		return res, nil, fmt.Errorf("Gerätedaten ungültig: %w", err)
	}
	parts, err := protocol.Split(res.Batch)
	if err != nil {
		return res, nil, fmt.Errorf("Aufteilen fehlgeschlagen: %w", err)
	}
	return res, parts, nil
}

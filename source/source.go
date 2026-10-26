// Paket source beschreibt Quellen von Nutzungszahlen (Claude Code ist die erste).
package source

import (
	"context"

	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

// ScanResult ist das Ergebnis eines Einlesevorgangs. Next wird erst nach erfolgreicher Lieferung gespeichert.
type ScanResult struct {
	Batch    protocol.Batch
	Next     *state.State
	Warnings []string
}

// Source ist eine Quelle von Nutzungszahlen.
type Source interface {
	Name() string
	Scan(ctx context.Context, st *state.State) (ScanResult, error)
}

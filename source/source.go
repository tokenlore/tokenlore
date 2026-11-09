// Paket source beschreibt Quellen von Nutzungszahlen (Claude Code ist die erste).
package source

import (
	"context"

	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

// ScanResult ist das Ergebnis eines Einlesevorgangs. Next (die neuen Lesestände) wird gespeichert, sobald die
// gelesenen Anfragen im Archiv stehen.
type ScanResult struct {
	Next     *state.State
	Warnings []string
	// Lines sind die Inhaltszeilen der fertigen Anfragen, mit der festen Feldliste aus protocol/doc.md. account_id
	// steht nur, wenn das Protokoll das Konto der Sitzung nennt (ownerAccountUuid); sonst ergänzt es das Archiv.
	Lines []protocol.RequestLine
	// NewFields sind die Namen unbekannter Felder in den gelesenen Anfragezeilen, sortiert, höchstens
	// state.MaxNewFields; nie ihre Werte.
	NewFields []string
	// HeldMonths sind die Monate (UTC, JJJJ-MM) der Anfragen, die noch nicht fertig sind und deshalb weder in Lines
	// stehen; solche Monate packt das Archiv noch nicht.
	HeldMonths []string
	// CurrentAccountID und CurrentEmail sind das in Claude Code angemeldete Konto mit voller Adresse, leer wenn keins.
	// Die Adresse gehört nur ins lokale Archiv (konten.jsonl), nie ungekürzt in ein Paket.
	CurrentAccountID, CurrentEmail string
	// IdentityChanged: Die Kennungsdatei passt nicht zur Kennung, mit der gekoppelt wurde. Geliefert wird dann gar
	// nichts, bis neu gekoppelt ist (deliver.CheckDevice).
	IdentityChanged bool
}

// Source ist eine Quelle von Nutzungszahlen.
type Source interface {
	Name() string
	Scan(ctx context.Context, st *state.State) (ScanResult, error)
}

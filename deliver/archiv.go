package deliver

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/source"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// MsgArchiveRecreated meldet status, wenn der Abgleich das Archiv neu anlegen musste, weil der Ordner fehlte.
const MsgArchiveRecreated = "Archiv fehlte, neu angelegt; alles, was noch in den Sitzungsdateien und in stats-cache.json steht, ist wieder übernommen."

var errNoIdentity = errors.New("Kennung dieses Rechners fehlt (tokenlore-id), bitte neu koppeln.")

// archiveIdentity ist die Kennung, nach der der Archivordner heißt: die Kennungsdatei, sonst ihre Kopie im Merkzettel.
func archiveIdentity(configDir string, st *state.State, paired string) (state.Identity, error) {
	ic, err := claudecode.CheckIdentity(configDir, st.Identity, paired)
	if err == nil && ic.Known {
		return ic.Current, nil
	}
	if st.Identity != nil && st.Identity.ID != "" {
		return *st.Identity, nil
	}
	return state.Identity{}, errNoIdentity
}

// ArchiveDir ist der Archivordner dieses Rechners im Datenordner dataDir: benannt nach der Kennungsdatei, sonst nach
// ihrer Kopie im Merkzettel. Ohne Kennung gibt es kein Archiv.
func ArchiveDir(dataDir, configDir string, st *state.State, paired string) (string, error) {
	id, err := archiveIdentity(configDir, st, paired)
	if err != nil {
		return "", err
	}
	return state.ArchiveDir(dataDir, id.ID), nil
}

// fillArchive übernimmt die fertigen Anfragen eines Einlesevorgangs ins Archiv, ergänzt konten.jsonl, übernimmt
// beim ersten Mal die Vorgeschichte und packt abgeschlossene Monate. Ein Fehler bricht den Abgleich ab; die
// Warnungen gehen nur in den lokalen Status.
func fillArchive(dir string, key ed25519.PrivateKey, now func() time.Time, configDir string, res source.ScanResult) ([]string, error) {
	a, err := archive.Open(dir, key, now)
	if err != nil {
		return nil, err
	}
	var warns []string
	r, err := a.AddRequests(res.Lines, res.CurrentAccountID)
	if err != nil {
		return nil, err
	}
	if r.Late > 0 {
		warns = append(warns, fmt.Sprintf("Archiv: %d Anfrage(n) nach dem Archivieren weitergeschrieben; die Archivzeile bleibt unverändert", r.Late))
	}
	if err := a.AddAccounts(r.Accounts, res.CurrentAccountID, res.CurrentEmail); err != nil {
		return nil, fmt.Errorf("%s: %w", archive.AccountsFile, err)
	}
	if !a.LegacyDone() {
		days, err := claudecode.LegacyDays(configDir)
		if err != nil {
			// Beim nächsten Abgleich erneut versuchen; die Vorgeschichte gilt erst als übernommen, wenn die Datei steht.
			warns = append(warns, fmt.Sprintf("Vorgeschichte nicht übernommen, stats-cache.json nicht lesbar: %v", err))
		} else {
			_, dropped, err := a.AddLegacy(days)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", archive.LegacyFile, err)
			}
			if dropped > 0 {
				warns = append(warns, fmt.Sprintf("Vorgeschichte: %d ungültige Tageswerte ausgelassen", dropped))
			}
		}
	}
	// Aufräumen ist nur Ordnung: Ein Fehler kostet keine Anfrage, also nur eine Warnung.
	if err := a.PruneSessionStarts(); err != nil {
		warns = append(warns, fmt.Sprintf("%s nicht aufgeräumt: %v", archive.SessionsFile, err))
	}
	if _, err := a.Pack(res.HeldMonths); err != nil {
		return nil, fmt.Errorf("Monat packen: %w", err)
	}
	return warns, nil
}

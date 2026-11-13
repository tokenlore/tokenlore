package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const (
	statusFile  = "status.json"
	maxWarnings = 20
)

// Status ist das Ergebnis des letzten Abgleichs, nur zur lokalen Anzeige.
// Warnungen enthalten Pfade und gehen nie an den Server.
type Status struct {
	// LastAttempt ist der letzte Versuch eines Abgleichs: gesetzt von jedem Abgleich (Sessionstart, sync, connect) und
	// vom Sitzungsende, bevor es seinen Abgleich startet. Das Sitzungsende gleicht nur ab, wenn er 5 Minuten zurückliegt.
	LastAttempt time.Time `json:"last_attempt"`
	LastSuccess time.Time `json:"last_success"`
	LastError   string    `json:"last_error,omitempty"`
	// LastErrorOldDatabase: LastError ist die Meldung eines Servers, dessen Datenbank von einer älteren Version stammt
	// (503 mit reason "old_database"). Der Sessionstart zeigt sie dann wie die Meldung zu einem 426.
	LastErrorOldDatabase bool     `json:"last_error_old_database,omitempty"`
	LastRequests         int      `json:"last_requests"`
	LastParts            int      `json:"last_parts"`
	Flagged              int      `json:"flagged"`
	Warnings             []string `json:"warnings,omitempty"`
	// KeyRejected ist die Meldung zum letzten 401 beim Liefern; leer, sobald wieder eine Lieferung gelingt.
	KeyRejected string `json:"key_rejected,omitempty"`
	// LastKnown, LastElsewhere und LastLegacyRejected sind die Hinweise des Servers zum letzten erfolgreichen
	// Abgleich: Anfragen, die er schon hatte, Anfragen, die schon einem anderen Benutzer gehören, und neu abgewiesene
	// Vorgeschichte-Tage. 0 ohne Treffer.
	LastKnown          int `json:"last_known,omitempty"`
	LastElsewhere      int `json:"last_elsewhere,omitempty"`
	LastLegacyRejected int `json:"last_legacy_rejected,omitempty"`
	// HeldForServer: Der letzte erfolgreiche Abgleich hat vor Zeilen angehalten, die der Server noch nicht annimmt
	// (ihm fehlt das Merkmal request_details); sie warten im Archiv.
	HeldForServer bool `json:"held_for_server,omitempty"`
	// NewFields sind die Namen neuer Felder, die in den Sitzungsdateien aufgetaucht sind und die das Plugin nicht
	// übernimmt (nie ihre Werte), gesammelt über alle Abgleiche, sortiert, höchstens MaxNewFields.
	NewFields []string `json:"new_fields,omitempty"`
	// ArchiveRecreated ist der Zeitpunkt, zu dem der Abgleich das fehlende Archiv neu angelegt hat; null, wenn nie.
	ArchiveRecreated time.Time `json:"archive_recreated,omitzero"`
}

// MaxNewFields ist die Höchstzahl gemerkter Namen neuer Felder.
const MaxNewFields = 32

// AddNewFields nimmt Namen neuer Felder in NewFields auf: ohne Doppelte, sortiert, höchstens MaxNewFields.
func (s *Status) AddNewFields(names []string) {
	all := append(slices.Clone(s.NewFields), names...)
	slices.Sort(all)
	all = slices.Compact(all)
	if len(all) > MaxNewFields {
		all = all[:MaxNewFields]
	}
	if len(all) == 0 {
		all = nil
	}
	s.NewFields = all
}

// LoadStatus liest den Status; fehlt er, kommt ein leerer zurück.
func LoadStatus(dir string) (*Status, error) {
	s := &Status{}
	b, err := os.ReadFile(filepath.Join(dir, statusFile))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	return s, nil
}

// Save schreibt den Status atomar und behält nur die letzten 20 Warnungen.
func (s *Status) Save(dir string) error {
	c := *s
	if len(c.Warnings) > maxWarnings {
		c.Warnings = c.Warnings[len(c.Warnings)-maxWarnings:]
	}
	b, err := json.MarshalIndent(&c, "", " ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(dir, statusFile, b)
}

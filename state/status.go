package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	statusFile  = "status.json"
	maxWarnings = 20
)

// Status ist das Ergebnis des letzten Abgleichs, nur zur lokalen Anzeige.
// Warnungen enthalten Pfade und gehen nie an den Server.
type Status struct {
	LastAttempt  time.Time `json:"last_attempt"`
	LastSuccess  time.Time `json:"last_success"`
	LastError    string    `json:"last_error,omitempty"`
	LastRequests int       `json:"last_requests"`
	LastParts    int       `json:"last_parts"`
	Flagged      int       `json:"flagged"`
	Warnings     []string  `json:"warnings,omitempty"`
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

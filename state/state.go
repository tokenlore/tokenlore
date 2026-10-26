// Paket state hält den Merkzettel von tokenlore: Lesestände, Konto je Sitzung, Einstellungen.
package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
)

const fileName = "state.json"

// FileMark ist der Lesestand einer Sitzungsdatei.
type FileMark struct {
	Offset  int64  `json:"offset"`   // ab hier wird beim nächsten Mal gelesen
	End     int64  `json:"end"`      // Ende der zuletzt gelesenen vollständigen Zeile; gleich Dateigröße ⇒ nichts Neues
	HeadLen int    `json:"head_len"` // min(4096, Dateigröße beim Merken)
	HeadSum string `json:"head_sum"` // hex sha256 der ersten HeadLen Bytes
}

// State ist der gesamte Merkzettel.
type State struct {
	Files       map[string]FileMark `json:"files"`    // Pfad relativ zum Konfigurationsordner, mit "/"
	Sessions    map[string]string   `json:"sessions"` // sessionId → accountUuid
	EmailMode   string              `json:"email_mode"`
	Salt        string              `json:"salt,omitempty"`
	LegacyDone  bool                `json:"legacy_done"`
	LegacySent  bool                `json:"legacy_sent"`            // ein Paket mit Altbestand wurde je von einem Server angenommen
	Delivered   bool                `json:"delivered"`              // mindestens eine Lieferung vollständig erfolgreich
	AccountsSum string              `json:"accounts_sum,omitempty"` // hex sha256 der zuletzt gelieferten Kontenliste
}

// Load liest den Merkzettel; fehlt er, gibt es einen leeren mit E-Mail-Stufe "off".
func Load(dir string) (*State, error) {
	s := &State{}
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(b, s); err != nil {
			return nil, err
		}
	}
	if s.Files == nil {
		s.Files = map[string]FileMark{}
	}
	if s.Sessions == nil {
		s.Sessions = map[string]string{}
	}
	if s.EmailMode == "" {
		s.EmailMode = "off"
	}
	if err := s.mergeSessionLog(dir); err != nil {
		return nil, err
	}
	return s, nil
}

// Save schreibt den Merkzettel atomar (temporäre Datei, dann umbenennen), nur für den Benutzer lesbar.
func (s *State) Save(dir string) error {
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(dir, fileName, b)
}

// Clone liefert eine unabhängige Kopie.
func (s *State) Clone() *State {
	c := *s
	c.Files = maps.Clone(s.Files)
	c.Sessions = maps.Clone(s.Sessions)
	return &c
}

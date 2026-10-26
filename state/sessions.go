package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// sessionLog nimmt die Konto-Zuordnung beim Sessionstart auf. Sie wird nur angehängt, damit ein
// gleichzeitig laufender Abgleich, der state.json schreibt, keinen Eintrag verliert.
const sessionLog = "session-accounts.jsonl"

type sessionEntry struct {
	SessionID string `json:"s"`
	AccountID string `json:"a"`
}

// RecordSession hängt die Zuordnung Sitzung → Konto an.
func RecordSession(dir, sessionID, accountID string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(sessionEntry{sessionID, accountID})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, sessionLog), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// mergeSessionLog übernimmt Einträge, die im Merkzettel noch fehlen. Kaputte oder überlange
// Zeilen werden übersprungen, damit ein einzelner Fehler den Abgleich nie dauerhaft blockiert.
func (s *State) mergeSessionLog(dir string) error {
	f, err := os.Open(filepath.Join(dir, sessionLog))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var e sessionEntry
		if len(line) > 0 && json.Unmarshal(line, &e) == nil && e.SessionID != "" && e.AccountID != "" {
			if _, ok := s.Sessions[e.SessionID]; !ok {
				s.Sessions[e.SessionID] = e.AccountID
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

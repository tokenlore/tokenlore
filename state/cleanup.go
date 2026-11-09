package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// OldSessionLog ist die Zuordnung Sitzung → Konto früherer Fassungen. Das Konto steht heute je Anfrage im Archiv,
// das Konto beim Sitzungsstart in sitzungen.jsonl im Archivordner.
const OldSessionLog = "session-accounts.jsonl"

// RemoveOldFiles entfernt Dateien früherer Fassungen aus dem Datenordner dir. Fehlt eine, ist das kein Fehler.
// Alte Felder in state.json fallen beim nächsten Save weg, weil der Merkzettel nur bekannte Felder schreibt.
func RemoveOldFiles(dir string) error {
	err := os.Remove(filepath.Join(dir, OldSessionLog))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

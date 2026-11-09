package secret

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tokenlore/tokenlore/state"
)

func saveFile(dir, name, key string) error {
	return state.WriteFileAtomic(dir, name, []byte(key))
}

// loadFile liest eine Schlüsseldatei; fehlt sie, ist der Fehler errAbsent.
func loadFile(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", errAbsent
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func removeFile(dir, name string) error {
	err := os.Remove(filepath.Join(dir, name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

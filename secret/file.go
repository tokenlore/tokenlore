package secret

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tokenlore/tokenlore/state"
)

func saveFile(dir, key string) error {
	return state.WriteFileAtomic(dir, fileName, []byte(key))
}

func loadFile(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func removeFile(dir string) error {
	err := os.Remove(filepath.Join(dir, fileName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

package state

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic schreibt eine Datei atomar (temporäre Datei, dann umbenennen), nur für den Benutzer lesbar.
// Ein Abbruch mittendrin hinterlässt nie eine halbe Datei.
func WriteFileAtomic(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Unter Windows wirkt Chmod nur auf das Schreibschutz-Bit; dort schützt der Benutzerordner.
	_ = tmp.Chmod(0o600)
	// Sync vor Close: nach einem Absturz soll keine leere Datei die alte ersetzen.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

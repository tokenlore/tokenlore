package state

import (
	"os"
	"path/filepath"
	"testing"
)

// Alte Dateien früherer Fassungen verschwinden aus dem Datenordner; alles andere bleibt.
func TestRemoveOldFiles(t *testing.T) {
	dir := t.TempDir()
	keep := []string{"state.json", "connection.json", "status.json", "device.key", "sign.key", filepath.Join("archiv", "rechner-x", "sitzungen.jsonl")}
	for _, n := range append([]string{OldSessionLog}, keep...) {
		p := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveOldFiles(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, OldSessionLog)); !os.IsNotExist(err) {
		t.Fatalf("%s noch da: %v", OldSessionLog, err)
	}
	for _, n := range keep {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s fehlt: %v", n, err)
		}
	}
	// Ein zweiter Lauf ohne alte Dateien ist kein Fehler.
	if err := RemoveOldFiles(dir); err != nil {
		t.Fatal(err)
	}
}

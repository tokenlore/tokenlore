package state

import (
	"os"
	"path/filepath"
)

// DataDir ist das feste Datenverzeichnis von tokenlore: $TOKENLORE_DATA_DIR, sonst <UserConfigDir>/tokenlore.
// CLAUDE_PLUGIN_DATA wird bewusst nicht beachtet: Aufrufe aus Plugin und Shell sollen immer dieselben Daten sehen.
func DataDir() (string, error) {
	if d := os.Getenv("TOKENLORE_DATA_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tokenlore"), nil
}

// DeviceDir ist gerätweit und hängt nicht vom Claude-Konfigurationsordner ab (Sperrdatei).
func DeviceDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tokenlore"), nil
}

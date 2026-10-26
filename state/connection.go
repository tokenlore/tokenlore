package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const connectionFile = "connection.json"

// Connection ist die Kopplung dieses Geräts mit dem Server. Der Geräteschlüssel steht hier nie drin,
// nur der Name der Ablage (KeyStore).
type Connection struct {
	ServerURL     string    `json:"server_url"`
	CAPEM         string    `json:"ca_pem"`
	CAFingerprint string    `json:"ca_fingerprint"`
	DeviceID      int64     `json:"device_id"`
	DeviceName    string    `json:"device_name"`
	KeyStore      string    `json:"key_store"` // "keyring" oder "file"
	KeySum        string    `json:"key_sum"`   // hex sha256 des Geräteschlüssels, prüft Load
	ConnectedAt   time.Time `json:"connected_at"`
}

// LoadConnection liest die Verbindung; fehlt sie, ist ok=false und kein Fehler.
func LoadConnection(dir string) (*Connection, bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, connectionFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	c := &Connection{}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, false, err
	}
	return c, true, nil
}

// Save schreibt die Verbindung atomar, nur für den Benutzer lesbar.
func (c *Connection) Save(dir string) error {
	b, err := json.MarshalIndent(c, "", " ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(dir, connectionFile, b)
}

// RemoveConnection löscht die Verbindung; eine fehlende Datei ist kein Fehler.
func RemoveConnection(dir string) error {
	err := os.Remove(filepath.Join(dir, connectionFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

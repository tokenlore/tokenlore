// Paket secret verwahrt den Geräteschlüssel: im Schlüsselspeicher des Systems, sonst in einer Datei nur für den Benutzer.
package secret

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tokenlore/tokenlore/protocol"
)

const (
	KindKeyring = "keyring"
	KindFile    = "file"

	service  = "tokenlore"
	fileName = "device.key"
)

var ErrNotFound = errors.New("Geräteschlüssel nicht gefunden")

var (
	errBadKey      = errors.New("Geräteschlüssel hat ein ungültiges Format")
	errKeyMismatch = errors.New("Geräteschlüssel passt nicht zur Verbindung")
)

// keyringUser bindet den Eintrag an das Datenverzeichnis, damit eine zweite Instanz (anderes
// TOKENLORE_DATA_DIR) den Schlüssel der ersten weder überschreibt noch löscht.
func keyringUser(dataDir string) string {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		abs = dataDir
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return "device-key:" + hex.EncodeToString(sum[:])[:16]
}

func sumOf(key string) string {
	s := sha256.Sum256([]byte(key))
	return hex.EncodeToString(s[:])
}

// Save legt den Schlüssel ab und gibt die Ablage und den SHA-256 des Schlüssels (hex) zurück.
// Der Schlüsselspeicher kommt vor der Datei: er verschlüsselt den Schlüssel im Ruhezustand und hält ihn aus
// Datensicherungen und Kopien des Datenverzeichnisses heraus. Vor anderen Prozessen desselben Benutzers
// schützt er NICHT (macOS "security", Windows CredRead, Linux Secret Service mit "plain"-Sitzung).
// Die Datei ist nur der Ausweg, wenn kein (funktionierender) Schlüsselspeicher da ist.
// Fehlertexte enthalten nie den Schlüssel.
func Save(dataDir, key string) (kind, keySum string, err error) {
	if !protocol.DeviceKeyPattern.MatchString(key) {
		return "", "", errBadKey
	}
	keySum = sumOf(key)
	useKeyring := os.Getenv("TOKENLORE_SECRET_STORE") != "file"
	if useKeyring {
		if err := keyringSet(keyringUser(dataDir), key); err == nil {
			// Eine ältere Datei würde den Schlüssel offen herumliegen lassen. Scheitert das Löschen, bleibt
			// der Schlüssel trotzdem im Schlüsselspeicher registriert (nie verwaisen); Remove räumt beide Ablagen.
			_ = removeFile(dataDir)
			return KindKeyring, keySum, nil
		}
	}
	if err := saveFile(dataDir, key); err != nil {
		return "", "", fmt.Errorf("Geräteschlüssel speichern: %w", err)
	}
	if useKeyring {
		// Ein älterer, womöglich noch gültiger Schlüssel unter demselben Eintrag soll nicht liegen bleiben,
		// wenn der neue in der Datei landet. Nur ein Versuch: der Schlüsselspeicher hat ja gerade versagt.
		_ = keyringDelete(keyringUser(dataDir))
	}
	return KindFile, keySum, nil
}

// Load liest den Schlüssel aus der genannten Ablage und prüft sein Format sowie, wenn keySum
// nicht leer ist, den SHA-256. Leeres keySum heißt "nicht geprüft" (nur für Altstände in diesem Zweig).
func Load(dataDir, kind, keySum string) (string, error) {
	var key string
	var err error
	switch kind {
	case KindKeyring:
		key, err = keyringGet(keyringUser(dataDir))
	case KindFile:
		key, err = loadFile(dataDir)
	default:
		return "", fmt.Errorf("unbekannte Schlüsselablage %q", kind)
	}
	if err != nil {
		return "", err
	}
	if !protocol.DeviceKeyPattern.MatchString(key) {
		return "", errBadKey
	}
	if keySum != "" && subtle.ConstantTimeCompare([]byte(sumOf(key)), []byte(keySum)) != 1 {
		return "", errKeyMismatch
	}
	return key, nil
}

// Remove löscht den Schlüssel aus BEIDEN Ablagen, unabhängig von kind: ein Zeitüberschreitungs-Rückfall
// oder ein Wechsel der Ablage kann Kopien hinterlassen haben. Fehlendes ist kein Fehler. Ein Fehler beim
// Schlüsselspeicher zählt nur, wenn kind ihn als Ablage nennt; sonst (kein Schlüsselspeicher da) wird er ignoriert.
func Remove(dataDir, kind string) error {
	if kind != KindKeyring && kind != KindFile {
		return fmt.Errorf("unbekannte Schlüsselablage %q", kind)
	}
	krErr := keyringDelete(keyringUser(dataDir))
	fileErr := removeFile(dataDir)
	if fileErr != nil {
		return fileErr
	}
	if kind == KindKeyring {
		return krErr
	}
	return nil
}

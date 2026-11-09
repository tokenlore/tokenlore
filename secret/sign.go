package secret

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

// Der private Signaturschlüssel liegt getrennt vom Geräteschlüssel: eigener Eintrag im Schlüsselspeicher, sonst
// die Datei sign.key. Gespeichert wird der 32-Byte-Seed als Base64. Er gehört zum Rechner, nicht zur Verbindung:
// Remove (disconnect, Rückrollen beim Koppeln) fasst ihn nie an.
const (
	signFileName      = "sign.key"
	signKeyringPrefix = "sign-key:"
)

var (
	ErrSignKeyNotFound = errors.New("Signaturschlüssel nicht gefunden")
	ErrSignKeyBad      = errors.New("Signaturschlüssel hat ein ungültiges Format")
)

func signKeyringUser(dataDir string) string { return keyringUserFor(signKeyringPrefix, dataDir) }

// SaveSignKey legt den privaten Signaturschlüssel ab, wie Save den Geräteschlüssel: Schlüsselspeicher vor Datei.
// Fehlertexte enthalten nie den Schlüssel.
func SaveSignKey(dataDir string, priv ed25519.PrivateKey) (kind string, err error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", ErrSignKeyBad
	}
	enc := base64.StdEncoding.EncodeToString(priv.Seed())
	useKeyring := os.Getenv("TOKENLORE_SECRET_STORE") != "file"
	if useKeyring {
		if err := keyringSet(signKeyringUser(dataDir), enc); err == nil {
			_ = removeFile(dataDir, signFileName)
			return KindKeyring, nil
		}
	}
	if err := saveFile(dataDir, signFileName, enc); err != nil {
		return "", fmt.Errorf("Signaturschlüssel speichern: %w", err)
	}
	if useKeyring {
		_ = keyringDelete(signKeyringUser(dataDir))
	}
	return KindFile, nil
}

// LoadSignKey liest den privaten Signaturschlüssel aus der Ablage kind und gibt die Ablage zurück, in der er lag.
// kind "" heißt: Ablage unbekannt (nichts gemerkt). Dann gilt erst die Datei, danach der Schlüsselspeicher, und ein
// Fehler des Schlüsselspeichers zählt wie „nicht da“. Fehlt der Schlüssel, ist der Fehler ErrSignKeyNotFound.
func LoadSignKey(dataDir, kind string) (ed25519.PrivateKey, string, error) {
	switch kind {
	case KindFile:
		enc, err := loadFile(dataDir, signFileName)
		return decodeSignKey(enc, err, KindFile)
	case KindKeyring:
		enc, err := keyringGet(signKeyringUser(dataDir))
		return decodeSignKey(enc, err, KindKeyring)
	case "":
		priv, k, err := LoadSignKey(dataDir, KindFile)
		if !errors.Is(err, ErrSignKeyNotFound) || os.Getenv("TOKENLORE_SECRET_STORE") == "file" {
			return priv, k, err
		}
		priv, k, err = LoadSignKey(dataDir, KindKeyring)
		if err != nil && !errors.Is(err, ErrSignKeyBad) {
			return nil, "", ErrSignKeyNotFound
		}
		return priv, k, err
	default:
		return nil, "", fmt.Errorf("unbekannte Schlüsselablage %q", kind)
	}
}

func decodeSignKey(enc string, err error, kind string) (ed25519.PrivateKey, string, error) {
	if errors.Is(err, errAbsent) {
		return nil, "", ErrSignKeyNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("Signaturschlüssel lesen: %w", err)
	}
	seed, derr := base64.StdEncoding.Strict().DecodeString(enc)
	if derr != nil || len(seed) != ed25519.SeedSize {
		return nil, "", ErrSignKeyBad
	}
	return ed25519.NewKeyFromSeed(seed), kind, nil
}

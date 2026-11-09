package secret

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func newSignKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func TestSignKeyKeyringRoundTrip(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	priv := newSignKey(t)
	kind, err := SaveSignKey(dir, priv)
	if err != nil || kind != KindKeyring {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	for _, k := range []string{KindKeyring, ""} {
		got, gotKind, err := LoadSignKey(dir, k)
		if err != nil || !bytes.Equal(got, priv) || gotKind != KindKeyring {
			t.Fatalf("LoadSignKey(%q): kind=%q err=%v", k, gotKind, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sign.key")); err == nil {
		t.Error("sign.key darf nicht existieren")
	}
	// Eigener Eintrag: der Geräteschlüssel unter keyringUser bleibt unberührt.
	if _, err := keyring.Get("tokenlore", keyringUser(dir)); err == nil {
		t.Error("Signaturschlüssel unter dem Eintrag des Geräteschlüssels")
	}
}

func TestSignKeyFileFallback(t *testing.T) {
	keyring.MockInitWithError(os.ErrPermission)
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	priv := newSignKey(t)
	kind, err := SaveSignKey(dir, priv)
	if err != nil || kind != KindFile {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	fi, err := os.Stat(filepath.Join(dir, "sign.key"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("Modus %v", fi.Mode().Perm())
	}
	for _, k := range []string{KindFile, ""} {
		got, gotKind, err := LoadSignKey(dir, k)
		if err != nil || !bytes.Equal(got, priv) || gotKind != KindFile {
			t.Fatalf("LoadSignKey(%q): kind=%q err=%v", k, gotKind, err)
		}
	}
}

func TestSignKeyForcedFileNeverTouchesKeyring(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "file")
	dir := t.TempDir()
	if kind, err := SaveSignKey(dir, newSignKey(t)); err != nil || kind != KindFile {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	if _, err := keyring.Get("tokenlore", keyringUserFor("sign-key:", dir)); err == nil {
		t.Error("Schlüsselspeicher benutzt")
	}
}

func TestSignKeyMissing(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	for _, k := range []string{KindKeyring, KindFile, ""} {
		if _, _, err := LoadSignKey(dir, k); !errors.Is(err, ErrSignKeyNotFound) {
			t.Errorf("LoadSignKey(%q): %v", k, err)
		}
	}
}

// Eine kaputte Datei ist ein Formatfehler, kein fehlender Schlüssel; der Fehlertext nennt den Inhalt nie.
func TestSignKeyBadFormat(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "file")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sign.key"), []byte("geheimkaputt"), 0o600)
	_, _, err := LoadSignKey(dir, KindFile)
	if err == nil || errors.Is(err, ErrSignKeyNotFound) || !errors.Is(err, ErrSignKeyBad) {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(err.Error(), "geheimkaputt") {
		t.Fatal("Inhalt im Fehlertext")
	}
}

// Ein Lesefehler des Schlüsselspeichers (nicht „fehlt“) bleibt ein Fehler; die Datei wird bei kind "" trotzdem versucht.
func TestSignKeyKeyringErrorIsNotMissing(t *testing.T) {
	keyring.MockInitWithError(os.ErrPermission)
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	if _, _, err := LoadSignKey(dir, KindKeyring); err == nil || errors.Is(err, ErrSignKeyNotFound) {
		t.Fatalf("err=%v", err)
	}
	// Ohne gemerkte Ablage zählt ein kaputter Schlüsselspeicher als „nicht da“, wenn auch die Datei fehlt.
	if _, _, err := LoadSignKey(dir, ""); !errors.Is(err, ErrSignKeyNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestSignKeyUnknownKind(t *testing.T) {
	if _, _, err := LoadSignKey(t.TempDir(), "tresor"); err == nil {
		t.Fatal("Fehler erwartet")
	}
}

// Remove (disconnect, Rückrollen beim Koppeln) räumt nur den Geräteschlüssel, in beiden Ablagen.
func TestRemoveKeepsSignKey(t *testing.T) {
	for _, store := range []string{"", "file"} {
		keyring.MockInit()
		t.Setenv("TOKENLORE_SECRET_STORE", store)
		dir := t.TempDir()
		priv := newSignKey(t)
		if _, err := SaveSignKey(dir, priv); err != nil {
			t.Fatal(err)
		}
		kind, _, err := Save(dir, goodKey)
		if err != nil {
			t.Fatal(err)
		}
		if err := Remove(dir, kind); err != nil {
			t.Fatal(err)
		}
		if got, _, err := LoadSignKey(dir, ""); err != nil || !bytes.Equal(got, priv) {
			t.Fatalf("Ablage %q: Signaturschlüssel weg: %v", store, err)
		}
	}
}

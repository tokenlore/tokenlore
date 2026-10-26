package secret

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// Der Mock des Schlüsselspeichers ist globaler Zustand: keine parallelen Tests.
const goodKey = "tl_dk_" + "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq"

func TestSaveRejectsMalformedKey(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	if _, _, err := Save(dir, "tl_dk_kurz"); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if _, err := os.Stat(filepath.Join(dir, "device.key")); err == nil {
		t.Error("Datei darf nicht existieren")
	}
	if _, err := keyring.Get("tokenlore", keyringUser(dir)); err == nil {
		t.Error("Eintrag darf nicht existieren")
	}
}

func TestSaveKeyringRoundTrip(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	kind, _, err := Save(dir, goodKey)
	if err != nil || kind != KindKeyring {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	got, err := Load(dir, kind, "")
	if err != nil || got != goodKey {
		t.Fatalf("Load: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "device.key")); err == nil {
		t.Error("device.key darf nicht existieren")
	}
}

func TestSaveFallsBackToFile(t *testing.T) {
	keyring.MockInitWithError(os.ErrPermission)
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	kind, _, err := Save(dir, goodKey)
	if err != nil || kind != KindFile {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	fi, err := os.Stat(filepath.Join(dir, "device.key"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("Modus %v", fi.Mode().Perm())
	}
	if got, err := Load(dir, kind, ""); err != nil || got != goodKey {
		t.Errorf("Load: %v", err)
	}
}

func TestForcedFileStore(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "file")
	dir := t.TempDir()
	kind, _, err := Save(dir, goodKey)
	if err != nil || kind != KindFile {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	if _, err := keyring.Get("tokenlore", keyringUser(dir)); err == nil {
		t.Error("Schlüsselspeicher darf nicht benutzt werden")
	}
}

func TestKeyringSaveRemovesOldFile(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "device.key"), []byte("alt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if kind, _, err := Save(dir, goodKey); err != nil || kind != KindKeyring {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "device.key")); err == nil {
		t.Error("alte device.key sollte gelöscht sein")
	}
}

func TestLoadRejectsTamperedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "device.key"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, KindFile, "")
	if err == nil || got != "" {
		t.Fatalf("Fehler erwartet, got %q", got)
	}
}

func TestLoadMissingIsErrNotFound(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	for _, k := range []string{KindFile, KindKeyring} {
		if _, err := Load(dir, k, ""); err != ErrNotFound {
			t.Errorf("%s: %v", k, err)
		}
	}
}

func TestRemoveMissingIsOK(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	for _, k := range []string{KindFile, KindKeyring} {
		if err := Remove(dir, k); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
}

func TestRemoveDeletes(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	kind, _, _ := Save(dir, goodKey)
	if err := Remove(dir, kind); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, kind, ""); err != ErrNotFound {
		t.Errorf("Load nach Remove: %v", err)
	}
}

func setHooks(t *testing.T, timeout time.Duration) {
	t.Helper()
	oldT, oldS, oldG, oldD := keyringTimeout, krSet, krGet, krDelete
	keyringTimeout = timeout
	t.Cleanup(func() { keyringTimeout, krSet, krGet, krDelete = oldT, oldS, oldG, oldD })
}

func TestTimeoutFallbackCleansLateKeyringWrite(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	setHooks(t, 50*time.Millisecond)
	release := make(chan struct{})
	realSet := krSet
	krSet = func(s, u, p string) error { <-release; return realSet(s, u, p) }
	dir := t.TempDir()
	kind, _, err := Save(dir, goodKey)
	if err != nil || kind != KindFile {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := keyring.Get("tokenlore", keyringUser(dir)); errors.Is(err, keyring.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("später Schlüsselspeicher-Eintrag blieb zurück")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRemoveClearsBothStores(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	if err := keyring.Set("tokenlore", keyringUser(dir), goodKey); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "device.key"), []byte(goodKey), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir, KindFile); err != nil {
		t.Fatal(err)
	}
	if _, err := keyring.Get("tokenlore", keyringUser(dir)); err == nil {
		t.Error("Schlüsselspeicher-Eintrag blieb")
	}
	if _, err := os.Stat(filepath.Join(dir, "device.key")); err == nil {
		t.Error("Datei blieb")
	}
}

func TestRemoveKeyringTimeoutIsError(t *testing.T) {
	keyring.MockInit()
	setHooks(t, 50*time.Millisecond)
	krDelete = func(s, u string) error { time.Sleep(300 * time.Millisecond); return nil }
	dir := t.TempDir()
	if err := Remove(dir, KindKeyring); err == nil {
		t.Error("Fehler erwartet")
	}
	if err := Remove(dir, KindFile); err != nil {
		t.Errorf("bei kind=file ist ein Schlüsselspeicher-Fehler egal: %v", err)
	}
}

func TestKeyringUserIsInstanceBound(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	a, b := t.TempDir(), t.TempDir()
	if keyringUser(a) == keyringUser(b) {
		t.Fatal("gleicher Benutzername für verschiedene Verzeichnisse")
	}
	keyB := "tl_dk_" + "BBCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopq"
	Save(a, goodKey)
	Save(b, keyB)
	if got, _ := Load(a, KindKeyring, ""); got != goodKey {
		t.Error("Schlüssel von a überschrieben")
	}
	if err := Remove(b, KindKeyring); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(a, KindKeyring, ""); err != nil || got != goodKey {
		t.Errorf("Remove(b) hat a berührt: %v", err)
	}
}

func TestLoadVerifiesKeySum(t *testing.T) {
	t.Setenv("TOKENLORE_SECRET_STORE", "file")
	dir := t.TempDir()
	kind, sum, err := Save(dir, goodKey)
	if err != nil || len(sum) != 64 {
		t.Fatalf("sum=%q err=%v", sum, err)
	}
	if got, err := Load(dir, kind, sum); err != nil || got != goodKey {
		t.Errorf("Load: %v", err)
	}
	got, err := Load(dir, kind, strings.Repeat("0", 64))
	if err == nil || got != "" || strings.Contains(err.Error(), goodKey) {
		t.Errorf("Abweichung erwartet: %q %v", got, err)
	}
}

func TestKeyringSaveSurvivesOldFileRemovalFailure(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	dir := t.TempDir()
	// device.key als nicht leeres Verzeichnis: Löschen scheitert.
	p := filepath.Join(dir, "device.key")
	if err := os.MkdirAll(filepath.Join(p, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	kind, _, err := Save(dir, goodKey)
	if err != nil || kind != KindKeyring {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	if got, err := Load(dir, kind, ""); err != nil || got != goodKey {
		t.Errorf("Load: %v", err)
	}
}

// Neu-Kopplung, während der Schlüsselspeicher ausfällt: Der alte, noch gültige Schlüssel darf nicht
// unter demselben Eintrag liegen bleiben, wenn der neue in der Datei landet.
func TestFileFallbackDeletesOldKeyringEntry(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "")
	setHooks(t, time.Second)
	dir := t.TempDir()
	old := "tl_dk_" + strings.Repeat("Z", 43)
	if err := keyring.Set("tokenlore", keyringUser(dir), old); err != nil {
		t.Fatal(err)
	}
	krSet = func(s, u, p string) error { return os.ErrPermission }
	kind, _, err := Save(dir, goodKey)
	if err != nil || kind != KindFile {
		t.Fatalf("kind=%q err=%v", kind, err)
	}
	if _, err := keyring.Get("tokenlore", keyringUser(dir)); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("alter Eintrag im Schlüsselspeicher geblieben: %v", err)
	}
}

// Ist die Datei erzwungen, wird der Schlüsselspeicher gar nicht angefasst (auch nicht zum Löschen).
func TestForcedFileStoreNeverTouchesKeyring(t *testing.T) {
	keyring.MockInit()
	t.Setenv("TOKENLORE_SECRET_STORE", "file")
	setHooks(t, time.Second)
	krDelete = func(s, u string) error { t.Fatal("Schlüsselspeicher angefasst"); return nil }
	krSet = func(s, u, p string) error { t.Fatal("Schlüsselspeicher angefasst"); return nil }
	if _, _, err := Save(t.TempDir(), goodKey); err != nil {
		t.Fatal(err)
	}
}

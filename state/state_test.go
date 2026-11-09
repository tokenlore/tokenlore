package state

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadMissingGivesDefaults(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.EmailMode != "off" {
		t.Errorf("EmailMode %q, want off", s.EmailMode)
	}
	if s.Files == nil {
		t.Error("Files darf nicht nil sein")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	key := "projects/P 1 ü/s1.jsonl"
	s.Files[key] = FileMark{Offset: 42, HeadLen: 10, HeadSum: "abc"}
	s.EmailMode = "short"
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Files[key] != (FileMark{Offset: 42, HeadLen: 10, HeadSum: "abc"}) {
		t.Errorf("FileMark: %+v", got.Files[key])
	}
	if got.EmailMode != "short" {
		t.Errorf("State: %+v", got)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "state.json" || strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("unerwartete Datei %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("%d Dateien, want 1", len(entries))
	}
}

func TestCloneIsIndependent(t *testing.T) {
	s, _ := Load(t.TempDir())
	s.Files["a"] = FileMark{Offset: 1}
	c := s.Clone()
	c.Files["a"] = FileMark{Offset: 2}
	c.Files["b"] = FileMark{Offset: 3}
	if s.Files["a"].Offset != 1 || len(s.Files) != 1 {
		t.Error("Clone teilt Maps mit dem Original")
	}
}

func TestCloneCopiesSignKeysPrevious(t *testing.T) {
	s := &State{SignPublicKeysPrevious: []string{"a"}}
	c := s.Clone()
	c.SignPublicKeysPrevious[0] = "b"
	if s.SignPublicKeysPrevious[0] != "a" {
		t.Error("Clone teilt sign_public_keys_previous")
	}
}

func TestCloneCopiesSignKeysRefused(t *testing.T) {
	s := &State{SignKeysRefused: []string{"a"}}
	c := s.Clone()
	c.SignKeysRefused[0] = "b"
	if s.SignKeysRefused[0] != "a" {
		t.Error("Clone teilt sign_keys_refused")
	}
}

// Ein neuer Signaturschlüssel schiebt den bisherigen öffentlichen Teil in die Liste der früheren: ohne Doppelte,
// höchstens maxPrev, die ältesten fallen heraus. Derselbe Schlüssel ändert nichts.
func TestSetSignPublicKey(t *testing.T) {
	const maxPrev = 16
	s := &State{}
	s.SetSignPublicKey("k0", maxPrev)
	if s.SignPublicKey != "k0" || len(s.SignPublicKeysPrevious) != 0 {
		t.Fatalf("%+v", s)
	}
	s.SetSignPublicKey("k0", maxPrev)
	if len(s.SignPublicKeysPrevious) != 0 {
		t.Fatalf("gleicher Schlüssel landet in der Liste: %v", s.SignPublicKeysPrevious)
	}
	s.SetSignPublicKey("k1", maxPrev)
	s.SetSignPublicKey("k0", maxPrev) // alter Schlüssel kehrt zurück: steht nicht mehr unter den früheren
	if s.SignPublicKey != "k0" || !slices.Equal(s.SignPublicKeysPrevious, []string{"k1"}) {
		t.Fatalf("%+v", s)
	}
	for i := 2; i < 2+maxPrev+3; i++ {
		s.SetSignPublicKey(fmt.Sprintf("k%d", i), maxPrev)
	}
	if len(s.SignPublicKeysPrevious) != maxPrev {
		t.Fatalf("Länge %d", len(s.SignPublicKeysPrevious))
	}
	last := fmt.Sprintf("k%d", 2+maxPrev+1)
	if s.SignPublicKeysPrevious[len(s.SignPublicKeysPrevious)-1] != last || slices.Contains(s.SignPublicKeysPrevious, "k1") {
		t.Fatalf("Liste %v", s.SignPublicKeysPrevious)
	}
}

// Felder der früheren Lieferung (Konto je Sitzung, Altbestand, Kontensumme) bleiben lesbar und fallen beim nächsten
// Speichern weg; geliefert wird aus dem Archiv, der Merker dazu steht in geliefert.json.
func TestOldDeliveryFieldsDroppedOnSave(t *testing.T) {
	dir := t.TempDir()
	old := `{"files":{"a":{"offset":5,"end":5}},"sessions":{"s1":"acc-1"},"email_mode":"full","delivered":true,` +
		`"accounts_sum":"abc","legacy_sum":"def","legacy_rejected":["2026-10-01#1,1,1"],` +
		`"identity":{"id":"` + strings.Repeat("a", 32) + `","last_day":"2026-10-01","branch_day":"2026-09-01"}}`
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.EmailMode != "full" || s.Files["a"].Offset != 5 || s.Identity == nil || s.Identity.ID != strings.Repeat("a", 32) {
		t.Fatalf("%+v", s)
	}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, fileName))
	for _, k := range []string{`"sessions"`, "delivered", "accounts_sum", "legacy_sum", "legacy_rejected", "last_day", "branch_day"} {
		if strings.Contains(string(b), k) {
			t.Errorf("%s noch gespeichert: %s", k, b)
		}
	}
}

// session-accounts.jsonl liest der Merkzettel nicht mehr; das Konto steht je Anfrage im Archiv.
func TestSessionLogIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session-accounts.jsonl"), []byte(`{"s":"s1","a":"acc-1"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, fileName))
	if strings.Contains(string(b), "acc-1") {
		t.Fatalf("Konto aus session-accounts.jsonl übernommen: %s", b)
	}
}

func TestDataDirIgnoresPluginData(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("TOKENLORE_DATA_DIR", "")
	got, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := os.UserConfigDir()
	if got != filepath.Join(base, "tokenlore") {
		t.Errorf("DataDir %q", got)
	}
}

func TestDataDirOverride(t *testing.T) {
	d := t.TempDir()
	t.Setenv("TOKENLORE_DATA_DIR", d)
	got, err := DataDir()
	if err != nil || got != d {
		t.Errorf("got %q err %v", got, err)
	}
}

func TestStateArchiveCreatedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	s.ArchiveCreated = true
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(dir)
	if !got.ArchiveCreated {
		t.Errorf("%+v", got)
	}
	if c := got.Clone(); !c.ArchiveCreated {
		t.Errorf("Clone: %+v", c)
	}
}

// Ein Merkzettel aus der Zeit mit Projekt-Kennung trägt noch ein Salz: Er bleibt lesbar, und beim nächsten
// Speichern fällt das Salz weg.
func TestOldSaltDroppedOnSave(t *testing.T) {
	dir := t.TempDir()
	old := `{"files":{},"sessions":{},"email_mode":"short","salt":"` + strings.Repeat("ab", 32) + `"}`
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.EmailMode != "short" {
		t.Fatalf("EmailMode %q", s.EmailMode)
	}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "salt") {
		t.Fatalf("Salz noch gespeichert: %s", b)
	}
}

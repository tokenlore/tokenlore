package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Eine Kennungsdatei mit Feldern früherer Fassungen (last_day, branch_day) wird ohne sie neu geschrieben;
// Kennung, Systemkennung und Vorgänger bleiben.
func TestTidyIdentityDropsOldFields(t *testing.T) {
	dir := t.TempDir()
	writeID(t, dir, `{"id":"`+testID+`","machine":"`+testMachine+`","predecessor":"`+otherID+`","last_day":"2026-09-01","branch_day":"2026-08-15"}`)
	if err := TidyIdentity(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, IdentityFile))
	if strings.Contains(string(b), "last_day") || strings.Contains(string(b), "branch_day") {
		t.Fatalf("Kennungsdatei %s", b)
	}
	id := readIDFile(t, dir)
	if id.ID != testID || id.Machine != testMachine || id.Predecessor != otherID {
		t.Fatalf("%+v", id)
	}
}

// Eine Datei in der heutigen Form bleibt unverändert; eine fehlende entsteht nicht, eine kaputte bleibt liegen.
func TestTidyIdentityLeavesOthers(t *testing.T) {
	dir := t.TempDir()
	if err := TidyIdentity(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, IdentityFile)); !os.IsNotExist(err) {
		t.Fatalf("Kennungsdatei angelegt: %v", err)
	}
	for _, content := range []string{"kaputt", `{"id":"zu-kurz","branch_day":"2026-08-15"}`} {
		writeID(t, dir, content)
		if err := TidyIdentity(dir); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, IdentityFile)); string(b) != content {
			t.Fatalf("kaputte Datei geändert: %s", b)
		}
	}
	writeID(t, dir, `{"id":"`+testID+`","machine":"","predecessor":""}`+"\n")
	p := filepath.Join(dir, IdentityFile)
	before, _ := os.Stat(p)
	if err := TidyIdentity(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(p)
	if !os.SameFile(before, after) {
		t.Fatal("Datei in heutiger Form neu geschrieben")
	}
}

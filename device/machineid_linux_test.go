package device

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstMachineIDFallback(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "fehlt")
	empty := filepath.Join(dir, "leer")
	dbus := filepath.Join(dir, "dbus")
	os.WriteFile(empty, []byte("\n"), 0o600)
	os.WriteFile(dbus, []byte("abcdefabcdefabcdefabcdefabcdefab\n"), 0o600)
	if got := firstMachineID([]string{missing, empty, dbus}); got != "abcdefabcdefabcdefabcdefabcdefab" {
		t.Fatalf("%q", got)
	}
	if got := firstMachineID([]string{missing, empty}); got != "" {
		t.Fatalf("%q", got)
	}
}

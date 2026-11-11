package claudecode

import (
	"os"
	"testing"
)

// TestMain leert das Konto der Desktop-App: Läuft der Test in einer ihrer Sitzungen, stünde es sonst in der Umgebung.
func TestMain(m *testing.M) {
	os.Unsetenv("CLAUDE_CODE_ACCOUNT_UUID")
	os.Unsetenv("CLAUDE_CODE_USER_EMAIL")
	os.Exit(m.Run())
}

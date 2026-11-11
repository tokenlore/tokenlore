package main

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMain ersetzt den Schlüsselspeicher des Systems für alle Tests dieses Pakets durch den Mock, auch
// in den Hilfsprozessen (sie sind dasselbe Testprogramm): Tests dürfen den echten nie berühren.
func TestMain(m *testing.M) {
	keyring.MockInit()
	// Läuft der Test in einer Sitzung der Desktop-App, stünde sonst deren Konto in der Umgebung.
	os.Unsetenv("CLAUDE_CODE_ACCOUNT_UUID")
	os.Unsetenv("CLAUDE_CODE_USER_EMAIL")
	os.Exit(m.Run())
}

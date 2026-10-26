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
	os.Exit(m.Run())
}

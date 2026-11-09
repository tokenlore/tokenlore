package device

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"testing"
)

func TestHashMachineID(t *testing.T) {
	a := hashMachineID("0123456789abcdef0123456789abcdef")
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a) {
		t.Fatalf("Hash %q", a)
	}
	if b := hashMachineID("  0123456789ABCDEF0123456789ABCDEF\r\n"); b != a {
		t.Fatal("Groß-/Kleinschreibung oder Leerraum ändert den Hash")
	}
	if hashMachineID("") != "" || hashMachineID(" \n") != "" {
		t.Fatal("leere Kennung ergibt einen Hash")
	}
	if hashMachineID("11111111-2222-3333-4444-555555555555") == a {
		t.Fatal("verschiedene Kennungen, gleicher Hash")
	}
	// Geschlüsselt: kein nackter SHA-256, der mit Hashes anderer Programme über dieselbe Kennung übereinstimmt.
	plain := sha256.Sum256([]byte("0123456789abcdef0123456789abcdef"))
	if a == hex.EncodeToString(plain[:]) {
		t.Fatal("Hash ohne Schlüssel")
	}
}

func TestParseIOPlatformUUID(t *testing.T) {
	out := `+-o J000AP  <class IOPlatformExpertDevice>
    {
      "IOPlatformSerialNumber" = "XXXXXXXXXXXX"
      "IOPlatformUUID" = "11111111-2222-3333-4444-555555555555"
    }`
	if got := parseIOPlatformUUID(out); got != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("%q", got)
	}
	if got := parseIOPlatformUUID("nichts"); got != "" {
		t.Fatalf("%q", got)
	}
}

func TestMachineHashIsHashOrEmpty(t *testing.T) {
	h := MachineHash()
	if h != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h) {
		t.Fatalf("MachineHash %q", h)
	}
}

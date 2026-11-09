package device

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// machineKey ist der feste, anwendungseigene Schlüssel für den Hash der Systemkennung. Mit ihm ist der Wert in der
// Kennungsdatei nicht mit Hashes anderer Programme über dieselbe Kennung verknüpfbar (machine-id(5) verlangt einen
// solchen Schlüssel).
const machineKey = "tokenlore-machine-id"

// MachineHash liefert den Hex-HMAC-SHA256 (Schlüssel machineKey) der Systemkennung dieses Rechners (Linux: machine-id, macOS: IOPlatformUUID,
// Windows: MachineGuid). Die Kennung selbst verlässt das Gerät nie und steht nirgends im Klartext. Ist sie nicht
// lesbar, ist das Ergebnis leer.
func MachineHash() string {
	return hashMachineID(readMachineID())
}

// hashMachineID macht aus der rohen Systemkennung den Hash; Groß-/Kleinschreibung und Leerraum zählen nicht.
func hashMachineID(raw string) string {
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "" {
		return ""
	}
	m := hmac.New(sha256.New, []byte(machineKey))
	m.Write([]byte(id))
	return hex.EncodeToString(m.Sum(nil))
}

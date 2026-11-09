package device

import (
	"os"
	"strings"
)

// machineIDFiles sind die Orte der machine-id; die zweite ist der Rückfall älterer Systeme.
var machineIDFiles = []string{"/etc/machine-id", "/var/lib/dbus/machine-id"}

func readMachineID() string {
	return firstMachineID(machineIDFiles)
}

// firstMachineID liest die erste nicht leere Datei.
func firstMachineID(files []string) string {
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	return ""
}

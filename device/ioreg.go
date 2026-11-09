package device

import "regexp"

var ioPlatformUUID = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

// parseIOPlatformUUID holt die IOPlatformUUID aus der Ausgabe von ioreg (macOS).
func parseIOPlatformUUID(out string) string {
	m := ioPlatformUUID.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return m[1]
}

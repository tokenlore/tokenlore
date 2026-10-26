// Paket device beschreibt das Gerät, auf dem tokenlore läuft.
package device

import (
	"errors"
	"time"
	_ "time/tzdata" // Zeitzonendaten im Programm, damit es auch auf Rechnern ohne Go-Installation stimmt

	"github.com/thlib/go-timezone-local/tzlocal"
)

var errFake = errors.New("Zeitzone nicht bestimmbar") // nur für Tests

// LocalTZ liefert die IANA-Zeitzone des Geräts (unter Windows aus der Systemzeitzone abgeleitet).
func LocalTZ() (name string, loc *time.Location) {
	return resolveTZ(tzlocal.RuntimeTZ())
}

// resolveTZ macht aus dem ermittelten Namen eine Zeitzone. Ist keine bestimmbar, gilt
// durchgehend UTC: Name und Versatz-Berechnung passen dann zusammen.
func resolveTZ(name string, err error) (string, *time.Location) {
	if err != nil || name == "" {
		return "UTC", time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return "UTC", time.UTC
	}
	return name, loc
}

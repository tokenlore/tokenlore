package device

import (
	"testing"
	"time"
)

func TestResolveTZFallbackIsConsistent(t *testing.T) {
	name, loc := resolveTZ("", errFake)
	if name != "UTC" || loc != time.UTC {
		t.Fatalf("Rückfall: %q %v, want UTC/UTC", name, loc)
	}
}

func TestResolveTZWithoutSystemTZData(t *testing.T) {
	// Mit eingebetteten Zeitzonendaten klappt das auch auf Rechnern ohne Go-Installation.
	t.Setenv("ZONEINFO", t.TempDir())
	name, loc := resolveTZ("Europe/Berlin", nil)
	if name != "Europe/Berlin" || loc.String() != "Europe/Berlin" {
		t.Fatalf("got %q %v", name, loc)
	}
}

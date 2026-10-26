package protocol

import (
	"regexp"
	"time"
)

// Grenzen eines Lieferpakets. Der Server lehnt Pakete ab, die sie überschreiten.
const (
	MaxBatchBytes       = 1 << 20
	MaxRequestsPerBatch = 2000
	MaxExtraFields      = 16
	MaxExtraString      = 64
	MaxEmailLen         = 254
	MaxSessionsPerBatch = 10_000
	MaxDeviceName       = 64
	MaxTZLen            = 64
	MaxLegacyList       = 10_000
	MaxLegacyNumber     = 1_000_000_000_000

	// MaxFutureSkew ist die Uhrabweichung, die ein Zeitstempel in der Zukunft haben darf.
	MaxFutureSkew = 5 * time.Minute
)

// ModelPattern beschreibt erlaubte Modellnamen.
var ModelPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// DeviceKeyPattern beschreibt das Format eines Geräteschlüssels (tl_dk_ + 32 Byte base64url ohne Auffüllung).
var DeviceKeyPattern = regexp.MustCompile(`^tl_dk_[A-Za-z0-9_-]{43}$`)

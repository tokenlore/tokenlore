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
	// MaxLegacyModels ist die Höchstzahl der Modelle eines Vorgeschichte-Tags.
	MaxLegacyModels = 64

	// Inhaltszeile im Archiv (protocol/doc.md, „Signierte Zeile“). MaxLineBytes gilt für die UTF-8-Bytes der Zeile.
	MaxLineBytes    = 8192
	MaxUTCOffsetMin = 14 * 60
	// usage: höchstens so viele Ebenen (usage selbst ist Ebene 1), Werte insgesamt und Einträge je Liste.
	UsageMaxDepth  = 4
	UsageMaxValues = 256
	UsageMaxList   = 64

	// MaxFutureSkew ist die Uhrabweichung, die ein Zeitstempel in der Zukunft haben darf.
	MaxFutureSkew = 5 * time.Minute
)

// ModelPattern beschreibt erlaubte Modellnamen.
var ModelPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// DeviceKeyPattern beschreibt das Format eines Geräteschlüssels (tl_dk_ + 32 Byte base64url ohne Auffüllung).
var DeviceKeyPattern = regexp.MustCompile(`^tl_dk_[A-Za-z0-9_-]{43}$`)

// ServerIDPattern beschreibt server_id aus GET /api/plugin/info und device_source: 64 Hex-Zeichen, klein.
var ServerIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// MinVersionPattern beschreibt min_plugin_version: drei Zahlen x.y.z.
var MinVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

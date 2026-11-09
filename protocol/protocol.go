// Paket protocol beschreibt das Lieferformat zwischen dem Plugin (tokenlore) und tokenlore-server.
// Der Server importiert dieses Paket und prüft gegen dieselben Grenzen (limits.go).
package protocol

import "time"

// Version ist die Protokollversion, die jede Lieferung trägt.
const Version = 2

// Batch ist ein Lieferpaket (protocol/doc.md, „Batch“). Anfragen und Vorgeschichte-Tage stehen als signierte Hüllen
// darin, genau so, wie sie im Archiv stehen.
type Batch struct {
	V          int        `json:"v"`
	Device     DeviceInfo `json:"device"`
	Accounts   []Account  `json:"accounts,omitempty"`
	Sessions   []Session  `json:"sessions,omitempty"`
	Requests   []Envelope `json:"requests,omitempty"`
	LegacyDays []Envelope `json:"legacy_days,omitempty"`
}

// DeviceInfo beschreibt das liefernde Gerät. Den Gerätenamen vergibt der Nutzer beim Koppeln.
type DeviceInfo struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	TZ            string `json:"tz"` // IANA-Zeitzone, z. B. "Europe/Berlin"
	PluginVersion string `json:"plugin_version"`
}

// Werte für Account.EmailMode.
const (
	EmailOff   = "off"   // nichts senden; der Server löscht eine gespeicherte Adresse
	EmailShort = "short" // gekürzt, z. B. "ma…@beispiel.de"
	EmailFull  = "full"  // volle Adresse
)

// Account ist ein Claude-Konto, erkannt an seiner accountUuid.
type Account struct {
	AccountID string `json:"account_id"`
	EmailMode string `json:"email_mode"`
	Email     string `json:"email,omitempty"`
}

// Session ist eine Claude-Code-Sitzung.
type Session struct {
	SessionID string `json:"session_id"`
	CCVersion string `json:"cc_version,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}

// Werte für das Feld kind einer Inhaltszeile.
const (
	KindRequest = "request"
	KindDay     = "day"
)

// RequestLine ist die Inhaltszeile einer Anfrage im Archiv (protocol/doc.md, „Inhaltszeile: Anfrage“). Sie enthält
// nur die Felder dieser festen Liste; optionale Felder fehlen ganz, wenn das Protokoll den Wert nicht hat.
// Usage ist der ganze Block message.usage, mit SanitizeUsage in die Grenzen gebracht; Zahlen sind json.Number.
type RequestLine struct {
	Kind           string         `json:"kind"`
	RequestID      string         `json:"request_id"`
	MessageID      string         `json:"message_id"`
	SessionID      string         `json:"session_id"`
	UUID           string         `json:"uuid,omitempty"`
	ParentUUID     string         `json:"parent_uuid,omitempty"`
	TS             time.Time      `json:"ts"` // immer UTC
	UTCOffsetMin   int            `json:"utc_offset_min"`
	Model          string         `json:"model"`
	RequestedModel string         `json:"requested_model,omitempty"`
	Effort         string         `json:"effort,omitempty"`
	Entrypoint     string         `json:"entrypoint,omitempty"`
	CCVersion      string         `json:"cc_version,omitempty"`
	StopReason     string         `json:"stop_reason,omitempty"`
	Subagent       bool           `json:"subagent"`
	Tools          map[string]int `json:"tools,omitempty"`
	AccountID      string         `json:"account_id,omitempty"`
	Usage          map[string]any `json:"usage"`
}

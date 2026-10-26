// Paket protocol beschreibt das Lieferformat zwischen dem Plugin (tokenlore) und tokenlore-server.
// Der Server importiert dieses Paket und prüft gegen dieselben Grenzen (limits.go).
package protocol

import "time"

// Version ist die Protokollversion, die jede Lieferung trägt.
const Version = 1

// Batch ist ein Lieferpaket.
type Batch struct {
	V              int                 `json:"v"`
	Device         DeviceInfo          `json:"device"`
	Accounts       []Account           `json:"accounts,omitempty"`
	Sessions       []Session           `json:"sessions,omitempty"`
	Requests       []Request           `json:"requests,omitempty"`
	LegacyModel    []LegacyModelDay    `json:"legacy_model,omitempty"`
	LegacyActivity []LegacyActivityDay `json:"legacy_activity,omitempty"`
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
	ProjectID string `json:"project_id,omitempty"` // HMAC des Projektordnernamens, nie der Name selbst
	CCVersion string `json:"cc_version,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}

// Request ist eine API-Anfrage, eindeutig über (RequestID, MessageID).
// Sie darf mehrfach gesendet werden; der Server zählt sie einmal.
type Request struct {
	RequestID    string         `json:"request_id"`
	MessageID    string         `json:"message_id"`
	SessionID    string         `json:"session_id"`
	TS           time.Time      `json:"ts"` // immer UTC
	UTCOffsetMin int            `json:"utc_offset_min"`
	Model        string         `json:"model"`
	Input        int64          `json:"input"`
	Output       int64          `json:"output"`
	CacheCreate  int64          `json:"cache_create"`
	CacheRead    int64          `json:"cache_read"`
	Cache1h      int64          `json:"cache_1h"`
	Cache5m      int64          `json:"cache_5m"`
	Thinking     int64          `json:"thinking"`
	WebSearch    int            `json:"web_search"`
	WebFetch     int            `json:"web_fetch"`
	StopReason   string         `json:"stop_reason,omitempty"`
	Subagent     bool           `json:"subagent"`
	Tools        map[string]int `json:"tools,omitempty"`
	AccountID    string         `json:"account_id,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"` // nur float64 oder string
}

// LegacyModelDay ist ein Tageswert aus dem Altbestand (stats-cache.json).
type LegacyModelDay struct {
	Date   string `json:"date"` // "2006-01-02"
	Model  string `json:"model"`
	Tokens int64  `json:"tokens"`
}

// LegacyActivityDay ist ein Tageswert der Aktivität aus dem Altbestand.
type LegacyActivityDay struct {
	Date      string `json:"date"`
	Messages  int    `json:"messages"`
	Sessions  int    `json:"sessions"`
	ToolCalls int    `json:"tool_calls"`
}

package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // Zeitzonen auch ohne Systemdaten (Windows, Container)
	"unicode"
	"unicode/utf8"
)

// Feldgrenzen. Das Plugin lässt Werte, die sie verletzen, vor dem Senden weg; der Server lehnt sie ab.
const (
	MaxIDLen          = 128
	MaxShortText      = 32
	MaxTools          = 64
	MaxTokensPerField = 100_000_000
	MaxCountPerField  = 10_000
)

var (
	IDPattern      = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	ToolPattern    = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	StopPattern    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)
	VersionPattern = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`)
	ExtraKey       = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
)

// Earliest ist der früheste zulässige Zeitstempel. Zeitpunkte in der Zukunft lehnt ValidateRequestLine ab (MaxFutureSkew).
var Earliest = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// TSPattern ist die Form von ts in einer Inhaltszeile: UTC mit Z, Bruchteile höchstens 9 Stellen.
var TSPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`)

// ValidateRequestLine prüft die Inhaltszeile einer Anfrage nach den Regeln, mit denen der Server sie prüft
// (protocol/doc.md, „Inhaltszeile: Anfrage“ und „Grenzen“), samt Länge der Zeile, wie MarshalLine sie schreibt.
// now ist die Uhr des Prüfenden.
func ValidateRequestLine(l RequestLine, now time.Time) error {
	if l.Kind != KindRequest {
		return errors.New("kind ist nicht request")
	}
	if err := checkHead(l.RequestID, l.MessageID, l.SessionID, l.TS, l.UTCOffsetMin, now); err != nil {
		return err
	}
	if !ModelPattern.MatchString(l.Model) {
		return errors.New("model ungültig")
	}
	if err := firstErr(
		checkOptional(IDPattern, [2]string{"uuid", l.UUID}, [2]string{"parent_uuid", l.ParentUUID},
			[2]string{"account_id", l.AccountID}),
		checkOptional(ModelPattern, [2]string{"requested_model", l.RequestedModel}),
		checkOptional(StopPattern, [2]string{"effort", l.Effort}, [2]string{"entrypoint", l.Entrypoint},
			[2]string{"stop_reason", l.StopReason}, [2]string{"prompt_cache_requested", l.PromptCacheRequested},
			[2]string{"cache_miss_reason", l.CacheMissReason}),
		checkOptional(VersionPattern, [2]string{"cc_version", l.CCVersion}),
		checkOptional(AttributionPattern, [2]string{"attribution_plugin", l.AttributionPlugin},
			[2]string{"attribution_agent", l.AttributionAgent}),
		checkRange("tool_input_stream_ms", l.ToolInputStreamMs, 0, MaxDurationMs),
		checkRange("thinking_duration_ms", l.ThinkingDurationMs, 0, MaxDurationMs),
	); err != nil {
		return err
	}
	if len(l.Tools) > MaxTools {
		return fmt.Errorf("mehr als %d Werkzeuge", MaxTools)
	}
	for name, n := range l.Tools {
		if !ToolPattern.MatchString(name) || n < 1 || n > MaxCountPerField {
			return errors.New("Werkzeug ungültig")
		}
	}
	if l.Usage == nil {
		return errors.New("usage fehlt")
	}
	n := 0
	if err := checkUsage(l.Usage, 1, &n); err != nil {
		return err
	}
	if _, err := UsageCounts(l.Usage); err != nil {
		return err
	}
	b, err := MarshalLine(l)
	if err != nil {
		return fmt.Errorf("Zeile nicht schreibbar: %w", err)
	}
	if len(b) > MaxLineBytes {
		return fmt.Errorf("Zeile länger als %d Byte", MaxLineBytes)
	}
	return nil
}

// ValidateErrorLine prüft die Inhaltszeile eines API-Fehlers nach den Regeln, mit denen der Server sie prüft
// (protocol/doc.md, „Inhaltszeile: Fehler“). Andere Schlüssel lehnt der strenge Leser ab, der z in ErrorLine liest.
// Länger als MaxLineBytes kann die Zeile in ihren Grenzen nicht werden.
func ValidateErrorLine(e ErrorLine, now time.Time) error {
	if e.Kind != KindError {
		return errors.New("kind ist nicht error")
	}
	return firstErr(
		checkHead(e.RequestID, e.MessageID, e.SessionID, e.TS, e.UTCOffsetMin, now),
		checkOptional(IDPattern, [2]string{"uuid", e.UUID}, [2]string{"parent_uuid", e.ParentUUID},
			[2]string{"account_id", e.AccountID}),
		checkOptional(StopPattern, [2]string{"entrypoint", e.Entrypoint}, [2]string{"error", e.Error},
			[2]string{"rate_limit_type", e.RateLimitType}, [2]string{"limit_status", e.LimitStatus},
			[2]string{"overage_status", e.OverageStatus}),
		checkOptional(VersionPattern, [2]string{"cc_version", e.CCVersion}),
		checkRange("api_error_status", e.APIErrorStatus, MinAPIErrorStatus, MaxAPIErrorStatus),
		checkRange("resets_at", e.ResetsAt, 0, MaxResetsAt),
	)
}

// NeedsRequestDetails sagt, ob die Inhaltszeile z das Merkmal request_details des Servers braucht: eine Fehlerzeile
// oder eine Anfrage mit einem der Felder, die nur dieses Merkmal kennt (protocol/doc.md, „Server-Kennung“).
// Eine Zeile, die kein JSON-Objekt ist, braucht es nicht; sie scheitert an der Prüfung.
func NeedsRequestDetails(z string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(z), &m) != nil {
		return false
	}
	var kind string
	if json.Unmarshal(m["kind"], &kind) == nil && kind == KindError {
		return true
	}
	for _, k := range []string{"prompt_cache_requested", "tool_input_stream_ms", "thinking_duration_ms",
		"attribution_plugin", "attribution_agent", "cache_miss_reason"} {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

// checkHead prüft die Pflichtfelder, die Anfrage- und Fehlerzeile gemeinsam haben.
func checkHead(requestID, messageID, sessionID string, ts time.Time, utcOffsetMin int, now time.Time) error {
	if !IDPattern.MatchString(messageID) {
		return errors.New("message_id ungültig")
	}
	if requestID != "" && !IDPattern.MatchString(requestID) {
		return errors.New("request_id ungültig")
	}
	if !IDPattern.MatchString(sessionID) {
		return errors.New("session_id ungültig")
	}
	if !TSPattern.MatchString(ts.Format(time.RFC3339Nano)) {
		return errors.New("ts nicht in UTC")
	}
	if ts.Before(Earliest) {
		return errors.New("ts vor 2024")
	}
	if ts.After(now.Add(MaxFutureSkew)) {
		return errors.New("ts liegt in der Zukunft")
	}
	if utcOffsetMin < -MaxUTCOffsetMin || utcOffsetMin > MaxUTCOffsetMin {
		return errors.New("utc_offset_min außerhalb ±14 h")
	}
	return nil
}

// checkOptional prüft optionale Texte {Name, Wert}: leer (fehlt) oder passend zu p.
func checkOptional(p *regexp.Regexp, fields ...[2]string) error {
	for _, f := range fields {
		if f[1] != "" && !p.MatchString(f[1]) {
			return errors.New(f[0] + " ungültig")
		}
	}
	return nil
}

// checkRange prüft eine optionale Zahl: nil (fehlt) oder in lo – hi.
func checkRange(name string, v *int64, lo, hi int64) error {
	if v != nil && (*v < lo || *v > hi) {
		return errors.New(name + " außerhalb des Bereichs")
	}
	return nil
}

// firstErr ist der erste Fehler aus errs, sonst nil.
func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// ValidateSession prüft eine Sitzung.
func ValidateSession(s Session) error {
	if !IDPattern.MatchString(s.SessionID) {
		return errors.New("session_id ungültig")
	}
	if s.CCVersion != "" && !VersionPattern.MatchString(s.CCVersion) {
		return errors.New("cc_version ungültig")
	}
	if s.AccountID != "" && !IDPattern.MatchString(s.AccountID) {
		return errors.New("account_id ungültig")
	}
	return nil
}

// ValidateAccount prüft ein Konto. Bei "off" darf keine Adresse mitkommen.
func ValidateAccount(a Account) error {
	if !IDPattern.MatchString(a.AccountID) {
		return errors.New("account_id ungültig")
	}
	switch a.EmailMode {
	case EmailOff:
		if a.Email != "" {
			return errors.New("Adresse trotz email_mode off")
		}
	case EmailShort, EmailFull:
		if a.Email != "" && (len(a.Email) > MaxEmailLen || !strings.Contains(a.Email, "@") ||
			hasControl(a.Email) || HasSpoofRune(a.Email) || strings.ContainsAny(a.Email, " <>\"")) {
			return errors.New("Adresse ungültig")
		}
	default:
		return errors.New("email_mode ungültig")
	}
	return nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// HasSpoofRune erkennt Format-, Zeilen-, Absatztrenner- und Privatzeichen (z. B. U+202E, U+200B, U+2028),
// die in der Oberfläche Text vortäuschen könnten. hasControl erfasst nur Steuerzeichen.
func HasSpoofRune(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || unicode.Is(unicode.Co, r) {
			return true
		}
	}
	return false
}

// ValidateDevice prüft die Gerätedaten eines Pakets.
func ValidateDevice(d DeviceInfo) error {
	switch d.OS {
	case "windows", "linux", "darwin":
	default:
		return errors.New("device.os nicht erlaubt")
	}
	switch d.Arch {
	case "amd64", "arm64":
	default:
		return errors.New("device.arch nicht erlaubt")
	}
	// "Local" würde die Zeitzone des Servers statt der des Geräts meinen.
	if d.TZ == "" || len(d.TZ) > MaxTZLen || d.TZ == "Local" {
		return errors.New("device.tz ungültig")
	}
	if _, err := time.LoadLocation(d.TZ); err != nil {
		return errors.New("device.tz unbekannte Zeitzone")
	}
	if !VersionPattern.MatchString(d.PluginVersion) {
		return errors.New("device.plugin_version ungültig")
	}
	return nil
}

var legacyDatePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// ValidateCompleteBefore prüft legacy_complete_before: ein gültiges Kalenderdatum YYYY-MM-DD. Es darf in der
// Zukunft liegen (der Server lässt es dann nur bis heute wirken).
func ValidateCompleteBefore(s string) error {
	if !legacyDatePattern.MatchString(s) {
		return errors.New("legacy_complete_before: Format YYYY-MM-DD erwartet")
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return errors.New("legacy_complete_before: kein gültiges Datum")
	}
	return nil
}

// checkLegacyDate lässt Tage von 2024-01-01 bis einschließlich morgen (UTC) zu; morgen wegen Zeitzonen.
func checkLegacyDate(s string, now time.Time) error {
	if !legacyDatePattern.MatchString(s) {
		return errors.New("date: Format YYYY-MM-DD erwartet")
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return errors.New("date: kein gültiges Datum")
	}
	n := now.UTC()
	latest := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	if t.Before(Earliest) || t.After(latest) {
		return errors.New("date: außerhalb des erlaubten Zeitraums")
	}
	return nil
}

// ValidDeviceName prüft einen vom Nutzer vergebenen Gerätenamen.
func ValidDeviceName(n string) bool {
	if !utf8.ValidString(n) {
		return false
	}
	if l := utf8.RuneCountInString(n); l < 1 || l > MaxDeviceName {
		return false
	}
	return !hasControl(n) && !HasSpoofRune(n)
}

package protocol

import (
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
	if !IDPattern.MatchString(l.MessageID) {
		return errors.New("message_id ungültig")
	}
	if l.RequestID != "" && !IDPattern.MatchString(l.RequestID) {
		return errors.New("request_id ungültig")
	}
	if !IDPattern.MatchString(l.SessionID) {
		return errors.New("session_id ungültig")
	}
	for _, f := range [][2]string{{"uuid", l.UUID}, {"parent_uuid", l.ParentUUID}, {"account_id", l.AccountID}} {
		if f[1] != "" && !IDPattern.MatchString(f[1]) {
			return errors.New(f[0] + " ungültig")
		}
	}
	if !TSPattern.MatchString(l.TS.Format(time.RFC3339Nano)) {
		return errors.New("ts nicht in UTC")
	}
	if l.TS.Before(Earliest) {
		return errors.New("ts vor 2024")
	}
	if l.TS.After(now.Add(MaxFutureSkew)) {
		return errors.New("ts liegt in der Zukunft")
	}
	if l.UTCOffsetMin < -MaxUTCOffsetMin || l.UTCOffsetMin > MaxUTCOffsetMin {
		return errors.New("utc_offset_min außerhalb ±14 h")
	}
	if !ModelPattern.MatchString(l.Model) {
		return errors.New("model ungültig")
	}
	if l.RequestedModel != "" && !ModelPattern.MatchString(l.RequestedModel) {
		return errors.New("requested_model ungültig")
	}
	for _, f := range [][2]string{{"effort", l.Effort}, {"entrypoint", l.Entrypoint}, {"stop_reason", l.StopReason}} {
		if f[1] != "" && !StopPattern.MatchString(f[1]) {
			return errors.New(f[0] + " ungültig")
		}
	}
	if l.CCVersion != "" && !VersionPattern.MatchString(l.CCVersion) {
		return errors.New("cc_version ungültig")
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

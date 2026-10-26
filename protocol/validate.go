package protocol

import (
	"errors"
	"fmt"
	"math"
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
	ProjectPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Earliest ist der früheste zulässige Zeitstempel. Zeitpunkte in der Zukunft lehnt ValidateRequest ab (MaxFutureSkew).
var Earliest = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// ValidateRequest prüft eine Anfrage gegen die Feldgrenzen. request_id darf leer sein
// (manche Backends liefern keine), message_id nicht. now ist die Uhr des Prüfenden; Plugin und Server
// prüfen mit derselben Funktion.
func ValidateRequest(r Request, now time.Time) error {
	if !IDPattern.MatchString(r.MessageID) {
		return errors.New("message_id ungültig")
	}
	if r.RequestID != "" && !IDPattern.MatchString(r.RequestID) {
		return errors.New("request_id ungültig")
	}
	if !IDPattern.MatchString(r.SessionID) {
		return errors.New("session_id ungültig")
	}
	if r.AccountID != "" && !IDPattern.MatchString(r.AccountID) {
		return errors.New("account_id ungültig")
	}
	if !ModelPattern.MatchString(r.Model) {
		return errors.New("model ungültig")
	}
	if r.TS.Before(Earliest) {
		return errors.New("ts vor 2024")
	}
	if r.TS.After(now.Add(MaxFutureSkew)) {
		return errors.New("ts liegt in der Zukunft")
	}
	if r.UTCOffsetMin < -14*60 || r.UTCOffsetMin > 14*60 {
		return errors.New("utc_offset_min außerhalb ±14 h")
	}
	for _, v := range []int64{r.Input, r.Output, r.CacheCreate, r.CacheRead, r.Cache1h, r.Cache5m, r.Thinking} {
		if v < 0 || v > MaxTokensPerField {
			return errors.New("Token-Wert außerhalb des Bereichs")
		}
	}
	if r.WebSearch < 0 || r.WebSearch > MaxCountPerField || r.WebFetch < 0 || r.WebFetch > MaxCountPerField {
		return errors.New("web_search/web_fetch außerhalb des Bereichs")
	}
	if r.StopReason != "" && !StopPattern.MatchString(r.StopReason) {
		return errors.New("stop_reason ungültig")
	}
	if len(r.Tools) > MaxTools {
		return fmt.Errorf("mehr als %d Werkzeuge", MaxTools)
	}
	for name, n := range r.Tools {
		if !ToolPattern.MatchString(name) || n < 1 || n > MaxCountPerField {
			return errors.New("Werkzeug ungültig")
		}
	}
	if len(r.Extra) > MaxExtraFields {
		return fmt.Errorf("mehr als %d extra-Felder", MaxExtraFields)
	}
	for k, v := range r.Extra {
		if !ExtraKey.MatchString(k) {
			return errors.New("extra-Schlüssel ungültig")
		}
		switch x := v.(type) {
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return errors.New("extra-Zahl ungültig")
			}
		case string:
			if len([]rune(x)) > MaxExtraString || hasControl(x) || HasSpoofRune(x) {
				return errors.New("extra-Text ungültig")
			}
		default:
			return errors.New("extra-Wert ist weder Zahl noch Text")
		}
	}
	return nil
}

// ValidateSession prüft eine Sitzung.
func ValidateSession(s Session) error {
	if !IDPattern.MatchString(s.SessionID) {
		return errors.New("session_id ungültig")
	}
	if s.ProjectID != "" && !ProjectPattern.MatchString(s.ProjectID) {
		return errors.New("project_id ungültig")
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

// ValidateLegacyModel prüft einen Tageswert je Modell aus dem Altbestand.
func ValidateLegacyModel(d LegacyModelDay, now time.Time) error {
	if err := checkLegacyDate(d.Date, now); err != nil {
		return err
	}
	if !ModelPattern.MatchString(d.Model) {
		return errors.New("model ungültig")
	}
	if d.Tokens < 0 || d.Tokens > MaxLegacyNumber {
		return errors.New("tokens außerhalb des Bereichs")
	}
	return nil
}

// ValidateLegacyActivity prüft einen Aktivitäts-Tageswert aus dem Altbestand.
func ValidateLegacyActivity(d LegacyActivityDay, now time.Time) error {
	if err := checkLegacyDate(d.Date, now); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		v    int
	}{{"messages", d.Messages}, {"sessions", d.Sessions}, {"tool_calls", d.ToolCalls}} {
		if f.v < 0 || int64(f.v) > MaxLegacyNumber {
			return errors.New(f.name + " außerhalb des Bereichs")
		}
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

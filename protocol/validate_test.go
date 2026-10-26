package protocol

import (
	"strings"
	"testing"
	"time"
)

func validRequest() Request {
	return Request{RequestID: "req_011CU", MessageID: "msg_01AB", SessionID: "0a1b2c3d-0000-4000-8000-000000000000",
		TS: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Model: "claude-opus-5-5", StopReason: "end_turn",
		Tools: map[string]int{"Read": 1, "mcp__github__get_issue": 2}, Extra: map[string]any{"speed": "fast"}}
}

func TestValidateRequestAcceptsReal(t *testing.T) {
	if err := ValidateRequest(validRequest(), testNow); err != nil {
		t.Fatal(err)
	}
	r := validRequest()
	r.RequestID = "" // Fremd-Backends liefern keine requestId
	if err := ValidateRequest(r, testNow); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRequestRejects(t *testing.T) {
	cases := map[string]func(*Request){
		"message_id leer":      func(r *Request) { r.MessageID = "" },
		"request_id Zeichen":   func(r *Request) { r.RequestID = "a b" },
		"session_id zu lang":   func(r *Request) { r.SessionID = strings.Repeat("a", MaxIDLen+1) },
		"Modell":               func(r *Request) { r.Model = "a/b" },
		"negativ":              func(r *Request) { r.Output = -1 },
		"stop_reason":          func(r *Request) { r.StopReason = strings.Repeat("x", MaxShortText+1) },
		"Werkzeugname":         func(r *Request) { r.Tools = map[string]int{"<script>": 1} },
		"zu viele Werkzeuge":   func(r *Request) { r.Tools = manyTools(MaxTools + 1) },
		"Werkzeug negativ":     func(r *Request) { r.Tools = map[string]int{"Read": -1} },
		"extra Objekt":         func(r *Request) { r.Extra = map[string]any{"o": map[string]any{}} },
		"extra Schlüssel":      func(r *Request) { r.Extra = map[string]any{"a b": 1.0} },
		"extra Text zu lang":   func(r *Request) { r.Extra = map[string]any{"t": strings.Repeat("ü", MaxExtraString+1)} },
		"Zeitstempel vor 2024": func(r *Request) { r.TS = time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC) },
	}
	for name, mod := range cases {
		r := validRequest()
		mod(&r)
		if err := ValidateRequest(r, testNow); err == nil {
			t.Errorf("%s: erwartet Fehler", name)
		}
	}
}

func manyTools(n int) map[string]int {
	m := map[string]int{}
	for i := 0; i < n; i++ {
		m["t"+strings.Repeat("x", i%50)+string(rune('a'+i%26))+strings.Repeat("y", i/26)] = 1
	}
	return m
}

func TestValidateSessionAndAccount(t *testing.T) {
	if err := ValidateSession(Session{SessionID: "s1", CCVersion: "2.1.309", ProjectID: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	if ValidateSession(Session{SessionID: "s1", CCVersion: "2.1.309; rm"}) == nil {
		t.Error("cc_version mit Sonderzeichen akzeptiert")
	}
	if ValidateSession(Session{SessionID: "s1", ProjectID: "nicht-hex"}) == nil {
		t.Error("project_id ohne Hex akzeptiert")
	}
	if err := ValidateAccount(Account{AccountID: "acc-1", EmailMode: EmailShort, Email: "ma…@beispiel.de"}); err != nil {
		t.Fatal(err)
	}
	if ValidateAccount(Account{AccountID: "acc-1", EmailMode: "egal"}) == nil {
		t.Error("unbekannte email_mode akzeptiert")
	}
	if ValidateAccount(Account{AccountID: "acc-1", EmailMode: EmailOff, Email: "x@y.de"}) == nil {
		t.Error("Adresse bei off akzeptiert")
	}
	if ValidateAccount(Account{AccountID: "acc-1", EmailMode: EmailFull, Email: strings.Repeat("a", MaxEmailLen) + "@b.de"}) == nil {
		t.Error("zu lange Adresse akzeptiert")
	}
}

var testNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func TestValidateRequestRejectsFuture(t *testing.T) {
	r := validRequest()
	r.TS = testNow.Add(4 * time.Minute)
	if err := ValidateRequest(r, testNow); err != nil {
		t.Fatalf("4 min Zukunft: %v", err)
	}
	r.TS = testNow.Add(6 * time.Minute)
	if ValidateRequest(r, testNow) == nil {
		t.Error("6 min Zukunft akzeptiert")
	}
}

func TestValidateRequestRejectsSpoofInExtra(t *testing.T) {
	for _, s := range []string{"a‮b", "a​b"} {
		r := validRequest()
		r.Extra = map[string]any{"x": s}
		if ValidateRequest(r, testNow) == nil {
			t.Errorf("Formatzeichen in %q akzeptiert", s)
		}
	}
}

func TestValidateAccountRejectsSpoof(t *testing.T) {
	if ValidateAccount(Account{AccountID: "acc-1", EmailMode: EmailFull, Email: "ma‮@x.de"}) == nil {
		t.Error("Formatzeichen in Adresse akzeptiert")
	}
}

func TestValidateDevice(t *testing.T) {
	ok := DeviceInfo{OS: "windows", Arch: "amd64", TZ: "Europe/Berlin", PluginVersion: "0.1.0"}
	if err := ValidateDevice(ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*DeviceInfo){
		"os":             func(d *DeviceInfo) { d.OS = "plan9" },
		"arch":           func(d *DeviceInfo) { d.Arch = "386" },
		"tz leer":        func(d *DeviceInfo) { d.TZ = "" },
		"tz Local":       func(d *DeviceInfo) { d.TZ = "Local" },
		"tz unbekannt":   func(d *DeviceInfo) { d.TZ = "Mars/Olympus" },
		"tz zu lang":     func(d *DeviceInfo) { d.TZ = strings.Repeat("a", MaxTZLen+1) },
		"plugin_version": func(d *DeviceInfo) { d.PluginVersion = "1 0" },
	}
	for name, mut := range cases {
		d := ok
		mut(&d)
		if ValidateDevice(d) == nil {
			t.Errorf("%s: akzeptiert", name)
		}
	}
}

func TestValidateLegacy(t *testing.T) {
	day := func(offset int) string { return testNow.AddDate(0, 0, offset).Format("2006-01-02") }
	dates := map[string]bool{
		"2023-12-31": false, "2024-01-01": true, day(1): true, day(2): false,
		"2026-02-30": false, "26-10-24": false,
	}
	for date, want := range dates {
		m := ValidateLegacyModel(LegacyModelDay{Date: date, Model: "claude-opus-5-5", Tokens: 1}, testNow)
		a := ValidateLegacyActivity(LegacyActivityDay{Date: date, Messages: 1}, testNow)
		if (m == nil) != want || (a == nil) != want {
			t.Errorf("Datum %s: Modell %v, Aktivität %v, erwartet gültig=%v", date, m, a, want)
		}
	}
	m := LegacyModelDay{Date: "2026-10-01", Model: "claude-opus-5-5", Tokens: MaxLegacyNumber}
	if err := ValidateLegacyModel(m, testNow); err != nil {
		t.Fatalf("Obergrenze: %v", err)
	}
	for name, d := range map[string]LegacyModelDay{
		"tokens -1":      {Date: "2026-10-01", Model: "m", Tokens: -1},
		"tokens zu groß": {Date: "2026-10-01", Model: "m", Tokens: MaxLegacyNumber + 1},
		"Modell":         {Date: "2026-10-01", Model: "a b", Tokens: 1},
	} {
		if ValidateLegacyModel(d, testNow) == nil {
			t.Errorf("%s: akzeptiert", name)
		}
	}
	for name, d := range map[string]LegacyActivityDay{
		"messages -1": {Date: "2026-10-01", Messages: -1},
		"sessions -1": {Date: "2026-10-01", Sessions: -1},
		"tools -1":    {Date: "2026-10-01", ToolCalls: -1},
		"zu groß":     {Date: "2026-10-01", Messages: MaxLegacyNumber + 1},
	} {
		if ValidateLegacyActivity(d, testNow) == nil {
			t.Errorf("%s: akzeptiert", name)
		}
	}
}

func TestValidDeviceName(t *testing.T) {
	for n, want := range map[string]bool{
		"Mein PC": true, "": false, strings.Repeat("a", 65): false, "a\nb": false,
		"a‮b": false, "\xff": false, strings.Repeat("ü", 64): true,
	} {
		if ValidDeviceName(n) != want {
			t.Errorf("ValidDeviceName(%q) != %v", n, want)
		}
	}
}

func TestDeviceKeyPattern(t *testing.T) {
	a := func(n int) string { return strings.Repeat("A", n) }
	for k, want := range map[string]bool{
		"tl_dk_" + a(43): true, "tl_dk_" + a(42): false, "tl_dk_" + a(44): false, "tl_dk_" + a(42) + "=": false,
	} {
		if DeviceKeyPattern.MatchString(k) != want {
			t.Errorf("%q: erwartet %v", k, want)
		}
	}
}

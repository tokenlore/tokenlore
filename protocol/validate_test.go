package protocol

import (
	"strings"
	"testing"
	"time"
)

func manyTools(n int) map[string]int {
	m := map[string]int{}
	for i := 0; i < n; i++ {
		m["t"+strings.Repeat("x", i%50)+string(rune('a'+i%26))+strings.Repeat("y", i/26)] = 1
	}
	return m
}

func TestValidateSessionAndAccount(t *testing.T) {
	if err := ValidateSession(Session{SessionID: "s1", CCVersion: "2.1.309"}); err != nil {
		t.Fatal(err)
	}
	if ValidateSession(Session{SessionID: "s1", CCVersion: "2.1.309; rm"}) == nil {
		t.Error("cc_version mit Sonderzeichen akzeptiert")
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

func TestValidateRequestLineRejectsFuture(t *testing.T) {
	l := validLine(t)
	l.TS = testNow.Add(4 * time.Minute)
	if err := ValidateRequestLine(l, testNow); err != nil {
		t.Fatalf("4 min Zukunft: %v", err)
	}
	l.TS = testNow.Add(6 * time.Minute)
	if ValidateRequestLine(l, testNow) == nil {
		t.Error("6 min Zukunft akzeptiert")
	}
}

func TestValidateRequestLineRejectsSpoofInUsage(t *testing.T) {
	for _, s := range []string{"a\u202eb", "a\u200bb"} {
		l := validLine(t)
		l.Usage["service_tier"] = s
		if ValidateRequestLine(l, testNow) == nil {
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

func TestValidateDayLineDates(t *testing.T) {
	day := func(offset int) string { return testNow.AddDate(0, 0, offset).Format("2006-01-02") }
	dates := map[string]bool{
		"2023-12-31": false, "2024-01-01": true, day(1): true, day(2): false,
		"2026-02-30": false, "26-10-24": false,
	}
	for date, want := range dates {
		m := ValidateDayLine(DayLine{Kind: KindDay, Date: date, Models: map[string]int64{"claude-opus-5-5": 1}}, testNow)
		a := ValidateDayLine(DayLine{Kind: KindDay, Date: date, Activity: &DayActivity{Messages: 1}}, testNow)
		if (m == nil) != want || (a == nil) != want {
			t.Errorf("Datum %s: Modell %v, Aktivität %v, erwartet gültig=%v", date, m, a, want)
		}
	}
	if err := ValidateDayLine(DayLine{Kind: KindDay, Date: "2026-10-01", Models: map[string]int64{"m": MaxLegacyNumber}}, testNow); err != nil {
		t.Fatalf("Obergrenze: %v", err)
	}
	for name, d := range map[string]DayLine{
		"tokens -1":      {Kind: KindDay, Date: "2026-10-01", Models: map[string]int64{"m": -1}},
		"tokens zu groß": {Kind: KindDay, Date: "2026-10-01", Models: map[string]int64{"m": MaxLegacyNumber + 1}},
		"Modell":         {Kind: KindDay, Date: "2026-10-01", Models: map[string]int64{"a b": 1}},
		"messages -1":    {Kind: KindDay, Date: "2026-10-01", Activity: &DayActivity{Messages: -1}},
		"sessions -1":    {Kind: KindDay, Date: "2026-10-01", Activity: &DayActivity{Sessions: -1}},
		"tools -1":       {Kind: KindDay, Date: "2026-10-01", Activity: &DayActivity{ToolCalls: -1}},
		"zu groß":        {Kind: KindDay, Date: "2026-10-01", Activity: &DayActivity{Messages: MaxLegacyNumber + 1}},
	} {
		if ValidateDayLine(d, testNow) == nil {
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

func TestValidateCompleteBefore(t *testing.T) {
	for _, ok := range []string{"2026-08-15", "2099-12-31", "2024-02-29"} {
		if err := ValidateCompleteBefore(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "2026-02-30", "2026-8-15", "2026-08-15T00:00:00Z", " 2026-08-15", "15.08.2026"} {
		if err := ValidateCompleteBefore(bad); err == nil {
			t.Errorf("%q angenommen", bad)
		}
	}
}

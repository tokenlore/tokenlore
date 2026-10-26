package email

import "testing"

func TestMask(t *testing.T) {
	cases := map[string]string{
		"max.mustermann@beispiel.de": "ma…@beispiel.de",
		"a@b.de":                     "a…@b.de",
		"ab@b.de":                    "a…@b.de",
		"abc@b.de":                   "ab…@b.de",
		"üöx@b.de":                   "üö…@b.de",
		"kaputt":                     "",
		"@b.de":                      "",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestForModeDefaultOff(t *testing.T) {
	x := "max@beispiel.de"
	if ForMode("off", x) != "" || ForMode("", x) != "" || ForMode("quatsch", x) != "" {
		t.Fatal("off/leer/unbekannt muss leer ergeben")
	}
	if ForMode("short", x) != "ma…@beispiel.de" || ForMode("full", x) != x {
		t.Fatal("short/full falsch")
	}
}

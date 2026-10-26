package main

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		"":                                  nil,
		"   ":                               nil,
		"full":                              {"full"},
		"https://x 123456 --ca AB:CD":       {"https://x", "123456", "--ca", "AB:CD"},
		`https://x 123456 --name "Mein PC"`: {"https://x", "123456", "--name", "Mein PC"},
		`--name ""`:                         {"--name", ""},
		"a\tb\r\nc":                         {"a", "b", "c"},
		`x; rm -rf ~ $(id) ` + "`id`":       {"x;", "rm", "-rf", "~", "$(id)", "`id`"},
		`--name "Mein ""PC"""`:              {"--name", "Mein PC"},
	}
	for in, want := range cases {
		got, err := splitArgs(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("splitArgs(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := splitArgs(`--name "Mein PC`); err != errUnclosedQuote {
		t.Fatalf("offenes Anführungszeichen: %v", err)
	}
}

func TestPluginArgs(t *testing.T) {
	got, err := pluginArgs([]string{"--args", `https://x 123456 --name "Mein PC"`})
	if err != nil || !reflect.DeepEqual(got, []string{"https://x", "123456", "--name", "Mein PC"}) {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, _ := pluginArgs([]string{"--args", ""}); len(got) != 0 {
		t.Fatalf("leer: %q", got)
	}
	// Ein ' im Text bricht die Klammer der Befehlsdatei auf: Die Shell liefert dann mehrere Teile.
	for _, broken := range [][]string{{"--args"}, {"--args", "https://x 123456 --name Annas", "s PC"}} {
		if _, err := pluginArgs(broken); err != errSingleQuote {
			t.Fatalf("%q: %v", broken, err)
		}
	}
	// Direkter Aufruf (Terminal, Hook) bleibt unverändert.
	if got, _ := pluginArgs(nil); len(got) != 0 {
		t.Fatalf("nil: %q", got)
	}
	direct := []string{"https://x", "123456"}
	if got, _ := pluginArgs(direct); !reflect.DeepEqual(got, direct) {
		t.Fatalf("direkt: %q", got)
	}
}

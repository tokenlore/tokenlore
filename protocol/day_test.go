package protocol

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var dayNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// Die Vorgeschichte-Zeile aus doc.md lässt sich als DayLine lesen, besteht die Prüfung, und ihre Signatur passt.
func TestDayLineMatchesDocExample(t *testing.T) {
	doc, err := os.ReadFile("doc.md")
	if err != nil {
		t.Fatal(err)
	}
	ex := regexp.MustCompile(`(?m)^\{"z":"\{\\"kind\\":\\"day\\".*$`).Find(doc)
	if ex == nil {
		t.Fatal("Beispielzeile nicht gefunden")
	}
	var h Envelope
	if err := json.Unmarshal(ex, &h); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(h.Z))
	dec.DisallowUnknownFields()
	var d DayLine
	if err := dec.Decode(&d); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDayLine(d, dayNow); err != nil {
		t.Fatal(err)
	}
	if d.Activity == nil || d.Activity.ToolCalls != 57 || d.Models["claude-haiku-4-5"] != 4100 || d.CompleteBefore != "2026-10-20" {
		t.Fatalf("gelesen: %+v", d)
	}
	pub := EncodeSignPublicKey(testSignKey(t).Public().(ed25519.PublicKey))
	if !VerifyLine(pub, []byte(h.Z), h.S) {
		t.Fatal("Signatur der Beispielzeile passt nicht")
	}
}

func TestDayLineMarshalFieldOrder(t *testing.T) {
	d := DayLine{Kind: KindDay, Date: "2026-03-14", Models: map[string]int64{"m": 1},
		Activity: &DayActivity{Messages: 2, Sessions: 3, ToolCalls: 4}, CompleteBefore: "2026-10-20"}
	b, err := MarshalLine(d)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"kind":"day","date":"2026-03-14","models":{"m":1},"activity":{"messages":2,"sessions":3,"tool_calls":4},"complete_before":"2026-10-20"}`
	if string(b) != want {
		t.Fatalf("%s", b)
	}
	b, _ = MarshalLine(DayLine{Kind: KindDay, Date: "2026-03-14", Models: map[string]int64{"m": 1}})
	if string(b) != `{"kind":"day","date":"2026-03-14","models":{"m":1}}` {
		t.Fatalf("optionale Felder nicht weggelassen: %s", b)
	}
}

func TestValidateDayLine(t *testing.T) {
	ok := DayLine{Kind: KindDay, Date: "2026-03-14", Models: map[string]int64{"claude-opus-5-5": 5}}
	if err := ValidateDayLine(ok, dayNow); err != nil {
		t.Fatal(err)
	}
	many := map[string]int64{}
	for i := range MaxLegacyModels + 1 {
		many[strings.Repeat("m", 1)+string(rune('a'+i%26))+string(rune('a'+i/26))] = 1
	}
	for name, d := range map[string]DayLine{
		"kind":             {Kind: KindRequest, Date: "2026-03-14", Models: ok.Models},
		"ohne Werte":       {Kind: KindDay, Date: "2026-03-14"},
		"Datum vor 2024":   {Kind: KindDay, Date: "2023-12-31", Models: ok.Models},
		"Datum Zukunft":    {Kind: KindDay, Date: "2026-10-26", Models: ok.Models},
		"Datum Form":       {Kind: KindDay, Date: "2026-3-14", Models: ok.Models},
		"Modellname":       {Kind: KindDay, Date: "2026-03-14", Models: map[string]int64{"a b": 1}},
		"Tokens negativ":   {Kind: KindDay, Date: "2026-03-14", Models: map[string]int64{"m": -1}},
		"Tokens zu groß":   {Kind: KindDay, Date: "2026-03-14", Models: map[string]int64{"m": MaxLegacyNumber + 1}},
		"zu viele Modelle": {Kind: KindDay, Date: "2026-03-14", Models: many},
		"Aktivität":        {Kind: KindDay, Date: "2026-03-14", Activity: &DayActivity{Messages: -1}},
		"complete_before":  {Kind: KindDay, Date: "2026-03-14", Models: ok.Models, CompleteBefore: "gestern"},
	} {
		if ValidateDayLine(d, dayNow) == nil {
			t.Errorf("%s: angenommen", name)
		}
	}
}

func TestSealLine(t *testing.T) {
	priv := testSignKey(t)
	z := []byte(`{"kind":"day","date":"2026-03-14","models":{"a<b":1}}`)
	b, err := SealLine(priv, z)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("\n")) || bytes.Contains(b, []byte("\\u003c")) || !bytes.Contains(b, []byte("a<b")) {
		t.Fatalf("Hülle nicht kompakt oder HTML-maskiert: %s", b)
	}
	var h Envelope
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	pub := EncodeSignPublicKey(priv.Public().(ed25519.PublicKey))
	if h.Z != string(z) || !VerifyLine(pub, []byte(h.Z), h.S) {
		t.Fatalf("Hülle %s", b)
	}
}

func TestSignMonth(t *testing.T) {
	priv := testSignKey(t)
	pub := EncodeSignPublicKey(priv.Public().(ed25519.PublicKey))
	gz := []byte{0x1f, 0x8b, 1, 2, 3}
	sig := SignMonth(priv, gz)
	if !VerifyMonth(pub, gz, sig) {
		t.Fatal("eigene Signatur abgelehnt")
	}
	if VerifyMonth(pub, append(gz, 4), sig) {
		t.Fatal("geänderte Datei angenommen")
	}
	// Eine Zeilensignatur über dieselben Bytes gilt nicht als Monatssignatur (anderer Vorsatz).
	if VerifyMonth(pub, gz, SignLine(priv, gz)) {
		t.Fatal("Zeilensignatur als Monatssignatur angenommen")
	}
}

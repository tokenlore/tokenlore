package protocol

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// decodeUsage dekodiert usage wie der Parser: Zahlen als json.Number.
func decodeUsage(t *testing.T, s string) map[string]any {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func validLine(t *testing.T) RequestLine {
	return RequestLine{Kind: KindRequest, RequestID: "req_1", MessageID: "msg_1", SessionID: "s1",
		UUID: "u1", ParentUUID: "p1", TS: time.Date(2026, 10, 23, 21, 30, 0, 0, time.UTC), UTCOffsetMin: 120,
		Model: "claude-opus-5-5", RequestedModel: "claude-opus-5-5", Effort: "medium", Entrypoint: "cli",
		CCVersion: "2.1.309", StopReason: "tool_use", Tools: map[string]int{"Read": 1}, AccountID: "acc-1",
		Usage: decodeUsage(t, `{"input_tokens":10,"output_tokens":200,"server_tool_use":{"web_search_requests":1},"speed":"standard"}`)}
}

// Die Beispielzeile aus doc.md passt zum Typ: gleiche Schlüssel, gültig nach ValidateRequestLine, Signatur gültig
// mit dem Testschlüssel. So bleiben Doku, Typ und Prüfung beieinander.
func TestRequestLineMatchesDocExample(t *testing.T) {
	doc, err := os.ReadFile("doc.md")
	if err != nil {
		t.Fatal(err)
	}
	ex := regexp.MustCompile(`(?m)^\{"z":"\{\\"kind\\":\\"request\\".*$`).Find(doc)
	if ex == nil {
		t.Fatal("Beispielzeile nicht gefunden")
	}
	var h struct{ Z, S string }
	if err := json.Unmarshal(ex, &h); err != nil {
		t.Fatal(err)
	}
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	sig, _ := base64.StdEncoding.DecodeString(h.S)
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if !ed25519.Verify(pub, append([]byte("tokenlore-archiv-v1\n"), h.Z...), sig) {
		t.Fatal("Signatur der Beispielzeile ungültig")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(h.Z), &raw); err != nil {
		t.Fatal(err)
	}
	var tags []string
	rt := reflect.TypeOf(RequestLine{})
	for i := 0; i < rt.NumField(); i++ {
		tags = append(tags, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
	}
	for k := range raw {
		if !slices.Contains(tags, k) {
			t.Errorf("Feld %q der Beispielzeile fehlt im Typ", k)
		}
	}
	if len(raw) != len(tags) {
		t.Errorf("Beispielzeile hat %d Felder, Typ %d", len(raw), len(tags))
	}

	d := json.NewDecoder(strings.NewReader(h.Z))
	d.UseNumber()
	d.DisallowUnknownFields()
	var l RequestLine
	if err := d.Decode(&l); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequestLine(l, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Beispielzeile ungültig: %v", err)
	}
	c, err := UsageCounts(l.Usage)
	if err != nil || c != (Counts{Input: 10, Output: 200, CacheCreate: 300, CacheRead: 4000, Cache1h: 300, Thinking: 50, WebSearch: 1}) {
		t.Fatalf("Zählwerte %+v, %v", c, err)
	}
	// Neu geschrieben enthält die Zeile dieselben Werte.
	b, err := MarshalLine(l)
	if err != nil {
		t.Fatal(err)
	}
	var a, z any
	_ = json.Unmarshal(b, &a)
	_ = json.Unmarshal([]byte(h.Z), &z)
	if !reflect.DeepEqual(a, z) {
		t.Fatalf("neu geschrieben anders:\n%s\n%s", b, h.Z)
	}
}

func TestMarshalLineCompactWithoutHTMLEscape(t *testing.T) {
	l := validLine(t)
	l.Usage["note"] = "a<b&c>"
	b, err := MarshalLine(l)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.ContainsAny(b, "\n ") || !bytes.Contains(b, []byte(`"note":"a<b&c>"`)) {
		t.Fatalf("Zeile %s", b)
	}
	if !bytes.HasPrefix(b, []byte(`{"kind":"request","request_id":"req_1"`)) {
		t.Fatalf("Reihenfolge: %s", b)
	}
	// Zahlen bleiben, wie sie im Protokoll stehen.
	if !bytes.Contains(b, []byte(`"input_tokens":10,`)) {
		t.Fatalf("Zahl umgeschrieben: %s", b)
	}
}

// lineFormProblem prüft die zwei Formregeln, die der Server zusätzlich an z stellt (doc.md, „Lesen“): kein Leerraum
// (Leerzeichen, Tab, \n, \r) außerhalb von Texten und kein Schlüssel doppelt in einem Objekt. Leer heißt: in Ordnung.
func lineFormProblem(z []byte) string {
	inStr, esc := false, false
	for _, c := range z {
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case !inStr && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			return "Leerraum außerhalb von Texten"
		}
	}
	dec := json.NewDecoder(bytes.NewReader(z))
	dec.UseNumber()
	var walk func() string
	walk = func() string {
		tok, err := dec.Token()
		if err != nil {
			return err.Error()
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]bool{}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return err.Error()
				}
				if seen[k.(string)] {
					return "Schlüssel doppelt: " + k.(string)
				}
				seen[k.(string)] = true
				if p := walk(); p != "" {
					return p
				}
			}
			_, err = dec.Token()
		case json.Delim('['):
			for dec.More() {
				if p := walk(); p != "" {
					return p
				}
			}
			_, err = dec.Token()
		}
		if err != nil {
			return err.Error()
		}
		return ""
	}
	return walk()
}

// MarshalLine schreibt z immer so, wie der Server es verlangt: kompakt, ohne doppelte Schlüssel, auch mit
// verschachteltem usage, Texten mit Leerraum und Vorgeschichte-Tagen. Der Prüfer selbst erkennt beide Fehler.
func TestMarshalLineServerForm(t *testing.T) {
	for z, want := range map[string]string{
		`{"a":1, "b":2}`:             "Leerraum außerhalb von Texten",
		"{\"a\":\n1}":                "Leerraum außerhalb von Texten",
		`{"a":{"b":1,"b":2}}`:        "Schlüssel doppelt: b",
		`{"a":[{"x":"y z","x":1}]}`:  "Schlüssel doppelt: x",
		`{"a":"x \t\" y","b":[1,2]}`: "",
	} {
		if got := lineFormProblem([]byte(z)); got != want {
			t.Errorf("%s: %q, want %q", z, got, want)
		}
	}
	l := validLine(t)
	l.Usage["note"] = "mit Leerzeichen\tund \"Zitat\""
	l.Usage["nested"] = map[string]any{"b": []any{map[string]any{"c": json.Number("1.5e3"), "d": "x y"}}, "a": true}
	l.Tools = map[string]int{"Read": 2, "Bash": 1}
	day := DayLine{Kind: KindDay, Date: "2026-10-15", Models: map[string]int64{"m-b": 2, "m-a": 1},
		Activity: &DayActivity{Messages: 1, Sessions: 1, ToolCalls: 1}, CompleteBefore: "2026-10-16"}
	for _, v := range []any{l, day} {
		b, err := MarshalLine(v)
		if err != nil {
			t.Fatal(err)
		}
		if p := lineFormProblem(b); p != "" {
			t.Errorf("%s: %s", b, p)
		}
		h, err := SealLine(testSignKey(t), b)
		if err != nil {
			t.Fatal(err)
		}
		if p := lineFormProblem(h); p != "" {
			t.Errorf("Hülle %s: %s", h, p)
		}
	}
}

func TestValidateRequestLineAccepts(t *testing.T) {
	if err := ValidateRequestLine(validLine(t), testNow); err != nil {
		t.Fatal(err)
	}
	l := validLine(t)
	l.RequestID, l.UUID, l.ParentUUID, l.RequestedModel, l.Effort, l.Entrypoint = "", "", "", "", "", ""
	l.CCVersion, l.StopReason, l.Tools, l.AccountID = "", "", nil, ""
	l.Usage = map[string]any{}
	if err := ValidateRequestLine(l, testNow); err != nil {
		t.Fatalf("Pflichtfelder allein: %v", err)
	}
}

func TestValidateRequestLineRejects(t *testing.T) {
	cases := map[string]func(*RequestLine){
		"kind":                 func(l *RequestLine) { l.Kind = KindDay },
		"message_id leer":      func(l *RequestLine) { l.MessageID = "" },
		"request_id Zeichen":   func(l *RequestLine) { l.RequestID = "a b" },
		"session_id leer":      func(l *RequestLine) { l.SessionID = "" },
		"uuid":                 func(l *RequestLine) { l.UUID = "a/b" },
		"parent_uuid":          func(l *RequestLine) { l.ParentUUID = strings.Repeat("a", MaxIDLen+1) },
		"account_id":           func(l *RequestLine) { l.AccountID = "a b" },
		"ts nicht UTC":         func(l *RequestLine) { l.TS = l.TS.In(time.FixedZone("x", 3600)) },
		"ts vor 2024":          func(l *RequestLine) { l.TS = time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC) },
		"ts Zukunft":           func(l *RequestLine) { l.TS = testNow.Add(MaxFutureSkew + time.Minute).UTC() },
		"utc_offset_min":       func(l *RequestLine) { l.UTCOffsetMin = MaxUTCOffsetMin + 1 },
		"model":                func(l *RequestLine) { l.Model = "<synthetic>" },
		"requested_model":      func(l *RequestLine) { l.RequestedModel = "a/b" },
		"effort":               func(l *RequestLine) { l.Effort = strings.Repeat("x", MaxShortText+1) },
		"entrypoint":           func(l *RequestLine) { l.Entrypoint = "a b" },
		"stop_reason":          func(l *RequestLine) { l.StopReason = "a<b" },
		"cc_version":           func(l *RequestLine) { l.CCVersion = "1 2" },
		"Werkzeugname":         func(l *RequestLine) { l.Tools = map[string]int{"<x>": 1} },
		"Werkzeuganzahl":       func(l *RequestLine) { l.Tools = map[string]int{"Read": 0} },
		"zu viele Werkzeuge":   func(l *RequestLine) { l.Tools = manyTools(MaxTools + 1) },
		"usage fehlt":          func(l *RequestLine) { l.Usage = nil },
		"usage Schlüssel":      func(l *RequestLine) { l.Usage["a-b"] = json.Number("1") },
		"usage Text lang":      func(l *RequestLine) { l.Usage["t"] = strings.Repeat("ü", MaxExtraString+1) },
		"usage Steuerzeichen":  func(l *RequestLine) { l.Usage["t"] = "a\x07" },
		"usage Formatzeichen":  func(l *RequestLine) { l.Usage["t"] = "a‮b" },
		"usage unendlich":      func(l *RequestLine) { l.Usage["x"] = json.Number("1e400") },
		"usage zu tief":        func(l *RequestLine) { l.Usage["a"] = nest(4) },
		"usage Liste lang":     func(l *RequestLine) { l.Usage["l"] = make([]any, UsageMaxList+1) },
		"usage zu viele Werte": func(l *RequestLine) { l.Usage["l"] = manyValues(UsageMaxValues) },
		"Zählwert null":        func(l *RequestLine) { l.Usage["input_tokens"] = nil },
		"Zählwert Komma":       func(l *RequestLine) { l.Usage["output_tokens"] = json.Number("1.0") },
		"Zählwert negativ":     func(l *RequestLine) { l.Usage["output_tokens"] = json.Number("-1") },
		"Zählwert Text":        func(l *RequestLine) { l.Usage["output_tokens"] = "1" },
		"Zählwert zu groß":     func(l *RequestLine) { l.Usage["output_tokens"] = json.Number("100000001") },
		"web_search zu groß": func(l *RequestLine) {
			l.Usage["server_tool_use"] = map[string]any{"web_search_requests": json.Number("10001")}
		},
		"Zeile zu lang": func(l *RequestLine) {
			for i := 0; i < 60; i++ {
				l.Tools[fmt.Sprintf("%s%02d", strings.Repeat("w", 126), i)] = 1
			}
		},
	}
	for name, mod := range cases {
		l := validLine(t)
		mod(&l)
		if err := ValidateRequestLine(l, testNow); err == nil {
			t.Errorf("%s: erwartet Fehler", name)
		}
	}
}

// nest liefert n ineinander geschachtelte Objekte; unter usage (Ebene 1) liegt das innerste auf Ebene n+1.
func nest(n int) any {
	var v any = json.Number("1")
	for i := 0; i < n; i++ {
		v = map[string]any{"a": v}
	}
	return v
}

// manyValues liefert eine Liste von Listen mit zusammen n Werten (sich selbst mitgezählt).
func manyValues(n int) []any {
	var out []any
	left := n - 1
	for left > 0 {
		k := min(left-1, UsageMaxList)
		inner := make([]any, k)
		for i := range inner {
			inner[i] = json.Number("1")
		}
		out = append(out, inner)
		left -= k + 1
	}
	return out
}

func TestUsageCountsMissingIsZero(t *testing.T) {
	c, err := UsageCounts(decodeUsage(t, `{"output_tokens":5,"cache_creation":3,"server_tool_use":{"web_fetch_requests":2}}`))
	if err != nil || c != (Counts{Output: 5, WebFetch: 2}) {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestSanitizeUsage(t *testing.T) {
	in := decodeUsage(t, `{
		"input_tokens": 10, "cache_read_input_tokens": null, "output_tokens": 7,
		"cache_creation": {"ephemeral_1h_input_tokens": null, "ephemeral_5m_input_tokens": 4},
		"service_tier": "standard", "inference_geo": null, "lang": "`+strings.Repeat("ü", 100)+`",
		"steuer": "a\u0007b", "format": "a​b", "a-b": 1, "riesig": 1e400, "flag": true,
		"iterations": [{}], "tief": {"b": {"c": {"d": 1, "e": {"f": 1}}}},
		"liste": [`+strings.Repeat("1,", 70)+`1]
	}`)
	out := SanitizeUsage(in)
	want := decodeUsage(t, `{
		"input_tokens": 10, "output_tokens": 7, "cache_creation": {"ephemeral_5m_input_tokens": 4},
		"service_tier": "standard", "inference_geo": null, "lang": "`+strings.Repeat("ü", 64)+`", "flag": true,
		"iterations": [{}], "tief": {"b": {"c": {"d": 1}}}, "liste": [`+strings.Repeat("1,", 63)+`1]
	}`)
	if !reflect.DeepEqual(out, want) {
		g, _ := MarshalLine(out)
		w, _ := MarshalLine(want)
		t.Fatalf("bereinigt:\n got %s\nwant %s", g, w)
	}
	if _, ok := in["steuer"]; !ok {
		t.Fatal("Eingabe verändert")
	}
	l := validLine(t)
	l.Usage = out
	if err := ValidateRequestLine(l, testNow); err != nil {
		t.Fatalf("bereinigte Zeile ungültig: %v", err)
	}
}

// Über UsageMaxValues hinaus fällt der Rest weg; das Ergebnis besteht die Prüfung des Servers genau an der Grenze.
func TestSanitizeUsageValueLimit(t *testing.T) {
	var fields []string
	for i := 0; i < 300; i++ {
		fields = append(fields, fmt.Sprintf(`"f%03d":{"x":1}`, i))
	}
	out := SanitizeUsage(decodeUsage(t, "{"+strings.Join(fields, ",")+"}"))
	n := 0
	if err := checkUsage(out, 1, &n); err != nil || n != UsageMaxValues {
		t.Fatalf("Werte %d, %v", n, err)
	}
	if _, ok := out["f127"]; !ok {
		t.Fatal("f127 fehlt (alphabetisch vorn)")
	}
	if _, ok := out["f128"]; ok {
		t.Fatal("f128 hätte wegfallen müssen")
	}
}

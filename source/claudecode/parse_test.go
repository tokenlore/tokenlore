package claudecode

import (
	"bufio"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixtureLines(t *testing.T, name string) [][]byte {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		out = append(out, append([]byte(nil), sc.Bytes()...))
	}
	return out
}

func berlin(t *testing.T) *time.Location {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestParseAggregatesOneRequest(t *testing.T) {
	aggs := map[string]*reqAgg{}
	var order []string
	for _, b := range fixtureLines(t, "basic.jsonl") {
		l, err := parseLine(b)
		if err != nil || l.Type != "assistant" || l.Usage == nil {
			continue
		}
		k := l.MessageID + "|" + l.RequestID
		if aggs[k] == nil {
			aggs[k] = &reqAgg{}
			order = append(order, k)
		}
		aggs[k].add(l)
	}
	var got []string
	for _, k := range order {
		r, ok := aggs[k].toRequest(berlin(t), false)
		if !ok {
			continue
		}
		got = append(got, k)
		if r.Input != 10 || r.Output != 200 || r.CacheCreate != 300 || r.CacheRead != 4000 ||
			r.Cache1h != 300 || r.Cache5m != 0 || r.Thinking != 50 || r.WebSearch != 1 || r.WebFetch != 0 {
			t.Errorf("Token-Werte: %+v", r)
		}
		if r.StopReason != "tool_use" || r.Model != "claude-opus-5-5" || r.SessionID != "s1" ||
			r.RequestID != "req_A" || r.MessageID != "msg_A" {
			t.Errorf("Metadaten: %+v", r)
		}
		if !reflect.DeepEqual(r.Tools, map[string]int{"Read": 1, "Bash": 1}) {
			t.Errorf("Tools: %v", r.Tools)
		}
		if r.UTCOffsetMin != 120 {
			t.Errorf("UTCOffsetMin %d, want 120", r.UTCOffsetMin)
		}
		if r.TS.Location() != time.UTC || !r.TS.Equal(time.Date(2026, 10, 23, 21, 30, 0, 0, time.UTC)) {
			t.Errorf("TS %v", r.TS)
		}
		if !reflect.DeepEqual(r.Extra, map[string]any{"service_tier": "standard"}) {
			t.Errorf("Extra: %v", r.Extra)
		}
	}
	if len(got) != 1 {
		t.Fatalf("Anfragen %v, want genau msg_A", got)
	}
}

func TestSyntheticIgnored(t *testing.T) {
	lines := fixtureLines(t, "basic.jsonl")
	l, err := parseLine(lines[4])
	if err != nil {
		t.Fatal(err)
	}
	a := &reqAgg{}
	a.add(l)
	if _, ok := a.toRequest(time.UTC, false); ok {
		t.Fatal("<synthetic> darf keine Anfrage ergeben")
	}
}

func TestBrokenLineIsError(t *testing.T) {
	if _, err := parseLine([]byte(`{"type":`)); err == nil {
		t.Fatal("erwartet Fehler")
	}
}

func TestOwnerAccountRead(t *testing.T) {
	l, err := parseLine(fixtureLines(t, "basic.jsonl")[1])
	if err != nil {
		t.Fatal(err)
	}
	if l.OwnerAccountUUID != "acc-1" {
		t.Fatalf("OwnerAccountUUID %q", l.OwnerAccountUUID)
	}
}

func TestNoContentRetained(t *testing.T) {
	b := []byte(`{"type":"assistant","sessionId":"s","requestId":"r","timestamp":"2026-10-23T21:30:00.000Z","message":{"id":"m","model":"x","content":[{"type":"text","text":"GEHEIM"},{"type":"tool_use","name":"Bash","input":{"command":"GEHEIM"}},{"type":"thinking","thinking":"GEHEIM"}],"usage":{"input_tokens":1,"output_tokens":1,"service_tier":"GEHEIM-nicht"}}}`)
	l, err := parseLine(b)
	if err != nil {
		t.Fatal(err)
	}
	s := fmt.Sprintf("%+v %+v", l, *l.Usage)
	if strings.Contains(strings.ReplaceAll(s, "GEHEIM-nicht", ""), "GEHEIM") {
		t.Fatalf("Inhalt im Parser-Ergebnis: %s", s)
	}
}

func TestExtraLimits(t *testing.T) {
	var fields []string
	for i := 0; i < 20; i++ {
		fields = append(fields, fmt.Sprintf(`"f%02d":%d`, i, i))
	}
	fields = append(fields, `"lang":"`+strings.Repeat("y", 100)+`"`, `"obj":{"a":1}`, `"arr":[1]`)
	b := []byte(`{"type":"assistant","sessionId":"s","requestId":"r","timestamp":"2026-10-23T21:30:00Z","message":{"id":"m","model":"x","content":[],"usage":{"input_tokens":1,` + strings.Join(fields, ",") + `}}}`)
	l, err := parseLine(b)
	if err != nil {
		t.Fatal(err)
	}
	a := &reqAgg{}
	a.add(l)
	r, ok := a.toRequest(time.UTC, false)
	if !ok {
		t.Fatal("keine Anfrage")
	}
	if len(r.Extra) != 16 {
		t.Fatalf("Extra hat %d Felder, want 16", len(r.Extra))
	}
	if _, ok := r.Extra["obj"]; ok {
		t.Error("Objekt in Extra")
	}
	if r.Extra["f00"] != float64(0) {
		t.Errorf("f00 = %v", r.Extra["f00"])
	}
	// alphabetisch: f00..f15 kommen vor "lang"
	if _, ok := r.Extra["lang"]; ok {
		t.Error("lang hätte nach f00..f15 wegfallen müssen")
	}
}

func TestExtraStringTruncated(t *testing.T) {
	b := []byte(`{"type":"assistant","sessionId":"s","requestId":"r","timestamp":"2026-10-23T21:30:00Z","message":{"id":"m","model":"x","content":[],"usage":{"input_tokens":1,"lang":"` + strings.Repeat("ü", 100) + `"}}}`)
	l, _ := parseLine(b)
	a := &reqAgg{}
	a.add(l)
	r, _ := a.toRequest(time.UTC, false)
	if s, _ := r.Extra["lang"].(string); len([]rune(s)) != 64 {
		t.Fatalf("lang hat %d Zeichen, want 64", len([]rune(s)))
	}
}

// Echte Dateien: frühe Zeilen einer Anfrage sind Streaming-Zwischenstände (Cache-Werte null,
// output_tokens zu klein); erst die letzte trägt die Endwerte. Gemessen am 24.10.2026: 6730 von 34122.
func TestStreamingLinesTakeFinalValues(t *testing.T) {
	first := []byte(`{"type":"assistant","sessionId":"s","requestId":"r","timestamp":"2026-10-23T21:30:00Z","message":{"id":"m","model":"x","stop_reason":null,"content":[{"type":"text"}],"usage":{"input_tokens":0,"cache_creation_input_tokens":null,"cache_read_input_tokens":null,"output_tokens":3}}}`)
	last := []byte(`{"type":"assistant","sessionId":"s","requestId":"r","timestamp":"2026-10-23T21:30:00Z","message":{"id":"m","model":"x","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Read"}],"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":20480,"output_tokens":260}}}`)
	a := &reqAgg{}
	for _, b := range [][]byte{first, last} {
		l, err := parseLine(b)
		if err != nil {
			t.Fatal(err)
		}
		a.add(l)
	}
	r, _ := a.toRequest(time.UTC, false)
	if r.Output != 260 || r.CacheRead != 20480 || r.StopReason != "tool_use" {
		t.Fatalf("got %+v", r)
	}
}

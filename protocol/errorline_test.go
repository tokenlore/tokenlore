package protocol

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func validErrorLine() ErrorLine {
	return ErrorLine{Kind: KindError, RequestID: "req_1", MessageID: "msg_1", SessionID: "s1", UUID: "u1",
		ParentUUID: "p1", TS: time.Date(2026, 10, 23, 21, 30, 0, 0, time.UTC), UTCOffsetMin: 120,
		Entrypoint: "cli", CCVersion: "2.1.309", AccountID: "acc-1", APIErrorStatus: ptr[int64](429),
		Error: "rate_limit", RateLimitType: "five_hour", LimitStatus: "rejected", OverageStatus: "disabled",
		IsUsingOverage: ptr(false), ResetsAt: ptr[int64](1_792_000_000)}
}

func TestValidateErrorLineAccepts(t *testing.T) {
	if err := ValidateErrorLine(validErrorLine(), testNow); err != nil {
		t.Fatal(err)
	}
	// Nur Pflichtfelder; request_id darf leer sein.
	e := ErrorLine{Kind: KindError, MessageID: "msg_1", SessionID: "s1", TS: testNow.Add(-time.Hour)}
	if err := ValidateErrorLine(e, testNow); err != nil {
		t.Fatalf("Pflichtfelder allein: %v", err)
	}
	// Grenzwerte und ":" in den Texten, die Kennungen erlauben.
	e = validErrorLine()
	e.APIErrorStatus, e.ResetsAt, e.UTCOffsetMin = ptr[int64](MinAPIErrorStatus), ptr[int64](0), -MaxUTCOffsetMin
	if err := ValidateErrorLine(e, testNow); err != nil {
		t.Fatalf("untere Grenzen: %v", err)
	}
	e.APIErrorStatus, e.ResetsAt = ptr[int64](MaxAPIErrorStatus), ptr[int64](MaxResetsAt)
	if err := ValidateErrorLine(e, testNow); err != nil {
		t.Fatalf("obere Grenzen: %v", err)
	}
}

func TestValidateErrorLineRejects(t *testing.T) {
	cases := map[string]func(*ErrorLine){
		"kind request":          func(e *ErrorLine) { e.Kind = KindRequest },
		"kind leer":             func(e *ErrorLine) { e.Kind = "" },
		"message_id leer":       func(e *ErrorLine) { e.MessageID = "" },
		"request_id Zeichen":    func(e *ErrorLine) { e.RequestID = "a b" },
		"session_id leer":       func(e *ErrorLine) { e.SessionID = "" },
		"uuid":                  func(e *ErrorLine) { e.UUID = "a/b" },
		"parent_uuid":           func(e *ErrorLine) { e.ParentUUID = strings.Repeat("a", MaxIDLen+1) },
		"account_id":            func(e *ErrorLine) { e.AccountID = "a b" },
		"ts fehlt":              func(e *ErrorLine) { e.TS = time.Time{} },
		"ts nicht UTC":          func(e *ErrorLine) { e.TS = e.TS.In(time.FixedZone("x", 3600)) },
		"ts Zukunft":            func(e *ErrorLine) { e.TS = testNow.Add(MaxFutureSkew + time.Minute).UTC() },
		"utc_offset_min":        func(e *ErrorLine) { e.UTCOffsetMin = MaxUTCOffsetMin + 1 },
		"entrypoint":            func(e *ErrorLine) { e.Entrypoint = "a b" },
		"cc_version":            func(e *ErrorLine) { e.CCVersion = "1 2" },
		"api_error_status 99":   func(e *ErrorLine) { e.APIErrorStatus = ptr[int64](MinAPIErrorStatus - 1) },
		"api_error_status 600":  func(e *ErrorLine) { e.APIErrorStatus = ptr[int64](MaxAPIErrorStatus + 1) },
		"api_error_status 0":    func(e *ErrorLine) { e.APIErrorStatus = ptr[int64](0) },
		"error Freitext":        func(e *ErrorLine) { e.Error = "Rate limit reached" },
		"error lang":            func(e *ErrorLine) { e.Error = strings.Repeat("x", MaxShortText+1) },
		"rate_limit_type":       func(e *ErrorLine) { e.RateLimitType = "a:b" },
		"limit_status":          func(e *ErrorLine) { e.LimitStatus = "a/b" },
		"overage_status":        func(e *ErrorLine) { e.OverageStatus = "a b" },
		"resets_at negativ":     func(e *ErrorLine) { e.ResetsAt = ptr[int64](-1) },
		"resets_at Millisekund": func(e *ErrorLine) { e.ResetsAt = ptr[int64](MaxResetsAt + 1) },
	}
	for name, mod := range cases {
		e := validErrorLine()
		mod(&e)
		if err := ValidateErrorLine(e, testNow); err == nil {
			t.Errorf("%s: erwartet Fehler", name)
		}
	}
}

// Andere Schlüssel sind ein Fehler: Der strenge Leser, wie ihn Plugin und Server verwenden, lehnt sie ab, auch
// model und usage einer Anfrage. Optionale Felder fehlen ganz, false und 0 bleiben erhalten.
func TestErrorLineKeys(t *testing.T) {
	b, err := MarshalLine(validErrorLine())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte(`{"kind":"error","request_id":"req_1","message_id":"msg_1"`)) ||
		!bytes.Contains(b, []byte(`"is_using_overage":false`)) {
		t.Fatalf("Zeile %s", b)
	}
	min, _ := MarshalLine(ErrorLine{Kind: KindError, MessageID: "m", SessionID: "s", TS: testNow})
	if want := `{"kind":"error","request_id":"","message_id":"m","session_id":"s","ts":"2026-10-24T12:00:00Z","utc_offset_min":0,"subagent":false}`; string(min) != want {
		t.Fatalf("nur Pflichtfelder:\n got %s\nwant %s", min, want)
	}
	z, _ := MarshalLine(ErrorLine{Kind: KindError, MessageID: "m", SessionID: "s", TS: testNow, ResetsAt: ptr[int64](0)})
	if !bytes.Contains(z, []byte(`"resets_at":0`)) {
		t.Fatalf("resets_at 0 fehlt: %s", z)
	}
	for _, extra := range []string{`"model":"x"`, `"usage":{}`, `"api_error":"x"`} {
		zz := strings.Replace(string(b), `"kind":"error",`, `"kind":"error",`+extra+`,`, 1)
		if strictDecodeTest([]byte(zz), &ErrorLine{}) == nil {
			t.Errorf("%s angenommen", extra)
		}
	}
}

func TestNeedsRequestDetails(t *testing.T) {
	e, _ := MarshalLine(validErrorLine())
	if !NeedsRequestDetails(string(e)) {
		t.Error("Fehlerzeile braucht das Merkmal")
	}
	plain, _ := MarshalLine(validLine(t))
	if NeedsRequestDetails(string(plain)) {
		t.Error("Anfrage ohne neue Felder braucht das Merkmal nicht")
	}
	day, _ := MarshalLine(DayLine{Kind: KindDay, Date: "2026-10-01", Models: map[string]int64{"m": 1}})
	if NeedsRequestDetails(string(day)) {
		t.Error("Vorgeschichte-Tag braucht das Merkmal nicht")
	}
	for name, mod := range requestDetailSetters() {
		l := validLine(t)
		mod(&l)
		b, _ := MarshalLine(l)
		if !NeedsRequestDetails(string(b)) {
			t.Errorf("%s gesetzt: braucht das Merkmal", name)
		}
	}
}

// requestDetailSetters setzt je eines der Felder, die das Merkmal request_details brauchen, auf einen gültigen Wert.
func requestDetailSetters() map[string]func(*RequestLine) {
	return map[string]func(*RequestLine){
		"prompt_cache_requested": func(l *RequestLine) { l.PromptCacheRequested = "1h" },
		"tool_input_stream_ms":   func(l *RequestLine) { l.ToolInputStreamMs = ptr[int64](0) },
		"thinking_duration_ms":   func(l *RequestLine) { l.ThinkingDurationMs = ptr[int64](MaxDurationMs) },
		"attribution_plugin":     func(l *RequestLine) { l.AttributionPlugin = "codecycle" },
		"attribution_agent":      func(l *RequestLine) { l.AttributionAgent = "codecycle:builder" },
		"cache_miss_reason":      func(l *RequestLine) { l.CacheMissReason = "ttl_expired" },
	}
}

func TestValidateRequestLineDetails(t *testing.T) {
	for name, mod := range requestDetailSetters() {
		l := validLine(t)
		mod(&l)
		if err := ValidateRequestLine(l, testNow); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	l := validLine(t)
	l.AttributionPlugin, l.AttributionAgent = strings.Repeat("a", MaxAttributionLen), "x.y_z-1:2"
	if err := ValidateRequestLine(l, testNow); err != nil {
		t.Errorf("Grenzwerte attribution: %v", err)
	}
	cases := map[string]func(*RequestLine){
		"prompt_cache_requested": func(l *RequestLine) { l.PromptCacheRequested = "1 h" },
		"cache_miss_reason":      func(l *RequestLine) { l.CacheMissReason = strings.Repeat("x", MaxShortText+1) },
		"tool_input_stream_ms -": func(l *RequestLine) { l.ToolInputStreamMs = ptr[int64](-1) },
		"tool_input_stream_ms +": func(l *RequestLine) { l.ToolInputStreamMs = ptr[int64](MaxDurationMs + 1) },
		"thinking_duration_ms -": func(l *RequestLine) { l.ThinkingDurationMs = ptr[int64](-1) },
		"thinking_duration_ms +": func(l *RequestLine) { l.ThinkingDurationMs = ptr[int64](MaxDurationMs + 1) },
		"attribution_plugin":     func(l *RequestLine) { l.AttributionPlugin = "a/b" },
		"attribution_plugin lang": func(l *RequestLine) {
			l.AttributionPlugin = strings.Repeat("a", MaxAttributionLen+1)
		},
		"attribution_agent": func(l *RequestLine) { l.AttributionAgent = "Agent mit Leerzeichen" },
	}
	for name, mod := range cases {
		l := validLine(t)
		mod(&l)
		if ValidateRequestLine(l, testNow) == nil {
			t.Errorf("%s: erwartet Fehler", name)
		}
	}
}

func TestErrorLineMatchesDocExample(t *testing.T) {
	z := docExample(t, KindError)
	checkDocKeys(t, z, ErrorLine{})
	var e ErrorLine
	if err := strictDecodeTest(z, &e); err != nil {
		t.Fatal(err)
	}
	if err := ValidateErrorLine(e, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Beispielzeile ungültig: %v", err)
	}
	if !NeedsRequestDetails(string(z)) {
		t.Fatal("Beispielzeile braucht das Merkmal")
	}
	b, _ := MarshalLine(e)
	if !bytes.Equal(b, z) {
		t.Fatalf("neu geschrieben anders:\n%s\n%s", b, z)
	}
}

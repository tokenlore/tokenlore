package claudecode

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
)

// Die sechs Anfragefelder und die Fehlerzeile aus details.jsonl: Felder aus der usage-Zeile, sonst aus der letzten
// Zeile, die sie hat; verletzte Grenzen fallen weg; die Fehlerzeile trägt nie Fehlertext, Inhalt oder Pfade.
func TestScanDetailsAndErrorLine(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s7.jsonl"), fixture(t, "details.jsonl"))
	res, err := c.Scan(context.Background(), emptyState(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"kind":"request","request_id":"req_D","message_id":"msg_D","session_id":"s7","uuid":"u-11","parent_uuid":"u-10",` +
			`"ts":"2026-10-23T21:30:00Z","utc_offset_min":0,"model":"claude-opus-5-5","entrypoint":"cli","cc_version":"2.1.309",` +
			`"stop_reason":"tool_use","subagent":false,"tools":{"Read":1},"prompt_cache_requested":"1h",` +
			`"tool_input_stream_ms":850,"thinking_duration_ms":4200,"attribution_plugin":"codecycle",` +
			`"attribution_agent":"codecycle:builder","cache_miss_reason":"ttl_expired","usage":{"input_tokens":10,"output_tokens":200}}`,
		`{"kind":"request","request_id":"req_E","message_id":"msg_E","session_id":"s7","uuid":"u-14","parent_uuid":"u-13",` +
			`"ts":"2026-10-23T21:32:00Z","utc_offset_min":0,"model":"claude-haiku-4-5","cc_version":"2.1.309",` +
			`"stop_reason":"end_turn","subagent":false,"usage":{"input_tokens":1,"output_tokens":2}}`,
	}
	wantErr := `{"kind":"error","request_id":"req_X","message_id":"5b1c2d3e-4f50-4617-8293-a4b5c6d7e8f0","session_id":"s7",` +
		`"uuid":"u-13","parent_uuid":"u-12","ts":"2026-10-23T21:31:00Z","utc_offset_min":0,"entrypoint":"cli",` +
		`"cc_version":"2.1.309","subagent":false,"api_error_status":429,"error":"rate_limit","rate_limit_type":"five_hour",` +
		`"limit_status":"rejected","overage_status":"disabled","is_using_overage":false,"resets_at":1792800000}`
	var got []string
	for _, l := range res.Lines {
		got = append(got, marshal(t, l))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Anfragen:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
	if len(res.Errors) != 1 || marshal(t, res.Errors[0]) != wantErr {
		t.Fatalf("Fehlerzeilen %+v\nwant %s", res.Errors, wantErr)
	}
	for _, s := range append(got, marshal(t, res.Errors[0])) {
		for _, bad := range []string{"GEHEIM", "geheim", "/home/", "attribution_skill", "request_ref", "r-1"} {
			if strings.Contains(s, bad) {
				t.Errorf("Inhaltszeile enthält %q: %s", bad, s)
			}
		}
	}
	if len(res.NewFields) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("neue Felder %q, Warnungen %q", res.NewFields, res.Warnings)
	}
	if err := protocol.ValidateErrorLine(res.Errors[0], time.Now()); err != nil {
		t.Fatal(err)
	}
}

// Werte außerhalb ihrer Grenze fehlen in der Fehlerzeile; die Zeile bleibt. Eine Fehlerzeile ohne usage zählt auch.
func TestErrorLineInvalidOptionalFieldsOmitted(t *testing.T) {
	a := aggOf(t, `{"type":"assistant","sessionId":"s","requestId":"r","timestamp":"2026-10-23T21:30:00Z",`+
		`"isSidechain":true,"isApiErrorMessage":true,"apiErrorStatus":99,"error":"a b","entrypoint":7,`+
		`"quotaLimits":{"status":1,"resetsAt":-1,"rateLimitType":"`+strings.Repeat("x", 40)+`","overageStatus":null,`+
		`"isUsingOverage":"ja"},"message":{"id":"m","model":"<synthetic>","content":[]}}`)
	e, ok := a.toErrorLine(time.UTC, false)
	if !ok {
		t.Fatal("keine Fehlerzeile")
	}
	want := protocol.ErrorLine{Kind: protocol.KindError, RequestID: "r", MessageID: "m", SessionID: "s",
		TS: time.Date(2026, 10, 23, 21, 30, 0, 0, time.UTC), Subagent: true}
	if !reflect.DeepEqual(e, want) {
		t.Fatalf("Fehlerzeile %+v", e)
	}
	if err := protocol.ValidateErrorLine(e, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// Verletzte Grenzen der sechs Felder: nur das Feld fehlt, die Anfrage bleibt; Grenzwerte gelten noch.
func TestDetailsBounds(t *testing.T) {
	a := aggOf(t, assistantLine(`,"thinkingDurationMs":0,"toolInputStreamMs":86400000,"attributionAgent":"`+
		strings.Repeat("a", 64)+`","attributionPlugin":"`+strings.Repeat("a", 65)+`","promptCacheRequested":1.5`,
		`{"output_tokens":1}`))
	l, ok := a.toLine(time.UTC, false)
	if !ok {
		t.Fatal("keine Zeile")
	}
	if l.ThinkingDurationMs == nil || *l.ThinkingDurationMs != 0 || l.ToolInputStreamMs == nil ||
		*l.ToolInputStreamMs != protocol.MaxDurationMs || len(l.AttributionAgent) != 64 || l.AttributionPlugin != "" ||
		l.PromptCacheRequested != "" {
		t.Fatalf("Grenzen falsch: %+v", l)
	}
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := protocol.MarshalLine(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

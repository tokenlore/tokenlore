package protocol

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestRequestJSONFieldNames(t *testing.T) {
	r := Request{
		RequestID: "req_1", MessageID: "msg_1", SessionID: "s1",
		TS:           time.Date(2026, 10, 23, 21, 30, 0, 0, time.UTC),
		UTCOffsetMin: 120, Model: "claude-opus-5-5",
		Input: 1, Output: 2, CacheCreate: 3, CacheRead: 4, Cache1h: 5, Cache5m: 6, Thinking: 7,
		WebSearch: 1, WebFetch: 1, StopReason: "end_turn", Subagent: true,
		Tools: map[string]int{"Read": 1}, AccountID: "acc", Extra: map[string]any{"speed": "fast"},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"account_id", "cache_1h", "cache_5m", "cache_create", "cache_read", "extra", "input",
		"message_id", "model", "output", "request_id", "session_id", "stop_reason", "subagent",
		"thinking", "tools", "ts", "utc_offset_min", "web_fetch", "web_search"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Schlüssel:\n got %v\nwant %v", got, want)
	}
	if ts, _ := m["ts"].(string); !strings.HasSuffix(ts, "Z") {
		t.Fatalf("ts endet nicht auf Z: %q", ts)
	}
}

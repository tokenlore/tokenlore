package protocol

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// Das Lieferpaket hat genau die Felder aus doc.md, „Batch“; Hüllen nur z und s.
func TestBatchJSONFieldNames(t *testing.T) {
	b := Batch{V: Version, Device: DeviceInfo{OS: "linux", Arch: "amd64", TZ: "UTC", PluginVersion: "0.6.0"},
		Accounts: []Account{{AccountID: "a", EmailMode: EmailOff}}, Sessions: []Session{{SessionID: "s"}},
		Requests: []Envelope{{Z: "{}", S: "x"}}, LegacyDays: []Envelope{{Z: "{}", S: "y"}}}
	j, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(j, &m); err != nil {
		t.Fatal(err)
	}
	var got []string
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"accounts", "device", "legacy_days", "requests", "sessions", "v"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Schlüssel:\n got %v\nwant %v", got, want)
	}
	if string(m["v"]) != "2" {
		t.Fatalf("v = %s, want 2", m["v"])
	}
	if !strings.Contains(string(m["requests"]), `[{"z":"{}","s":"x"}]`) {
		t.Fatalf("Hülle: %s", m["requests"])
	}
}

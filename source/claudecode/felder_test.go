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

// Testdatei mit Inhalt, Pfaden, Git-Zweig und erfundenen neuen Feldern: Die Inhaltszeilen enthalten nur die feste
// Feldliste, die neuen Felder erscheinen nur mit Namen.
func TestScanFieldList(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s9.jsonl"), fixture(t, "felder.jsonl"))
	res, err := c.Scan(context.Background(), emptyState(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"kind":"request","request_id":"req_F","message_id":"msg_F","session_id":"s9","uuid":"u-1","parent_uuid":"u-0",` +
			`"ts":"2026-10-23T21:30:00Z","utc_offset_min":0,"model":"claude-opus-5-5","requested_model":"claude-opus-5-5",` +
			`"effort":"high","entrypoint":"cli","cc_version":"2.1.309","stop_reason":"tool_use","subagent":false,` +
			`"tools":{"Bash":1,"Read":1},"usage":{"cache_creation":{"ephemeral_1h_input_tokens":300,"ephemeral_5m_input_tokens":0},` +
			`"cache_creation_input_tokens":300,"cache_read_input_tokens":4000,"inference_geo":"global","input_tokens":10,` +
			`"iterations":[],"output_tokens":260,"output_tokens_details":{"thinking_tokens":50},` +
			`"server_tool_use":{"web_fetch_requests":1,"web_search_requests":2},"service_tier":"standard","speed":"fast"}}`,
		`{"kind":"request","request_id":"req_G","message_id":"msg_G","session_id":"s9","uuid":"u-3","parent_uuid":"u-2",` +
			`"ts":"2026-10-23T21:31:00Z","utc_offset_min":0,"model":"claude-haiku-4-5","entrypoint":"sdk-cli",` +
			`"cc_version":"2.1.309","stop_reason":"end_turn","subagent":true,"usage":{"input_tokens":5,"output_tokens":6}}`,
	}
	var got []string
	for _, l := range res.Lines {
		b, err := protocol.MarshalLine(l)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(b))
		for _, bad := range []string{"GEHEIM", "/home/", "zweig", "cwd", "gitBranch", "wireToolInputs",
			"wireIngestContext", "content", "neues", "perTurnEffort", "advisorModel", "userType", "Leerzeichen"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("Inhaltszeile enthält %q: %s", bad, b)
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Inhaltszeilen:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
	wantNames := []string{"message.neuesMessageFeld",
		"nameMitSehrVielenZeichenDerUeberVierundsechzigZeichenHinausgehtU", "neuesFeld"}
	if !reflect.DeepEqual(res.NewFields, wantNames) {
		t.Fatalf("neue Felder %q, want %q", res.NewFields, wantNames)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "GEHEIM") {
			t.Errorf("Wert in Warnung: %s", w)
		}
	}

	// Die Zählwerte stehen in usage, wie Plugin und Server sie lesen (Tabelle „Zählwerte“).
	if len(res.Lines) != 2 {
		t.Fatalf("Anfragen %v", ids(res.Lines))
	}
	r, err := protocol.UsageCounts(res.Lines[0].Usage)
	if err != nil {
		t.Fatal(err)
	}
	if r.Input != 10 || r.Output != 260 || r.CacheCreate != 300 || r.CacheRead != 4000 || r.Cache1h != 300 ||
		r.Thinking != 50 || r.WebSearch != 2 || r.WebFetch != 1 || res.Lines[0].StopReason != "tool_use" {
		t.Errorf("Zahlen %+v", r)
	}
	if !res.Lines[1].Subagent {
		t.Error("isSidechain nicht als Subagent erkannt")
	}
}

func aggOf(t *testing.T, lines ...string) *reqAgg {
	t.Helper()
	a := &reqAgg{}
	for _, s := range lines {
		l, err := parseLine([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		a.add(l)
	}
	return a
}

func assistantLine(extra, usage string) string {
	return `{"type":"assistant","sessionId":"s","requestId":"r","timestamp":"2026-10-23T21:30:00Z"` + extra +
		`,"message":{"id":"m","model":"x","content":[],"usage":` + usage + `}}`
}

// usage stammt aus der Zeile mit dem größten output_tokens, bei Gleichstand aus der letzten.
func TestUsageFromLineWithMostOutput(t *testing.T) {
	a := aggOf(t,
		assistantLine("", `{"output_tokens":3,"cache_read_input_tokens":null}`),
		assistantLine("", `{"output_tokens":260,"cache_read_input_tokens":20480,"tag":"gross"}`),
		assistantLine("", `{"output_tokens":100,"cache_read_input_tokens":1}`))
	l, ok := a.toLine(time.UTC, false)
	if !ok {
		t.Fatal("keine Zeile")
	}
	if l.Usage["tag"] != "gross" {
		t.Fatalf("usage %v", l.Usage)
	}
	a = aggOf(t, assistantLine("", `{"output_tokens":5,"tag":"a"}`), assistantLine("", `{"output_tokens":5,"tag":"b"}`))
	if l, _ := a.toLine(time.UTC, false); l.Usage["tag"] != "b" {
		t.Fatalf("Gleichstand: usage %v", l.Usage)
	}
}

// Ungültige Werte optionaler Felder fallen weg; die Anfrage bleibt.
func TestInvalidOptionalFieldsOmitted(t *testing.T) {
	a := aggOf(t, assistantLine(`,"uuid":"a b","parentUuid":{"x":1},"requestedModel":"a/b","effort":"`+
		strings.Repeat("e", 40)+`","entrypoint":7,"version":"1 2","isSidechain":"ja"`, `{"output_tokens":1}`))
	l, ok := a.toLine(time.UTC, false)
	if !ok {
		t.Fatal("keine Zeile")
	}
	if l.UUID != "" || l.ParentUUID != "" || l.RequestedModel != "" || l.Effort != "" || l.Entrypoint != "" ||
		l.CCVersion != "" || l.Subagent {
		t.Fatalf("ungültige Werte übernommen: %+v", l)
	}
	l.SessionID = "s"
	if err := protocol.ValidateRequestLine(l, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestNewFieldNames(t *testing.T) {
	l, err := parseLine([]byte(assistantLine(`,"neu":1,"cwd":"/x","userType":"y","a.b":1,"x y":1`, `{"output_tokens":1}`)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l.NewFields, []string{"neu"}) {
		t.Fatalf("neue Felder %q", l.NewFields)
	}
	// Zeilen ohne Anfrage (Nutzer, Zusammenfassung) werden nicht geprüft.
	l, _ = parseLine([]byte(`{"type":"user","sessionId":"s","neu":1,"message":{"content":"x"}}`))
	if len(l.NewFields) != 0 {
		t.Fatalf("Nutzerzeile geprüft: %q", l.NewFields)
	}
}

func TestFieldListsDisjoint(t *testing.T) {
	for k := range knownTop {
		if blockedTop[k] {
			t.Errorf("%s bekannt und gesperrt", k)
		}
	}
	for k := range knownMessage {
		if blockedMessage[k] {
			t.Errorf("message.%s bekannt und gesperrt", k)
		}
	}
	for _, k := range []string{"content", "wireToolInputs", "wireIngestContext", "cwd", "gitBranch"} {
		if !blockedTop[k] {
			t.Errorf("%s nicht gesperrt", k)
		}
	}
	if !blockedMessage["content"] {
		t.Error("message.content nicht gesperrt")
	}
}

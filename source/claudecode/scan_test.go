package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

const lineC = `{"type":"assistant","sessionId":"s1","requestId":"req_C","timestamp":"2026-10-23T22:00:00.000Z","message":{"id":"msg_C","model":"claude-opus-5-5","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":5,"output_tokens":6}}}`

func newScanEnv(t *testing.T) (*ClaudeCode, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Jö Test")
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	return &ClaudeCode{ConfigDir: root, Loc: time.UTC, Now: time.Now}, root
}

func fixture(t *testing.T, name string) string {
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func emptyState(t *testing.T) *state.State {
	s, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ids(rs []protocol.Request) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.MessageID)
	}
	return out
}

func scan(t *testing.T, c *ClaudeCode, st *state.State) (protocolBatch protocol.Batch, next *state.State, warnings []string) {
	t.Helper()
	res, err := c.Scan(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	return res.Batch, res.Next, res.Warnings
}

func TestScanFirstRun(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), fixture(t, "basic.jsonl"))
	b, _, warns := scan(t, c, emptyState(t))
	if !reflect.DeepEqual(ids(b.Requests), []string{"msg_A"}) {
		t.Fatalf("Anfragen %v", ids(b.Requests))
	}
	if len(b.Sessions) != 1 || b.Sessions[0].AccountID != "acc-1" || b.Sessions[0].SessionID != "s1" || b.Sessions[0].CCVersion != "2.1.309" {
		t.Fatalf("Sitzungen %+v", b.Sessions)
	}
	if b.Requests[0].AccountID != "acc-1" {
		t.Errorf("Anfrage ohne Konto: %+v", b.Requests[0])
	}
	if len(warns) != 1 {
		t.Fatalf("Warnungen %v", warns)
	}
	if strings.Contains(warns[0], `"type"`) {
		t.Errorf("Warnung enthält Zeileninhalt: %s", warns[0])
	}
}

func TestScanIncrementalNoDuplicates(t *testing.T) {
	c, root := newScanEnv(t)
	f := filepath.Join(root, "projects", "P", "s1.jsonl")
	// ohne kaputte letzte Zeile, damit nur die Lesestand-Regel wirkt
	write(t, f, strings.Join(strings.Split(fixture(t, "basic.jsonl"), "\n")[:5], "\n")+"\n")
	_, next, _ := scan(t, c, emptyState(t))
	appendFile(t, f, lineC+"\n")
	b, _, _ := scan(t, c, next)
	got := ids(b.Requests)
	if len(got) == 0 || got[len(got)-1] != "msg_C" {
		t.Fatalf("Anfragen %v, msg_C fehlt", got)
	}
	for _, id := range got {
		if id != "msg_C" && id != "msg_A" {
			t.Errorf("unerwartete Anfrage %s", id)
		}
	}
}

func TestScanHalfLine(t *testing.T) {
	c, root := newScanEnv(t)
	f := filepath.Join(root, "projects", "P", "s1.jsonl")
	write(t, f, "")
	appendFile(t, f, lineC) // ohne \n
	b, next, warns := scan(t, c, emptyState(t))
	if len(b.Requests) != 0 || len(warns) != 0 {
		t.Fatalf("halbe Zeile verarbeitet: %v %v", ids(b.Requests), warns)
	}
	if next.Files["projects/P/s1.jsonl"].Offset != 0 {
		t.Fatalf("Offset %d, want 0", next.Files["projects/P/s1.jsonl"].Offset)
	}
	appendFile(t, f, "\n")
	b, _, _ = scan(t, c, next)
	if !reflect.DeepEqual(ids(b.Requests), []string{"msg_C"}) {
		t.Fatalf("Anfragen %v", ids(b.Requests))
	}
}

func TestScanRequestSplitAcrossRuns(t *testing.T) {
	c, root := newScanEnv(t)
	f := filepath.Join(root, "projects", "P", "s1.jsonl")
	lines := strings.Split(fixture(t, "basic.jsonl"), "\n")
	write(t, f, lines[2]+"\n") // nur Text-Zeile von msg_A
	_, next, _ := scan(t, c, emptyState(t))
	appendFile(t, f, lines[3]+"\n") // Werkzeug-Zeile von msg_A
	b, _, _ := scan(t, c, next)
	if len(b.Requests) != 1 || b.Requests[0].MessageID != "msg_A" {
		t.Fatalf("Anfragen %v", ids(b.Requests))
	}
	if !reflect.DeepEqual(b.Requests[0].Tools, map[string]int{"Read": 1, "Bash": 1}) {
		t.Fatalf("Tools %v", b.Requests[0].Tools)
	}
}

func TestScanTruncatedFileRereads(t *testing.T) {
	c, root := newScanEnv(t)
	f := filepath.Join(root, "projects", "P", "s1.jsonl")
	write(t, f, fixture(t, "basic.jsonl"))
	_, next, _ := scan(t, c, emptyState(t))
	write(t, f, lineC+"\n")
	b, _, _ := scan(t, c, next)
	if !reflect.DeepEqual(ids(b.Requests), []string{"msg_C"}) {
		t.Fatalf("Anfragen %v", ids(b.Requests))
	}
}

func TestScanReplacedSameSizeRereads(t *testing.T) {
	c, root := newScanEnv(t)
	f := filepath.Join(root, "projects", "P", "s1.jsonl")
	write(t, f, lineC+"\n")
	_, next, _ := scan(t, c, emptyState(t))
	// gleich lang, anderer Anfang, dazu eine neue Zeile
	write(t, f, strings.Replace(lineC, "msg_C", "msg_D", 1)+"\n"+lineC+"\n")
	b, _, _ := scan(t, c, next)
	if got := ids(b.Requests); len(got) == 0 || got[0] != "msg_D" {
		t.Fatalf("Anfragen %v, erwartet Neulesen ab Anfang", got)
	}
}

func TestScanSubagentFlag(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1", "subagents", "a.jsonl"), lineC+"\n")
	b, _, _ := scan(t, c, emptyState(t))
	if len(b.Requests) != 1 || !b.Requests[0].Subagent {
		t.Fatalf("Anfragen %+v", b.Requests)
	}
}

func TestScanAccountFromState(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), lineC+"\n")
	st := emptyState(t)
	st.Sessions["s1"] = "acc-2"
	b, _, _ := scan(t, c, st)
	if len(b.Sessions) != 1 || b.Sessions[0].AccountID != "acc-2" || b.Requests[0].AccountID != "acc-2" {
		t.Fatalf("Konto nicht übernommen: %+v %+v", b.Sessions, b.Requests)
	}
}

func TestScanDoesNotMutateInput(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), lineC+"\n")
	st := emptyState(t)
	scan(t, c, st)
	if len(st.Files) != 0 {
		t.Fatal("Scan hat den übergebenen State verändert")
	}
}

func TestScanNothingNewIsEmpty(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), "{\"type\":\"user\"}\n")
	_, next, _ := scan(t, c, emptyState(t))
	b, _, _ := scan(t, c, next)
	if len(b.Requests) != 0 || len(b.Sessions) != 0 {
		t.Fatalf("zweiter Lauf liefert %+v", b)
	}
}

func TestScanUnchangedFileSendsNothing(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), lineC+"\n")
	_, next, _ := scan(t, c, emptyState(t))
	b, _, _ := scan(t, c, next)
	if len(b.Requests) != 0 || len(b.Sessions) != 0 {
		t.Fatalf("unveränderte Datei liefert erneut: %v", ids(b.Requests))
	}
}

func TestScanOwnerRememberedAcrossRuns(t *testing.T) {
	c, root := newScanEnv(t)
	f := filepath.Join(root, "projects", "P", "s1.jsonl")
	write(t, f, `{"type":"bridge-session","sessionId":"s1","ownerAccountUuid":"acc-1"}`+"\n")
	_, next, _ := scan(t, c, emptyState(t))
	appendFile(t, f, lineC+"\n")
	b, _, _ := scan(t, c, next)
	if len(b.Requests) != 1 || b.Requests[0].AccountID != "acc-1" {
		t.Fatalf("Konto aus früherem Lauf fehlt: %+v", b.Requests)
	}
}

func TestScanSessionDedupMainAndSubagent(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), lineC+"\n")
	write(t, filepath.Join(root, "projects", "P", "s1", "subagents", "a.jsonl"),
		strings.NewReplacer("msg_C", "msg_S", "req_C", "req_S").Replace(lineC)+"\n")
	b, _, _ := scan(t, c, emptyState(t))
	if len(b.Sessions) != 1 || len(b.Requests) != 2 {
		t.Fatalf("Sitzungen %+v, Anfragen %v", b.Sessions, ids(b.Requests))
	}
}

func TestScanAccountsWithEmailMode(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, ".claude.json"), `{"oauthAccount":{"accountUuid":"acc-1","emailAddress":"max@beispiel.de"}}`)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), lineC+"\n")
	st := emptyState(t)
	st.Sessions["s1"] = "acc-1"
	st.Sessions["s0"] = "acc-0"
	for mode, want := range map[string]string{"off": "", "short": "ma…@beispiel.de", "full": "max@beispiel.de"} {
		st.EmailMode = mode
		b, _, _ := scan(t, c, st)
		got := map[string]protocol.Account{}
		for _, a := range b.Accounts {
			got[a.AccountID] = a
		}
		if len(got) != 2 {
			t.Fatalf("%s: Konten %+v", mode, b.Accounts)
		}
		if got["acc-1"].EmailMode != mode || got["acc-1"].Email != want {
			t.Errorf("%s: acc-1 %+v", mode, got["acc-1"])
		}
		if got["acc-0"].Email != "" {
			t.Errorf("%s: fremdes Konto bekommt Adresse %+v", mode, got["acc-0"])
		}
	}
}

func TestScanRequestWithoutRequestIDCounted(t *testing.T) {
	c, root := newScanEnv(t)
	l := `{"type":"assistant","sessionId":"s1","timestamp":"2026-10-01T10:00:00Z","message":{"id":"msg_X","model":"deepseek-v4.1-flash","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}`
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), l+"\n"+l+"\n")
	b, _, _ := scan(t, c, emptyState(t))
	if len(b.Requests) != 1 || b.Requests[0].MessageID != "msg_X" || b.Requests[0].RequestID != "" {
		t.Fatalf("Anfragen %+v", b.Requests)
	}
}

func TestScanSessionChangeInsideFile(t *testing.T) {
	c, root := newScanEnv(t)
	l2 := strings.NewReplacer(`"sessionId":"s1"`, `"sessionId":"s2"`, "msg_C", "msg_2", "req_C", "req_2").Replace(lineC)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), lineC+"\n"+l2+"\n")
	st := emptyState(t)
	st.Sessions["s1"] = "acc-1"
	st.Sessions["s2"] = "acc-2"
	b, _, _ := scan(t, c, st)
	acc := map[string]string{}
	for _, r := range b.Requests {
		acc[r.MessageID] = r.AccountID
	}
	if acc["msg_C"] != "acc-1" || acc["msg_2"] != "acc-2" {
		t.Fatalf("Konten %v", acc)
	}
	sess := map[string]string{}
	for _, s := range b.Sessions {
		sess[s.SessionID] = s.AccountID
	}
	if sess["s1"] != "acc-1" || sess["s2"] != "acc-2" {
		t.Fatalf("Sitzungen %+v", b.Sessions)
	}
}

func TestScanDropsInvalidWithWarning(t *testing.T) {
	c, root := newScanEnv(t)
	bad := strings.Replace(lineC, `"req_C"`, `"req mit leerzeichen"`, 1)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), bad+"\n")
	b, _, warns := scan(t, c, emptyState(t))
	if len(b.Requests) != 0 {
		t.Fatalf("ungültige Anfrage geliefert: %+v", b.Requests)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "ungültig") {
		t.Fatalf("Warnungen %v", warns)
	}
}

func TestScanAccountEmailTooLongDropped(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, ".claude.json"), `{"oauthAccount":{"accountUuid":"acc-1","emailAddress":"`+strings.Repeat("a", 300)+`@b.de"}}`)
	st := emptyState(t)
	st.EmailMode = "full"
	b, _, _ := scan(t, c, st)
	if len(b.Accounts) != 1 || b.Accounts[0].Email != "" || protocol.ValidateAccount(b.Accounts[0]) != nil {
		t.Fatalf("Konten %+v", b.Accounts)
	}
}

func TestScanInvalidToolNameKeepsRequest(t *testing.T) {
	c, root := newScanEnv(t)
	l := strings.Replace(lineC, `"content":[{"type":"text","text":"x"}]`,
		`"content":[{"type":"tool_use","name":"Read"},{"type":"tool_use","name":"Bash\" <x>; rm -rf"}]`, 1)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), l+"\n")
	b, _, warns := scan(t, c, emptyState(t))
	if len(b.Requests) != 1 || len(warns) != 0 {
		t.Fatalf("Anfragen %v, Warnungen %v", ids(b.Requests), warns)
	}
	if !reflect.DeepEqual(b.Requests[0].Tools, map[string]int{"Read": 1, "_invalid": 1}) {
		t.Fatalf("Tools %v", b.Requests[0].Tools)
	}
}

func TestScanDropsInvalidLegacy(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "stats-cache.json"), `{"version":2,
"dailyActivity":[{"date":"2023-05-01","messageCount":1,"sessionCount":1,"toolCallCount":1},
{"date":"2026-08-14","messageCount":120,"sessionCount":3,"toolCallCount":40}],
"dailyModelTokens":[{"date":"2023-05-01","tokensByModel":{"claude-opus-4-1":10}},
{"date":"2026-08-14","tokensByModel":{"claude-opus-4-1":150000}}]}`)
	b, _, warns := scan(t, c, emptyState(t))
	if len(b.LegacyModel) != 1 || b.LegacyModel[0].Date != "2026-08-14" ||
		len(b.LegacyActivity) != 1 || b.LegacyActivity[0].Date != "2026-08-14" {
		t.Fatalf("Altbestand %+v %+v", b.LegacyModel, b.LegacyActivity)
	}
	if len(warns) != 1 || warns[0] != "stats-cache.json: 2 ungültige Tageswerte ausgelassen" {
		t.Fatalf("Warnungen %v", warns)
	}
}

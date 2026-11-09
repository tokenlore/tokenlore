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
	// Die Uhr steht eine Stunde vor: Frisch geschriebene Testdateien gelten als ruhend, ihre letzte Anfrage als fertig.
	// Das Zurückhalten unfertiger Anfragen prüfen die Tests in hold_test.go mit eigener Uhr.
	return &ClaudeCode{ConfigDir: root, Loc: time.UTC, Now: idleNow}, root
}

func idleNow() time.Time { return time.Now().Add(time.Hour) }

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

func ids(rs []protocol.RequestLine) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.MessageID)
	}
	return out
}

func scan(t *testing.T, c *ClaudeCode, st *state.State) (lines []protocol.RequestLine, next *state.State, warnings []string) {
	t.Helper()
	res, err := c.Scan(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	return res.Lines, res.Next, res.Warnings
}

func TestScanFirstRun(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), fixture(t, "basic.jsonl"))
	b, _, warns := scan(t, c, emptyState(t))
	if !reflect.DeepEqual(ids(b), []string{"msg_A"}) {
		t.Fatalf("Anfragen %v", ids(b))
	}
	if b[0].SessionID != "s1" || b[0].CCVersion != "2.1.309" {
		t.Fatalf("Anfrage %+v", b[0])
	}
	if b[0].AccountID != "acc-1" {
		t.Errorf("Anfrage ohne Konto: %+v", b[0])
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
	got := ids(b)
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
	if len(b) != 0 || len(warns) != 0 {
		t.Fatalf("halbe Zeile verarbeitet: %v %v", ids(b), warns)
	}
	if next.Files["projects/P/s1.jsonl"].Offset != 0 {
		t.Fatalf("Offset %d, want 0", next.Files["projects/P/s1.jsonl"].Offset)
	}
	appendFile(t, f, "\n")
	b, _, _ = scan(t, c, next)
	if !reflect.DeepEqual(ids(b), []string{"msg_C"}) {
		t.Fatalf("Anfragen %v", ids(b))
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
	if len(b) != 1 || b[0].MessageID != "msg_A" {
		t.Fatalf("Anfragen %v", ids(b))
	}
	if !reflect.DeepEqual(b[0].Tools, map[string]int{"Read": 1, "Bash": 1}) {
		t.Fatalf("Tools %v", b[0].Tools)
	}
}

func TestScanTruncatedFileRereads(t *testing.T) {
	c, root := newScanEnv(t)
	f := filepath.Join(root, "projects", "P", "s1.jsonl")
	write(t, f, fixture(t, "basic.jsonl"))
	_, next, _ := scan(t, c, emptyState(t))
	write(t, f, lineC+"\n")
	b, _, _ := scan(t, c, next)
	if !reflect.DeepEqual(ids(b), []string{"msg_C"}) {
		t.Fatalf("Anfragen %v", ids(b))
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
	if got := ids(b); len(got) == 0 || got[0] != "msg_D" {
		t.Fatalf("Anfragen %v, erwartet Neulesen ab Anfang", got)
	}
}

func TestScanSubagentFlag(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1", "subagents", "a.jsonl"), lineC+"\n")
	b, _, _ := scan(t, c, emptyState(t))
	if len(b) != 1 || !b[0].Subagent {
		t.Fatalf("Anfragen %+v", b)
	}
}

// Ohne ownerAccountUuid bleibt das Konto im Scan leer; das Archiv ergänzt es (Sitzung, sonst angemeldetes Konto).
// Der Scan setzt kein angemeldetes Konto ein.
func TestScanAccountEmptyWithoutOwner(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, ".claude.json"), `{"oauthAccount":{"accountUuid":"acc-jetzt","emailAddress":"max@beispiel.de"}}`)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), lineC+"\n")
	res, err := c.Scan(context.Background(), emptyState(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 1 || res.Lines[0].AccountID != "" {
		t.Fatalf("Konto gesetzt: %+v", res.Lines)
	}
	if res.CurrentAccountID != "acc-jetzt" || res.CurrentEmail != "max@beispiel.de" {
		t.Fatalf("angemeldetes Konto %q %q", res.CurrentAccountID, res.CurrentEmail)
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
	if len(b) != 0 {
		t.Fatalf("zweiter Lauf liefert %+v", b)
	}
}

func TestScanUnchangedFileSendsNothing(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), lineC+"\n")
	_, next, _ := scan(t, c, emptyState(t))
	b, _, _ := scan(t, c, next)
	if len(b) != 0 {
		t.Fatalf("unveränderte Datei liefert erneut: %v", ids(b))
	}
}

// Steht ownerAccountUuid nur in der Hauptdatei, gilt das Konto auch für die Anfragen der Subagenten derselben
// Sitzung, gleich in welcher Reihenfolge die Dateien gelesen werden.
func TestScanOwnerSharedAcrossFilesOfSession(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"),
		lineC+"\n"+`{"type":"bridge-session","sessionId":"s1","ownerAccountUuid":"acc-1"}`+"\n")
	write(t, filepath.Join(root, "projects", "P", "s1", "subagents", "a.jsonl"),
		strings.NewReplacer("msg_C", "msg_S", "req_C", "req_S").Replace(lineC)+"\n")
	b, _, _ := scan(t, c, emptyState(t))
	if len(b) != 2 {
		t.Fatalf("Anfragen %v", ids(b))
	}
	for _, l := range b {
		if l.AccountID != "acc-1" {
			t.Errorf("%s: Konto %q", l.MessageID, l.AccountID)
		}
	}
}

func TestScanRequestWithoutRequestIDCounted(t *testing.T) {
	c, root := newScanEnv(t)
	l := `{"type":"assistant","sessionId":"s1","timestamp":"2026-10-01T10:00:00Z","message":{"id":"msg_X","model":"deepseek-v4.1-flash","content":[],"usage":{"input_tokens":1,"output_tokens":2}}}`
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), l+"\n"+l+"\n")
	b, _, _ := scan(t, c, emptyState(t))
	if len(b) != 1 || b[0].MessageID != "msg_X" || b[0].RequestID != "" {
		t.Fatalf("Anfragen %+v", b)
	}
}

func TestScanSessionChangeInsideFile(t *testing.T) {
	c, root := newScanEnv(t)
	l2 := strings.NewReplacer(`"sessionId":"s1"`, `"sessionId":"s2"`, "msg_C", "msg_2", "req_C", "req_2").Replace(lineC)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"),
		`{"type":"user","sessionId":"s1","ownerAccountUuid":"acc-1"}`+"\n"+lineC+"\n"+
			`{"type":"user","sessionId":"s2","ownerAccountUuid":"acc-2"}`+"\n"+l2+"\n")
	b, _, _ := scan(t, c, emptyState(t))
	acc := map[string]string{}
	for _, r := range b {
		acc[r.MessageID] = r.AccountID
	}
	if acc["msg_C"] != "acc-1" || acc["msg_2"] != "acc-2" {
		t.Fatalf("Konten %v", acc)
	}
}

func TestScanDropsInvalidWithWarning(t *testing.T) {
	c, root := newScanEnv(t)
	bad := strings.Replace(lineC, `"req_C"`, `"req mit leerzeichen"`, 1)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), bad+"\n")
	b, _, warns := scan(t, c, emptyState(t))
	if len(b) != 0 {
		t.Fatalf("ungültige Anfrage geliefert: %+v", b)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "ungültig") {
		t.Fatalf("Warnungen %v", warns)
	}
}

func TestScanInvalidToolNameKeepsRequest(t *testing.T) {
	c, root := newScanEnv(t)
	l := strings.Replace(lineC, `"content":[{"type":"text","text":"x"}]`,
		`"content":[{"type":"tool_use","name":"Read"},{"type":"tool_use","name":"Bash\" <x>; rm -rf"}]`, 1)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), l+"\n")
	b, _, warns := scan(t, c, emptyState(t))
	if len(b) != 1 || len(warns) != 0 {
		t.Fatalf("Anfragen %v, Warnungen %v", ids(b), warns)
	}
	if !reflect.DeepEqual(b[0].Tools, map[string]int{"Read": 1, "_invalid": 1}) {
		t.Fatalf("Tools %v", b[0].Tools)
	}
}

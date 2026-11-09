package claudecode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/source"
	"github.com/tokenlore/tokenlore/state"
)

// holdLine ist eine Protokollzeile einer Anfrage mit Zeitpunkt ts und output_tokens out.
func holdLine(id, ts string, out int) string {
	return fmt.Sprintf(`{"type":"assistant","sessionId":"s1","requestId":"req_%s","timestamp":"%s","message":{"id":"msg_%s","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":%d}}}`+"\n", id, ts, id, out)
}

// holdEnv: Uhr fest, die Sitzungsdatei bekommt ihre Änderungszeit von touch.
type holdEnv struct {
	c    *ClaudeCode
	path string
	now  time.Time
}

func newHoldEnv(t *testing.T) *holdEnv {
	t.Helper()
	root := filepath.Join(t.TempDir(), "claude")
	h := &holdEnv{now: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	h.c = &ClaudeCode{ConfigDir: root, Loc: time.UTC, Now: func() time.Time { return h.now }}
	h.path = filepath.Join(root, "projects", "P", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		t.Fatal(err)
	}
	return h
}

// write schreibt den Inhalt und setzt die Änderungszeit auf age vor der Uhr.
func (h *holdEnv) write(t *testing.T, content string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(h.path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	h.touch(t, age)
}

func (h *holdEnv) touch(t *testing.T, age time.Duration) {
	t.Helper()
	m := h.now.Add(-age)
	if err := os.Chtimes(h.path, m, m); err != nil {
		t.Fatal(err)
	}
}

func (h *holdEnv) scan(t *testing.T, st *state.State) source.ScanResult {
	t.Helper()
	res, err := h.c.Scan(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func lineIDs(res source.ScanResult) []string {
	var out []string
	for _, l := range res.Lines {
		out = append(out, l.MessageID)
	}
	return out
}

func outputOf(t *testing.T, res source.ScanResult, msg string) string {
	t.Helper()
	for _, l := range res.Lines {
		if l.MessageID == msg {
			return fmt.Sprint(l.Usage["output_tokens"])
		}
	}
	t.Fatalf("%s fehlt", msg)
	return ""
}

// Unfertige Anfrage: Die letzte Anfrage einer wachsenden Datei kommt nicht ins Ergebnis; nach dem Weiterschreiben
// steht sie genau einmal mit den vollen Zahlen da.
func TestHoldLastRequestUntilNewerFollows(t *testing.T) {
	h := newHoldEnv(t)
	first := holdLine("A", "2026-11-06T11:00:00Z", 10) + holdLine("B", "2026-11-06T11:01:00Z", 3)
	h.write(t, first+`{"type":"assistant","sessionId":"s1","requestId":"req_B"`, time.Second) // halbe Zeile
	res := h.scan(t, emptyState(t))
	if !reflect.DeepEqual(lineIDs(res), []string{"msg_A"}) {
		t.Fatalf("Anfragen %v, erwartet nur msg_A", lineIDs(res))
	}
	if !reflect.DeepEqual(res.HeldMonths, []string{"2026-11"}) {
		t.Fatalf("zurückgehaltene Monate %v", res.HeldMonths)
	}
	mark := res.Next.Files["projects/P/s1.jsonl"]
	if want := int64(len(holdLine("A", "2026-11-06T11:00:00Z", 10))); mark.Offset != want || mark.End != int64(len(first)) {
		t.Fatalf("Lesestand %+v, Offset erwartet %d", mark, want)
	}

	// Weitergeschrieben: B endet mit 50 Tokens, danach beginnt C.
	full := first + holdLine("B", "2026-11-06T11:01:00Z", 50) + holdLine("C", "2026-11-06T11:02:00Z", 1)
	h.write(t, full, time.Second)
	res2 := h.scan(t, res.Next)
	if !reflect.DeepEqual(lineIDs(res2), []string{"msg_B"}) || outputOf(t, res2, "msg_B") != "50" {
		t.Fatalf("Anfragen %v", lineIDs(res2))
	}
}

// Eine folgende Zeile einer anderen Anfrage mit früherem oder gleichem Zeitpunkt gibt nicht frei.
func TestHoldNeedsLaterTimestamp(t *testing.T) {
	h := newHoldEnv(t)
	h.write(t, holdLine("A", "2026-11-06T11:00:00Z", 10)+holdLine("B", "2026-11-06T11:00:00Z", 3)+
		holdLine("C", "2026-11-06T10:59:00Z", 3), time.Second)
	if res := h.scan(t, emptyState(t)); len(res.Lines) != 0 {
		t.Fatalf("freigegeben: %v", lineIDs(res))
	}
	h.write(t, holdLine("A", "2026-11-06T11:00:00Z", 10)+holdLine("B", "2026-11-06T11:00:01Z", 3), time.Second)
	if res := h.scan(t, emptyState(t)); !reflect.DeepEqual(lineIDs(res), []string{"msg_A"}) {
		t.Fatalf("Anfragen %v", lineIDs(res))
	}
}

// Eine Datei, die 5 Minuten ruht, gibt ihre letzte Anfrage frei, ohne gewachsen zu sein, und wird danach nicht mehr
// neu gelesen.
func TestHoldReleasedWhenIdle(t *testing.T) {
	h := newHoldEnv(t)
	// Ein langer Kopf, damit der Test unten das Ende ändern kann, ohne dass die Prüfsumme des Kopfs anschlägt.
	pad := `{"type":"user","sessionId":"s1","message":{"role":"user","content":"` + strings.Repeat("x", headMax) + `"}}` + "\n"
	content := pad + holdLine("A", "2026-10-31T23:58:00Z", 10) + holdLine("B", "2026-10-31T23:59:00Z", 20)
	h.write(t, content, IdleAfter-time.Second)
	res := h.scan(t, emptyState(t))
	if !reflect.DeepEqual(lineIDs(res), []string{"msg_A"}) || !reflect.DeepEqual(res.HeldMonths, []string{"2026-10"}) {
		t.Fatalf("Anfragen %v, Monate %v", lineIDs(res), res.HeldMonths)
	}

	h.now = h.now.Add(2 * time.Second) // jetzt ruht die Datei 5 Minuten und eine Sekunde
	res2 := h.scan(t, res.Next)
	if !reflect.DeepEqual(lineIDs(res2), []string{"msg_B"}) || len(res2.HeldMonths) != 0 {
		t.Fatalf("Anfragen %v, Monate %v", lineIDs(res2), res2.HeldMonths)
	}
	mark := res2.Next.Files["projects/P/s1.jsonl"]
	if mark.Offset != mark.End || mark.End != int64(len(content)) {
		t.Fatalf("Lesestand %+v springt nicht ans Ende", mark)
	}

	// Danach nicht mehr gelesen: Auch ein anderer Inhalt gleicher Länge (nach dem Kopf) bringt nichts.
	h.write(t, content[:len(content)-3]+"xx\n", time.Hour)
	res3 := h.scan(t, res2.Next)
	if len(res3.Lines) != 0 || len(res3.Warnings) != 0 || !reflect.DeepEqual(res3.Next.Files, res2.Next.Files) {
		t.Fatalf("erneut gelesen: %v %v", lineIDs(res3), res3.Warnings)
	}
}

// Ohne Größenänderung, aber mit zurückgehaltener Anfrage, liest der Lauf die Datei erneut und meldet den Monat weiter.
func TestHoldRereadsUnchangedFile(t *testing.T) {
	h := newHoldEnv(t)
	h.write(t, holdLine("A", "2026-10-31T23:59:00Z", 10), time.Minute)
	res := h.scan(t, emptyState(t))
	h.now = h.now.Add(time.Minute) // noch keine 5 Minuten
	res2 := h.scan(t, res.Next)
	if len(res2.Lines) != 0 || !reflect.DeepEqual(res2.HeldMonths, []string{"2026-10"}) {
		t.Fatalf("Anfragen %v, Monate %v", lineIDs(res2), res2.HeldMonths)
	}
}

// Konto je Zeile: ownerAccountUuid, sonst das Konto beim Sitzungsstart, sonst das angemeldete Konto.

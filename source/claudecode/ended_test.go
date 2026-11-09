package claudecode

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Sitzungsende: Die letzte Anfrage der beendeten Sitzung und ihrer Subagenten gilt sofort als fertig, auch wenn die
// Datei eben erst geschrieben wurde; eine andere Sitzung im selben Ordner bleibt zurückgehalten.
func TestEndedSessionReleasesLastRequest(t *testing.T) {
	h := newHoldEnv(t)
	h.write(t, holdLine("A", "2026-11-06T11:00:00Z", 10)+holdLine("B", "2026-11-06T11:01:00Z", 3), time.Second)
	sub := filepath.Join(filepath.Dir(h.path), "s1", "subagents", "agent-1.jsonl")
	other := filepath.Join(filepath.Dir(h.path), "s2.jsonl")
	if err := os.MkdirAll(filepath.Dir(sub), 0o700); err != nil {
		t.Fatal(err)
	}
	for p, id := range map[string]string{sub: "S", other: "O"} {
		if err := os.WriteFile(p, []byte(holdLine(id, "2026-11-06T11:02:00Z", 5)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, h.now, h.now); err != nil {
			t.Fatal(err)
		}
	}

	rel, ok := EndedRel(h.c.ConfigDir, h.path)
	if !ok || rel != "projects/P/s1.jsonl" {
		t.Fatalf("EndedRel = %q, %v", rel, ok)
	}
	h.c.Ended = []string{rel}
	res := h.scan(t, emptyState(t))
	got := map[string]bool{}
	for _, id := range lineIDs(res) {
		got[id] = true
	}
	if !reflect.DeepEqual(got, map[string]bool{"msg_A": true, "msg_B": true, "msg_S": true}) {
		t.Fatalf("Anfragen %v, erwartet A, B und S", lineIDs(res))
	}
	if !reflect.DeepEqual(res.HeldMonths, []string{"2026-11"}) {
		t.Fatalf("zurückgehaltene Monate %v (s2 ist nicht beendet)", res.HeldMonths)
	}
	// Danach steht der Lesestand am Ende: Der nächste Lauf liest nichts doppelt.
	h.c.Ended = nil
	if res2 := h.scan(t, res.Next); len(res2.Lines) != 0 {
		t.Fatalf("zweiter Lauf lieferte %v", lineIDs(res2))
	}
}

// Ein Protokollname außerhalb von projects/ des Konfigurationsordners wird nicht übernommen.
func TestEndedRelRejectsForeignPath(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"", "relativ.jsonl", filepath.Join(root, "x.jsonl"), filepath.Join(root, "projects", "P", "s1.txt"),
		filepath.Join(filepath.Dir(root), "anders", "projects", "P", "s1.jsonl"), filepath.Join(root, "projects", "P", "s1", "subagents", "a.jsonl")} {
		if rel, ok := EndedRel(root, p); ok {
			t.Errorf("EndedRel(%q) = %q, erwartet abgelehnt", p, rel)
		}
	}
}

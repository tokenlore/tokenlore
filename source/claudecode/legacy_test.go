package claudecode

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/tokenlore/tokenlore/protocol"
)

func TestReadLegacy(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "stats-cache.json"), fixture(t, "stats-cache.json"))
	models, acts, err := readLegacy(root)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Date < models[j].Date })
	wantM := []protocol.LegacyModelDay{
		{Date: "2026-08-14", Model: "claude-opus-4-1", Tokens: 150000},
		{Date: "2026-08-15", Model: "claude-sonnet-4-5", Tokens: 90000},
	}
	if !reflect.DeepEqual(models, wantM) {
		t.Errorf("Modelle %+v", models)
	}
	wantA := []protocol.LegacyActivityDay{
		{Date: "2026-08-14", Messages: 120, Sessions: 3, ToolCalls: 40},
		{Date: "2026-08-15", Messages: 80, Sessions: 2, ToolCalls: 25},
	}
	if !reflect.DeepEqual(acts, wantA) {
		t.Errorf("Aktivität %+v", acts)
	}
}

func TestReadLegacyMissing(t *testing.T) {
	m, a, err := readLegacy(t.TempDir())
	if err != nil || len(m) != 0 || len(a) != 0 {
		t.Fatalf("%v %v %v", m, a, err)
	}
}

func TestLegacyOnlyOnce(t *testing.T) {
	c, root := newScanEnv(t)
	os.MkdirAll(root, 0o700)
	write(t, filepath.Join(root, "stats-cache.json"), fixture(t, "stats-cache.json"))
	b, next, _ := scan(t, c, emptyState(t))
	if len(b.LegacyModel) != 2 || len(b.LegacyActivity) != 2 || !next.LegacyDone {
		t.Fatalf("erster Lauf: %d/%d done=%v", len(b.LegacyModel), len(b.LegacyActivity), next.LegacyDone)
	}
	b, _, _ = scan(t, c, next)
	if len(b.LegacyModel) != 0 || len(b.LegacyActivity) != 0 {
		t.Fatal("Altbestand ein zweites Mal geliefert")
	}
}

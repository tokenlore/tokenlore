package claudecode

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/tokenlore/tokenlore/protocol"
)

func TestReadLegacy(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "stats-cache.json"), fixture(t, "stats-cache.json"))
	lf, err := readLegacy(root)
	if err != nil {
		t.Fatal(err)
	}
	models, acts := lf.Models, lf.Acts
	if lf.CompleteBefore != "2026-08-15" {
		t.Errorf("lastComputedDate %q", lf.CompleteBefore)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Date < models[j].Date })
	wantM := []legacyModel{
		{Date: "2026-08-14", Model: "claude-opus-4-1", Tokens: 150000},
		{Date: "2026-08-15", Model: "claude-sonnet-4-5", Tokens: 90000},
	}
	if !reflect.DeepEqual(models, wantM) {
		t.Errorf("Modelle %+v", models)
	}
	wantA := []legacyActivity{
		{Date: "2026-08-14", Messages: 120, Sessions: 3, ToolCalls: 40},
		{Date: "2026-08-15", Messages: 80, Sessions: 2, ToolCalls: 25},
	}
	if !reflect.DeepEqual(acts, wantA) {
		t.Errorf("Aktivität %+v", acts)
	}
}

func TestReadLegacyMissing(t *testing.T) {
	lf, err := readLegacy(t.TempDir())
	if err != nil || len(lf.Models) != 0 || len(lf.Acts) != 0 || lf.CompleteBefore != "" {
		t.Fatalf("%+v %v", lf, err)
	}
}

// LegacyDays fasst Modelle und Aktivität je Tag zu einer Inhaltszeile zusammen, mit lastComputedDate als
// complete_before.
func TestLegacyDays(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "stats-cache.json"), fixture(t, "stats-cache.json"))
	days, err := LegacyDays(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []protocol.DayLine{
		{Kind: protocol.KindDay, Date: "2026-08-14", Models: map[string]int64{"claude-opus-4-1": 150000},
			Activity: &protocol.DayActivity{Messages: 120, Sessions: 3, ToolCalls: 40}, CompleteBefore: "2026-08-15"},
		{Kind: protocol.KindDay, Date: "2026-08-15", Models: map[string]int64{"claude-sonnet-4-5": 90000},
			Activity: &protocol.DayActivity{Messages: 80, Sessions: 2, ToolCalls: 25}, CompleteBefore: "2026-08-15"},
	}
	if !reflect.DeepEqual(days, want) {
		t.Fatalf("Tage %+v", days)
	}
	days, err = LegacyDays(t.TempDir())
	if err != nil || days != nil {
		t.Fatalf("ohne Datei: %+v %v", days, err)
	}
}

package claudecode

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/state"
)

// Erfundene Kennungen im richtigen Format.
var (
	idOrigin = strings.Repeat("a1", 16)
	idOther  = strings.Repeat("b2", 16)
)

// statsCache baut eine stats-cache.json mit je einem Tageswert pro Tag; lastComputed leer lässt das Feld weg.
func statsCache(lastComputed string, days ...string) string {
	var acts, models []string
	for _, d := range days {
		acts = append(acts, `{"date":"`+d+`","messageCount":10,"sessionCount":1,"toolCallCount":2}`)
		models = append(models, `{"date":"`+d+`","tokensByModel":{"claude-opus-4-1":1000}}`)
	}
	lc := ""
	if lastComputed != "" {
		lc = `"lastComputedDate":"` + lastComputed + `",`
	}
	return `{"version":2,` + lc + `"dailyActivity":[` + strings.Join(acts, ",") + `],"dailyModelTokens":[` + strings.Join(models, ",") + `]}`
}

func writeIdentity(t *testing.T, root string, id state.Identity) {
	t.Helper()
	if err := WriteIdentity(root, id); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyCompleteBeforeFromLastComputedDate(t *testing.T) {
	_, root := newScanEnv(t)
	write(t, filepath.Join(root, "stats-cache.json"), fixture(t, "stats-cache.json"))
	days, err := LegacyDays(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) == 0 {
		t.Fatal("keine Tage")
	}
	for _, d := range days {
		if d.CompleteBefore != "2026-08-15" {
			t.Fatalf("%s: complete_before %q", d.Date, d.CompleteBefore)
		}
	}
}

func TestLegacyCompleteBeforeMissingOrInvalidOmitted(t *testing.T) {
	for name, lc := range map[string]string{"fehlt": "", "ungültig": "2026-02-30"} {
		t.Run(name, func(t *testing.T) {
			_, root := newScanEnv(t)
			write(t, filepath.Join(root, "stats-cache.json"), statsCache(lc, "2026-08-14"))
			days, err := LegacyDays(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(days) != 1 || days[0].CompleteBefore != "" {
				t.Fatalf("Tage %+v", days)
			}
		})
	}
}

func TestLegacyNoDaysWithoutDays(t *testing.T) {
	_, root := newScanEnv(t)
	write(t, filepath.Join(root, "stats-cache.json"), statsCache("2026-08-15"))
	days, err := LegacyDays(root)
	if err != nil || len(days) != 0 {
		t.Fatalf("Tage %+v, %v", days, err)
	}
}

// Passt die Kennungsdatei nicht zur Kopplung, meldet der Scan es und lässt die Kopie im Merkzettel stehen; geliefert
// wird dann nichts (deliver.CheckDevice).
func TestIdentityChangedDetected(t *testing.T) {
	c, root := newScanEnv(t)
	write(t, filepath.Join(root, "projects", "P", "s1.jsonl"), fixture(t, "basic.jsonl"))
	writeIdentity(t, root, state.Identity{ID: idOther})
	c.PairedIdentity = idOrigin
	res, err := c.Scan(t.Context(), emptyState(t))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IdentityChanged {
		t.Fatal("geänderte Kennung nicht erkannt")
	}
	if res.Next.Identity != nil {
		t.Fatalf("Kopie übernommen: %+v", res.Next.Identity)
	}
}

func TestIdentityMissingCountsAsChanged(t *testing.T) {
	c, _ := newScanEnv(t)
	c.PairedIdentity = idOrigin
	res, err := c.Scan(t.Context(), emptyState(t))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IdentityChanged {
		t.Fatal("ohne Kennungsdatei und Kopie nicht als geändert erkannt")
	}
}

func TestIdentityFromCopyWhenFileMissing(t *testing.T) {
	c, _ := newScanEnv(t)
	c.PairedIdentity = idOrigin
	st := emptyState(t)
	st.Identity = &state.Identity{ID: idOrigin}
	res, err := c.Scan(t.Context(), st)
	if err != nil {
		t.Fatal(err)
	}
	if res.IdentityChanged {
		t.Fatal("Kopie passt, trotzdem geändert")
	}
}

func TestIdentityMatchesUpdatesCopy(t *testing.T) {
	c, root := newScanEnv(t)
	machine := strings.Repeat("c3", 32)
	writeIdentity(t, root, state.Identity{ID: idOrigin, Machine: machine})
	c.PairedIdentity = strings.ToUpper(idOrigin)
	st := emptyState(t)
	st.Identity = &state.Identity{ID: idOrigin}
	res, err := c.Scan(t.Context(), st)
	if err != nil {
		t.Fatal(err)
	}
	if res.IdentityChanged {
		t.Fatal("gleiche Kennung als geändert erkannt")
	}
	if res.Next.Identity == nil || res.Next.Identity.Machine != machine || st.Identity.Machine != "" {
		t.Fatalf("Kopie %+v, Eingabe %+v", res.Next.Identity, st.Identity)
	}
}

func TestIdentityWithoutPairedNotChecked(t *testing.T) {
	c, root := newScanEnv(t)
	writeIdentity(t, root, state.Identity{ID: idOther})
	res, err := c.Scan(t.Context(), emptyState(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.IdentityChanged {
		t.Fatal("ohne Kopplung verglichen")
	}
}

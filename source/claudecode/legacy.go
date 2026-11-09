package claudecode

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
)

// legacyModel und legacyActivity sind die Tageswerte aus stats-cache.json, wie sie dort stehen.
type legacyModel struct {
	Date, Model string
	Tokens      int64
}

type legacyActivity struct {
	Date                          string
	Messages, Sessions, ToolCalls int
}

// legacyFile ist der gelesene Altbestand aus stats-cache.json.
type legacyFile struct {
	Models []legacyModel
	Acts   []legacyActivity
	// CompleteBefore ist lastComputedDate, wenn es ein gültiges Datum ist, sonst leer.
	CompleteBefore string
}

// readLegacy liest die Tageswerte aus stats-cache.json (Altbestand aus der Zeit, bevor tokenlore lief).
func readLegacy(configDir string) (legacyFile, error) {
	var lf legacyFile
	b, err := os.ReadFile(filepath.Join(configDir, "stats-cache.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return lf, nil
	}
	if err != nil {
		return lf, err
	}
	var f struct {
		LastComputedDate string `json:"lastComputedDate"`

		DailyActivity []struct {
			Date          string `json:"date"`
			MessageCount  int    `json:"messageCount"`
			SessionCount  int    `json:"sessionCount"`
			ToolCallCount int    `json:"toolCallCount"`
		} `json:"dailyActivity"`
		DailyModelTokens []struct {
			Date          string           `json:"date"`
			TokensByModel map[string]int64 `json:"tokensByModel"`
		} `json:"dailyModelTokens"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return lf, err
	}
	if protocol.ValidateCompleteBefore(f.LastComputedDate) == nil {
		lf.CompleteBefore = f.LastComputedDate
	}
	validDate := func(d string) bool { _, err := time.Parse("2006-01-02", d); return err == nil }
	for _, d := range f.DailyModelTokens {
		if !validDate(d.Date) {
			continue
		}
		for m, n := range d.TokensByModel {
			if m == "<synthetic>" || !protocol.ModelPattern.MatchString(m) || n < 0 {
				continue
			}
			lf.Models = append(lf.Models, legacyModel{Date: d.Date, Model: m, Tokens: n})
		}
	}
	for _, d := range f.DailyActivity {
		if !validDate(d.Date) {
			continue
		}
		lf.Acts = append(lf.Acts, legacyActivity{Date: d.Date, Messages: d.MessageCount,
			Sessions: d.SessionCount, ToolCalls: d.ToolCallCount})
	}
	return lf, nil
}

// LegacyDays liest stats-cache.json als Inhaltszeilen der Vorgeschichte, je Tag eine, sortiert nach Datum, mit
// lastComputedDate als complete_before. Fehlt die Datei, gibt es keine Tage und keinen Fehler. Geprüft wird erst
// beim Archivieren.
func LegacyDays(configDir string) ([]protocol.DayLine, error) {
	lf, err := readLegacy(configDir)
	if err != nil {
		return nil, err
	}
	models, acts := lf.Models, lf.Acts
	byDate := map[string]*protocol.DayLine{}
	get := func(date string) *protocol.DayLine {
		d := byDate[date]
		if d == nil {
			d = &protocol.DayLine{Kind: protocol.KindDay, Date: date, CompleteBefore: lf.CompleteBefore}
			byDate[date] = d
		}
		return d
	}
	for _, m := range models {
		d := get(m.Date)
		if d.Models == nil {
			d.Models = map[string]int64{}
		}
		d.Models[m.Model] += m.Tokens
	}
	for _, a := range acts {
		get(a.Date).Activity = &protocol.DayActivity{Messages: int64(a.Messages), Sessions: int64(a.Sessions), ToolCalls: int64(a.ToolCalls)}
	}
	var out []protocol.DayLine
	for _, date := range slices.Sorted(maps.Keys(byDate)) {
		out = append(out, *byDate[date])
	}
	return out, nil
}

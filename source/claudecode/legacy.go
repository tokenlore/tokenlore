package claudecode

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
)

// readLegacy liest die Tageswerte aus stats-cache.json (Altbestand aus der Zeit, bevor tokenlore lief).
func readLegacy(configDir string) ([]protocol.LegacyModelDay, []protocol.LegacyActivityDay, error) {
	b, err := os.ReadFile(filepath.Join(configDir, "stats-cache.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var f struct {
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
		return nil, nil, err
	}
	validDate := func(d string) bool { _, err := time.Parse("2006-01-02", d); return err == nil }
	var models []protocol.LegacyModelDay
	for _, d := range f.DailyModelTokens {
		if !validDate(d.Date) {
			continue
		}
		for m, n := range d.TokensByModel {
			if m == "<synthetic>" || !protocol.ModelPattern.MatchString(m) || n < 0 {
				continue
			}
			models = append(models, protocol.LegacyModelDay{Date: d.Date, Model: m, Tokens: n})
		}
	}
	var acts []protocol.LegacyActivityDay
	for _, d := range f.DailyActivity {
		if !validDate(d.Date) {
			continue
		}
		acts = append(acts, protocol.LegacyActivityDay{Date: d.Date, Messages: d.MessageCount,
			Sessions: d.SessionCount, ToolCalls: d.ToolCallCount})
	}
	return models, acts, nil
}

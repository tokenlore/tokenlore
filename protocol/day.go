package protocol

import (
	"errors"
	"fmt"
	"time"
)

// DayLine ist die Inhaltszeile eines Vorgeschichte-Tags im Archiv (protocol/doc.md, „Inhaltszeile: Vorgeschichte-Tag“):
// die Tageswerte aus stats-cache.json für einen Tag ohne Einzelanfragen. Optionale Felder fehlen ganz.
type DayLine struct {
	Kind           string           `json:"kind"`
	Date           string           `json:"date"`
	Models         map[string]int64 `json:"models,omitempty"`
	Activity       *DayActivity     `json:"activity,omitempty"`
	CompleteBefore string           `json:"complete_before,omitempty"`
}

// DayActivity ist die Aktivität eines Vorgeschichte-Tags; alle drei Felder sind Pflicht.
type DayActivity struct {
	Messages  int64 `json:"messages"`
	Sessions  int64 `json:"sessions"`
	ToolCalls int64 `json:"tool_calls"`
}

// ValidateDayLine prüft einen Vorgeschichte-Tag nach den Regeln, mit denen der Server ihn prüft, samt Länge der Zeile.
func ValidateDayLine(d DayLine, now time.Time) error {
	if d.Kind != KindDay {
		return errors.New("kind ist nicht day")
	}
	if err := checkLegacyDate(d.Date, now); err != nil {
		return err
	}
	if len(d.Models) == 0 && d.Activity == nil {
		return errors.New("models oder activity fehlt")
	}
	if len(d.Models) > MaxLegacyModels {
		return fmt.Errorf("mehr als %d Modelle", MaxLegacyModels)
	}
	for m, n := range d.Models {
		if !ModelPattern.MatchString(m) || n < 0 || n > MaxLegacyNumber {
			return errors.New("models: Modell ungültig")
		}
	}
	if a := d.Activity; a != nil {
		for _, v := range []int64{a.Messages, a.Sessions, a.ToolCalls} {
			if v < 0 || v > MaxLegacyNumber {
				return errors.New("activity außerhalb des Bereichs")
			}
		}
	}
	if d.CompleteBefore != "" {
		if err := ValidateCompleteBefore(d.CompleteBefore); err != nil {
			return err
		}
	}
	b, err := MarshalLine(d)
	if err != nil {
		return fmt.Errorf("Zeile nicht schreibbar: %w", err)
	}
	if len(b) > MaxLineBytes {
		return fmt.Errorf("Zeile länger als %d Byte", MaxLineBytes)
	}
	return nil
}

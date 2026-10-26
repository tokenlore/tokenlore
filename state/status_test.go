package state

import (
	"fmt"
	"testing"
	"time"
)

func TestStatusKeepsAtMost20Warnings(t *testing.T) {
	dir := t.TempDir()
	s := &Status{LastAttempt: time.Now(), LastRequests: 3, LastParts: 1, Flagged: 2}
	for i := 0; i < 25; i++ {
		s.Warnings = append(s.Warnings, fmt.Sprintf("w%d", i))
	}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 20 || got.Warnings[0] != "w5" || got.Warnings[19] != "w24" {
		t.Errorf("Warnungen: %v", got.Warnings)
	}
	if got.LastRequests != 3 || got.LastParts != 1 || got.Flagged != 2 {
		t.Errorf("Status: %+v", got)
	}
}

func TestStatusMissingIsEmpty(t *testing.T) {
	s, err := LoadStatus(t.TempDir())
	if err != nil || s == nil {
		t.Fatalf("s=%v err=%v", s, err)
	}
}

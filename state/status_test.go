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

func none(string) bool { return false }

func TestStatusAddNewFieldsDropsKnown(t *testing.T) {
	s := &Status{NewFields: []string{"alt", "jetztGelesen"}}
	s.AddNewFields([]string{"neu"}, func(n string) bool { return n == "jetztGelesen" })
	if fmt.Sprint(s.NewFields) != "[alt neu]" {
		t.Fatalf("Namen %q", s.NewFields)
	}
	s.AddNewFields(nil, func(string) bool { return true })
	if s.NewFields != nil {
		t.Fatalf("alle bekannt: %q", s.NewFields)
	}
}

func TestStatusAddNewFields(t *testing.T) {
	s := &Status{}
	s.AddNewFields(nil, none)
	if s.NewFields != nil {
		t.Fatalf("leer: %q", s.NewFields)
	}
	s.AddNewFields([]string{"neu", "alt"}, none)
	s.AddNewFields([]string{"alt", "mitte"}, none)
	if fmt.Sprint(s.NewFields) != "[alt mitte neu]" {
		t.Fatalf("Namen %q", s.NewFields)
	}
	var many []string
	for i := 0; i < MaxNewFields+5; i++ {
		many = append(many, fmt.Sprintf("f%02d", i))
	}
	s.AddNewFields(many, none)
	if len(s.NewFields) != MaxNewFields || s.NewFields[0] != "alt" {
		t.Fatalf("%d Namen, erster %q", len(s.NewFields), s.NewFields[0])
	}
	dir := t.TempDir()
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	back, err := LoadStatus(dir)
	if err != nil || len(back.NewFields) != MaxNewFields {
		t.Fatalf("gespeichert %d, %v", len(back.NewFields), err)
	}
}

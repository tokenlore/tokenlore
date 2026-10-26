package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingGivesDefaults(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.EmailMode != "off" {
		t.Errorf("EmailMode %q, want off", s.EmailMode)
	}
	if s.Files == nil || s.Sessions == nil {
		t.Error("Files/Sessions dürfen nicht nil sein")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	key := "projects/P 1 ü/s1.jsonl"
	s.Files[key] = FileMark{Offset: 42, HeadLen: 10, HeadSum: "abc"}
	s.Sessions["s1"] = "acc-1"
	s.EmailMode = "short"
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Files[key] != (FileMark{Offset: 42, HeadLen: 10, HeadSum: "abc"}) {
		t.Errorf("FileMark: %+v", got.Files[key])
	}
	if got.Sessions["s1"] != "acc-1" || got.EmailMode != "short" {
		t.Errorf("State: %+v", got)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "state.json" || strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("unerwartete Datei %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("%d Dateien, want 1", len(entries))
	}
}

func TestCloneIsIndependent(t *testing.T) {
	s, _ := Load(t.TempDir())
	s.Files["a"] = FileMark{Offset: 1}
	c := s.Clone()
	c.Files["a"] = FileMark{Offset: 2}
	c.Sessions["x"] = "y"
	if s.Files["a"].Offset != 1 || len(s.Sessions) != 0 {
		t.Error("Clone teilt Maps mit dem Original")
	}
}

func TestRecordSessionMergedOnLoad(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	s.Sessions["alt"] = "acc-0"
	s.Save(dir)
	if err := RecordSession(dir, "s9", "acc-9"); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sessions["s9"] != "acc-9" || got.Sessions["alt"] != "acc-0" {
		t.Fatalf("Sessions %v", got.Sessions)
	}
}

func TestLoadSurvivesBrokenSessionLog(t *testing.T) {
	dir := t.TempDir()
	RecordSession(dir, "s1", "acc-1")
	f, _ := os.OpenFile(dir+"/session-accounts.jsonl", os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"s":"kaputt` + strings.Repeat("x", 100000) + "\n")
	f.Close()
	RecordSession(dir, "s2", "acc-2")
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load scheitert an kaputter Zeile: %v", err)
	}
	if s.Sessions["s1"] != "acc-1" || s.Sessions["s2"] != "acc-2" {
		t.Fatalf("Sessions %v", s.Sessions)
	}
}

func TestDataDirIgnoresPluginData(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("TOKENLORE_DATA_DIR", "")
	got, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	base, _ := os.UserConfigDir()
	if got != filepath.Join(base, "tokenlore") {
		t.Errorf("DataDir %q", got)
	}
}

func TestDataDirOverride(t *testing.T) {
	d := t.TempDir()
	t.Setenv("TOKENLORE_DATA_DIR", d)
	got, err := DataDir()
	if err != nil || got != d {
		t.Errorf("got %q err %v", got, err)
	}
}

func TestStateDeliveredRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	s.Delivered = true
	s.AccountsSum = "abc123"
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(dir)
	if !got.Delivered || got.AccountsSum != "abc123" {
		t.Errorf("%+v", got)
	}
	c := got.Clone()
	if !c.Delivered || c.AccountsSum != "abc123" {
		t.Errorf("Clone: %+v", c)
	}
}

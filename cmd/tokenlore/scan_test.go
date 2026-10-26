package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

func scanEnv(t *testing.T) (cfg, data string) {
	t.Helper()
	cfg = filepath.Join(t.TempDir(), "Jö Test")
	data = t.TempDir()
	fx, err := os.ReadFile("../../source/claudecode/testdata/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(cfg, "projects", "P")
	os.MkdirAll(p, 0o700)
	os.WriteFile(filepath.Join(p, "s1.jsonl"), fx, 0o600)
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	return cfg, data
}

func TestScanSummaryOnFixture(t *testing.T) {
	cfg, data := scanEnv(t)
	var out bytes.Buffer
	if code := runScan([]string{"--dry-run", "--summary"}, &out, data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "claude-opus-5-5") || !strings.Contains(s, "Anfragen gesamt: 1") {
		t.Fatalf("Zusammenfassung:\n%s", s)
	}
	st, _ := state.Load(data)
	if len(st.Files) != 0 {
		t.Fatal("--dry-run ohne --commit darf den Merkzettel nicht speichern")
	}
}

func TestScanJSON(t *testing.T) {
	cfg, data := scanEnv(t)
	var out bytes.Buffer
	if code := runScan([]string{"--json"}, &out, data, cfg); code != 0 {
		t.Fatalf("Exit %d", code)
	}
	var parts []protocol.Batch
	if err := json.Unmarshal(out.Bytes(), &parts); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out.String())
	}
	if len(parts) != 1 || parts[0].V != protocol.Version || len(parts[0].Requests) != 1 || parts[0].Device.OS == "" {
		t.Fatalf("Pakete %+v", parts)
	}
}

// scan zeigt nur an; den Merkzettel speichert allein der Abgleich nach erfolgreicher Lieferung.
func TestScanHasNoCommitFlag(t *testing.T) {
	cfg, data := scanEnv(t)
	var out bytes.Buffer
	if code := runScan([]string{"--commit"}, &out, data, cfg); code != 2 {
		t.Fatalf("scan --commit: Exit %d, want 2", code)
	}
	out.Reset()
	if code := runScan([]string{"--summary"}, &out, data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(data, "state.json")); !os.IsNotExist(err) {
		t.Fatal("scan --summary hat state.json geschrieben")
	}
}

func TestSummaryTakesMaxOfDuplicates(t *testing.T) {
	b := protocol.Batch{Requests: []protocol.Request{
		{MessageID: "m", RequestID: "r", Model: "x", Output: 10, CacheRead: 0},
		{MessageID: "m", RequestID: "r", Model: "x", Output: 7, CacheRead: 5},
	}}
	var out bytes.Buffer
	printSummary(&out, b, 1, 0)
	if !strings.Contains(out.String(), "Anfragen gesamt: 1") {
		t.Fatalf("%s", out.String())
	}
	fields := strings.Fields(strings.Split(strings.TrimSpace(out.String()), "\n")[7])
	if fields[0] != "x" || fields[3] != "10" || fields[5] != "5" {
		t.Fatalf("Zeile %v, want Output 10 und CacheRead 5", fields)
	}
}

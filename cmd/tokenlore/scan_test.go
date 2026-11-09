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
	var lines []protocol.RequestLine
	if err := json.Unmarshal(out.Bytes(), &lines); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out.String())
	}
	if len(lines) != 1 || lines[0].Kind != protocol.KindRequest || lines[0].MessageID != "msg_A" {
		t.Fatalf("Zeilen %+v", lines)
	}
}

// scan zeigt nur an; Archiv und Merkzettel schreibt allein der Abgleich.
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
	for _, n := range []string{"state.json", "archiv"} {
		if _, err := os.Stat(filepath.Join(data, n)); !os.IsNotExist(err) {
			t.Fatalf("scan --summary hat %s geschrieben", n)
		}
	}
}

// Wie im Archiv zählt eine Anfrage einmal, mit der zuerst gelesenen Zeile; spätere Kopien ändern nichts.
func TestSummaryCountsDuplicatesOnce(t *testing.T) {
	line := func(out string) protocol.RequestLine {
		return protocol.RequestLine{MessageID: "m", RequestID: "r", SessionID: "s", Model: "x",
			Usage: map[string]any{"output_tokens": json.Number(out), "cache_read_input_tokens": json.Number("5")}}
	}
	var out bytes.Buffer
	printSummary(&out, []protocol.RequestLine{line("10"), line("7")}, 0)
	if !strings.Contains(out.String(), "Anfragen gesamt: 1") {
		t.Fatalf("%s", out.String())
	}
	fields := strings.Fields(strings.Split(strings.TrimSpace(out.String()), "\n")[4])
	if fields[0] != "x" || fields[1] != "1" || fields[3] != "10" || fields[5] != "5" {
		t.Fatalf("Zeile %v, want 1 Anfrage, Output 10 und CacheRead 5", fields)
	}
}

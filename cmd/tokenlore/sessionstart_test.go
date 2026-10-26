package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/state"
)

func TestSessionStartRecords(t *testing.T) {
	data := t.TempDir()
	cj := filepath.Join(t.TempDir(), ".claude.json")
	os.WriteFile(cj, []byte(`{"oauthAccount":{"accountUuid":"acc-9"}}`), 0o600)
	if err := runSessionStart(strings.NewReader(`{"session_id":"s9","hook_event_name":"SessionStart"}`), data, cj); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(data)
	if st.Sessions["s9"] != "acc-9" {
		t.Fatalf("Sessions %v", st.Sessions)
	}
}

func TestSessionStartNoAccount(t *testing.T) {
	data := t.TempDir()
	if err := runSessionStart(strings.NewReader(`{"session_id":"s9"}`), data, filepath.Join(data, "fehlt.json")); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(data)
	if len(st.Sessions) != 0 {
		t.Fatalf("Sessions %v", st.Sessions)
	}
}

func TestSessionStartBadStdin(t *testing.T) {
	if err := runSessionStart(strings.NewReader(`kaputt`), t.TempDir(), "x"); err != nil {
		t.Fatalf("kaputte Eingabe darf den Sessionstart nicht stören: %v", err)
	}
}

func TestSessionStartRejectsOddSessionID(t *testing.T) {
	data := t.TempDir()
	cj := filepath.Join(t.TempDir(), ".claude.json")
	os.WriteFile(cj, []byte(`{"oauthAccount":{"accountUuid":"acc-9"}}`), 0o600)
	runSessionStart(strings.NewReader(`{"session_id":"`+strings.Repeat("a", 5000)+`"}`), data, cj)
	runSessionStart(strings.NewReader(`{"session_id":"a\nb"}`), data, cj)
	st, _ := state.Load(data)
	if len(st.Sessions) != 0 {
		t.Fatalf("ungültige session_id gespeichert: %d Einträge", len(st.Sessions))
	}
}

func TestSessionStartNeverHangsOnOpenStdin(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	go w.Write([]byte(`{"session_id":"s1"`)) // unvollständig, stdin wird nie geschlossen
	start := time.Now()
	done := make(chan struct{})
	go func() { runSessionStart(r, t.TempDir(), "x"); close(done) }()
	select {
	case <-done:
		if d := time.Since(start); d > sessionStartBudget+200*time.Millisecond {
			t.Fatalf("Sessionstart dauerte %v", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Sessionstart hängt bei offenem stdin")
	}
}

// TestHelperProcess ist kein echter Test: Die Hook-Tests starten das Testprogramm erneut als Hook
// bzw. als Hintergrund-Kind (übliches Go-Muster). Das Merkmal GO_WANT_HELPER_PROCESS wirkt nur hier im Testprogramm.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv("GO_WANT_HELPER_PROCESS") {
	case "hook":
		self := os.Args[0]
		backgroundCommand = func() (string, []string) {
			return self, []string{"-test.run=^TestHelperProcess$"}
		}
		os.Setenv("GO_WANT_HELPER_PROCESS", "child")
		os.Exit(run([]string{"session-start"}))
	case "child":
		time.Sleep(1500 * time.Millisecond)
		// Das Kind muss dasselbe Datenverzeichnis sehen wie der Hook.
		os.WriteFile(os.Getenv("HELPER_OUT_FILE"), []byte(os.Getenv("TOKENLORE_DATA_DIR")), 0o600)
		os.Exit(0)
	}
}

func TestSessionStartReturnsWhileChildRuns(t *testing.T) {
	f := newFakeServer(t)
	_, data := connectedEnv(t, f)
	outFile := filepath.Join(t.TempDir(), "kind.txt")

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=hook", "HELPER_OUT_FILE="+outFile)
	cmd.Stdin = strings.NewReader(`{"session_id":"s1"}`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	io.ReadAll(stdout) // EOF erst, wenn niemand mehr die Schreibseite hält – auch kein Kind
	eof := time.Since(start)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Hook: %v (%s)", err, stderr.String())
	}
	waited := time.Since(start)
	if eof >= time.Second || waited >= time.Second {
		t.Fatalf("Hook hielt stdout %v bzw. bis zum Ende %v offen", eof, waited)
	}
	if _, err := os.Stat(outFile); err == nil {
		t.Fatal("Kind war schon fertig – Test misst nichts")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(outFile)
		if err == nil && len(b) > 0 {
			abs, _ := filepath.Abs(data)
			if string(b) != abs {
				t.Fatalf("Kind sah Datenverzeichnis %q, want %q", b, abs)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Hintergrund-Kind hat die Datei nicht geschrieben")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSessionStartNoSpawnWhenNotConnected(t *testing.T) {
	_, data := connectEnv(t)
	called := false
	old := backgroundCommand
	backgroundCommand = func() (string, []string) { called = true; return "", nil }
	t.Cleanup(func() { backgroundCommand = old })
	startBackgroundSync(data)
	if called {
		t.Fatal("ohne connection.json gestartet")
	}
}

// Das Kind erbt weder das Arbeitsverzeichnis des Hooks noch einen relativen CLAUDE_CONFIG_DIR.
func TestBackgroundCmdDirAndEnv(t *testing.T) {
	data := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "rel/claude")
	t.Setenv("TOKENLORE_DATA_DIR", "anderes")
	cmd := backgroundCmd(data, "x", nil)
	if cmd.Dir != data {
		t.Fatalf("Dir %q, want %q", cmd.Dir, data)
	}
	wantCfg, _ := filepath.Abs("rel/claude")
	var gotCfg, gotData []string
	for _, e := range cmd.Env {
		if v, ok := strings.CutPrefix(e, "CLAUDE_CONFIG_DIR="); ok {
			gotCfg = append(gotCfg, v)
		}
		if v, ok := strings.CutPrefix(e, "TOKENLORE_DATA_DIR="); ok {
			gotData = append(gotData, v)
		}
	}
	if len(gotCfg) != 1 || gotCfg[0] != wantCfg || len(gotData) != 1 || gotData[0] != data {
		t.Fatalf("CLAUDE_CONFIG_DIR %v, TOKENLORE_DATA_DIR %v", gotCfg, gotData)
	}
}

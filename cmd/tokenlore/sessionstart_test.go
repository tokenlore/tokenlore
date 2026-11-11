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

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/state"
)

// sessionCfg legt einen Claude-Ordner mit angemeldetem Konto und, wenn id nicht leer ist, einer Kennungsdatei an.
func sessionCfg(t *testing.T, id string) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"acc-9"}}`), 0o600)
	if id != "" {
		writeIdentityFile(t, cfg, `{"id":"`+id+`"}`)
	}
	return cfg
}

func readStarts(t *testing.T, data, id string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(state.ArchiveDir(data, id), archive.SessionsFile))
	return string(b)
}

// Der Sessionstart hält Sitzung und angemeldetes Konto im Archivordner fest.
func TestSessionStartRecords(t *testing.T) {
	data, cfg := t.TempDir(), sessionCfg(t, fixedID)
	if err := runSessionStart(strings.NewReader(`{"session_id":"s9","hook_event_name":"SessionStart"}`), data, cfg, time.Now(), noEnv); err != nil {
		t.Fatal(err)
	}
	if got := readStarts(t, data, fixedID); !strings.Contains(got, `"session_id":"s9","account_id":"acc-9"`) {
		t.Fatalf("sitzungen.jsonl: %q", got)
	}
	if _, err := os.Stat(filepath.Join(data, "session-accounts.jsonl")); !os.IsNotExist(err) {
		t.Fatal("session-accounts.jsonl geschrieben")
	}
	// Fortgesetzte Sitzung (gleiche session_id, etwa nach /resume oder /clear): kein zweiter Eintrag, auch ohne Kopplung.
	for range 3 {
		runSessionStart(strings.NewReader(`{"session_id":"s9"}`), data, cfg, time.Now(), noEnv)
	}
	if got := readStarts(t, data, fixedID); strings.Count(got, "\n") != 1 {
		t.Fatalf("sitzungen.jsonl mit mehreren Einträgen je Sitzung: %q", got)
	}
}

// Ohne Kennung (nie gekoppelt) und ohne angemeldetes Konto schreibt der Sessionstart nichts, stört aber auch nicht.
// Fehlt nur die Kennungsdatei, gilt die Kopie im Merkzettel.
func TestSessionStartWithoutIdentityOrAccount(t *testing.T) {
	data, cfg := t.TempDir(), sessionCfg(t, "")
	runSessionStart(strings.NewReader(`{"session_id":"s9"}`), data, cfg, time.Now(), noEnv)
	if _, err := os.Stat(filepath.Join(data, "archiv")); !os.IsNotExist(err) {
		t.Fatal("ohne Kennung ein Archiv angelegt")
	}
	st, _ := state.Load(data)
	st.Identity = &state.Identity{ID: fixedID}
	st.Save(data)
	if err := runSessionStart(strings.NewReader(`{"session_id":"s9"}`), data, cfg, time.Now(), noEnv); err != nil || readStarts(t, data, fixedID) == "" {
		t.Fatalf("Kopie der Kennung nicht genutzt: %v", err)
	}
	data2 := t.TempDir()
	runSessionStart(strings.NewReader(`{"session_id":"s9"}`), data2, sessionCfgNoAccount(t), time.Now(), noEnv)
	if _, err := os.Stat(filepath.Join(data2, "archiv")); !os.IsNotExist(err) {
		t.Fatal("ohne Konto geschrieben")
	}
}

func sessionCfgNoAccount(t *testing.T) string {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	writeIdentityFile(t, cfg, `{"id":"`+fixedID+`"}`)
	return cfg
}

func TestSessionStartBadStdin(t *testing.T) {
	if err := runSessionStart(strings.NewReader(`kaputt`), t.TempDir(), t.TempDir(), time.Now(), noEnv); err != nil {
		t.Fatalf("kaputte Eingabe darf den Sessionstart nicht stören: %v", err)
	}
}

func TestSessionStartRejectsOddSessionID(t *testing.T) {
	data, cfg := t.TempDir(), sessionCfg(t, fixedID)
	runSessionStart(strings.NewReader(`{"session_id":"`+strings.Repeat("a", 5000)+`"}`), data, cfg, time.Now(), noEnv)
	runSessionStart(strings.NewReader(`{"session_id":"a\nb"}`), data, cfg, time.Now(), noEnv)
	if got := readStarts(t, data, fixedID); got != "" {
		t.Fatalf("ungültige session_id gespeichert: %q", got)
	}
}

func TestSessionStartNeverHangsOnOpenStdin(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	go w.Write([]byte(`{"session_id":"s1"`)) // unvollständig, stdin wird nie geschlossen
	start := time.Now()
	done := make(chan struct{})
	go func() { runSessionStart(r, t.TempDir(), t.TempDir(), time.Now(), noEnv); close(done) }()
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
	case "hook-end":
		self := os.Args[0]
		backgroundCommand = func() (string, []string) {
			return self, []string{"-test.run=^TestHelperProcess$"}
		}
		os.Setenv("GO_WANT_HELPER_PROCESS", "child")
		os.Exit(run([]string{"session-end"}))
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

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/lock"
	"github.com/tokenlore/tokenlore/state"
)

// spawnCalls ersetzt das Starten des Hintergrund-Kinds und sammelt die Argumente jedes Starts.
func spawnCalls(t *testing.T) func() [][]string {
	t.Helper()
	var mu sync.Mutex
	var calls [][]string
	old := spawn
	spawn = func(dataDir, exe string, args ...string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, slices.Clone(args))
		return nil
	}
	t.Cleanup(func() { spawn = old })
	return func() [][]string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

func endInput(transcript string) *strings.Reader {
	b, _ := json.Marshal(map[string]string{"session_id": "s9", "transcript_path": transcript, "hook_event_name": "SessionEnd", "reason": "other"})
	return strings.NewReader(string(b))
}

// Sitzungsende: Hook zweimal in 5 Minuten ergibt einen Abgleich, nach 5 Minuten wieder einen. Der Protokollname geht
// mit --ended an das Kind.
func TestSessionEndMinimumGap(t *testing.T) {
	f := newFakeServer(t)
	_, data := connectedEnv(t, f)
	testLock(t)
	calls := spawnCalls(t)
	t0 := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

	runSessionEnd(endInput("/x/projects/P/s9.jsonl"), data, t0)
	runSessionEnd(endInput("/x/projects/P/s9.jsonl"), data, t0.Add(4*time.Minute+59*time.Second))
	if n := len(calls()); n != 1 {
		t.Fatalf("%d Abgleiche in 5 Minuten, erwartet 1", n)
	}
	if got := strings.Join(calls()[0], " "); !strings.HasSuffix(got, "sync --background --ended /x/projects/P/s9.jsonl") {
		t.Fatalf("Argumente %q", got)
	}
	runSessionEnd(endInput(""), data, t0.Add(5*time.Minute))
	if n := len(calls()); n != 2 {
		t.Fatalf("%d Abgleiche nach 5 Minuten, erwartet 2", n)
	}
	// Ohne Protokollname: Abgleich ohne --ended, die letzte Anfrage kommt dann beim nächsten Abgleich.
	if got := strings.Join(calls()[1], " "); strings.Contains(got, "--ended") {
		t.Fatalf("Argumente ohne Protokollname: %q", got)
	}
}

// Der letzte Versuch eines Abgleichs (Sessionstart, sync) zählt für den Abstand mit, ebenso einer, der gerade läuft.
func TestSessionEndRespectsOtherSync(t *testing.T) {
	f := newFakeServer(t)
	_, data := connectedEnv(t, f)
	dir := testLock(t)
	calls := spawnCalls(t)
	now := time.Now()
	if err := (&state.Status{LastAttempt: now.Add(-2 * time.Minute)}).Save(data); err != nil {
		t.Fatal(err)
	}
	runSessionEnd(endInput(""), data, now)
	if n := len(calls()); n != 0 {
		t.Fatalf("Abgleich vor 2 Minuten, trotzdem %d gestartet", n)
	}

	if err := (&state.Status{LastAttempt: now.Add(-time.Hour)}).Save(data); err != nil {
		t.Fatal(err)
	}
	release, ok, err := lock.Acquire(dir)
	if err != nil || !ok {
		t.Fatal("Sperre nicht bekommen")
	}
	runSessionEnd(endInput(""), data, now)
	release()
	if n := len(calls()); n != 0 {
		t.Fatalf("Abgleich läuft, trotzdem %d gestartet", n)
	}
	runSessionEnd(endInput(""), data, now)
	if n := len(calls()); n != 1 {
		t.Fatalf("%d Abgleiche, erwartet 1", n)
	}
	if s, _ := state.LoadStatus(data); !s.LastAttempt.Equal(now) {
		t.Fatalf("letzter Versuch %v, erwartet %v", s.LastAttempt, now)
	}
}

// Ohne Verbindung startet nichts, und status.json bleibt unberührt.
func TestSessionEndNotConnected(t *testing.T) {
	_, data := connectEnv(t)
	testLock(t)
	calls := spawnCalls(t)
	runSessionEnd(endInput(""), data, time.Now())
	if len(calls()) != 0 {
		t.Fatal("ohne Verbindung gestartet")
	}
	if _, err := os.Stat(filepath.Join(data, "status.json")); err == nil {
		t.Fatal("status.json ohne Verbindung geschrieben")
	}
}

// Der Hook kehrt auch bei offenem stdin rasch zurück.
func TestSessionEndOpenStdin(t *testing.T) {
	_, data := connectEnv(t)
	testLock(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer r.Close()
	start := time.Now()
	runSessionEnd(r, data, start)
	if d := time.Since(start); d > sessionStartBudget+200*time.Millisecond {
		t.Fatalf("Sitzungsende dauerte %v", d)
	}
}

// endLine ist eine Anfragezeile der Sitzung s9.
func endLine(id string, ts time.Time, out int) string {
	return fmt.Sprintf(`{"type":"assistant","sessionId":"s9","requestId":"req_%s","timestamp":"%s","message":{"id":"msg_%s","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":%d}}}`+"\n",
		id, ts.UTC().Format(time.RFC3339Nano), id, out)
}

// requestIDs zählt die request_id jeder Archivzeile im Archivordner.
func archivedIDs(t *testing.T, dir string) map[string]int {
	t.Helper()
	got := map[string]int{}
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for _, p := range files {
		lines, _, err := archive.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range lines {
			var z struct {
				Kind      string `json:"kind"`
				RequestID string `json:"request_id"`
			}
			if json.Unmarshal([]byte(e.Z), &z) == nil && z.Kind == "request" {
				got[z.RequestID]++
			}
		}
	}
	return got
}

func deliveredIDs(t *testing.T, f *fakeServer) map[string]int {
	t.Helper()
	got := map[string]int{}
	for _, b := range f.ingested() {
		for _, e := range b.Requests {
			var z struct {
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal([]byte(e.Z), &z); err != nil {
				t.Fatal(err)
			}
			got[z.RequestID]++
		}
	}
	return got
}

// Sitzungsende mit Zahlen und nichts doppelt: Die letzte Anfrage einer eben beendeten Sitzung (und ihrer Subagenten)
// geht mit dem Abgleich nach dem Hook raus, ohne neuen Sitzungsstart. Abgleiche davor, gleichzeitig und danach
// (Sessionstart, sync, ein zweites Sitzungsende) archivieren und liefern keine Anfrage zweimal.
func TestSessionEndDeliversWithoutDuplicates(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	testLock(t)
	now := time.Now()
	main := filepath.Join(cfg, "projects", "P", "s9.jsonl")
	sub := filepath.Join(cfg, "projects", "P", "s9", "subagents", "agent-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(sub), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte(endLine("E1", now.Add(-2*time.Minute), 5)+endLine("E2", now.Add(-time.Minute), 7)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sub, []byte(endLine("S1", now.Add(-90*time.Second), 3)), 0o600); err != nil {
		t.Fatal(err)
	}

	// Abgleich während der Sitzung (Sessionstart): E2 und S1 sind die letzten ihrer Datei und noch zurückgehalten.
	if code, out, errOut := syncRun(data, cfg, "--background"); code != 0 {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
	if d := deliveredIDs(t, f); d["req_E1"] != 1 || d["req_E2"] != 0 || d["req_S1"] != 0 {
		t.Fatalf("vor dem Sitzungsende geliefert: %v", d)
	}

	// Sitzungsende: Der Hook startet den Abgleich mit dem Protokollnamen; hier läuft er direkt mit dessen Argumenten.
	// Zugleich starten ein sync und ein Sessionstart: Die Sperre lässt nur einen arbeiten, keiner zählt doppelt.
	if err := (&state.Status{LastAttempt: now.Add(-time.Hour)}).Save(data); err != nil {
		t.Fatal(err)
	}
	calls := spawnCalls(t)
	runSessionEnd(endInput(main), data, now)
	if len(calls()) != 1 {
		t.Fatalf("Hook startete %d Abgleiche", len(calls()))
	}
	args := calls()[0]
	args = args[slices.Index(args, "sync")+1:]
	var wg sync.WaitGroup
	for _, a := range [][]string{args, {"--background"}, args, {}} {
		wg.Add(1)
		go func() { defer wg.Done(); syncRun(data, cfg, a...) }()
	}
	wg.Wait()
	// Was die Sperre eben abgewiesen hat, holt der nächste Lauf nach; danach noch ein sync und ein Sitzungsende.
	syncRun(data, cfg, args...)
	syncRun(data, cfg)
	if code, out, _ := syncRun(data, cfg, "--ended", main); code != 0 || !strings.Contains(out, "Nichts Neues.") {
		t.Fatalf("letzter Lauf: Exit %d: %s", code, out)
	}

	d := deliveredIDs(t, f)
	for _, id := range []string{"req_E1", "req_E2", "req_S1"} {
		if d[id] != 1 {
			t.Fatalf("%s %d-mal geliefert, alle: %v", id, d[id], d)
		}
	}
	for id, n := range d {
		if n != 1 {
			t.Fatalf("%s %d-mal geliefert", id, n)
		}
	}
	a := archivedIDs(t, state.ArchiveDir(data, "fedcba9876543210fedcba9876543210"))
	for id, n := range a {
		if n != 1 {
			t.Fatalf("%s %d-mal im Archiv", id, n)
		}
	}
	if a["req_E2"] != 1 || a["req_S1"] != 1 {
		t.Fatalf("Archiv: %v", a)
	}
}

// Der Hook SessionEnd kehrt sofort zurück, während das Kind noch läuft, und das Kind sieht dasselbe Datenverzeichnis.
// Die gerätweite Sperre liegt dabei in einem Temp-Ordner (Cache-Verzeichnis umgelenkt).
func TestSessionEndReturnsWhileChildRuns(t *testing.T) {
	f := newFakeServer(t)
	_, data := connectedEnv(t, f)
	outFile := filepath.Join(t.TempDir(), "kind.txt")
	home := t.TempDir()

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=hook-end", "HELPER_OUT_FILE="+outFile,
		"HOME="+home, "XDG_CACHE_HOME="+filepath.Join(home, "cache"), "LocalAppData="+filepath.Join(home, "appdata"))
	cmd.Stdin = endInput("")
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
	io.ReadAll(stdout)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Hook: %v (%s)", err, stderr.String())
	}
	if waited := time.Since(start); waited >= sessionEndBrake {
		t.Fatalf("Hook lief %v", waited)
	}
	if _, err := os.Stat(outFile); err == nil {
		t.Fatal("Kind war schon fertig – Test misst nichts")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(outFile)
		if err == nil && len(b) > 0 {
			if abs, _ := filepath.Abs(data); string(b) != abs {
				t.Fatalf("Kind sah Datenverzeichnis %q, want %q", b, abs)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Hintergrund-Kind hat die Datei nicht geschrieben")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s, _ := state.LoadStatus(data); s.LastAttempt.Before(start.Add(-time.Second)) {
		t.Fatalf("letzter Versuch nicht gesetzt: %v", s.LastAttempt)
	}
}

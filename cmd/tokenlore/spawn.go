package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tokenlore/tokenlore/state"
)

// spawn startet ein abgekoppeltes Kind; Tests ersetzen es, um die Aufrufe zu zählen.
var spawn = spawnBackground

// startBackgroundSync startet nach Sessionstart oder Sitzungsende einen abgekoppelten Abgleich, aber nur mit
// Verbindung; extra hängt Argumente an (etwa --ended). Fehler werden geschluckt: Der Hook darf nie stören.
func startBackgroundSync(dataDir string, extra ...string) {
	if _, ok, err := state.LoadConnection(dataDir); err != nil || !ok {
		return
	}
	exe, args := backgroundCommand()
	if exe == "" {
		return
	}
	_ = spawn(dataDir, exe, append(args, extra...)...)
}

// backgroundCmd baut den Befehl für das Kind: ohne stdin/stdout/stderr (exec öffnet dann das Null-Gerät),
// damit es die Pipes des Hooks nicht erbt und Claude Code nicht auf sein Ende wartet. Das aufgelöste
// Datenverzeichnis geht mit, damit Hook und Kind sicher dieselben Daten nutzen.
func backgroundCmd(dataDir, exe string, args []string) *exec.Cmd {
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	// Nicht das Arbeitsverzeichnis des Hooks erben: Unter Windows könnte der Projektordner sonst
	// nicht gelöscht werden, solange der Abgleich läuft. Deshalb muss auch ein relativer
	// CLAUDE_CONFIG_DIR vorher absolut werden.
	cmd.Dir = dataDir
	env := make([]string, 0, len(os.Environ())+2)
	for _, e := range os.Environ() {
		u := strings.ToUpper(e)
		if !strings.HasPrefix(u, "TOKENLORE_DATA_DIR=") && !strings.HasPrefix(u, "CLAUDE_CONFIG_DIR=") {
			env = append(env, e)
		}
	}
	if cfg := os.Getenv("CLAUDE_CONFIG_DIR"); cfg != "" {
		if abs, err := filepath.Abs(cfg); err == nil {
			cfg = abs
		}
		env = append(env, "CLAUDE_CONFIG_DIR="+cfg)
	}
	cmd.Env = append(env, "TOKENLORE_DATA_DIR="+dataDir)
	return cmd
}

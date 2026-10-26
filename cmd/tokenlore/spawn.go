package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tokenlore/tokenlore/state"
)

// startBackgroundSync startet nach dem Sessionstart einen abgekoppelten Abgleich, aber nur mit Verbindung.
// Fehler werden geschluckt: Der Sessionstart darf nie gestört werden.
func startBackgroundSync(dataDir string) {
	if _, ok, err := state.LoadConnection(dataDir); err != nil || !ok {
		return
	}
	exe, args := backgroundCommand()
	if exe == "" {
		return
	}
	_ = spawnBackground(dataDir, exe, args...)
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

package claudecode

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ConfigDir ist der Claude-Code-Konfigurationsordner: $CLAUDE_CONFIG_DIR, sonst ~/.claude.
func ConfigDir() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// ClaudeJSONPath ist die Datei mit dem angemeldeten Konto. Ohne CLAUDE_CONFIG_DIR liegt sie
// neben dem Konfigurationsordner (~/.claude.json), sonst darin.
func ClaudeJSONPath(configDir string) string {
	if os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		return filepath.Join(configDir, ".claude.json")
	}
	return filepath.Join(filepath.Dir(configDir), ".claude.json")
}

// sessionFile ist eine Sitzungsdatei; Rel ist relativ zum Konfigurationsordner, mit "/".
type sessionFile struct {
	Rel      string
	Project  string
	Subagent bool
}

// listSessionFiles findet projects/<p>/*.jsonl und projects/<p>/<id>/subagents/*.jsonl.
func listSessionFiles(configDir string) ([]sessionFile, error) {
	projects, err := os.ReadDir(filepath.Join(configDir, "projects"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []sessionFile
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(configDir, "projects", p.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			switch {
			case !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl"):
				out = append(out, sessionFile{Rel: path.Join("projects", p.Name(), e.Name()), Project: p.Name()})
			case e.IsDir():
				subs, err := os.ReadDir(filepath.Join(configDir, "projects", p.Name(), e.Name(), "subagents"))
				if err != nil {
					continue // kein Subagenten-Ordner
				}
				for _, s := range subs {
					if !s.IsDir() && strings.HasSuffix(s.Name(), ".jsonl") {
						out = append(out, sessionFile{
							Rel:     path.Join("projects", p.Name(), e.Name(), "subagents", s.Name()),
							Project: p.Name(), Subagent: true,
						})
					}
				}
			}
		}
	}
	return out, nil
}

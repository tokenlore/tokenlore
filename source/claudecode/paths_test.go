package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDirEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	got, err := ConfigDir()
	if err != nil || got != dir {
		t.Fatalf("ConfigDir = %q, %v; want %q", got, err, dir)
	}
}

func TestClaudeJSONPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if got := ClaudeJSONPath(dir); got != filepath.Join(dir, ".claude.json") {
		t.Fatalf("mit CLAUDE_CONFIG_DIR: %q", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, _ := os.UserHomeDir()
	if got := ClaudeJSONPath(filepath.Join(home, ".claude")); got != filepath.Join(home, ".claude.json") {
		t.Fatalf("ohne CLAUDE_CONFIG_DIR: %q", got)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListSessionFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Jö Test")
	p := filepath.Join(root, "projects", "P 1 ü")
	write(t, filepath.Join(p, "s1.jsonl"), "")
	write(t, filepath.Join(p, "s1", "subagents", "a.jsonl"), "")
	write(t, filepath.Join(p, "notes.txt"), "")
	files, err := listSessionFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("%d Dateien: %+v", len(files), files)
	}
	want := map[string]sessionFile{
		"projects/P 1 ü/s1.jsonl":             {Rel: "projects/P 1 ü/s1.jsonl", Project: "P 1 ü", Subagent: false},
		"projects/P 1 ü/s1/subagents/a.jsonl": {Rel: "projects/P 1 ü/s1/subagents/a.jsonl", Project: "P 1 ü", Subagent: true},
	}
	for _, f := range files {
		if want[f.Rel] != f {
			t.Errorf("unerwartet: %+v", f)
		}
	}
}

func TestListSessionFilesNoProjects(t *testing.T) {
	files, err := listSessionFiles(t.TempDir())
	if err != nil || len(files) != 0 {
		t.Fatalf("got %v, %v", files, err)
	}
}

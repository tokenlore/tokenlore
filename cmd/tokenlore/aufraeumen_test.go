package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// Struktur: Nach dem ersten Lauf auf einem Datenordner mit alten Dateien gibt es kein session-accounts.jsonl und
// kein Salz mehr, tokenlore-id trägt kein branch_day, und alles liegt wie in der Ordnerstruktur: Datenordner mit
// Kopplung, Merkzettel, Status und Schlüsseldateien, Archivordner des Rechners mit Monatsdatei, konten.jsonl und
// geliefert.json, die Sperre im Cache-Ordner.
func TestFirstRunTidiesOldDataDir(t *testing.T) {
	lockDir := testLock(t)
	setMachine(t, machineA)
	cfg, data := connectEnv(t)
	writeAccount(t, cfg)
	// Stand einer früheren Fassung: Salz, Konto je Sitzung und Altbestand im Merkzettel, Zuordnung Sitzung → Konto
	// als eigene Datei, Kennungsdatei mit last_day und branch_day.
	old := `{"files":{},"sessions":{"s1":"acc-1"},"email_mode":"full","salt":"` + strings.Repeat("ab", 32) + `",` +
		`"delivered":true,"accounts_sum":"abc","legacy_sum":"def","legacy_rejected":["2026-10-01#1,1,1"],"unbekannt":1}`
	if err := os.WriteFile(filepath.Join(data, "state.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, state.OldSessionLog), []byte(`{"s":"s1","a":"acc-1"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeIdentityFile(t, cfg, `{"id":"`+fixedID+`","machine":"`+machineA+`","predecessor":"","last_day":"2026-09-01","branch_day":"2026-08-15"}`)

	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	if code, out, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("sync Exit %d: %s %s", code, out, errOut)
	}

	b, err := os.ReadFile(filepath.Join(data, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"salt"`, `"sessions"`, `"delivered"`, `"accounts_sum"`, `"legacy_sum"`, `"legacy_rejected"`, `"unbekannt"`} {
		if strings.Contains(string(b), k) {
			t.Errorf("state.json enthält %s: %s", k, b)
		}
	}
	if !strings.Contains(string(b), `"email_mode": "full"`) {
		t.Errorf("E-Mail-Stufe verloren: %s", b)
	}
	b, _ = os.ReadFile(filepath.Join(cfg, claudecode.IdentityFile))
	if strings.Contains(string(b), "branch_day") || strings.Contains(string(b), "last_day") || !strings.Contains(string(b), fixedID) {
		t.Errorf("tokenlore-id: %s", b)
	}

	// Datenordner: nur Dateien der Ordnerstruktur.
	got := tree(t, data)
	arch := "archiv/rechner-" + fixedID + "/"
	allowed := []string{"connection.json", "state.json", "status.json", "device.key", "sign.key",
		arch + "konten.jsonl", arch + "geliefert.json", arch + "vorgeschichte.jsonl", arch + "sitzungen.jsonl"}
	for _, p := range got {
		month := strings.HasPrefix(p, arch) && (strings.HasSuffix(p, ".jsonl") || strings.HasSuffix(p, ".jsonl.gz") || strings.HasSuffix(p, ".jsonl.gz.sig"))
		if !slices.Contains(allowed, p) && !month {
			t.Errorf("unerwartete Datei %s", p)
		}
	}
	for _, p := range []string{"connection.json", "state.json", "status.json", arch + "konten.jsonl", arch + "geliefert.json"} {
		if !slices.Contains(got, p) {
			t.Errorf("%s fehlt; vorhanden: %v", p, got)
		}
	}
	if !slices.ContainsFunc(got, func(p string) bool {
		n, ok := strings.CutPrefix(p, arch)
		return ok && len(n) >= len("2026-10.jsonl") && n[4] == '-' && (strings.HasSuffix(n, ".jsonl") || strings.HasSuffix(n, ".jsonl.gz"))
	}) {
		t.Errorf("keine Monatsdatei: %v", got)
	}
	if _, err := os.Stat(filepath.Join(lockDir, "tokenlore.lock")); err != nil {
		t.Errorf("Sperre nicht im Sperrordner: %v", err)
	}
	// Im Claude-Ordner legt tokenlore nur die Kennungsdatei an.
	for _, p := range tree(t, cfg) {
		if strings.HasPrefix(filepath.Base(p), "tokenlore") && p != claudecode.IdentityFile {
			t.Errorf("unerwartete Datei im Claude-Ordner: %s", p)
		}
	}
}

// tree listet alle Dateien unter dir, relativ und mit "/".
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// writeAccount legt das angemeldete Konto mit erfundener Adresse im Claude-Ordner ab (für konten.jsonl).
func writeAccount(t *testing.T, cfg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"oauthAccount":{"accountUuid":"acc-1","emailAddress":"max@beispiel.de"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

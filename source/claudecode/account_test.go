package claudecode

import (
	"path/filepath"
	"testing"
)

func TestReadCurrentAccountMissing(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := ReadCurrentAccount(filepath.Join(dir, "fehlt.json")); ok || err != nil {
		t.Fatalf("fehlende Datei: ok=%v err=%v", ok, err)
	}
	p := filepath.Join(dir, "c.json")
	write(t, p, `{"numStartups": 3}`)
	if _, ok, err := ReadCurrentAccount(p); ok || err != nil {
		t.Fatalf("ohne oauthAccount: ok=%v err=%v", ok, err)
	}
}

func TestReadCurrentAccountIgnoresNames(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	write(t, p, `{"oauthAccount":{"accountUuid":"acc-9","emailAddress":"max@beispiel.de","displayName":"Max","fullName":"Max Mustermann","organizationUuid":"org"}}`)
	a, ok, err := ReadCurrentAccount(p)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if a != (CurrentAccount{AccountID: "acc-9", Email: "max@beispiel.de", From: FromLogin}) {
		t.Fatalf("got %+v", a)
	}
}

func TestSessionAccount(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	write(t, p, `{"oauthAccount":{"accountUuid":"acc-terminal","emailAddress":"max@beispiel.de"}}`)
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }

	a, ok, err := SessionAccount(p, getenv)
	if err != nil || !ok || a != (CurrentAccount{AccountID: "acc-terminal", Email: "max@beispiel.de", From: FromLogin}) {
		t.Fatalf("ohne App: %+v %v %v", a, ok, err)
	}
	env[EnvAccountID], env[EnvEmail] = " acc-app ", "erika@beispiel.de"
	a, ok, err = SessionAccount(p, getenv)
	if err != nil || !ok || a != (CurrentAccount{AccountID: "acc-app", Email: "erika@beispiel.de", From: FromApp}) {
		t.Fatalf("mit App: %+v %v %v", a, ok, err)
	}
	// Eine ungültige Kennung der App zählt nicht.
	env[EnvAccountID] = "kein konto"
	if a, _, _ = SessionAccount(p, getenv); a.AccountID != "acc-terminal" {
		t.Fatalf("ungültige App-Kennung: %+v", a)
	}
	// Ohne .claude.json gilt die App allein.
	env[EnvAccountID] = "acc-app"
	if a, ok, err = SessionAccount(filepath.Join(t.TempDir(), "fehlt.json"), getenv); err != nil || !ok || a.AccountID != "acc-app" {
		t.Fatalf("nur App: %+v %v %v", a, ok, err)
	}
}

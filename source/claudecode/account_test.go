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
	if a != (CurrentAccount{AccountID: "acc-9", Email: "max@beispiel.de"}) {
		t.Fatalf("got %+v", a)
	}
}

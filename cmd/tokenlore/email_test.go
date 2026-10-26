package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/state"
)

func TestEmailPreviewAndSet(t *testing.T) {
	data := t.TempDir()
	cj := filepath.Join(t.TempDir(), ".claude.json")
	os.WriteFile(cj, []byte(`{"oauthAccount":{"accountUuid":"acc-9","emailAddress":"max@beispiel.de"}}`), 0o600)

	var out bytes.Buffer
	if code := runEmail(nil, &out, data, cj); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	for _, want := range []string{"off", "short", "ma…@beispiel.de", "full", "max@beispiel.de", "Aktuell: off", "Ändern mit: /tokenlore:email off|short|full"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("Vorschau ohne %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if code := runEmail([]string{"short"}, &out, data, cj); code != 0 {
		t.Fatalf("Exit %d", code)
	}
	st, _ := state.Load(data)
	if st.EmailMode != "short" {
		t.Fatalf("EmailMode %q", st.EmailMode)
	}
	if code := runEmail([]string{"quatsch"}, &out, data, cj); code != 2 {
		t.Fatalf("unbekannte Stufe: Exit %d, want 2", code)
	}
}

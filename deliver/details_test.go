package deliver

import (
	"os"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/protocol"
)

// detailsFixture sind die Sitzungsdateien für die Tests zum Merkmal request_details: a.jsonl mit einer alten Anfrage
// ohne neue Felder (msg_A), b.jsonl mit einer Anfrage mit neuen Feldern (msg_D), einer ohne (msg_E) und einem
// API-Fehler. Im Archiv stehen sie so: msg_A, msg_D, msg_E, Fehler.
func detailsFixture(t *testing.T) map[string][]byte {
	t.Helper()
	b, err := os.ReadFile("../source/claudecode/testdata/details.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"a.jsonl": fixture(t), "b.jsonl": b}
}

// Ohne Merkmal request_details liefert der Abgleich nur bis vor die erste Zeile, die es braucht, und hält dort an;
// der Merker rückt nur bis dahin vor, und sync, status und Sessionstart nennen den Grund. Nennt der Server das
// Merkmal danach, geht der Rest, jede Zeile einmal.
func TestOldServerHoldsBeforeDetails(t *testing.T) {
	cfg, data := env(t, detailsFixture(t))
	old := newRecorder()
	old.features = []string{"predecessor"}
	sent := runWith(t, cfg, data, fixedNow, old)
	if len(sent) != 1 || !strings.Contains(sent[0], `"message_id":"msg_A"`) || old.infos != 1 {
		t.Fatalf("gesendet %q, Info-Abrufe %d", sent, old.infos)
	}
	month := "2026-10.jsonl"
	if n := delivered(t, data).Lines[month]; n != 1 {
		t.Fatalf("Merker %d, want 1", n)
	}
	stat := loadStatus(t, data)
	if !stat.HeldForServer || len(Notices(stat)) != 1 || Notices(stat)[0] != MsgHeldForServer {
		t.Fatalf("Hinweis fehlt: %+v", stat)
	}
	// Noch einmal mit demselben Server: nichts Neues, der Hinweis bleibt.
	if sent := runWith(t, cfg, data, fixedNow, old); len(sent) != 0 || !loadStatus(t, data).HeldForServer {
		t.Fatalf("zweiter Lauf gesendet %q", sent)
	}

	updated := newRecorder()
	sent = runWith(t, cfg, data, fixedNow, updated)
	if len(sent) != 3 || onceIn(t, sent) != 3 || !strings.Contains(sent[0], `"thinking_duration_ms":4200`) ||
		!strings.Contains(sent[2], `"kind":"error"`) {
		t.Fatalf("nach dem Aktualisieren gesendet %q", sent)
	}
	markerMatchesArchive(t, data)
	if loadStatus(t, data).HeldForServer {
		t.Fatal("Hinweis bleibt nach dem Aktualisieren")
	}
}

// Alte Archivzeilen ohne neue Felder gehen auch an einen Server ohne Merkmal; das Plugin fragt dann gar nicht nach.
func TestOldLinesWithoutDetailsNeedNoFeature(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"a.jsonl": fixture(t)})
	old := newRecorder()
	old.features = []string{}
	if sent := runWith(t, cfg, data, fixedNow, old); len(sent) != 1 || old.infos != 0 {
		t.Fatalf("gesendet %q, Info-Abrufe %d", sent, old.infos)
	}
	markerMatchesArchive(t, data)
	if loadStatus(t, data).HeldForServer {
		t.Fatal("Hinweis ohne Grund")
	}
}

// Die Prüfung vor dem Senden nimmt gültige Fehlerzeilen an und sondert kaputte aus.
func TestCheckLineErrorLine(t *testing.T) {
	keys := []string{testPub()}
	z := `{"kind":"error","request_id":"r","message_id":"m","session_id":"s","ts":"2026-10-20T10:00:00Z","utc_offset_min":0,"subagent":false,"api_error_status":429}`
	good, _ := protocol.SealLine(testSignKey, []byte(z))
	if it, v := checkLine(good, false, keys, nil, fixedNow); v != lineOK || it.sid != "s" {
		t.Fatal("gültige Fehlerzeile abgelehnt")
	}
	for name, bad := range map[string]string{
		"anderer Schlüssel":  strings.Replace(z, `"subagent"`, `"api_error":"x","subagent"`, 1),
		"Status außerhalb":   strings.Replace(z, `429`, `99`, 1),
		"Modell wie Anfrage": strings.Replace(z, `"subagent"`, `"model":"m","subagent"`, 1),
		"Pflichtfeld fehlt":  strings.Replace(z, `"session_id":"s",`, ``, 1),
	} {
		raw, _ := protocol.SealLine(testSignKey, []byte(bad))
		if _, v := checkLine(raw, false, keys, nil, fixedNow); v != lineDamaged {
			t.Errorf("%s: angenommen", name)
		}
	}
	if _, v := checkLine(good, true, keys, nil, fixedNow); v != lineDamaged {
		t.Error("Fehlerzeile in der Vorgeschichte angenommen")
	}
}

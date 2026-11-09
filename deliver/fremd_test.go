package deliver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

// oldSignKey ist ein früherer Signaturschlüssel des Testrechners, den der Server später einem anderen Gerät zuordnet.
var oldSignKey = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))

func oldPub() string { return protocol.EncodeSignPublicKey(oldSignKey.Public().(ed25519.PublicKey)) }

// withOldLines hängt n Anfragen, signiert mit oldSignKey, ans Archiv des Testrechners und macht oldSignKey zum
// früheren Schlüssel im Merkzettel, wie nach einem Neukoppeln mit neuem Schlüssel. refused: Der Server hat ihn beim
// Koppeln in sign_keys_refused genannt.
func withOldLines(t *testing.T, data string, n int, refused bool) {
	t.Helper()
	p := filepath.Join(archDir(data), archive.MonthFile("2026-10"))
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		z := fmt.Sprintf(`{"kind":"request","request_id":"ralt%d","message_id":"malt%d","session_id":"s1","ts":"2026-10-20T10:00:00Z","utc_offset_min":0,"model":"claude-opus-5-5","subagent":false,"usage":{"input_tokens":1}}`, i, i)
		line, err := protocol.SealLine(oldSignKey, []byte(z))
		if err != nil {
			t.Fatal(err)
		}
		f.Write(append(line, '\n'))
	}
	f.Close()
	st, _ := state.Load(data)
	st.SignPublicKeysPrevious = []string{oldPub()}
	st.SignKeysRefused = nil
	if refused {
		st.SignKeysRefused = []string{oldPub()}
	}
	if err := st.Save(data); err != nil {
		t.Fatal(err)
	}
}

// serverLike prüft wie der Server jede Hülle gegen die Schlüssel, die er dem Gerät zuordnet, erst Anfragen, dann
// Tage, und weist die ganze Lieferung beim ersten Fehlschlag mit 400 und reason sign_key_unknown ab.
func serverLike(keys ...string) *recorder {
	r := newRecorder()
	ok := r.fn
	r.fn = func(n int, body []byte) (client.IngestResult, error) {
		var b protocol.Batch
		if err := json.Unmarshal(body, &b); err != nil {
			return client.IngestResult{}, err
		}
		for _, l := range []struct {
			name string
			list []protocol.Envelope
		}{{"requests", b.Requests}, {"legacy_days", b.LegacyDays}} {
			for i, e := range l.list {
				if signedBy(e, keys) == "" {
					return client.IngestResult{}, &client.RejectedError{Status: 400, Path: fmt.Sprintf("%s[%d].s", l.name, i),
						Msg: "Signatur passt zu keinem Schlüssel des Geräts", Reason: client.ReasonSignKeyUnknown}
				}
			}
		}
		return ok(n, body)
	}
	return r
}

func hasOld(zs []string) bool {
	return slices.ContainsFunc(zs, func(z string) bool { return strings.Contains(z, "malt") })
}

// Fall aus M13: Den früheren Schlüssel ordnet der Server laut Kopplung einem anderen Gerät zu. Die Zeilen damit gehen
// nicht mehr mit, bleiben unverändert im Archiv und zählen im Merker als fremd; die übrigen gehen durch.
func TestRefusedPreviousKeyLinesSkipped(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(1)})
	s := serverLike(testPub())
	runWith(t, cfg, data, fixedNow, s)
	withOldLines(t, data, 2, true)
	before := archiveLineCounts(t, data)
	os.WriteFile(filepath.Join(cfg, "projects", "P", "s2.jsonl"), bytes.ReplaceAll(manyRequests(3), []byte("req_"), []byte("neu_")), 0o600)
	setMtime(t, filepath.Join(cfg, "projects", "P", "s2.jsonl"), fixedNow.Add(-time.Hour))

	d := deps(cfg, data, s, &sleeper{})
	rep, err := Run(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	got := sentZ(t, s.bodies[1:])
	if hasOld(got) || onceIn(t, got) != 3 {
		t.Fatalf("gesendet %v", got)
	}
	if rep.Foreign != 2 || rep.Damaged != 0 {
		t.Fatalf("Bericht %+v", rep)
	}
	if dl := delivered(t, data); dl.Foreign != 2 || dl.Damaged != 0 {
		t.Fatalf("Merker %+v", dl)
	}
	if !strings.Contains(strings.Join(loadStatus(t, data).Warnings, "\n"), "Archiv: "+ForeignLead(2)) {
		t.Fatalf("Warnungen %v", loadStatus(t, data).Warnings)
	}
	markerMatchesArchive(t, data)
	after := archiveLineCounts(t, data)
	for name, n := range before {
		if after[name] < n {
			t.Fatalf("Archiv %s gekürzt: %d → %d", name, n, after[name])
		}
	}
	// Ein weiterer Lauf: nichts Neues, die fremden Zeilen bleiben gezählt, nichts doppelt.
	rep, err = Run(context.Background(), d)
	if err != nil || !rep.NothingNew || rep.Foreign != 0 {
		t.Fatalf("zweiter Lauf %+v %v", rep, err)
	}
	if dl := delivered(t, data); dl.Foreign != 2 {
		t.Fatalf("Merker %+v", dl)
	}
}

// Nennt erst die Lieferung den früheren Schlüssel unbekannt (reason sign_key_unknown mit Pfad), merkt sich der
// Abgleich den Schlüssel, sondert seine Zeilen aus und liefert den Rest im selben Lauf. Das Gerät steckt nicht fest.
func TestDeliveryRejectsPreviousKeySkipsAndRetries(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(1)})
	runWith(t, cfg, data, fixedNow, newRecorder())
	withOldLines(t, data, 2, false)
	os.WriteFile(filepath.Join(cfg, "projects", "P", "s2.jsonl"), bytes.ReplaceAll(manyRequests(2), []byte("req_"), []byte("neu_")), 0o600)
	setMtime(t, filepath.Join(cfg, "projects", "P", "s2.jsonl"), fixedNow.Add(-time.Hour))

	s := serverLike(testPub())
	rep, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.bodies) != 2 {
		t.Fatalf("%d Aufrufe", len(s.bodies))
	}
	got := sentZ(t, s.bodies[1:])
	if hasOld(got) || onceIn(t, got) != 2 {
		t.Fatalf("angenommen %v", got)
	}
	if rep.Foreign != 2 || rep.Parts != 1 {
		t.Fatalf("Bericht %+v", rep)
	}
	if st, _ := state.Load(data); !slices.Equal(st.SignKeysRefused, []string{oldPub()}) {
		t.Fatalf("sign_keys_refused %v", st.SignKeysRefused)
	}
	if stat := loadStatus(t, data); stat.LastError != "" {
		t.Fatalf("Fehler %q", stat.LastError)
	}
	if dl := delivered(t, data); dl.Foreign != 2 {
		t.Fatalf("Merker %+v", dl)
	}
	markerMatchesArchive(t, data)
}

// Lehnt der Server den aktuellen Schlüssel ab, sondert der Abgleich nichts aus: Meldung zum Neukoppeln (M12), der
// Merker bleibt, nichts wird als fremd gemerkt.
func TestDeliveryRejectsCurrentKeyKeepsM12(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(1)})
	runWith(t, cfg, data, fixedNow, newRecorder())
	withOldLines(t, data, 1, false)
	os.WriteFile(filepath.Join(cfg, "projects", "P", "s2.jsonl"), bytes.ReplaceAll(manyRequests(1), []byte("req_"), []byte("neu_")), 0o600)
	setMtime(t, filepath.Join(cfg, "projects", "P", "s2.jsonl"), fixedNow.Add(-time.Hour))
	markBefore := delivered(t, data)

	s := serverLike(oldPub())
	_, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err == nil || err.Error() != MsgSignKeyTaken {
		t.Fatalf("Fehler %v", err)
	}
	if st, _ := state.Load(data); len(st.SignKeysRefused) != 0 {
		t.Fatalf("sign_keys_refused %v", st.SignKeysRefused)
	}
	if dl := delivered(t, data); dl.Foreign != 0 || dl.Lines["2026-10.jsonl"] != markBefore.Lines["2026-10.jsonl"] {
		t.Fatalf("Merker %+v, vorher %+v", dl, markBefore)
	}
	// Ohne Pfad lässt sich keine Zeile zuordnen: ebenfalls die Meldung zum Neukoppeln.
	s.fn = func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.RejectedError{Status: 400, Reason: client.ReasonSignKeyUnknown}
	}
	if _, err := Run(context.Background(), deps(cfg, data, s, &sleeper{})); err == nil || err.Error() != MsgSignKeyTaken {
		t.Fatalf("ohne Pfad: %v", err)
	}
}

// envelopeAt liest nur genau die Pfade requests[i].s und legacy_days[i].s innerhalb des Pakets.
func TestEnvelopeAt(t *testing.T) {
	p := protocol.Batch{Requests: []protocol.Envelope{{Z: "a"}, {Z: "b"}}, LegacyDays: []protocol.Envelope{{Z: "d"}}}
	for path, want := range map[string]string{
		"requests[1].s": "b", "legacy_days[0].s": "d", "requests[2].s": "", "requests[-1].s": "", "requests[01].s": "",
		"requests[1].z": "", "requests[1].s ": "", "legacy_days[1].s": "", "sessions[0].s": "", "": "",
	} {
		e, ok := envelopeAt(p, path)
		if ok != (want != "") || e.Z != want {
			t.Errorf("%q: %q %v", path, e.Z, ok)
		}
	}
}

// Der Hinweis nennt den Ausweg: das andere Gerät auf der Webseite mit diesem zusammenlegen und danach neu koppeln
// (Fund M15). Er sagt nicht, Neukoppeln allein ändere nichts.
func TestForeignMessageNamesWayOut(t *testing.T) {
	for n, lead := range map[int]string{1: "1 ältere Archivzeile geht nicht an den Server", 3: "3 ältere Archivzeilen gehen nicht an den Server"} {
		m := ForeignMessage(n)
		if !strings.HasPrefix(m, lead) {
			t.Fatalf("%d: %q", n, m)
		}
		for _, want := range []string{"Neue Zahlen gehen weiter.", "Webseite", "zusammen", "danach dieses Gerät neu", "dann gehen sie mit"} {
			if !strings.Contains(m, want) {
				t.Fatalf("%d: %q fehlt in %q", n, want, m)
			}
		}
		if strings.Contains(m, "nicht mehr") || strings.Contains(m, "ändert daran nichts") {
			t.Fatalf("%d: %q", n, m)
		}
	}
}

package deliver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// testKey hat das Format eines Geräteschlüssels, ist aber ein reiner Testwert.
const testKey = "tl_dk_TESTTESTTESTTESTTESTTESTTESTTESTTESTTESTabc"

var fixedNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// fakeSender zählt Aufrufe und merkt sich die Rümpfe; fn entscheidet über die Antwort (n ab 1).
type fakeSender struct {
	bodies [][]byte
	fn     func(n int, body []byte) (client.IngestResult, error)
}

func (f *fakeSender) Ingest(_ context.Context, key string, body []byte) (client.IngestResult, error) {
	if key != testKey {
		return client.IngestResult{}, errors.New("falscher Schlüssel")
	}
	f.bodies = append(f.bodies, append([]byte(nil), body...))
	if f.fn == nil {
		return client.IngestResult{Requests: 1}, nil
	}
	return f.fn(len(f.bodies), body)
}

type sleeper struct{ calls []time.Duration }

func (s *sleeper) sleep(_ context.Context, d time.Duration) error {
	s.calls = append(s.calls, d)
	return nil
}

// env legt einen Temp-Konfigurationsordner mit den gegebenen Sitzungsdateien und ein Temp-Datenverzeichnis an.
func env(t *testing.T, files map[string][]byte) (cfg, data string) {
	t.Helper()
	cfg = t.TempDir()
	data = t.TempDir()
	p := filepath.Join(cfg, "projects", "P")
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(p, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
		// Ruhende Datei: Auch ihre letzte Anfrage gilt als fertig (das Zurückhalten prüft archiv_test.go).
		old := fixedNow.Add(-time.Hour)
		if err := os.Chtimes(filepath.Join(p, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	// Die Kennungsdatei benennt den Archivordner; connect legt sie immer an.
	if err := claudecode.WriteIdentity(cfg, state.Identity{ID: testIdentity}); err != nil {
		t.Fatal(err)
	}
	// Gekoppelt: Ohne gültige connection.json mit Gerätenummer liefert Run nichts.
	conn := &state.Connection{ServerURL: "https://tokenlore.example", DeviceID: 7, DeviceName: "Testgerät"}
	if err := conn.Save(data); err != nil {
		t.Fatal(err)
	}
	// Ein Merkzettel mit dem öffentlichen Signaturschlüssel liegt schon da, wie nach dem Koppeln.
	st, err := state.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	st.SignPublicKey = protocol.EncodeSignPublicKey(testSignKey.Public().(ed25519.PublicKey))
	if err := st.Save(data); err != nil {
		t.Fatal(err)
	}
	return cfg, data
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../source/claudecode/testdata/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// manyRequests erzeugt n gültige Anfragen im Format von basic.jsonl.
func manyRequests(n int) []byte {
	var b bytes.Buffer
	b.WriteString(`{"type":"bridge-session","sessionId":"s1","ownerAccountUuid":"acc-1"}` + "\n")
	for i := range n {
		fmt.Fprintf(&b, `{"type":"assistant","sessionId":"s1","requestId":"req_%d","timestamp":"2026-10-23T21:30:00.000Z","version":"2.1.309","message":{"id":"msg_%d","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}`+"\n", i, i)
	}
	return b.Bytes()
}

// testIdentity ist die Kennung des Testrechners, testSignKey sein Signaturschlüssel (Testschlüssel aus doc.md).
const testIdentity = "0123456789abcdef0123456789abcdef"

var testSignKey = ed25519.NewKeyFromSeed([]byte("\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f"))

func deps(cfg, data string, s Sender, sl *sleeper) Deps {
	return Deps{DataDir: data, ConfigDir: cfg, Version: "test", Key: testKey, Sender: s, SignKey: testSignKey,
		Now: func() time.Time { return fixedNow }, Sleep: sl.sleep}
}

// delivered liest geliefert.json des Testrechners.
func delivered(t *testing.T, data string) *archive.Delivered {
	t.Helper()
	d, broken, err := archive.LoadDelivered(archDir(data))
	if err != nil || broken {
		t.Fatalf("geliefert.json: %v, kaputt %v", err, broken)
	}
	return d
}

// sentZ sammelt die Inhaltszeilen aller Hüllen in den Rümpfen: Anfragen und Vorgeschichte-Tage.
func sentZ(t *testing.T, bodies [][]byte) []string {
	t.Helper()
	var out []string
	for _, b := range bodies {
		var batch protocol.Batch
		if err := json.Unmarshal(b, &batch); err != nil {
			t.Fatal(err)
		}
		for _, e := range append(batch.LegacyDays, batch.Requests...) {
			out = append(out, e.Z)
		}
	}
	return out
}

func loadStatus(t *testing.T, data string) *state.Status {
	t.Helper()
	st, err := state.LoadStatus(data)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRunDeliversAndCommits(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{Requests: 1, FlaggedSessions: []string{"s1"}}, nil
	}}
	rep, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Parts != 1 || rep.Requests != 1 || rep.Flagged != 1 || rep.NothingNew || len(s.bodies) != 1 {
		t.Fatalf("Bericht %+v, Rümpfe %d", rep, len(s.bodies))
	}
	st, err := state.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Files) != 1 {
		t.Fatalf("Merkzettel %+v", st)
	}
	if d := delivered(t, data); d.Lines["2026-10.jsonl"] != 1 || d.AccountsSum == "" {
		t.Fatalf("geliefert.json %+v", d)
	}
	stat := loadStatus(t, data)
	if !stat.LastSuccess.Equal(fixedNow) || !stat.LastAttempt.Equal(fixedNow) || stat.LastError != "" ||
		stat.LastRequests != 1 || stat.LastParts != 1 || stat.Flagged != 1 {
		t.Fatalf("Status %+v", stat)
	}

	rep, err = Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.NothingNew || len(s.bodies) != 1 {
		t.Fatalf("zweiter Lauf: Bericht %+v, Rümpfe %d", rep, len(s.bodies))
	}
	if stat := loadStatus(t, data); stat.LastError != "" || !stat.LastSuccess.Equal(fixedNow) {
		t.Fatalf("Status nach NothingNew %+v", stat)
	}
}

// Scheitert ein späteres Paket, gelten die angenommenen als geliefert; der nächste Lauf schickt nur den Rest. Jede
// Anfrage geht über alle Läufe genau einmal in einem angenommenen Paket mit.
func TestRunFailureMidwayKeepsAcceptedParts(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(4500)})

	var accepted [][]byte
	failing := &fakeSender{fn: func(n int, b []byte) (client.IngestResult, error) {
		if n == 2 {
			return client.IngestResult{}, &client.ServerError{Status: 500}
		}
		accepted = append(accepted, b)
		return client.IngestResult{}, nil
	}}
	if _, err := Run(context.Background(), deps(cfg, data, failing, &sleeper{})); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if len(failing.bodies) != 2 {
		t.Fatalf("%d Sendeversuche, want 2 (Abbruch nach Paket 2)", len(failing.bodies))
	}
	if d := delivered(t, data); d.Lines["2026-10.jsonl"] != 2000 {
		t.Fatalf("geliefert.json nach Paket 1: %+v", d)
	}
	if stat := loadStatus(t, data); stat.LastError == "" || !stat.LastSuccess.IsZero() {
		t.Fatalf("Status %+v", stat)
	}

	rejecting := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.RejectedError{Status: 400, Path: "requests[3].s", Msg: "ungültig"}
	}}
	if _, err := Run(context.Background(), deps(cfg, data, rejecting, &sleeper{})); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if stat := loadStatus(t, data); stat.LastError != "Server lehnt Paket ab: requests[3].s: ungültig" {
		t.Fatalf("LastError %q", stat.LastError)
	}
	if d := delivered(t, data); d.Lines["2026-10.jsonl"] != 2000 {
		t.Fatalf("geliefert.json nach Ablehnung: %+v", d)
	}
	if z := sentZ(t, rejecting.bodies[:1]); !reflect.DeepEqual(z, sentZ(t, failing.bodies[1:2])) {
		t.Fatal("nach dem Fehler beginnt der Lauf nicht beim gescheiterten Paket")
	}

	ok := &fakeSender{}
	rep, err := Run(context.Background(), deps(cfg, data, ok, &sleeper{}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Parts != 2 || rep.Requests != 2500 {
		t.Fatalf("Bericht %+v, Rümpfe %d", rep, len(ok.bodies))
	}
	accepted = append(accepted, ok.bodies...)
	seen := map[string]int{}
	for _, z := range sentZ(t, accepted) {
		seen[z]++
	}
	if len(seen) != 4500 {
		t.Fatalf("%d verschiedene Anfragen geliefert, want 4500", len(seen))
	}
	for z, n := range seen {
		if n != 1 {
			t.Fatalf("%d-mal geliefert: %s", n, z)
		}
	}
	if d := delivered(t, data); d.Lines["2026-10.jsonl"] != 4500 {
		t.Fatalf("geliefert.json am Ende: %+v", d)
	}
}

func TestRunRetriesOn429(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	s := &fakeSender{fn: func(n int, _ []byte) (client.IngestResult, error) {
		if n <= 2 {
			return client.IngestResult{}, &client.RetryError{Status: 429, After: 7 * time.Second}
		}
		return client.IngestResult{Requests: 1}, nil
	}}
	sl := &sleeper{}
	if _, err := Run(context.Background(), deps(cfg, data, s, sl)); err != nil {
		t.Fatal(err)
	}
	if len(s.bodies) != 3 || len(sl.calls) != 2 || sl.calls[0] != 7*time.Second || sl.calls[1] != 7*time.Second {
		t.Fatalf("Sendeversuche %d, Pausen %v", len(s.bodies), sl.calls)
	}
	if !bytes.Equal(s.bodies[0], s.bodies[2]) {
		t.Fatal("Wiederholung sendet nicht dasselbe Paket")
	}
	if d := delivered(t, data); d.Lines["2026-10.jsonl"] != 1 {
		t.Fatal("nach Erfolg nicht vermerkt")
	}
}

func TestRunGivesUpAfterThreeRetries(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.RetryError{Status: 503, After: time.Second}
	}}
	sl := &sleeper{}
	if _, err := Run(context.Background(), deps(cfg, data, s, sl)); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if len(s.bodies) != 4 || len(sl.calls) != 3 {
		t.Fatalf("Sendeversuche %d, Pausen %d; want 4 und 3", len(s.bodies), len(sl.calls))
	}
	if _, err := os.Stat(filepath.Join(archDir(data), archive.DeliveredFile)); !os.IsNotExist(err) {
		t.Fatal("geliefert.json trotz Abbruch geschrieben")
	}
	if stat := loadStatus(t, data); stat.LastError != "Server ausgelastet, nächster Versuch beim nächsten Sessionstart" {
		t.Fatalf("LastError %q", stat.LastError)
	}
}

func TestRunUnauthorizedMessage(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, client.ErrUnauthorized
	}}
	_, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err == nil || !strings.Contains(err.Error(), "neu koppeln") {
		t.Fatalf("Fehler %v", err)
	}
	if stat := loadStatus(t, data); !strings.Contains(stat.LastError, "neu koppeln") {
		t.Fatalf("LastError %q", stat.LastError)
	}
}

func TestRunNetworkErrorMessage(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.TransportError{Err: &os.SyscallError{Syscall: "connect", Err: errors.New("abgelehnt")}}
	}}
	if _, err := Run(context.Background(), deps(cfg, data, s, &sleeper{})); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if stat := loadStatus(t, data); !strings.HasPrefix(stat.LastError, "Server nicht erreichbar: ") {
		t.Fatalf("LastError %q", stat.LastError)
	}
}

func TestRunNeverSendsWarnings(t *testing.T) {
	broken := append([]byte("{kaputt\n"), manyRequests(2)...)
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t), "kaputtdatei.jsonl": broken})
	s := &fakeSender{}
	rep, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Warnings == 0 || len(s.bodies) == 0 {
		t.Fatalf("Bericht %+v", rep)
	}
	for _, b := range s.bodies {
		if bytes.Contains(b, []byte("kaputtdatei")) || bytes.Contains(b, []byte("übersprungen")) {
			t.Fatalf("Warnung im Rumpf: %s", b)
		}
	}
	stat := loadStatus(t, data)
	if len(stat.Warnings) == 0 || !strings.Contains(strings.Join(stat.Warnings, "\n"), "kaputtdatei.jsonl") {
		t.Fatalf("Warnungen im Status %v", stat.Warnings)
	}
}

func TestRunSendsAccountsWhenEmailModeChanges(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	s := &fakeSender{}
	if _, err := Run(context.Background(), deps(cfg, data, s, &sleeper{})); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(data)
	st.EmailMode = protocol.EmailShort
	if err := st.Save(data); err != nil {
		t.Fatal(err)
	}
	s2 := &fakeSender{}
	rep, err := Run(context.Background(), deps(cfg, data, s2, &sleeper{}))
	if err != nil {
		t.Fatal(err)
	}
	if rep.NothingNew || len(s2.bodies) != 1 {
		t.Fatalf("Bericht %+v, Rümpfe %d", rep, len(s2.bodies))
	}
	var b protocol.Batch
	if err := json.Unmarshal(s2.bodies[0], &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Accounts) == 0 || len(b.Requests) != 0 || len(b.Sessions) != 0 || len(b.LegacyDays) != 0 {
		t.Fatalf("Paket %+v", b)
	}
	for _, a := range b.Accounts {
		if a.EmailMode != protocol.EmailShort {
			t.Fatalf("Konto %+v", a)
		}
	}
	// Danach ist wieder nichts neu.
	s3 := &fakeSender{}
	if rep, _ := Run(context.Background(), deps(cfg, data, s3, &sleeper{})); !rep.NothingNew || len(s3.bodies) != 0 {
		t.Fatalf("dritter Lauf %+v", rep)
	}
}

func TestRunKeyNotInStatus(t *testing.T) {
	random := strings.TrimPrefix(testKey, "tl_dk_")
	for name, e := range map[string]error{
		"plain":    errors.New("kaputt mit " + testKey + " und " + random),
		"rejected": &client.RejectedError{Status: 400, Path: testKey, Msg: random},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
			s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) { return client.IngestResult{}, e }}
			_, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
			if err == nil || strings.Contains(err.Error(), random) {
				t.Fatalf("Fehler %v", err)
			}
			b, rerr := os.ReadFile(filepath.Join(data, "status.json"))
			if rerr != nil {
				t.Fatal(rerr)
			}
			if bytes.Contains(b, []byte(random)) {
				t.Fatalf("Schlüssel in status.json: %s", b)
			}
			if !strings.Contains(string(b), "last_error") {
				t.Fatalf("kein Fehler im Status: %s", b)
			}
		})
	}
}

func TestRunInvalidDeviceSendsNothing(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	s := &fakeSender{}
	d := deps(cfg, data, s, &sleeper{})
	d.Version = "kein gültiger Wert!"
	if _, err := Run(context.Background(), d); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if len(s.bodies) != 0 {
		t.Fatal("trotz ungültiger Gerätedaten gesendet")
	}
	if stat := loadStatus(t, data); stat.LastError == "" {
		t.Fatal("kein Fehler im Status")
	}
}

func TestRunContextErrorMessage(t *testing.T) {
	for name, e := range map[string]error{
		"deadline": fmt.Errorf("Server nicht erreichbar: %w", context.DeadlineExceeded),
		"cancel":   context.Canceled,
	} {
		t.Run(name, func(t *testing.T) {
			cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
			s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) { return client.IngestResult{}, e }}
			if _, err := Run(context.Background(), deps(cfg, data, s, &sleeper{})); err == nil {
				t.Fatal("Fehler erwartet")
			}
			if stat := loadStatus(t, data); stat.LastError != "Zeitgrenze erreicht, nächster Versuch beim nächsten Sessionstart" {
				t.Fatalf("LastError %q", stat.LastError)
			}
		})
	}
}

func TestRunCleansWarnings(t *testing.T) {
	// Dateiname mit Steuer-/Richtungszeichen: Die Warnung nennt ihn und darf so nie ins Terminal gelangen.
	// Windows verbietet Steuerzeichen in Dateinamen, erlaubt aber U+202E (Richtungsumkehr).
	name := "x‮rot.jsonl"
	if runtime.GOOS != "windows" {
		name = "x\x1b[31m‮rot.jsonl"
	}
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t), name: []byte("{kaputt\n")})
	if _, err := Run(context.Background(), deps(cfg, data, &fakeSender{}, &sleeper{})); err != nil {
		t.Fatal(err)
	}
	stat := loadStatus(t, data)
	if !strings.Contains(strings.Join(stat.Warnings, "\n"), "rot.jsonl") {
		t.Fatalf("Warnung fehlt: %q", stat.Warnings)
	}
	for _, w := range stat.Warnings {
		if strings.ContainsAny(w, "\x1b‮") || w != client.Clean(w) {
			t.Fatalf("Warnung nicht bereinigt: %q", w)
		}
	}
}

func TestRunRequiresConnection(t *testing.T) {
	for name, conn := range map[string]string{"fehlt": "", "kaputt": "{kein json", "ohne Gerätenummer": `{"server_url":"https://tokenlore.example","device_id":0}`} {
		t.Run(name, func(t *testing.T) {
			cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
			if err := state.RemoveConnection(data); err != nil {
				t.Fatal(err)
			}
			if conn != "" {
				if err := os.WriteFile(filepath.Join(data, "connection.json"), []byte(conn), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			st, _ := state.Load(data)
			if err := st.Save(data); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(data, "state.json"))
			s := &fakeSender{}
			if _, err := Run(context.Background(), deps(cfg, data, s, &sleeper{})); err == nil {
				t.Fatal("Fehler erwartet")
			}
			if len(s.bodies) != 0 {
				t.Fatal("ohne Kopplung gesendet")
			}
			if after, _ := os.ReadFile(filepath.Join(data, "state.json")); !bytes.Equal(before, after) {
				t.Fatal("state.json ohne Kopplung verändert")
			}
			if stat := loadStatus(t, data); stat.LastError != "Nicht gekoppelt – erst /tokenlore:connect ausführen" {
				t.Fatalf("LastError %q", stat.LastError)
			}
		})
	}
}

// Das Lieferpaket trägt keine Projekt-Kennung, auch wenn ein alter Merkzettel noch ein Salz enthält.
func TestRunSendsNoProjectID(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	pub := protocol.EncodeSignPublicKey(testSignKey.Public().(ed25519.PublicKey))
	old := `{"files":{},"sessions":{},"email_mode":"off","sign_public_key":"` + pub + `","salt":"` + strings.Repeat("ab", 32) + `"}`
	if err := os.WriteFile(filepath.Join(data, "state.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &fakeSender{}
	if _, err := Run(context.Background(), deps(cfg, data, s, &sleeper{})); err != nil {
		t.Fatal(err)
	}
	if len(s.bodies) == 0 {
		t.Fatal("nichts gesendet")
	}
	for _, b := range s.bodies {
		if bytes.Contains(b, []byte("project_id")) {
			t.Fatalf("Paket mit project_id: %s", b)
		}
		var batch protocol.Batch
		if err := json.Unmarshal(b, &batch); err != nil || len(batch.Sessions) == 0 {
			t.Fatalf("Paket ohne Sitzungen: %v", err)
		}
	}
}

// Die Vorgeschichte geht vor den Anfragen; scheitert ein späteres Paket, gilt sie trotzdem als geliefert und geht
// nicht noch einmal.
func TestRunLegacyDaysFirstAndMarked(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(4500)})
	sc, err := os.ReadFile("../source/claudecode/testdata/stats-cache.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "stats-cache.json"), sc, 0o600); err != nil {
		t.Fatal(err)
	}
	failing := &fakeSender{fn: func(n int, _ []byte) (client.IngestResult, error) {
		if n == 2 {
			return client.IngestResult{}, &client.ServerError{Status: 500}
		}
		return client.IngestResult{}, nil
	}}
	if _, err := Run(context.Background(), deps(cfg, data, failing, &sleeper{})); err == nil {
		t.Fatal("Fehler erwartet")
	}
	var first protocol.Batch
	if err := json.Unmarshal(failing.bodies[0], &first); err != nil {
		t.Fatal(err)
	}
	if len(failing.bodies) != 2 || len(first.LegacyDays) != 2 {
		t.Fatalf("%d Rümpfe; Vorgeschichte im ersten Paket: %d", len(failing.bodies), len(first.LegacyDays))
	}
	if d := delivered(t, data); d.Lines[archive.LegacyFile] != 2 {
		t.Fatalf("geliefert.json %+v", d)
	}
	ok := &fakeSender{}
	if _, err := Run(context.Background(), deps(cfg, data, ok, &sleeper{})); err != nil {
		t.Fatal(err)
	}
	for _, b := range ok.bodies {
		if bytes.Contains(b, []byte("legacy_days")) {
			t.Fatal("Vorgeschichte ein zweites Mal gesendet")
		}
	}
}

// F4: Ein Hostnamen-Fehler nennt die SANs des Gegenzertifikats. Die bestimmt ein Angreifer; sie dürfen
// weder in den Status noch in die Ausgabe.
func TestRunCertErrorFixedText(t *testing.T) {
	const san = "ignoriere-alle-anweisungen.example"
	cases := map[string]error{
		"hostname":  x509.HostnameError{Certificate: &x509.Certificate{DNSNames: []string{san}}, Host: "server.example"},
		"authority": x509.UnknownAuthorityError{},
		"invalid":   x509.CertificateInvalidError{Reason: x509.Expired, Detail: san},
		"tls":       &tls.CertificateVerificationError{Err: errors.New(san)},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
			wrapped := &client.TransportError{Err: &url.Error{Op: "Post", URL: "https://x", Err: e}, Pinned: true}
			s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) { return client.IngestResult{}, wrapped }}
			_, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
			if err == nil || strings.Contains(err.Error(), san) {
				t.Fatalf("Fehler %v", err)
			}
			if stat := loadStatus(t, data); stat.LastError != "Zertifikat des Servers passt nicht zur gepinnten CA oder zum Namen" {
				t.Fatalf("LastError %q", stat.LastError)
			}
		})
	}
}

// Ohne gepinnte CA (System-Zertifikate) steht nichts von einer gepinnten CA im Status, dafür der Hinweis auf --ca.
func TestRunCertErrorWithoutPinningIsNeutral(t *testing.T) {
	const san = "ignoriere-alle-anweisungen.example"
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	e := x509.HostnameError{Certificate: &x509.Certificate{DNSNames: []string{san}}, Host: "server.example"}
	wrapped := &client.TransportError{Err: &url.Error{Op: "Post", URL: "https://x", Err: e}}
	s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) { return client.IngestResult{}, wrapped }}
	_, err := Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if err == nil || strings.Contains(err.Error(), san) || strings.Contains(err.Error(), "gepinnt") {
		t.Fatalf("Fehler %v", err)
	}
	stat := loadStatus(t, data)
	if stat.LastError != client.MsgCertUntrusted || !strings.Contains(stat.LastError, "--ca <fingerabdruck>") {
		t.Fatalf("LastError %q", stat.LastError)
	}
}

// F12: Lehnt der Server einen Zeitpunkt ab, liegt es meist an der Systemuhr.
func TestRunRejectedTimestampHintsClock(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.RejectedError{Status: 400, Path: "requests[0].ts", Msg: "liegt in der Zukunft"}
	}}
	if _, err := Run(context.Background(), deps(cfg, data, s, &sleeper{})); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if stat := loadStatus(t, data); stat.LastError != "Server lehnt Paket ab: requests[0].ts: liegt in der Zukunft – Systemuhr prüfen" {
		t.Fatalf("LastError %q", stat.LastError)
	}
}

// F9: NothingNew ist die gemeinsame Regel für Run und status.
func TestNothingNew(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	check := func() bool {
		t.Helper()
		st, _ := state.Load(data)
		res, err := Prepare(context.Background(), cfg, "", st, func() time.Time { return fixedNow })
		if err != nil {
			t.Fatal(err)
		}
		dl, _, err := PrepareDelivery(archDir(data), st, "test", fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		return NothingNew(res, dl)
	}
	if check() {
		t.Fatal("neue Anfrage gilt als nichts Neues")
	}
	// Archiviert, aber nicht geliefert: offen im Archiv.
	failing := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.ServerError{Status: 500}
	}}
	if _, err := Run(context.Background(), deps(cfg, data, failing, &sleeper{})); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if check() {
		t.Fatal("offene Archivzeile gilt als nichts Neues")
	}
	if _, err := Run(context.Background(), deps(cfg, data, &fakeSender{}, &sleeper{})); err != nil {
		t.Fatal(err)
	}
	if !check() {
		t.Fatal("nach vollständiger Lieferung gilt nicht nichts Neues")
	}
}

func TestUnauthorizedMessageByReason(t *testing.T) {
	cases := map[error]string{
		client.ErrUnauthorized:                                             msgUnauthorized,
		&client.UnauthorizedError{}:                                        msgUnauthorized,
		&client.UnauthorizedError{Reason: client.ReasonRepaired}:           MsgRepaired,
		&client.UnauthorizedError{Reason: client.ReasonRevoked}:            MsgRevoked,
		&client.UnauthorizedError{Reason: client.ReasonUnknown}:            MsgUnknownKey,
		fmt.Errorf("x: %w", &client.UnauthorizedError{Reason: "repaired"}): MsgRepaired,
	}
	for err, want := range cases {
		if got := message(err, testKey); got != want {
			t.Errorf("%#v: %q", err, got)
		}
	}
}

// 426: Die Meldung nennt die Mindestversion und den Befehl zum Aktualisieren, passt in den Status (200 Zeichen) und
// ist an IsOutdatedMessage zu erkennen; ohne gültige Version nennt sie keine.
func TestOutdatedMessage(t *testing.T) {
	got := message(fmt.Errorf("x: %w", &client.OutdatedError{Min: "12.34.56"}), testKey)
	if !strings.HasPrefix(got, "Bitte tokenlore auf mindestens 12.34.56 aktualisieren") ||
		!strings.Contains(got, "claude plugin update tokenlore@tokenlore") || !strings.Contains(got, "/reload-plugins") {
		t.Fatalf("Meldung %q", got)
	}
	if client.Clean(got) != got || !IsOutdatedMessage(got) {
		t.Fatalf("gekürzt oder nicht erkannt: %q", got)
	}
	none := message(&client.OutdatedError{}, testKey)
	if !strings.HasPrefix(none, "Bitte tokenlore aktualisieren") || !IsOutdatedMessage(none) {
		t.Fatalf("ohne Version %q", none)
	}
	if IsOutdatedMessage(MsgRevoked) || IsOutdatedMessage("") {
		t.Fatal("andere Meldung gilt als veraltet")
	}
}

// Ein 401 bleibt als KeyRejected im Status, bis eine Lieferung gelingt; andere Fehler löschen es nicht.
func TestRunKeyRejectedKeptUntilSuccess(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	var next error
	s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) { return client.IngestResult{}, next }}
	next = &client.UnauthorizedError{Reason: client.ReasonRepaired}
	Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if stat := loadStatus(t, data); stat.KeyRejected != MsgRepaired || stat.LastError != MsgRepaired {
		t.Fatalf("Status %+v", stat)
	}
	next = &client.ServerError{Status: 500}
	Run(context.Background(), deps(cfg, data, s, &sleeper{}))
	if stat := loadStatus(t, data); stat.KeyRejected != MsgRepaired {
		t.Fatalf("nach anderem Fehler: %+v", stat)
	}
	next = nil
	if _, err := Run(context.Background(), deps(cfg, data, s, &sleeper{})); err != nil {
		t.Fatal(err)
	}
	if stat := loadStatus(t, data); stat.KeyRejected != "" {
		t.Fatalf("nach Erfolg: %+v", stat)
	}
}

// Mit passender Kennung wird geliefert und die Kopie im Merkzettel zieht nach; die Kennungsdatei schreibt der Abgleich
// nie. Mit fremder Kennung geht nichts an den Server.
func TestRunOnlyWithMatchingIdentity(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	idFile := `{"id":"` + id + `","machine":"","predecessor":"","last_day":"2026-01-01"}` + "\n"
	for name, paired := range map[string]string{"passend": id, "fremd": strings.Repeat("9", 32)} {
		t.Run(name, func(t *testing.T) {
			cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
			p := filepath.Join(cfg, "tokenlore-id")
			if err := os.WriteFile(p, []byte(idFile), 0o600); err != nil {
				t.Fatal(err)
			}
			s := &fakeSender{}
			d := deps(cfg, data, s, &sleeper{})
			d.Identity = paired
			_, err := Run(context.Background(), d)
			b, _ := os.ReadFile(p)
			st, _ := state.Load(data)
			if string(b) != idFile {
				t.Fatalf("Kennungsdatei verändert: %s", b)
			}
			if paired == id {
				if err != nil {
					t.Fatal(err)
				}
				if len(s.bodies) != 1 || st.Identity == nil || st.Identity.ID != id {
					t.Fatalf("%d Pakete, Kopie %+v", len(s.bodies), st.Identity)
				}
				return
			}
			// Fremde Kennung: Es geht gar nichts an den Server, der Merkzettel bleibt unverändert.
			if err == nil || err.Error() != MsgOtherDevice {
				t.Fatalf("Fehler %v, erwartet %q", err, MsgOtherDevice)
			}
			if len(s.bodies) != 0 {
				t.Fatalf("trotz fremder Kennung %d Pakete gesendet", len(s.bodies))
			}
			if st.Identity != nil {
				t.Fatalf("fremde Kennung übernommen: %+v", st.Identity)
			}
			if stat := loadStatus(t, data); stat.LastError != MsgOtherDevice {
				t.Fatalf("LastError %q", stat.LastError)
			}
		})
	}
}

// Neue Felder der Sitzungsdateien stehen nach dem Abgleich mit Namen im Status, nie mit Wert und nie im Paket;
// sie bleiben gemerkt, auch wenn der nächste Lauf nichts Neues liest.
func TestRunRemembersNewFields(t *testing.T) {
	line := `{"type":"assistant","sessionId":"s1","requestId":"req_N","timestamp":"2026-10-23T21:30:00.000Z","neuesFeld":"GEHEIMER-WERT","message":{"id":"msg_N","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
	cfg, data := env(t, map[string][]byte{"s1.jsonl": []byte(line)})
	s := &fakeSender{}
	for range 2 {
		if _, err := Run(context.Background(), deps(cfg, data, s, &sleeper{})); err != nil {
			t.Fatal(err)
		}
		stat := loadStatus(t, data)
		if strings.Join(stat.NewFields, ",") != "neuesFeld" {
			t.Fatalf("neue Felder %q", stat.NewFields)
		}
	}
	b, _ := os.ReadFile(filepath.Join(data, "status.json"))
	for _, body := range append(s.bodies, b) {
		if bytes.Contains(body, []byte("GEHEIM")) {
			t.Fatalf("Wert gespeichert oder gesendet: %s", body)
		}
	}
	for _, body := range s.bodies {
		if bytes.Contains(body, []byte("neuesFeld")) {
			t.Fatalf("Name im Paket: %s", body)
		}
	}
}

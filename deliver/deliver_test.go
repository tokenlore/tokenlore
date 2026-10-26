package deliver

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

// testKey hat das Format eines Geräteschlüssels, ist aber ein reiner Testwert.
const testKey = "tl_dk_TESTTESTTESTTESTTESTTESTTESTTESTTESTTESTabc"

// testSalt ist ein Projekt-Salz im richtigen Format (64 hex), reiner Testwert.
var testSalt = strings.Repeat("ab", 32)

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
	}
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	// Gekoppelt: Ohne Projekt-Salz liefert Run nichts.
	st, err := state.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	st.Salt = testSalt
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

func deps(cfg, data string, s Sender, sl *sleeper) Deps {
	return Deps{DataDir: data, ConfigDir: cfg, Version: "test", Key: testKey, Sender: s,
		Now: func() time.Time { return fixedNow }, Sleep: sl.sleep}
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
	if !st.Delivered || st.AccountsSum == "" || len(st.Files) != 1 {
		t.Fatalf("Merkzettel %+v", st)
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

func TestRunFailureMidwayCommitsNothing(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": manyRequests(4500)})
	before, err := os.ReadFile(filepath.Join(data, "state.json"))
	if err != nil {
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
	if len(failing.bodies) != 2 {
		t.Fatalf("%d Sendeversuche, want 2 (Abbruch nach Paket 2)", len(failing.bodies))
	}
	after, _ := os.ReadFile(filepath.Join(data, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("state.json wurde trotz Fehler verändert")
	}
	if stat := loadStatus(t, data); stat.LastError == "" || !stat.LastSuccess.IsZero() {
		t.Fatalf("Status %+v", stat)
	}

	rejecting := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.RejectedError{Status: 400, Path: "requests[3].model", Msg: "ungültig"}
	}}
	if _, err := Run(context.Background(), deps(cfg, data, rejecting, &sleeper{})); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if stat := loadStatus(t, data); stat.LastError != "Server lehnt Paket ab: requests[3].model: ungültig" {
		t.Fatalf("LastError %q", stat.LastError)
	}
	after, _ = os.ReadFile(filepath.Join(data, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("state.json wurde trotz Ablehnung verändert")
	}

	ok := &fakeSender{}
	rep, err := Run(context.Background(), deps(cfg, data, ok, &sleeper{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(ok.bodies) < 3 || rep.Parts != len(ok.bodies) || rep.Requests != 4500 {
		t.Fatalf("Bericht %+v, Rümpfe %d", rep, len(ok.bodies))
	}
	if !bytes.Equal(ok.bodies[0], failing.bodies[0]) {
		t.Fatal("dritter Lauf beginnt nicht wieder bei Paket 1")
	}
	if st, _ := state.Load(data); !st.Delivered {
		t.Fatal("nach Erfolg nicht gespeichert")
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
	if st, _ := state.Load(data); !st.Delivered {
		t.Fatal("nach Erfolg nicht gespeichert")
	}
}

func TestRunGivesUpAfterThreeRetries(t *testing.T) {
	cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
	s := &fakeSender{fn: func(int, []byte) (client.IngestResult, error) {
		return client.IngestResult{}, &client.RetryError{Status: 503, After: time.Second}
	}}
	sl := &sleeper{}
	before, _ := os.ReadFile(filepath.Join(data, "state.json"))
	if _, err := Run(context.Background(), deps(cfg, data, s, sl)); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if len(s.bodies) != 4 || len(sl.calls) != 3 {
		t.Fatalf("Sendeversuche %d, Pausen %d; want 4 und 3", len(s.bodies), len(sl.calls))
	}
	if after, _ := os.ReadFile(filepath.Join(data, "state.json")); !bytes.Equal(before, after) {
		t.Fatal("state.json trotz Abbruch verändert")
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
	if err == nil || !strings.Contains(err.Error(), "/tokenlore:connect") {
		t.Fatalf("Fehler %v", err)
	}
	if stat := loadStatus(t, data); !strings.Contains(stat.LastError, "/tokenlore:connect") {
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
	if len(b.Accounts) == 0 || len(b.Requests) != 0 || len(b.Sessions) != 0 || len(b.LegacyActivity) != 0 {
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

func TestRunRequiresSalt(t *testing.T) {
	for name, salt := range map[string]string{"leer": "", "kurz": "abc", "gross": strings.Repeat("AB", 32)} {
		t.Run(name, func(t *testing.T) {
			cfg, data := env(t, map[string][]byte{"s1.jsonl": fixture(t)})
			st, _ := state.Load(data)
			st.Salt = salt
			if err := st.Save(data); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(data, "state.json"))
			s := &fakeSender{}
			if _, err := Run(context.Background(), deps(cfg, data, s, &sleeper{})); err == nil {
				t.Fatal("Fehler erwartet")
			}
			if len(s.bodies) != 0 {
				t.Fatal("ohne Salz gesendet")
			}
			if after, _ := os.ReadFile(filepath.Join(data, "state.json")); !bytes.Equal(before, after) {
				t.Fatal("state.json ohne Salz verändert")
			}
			if stat := loadStatus(t, data); stat.LastError != "Nicht gekoppelt – erst /tokenlore:connect ausführen" {
				t.Fatalf("LastError %q", stat.LastError)
			}
		})
	}
}

// F1: Ist das Paket mit dem Altbestand angenommen, muss das sofort vermerkt sein, auch wenn ein späteres
// Paket scheitert. Die Lesestände bleiben dabei unverändert.
func TestRunMarksLegacySentAfterFirstPart(t *testing.T) {
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
	if len(failing.bodies) != 2 || !bytes.Contains(failing.bodies[0], []byte("legacy_model")) {
		t.Fatalf("%d Rümpfe; Altbestand nicht im ersten Paket", len(failing.bodies))
	}
	st, err := state.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if !st.LegacySent || st.Delivered || len(st.Files) != 0 || st.AccountsSum != "" {
		t.Fatalf("LegacySent %v Delivered %v, %d Lesestände", st.LegacySent, st.Delivered, len(st.Files))
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
	st, _ := state.Load(data)
	res, _, err := Prepare(context.Background(), cfg, "test", st, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	if NothingNew(st, res) {
		t.Fatal("neue Anfrage gilt als nichts Neues")
	}
	if _, err := Run(context.Background(), deps(cfg, data, &fakeSender{}, &sleeper{})); err != nil {
		t.Fatal(err)
	}
	st, _ = state.Load(data)
	res, _, err = Prepare(context.Background(), cfg, "test", st, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	if !NothingNew(st, res) {
		t.Fatal("nach vollständiger Lieferung gilt nicht nichts Neues")
	}
}

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/internal/testpki"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/secret"
	"github.com/tokenlore/tokenlore/state"
)

const (
	testDeviceKey = "tl_dk_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	testSalt      = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

// fakeServer bildet /api/plugin/ca, /pair und /ingest nach.
type fakeServer struct {
	srv         *httptest.Server
	root        *testpki.CA
	pairFP      string // ca_fingerprint der Pair-Antwort; leer = echter Fingerabdruck
	pairEmptyFP bool   // Pair-Antwort mit leerem ca_fingerprint (Server ohne eigene CA)
	noCA        bool   // /api/plugin/ca antwortet 404 (Server ohne eigene CA)
	ingestCode  int    // 0 = 200
	failAfter   int    // >0: jedes Paket nach den ersten failAfter angenommenen bekommt 500
	pairCalls   atomic.Int32
	caCalls     atomic.Int32
	onIngest    func() // wird bei jedem Paket vor der Antwort aufgerufen
	mu          sync.Mutex
	batches     []protocol.Batch
	rawBodies   []string
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	return newFakeServerHost(t, "127.0.0.1")
}

// newFakeServerHost stellt das Blatt-Zertifikat auf host aus; ein anderer Name als 127.0.0.1 erzeugt einen Hostnamen-Fehler.
func newFakeServerHost(t *testing.T, host string) *fakeServer {
	t.Helper()
	f := &fakeServer{root: testpki.NewRoot(t)}
	inter := f.root.Intermediate(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/plugin/ca", func(w http.ResponseWriter, r *http.Request) {
		f.caCalls.Add(1)
		if f.noCA {
			http.NotFound(w, r)
			return
		}
		w.Write(f.root.PEM())
	})
	mux.HandleFunc("/api/plugin/pair", func(w http.ResponseWriter, r *http.Request) {
		f.pairCalls.Add(1)
		fp := f.pairFP
		if fp == "" {
			fp = f.fingerprint()
		}
		if f.pairEmptyFP {
			fp = ""
		}
		json.NewEncoder(w).Encode(map[string]any{
			"device_id": 7, "device_key": testDeviceKey, "project_salt": testSalt, "ca_fingerprint": fp,
		})
	})
	mux.HandleFunc("/api/plugin/ingest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testDeviceKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.ingestCode != 0 {
			w.WriteHeader(f.ingestCode)
			return
		}
		if f.onIngest != nil {
			f.onIngest()
		}
		body, _ := io.ReadAll(r.Body)
		var b protocol.Batch
		json.Unmarshal(body, &b)
		f.mu.Lock()
		if f.failAfter > 0 && len(f.batches) >= f.failAfter {
			f.mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.batches = append(f.batches, b)
		f.rawBodies = append(f.rawBodies, string(body))
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"requests": len(b.Requests), "sessions": len(b.Sessions), "accounts": len(b.Accounts)})
	})
	srv := httptest.NewUnstartedServer(mux)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{inter.Leaf(t, host)}}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	f.srv = srv
	return f
}

func (f *fakeServer) fingerprint() string { return client.Fingerprint(f.root.DER()) }

func (f *fakeServer) ingested() []protocol.Batch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Batch(nil), f.batches...)
}

// connectEnv legt Claude-Testdaten (Sitzung und Altbestand) und ein leeres Datenverzeichnis an.
// Der Schlüssel landet immer in der Datei, nie im echten Schlüsselspeicher.
func connectEnv(t *testing.T) (cfg, data string) {
	t.Helper()
	cfg = filepath.Join(t.TempDir(), "claude")
	data = t.TempDir()
	p := filepath.Join(cfg, "projects", "P")
	os.MkdirAll(p, 0o700)
	for src, dst := range map[string]string{
		"../../source/claudecode/testdata/basic.jsonl":      filepath.Join(p, "s1.jsonl"),
		"../../source/claudecode/testdata/stats-cache.json": filepath.Join(cfg, "stats-cache.json"),
	} {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(dst, b, 0o600)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv("TOKENLORE_DATA_DIR", data)
	t.Setenv("TOKENLORE_SECRET_STORE", "file")
	return cfg, data
}

func connect(t *testing.T, data, cfg string, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := runConnect(context.Background(), args, &out, data, cfg)
	return code, out.String()
}

func assertNothingStored(t *testing.T, data string) {
	t.Helper()
	for _, n := range []string{"connection.json", "device.key", "state.json"} {
		if _, err := os.Stat(filepath.Join(data, n)); !os.IsNotExist(err) {
			t.Fatalf("%s wurde gespeichert", n)
		}
	}
}

// useTestRoots ersetzt newClient: Mit nil (System-Zertifikate) vertraut der Client stattdessen der Test-CA des
// Servers, denn die System-Zertifikate kennen sie nicht. Gemerkt wird, ob connect/sync wirklich nil übergeben haben.
func useTestRoots(t *testing.T, f *fakeServer) *atomic.Int32 {
	t.Helper()
	var systemCalls atomic.Int32
	old := newClient
	newClient = func(base *url.URL, caPEM []byte, timeout time.Duration) (*client.Client, error) {
		if caPEM == nil {
			systemCalls.Add(1)
			caPEM = f.root.PEM()
		}
		return client.New(base, caPEM, timeout)
	}
	t.Cleanup(func() { newClient = old })
	return &systemCalls
}

// Ohne --ca: kein Abruf der CA, System-Zertifikate, gespeicherter Zustand ohne ca_pem. Server ohne eigene CA
// antwortet auf /api/plugin/ca mit 404 und in der Kopplung mit leerem ca_fingerprint.
func TestConnectWithoutCA(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.noCA, f.pairEmptyFP = true, true
	system := useTestRoots(t, f)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--name", "Testgerät")
	if code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if f.caCalls.Load() != 0 {
		t.Fatal("/api/plugin/ca abgerufen")
	}
	if f.pairCalls.Load() != 1 {
		t.Fatalf("Pair-Aufrufe: %d", f.pairCalls.Load())
	}
	if system.Load() == 0 {
		t.Fatal("Client nicht mit System-Zertifikaten (caPEM nil) gebaut")
	}
	conn, ok, err := state.LoadConnection(data)
	if err != nil || !ok {
		t.Fatalf("Verbindung: %v %v", ok, err)
	}
	if conn.CAPEM != "" || conn.CAFingerprint != "" || conn.DeviceID != 7 || conn.ServerURL != f.srv.URL {
		t.Fatalf("Verbindung %+v", conn)
	}
	if len(f.ingested()) < 1 {
		t.Fatal("kein Paket geliefert")
	}
	assertKeyAbsent(t, data, out)

	// Der gespeicherte Zustand ohne ca_pem trägt auch den späteren Abgleich.
	testLock(t)
	if code, out, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("sync: Exit %d: %s%s", code, out, errOut)
	}
}

// Ohne --ca wird ca_fingerprint der Kopplungsantwort nicht geprüft, auch wenn der Server einen nennt.
func TestConnectWithoutCAIgnoresPairFingerprint(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.pairFP = client.Fingerprint(testpki.NewRoot(t).DER())
	useTestRoots(t, f)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--name", "x")
	if code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	conn, _, _ := state.LoadConnection(data)
	if conn.CAPEM != "" || conn.CAFingerprint != "" {
		t.Fatalf("Verbindung %+v", conn)
	}
}

// Mit --ca bleibt es beim Pinning: Meldet die Kopplung keinen Fingerabdruck, wird abgebrochen.
func TestConnectWithCARejectsEmptyPairFingerprint(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.pairEmptyFP = true
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 1 || !strings.Contains(out, "Server bestätigt einen anderen CA-Fingerabdruck") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	assertNothingStored(t, data)
}

// Mit --ca wie bisher: CA geholt, gepinnt und gespeichert, kein Aufruf mit System-Zertifikaten.
func TestConnectWithCAUnchanged(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	system := useTestRoots(t, f)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if f.caCalls.Load() != 1 || system.Load() != 0 {
		t.Fatalf("CA-Abrufe %d, System-Aufrufe %d", f.caCalls.Load(), system.Load())
	}
	conn, _, _ := state.LoadConnection(data)
	if conn.CAPEM == "" || conn.CAFingerprint != f.fingerprint() {
		t.Fatalf("Verbindung %+v", conn)
	}
}

func TestConnectWrongCARefuses(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	other := client.Fingerprint(testpki.NewRoot(t).DER())
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", other)
	if code != 1 || !strings.Contains(out, "Fingerabdruck stimmt nicht") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	assertNothingStored(t, data)
	if f.pairCalls.Load() != 0 {
		t.Fatal("Pair aufgerufen")
	}
}

func TestConnectRejectsEmptyCA(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", "")
	if code != 1 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	assertNothingStored(t, data)
	if f.pairCalls.Load() != 0 {
		t.Fatal("Pair aufgerufen")
	}
}

func TestConnectPairFingerprintMismatch(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.pairFP = client.Fingerprint(testpki.NewRoot(t).DER())
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint())
	if code != 1 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	assertNothingStored(t, data)
	if strings.Contains(out, testDeviceKey) {
		t.Fatal("Schlüssel in der Ausgabe")
	}
}

func TestConnectRejectsBadCode(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	for _, c := range []string{"12345", "1234567", "12a456", ""} {
		if code, out := connect(t, data, cfg, f.srv.URL, c, "--ca", f.fingerprint()); code != 1 {
			t.Fatalf("Code %q: Exit %d: %s", c, code, out)
		}
	}
	assertNothingStored(t, data)
	if f.pairCalls.Load() != 0 {
		t.Fatal("Pair aufgerufen")
	}
}

func TestConnectRejectsBadName(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "a‮b")
	if code != 1 || !strings.Contains(out, "--name") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if strings.ContainsRune(out, '‮') {
		t.Fatal("ungültiger Name ausgegeben")
	}
	assertNothingStored(t, data)
	if f.pairCalls.Load() != 0 {
		t.Fatal("Pair aufgerufen")
	}
}

func TestConnectSuccess(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	// Fingerabdruck in Kleinbuchstaben ohne ":" muss genauso gehen.
	fp := strings.ToLower(strings.ReplaceAll(f.fingerprint(), ":", ""))
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", fp, "--name", "Testgerät")
	if code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if out != "Verbunden mit "+f.srv.URL+" als „Testgerät“ (Gerät 7).\nErster Abgleich: 1 Anfrage in 1 Paket.\n" {
		t.Fatalf("Ausgabe:\n%s", out)
	}
	conn, ok, err := state.LoadConnection(data)
	if err != nil || !ok {
		t.Fatalf("Verbindung: %v %v", ok, err)
	}
	if conn.CAFingerprint != f.fingerprint() || conn.DeviceID != 7 || conn.DeviceName != "Testgerät" ||
		conn.ServerURL != f.srv.URL || conn.KeyStore != secret.KindFile || conn.KeySum == "" || conn.CAPEM == "" || conn.ConnectedAt.IsZero() {
		t.Fatalf("Verbindung %+v", conn)
	}
	key, err := secret.Load(data, conn.KeyStore, conn.KeySum)
	if err != nil || key != testDeviceKey {
		t.Fatalf("Schlüssel: %v", err)
	}
	st, _ := state.Load(data)
	if st.Salt != testSalt || !st.Delivered {
		t.Fatalf("Merkzettel: Salt %q Delivered %v", st.Salt, st.Delivered)
	}
	if len(f.ingested()) < 1 {
		t.Fatal("kein Paket geliefert")
	}
	assertKeyAbsent(t, data, out)
}

// assertKeyAbsent prüft, dass der Schlüssel weder in der Ausgabe noch in connection.json oder status.json steht.
func assertKeyAbsent(t *testing.T, data, out string) {
	t.Helper()
	secretPart := strings.TrimPrefix(testDeviceKey, "tl_dk_")
	if strings.Contains(out, secretPart) {
		t.Fatal("Schlüssel in der Ausgabe")
	}
	for _, n := range []string{"connection.json", "status.json", "state.json"} {
		b, _ := os.ReadFile(filepath.Join(data, n))
		if strings.Contains(string(b), secretPart) {
			t.Fatalf("Schlüssel in %s", n)
		}
	}
}

// writeMarkedState legt einen Merkzettel an, in dem s1.jsonl schon vollständig gelesen ist.
func writeMarkedState(t *testing.T, cfg, data string, legacyDone, delivered bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(cfg, "projects", "P", "s1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	n := min(4096, len(b))
	sum := sha256.Sum256(b[:n])
	st, _ := state.Load(data)
	st.Files["projects/P/s1.jsonl"] = state.FileMark{Offset: int64(len(b)), End: int64(len(b)), HeadLen: n, HeadSum: hex.EncodeToString(sum[:])}
	st.LegacyDone, st.Delivered = legacyDone, delivered
	st.Salt = strings.Repeat("f", 64)
	st.AccountsSum = "alt"
	if err := st.Save(data); err != nil {
		t.Fatal(err)
	}
}

func hasLegacy(bs []protocol.Batch) bool {
	for _, b := range bs {
		if len(b.LegacyModel) > 0 || len(b.LegacyActivity) > 0 {
			return true
		}
	}
	return false
}

func requestCount(bs []protocol.Batch) int {
	n := 0
	for _, b := range bs {
		n += len(b.Requests)
	}
	return n
}

func TestConnectFirstTimeResetsMarks(t *testing.T) {
	cfg, data := connectEnv(t)
	writeMarkedState(t, cfg, data, true, false)
	f := newFakeServer(t)
	if code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x"); code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	got := f.ingested()
	if !hasLegacy(got) || requestCount(got) == 0 {
		t.Fatalf("erster Abgleich ohne Altbestand oder Anfragen: %d Anfragen", requestCount(got))
	}
	st, _ := state.Load(data)
	if st.Salt != testSalt {
		t.Fatal("Salz nicht ersetzt")
	}
}

func TestReconnectKeepsLegacyDone(t *testing.T) {
	cfg, data := connectEnv(t)
	writeMarkedState(t, cfg, data, true, true)
	f := newFakeServer(t)
	if code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x"); code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	got := f.ingested()
	if hasLegacy(got) {
		t.Fatal("Altbestand nach Neu-Kopplung erneut gesendet")
	}
	for _, raw := range f.rawBodies {
		if strings.Contains(raw, "legacy_model") || strings.Contains(raw, "legacy_activity") {
			t.Fatal("Paket enthält Altbestand")
		}
	}
	if requestCount(got) == 0 {
		t.Fatal("Anfragen nicht erneut gesendet")
	}
}

func TestReconnectReplacesOldConnection(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	if code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x"); code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	code, out := connect(t, data, cfg, f.srv.URL, "654321", "--ca", f.fingerprint(), "--name", "y")
	if code != 0 || !strings.Contains(out, "Das alte Gerät bleibt auf dem Server, bis du es dort sperrst.") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	conn, _, _ := state.LoadConnection(data)
	if conn.DeviceName != "y" {
		t.Fatalf("Verbindung nicht ersetzt: %+v", conn)
	}
}

func TestConnectFirstSyncFailureKeepsConnection(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.ingestCode = http.StatusInternalServerError
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 0 || !strings.Contains(out, "Erster Abgleich fehlgeschlagen") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if _, ok, _ := state.LoadConnection(data); !ok {
		t.Fatal("Verbindung nach gescheitertem Abgleich weg")
	}
	assertKeyAbsent(t, data, out)
}

func TestDisconnect(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	if code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x"); code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	var out bytes.Buffer
	if code := runDisconnect(&out, data); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	if !strings.Contains(strings.ToLower(out.String()), "sperre das gerät") {
		t.Fatalf("Ausgabe ohne Hinweis aufs Sperren:\n%s", out.String())
	}
	if _, ok, _ := state.LoadConnection(data); ok {
		t.Fatal("connection.json noch da")
	}
	if _, err := os.Stat(filepath.Join(data, "device.key")); !os.IsNotExist(err) {
		t.Fatal("device.key noch da")
	}
	st, _ := state.Load(data)
	if st.Salt != testSalt || len(st.Files) == 0 {
		t.Fatal("Merkzettel darf beim Trennen bleiben")
	}
}

// failConnSave lässt das Speichern der Verbindung scheitern.
func failConnSave(t *testing.T) {
	t.Helper()
	old := saveConnection
	saveConnection = func(*state.Connection, string) error { return errors.New("Platte voll") }
	t.Cleanup(func() { saveConnection = old })
}

// Stirbt connect zwischen Schlüssel und Verbindung, darf nie ein altes Salz mit neuer Verbindung zusammenkommen.
func TestConnectConnSaveFailureLeavesNoStaleSalt(t *testing.T) {
	cfg, data := connectEnv(t)
	writeMarkedState(t, cfg, data, true, true)
	f := newFakeServer(t)
	failConnSave(t)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 1 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	st, _ := state.Load(data)
	if st.Salt != "" || len(st.Files) != 0 || st.AccountsSum != "" {
		t.Fatalf("Merkzettel mit altem Stand: Salt %q, %d Lesestände", st.Salt, len(st.Files))
	}
	for _, n := range []string{"connection.json", "device.key"} {
		if _, err := os.Stat(filepath.Join(data, n)); !os.IsNotExist(err) {
			t.Fatalf("%s nach Fehler noch da", n)
		}
	}
	if !strings.Contains(out, "auf dem Server sperren") {
		t.Fatalf("Ausgabe ohne Hinweis, das neue Gerät zu sperren:\n%s", out)
	}
	if len(f.ingested()) != 0 {
		t.Fatal("trotz Fehler geliefert")
	}
}

func TestReconnectSaveFailureSaysOldConnectionGone(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	if code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x"); code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	failConnSave(t)
	code, out := connect(t, data, cfg, f.srv.URL, "654321", "--ca", f.fingerprint(), "--name", "y")
	if code != 1 || !strings.Contains(out, "bisherige Verbindung ist ebenfalls entfernt") || !strings.Contains(out, "neu koppeln") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if _, ok, _ := state.LoadConnection(data); ok {
		t.Fatal("halbe Verbindung geblieben")
	}
}

func TestDisconnectWithoutConnectionRemovesLeftoverKey(t *testing.T) {
	_, data := connectEnv(t)
	if _, _, err := secret.Save(data, testDeviceKey); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := runDisconnect(&out, data); code != 0 || !strings.Contains(out.String(), "Nicht verbunden.") {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(data, "device.key")); !os.IsNotExist(err) {
		t.Fatal("übrig gebliebener Schlüssel nicht entfernt")
	}
}

// Ist der Schlüsselspeicher dauerhaft weg, muss das Trennen trotzdem möglich sein.
func TestDisconnectKeyringUnavailable(t *testing.T) {
	f := newFakeServer(t)
	_, data := connectedEnv(t, f)
	old := removeSecret
	removeSecret = func(string, string) error { return errors.New("Schlüsselspeicher antwortet nicht") }
	t.Cleanup(func() { removeSecret = old })
	var out bytes.Buffer
	code := runDisconnect(&out, data)
	if code != 1 || !strings.Contains(out.String(), "nicht gelöscht") || !strings.Contains(out.String(), "sperre") {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	if _, ok, _ := state.LoadConnection(data); ok {
		t.Fatal("connection.json trotzdem noch da")
	}
}

func TestWithLockReportsRealError(t *testing.T) {
	old := lockDevice
	t.Cleanup(func() { lockDevice = old })
	lockDevice = func() (func(), bool, error) { return nil, false, errors.New("Sperrdatei nicht anlegbar") }
	var errOut bytes.Buffer
	if code := withLock(&errOut, func() int { t.Fatal("ohne Sperre ausgeführt"); return 0 }); code != 1 {
		t.Fatalf("Exit %d", code)
	}
	if !strings.Contains(errOut.String(), "Sperrdatei nicht anlegbar") || strings.Contains(errOut.String(), "läuft ein Abgleich") {
		t.Fatalf("Ausgabe %q", errOut.String())
	}
	lockDevice = func() (func(), bool, error) { return nil, false, nil }
	errOut.Reset()
	withLock(&errOut, func() int { t.Fatal("ohne Sperre ausgeführt"); return 0 })
	if !strings.Contains(errOut.String(), "läuft ein Abgleich") {
		t.Fatalf("belegt: %q", errOut.String())
	}
}

func TestCount(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{{0, "0 Anfragen in 0 Paketen"}, {1, "1 Anfrage in 1 Paket"}, {2, "2 Anfragen in 2 Paketen"}} {
		if got := requestsInParts(c.n, c.n); got != c.want {
			t.Fatalf("%d: %q, want %q", c.n, got, c.want)
		}
	}
}

// lockedBuffer ist ein Puffer, den Testserver und connect gleichzeitig benutzen dürfen.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// F11: „Verbunden mit …“ steht schon da, wenn das erste Paket ankommt; die Zusammenfassung folgt danach.
func TestConnectPrintsConnectedBeforeFirstSync(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	var out lockedBuffer
	var seen atomic.Value
	f.onIngest = func() { seen.Store(out.String()) }
	if code := runConnect(context.Background(), []string{f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x"}, &out, data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s", code, out.String())
	}
	before, _ := seen.Load().(string)
	if !strings.Contains(before, "Verbunden mit "+f.srv.URL) {
		t.Fatalf("beim ersten Paket noch nicht ausgegeben: %q", before)
	}
	if !strings.HasSuffix(out.String(), "Erster Abgleich: 1 Anfrage in 1 Paket.\n") {
		t.Fatalf("Ausgabe:\n%s", out.String())
	}
}

// writeManyRequests ersetzt die Sitzungsdatei durch eine mit n Anfragen, damit der Abgleich mehrere Pakete braucht.
func writeManyRequests(t *testing.T, cfg string, n int) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString(`{"type":"bridge-session","sessionId":"s1","ownerAccountUuid":"acc-1"}` + "\n")
	for i := range n {
		fmt.Fprintf(&b, `{"type":"assistant","sessionId":"s1","requestId":"req_%d","timestamp":"2026-10-23T21:30:00.000Z","version":"2.1.309","message":{"id":"msg_%d","model":"claude-opus-5-5","usage":{"input_tokens":1,"output_tokens":2}}}`+"\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(cfg, "projects", "P", "s1.jsonl"), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// F1: Erst-Abgleich scheitert bei Paket 2, nachdem Paket 1 mit dem Altbestand angenommen war. Eine Neu-Kopplung
// darf den Altbestand nicht noch einmal schicken, sonst zählt der Server ihn unter der neuen Geräte-ID doppelt.
func TestReconnectAfterPartialFirstSyncSendsNoLegacy(t *testing.T) {
	cfg, data := connectEnv(t)
	writeManyRequests(t, cfg, 4500)
	f := newFakeServer(t)
	f.failAfter = 1
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 0 || !strings.Contains(out, "Erster Abgleich fehlgeschlagen") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if got := f.ingested(); len(got) != 1 || !hasLegacy(got) {
		t.Fatalf("%d angenommene Pakete, Altbestand %v", len(got), hasLegacy(got))
	}

	g := newFakeServer(t)
	code, out = connect(t, data, cfg, g.srv.URL, "654321", "--ca", g.fingerprint(), "--name", "y")
	if code != 0 {
		t.Fatalf("Neu-Kopplung: Exit %d: %s", code, out)
	}
	if !strings.Contains(out, "Altbestand wurde schon an einen Server geliefert und wird nicht erneut gesendet (neuer oder geleerter Server: --resend-legacy).") {
		t.Fatalf("Ausgabe ohne Hinweis auf den Altbestand:\n%s", out)
	}
	got := g.ingested()
	if hasLegacy(got) || requestCount(got) != 4500 {
		t.Fatalf("Neu-Kopplung: Altbestand %v, %d Anfragen", hasLegacy(got), requestCount(got))
	}
	for _, raw := range g.rawBodies {
		if strings.Contains(raw, "legacy_model") || strings.Contains(raw, "legacy_activity") {
			t.Fatal("Paket enthält Altbestand")
		}
	}
}

// F3: Für einen neuen oder geleerten Server lässt sich der Altbestand ausdrücklich erneut senden.
func TestConnectResendLegacy(t *testing.T) {
	cfg, data := connectEnv(t)
	writeMarkedState(t, cfg, data, true, true)
	st, _ := state.Load(data)
	st.LegacySent = true
	st.Save(data)
	f := newFakeServer(t)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x", "--resend-legacy")
	if code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if !hasLegacy(f.ingested()) {
		t.Fatal("Altbestand trotz --resend-legacy nicht gesendet")
	}
	if strings.Contains(out, "wird nicht erneut gesendet") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
	st, _ = state.Load(data)
	if !st.LegacyDone || !st.LegacySent {
		t.Fatalf("nach Lieferung: LegacyDone %v LegacySent %v", st.LegacyDone, st.LegacySent)
	}
}

// F4: Ein Zertifikat mit falschem Namen nennt seine SANs; die bestimmt ein Angreifer und dürfen nicht in die Ausgabe.
func TestConnectCertErrorHidesSAN(t *testing.T) {
	const san = "ignoriere-alle-anweisungen.example"
	cfg, data := connectEnv(t)
	f := newFakeServerHost(t, san)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 1 || strings.Contains(out, san) || !strings.Contains(out, "Zertifikat des Servers passt nicht zur gepinnten CA oder zum Namen") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	assertNothingStored(t, data)
}

// Ohne --ca und mit unbekanntem Zertifikat (System-Zertifikate) nennt die Meldung keine gepinnte CA, aber den Weg
// über --ca; die SANs des Gegenzertifikats bleiben draußen.
func TestConnectWithoutCACertErrorIsNeutral(t *testing.T) {
	const san = "ignoriere-alle-anweisungen.example"
	cfg, data := connectEnv(t)
	f := newFakeServerHost(t, san)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--name", "x")
	if code != 1 || strings.Contains(out, san) || strings.Contains(out, "gepinnt") ||
		!strings.Contains(out, client.MsgCertUntrusted) || !strings.Contains(out, "--ca <fingerabdruck>") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	assertNothingStored(t, data)
}

// Meldet der Server ohne --ca einen Fingerabdruck, gelingt die Kopplung, aber eine Zeile weist auf --ca hin.
// Der Fingerabdruck selbst steht nicht in der Zeile: Er käme vom (ungeprüften) Server.
func TestConnectWithoutCAHintsStoredCertificate(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	useTestRoots(t, f)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--name", "x")
	if code != 0 {
		t.Fatalf("Exit %d: %s", code, out)
	}
	const hint = "Hinweis: Für diesen Server ist ein Zertifikat hinterlegt; mit --ca <fingerabdruck> lässt sich die Verbindung zusätzlich daran binden."
	if !strings.Contains(out, hint) {
		t.Fatalf("Hinweis fehlt:\n%s", out)
	}
	if strings.Contains(out, f.fingerprint()) {
		t.Fatalf("Fingerabdruck des Servers in der Ausgabe:\n%s", out)
	}
	if conn, ok, _ := state.LoadConnection(data); !ok || conn.CAPEM != "" || conn.CAFingerprint != "" {
		t.Fatalf("Verbindung %+v", conn)
	}
}

// Kein Hinweis, wenn der Server keinen Fingerabdruck meldet oder mit --ca gepinnt wurde.
func TestConnectNoHintWithoutServerCAOrWithCA(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.noCA, f.pairEmptyFP = true, true
	useTestRoots(t, f)
	if code, out := connect(t, data, cfg, f.srv.URL, "123456", "--name", "x"); code != 0 || strings.Contains(out, "Hinweis:") {
		t.Fatalf("ohne Fingerabdruck: Exit %d: %s", code, out)
	}

	cfg, data = connectEnv(t)
	g := newFakeServer(t)
	if code, out := connect(t, data, cfg, g.srv.URL, "123456", "--ca", g.fingerprint(), "--name", "x"); code != 0 || strings.Contains(out, "Hinweis:") {
		t.Fatalf("mit --ca: Exit %d: %s", code, out)
	}
}

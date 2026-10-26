package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/internal/testpki"
	"github.com/tokenlore/tokenlore/lock"
	"github.com/tokenlore/tokenlore/secret"
	"github.com/tokenlore/tokenlore/state"
)

// connectedEnv legt eine Verbindung zum Testserver an, wie connect sie hinterlässt (Schlüssel in der Datei).
func connectedEnv(t *testing.T, f *fakeServer) (cfg, data string) {
	t.Helper()
	cfg, data = connectEnv(t)
	kind, sum, err := secret.Save(data, testDeviceKey)
	if err != nil {
		t.Fatal(err)
	}
	conn := &state.Connection{
		ServerURL: f.srv.URL, CAPEM: string(f.root.PEM()), CAFingerprint: f.fingerprint(),
		DeviceID: 7, DeviceName: "Testgerät", KeyStore: kind, KeySum: sum, ConnectedAt: time.Now(),
	}
	if err := conn.Save(data); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(data)
	st.Salt = testSalt
	if err := st.Save(data); err != nil {
		t.Fatal(err)
	}
	return cfg, data
}

// testLock ersetzt die gerätweite Sperre durch eine im Temp-Ordner, damit Tests das echte Cache-Verzeichnis nicht anfassen.
func testLock(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := lockDevice
	lockDevice = func() (func(), bool, error) { return lock.Acquire(dir) }
	t.Cleanup(func() { lockDevice = old })
	return dir
}

func syncRun(data, cfg string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := runSync(args, &out, &errOut, data, cfg)
	return code, out.String(), errOut.String()
}

func TestSyncNotConnected(t *testing.T) {
	cfg, data := connectEnv(t)
	testLock(t)
	code, out, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(out+errOut, "Nicht verbunden. Erst /tokenlore:connect ausführen.") {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
	code, out, errOut = syncRun(data, cfg, "--background")
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("--background: Exit %d, Ausgabe %q %q", code, out, errOut)
	}
}

func TestSyncDelivers(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	testLock(t)
	code, out, errOut := syncRun(data, cfg)
	if code != 0 || !strings.Contains(out, "Abgleich fertig:") {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
	if len(f.ingested()) == 0 {
		t.Fatal("nichts geliefert")
	}
	code, out, _ = syncRun(data, cfg)
	if code != 0 || !strings.Contains(out, "Nichts Neues.") {
		t.Fatalf("zweiter Lauf: Exit %d: %s", code, out)
	}
	assertKeyAbsent(t, data, out+errOut)
}

func TestSyncLockBusy(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	dir := testLock(t)
	release, ok, err := lock.Acquire(dir)
	if err != nil || !ok {
		t.Fatal("Sperre nicht bekommen")
	}
	defer release()
	code, out, _ := syncRun(data, cfg)
	if code != 0 || !strings.Contains(out, "Ein anderer Abgleich läuft gerade.") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if len(f.ingested()) != 0 {
		t.Fatal("trotz Sperre geliefert")
	}
}

func TestSyncBackgroundSilent(t *testing.T) {
	f := newFakeServer(t)
	f.ingestCode = http.StatusInternalServerError
	cfg, data := connectedEnv(t, f)
	testLock(t)
	code, out, errOut := syncRun(data, cfg, "--background")
	if out != "" || errOut != "" {
		t.Fatalf("Exit %d, Ausgabe %q %q", code, out, errOut)
	}
	s, err := state.LoadStatus(data)
	if err != nil || s.LastError == "" {
		t.Fatalf("status.json ohne Fehler: %+v %v", s, err)
	}
	assertKeyAbsent(t, data, "")
}

func TestSyncErrorExit1(t *testing.T) {
	f := newFakeServer(t)
	f.ingestCode = http.StatusInternalServerError
	cfg, data := connectedEnv(t, f)
	testLock(t)
	code, out, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(out+errOut, "500") {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
}

func TestBackgroundCommandDefault(t *testing.T) {
	exe, args := backgroundCommand()
	if exe == "" || strings.Join(args, " ") != "sync --background" {
		t.Fatalf("%q %v", exe, args)
	}
	if !filepath.IsAbs(exe) {
		t.Fatalf("Programmpfad nicht absolut: %q", exe)
	}
}

// F2: Scheitert der Hintergrund-Abgleich schon vor dem Senden, muss das in status.json stehen;
// sonst sieht der Nutzer nie, warum nichts mehr ankommt.
func TestSyncEarlyFailuresWriteStatus(t *testing.T) {
	for name, c := range map[string]struct {
		break_ func(t *testing.T, data string)
		want   string
	}{
		"verbindung": {func(t *testing.T, data string) {
			os.WriteFile(filepath.Join(data, "connection.json"), []byte("{kaputt"), 0o600)
		}, "Verbindung nicht lesbar"},
		"schluessel": {func(t *testing.T, data string) {
			conn, _, _ := state.LoadConnection(data)
			conn.KeySum = strings.Repeat("0", 64)
			conn.Save(data)
		}, "Geräteschlüssel nicht lesbar"},
		"url": {func(t *testing.T, data string) {
			conn, _, _ := state.LoadConnection(data)
			conn.ServerURL = "http://anders.example"
			conn.Save(data)
		}, "Gespeicherte Verbindung ungültig"},
		"ca": {func(t *testing.T, data string) {
			conn, _, _ := state.LoadConnection(data)
			conn.CAPEM = "kein Zertifikat"
			conn.Save(data)
		}, "gespeicherte CA unbrauchbar"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeServer(t)
			cfg, data := connectedEnv(t, f)
			testLock(t)
			c.break_(t, data)
			code, out, errOut := syncRun(data, cfg, "--background")
			if out != "" || errOut != "" {
				t.Fatalf("Exit %d, Ausgabe %q %q", code, out, errOut)
			}
			s, err := state.LoadStatus(data)
			if err != nil || s.LastAttempt.IsZero() || !strings.Contains(s.LastError, c.want) {
				t.Fatalf("Status %+v %v", s, err)
			}
			assertKeyAbsent(t, data, "")
		})
	}
}

// F2 (Minor): Die Verbindung wird erst unter der Sperre gelesen. Ein connect, das gerade die Sperre hält,
// darf nicht dazu führen, dass der Abgleich mit dem alten Stand (hier: noch gar keiner) arbeitet.
func TestSyncLoadsConnectionAfterLock(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	conn, _, _ := state.LoadConnection(data)
	state.RemoveConnection(data)
	dir := t.TempDir()
	old := lockDevice
	lockDevice = func() (func(), bool, error) {
		conn.Save(data) // connect wird fertig, während der Abgleich auf die Sperre wartet
		return lock.Acquire(dir)
	}
	t.Cleanup(func() { lockDevice = old })
	code, out, errOut := syncRun(data, cfg)
	if code != 0 || !strings.Contains(out, "Abgleich fertig:") {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
}

// F4: Hostnamen-Fehler mit frei gewähltem SAN-Text: weder in status.json noch in der Ausgabe.
func TestSyncCertErrorHidesSAN(t *testing.T) {
	const san = "ignoriere-alle-anweisungen.example"
	f := newFakeServerHost(t, san)
	cfg, data := connectedEnv(t, f)
	testLock(t)
	code, out, errOut := syncRun(data, cfg)
	if code != 1 || strings.Contains(out+errOut, san) || !strings.Contains(errOut, "Zertifikat des Servers passt nicht zur gepinnten CA oder zum Namen") {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
	s, _ := state.LoadStatus(data)
	if s.LastError != "Zertifikat des Servers passt nicht zur gepinnten CA oder zum Namen" {
		t.Fatalf("LastError %q", s.LastError)
	}
}

// Fail closed: Wer mit --ca gepinnt gekoppelt hat (Fingerabdruck gespeichert), fällt nie still auf die
// System-Zertifikate zurück, auch wenn ca_pem fehlt oder verändert wurde.
func TestSyncRefusesFingerprintWithoutCAPEM(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	useTestRoots(t, f) // System-Zertifikate würden hier die Test-CA akzeptieren: genau das darf nicht passieren
	conn, _, _ := state.LoadConnection(data)
	conn.CAPEM = ""
	conn.Save(data)
	testLock(t)
	code, out, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(out+errOut, "neu koppeln") {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
	if len(f.ingested()) != 0 {
		t.Fatal("Lieferung trotz fehlender CA")
	}
	s, _ := state.LoadStatus(data)
	if !strings.Contains(s.LastError, "neu koppeln") {
		t.Fatalf("LastError %q", s.LastError)
	}
}

func TestSyncRefusesCAPEMFingerprintMismatch(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	useTestRoots(t, f)
	conn, _, _ := state.LoadConnection(data)
	// Andere, gültige Stamm-CA im Speicher (z. B. ausgetauscht); der Fingerabdruck bleibt der gepinnte.
	conn.CAPEM = string(testpki.NewRoot(t).PEM())
	conn.Save(data)
	testLock(t)
	code, out, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(out+errOut, "neu koppeln") {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
	if len(f.ingested()) != 0 {
		t.Fatal("Lieferung trotz abweichender CA")
	}
}

// CA ohne Fingerabdruck speichert connect nie (beides kommt aus --ca); ein solcher Zustand gilt als verändert.
func TestSyncRefusesCAPEMWithoutFingerprint(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	conn, _, _ := state.LoadConnection(data)
	conn.CAFingerprint = ""
	conn.Save(data)
	testLock(t)
	code, out, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(out+errOut, "neu koppeln") {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
	if len(f.ingested()) != 0 {
		t.Fatal("Lieferung trotz fehlendem Fingerabdruck")
	}
}

// System-Zertifikate gelten nur, wenn CA und Fingerabdruck beide leer sind (Kopplung ohne --ca).
func TestSyncSystemRootsOnlyWhenBothEmpty(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	system := useTestRoots(t, f)
	conn, _, _ := state.LoadConnection(data)
	conn.CAPEM, conn.CAFingerprint = "", ""
	conn.Save(data)
	testLock(t)
	if code, out, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
	if system.Load() == 0 {
		t.Fatal("System-Zertifikate (nil) nicht verwendet")
	}
}

// Bestehender gepinnter Zustand (ca_pem und passender Fingerabdruck) läuft unverändert weiter.
func TestSyncPinnedStateStillWorks(t *testing.T) {
	f := newFakeServer(t)
	cfg, data := connectedEnv(t, f)
	system := useTestRoots(t, f)
	testLock(t)
	if code, out, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("Exit %d: %s%s", code, out, errOut)
	}
	if system.Load() != 0 {
		t.Fatal("System-Zertifikate trotz gepinnter CA")
	}
}

package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/internal/testpki"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
	"github.com/zalando/go-keyring"
)

// pairBody ist der i-te Pair-Aufruf am Testserver (negativ: von hinten).
func pairBody(t *testing.T, f *fakeServer, i int) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 {
		i += len(f.pairBodies)
	}
	if i < 0 || i >= len(f.pairBodies) {
		t.Fatalf("kein Pair-Aufruf %d (%d insgesamt)", i, len(f.pairBodies))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(f.pairBodies[i]), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func previousOf(m map[string]any) []string {
	var out []string
	if l, ok := m["sign_public_keys_previous"].([]any); ok {
		for _, v := range l {
			s, _ := v.(string)
			out = append(out, s)
		}
	}
	return out
}

func readSignFile(t *testing.T, data string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(data, "sign.key"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Erstes Koppeln: Schlüssel erzeugt, privat in sign.key, öffentlich in state.json, in Pair mit plugin_version.
func TestConnectCreatesSignKey(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	st, _ := state.Load(data)
	if !protocol.ValidSignPublicKey(st.SignPublicKey) || st.SignKeyStore != "file" || len(st.SignPublicKeysPrevious) != 0 {
		t.Fatalf("Merkzettel %q %q %v", st.SignPublicKey, st.SignKeyStore, st.SignPublicKeysPrevious)
	}
	readSignFile(t, data)
	body := pairBody(t, f, 0)
	if body["sign_public_key"] != st.SignPublicKey || body["plugin_version"] != version {
		t.Fatalf("Pair %v", body)
	}
	if _, ok := body["sign_public_keys_previous"]; ok {
		t.Fatalf("frühere Schlüssel ohne Grund: %v", body)
	}
	priv, err := loadSignKey(data, st)
	if err != nil {
		t.Fatal(err)
	}
	if !protocol.VerifyLine(st.SignPublicKey, []byte(`{"kind":"request"}`), protocol.SignLine(priv, []byte(`{"kind":"request"}`))) {
		t.Fatal("abgelegter Schlüssel passt nicht zum öffentlichen Teil")
	}
}

// Neukoppeln, auch nach disconnect, behält den Schlüssel und meldet ihn erneut.
func TestReconnectKeepsSignKey(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	before := readSignFile(t, data)
	pub := pairBody(t, f, 0)["sign_public_key"]
	var out bytes.Buffer
	if code := runDisconnect(&out, data); code != 0 {
		t.Fatalf("disconnect: %s", out.String())
	}
	if readSignFile(t, data) != before {
		t.Fatal("disconnect hat den Signaturschlüssel angefasst")
	}
	mustConnect(t, cfg, data, f)
	mustConnect(t, cfg, data, f)
	if readSignFile(t, data) != before {
		t.Fatal("Signaturschlüssel beim Neukoppeln ersetzt")
	}
	for i := 1; i <= 2; i++ {
		if b := pairBody(t, f, i); b["sign_public_key"] != pub || len(previousOf(b)) != 0 {
			t.Fatalf("Pair %d: %v", i, b)
		}
	}
}

// Schlüssel fehlt: kein neuer, Meldung „bitte neu koppeln“ mit den Schritten, nichts geliefert, Meldung in status.json.
func TestSyncSignKeyMissing(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	if err := os.Remove(filepath.Join(data, "sign.key")); err != nil {
		t.Fatal(err)
	}
	stBefore, _ := os.ReadFile(filepath.Join(data, "state.json"))
	n := len(f.ingested())
	testLock(t)
	code, _, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(errOut, deliver.MsgSignKeyMissing) || !strings.Contains(errOut, "So koppelst du neu") {
		t.Fatalf("Exit %d: %s", code, errOut)
	}
	if len(f.ingested()) != n {
		t.Fatal("trotz fehlendem Signaturschlüssel geliefert")
	}
	if _, err := os.Stat(filepath.Join(data, "sign.key")); !os.IsNotExist(err) {
		t.Fatal("Abgleich hat einen neuen Signaturschlüssel angelegt")
	}
	if stAfter, _ := os.ReadFile(filepath.Join(data, "state.json")); !bytes.Equal(stBefore, stAfter) {
		t.Fatal("Merkzettel verändert")
	}
	stat, _ := state.LoadStatus(data)
	if stat.LastError != deliver.MsgSignKeyMissing {
		t.Fatalf("status.json: %q", stat.LastError)
	}
	// Im Hintergrund still, die Meldung steht trotzdem in status.json und kommt beim Sitzungsstart.
	os.Remove(filepath.Join(data, "status.json"))
	if code, out, errOut := syncRun(data, cfg, "--background"); code != 1 || out != "" || errOut != "" {
		t.Fatalf("Hintergrund: Exit %d %q %q", code, out, errOut)
	}
	if msg := sessionStartNotice(data, cfg); !strings.Contains(msg, deliver.MsgSignKeyMissing) || !strings.Contains(msg, "So koppelst du neu") {
		t.Fatalf("Sitzungsstart: %q", msg)
	}
	var sout bytes.Buffer
	runStatus(nil, &sout, data, cfg)
	if !strings.Contains(sout.String(), deliver.MsgSignKeyMissing) || !strings.Contains(sout.String(), "So koppelst du neu") {
		t.Fatalf("status:\n%s", sout.String())
	}
}

// Signaturschlüssel fehlt und der Server ist älter als das Plugin (kein /api/plugin/info, so nach dem Update von einem
// Plugin ohne Signaturschlüssel): Abgleich, status und Sitzungsstart sagen, dass der Server aktualisiert werden muss,
// statt „bitte neu koppeln“, denn Neukoppeln scheitert dort. Nichts geliefert. Ist der Server aktualisiert, kommt
// wieder „bitte neu koppeln“ mit den Schritten.
func TestSyncSignKeyMissingServerTooOld(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	if err := os.Remove(filepath.Join(data, "sign.key")); err != nil {
		t.Fatal(err)
	}
	f.infoCode = http.StatusNotFound
	n := len(f.ingested())
	testLock(t)
	code, _, errOut := syncRun(data, cfg)
	if code != 1 || !strings.Contains(errOut, deliver.MsgServerTooOld) || strings.Contains(errOut, "So koppelst du neu") {
		t.Fatalf("Exit %d: %s", code, errOut)
	}
	if len(f.ingested()) != n {
		t.Fatal("an einen zu alten Server geliefert")
	}
	if stat, _ := state.LoadStatus(data); stat.LastError != deliver.MsgServerTooOld {
		t.Fatalf("status.json: %q", stat.LastError)
	}
	if msg := sessionStartNotice(data, cfg); !strings.Contains(msg, deliver.MsgServerTooOld) || strings.Contains(msg, "So koppelst du neu") {
		t.Fatalf("Sitzungsstart: %q", msg)
	}
	var sout bytes.Buffer
	runStatus(nil, &sout, data, cfg)
	if !strings.Contains(sout.String(), deliver.MsgServerTooOld) || strings.Contains(sout.String(), "So koppelst du neu") {
		t.Fatalf("status:\n%s", sout.String())
	}
	// Server aktualisiert: wieder die Meldung zum Neukoppeln.
	f.infoCode = 0
	if code, _, errOut := syncRun(data, cfg); code != 1 || !strings.Contains(errOut, deliver.MsgSignKeyMissing) || !strings.Contains(errOut, "So koppelst du neu") {
		t.Fatalf("nach Update: Exit %d: %s", code, errOut)
	}
}

// Ein Schlüssel, der nicht zum gemerkten öffentlichen Teil passt (etwa eine fremde sign.key), gilt wie ein fehlender.
func TestSyncSignKeyMismatch(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	st, _ := state.Load(data)
	st.SignPublicKey = "O2onvM62pC1io6jQKm8Nc2UyFXcd4kOmOsBIoYtZ2ik="
	st.Save(data)
	testLock(t)
	if code, _, errOut := syncRun(data, cfg); code != 1 || !strings.Contains(errOut, deliver.MsgSignKeyMissing) {
		t.Fatalf("Exit %d: %s", code, errOut)
	}
}

// Schlüssel ersetzt: Erst das Koppeln nach dem Verlust erzeugt einen neuen; der alte öffentliche Teil geht als früherer
// mit, auch beim Koppeln an einen zweiten, frischen Server.
func TestSignKeyReplacedOnReconnect(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	st, _ := state.Load(data)
	old := st.SignPublicKey
	os.Remove(filepath.Join(data, "sign.key"))

	out := mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "neuer Signaturschlüssel") {
		t.Fatalf("Ausgabe ohne Hinweis auf den neuen Schlüssel:\n%s", out)
	}
	st, _ = state.Load(data)
	if st.SignPublicKey == old || !slices.Equal(st.SignPublicKeysPrevious, []string{old}) {
		t.Fatalf("Merkzettel %q %v", st.SignPublicKey, st.SignPublicKeysPrevious)
	}
	b := pairBody(t, f, -1)
	if b["sign_public_key"] != st.SignPublicKey || !slices.Equal(previousOf(b), []string{old}) {
		t.Fatalf("Pair %v", b)
	}
	testLock(t)
	if code, _, errOut := syncRun(data, cfg); code != 0 {
		t.Fatalf("sync nach Neukoppeln: Exit %d %s", code, errOut)
	}

	f2 := newFakeServer(t)
	mustConnect(t, cfg, data, f2)
	b = pairBody(t, f2, 0)
	if b["sign_public_key"] != st.SignPublicKey || !slices.Equal(previousOf(b), []string{old}) {
		t.Fatalf("Pair am zweiten Server %v", b)
	}
}

// Ein gescheitertes Koppeln (Server bestätigt eine andere CA, Speichern der Verbindung scheitert) lässt den
// Signaturschlüssel stehen.
func TestFailedConnectKeepsSignKey(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	key := readSignFile(t, data)
	st, _ := state.Load(data)
	pub := st.SignPublicKey

	f.pairFP = client.Fingerprint(testpki.NewRoot(t).DER())
	if code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint()); code != 1 {
		t.Fatalf("andere CA: Exit %d: %s", code, out)
	}
	f.pairFP = ""
	failConnSave(t)
	if code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint()); code != 1 {
		t.Fatalf("Speichern scheitert: Exit %d: %s", code, out)
	}
	if readSignFile(t, data) != key {
		t.Fatal("Signaturschlüssel nach gescheitertem Koppeln anders")
	}
	if st, _ := state.Load(data); st.SignPublicKey != pub || len(st.SignPublicKeysPrevious) != 0 {
		t.Fatalf("Merkzettel %q %v", st.SignPublicKey, st.SignPublicKeysPrevious)
	}
}

// Scheitert das Ablegen des neuen Schlüssels, wird nicht gekoppelt.
func TestConnectSignKeySaveFailure(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	old := saveSignKey
	saveSignKey = func(string, ed25519.PrivateKey) (string, error) { return "", errors.New("Platte voll") }
	t.Cleanup(func() { saveSignKey = old })
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint())
	if code != 1 || !strings.Contains(out, "Platte voll") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if f.pairCalls.Load() != 0 {
		t.Fatal("trotzdem gekoppelt")
	}
	if st, _ := state.Load(data); st.SignPublicKey != "" {
		t.Fatal("öffentlicher Teil ohne abgelegten privaten gemerkt")
	}
}

// Ist der Schlüsselspeicher mit dem gemerkten Schlüssel nicht lesbar, entsteht kein neuer und es wird nicht gekoppelt.
func TestConnectSignKeyStoreUnreadable(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	st, _ := state.Load(data)
	st.SignPublicKey, st.SignKeyStore = "O2onvM62pC1io6jQKm8Nc2UyFXcd4kOmOsBIoYtZ2ik=", "keyring"
	st.Save(data)
	keyring.MockInitWithError(os.ErrPermission)
	t.Cleanup(keyring.MockInit)
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint())
	if code != 1 || !strings.Contains(out, "Signaturschlüssel nicht lesbar") {
		t.Fatalf("Exit %d: %s", code, out)
	}
	if f.pairCalls.Load() != 0 {
		t.Fatal("trotzdem gekoppelt")
	}
	if st, _ := state.Load(data); st.SignPublicKey != "O2onvM62pC1io6jQKm8Nc2UyFXcd4kOmOsBIoYtZ2ik=" || len(st.SignPublicKeysPrevious) != 0 {
		t.Fatal("Schlüssel ersetzt")
	}
}

// sign_keys_refused: connect koppelt und warnt.
func TestConnectWarnsSignKeysRefused(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	st, _ := state.Load(data)
	f.signRefused = []string{st.SignPublicKey}
	out := mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "ist auf dem Server nicht übernommen: Er gehört dort einem anderen Gerät.") {
		t.Fatalf("keine Warnung:\n%s", out)
	}
	// Der aktuelle Schlüssel wird nie als fremder früherer gemerkt: Seine Zeilen bleiben offen (Meldung zum Neukoppeln).
	if st, _ := state.Load(data); len(st.SignKeysRefused) != 0 {
		t.Fatalf("sign_keys_refused %v", st.SignKeysRefused)
	}
}

// Ein abgewiesener früherer Schlüssel: connect merkt ihn und sagt, dass ältere Zeilen damit nicht mehr mitgehen; das
// nächste Koppeln ersetzt die Liste durch die Antwort des Servers.
func TestConnectRemembersRefusedPreviousKey(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	st, _ := state.Load(data)
	old := st.SignPublicKey
	os.Remove(filepath.Join(data, "sign.key"))
	f.signRefused = []string{old, "O2onvM62pC1io6jQKm8Nc2UyFXcd4kOmOsBIoYtZ2ik="}
	out := mustConnect(t, cfg, data, f)
	if !strings.Contains(out, "Hinweis: "+deliver.ForeignMessage(3)) || strings.Contains(out, "prüft der Server weiter") {
		t.Fatalf("kein Hinweis:\n%s", out)
	}
	if st, _ := state.Load(data); !slices.Equal(st.SignKeysRefused, []string{old}) {
		t.Fatalf("sign_keys_refused %v", st.SignKeysRefused)
	}
	f.signRefused = nil
	mustConnect(t, cfg, data, f)
	if st, _ := state.Load(data); len(st.SignKeysRefused) != 0 {
		t.Fatalf("nach erneutem Koppeln %v", st.SignKeysRefused)
	}
}

// status zeigt Ablage und Zahl der früheren Schlüssel, nie den Schlüssel selbst.
func TestStatusShowsSignKey(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f)
	var out bytes.Buffer
	runStatus(nil, &out, data, cfg)
	if !strings.Contains(out.String(), "Signaturschlüssel: Datei nur für den Benutzer\n") {
		t.Fatalf("status:\n%s", out.String())
	}
	if strings.Contains(out.String(), strings.TrimSpace(readSignFile(t, data))) {
		t.Fatal("privater Schlüssel in der Ausgabe")
	}
	st, _ := state.Load(data)
	st.SetSignPublicKey("O2onvM62pC1io6jQKm8Nc2UyFXcd4kOmOsBIoYtZ2ik=", protocol.MaxSignKeysPrevious)
	st.Save(data)
	out.Reset()
	runStatus(nil, &out, data, cfg)
	if !strings.Contains(out.String(), "(1 früherer)") {
		t.Fatalf("status:\n%s", out.String())
	}
}

// Scheitert der erste Abgleich, nennt connect den fremd gehaltenen früheren Schlüssel ohne Zahl, aber mit Ausweg.
func TestMsgRefusedPreviousNamesWayOut(t *testing.T) {
	for _, want := range []string{"anderen Gerät", "zusammen", "danach dieses Gerät neu", "dann gehen sie mit"} {
		if !strings.Contains(msgRefusedPrevious, want) {
			t.Fatalf("%q fehlt in %q", want, msgRefusedPrevious)
		}
	}
}

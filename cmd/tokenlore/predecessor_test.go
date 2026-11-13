package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tokenlore/tokenlore/client"
)

// Erfundenes Gerät A und ein fester Zeitpunkt.
var (
	testNow  = time.Date(2026, 11, 12, 10, 15, 0, 0, time.UTC)
	twoHours = testNow.Add(-2 * time.Hour)
	weeks    = testNow.Add(-21 * 24 * time.Hour)
)

func question(recent bool, lastSeen *time.Time, signMatch bool) *client.PredecessorQuestionError {
	rec := client.AnswerSame
	if recent {
		rec = client.AnswerNew
	}
	return &client.PredecessorQuestionError{
		Predecessor: client.PredecessorDevice{DeviceID: 7, Name: "Laptop", LastSeenAt: lastSeen, OS: "linux", Arch: "amd64",
			Recent: recent, SignKeyMatch: signMatch},
		Recommended: rec,
	}
}

var testCmd = connectCommand{URL: "https://tokenlore.example.org", Code: "123456"}

// labelsOf sind die Labels der Optionen in ihrer Reihenfolge.
func labelsOf(p predecessorQuestionPrompt) string {
	var l []string
	for _, o := range p.Options {
		l = append(l, o.Label)
	}
	return strings.Join(l, "\n")
}

// A hat vor 2 Stunden geliefert: kurze Frage mit Warnung, „Nein“ empfohlen und zuerst, bei „Ja“ der Hinweis, dass
// dieser Rechner „Laptop“ wird und der andere Rechner nicht mehr liefert; anderes Betriebssystem wird genannt.
func TestPredecessorPromptRecent(t *testing.T) {
	p := predecessorPrompt(question(true, &twoHours, false), testCmd, "darwin", "arm64", testNow)
	for _, want := range []string{
		"Dieser Ordner stammt von „Laptop“ (Gerät 7)", "vor 2 Stunden", "scheint noch in Gebrauch",
		"„Laptop“ lief auf Linux, amd64, dieser Rechner läuft auf macOS, arm64.",
		"Ist dieser Rechner trotzdem das alte Gerät „Laptop“?",
	} {
		if !strings.Contains(p.Question, want) {
			t.Errorf("fehlt %q in:\n%s", want, p.Question)
		}
	}
	if strings.Contains(p.Question, "Signaturschlüssel") || strings.Count(p.Question, "\n") > 3 {
		t.Errorf("Frage zu lang oder falscher Hinweis:\n%s", p.Question)
	}
	want := []promptOption{
		{"Nein: eigenes Gerät, „Laptop“ liefert weiter (empfohlen)", "neu"},
		{"Ja: dieser Rechner wird „Laptop“, der andere Rechner liefert dann nicht mehr", "gleich"},
		{"Später: eigenes Gerät, auf der Webseite entscheiden", "spaeter"},
	}
	for i, o := range want {
		if i >= len(p.Options) || p.Options[i] != o {
			t.Errorf("Option %d: %+v, erwartet %+v", i, p.Options, o)
		}
	}
	if !strings.Contains(p.commands[0], "--vorgaenger neu") {
		t.Errorf("Befehle in anderer Reihenfolge als die Optionen: %v", p.commands)
	}
}

// A ist seit 3 Wochen still: „Ja“ empfohlen und zuerst, keine Warnung, gleiches Betriebssystem nicht genannt.
func TestPredecessorPromptQuiet(t *testing.T) {
	p := predecessorPrompt(question(false, &weeks, true), testCmd, "linux", "amd64", testNow)
	for _, want := range []string{
		"Dieser Ordner stammt von „Laptop“ (Gerät 7), zuletzt geliefert vor 21 Tagen.",
		"Dieser Rechner hat den Signaturschlüssel von „Laptop“.",
		"Ist dieser Rechner das alte Gerät „Laptop“?",
	} {
		if !strings.Contains(p.Question, want) {
			t.Errorf("fehlt %q in:\n%s", want, p.Question)
		}
	}
	if strings.Contains(p.Question, "scheint noch in Gebrauch") || strings.Contains(p.Question, "lief auf") {
		t.Errorf("Frage:\n%s", p.Question)
	}
	l := labelsOf(p)
	for i, v := range []string{"gleich", "neu", "spaeter"} {
		if len(p.Options) != 3 || p.Options[i].Value != v {
			t.Fatalf("Optionen %+v", p.Options)
		}
	}
	if p.Options[0].Label != "Ja: dieser Rechner wird „Laptop“, alle Zahlen bleiben (empfohlen)" ||
		p.Options[1].Label != "Nein: eigenes Gerät, „Laptop“ bleibt" || strings.Count(l, "(empfohlen)") != 1 {
		t.Errorf("Labels:\n%s", l)
	}
	if utf8.RuneCountInString(p.Header) > 12 || p.Header == "" {
		t.Errorf("Kopf %q", p.Header)
	}
	// Der Text ohne Fenster nennt je Antwort Label und Befehl.
	if txt := p.text(); !strings.Contains(txt, "- "+p.Options[1].Label+"\n  /tokenlore:connect") {
		t.Errorf("Text:\n%s", txt)
	}
}

func TestPredecessorPromptNeverDelivered(t *testing.T) {
	p := predecessorPrompt(question(false, nil, false), testCmd, "linux", "amd64", testNow)
	if !strings.Contains(p.Question, "„Laptop“ (Gerät 7), das nie geliefert hat.") {
		t.Fatalf("Frage:\n%s", p.Question)
	}
}

// Die drei Befehle wiederholen Adresse, Code, --ca und einen gewählten Namen.
func TestPredecessorCommands(t *testing.T) {
	c := connectCommand{URL: "https://tokenlore.example.org", Code: "123456", CA: "AB:CD", Name: "Mein PC"}
	p := predecessorPrompt(question(false, nil, false), c, "linux", "amd64", testNow)
	for _, v := range []string{"gleich", "neu", "spaeter"} {
		want := `/tokenlore:connect https://tokenlore.example.org 123456 --ca AB:CD --name "Mein PC" --vorgaenger ` + v
		if !strings.Contains(p.NotPaired, want) || !strings.Contains(p.text(), want) {
			t.Errorf("Befehl fehlt: %s\n%s", want, p.text())
		}
	}
	if !strings.Contains(p.NotPaired, "Nicht gekoppelt") || !strings.Contains(p.text(), "Code bleibt") {
		t.Errorf("Meldungen:\n%s\n%s", p.NotPaired, p.text())
	}
}

func TestParsePredecessorAnswer(t *testing.T) {
	ok := map[string]string{"gleich": "same", "Gleich": "same", "neu": "new", "spaeter": "later", "später": "later", "SPÄTER": "later"}
	for in, want := range ok {
		if got, err := parsePredecessorAnswer(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"ja", "", "same", "nein"} {
		if _, err := parsePredecessorAnswer(in); err == nil {
			t.Errorf("%q: kein Fehler", in)
		}
	}
}

const questionBody = `{"error":"predecessor_question","predecessor":{"device_id":7,"name":"Laptop","last_seen_at":null,"os":"linux","arch":"amd64","recent":false,"sign_key_match":false},"recommended":"same"}`

// forkedEnv legt einen Ordner an, der von einem anderen Rechner stammt: connect zweigt die Kennung ab, fixedID wird
// Vorgänger.
func forkedEnv(t *testing.T) (cfg, data string) {
	t.Helper()
	cfg, data = connectEnv(t)
	writeIdentityFile(t, cfg, `{"id":"`+fixedID+`","machine":"`+machineA+`","predecessor":""}`)
	setMachine(t, machineB)
	return cfg, data
}

func lastPairBody(t *testing.T, f *fakeServer) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.pairBodies) == 0 {
		t.Fatal("keine Kopplung")
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(f.pairBodies[len(f.pairBodies)-1]), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

// Kennungsdatei mit Vorgänger: Die Anfrage nennt predecessor_source, der Server fragt, connect druckt die Frage mit
// Exit 3 und speichert nichts. Mit --vorgaenger gleich koppelt es neu und meldet das bestehende Gerät.
func TestConnectPredecessorQuestionThenSame(t *testing.T) {
	cfg, data := forkedEnv(t)
	f := newFakeServer(t)
	f.features = []string{"predecessor"}
	f.question = questionBody
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--name", "x")
	if code != 3 || !strings.Contains(out, "Ist dieser Rechner das alte Gerät „Laptop“?") || !strings.Contains(out, "--vorgaenger gleich") {
		t.Fatalf("Exit %d:\n%s", code, out)
	}
	t.Log(out)
	if got := lastPairBody(t, f)["predecessor_source"]; got != sourceFor(t, fixedID, f) {
		t.Fatalf("predecessor_source %v", got)
	}
	if _, ok := lastPairBody(t, f)["predecessor_answer"]; ok {
		t.Fatal("Antwort ohne Frage gesendet")
	}
	assertNothingStored(t, data)

	f.repaired = true
	out = mustConnect(t, cfg, data, f, "--vorgaenger", "Gleich")
	b := lastPairBody(t, f)
	if b["predecessor_answer"] != "same" || b["predecessor_source"] != sourceFor(t, fixedID, f) {
		t.Fatalf("zweiter Lauf: %v", b)
	}
	if !strings.Contains(out, "Bestehendes Gerät neu gekoppelt") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
	// Der zweite Lauf behält die abgezweigte Kennung: dieselbe device_source wie beim ersten.
	if sentSource(t, f) != sourceFor(t, identityFile(t, cfg).ID, f) || identityFile(t, cfg).Predecessor != fixedID {
		t.Fatal("Kennung zwischen den Läufen geändert")
	}
}

// --frage-json: die Frage als JSON für das Auswahlfenster, Exit 3.
func TestConnectPredecessorQuestionJSON(t *testing.T) {
	cfg, data := forkedEnv(t)
	f := newFakeServer(t)
	f.features = []string{"predecessor"}
	f.question = questionBody
	code, out := connect(t, data, cfg, f.srv.URL, "123456", "--ca", f.fingerprint(), "--frage-json")
	if code != 3 {
		t.Fatalf("Exit %d:\n%s", code, out)
	}
	var p struct {
		Question  string `json:"question"`
		Header    string `json:"header"`
		Options   []struct{ Label, Value string }
		NotPaired string `json:"not_paired"`
		Before    string `json:"before"`
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("kein JSON: %v\n%s", err, out)
	}
	if !strings.Contains(p.Question, "Ist dieser Rechner das alte Gerät „Laptop“?") || len(p.Options) != 3 || p.Options[0].Value != "gleich" ||
		!strings.HasSuffix(p.Options[0].Label, "(empfohlen)") || !strings.Contains(p.NotPaired, "--vorgaenger spaeter") {
		t.Fatalf("JSON %+v", p)
	}
	// Die Meldung zur Kennungsdatei geht nicht verloren, sie steht vor der Frage.
	if !strings.Contains(p.Before, "dieser Rechner bekommt eine eigene Kennung") {
		t.Fatalf("before %q", p.Before)
	}
	assertNothingStored(t, data)
}

// Ohne Frage gibt --frage-json dieselbe Ausgabe wie ohne.
func TestConnectQuestionJSONWithoutQuestion(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	out := mustConnect(t, cfg, data, f, "--frage-json")
	if !strings.Contains(out, "Verbunden mit") || strings.HasPrefix(out, "{") {
		t.Fatalf("Ausgabe:\n%s", out)
	}
}

// Server ohne Merkmal predecessor: keine Felder zum Vorgänger, auch nicht mit --vorgaenger; Koppeln wie bisher.
func TestConnectPredecessorOldServer(t *testing.T) {
	cfg, data := forkedEnv(t)
	f := newFakeServer(t)
	mustConnect(t, cfg, data, f, "--vorgaenger", "neu")
	for _, k := range []string{"predecessor_source", "predecessor_answer"} {
		if _, ok := lastPairBody(t, f)[k]; ok {
			t.Fatalf("%s an alten Server gesendet", k)
		}
	}
}

// Ohne Vorgänger kein predecessor_source; eine Antwort geht trotzdem mit (der Server übergeht sie).
func TestConnectWithoutPredecessor(t *testing.T) {
	cfg, data := connectEnv(t)
	f := newFakeServer(t)
	f.features = []string{"predecessor"}
	mustConnect(t, cfg, data, f, "--vorgaenger=neu")
	b := lastPairBody(t, f)
	if _, ok := b["predecessor_source"]; ok || b["predecessor_answer"] != "new" {
		t.Fatalf("Anfrage %v", b)
	}
}

// Ungültige Antwort: klare Meldung, Exit 2, keine Anfrage.
func TestConnectPredecessorAnswerInvalid(t *testing.T) {
	for _, args := range [][]string{{"--vorgaenger", "ja"}, {"--vorgaenger="}, {"--vorgaenger"}} {
		cfg, data := connectEnv(t)
		f := newFakeServer(t)
		code, out := connect(t, data, cfg, append([]string{f.srv.URL, "123456", "--ca", f.fingerprint()}, args...)...)
		if code != 2 || !strings.Contains(out, "vorgaenger") || f.pairCalls.Load() != 0 {
			t.Errorf("%v: Exit %d: %s", args, code, out)
		}
		if !strings.Contains(out, "gleich, neu oder spaeter") {
			t.Errorf("%v: Meldung %s", args, out)
		}
	}
}

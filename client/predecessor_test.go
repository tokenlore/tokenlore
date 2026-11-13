package client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tokenlore/tokenlore/internal/testpki"
)

// Erfundene Kennung des Vorgängers für diesen Server.
const testPredSource = "6666666666666666666666666666666666666666666666666666666666666666"

// features aus GET /api/plugin/info: bekannte und unbekannte Einträge kommen an, fehlt das Feld, ist die Liste leer.
func TestInfoFeatures(t *testing.T) {
	root := testpki.NewRoot(t)
	cases := map[string]bool{
		`{"server_id":"` + testServerID + `","features":["predecessor","x"]}`: true,
		`{"server_id":"` + testServerID + `","features":["x"]}`:               false,
		`{"server_id":"` + testServerID + `"}`:                                false,
		`{"server_id":"` + testServerID + `","features":"predecessor"}`:       false,
		`{"server_id":"` + testServerID + `","features":[7,"predecessor"]}`:   false,
	}
	for body, want := range cases {
		srv := statusServer(t, root, 200, nil, body)
		info, err := newClient(t, srv.URL, root, 5*time.Second).Info(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if got := info.Has(FeaturePredecessor); got != want {
			t.Errorf("%s: Has=%v", body, got)
		}
	}
}

// predecessor_source und predecessor_answer gehen nur mit, wenn gesetzt.
func TestPairSendsPredecessor(t *testing.T) {
	root := testpki.NewRoot(t)
	good := `{"device_id":7,"device_key":"` + testKey + `"}`
	var sent string
	srv := pairServer(t, root, 200, good, &sent)
	c := newClient(t, srv.URL, root, 5*time.Second)
	if _, err := c.Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign, PairPredecessor{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sent, "predecessor") {
		t.Fatalf("ohne Vorgänger gesendet: %s", sent)
	}
	if _, err := c.Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign,
		PairPredecessor{Source: testPredSource, Answer: AnswerSame}); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.Unmarshal([]byte(sent), &body)
	if body["predecessor_source"] != testPredSource || body["predecessor_answer"] != "same" {
		t.Fatalf("gesendet: %s", sent)
	}
	// Ungültige Werte gehen nie raus.
	for _, p := range []PairPredecessor{{Source: "abc"}, {Answer: "Same"}, {Answer: "ja"}} {
		if _, err := c.Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign, p); err == nil {
			t.Errorf("%+v: kein Fehler", p)
		}
	}
}

// 409 predecessor_question wird ein eigener Fehler mit den Angaben zu A; Texte des Servers sind bereinigt.
func TestPairPredecessorQuestion(t *testing.T) {
	root := testpki.NewRoot(t)
	body := `{"error":"predecessor_question","predecessor":{"device_id":7,"name":"Lap\u001btop","last_seen_at":"2026-11-12T08:15:00Z","os":"linux","arch":"amd64","recent":true,"sign_key_match":true},"recommended":"new"}`
	srv := pairServer(t, root, 409, body, nil)
	_, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign, PairPredecessor{Source: testPredSource})
	var q *PredecessorQuestionError
	if !errors.As(err, &q) {
		t.Fatalf("kein PredecessorQuestionError: %#v", err)
	}
	p := q.Predecessor
	if p.DeviceID != 7 || p.Name != "Laptop" || p.OS != "linux" || p.Arch != "amd64" || !p.Recent || !p.SignKeyMatch || q.Recommended != AnswerNew {
		t.Fatalf("Frage %+v", q)
	}
	if p.LastSeenAt == nil || !p.LastSeenAt.Equal(time.Date(2026, 11, 12, 8, 15, 0, 0, time.UTC)) {
		t.Fatalf("last_seen_at %v", p.LastSeenAt)
	}
}

// Ohne last_seen_at hat A nie geliefert; eine fehlende oder unbekannte Empfehlung folgt aus recent.
func TestPairPredecessorQuestionDefaults(t *testing.T) {
	root := testpki.NewRoot(t)
	cases := map[string]string{
		`{"error":"predecessor_question","predecessor":{"device_id":3,"name":"A","last_seen_at":null,"os":"x\u0007","arch":"","recent":false,"sign_key_match":false}}`: AnswerSame,
		`{"error":"predecessor_question","predecessor":{"device_id":3,"name":"A","recent":true},"recommended":"later"}`:                                                AnswerNew,
		`{"error":"predecessor_question","predecessor":{"device_id":3,"name":"A","last_seen_at":"gestern","recent":false},"recommended":"same"}`:                       AnswerSame,
	}
	for body, want := range cases {
		srv := pairServer(t, root, 409, body, nil)
		_, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign, PairPredecessor{Source: testPredSource})
		var q *PredecessorQuestionError
		if !errors.As(err, &q) || q.Recommended != want || q.Predecessor.LastSeenAt != nil {
			t.Errorf("%s: %#v", body, err)
			continue
		}
		if strings.ContainsRune(q.Predecessor.OS, 7) {
			t.Errorf("Betriebssystem nicht bereinigt: %q", q.Predecessor.OS)
		}
	}
}

// Eine Frage ohne gültige Nummer von A ist keine Frage; 409 source_taken bleibt wie bisher.
func TestPairPredecessorQuestionInvalid(t *testing.T) {
	root := testpki.NewRoot(t)
	for _, body := range []string{
		`{"error":"predecessor_question"}`,
		`{"error":"predecessor_question","predecessor":{"device_id":0,"name":"A"}}`,
		`{"error":"predecessor_question","predecessor":{"device_id":3,"name":""}}`,
	} {
		srv := pairServer(t, root, 409, body, nil)
		_, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign, PairPredecessor{})
		var q *PredecessorQuestionError
		if err == nil || errors.As(err, &q) {
			t.Errorf("%s: %#v", body, err)
		}
	}
	srv := pairServer(t, root, 409, `{"error":"source_taken"}`, nil)
	_, err := newClient(t, srv.URL, root, 5*time.Second).Pair(context.Background(), "c", "n", "linux", "amd64", testSource, testServerID, false, testSign, PairPredecessor{})
	if !errors.Is(err, ErrSourceTaken) {
		t.Fatalf("source_taken: %#v", err)
	}
}

package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// makeRequests baut n Hüllen; jede Inhaltszeile ist extras*64 Zeichen länger als nötig.
func makeRequests(n, sessions, extras int) ([]Envelope, []string, []Session) {
	var rs []Envelope
	var of []string
	var ss []Session
	for s := 0; s < sessions; s++ {
		ss = append(ss, Session{SessionID: fmt.Sprintf("s%d", s)})
	}
	for i := 0; i < n; i++ {
		sid := fmt.Sprintf("s%d", i*sessions/n)
		z := fmt.Sprintf(`{"kind":"request","request_id":"req_%d","message_id":"msg_%d","session_id":%q,"pad":%q}`,
			i, i, sid, strings.Repeat("x", extras*MaxExtraString))
		rs = append(rs, Envelope{Z: z, S: strings.Repeat("A", 86) + "=="})
		of = append(of, sid)
	}
	return rs, of, ss
}

func sessionsOfPart(p Batch, of map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, r := range p.Requests {
		out[of[r.Z]] = true
	}
	return out
}

func TestSplitByCount(t *testing.T) {
	rs, of, ss := makeRequests(4500, 3, 0)
	parts, err := Split(Batch{V: Version, Sessions: ss, Requests: rs}, of)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("Pakete: got %d, want 3", len(parts))
	}
	for i, want := range []int{2000, 2000, 500} {
		if len(parts[i].Requests) != want {
			t.Errorf("Paket %d: %d Anfragen, want %d", i, len(parts[i].Requests), want)
		}
		if parts[i].V != 2 {
			t.Errorf("Paket %d: V=%d", i, parts[i].V)
		}
	}
	zOf := map[string]string{}
	for i, r := range rs {
		zOf[r.Z] = of[i]
	}
	seen := map[string]int{}
	k := 0
	for i, p := range parts {
		// Reihenfolge bleibt: jedes Paket ist der nächste zusammenhängende Abschnitt.
		for _, r := range p.Requests {
			if r != rs[k] {
				t.Fatalf("Paket %d: Hülle %d nicht in Reihenfolge", i, k)
			}
			k++
		}
		in := sessionsOfPart(p, zOf)
		for _, s := range p.Sessions {
			seen[s.SessionID]++
			if !in[s.SessionID] {
				t.Errorf("Sitzung %s in Paket %d ohne eigene Anfrage", s.SessionID, i)
			}
			// erste Anfrage der Sitzung muss in diesem Paket liegen
			for j := 0; j < i; j++ {
				if sessionsOfPart(parts[j], zOf)[s.SessionID] {
					t.Errorf("Sitzung %s steht in Paket %d, erste Anfrage aber in Paket %d", s.SessionID, i, j)
				}
			}
		}
	}
	for _, s := range ss {
		if seen[s.SessionID] != 1 {
			t.Errorf("Sitzung %s %d-mal enthalten", s.SessionID, seen[s.SessionID])
		}
	}
}

func TestSplitBySize(t *testing.T) {
	rs, of, ss := makeRequests(1500, 1, 15)
	parts, err := Split(Batch{V: Version, Sessions: ss, Requests: rs}, of)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for i, p := range parts {
		b, _ := json.Marshal(p)
		if len(b) > MaxBatchBytes {
			t.Errorf("Paket %d: %d Byte > %d", i, len(b), MaxBatchBytes)
		}
		total += len(p.Requests)
	}
	if total != 1500 {
		t.Fatalf("Summe Anfragen %d, want 1500", total)
	}
	if len(parts) < 2 {
		t.Fatalf("erwartet Aufteilung nach Größe, got %d Paket(e)", len(parts))
	}
}

func TestSplitAccountsOnlyFirst(t *testing.T) {
	rs, of, ss := makeRequests(4100, 2, 0)
	var days []Envelope
	for i := 0; i < MaxLegacyList+5; i++ {
		days = append(days, Envelope{Z: fmt.Sprintf(`{"kind":"day","date":"d%d"}`, i), S: "s"})
	}
	b := Batch{V: Version, Sessions: ss, Requests: rs, LegacyDays: days,
		Accounts: []Account{{AccountID: "a", EmailMode: "off"}}}
	parts, err := Split(b, of)
	if err != nil {
		t.Fatal(err)
	}
	nDays, nReq := 0, 0
	for i, p := range parts {
		if (len(p.Accounts) > 0) != (i == 0) {
			t.Errorf("Paket %d: Konten %d", i, len(p.Accounts))
		}
		if len(p.LegacyDays) > MaxLegacyList {
			t.Errorf("Paket %d: %d Tage", i, len(p.LegacyDays))
		}
		// Tage stehen vor den Anfragen: Ein Paket mit Tagen folgt keinem mit Anfragen.
		if len(p.LegacyDays) > 0 && nReq > 0 {
			t.Errorf("Paket %d: Tage nach Anfragen", i)
		}
		for j, d := range p.LegacyDays {
			if d != days[nDays+j] {
				t.Fatalf("Paket %d: Tag nicht in Reihenfolge", i)
			}
		}
		nDays += len(p.LegacyDays)
		nReq += len(p.Requests)
	}
	if nDays != len(days) || nReq != len(rs) {
		t.Fatalf("Tage %d/%d, Anfragen %d/%d", nDays, len(days), nReq, len(rs))
	}
}

func TestSplitSingleRequestTooLarge(t *testing.T) {
	r := Envelope{Z: strings.Repeat("x", MaxBatchBytes), S: "s"}
	if _, err := Split(Batch{V: Version, Requests: []Envelope{r}}, []string{"s"}); err == nil {
		t.Fatal("erwartet Fehler bei übergroßer Einzelanfrage")
	}
	if _, err := Split(Batch{V: Version, Requests: []Envelope{{Z: "z"}}}, nil); err == nil {
		t.Fatal("erwartet Fehler, wenn Sitzungen und Anfragen nicht zusammenpassen")
	}
}

func TestSplitManySessionsWithoutRequests(t *testing.T) {
	var ss []Session
	for i := 0; i < 20000; i++ {
		ss = append(ss, Session{SessionID: fmt.Sprintf("sitzung-ohne-anfrage-%06d", i), CCVersion: "2.1.309",
			AccountID: "00000000-0000-0000-0000-000000000000"})
	}
	parts, err := Split(Batch{V: Version, Sessions: ss}, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for i, p := range parts {
		b, _ := json.Marshal(p)
		if len(b) > MaxBatchBytes {
			t.Errorf("Paket %d: %d Byte > %d", i, len(b), MaxBatchBytes)
		}
		n += len(p.Sessions)
	}
	if n != 20000 {
		t.Fatalf("Sitzungen %d, want 20000", n)
	}
}

func TestSplitCapsSessionsPerBatch(t *testing.T) {
	const n = MaxSessionsPerBatch + 1
	check := func(name string, b Batch, of []string) {
		t.Helper()
		parts, err := Split(b, of)
		if err != nil {
			t.Fatal(name, err)
		}
		seen := map[string]int{}
		for i, p := range parts {
			if len(p.Sessions) > MaxSessionsPerBatch {
				t.Errorf("%s: Paket %d hat %d Sitzungen", name, i, len(p.Sessions))
			}
			for _, s := range p.Sessions {
				seen[s.SessionID]++
			}
		}
		if len(seen) != n {
			t.Fatalf("%s: %d verschiedene Sitzungen, want %d", name, len(seen), n)
		}
		for id, c := range seen {
			if c != 1 {
				t.Fatalf("%s: Sitzung %s %d-mal", name, id, c)
			}
		}
	}
	var ss []Session
	for i := 0; i < n; i++ {
		ss = append(ss, Session{SessionID: fmt.Sprintf("s%d", i)})
	}
	check("ohne Anfragen", Batch{V: Version, Sessions: ss}, nil)

	var rs []Envelope
	var of []string
	for i := 0; i < n; i++ {
		rs = append(rs, Envelope{Z: fmt.Sprintf("r%d", i), S: "s"})
		of = append(of, fmt.Sprintf("s%d", i))
	}
	check("mit Anfragen", Batch{V: Version, Sessions: ss, Requests: rs}, of)
}

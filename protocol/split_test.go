package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func makeRequests(n, sessions, extras int) ([]Request, []Session) {
	var rs []Request
	var ss []Session
	for s := 0; s < sessions; s++ {
		ss = append(ss, Session{SessionID: fmt.Sprintf("s%d", s)})
	}
	for i := 0; i < n; i++ {
		r := Request{
			RequestID: fmt.Sprintf("req_%d", i), MessageID: fmt.Sprintf("msg_%d", i),
			SessionID: fmt.Sprintf("s%d", i*sessions/n),
			TS:        time.Date(2026, 10, 1, 0, 0, i, 0, time.UTC), Model: "claude-opus-5-5",
		}
		if extras > 0 {
			r.Extra = map[string]any{}
			for e := 0; e < extras; e++ {
				r.Extra[fmt.Sprintf("feld_%02d", e)] = strings.Repeat("x", MaxExtraString)
			}
		}
		rs = append(rs, r)
	}
	return rs, ss
}

func TestSplitByCount(t *testing.T) {
	rs, ss := makeRequests(4500, 3, 0)
	parts, err := Split(Batch{V: Version, Sessions: ss, Requests: rs})
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
		if parts[i].V != 1 {
			t.Errorf("Paket %d: V=%d", i, parts[i].V)
		}
	}
	seen := map[string]int{}
	for i, p := range parts {
		for _, s := range p.Sessions {
			seen[s.SessionID]++
			found := false
			for _, r := range p.Requests {
				if r.SessionID == s.SessionID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Sitzung %s in Paket %d ohne eigene Anfrage", s.SessionID, i)
			}
			// erste Anfrage der Sitzung muss in diesem Paket liegen
			for j := 0; j < i; j++ {
				for _, r := range parts[j].Requests {
					if r.SessionID == s.SessionID {
						t.Errorf("Sitzung %s steht in Paket %d, erste Anfrage aber in Paket %d", s.SessionID, i, j)
					}
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
	rs, ss := makeRequests(1500, 1, 15)
	parts, err := Split(Batch{V: Version, Sessions: ss, Requests: rs})
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
	rs, ss := makeRequests(4100, 2, 0)
	b := Batch{V: Version, Sessions: ss, Requests: rs,
		Accounts:       []Account{{AccountID: "a", EmailMode: "off"}},
		LegacyModel:    []LegacyModelDay{{Date: "2026-08-14", Model: "m", Tokens: 1}},
		LegacyActivity: []LegacyActivityDay{{Date: "2026-08-14", Messages: 1}},
	}
	parts, err := Split(b)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range parts {
		has := len(p.Accounts) > 0 || len(p.LegacyModel) > 0 || len(p.LegacyActivity) > 0
		if i == 0 && !has {
			t.Error("Paket 0 ohne Konten/Altbestand")
		}
		if i > 0 && has {
			t.Errorf("Paket %d enthält Konten/Altbestand", i)
		}
	}
}

func TestSplitSingleRequestTooLarge(t *testing.T) {
	r := Request{RequestID: "r", MessageID: "m", SessionID: "s", Model: "x",
		Extra: map[string]any{"riesig": strings.Repeat("x", MaxBatchBytes)}}
	if _, err := Split(Batch{V: Version, Requests: []Request{r}}); err == nil {
		t.Fatal("erwartet Fehler bei übergroßer Einzelanfrage")
	}
}

func TestSplitManySessionsWithoutRequests(t *testing.T) {
	var ss []Session
	for i := 0; i < 20000; i++ {
		ss = append(ss, Session{SessionID: fmt.Sprintf("sitzung-ohne-anfrage-%06d", i), CCVersion: "2.1.309",
			ProjectID: strings.Repeat("a", 64), AccountID: "00000000-0000-0000-0000-000000000000"})
	}
	parts, err := Split(Batch{V: Version, Sessions: ss})
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
	check := func(name string, b Batch) {
		t.Helper()
		parts, err := Split(b)
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
	check("ohne Anfragen", Batch{V: Version, Sessions: ss})

	var rs []Request
	for i := 0; i < n; i++ {
		rs = append(rs, Request{RequestID: fmt.Sprintf("r%d", i), MessageID: fmt.Sprintf("m%d", i),
			SessionID: fmt.Sprintf("s%d", i), TS: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Model: "m"})
	}
	check("mit Anfragen", Batch{V: Version, Sessions: ss, Requests: rs})
}

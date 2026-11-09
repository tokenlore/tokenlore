package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// headerReserve ist der Platz, der pro Paket für Kopf und JSON-Rahmen frei bleibt.
const headerReserve = 4096

// Split teilt eine Lieferung in Pakete, die die Grenzen aus limits.go einhalten. sessionOf[i] ist die Sitzung der
// Hülle b.Requests[i]. Konten kommen ins erste Paket, die Vorgeschichte-Tage vor die Anfragen, eine Sitzung in das
// Paket ihrer ersten Anfrage. Die Reihenfolge der Hüllen bleibt: Jedes Paket trägt einen zusammenhängenden Abschnitt
// von b.LegacyDays und b.Requests, und die Abschnitte folgen aufeinander.
func Split(b Batch, sessionOf []string) ([]Batch, error) {
	if len(sessionOf) != len(b.Requests) {
		return nil, errors.New("Split: Sitzungen passen nicht zu den Anfragen")
	}
	first := Batch{V: b.V, Device: b.Device, Accounts: b.Accounts}
	firstSize, err := jsonSize(first)
	if err != nil {
		return nil, err
	}
	baseSize, err := jsonSize(Batch{V: b.V, Device: b.Device})
	if err != nil {
		return nil, err
	}

	sessions := make(map[string]Session, len(b.Sessions))
	for _, s := range b.Sessions {
		sessions[s.SessionID] = s
	}
	placed := make(map[string]bool, len(b.Sessions))

	parts := []Batch{first}
	size := firstSize + headerReserve
	newPart := func() *Batch {
		parts = append(parts, Batch{V: b.V, Device: b.Device})
		size = baseSize + headerReserve
		return &parts[len(parts)-1]
	}
	for i, d := range b.LegacyDays {
		cost, err := jsonSize(d)
		if err != nil {
			return nil, err
		}
		cost++
		if baseSize+headerReserve+cost > MaxBatchBytes {
			return nil, fmt.Errorf("Vorgeschichte-Tag %d ist allein größer als %d Byte", i, MaxBatchBytes)
		}
		cur := &parts[len(parts)-1]
		if len(cur.LegacyDays) >= MaxLegacyList || size+cost > MaxBatchBytes {
			cur = newPart()
		}
		cur.LegacyDays = append(cur.LegacyDays, d)
		size += cost
	}
	for i, r := range b.Requests {
		cost, err := jsonSize(r)
		if err != nil {
			return nil, err
		}
		cost++
		var sess *Session
		if s, ok := sessions[sessionOf[i]]; ok && !placed[sessionOf[i]] {
			sess = &s
			sc, err := jsonSize(s)
			if err != nil {
				return nil, err
			}
			cost += sc + 1
		}
		if baseSize+headerReserve+cost > MaxBatchBytes {
			return nil, fmt.Errorf("Anfrage %d ist allein größer als %d Byte", i, MaxBatchBytes)
		}
		cur := &parts[len(parts)-1]
		if len(cur.Requests) >= MaxRequestsPerBatch || size+cost > MaxBatchBytes ||
			(sess != nil && len(cur.Sessions) >= MaxSessionsPerBatch) {
			cur = newPart()
		}
		if sess != nil {
			cur.Sessions = append(cur.Sessions, *sess)
			placed[sess.SessionID] = true
		}
		cur.Requests = append(cur.Requests, r)
		size += cost
	}
	// Sitzungen ohne Anfrage hängen hinten an, notfalls in neuen Paketen.
	for _, s := range b.Sessions {
		if placed[s.SessionID] {
			continue
		}
		cost, err := jsonSize(s)
		if err != nil {
			return nil, err
		}
		cost++
		cur := &parts[len(parts)-1]
		if size+cost > MaxBatchBytes || len(cur.Sessions) >= MaxSessionsPerBatch {
			cur = newPart()
		}
		cur.Sessions = append(cur.Sessions, s)
		placed[s.SessionID] = true
		size += cost
	}
	return parts, nil
}

func jsonSize(v any) (int, error) {
	b, err := json.Marshal(v)
	return len(b), err
}

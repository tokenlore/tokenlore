package protocol

import (
	"encoding/json"
	"fmt"
)

// headerReserve ist der Platz, der pro Paket für Kopf und JSON-Rahmen frei bleibt.
const headerReserve = 4096

// Split teilt eine Lieferung in Pakete, die die Grenzen aus limits.go einhalten.
// Konten und Altbestand kommen ins erste Paket, eine Sitzung in das Paket ihrer ersten Anfrage.
func Split(b Batch) ([]Batch, error) {
	first := Batch{V: b.V, Device: b.Device, Accounts: b.Accounts,
		LegacyModel: b.LegacyModel, LegacyActivity: b.LegacyActivity}
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
	for _, r := range b.Requests {
		cost, err := jsonSize(r)
		if err != nil {
			return nil, err
		}
		cost++
		var sess *Session
		if s, ok := sessions[r.SessionID]; ok && !placed[r.SessionID] {
			sess = &s
			sc, err := jsonSize(s)
			if err != nil {
				return nil, err
			}
			cost += sc + 1
		}
		if baseSize+headerReserve+cost > MaxBatchBytes {
			return nil, fmt.Errorf("Anfrage %s/%s ist allein größer als %d Byte", r.RequestID, r.MessageID, MaxBatchBytes)
		}
		cur := &parts[len(parts)-1]
		if len(cur.Requests) >= MaxRequestsPerBatch || size+cost > MaxBatchBytes ||
			(sess != nil && len(cur.Sessions) >= MaxSessionsPerBatch) {
			parts = append(parts, Batch{V: b.V, Device: b.Device})
			size = baseSize + headerReserve
			cur = &parts[len(parts)-1]
		}
		if sess != nil {
			cur.Sessions = append(cur.Sessions, *sess)
			placed[r.SessionID] = true
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
			parts = append(parts, Batch{V: b.V, Device: b.Device})
			size = baseSize + headerReserve
			cur = &parts[len(parts)-1]
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

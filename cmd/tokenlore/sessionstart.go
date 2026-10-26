package main

import (
	"encoding/json"
	"io"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// sessionStartBudget ist die längste Zeit, die das Lesen der Hook-Eingabe dauern darf.
// Claude Code soll beim Start nie spürbar warten.
const sessionStartBudget = 500 * time.Millisecond

// runSessionStart merkt sich beim Sessionstart, welches Konto die Sitzung nutzt.
// Fehlende, kaputte oder hängende Eingaben sind kein Fehler: der Sessionstart darf nie gestört werden.
func runSessionStart(stdin io.Reader, dataDir, claudeJSON string) error {
	type input struct {
		SessionID string `json:"session_id"`
	}
	got := make(chan input, 1)
	go func() {
		var in input
		_ = json.NewDecoder(io.LimitReader(stdin, 1<<16)).Decode(&in)
		got <- in
	}()
	var in input
	select {
	case in = <-got:
	case <-time.After(sessionStartBudget):
		return nil
	}
	if !protocol.IDPattern.MatchString(in.SessionID) {
		return nil
	}
	acc, ok, err := claudecode.ReadCurrentAccount(claudeJSON)
	if err != nil || !ok || !protocol.IDPattern.MatchString(acc.AccountID) {
		return nil
	}
	return state.RecordSession(dataDir, in.SessionID, acc.AccountID)
}

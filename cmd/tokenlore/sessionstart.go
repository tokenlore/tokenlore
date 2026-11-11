package main

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// sessionStartBudget ist die längste Zeit, die das Lesen der Hook-Eingabe dauern darf.
// Claude Code soll beim Start nie spürbar warten.
const sessionStartBudget = 500 * time.Millisecond

// hookInput sind die Felder der Hook-Eingabe von Claude Code, die tokenlore braucht.
type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// readHookInput liest die Hook-Eingabe von stdin, höchstens sessionStartBudget lang. ok ist false, wenn sie nicht
// rechtzeitig kam; eine kaputte Eingabe ergibt leere Felder.
func readHookInput(stdin io.Reader) (hookInput, bool) {
	got := make(chan hookInput, 1)
	go func() {
		var in hookInput
		_ = json.NewDecoder(io.LimitReader(stdin, 1<<16)).Decode(&in)
		got <- in
	}()
	select {
	case in := <-got:
		return in, true
	case <-time.After(sessionStartBudget):
		return hookInput{}, false
	}
}

// runSessionStart hält beim Sessionstart fest, welches Konto die Sitzung nutzt: eine Zeile in sitzungen.jsonl im
// Archivordner dieses Rechners. Eine Sitzung gehört genau einem Konto; das Archiv nimmt es für Anfragen ohne
// ownerAccountUuid, auch wenn danach ein anderes Konto angemeldet wird. Ohne Kennung (nie gekoppelt) gibt es kein
// Archiv und nichts zu tun. Fehlende, kaputte oder hängende Eingaben sind kein Fehler: der Sessionstart darf nie
// gestört werden.
//
// Das Konto ist das, das die Desktop-App der Sitzung übergibt, sonst das aus .claude.json (claudecode.SessionAccount).
func runSessionStart(stdin io.Reader, dataDir, configDir string, now time.Time, getenv func(string) string) error {
	in, ok := readHookInput(stdin)
	if !ok || !protocol.IDPattern.MatchString(in.SessionID) {
		return nil
	}
	acc, ok, err := claudecode.SessionAccount(claudecode.ClaudeJSONPath(configDir), getenv)
	if err != nil || !ok || !protocol.IDPattern.MatchString(acc.AccountID) {
		return nil
	}
	id, ok, err := claudecode.ReadIdentity(configDir)
	if err != nil || !ok {
		st, serr := state.Load(dataDir)
		if serr != nil || st.Identity == nil || st.Identity.ID == "" {
			return errors.New("keine Kennung")
		}
		id = *st.Identity
	}
	return archive.RecordSessionStart(state.ArchiveDir(dataDir, id.ID), in.SessionID, acc.AccountID, now)
}

// sessionStartNotice sammelt, was der Sessionstart melden soll: einen fremden Ordner oder Rechner, einen beim
// letzten Abgleich abgelehnten Schlüssel, einen fehlenden oder von einem anderen Gerät übernommenen Signaturschlüssel
// (alle mit den Schritten zum Neukoppeln), eine
// dem Server zu alte Plugin-Version (mit dem Befehl zum Aktualisieren), die Meldung eines Servers mit Datenbank einer
// älteren Version und die Hinweise des Servers zum
// letzten Abgleich (der Abgleich läuft im Hintergrund, sonst sähe das niemand). Es liest nur. Ohne Verbindung oder
// bei unlesbaren Dateien gibt es nichts zu melden.
func sessionStartNotice(dataDir, configDir string) string {
	conn, ok, err := state.LoadConnection(dataDir)
	if err != nil || !ok {
		return ""
	}
	var msgs, notices []string
	outdated := ""
	if stat, err := state.LoadStatus(dataDir); err == nil {
		// Zu alte Version: Neukoppeln hilft nicht, nur Aktualisieren; daher ohne die Schritte zum Neukoppeln.
		if deliver.IsOutdatedMessage(stat.LastError) {
			outdated = client.Clean(stat.LastError)
		}
		// Server älter als das Plugin: ebenso ohne die Schritte, erst muss der Betreiber aktualisieren.
		if stat.LastError == deliver.MsgServerTooOld {
			outdated = stat.LastError
		}
		// Alte Datenbank auf dem Server: ebenso ohne die Schritte, denn erst muss der Betreiber neu einrichten.
		if stat.LastErrorOldDatabase && stat.LastError != "" {
			outdated = client.Clean(stat.LastError)
		}
		if stat.KeyRejected != "" {
			msgs = append(msgs, client.Clean(stat.KeyRejected))
		}
		if stat.LastError == deliver.MsgSignKeyMissing || stat.LastError == deliver.MsgSignKeyTaken {
			msgs = append(msgs, stat.LastError)
		}
		notices = deliver.Notices(stat)
	}
	if st, err := state.Load(dataDir); err == nil && deliver.CheckDevice(configDir, st, conn.Identity, machineHash()).Mismatch {
		msgs = append(msgs, deliver.MsgOtherDevice)
	}
	if len(msgs) > 0 {
		// Die Schritte zum Neukoppeln einmal, hinter den Meldungen, die sie brauchen.
		msgs[len(msgs)-1] += " " + deliver.RepairSteps(client.Clean(conn.ServerURL))
	}
	if outdated != "" {
		msgs = append(msgs, outdated)
	}
	msgs = append(msgs, notices...)
	if len(msgs) == 0 {
		return ""
	}
	return "tokenlore: " + strings.Join(msgs, " – ")
}

// writeSessionNotice gibt die Meldung als JSON-Ausgabe des SessionStart-Hooks aus: systemMessage zeigt Claude Code
// dem Nutzer, additionalContext liest Claude mit. Ohne Meldung bleibt stdout leer.
func writeSessionNotice(w io.Writer, dataDir, configDir string) {
	msg := sessionStartNotice(dataDir, configDir)
	if msg == "" {
		return
	}
	type specific struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	_ = json.NewEncoder(w).Encode(struct {
		SystemMessage      string   `json:"systemMessage"`
		HookSpecificOutput specific `json:"hookSpecificOutput"`
	}{msg, specific{"SessionStart", msg}})
}

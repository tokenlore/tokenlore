package main

import (
	"io"
	"time"

	"github.com/tokenlore/tokenlore/state"
)

// endSyncGap ist der Mindestabstand zwischen dem letzten Versuch eines Abgleichs und dem Abgleich am Sitzungsende.
// Gemessen wird am Versuch, nicht am Erfolg: Ein klemmender Server wird nicht bei jedem Sitzungsende angefragt.
const endSyncGap = 5 * time.Minute

// sessionEndBrake ist die Notbremse des Hooks SessionEnd: Claude Code gibt allen Hooks am Sitzungsende zusammen
// 1,5 s, Plugin-Hooks können das nicht verlängern.
const sessionEndBrake = time.Second

// runSessionEnd startet am Sitzungsende im Hintergrund einen Abgleich, wenn der letzte Versuch mindestens endSyncGap
// zurückliegt und gerade keiner läuft. Den Protokollnamen der beendeten Sitzung bekommt das Kind mit --ended: Deren
// letzte Anfrage und die ihrer Subagenten gelten dort sofort als fertig. Ohne ihn kommt sie beim nächsten Abgleich.
// Fehler werden geschluckt: Das Sitzungsende darf nie gestört werden.
func runSessionEnd(stdin io.Reader, dataDir string, now time.Time) {
	in, _ := readHookInput(stdin)
	if !claimEndSync(dataDir, now) {
		return
	}
	var extra []string
	if in.TranscriptPath != "" {
		extra = []string{"--ended", in.TranscriptPath}
	}
	startBackgroundSync(dataDir, extra...)
}

// claimEndSync prüft unter der gerätweiten Sperre den letzten Versuch und hält now als neuen fest, bevor das Kind
// startet; so startet ein zweites Sitzungsende kurz danach keinen weiteren Abgleich. Hält ein Abgleich die Sperre,
// läuft gerade einer, und es gibt nichts zu tun.
func claimEndSync(dataDir string, now time.Time) bool {
	if _, ok, err := state.LoadConnection(dataDir); err != nil || !ok {
		return false
	}
	release, ok, err := lockDevice()
	if err != nil || !ok {
		return false
	}
	defer release()
	stat, err := state.LoadStatus(dataDir)
	if err != nil {
		stat = &state.Status{} // kaputter Status ist nur Anzeige
	}
	// Ein Versuch in der Zukunft (Uhr verstellt) bremst nicht.
	if last := stat.LastAttempt; !last.IsZero() && !last.After(now) && now.Sub(last) < endSyncGap {
		return false
	}
	stat.LastAttempt = now
	return stat.Save(dataDir) == nil
}

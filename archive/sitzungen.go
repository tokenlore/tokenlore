package archive

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

// SessionsFile hält je Sitzung das Konto, das beim Sitzungsstart angemeldet war. Es gehört zum Archiv, ist nicht
// signiert und geht nie an den Server; es dient nur dem Konto einer Anfrage ohne ownerAccountUuid.
const SessionsFile = "sitzungen.jsonl"

const (
	// sessionKeep: So lange bleibt ein Eintrag. Ein Eintrag wird nur gebraucht, bis die erste Anfrage der Sitzung im
	// Archiv steht; danach gilt das Konto dort. Claude Code löscht Sitzungsdateien standardmäßig nach 30 Tagen, eine
	// länger ruhende Sitzung liefert also kaum noch neue Anfragen. 90 Tage lassen dafür reichlich Luft.
	sessionKeep = 90 * 24 * time.Hour
	// sessionMax begrenzt die Datei, auch wenn sehr viele Sitzungen in kurzer Zeit starten; die neuesten bleiben.
	sessionMax = 10_000
)

// sessionStart ist eine Zeile in sitzungen.jsonl.
type sessionStart struct {
	SessionID string    `json:"session_id"`
	AccountID string    `json:"account_id"`
	At        time.Time `json:"at"`
}

// RecordSessionStart hängt im Archivordner dir an, welches Konto beim Start der Sitzung sid angemeldet war. Hat die
// Sitzung schon einen Eintrag (fortgesetzte Sitzung), schreibt es nichts: Eine Sitzung hat genau ein Konto. Angehängt
// wird eine einzige kurze Zeile in einem Schreibaufruf (O_APPEND); so verliert ein gleichzeitig startender Sitzungsstart
// keinen Eintrag. Ungültige Kennungen schreibt es nicht. Ordner 0700, Datei 0600.
//
// Die Grenzen gelten schon hier, nicht erst im Abgleich, damit die Datei auch ohne Kopplung nicht wächst: Steht danach
// ein Eintrag älter als sessionKeep darin oder sind es mehr als sessionMax, räumt es wie PruneSessionStarts auf, mit at
// als Uhr.
func RecordSessionStart(dir, sid, account string, at time.Time) error {
	if !protocol.IDPattern.MatchString(sid) || !protocol.IDPattern.MatchString(account) {
		return errors.New("Sitzung oder Konto ungültig")
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	all, first, err := readSessionStarts(dir)
	if err != nil {
		return err
	}
	if _, ok := first[sid]; ok {
		return nil
	}
	if err := appendSessionStart(dir, sessionStart{SessionID: sid, AccountID: account, At: at.UTC()}); err != nil {
		return err
	}
	cut := at.Add(-sessionKeep)
	if len(all)+1 > sessionMax || slices.ContainsFunc(all, func(s sessionStart) bool { return s.At.Before(cut) }) {
		return pruneSessionStarts(dir, at)
	}
	return nil
}

// appendSessionStart hängt eine Zeile an sitzungen.jsonl an, in einem Schreibaufruf.
func appendSessionStart(dir string, s sessionStart) error {
	b, err := protocol.MarshalLine(s)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, SessionsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// readSessionStarts liest sitzungen.jsonl; je Sitzung gilt der erste Eintrag (eine Sitzung hat genau ein Konto).
// Unlesbare Zeilen werden übersprungen.
func readSessionStarts(dir string) ([]sessionStart, map[string]string, error) {
	var all []sessionStart
	first := map[string]string{}
	err := eachLine(filepath.Join(dir, SessionsFile), func(b []byte) {
		var s sessionStart
		if json.Unmarshal(b, &s) != nil || !protocol.IDPattern.MatchString(s.SessionID) || !protocol.IDPattern.MatchString(s.AccountID) {
			return
		}
		all = append(all, s)
		if _, ok := first[s.SessionID]; !ok {
			first[s.SessionID] = s.AccountID
		}
	})
	return all, first, err
}

// PruneSessionStarts räumt sitzungen.jsonl auf: Einträge älter als sessionKeep fallen weg, je Sitzung bleibt der
// erste, höchstens sessionMax (die neuesten). Geschrieben wird nur bei einer Änderung, atomar. Der Abgleich ruft es
// unter der gerätweiten Sperre, RecordSessionStart ohne sie, wenn eine Grenze überschritten ist; ein Sitzungsstart, der
// genau zwischen Lesen und Umbenennen anhängt, verliert seinen Eintrag, und die Anfrage bekommt dann das Konto aus dem
// Archiv oder das angemeldete Konto.
func (a *Archive) PruneSessionStarts() error {
	return pruneSessionStarts(a.dir, a.now())
}

func pruneSessionStarts(dir string, now time.Time) error {
	all, _, err := readSessionStarts(dir)
	if err != nil {
		return err
	}
	cut := now.Add(-sessionKeep)
	seen := map[string]bool{}
	var keep []sessionStart
	for _, s := range all {
		if s.At.Before(cut) || seen[s.SessionID] {
			continue
		}
		seen[s.SessionID] = true
		keep = append(keep, s)
	}
	if n := len(keep) - sessionMax; n > 0 {
		slices.SortStableFunc(keep, func(x, y sessionStart) int { return x.At.Compare(y.At) })
		keep = keep[n:]
	}
	if len(keep) == len(all) {
		return nil
	}
	var buf bytes.Buffer
	for _, s := range keep {
		b, err := protocol.MarshalLine(s)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return state.WriteFileAtomic(dir, SessionsFile, buf.Bytes())
}

// startAccount ist das Konto beim Start der Sitzung sid laut sitzungen.jsonl, sonst leer. Die Datei wird einmal je
// geöffnetem Archiv gelesen.
func (a *Archive) startAccount(sid string) (string, error) {
	if a.starts == nil {
		_, first, err := readSessionStarts(a.dir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		a.starts = first
	}
	return a.starts[sid], nil
}

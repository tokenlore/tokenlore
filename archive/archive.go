// Paket archive führt das signierte Archiv eines Rechners (protocol/doc.md, „Archiv des Plugins“): je fertige Anfrage
// genau eine Hülle in der Monatsdatei (Monat nach UTC), die Vorgeschichte einmal, die Konten mit voller Adresse,
// abgeschlossene Monate gepackt und signiert. Eine Zeile im Archiv wird nie geändert.
package archive

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

// Dateinamen im Archivordner.
const (
	LegacyFile   = "vorgeschichte.jsonl"
	AccountsFile = "konten.jsonl"
)

const (
	dirPerm  = 0o700
	filePerm = 0o600
	// boundary: Liegt eine Anfrage so nah an einer Monatsgrenze, prüft das Entdoppeln auch den Nachbarmonat. Dieselbe
	// Anfrage kann in zwei Protokolldateien (Subagent, Fortsetzung) mit leicht anderem Zeitpunkt stehen.
	boundary = 24 * time.Hour
)

var monthPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}$`)

// MonthFile, PackedFile, SigFile und NachtragFile sind die Dateinamen eines Monats JJJJ-MM.
func MonthFile(m string) string    { return m + ".jsonl" }
func PackedFile(m string) string   { return m + ".jsonl.gz" }
func SigFile(m string) string      { return m + ".jsonl.gz.sig" }
func NachtragFile(m string) string { return m + ".nachtrag.jsonl" }

// Month ist der Monat (UTC) eines Zeitpunkts, JJJJ-MM.
func Month(t time.Time) string { return t.UTC().Format("2006-01") }

// Archive ist ein geöffneter Archivordner. Es ist nicht für gleichzeitige Nutzung gedacht; die gerätweite Sperre des
// Abgleichs gilt auch für das Archiv.
type Archive struct {
	dir string
	key ed25519.PrivateKey
	now func() time.Time
	// index: je geladenem Monat die Anfragen und die Konten ihrer Sitzungen.
	index map[string]*monthIdx
	// starts: Konto je Sitzung beim Sitzungsstart (sitzungen.jsonl), geladen beim ersten Bedarf.
	starts map[string]string
}

// monthIdx ist, was das Archiv von einem Monat im Speicher hält: je Anfrage (message_id|request_id) der Hash ihres
// usage-Blocks, je Sitzung das Konto ihrer Anfragen.
type monthIdx struct {
	req  map[string][32]byte
	sess map[string]string
}

// AddResult zählt, was AddRequests getan hat.
type AddResult struct {
	Added    int // neu ins Archiv geschrieben, davon
	Nachtrag int // in die Nachtragsdatei eines gepackten Monats
	// Late zählt Anfragen, die schon im Archiv stehen, aber jetzt mit anderem usage gelesen wurden: Zeilen kamen nach
	// dem Archivieren hinzu. Die Archivzeile bleibt unverändert (bekannte Lücke).
	Late int
	// Accounts sind die Konten der neu geschriebenen Zeilen, je eins, für konten.jsonl.
	Accounts []string
}

// Exists meldet, ob der Archivordner mit Archivinhalt da ist. sitzungen.jsonl allein zählt nicht: Der Sessionstart legt
// sie auch in einem gelöschten Archivordner wieder an, und das Archiv muss trotzdem als fehlend gelten.
func Exists(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != SessionsFile {
			return true
		}
	}
	return false
}

// Open legt den Archivordner an, wenn er fehlt (Ordner 0700), und räumt Reste eines abgebrochenen Laufs auf:
// temporäre Dateien, eine halbe letzte Zeile, ein gepackter Monat ohne Signatur oder mit liegengebliebener Monatsdatei.
func Open(dir string, key ed25519.PrivateKey, now func() time.Time) (*Archive, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("Signaturschlüssel fehlt")
	}
	if now == nil {
		now = time.Now
	}
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := os.MkdirAll(d, dirPerm); err != nil {
			return nil, err
		}
		// Unter Windows wirkt Chmod nur auf das Schreibschutz-Bit; dort schützt der Benutzerordner.
		if err := os.Chmod(d, dirPerm); err != nil {
			return nil, err
		}
	}
	a := &Archive{dir: dir, key: key, now: now, index: map[string]*monthIdx{}}
	if err := a.repair(); err != nil {
		return nil, fmt.Errorf("aufräumen: %w", err)
	}
	return a, nil
}

// Dir ist der Archivordner.
func (a *Archive) Dir() string { return a.dir }

func (a *Archive) path(name string) string { return filepath.Join(a.dir, name) }

func (a *Archive) exists(name string) bool {
	_, err := os.Stat(a.path(name))
	return err == nil
}

// Packed meldet, ob der Monat m schon gepackt ist.
func (a *Archive) Packed(m string) bool { return a.exists(PackedFile(m)) }

func (a *Archive) repair() error {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		n := e.Name()
		switch {
		case e.IsDir():
		case strings.HasSuffix(n, ".tmp"):
			if err := os.Remove(a.path(n)); err != nil {
				return err
			}
		case strings.HasSuffix(n, ".jsonl.gz"):
			m := strings.TrimSuffix(n, ".jsonl.gz")
			if !monthPattern.MatchString(m) {
				continue
			}
			// Abbruch nach dem Umbenennen der .gz: Signatur nachholen, Monatsdatei entfernen. Die .gz entstand aus
			// genau dieser Monatsdatei, unter derselben Sperre.
			if !a.exists(SigFile(m)) {
				if err := a.writeSig(m); err != nil {
					return err
				}
			}
			if err := os.Remove(a.path(MonthFile(m))); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	entries, err = os.ReadDir(a.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			if err := truncatePartial(a.path(e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// truncatePartial schneidet eine halbe letzte Zeile ab (Abbruch mitten im Schreiben); sie wird danach neu geschrieben.
func truncatePartial(p string) error {
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size := fi.Size()
	if size == 0 {
		return nil
	}
	const chunk = 1 << 16
	buf := make([]byte, chunk)
	end := size
	for end > 0 {
		start := max(0, end-chunk)
		b := buf[:end-start]
		if _, err := f.ReadAt(b, start); err != nil {
			return err
		}
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			cut := start + int64(i) + 1
			if cut == size {
				return nil
			}
			return f.Truncate(cut)
		}
		end = start
	}
	return f.Truncate(0)
}

// ReadFile liest alle Hüllen einer Archivdatei (gepackt oder nicht). Eine fehlende Datei ergibt keine Zeilen; eine
// unlesbare Zeile wird übersprungen und gezählt.
func ReadFile(p string) (lines []protocol.Envelope, bad int, err error) {
	err = eachLine(p, func(b []byte) {
		var h protocol.Envelope
		if json.Unmarshal(b, &h) != nil || h.Z == "" {
			bad++
			return
		}
		lines = append(lines, h)
	})
	return lines, bad, err
}

func eachLine(p string, fn func([]byte)) error {
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(p, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer zr.Close()
		r = zr
	}
	br := bufio.NewReaderSize(r, 1<<16)
	for {
		b, err := br.ReadBytes('\n')
		if len(b) > 0 && b[len(b)-1] == '\n' {
			if b = bytes.TrimSpace(b); len(b) > 0 {
				fn(b)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// requestKey ist der Teil einer Anfrage-Inhaltszeile, den das Archiv liest.
type requestKey struct {
	Kind         string          `json:"kind"`
	RequestID    string          `json:"request_id"`
	MessageID    string          `json:"message_id"`
	SessionID    string          `json:"session_id"`
	AccountID    string          `json:"account_id"`
	TS           time.Time       `json:"ts"`
	UTCOffsetMin int             `json:"utc_offset_min"`
	Usage        json.RawMessage `json:"usage"`
}

func key(messageID, requestID string) string { return messageID + "|" + requestID }

// monthIndex lädt die Anfragen eines Monats: aus der Monatsdatei oder, wenn er gepackt ist, aus .gz und Nachtrag.
func (a *Archive) monthIndex(m string) (*monthIdx, error) {
	if idx, ok := a.index[m]; ok {
		return idx, nil
	}
	idx := &monthIdx{req: map[string][32]byte{}, sess: map[string]string{}}
	files := []string{MonthFile(m)}
	if a.Packed(m) {
		files = []string{PackedFile(m), NachtragFile(m)}
	}
	for _, n := range files {
		err := eachRequest(a.path(n), func(k requestKey) {
			idx.req[key(k.MessageID, k.RequestID)] = sha256.Sum256(k.Usage)
			if k.AccountID != "" {
				idx.sess[k.SessionID] = k.AccountID
			}
		})
		if err != nil {
			return nil, fmt.Errorf("%s lesen: %w", n, err)
		}
	}
	a.index[m] = idx
	return idx, nil
}

func eachRequest(p string, fn func(requestKey)) error {
	lines, _, err := ReadFile(p)
	if err != nil {
		return err
	}
	for _, h := range lines {
		var k requestKey
		if json.Unmarshal([]byte(h.Z), &k) != nil || k.Kind != protocol.KindRequest || k.MessageID == "" {
			continue
		}
		fn(k)
	}
	return nil
}

// AddRequests schreibt jede Anfrage, die noch nicht im Archiv steht, als signierte Hülle in die Datei ihres Monats
// (UTC von ts), in einen gepackten Monat als Nachtrag. Entdoppelt wird über message_id und request_id gegen die
// Dateien dieses Monats und, nahe der Monatsgrenze, des Nachbarmonats, dazu gegen die Zeilen dieses Aufrufs.
// Das Konto einer Zeile setzt fillAccount. Die Zeilen müssen protocol.ValidateRequestLine bestehen.
func (a *Archive) AddRequests(lines []protocol.RequestLine, fallback string) (AddResult, error) {
	var res AddResult
	pending := map[string][]byte{}
	for _, l := range lines {
		u, err := protocol.MarshalLine(l.Usage)
		if err != nil {
			return res, err
		}
		hash := sha256.Sum256(u)
		k := key(l.MessageID, l.RequestID)
		m := Month(l.TS)
		months := []string{m}
		if n := Month(l.TS.Add(-boundary)); n != m {
			months = append(months, n)
		}
		if n := Month(l.TS.Add(boundary)); n != m {
			months = append(months, n)
		}
		known := false
		for _, mm := range months {
			idx, err := a.monthIndex(mm)
			if err != nil {
				return res, err
			}
			if old, ok := idx.req[k]; ok {
				known = true
				if old != hash {
					res.Late++
				}
				break
			}
		}
		if known {
			continue
		}
		if err := a.fillAccount(&l, m, fallback); err != nil {
			return res, err
		}
		z, err := protocol.MarshalLine(l)
		if err != nil {
			return res, err
		}
		if len(z) > protocol.MaxLineBytes {
			return res, fmt.Errorf("Zeile länger als %d Byte", protocol.MaxLineBytes)
		}
		env, err := protocol.SealLine(a.key, z)
		if err != nil {
			return res, err
		}
		target := MonthFile(m)
		if a.Packed(m) {
			target = NachtragFile(m)
			res.Nachtrag++
		}
		pending[target] = append(append(pending[target], env...), '\n')
		a.index[m].req[k] = hash
		if l.AccountID != "" {
			a.index[m].sess[l.SessionID] = l.AccountID
			if !slices.Contains(res.Accounts, l.AccountID) {
				res.Accounts = append(res.Accounts, l.AccountID)
			}
		}
		res.Added++
	}
	for _, name := range slices.Sorted(maps.Keys(pending)) {
		if err := a.appendFile(name, pending[name]); err != nil {
			return res, fmt.Errorf("%s schreiben: %w", name, err)
		}
	}
	return res, nil
}

// fillAccount setzt das Konto einer Zeile des Monats m. Eine Sitzung hat genau ein Konto: Steht sie schon mit
// Konto im Archiv (dieser und der Vormonat), gilt dieses. Sonst gilt ownerAccountUuid aus dem Protokoll (schon in
// l.AccountID), sonst das Konto beim Sitzungsstart aus sitzungen.jsonl, sonst fallback, das angemeldete Konto.
func (a *Archive) fillAccount(l *protocol.RequestLine, m, fallback string) error {
	acc, err := a.sessionAccount(l.SessionID, m)
	if err != nil {
		return err
	}
	switch {
	case acc != "":
		l.AccountID = acc
	case l.AccountID != "":
	default:
		start, err := a.startAccount(l.SessionID)
		if err != nil {
			return err
		}
		if start != "" {
			l.AccountID = start
		} else if protocol.IDPattern.MatchString(fallback) {
			l.AccountID = fallback
		}
	}
	return nil
}

// sessionAccount ist das Konto, das die Sitzung sid im Monat m oder im Vormonat schon im Archiv hat, sonst leer.
func (a *Archive) sessionAccount(sid, m string) (string, error) {
	t, err := time.Parse("2006-01", m)
	if err != nil {
		return "", err
	}
	for _, mm := range []string{m, Month(t.AddDate(0, -1, 0))} {
		idx, err := a.monthIndex(mm)
		if err != nil {
			return "", err
		}
		if acc := idx.sess[sid]; acc != "" {
			return acc, nil
		}
	}
	return "", nil
}

// appendFile hängt b an (Datei 0600) und schreibt es auf die Platte.
func (a *Archive) appendFile(name string, b []byte) error {
	f, err := os.OpenFile(a.path(name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePerm)
	if err != nil {
		return err
	}
	_ = f.Chmod(filePerm)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// LegacyDone meldet, ob die Vorgeschichte schon übernommen ist.
func (a *Archive) LegacyDone() bool { return a.exists(LegacyFile) }

// AddLegacy übernimmt die Vorgeschichte einmal: je Tag ohne Einzelanfrage im Archiv (lokales Datum aus ts und
// utc_offset_min, wie in stats-cache.json) eine signierte Hülle. Die Datei entsteht auch ohne Tage, damit die
// Vorgeschichte nicht bei jedem Lauf neu übernommen wird. dropped zählt Tage, die die Prüfung nicht bestehen.
func (a *Archive) AddLegacy(days []protocol.DayLine) (written, dropped int, err error) {
	if a.LegacyDone() {
		return 0, 0, nil
	}
	have, err := a.requestDays()
	if err != nil {
		return 0, 0, err
	}
	var buf bytes.Buffer
	now := a.now()
	for _, d := range days {
		if have[d.Date] {
			continue
		}
		if protocol.ValidateDayLine(d, now) != nil {
			dropped++
			continue
		}
		z, err := protocol.MarshalLine(d)
		if err != nil {
			return 0, 0, err
		}
		env, err := protocol.SealLine(a.key, z)
		if err != nil {
			return 0, 0, err
		}
		buf.Write(env)
		buf.WriteByte('\n')
		written++
	}
	if err := state.WriteFileAtomic(a.dir, LegacyFile, buf.Bytes()); err != nil {
		return 0, 0, err
	}
	return written, dropped, nil
}

// requestDays sind die lokalen Tage aller Anfragen im Archiv.
func (a *Archive) requestDays() (map[string]bool, error) {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return nil, err
	}
	days := map[string]bool{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !(strings.HasSuffix(n, ".jsonl") || strings.HasSuffix(n, ".jsonl.gz")) || n == LegacyFile || n == AccountsFile {
			continue
		}
		err := eachRequest(a.path(n), func(k requestKey) {
			days[k.TS.Add(time.Duration(k.UTCOffsetMin)*time.Minute).UTC().Format("2006-01-02")] = true
		})
		if err != nil {
			return nil, fmt.Errorf("%s lesen: %w", n, err)
		}
	}
	return days, nil
}

// Account ist eine Zeile in konten.jsonl: das Konto mit voller Adresse, wenn sie bekannt ist. Nicht signiert; was
// davon das Gerät verlässt, entscheidet die E-Mail-Stufe beim Senden.
type Account struct {
	AccountID string `json:"account_id"`
	Email     string `json:"email,omitempty"`
}

// ReadAccounts liest konten.jsonl.
func ReadAccounts(dir string) ([]Account, error) {
	var out []Account
	err := eachLine(filepath.Join(dir, AccountsFile), func(b []byte) {
		var acc Account
		if json.Unmarshal(b, &acc) == nil && protocol.IDPattern.MatchString(acc.AccountID) {
			out = append(out, acc)
		}
	})
	return out, err
}

// AddAccounts nimmt jedes Konto aus ids einmal in konten.jsonl auf; das angemeldete Konto current bekommt seine volle
// Adresse email (eine geänderte Adresse ersetzt die alte). Ungültige Kennungen und Adressen bleiben weg. Die Datei
// wird nur bei einer Änderung neu geschrieben, atomar.
func (a *Archive) AddAccounts(ids []string, current, email string) error {
	have, err := ReadAccounts(a.dir)
	if err != nil {
		return err
	}
	byID := map[string]string{}
	for _, acc := range have {
		byID[acc.AccountID] = acc.Email
	}
	changed := false
	add := func(id string) {
		if _, ok := byID[id]; !ok && protocol.IDPattern.MatchString(id) {
			byID[id] = ""
			changed = true
		}
	}
	for _, id := range ids {
		add(id)
	}
	if current != "" && protocol.IDPattern.MatchString(current) {
		add(current)
		if email != "" && byID[current] != email &&
			protocol.ValidateAccount(protocol.Account{AccountID: current, EmailMode: protocol.EmailFull, Email: email}) == nil {
			byID[current] = email
			changed = true
		}
	}
	if !changed && len(byID) == len(have) {
		return nil
	}
	var buf bytes.Buffer
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		b, err := protocol.MarshalLine(Account{AccountID: id, Email: byID[id]})
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return state.WriteFileAtomic(a.dir, AccountsFile, buf.Bytes())
}

// Pack packt jeden Monat vor dem laufenden UTC-Monat, der nicht in held steht (Monate mit zurückgehaltenen
// Anfragen): .jsonl.gz und .sig je über eine temporäre Datei mit Umbenennen, danach fällt die Monatsdatei weg.
// Anfragen, die später noch in einen gepackten Monat fallen, gehen in dessen Nachtragsdatei.
func (a *Archive) Pack(held []string) ([]string, error) {
	cur := Month(a.now())
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return nil, err
	}
	var packed []string
	for _, e := range entries {
		m, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if e.IsDir() || !ok || !monthPattern.MatchString(m) || m >= cur || slices.Contains(held, m) || a.Packed(m) {
			continue
		}
		raw, err := os.ReadFile(a.path(e.Name()))
		if err != nil {
			return packed, err
		}
		var gz bytes.Buffer
		zw, err := gzip.NewWriterLevel(&gz, gzip.BestCompression)
		if err != nil {
			return packed, err
		}
		if _, err := zw.Write(raw); err != nil {
			return packed, err
		}
		if err := zw.Close(); err != nil {
			return packed, err
		}
		if err := state.WriteFileAtomic(a.dir, PackedFile(m), gz.Bytes()); err != nil {
			return packed, err
		}
		if err := a.writeSig(m); err != nil {
			return packed, err
		}
		if err := os.Remove(a.path(e.Name())); err != nil {
			return packed, err
		}
		packed = append(packed, m)
	}
	return packed, nil
}

func (a *Archive) writeSig(m string) error {
	gz, err := os.ReadFile(a.path(PackedFile(m)))
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(a.dir, SigFile(m), []byte(protocol.SignMonth(a.key, gz)+"\n"))
}

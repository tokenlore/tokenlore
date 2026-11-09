package deliver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/device"
	"github.com/tokenlore/tokenlore/email"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

// item ist eine Hülle aus dem Archiv, die geliefert wird, mit ihrer Stelle im Archiv.
type item struct {
	src  int // Index in Delivery.sources
	line int // Nummer der Zeile in ihrer Datei
	env  protocol.Envelope
	// Nur bei Anfragen: Sitzung, Zeitpunkt, Claude-Code-Version und Konto aus der Inhaltszeile.
	sid, ccVersion, account string
	ts                      time.Time
	date                    string // nur bei Vorgeschichte-Tagen
}

// Delivery ist, was aus dem Archiv noch zum Server muss: Pakete und je Paket der Stand des Merkers „geliefert“
// nach seiner Annahme. Sie entsteht nur aus dem Archivordner dieses Rechners, nie aus einem anderen.
type Delivery struct {
	Parts []protocol.Batch
	// Requests und Days zählen die Hüllen in Parts; Damaged die Zeilen, die dieser Lauf neu als beschädigt aussondert,
	// Foreign die, die er aussondert, weil der Server ihren früheren Signaturschlüssel einem anderen Gerät zuordnet.
	Requests, Days, Damaged, Foreign int
	// NothingNew: keine Hülle offen und die Kontenliste unverändert; zu senden gibt es nichts.
	NothingNew bool

	dir         string
	done        *archive.Delivered
	sources     []archive.Source
	total       []int   // Zeilen je Datei
	damagedAt   [][]int // als beschädigt ausgesonderte Zeilen je Datei, aufsteigend
	foreignAt   [][]int // wegen eines fremd gehaltenen früheren Signaturschlüssels ausgesonderte Zeilen je Datei
	items       []item  // in Lieferreihenfolge: erst Tage, dann Anfragen, je Datei aufsteigend
	cut         []int   // cut[k]: Zahl der Hüllen in Parts[0..k]
	accountsSum string
}

// PrepareDelivery liest den Archivordner dir und baut die Pakete aus allem, was laut geliefert.json noch offen ist.
// Jede Hülle wird vorher selbst geprüft: Signatur gegen den aktuellen oder einen früheren öffentlichen Schlüssel aus
// st, Inhaltszeile nach den Regeln des Servers. Was nicht besteht, geht nicht mit, zählt als erledigt und als
// beschädigt, damit eine kaputte Zeile die übrigen nie blockiert. Ebenso geht eine Zeile nicht mit, deren früheren
// Signaturschlüssel der Server einem anderen Gerät zuordnet (st.SignKeysRefused); sie zählt als erledigt und als
// fremd. Es schreibt nichts. Warnungen nennen Dateinamen, nie Inhalte.
func PrepareDelivery(dir string, st *state.State, version string, now time.Time) (*Delivery, []string, error) {
	tzName, _ := device.LocalTZ()
	dev := protocol.DeviceInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, TZ: tzName, PluginVersion: version}
	if err := protocol.ValidateDevice(dev); err != nil {
		return nil, nil, fmt.Errorf("Gerätedaten ungültig: %w", err)
	}
	var warns []string
	done, broken, err := archive.LoadDelivered(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("%s nicht lesbar: %w", archive.DeliveredFile, err)
	}
	if broken {
		warns = append(warns, fmt.Sprintf("%s unlesbar, das Archiv geht noch einmal ganz an den Server (er zählt nichts doppelt)", archive.DeliveredFile))
	}
	sources, err := archive.Sources(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("Archiv nicht lesbar: %w", err)
	}
	keys, foreign := signKeys(st)
	dl := &Delivery{dir: dir, done: done, sources: sources, total: make([]int, len(sources)),
		damagedAt: make([][]int, len(sources)), foreignAt: make([][]int, len(sources))}
	var days, reqs []item
	for si, src := range sources {
		lines, err := archive.ReadLines(src.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("%s nicht lesbar: %w", src.Name, err)
		}
		dl.total[si] = len(lines)
		from := done.Lines[src.Name]
		if from > len(lines) {
			// Die Datei ist kürzer als gemerkt, also ersetzt: lieber alles noch einmal als etwas verlieren.
			warns = append(warns, fmt.Sprintf("%s: kürzer als in %s vermerkt, wird noch einmal ganz geliefert", src.Name, archive.DeliveredFile))
			from = 0
			delete(done.Lines, src.Name)
		}
		for i := from; i < len(lines); i++ {
			it, v := checkLine(lines[i], src.Day, keys, foreign, now)
			switch v {
			case lineDamaged:
				dl.damagedAt[si] = append(dl.damagedAt[si], i)
				dl.Damaged++
				continue
			case lineForeign:
				dl.foreignAt[si] = append(dl.foreignAt[si], i)
				dl.Foreign++
				continue
			}
			it.src, it.line = si, i
			if src.Day {
				days = append(days, it)
			} else {
				reqs = append(reqs, it)
			}
		}
	}
	if dl.Damaged > 0 {
		warns = append(warns, fmt.Sprintf("Archiv: %d beschädigte Zeile(n) ausgesondert (Signatur oder Inhalt passt nicht); sie gehen nicht an den Server", dl.Damaged))
	}
	if dl.Foreign > 0 {
		warns = append(warns, "Archiv: "+ForeignLead(dl.Foreign))
	}
	dl.items = append(days, reqs...)

	accounts, err := deliveryAccounts(dir, st.EmailMode)
	if err != nil {
		return nil, nil, fmt.Errorf("%s nicht lesbar: %w", archive.AccountsFile, err)
	}
	if dl.accountsSum, err = accountsSum(accounts); err != nil {
		return nil, nil, err
	}
	if len(dl.items) == 0 && dl.accountsSum == done.AccountsSum {
		dl.NothingNew = true
		return dl, warns, nil
	}

	b := protocol.Batch{V: protocol.Version, Device: dev, Accounts: accounts, Sessions: sessionsOf(reqs)}
	sessionOf := make([]string, 0, len(reqs))
	for _, it := range days {
		b.LegacyDays = append(b.LegacyDays, it.env)
	}
	for _, it := range reqs {
		b.Requests = append(b.Requests, it.env)
		sessionOf = append(sessionOf, it.sid)
	}
	dl.Parts, err = protocol.Split(b, sessionOf)
	if err != nil {
		return nil, nil, fmt.Errorf("Aufteilen fehlgeschlagen: %w", err)
	}
	n := 0
	for _, p := range dl.Parts {
		n += len(p.LegacyDays) + len(p.Requests)
		dl.cut = append(dl.cut, n)
	}
	dl.Requests, dl.Days = len(reqs), len(days)
	return dl, warns, nil
}

// DamagedTotal zählt alle als beschädigt ausgesonderten Zeilen: die schon im Merker vermerkten und die dieses Laufs.
func (dl *Delivery) DamagedTotal() int { return dl.done.Damaged + dl.Damaged }

// ForeignTotal zählt alle Zeilen, die wegen eines fremd gehaltenen früheren Signaturschlüssels nicht mehr mitgehen:
// die schon im Merker vermerkten und die dieses Laufs.
func (dl *Delivery) ForeignTotal() int { return dl.done.Foreign + dl.Foreign }

// signKeys teilt die öffentlichen Signaturschlüssel dieses Rechners: keys sind die, mit denen eine Zeile gültig ist
// und mitgeht (der aktuelle und die früheren, wie sie auch der Server kennt); foreign die früheren, die der Server
// einem anderen Gerät zuordnet. Der aktuelle Schlüssel steht nie in foreign: Lehnt der Server ihn ab, hilft nur
// Neukoppeln (MsgSignKeyTaken).
func signKeys(st *state.State) (keys, foreign []string) {
	if st.SignPublicKey != "" {
		keys = append(keys, st.SignPublicKey)
	}
	for _, k := range st.SignPublicKeysPrevious {
		if slices.Contains(st.SignKeysRefused, k) {
			foreign = append(foreign, k)
		} else {
			keys = append(keys, k)
		}
	}
	return keys, foreign
}

// Ergebnis von checkLine.
const (
	lineOK      = iota
	lineDamaged // Hülle, Signatur oder Inhalt passt nicht
	lineForeign // Signatur passt zu einem früheren Schlüssel, den der Server einem anderen Gerät zuordnet
)

// checkLine prüft eine Archivzeile wie der Server: Hülle mit genau z und s, Signatur passt zu einem der Schlüssel,
// z ist eine Inhaltszeile der erwarteten Art in ihren Grenzen. Passt die Signatur nur zu einem Schlüssel aus foreign,
// ist die Zeile fremd, gleich was in z steht.
func checkLine(raw []byte, day bool, keys, foreign []string, now time.Time) (item, int) {
	var env protocol.Envelope
	if !strictDecode(raw, &env) || env.Z == "" || len(env.Z) > protocol.MaxLineBytes || !utf8.ValidString(env.Z) {
		return item{}, lineDamaged
	}
	if signedBy(env, keys) == "" {
		if signedBy(env, foreign) != "" {
			return item{}, lineForeign
		}
		return item{}, lineDamaged
	}
	it := item{env: env}
	if day {
		var d protocol.DayLine
		if !strictDecode([]byte(env.Z), &d) || protocol.ValidateDayLine(d, now) != nil {
			return item{}, lineDamaged
		}
		it.date = d.Date
		return it, lineOK
	}
	var l protocol.RequestLine
	if !strictDecode([]byte(env.Z), &l) || protocol.ValidateRequestLine(l, now) != nil {
		return item{}, lineDamaged
	}
	it.sid, it.ccVersion, it.account, it.ts = l.SessionID, l.CCVersion, l.AccountID, l.TS
	return it, lineOK
}

// signedBy ist der Schlüssel aus keys, zu dem die Signatur der Hülle passt, sonst leer.
func signedBy(env protocol.Envelope, keys []string) string {
	for _, k := range keys {
		if protocol.VerifyLine(k, []byte(env.Z), env.S) {
			return k
		}
	}
	return ""
}

// strictDecode liest genau ein JSON-Objekt ohne unbekannte Schlüssel; Zahlen bleiben json.Number.
func strictDecode(b []byte, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if dec.Decode(v) != nil {
		return false
	}
	_, err := dec.Token()
	return err != nil // nach dem Objekt darf nichts mehr kommen
}

// sessionsOf leitet die Sitzungen der Lieferung aus den Inhaltszeilen ab (protocol/doc.md, „Session“): je Sitzung
// einmal, Claude-Code-Version und Konto aus ihrer Anfrage mit dem frühesten Zeitpunkt. Ohne eigene Signatur.
func sessionsOf(reqs []item) []protocol.Session {
	first := map[string]item{}
	var order []string
	for _, it := range reqs {
		f, ok := first[it.sid]
		if !ok {
			order = append(order, it.sid)
		}
		if !ok || it.ts.Before(f.ts) {
			first[it.sid] = it
		}
	}
	out := make([]protocol.Session, 0, len(order))
	for _, sid := range order {
		f := first[sid]
		s := protocol.Session{SessionID: sid, CCVersion: f.ccVersion, AccountID: f.account}
		if protocol.ValidateSession(s) != nil {
			s.CCVersion = "" // Kennungen sind mit der Inhaltszeile schon geprüft
		}
		out = append(out, s)
	}
	return out
}

// deliveryAccounts ist die Kontenliste aus konten.jsonl mit der E-Mail-Stufe des Geräts: die Adresse gekürzt, voll
// oder gar nicht. Eine ungültige Adresse fällt weg, das Konto bleibt.
func deliveryAccounts(dir, mode string) ([]protocol.Account, error) {
	accs, err := archive.ReadAccounts(dir)
	if err != nil {
		return nil, err
	}
	var out []protocol.Account
	for _, acc := range accs {
		a := protocol.Account{AccountID: acc.AccountID, EmailMode: mode, Email: email.ForMode(mode, acc.Email)}
		if protocol.ValidateAccount(a) != nil {
			a.Email = ""
		}
		if protocol.ValidateAccount(a) != nil {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// accountsSum ist die hex-SHA-256 des JSON der Kontenliste. Ändert sie sich (z. B. die E-Mail-Stufe),
// wird die Kontenliste auch ohne neue Anfragen geliefert.
func accountsSum(accounts []protocol.Account) (string, error) {
	j, err := json.Marshal(accounts)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(j)
	return hex.EncodeToString(s[:]), nil
}

// Accepted trägt in den Merker ein, dass der Server Parts[k] angenommen hat, und speichert ihn. Davor sind alle
// früheren Pakete angenommen. Je Datei gilt danach: alle Zeilen bis vor die erste noch nicht gelieferte Hülle sind
// erledigt, ausgesonderte eingeschlossen; ist keine mehr offen, die ganze Datei. rejected sind die Tage, die der
// Server in diesem Paket abgewiesen hat; gemerkt wird nur, was im Paket stand. newDays zählt die neu abgewiesenen.
func (dl *Delivery) Accepted(k int, r client.IngestResult) (newDays int, err error) {
	n := dl.cut[k]
	next := make([]int, len(dl.sources))
	for i := range next {
		next[i] = dl.total[i]
	}
	for _, it := range dl.items[n:] {
		next[it.src] = min(next[it.src], it.line)
	}
	sent := map[string]bool{}
	for _, it := range dl.items[:n] {
		if it.date != "" {
			sent[it.date] = true
		}
	}
	d := dl.done
	for i, src := range dl.sources {
		d.Damaged += countIn(dl.damagedAt[i], d.Lines[src.Name], next[i])
		d.Foreign += countIn(dl.foreignAt[i], d.Lines[src.Name], next[i])
		if next[i] > 0 {
			d.Lines[src.Name] = next[i]
		}
	}
	for _, day := range r.LegacyRejectedDays {
		if sent[day] && !slices.Contains(d.RejectedDays, day) {
			d.RejectedDays = append(d.RejectedDays, day)
			newDays++
		}
	}
	if k == 0 {
		d.AccountsSum = dl.accountsSum
	}
	return newDays, d.Save(dl.dir)
}

// SkipDamaged trägt ein, dass es nichts zu senden gibt: Ausgesonderte Zeilen (beschädigt oder fremd) gelten als
// erledigt, damit sie nicht bei jedem Abgleich wieder auftauchen. Ohne ausgesonderte Zeilen schreibt es nichts.
func (dl *Delivery) SkipDamaged() error {
	if dl.Damaged == 0 && dl.Foreign == 0 {
		return nil
	}
	d := dl.done
	for i, src := range dl.sources {
		d.Damaged += countIn(dl.damagedAt[i], d.Lines[src.Name], dl.total[i])
		d.Foreign += countIn(dl.foreignAt[i], d.Lines[src.Name], dl.total[i])
		d.Lines[src.Name] = dl.total[i]
	}
	return d.Save(dl.dir)
}

// countIn zählt die Zeilennummern in at mit from <= at < to.
func countIn(at []int, from, to int) int {
	n := 0
	for _, a := range at {
		if a >= from && a < to {
			n++
		}
	}
	return n
}

// envelopeAt ist die Hülle, auf die ein Pfad des Servers wie requests[3].s oder legacy_days[0].s in Paket p zeigt.
func envelopeAt(p protocol.Batch, path string) (protocol.Envelope, bool) {
	list, rest := p.Requests, ""
	if r, ok := strings.CutPrefix(path, "requests["); ok {
		rest = r
	} else if r, ok := strings.CutPrefix(path, "legacy_days["); ok {
		list, rest = p.LegacyDays, r
	}
	num, ok := strings.CutSuffix(rest, "].s")
	if !ok {
		return protocol.Envelope{}, false
	}
	i, err := strconv.Atoi(num)
	if err != nil || i < 0 || i >= len(list) || strconv.Itoa(i) != num {
		return protocol.Envelope{}, false
	}
	return list[i], true
}

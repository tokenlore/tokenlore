package claudecode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/source"
	"github.com/tokenlore/tokenlore/state"
)

const headMax = 4096

// IdleAfter: Hat sich eine Sitzungsdatei so lange nicht geändert, gilt auch ihre letzte Anfrage als fertig.
const IdleAfter = 5 * time.Minute

// ClaudeCode ist die Quelle für Claude-Code-Sitzungsdateien.
type ClaudeCode struct {
	ConfigDir string
	Loc       *time.Location // Ortszeit des Geräts für utc_offset_min
	Now       func() time.Time
	// PairedIdentity ist die Kennung, mit der gekoppelt wurde (Connection.Identity). Weicht die Kennungsdatei
	// davon ab, legt Scan keinen Altbestand bei (deliver liefert dann ohnehin nichts, siehe deliver.CheckDevice);
	// leer heißt: Kopplung ohne Kennung, nichts zu vergleichen.
	PairedIdentity string
	// Ended sind die Hauptdateien beendeter Sitzungen (relativ zu ConfigDir, mit /, wie EndedRel sie liefert): Ihre
	// letzte Anfrage und die ihrer Subagenten gelten sofort als fertig (Hook SessionEnd).
	Ended []string
	// Getenv liest Umgebungsvariablen (das Konto, das die Desktop-App übergibt); nil heißt os.Getenv.
	Getenv func(string) string
}

// EndedRel macht aus dem Protokollnamen, den der Hook SessionEnd nennt (transcript_path), den Namen der Hauptdatei
// relativ zu configDir, wie Scan ihn führt: projects/<ordner>/<sitzung>.jsonl. Ein Name außerhalb davon gilt nicht;
// die Anfrage kommt dann beim nächsten Abgleich, wenn sie nach den übrigen Regeln fertig ist.
func EndedRel(configDir, transcript string) (string, bool) {
	if transcript == "" || !filepath.IsAbs(transcript) {
		return "", false
	}
	try := func(base, p string) (string, bool) {
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return "", false
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 3 || parts[0] != "projects" || parts[1] == ".." || !strings.HasSuffix(parts[2], ".jsonl") {
			return "", false
		}
		return strings.Join(parts, "/"), true
	}
	if rel, ok := try(filepath.Clean(configDir), filepath.Clean(transcript)); ok {
		return rel, true
	}
	// Verknüpfte Ordner (etwa ~/.claude als Link): beide Seiten aufgelöst vergleichen.
	base, err1 := filepath.EvalSymlinks(configDir)
	p, err2 := filepath.EvalSymlinks(transcript)
	if err1 != nil || err2 != nil {
		return "", false
	}
	return try(base, p)
}

// ended meldet, ob rel zu einer beendeten Sitzung gehört: ihre Hauptdatei oder eine Datei ihrer Subagenten.
func (c *ClaudeCode) ended(rel string) bool {
	for _, e := range c.Ended {
		if rel == e || strings.HasPrefix(rel, strings.TrimSuffix(e, ".jsonl")+"/subagents/") {
			return true
		}
	}
	return false
}

func (c *ClaudeCode) Name() string { return "claude-code" }

// Scan liest alle Sitzungsdateien ab ihrem Lesestand. st bleibt unverändert; die neuen Lesestände stehen in
// ScanResult.Next und werden gespeichert, sobald die gelesenen Anfragen im Archiv stehen.
func (c *ClaudeCode) Scan(ctx context.Context, st *state.State) (source.ScanResult, error) {
	next := st.Clone()
	res := source.ScanResult{Next: next}
	files, err := listSessionFiles(c.ConfigDir)
	if err != nil {
		return res, err
	}
	newFields := map[string]bool{}
	held := map[string]bool{}
	// owners: Konto je Sitzung aus ownerAccountUuid, über alle Dateien dieses Laufs (Hauptdatei und Subagenten).
	owners := map[string]string{}
	getenv := c.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	app, appOK := AppAccount(getenv)
	login, loginOK, loginErr := ReadCurrentAccount(ClaudeJSONPath(c.ConfigDir))
	loginOK = loginOK && protocol.IDPattern.MatchString(login.AccountID)
	switch {
	case appOK:
		res.CurrentAccountID, res.CurrentEmail = app.AccountID, app.Email
		if loginOK && login.AccountID != app.AccountID {
			res.OtherAccountID, res.OtherEmail = login.AccountID, login.Email
		}
	case loginOK:
		res.CurrentAccountID, res.CurrentEmail = login.AccountID, login.Email
	}
	if loginErr != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("angemeldetes Konto nicht lesbar: %v", loginErr))
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		fr, err := c.scanFile(f, st, next, owners)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: nicht lesbar (%v)", f.Rel, errors.Unwrap(err)))
			continue
		}
		res.Warnings = append(res.Warnings, fr.warnings...)
		res.Lines = append(res.Lines, fr.lines...)
		res.Errors = append(res.Errors, fr.errors...)
		for _, n := range fr.newFields {
			newFields[n] = true
		}
		for _, m := range fr.heldMonths {
			held[m] = true
		}
	}
	// Konto aus dem Protokoll, auch wenn es erst in einer anderen Datei derselben Sitzung steht. Fehlt es, ergänzt
	// das Archiv das Konto (archive.AddRequests).
	for i := range res.Lines {
		if res.Lines[i].AccountID == "" {
			res.Lines[i].AccountID = owners[res.Lines[i].SessionID]
		}
	}
	for i := range res.Errors {
		if res.Errors[i].AccountID == "" {
			res.Errors[i].AccountID = owners[res.Errors[i].SessionID]
		}
	}
	res.HeldMonths = slices.Sorted(maps.Keys(held))
	res.NewFields = slices.Sorted(maps.Keys(newFields))
	if len(res.NewFields) > state.MaxNewFields {
		res.NewFields = res.NewFields[:state.MaxNewFields]
	}
	ic, err := CheckIdentity(c.ConfigDir, st.Identity, c.PairedIdentity)
	if err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Kennungsdatei %s nicht lesbar: %v", IdentityFile, err))
	}
	res.IdentityChanged = ic.Changed
	if err == nil && ic.Known && !ic.Changed {
		cur := ic.Current
		next.Identity = &cur // die Datei gilt; die Kopie im Merkzettel zieht nach
	}
	return res, nil
}

type fileResult struct {
	lines  []protocol.RequestLine
	errors []protocol.ErrorLine
	// newFields: Namen unbekannter Schlüssel in Anfragezeilen, mit Wiederholungen.
	newFields []string
	// heldMonths: Monate (UTC) der zurückgehaltenen, noch nicht fertigen Anfragen dieser Datei.
	heldMonths []string
	warnings   []string
}

// scanFile liest eine Sitzungsdatei ab ihrem Lesestand. Übernommen werden nur fertige Anfragen (protocol/doc.md,
// „Inhaltszeile: Anfrage“): Nach ihrer letzten Zeile folgt eine Zeile einer anderen Anfrage mit späterem Zeitpunkt,
// die Datei hat sich seit IdleAfter nicht geändert, oder ihre Sitzung ist beendet (Ended). Der Lesestand bleibt vor der ersten zurückgehaltenen
// Anfrage; steht er so (Offset < End), liest der nächste Lauf die Datei auch ohne Größenänderung erneut. Sind alle
// Anfragen fertig, springt er auf das Ende, und die Datei wird erst wieder gelesen, wenn sie wächst.
// owners sammelt das Konto je Sitzung aus ownerAccountUuid.
func (c *ClaudeCode) scanFile(f sessionFile, st, next *state.State, owners map[string]string) (fileResult, error) {
	var fr fileResult
	full := filepath.Join(c.ConfigDir, filepath.FromSlash(f.Rel))
	fh, err := os.Open(full)
	if err != nil {
		return fr, fmt.Errorf("öffnen: %w", err)
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil {
		return fr, fmt.Errorf("stat: %w", err)
	}
	size := info.Size()
	// Fertig ist auch die letzte Anfrage, wenn die Datei ruht oder ihre Sitzung beendet ist.
	idle := c.Now().Sub(info.ModTime()) >= IdleAfter || c.ended(f.Rel)

	var start int64
	if mark, ok := st.Files[f.Rel]; ok && size >= mark.End && mark.Offset <= mark.End {
		sum, err := headSum(fh, mark.HeadLen)
		if err == nil && sum == mark.HeadSum {
			if size == mark.End && mark.Offset == mark.End {
				return fr, nil // nichts Neues, nichts zurückgehalten
			}
			start = mark.Offset
		}
	}
	if _, err := fh.Seek(start, io.SeekStart); err != nil {
		return fr, fmt.Errorf("springen: %w", err)
	}

	var (
		r          = bufio.NewReaderSize(fh, 1<<16)
		pos        = start
		end        = start
		aggs       = map[string]*reqAgg{}
		keys       []string
		firstStart = map[string]int64{}
		firstTS    = map[string]time.Time{}
		lastIdx    = map[string]int{} // Index der letzten Zeile der Anfrage in seq
		seq        []time.Time        // Zeitpunkte aller Anfragezeilen, in Dateireihenfolge
		fileSID    string             // erste sessionId der Datei, für Zeilen ohne eigene
	)
	for {
		b, err := r.ReadBytes('\n')
		if err != nil {
			break // EOF; eine Zeile ohne \n wird noch geschrieben und erst beim nächsten Mal gelesen
		}
		lineStart := pos
		pos += int64(len(b))
		end = pos
		l, perr := parseLine(b)
		if perr != nil {
			fr.warnings = append(fr.warnings, fmt.Sprintf("%s: unlesbare Zeile bei Byte %d übersprungen", f.Rel, lineStart))
			continue
		}
		if l.SessionID != "" {
			if fileSID == "" {
				fileSID = l.SessionID
			}
			if l.OwnerAccountUUID != "" && protocol.IDPattern.MatchString(l.OwnerAccountUUID) {
				owners[l.SessionID] = l.OwnerAccountUUID
			}
		}
		// Manche Backends liefern keine requestId; dann ist message.id allein der Schlüssel. Eine Fehlerzeile ist eine
		// Einheit für sich und wird fertig wie eine Anfrage; ihr Schlüssel trifft nie den einer Anfrage.
		if l.Type != "assistant" || (l.Usage == nil && !l.APIError) || l.MessageID == "" {
			continue
		}
		k := l.MessageID + "|" + l.RequestID
		if l.APIError {
			k = "error|" + k
		}
		lt, _ := time.Parse(time.RFC3339Nano, l.Timestamp) // nicht lesbar: Nullzeit, gibt nichts frei
		if aggs[k] == nil {
			aggs[k] = &reqAgg{}
			keys = append(keys, k)
			firstStart[k] = lineStart
			firstTS[k] = lt
		}
		aggs[k].add(l)
		lastIdx[k] = len(seq)
		seq = append(seq, lt)
		fr.newFields = append(fr.newFields, l.NewFields...)
	}

	// later[i] ist der späteste Zeitpunkt der Anfragezeilen nach Position i.
	later := make([]time.Time, len(seq)+1)
	for i := len(seq) - 1; i >= 0; i-- {
		later[i] = later[i+1]
		if seq[i].After(later[i]) {
			later[i] = seq[i]
		}
	}
	// Lesestand vor die erste zurückgehaltene Anfrage: Ihre Zeilen können noch unvollständig sein.
	offset := end
	finished := map[string]bool{}
	heldMonths := map[string]bool{}
	for _, k := range keys {
		if idle || later[lastIdx[k]+1].After(firstTS[k]) {
			finished[k] = true
			continue
		}
		offset = min(offset, firstStart[k])
		if !firstTS[k].IsZero() {
			heldMonths[firstTS[k].UTC().Format("2006-01")] = true
		}
	}
	fr.heldMonths = slices.Sorted(maps.Keys(heldMonths))
	headLen := int(min(int64(headMax), size))
	sum, err := headSum(fh, headLen)
	if err != nil {
		return fr, fmt.Errorf("prüfsumme: %w", err)
	}
	next.Files[f.Rel] = state.FileMark{Offset: offset, End: end, HeadLen: headLen, HeadSum: sum}

	invalid := 0
	for _, k := range keys {
		if !finished[k] {
			continue
		}
		a := aggs[k]
		if a.first.APIError {
			e, ok := a.toErrorLine(c.Loc, f.Subagent)
			if e.SessionID == "" {
				e.SessionID = fileSID
			}
			if !ok || protocol.ValidateErrorLine(e, c.Now()) != nil {
				invalid++
				continue
			}
			fr.errors = append(fr.errors, e)
			continue
		}
		l, ok := a.toLine(c.Loc, f.Subagent)
		if !ok {
			if m := a.first.Model; m != "" && m != "<synthetic>" {
				invalid++
			}
			continue
		}
		if l.SessionID == "" {
			l.SessionID = fileSID
		}
		if protocol.ValidateRequestLine(l, c.Now()) != nil {
			invalid++
			continue
		}
		fr.lines = append(fr.lines, l)
	}
	if invalid > 0 {
		fr.warnings = append(fr.warnings, fmt.Sprintf("%s: %d ungültige Anfrage(n) ausgelassen", f.Rel, invalid))
	}
	return fr, nil
}

// headSum ist die sha256 der ersten n Bytes der Datei, hex.
func headSum(f *os.File, n int) (string, error) {
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, 0); err != nil && !(errors.Is(err, io.EOF) && n == 0) {
		return "", err
	}
	s := sha256.Sum256(buf)
	return hex.EncodeToString(s[:]), nil
}

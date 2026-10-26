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
	"time"

	"github.com/tokenlore/tokenlore/email"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/source"
	"github.com/tokenlore/tokenlore/state"
)

const headMax = 4096

// ClaudeCode ist die Quelle für Claude-Code-Sitzungsdateien.
type ClaudeCode struct {
	ConfigDir string
	Loc       *time.Location // Ortszeit des Geräts für utc_offset_min
	Now       func() time.Time
}

func (c *ClaudeCode) Name() string { return "claude-code" }

// Scan liest alle Sitzungsdateien ab ihrem Lesestand. st bleibt unverändert; der neue
// Merkzettel steht in ScanResult.Next und wird erst nach erfolgreicher Lieferung gespeichert.
func (c *ClaudeCode) Scan(ctx context.Context, st *state.State) (source.ScanResult, error) {
	next := st.Clone()
	res := source.ScanResult{Next: next}
	files, err := listSessionFiles(c.ConfigDir)
	if err != nil {
		return res, err
	}
	sessions := map[string]*protocol.Session{}
	var order []string
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		fr, err := c.scanFile(f, st, next)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: nicht lesbar (%v)", f.Rel, errors.Unwrap(err)))
			continue
		}
		res.Warnings = append(res.Warnings, fr.warnings...)
		res.Batch.Requests = append(res.Batch.Requests, fr.requests...)
		for _, fs := range fr.sessions {
			s := sessions[fs.SessionID]
			if s == nil {
				s = &protocol.Session{SessionID: fs.SessionID}
				sessions[s.SessionID] = s
				order = append(order, s.SessionID)
			}
			// Hauptdatei und Subagenten-Dateien teilen die Sitzung; gefüllte Werte gewinnen.
			if s.ProjectID == "" {
				s.ProjectID = fs.ProjectID
			}
			if fs.CCVersion != "" {
				s.CCVersion = fs.CCVersion
			}
			if s.AccountID == "" {
				s.AccountID = fs.AccountID
			}
		}
	}
	for _, id := range order {
		res.Batch.Sessions = append(res.Batch.Sessions, *sessions[id])
	}
	accounts, err := c.accounts(next)
	if err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("angemeldetes Konto nicht lesbar: %v", err))
	}
	res.Batch.Accounts = accounts
	if !st.LegacyDone {
		models, acts, err := readLegacy(c.ConfigDir)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("stats-cache.json nicht lesbar: %v", err))
		} else {
			models, acts, dropped := c.filterLegacy(models, acts)
			if dropped > 0 {
				res.Warnings = append(res.Warnings, fmt.Sprintf("stats-cache.json: %d ungültige Tageswerte ausgelassen", dropped))
			}
			res.Batch.LegacyModel, res.Batch.LegacyActivity = models, acts
			next.LegacyDone = true
		}
	}
	return res, nil
}

// filterLegacy lässt Tageswerte weg, die der Server ablehnen würde, damit sie nicht die ganze Lieferung blockieren.
func (c *ClaudeCode) filterLegacy(models []protocol.LegacyModelDay, acts []protocol.LegacyActivityDay) ([]protocol.LegacyModelDay, []protocol.LegacyActivityDay, int) {
	now := c.Now()
	dropped := 0
	var m []protocol.LegacyModelDay
	for _, d := range models {
		if protocol.ValidateLegacyModel(d, now) != nil {
			dropped++
			continue
		}
		m = append(m, d)
	}
	var a []protocol.LegacyActivityDay
	for _, d := range acts {
		if protocol.ValidateLegacyActivity(d, now) != nil {
			dropped++
			continue
		}
		a = append(a, d)
	}
	return m, a, dropped
}

// accounts liefert alle bekannten Konten mit der E-Mail-Stufe des Geräts. Eine Adresse gibt es
// nur für das gerade angemeldete Konto, weil nur dessen Adresse auf dem Gerät steht.
func (c *ClaudeCode) accounts(st *state.State) ([]protocol.Account, error) {
	cur, ok, err := ReadCurrentAccount(ClaudeJSONPath(c.ConfigDir))
	ids := map[string]bool{}
	for _, a := range st.Sessions {
		ids[a] = true
	}
	if ok {
		ids[cur.AccountID] = true
	}
	var out []protocol.Account
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		a := protocol.Account{AccountID: id, EmailMode: st.EmailMode}
		if ok && id == cur.AccountID {
			a.Email = email.ForMode(st.EmailMode, cur.Email)
		}
		if protocol.ValidateAccount(a) != nil {
			a.Email = "" // ungültige Adresse lieber weglassen als das ganze Paket gefährden
		}
		if protocol.ValidateAccount(a) != nil {
			continue
		}
		out = append(out, a)
	}
	return out, err
}

type fileResult struct {
	sessions []protocol.Session
	requests []protocol.Request
	warnings []string
}

func (c *ClaudeCode) scanFile(f sessionFile, st, next *state.State) (fileResult, error) {
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

	var start int64
	if mark, ok := st.Files[f.Rel]; ok && size >= mark.End && mark.Offset <= mark.End {
		sum, err := headSum(fh, mark.HeadLen)
		if err == nil && sum == mark.HeadSum {
			if size == mark.End {
				return fr, nil // nichts Neues
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
		lastKey    string
		fileSID    string   // erste sessionId der Datei, für Zeilen ohne eigene
		sids       []string // Sitzungen in Reihenfolge des Auftretens
		version    = map[string]string{}
		owner      = map[string]string{}
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
			if _, ok := version[l.SessionID]; !ok {
				version[l.SessionID] = ""
				sids = append(sids, l.SessionID)
			}
			if l.Version != "" {
				version[l.SessionID] = l.Version
			}
			if l.OwnerAccountUUID != "" {
				owner[l.SessionID] = l.OwnerAccountUUID
			}
		}
		// Manche Backends liefern keine requestId; dann ist message.id allein der Schlüssel.
		if l.Type != "assistant" || l.Usage == nil || l.MessageID == "" {
			continue
		}
		k := l.MessageID + "|" + l.RequestID
		if aggs[k] == nil {
			aggs[k] = &reqAgg{}
			keys = append(keys, k)
			firstStart[k] = lineStart
		}
		aggs[k].add(l)
		lastKey = k
	}

	// Lesestand vor die letzte Anfrage setzen: ihre Zeilen können noch unvollständig sein.
	offset := end
	if lastKey != "" {
		offset = firstStart[lastKey]
	}
	headLen := int(min(int64(headMax), size))
	sum, err := headSum(fh, headLen)
	if err != nil {
		return fr, fmt.Errorf("prüfsumme: %w", err)
	}
	next.Files[f.Rel] = state.FileMark{Offset: offset, End: end, HeadLen: headLen, HeadSum: sum}

	for sid, acc := range owner {
		next.Sessions[sid] = acc
	}

	used := map[string]bool{}
	invalid := 0
	for _, k := range keys {
		a := aggs[k]
		req, ok := a.toRequest(c.Loc, f.Subagent)
		if !ok {
			if m := a.first.Model; m != "" && m != "<synthetic>" {
				invalid++
			}
			continue
		}
		if req.SessionID == "" {
			req.SessionID = fileSID
		}
		req.AccountID = next.Sessions[req.SessionID]
		if protocol.ValidateRequest(req, c.Now()) != nil {
			invalid++
			continue
		}
		used[req.SessionID] = true
		fr.requests = append(fr.requests, req)
	}
	if invalid > 0 {
		fr.warnings = append(fr.warnings, fmt.Sprintf("%s: %d ungültige Anfrage(n) ausgelassen", f.Rel, invalid))
	}
	project := ProjectID(st.Salt, f.Project)
	for _, sid := range sids {
		if !used[sid] {
			continue
		}
		s := protocol.Session{SessionID: sid, ProjectID: project, CCVersion: version[sid], AccountID: next.Sessions[sid]}
		if protocol.ValidateSession(s) != nil {
			s.CCVersion = "" // einzig frei wählbarer Wert; Kennungen sind schon geprüft
		}
		fr.sessions = append(fr.sessions, s)
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

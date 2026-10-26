package deliver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/source"
	"github.com/tokenlore/tokenlore/state"
)

// maxRetries ist die Zahl der Wiederholungen je Paket bei 429/503 (also höchstens 4 Versuche).
const maxRetries = 3

const (
	msgUnauthorized = "Gerät gesperrt oder Schlüssel ungültig – neu koppeln mit /tokenlore:connect"
	msgBusy         = "Server ausgelastet, nächster Versuch beim nächsten Sessionstart"
	msgTimeout      = "Zeitgrenze erreicht, nächster Versuch beim nächsten Sessionstart"
	msgNotPaired    = "Nicht gekoppelt – erst /tokenlore:connect ausführen"
)

var errNotPaired = errors.New(msgNotPaired)

// Sender liefert ein fertiges Paket aus (in Betrieb *client.Client).
type Sender interface {
	Ingest(ctx context.Context, key string, body []byte) (client.IngestResult, error)
}

// Deps sind die Abhängigkeiten eines Abgleichs. Now und Sleep sind für Tests austauschbar;
// fehlen sie, gelten die Uhr und ein abbrechbares Warten.
type Deps struct {
	DataDir, ConfigDir, Version string
	Key                         string
	Sender                      Sender
	Now                         func() time.Time
	Sleep                       func(ctx context.Context, d time.Duration) error
}

// Report fasst einen Abgleich zusammen.
type Report struct {
	Parts, Requests, Flagged, Warnings int
	NothingNew                         bool
}

// Run liest neue Nutzungszahlen ein, sendet sie paketweise und speichert den Merkzettel nur,
// wenn alle Pakete angenommen wurden. Sonst bleibt er unverändert und der nächste Lauf
// sendet wieder ab Paket 1 (der Server zählt jede Anfrage nur einmal).
// Der Status wird immer geschrieben. Fehlertexte sind bereinigt und enthalten nie den Schlüssel.
func Run(ctx context.Context, d Deps) (Report, error) {
	now, sleep := d.Now, d.Sleep
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepCtx
	}
	var rep Report
	stat, err := state.LoadStatus(d.DataDir)
	if err != nil {
		stat = &state.Status{} // kaputter Status ist nur Anzeige und darf den Abgleich nicht verhindern
	}
	stat.LastAttempt = now()
	fail := func(err error) (Report, error) {
		stat.LastError = message(err, d.Key)
		_ = stat.Save(d.DataDir) // Status ist nur Anzeige; der eigentliche Fehler ist wichtiger
		return rep, errors.New(stat.LastError)
	}

	st, err := state.Load(d.DataDir)
	if err != nil {
		return fail(fmt.Errorf("Merkzettel nicht lesbar: %w", err))
	}
	// Ohne Projekt-Salz gingen Sitzungen ohne project_id raus und die Lesestände würden trotzdem
	// gemerkt: Die Zuordnung zum Projekt wäre für immer verloren. Also gar nicht erst einlesen.
	if !protocol.ProjectPattern.MatchString(st.Salt) {
		return fail(errNotPaired)
	}
	res, parts, err := Prepare(ctx, d.ConfigDir, d.Version, st, now)
	// Warnungen enthalten Pfade: Sie landen nur im lokalen Status, nie in einem Paket.
	// Dateinamen in Warnungen stammen von der Platte und könnten Terminal-Escapes enthalten.
	stat.Warnings = cleanAll(lastN(res.Warnings, 20))
	rep.Warnings = len(res.Warnings)
	if err != nil {
		return fail(err)
	}
	sum, err := accountsSum(res.Batch.Accounts)
	if err != nil {
		return fail(err)
	}

	b := res.Batch
	if nothingNew(b, sum, st) {
		// Neue Lesestände trotzdem merken (z. B. Zeilen ohne Nutzungszahlen), es gibt nichts zu liefern.
		if err := res.Next.Save(d.DataDir); err != nil {
			return fail(fmt.Errorf("Merkzettel nicht gespeichert: %w", err))
		}
		rep.NothingNew = true
		succeed(stat, rep)
		_ = stat.Save(d.DataDir)
		return rep, nil
	}

	flagged := 0
	for _, p := range parts {
		body, err := json.Marshal(p)
		if err != nil {
			return fail(err)
		}
		r, err := send(ctx, d, sleep, body)
		if err != nil {
			return fail(err)
		}
		flagged += len(r.FlaggedSessions)
		// Der Altbestand ist angenommen: Das muss sofort feststehen, nicht erst nach dem letzten Paket.
		// Sonst schickte eine Neu-Kopplung nach einem späteren Fehler ihn unter neuer Geräte-ID noch einmal,
		// und der Server zählte ihn doppelt. Die Lesestände bleiben dabei unverändert.
		if (len(p.LegacyModel) > 0 || len(p.LegacyActivity) > 0) && !st.LegacySent {
			st.LegacySent = true
			if err := st.Save(d.DataDir); err != nil {
				return fail(fmt.Errorf("Merkzettel nicht gespeichert: %w", err))
			}
		}
	}
	res.Next.LegacySent = res.Next.LegacySent || st.LegacySent

	res.Next.Delivered = true
	res.Next.AccountsSum = sum
	if err := res.Next.Save(d.DataDir); err != nil {
		return fail(fmt.Errorf("Merkzettel nicht gespeichert: %w", err))
	}
	rep.Parts, rep.Requests, rep.Flagged = len(parts), len(b.Requests), flagged
	succeed(stat, rep)
	_ = stat.Save(d.DataDir)
	return rep, nil
}

// NothingNew meldet, ob eine Lieferung nichts enthielte, was der Server noch nicht hat: keine Anfragen,
// Sitzungen und kein Altbestand, und die Kontenliste ist unverändert. status nutzt dieselbe Regel wie Run.
func NothingNew(st *state.State, res source.ScanResult) bool {
	sum, err := accountsSum(res.Batch.Accounts)
	return err == nil && nothingNew(res.Batch, sum, st)
}

func nothingNew(b protocol.Batch, sum string, st *state.State) bool {
	return len(b.Requests) == 0 && len(b.Sessions) == 0 && len(b.LegacyModel) == 0 &&
		len(b.LegacyActivity) == 0 && sum == st.AccountsSum
}

// send liefert ein Paket; bei 429/503 wartet es die vom Server genannte Zeit und versucht es bis zu
// maxRetries-mal erneut mit demselben Rumpf.
func send(ctx context.Context, d Deps, sleep func(context.Context, time.Duration) error, body []byte) (client.IngestResult, error) {
	for attempt := 0; ; attempt++ {
		r, err := d.Sender.Ingest(ctx, d.Key, body)
		var re *client.RetryError
		if err == nil || !errors.As(err, &re) || attempt == maxRetries {
			return r, err
		}
		if err := sleep(ctx, re.After); err != nil {
			return r, err
		}
	}
}

func succeed(stat *state.Status, rep Report) {
	stat.LastSuccess = stat.LastAttempt
	stat.LastError = ""
	stat.LastRequests, stat.LastParts, stat.Flagged = rep.Requests, rep.Parts, rep.Flagged
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

// message macht aus einem Fehler einen deutschen Text für Status und Ausgabe: ohne Steuerzeichen,
// höchstens 200 Zeichen, und ohne den Geräteschlüssel, selbst wenn ein Fehler ihn enthielte.
func message(err error, key string) string {
	var (
		rej   *client.RejectedError
		retry *client.RetryError
		terr  *client.TransportError
		s     string
	)
	switch {
	case errors.Is(err, errNotPaired):
		s = msgNotPaired
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		s = msgTimeout
	case client.IsCertError(err):
		s = client.CertMessage(err)
	case errors.Is(err, client.ErrUnauthorized):
		s = msgUnauthorized
	case errors.As(err, &rej):
		s = "Server lehnt Paket ab"
		switch {
		case rej.Path != "" && rej.Msg != "":
			s += ": " + rej.Path + ": " + rej.Msg
		case rej.Path != "" || rej.Msg != "":
			s += ": " + rej.Path + rej.Msg
		default:
			s += fmt.Sprintf(" (Status %d)", rej.Status)
		}
		// Ein abgelehnter Zeitpunkt liegt fast immer an einer falsch gehenden Uhr dieses Geräts.
		if strings.HasSuffix(rej.Path, ".ts") {
			s += " – Systemuhr prüfen"
		}
	case errors.As(err, &retry):
		s = msgBusy
	case errors.As(err, &terr):
		s = terr.Error() // beginnt mit "Server nicht erreichbar: "
	default:
		s = err.Error()
	}
	return client.Clean(scrubKey(s, key))
}

// scrubKey entfernt den Schlüssel und seinen zufälligen Teil vor dem Kürzen, damit kein Teilstück übrig bleibt.
func scrubKey(s, key string) string {
	if key == "" {
		return s
	}
	s = strings.ReplaceAll(s, key, "[Schlüssel]")
	if r := strings.TrimPrefix(key, "tl_dk_"); r != "" {
		s = strings.ReplaceAll(s, r, "[Schlüssel]")
	}
	return s
}

func cleanAll(s []string) []string {
	out := make([]string, 0, len(s))
	for _, w := range s {
		out = append(out, client.Clean(w))
	}
	return out
}

func lastN(s []string, n int) []string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

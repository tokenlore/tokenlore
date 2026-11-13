package deliver

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/source"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// maxRetries ist die Zahl der Wiederholungen je Paket bei 429/503 (also höchstens 4 Versuche).
const maxRetries = 3

const (
	// msgUnauthorized, MsgRepaired, MsgRevoked und MsgUnknownKey sind die Meldungen zu einem 401 (protocol/doc.md,
	// „Liefern“). Die Schritte zum Neukoppeln hängen status und Sessionstart mit RepairSteps an.
	msgUnauthorized = "Gerät gesperrt oder Schlüssel ungültig, bitte neu koppeln."
	MsgRepaired     = "Dieses Gerät wurde anderswo neu gekoppelt, der Schlüssel hier gilt nicht mehr. Soll dieser Rechner weiter liefern, kopple ihn neu."
	MsgRevoked      = "Dieses Gerät ist auf dem Server gesperrt. Soll es wieder liefern, kopple es neu."
	MsgUnknownKey   = "Der Server kennt den Schlüssel dieses Geräts nicht, bitte neu koppeln."
	msgBusy         = "Server ausgelastet, nächster Versuch beim nächsten Sessionstart"
	msgTimeout      = "Zeitgrenze erreicht, nächster Versuch beim nächsten Sessionstart"
	msgNotPaired    = "Nicht gekoppelt – erst /tokenlore:connect ausführen"
)

var errNotPaired = errors.New(msgNotPaired)

// outdatedPrefix beginnt jede Meldung zu einem 426; IsOutdatedMessage erkennt sie daran im Status wieder.
const (
	outdatedPrefix = "Bitte tokenlore "
	updateSteps    = "claude plugin marketplace update tokenlore, dann claude plugin update tokenlore@tokenlore, danach /reload-plugins."
)

// OutdatedMessage ist die Meldung zu einem 426 (protocol/doc.md, „Mindestversion“): Mindestversion und der Befehl zum
// Aktualisieren. min ist leer, wenn der Server keine gültige nennt. Connect, Abgleich, status und Sessionstart zeigen sie.
func OutdatedMessage(min string) string {
	if min == "" {
		return outdatedPrefix + "aktualisieren: " + updateSteps
	}
	return outdatedPrefix + "auf mindestens " + min + " aktualisieren: " + updateSteps
}

// IsOutdatedMessage meldet, ob s eine Meldung von OutdatedMessage ist (etwa der letzte Fehler im Status).
func IsOutdatedMessage(s string) bool {
	return strings.HasPrefix(s, outdatedPrefix) && strings.HasSuffix(s, updateSteps)
}

// Sender liefert ein fertiges Paket aus (in Betrieb *client.Client).
type Sender interface {
	Ingest(ctx context.Context, key string, body []byte) (client.IngestResult, error)
	Info(ctx context.Context) (client.ServerInfo, error)
}

// Deps sind die Abhängigkeiten eines Abgleichs. Now und Sleep sind für Tests austauschbar;
// fehlen sie, gelten die Uhr und ein abbrechbares Warten.
type Deps struct {
	DataDir, ConfigDir, Version string
	Key                         string
	// Identity ist die Kennung, mit der gekoppelt wurde (Connection.Identity); leer = nicht zu prüfen.
	Identity string
	// Machine ist der Hash der Systemkennung dieses Rechners; leer = nicht lesbar, dann wird nicht verglichen.
	Machine string
	// SignKey ist der private Signaturschlüssel dieses Rechners; ohne ihn schreibt der Abgleich kein Archiv und liefert nichts.
	SignKey ed25519.PrivateKey
	// Ended sind die Hauptdateien beendeter Sitzungen (claudecode.EndedRel, Hook SessionEnd): Ihre letzte Anfrage und
	// die ihrer Subagenten gelten sofort als fertig. Leer = nur die üblichen Regeln.
	Ended  []string
	Sender Sender
	Now    func() time.Time
	Sleep  func(ctx context.Context, d time.Duration) error
}

// Report fasst einen Abgleich zusammen.
type Report struct {
	Parts, Requests, Days, Flagged, Warnings int
	NothingNew                               bool
	// Held: Die Lieferung hat vor der ersten Zeile angehalten, die der Server ohne Merkmal request_details ablehnte.
	Held bool
	// Known zählt die gelieferten Anfragen, die der Server schon hatte; Elsewhere die, die schon einem anderen
	// Benutzer gehören; LegacyRejected die Tage, die der Server in diesem Abgleich neu abgewiesen hat; Damaged die
	// Archivzeilen, die dieser Abgleich als beschädigt ausgesondert hat; Foreign die, die er ausgesondert hat, weil der
	// Server ihren früheren Signaturschlüssel einem anderen Gerät zuordnet.
	Known, Elsewhere, LegacyRejected, Damaged, Foreign int
}

// Run liest neue Nutzungszahlen ein, schreibt sie ins Archiv, speichert die Lesestände und liefert dann alles aus
// dem Archiv, was laut geliefert.json noch offen ist, paketweise. Nach jedem angenommenen Paket rückt der Merker vor;
// scheitert ein Paket, bleibt der Rest offen und geht beim nächsten Lauf (der Server zählt jede Anfrage nur einmal).
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
		var od *client.OldDatabaseError
		stat.LastErrorOldDatabase = errors.As(err, &od)
		// Bleibt stehen, bis eine Lieferung gelingt: Der Sessionstart meldet es, auch wenn danach z. B. das Netz fehlte.
		if errors.Is(err, client.ErrUnauthorized) {
			stat.KeyRejected = stat.LastError
		}
		_ = stat.Save(d.DataDir) // Status ist nur Anzeige; der eigentliche Fehler ist wichtiger
		return rep, errors.New(stat.LastError)
	}

	// Gekoppelt heißt: eine lesbare connection.json mit Gerätenummer. connect entfernt sie, bevor es Merkzettel und
	// Schlüssel ersetzt, und schreibt sie zuletzt; bricht es mittendrin ab, wird hier gar nicht erst eingelesen.
	if conn, ok, err := state.LoadConnection(d.DataDir); err != nil || !ok || conn.DeviceID <= 0 {
		return fail(errNotPaired)
	}
	st, err := state.Load(d.DataDir)
	if err != nil {
		return fail(fmt.Errorf("Merkzettel nicht lesbar: %w", err))
	}
	// Passt dieser Ordner auf diesem Rechner nicht zur Kopplung, geht gar nichts.
	if CheckDevice(d.ConfigDir, st, d.Identity, d.Machine).Mismatch {
		return fail(errOtherDevice)
	}
	if len(d.SignKey) != ed25519.PrivateKeySize {
		return fail(errors.New(MsgSignKeyMissing))
	}
	id, err := archiveIdentity(d.ConfigDir, st, d.Identity)
	if err != nil {
		return fail(err)
	}
	// Gibt es das Archiv noch nicht oder fehlt es, obwohl es schon angelegt war, liest der Abgleich alle
	// Sitzungsdateien von vorn und übernimmt alles noch Vorhandene; das Archiv entdoppelt, der Server zählt nichts
	// doppelt. So kommt auch alles, was ein Plugin ohne Archiv schon gelesen hatte, ins Archiv.
	dir := state.ArchiveDir(d.DataDir, id.ID)
	recreated := st.ArchiveCreated && !archive.Exists(dir)
	if recreated || !st.ArchiveCreated {
		st = st.Clone()
		st.Files = map[string]state.FileMark{}
	}
	res, err := Prepare(ctx, d.ConfigDir, d.Identity, st, now, d.Ended...)
	// Warnungen enthalten Pfade: Sie landen nur im lokalen Status, nie in einem Paket.
	// Dateinamen in Warnungen stammen von der Platte und könnten Terminal-Escapes enthalten.
	warnings := res.Warnings
	// Neue Felder bleiben gemerkt, auch wenn ihre Zeilen schon geliefert sind; nur Namen, nie Werte.
	stat.AddNewFields(res.NewFields, claudecode.KnownField)
	if err != nil {
		stat.Warnings = cleanAll(lastN(warnings, 20))
		return fail(err)
	}
	warns, err := fillArchive(dir, d.SignKey, now, d.ConfigDir, res)
	warnings = append(warnings, warns...)
	stat.Warnings = cleanAll(lastN(warnings, 20))
	rep.Warnings = len(warnings)
	if err != nil {
		return fail(fmt.Errorf("Archiv nicht geschrieben: %w", err))
	}
	// Was gelesen ist, steht jetzt im Archiv: Lesestände, Kennungskopie und der Merker archiv_angelegt gelten ab hier,
	// auch wenn die Lieferung danach scheitert. Geliefert wird nur noch aus dem Archiv.
	res.Next.ArchiveCreated = true
	if err := res.Next.Save(d.DataDir); err != nil {
		return fail(fmt.Errorf("Merkzettel nicht gespeichert: %w", err))
	}
	if recreated {
		stat.ArchiveRecreated = now()
	}
	// Reste früherer Fassungen: state.json hat seine alten Felder beim Speichern eben verloren, die alte Zuordnung
	// Sitzung → Konto geht hier. Scheitert das, bleibt es eine Warnung; der Abgleich läuft weiter. Die Kennungsdatei
	// schreibt der Abgleich nie, ihre alten Felder räumt connect auf.
	if err := state.RemoveOldFiles(d.DataDir); err != nil {
		warnings = append(warnings, fmt.Sprintf("Alte Datei %s nicht entfernt: %v", state.OldSessionLog, err))
	}

	// Lehnt der Server eine Zeile ab, weil er ihren früheren Signaturschlüssel einem anderen Gerät zuordnet, merkt
	// sich der Abgleich den Schlüssel, baut die Lieferung aus dem Rest neu und versucht es noch einmal. Jeder Durchgang
	// sondert einen weiteren früheren Schlüssel aus, also gibt es höchstens so viele Durchgänge wie frühere Schlüssel.
	foreignBase := -1
	// Ob der Server die neueren Felder und Fehlerzeilen annimmt, fragt der Abgleich höchstens einmal und nur, wenn eine
	// offene Zeile sie hat.
	var details *bool
	hasDetails := func() (bool, error) {
		if details == nil {
			info, err := d.Sender.Info(ctx)
			if err != nil {
				return false, err
			}
			details = new(info.Has(client.FeatureRequestDetails))
		}
		return *details, nil
	}
	for round := 0; ; round++ {
		dl, warns, err := PrepareDelivery(dir, res.Next, d.Version, now(), hasDetails)
		warnings = append(warnings, warns...)
		stat.Warnings = cleanAll(lastN(warnings, 20))
		rep.Warnings = len(warnings)
		if err != nil {
			return fail(err)
		}
		if foreignBase < 0 {
			// Was ein späterer Durchgang noch als beschädigt findet, hat schon der erste gezählt.
			foreignBase, rep.Damaged = dl.done.Foreign, dl.Damaged
		}
		if dl.NothingNew {
			if err := dl.SkipDamaged(); err != nil {
				return fail(fmt.Errorf("%s nicht gespeichert: %w", archive.DeliveredFile, err))
			}
			rep.NothingNew = rep.Parts == 0
			rep.Foreign, rep.Held = dl.done.Foreign-foreignBase, dl.Held
			succeed(stat, rep)
			_ = stat.Save(d.DataDir)
			return rep, nil
		}
		retry := false
		for k, p := range dl.Parts {
			body, err := json.Marshal(p)
			if err != nil {
				return fail(err)
			}
			r, err := send(ctx, d, sleep, body)
			if err != nil {
				// Die Grenze schützt nur vor einer Schleife, falls ein gemerkter Schlüssel doch wieder mitginge.
				if key := foreignKey(err, p, res.Next); key != "" && round <= len(res.Next.SignPublicKeysPrevious) {
					res.Next.SignKeysRefused = append(res.Next.SignKeysRefused, key)
					if err := res.Next.Save(d.DataDir); err != nil {
						return fail(fmt.Errorf("Merkzettel nicht gespeichert: %w", err))
					}
					retry = true
					break
				}
				return fail(err)
			}
			newDays, err := dl.Accepted(k, r)
			if err != nil {
				return fail(fmt.Errorf("%s nicht gespeichert: %w", archive.DeliveredFile, err))
			}
			rep.Parts++
			rep.Requests += len(p.Requests)
			rep.Days += len(p.LegacyDays)
			rep.Flagged += len(r.FlaggedSessions)
			rep.Known += r.KnownRequests
			rep.Elsewhere += r.ElsewhereRequests
			rep.LegacyRejected += newDays
		}
		if retry {
			continue
		}
		rep.Foreign, rep.Held = dl.done.Foreign-foreignBase, dl.Held
		succeed(stat, rep)
		_ = stat.Save(d.DataDir)
		return rep, nil
	}
}

// foreignKey prüft eine Ablehnung beim Liefern: Ist es ein 400 mit reason sign_key_unknown und zeigt sein Pfad auf
// eine Zeile in p, die mit einem früheren, noch nicht ausgesonderten Schlüssel aus st signiert ist, ist das Ergebnis
// dieser Schlüssel. Sonst ist es leer, auch wenn die Zeile mit dem aktuellen Schlüssel signiert ist: Dann hilft nur
// Neukoppeln (MsgSignKeyTaken).
func foreignKey(err error, p protocol.Batch, st *state.State) string {
	var rej *client.RejectedError
	if !errors.As(err, &rej) || rej.Reason != client.ReasonSignKeyUnknown {
		return ""
	}
	env, ok := envelopeAt(p, rej.Path)
	if !ok {
		return ""
	}
	keys, _ := signKeys(st)
	k := signedBy(env, keys)
	if k == st.SignPublicKey {
		return "" // auch k == "": nicht zuzuordnen
	}
	return k
}

// NothingNew meldet, ob ein Abgleich nichts zu senden hätte: keine neu eingelesene Anfrage oder Fehlerzeile und
// nichts Offenes im Archiv. status nutzt dieselbe Regel wie Run.
func NothingNew(res source.ScanResult, dl *Delivery) bool {
	return len(res.Lines) == 0 && len(res.Errors) == 0 && dl != nil && dl.NothingNew
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
	stat.LastErrorOldDatabase = false
	stat.KeyRejected = ""
	stat.LastRequests, stat.LastParts, stat.Flagged = rep.Requests, rep.Parts, rep.Flagged
	stat.LastKnown, stat.LastElsewhere, stat.LastLegacyRejected = rep.Known, rep.Elsewhere, rep.LegacyRejected
	stat.HeldForServer = rep.Held
}

// message macht aus einem Fehler einen deutschen Text für Status und Ausgabe: ohne Steuerzeichen,
// höchstens 200 Zeichen, und ohne den Geräteschlüssel, selbst wenn ein Fehler ihn enthielte.
func message(err error, key string) string {
	var (
		old   *client.OutdatedError
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
		s = UnauthorizedMessage(err)
	case errors.As(err, &old):
		s = OutdatedMessage(old.Min)
	case errors.As(err, &rej) && rej.Reason == client.ReasonSignKeyUnknown:
		s = MsgSignKeyTaken
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

// UnauthorizedMessage liefert die Meldung zu einem 401 beim Liefern, passend zum Grund des Servers.
func UnauthorizedMessage(err error) string {
	var ue *client.UnauthorizedError
	reason := ""
	if errors.As(err, &ue) {
		reason = ue.Reason
	}
	switch reason {
	case client.ReasonRepaired:
		return MsgRepaired
	case client.ReasonRevoked:
		return MsgRevoked
	case client.ReasonUnknown:
		return MsgUnknownKey
	}
	return msgUnauthorized
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

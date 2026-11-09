package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tokenlore/tokenlore/archive"
	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/device"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/secret"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

const (
	// clientTimeout ist die Grenze je HTTP-Anfrage; syncTimeout die für einen ganzen Abgleich.
	clientTimeout = 60 * time.Second
	syncTimeout   = 10 * time.Minute
	// infoTimeout begrenzt die Rückfrage beim Abgleich, ob der Server älter als das Plugin ist.
	infoTimeout = 15 * time.Second
)

var (
	codePattern = regexp.MustCompile(`^[0-9]{6}$`)
	// fpPiece ist ein Stück eines Fingerabdrucks, wie es ein Zeilenumbruch beim Kopieren abtrennt.
	fpPiece = regexp.MustCompile(`^[0-9A-Fa-f:]+$`)
)

const connectUsage = "Aufruf: /tokenlore:connect <url> <code> [--ca <fingerabdruck>] [--name <name>]"

// flagMessage übersetzt Fehler des flag-Pakets; Nutzereingaben darin werden bereinigt.
func flagMessage(err error) string {
	msg := err.Error()
	if name, ok := strings.CutPrefix(msg, "flag provided but not defined: "); ok {
		return "Unbekannte Option -" + client.Clean(name)
	}
	if name, ok := strings.CutPrefix(msg, "flag needs an argument: "); ok {
		return "Option -" + client.Clean(name) + " braucht einen Wert"
	}
	return "Ungültige Angabe: " + client.Clean(msg)
}

// joinFingerprint setzt einen Fingerabdruck wieder zusammen, den ein Zeilenumbruch beim Kopieren (z. B. aus PuTTY)
// in mehrere Argumente zerlegt hat. Das schwächt das Pinning nicht: Ob er stimmt, entscheidet allein der Vergleich
// mit der geholten CA. ok ist false, wenn ein übriges Argument kein Fingerabdruck-Stück ist.
func joinFingerprint(ca string, rest []string) (string, bool) {
	for _, p := range rest {
		if !fpPiece.MatchString(p) {
			return "", false
		}
		ca += p
	}
	return ca, true
}

// fingerprintProblem sagt, warum ein Fingerabdruck nicht passt (Leerzeichen sind schon entfernt).
func fingerprintProblem(fp string) string {
	h := strings.ReplaceAll(fp, ":", "")
	if !fpPiece.MatchString(fp) {
		return "CA-Fingerabdruck ungültig: nur 0–9, A–F und „:“ erlaubt."
	}
	return fmt.Sprintf("CA-Fingerabdruck ungültig: %d statt 64 Hex-Zeichen – bitte den ganzen Befehl von der Webseite (Geräte, Kopplungscode) kopieren.", len(h))
}

// parseInterspersed erlaubt Optionen auch nach den Argumenten ("connect <url> <code> --ca <fp>"),
// was flag allein nicht kann: es hört beim ersten Nicht-Flag auf.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// runConnect koppelt dieses Gerät. Mit --ca wird erst gespeichert, wenn die geholte CA zum Fingerabdruck des Nutzers
// passt und die Kopplung denselben Fingerabdruck bestätigt; ohne --ca gelten die System-Zertifikate und es wird
// keine CA gespeichert. Scheitert danach etwas, wird zurückgerollt.
func runConnect(ctx context.Context, args []string, out io.Writer, dataDir, configDir string) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // eigene, deutsche Meldungen statt der englischen des flag-Pakets
	caFlag := fs.String("ca", "", "CA-Fingerabdruck aus dem Befehl auf der Webseite unter „Geräte“")
	nameFlag := fs.String("name", "", "Gerätename (Standard: Rechnername)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		fmt.Fprintf(out, "%s.\n%s\n", flagMessage(err), connectUsage)
		return 2
	}
	caGiven := false
	fs.Visit(func(f *flag.Flag) { caGiven = caGiven || f.Name == "ca" })
	ca := strings.Join(strings.Fields(*caFlag), "")
	switch {
	case len(pos) == 0:
		fmt.Fprintf(out, "Es fehlen Server-Adresse und Kopplungscode.\n%s\n", connectUsage)
		return 2
	case len(pos) == 1:
		fmt.Fprintf(out, "Es fehlt der Kopplungscode (6 Ziffern).\n%s\n", connectUsage)
		return 2
	case len(pos) > 2:
		joined, ok := joinFingerprint(ca, pos[2:])
		if !caGiven || !ok {
			extra := client.Clean(strings.Join(pos[2:], " "))
			if r := []rune(extra); len(r) > 40 {
				extra = string(r[:40]) + "…"
			}
			fmt.Fprintf(out, "Unerwartete Angabe nach dem Kopplungscode: „%s“.\n%s\n", extra, connectUsage)
			return 2
		}
		ca = joined
	}
	base, err := client.ParseServerURL(pos[0])
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if !codePattern.MatchString(pos[1]) {
		fmt.Fprintln(out, "Der Kopplungscode besteht aus 6 Ziffern.")
		return 1
	}
	name := *nameFlag
	if name == "" {
		h, _ := os.Hostname()
		name = truncateRunes(h, protocol.MaxDeviceName)
	}
	if !protocol.ValidDeviceName(name) {
		// Den Namen nicht ausgeben: Er kann Steuer- oder Richtungszeichen enthalten.
		fmt.Fprintln(out, "Gerätename ungültig (1–64 Zeichen, keine Steuerzeichen) – bitte mit --name einen anderen angeben.")
		return 1
	}
	// Die gemerkte Kopie der bisherigen Kennung, bevor connect sie ersetzt: Ihre Systemkennung sagt, ob die bisherige
	// Verbindung auf diesem Rechner lag.
	var prevIdent *state.Identity
	if st, err := state.Load(dataDir); err == nil {
		prevIdent = st.Identity
	}
	ident, newForBroken, code, ok := resolveIdentity(out, dataDir, configDir)
	if !ok {
		return code
	}

	// Mit --ca wird die CA des Servers geholt und gegen den Fingerabdruck geprüft; ohne --ca (Server mit öffentlich
	// beglaubigtem Zertifikat) gibt es weder Abruf noch Pinning, der Client vertraut den System-Zertifikaten.
	var pinned string
	var caPEM []byte
	if caGiven {
		var err error
		if pinned, err = client.NormalizeFingerprint(ca); err != nil {
			fmt.Fprintln(out, fingerprintProblem(ca))
			return 1
		}
		var caCert *x509.Certificate
		if caPEM, caCert, err = client.FetchCA(ctx, base); err != nil {
			fmt.Fprintln(out, client.Clean(err.Error()))
			return 1
		}
		if client.Fingerprint(caCert.Raw) != pinned {
			fmt.Fprintln(out, "Fingerabdruck stimmt nicht – Verbindung abgebrochen.")
			return 1
		}
	}

	c, err := newClient(base, caPEM, clientTimeout)
	if err != nil {
		fmt.Fprintln(out, client.Clean(err.Error()))
		return 1
	}
	// Die Server-Kennung zuerst: Aus ihr entsteht device_source, und ein Umzug des Servers ändert sie nicht.
	info, err := c.Info(ctx)
	if err != nil {
		fmt.Fprintf(out, "%s – Kopplung abgebrochen.\n", infoProblem(err))
		return 1
	}

	// Den Signaturschlüssel erst laden oder erzeugen und lokal ablegen, dann koppeln: Ein öffentlicher Schlüssel, den der
	// Server kennt, gehört immer zu einem hier abgelegten privaten. Scheitert danach etwas, bleibt er stehen.
	sk, err := ensureSignKey(dataDir)
	if err != nil {
		fmt.Fprintf(out, "%s – Kopplung abgebrochen.\n", client.Clean(err.Error()))
		return 1
	}
	// Nur ein mit --name gewählter Name ersetzt beim Neukoppeln den auf dem Server (etwa auf der Webseite vergebenen).
	res, err := c.Pair(ctx, pos[1], name, runtime.GOOS, runtime.GOARCH, claudecode.DeviceSource(ident.ID, info.ServerID), info.ServerID, *nameFlag != "", sk.Keys)
	var outdated *client.OutdatedError
	if errors.As(err, &outdated) {
		fmt.Fprintf(out, "Kopplung abgelehnt: Diese tokenlore-Version ist dem Server zu alt. %s Der Kopplungscode bleibt gültig.\n", deliver.OutdatedMessage(outdated.Min))
		return 1
	}
	if errors.Is(err, client.ErrBadCode) {
		fmt.Fprintln(out, "Kopplungscode ungültig, abgelaufen oder schon benutzt.")
		return 1
	}
	if errors.Is(err, client.ErrSourceTaken) {
		fmt.Fprintln(out, "Kopplung abgelehnt: Dieser Rechner ist auf dem Server schon ein anderes Gerät. Der Code gilt für ein bestimmtes Gerät; nimm einen Code für ein neues Gerät oder für das Gerät dieses Rechners.")
		return 1
	}
	if err != nil {
		fmt.Fprintf(out, "Kopplung fehlgeschlagen: %s\n", client.Clean(err.Error()))
		return 1
	}
	if caGiven && res.CAFingerprint != pinned {
		fmt.Fprintln(out, "Server bestätigt einen anderen CA-Fingerabdruck – Verbindung abgebrochen.")
		return 1
	}

	old, hadOld, _ := state.LoadConnection(dataDir)
	conn := &state.Connection{
		ServerURL: base.String(), CAPEM: string(caPEM), CAFingerprint: pinned,
		DeviceID: res.DeviceID, DeviceName: name, ConnectedAt: time.Now().UTC(), Identity: ident.ID, ServerID: info.ServerID,
	}
	touched, err := storeConnection(dataDir, conn, res, ident)
	if err != nil {
		fmt.Fprintf(out, "Verbindung nicht gespeichert: %s\n", client.Clean(err.Error()))
		if touched && hadOld {
			fmt.Fprintln(out, "Die bisherige Verbindung ist ebenfalls entfernt. Bitte mit einem neuen Code neu koppeln.")
		}
		// Der Server kennt das eben gekoppelte Gerät schon; sein Schlüssel ist hier aber verworfen.
		fmt.Fprintf(out, "Das eben gekoppelte Gerät „%s“ (Nummer %d) bitte auf dem Server sperren.\n", name, res.DeviceID)
		return 1
	}
	// Hat der Server dasselbe Gerät neu gekoppelt, gibt es kein altes Gerät, das stehen bliebe.
	sameDevice := hadOld && res.Repaired && sameServerAs(old, conn) && old.DeviceID == res.DeviceID
	otherIdentity := hadOld && old.Identity != "" && old.Identity != ident.ID
	switch {
	case sameDevice:
		// Auch bei anderer Kennung: dasselbe Gerät, nichts bleibt stehen.
	case otherIdentity && (newForBroken || identityWasHere(prevIdent, old.Identity, machineHash())):
		// Die bisherige Kennung lag auf diesem Rechner (kaputte Kennungsdatei ersetzt, anderer Claude-Ordner mit
		// demselben Datenordner): Ihr Gerät liefert von hier nicht mehr.
		fmt.Fprintln(out, "Die bisherige Verbindung gehörte zu einer früheren Kennung dieses Rechners und ist ersetzt. Ihr Gerät liefert von hier nicht mehr; du kannst es auf dem Server sperren.")
	case otherIdentity:
		// Die bisherige Verbindung gehört zu einer anderen Kennung, meist zum Rechner, von dem der Ordner kopiert
		// wurde. Dessen Gerät liefert dort womöglich weiter; also nicht zum Sperren auffordern. Das gilt auch, wenn
		// ein früherer Versuch die Kennung schon abgezweigt hat.
		fmt.Fprintln(out, "Die bisherige Verbindung gehörte zu einer anderen Kennung, etwa dem Rechner, von dem kopiert wurde, und ist hier ersetzt. Ihr Gerät bleibt auf dem Server; sperre es nur, wenn es nicht mehr liefert.")
	case hadOld:
		fmt.Fprintln(out, "Die bisherige Verbindung wurde ersetzt. Das alte Gerät bleibt auf dem Server, bis du es dort sperrst.")
	}

	// Erst die Verbindung melden, dann abgleichen: Der erste Abgleich kann dauern.
	fmt.Fprintf(out, "Verbunden mit %s als „%s“ (Gerät %d).\n", conn.ServerURL, name, res.DeviceID)
	if res.Repaired {
		fmt.Fprintln(out, "Bestehendes Gerät neu gekoppelt: Seine Daten bleiben, ältere Schlüssel dieses Geräts sind gesperrt.")
	}
	// Ein früherer Schlüssel, den der Server einem anderen Gerät zuordnet: Was mit den älteren Zeilen geschieht, sagt
	// genau eine Zeile, der Hinweis mit ihrer Zahl nach dem ersten Abgleich (oder, scheitert er, ohne Zahl).
	refusedPrevious := false
	for _, k := range res.SignKeysRefused {
		if k != sk.Keys.Public {
			refusedPrevious = true
		}
	}
	if sk.Replaced {
		if refusedPrevious {
			fmt.Fprintln(out, "Der bisherige Signaturschlüssel fehlte; ein neuer Signaturschlüssel ist erzeugt.")
		} else {
			fmt.Fprintln(out, "Der bisherige Signaturschlüssel fehlte; ein neuer Signaturschlüssel ist erzeugt. Ältere Zeilen prüft der Server weiter mit dem früheren.")
		}
	}
	// Die Schlüssel selbst nicht nennen: Für den Nutzer zählt nur, welcher es ist.
	if slices.Contains(res.SignKeysRefused, sk.Keys.Public) {
		fmt.Fprintln(out, "Warnung: Der Signaturschlüssel dieses Rechners ist auf dem Server nicht übernommen: Er gehört dort einem anderen Gerät. Zeilen damit nimmt der Server von diesem Gerät nicht an.")
	}
	if !caGiven && res.CAFingerprint != "" {
		// Ohne --ca prüft nichts, ob der Server der richtige ist, außer den System-Zertifikaten. Den Fingerabdruck
		// nicht nennen: Er käme vom noch ungeprüften Server.
		fmt.Fprintln(out, "Hinweis: Für diesen Server ist ein Zertifikat hinterlegt; mit --ca <fingerabdruck> lässt sich die Verbindung zusätzlich daran binden.")
	}
	runCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	rep, err := deliver.Run(runCtx, deliver.Deps{DataDir: dataDir, ConfigDir: configDir, Version: version, Key: res.DeviceKey, Sender: c, Identity: ident.ID, Machine: machineHash(), SignKey: sk.Priv})
	if err != nil {
		// Die Verbindung bleibt: Der nächste Sessionstart versucht es erneut.
		fmt.Fprintf(out, "Erster Abgleich fehlgeschlagen: %s\n", err)
		if refusedPrevious {
			fmt.Fprintln(out, "Hinweis: "+msgRefusedPrevious)
		}
		return 0
	}
	printSyncResult(out, rep, "Erster Abgleich: ", "Erster Abgleich: ")
	return 0
}

// msgRefusedPrevious zeigt connect, wenn der Server einen früheren Signaturschlüssel einem anderen Gerät zuordnet und
// der erste Abgleich die betroffenen Zeilen nicht zählen konnte.
const msgRefusedPrevious = "Ein früherer Signaturschlüssel dieses Rechners gehört auf dem Server einem anderen Gerät. Ältere Zeilen damit gehen nicht an den Server; neue liefert dieses Gerät weiter. Ist das andere Gerät derselbe Rechner, lege beide auf der Webseite unter „Geräte“ zusammen und kopple danach dieses Gerät neu; dann gehen sie mit."

// identityWasHere meldet, ob die Kennung prev (die gemerkte Kopie) die Kennung id ist und auf diesem Rechner lag.
// Ohne Kopie oder ohne lesbare Systemkennung ist das unbekannt, also false.
func identityWasHere(prev *state.Identity, id, machine string) bool {
	return prev != nil && prev.ID == id && machine != "" && prev.Machine == machine
}

// resolveIdentity liefert die Kennung dieses Konfigurationsordners (Datei tokenlore-id, notfalls aus der Kopie im
// Merkzettel oder neu). Stammt die Datei von einem anderen Rechner, wird dieser Rechner ein eigenes Gerät: Er
// bekommt eine neue Kennung, die bisherige wird Vorgänger. newForBroken: Die Datei war unlesbar und ohne Kopie, die
// Kennung ist neu. Ist ok false, ist code der Exit-Code.
func resolveIdentity(out io.Writer, dataDir, configDir string) (id state.Identity, newForBroken bool, code int, ok bool) {
	st, err := state.Load(dataDir)
	if err != nil {
		fmt.Fprintf(out, "Merkzettel nicht lesbar: %s\n", client.Clean(err.Error()))
		return state.Identity{}, false, 1, false
	}
	machine := machineHash()
	id, origin, err := claudecode.EnsureIdentity(configDir, st.Identity, machine)
	if err != nil {
		fmt.Fprintln(out, client.Clean(err.Error()))
		return state.Identity{}, false, 1, false
	}
	switch origin {
	case claudecode.IdentityFromCopy:
		fmt.Fprintf(out, "Kennungsdatei %s fehlte oder war unlesbar und ist aus dem Merkzettel wiederhergestellt.\n", claudecode.IdentityFile)
	case claudecode.IdentityNewForBroken:
		how := "dieser Ordner bekommt eine neue Kennung"
		if id.ID == claudecode.DeriveIdentityID(machine, configDir) {
			how = "die Kennung ist aus der Systemkennung dieses Rechners bestimmt"
		}
		fmt.Fprintf(out, "Kennungsdatei %s war unlesbar und liegt jetzt als %s.kaputt daneben; %s.\n",
			claudecode.IdentityFile, claudecode.IdentityFile, how)
	}
	switch {
	case claudecode.MachineConflict(id, machine):
		fmt.Fprintf(out, "Die Kennungsdatei %s stammt von einem anderen Rechner; dieser Rechner wird ein eigenes Gerät.\n", claudecode.IdentityFile)
		id, err = claudecode.ForkIdentity(configDir, id, machine)
	case machine != "" && id.Machine == "":
		// Die Systemkennung war beim Anlegen unbekannt: ab jetzt ist die Datei an diesen Rechner gebunden.
		id, err = claudecode.BindIdentity(configDir, id, machine)
	}
	if err == nil {
		// Felder früherer Fassungen (last_day, branch_day) fallen weg; Kennung und Systemkennung bleiben.
		err = claudecode.TidyIdentity(configDir)
	}
	if err != nil {
		fmt.Fprintf(out, "Kennungsdatei nicht geschrieben: %s\n", client.Clean(err.Error()))
		return state.Identity{}, false, 1, false
	}
	return id, origin == claudecode.IdentityNewForBroken, 0, true
}

// machineHash liefert den Hash der Systemkennung; Tests ersetzen es, um einen anderen Rechner nachzustellen.
var machineHash = device.MachineHash

// newClient baut den Client für connect und sync; Tests ersetzen es, weil die System-Zertifikate ihre Test-CA nicht kennen.
var newClient = client.New

// Für Tests austauschbar, um Fehler beim Speichern und einen dauerhaft fehlenden Schlüsselspeicher nachzustellen.
var (
	saveConnection = func(c *state.Connection, dir string) error { return c.Save(dir) }
	removeSecret   = secret.Remove
)

// storeConnection legt Merkzettel, Schlüssel und Verbindung ab. Die Reihenfolge schützt vor Abstürzen
// mittendrin: Zuerst wird die bisherige Verbindung entfernt, erst ganz zum Schluss die neue geschrieben. Bricht
// dazwischen etwas ab, verweigert der Abgleich ("Nicht gekoppelt"), statt mit altem Merker „geliefert“ oder einem
// ersetzten Schlüssel zu liefern. Scheitert ein Schritt, werden Schlüssel und Verbindung entfernt.
// touched meldet, ob schon etwas verändert war (dann ist auch eine bisherige Verbindung unbrauchbar).
func storeConnection(dataDir string, conn *state.Connection, res client.PairResult, ident state.Identity) (touched bool, err error) {
	st, err := state.Load(dataDir)
	if err != nil {
		return false, fmt.Errorf("Merkzettel nicht lesbar: %w", err)
	}
	rollback := func(err error) (bool, error) {
		_ = removeSecret(dataDir, secret.KindFile) // räumt beide Ablagen
		_ = state.RemoveConnection(dataDir)
		return true, err
	}
	if err := state.RemoveConnection(dataDir); err != nil {
		return false, err // nichts verändert: die bisherige Verbindung ist noch da
	}
	// Neue Kopplung: Der Merker „geliefert“ dieses Rechners fällt weg, damit der Server das ganze Archiv einmal
	// bekommt; er zählt jede Anfrage nur einmal. Die Lesestände bleiben: Was gelesen ist, steht schon im Archiv.
	if err := archive.ResetDelivered(state.ArchiveDir(dataDir, ident.ID)); err != nil {
		return rollback(fmt.Errorf("%s nicht zurückgesetzt: %w", archive.DeliveredFile, err))
	}
	st.Identity = &ident
	// Frühere Schlüssel, die der Server einem anderen Gerät zuordnet: Ihre Archivzeilen gehen nicht mehr mit. Der
	// aktuelle zählt nie dazu; lehnt der Server ihn ab, meldet der Abgleich das Neukoppeln (deliver.MsgSignKeyTaken).
	st.SignKeysRefused = nil
	for _, k := range res.SignKeysRefused {
		if k != st.SignPublicKey && slices.Contains(st.SignPublicKeysPrevious, k) && !slices.Contains(st.SignKeysRefused, k) {
			st.SignKeysRefused = append(st.SignKeysRefused, k)
		}
	}
	if err := st.Save(dataDir); err != nil {
		return rollback(err) // atomar: der alte Merkzettel ist unverändert, die alte Verbindung aber weg
	}
	// secret.Save schreibt in dieselbe Ablage wie zuvor und ersetzt damit den alten Schlüssel; landet der neue in
	// der Datei, versucht es zusätzlich, einen alten Eintrag im Schlüsselspeicher zu löschen.
	kind, sum, err := secret.Save(dataDir, res.DeviceKey)
	if err != nil {
		return rollback(err)
	}
	conn.KeyStore, conn.KeySum = kind, sum
	if err := saveConnection(conn, dataDir); err != nil {
		return rollback(err)
	}
	return true, nil
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// requestsInParts formuliert "1 Anfrage in 1 Paket" bzw. "2 Anfragen in 3 Paketen".
func requestsInParts(requests, parts int) string {
	return fmt.Sprintf("%d %s in %d %s", requests, plural(requests, "Anfrage", "Anfragen"), parts, plural(parts, "Paket", "Paketen"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// runDisconnect entfernt Schlüssel und Verbindung; der Merkzettel bleibt für eine spätere Kopplung.
func runDisconnect(out io.Writer, dataDir string) int {
	conn, ok, err := state.LoadConnection(dataDir)
	if err != nil {
		fmt.Fprintf(out, "Verbindung nicht lesbar: %v\n", err)
		return 1
	}
	if !ok {
		// Ein Absturz während connect kann einen Schlüssel ohne Verbindung hinterlassen haben.
		_ = removeSecret(dataDir, secret.KindFile) // räumt beide Ablagen
		fmt.Fprintln(out, "Nicht verbunden.")
		return 0
	}
	kind := conn.KeyStore
	if kind != secret.KindKeyring && kind != secret.KindFile {
		kind = secret.KindFile // Remove räumt ohnehin beide Ablagen
	}
	keyErr := removeSecret(dataDir, kind)
	// Auch wenn der Schlüssel nicht zu löschen ist (Schlüsselspeicher dauerhaft weg): Die Verbindung wird
	// trotzdem getrennt, sonst käme der Nutzer nie mehr davon los.
	if err := state.RemoveConnection(dataDir); err != nil {
		fmt.Fprintf(out, "Verbindung nicht gelöscht: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Verbindung zu %s getrennt. Sperre das Gerät „%s“ (Nummer %d) auch auf dem Server, sonst bleibt sein Schlüssel dort gültig.\n",
		client.Clean(conn.ServerURL), client.Clean(conn.DeviceName), conn.DeviceID)
	if keyErr != nil {
		fmt.Fprintf(out, "Achtung: Der Geräteschlüssel wurde nicht gelöscht (%s). Er liegt noch im Schlüsselspeicher – sperre das Gerät unbedingt auf dem Server.\n",
			client.Clean(keyErr.Error()))
		return 1
	}
	return 0
}

// sameServerAs meldet, ob zwei Verbindungen zum selben Server gehören: über server_id, die bei einem Umzug gleich
// bleibt; fehlt sie bei einer (Verbindung aus einer älteren Version), über die Adresse.
func sameServerAs(a, b *state.Connection) bool {
	if a.ServerID != "" && b.ServerID != "" {
		return a.ServerID == b.ServerID
	}
	return sameServer(a.ServerURL, b.ServerURL)
}

// infoProblem beschreibt, warum GET /api/plugin/info nicht klappte. Zertifikatsfehler bekommen den festen Text.
func infoProblem(err error) string {
	var (
		rej   *client.RejectedError
		retry *client.RetryError
		old   *client.OldDatabaseError
	)
	switch {
	case client.IsCertError(err):
		return client.CertMessage(err)
	case errors.As(err, &old):
		return old.Error() // Meldung des Servers, schon bereinigt
	case errors.As(err, &retry):
		return "Server ausgelastet, bitte gleich noch einmal versuchen"
	case errors.As(err, &rej) && rej.Status == 404:
		return "Der Server nennt keine Server-Kennung (Status 404). Meist ist er älter als dieses Plugin; dann muss sein Betreiber ihn aktualisieren. Sonst prüfe die Adresse"
	}
	return "Server-Kennung nicht abrufbar: " + client.Clean(err.Error())
}

// sameServer vergleicht zwei gespeicherte Server-Adressen in fester Schreibweise (Host klein, ohne :443, ohne "/"
// am Ende).
func sameServer(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return claudecode.NormalizeServerAddress(ua) == claudecode.NormalizeServerAddress(ub)
}

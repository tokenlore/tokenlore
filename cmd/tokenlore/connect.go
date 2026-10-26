package main

import (
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/secret"
	"github.com/tokenlore/tokenlore/state"
)

const (
	// clientTimeout ist die Grenze je HTTP-Anfrage; syncTimeout die für einen ganzen Abgleich.
	clientTimeout = 60 * time.Second
	syncTimeout   = 10 * time.Minute
)

var (
	codePattern = regexp.MustCompile(`^[0-9]{6}$`)
	// fpPiece ist ein Stück eines Fingerabdrucks, wie es ein Zeilenumbruch beim Kopieren abtrennt.
	fpPiece = regexp.MustCompile(`^[0-9A-Fa-f:]+$`)
)

const connectUsage = "Aufruf: /tokenlore:connect <url> <code> [--ca <fingerabdruck>] [--name <name>] [--resend-legacy]"

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
	resendFlag := fs.Bool("resend-legacy", false, "Altbestand erneut senden (neuer oder geleerter Server)")
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
	res, err := c.Pair(ctx, pos[1], name, runtime.GOOS, runtime.GOARCH)
	if errors.Is(err, client.ErrBadCode) {
		fmt.Fprintln(out, "Kopplungscode ungültig, abgelaufen oder schon benutzt.")
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

	_, hadOld, _ := state.LoadConnection(dataDir)
	conn := &state.Connection{
		ServerURL: base.String(), CAPEM: string(caPEM), CAFingerprint: pinned,
		DeviceID: res.DeviceID, DeviceName: name, ConnectedAt: time.Now().UTC(),
	}
	legacyDone, touched, err := storeConnection(dataDir, conn, res, *resendFlag)
	if err != nil {
		fmt.Fprintf(out, "Verbindung nicht gespeichert: %s\n", client.Clean(err.Error()))
		if touched && hadOld {
			fmt.Fprintln(out, "Die bisherige Verbindung ist ebenfalls entfernt. Bitte mit einem neuen Code neu koppeln.")
		}
		// Der Server kennt das eben gekoppelte Gerät schon; sein Schlüssel ist hier aber verworfen.
		fmt.Fprintf(out, "Das eben gekoppelte Gerät „%s“ (Nummer %d) bitte auf dem Server sperren.\n", name, res.DeviceID)
		return 1
	}
	if hadOld {
		fmt.Fprintln(out, "Die bisherige Verbindung wurde ersetzt. Das alte Gerät bleibt auf dem Server, bis du es dort sperrst.")
	}

	// Erst die Verbindung melden, dann abgleichen: Der erste Abgleich kann dauern.
	fmt.Fprintf(out, "Verbunden mit %s als „%s“ (Gerät %d).\n", conn.ServerURL, name, res.DeviceID)
	if !caGiven && res.CAFingerprint != "" {
		// Ohne --ca prüft nichts, ob der Server der richtige ist, außer den System-Zertifikaten. Den Fingerabdruck
		// nicht nennen: Er käme vom noch ungeprüften Server.
		fmt.Fprintln(out, "Hinweis: Für diesen Server ist ein Zertifikat hinterlegt; mit --ca <fingerabdruck> lässt sich die Verbindung zusätzlich daran binden.")
	}
	if legacyDone {
		fmt.Fprintln(out, "Altbestand wurde schon an einen Server geliefert und wird nicht erneut gesendet (neuer oder geleerter Server: --resend-legacy).")
	}
	runCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	rep, err := deliver.Run(runCtx, deliver.Deps{DataDir: dataDir, ConfigDir: configDir, Version: version, Key: res.DeviceKey, Sender: c})
	if err != nil {
		// Die Verbindung bleibt: Der nächste Sessionstart versucht es erneut.
		fmt.Fprintf(out, "Erster Abgleich fehlgeschlagen: %s\n", err)
		return 0
	}
	fmt.Fprintf(out, "Erster Abgleich: %s.\n", requestsInParts(rep.Requests, rep.Parts))
	return 0
}

// newClient baut den Client für connect und sync; Tests ersetzen es, weil die System-Zertifikate ihre Test-CA nicht kennen.
var newClient = client.New

// Für Tests austauschbar, um Fehler beim Speichern und einen dauerhaft fehlenden Schlüsselspeicher nachzustellen.
var (
	saveConnection = func(c *state.Connection, dir string) error { return c.Save(dir) }
	removeSecret   = secret.Remove
)

// storeConnection legt Merkzettel, Schlüssel und Verbindung ab. Die Reihenfolge schützt vor Abstürzen
// mittendrin: Zuerst wird der Merkzettel ohne Salz gespeichert, erst ganz zum Schluss mit dem neuen. Bricht
// dazwischen etwas ab, verweigert der Abgleich ("Nicht gekoppelt"), statt mit altem Salz und alten Leseständen
// für die neue Verbindung zu liefern. Scheitert ein Schritt, werden Schlüssel und Verbindung entfernt.
// touched meldet, ob schon etwas verändert war (dann ist auch eine bisherige Verbindung unbrauchbar).
// legacyDone meldet, dass der Altbestand bei diesem Abgleich nicht erneut gesendet wird.
func storeConnection(dataDir string, conn *state.Connection, res client.PairResult, resendLegacy bool) (legacyDone, touched bool, err error) {
	st, err := state.Load(dataDir)
	if err != nil {
		return false, false, fmt.Errorf("Merkzettel nicht lesbar: %w", err)
	}
	// Neues Gerät, neues Salz: alle Lesestände verwerfen, damit der Server alles einmal bekommt.
	// Der Altbestand geht dagegen nur einmal raus: Der Server speichert ihn je Gerät, unter neuer Geräte-ID
	// zählte er doppelt. Angenommen ist er, sobald ein Paket mit ihm durchging (LegacySent), auch wenn danach
	// ein Paket scheiterte. Ein neuer oder geleerter Server bekommt ihn nur mit --resend-legacy erneut.
	st.Salt = ""
	st.Files = map[string]state.FileMark{}
	switch {
	case resendLegacy:
		st.LegacyDone, st.LegacySent = false, false
	case st.LegacySent:
		st.LegacyDone = true
	case !st.Delivered:
		st.LegacyDone = false
	}
	st.AccountsSum = ""
	if err := st.Save(dataDir); err != nil {
		return false, false, err // atomar: der alte Merkzettel ist unverändert
	}
	rollback := func(err error) (bool, bool, error) {
		_ = removeSecret(dataDir, secret.KindFile) // räumt beide Ablagen
		_ = state.RemoveConnection(dataDir)
		return false, true, err
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
	st.Salt = res.ProjectSalt
	if err := st.Save(dataDir); err != nil {
		return rollback(err)
	}
	return st.LegacyDone, true, nil
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

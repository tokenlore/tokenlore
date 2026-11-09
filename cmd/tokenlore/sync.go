package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/secret"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// lockDevice nimmt die gerätweite Sperre; Tests ersetzen sie, damit sie das echte Cache-Verzeichnis nicht anfassen.
var lockDevice = deviceLock

// backgroundCommand ist das Programm, das session-start und session-end im Hintergrund starten.
// Tests ersetzen es durch einen Hilfsprozess des Testprogramms.
var backgroundCommand = func() (exe string, args []string) {
	exe, err := os.Executable()
	if err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
	}
	return exe, []string{"sync", "--background"}
}

// runSync gleicht einmal mit dem Server ab. Mit --background schreibt es nichts nach stdout/stderr:
// Es läuft abgekoppelt vom Hook, das Ergebnis steht in status.json. --ended nennt die Protokolldatei einer eben
// beendeten Sitzung, deren letzte Anfragen sofort fertig sind.
func runSync(args []string, stdout, stderr io.Writer, dataDir, configDir string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bg := fs.Bool("background", false, "still im Hintergrund abgleichen")
	endedPath := fs.String("ended", "", "Protokolldatei einer beendeten Sitzung (Hook SessionEnd)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "Aufruf: /tokenlore:sync")
		return 2
	}
	if *bg {
		stdout, stderr = io.Discard, io.Discard
	}

	// Erst die Sperre, dann die Verbindung lesen: Ein laufendes connect hält die Sperre und schreibt sie gerade neu.
	release, ok, err := lockDevice()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintln(stdout, "Ein anderer Abgleich läuft gerade.")
		return 0
	}
	defer release()

	// early hält Fehler vor dem eigentlichen Abgleich in status.json fest: Im Hintergrund sieht sie sonst niemand.
	early := func(msg string) int {
		msg = client.Clean(msg)
		fmt.Fprintln(stderr, msg)
		stat, err := state.LoadStatus(dataDir)
		if err != nil {
			stat = &state.Status{}
		}
		stat.LastAttempt, stat.LastError, stat.LastErrorOldDatabase = time.Now(), msg, false
		_ = stat.Save(dataDir) // Status ist nur Anzeige
		return 1
	}

	conn, ok, err := state.LoadConnection(dataDir)
	if err != nil {
		return early(fmt.Sprintf("Verbindung nicht lesbar: %v", err))
	}
	if !ok {
		if *bg {
			return 0
		}
		fmt.Fprintln(stderr, "Nicht verbunden. Erst /tokenlore:connect ausführen.")
		return 1
	}

	// Der Fehlertext von secret.Load enthält den Schlüssel nie (nur Ablage, Zeitgrenze oder Prüfsumme).
	key, err := secret.Load(dataDir, conn.KeyStore, conn.KeySum)
	if err != nil {
		code := early(fmt.Sprintf("Geräteschlüssel nicht lesbar: %v, bitte neu koppeln.", err))
		fmt.Fprintln(stderr, deliver.RepairSteps(client.Clean(conn.ServerURL)))
		return code
	}
	// Ohne passenden Signaturschlüssel geht nichts raus; einen neuen erzeugt erst das Koppeln.
	st, err := state.Load(dataDir)
	if err != nil {
		return early(fmt.Sprintf("Merkzettel nicht lesbar: %v", err))
	}
	signKey, err := loadSignKey(dataDir, st)
	if err != nil {
		// Ist der Server älter als das Plugin, hilft Neukoppeln nicht: erst muss er aktualisiert werden.
		if errors.Is(err, errSignKeyMissing) && serverTooOld(conn) {
			return early(deliver.MsgServerTooOld)
		}
		code := early(err.Error())
		if errors.Is(err, errSignKeyMissing) {
			fmt.Fprintln(stderr, deliver.RepairSteps(client.Clean(conn.ServerURL)))
		}
		return code
	}
	base, err := client.ParseServerURL(conn.ServerURL)
	if err != nil {
		return early(fmt.Sprintf("Gespeicherte Verbindung ungültig: %v", err))
	}
	c, err := connectionClient(base, conn)
	if err != nil {
		return early(err.Error())
	}
	// Ein Protokollname außerhalb von projects/ gilt nicht; die Anfrage kommt dann nach den übrigen Regeln.
	var ended []string
	if rel, ok := claudecode.EndedRel(configDir, *endedPath); ok {
		ended = []string{rel}
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
	defer cancel()
	rep, err := deliver.Run(ctx, deliver.Deps{DataDir: dataDir, ConfigDir: configDir, Version: version, Key: key, Sender: c, Identity: conn.Identity, Machine: machineHash(), SignKey: signKey, Ended: ended})
	if err != nil {
		fmt.Fprintln(stderr, err)
		// Abgelehnter Schlüssel, fremder Rechner oder übernommener Signaturschlüssel: Die Schritte zum Neukoppeln gleich dazu.
		if stat, serr := state.LoadStatus(dataDir); serr == nil && stat.LastError != "" &&
			(stat.LastError == stat.KeyRejected || stat.LastError == deliver.MsgOtherDevice || stat.LastError == deliver.MsgSignKeyTaken) {
			fmt.Fprintln(stderr, deliver.RepairSteps(client.Clean(conn.ServerURL)))
		}
		return 1
	}
	printSyncResult(stdout, rep, "", "Abgleich fertig: ")
	return 0
}

// printSyncResult schreibt das Ergebnis eines Abgleichs wie sync: „Nichts Neues.“ oder die Zahl der Anfragen, dazu die
// Hinweise. Ein Paket nur mit der Kontenliste zählt als nichts Neues. lead steht vor „Nichts Neues.“, doneLead vor der
// Zahl; connect nutzt dieselbe Ausgabe für den ersten Abgleich.
func printSyncResult(w io.Writer, rep deliver.Report, lead, doneLead string) {
	if rep.NothingNew || (rep.Requests == 0 && rep.Days == 0) {
		fmt.Fprint(w, lead+"Nichts Neues.")
		if rep.Damaged > 0 {
			fmt.Fprintf(w, " %d beschädigte Archiv%s ausgesondert.", rep.Damaged, plural(rep.Damaged, "zeile", "zeilen"))
		}
		fmt.Fprintln(w)
		if rep.Foreign > 0 {
			fmt.Fprintf(w, "Hinweis: %s\n", deliver.ForeignMessage(rep.Foreign))
		}
		return
	}
	fmt.Fprintf(w, "%s%s.", doneLead, requestsInParts(rep.Requests, rep.Parts))
	if rep.Days > 0 {
		fmt.Fprintf(w, " %d %s aus der Vorgeschichte.", rep.Days, plural(rep.Days, "Tag", "Tage"))
	}
	if rep.Damaged > 0 {
		fmt.Fprintf(w, " %d beschädigte Archiv%s ausgesondert.", rep.Damaged, plural(rep.Damaged, "zeile", "zeilen"))
	}
	if rep.Warnings > 0 {
		fmt.Fprintf(w, " %d %s (Texte: /tokenlore:status --warnings).", rep.Warnings, plural(rep.Warnings, "Warnung", "Warnungen"))
	}
	fmt.Fprintln(w)
	if rep.Foreign > 0 {
		fmt.Fprintf(w, "Hinweis: %s\n", deliver.ForeignMessage(rep.Foreign))
	}
	for _, n := range deliver.Notices(&state.Status{LastKnown: rep.Known, LastElsewhere: rep.Elsewhere, LastLegacyRejected: rep.LegacyRejected}) {
		fmt.Fprintf(w, "Hinweis: %s\n", n)
	}
}

// connectionClient baut den Client für die gespeicherte Verbindung und senkt die Vertrauensstufe nie still:
// Nur wenn CA und Fingerabdruck beide fehlen (Kopplung ohne --ca), gelten die System-Zertifikate. Ist einer von
// beiden gesetzt, müssen beide da sein und zusammenpassen; connect speichert sie immer gemeinsam. Sonst Abbruch.
func connectionClient(base *url.URL, conn *state.Connection) (*client.Client, error) {
	if conn.CAPEM == "" && conn.CAFingerprint == "" {
		return newClient(base, nil, clientTimeout)
	}
	if conn.CAPEM == "" || conn.CAFingerprint == "" {
		return nil, client.ErrStoredCA
	}
	if err := client.CheckStoredCA([]byte(conn.CAPEM), conn.CAFingerprint); err != nil {
		return nil, err
	}
	return newClient(base, []byte(conn.CAPEM), clientTimeout)
}

// serverTooOld meldet, ob der gekoppelte Server GET /api/plugin/info nicht kennt (404), also älter als dieses Plugin
// ist. Jeder andere Ausgang, auch ein Netzfehler, zählt nicht als zu alt.
func serverTooOld(conn *state.Connection) bool {
	base, err := client.ParseServerURL(conn.ServerURL)
	if err != nil {
		return false
	}
	c, err := connectionClient(base, conn)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), infoTimeout)
	defer cancel()
	_, err = c.Info(ctx)
	var rej *client.RejectedError
	return errors.As(err, &rej) && rej.Status == http.StatusNotFound
}

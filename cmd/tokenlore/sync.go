package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/secret"
	"github.com/tokenlore/tokenlore/state"
)

// lockDevice nimmt die gerätweite Sperre; Tests ersetzen sie, damit sie das echte Cache-Verzeichnis nicht anfassen.
var lockDevice = deviceLock

// backgroundCommand ist das Programm, das session-start im Hintergrund startet.
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
// Es läuft abgekoppelt vom Sessionstart, das Ergebnis steht in status.json.
func runSync(args []string, stdout, stderr io.Writer, dataDir, configDir string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bg := fs.Bool("background", false, "still im Hintergrund abgleichen")
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
		stat.LastAttempt, stat.LastError = time.Now(), msg
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
		return early(fmt.Sprintf("Geräteschlüssel nicht lesbar: %v – neu koppeln mit /tokenlore:connect", err))
	}
	base, err := client.ParseServerURL(conn.ServerURL)
	if err != nil {
		return early(fmt.Sprintf("Gespeicherte Verbindung ungültig: %v", err))
	}
	c, err := connectionClient(base, conn)
	if err != nil {
		return early(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
	defer cancel()
	rep, err := deliver.Run(ctx, deliver.Deps{DataDir: dataDir, ConfigDir: configDir, Version: version, Key: key, Sender: c})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if rep.NothingNew {
		fmt.Fprintln(stdout, "Nichts Neues.")
		return 0
	}
	fmt.Fprintf(stdout, "Abgleich fertig: %s.", requestsInParts(rep.Requests, rep.Parts))
	if rep.Warnings > 0 {
		fmt.Fprintf(stdout, " %d %s (Texte: /tokenlore:status --warnings).", rep.Warnings, plural(rep.Warnings, "Warnung", "Warnungen"))
	}
	fmt.Fprintln(stdout)
	return 0
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

// Befehl tokenlore: liest Claude-Code-Nutzungszahlen und liefert sie an den tokenlore-Server.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/lock"
	"github.com/tokenlore/tokenlore/source/claudecode"
	"github.com/tokenlore/tokenlore/state"
)

// version wird beim Bauen per -ldflags "-X main.version=…" gesetzt.
var version = "dev"

const usage = `Aufruf: tokenlore <befehl>

Befehle:
  connect <url> <code> [--ca <fingerabdruck>] [--name <name>] [--resend-legacy]
                  koppelt dieses Gerät mit dem Server
  disconnect      trennt die Verbindung (Gerät danach auf dem Server sperren)
  sync            gleicht jetzt mit dem Server ab
  status [--next] [--warnings]
                  zeigt Verbindung, letzten Abgleich und offene Daten
  session-start   beim Sessionstart aufrufen (Hook), merkt sich das Konto und gleicht im Hintergrund ab
  scan            liest neue Nutzungszahlen ein
  email           zeigt oder setzt, ob die E-Mail-Adresse mitgesendet wird
  version         zeigt die Version
`

func main() {
	client.Version = version
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "connect", "disconnect", "sync", "status", "email":
		rest, err := pluginArgs(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stdout, err)
			return 2
		}
		args = append([]string{args[0]}, rest...)
	}
	switch args[0] {
	case "version":
		fmt.Printf("tokenlore %s\n", version)
		return 0
	case "session-start":
		// Notbremse: was immer hängt (Festplatte, Virenscanner), nach 2 s ist Schluss.
		time.AfterFunc(2*time.Second, func() { os.Exit(0) })
		dataDir, cfg, err := dirs()
		if err != nil {
			return 0 // der Sessionstart darf nie gestört werden
		}
		_ = runSessionStart(os.Stdin, dataDir, claudecode.ClaudeJSONPath(cfg))
		// Der Abgleich läuft abgekoppelt: Claude Code wartet nie auf das Netz.
		startBackgroundSync(dataDir)
		return 0
	case "connect", "disconnect":
		dataDir, cfg, err := dirs()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return withLock(os.Stderr, func() int {
			if args[0] == "disconnect" {
				return runDisconnect(os.Stdout, dataDir)
			}
			return runConnect(context.Background(), args[1:], os.Stdout, dataDir, cfg)
		})
	case "sync":
		dataDir, cfg, err := dirs()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return runSync(args[1:], os.Stdout, os.Stderr, dataDir, cfg)
	case "status":
		dataDir, cfg, err := dirs()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return runStatus(args[1:], os.Stdout, dataDir, cfg)
	case "email":
		dataDir, cfg, err := dirs()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		release, ok, err := deviceLock()
		if err != nil || !ok {
			fmt.Fprintln(os.Stderr, "Gerade läuft ein Abgleich. Bitte gleich noch einmal versuchen.")
			return 1
		}
		defer release()
		return runEmail(args[1:], os.Stdout, dataDir, claudecode.ClaudeJSONPath(cfg))
	case "scan":
		dataDir, cfg, err := dirs()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		release, ok, err := deviceLock()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if !ok {
			fmt.Println("Ein anderer Abgleich läuft gerade.")
			return 0
		}
		defer release()
		return runScan(args[1:], os.Stdout, dataDir, cfg)
	default:
		fmt.Fprintf(os.Stderr, "Unbekannter Befehl %q.\n\n%s", args[0], usage)
		return 2
	}
}

// dirs liefert das Datenverzeichnis von tokenlore und den Claude-Konfigurationsordner.
func dirs() (dataDir, configDir string, err error) {
	if dataDir, err = state.DataDir(); err != nil {
		return "", "", err
	}
	if configDir, err = claudecode.ConfigDir(); err != nil {
		return "", "", err
	}
	return dataDir, configDir, nil
}

// deviceLock nimmt die gerätweite Sperre, damit Abgleich und Einstellungen sich nicht überschreiben.
func deviceLock() (func(), bool, error) {
	dir, err := state.DeviceDir()
	if err != nil {
		return nil, false, err
	}
	return lock.Acquire(dir)
}

// withLock führt fn unter der gerätweiten Sperre aus. Ein echter Fehler beim Sperren wird genannt,
// nicht als „läuft gerade“ ausgegeben: Sonst sucht der Nutzer nach einem Abgleich, den es nicht gibt.
func withLock(errOut io.Writer, fn func() int) int {
	release, ok, err := lockDevice()
	if err != nil {
		fmt.Fprintf(errOut, "Gerätweite Sperre nicht möglich: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintln(errOut, "Gerade läuft ein Abgleich. Bitte gleich noch einmal versuchen.")
		return 1
	}
	defer release()
	return fn()
}

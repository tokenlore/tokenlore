package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/secret"
	"github.com/tokenlore/tokenlore/state"
)

// runStatus zeigt Verbindung, letzten Abgleich und was als Nächstes geliefert würde.
// Es nimmt keine Sperre, speichert nichts und liest den Geräteschlüssel nie.
// Alles aus den Dateien wird bereinigt ausgegeben: sie könnten von Hand verändert sein.
func runStatus(args []string, out io.Writer, dataDir, configDir string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(out)
	next := fs.Bool("next", false, "das nächste Paket als JSON zeigen")
	warnings := fs.Bool("warnings", false, "die Texte der Warnungen zeigen (sie nennen Pfade)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	conn, ok, err := state.LoadConnection(dataDir)
	switch {
	case err != nil:
		fmt.Fprintf(out, "Verbindung nicht lesbar: %v\n", err)
	case !ok:
		fmt.Fprintln(out, "Nicht verbunden. Erst /tokenlore:connect ausführen.")
	default:
		fmt.Fprintf(out, "Verbunden mit: %s\n", client.Clean(conn.ServerURL))
		fmt.Fprintf(out, "Gerät: „%s“ (Nummer %d)\n", client.Clean(conn.DeviceName), conn.DeviceID)
		fmt.Fprintf(out, "Schlüsselablage: %s\n", storeName(conn.KeyStore))
		fmt.Fprintf(out, "CA-Fingerabdruck: %s\n", client.Clean(conn.CAFingerprint))
		fmt.Fprintf(out, "Verbunden seit: %s\n", when(conn.ConnectedAt))
	}

	stat, err := state.LoadStatus(dataDir)
	if err != nil {
		fmt.Fprintf(out, "Status nicht lesbar: %v\n", err)
		stat = &state.Status{}
	}
	fmt.Fprintf(out, "Letzter Erfolg: %s\n", when(stat.LastSuccess))
	fmt.Fprintf(out, "Letzter Versuch: %s\n", when(stat.LastAttempt))
	if stat.LastError != "" {
		fmt.Fprintf(out, "Letzter Fehler: %s\n", client.Clean(stat.LastError))
	} else {
		fmt.Fprintln(out, "Letzter Fehler: keiner")
	}
	// Warnungen nennen Pfade und Projektordner. /tokenlore:status landet in Claudes Kontext, daher
	// standardmäßig nur die Anzahl; die Texte nur auf Wunsch (für das Terminal).
	fmt.Fprintf(out, "Warnungen: %d\n", len(stat.Warnings))
	if *warnings {
		for _, w := range stat.Warnings {
			fmt.Fprintf(out, "  - %s\n", client.Clean(w))
		}
	} else if len(stat.Warnings) > 0 {
		fmt.Fprintln(out, "  (Texte mit: /tokenlore:status --warnings – sie enthalten Pfade, die Claude dann mitliest)")
	}

	st, err := state.Load(dataDir)
	if err != nil {
		fmt.Fprintf(out, "Merkzettel nicht lesbar: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "E-Mail-Stufe: %s\n", client.Clean(st.EmailMode))
	res, parts, err := deliver.Prepare(context.Background(), configDir, version, st, time.Now)
	if err != nil {
		fmt.Fprintf(out, "Offen: nicht ermittelbar: %s\n", client.Clean(err.Error()))
		return 1
	}
	// Dieselbe Regel wie beim Abgleich: Split bildet immer ein erstes Paket, auch wenn es nichts Neues trägt.
	if deliver.NothingNew(st, res) {
		fmt.Fprintln(out, "Kein Paket offen.")
		return 0
	}
	n := 0
	for _, p := range parts {
		n += len(p.Requests)
	}
	fmt.Fprintf(out, "Offen: %s\n", requestsInParts(n, len(parts)))
	if *next {
		if len(parts) == 0 {
			fmt.Fprintln(out, "Kein Paket offen.")
			return 0
		}
		fmt.Fprintln(out, "Nächstes Paket:")
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(parts[0]); err != nil {
			return 1
		}
	}
	return 0
}

func storeName(kind string) string {
	switch kind {
	case secret.KindKeyring:
		return "Schlüsselspeicher des Systems"
	case secret.KindFile:
		return "Datei nur für den Benutzer"
	default:
		return "unbekannt"
	}
}

func when(t time.Time) string {
	if t.IsZero() {
		return "noch nie"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

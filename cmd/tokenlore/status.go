package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
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

	conn, connected, err := state.LoadConnection(dataDir)
	connected = connected && err == nil
	switch {
	case err != nil:
		fmt.Fprintf(out, "Verbindung nicht lesbar: %v\n", err)
	case !connected:
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
	if stat.KeyRejected != "" && stat.KeyRejected != stat.LastError {
		fmt.Fprintf(out, "Schlüssel abgelehnt: %s\n", client.Clean(stat.KeyRejected))
	}
	for _, n := range deliver.Notices(stat) {
		fmt.Fprintf(out, "Hinweis: %s\n", n)
	}
	if !stat.ArchiveRecreated.IsZero() {
		fmt.Fprintf(out, "Hinweis: %s (%s)\n", deliver.MsgArchiveRecreated, when(stat.ArchiveRecreated))
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
	fmt.Fprintf(out, "Signaturschlüssel: %s\n", signKeyInfo(st))
	paired := ""
	if connected {
		paired = conn.Identity
		dc := deliver.CheckDevice(configDir, st, paired, machineHash())
		// Steht die Meldung schon als letzter Fehler da, nicht noch einmal; die Schritte zum Neukoppeln nur einmal.
		if dc.Mismatch && stat.LastError != deliver.MsgOtherDevice {
			fmt.Fprintf(out, "Achtung: %s\n", deliver.MsgOtherDevice)
		}
		if dc.Mismatch || stat.KeyRejected != "" || stat.LastError == deliver.MsgSignKeyMissing || stat.LastError == deliver.MsgSignKeyTaken {
			fmt.Fprintln(out, deliver.RepairSteps(client.Clean(conn.ServerURL)))
		}
		if dc.NoMachine {
			fmt.Fprintf(out, "Warnung: %s\n", deliver.MsgNoMachine)
		}
	}
	res, err := deliver.Prepare(context.Background(), configDir, paired, st, time.Now)
	// Neue Felder aus den bisherigen Abgleichen und aus dem, was jetzt offen ist; nur Namen, nie Werte.
	stat.AddNewFields(res.NewFields)
	if len(stat.NewFields) > 0 {
		fmt.Fprintf(out, "Neue Felder in den Sitzungsdateien (nicht übernommen): %s\n", client.Clean(strings.Join(stat.NewFields, ", ")))
	}
	if err != nil {
		fmt.Fprintf(out, "Offen: nicht ermittelbar: %s\n", client.Clean(err.Error()))
		return 1
	}
	// Was offen ist, steht im Archiv; status liest es nur. Ohne Kennung gibt es noch kein Archiv.
	var dl *deliver.Delivery
	if dir, err := deliver.ArchiveDir(dataDir, configDir, st, paired); err == nil {
		if dl, _, err = deliver.PrepareDelivery(dir, st, version, time.Now()); err != nil {
			fmt.Fprintf(out, "Offen: nicht ermittelbar: %s\n", client.Clean(err.Error()))
			return 1
		}
		if n := dl.DamagedTotal(); n > 0 {
			fmt.Fprintf(out, "Ausgesonderte Archivzeilen (Signatur oder Inhalt passt nicht, gehen nie mit): %d\n", n)
		}
		if n := dl.ForeignTotal(); n > 0 {
			fmt.Fprintf(out, "Hinweis: %s\n", deliver.ForeignMessage(n))
		}
	}
	if deliver.NothingNew(res, dl) {
		fmt.Fprintln(out, "Kein Paket offen.")
		return 0
	}
	if dl != nil && !dl.NothingNew {
		fmt.Fprintf(out, "Offen: %s\n", requestsInParts(dl.Requests, len(dl.Parts)))
		if dl.Days > 0 {
			fmt.Fprintf(out, "Offen aus der Vorgeschichte: %d %s\n", dl.Days, plural(dl.Days, "Tag", "Tage"))
		}
	}
	if n := len(res.Lines); n > 0 {
		fmt.Fprintf(out, "Noch nicht im Archiv: %d %s (kommen beim nächsten Abgleich dazu)\n", n, plural(n, "Anfrage", "Anfragen"))
	}
	if *next {
		if dl == nil || len(dl.Parts) == 0 {
			fmt.Fprintln(out, "Im Archiv ist kein Paket offen.")
			return 0
		}
		fmt.Fprintln(out, "Nächstes Paket:")
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(dl.Parts[0]); err != nil {
			return 1
		}
	}
	return 0
}

// pairedIdentity ist die Kennung der gespeicherten Kopplung, leer ohne Verbindung.
func pairedIdentity(dataDir string) string {
	conn, ok, err := state.LoadConnection(dataDir)
	if err != nil || !ok {
		return ""
	}
	return conn.Identity
}

// signKeyInfo beschreibt den Signaturschlüssel aus dem Merkzettel: Ablage und Zahl der früheren. Den privaten Schlüssel
// liest status nie; ob er fehlt, meldet der letzte Abgleich.
func signKeyInfo(st *state.State) string {
	if st.SignPublicKey == "" {
		return "keiner (entsteht beim Koppeln)"
	}
	s := storeName(st.SignKeyStore)
	if n := len(st.SignPublicKeysPrevious); n > 0 {
		s += fmt.Sprintf(" (%d %s)", n, plural(n, "früherer", "frühere"))
	}
	return s
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

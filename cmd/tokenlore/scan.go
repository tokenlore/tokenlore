package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/state"
)

// runScan liest die fertigen Anfragen ein, die der nächste Abgleich ins Archiv übernähme, und zeigt sie. Es sendet
// nichts, schreibt kein Archiv und speichert den Merkzettel nie; das tut allein der Abgleich.
func runScan(args []string, out io.Writer, dataDir, configDir string) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Bool("dry-run", true, "nichts senden (immer so)")
	summary := fs.Bool("summary", false, "Zusammenfassung ausgeben")
	asJSON := fs.Bool("json", false, "Inhaltszeilen als JSON ausgeben")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	st, err := state.Load(dataDir)
	if err != nil {
		fmt.Fprintf(out, "Merkzettel nicht lesbar: %v\n", err)
		return 1
	}
	res, err := deliver.Prepare(context.Background(), configDir, pairedIdentity(dataDir), st, time.Now)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}

	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", " ")
		enc.SetEscapeHTML(false)
		lines := res.Lines
		if lines == nil {
			lines = []protocol.RequestLine{}
		}
		if err := enc.Encode(lines); err != nil {
			return 1
		}
	}
	if *summary {
		printSummary(out, res.Lines, len(res.Warnings))
	}
	return 0
}

type modelSum struct{ n, input, output, cacheCreate, cacheRead int64 }

// printSummary zählt wie das Archiv: jede (message_id, request_id) nur einmal, mit der zuerst gelesenen Zeile.
func printSummary(out io.Writer, lines []protocol.RequestLine, warnings int) {
	seen := map[string]bool{}
	sessions := map[string]bool{}
	sums := map[string]*modelSum{}
	n := 0
	for _, l := range lines {
		k := l.MessageID + "|" + l.RequestID
		if seen[k] {
			continue
		}
		seen[k] = true
		n++
		sessions[l.SessionID] = true
		c, err := protocol.UsageCounts(l.Usage)
		if err != nil {
			continue // Zeilen aus dem Scan sind geprüft; sicher ist sicher
		}
		s := sums[l.Model]
		if s == nil {
			s = &modelSum{}
			sums[l.Model] = s
		}
		s.n++
		s.input += c.Input
		s.output += c.Output
		s.cacheCreate += c.CacheCreate
		s.cacheRead += c.CacheRead
	}
	fmt.Fprintf(out, "Anfragen gesamt: %d\nSitzungen: %d\nWarnungen: %d\n", n, len(sessions), warnings)
	fmt.Fprintf(out, "%-28s %8s %14s %14s %14s %16s\n", "Modell", "Anfragen", "input", "output", "cache_create", "cache_read")
	for _, m := range slices.Sorted(maps.Keys(sums)) {
		s := sums[m]
		fmt.Fprintf(out, "%-28s %8d %14d %14d %14d %16d\n", m, s.n, s.input, s.output, s.cacheCreate, s.cacheRead)
	}
}

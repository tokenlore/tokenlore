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

// runScan liest neue Nutzungszahlen ein und zeigt, was geliefert würde. Es sendet nichts und
// speichert den Merkzettel nie; das tut allein der Abgleich nach vollständig erfolgreicher Lieferung.
func runScan(args []string, out io.Writer, dataDir, configDir string) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Bool("dry-run", true, "nichts senden (immer so)")
	summary := fs.Bool("summary", false, "Zusammenfassung ausgeben")
	asJSON := fs.Bool("json", false, "Pakete als JSON ausgeben")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	st, err := state.Load(dataDir)
	if err != nil {
		fmt.Fprintf(out, "Merkzettel nicht lesbar: %v\n", err)
		return 1
	}
	res, parts, err := deliver.Prepare(context.Background(), configDir, version, st, time.Now)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}

	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", " ")
		if err := enc.Encode(parts); err != nil {
			return 1
		}
	}
	if *summary {
		printSummary(out, res.Batch, len(parts), len(res.Warnings))
	}
	return 0
}

type modelSum struct{ n, input, output, cacheCreate, cacheRead int64 }

// printSummary zählt wie der Server: jede (message_id, request_id) nur einmal, je Feld das Maximum.
func printSummary(out io.Writer, b protocol.Batch, parts, warnings int) {
	seen := map[string]*protocol.Request{}
	var keys []string
	for _, r := range b.Requests {
		k := r.MessageID + "|" + r.RequestID
		if p := seen[k]; p != nil {
			p.Input, p.Output = max(p.Input, r.Input), max(p.Output, r.Output)
			p.CacheCreate, p.CacheRead = max(p.CacheCreate, r.CacheCreate), max(p.CacheRead, r.CacheRead)
			continue
		}
		c := r
		seen[k] = &c
		keys = append(keys, k)
	}
	sums := map[string]*modelSum{}
	for _, k := range keys {
		r := seen[k]
		s := sums[r.Model]
		if s == nil {
			s = &modelSum{}
			sums[r.Model] = s
		}
		s.n++
		s.input += r.Input
		s.output += r.Output
		s.cacheCreate += r.CacheCreate
		s.cacheRead += r.CacheRead
	}
	fmt.Fprintf(out, "Anfragen gesamt: %d\nSitzungen: %d\nPakete: %d\nKonten: %d\nAltbestand-Tage: %d\nWarnungen: %d\n",
		len(seen), len(b.Sessions), parts, len(b.Accounts), len(b.LegacyActivity), warnings)
	fmt.Fprintf(out, "%-28s %8s %14s %14s %14s %16s\n", "Modell", "Anfragen", "input", "output", "cache_create", "cache_read")
	for _, m := range slices.Sorted(maps.Keys(sums)) {
		s := sums[m]
		fmt.Fprintf(out, "%-28s %8d %14d %14d %14d %16d\n", m, s.n, s.input, s.output, s.cacheCreate, s.cacheRead)
	}
}

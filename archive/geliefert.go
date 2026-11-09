package archive

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tokenlore/tokenlore/state"
)

// DeliveredFile ist der Merker „geliefert“ im Archivordner.
const DeliveredFile = "geliefert.json"

// Delivered ist der Inhalt von geliefert.json: was aus diesem Archiv schon beim Server ist. Eine signierte Zeile
// bleibt unverändert; deshalb steht der Merker in einer eigenen Datei. Neukoppeln löscht sie (ResetDelivered), dann
// geht das ganze Archiv noch einmal; der Server zählt jede Anfrage einmal.
type Delivered struct {
	// Lines: je Archivdatei (Name ohne .gz, etwa 2026-10.jsonl) die Zahl der Zeilen vom Anfang an, die erledigt sind:
	// geliefert oder als beschädigt ausgesondert. Gezählt wird im ungepackten Inhalt, damit die Zahl nach dem Packen gilt.
	Lines map[string]int `json:"zeilen"`
	// RejectedDays sind die Vorgeschichte-Tage, die der Server abgewiesen hat, sortiert.
	RejectedDays []string `json:"abgewiesene_tage,omitempty"`
	// Damaged zählt die ausgesonderten Zeilen (Signatur oder Inhalt passt nicht); sie gehen nie mit.
	Damaged int `json:"beschaedigt,omitempty"`
	// Foreign zählt die Zeilen, deren früheren Signaturschlüssel der Server einem anderen Gerät zuordnet; sie gehen
	// ebenfalls nie mit. Die Zeilen selbst bleiben unverändert im Archiv.
	Foreign int `json:"fremder_schluessel,omitempty"`
	// AccountsSum ist die hex-SHA-256 der zuletzt gelieferten Kontenliste; ändert sie sich, geht sie wieder mit.
	AccountsSum string `json:"konten_summe,omitempty"`
}

// LoadDelivered liest geliefert.json aus dem Archivordner dir. Fehlt die Datei, ist nichts geliefert. Ist sie
// unlesbar, gilt ebenfalls nichts als geliefert (broken = true): Doppelt Geliefertes zählt der Server nur einmal,
// Fehlendes ginge dagegen verloren.
func LoadDelivered(dir string) (d *Delivered, broken bool, err error) {
	d = &Delivered{Lines: map[string]int{}}
	b, err := os.ReadFile(filepath.Join(dir, DeliveredFile))
	if errors.Is(err, fs.ErrNotExist) {
		return d, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var got Delivered
	if json.Unmarshal(b, &got) != nil {
		return d, true, nil
	}
	for name, n := range got.Lines {
		if n > 0 && deliverable(name) {
			d.Lines[name] = n
		}
	}
	d.RejectedDays, d.Damaged, d.Foreign, d.AccountsSum = got.RejectedDays, max(got.Damaged, 0), max(got.Foreign, 0), got.AccountsSum
	return d, false, nil
}

// Save schreibt geliefert.json atomar in den Archivordner dir.
func (d *Delivered) Save(dir string) error {
	c := *d
	if len(c.RejectedDays) == 0 {
		c.RejectedDays = nil
	} else {
		c.RejectedDays = slices.Compact(slices.Sorted(slices.Values(c.RejectedDays)))
	}
	b, err := json.MarshalIndent(&c, "", " ")
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(dir, DeliveredFile, append(b, '\n'))
}

// ResetDelivered löscht den Merker im Archivordner dir: Danach geht das ganze Archiv noch einmal an den Server.
// Alles andere im Archiv und die Lesestände bleiben.
func ResetDelivered(dir string) error {
	err := os.Remove(filepath.Join(dir, DeliveredFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Source ist eine Archivdatei, aus der geliefert wird.
type Source struct {
	Name string // Name im Merker: ohne .gz
	Path string // Pfad der Datei, gepackt oder nicht
	Day  bool   // Vorgeschichte (kind day), sonst Anfragen (kind request)
}

// deliverable meldet, ob name (ohne .gz) eine Archivdatei ist, aus der geliefert wird.
func deliverable(name string) bool {
	if name == LegacyFile {
		return true
	}
	m, ok := strings.CutSuffix(name, ".nachtrag.jsonl")
	if !ok {
		m, ok = strings.CutSuffix(name, ".jsonl")
	}
	return ok && monthPattern.MatchString(m)
}

// Sources listet die Archivdateien im Ordner dir in Lieferreihenfolge: Vorgeschichte, dann je Monat (aufsteigend)
// die Monatsdatei oder, wenn gepackt, die .gz, danach der Nachtrag. Andere Dateien und Unterordner gehören nicht
// dazu. Fehlt der Ordner, gibt es keine Dateien.
func Sources(dir string) ([]Source, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			have[e.Name()] = true
		}
	}
	var out []Source
	if have[LegacyFile] {
		out = append(out, Source{Name: LegacyFile, Path: filepath.Join(dir, LegacyFile), Day: true})
	}
	months := map[string]bool{}
	for n := range have {
		for _, suf := range []string{".jsonl.gz", ".nachtrag.jsonl", ".jsonl"} {
			if m, ok := strings.CutSuffix(n, suf); ok && monthPattern.MatchString(m) {
				months[m] = true
				break
			}
		}
	}
	for _, m := range slices.Sorted(maps.Keys(months)) {
		switch {
		case have[PackedFile(m)]:
			out = append(out, Source{Name: MonthFile(m), Path: filepath.Join(dir, PackedFile(m))})
		case have[MonthFile(m)]:
			out = append(out, Source{Name: MonthFile(m), Path: filepath.Join(dir, MonthFile(m))})
		}
		if have[NachtragFile(m)] {
			out = append(out, Source{Name: NachtragFile(m), Path: filepath.Join(dir, NachtragFile(m))})
		}
	}
	return out, nil
}

// ReadLines liest die Zeilen einer Archivdatei (gepackt oder nicht) roh, ohne Leerzeilen und ohne eine halbe letzte
// Zeile. Die Position einer Zeile in der Liste ist ihre Nummer im Merker.
func ReadLines(p string) ([][]byte, error) {
	var out [][]byte
	err := eachLine(p, func(b []byte) { out = append(out, slices.Clone(b)) })
	return out, err
}

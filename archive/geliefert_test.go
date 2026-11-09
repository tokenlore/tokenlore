package archive

import (
	"os"
	"path/filepath"
	"testing"
)

// Lieferreihenfolge: Vorgeschichte, dann je Monat die Monatsdatei oder die .gz, danach der Nachtrag. Andere Dateien,
// Unterordner und fremde Namen gehören nicht dazu; der Name im Merker ist immer der ohne .gz.
func TestSourcesOrderAndNames(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"2026-11.jsonl", "2026-10.jsonl.gz", "2026-10.jsonl.gz.sig", "2026-10.nachtrag.jsonl",
		LegacyFile, AccountsFile, DeliveredFile, "notizen.jsonl", "2026-9.jsonl", "x.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	os.Mkdir(filepath.Join(dir, "2026-12.jsonl"), 0o700)
	srcs, err := Sources(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Source{
		{Name: LegacyFile, Path: filepath.Join(dir, LegacyFile), Day: true},
		{Name: "2026-10.jsonl", Path: filepath.Join(dir, "2026-10.jsonl.gz")},
		{Name: "2026-10.nachtrag.jsonl", Path: filepath.Join(dir, "2026-10.nachtrag.jsonl")},
		{Name: "2026-11.jsonl", Path: filepath.Join(dir, "2026-11.jsonl")},
	}
	if len(srcs) != len(want) {
		t.Fatalf("Quellen %+v", srcs)
	}
	for i := range want {
		if srcs[i] != want[i] {
			t.Fatalf("%d: %+v, want %+v", i, srcs[i], want[i])
		}
	}
	if s, err := Sources(filepath.Join(dir, "fehlt")); err != nil || s != nil {
		t.Fatalf("fehlender Ordner: %v %v", s, err)
	}
}

// Die Zeilen eines gepackten Monats zählen wie vor dem Packen: Der Merker gilt danach unverändert.
func TestReadLinesSameAfterPacking(t *testing.T) {
	dir := newDir(t)
	a := open(t, dir, nov6)
	add(t, a, req("m1", "r1", "2026-10-01T10:00:00Z", 1), req("m2", "r2", "2026-10-02T10:00:00Z", 1))
	before, err := ReadLines(filepath.Join(dir, MonthFile("2026-10")))
	if err != nil || len(before) != 2 {
		t.Fatalf("%d Zeilen, %v", len(before), err)
	}
	if _, err := a.Pack(nil); err != nil {
		t.Fatal(err)
	}
	after, err := ReadLines(filepath.Join(dir, PackedFile("2026-10")))
	if err != nil || len(after) != 2 || string(after[1]) != string(before[1]) {
		t.Fatalf("nach dem Packen %d Zeilen, %v", len(after), err)
	}
}

// Ein unlesbarer Merker gilt als leer (alles noch einmal); unbekannte Namen und negative Zahlen fallen weg.
func TestLoadDelivered(t *testing.T) {
	dir := t.TempDir()
	d, broken, err := LoadDelivered(dir)
	if err != nil || broken || len(d.Lines) != 0 {
		t.Fatalf("fehlend: %+v %v %v", d, broken, err)
	}
	os.WriteFile(filepath.Join(dir, DeliveredFile), []byte("{kaputt"), 0o600)
	if d, broken, err = LoadDelivered(dir); err != nil || !broken || len(d.Lines) != 0 {
		t.Fatalf("kaputt: %+v %v %v", d, broken, err)
	}
	os.WriteFile(filepath.Join(dir, DeliveredFile), []byte(`{"zeilen":{"2026-10.jsonl":3,"../x.jsonl":5,"2026-11.jsonl":-1,"vorgeschichte.jsonl":2},"beschaedigt":-4,"fremder_schluessel":-2}`), 0o600)
	d, broken, err = LoadDelivered(dir)
	if err != nil || broken || len(d.Lines) != 2 || d.Lines["2026-10.jsonl"] != 3 || d.Lines[LegacyFile] != 2 || d.Damaged != 0 || d.Foreign != 0 {
		t.Fatalf("gefiltert: %+v %v %v", d, broken, err)
	}
	os.WriteFile(filepath.Join(dir, DeliveredFile), []byte(`{"zeilen":{},"fremder_schluessel":3}`), 0o600)
	if d, _, _ = LoadDelivered(dir); d.Foreign != 3 {
		t.Fatalf("fremder_schluessel: %+v", d)
	}
}

// Save schreibt sortierte Tage ohne Doppelte, nur für den Benutzer lesbar; ResetDelivered löscht nur den Merker.
func TestSaveAndResetDelivered(t *testing.T) {
	dir := t.TempDir()
	d := &Delivered{Lines: map[string]int{"2026-10.jsonl": 1}, RejectedDays: []string{"2026-08-15", "2026-08-14", "2026-08-15"}}
	if err := d.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, _, _ := LoadDelivered(dir)
	if len(got.RejectedDays) != 2 || got.RejectedDays[0] != "2026-08-14" {
		t.Fatalf("Tage %v", got.RejectedDays)
	}
	if fi, _ := os.Stat(filepath.Join(dir, DeliveredFile)); fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("Rechte %v", fi.Mode())
	}
	os.WriteFile(filepath.Join(dir, MonthFile("2026-10")), []byte("x\n"), 0o600)
	if err := ResetDelivered(dir); err != nil {
		t.Fatal(err)
	}
	if err := ResetDelivered(dir); err != nil {
		t.Fatalf("zweimal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, MonthFile("2026-10"))); err != nil {
		t.Fatal("Archivdatei mit gelöscht")
	}
	if _, err := os.Stat(filepath.Join(dir, DeliveredFile)); !os.IsNotExist(err) {
		t.Fatal("Merker noch da")
	}
}

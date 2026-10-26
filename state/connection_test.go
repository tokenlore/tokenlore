package state

import (
	"testing"
	"time"
)

func TestConnectionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Connection{
		ServerURL: "https://example.test", CAPEM: "PEM", CAFingerprint: "ab:cd",
		DeviceID: 7, DeviceName: "Laptop", KeyStore: "keyring", KeySum: "abc",
		ConnectedAt: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
	}
	if err := want.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadConnection(dir)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !got.ConnectedAt.Equal(want.ConnectedAt) {
		t.Errorf("ConnectedAt %v", got.ConnectedAt)
	}
	got.ConnectedAt = want.ConnectedAt
	if *got != want {
		t.Errorf("got %+v, want %+v", *got, want)
	}
}

func TestConnectionMissing(t *testing.T) {
	c, ok, err := LoadConnection(t.TempDir())
	if err != nil || ok || c != nil {
		t.Fatalf("c=%v ok=%v err=%v", c, ok, err)
	}
}

func TestRemoveConnectionMissingIsOK(t *testing.T) {
	dir := t.TempDir()
	if err := RemoveConnection(dir); err != nil {
		t.Fatal(err)
	}
	c := Connection{ServerURL: "x"}
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	if err := RemoveConnection(dir); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadConnection(dir); ok {
		t.Error("Verbindung sollte entfernt sein")
	}
}

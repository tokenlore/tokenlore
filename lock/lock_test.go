package lock

import "testing"

func TestAcquireTwice(t *testing.T) {
	dir := t.TempDir()
	release, ok, err := Acquire(dir)
	if err != nil || !ok {
		t.Fatalf("erstes Acquire: ok=%v err=%v", ok, err)
	}
	_, ok2, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok2 {
		t.Fatal("zweites Acquire darf nicht gelingen")
	}
	release()
	release3, ok3, err := Acquire(dir)
	if err != nil || !ok3 {
		t.Fatalf("Acquire nach release: ok=%v err=%v", ok3, err)
	}
	release3()
}

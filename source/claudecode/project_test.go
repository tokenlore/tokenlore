package claudecode

import "testing"

func TestProjectIDEmptyWithoutSalt(t *testing.T) {
	if got := ProjectID("", "x"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestProjectIDStable(t *testing.T) {
	name := "C--Users-a-Projekte-b"
	a, b := ProjectID("00ff", name), ProjectID("00ff", name)
	if a != b || len(a) != 64 || a == name {
		t.Fatalf("a=%q b=%q", a, b)
	}
	if ProjectID("0100", name) == a {
		t.Fatal("anderes Salz muss andere Kennung ergeben")
	}
}

package protocol

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

func testSignKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	return ed25519.NewKeyFromSeed(seed)
}

// SignLine ergibt für die Beispielzeile aus doc.md genau deren Signatur (Ed25519 ist deterministisch), und
// VerifyLine nimmt sie an. So prüft der Test Vorsatz und Kodierung gegen die Doku, nicht gegen sich selbst.
func TestSignLineMatchesDocExample(t *testing.T) {
	doc, err := os.ReadFile("doc.md")
	if err != nil {
		t.Fatal(err)
	}
	ex := regexp.MustCompile(`(?m)^\{"z":"\{\\"kind\\":\\"request\\".*$`).Find(doc)
	if ex == nil {
		t.Fatal("Beispielzeile nicht gefunden")
	}
	var h struct{ Z, S string }
	if err := json.Unmarshal(ex, &h); err != nil {
		t.Fatal(err)
	}
	priv := testSignKey(t)
	if got := SignLine(priv, []byte(h.Z)); got != h.S {
		t.Fatalf("Signatur %q, erwartet %q", got, h.S)
	}
	pub := EncodeSignPublicKey(priv.Public().(ed25519.PublicKey))
	if pub != "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=" {
		t.Fatalf("öffentlicher Schlüssel %q", pub)
	}
	if !VerifyLine(pub, []byte(h.Z), h.S) {
		t.Fatal("VerifyLine lehnt die Beispielzeile ab")
	}
}

func TestVerifyLineRejects(t *testing.T) {
	priv := testSignKey(t)
	pub := EncodeSignPublicKey(priv.Public().(ed25519.PublicKey))
	z := []byte(`{"kind":"request","request_id":"req_1"}`)
	sig := SignLine(priv, z)
	other := EncodeSignPublicKey(ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey))
	raw, _ := base64.StdEncoding.DecodeString(sig)
	plain := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, z)) // ohne Vorsatz
	for name, c := range map[string]struct{ pub, z, sig string }{
		"geänderte Zeile":   {pub, strings.Replace(string(z), "req_1", "req_2", 1), sig},
		"anderer Schlüssel": {other, string(z), sig},
		"ohne Vorsatz":      {pub, string(z), plain},
		"Signatur kurz":     {pub, string(z), base64.StdEncoding.EncodeToString(raw[:63])},
		"Signatur kein B64": {pub, string(z), "!!"},
		"Schlüssel kaputt":  {"A6EHv", string(z), sig},
		"Schlüssel leer":    {"", string(z), sig},
	} {
		if VerifyLine(c.pub, []byte(c.z), c.sig) {
			t.Errorf("%s: angenommen", name)
		}
	}
	if !VerifyLine(pub, z, sig) {
		t.Fatal("gültige Zeile abgelehnt")
	}
}

func TestSignPublicKeyFormat(t *testing.T) {
	good := "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="
	if !ValidSignPublicKey(good) {
		t.Fatal("gültiger Schlüssel abgelehnt")
	}
	for _, bad := range []string{"", "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg", "A6EHv_POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=",
		base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, 33)), " " + good} {
		if ValidSignPublicKey(bad) {
			t.Errorf("%q angenommen", bad)
		}
	}
}

// Besitznachweis beim Koppeln: Signatur über "tokenlore-pair-v2\n" + server_id + "\n" + Kopplungscode, so wie der
// Server ihn prüft (sodium_crypto_sign_verify_detached über dieselben Bytes). Der Wert für den Beispielschlüssel und
// die Beispiel-Kennung steht in doc.md.
func TestPairProof(t *testing.T) {
	priv := testSignKey(t)
	pub := EncodeSignPublicKey(priv.Public().(ed25519.PublicKey))
	const sid = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const otherSid = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	proof := SignPairProof(priv, sid, "123456")
	raw, err := base64.StdEncoding.Strict().DecodeString(proof)
	if err != nil || len(raw) != ed25519.SignatureSize {
		t.Fatalf("Nachweis %q: %v", proof, err)
	}
	if !ed25519.Verify(priv.Public().(ed25519.PublicKey), []byte("tokenlore-pair-v2\n"+sid+"\n123456"), raw) {
		t.Fatal("Nachweis gilt nicht für Vorsatz, server_id und Code")
	}
	if !VerifyPairProof(pub, sid, "123456", proof) {
		t.Fatal("VerifyPairProof lehnt den eigenen Nachweis ab")
	}
	other := EncodeSignPublicKey(ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey))
	v1 := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("tokenlore-pair-v1\n123456")))
	for name, c := range map[string]struct{ pub, sid, code, proof string }{
		"anderer Code":      {pub, sid, "123457", proof},
		"andere server_id":  {pub, otherSid, "123456", proof},
		"anderer Schlüssel": {other, sid, "123456", proof},
		"Zeilensignatur":    {pub, sid, "123456", SignLine(priv, []byte("123456"))},
		"alter Nachweis v1": {pub, sid, "123456", v1},
		"leer":              {pub, sid, "123456", ""},
	} {
		if VerifyPairProof(c.pub, c.sid, c.code, c.proof) {
			t.Errorf("%s: angenommen", name)
		}
	}
	doc, err := os.ReadFile("doc.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "`"+proof+"`") {
		t.Fatalf("Beispiel-Nachweis %s fehlt in doc.md", proof)
	}
}

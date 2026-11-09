package protocol

import (
	"crypto/ed25519"
	"encoding/base64"
)

// SignPrefix steht beim Signieren vor jeder Inhaltszeile, damit eine Signatur nie für etwas anderes gilt
// (protocol/doc.md, „Signieren“).
const SignPrefix = "tokenlore-archiv-v1\n"

// MonthSignPrefix steht beim Signieren vor den Bytes eines gepackten Monats (protocol/doc.md, „Archiv des Plugins“).
const MonthSignPrefix = "tokenlore-archiv-monat-v1\n"

// PairProofPrefix steht beim Besitznachweis vor server_id und Kopplungscode (protocol/doc.md, „Koppeln“).
const PairProofPrefix = "tokenlore-pair-v2\n"

// MaxSignKeysPrevious ist die Grenze für sign_public_keys_previous beim Koppeln.
const MaxSignKeysPrevious = 16

// Envelope ist die Hülle einer signierten Zeile: die Inhaltszeile als Text und ihre Signatur (protocol/doc.md, „Hülle“).
type Envelope struct {
	Z string `json:"z"`
	S string `json:"s"`
}

// signedBytes ist der Vorsatz plus die Inhaltszeile, in einem neuen Puffer.
func signedBytes(z []byte) []byte {
	return withPrefix(SignPrefix, z)
}

func withPrefix(prefix string, b []byte) []byte {
	out := make([]byte, 0, len(prefix)+len(b))
	return append(append(out, prefix...), b...)
}

// SealLine signiert die Inhaltszeile z und gibt die Hülle als kompaktes JSON in einer Zeile zurück (ohne
// Zeilenumbruch), so wie sie im Archiv steht.
func SealLine(priv ed25519.PrivateKey, z []byte) ([]byte, error) {
	return MarshalLine(Envelope{Z: string(z), S: SignLine(priv, z)})
}

// SignMonth signiert die Bytes einer gepackten Monatsdatei (.jsonl.gz) und gibt die Signatur als Base64 zurück.
func SignMonth(priv ed25519.PrivateKey, gz []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, withPrefix(MonthSignPrefix, gz)))
}

// VerifyMonth prüft die Signatur sig (Base64) einer gepackten Monatsdatei gegen den öffentlichen Schlüssel pub.
func VerifyMonth(pub string, gz []byte, sig string) bool {
	return verify(pub, withPrefix(MonthSignPrefix, gz), sig)
}

// SignPairProof bildet den Besitznachweis sign_public_key_proof für den Server serverID (server_id aus
// GET /api/plugin/info) und den Kopplungscode code: die Signatur über PairProofPrefix, serverID, einen Zeilenumbruch
// und code, als Base64. Er gilt nur für diesen Server und diesen Code und zeigt dem Server, dass dieses Gerät den
// privaten Teil des gemeldeten Schlüssels hat.
func SignPairProof(priv ed25519.PrivateKey, serverID, code string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, pairProofBytes(serverID, code)))
}

// VerifyPairProof prüft einen Besitznachweis proof (Base64) für serverID und code gegen den öffentlichen Schlüssel
// pub (Base64).
func VerifyPairProof(pub, serverID, code, proof string) bool {
	return verify(pub, pairProofBytes(serverID, code), proof)
}

func pairProofBytes(serverID, code string) []byte {
	return withPrefix(PairProofPrefix, []byte(serverID+"\n"+code))
}

// SignLine signiert die Inhaltszeile z (die Bytes aus MarshalLine) mit Ed25519 und gibt die Signatur als Base64
// (Standardalphabet, mit "=") zurück, wie sie als "s" in der Hülle steht.
func SignLine(priv ed25519.PrivateKey, z []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, signedBytes(z)))
}

// VerifyLine prüft die Signatur sig (Base64) der Inhaltszeile z gegen den öffentlichen Schlüssel pub (Base64).
// Ein Schlüssel oder eine Signatur im falschen Format gilt als ungültig.
func VerifyLine(pub string, z []byte, sig string) bool {
	return verify(pub, signedBytes(z), sig)
}

func verify(pub string, msg []byte, sig string) bool {
	key, ok := decodeSignPublicKey(pub)
	if !ok {
		return false
	}
	s, err := base64.StdEncoding.Strict().DecodeString(sig)
	if err != nil || len(s) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(key, msg, s)
}

// EncodeSignPublicKey schreibt einen öffentlichen Signaturschlüssel so, wie ihn Pair und state.json führen.
func EncodeSignPublicKey(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// ValidSignPublicKey prüft das Format eines öffentlichen Signaturschlüssels: Base64 mit "=", 44 Zeichen, 32 Byte.
func ValidSignPublicKey(s string) bool {
	_, ok := decodeSignPublicKey(s)
	return ok
}

func decodeSignPublicKey(s string) (ed25519.PublicKey, bool) {
	if len(s) != 44 {
		return nil, false
	}
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, false
	}
	return ed25519.PublicKey(b), true
}

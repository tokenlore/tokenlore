package claudecode

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// ProjectID verschleiert den Projektordnernamen mit dem Salz des Nutzers (HMAC-SHA256, hex).
// Ohne gültiges Salz (Gerät nicht gekoppelt) ist die Kennung leer.
func ProjectID(salt, folderName string) string {
	key, err := hex.DecodeString(salt)
	if salt == "" || err != nil {
		return ""
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(folderName))
	return hex.EncodeToString(m.Sum(nil))
}

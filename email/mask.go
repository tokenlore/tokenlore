// Paket email kürzt die Konto-Adresse auf dem Gerät, bevor sie gesendet wird.
package email

import "strings"

// Mask kürzt eine Adresse: "max.mustermann@beispiel.de" → "ma…@beispiel.de".
// Lokaler Teil mit höchstens 2 Zeichen → erstes Zeichen + "…". Ungültige Adresse → "".
func Mask(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 0 {
		return ""
	}
	local := []rune(addr[:at])
	keep := 2
	if len(local) <= 2 {
		keep = 1
	}
	return string(local[:keep]) + "…" + addr[at:]
}

// ForMode liefert, was bei der Stufe gesendet wird: "off" (und Unbekanntes) → "", "short" → Mask, "full" → Adresse.
func ForMode(mode, addr string) string {
	switch mode {
	case "short":
		return Mask(addr)
	case "full":
		return addr
	default:
		return ""
	}
}

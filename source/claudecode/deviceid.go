package claudecode

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tokenlore/tokenlore/state"
)

// IdentityFile ist die Kennungsdatei im Claude-Konfigurationsordner. Sie macht den Ordner zu einem Gerät: Wer
// mit derselben Kennung neu koppelt, landet beim selben Gerät. tokenlore löscht sie nie.
const IdentityFile = "tokenlore-id"

// brokenSuffix hängt an einer unlesbaren Kennungsdatei, die nicht durch die Kopie zu ersetzen war.
const brokenSuffix = ".kaputt"

var (
	idPattern      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	machinePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// IdentityOrigin sagt, woher die Kennung bei EnsureIdentity kam.
type IdentityOrigin int

const (
	IdentityFromFile     IdentityOrigin = iota // Datei war gültig
	IdentityFromCopy                           // Datei fehlte oder war kaputt, aus der Kopie im Merkzettel neu geschrieben
	IdentityNew                                // weder Datei noch Kopie: neue Kennung
	IdentityNewForBroken                       // kaputte Datei ohne Kopie: neue Kennung, die alte Datei liegt als .kaputt daneben
)

// IdentityPath ist der Pfad der Kennungsdatei.
func IdentityPath(configDir string) string {
	return filepath.Join(configDir, IdentityFile)
}

// parseIdentity liest den Inhalt der Kennungsdatei (oder der Kopie). ok ist false, wenn er kein JSON-Objekt ist
// oder id nach dem Normalisieren (Leerraum weg, klein) keine 32 Hex-Zeichen sind. Die übrigen Felder dürfen
// fehlen oder ungültig sein; sie gelten dann als leer.
func parseIdentity(b []byte) (state.Identity, bool) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) // BOM, wie ihn manche Editoren unter Windows schreiben
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil || raw == nil {
		return state.Identity{}, false
	}
	field := func(name string) string {
		var s string
		if json.Unmarshal(raw[name], &s) != nil {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(s))
	}
	id := state.Identity{ID: field("id"), Machine: field("machine"), Predecessor: field("predecessor")}
	return normalizeIdentity(id)
}

// normalizeIdentity prüft eine Kennung; ungültige Nebenfelder werden leer.
func normalizeIdentity(id state.Identity) (state.Identity, bool) {
	clean := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	id.ID, id.Machine, id.Predecessor = clean(id.ID), clean(id.Machine), clean(id.Predecessor)
	if !idPattern.MatchString(id.ID) {
		return state.Identity{}, false
	}
	if !machinePattern.MatchString(id.Machine) {
		id.Machine = ""
	}
	if !idPattern.MatchString(id.Predecessor) {
		id.Predecessor = ""
	}
	return id, true
}

// ReadIdentity liest die Kennungsdatei, ohne etwas zu ändern. ok ist false, wenn sie fehlt oder kaputt ist.
func ReadIdentity(configDir string) (id state.Identity, ok bool, err error) {
	b, err := os.ReadFile(IdentityPath(configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return state.Identity{}, false, nil
	}
	if err != nil {
		return state.Identity{}, false, err
	}
	id, ok = parseIdentity(b)
	return id, ok, nil
}

// WriteIdentity schreibt die Kennungsdatei atomar.
func WriteIdentity(configDir string, id state.Identity) error {
	b, err := json.Marshal(id)
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(configDir, IdentityFile, append(b, '\n'))
}

// NewIdentityID liefert eine neue Kennung: 128 Bit Zufall als 32 Hex-Zeichen.
func NewIdentityID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// identityDeriveKey ist der feste Schlüssel, mit dem DeriveIdentityID die Kennung aus der Systemkennung ableitet.
const identityDeriveKey = "tokenlore-id-v1"

// DeriveIdentityID leitet die Kennung eines Konfigurationsordners aus der Systemkennung ab: die ersten 32 Hex-Zeichen
// von HMAC-SHA256 (Schlüssel identityDeriveKey) über machine, einen Zeilenumbruch und den echten Pfad von configDir:
// absolut, mit aufgelösten Symlinks; scheitert das Auflösen (etwa weil der Ordner fehlt), der absolute, bereinigte Pfad.
// machine ist schon der Hash aus device.MachineHash; die rohe Systemkennung kommt hier nie vor. Derselbe Ordner auf
// demselben Rechner ergibt immer dieselbe Kennung, auch über einen Symlink erreicht; ein anderer Ordner (etwa der eines
// zweiten Benutzers desselben Rechners) eine andere. Ohne gültige Systemkennung ist das Ergebnis leer.
func DeriveIdentityID(machine, configDir string) string {
	if !machinePattern.MatchString(machine) {
		return ""
	}
	dir, err := filepath.Abs(configDir)
	if err != nil {
		return ""
	}
	dir = filepath.Clean(dir)
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	m := hmac.New(sha256.New, []byte(identityDeriveKey))
	m.Write([]byte(machine + "\n" + dir))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// EnsureIdentity liefert die Kennung dieses Konfigurationsordners und legt die Datei an, wenn nötig:
//   - Datei gültig: sie gilt, auch wenn die Kopie abweicht.
//   - Datei fehlt oder ist kaputt, Kopie gültig: die Kopie gilt und wird in die Datei geschrieben.
//   - Datei fehlt, keine Kopie: Kennung aus DeriveIdentityID, mit machine als Systemkennung; derselbe Rechner bekommt
//     so dieselbe Kennung zurück. Ohne Systemkennung eine zufällige.
//   - Datei kaputt, keine Kopie: ebenso; die kaputte Datei bleibt als tokenlore-id.kaputt erhalten.
//
// copy ist die Kopie aus dem Merkzettel (nil = keine). Die Systemkennung wird hier nicht verglichen, das
// entscheidet der Aufrufer mit MachineConflict.
func EnsureIdentity(configDir string, copy *state.Identity, machine string) (state.Identity, IdentityOrigin, error) {
	b, err := os.ReadFile(IdentityPath(configDir))
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		// Nicht lesbar (Rechte, Sperre): nichts anfassen, sonst entstünde eine zweite Kennung.
		return state.Identity{}, 0, fmt.Errorf("Kennungsdatei nicht lesbar: %w", err)
	}
	if !missing {
		if id, ok := parseIdentity(b); ok {
			return id, IdentityFromFile, nil
		}
	}
	if copy != nil {
		if id, ok := normalizeIdentity(*copy); ok {
			if err := WriteIdentity(configDir, id); err != nil {
				return state.Identity{}, 0, fmt.Errorf("Kennungsdatei nicht geschrieben: %w", err)
			}
			return id, IdentityFromCopy, nil
		}
	}
	origin := IdentityNew
	if !missing {
		if err := keepBroken(configDir); err != nil {
			return state.Identity{}, 0, fmt.Errorf("Kaputte Kennungsdatei nicht beiseitegelegt: %w", err)
		}
		origin = IdentityNewForBroken
	}
	newID := DeriveIdentityID(machine, configDir)
	if newID == "" {
		if newID, err = NewIdentityID(); err != nil {
			return state.Identity{}, 0, err
		}
	}
	id := state.Identity{ID: newID, Machine: machine}
	if err := WriteIdentity(configDir, id); err != nil {
		return state.Identity{}, 0, fmt.Errorf("Kennungsdatei nicht geschrieben: %w", err)
	}
	return id, origin, nil
}

// keepBroken benennt die kaputte Kennungsdatei in tokenlore-id.kaputt um; liegt dort schon eine, in
// tokenlore-id.kaputt.2, .3 usw. Nichts wird überschrieben.
func keepBroken(configDir string) error {
	src := IdentityPath(configDir)
	for n := 1; n < 1000; n++ {
		dst := src + brokenSuffix
		if n > 1 {
			dst = fmt.Sprintf("%s%s.%d", src, brokenSuffix, n)
		}
		if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
			return os.Rename(src, dst)
		}
	}
	return errors.New("zu viele kaputte Kennungsdateien")
}

// MachineConflict meldet, dass die Kennungsdatei von einem anderen Rechner stammt (kopierter
// Konfigurationsordner). Ist eine der beiden Systemkennungen unbekannt, gibt es keinen Konflikt.
func MachineConflict(id state.Identity, machine string) bool {
	return machine != "" && id.Machine != "" && id.Machine != machine
}

// BindIdentity bindet eine Kennung ohne Systemkennung an diesen Rechner: gleiche Kennung, Systemkennung gesetzt.
func BindIdentity(configDir string, id state.Identity, machine string) (state.Identity, error) {
	id.Machine = machine
	return id, WriteIdentity(configDir, id)
}

// ForkIdentity macht diesen Rechner zu einem eigenen Gerät (kopierter Ordner): neue Kennung, die bisherige als
// Vorgänger. Er bekommt damit ein eigenes Archiv; das des Ursprungs liefert er nie.
func ForkIdentity(configDir string, id state.Identity, machine string) (state.Identity, error) {
	newID, err := NewIdentityID()
	if err != nil {
		return state.Identity{}, err
	}
	next := state.Identity{ID: newID, Machine: machine, Predecessor: id.ID}
	return next, WriteIdentity(configDir, next)
}

// NormalizeServerAddress macht aus der Server-Adresse eine feste Schreibweise: Host klein, ohne Standardport
// :443, Pfad ohne "/" am Ende. https://x.de, https://X.de/ und https://x.de:443 ergeben dasselbe.
func NormalizeServerAddress(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6
	}
	if p := u.Port(); p != "" && p != "443" {
		host = net.JoinHostPort(strings.ToLower(u.Hostname()), p)
	}
	return "https://" + host + strings.TrimRight(u.EscapedPath(), "/")
}

// DeviceSource ist die Kennung dieses Geräts für einen Server: Hex-HMAC-SHA256 der server_id aus
// GET /api/plugin/info, Schlüssel ist die Kennung (ihre 32 Hex-Zeichen als Text). Ohne die Kennung ist sie nicht
// zurückzurechnen, jeder Server bekommt eine andere, und zieht der Server mit seiner Datenbank um, bleibt sie gleich.
func DeviceSource(id, serverID string) string {
	m := hmac.New(sha256.New, []byte(id))
	m.Write([]byte(serverID))
	return hex.EncodeToString(m.Sum(nil))
}

// IdentityCheck ist das Ergebnis von CheckIdentity.
type IdentityCheck struct {
	Current state.Identity // gültige Kennung dieses Ordners: die Datei, sonst die Kopie im Merkzettel
	Known   bool           // Current ist gesetzt
	Changed bool           // gekoppelt wurde mit einer anderen Kennung als der heutigen
}

// CheckIdentity vergleicht die Kennung dieses Konfigurationsordners mit der, mit der gekoppelt wurde (paired). Es
// liest nur und schreibt nichts. Fehlt oder ist die Datei kaputt, gilt die Kopie copy (nil = keine). Ist paired
// leer (Kopplung ohne Kennung), gibt es nichts zu vergleichen. Ohne gültige Kennung gilt sie als geändert.
func CheckIdentity(configDir string, copy *state.Identity, paired string) (IdentityCheck, error) {
	var c IdentityCheck
	id, ok, err := ReadIdentity(configDir)
	if err != nil {
		return c, err
	}
	if !ok && copy != nil {
		id, ok = normalizeIdentity(*copy)
	}
	c.Current, c.Known = id, ok
	c.Changed = paired != "" && (!ok || id.ID != strings.ToLower(strings.TrimSpace(paired)))
	return c, nil
}

// TidyIdentity schreibt eine gültige Kennungsdatei in die heutige Form (nur id, machine und predecessor), wenn sie
// davon abweicht, etwa mit last_day und branch_day früherer Fassungen. Kennung, Systemkennung und Vorgänger bleiben.
// Eine fehlende oder kaputte Datei fasst es nicht an; das entscheidet EnsureIdentity. Nur connect ruft es auf, der
// Abgleich schreibt die Kennungsdatei nie.
func TidyIdentity(configDir string) error {
	b, err := os.ReadFile(IdentityPath(configDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	id, ok := parseIdentity(b)
	if !ok {
		return nil
	}
	want, err := json.Marshal(id)
	if err != nil {
		return err
	}
	if bytes.Equal(b, append(want, '\n')) {
		return nil
	}
	return WriteIdentity(configDir, id)
}

// Paket state hält den Merkzettel von tokenlore: Lesestände, Kennung, Signaturschlüssel, Einstellungen.
package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

const fileName = "state.json"

// FileMark ist der Lesestand einer Sitzungsdatei.
type FileMark struct {
	Offset  int64  `json:"offset"`   // ab hier wird beim nächsten Mal gelesen
	End     int64  `json:"end"`      // Ende der zuletzt gelesenen vollständigen Zeile; gleich Dateigröße ⇒ nichts Neues
	HeadLen int    `json:"head_len"` // min(4096, Dateigröße beim Merken)
	HeadSum string `json:"head_sum"` // hex sha256 der ersten HeadLen Bytes
}

// State ist der gesamte Merkzettel.
type State struct {
	// Files sind die Lesestände der Sitzungsdateien, Pfad relativ zum Konfigurationsordner, mit "/". Sie gelten ab
	// dem Archivieren: Was gelesen ist, steht im Archiv; was davon geliefert ist, steht in geliefert.json.
	Files     map[string]FileMark `json:"files"`
	EmailMode string              `json:"email_mode"`
	// Identity ist die Kopie der Kennungsdatei tokenlore-id aus dem Claude-Konfigurationsordner.
	Identity *Identity `json:"identity,omitempty"`
	// SignPublicKey ist der öffentliche Teil des aktuellen Signaturschlüssels (Base64), abgelegt schon beim Erzeugen:
	// Aus einem verlorenen privaten Schlüssel lässt er sich nicht mehr ableiten.
	SignPublicKey string `json:"sign_public_key,omitempty"`
	// SignPublicKeysPrevious sind die öffentlichen Teile ersetzter Signaturschlüssel, älteste zuerst. Sie gehen bei
	// jedem Koppeln mit, damit auch ein neuer Server alte Zeilen prüfen kann. connect leert sie nie.
	SignPublicKeysPrevious []string `json:"sign_public_keys_previous,omitempty"`
	// SignKeysRefused sind frühere öffentliche Schlüssel aus SignPublicKeysPrevious, die der Server einem anderen Gerät
	// zuordnet: aus sign_keys_refused beim Koppeln oder aus einer Ablehnung beim Liefern. Archivzeilen damit gehen nicht
	// mehr mit. connect ersetzt die Liste bei jedem Koppeln durch die Antwort des Servers.
	SignKeysRefused []string `json:"sign_keys_refused,omitempty"`
	// SignKeyStore ist die Ablage des privaten Signaturschlüssels: "keyring" oder "file".
	SignKeyStore string `json:"sign_key_store,omitempty"`
	// ArchiveCreated: Das Archiv dieses Rechners wurde schon einmal angelegt. Fehlt der Archivordner trotzdem, legt
	// der Abgleich ihn neu an, verwirft die Lesestände und übernimmt alles Vorhandene erneut.
	ArchiveCreated bool `json:"archiv_angelegt,omitempty"`
}

// SetSignPublicKey macht pub zum aktuellen Signaturschlüssel. Ein bisheriger, anderer Schlüssel wandert ans Ende von
// SignPublicKeysPrevious; pub selbst steht danach nicht mehr dort. Über maxPrevious hinaus (die Grenze des Protokolls)
// fallen die ältesten heraus.
func (s *State) SetSignPublicKey(pub string, maxPrevious int) {
	if s.SignPublicKey == pub {
		return
	}
	prev := slices.DeleteFunc(slices.Clone(s.SignPublicKeysPrevious), func(k string) bool {
		return k == pub || k == s.SignPublicKey
	})
	if s.SignPublicKey != "" {
		prev = append(prev, s.SignPublicKey)
	}
	if n := len(prev) - maxPrevious; n > 0 {
		prev = prev[n:]
	}
	if len(prev) == 0 {
		prev = nil
	}
	s.SignPublicKey, s.SignPublicKeysPrevious = pub, prev
}

// Identity ist der Inhalt der Kennungsdatei tokenlore-id; die Kopie im Merkzettel hat dasselbe Format.
// Lesen, Prüfen und Schreiben der Datei übernimmt source/claudecode.
type Identity struct {
	ID          string `json:"id"`          // Kennung: 128 Bit Zufall, 32 Hex-Zeichen klein
	Machine     string `json:"machine"`     // hex sha256 der Systemkennung, leer wenn unbekannt
	Predecessor string `json:"predecessor"` // Kennung, von der dieser Rechner abgezweigt wurde, sonst leer
}

// Load liest den Merkzettel; fehlt er, gibt es einen leeren mit E-Mail-Stufe "off".
func Load(dir string) (*State, error) {
	s := &State{}
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(b, s); err != nil {
			return nil, err
		}
	}
	if s.Files == nil {
		s.Files = map[string]FileMark{}
	}
	if s.EmailMode == "" {
		s.EmailMode = "off"
	}
	return s, nil
}

// Save schreibt den Merkzettel atomar (temporäre Datei, dann umbenennen), nur für den Benutzer lesbar.
func (s *State) Save(dir string) error {
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(dir, fileName, b)
}

// Clone liefert eine unabhängige Kopie.
func (s *State) Clone() *State {
	c := *s
	c.Files = maps.Clone(s.Files)
	c.SignPublicKeysPrevious = slices.Clone(s.SignPublicKeysPrevious)
	c.SignKeysRefused = slices.Clone(s.SignKeysRefused)
	if s.Identity != nil {
		id := *s.Identity
		c.Identity = &id
	}
	return &c
}

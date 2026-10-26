package claudecode

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

// CurrentAccount ist das gerade in Claude Code angemeldete Konto. Name und Anzeigename werden nie gelesen.
type CurrentAccount struct {
	AccountID string
	Email     string
}

// ReadCurrentAccount liest oauthAccount aus .claude.json. ok ist false (ohne Fehler),
// wenn Datei oder Konto fehlen, z. B. bei Anmeldung per API-Schlüssel.
func ReadCurrentAccount(claudeJSON string) (CurrentAccount, bool, error) {
	b, err := os.ReadFile(claudeJSON)
	if errors.Is(err, fs.ErrNotExist) {
		return CurrentAccount{}, false, nil
	}
	if err != nil {
		return CurrentAccount{}, false, err
	}
	var f struct {
		OAuth *struct {
			AccountUUID string `json:"accountUuid"`
			Email       string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return CurrentAccount{}, false, err
	}
	if f.OAuth == nil || f.OAuth.AccountUUID == "" {
		return CurrentAccount{}, false, nil
	}
	return CurrentAccount{AccountID: f.OAuth.AccountUUID, Email: f.OAuth.Email}, true, nil
}

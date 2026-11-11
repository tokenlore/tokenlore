package claudecode

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/tokenlore/tokenlore/protocol"
)

// Umgebungsvariablen, mit denen die Desktop-App von Claude jeder Sitzung ihr Konto übergibt. Claude Code
// dokumentiert sie nicht; fehlen sie, gilt das Konto aus .claude.json.
const (
	EnvAccountID = "CLAUDE_CODE_ACCOUNT_UUID"
	EnvEmail     = "CLAUDE_CODE_USER_EMAIL"
)

// Herkunft eines Kontos.
const (
	// FromApp: von der Desktop-App an die Sitzung übergeben.
	FromApp = "app"
	// FromLogin: aus .claude.json, der Anmeldung von claude im Terminal.
	FromLogin = "login"
)

// CurrentAccount ist ein in Claude Code angemeldetes Konto. Name und Anzeigename werden nie gelesen.
type CurrentAccount struct {
	AccountID string
	Email     string
	From      string
}

// AppAccount ist das Konto, das die Desktop-App der Sitzung übergibt. ok ist false ohne gültige Kennung.
func AppAccount(getenv func(string) string) (CurrentAccount, bool) {
	id := strings.TrimSpace(getenv(EnvAccountID))
	if !protocol.IDPattern.MatchString(id) {
		return CurrentAccount{}, false
	}
	return CurrentAccount{AccountID: id, Email: strings.TrimSpace(getenv(EnvEmail)), From: FromApp}, true
}

// SessionAccount ist das Konto der laufenden Sitzung: das der Desktop-App, sonst das aus .claude.json.
// Eine im Terminal oder headless gestartete Sitzung hat keine App-Angabe und nutzt die Anmeldung dort.
func SessionAccount(claudeJSON string, getenv func(string) string) (CurrentAccount, bool, error) {
	if a, ok := AppAccount(getenv); ok {
		return a, true, nil
	}
	return ReadCurrentAccount(claudeJSON)
}

// ReadCurrentAccount liest oauthAccount aus .claude.json, die Anmeldung von claude im Terminal. ok ist false (ohne Fehler),
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
	return CurrentAccount{AccountID: f.OAuth.AccountUUID, Email: f.OAuth.Email, From: FromLogin}, true, nil
}

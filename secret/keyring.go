package secret

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

var errKeyringTimeout = errors.New("Schlüsselspeicher antwortet nicht")

// Für Tests austauschbar: Zeitgrenze und die eigentlichen Schlüsselspeicher-Aufrufe.
var (
	keyringTimeout = 3 * time.Second
	krSet          = keyring.Set
	krGet          = keyring.Get
	krDelete       = keyring.Delete
)

// keyringSet schreibt mit Zeitgrenze. Läuft sie ab, wird der Aufruf nicht abgebrochen (das geht nicht);
// stattdessen löscht die Goroutine den Eintrag wieder, falls das Set doch noch gelingt. So bleibt nach
// einem Rückfall auf die Datei keine unbekannte Kopie im Schlüsselspeicher.
func keyringSet(user, key string) error {
	var mu sync.Mutex
	done, abandoned := false, false
	ch := make(chan error, 1)
	go func() {
		err := krSet(service, user, key)
		mu.Lock()
		done = true
		if !abandoned {
			ch <- err // gepuffert; unter dem Lock, damit der Wartende es sicher sieht
			mu.Unlock()
			return
		}
		mu.Unlock()
		if err == nil {
			_ = krDelete(service, user)
		}
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(keyringTimeout):
		mu.Lock()
		defer mu.Unlock()
		if done { // Ergebnis kam knapp vor der Zeitgrenze
			select {
			case err := <-ch:
				return err
			default:
			}
		}
		abandoned = true
		return errKeyringTimeout
	}
}

func keyringGet(user string) (string, error) {
	type res struct {
		v   string
		err error
	}
	ch := make(chan res, 1)
	go func() {
		v, err := krGet(service, user)
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		if errors.Is(r.err, keyring.ErrNotFound) {
			return "", ErrNotFound
		}
		if r.err != nil {
			return "", fmt.Errorf("Schlüsselspeicher lesen: %w", r.err)
		}
		return r.v, nil
	case <-time.After(keyringTimeout):
		return "", fmt.Errorf("Schlüsselspeicher lesen: %w", errKeyringTimeout)
	}
}

func keyringDelete(user string) error {
	ch := make(chan error, 1)
	go func() { ch <- krDelete(service, user) }()
	select {
	case err := <-ch:
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("Schlüsselspeicher löschen: %w", err)
		}
		return nil
	case <-time.After(keyringTimeout):
		return fmt.Errorf("Schlüsselspeicher löschen: %w", errKeyringTimeout)
	}
}

// Paket lock verhindert, dass zwei Läufe von tokenlore auf einem Gerät gleichzeitig arbeiten.
package lock

import (
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// Acquire versucht die gerätweite Sperre zu nehmen, ohne zu warten.
// ok ist false, wenn ein anderer Lauf sie hält.
func Acquire(dir string) (release func(), ok bool, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	fl := flock.New(filepath.Join(dir, "tokenlore.lock"))
	ok, err = fl.TryLock()
	if err != nil || !ok {
		return nil, false, err
	}
	return func() { _ = fl.Unlock() }, true, nil
}

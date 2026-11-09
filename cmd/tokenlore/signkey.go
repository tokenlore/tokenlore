package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/tokenlore/tokenlore/client"
	"github.com/tokenlore/tokenlore/deliver"
	"github.com/tokenlore/tokenlore/protocol"
	"github.com/tokenlore/tokenlore/secret"
	"github.com/tokenlore/tokenlore/state"
)

// Der Signaturschlüssel gehört zum Rechner, nicht zur Verbindung: connect erzeugt ihn nur, wenn er fehlt, und
// behält ihn sonst bei jedem Koppeln; disconnect und ein gescheitertes Koppeln lassen ihn stehen. Der Abgleich
// erzeugt nie einen neuen, er meldet nur, dass neu gekoppelt werden muss.

var errSignKeyMissing = errors.New(deliver.MsgSignKeyMissing)

// Für Tests austauschbar, um Fehler beim Ablegen nachzustellen.
var saveSignKey = secret.SaveSignKey

// signKeyResult ist das Ergebnis von ensureSignKey.
type signKeyResult struct {
	Keys     client.SignKeys    // für Pair: aktueller öffentlicher Schlüssel und frühere
	Priv     ed25519.PrivateKey // privater Schlüssel, für das Archiv beim ersten Abgleich
	Created  bool               // der Schlüssel ist eben neu entstanden
	Replaced bool               // dabei hat er einen früheren ersetzt, dessen öffentlicher Teil jetzt in Previous steht
}

// ensureSignKey lädt den privaten Signaturschlüssel dieses Rechners oder erzeugt einen neuen, wenn er fehlt oder
// unbrauchbar ist. Der private Schlüssel wird abgelegt, bevor der öffentliche Teil in state.json steht; beides
// geschieht vor dem Koppeln. Ein Lesefehler der Ablage (etwa Zeitgrenze des Schlüsselspeichers) erzeugt keinen neuen:
// Der vorhandene Schlüssel bliebe sonst still ersetzt.
func ensureSignKey(dataDir string) (signKeyResult, error) {
	st, err := state.Load(dataDir)
	if err != nil {
		return signKeyResult{}, fmt.Errorf("Merkzettel nicht lesbar: %w", err)
	}
	var res signKeyResult
	priv, kind, err := secret.LoadSignKey(dataDir, st.SignKeyStore)
	switch {
	case err == nil:
	case errors.Is(err, secret.ErrSignKeyNotFound), errors.Is(err, secret.ErrSignKeyBad):
		if _, priv, err = ed25519.GenerateKey(rand.Reader); err != nil {
			return signKeyResult{}, fmt.Errorf("Signaturschlüssel erzeugen: %w", err)
		}
		if kind, err = saveSignKey(dataDir, priv); err != nil {
			return signKeyResult{}, err
		}
		res.Created = true
	default:
		return signKeyResult{}, fmt.Errorf("Signaturschlüssel nicht lesbar: %w", err)
	}
	pub := protocol.EncodeSignPublicKey(priv.Public().(ed25519.PublicKey))
	res.Replaced = st.SignPublicKey != "" && st.SignPublicKey != pub
	if st.SignPublicKey != pub || st.SignKeyStore != kind {
		st.SetSignPublicKey(pub, protocol.MaxSignKeysPrevious)
		st.SignKeyStore = kind
		if err := st.Save(dataDir); err != nil {
			return signKeyResult{}, fmt.Errorf("Signaturschlüssel nicht gemerkt: %w", err)
		}
	}
	res.Keys = client.SignKeys{Public: st.SignPublicKey, Previous: st.SignPublicKeysPrevious, Priv: priv}
	res.Priv = priv
	return res, nil
}

// loadSignKey liest den privaten Signaturschlüssel für den Abgleich. Er muss zum öffentlichen Teil in state.json
// passen. Fehlt er oder passt er nicht, ist der Fehlertext deliver.MsgSignKeyMissing; neu erzeugt wird hier nie.
// Fehlertexte enthalten den Schlüssel nie.
func loadSignKey(dataDir string, st *state.State) (ed25519.PrivateKey, error) {
	priv, _, err := secret.LoadSignKey(dataDir, st.SignKeyStore)
	switch {
	case errors.Is(err, secret.ErrSignKeyNotFound), errors.Is(err, secret.ErrSignKeyBad):
		return nil, errSignKeyMissing
	case err != nil:
		return nil, fmt.Errorf("Signaturschlüssel nicht lesbar: %w", err)
	}
	if st.SignPublicKey != protocol.EncodeSignPublicKey(priv.Public().(ed25519.PublicKey)) {
		return nil, errSignKeyMissing
	}
	return priv, nil
}

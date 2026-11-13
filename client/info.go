package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/tokenlore/tokenlore/protocol"
)

// ServerInfo ist die geprüfte Antwort von GET /api/plugin/info (protocol/doc.md, „Server-Kennung“).
type ServerInfo struct {
	// ServerID ist die Kennung des Servers, 64 Hex-Zeichen, klein; sie bleibt bei einem Umzug gleich.
	ServerID string
	// MinPluginVersion ist die älteste Plugin-Version, die der Server annimmt; leer, wenn er keine gültige nennt.
	MinPluginVersion string
	// Features sind die Merkmale, die der Server über die Grundform des Protokolls hinaus kennt; leer, wenn er keine
	// oder keine lesbare Liste nennt.
	Features []string
}

// FeaturePredecessor: Der Server nimmt beim Koppeln predecessor_source und predecessor_answer an und kann nach dem
// Vorgänger fragen. Einem Server ohne dieses Merkmal sendet das Plugin die beiden Felder nie (er lehnte sie mit 400 ab).
const FeaturePredecessor = "predecessor"

// Has meldet, ob der Server das Merkmal f nennt.
func (i ServerInfo) Has(f string) bool { return slices.Contains(i.Features, f) }

// Info holt Server-Kennung und Mindestversion. Ohne gültige server_id ist das ein Fehler: Ohne sie lässt sich
// device_source nicht bilden, und das Plugin koppelt nicht.
func (c *Client) Info(ctx context.Context) (ServerInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.String()+"/api/plugin/info", nil)
	if err != nil {
		return ServerInfo{}, errors.New("Anfrage ungültig")
	}
	req.Header.Set("User-Agent", userAgent())
	r, err := c.hc.Do(req)
	if err != nil {
		return ServerInfo{}, &TransportError{Err: err, Pinned: c.pinned}
	}
	defer r.Body.Close()
	body, tooBig, err := readCapped(r.Body)
	if err != nil {
		return ServerInfo{}, &TransportError{Err: err, Pinned: c.pinned}
	}
	if r.StatusCode != http.StatusOK {
		return ServerInfo{}, mapStatus(r.StatusCode, r.Header, body, tooBig, "")
	}
	if tooBig {
		return ServerInfo{}, errors.New("Antwort des Servers zu groß")
	}
	var v struct {
		ServerID string          `json:"server_id"`
		Min      string          `json:"min_plugin_version"`
		Features json.RawMessage `json:"features"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ServerInfo{}, errors.New("Server-Kennung nicht lesbar")
	}
	if !protocol.ServerIDPattern.MatchString(v.ServerID) {
		return ServerInfo{}, errors.New("Server-Kennung ungültig")
	}
	info := ServerInfo{ServerID: v.ServerID}
	if protocol.MinVersionPattern.MatchString(v.Min) {
		info.MinPluginVersion = v.Min
	}
	// Eine Liste mit anderem als Texten gilt als leer: Ein Merkmal zu viel könnte Felder auslösen, die der Server ablehnt.
	var features []string
	if json.Unmarshal(v.Features, &features) == nil {
		info.Features = features
	}
	return info, nil
}

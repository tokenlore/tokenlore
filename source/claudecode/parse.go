// Paket claudecode liest die Sitzungsdateien von Claude Code (~/.claude/projects/…/*.jsonl).
package claudecode

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
)

// rawLine enthält nur die Felder, die tokenlore braucht. Inhalte (text, input, thinking) haben hier kein Feld und
// werden vom JSON-Decoder verworfen. Felder der Inhaltszeile, die Claude Code erst neuerdings schreibt, sind
// looseString/looseBool: Ein anderer Typ macht sie leer, nicht die ganze Zeile unlesbar.
type rawLine struct {
	Type             string      `json:"type"`
	SessionID        string      `json:"sessionId"`
	RequestID        string      `json:"requestId"`
	Timestamp        string      `json:"timestamp"`
	Version          string      `json:"version"`
	OwnerAccountUUID string      `json:"ownerAccountUuid"`
	UUID             looseString `json:"uuid"`
	ParentUUID       looseString `json:"parentUuid"`
	RequestedModel   looseString `json:"requestedModel"`
	Effort           looseString `json:"effort"`
	Entrypoint       looseString `json:"entrypoint"`
	IsSidechain      looseBool   `json:"isSidechain"`
	Message          *struct {
		ID         string          `json:"id"`
		Model      string          `json:"model"`
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"` // nur kurz zum Lesen der Werkzeugnamen
		Usage      json.RawMessage `json:"usage"`
	} `json:"message"`
}

// looseString nimmt nur einen JSON-Text an; jeder andere Wert ergibt "".
type looseString string

func (s *looseString) UnmarshalJSON(b []byte) error {
	var v string
	if json.Unmarshal(b, &v) == nil {
		*s = looseString(v)
	}
	return nil
}

// looseBool nimmt nur true an; jeder andere Wert ergibt false.
type looseBool bool

func (v *looseBool) UnmarshalJSON(b []byte) error {
	*v = looseBool(bytes.Equal(b, []byte("true")))
	return nil
}

type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type line struct {
	Type, SessionID, RequestID, Timestamp, Version, OwnerAccountUUID string
	UUID, ParentUUID, RequestedModel, Effort, Entrypoint             string
	Sidechain                                                        bool
	MessageID, Model, StopReason                                     string
	// Usage ist der Block message.usage, Zahlen als json.Number; nil, wenn die Zeile keinen hat.
	Usage     map[string]any
	ToolNames []string
	// NewFields sind die Namen unbekannter Schlüssel einer Anfragezeile (oberste Ebene und message.…), nie Werte.
	NewFields []string
}

// Schlüssel einer Anfragezeile (type assistant mit message.usage). Bekannt sind die Felder, die tokenlore liest,
// und die, die es kennt und absichtlich nicht übernimmt. Gesperrt sind Felder mit Inhalt, Pfaden oder Projekt;
// sie kommen nie in eine Inhaltszeile. Jeder andere Schlüssel gilt als neu und wird nur mit Namen gemeldet.
var (
	knownTop = setOf(
		// gelesen
		"type", "sessionId", "requestId", "timestamp", "version", "ownerAccountUuid", "uuid", "parentUuid",
		"requestedModel", "effort", "entrypoint", "isSidechain", "message",
		// bekannt, nicht übernommen
		"userType", "perTurnEffort", "advisorModel", "serverClassifierRequest", "requestRef", "apiBlockIndex",
		"agentId", "attributionAgent", "attributionPlugin", "attributionMcpTool", "attributionMcpServer",
		"attributionSkill", "thinkingDurationMs", "thinkingDisplay", "quotaLimits", "isApiErrorMessage", "error",
		"apiError", "apiErrorStatus", "apiErrorParams", "slug", "isMeta",
	)
	blockedTop = setOf("content", "wireToolInputs", "wireIngestContext", "cwd", "gitBranch", "toolUseResult",
		"attachment", "snapshot", "lastPrompt", "customTitle", "title", "summary", "path", "params",
		"serverClassifierContext", "hookAdditionalContext")
	knownMessage = setOf(
		// gelesen; aus content nur die Namen der tool_use-Blöcke
		"id", "model", "stop_reason", "usage",
		// bekannt, nicht übernommen
		"type", "role", "stop_details", "diagnostics", "context_management", "container", "input_transformations",
		"output_config",
	)
	blockedMessage = setOf("content", "stop_sequence")
)

func setOf(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// fieldNamePattern: nur solche Namen werden gemeldet; andere (Leerzeichen, Sonderzeichen) übergeht das Plugin.
var fieldNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// maxFieldName ist die Länge, auf die ein gemeldeter Name gekürzt wird.
const maxFieldName = 64

func parseLine(b []byte) (line, error) {
	var r rawLine
	if err := json.Unmarshal(b, &r); err != nil {
		return line{}, err
	}
	l := line{Type: r.Type, SessionID: r.SessionID, RequestID: r.RequestID, Timestamp: r.Timestamp,
		Version: r.Version, OwnerAccountUUID: r.OwnerAccountUUID, UUID: string(r.UUID),
		ParentUUID: string(r.ParentUUID), RequestedModel: string(r.RequestedModel), Effort: string(r.Effort),
		Entrypoint: string(r.Entrypoint), Sidechain: bool(r.IsSidechain)}
	if r.Message == nil {
		return l, nil
	}
	l.MessageID, l.Model, l.StopReason = r.Message.ID, r.Message.Model, r.Message.StopReason
	var blocks []contentBlock
	if json.Unmarshal(r.Message.Content, &blocks) == nil {
		for _, c := range blocks {
			if c.Type == "tool_use" && c.Name != "" {
				l.ToolNames = append(l.ToolNames, c.Name)
			}
		}
	}
	if len(r.Message.Usage) > 0 {
		d := json.NewDecoder(bytes.NewReader(r.Message.Usage))
		d.UseNumber()
		var u any
		if d.Decode(&u) == nil {
			l.Usage, _ = u.(map[string]any)
		}
	}
	if l.Type == "assistant" && l.Usage != nil {
		l.NewFields = newFields(b)
	}
	return l, nil
}

// newFields liefert die Namen der Schlüssel einer Anfragezeile, die weder bekannt noch gesperrt sind, sortiert.
// Schlüssel von message stehen als „message.<Name>“ da.
func newFields(b []byte) []string {
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil {
		return nil
	}
	var out []string
	add := func(prefix, k string) {
		if !fieldNamePattern.MatchString(k) {
			return
		}
		if len(k) > maxFieldName {
			k = k[:maxFieldName]
		}
		out = append(out, prefix+k)
	}
	for k := range top {
		if !knownTop[k] && !blockedTop[k] {
			add("", k)
		}
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(top["message"], &msg) == nil {
		for k := range msg {
			if !knownMessage[k] && !blockedMessage[k] {
				add("message.", k)
			}
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// invalidTool ersetzt Werkzeugnamen, die nicht zu protocol.ToolPattern passen.
const invalidTool = "_invalid"

// reqAgg sammelt die Zeilen eines (message.id, requestId)-Paars.
type reqAgg struct {
	first line
	// Optionale Angaben: der erste nicht leere Wert über alle Zeilen der Anfrage.
	version, requestedModel, effort, entrypoint string
	sidechain                                   bool
	usage                                       map[string]any // usage der Zeile mit dem größten output_tokens
	usageOut                                    int64
	tools                                       map[string]int
	stop                                        string
	n                                           int
}

func (a *reqAgg) add(l line) {
	if a.n == 0 {
		a.first = l
	}
	a.n++
	if l.StopReason != "" {
		a.stop = l.StopReason
	}
	a.sidechain = a.sidechain || l.Sidechain
	for dst, v := range map[*string]string{&a.version: l.Version, &a.requestedModel: l.RequestedModel,
		&a.effort: l.Effort, &a.entrypoint: l.Entrypoint} {
		if *dst == "" {
			*dst = v
		}
	}
	for _, t := range l.ToolNames {
		if a.tools == nil {
			a.tools = map[string]int{}
		}
		// Kaputte Namen (z. B. von Modellen erfundene) können Inhalt enthalten: nur zählen, nicht senden.
		if !protocol.ToolPattern.MatchString(t) {
			t = invalidTool
		}
		a.tools[t]++
	}
	// Frühe Zeilen einer Anfrage sind Streaming-Zwischenstände (Cache-Werte null, output_tokens zu klein); die Zeile
	// mit dem größten output_tokens trägt die Endwerte, bei Gleichstand die letzte.
	if l.Usage != nil {
		out := outputTokens(l.Usage)
		if a.usage == nil || out >= a.usageOut {
			a.usage, a.usageOut = l.Usage, out
		}
	}
}

// outputTokens liest output_tokens; fehlt der Wert oder ist er keine ganze Zahl, gilt 0.
func outputTokens(u map[string]any) int64 {
	n, ok := u["output_tokens"].(json.Number)
	if !ok {
		return 0
	}
	v, err := n.Int64()
	if err != nil {
		return 0
	}
	return v
}

// valid gibt s zurück, wenn es zum Muster passt, sonst "": Ein ungültiger optionaler Wert fehlt in der Zeile.
func valid(p *regexp.Regexp, s string) string {
	if s == "" || !p.MatchString(s) {
		return ""
	}
	return s
}

// toLine baut die Inhaltszeile mit der festen Feldliste. ok ist false für "<synthetic>", leeres oder ungültiges
// Modell, fehlendes usage und nicht lesbare Zeitstempel. SessionID und AccountID setzt der Aufrufer, wenn nötig.
func (a *reqAgg) toLine(loc *time.Location, subagent bool) (protocol.RequestLine, bool) {
	f := a.first
	if f.Model == "" || f.Model == "<synthetic>" || !protocol.ModelPattern.MatchString(f.Model) || a.usage == nil {
		return protocol.RequestLine{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, f.Timestamp)
	if err != nil {
		return protocol.RequestLine{}, false
	}
	_, off := ts.In(loc).Zone()
	return protocol.RequestLine{
		Kind: protocol.KindRequest, RequestID: f.RequestID, MessageID: f.MessageID, SessionID: f.SessionID,
		UUID: valid(protocol.IDPattern, f.UUID), ParentUUID: valid(protocol.IDPattern, f.ParentUUID),
		TS: ts.UTC(), UTCOffsetMin: off / 60, Model: f.Model,
		RequestedModel: valid(protocol.ModelPattern, a.requestedModel),
		Effort:         valid(protocol.StopPattern, a.effort), Entrypoint: valid(protocol.StopPattern, a.entrypoint),
		CCVersion: valid(protocol.VersionPattern, a.version), StopReason: valid(protocol.StopPattern, a.stop),
		Subagent: subagent || a.sidechain, Tools: a.tools,
		Usage: protocol.SanitizeUsage(a.usage),
	}, true
}

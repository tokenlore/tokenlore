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
	// Angaben je Anfrage (protocol/doc.md, „Inhaltszeile: Anfrage“) und je API-Fehler („Inhaltszeile: Fehler“).
	PromptCacheRequested looseString     `json:"promptCacheRequested"`
	ToolInputStreamMs    json.RawMessage `json:"toolInputStreamMs"`
	ThinkingDurationMs   json.RawMessage `json:"thinkingDurationMs"`
	AttributionPlugin    looseString     `json:"attributionPlugin"`
	AttributionAgent     looseString     `json:"attributionAgent"`
	IsAPIErrorMessage    looseBool       `json:"isApiErrorMessage"`
	APIErrorStatus       json.RawMessage `json:"apiErrorStatus"`
	Error                looseString     `json:"error"`
	QuotaLimits          json.RawMessage `json:"quotaLimits"`
	Message              *struct {
		ID          string          `json:"id"`
		Model       string          `json:"model"`
		StopReason  string          `json:"stop_reason"`
		Content     json.RawMessage `json:"content"` // nur kurz zum Lesen der Werkzeugnamen
		Usage       json.RawMessage `json:"usage"`
		Diagnostics json.RawMessage `json:"diagnostics"`
	} `json:"message"`
}

// quotaLimits ist der Block quotaLimits einer Fehlerzeile; nur feste Werte und Zahlen.
type quotaLimits struct {
	RateLimitType  looseString     `json:"rateLimitType"`
	Status         looseString     `json:"status"`
	OverageStatus  looseString     `json:"overageStatus"`
	IsUsingOverage json.RawMessage `json:"isUsingOverage"`
	ResetsAt       json.RawMessage `json:"resetsAt"`
}

// intIn ist die ganze Zahl in b, wenn sie in lo – hi liegt, sonst nil: Ein fehlender oder ungültiger Wert fehlt.
func intIn(b json.RawMessage, lo, hi int64) *int64 {
	var n json.Number
	if json.Unmarshal(b, &n) != nil {
		return nil
	}
	v, err := n.Int64()
	if err != nil || v < lo || v > hi {
		return nil
	}
	return &v
}

// boolOf ist true oder false aus b, sonst nil.
func boolOf(b json.RawMessage) *bool {
	var v bool
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	return &v
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
	Details   details
	// APIError: eine Fehlerzeile (isApiErrorMessage, Modell "<synthetic>"); ErrInfo trägt dann ihre optionalen
	// Angaben, schon in ihren Grenzen.
	APIError bool
	ErrInfo  protocol.ErrorLine
}

// details sind die Angaben einer Anfrage, die das Merkmal request_details des Servers brauchen; schon in ihren
// Grenzen, ein ungültiger Wert ist leer oder nil.
type details struct {
	promptCache, plugin, agent, cacheMiss string
	toolStream, thinking                  *int64
}

// or ergänzt jedes in d fehlende Feld aus o.
func (d details) or(o details) details {
	for dst, v := range map[*string]string{&d.promptCache: o.promptCache, &d.plugin: o.plugin, &d.agent: o.agent,
		&d.cacheMiss: o.cacheMiss} {
		if *dst == "" {
			*dst = v
		}
	}
	if d.toolStream == nil {
		d.toolStream = o.toolStream
	}
	if d.thinking == nil {
		d.thinking = o.thinking
	}
	return d
}

// Schlüssel einer Anfragezeile (type assistant mit message.usage). Bekannt sind die Felder, die tokenlore liest,
// und die, die es kennt und absichtlich nicht übernimmt. Gesperrt sind Felder mit Inhalt, Pfaden oder Projekt;
// sie kommen nie in eine Inhaltszeile. Jeder andere Schlüssel gilt als neu und wird nur mit Namen gemeldet.
var (
	knownTop = setOf(
		// gelesen; aus quotaLimits nur feste Werte und Zahlen
		"type", "sessionId", "requestId", "timestamp", "version", "ownerAccountUuid", "uuid", "parentUuid",
		"requestedModel", "effort", "entrypoint", "isSidechain", "message", "promptCacheRequested",
		"toolInputStreamMs", "thinkingDurationMs", "attributionPlugin", "attributionAgent", "isApiErrorMessage",
		"apiErrorStatus", "error", "quotaLimits",
		// bekannt, nicht übernommen
		"userType", "perTurnEffort", "advisorModel", "serverClassifierRequest", "requestRef", "apiBlockIndex",
		"agentId", "attributionMcpTool", "attributionMcpServer", "attributionSkill", "thinkingDisplay", "apiError",
		"apiErrorParams", "failedResponseHadNoToolCall", "slug", "isMeta",
	)
	blockedTop = setOf("content", "wireToolInputs", "wireIngestContext", "cwd", "gitBranch", "toolUseResult",
		"attachment", "snapshot", "lastPrompt", "customTitle", "title", "summary", "path", "params",
		"serverClassifierContext", "hookAdditionalContext")
	knownMessage = setOf(
		// gelesen; aus content nur die Namen der tool_use-Blöcke, aus diagnostics nur cache_miss_reason.type
		"id", "model", "stop_reason", "usage", "diagnostics",
		// bekannt, nicht übernommen
		"type", "role", "stop_details", "context_management", "container", "input_transformations", "output_config",
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
		Entrypoint: string(r.Entrypoint), Sidechain: bool(r.IsSidechain),
		Details: details{promptCache: valid(protocol.StopPattern, string(r.PromptCacheRequested)),
			plugin:     valid(protocol.AttributionPattern, string(r.AttributionPlugin)),
			agent:      valid(protocol.AttributionPattern, string(r.AttributionAgent)),
			toolStream: intIn(r.ToolInputStreamMs, 0, protocol.MaxDurationMs),
			thinking:   intIn(r.ThinkingDurationMs, 0, protocol.MaxDurationMs)}}
	if r.Message == nil {
		return l, nil
	}
	l.MessageID, l.Model, l.StopReason = r.Message.ID, r.Message.Model, r.Message.StopReason
	var diag struct {
		CacheMissReason struct {
			Type looseString `json:"type"`
		} `json:"cache_miss_reason"`
	}
	_ = json.Unmarshal(r.Message.Diagnostics, &diag) // kein Objekt: kein Grund
	l.Details.cacheMiss = valid(protocol.StopPattern, string(diag.CacheMissReason.Type))
	if r.IsAPIErrorMessage && l.Model == "<synthetic>" {
		var q quotaLimits
		_ = json.Unmarshal(r.QuotaLimits, &q) // kein Objekt: keine Angaben
		l.APIError = true
		l.ErrInfo = protocol.ErrorLine{
			APIErrorStatus: intIn(r.APIErrorStatus, protocol.MinAPIErrorStatus, protocol.MaxAPIErrorStatus),
			Error:          valid(protocol.StopPattern, string(r.Error)),
			RateLimitType:  valid(protocol.StopPattern, string(q.RateLimitType)),
			LimitStatus:    valid(protocol.StopPattern, string(q.Status)),
			OverageStatus:  valid(protocol.StopPattern, string(q.OverageStatus)),
			IsUsingOverage: boolOf(q.IsUsingOverage), ResetsAt: intIn(q.ResetsAt, 0, protocol.MaxResetsAt),
		}
	}
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
	// Angaben der usage-Zeile und, je Feld, der letzten Zeile, die es hat (fehlt es in der usage-Zeile).
	usageDetails, lastDetails details
	tools                     map[string]int
	stop                      string
	n                         int
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
			a.usage, a.usageOut, a.usageDetails = l.Usage, out, l.Details
		}
	}
	a.lastDetails = l.Details.or(a.lastDetails)
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
	d := a.usageDetails.or(a.lastDetails)
	return protocol.RequestLine{
		Kind: protocol.KindRequest, RequestID: f.RequestID, MessageID: f.MessageID, SessionID: f.SessionID,
		UUID: valid(protocol.IDPattern, f.UUID), ParentUUID: valid(protocol.IDPattern, f.ParentUUID),
		TS: ts.UTC(), UTCOffsetMin: off / 60, Model: f.Model,
		RequestedModel: valid(protocol.ModelPattern, a.requestedModel),
		Effort:         valid(protocol.StopPattern, a.effort), Entrypoint: valid(protocol.StopPattern, a.entrypoint),
		CCVersion: valid(protocol.VersionPattern, a.version), StopReason: valid(protocol.StopPattern, a.stop),
		Subagent: subagent || a.sidechain, Tools: a.tools,
		PromptCacheRequested: d.promptCache, ToolInputStreamMs: d.toolStream, ThinkingDurationMs: d.thinking,
		AttributionPlugin: d.plugin, AttributionAgent: d.agent, CacheMissReason: d.cacheMiss,
		Usage: protocol.SanitizeUsage(a.usage),
	}, true
}

// toErrorLine baut die Inhaltszeile eines API-Fehlers aus der ersten Zeile; ok ist false ohne Fehlerzeile oder mit
// nicht lesbarem Zeitstempel. SessionID und AccountID setzt der Aufrufer, wenn nötig.
func (a *reqAgg) toErrorLine(loc *time.Location, subagent bool) (protocol.ErrorLine, bool) {
	f := a.first
	ts, err := time.Parse(time.RFC3339Nano, f.Timestamp)
	if !f.APIError || err != nil {
		return protocol.ErrorLine{}, false
	}
	_, off := ts.In(loc).Zone()
	e := f.ErrInfo
	e.Kind, e.RequestID, e.MessageID, e.SessionID = protocol.KindError, f.RequestID, f.MessageID, f.SessionID
	e.UUID, e.ParentUUID = valid(protocol.IDPattern, f.UUID), valid(protocol.IDPattern, f.ParentUUID)
	e.TS, e.UTCOffsetMin = ts.UTC(), off/60
	e.Entrypoint, e.CCVersion = valid(protocol.StopPattern, f.Entrypoint), valid(protocol.VersionPattern, f.Version)
	e.Subagent = subagent || f.Sidechain
	return e, true
}

// Paket claudecode liest die Sitzungsdateien von Claude Code (~/.claude/projects/…/*.jsonl).
package claudecode

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/tokenlore/tokenlore/protocol"
)

// rawLine enthält nur die Felder, die tokenlore braucht. Inhalte (text, input, thinking)
// haben hier kein Feld und werden vom JSON-Decoder verworfen.
type rawLine struct {
	Type             string `json:"type"`
	SessionID        string `json:"sessionId"`
	RequestID        string `json:"requestId"`
	Timestamp        string `json:"timestamp"`
	Version          string `json:"version"`
	OwnerAccountUUID string `json:"ownerAccountUuid"`
	Message          *struct {
		ID         string                     `json:"id"`
		Model      string                     `json:"model"`
		StopReason string                     `json:"stop_reason"`
		Content    json.RawMessage            `json:"content"` // nur kurz zum Lesen der Werkzeugnamen
		Usage      map[string]json.RawMessage `json:"usage"`
	} `json:"message"`
}

type contentBlock struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type usage struct {
	Input, Output, CacheCreate, CacheRead, Cache1h, Cache5m, Thinking int64
	WebSearch, WebFetch                                               int
	Extra                                                             map[string]any
}

type line struct {
	Type, SessionID, RequestID, Timestamp, Version, OwnerAccountUUID string
	MessageID, Model, StopReason                                     string
	Usage                                                            *usage
	ToolNames                                                        []string
}

// knownUsage sind die Felder aus message.usage, die tokenlore selbst auswertet.
var knownUsage = map[string]bool{
	"input_tokens": true, "output_tokens": true, "cache_creation_input_tokens": true,
	"cache_read_input_tokens": true, "output_tokens_details": true, "server_tool_use": true,
	"cache_creation": true,
}

func parseLine(b []byte) (line, error) {
	var r rawLine
	if err := json.Unmarshal(b, &r); err != nil {
		return line{}, err
	}
	l := line{Type: r.Type, SessionID: r.SessionID, RequestID: r.RequestID, Timestamp: r.Timestamp,
		Version: r.Version, OwnerAccountUUID: r.OwnerAccountUUID}
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
	if r.Message.Usage != nil {
		l.Usage = parseUsage(r.Message.Usage)
	}
	return l, nil
}

func parseUsage(m map[string]json.RawMessage) *usage {
	u := &usage{}
	num := func(k string) int64 {
		var v int64
		_ = json.Unmarshal(m[k], &v)
		return v
	}
	u.Input, u.Output = num("input_tokens"), num("output_tokens")
	u.CacheCreate, u.CacheRead = num("cache_creation_input_tokens"), num("cache_read_input_tokens")
	var otd struct {
		Thinking int64 `json:"thinking_tokens"`
	}
	_ = json.Unmarshal(m["output_tokens_details"], &otd)
	u.Thinking = otd.Thinking
	var stu struct {
		Search int `json:"web_search_requests"`
		Fetch  int `json:"web_fetch_requests"`
	}
	_ = json.Unmarshal(m["server_tool_use"], &stu)
	u.WebSearch, u.WebFetch = stu.Search, stu.Fetch
	var cc struct {
		H1 int64 `json:"ephemeral_1h_input_tokens"`
		M5 int64 `json:"ephemeral_5m_input_tokens"`
	}
	_ = json.Unmarshal(m["cache_creation"], &cc)
	u.Cache1h, u.Cache5m = cc.H1, cc.M5

	// Unbekannte flache Felder als Extra: alphabetisch, höchstens MaxExtraFields.
	var keys []string
	for k := range m {
		if !knownUsage[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(u.Extra) >= protocol.MaxExtraFields {
			break
		}
		var v any
		if json.Unmarshal(m[k], &v) != nil {
			continue
		}
		switch x := v.(type) {
		case float64:
		case string:
			if r := []rune(x); len(r) > protocol.MaxExtraString {
				v = string(r[:protocol.MaxExtraString])
			}
		default:
			continue
		}
		if u.Extra == nil {
			u.Extra = map[string]any{}
		}
		u.Extra[k] = v
	}
	return u
}

// invalidTool ersetzt Werkzeugnamen, die nicht zu protocol.ToolPattern passen.
const invalidTool = "_invalid"

// reqAgg sammelt die Zeilen eines (message.id, requestId)-Paars.
type reqAgg struct {
	first line
	u     usage
	tools map[string]int
	stop  string
	n     int
}

func (a *reqAgg) add(l line) {
	if a.n == 0 {
		a.first = l
	}
	a.n++
	if l.StopReason != "" {
		a.stop = l.StopReason
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
	if u := l.Usage; u != nil {
		a.u.Input = max(a.u.Input, u.Input)
		a.u.Output = max(a.u.Output, u.Output)
		a.u.CacheCreate = max(a.u.CacheCreate, u.CacheCreate)
		a.u.CacheRead = max(a.u.CacheRead, u.CacheRead)
		a.u.Cache1h = max(a.u.Cache1h, u.Cache1h)
		a.u.Cache5m = max(a.u.Cache5m, u.Cache5m)
		a.u.Thinking = max(a.u.Thinking, u.Thinking)
		a.u.WebSearch = max(a.u.WebSearch, u.WebSearch)
		a.u.WebFetch = max(a.u.WebFetch, u.WebFetch)
		if a.u.Extra == nil {
			a.u.Extra = u.Extra
		}
	}
}

// toRequest baut die Anfrage. ok ist false für "<synthetic>", leeres oder ungültiges Modell
// und nicht lesbare Zeitstempel.
func (a *reqAgg) toRequest(loc *time.Location, subagent bool) (protocol.Request, bool) {
	f := a.first
	if f.Model == "" || f.Model == "<synthetic>" || !protocol.ModelPattern.MatchString(f.Model) {
		return protocol.Request{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, f.Timestamp)
	if err != nil {
		return protocol.Request{}, false
	}
	_, off := ts.In(loc).Zone()
	return protocol.Request{
		RequestID: f.RequestID, MessageID: f.MessageID, SessionID: f.SessionID,
		TS: ts.UTC(), UTCOffsetMin: off / 60, Model: f.Model,
		Input: a.u.Input, Output: a.u.Output, CacheCreate: a.u.CacheCreate, CacheRead: a.u.CacheRead,
		Cache1h: a.u.Cache1h, Cache5m: a.u.Cache5m, Thinking: a.u.Thinking,
		WebSearch: a.u.WebSearch, WebFetch: a.u.WebFetch, StopReason: a.stop,
		Subagent: subagent, Tools: a.tools, Extra: a.u.Extra,
	}, true
}

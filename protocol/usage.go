package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"strconv"
)

// Counts sind die Zählwerte einer Anfrage, gelesen aus usage (protocol/doc.md, Tabelle „Zählwerte“).
type Counts struct {
	Input, Output, CacheCreate, CacheRead, Cache1h, Cache5m, Thinking, WebSearch, WebFetch int64
}

// countPaths ordnet jedem Zählwert seinen Pfad in usage und seine Obergrenze zu.
var countPaths = []struct {
	path []string
	max  int64
	dst  func(*Counts) *int64
}{
	{[]string{"input_tokens"}, MaxTokensPerField, func(c *Counts) *int64 { return &c.Input }},
	{[]string{"output_tokens"}, MaxTokensPerField, func(c *Counts) *int64 { return &c.Output }},
	{[]string{"cache_creation_input_tokens"}, MaxTokensPerField, func(c *Counts) *int64 { return &c.CacheCreate }},
	{[]string{"cache_read_input_tokens"}, MaxTokensPerField, func(c *Counts) *int64 { return &c.CacheRead }},
	{[]string{"cache_creation", "ephemeral_1h_input_tokens"}, MaxTokensPerField, func(c *Counts) *int64 { return &c.Cache1h }},
	{[]string{"cache_creation", "ephemeral_5m_input_tokens"}, MaxTokensPerField, func(c *Counts) *int64 { return &c.Cache5m }},
	{[]string{"output_tokens_details", "thinking_tokens"}, MaxTokensPerField, func(c *Counts) *int64 { return &c.Thinking }},
	{[]string{"server_tool_use", "web_search_requests"}, MaxCountPerField, func(c *Counts) *int64 { return &c.WebSearch }},
	{[]string{"server_tool_use", "web_fetch_requests"}, MaxCountPerField, func(c *Counts) *int64 { return &c.WebFetch }},
}

// UsageCounts liest die Zählwerte aus usage. Fehlt ein Wert, gilt 0. Ein Wert, der da ist, aber keine ganze Zahl
// in seiner Grenze ist (auch null), ist ein Fehler, wie beim Server.
func UsageCounts(u map[string]any) (Counts, error) {
	var c Counts
	for _, p := range countPaths {
		v, ok := pathValue(u, p.path)
		if !ok {
			continue
		}
		n, ok := wholeNumber(v)
		if !ok || n < 0 || n > p.max {
			return Counts{}, errors.New("usage: Zählwert keine ganze Zahl im erlaubten Bereich")
		}
		*p.dst(&c) = n
	}
	return c, nil
}

// pathValue liefert den Wert unter path, wenn jeder Schritt ein Objekt mit diesem Schlüssel ist (auch mit Wert nil).
func pathValue(u map[string]any, path []string) (any, bool) {
	var node any = u
	for _, k := range path {
		m, ok := node.(map[string]any)
		if !ok {
			return nil, false
		}
		if node, ok = m[k]; !ok {
			return nil, false
		}
	}
	return node, true
}

// wholeNumber erkennt eine ganze Zahl so, wie sie in der Zeile steht: json.Number nur ohne Bruch und Exponent
// (der Server liest „1.0“ oder „1e3“ als Kommazahl), float64 nur ganzzahlig.
func wholeNumber(v any) (int64, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := strconv.ParseInt(string(x), 10, 64)
		return n, err == nil
	case int:
		return int64(x), true
	case int64:
		return x, true
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > 1e15 {
			return 0, false
		}
		return int64(x), true
	}
	return 0, false
}

// SanitizeUsage bringt einen dekodierten usage-Block in die Grenzen von protocol/doc.md, damit eine Zeile im Archiv
// sie nie verletzt: Texte über MaxExtraString Zeichen werden gekürzt; Schlüssel mit anderem Muster, Werte jenseits
// der vierten Ebene, über UsageMaxValues hinaus, Listeneinträge über UsageMaxList hinaus, Texte mit Steuer- und
// Formatzeichen, nicht endliche Zahlen und andere Typen fallen weg. Zählwerte mit null fallen ebenfalls weg (fehlt
// ein Wert, gilt 0; null nimmt der Server dort nicht an). Schlüssel werden alphabetisch durchlaufen, in der
// Reihenfolge, in der MarshalLine sie schreibt. u bleibt unverändert.
func SanitizeUsage(u map[string]any) map[string]any {
	n := 0
	out := sanitizeObject(u, 1, &n)
	for _, p := range countPaths {
		parent, ok := pathValue(out, p.path[:len(p.path)-1])
		if m, isMap := parent.(map[string]any); ok && isMap {
			if v, has := m[p.path[len(p.path)-1]]; has && v == nil {
				delete(m, p.path[len(p.path)-1])
			}
		}
	}
	return out
}

// sanitizeObject bereinigt ein Objekt der Ebene depth; n zählt die behaltenen Werte unterhalb von usage.
func sanitizeObject(m map[string]any, depth int, n *int) map[string]any {
	out := make(map[string]any, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !ExtraKey.MatchString(k) {
			continue
		}
		if v, ok := sanitizeValue(m[k], depth, n); ok {
			out[k] = v
		}
	}
	return out
}

// sanitizeValue bereinigt einen Wert in einem Behälter der Ebene depth. Ein Behälter zählt vor seinem Inhalt,
// wie beim Server.
func sanitizeValue(v any, depth int, n *int) (any, bool) {
	if *n >= UsageMaxValues {
		return nil, false
	}
	switch x := v.(type) {
	case map[string]any:
		if depth+1 > UsageMaxDepth {
			return nil, false
		}
		*n++
		return sanitizeObject(x, depth+1, n), true
	case []any:
		if depth+1 > UsageMaxDepth {
			return nil, false
		}
		*n++
		if len(x) > UsageMaxList {
			x = x[:UsageMaxList]
		}
		out := make([]any, 0, len(x))
		for _, e := range x {
			if e2, ok := sanitizeValue(e, depth+1, n); ok {
				out = append(out, e2)
			}
		}
		return out, true
	case string:
		if r := []rune(x); len(r) > MaxExtraString {
			x = string(r[:MaxExtraString])
		}
		if hasControl(x) || HasSpoofRune(x) {
			return nil, false
		}
		v = x
	case json.Number, float64:
		if !finiteNumber(x) {
			return nil, false
		}
	case bool, nil:
	default:
		return nil, false
	}
	*n++
	return v, true
}

// finiteNumber: endliche Zahl. Ein Literal wie 1e400 liest der Server als unendlich.
func finiteNumber(v any) bool {
	switch x := v.(type) {
	case json.Number:
		f, err := strconv.ParseFloat(string(x), 64)
		return err == nil && !math.IsInf(f, 0) && !math.IsNaN(f)
	case float64:
		return !math.IsInf(x, 0) && !math.IsNaN(x)
	case int, int64:
		return true
	}
	return false
}

// checkUsage prüft die Grenzen von usage wie der Server: Behälter bis UsageMaxDepth Ebenen (usage ist Ebene 1),
// höchstens UsageMaxValues Werte unterhalb von usage, Schlüssel nach ExtraKey, Listen bis UsageMaxList Einträge;
// Werte endliche Zahl, true, false, null, Text bis MaxExtraString Zeichen ohne Steuer- und Formatzeichen.
func checkUsage(node any, depth int, n *int) error {
	var vals []any
	switch x := node.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			if !ExtraKey.MatchString(k) {
				return errors.New("usage: Schlüssel ungültig")
			}
			vals = append(vals, x[k])
		}
	case []any:
		if len(x) > UsageMaxList {
			return errors.New("usage: Liste zu lang")
		}
		vals = x
	}
	for _, v := range vals {
		*n++
		if *n > UsageMaxValues {
			return errors.New("usage: zu viele Werte")
		}
		switch x := v.(type) {
		case map[string]any, []any:
			if depth+1 > UsageMaxDepth {
				return errors.New("usage: zu tief")
			}
			if err := checkUsage(x, depth+1, n); err != nil {
				return err
			}
		case string:
			if len([]rune(x)) > MaxExtraString || hasControl(x) || HasSpoofRune(x) {
				return errors.New("usage: Text ungültig")
			}
		case bool, nil:
		default:
			if !finiteNumber(x) {
				return errors.New("usage: Wert ungültig")
			}
		}
	}
	return nil
}

// MarshalLine schreibt eine Inhaltszeile als kompaktes JSON in einer Zeile, ohne HTML-Maskierung (<, >, & bleiben
// so). Diese Bytes werden signiert und gehen unverändert an den Server.
func MarshalLine(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

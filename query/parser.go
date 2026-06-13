// Package query implements the QUERY layer: symbol search, graph traversal
// (callers/callees/impact), and index status/files verbs.
//
// Ported from src/db/queries.ts, src/graph/traversal.ts,
// src/search/query-parser.ts, src/search/query-utils.ts, and
// src/bin/codegraph.ts of github.com/colbymchenry/codegraph (MIT).
//
// Public functions accept a *store.Store and return typed structs that marshal
// to the exact JSON payloads produced by the original CLI's --json flags.
package query

import (
	"strings"

	"github.com/specscore/codegrapher/model"
)

// ParsedQuery holds the result of parsing a raw search string.
// Mirrors ParsedQuery from src/search/query-parser.ts.
type ParsedQuery struct {
	// Free-text portion to feed to FTS / LIKE. May be empty.
	Text string
	// kind: filters (OR'd). Empty when none specified.
	Kinds []model.NodeKind
	// lang:/language: filters (OR'd). Empty when none specified.
	Languages []model.Language
	// path: filters (OR'd, case-insensitive substring of file_path).
	PathFilters []string
	// name: filters (OR'd, case-insensitive substring of node.name).
	NameFilters []string
}

// kindValues is the set of valid NodeKind strings (mirrors NODE_KINDS).
var kindValues map[string]bool

// languageValues is the set of valid Language strings (mirrors LANGUAGES).
var languageValues map[string]bool

func init() {
	kindValues = make(map[string]bool, len(model.NodeKinds))
	for _, k := range model.NodeKinds {
		kindValues[string(k)] = true
	}
	languageValues = map[string]bool{
		"typescript": true, "javascript": true, "tsx": true, "jsx": true,
		"go": true, "go.mod": true, "node": true, "package.json": true,
		"yaml": true, "unknown": true,
		// Additional languages from the original types.ts LANGUAGES array.
		"python": true, "java": true, "csharp": true, "cpp": true,
		"c": true, "ruby": true, "rust": true, "kotlin": true,
		"swift": true, "php": true, "scala": true, "r": true,
		"dart": true, "elixir": true, "haskell": true, "lua": true,
		"julia": true, "markdown": true, "html": true, "css": true,
		"sql": true, "shell": true, "dockerfile": true,
		"terraform": true, "protobuf": true, "graphql": true,
	}
}

// ParseQuery parses a raw query string into structured filters + remaining text.
// Mirrors parseQuery from src/search/query-parser.ts.
// Always returns a value; never panics.
func ParseQuery(raw string) ParsedQuery {
	out := ParsedQuery{}
	tokens := tokenise(raw)

	var textParts []string
	for _, tok := range tokens {
		colon := strings.Index(tok, ":")
		if colon <= 0 || colon == len(tok)-1 {
			textParts = append(textParts, tok)
			continue
		}
		key := strings.ToLower(tok[:colon])
		valueRaw := unquote(tok[colon+1:])
		if valueRaw == "" {
			textParts = append(textParts, tok)
			continue
		}
		switch key {
		case "kind":
			if kindValues[valueRaw] {
				out.Kinds = append(out.Kinds, model.NodeKind(valueRaw))
			} else {
				textParts = append(textParts, tok)
			}
		case "lang", "language":
			lower := strings.ToLower(valueRaw)
			if languageValues[lower] {
				out.Languages = append(out.Languages, model.Language(lower))
			} else {
				textParts = append(textParts, tok)
			}
		case "path":
			out.PathFilters = append(out.PathFilters, valueRaw)
		case "name":
			out.NameFilters = append(out.NameFilters, valueRaw)
		default:
			textParts = append(textParts, tok)
		}
	}

	out.Text = strings.TrimSpace(strings.Join(textParts, " "))
	return out
}

// tokenise splits raw on whitespace while preserving quoted spans.
func tokenise(raw string) []string {
	var tokens []string
	i := 0
	for i < len(raw) {
		for i < len(raw) && isSpace(raw[i]) {
			i++
		}
		if i >= len(raw) {
			break
		}
		start := i
		for i < len(raw) && !isSpace(raw[i]) {
			if raw[i] == '"' {
				end := strings.Index(raw[i+1:], `"`)
				if end == -1 {
					i = len(raw)
					break
				}
				i = i + 1 + end + 1
				continue
			}
			i++
		}
		tokens = append(tokens, raw[start:i])
	}
	return tokens
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// BoundedEditDistance computes a bounded Levenshtein distance between a and b.
// Returns maxDist+1 as soon as distance exceeds maxDist.
// Mirrors boundedEditDistance from src/search/query-parser.ts.
func BoundedEditDistance(a, b string, maxDist int) int {
	if a == b {
		return 0
	}
	ar := []rune(a)
	br := []rune(b)
	al, bl := len(ar), len(br)
	if absDiff(al, bl) > maxDist {
		return maxDist + 1
	}
	if al == 0 {
		return bl
	}
	if bl == 0 {
		return al
	}

	prev := make([]int, bl+1)
	cur := make([]int, bl+1)
	for j := 0; j <= bl; j++ {
		prev[j] = j
	}
	for i := 1; i <= al; i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= bl; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			ins := cur[j-1] + 1
			del := prev[j] + 1
			sub := prev[j-1] + cost
			cur[j] = minInt3(ins, del, sub)
			if cur[j] < rowMin {
				rowMin = cur[j]
			}
		}
		if rowMin > maxDist {
			return maxDist + 1
		}
		prev, cur = cur, prev
	}
	return prev[bl]
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

func minInt3(a, b, c int) int {
	if a <= b && a <= c {
		return a
	}
	if b <= c {
		return b
	}
	return c
}

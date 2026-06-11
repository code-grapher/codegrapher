package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// -----------------------------------------------------------------------
// Ports of src/search/query-utils.ts helpers the MCP layer needs.
// (ScorePathRelevance / IsTestFile / IsGeneratedFile already live in the
// query package and are reused from there.)
// -----------------------------------------------------------------------

// stopWords mirrors STOP_WORDS in query-utils.ts.
var stopWords = map[string]bool{
	// English
	"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
	"in": true, "on": true, "at": true, "to": true, "for": true,
	"of": true, "with": true, "by": true, "from": true, "is": true, "it": true,
	"that": true, "this": true, "are": true, "was": true,
	"be": true, "has": true, "had": true, "have": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "could": true,
	"should": true, "may": true, "might": true, "can": true, "shall": true,
	"not": true, "no": true, "all": true, "each": true,
	"every": true, "how": true, "what": true, "where": true, "when": true,
	"who": true, "which": true, "why": true,
	"i": true, "me": true, "my": true, "we": true, "our": true, "you": true,
	"your": true, "he": true, "she": true, "they": true,
	"show": true, "give": true, "tell": true,
	"been": true, "done": true, "made": true, "used": true, "using": true,
	"work": true, "works": true, "found": true,
	"also": true, "into": true, "then": true, "than": true, "just": true,
	"more": true, "some": true, "such": true,
	"over": true, "only": true, "out": true, "its": true, "so": true,
	"up": true, "as": true, "if": true,
	"look": true, "need": true, "needs": true, "want": true, "happen": true,
	"happens": true,
	"affect":  true, "affected": true, "break": true, "breaks": true,
	"failing":     true,
	"implemented": true, "implement": true,
	// Code-specific noise
	"code": true, "file": true, "files": true, "function": true,
	"method": true, "class": true, "type": true,
	"fix": true, "bug": true, "called": true,
}

// getStemVariants mirrors getStemVariants in query-utils.ts.
func getStemVariants(term string) []string {
	variants := make(map[string]struct{})
	add := func(v string) { variants[v] = struct{}{} }
	t := strings.ToLower(term)

	if strings.HasSuffix(t, "ing") && len(t) > 5 {
		base := t[:len(t)-3]
		add(base)
		add(base + "e")
		if len(base) >= 2 && base[len(base)-1] == base[len(base)-2] {
			add(base[:len(base)-1])
		}
	}
	if (strings.HasSuffix(t, "tion") || strings.HasSuffix(t, "sion")) && len(t) > 5 {
		add(t[:len(t)-3])
	}
	if strings.HasSuffix(t, "ment") && len(t) > 6 {
		add(t[:len(t)-4])
	}
	if strings.HasSuffix(t, "ies") && len(t) > 4 {
		add(t[:len(t)-3] + "y")
	} else if strings.HasSuffix(t, "es") && len(t) > 4 {
		add(t[:len(t)-2])
	} else if strings.HasSuffix(t, "s") && !strings.HasSuffix(t, "ss") && len(t) > 4 {
		add(t[:len(t)-1])
	}
	if strings.HasSuffix(t, "ed") && !strings.HasSuffix(t, "eed") && len(t) > 4 {
		add(t[:len(t)-1])
		add(t[:len(t)-2])
		if strings.HasSuffix(t, "ied") && len(t) > 5 {
			add(t[:len(t)-3] + "y")
		}
	}
	if strings.HasSuffix(t, "er") && len(t) > 4 {
		base := t[:len(t)-2]
		add(base)
		add(base + "e")
		if len(base) >= 2 && base[len(base)-1] == base[len(base)-2] {
			add(base[:len(base)-1])
		}
	}

	out := make([]string, 0, len(variants))
	// Deterministic order for tests; callers add results into sets.
	for v := range variants {
		if len(v) >= 3 && v != t {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

var (
	reCompound    = regexp.MustCompile(`\b([a-zA-Z][a-zA-Z0-9]*(?:[A-Z][a-z]+)+|[A-Z][a-z]+(?:[A-Z][a-z]*)+)\b`)
	reSnakeSearch = regexp.MustCompile(`\b([a-zA-Z][a-zA-Z0-9]*(?:_[a-zA-Z0-9]+)+)\b`)
	reCamelLower  = regexp.MustCompile(`([a-z])([A-Z])`)
	reCamelUpper  = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	reNonAlnum    = regexp.MustCompile(`[^a-zA-Z0-9]+`)
)

// extractSearchTerms mirrors extractSearchTerms in query-utils.ts.
// Returned order matches the JS Set insertion order.
func extractSearchTerms(query string, includeStems bool) []string {
	var order []string
	seen := make(map[string]struct{})
	add := func(tok string) {
		if _, ok := seen[tok]; !ok {
			seen[tok] = struct{}{}
			order = append(order, tok)
		}
	}

	for _, m := range reCompound.FindAllStringSubmatch(query, -1) {
		if len(m[1]) >= 3 {
			add(strings.ToLower(m[1]))
		}
	}
	for _, m := range reSnakeSearch.FindAllStringSubmatch(query, -1) {
		if len(m[1]) >= 3 {
			add(strings.ToLower(m[1]))
		}
	}

	camelSplit := reCamelLower.ReplaceAllString(query, "$1 $2")
	camelSplit = reCamelUpper.ReplaceAllString(camelSplit, "$1 $2")
	normalised := regexp.MustCompile(`[_.]+`).ReplaceAllString(camelSplit, " ")
	for _, word := range reNonAlnum.Split(normalised, -1) {
		if word == "" {
			continue
		}
		lower := strings.ToLower(word)
		if len(lower) < 3 || stopWords[lower] {
			continue
		}
		add(lower)
	}

	if includeStems {
		var stems []string
		stemSeen := make(map[string]struct{})
		for _, token := range order {
			for _, variant := range getStemVariants(token) {
				if _, ok := seen[variant]; ok {
					continue
				}
				if stopWords[variant] {
					continue
				}
				if _, ok := stemSeen[variant]; !ok {
					stemSeen[variant] = struct{}{}
					stems = append(stems, variant)
				}
			}
		}
		for _, stem := range stems {
			add(stem)
		}
	}

	return order
}

var (
	reSymCamel     = regexp.MustCompile(`\b([A-Z][a-z]+(?:[A-Z][a-z]*)*|[a-z]+(?:[A-Z][a-z]*)+)\b`)
	reSymSnake     = regexp.MustCompile(`(?i)\b([a-z][a-z0-9]*(?:_[a-z0-9]+)+)\b`)
	reSymScreaming = regexp.MustCompile(`\b([A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+)\b`)
	reSymAcronym   = regexp.MustCompile(`\b([A-Z]{2,})\b`)
	reSymDotted    = regexp.MustCompile(`\b([a-zA-Z][a-zA-Z0-9]*(?:\.[a-zA-Z][a-zA-Z0-9]*)+)\b`)
	reSymLower     = regexp.MustCompile(`\b([a-z][a-z0-9]{2,})\b`)
)

// symbolCommonWords mirrors the commonWords filter in extractSymbolsFromQuery
// (src/context/index.ts).
var symbolCommonWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"this": true, "that": true, "have": true, "been": true,
	"will": true, "would": true, "could": true, "should": true, "does": true,
	"done": true, "make": true, "made": true,
	"use": true, "used": true, "using": true, "work": true, "works": true,
	"find": true, "found": true, "show": true,
	"call": true, "called": true, "calling": true, "get": true, "set": true,
	"add": true, "all": true, "any": true,
	"how": true, "what": true, "when": true, "where": true, "which": true,
	"who": true, "why": true,
	"not": true, "but": true, "are": true, "was": true, "were": true,
	"has": true, "had": true, "its": true,
	"can": true, "did": true, "may": true, "also": true, "into": true,
	"than": true, "then": true, "them": true,
	"each": true, "other": true, "some": true, "such": true, "only": true,
	"same": true, "about": true,
	"after": true, "before": true, "between": true, "through": true,
	"during": true, "without": true,
	"again": true, "further": true, "once": true, "here": true,
	"there": true, "both": true, "just": true,
	"more": true, "most": true, "very": true, "being": true, "having": true,
	"doing":  true,
	"system": true, "need": true, "needs": true, "want": true, "wants": true,
	"like": true, "look": true,
	"change": true, "changes": true, "changed": true, "changing": true,
	"layer": true, "handle": true, "handles": true, "handling": true,
	"incoming": true, "outgoing": true,
	"data": true, "flow": true, "flows": true, "level": true, "levels": true,
	"request": true, "requests": true,
	"response": true, "responses": true, "implement": true,
	"implements": true, "implementation": true,
	"interface": true, "interfaces": true, "class": true, "classes": true,
	"method": true, "methods": true,
	"trigger": true, "triggers": true, "affected": true, "affect": true,
	"affects": true,
	"else":    true, "code": true, "failing": true, "failed": true,
	"silently": true, "decide": true, "decides": true,
	"return": true, "returns": true, "returned": true, "take": true,
	"takes": true, "taken": true,
	"check": true, "checks": true, "checked": true, "create": true,
	"creates": true, "created": true,
	"read": true, "reads": true, "write": true, "writes": true,
	"written": true,
	"start":   true, "starts": true, "stop": true, "stops": true, "run": true,
	"runs": true, "running": true,
}

// extractSymbolsFromQuery mirrors extractSymbolsFromQuery in
// src/context/index.ts. Returned order matches the JS Set insertion order.
func extractSymbolsFromQuery(query string) []string {
	var order []string
	seen := make(map[string]struct{})
	add := func(tok string) {
		if _, ok := seen[tok]; !ok {
			seen[tok] = struct{}{}
			order = append(order, tok)
		}
	}

	for _, m := range reSymCamel.FindAllStringSubmatch(query, -1) {
		if len(m[1]) >= 2 {
			add(m[1])
		}
	}
	for _, m := range reSymSnake.FindAllStringSubmatch(query, -1) {
		if len(m[1]) >= 3 {
			add(m[1])
		}
	}
	for _, m := range reSymScreaming.FindAllStringSubmatch(query, -1) {
		add(m[1])
	}
	for _, m := range reSymAcronym.FindAllStringSubmatch(query, -1) {
		add(m[1])
	}
	for _, m := range reSymDotted.FindAllStringSubmatch(query, -1) {
		add(m[1])
		for _, part := range strings.Split(m[1], ".") {
			if len(part) >= 2 {
				add(part)
			}
		}
	}
	for _, m := range reSymLower.FindAllStringSubmatch(query, -1) {
		add(m[1])
	}

	out := order[:0:0]
	for _, s := range order {
		if !symbolCommonWords[strings.ToLower(s)] {
			out = append(out, s)
		}
	}
	return out
}

// isDistinctiveIdentifier mirrors isDistinctiveIdentifier in query-utils.ts.
func isDistinctiveIdentifier(token string) bool {
	if token == "" {
		return false
	}
	if strings.ContainsAny(token, "_0123456789") {
		return true
	}
	for _, r := range token[1:] {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

// normalizeNameToken mirrors normalizeNameToken in query-utils.ts.
func normalizeNameToken(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// deriveProjectNameTokens mirrors deriveProjectNameTokens in query-utils.ts.
func deriveProjectNameTokens(projectRoot string) map[string]struct{} {
	tokens := make(map[string]struct{})
	add := func(raw string) {
		if raw == "" {
			return
		}
		norm := normalizeNameToken(raw)
		if len(norm) >= 5 {
			tokens[norm] = struct{}{}
		}
	}

	if data, err := os.ReadFile(filepath.Join(projectRoot, "go.mod")); err == nil {
		if m := regexp.MustCompile(`(?m)^\s*module\s+(\S+)`).FindStringSubmatch(string(data)); m != nil {
			parts := strings.Split(m[1], "/")
			add(parts[len(parts)-1])
		}
	}
	if data, err := os.ReadFile(filepath.Join(projectRoot, "package.json")); err == nil {
		var pkg struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &pkg) == nil && pkg.Name != "" {
			add(regexp.MustCompile(`^@[^/]+/`).ReplaceAllString(pkg.Name, ""))
		}
	}
	abs, err := filepath.Abs(projectRoot)
	if err == nil {
		add(filepath.Base(abs))
	}
	return tokens
}

// isLowValueFile mirrors isLowValueFile in src/db/queries.ts (test/spec
// detection + generated files).
var lowValueFilePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:^|/)(tests?|__tests?__|spec)/`),
	regexp.MustCompile(`_test\.go$`),
	regexp.MustCompile(`(?:^|/)test_[^/]+\.py$`),
	regexp.MustCompile(`_test\.py$`),
	regexp.MustCompile(`_spec\.rb$`),
	regexp.MustCompile(`_test\.rb$`),
	regexp.MustCompile(`\.(test|spec)\.[jt]sx?$`),
	regexp.MustCompile(`(test|spec|tests)\.(java|kt|scala)$`),
	regexp.MustCompile(`(tests?|spec)\.cs$`),
	regexp.MustCompile(`tests?\.swift$`),
	regexp.MustCompile(`_test\.dart$`),
}

func isLowValueDBFile(filePath string, isGenerated func(string) bool) bool {
	lp := strings.ToLower(filePath)
	for _, re := range lowValueFilePatterns {
		if re.MatchString(lp) {
			return true
		}
	}
	return isGenerated(filePath)
}

// lastQualifierPart mirrors lastQualifierPart in src/mcp/tools.ts.
var reQualSplit = regexp.MustCompile(`::|[./]`)

func lastQualifierPart(symbol string) string {
	var parts []string
	for _, p := range reQualSplit.Split(symbol, -1) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return symbol
	}
	return parts[len(parts)-1]
}

// clamp mirrors clamp in src/utils.ts.
func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// numberSourceLines mirrors numberSourceLines in src/mcp/tools.ts: prefix
// each line with its 1-based line number (cat -n convention, number + tab).
func numberSourceLines(slice string, firstLineNumber int) string {
	split := strings.Split(slice, "\n")
	var b strings.Builder
	for i, line := range split {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(firstLineNumber + i))
		b.WriteByte('\t')
		b.WriteString(line)
	}
	return b.String()
}

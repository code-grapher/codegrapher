package query

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/specscore/codegrapher/model"
)

// KindBonus returns the relevance bonus for a node kind.
// Mirrors kindBonus from src/search/query-utils.ts.
func KindBonus(kind model.NodeKind) float64 {
	switch kind {
	case model.KindFunction:
		return 10
	case model.KindMethod:
		return 10
	case model.KindInterface:
		return 9
	case model.KindTrait:
		return 9
	case model.KindProtocol:
		return 9
	case model.KindRoute:
		return 9
	case model.KindClass:
		return 8
	case model.KindComponent:
		return 8
	case model.KindTypeAlias:
		return 6
	case model.KindStruct:
		return 6
	case model.KindEnum:
		return 5
	case model.KindModule:
		return 4
	case model.KindNamespace:
		return 4
	case model.KindProperty:
		return 3
	case model.KindField:
		return 3
	case model.KindConstant:
		return 3
	case model.KindEnumMember:
		return 3
	case model.KindVariable:
		return 2
	case model.KindImport:
		return 1
	case model.KindExport:
		return 1
	default:
		return 0
	}
}

// NameMatchBonus returns a score bonus when the node name matches the query.
// Mirrors nameMatchBonus from src/search/query-utils.ts.
func NameMatchBonus(nodeName, query string) float64 {
	nameLower := strings.ToLower(nodeName)

	// Split query into raw terms by camelCase / separators.
	rawTerms := splitQueryTerms(query)

	// Space-separated tokens for exact-token matching.
	queryTokens := strings.Fields(strings.ToLower(query))
	// Full query as a single token (compound identifiers: drop all spaces).
	queryLower := strings.ToLower(removeSpaces(query))

	if queryLower == "" {
		return 0
	}

	// Exact match: query equals node name.
	if nameLower == queryLower {
		return 80
	}

	// Exact token match: one of the space-separated query tokens equals the name.
	if len(queryTokens) > 1 {
		for _, t := range queryTokens {
			if nameLower == t {
				return 60
			}
		}
	}

	// Name starts with query — scale by length ratio.
	if strings.HasPrefix(nameLower, queryLower) && len(nameLower) > 0 {
		ratio := float64(len(queryLower)) / float64(len(nameLower))
		return 10 + 30*ratio
	}

	// All camelCase-split terms appear in the name.
	if len(rawTerms) > 1 {
		allMatch := true
		for _, t := range rawTerms {
			if !strings.Contains(nameLower, t) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return 15
		}
	}

	// Name contains full query as substring.
	if strings.Contains(nameLower, queryLower) {
		return 10
	}

	return 0
}

func splitQueryTerms(query string) []string {
	camelSplit := camelCaseSplit(query)
	normalised := strings.NewReplacer("_", " ", ".", " ", "-", " ").Replace(camelSplit)
	fields := strings.Fields(normalised)
	var out []string
	for _, f := range fields {
		lower := strings.ToLower(f)
		if len(lower) >= 2 {
			out = append(out, lower)
		}
	}
	return out
}

var (
	reLC2UC = regexp.MustCompile(`([a-z])([A-Z])`)
	reUC2UC = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
)

func camelCaseSplit(s string) string {
	s = reLC2UC.ReplaceAllString(s, "$1 $2")
	s = reUC2UC.ReplaceAllString(s, "$1 $2")
	return s
}

func removeSpaces(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ScorePathRelevance returns a relevance score for a file path against a query.
// Mirrors scorePathRelevance from src/search/query-utils.ts.
// projectNameTokens can be nil (no down-weighting).
func ScorePathRelevance(filePath, query string, projectNameTokens map[string]struct{}) float64 {
	pathLower := strings.ToLower(filePath)
	fileName := strings.ToLower(filepath.Base(filePath))
	dirName := strings.ToLower(filepath.Dir(filePath))

	var score float64

	allWords := strings.Fields(query)
	if len(allWords) == 0 {
		return 0
	}

	// Filter out project name tokens when other words remain.
	words := allWords
	if len(projectNameTokens) > 0 {
		var filtered []string
		for _, w := range allWords {
			if _, ok := projectNameTokens[normalizeToken(w)]; !ok {
				filtered = append(filtered, w)
			}
		}
		if len(filtered) > 0 {
			words = filtered
		}
	}

	for _, word := range words {
		subtokens := extractBaseTerms(word)
		if len(subtokens) == 0 {
			continue
		}
		if anyContains(fileName, subtokens) {
			score += 10
		} else if anyContains(dirName, subtokens) {
			score += 5
		} else if anyContains(pathLower, subtokens) {
			score += 3
		}
	}

	// Deprioritize test files unless query is about tests.
	queryLower := strings.ToLower(query)
	isTestQuery := strings.Contains(queryLower, "test") || strings.Contains(queryLower, "spec")
	if !isTestQuery && IsTestFile(filePath) {
		score -= 15
	}

	return score
}

func anyContains(s string, subtokens []string) bool {
	for _, t := range subtokens {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

func normalizeToken(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func extractBaseTerms(word string) []string {
	split := camelCaseSplit(word)
	split = strings.NewReplacer("_", " ", ".", " ").Replace(split)
	fields := strings.Fields(split)
	var out []string
	for _, f := range fields {
		lower := strings.ToLower(f)
		if len(lower) >= 3 {
			out = append(out, lower)
		}
	}
	return out
}

// IsTestFile checks whether a file path looks like a test file.
// Mirrors isTestFile from src/search/query-utils.ts.
func IsTestFile(filePath string) bool {
	lower := strings.ToLower(filePath)
	fileName := filepath.Base(filePath)
	lowerName := strings.ToLower(fileName)

	if strings.HasPrefix(lowerName, "test_") || strings.HasPrefix(lowerName, "test.") {
		return true
	}
	if reSuffixTest.MatchString(lowerName) || reSuffixCap.MatchString(fileName) {
		return true
	}

	for _, p := range []string{"/tests/", "/test/", "/__tests__/", "/spec/", "/specs/", "/testlib/", "/testing/"} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	if strings.HasPrefix(lower, "test/") || strings.HasPrefix(lower, "tests/") ||
		strings.HasPrefix(lower, "spec/") || strings.HasPrefix(lower, "specs/") {
		return true
	}
	if reCapTestDir.MatchString(filePath) {
		return true
	}
	return matchesNonProductionDir(lower)
}

var (
	reCapTestDir = regexp.MustCompile(`(?:^|/)[A-Za-z0-9]*(?:Test|Tests|Spec)/`)
	reSuffixTest = regexp.MustCompile(`[._-](test|tests|spec|specs)\.[a-z0-9]+$`)
	reSuffixCap  = regexp.MustCompile(`(?:Test|Tests|TestCase|Tester|Spec|Specs)\.[A-Za-z0-9]+$`)
)

func matchesNonProductionDir(lower string) bool {
	for _, d := range []string{
		"integration", "sample", "samples", "example", "examples",
		"fixture", "fixtures", "benchmark", "benchmarks", "demo", "demos",
	} {
		if strings.Contains(lower, "/"+d+"/") || strings.HasPrefix(lower, d+"/") {
			return true
		}
	}
	return false
}

// IsGeneratedFile returns true for files that appear auto-generated.
// Mirrors isGeneratedFile from src/extraction/generated-detection.ts.
func IsGeneratedFile(filePath string) bool {
	lower := strings.ToLower(filePath)
	for _, suf := range []string{
		".pb.go", ".pb.gw.go", ".pulsar.go", ".generated.go", "_gen.go", ".gen.go",
		".generated.ts", ".generated.tsx",
	} {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	for _, dir := range []string{"/vendor/", "/__generated__/", "/generated/"} {
		if strings.Contains(lower, dir) {
			return true
		}
	}
	return false
}

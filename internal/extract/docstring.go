package extract

import (
	"regexp"
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
)

// commentKinds is the set of tree-sitter node kinds treated as comments.
var commentKindSet = map[string]bool{
	"comment":               true,
	"line_comment":          true,
	"block_comment":         true,
	"documentation_comment": true,
}

// buildCommentIndex walks the entire syntax tree and records the last comment
// text whose end line is key (1-indexed). The extractor calls this once per
// file and passes the resulting map to lookupDocstring.
func buildCommentIndex(root *tsparse.Node) map[int]string {
	m := make(map[int]string)
	tsparse.Walk(root, func(n *tsparse.Node) {
		if commentKindSet[n.Kind()] {
			endLine := int(n.EndPoint().Row) + 1
			m[endLine] = n.Text()
		}
	})
	return m
}

// lookupDocstring returns the cleaned-up docstring for a node whose start line
// is startLine, by looking for a comment that ends on startLine-1.
// Returns "" if none found.
func lookupDocstring(commentByEndLine map[int]string, startLine int) string {
	raw, ok := commentByEndLine[startLine-1]
	if !ok {
		return ""
	}
	return cleanComment(raw)
}

// reLeadingSlashStar matches the opening /** or /* of a block comment.
var reLeadingSlashStar = regexp.MustCompile(`^/\*\*?`)

// reTrailingStarSlash matches the closing */ of a block comment.
var reTrailingStarSlash = regexp.MustCompile(`\*/$`)

// reLineComment matches // at the start of a line (with optional space).
var reLineComment = regexp.MustCompile(`(?m)^//\s?`)

// reStarPrefix matches a leading * (with optional space) at the start of a line
// inside a block comment.
var reStarPrefix = regexp.MustCompile(`(?m)^\s*\*\s?`)

// cleanComment strips comment markers from a raw comment string, mirroring the
// cleanup in getPrecedingDocstring from tree-sitter-helpers.ts.
func cleanComment(c string) string {
	c = reLeadingSlashStar.ReplaceAllString(c, "")
	c = reTrailingStarSlash.ReplaceAllString(c, "")
	c = reLineComment.ReplaceAllString(c, "")
	c = reStarPrefix.ReplaceAllString(c, "")
	return strings.TrimSpace(c)
}

package coverage

import (
	"github.com/specscore/codegrapher/model"
)

// isFunctionKind reports whether a node kind participates in per-function
// coverage attribution. Only function/method bodies (and nested func literals,
// were the extractor to emit them) carry line coverage; types, fields, imports,
// etc. do not enclose executable lines of their own.
func isFunctionKind(k model.NodeKind) bool {
	return k == model.KindFunction || k == model.KindMethod
}

// nodeLineCount is the per-node innermost-attributed covered/uncovered count.
type nodeLineCount struct {
	NodeID    string
	Covered   int
	Uncovered int
}

// attributeLines assigns each measured line to the INNERMOST enclosing
// function-like node and returns per-node covered/uncovered counts. A line is
// attributed to the node with the tightest [StartLine,EndLine] span containing
// it; lines in no function node are file-level only and excluded here (they
// still count in the per-file totals). Counts are non-overlapping: a parent
// never counts a line that falls inside a nested child.
//
// nodes is a file's nodes (any order; typically store.GetNodesByFile output).
func attributeLines(nodes []model.Node, covered, uncovered map[int]bool) []nodeLineCount {
	fns := make([]model.Node, 0, len(nodes))
	for _, n := range nodes {
		if isFunctionKind(n.Kind) && n.StartLine > 0 && n.EndLine >= n.StartLine {
			fns = append(fns, n)
		}
	}
	if len(fns) == 0 {
		return nil
	}

	counts := make(map[string]*nodeLineCount, len(fns))
	assign := func(line int, isCovered bool) {
		owner := innermost(fns, line)
		if owner == nil {
			return // file-level line, not attributed to any function
		}
		c := counts[owner.ID]
		if c == nil {
			c = &nodeLineCount{NodeID: owner.ID}
			counts[owner.ID] = c
		}
		if isCovered {
			c.Covered++
		} else {
			c.Uncovered++
		}
	}
	for ln := range covered {
		assign(ln, true)
	}
	for ln := range uncovered {
		assign(ln, false)
	}

	out := make([]nodeLineCount, 0, len(counts))
	for _, c := range counts {
		out = append(out, *c)
	}
	return out
}

// innermost returns the function-like node with the tightest span containing
// line, or nil if none contains it. Ties (identical spans) resolve to the node
// with the smaller ID for determinism.
func innermost(fns []model.Node, line int) *model.Node {
	var best *model.Node
	for i := range fns {
		n := &fns[i]
		if line < n.StartLine || line > n.EndLine {
			continue
		}
		if best == nil || tighter(n, best) {
			best = n
		}
	}
	return best
}

// tighter reports whether span a is strictly inside, or equal-but-lower-ID than,
// span b — i.e. a is the more specific owner.
func tighter(a, b *model.Node) bool {
	aspan := a.EndLine - a.StartLine
	bspan := b.EndLine - b.StartLine
	if aspan != bspan {
		return aspan < bspan
	}
	return a.ID < b.ID
}

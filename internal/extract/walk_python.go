package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkPython walks a parsed Python (tree-sitter `python`) file root and
// extracts symbols. Called by ExtractFile after the file node is emitted.
func (e *extractor) walkPython(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodePython(child)
		}
	}
}

// visitNodePython dispatches a single statement node. Construct handlers are
// added incrementally; unknown kinds descend into their bodies so calls nested
// inside control flow are still seen.
func (e *extractor) visitNodePython(node *tsparse.Node) {
	switch node.Kind() {
	default:
		e.visitPyBody(node)
	}
}

// visitPyBody descends into a node's named children looking for calls and
// nested definitions without emitting a node for the container itself.
func (e *extractor) visitPyBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodePython(child)
		}
	}
}

var _ = strings.TrimSpace // retained; used by construct handlers added in Phase 2
var _ = model.KindFunction

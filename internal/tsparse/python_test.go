package tsparse_test

import (
	"testing"

	"github.com/specscore/codegrapher/internal/tsparse"
)

func TestPythonParseSmoke(t *testing.T) {
	src := []byte("class A:\n    def m(self):\n        return 1\n")
	p, err := tsparse.NewParser(tsparse.LangPython)
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	tree, err := p.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(collectByKind(tree.RootNode(), "class_definition")); got != 1 {
		t.Fatalf("class_definition count = %d, want 1", got)
	}
	if got := len(collectByKind(tree.RootNode(), "function_definition")); got != 1 {
		t.Fatalf("function_definition count = %d, want 1", got)
	}
}

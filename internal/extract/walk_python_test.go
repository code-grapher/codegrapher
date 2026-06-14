package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractPy(t *testing.T, src string) ([]model.Node, []model.Edge, []model.UnresolvedReference) {
	t.Helper()
	res, err := ExtractFile("m.py", []byte(src), model.LangPython)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return res.Nodes, res.Edges, res.UnresolvedReferences
}

func TestPyFileNodeEmitted(t *testing.T) {
	nodes, _, _ := extractPy(t, "x = 1\n")
	if len(nodes) == 0 || nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", nodes)
	}
	if nodes[0].Language != model.LangPython {
		t.Fatalf("file node language = %q, want python", nodes[0].Language)
	}
}

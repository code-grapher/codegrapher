package mcp_test

import (
	"testing"

	"github.com/specscore/codegrapher/mcp"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// scopeRich builds a Go scope with a class "Base" extended by "Child", where
// "Child" contains a method "Run" that calls "Helper". Exercises the
// container/hierarchy/edge methods.
func scopeRich(t *testing.T) *store.Store {
	nodes := []model.Node{
		{ID: "go:c:Base", Kind: model.KindClass, Name: "Base", QualifiedName: "Base", FilePath: "c.go", Language: model.LangGo, StartLine: 1, EndLine: 2},
		{ID: "go:c:Child", Kind: model.KindClass, Name: "Child", QualifiedName: "Child", FilePath: "c.go", Language: model.LangGo, StartLine: 3, EndLine: 8},
		{ID: "go:c:Run", Kind: model.KindMethod, Name: "Run", QualifiedName: "Child.Run", FilePath: "c.go", Language: model.LangGo, StartLine: 4, EndLine: 6},
		{ID: "go:c:Helper", Kind: model.KindFunction, Name: "Helper", QualifiedName: "Helper", FilePath: "c.go", Language: model.LangGo, StartLine: 9, EndLine: 10},
	}
	edges := []model.Edge{
		{Source: "go:c:Child", Target: "go:c:Base", Kind: model.EdgeExtends},
		{Source: "go:c:Child", Target: "go:c:Run", Kind: model.EdgeContains},
		{Source: "go:c:Run", Target: "go:c:Helper", Kind: model.EdgeCalls},
	}
	files := []model.FileRecord{fileRec("c.go", model.LangGo, 4)}
	return newStore(t, nodes, edges, files)
}

func TestMultiBackendMethodsFanOut(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{scopeRich(t), scopeTS(t)}, root)

	t.Run("GetNodesInFile", func(t *testing.T) {
		nodes, err := multi.GetNodesInFile("c.go")
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) != 4 {
			t.Errorf("GetNodesInFile(c.go) = %d, want 4", len(nodes))
		}
	})

	t.Run("GetCallers", func(t *testing.T) {
		callers, err := multi.GetCallers("go:c:Helper")
		if err != nil {
			t.Fatal(err)
		}
		if len(callers) != 1 || callers[0].Node.ID != "go:c:Run" {
			t.Errorf("GetCallers(Helper) = %+v, want [Run]", callers)
		}
	})

	t.Run("GetChildren", func(t *testing.T) {
		children, err := multi.GetChildren("go:c:Child")
		if err != nil {
			t.Fatal(err)
		}
		if len(children) != 1 || children[0].ID != "go:c:Run" {
			t.Errorf("GetChildren(Child) = %+v, want [Run]", children)
		}
	})

	t.Run("GetTypeHierarchy", func(t *testing.T) {
		sg, err := multi.GetTypeHierarchy("go:c:Child")
		if err != nil {
			t.Fatal(err)
		}
		if !sg.Has("go:c:Base") || !sg.Has("go:c:Child") {
			t.Errorf("type hierarchy of Child missing Base/Child: %v", sg.IDs())
		}
	})

	t.Run("GetImpactRadius", func(t *testing.T) {
		sg, err := multi.GetImpactRadius("go:c:Helper", 3)
		if err != nil {
			t.Fatal(err)
		}
		if !sg.Has("go:c:Helper") || !sg.Has("go:c:Run") {
			t.Errorf("impact of Helper missing Helper/Run: %v", sg.IDs())
		}
	})

	t.Run("TraverseBFS", func(t *testing.T) {
		sg, err := multi.TraverseBFS("go:c:Child", mcp.TraversalOptions{Direction: "outgoing"})
		if err != nil {
			t.Fatal(err)
		}
		if !sg.Has("go:c:Child") {
			t.Errorf("BFS from Child missing root: %v", sg.IDs())
		}
	})

	t.Run("GetOutgoingEdges", func(t *testing.T) {
		edges, err := multi.GetOutgoingEdges("go:c:Child", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 2 {
			t.Errorf("outgoing edges of Child = %d, want 2", len(edges))
		}
	})

	t.Run("GetIncomingEdges", func(t *testing.T) {
		edges, err := multi.GetIncomingEdges("go:c:Base", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 || edges[0].Source != "go:c:Child" {
			t.Errorf("incoming edges of Base = %+v, want [Child->Base]", edges)
		}
	})

	t.Run("FindEdgesBetweenNodes", func(t *testing.T) {
		edges, err := multi.FindEdgesBetweenNodes([]string{"go:c:Child", "go:c:Run"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 || edges[0].Kind != model.EdgeContains {
			t.Errorf("edges between Child/Run = %+v, want [contains]", edges)
		}
	})

	t.Run("FindNodesByExactName", func(t *testing.T) {
		res, err := multi.FindNodesByExactName([]string{"Run"}, nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].Node.ID != "go:c:Run" {
			t.Errorf("FindNodesByExactName(Run) = %+v, want [Run]", res)
		}
	})

	t.Run("FindNodesByNameSubstring", func(t *testing.T) {
		res, err := multi.FindNodesByNameSubstring("Hel", nil, 10, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].Node.ID != "go:c:Helper" {
			t.Errorf("FindNodesByNameSubstring(Hel) = %+v, want [Helper]", res)
		}
	})

	t.Run("GetFileDependents", func(t *testing.T) {
		// No cross-file edges in these fixtures; just assert no error and a slice.
		deps, err := multi.GetFileDependents("c.go")
		if err != nil {
			t.Fatalf("GetFileDependents: %v", err)
		}
		_ = deps
	})

	t.Run("GetProjectNameTokens", func(t *testing.T) {
		tokens := multi.GetProjectNameTokens()
		if tokens == nil {
			t.Error("GetProjectNameTokens returned nil")
		}
	})

	t.Run("GetCode", func(t *testing.T) {
		// File c.go does not exist on disk under root, so GetCode returns ""
		// with no error (matches single-store behavior for unreadable files).
		code, err := multi.GetCode("go:c:Run")
		if err != nil {
			t.Fatalf("GetCode: %v", err)
		}
		_ = code
		// Missing node resolves to "" with no error.
		miss, err := multi.GetCode("nope")
		if err != nil || miss != "" {
			t.Errorf("GetCode(nope) = %q %v, want \"\" nil", miss, err)
		}
	})
}

// TestMultiBackendGetDominantFile picks the densest file across scopes.
func TestMultiBackendGetDominantFile(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{scopeGo(t), scopeTS(t)}, root)
	// Neither fixture crosses the in-file-edge threshold, so the result is nil
	// — but the merge path (querying each scope) is exercised.
	df, err := multi.GetDominantFile()
	if err != nil {
		t.Fatal(err)
	}
	_ = df
}

// TestMultiBackendEmpty covers the zero-backend edge cases.
func TestMultiBackendEmpty(t *testing.T) {
	multi := mcp.NewMultiBackend(nil, root)

	if got := multi.GetProjectNameTokens(); got == nil {
		t.Error("empty GetProjectNameTokens returned nil")
	}
	sg, err := multi.GetImpactRadius("x", 1)
	if err != nil || sg == nil || sg.Len() != 0 {
		t.Errorf("empty GetImpactRadius = %v %v", sg, err)
	}
	n, err := multi.GetNodeByID("x")
	if err != nil || n != nil {
		t.Errorf("empty GetNodeByID = %v %v", n, err)
	}
	code, err := multi.GetCode("x")
	if err != nil || code != "" {
		t.Errorf("empty GetCode = %q %v", code, err)
	}
	gs, err := multi.GetStats()
	if err != nil || gs.NodeCount != 0 {
		t.Errorf("empty GetStats = %+v %v", gs, err)
	}
}

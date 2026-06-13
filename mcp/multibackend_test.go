package mcp_test

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/mcp"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// newStore opens a fresh in-memory-ish store on a temp file and seeds it with
// the given nodes, edges and file records.
func newStore(t *testing.T, nodes []model.Node, edges []model.Edge, files []model.FileRecord) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := s.InsertEdges(edges); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}
	for _, f := range files {
		if err := s.UpsertFile(f); err != nil {
			t.Fatalf("UpsertFile: %v", err)
		}
	}
	return s
}

func fnNode(id, name, file string, lang model.Language) model.Node {
	return model.Node{
		ID:            id,
		Kind:          model.KindFunction,
		Name:          name,
		QualifiedName: name,
		FilePath:      file,
		Language:      lang,
		StartLine:     1,
		EndLine:       3,
	}
}

func fileRec(path string, lang model.Language, n int) model.FileRecord {
	return model.FileRecord{Path: path, Language: lang, NodeCount: n, ContentHash: "h"}
}

// scopeGo builds a Go-language scope with a single function "Handle" calling "Helper".
func scopeGo(t *testing.T) *store.Store {
	nodes := []model.Node{
		fnNode("go:a:Handle", "Handle", "a.go", model.LangGo),
		fnNode("go:a:Helper", "Helper", "a.go", model.LangGo),
	}
	edges := []model.Edge{{Source: "go:a:Handle", Target: "go:a:Helper", Kind: model.EdgeCalls}}
	files := []model.FileRecord{fileRec("a.go", model.LangGo, 2)}
	return newStore(t, nodes, edges, files)
}

// scopeTS builds a TypeScript scope with a function "Handle" (same name,
// different file/scope) calling "Process".
func scopeTS(t *testing.T) *store.Store {
	nodes := []model.Node{
		fnNode("ts:b:Handle", "Handle", "b.ts", model.LangTypeScript),
		fnNode("ts:b:Process", "Process", "b.ts", model.LangTypeScript),
	}
	edges := []model.Edge{{Source: "ts:b:Handle", Target: "ts:b:Process", Kind: model.EdgeCalls}}
	files := []model.FileRecord{fileRec("b.ts", model.LangTypeScript, 2)}
	return newStore(t, nodes, edges, files)
}

const root = "/tmp/proj"

// TestMultiBackendSingleStoreEquivalence: a MultiBackend over one store must
// behave identically to a StoreBackend over the same store.
func TestMultiBackendSingleStoreEquivalence(t *testing.T) {
	s := scopeGo(t)
	single := mcp.NewStoreBackend(s, root)
	multi := mcp.NewMultiBackend([]*store.Store{s}, root)

	t.Run("stats", func(t *testing.T) {
		gs, err := single.GetStats()
		if err != nil {
			t.Fatal(err)
		}
		gm, err := multi.GetStats()
		if err != nil {
			t.Fatal(err)
		}
		if gs.NodeCount != gm.NodeCount || gs.EdgeCount != gm.EdgeCount || gs.FileCount != gm.FileCount {
			t.Errorf("counts differ: %+v vs %+v", gs, gm)
		}
		if len(gs.NodesByKind) != len(gm.NodesByKind) || len(gs.FilesByLanguage) != len(gm.FilesByLanguage) {
			t.Errorf("maps differ: %+v vs %+v", gs, gm)
		}
	})

	t.Run("search", func(t *testing.T) {
		rs, _ := single.SearchNodes("Handle", nil, 10)
		rm, _ := multi.SearchNodes("Handle", nil, 10)
		if len(rs) != len(rm) {
			t.Errorf("search len %d vs %d", len(rs), len(rm))
		}
	})

	t.Run("node lookup", func(t *testing.T) {
		n, err := multi.GetNodeByID("go:a:Handle")
		if err != nil || n == nil || n.Name != "Handle" {
			t.Fatalf("GetNodeByID: %v %+v", err, n)
		}
		miss, err := multi.GetNodeByID("nope")
		if err != nil || miss != nil {
			t.Errorf("expected nil for missing, got %+v %v", miss, err)
		}
	})

	t.Run("callees", func(t *testing.T) {
		cs, _ := single.GetCallees("go:a:Handle")
		cm, _ := multi.GetCallees("go:a:Handle")
		if len(cs) != len(cm) {
			t.Errorf("callees len %d vs %d", len(cs), len(cm))
		}
	})

	t.Run("files", func(t *testing.T) {
		fs, _ := single.GetFiles()
		fm, _ := multi.GetFiles()
		if len(fs) != len(fm) {
			t.Errorf("files len %d vs %d", len(fs), len(fm))
		}
	})
}

// TestMultiBackendStatsAggregate sums counts and unions language/kind maps.
func TestMultiBackendStatsAggregate(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{scopeGo(t), scopeTS(t)}, root)
	gs, err := multi.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if gs.NodeCount != 4 {
		t.Errorf("NodeCount = %d, want 4", gs.NodeCount)
	}
	if gs.EdgeCount != 2 {
		t.Errorf("EdgeCount = %d, want 2", gs.EdgeCount)
	}
	if gs.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", gs.FileCount)
	}
	if gs.NodesByKind[model.KindFunction] != 4 {
		t.Errorf("NodesByKind[function] = %d, want 4", gs.NodesByKind[model.KindFunction])
	}
	if gs.FilesByLanguage[model.LangGo] != 1 || gs.FilesByLanguage[model.LangTypeScript] != 1 {
		t.Errorf("FilesByLanguage = %+v, want go=1 py=1", gs.FilesByLanguage)
	}
}

// TestMultiBackendSearchMergeDedup: same-named node across scopes both appear
// (distinct IDs), and the merged result respects the limit.
func TestMultiBackendSearchMergeDedup(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{scopeGo(t), scopeTS(t)}, root)
	res, err := multi.SearchNodes("Handle", nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, r := range res {
		seen[r.Node.ID]++
	}
	for id, c := range seen {
		if c > 1 {
			t.Errorf("duplicate id %s appears %d times", id, c)
		}
	}
	// Both scopes have a "Handle"; merged set must include both IDs.
	if _, ok := seen["go:a:Handle"]; !ok {
		t.Errorf("missing go:a:Handle; got %v", seen)
	}
	if _, ok := seen["ts:b:Handle"]; !ok {
		t.Errorf("missing ts:b:Handle; got %v", seen)
	}

	t.Run("limit applied", func(t *testing.T) {
		lim, err := multi.SearchNodes("Handle", nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(lim) != 1 {
			t.Errorf("limit not applied: got %d results", len(lim))
		}
	})
}

// TestMultiBackendNodeLookupFirstResolver: lookups resolve in the scope that
// owns the id and ignore others.
func TestMultiBackendNodeLookupFirstResolver(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{scopeGo(t), scopeTS(t)}, root)
	n, err := multi.GetNodeByID("ts:b:Process")
	if err != nil || n == nil || n.Name != "Process" {
		t.Fatalf("GetNodeByID(ts:b:Process): %v %+v", err, n)
	}
	n2, err := multi.GetNodeByID("go:a:Helper")
	if err != nil || n2 == nil || n2.Name != "Helper" {
		t.Fatalf("GetNodeByID(go:a:Helper): %v %+v", err, n2)
	}
}

// TestMultiBackendTraversalPerScope: callees of a node resolve within its own
// scope; the other scope contributes nothing for that id.
func TestMultiBackendTraversalPerScope(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{scopeGo(t), scopeTS(t)}, root)
	cs, err := multi.GetCallees("go:a:Handle")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Node.Name != "Helper" {
		t.Fatalf("callees of go:a:Handle = %+v, want [Helper]", cs)
	}
}

// TestMultiBackendFilesMergeDedup: files from all scopes are concatenated.
func TestMultiBackendFilesMergeDedup(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{scopeGo(t), scopeTS(t)}, root)
	files, err := multi.GetFiles()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]int{}
	for _, f := range files {
		paths[f.Path]++
	}
	if paths["a.go"] != 1 || paths["b.ts"] != 1 {
		t.Errorf("files = %+v, want a.go=1 b.ts=1", paths)
	}
}

// TestMultiBackendGetNodesByNameMerge: a name present in two scopes returns
// both nodes, deduped by id.
func TestMultiBackendGetNodesByNameMerge(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{scopeGo(t), scopeTS(t)}, root)
	nodes, err := multi.GetNodesByName("Handle")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("GetNodesByName(Handle) = %d nodes, want 2: %+v", len(nodes), nodes)
	}
}

// TestMultiBackendProjectRoot returns the shared root.
func TestMultiBackendProjectRoot(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{scopeGo(t)}, root)
	if multi.GetProjectRoot() != root {
		t.Errorf("GetProjectRoot = %q, want %q", multi.GetProjectRoot(), root)
	}
}

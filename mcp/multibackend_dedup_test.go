package mcp_test

import (
	"fmt"
	"testing"

	"github.com/specscore/codegrapher/mcp"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// TestMultiBackendDedupAcrossDuplicateScopes fans out two stores holding the
// SAME file and node IDs. The merge layer must collapse the duplicates so the
// caller sees each identity once — the defensive de-dup paths.
func TestMultiBackendDedupAcrossDuplicateScopes(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{scopeGo(t), scopeGo(t)}, root)

	files, err := multi.GetFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Errorf("GetFiles deduped = %d, want 1", len(files))
	}

	nodes, err := multi.GetNodesByName("Handle")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Errorf("GetNodesByName deduped = %d, want 1", len(nodes))
	}

	inFile, err := multi.GetNodesInFile("a.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(inFile) != 2 {
		t.Errorf("GetNodesInFile deduped = %d, want 2", len(inFile))
	}

	callees, err := multi.GetCallees("go:a:Handle")
	if err != nil {
		t.Fatal(err)
	}
	if len(callees) != 1 {
		t.Errorf("GetCallees deduped = %d, want 1", len(callees))
	}

	out, err := multi.GetOutgoingEdges("go:a:Handle", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Errorf("GetOutgoingEdges deduped = %d, want 1", len(out))
	}

	search, err := multi.SearchNodes("Handle", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range search {
		if r.Node.ID == "go:a:Handle" {
			// ensure single occurrence
			count := 0
			for _, x := range search {
				if x.Node.ID == "go:a:Handle" {
					count++
				}
			}
			if count != 1 {
				t.Errorf("SearchNodes duplicate go:a:Handle: %d", count)
			}
			break
		}
	}
}

// TestMultiBackendFileDependents exercises the GetFileDependents append/dedup
// path with a real cross-file calls edge, and the duplicate-scope dedup path of
// GetChildren and owningSubgraph's unknown-id fallback.
func TestMultiBackendFileDependents(t *testing.T) {
	// dep.go's Caller calls target.go's Target (a cross-file calls edge), so
	// target.go has dep.go as a dependent.
	nodes := []model.Node{
		fnNode("d:Caller", "Caller", "dep.go", model.LangGo),
		fnNode("d:Target", "Target", "target.go", model.LangGo),
	}
	edges := []model.Edge{{Source: "d:Caller", Target: "d:Target", Kind: model.EdgeCalls}}
	files := []model.FileRecord{
		fileRec("dep.go", model.LangGo, 1),
		fileRec("target.go", model.LangGo, 1),
	}
	s := newStore(t, nodes, edges, files)
	multi := mcp.NewMultiBackend([]*store.Store{s, s}, root)

	deps, err := multi.GetFileDependents("target.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0] != "dep.go" {
		t.Errorf("GetFileDependents(target.go) = %v, want [dep.go]", deps)
	}

	// GetChildren dedup across duplicate scopes.
	rich := scopeRich(t)
	multiRich := mcp.NewMultiBackend([]*store.Store{rich, rich}, root)
	children, err := multiRich.GetChildren("go:c:Child")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 {
		t.Errorf("GetChildren deduped = %d, want 1", len(children))
	}

	// owningSubgraph fallback: no scope owns the id, so the first backend's
	// (empty) subgraph is returned.
	sg, err := multiRich.GetImpactRadius("does-not-exist", 2)
	if err != nil {
		t.Fatal(err)
	}
	if sg.Len() != 0 {
		t.Errorf("GetImpactRadius(unknown) = %d nodes, want 0", sg.Len())
	}
}

// scopeDense builds a Go scope whose single file holds enough in-file edges to
// clear getDominantFile's threshold (>=20), and tags edgeCount via fanCount.
func scopeDense(t *testing.T, file string, fanCount int) *store.Store {
	nodes := []model.Node{
		fnNode("hub:"+file, "Hub", file, model.LangGo),
	}
	var edges []model.Edge
	for i := range fanCount {
		id := fmt.Sprintf("leaf:%s:%d", file, i)
		nodes = append(nodes, fnNode(id, fmt.Sprintf("Leaf%d", i), file, model.LangGo))
		edges = append(edges, model.Edge{Source: "hub:" + file, Target: id, Kind: model.EdgeCalls})
	}
	files := []model.FileRecord{fileRec(file, model.LangGo, len(nodes))}
	return newStore(t, nodes, edges, files)
}

// TestMultiBackendDominantFilePicksDensest selects the file with the most
// in-file edges across scopes.
func TestMultiBackendDominantFilePicksDensest(t *testing.T) {
	multi := mcp.NewMultiBackend([]*store.Store{
		scopeDense(t, "small.go", 22),
		scopeDense(t, "big.go", 40),
	}, root)
	df, err := multi.GetDominantFile()
	if err != nil {
		t.Fatal(err)
	}
	if df == nil {
		t.Fatal("GetDominantFile = nil, want big.go")
	}
	if df.FilePath != "big.go" {
		t.Errorf("dominant = %q (edges=%d), want big.go", df.FilePath, df.EdgeCount)
	}
}

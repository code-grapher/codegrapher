package cli

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/query"
	"github.com/specscore/codegrapher/store"
)

// newMemStore creates an empty in-memory-equivalent (temp-dir) store.
func newMemStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func fn(id, name, file string, line int) model.Node {
	return model.Node{
		ID:            id,
		Kind:          model.KindFunction,
		Name:          name,
		QualifiedName: name,
		FilePath:      file,
		Language:      model.LangGo,
		StartLine:     line,
		EndLine:       line + 3,
	}
}

func fileNode(id, file string) model.Node {
	n := fn(id, file, file, 1)
	n.Kind = model.KindFile
	n.Name = file
	n.QualifiedName = file
	return n
}

func mustInsertNodes(t *testing.T, s *store.Store, nodes ...model.Node) {
	t.Helper()
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
}

func mustInsertEdges(t *testing.T, s *store.Store, edges ...model.Edge) {
	t.Helper()
	if err := s.InsertEdges(edges); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}
}

// --- Callers/Callees merge: disjoint stores both contribute ---

func TestStoreQuerier_Callers_DisjointStores(t *testing.T) {
	// Store A: aCaller -> target (calls).
	sa := newMemStore(t)
	mustInsertNodes(t, sa,
		fn("function:a.go:target", "target", "a.go", 10),
		fn("function:a.go:aCaller", "aCaller", "a.go", 1),
	)
	mustInsertEdges(t, sa, model.Edge{Source: "function:a.go:aCaller", Target: "function:a.go:target", Kind: model.EdgeCalls})

	// Store B: a different "target" with its own caller.
	sb := newMemStore(t)
	mustInsertNodes(t, sb,
		fn("function:b.go:target", "target", "b.go", 20),
		fn("function:b.go:bCaller", "bCaller", "b.go", 2),
	)
	mustInsertEdges(t, sb, model.Edge{Source: "function:b.go:bCaller", Target: "function:b.go:target", Kind: model.EdgeCalls})

	q := NewStoreQuerier(sa, sb)
	res, err := q.Callers("target")
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	names := map[string]bool{}
	for _, c := range res.Callers {
		names[c.Name] = true
	}
	if !names["aCaller"] || !names["bCaller"] {
		t.Fatalf("expected both aCaller and bCaller, got %+v", res.Callers)
	}
	if len(res.Callers) != 2 {
		t.Fatalf("expected 2 callers, got %d: %+v", len(res.Callers), res.Callers)
	}
}

func TestStoreQuerier_Callees_DisjointStores(t *testing.T) {
	sa := newMemStore(t)
	mustInsertNodes(t, sa,
		fn("function:a.go:src", "src", "a.go", 1),
		fn("function:a.go:aCallee", "aCallee", "a.go", 5),
	)
	mustInsertEdges(t, sa, model.Edge{Source: "function:a.go:src", Target: "function:a.go:aCallee", Kind: model.EdgeCalls})

	sb := newMemStore(t)
	mustInsertNodes(t, sb,
		fn("function:b.go:src", "src", "b.go", 1),
		fn("function:b.go:bCallee", "bCallee", "b.go", 5),
	)
	mustInsertEdges(t, sb, model.Edge{Source: "function:b.go:src", Target: "function:b.go:bCallee", Kind: model.EdgeCalls})

	q := NewStoreQuerier(sa, sb)
	res, err := q.Callees("src")
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	if len(res.Callees) != 2 {
		t.Fatalf("expected 2 callees, got %d: %+v", len(res.Callees), res.Callees)
	}
}

// --- Callers dedup: identical SymbolRef across stores collapses ---

func TestStoreQuerier_Callers_DedupAcrossStores(t *testing.T) {
	// Both stores have an identical caller ref (same name/kind/path/line).
	mk := func() *store.Store {
		s := newMemStore(t)
		mustInsertNodes(t, s,
			fn("function:x.go:target", "target", "x.go", 10),
			fn("function:x.go:caller", "caller", "x.go", 1),
		)
		mustInsertEdges(t, s, model.Edge{Source: "function:x.go:caller", Target: "function:x.go:target", Kind: model.EdgeCalls})
		return s
	}
	q := NewStoreQuerier(mk(), mk())
	res, err := q.Callers("target")
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	if len(res.Callers) != 1 {
		t.Fatalf("expected dedup to 1 caller, got %d: %+v", len(res.Callers), res.Callers)
	}
}

// --- Impact merge: counts sum, affected dedups ---

func TestStoreQuerier_Impact_MergeStores(t *testing.T) {
	sa := newMemStore(t)
	mustInsertNodes(t, sa,
		fn("function:a.go:root", "root", "a.go", 1),
		fn("function:a.go:dep", "dep", "a.go", 5),
	)
	mustInsertEdges(t, sa, model.Edge{Source: "function:a.go:dep", Target: "function:a.go:root", Kind: model.EdgeCalls})

	sb := newMemStore(t)
	mustInsertNodes(t, sb,
		fn("function:b.go:root", "root", "b.go", 1),
		fn("function:b.go:dep2", "dep2", "b.go", 5),
	)
	mustInsertEdges(t, sb, model.Edge{Source: "function:b.go:dep2", Target: "function:b.go:root", Kind: model.EdgeCalls})

	q := NewStoreQuerier(sa, sb)
	res, err := q.Impact("root", 3)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	// Each store resolves "root" and contributes its own subgraph.
	if res.NodeCount < 4 {
		t.Fatalf("expected merged NodeCount >= 4, got %d", res.NodeCount)
	}
	if res.EdgeCount < 2 {
		t.Fatalf("expected merged EdgeCount >= 2, got %d", res.EdgeCount)
	}
	if res.Depth != 3 {
		t.Fatalf("expected depth 3, got %d", res.Depth)
	}
}

// --- Files merge: concat across stores, dedup by path ---

func TestStoreQuerier_Files_MergeStores(t *testing.T) {
	sa := newMemStore(t)
	mustInsertNodes(t, sa, fileNode("file:a.go", "a.go"))
	if err := sa.UpsertFile(model.FileRecord{Path: "a.go", Language: model.LangGo, Size: 10, NodeCount: 1}); err != nil {
		t.Fatal(err)
	}
	sb := newMemStore(t)
	mustInsertNodes(t, sb, fileNode("file:b.go", "b.go"))
	if err := sb.UpsertFile(model.FileRecord{Path: "b.go", Language: model.LangGo, Size: 20, NodeCount: 1}); err != nil {
		t.Fatal(err)
	}

	q := NewStoreQuerier(sa, sb)
	files, err := q.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
	}
	if !paths["a.go"] || !paths["b.go"] || len(files) != 2 {
		t.Fatalf("expected a.go and b.go, got %+v", files)
	}
}

// --- Status aggregation: sums and union ---

func TestStoreQuerier_Status_Aggregate(t *testing.T) {
	sa := newMemStore(t)
	mustInsertNodes(t, sa, fn("function:a.go:f", "f", "a.go", 1))
	if err := sa.UpsertFile(model.FileRecord{Path: "a.go", Language: model.LangGo, NodeCount: 1}); err != nil {
		t.Fatal(err)
	}
	sb := newMemStore(t)
	mustInsertNodes(t, sb, fn("function:b.ts:g", "g", "b.ts", 1))
	sb.InsertNodes([]model.Node{{ID: "function:b.ts:g2", Kind: model.KindFunction, Name: "g2", QualifiedName: "g2", FilePath: "b.ts", Language: model.LangTypeScript, StartLine: 5, EndLine: 8}})
	if err := sb.UpsertFile(model.FileRecord{Path: "b.ts", Language: model.LangTypeScript, NodeCount: 2}); err != nil {
		t.Fatal(err)
	}

	q := NewStoreQuerier(sa, sb)
	st, err := q.Status("/proj")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.FileCount != 2 {
		t.Fatalf("FileCount = %d, want 2", st.FileCount)
	}
	if st.NodeCount != 3 {
		t.Fatalf("NodeCount = %d, want 3", st.NodeCount)
	}
	// Languages union, sorted.
	if len(st.Languages) != 2 || st.Languages[0] != "go" || st.Languages[1] != "typescript" {
		t.Fatalf("Languages = %v, want [go typescript]", st.Languages)
	}
	if st.NodesByKind[model.KindFunction] != 3 {
		t.Fatalf("NodesByKind[function] = %d, want 3", st.NodesByKind[model.KindFunction])
	}
}

// --- Search merge: results from both stores, ranked, limited ---

func TestStoreQuerier_Search_MergeAndLimit(t *testing.T) {
	sa := newMemStore(t)
	mustInsertNodes(t, sa,
		fn("function:a.go:handler", "handler", "a.go", 1),
		fn("function:a.go:handlerHelper", "handlerHelper", "a.go", 5),
	)
	sb := newMemStore(t)
	mustInsertNodes(t, sb,
		fn("function:b.go:handlerB", "handlerB", "b.go", 1),
	)

	q := NewStoreQuerier(sa, sb)
	res, err := q.SearchNodes("handler", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	names := map[string]bool{}
	for _, r := range res {
		names[r.Node.Name] = true
	}
	if !names["handler"] || !names["handlerB"] {
		t.Fatalf("expected results from both stores, got %v", names)
	}

	// Limit must be respected after merge.
	res2, err := q.SearchNodes("handler", SearchOptions{Limit: 1})
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	if len(res2) != 1 {
		t.Fatalf("expected 1 result with Limit=1, got %d", len(res2))
	}
}

// --- Single-store equivalence: fan-out over one store == direct query ---

func TestStoreQuerier_SingleStoreEquivalence(t *testing.T) {
	s := newMemStore(t)
	mustInsertNodes(t, s,
		fn("function:m.go:target", "target", "m.go", 10),
		fn("function:m.go:caller", "caller", "m.go", 1),
		fn("function:m.go:callee", "callee", "m.go", 20),
	)
	mustInsertEdges(t, s,
		model.Edge{Source: "function:m.go:caller", Target: "function:m.go:target", Kind: model.EdgeCalls},
		model.Edge{Source: "function:m.go:target", Target: "function:m.go:callee", Kind: model.EdgeCalls},
	)
	if err := s.UpsertFile(model.FileRecord{Path: "m.go", Language: model.LangGo, NodeCount: 3, Size: 99}); err != nil {
		t.Fatal(err)
	}

	q := NewStoreQuerier(s)

	// Search.
	got, err := q.SearchNodes("target", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	want, err := query.SearchNodes(s, "target", query.SearchOptions{Limit: 5})
	if err != nil {
		t.Fatalf("query.SearchNodes: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("search len mismatch: got %d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Node.ID != want[i].Node.ID || got[i].Score != want[i].Score {
			t.Fatalf("search[%d] mismatch: got %+v want %+v", i, got[i], want[i])
		}
	}

	// Callers.
	gotC, _ := q.Callers("target")
	wantC, _ := query.Callers(s, "target")
	if len(gotC.Callers) != len(wantC.Callers) {
		t.Fatalf("callers len mismatch: got %d want %d", len(gotC.Callers), len(wantC.Callers))
	}

	// Callees.
	gotCe, _ := q.Callees("target")
	wantCe, _ := query.Callees(s, "target")
	if len(gotCe.Callees) != len(wantCe.Callees) {
		t.Fatalf("callees len mismatch: got %d want %d", len(gotCe.Callees), len(wantCe.Callees))
	}

	// Impact.
	gotI, _ := q.Impact("target", 2)
	wantI, _ := query.Impact(s, "target", 2)
	if gotI.NodeCount != wantI.NodeCount || gotI.EdgeCount != wantI.EdgeCount || gotI.Depth != wantI.Depth {
		t.Fatalf("impact mismatch: got %+v want %+v", gotI, wantI)
	}

	// Files.
	gotF, _ := q.Files()
	wantF, _ := query.Files(s)
	if len(gotF) != len(wantF) {
		t.Fatalf("files len mismatch: got %d want %d", len(gotF), len(wantF))
	}

	// Status.
	gotS, _ := q.Status("/proj")
	wantS, _ := query.Status(s, "/proj")
	if gotS.FileCount != wantS.FileCount || gotS.NodeCount != wantS.NodeCount || gotS.EdgeCount != wantS.EdgeCount {
		t.Fatalf("status counts mismatch: got %+v want %+v", gotS, wantS)
	}
}

// --- Zero stores: no panics, empty results ---

func TestStoreQuerier_ZeroStores(t *testing.T) {
	q := NewStoreQuerier()
	if res, err := q.SearchNodes("x", SearchOptions{Limit: 5}); err != nil || len(res) != 0 {
		t.Fatalf("SearchNodes zero-store: res=%v err=%v", res, err)
	}
	if c, err := q.Callers("x"); err != nil || len(c.Callers) != 0 {
		t.Fatalf("Callers zero-store: %+v err=%v", c, err)
	}
	if c, err := q.Callees("x"); err != nil || len(c.Callees) != 0 {
		t.Fatalf("Callees zero-store: %+v err=%v", c, err)
	}
	if i, err := q.Impact("x", 2); err != nil || len(i.Affected) != 0 {
		t.Fatalf("Impact zero-store: %+v err=%v", i, err)
	}
	if f, err := q.Files(); err != nil || len(f) != 0 {
		t.Fatalf("Files zero-store: %v err=%v", f, err)
	}
	if s, err := q.Status("/p"); err != nil || s.FileCount != 0 || s.NodesByKind == nil {
		t.Fatalf("Status zero-store: %+v err=%v", s, err)
	}
}

// --- splitCSV helper ---

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"go-1.22", []string{"go-1.22"}},
		{"go-1.22,typescript-5.4", []string{"go-1.22", "typescript-5.4"}},
		{" go-1.22 , , typescript-5.4 ,", []string{"go-1.22", "typescript-5.4"}},
	}
	for _, c := range cases {
		got := splitCSV(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("splitCSV(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("splitCSV(%q) = %v, want %v", c.in, got, c.want)
			}
		}
	}
}

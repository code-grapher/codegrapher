package cli

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

// When one store holds the EXACT symbol and another store holds only a
// substring/fuzzy match (e.g. "target" fuzzily matching "targetHelper"), the
// per-scope fan-out must not leak the fuzzy store's results. This mirrors the
// real bug where impact/callees of "get" picked up "Widget" ("Wid-get") from a
// different scope. See StoreQuerier.storesForSymbol.

func TestStoreQuerier_Callers_NoSubstringLeakAcrossStores(t *testing.T) {
	// Store A: the EXACT symbol "target" with a real caller.
	sa := newMemStore(t)
	mustInsertNodes(t, sa,
		fn("function:a.go:target", "target", "a.go", 10),
		fn("function:a.go:aCaller", "aCaller", "a.go", 1),
	)
	mustInsertEdges(t, sa, model.Edge{Source: "function:a.go:aCaller", Target: "function:a.go:target", Kind: model.EdgeCalls})

	// Store B: NO exact "target" — only the substring match "targetHelper".
	sb := newMemStore(t)
	mustInsertNodes(t, sb,
		fn("function:b.go:targetHelper", "targetHelper", "b.go", 20),
		fn("function:b.go:bCaller", "bCaller", "b.go", 2),
	)
	mustInsertEdges(t, sb, model.Edge{Source: "function:b.go:bCaller", Target: "function:b.go:targetHelper", Kind: model.EdgeCalls})

	q := NewStoreQuerier(sa, sb)
	res, err := q.Callers("target")
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	names := map[string]bool{}
	for _, c := range res.Callers {
		names[c.Name] = true
	}
	if !names["aCaller"] {
		t.Errorf("expected aCaller (caller of the exact 'target'), got %+v", res.Callers)
	}
	if names["bCaller"] {
		t.Errorf("bCaller leaked from store B via substring match 'targetHelper'; got %+v", res.Callers)
	}
}

func TestStoreQuerier_Impact_NoSubstringLeakAcrossStores(t *testing.T) {
	sa := newMemStore(t)
	mustInsertNodes(t, sa,
		fn("function:a.go:target", "target", "a.go", 10),
		fn("function:a.go:aCaller", "aCaller", "a.go", 1),
	)
	mustInsertEdges(t, sa, model.Edge{Source: "function:a.go:aCaller", Target: "function:a.go:target", Kind: model.EdgeCalls})

	sb := newMemStore(t)
	mustInsertNodes(t, sb,
		fn("function:b.go:targetHelper", "targetHelper", "b.go", 20),
		fn("function:b.go:bCaller", "bCaller", "b.go", 2),
	)
	mustInsertEdges(t, sb, model.Edge{Source: "function:b.go:bCaller", Target: "function:b.go:targetHelper", Kind: model.EdgeCalls})

	q := NewStoreQuerier(sa, sb)
	res, err := q.Impact("target", 2)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	for _, ref := range res.Affected {
		if ref.Name == "targetHelper" || ref.Name == "bCaller" {
			t.Errorf("store B leaked %q into impact of exact 'target'; got %+v", ref.Name, res.Affected)
		}
	}
}

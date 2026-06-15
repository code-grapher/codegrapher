package resolve_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

// TestSpecScoreResolution builds a small SpecScore spec tree whose artifacts
// cross-reference each other, runs extraction + resolve, and asserts the
// resulting promotes_to / depends_on / references edges land between the right
// artifact nodes — and that an unresolvable target produces no edge.
//
// Covers:
//   - idea Promotes To <feature-slug>  → promotes_to edge (bare-slug target)
//   - feature Dependencies → <other feature> → depends_on edge (slug target,
//     the feature parser collapses a relative link to a bare slug)
//   - idea Related Ideas via a relative markdown link → references edge
//     (relative-path target resolved against the source artifact's directory)
//   - idea Promotes To a slug that does not exist in the repo → no edge
func TestSpecScoreResolution(t *testing.T) {
	dir := t.TempDir()

	// spec/features/checkout/README.md — a feature (slug = "checkout").
	checkoutPath := filepath.Join(dir, "spec", "features", "checkout", "README.md")
	writeFile(t, checkoutPath, `---
format: https://specscore.md/feature-specification
---
# Feature: Checkout

**Status:** Approved

## Dependencies

- [cart](../cart/README.md)
`)

	// spec/features/cart/README.md — a feature (slug = "cart"), depended on above.
	cartPath := filepath.Join(dir, "spec", "features", "cart", "README.md")
	writeFile(t, cartPath, `---
format: https://specscore.md/feature-specification
---
# Feature: Cart

**Status:** Approved
`)

	// spec/ideas/payments.md — an idea that promotes to the checkout feature
	// (bare slug), relates to another idea via a relative link, and promotes to a
	// non-existent slug (must stay unresolved).
	ideaPath := filepath.Join(dir, "spec", "ideas", "payments.md")
	writeFile(t, ideaPath, `---
format: https://specscore.md/idea-specification
status: Approved
---
# Idea: Payments

**Status:** Approved
**Promotes To:** checkout, ghost-feature
**Related Ideas:** ./refunds.md
`)

	// spec/ideas/refunds.md — the related idea targeted by the relative link.
	refundsPath := filepath.Join(dir, "spec", "ideas", "refunds.md")
	writeFile(t, refundsPath, `---
format: https://specscore.md/idea-specification
status: Approved
---
# Idea: Refunds

**Status:** Draft
`)

	s, err := store.Initialize(filepath.Join(dir, store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	insert := func(path string) {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		res, err := extract.ExtractFile(path, content, model.LangSpecScore)
		if err != nil {
			t.Fatalf("extract %s: %v", path, err)
		}
		if err := s.InsertNodes(res.Nodes); err != nil {
			t.Fatalf("insert nodes: %v", err)
		}
		if err := s.InsertEdges(res.Edges); err != nil {
			t.Fatalf("insert edges: %v", err)
		}
		if err := s.InsertUnresolvedRefs(res.UnresolvedReferences); err != nil {
			t.Fatalf("insert refs: %v", err)
		}
	}
	insert(checkoutPath)
	insert(cartPath)
	insert(ideaPath)
	insert(refundsPath)

	if _, err := resolve.Resolve(s, dir); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	checkoutID := nodeID(t, s, model.KindFeature, "checkout")
	cartID := nodeID(t, s, model.KindFeature, "cart")
	paymentsID := nodeID(t, s, model.KindIdea, "payments")
	refundsID := nodeID(t, s, model.KindIdea, "refunds")

	// idea payments --promotes_to--> feature checkout
	if !sqliteHasEdge(t, s, paymentsID, checkoutID, model.EdgePromotesTo) {
		t.Errorf("missing promotes_to edge: payments → checkout")
	}
	// feature checkout --depends_on--> feature cart
	if !sqliteHasEdge(t, s, checkoutID, cartID, model.EdgeDependsOn) {
		t.Errorf("missing depends_on edge: checkout → cart")
	}
	// idea payments --references--> idea refunds (via relative link)
	if !sqliteHasEdge(t, s, paymentsID, refundsID, model.EdgeReferences) {
		t.Errorf("missing references edge: payments → refunds (relative link)")
	}

	// The "ghost-feature" promotes-to target does not exist → no edge at all.
	for _, e := range outgoing(t, s, paymentsID) {
		if e.Kind == model.EdgePromotesTo && e.Target != checkoutID {
			t.Errorf("unexpected promotes_to edge to unresolved target: %s", e.Target)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func outgoing(t *testing.T, s *store.Store, source string) []model.Edge {
	t.Helper()
	edges, err := s.GetOutgoingEdges(source, nil, "")
	if err != nil {
		t.Fatalf("GetOutgoingEdges: %v", err)
	}
	return edges
}

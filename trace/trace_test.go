package trace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

func TestBuildAttachesTypedLinksAndRejectsNonExecutableVerification(t *testing.T) {
	root := t.TempDir()
	featureDir := filepath.Join(root, "spec", "features", "checkout")
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	feature := `---
format: https://specscore.md/feature-specification
status: Implementing
---

# Feature: Checkout

**Status:** Implementing

## Behavior

#### REQ: totals

The total is calculated.

## Acceptance Criteria

### AC: total-visible

The total is visible.
`
	if err := os.WriteFile(filepath.Join(featureDir, "README.md"), []byte(feature), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "checkout.go")
	if err := os.WriteFile(source, []byte(`package checkout

// specscore:implements feature/checkout#REQ:totals
func Calculate() {}

// specscore:verifies feature/checkout#AC:total-visible
func Helper() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Initialize(filepath.Join(root, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	calculateID := model.GenerateNodeID("checkout.go", model.KindFunction, "Calculate", 4)
	helperID := model.GenerateNodeID("checkout.go", model.KindFunction, "Helper", 7)
	if err := st.InsertNodes([]model.Node{
		{ID: calculateID, Kind: model.KindFunction, Name: "Calculate", QualifiedName: "Calculate", FilePath: "checkout.go", Language: model.LangGo, StartLine: 4, EndLine: 4},
		{ID: helperID, Kind: model.KindFunction, Name: "Helper", QualifiedName: "Helper", FilePath: "checkout.go", Language: model.LangGo, StartLine: 7, EndLine: 7},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFile(model.FileRecord{Path: "checkout.go", Language: model.LangGo}); err != nil {
		t.Fatal(err)
	}

	nodes, edges, err := Build(root, []*store.Store{st})
	if err != nil {
		t.Fatal(err)
	}
	var implements, verifies int
	for _, edge := range edges {
		if edge.Accepted && edge.Relation == "implements" {
			implements++
		}
		if edge.Accepted && edge.Relation == "verifies" {
			verifies++
		}
	}
	if implements != 1 {
		t.Fatalf("accepted implements links = %d, want 1; edges=%+v", implements, edges)
	}
	if verifies != 0 {
		t.Fatalf("accepted verifies links = %d, want 0", verifies)
	}
	if len(nodes) < 4 {
		t.Fatalf("nodes = %d, want feature, req, ac, and symbols", len(nodes))
	}
}

func TestIndexAndQuerySeparatesImplementationVerificationAndCoverage(t *testing.T) {
	root := t.TempDir()
	featureDir := filepath.Join(root, "spec", "features", "checkout")
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featureDir, "README.md"), []byte("---\nformat: https://specscore.md/feature-specification\nstatus: Implementing\n---\n# Feature: Checkout\n\n## Behavior\n\n#### REQ: totals\n\nTotals are calculated.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "checkout_test.go"), []byte("package checkout\n\n// specscore:verifies feature/checkout#REQ:totals\nfunc TestTotals() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Initialize(filepath.Join(root, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	id := model.GenerateNodeID("checkout_test.go", model.KindFunction, "TestTotals", 4)
	if err := st.InsertNode(model.Node{ID: id, Kind: model.KindFunction, Name: "TestTotals", QualifiedName: "TestTotals", FilePath: "checkout_test.go", Language: model.LangGo, StartLine: 4, EndLine: 4}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFile(model.FileRecord{Path: "checkout_test.go", Language: model.LangGo}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutNodeCoverage([]store.NodeCoverageRow{{NodeID: id, ContentHash: "hash", LinesCovered: 1, RunAt: 7}}); err != nil {
		t.Fatal(err)
	}
	projection, err := store.Initialize(filepath.Join(root, "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = projection.Close() }()
	if err := Index(root, []*store.Store{st}, projection); err != nil {
		t.Fatal(err)
	}
	result, err := Query("feature/checkout#req:totals", projection, []*store.Store{st}, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Implements) != 0 || len(result.Verifies) != 1 {
		t.Fatalf("links = implements %d verifies %d", len(result.Implements), len(result.Verifies))
	}
	if result.Verifies[0].Coverage == nil || !result.Verifies[0].Coverage.Available {
		t.Fatalf("verification coverage = %+v", result.Verifies[0].Coverage)
	}
	if result.Version != ContractVersion || result.IndexedRevision != "" {
		t.Fatalf("result metadata = %+v", result)
	}
}

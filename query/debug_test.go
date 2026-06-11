package query_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
)

func TestDebugImpact(t *testing.T) {
	if os.Getenv("DEBUG_IMPACT") == "" {
		t.Skip("set DEBUG_IMPACT=1 to run")
	}
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "go-small")
	s := buildStore(t, fixtureDir)

	// Find Store::Get node
	nodes, err := s.GetNodesByQualifiedNameExact("Store::Get")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		fmt.Printf("Found: %s %s %s line=%d\n", n.ID[:30], n.Kind, n.QualifiedName, n.StartLine)
	}

	if len(nodes) == 0 {
		t.Fatal("no Store::Get found")
	}
	nodeID := nodes[0].ID

	// Check incoming edges
	incoming, err := s.GetIncomingEdges(nodeID, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\nIncoming edges to Store::Get (%s):\n", nodeID[:20])
	for _, e := range incoming {
		fmt.Printf("  %s %s <- %s [prov=%s]\n", e.Kind, e.Target[:20], e.Source[:20], e.Provenance)
	}

	// Check all edges in the store
	fmt.Printf("\nAll non-contains edges:\n")
	allNodes, _ := s.GetNodesByFile("internal/store/store.go")
	for _, n := range allNodes {
		out, _ := s.GetOutgoingEdges(n.ID, nil, "")
		for _, e := range out {
			if e.Kind != model.EdgeContains {
				fmt.Printf("  %s %s(%s) -> %s [prov=%s]\n", e.Kind, n.Name, n.ID[:15], e.Target[:20], e.Provenance)
			}
		}
		inc, _ := s.GetIncomingEdges(n.ID, nil)
		for _, e := range inc {
			if e.Kind != model.EdgeContains {
				fmt.Printf("  <- %s %s(%s) %s [prov=%s]\n", e.Kind, n.Name, n.ID[:15], e.Source[:20], e.Provenance)
			}
		}
	}
}

func TestDebugFTS(t *testing.T) {
	if os.Getenv("DEBUG_FTS") == "" {
		t.Skip("set DEBUG_FTS=1 to run")
	}
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "go-small")
	s := buildStore(t, fixtureDir)

	results, err := s.SearchFTS("store", nil, nil, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("FTS results for 'store' (%d total):\n", len(results))
	for i, r := range results {
		fmt.Printf("  [%d] score=%.4f kind=%s name=%s qualified=%s\n",
			i, r.Score, r.Node.Kind, r.Node.Name, r.Node.QualifiedName)
	}
}

func TestDebugFTS2(t *testing.T) {
	if os.Getenv("DEBUG_FTS2") == "" {
		t.Skip("set DEBUG_FTS2=1 to run")
	}
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "go-small")
	s := buildStore(t, fixtureDir)

	// Try different FTS queries
	queries := []string{"store*", `"store"*`, "store", "Store", `"Store"*`}
	for _, q := range queries {
		results, err := s.SearchFTS(q, nil, nil, 100, 0)
		fmt.Printf("Query %q: err=%v results=%d\n", q, err, len(results))
	}

	// Check nodes count
	allNodes, _ := s.GetNodesByFile("internal/store/store.go")
	fmt.Printf("Nodes in store.go: %d\n", len(allNodes))
	for _, n := range allNodes {
		fmt.Printf("  kind=%s name=%s qualified=%s docstring=%s\n", n.Kind, n.Name, n.QualifiedName, n.Docstring)
	}
}

func TestDebugFTSSearch(t *testing.T) {
	if os.Getenv("DEBUG_FTS_SEARCH") == "" {
		t.Skip("set DEBUG_FTS_SEARCH=1 to run")
	}
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "go-small")
	s := buildStore(t, fixtureDir)

	// Check what SearchFTS returns (direct call without buildFTSQuery)
	// Try raw SearchFTS
	results, err := s.SearchFTS("Store", nil, nil, 100, 0)
	fmt.Printf("SearchFTS('Store'): err=%v results=%d\n", err, len(results))

	results, err = s.SearchFTS("Get", nil, nil, 100, 0)
	fmt.Printf("SearchFTS('Get'): err=%v results=%d\n", err, len(results))

	// Try SearchLike
	likeResults, err := s.SearchLike("Store", nil, nil, 100, 0)
	fmt.Printf("SearchLike('Store'): err=%v results=%d\n", err, len(likeResults))

	// Check node count
	allNodes, _ := s.GetNodesByName("Store")
	fmt.Printf("GetNodesByName('Store'): %d\n", len(allNodes))

	// FTS count via raw nodes query
	all, _ := s.GetNodesByFile("internal/store/store.go")
	fmt.Printf("Nodes in store.go: %d\n", len(all))
}

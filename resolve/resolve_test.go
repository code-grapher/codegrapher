package resolve_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

const repoRoot = ".."

// goldenEdge mirrors the JSON format used in the golden resolution-edges file.
type goldenEdge struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Kind       string  `json:"kind"`
	Provenance *string `json:"provenance"`
	Line       int     `json:"line"`
	Col        *int    `json:"col"`
}

// edgeKey returns a stable sort key for an edge.
func edgeKey(e model.Edge) string {
	return e.Source + "|" + e.Target + "|" + string(e.Kind)
}

func goldenEdgeKey(e goldenEdge) string {
	return e.Source + "|" + e.Target + "|" + e.Kind
}

// TestResolutionParityGoSmall extracts go-small, resolves, and compares the
// non-heuristic edges against the golden.
func TestResolutionParityGoSmall(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "go-small")
	goldenFile := filepath.Join(repoRoot, "testdata", "golden", "go-small", "resolution-edges.json")

	// Load golden edges, keeping only non-heuristic ones.
	rawGolden, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var allGolden []goldenEdge
	if err := json.Unmarshal(rawGolden, &allGolden); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	goldenNonHeuristic := make(map[string]goldenEdge)
	for _, g := range allGolden {
		if g.Provenance != nil && *g.Provenance == "heuristic" {
			continue
		}
		goldenNonHeuristic[goldenEdgeKey(g)] = g
	}

	// Build an in-memory store.
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// Extract all Go files from the fixture.
	err = filepath.Walk(fixtureDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		lang := extract.DetectLanguage(path)
		if lang == model.LangUnknown {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(fixtureDir, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		result, err := extract.ExtractFile(relPath, content, lang)
		if err != nil {
			return err
		}
		if err := s.InsertNodes(result.Nodes); err != nil {
			return err
		}
		if err := s.InsertEdges(result.Edges); err != nil {
			return err
		}
		if err := s.InsertUnresolvedRefs(result.UnresolvedReferences); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixture: %v", err)
	}

	// Run the resolver.
	stats, err := resolve.Resolve(s, fixtureDir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	t.Logf("resolved=%d unresolved=%d", stats.Resolved, stats.Unresolved)

	// Collect all non-contains edges from the store.
	// We query all nodes, then collect their outgoing edges, deduplicating.
	gotEdges := map[string]model.Edge{}
	// Use a query approach: fetch all edges by iterating nodes.
	// Since the store doesn't have a "get all edges" method, we collect via
	// all node IDs, but that would be O(n²). Instead, use FindEdgesBetweenNodes
	// with all node IDs to get intra-project edges.
	//
	// Simpler: query the DB directly via exported methods.
	// The store exposes GetOutgoingEdges per node. We'll iterate file nodes
	// and collect all outgoing edges by doing a broad sweep.
	//
	// Actually the simplest approach: for each golden edge source, query outgoing.
	// But we also want to catch extra edges. Instead, collect from all known node IDs.
	allNodeIDs := collectAllNodeIDs(t, s, fixtureDir)
	for _, id := range allNodeIDs {
		edges, err := s.GetOutgoingEdges(id, nil, "")
		if err != nil {
			t.Fatalf("GetOutgoingEdges(%s): %v", id, err)
		}
		for _, e := range edges {
			if e.Kind == model.EdgeContains {
				continue // ignore contains edges (tested by extraction parity)
			}
			gotEdges[edgeKey(e)] = e
		}
	}

	// Sort got edge keys for deterministic output.
	var gotKeys []string
	for k := range gotEdges {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)

	// Check: every golden non-heuristic edge is present.
	t.Run("golden_edges_present", func(t *testing.T) {
		for key, g := range goldenNonHeuristic {
			if _, ok := gotEdges[key]; !ok {
				t.Errorf("missing golden edge: %s → %s kind=%s", g.Source, g.Target, g.Kind)
			}
		}
	})

	// Check: no extra non-contains edges that aren't in golden (excluding heuristic).
	t.Run("no_extra_edges", func(t *testing.T) {
		for _, key := range gotKeys {
			e := gotEdges[key]
			if _, ok := goldenNonHeuristic[key]; !ok {
				t.Errorf("extra edge not in golden: %s → %s kind=%s", e.Source, e.Target, e.Kind)
			}
		}
	})
}

// collectAllNodeIDs returns all node IDs in the store by reading the extraction
// golden, since the store doesn't have a "list all node IDs" method.
// We use the fixture files to reconstruct them.
func collectAllNodeIDs(t *testing.T, s *store.Store, fixtureDir string) []string {
	t.Helper()
	var ids []string
	err := filepath.Walk(fixtureDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		lang := extract.DetectLanguage(path)
		if lang == model.LangUnknown {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(fixtureDir, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)
		result, err := extract.ExtractFile(relPath, content, lang)
		if err != nil {
			return err
		}
		for _, n := range result.Nodes {
			ids = append(ids, n.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("collectAllNodeIDs walk: %v", err)
	}
	return ids
}

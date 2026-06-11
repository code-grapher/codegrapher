package extract

// TestOracleGoSmall_Differential runs both the go/parser walk (primary) and the
// gotreesitter walk (oracle) over every .go file in testdata/fixtures/go-small,
// asserting that both produce identical node IDs, kinds, names, start lines,
// contains edges, and unresolved-ref sets.
//
// Files where gotreesitter returns an ERROR or HasError root are skipped with a
// note — the gotreesitter Go grammar has known parsing bugs on certain patterns
// (e.g. []struct{...} anonymous struct slices in function bodies) that do not
// affect the standard library parser. Skipping those keeps the oracle honest:
// we only compare on files where both parsers agree on the AST shape.
//
// ADR-003: go/parser is primary; walkGo (gotreesitter) is the test oracle.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

const oracleRepoRoot = "../.."

func TestOracleGoSmall_Differential(t *testing.T) {
	runOracleDiff(t, filepath.Join(oracleRepoRoot, "testdata", "fixtures", "go-small"))
}

// runOracleDiff runs the differential oracle over every .go file in dir.
func runOracleDiff(t *testing.T, dir string) {
	t.Helper()
	var goFiles []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if filepath.Ext(path) == ".go" {
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk dir: %v", err)
	}

	skipped := 0
	for _, absPath := range goFiles {
		relPath, _ := filepath.Rel(dir, absPath)
		relPath = filepath.ToSlash(relPath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}

		t.Run(relPath, func(t *testing.T) {
			// --- Oracle walk: gotreesitter ---
			tsNodes, tsEdges, tsRefs, ok := oracleWalkGo(t, relPath, content)
			if !ok {
				t.Skipf("gotreesitter ERROR/HasError root — skipping oracle diff for %s", relPath)
				skipped++
				return
			}

			// --- Primary walk: go/parser ---
			primary := &extractor{
				filePath: relPath,
				content:  string(content),
				lang:     model.LangGo,
			}
			primary.emitFileNode(nil) // no tree for go/parser path
			primary.walkGoFallback(content, true)

			primaryNodes := nodeSet(primary.nodes)
			primaryEdges := containsEdgeSet(primary.edges)
			primaryRefs := refSet(primary.unresolvedRefs)

			// --- Node comparison ---
			for id, tn := range tsNodes {
				pn, ok := primaryNodes[id]
				if !ok {
					t.Errorf("primary missing node ID %s (kind=%s name=%s line=%d)", id, tn.kind, tn.name, tn.line)
					continue
				}
				if pn.kind != tn.kind {
					t.Errorf("node %s kind: primary=%s oracle=%s", id, pn.kind, tn.kind)
				}
				if pn.name != tn.name {
					t.Errorf("node %s name: primary=%q oracle=%q", id, pn.name, tn.name)
				}
				if pn.line != tn.line {
					t.Errorf("node %s startLine: primary=%d oracle=%d", id, pn.line, tn.line)
				}
			}
			for id, pn := range primaryNodes {
				if _, ok := tsNodes[id]; !ok {
					t.Errorf("primary has extra node ID %s (kind=%s name=%s line=%d)", id, pn.kind, pn.name, pn.line)
				}
			}

			// --- Contains edge comparison ---
			for k := range tsEdges {
				if !primaryEdges[k] {
					t.Errorf("primary missing contains edge %s", k)
				}
			}
			for k := range primaryEdges {
				if !tsEdges[k] {
					t.Errorf("primary has extra contains edge %s", k)
				}
			}

			// --- Unresolved refs comparison ---
			// Sort and compare. We only check that the SETS are identical
			// (same from-node, name, kind triples) since line/col may differ
			// between the two parsers for complex expressions.
			for k := range tsRefs {
				if !primaryRefs[k] {
					t.Errorf("primary missing unresolved ref %s", k)
				}
			}
			for k := range primaryRefs {
				if !tsRefs[k] {
					t.Errorf("primary has extra unresolved ref %s", k)
				}
			}
		})
	}

	if skipped > 0 {
		t.Logf("skipped %d files where gotreesitter returned ERROR/HasError root", skipped)
	}
}

type nodeEntry struct {
	kind, name string
	line       int
}

func nodeSet(nodes []model.Node) map[string]nodeEntry {
	m := make(map[string]nodeEntry, len(nodes))
	for _, n := range nodes {
		m[n.ID] = nodeEntry{string(n.Kind), n.Name, n.StartLine}
	}
	return m
}

func containsEdgeSet(edges []model.Edge) map[string]bool {
	m := make(map[string]bool)
	for _, e := range edges {
		if e.Kind == model.EdgeContains {
			m[e.Source+"->"+e.Target] = true
		}
	}
	return m
}

func refSet(refs []model.UnresolvedReference) map[string]bool {
	m := make(map[string]bool)
	for _, r := range refs {
		key := r.FromNodeID + "|" + r.ReferenceName + "|" + string(r.ReferenceKind)
		m[key] = true
	}
	return m
}

// oracleWalkGo runs the gotreesitter Go walk on content and returns the
// (nodes, containsEdges, refs, ok) tuple. ok=false when the root has errors
// (gotreesitter bug on certain Go files).
func oracleWalkGo(t *testing.T, path string, content []byte) (map[string]nodeEntry, map[string]bool, map[string]bool, bool) {
	t.Helper()

	p, err := tsparse.NewParser(tsparse.LangGo)
	if err != nil {
		t.Fatalf("tsparse.NewParser: %v", err)
	}

	type result struct {
		tree *tsparse.Tree
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		tree, err := p.Parse(content)
		ch <- result{tree, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil || r.tree == nil {
			return nil, nil, nil, false
		}
		root := r.tree.RootNode()
		if root.Kind() == "ERROR" || root.HasError() {
			return nil, nil, nil, false
		}
		e := &extractor{
			filePath:         path,
			content:          string(content),
			lang:             model.LangGo,
			commentByEndLine: buildCommentIndex(root),
		}
		e.emitFileNode(r.tree)
		e.walkGo(root)
		return nodeSet(e.nodes), containsEdgeSet(e.edges), refSet(e.unresolvedRefs), true
	case <-time.After(30 * time.Second):
		t.Logf("gotreesitter timeout on %s — treating as HasError", path)
		return nil, nil, nil, false
	}
}

// sortedKeys returns sorted keys of a string set (for stable error messages).
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

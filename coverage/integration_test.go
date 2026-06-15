package coverage_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/coverage"
	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
)

// TestIngest_AgainstIndexedFixture indexes the go-small fixture, then ingests a
// synthetic profile covering a real function's lines and asserts the resulting
// coverage + node_coverage rows.
func TestIngest_AgainstIndexedFixture(t *testing.T) {
	repoRoot := func() string {
		abs, _ := filepath.Abs("..")
		return abs
	}()
	src := filepath.Join(repoRoot, "testdata", "fixtures", "go-small")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fixture missing: %v", err)
	}

	tmp := t.TempDir()
	if err := copyTree(src, tmp); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	t.Setenv("CODEGRAPH_NO_WATCH", "1")

	idx, _, err := indexer.Init(tmp, indexer.Options{})
	if err != nil {
		t.Fatalf("indexer.Init: %v", err)
	}
	defer func() { _ = idx.Close() }()

	goStores := idx.StoresFiltered([]string{"go-v1"})
	if len(goStores) != 1 {
		t.Fatalf("expected 1 go store, got %d", len(goStores))
	}
	st := goStores[0]

	// Find a function node in internal/store/store.go to target.
	nodes, err := st.GetNodesByFile("internal/store/store.go")
	if err != nil {
		t.Fatal(err)
	}
	var target *model.Node
	for i := range nodes {
		k := nodes[i].Kind
		if (k == model.KindFunction || k == model.KindMethod) && nodes[i].EndLine-nodes[i].StartLine >= 2 {
			target = &nodes[i]
			break
		}
	}
	if target == nil {
		t.Fatal("no multi-line function/method node in store.go")
	}

	// Build a profile: cover target's first body line as hit, second as miss.
	hit := target.StartLine + 1
	miss := target.StartLine + 2
	profile := "mode: set\n" +
		fmt.Sprintf("example.com/go-small/internal/store/store.go:%d.1,%d.10 1 1\n", hit, hit) +
		fmt.Sprintf("example.com/go-small/internal/store/store.go:%d.1,%d.10 1 0\n", miss, miss)

	sum, err := coverage.NewIngestor().Ingest(context.Background(), st,
		strings.NewReader(profile), coverage.Options{Root: tmp, Now: func() int64 { return 42 }})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if sum.FilesMatched != 1 {
		t.Errorf("FilesMatched = %d, want 1", sum.FilesMatched)
	}
	if sum.LinesCovered != 1 || sum.LinesUncovered != 1 || sum.PctCovered != 50 {
		t.Errorf("summary = %+v, want 1 cov / 1 unc / 50%%", sum)
	}

	cov, err := st.GetCoverageByFile("internal/store/store.go")
	if err != nil || cov == nil {
		t.Fatalf("file coverage missing: %v / %v", cov, err)
	}
	if cov.RunAt != 42 || cov.Mode != "set" || cov.ContentHash == "" {
		t.Errorf("file coverage stamp wrong: %+v", cov)
	}

	nodeCov, err := st.GetAllNodeCoverage()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range nodeCov {
		if r.NodeID == target.ID {
			found = true
			if r.LinesCovered != 1 || r.LinesUncovered != 1 {
				t.Errorf("target node coverage = %+v, want 1/1", r)
			}
		}
	}
	if !found {
		t.Errorf("target node %s has no coverage row", target.ID)
	}

	// Recordset round-trip from the store.
	fc, err := coverage.FileCoverageFromStore(st)
	if err != nil || len(fc) != 1 {
		t.Fatalf("FileCoverageFromStore = %d (%v)", len(fc), err)
	}
	if fc[0].PctCovered != 50 {
		t.Errorf("recordset pct = %v, want 50", fc[0].PctCovered)
	}
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if strings.Contains(rel, ".codegraph") {
			return nil
		}
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, fi.Mode())
	})
}

package snapshot_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/coverage"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/snapshot"
	"github.com/specscore/codegrapher/store"
)

// ingestSampleCoverage indexes go-small, ingests a small profile into the scope
// DB, and returns the project root + db path.
func ingestSampleCoverage(t *testing.T) (root, dbPath string) {
	t.Helper()
	fixture := goSmallFixture(t)
	root, dbPath = indexFixture(t, fixture)

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	nodes, err := s.GetNodesByFile("internal/store/store.go")
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
		t.Fatal("no multi-line func in store.go")
	}
	profile := "mode: set\n" +
		profLine(target.StartLine+1, true) +
		profLine(target.StartLine+2, false)

	_, err = coverage.NewIngestor().Ingest(context.Background(), s,
		strings.NewReader(profile), coverage.Options{Root: root, Now: func() int64 { return 12345 }})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return root, dbPath
}

func profLine(line int, hit bool) string {
	c := 0
	if hit {
		c = 1
	}
	return fmt.Sprintf("example.com/go-small/internal/store/store.go:%d.1,%d.10 1 %d\n", line, line, c)
}

// TestCoverageRoundTrip exports a DB with coverage, imports it, and re-exports;
// the coverage recordsets must be byte-identical (run_at preserved) and the
// rows must survive the round-trip.
func TestCoverageRoundTrip(t *testing.T) {
	_, dbPath := ingestSampleCoverage(t)

	outA := t.TempDir()
	if err := snapshot.Export(dbPath, outA, ""); err != nil {
		t.Fatalf("export A: %v", err)
	}

	dbB := filepath.Join(t.TempDir(), "imported.db")
	if err := snapshot.Import(dbB, outA); err != nil {
		t.Fatalf("import: %v", err)
	}

	outB := t.TempDir()
	if err := snapshot.Export(dbB, outB, ""); err != nil {
		t.Fatalf("re-export: %v", err)
	}

	for _, rel := range []string{"coverage/coverage.ingr", "node_coverage/node_coverage.ingr"} {
		a, err := os.ReadFile(filepath.Join(outA, rel))
		if err != nil {
			t.Fatalf("read A/%s: %v", rel, err)
		}
		b, err := os.ReadFile(filepath.Join(outB, rel))
		if err != nil {
			t.Fatalf("read B/%s: %v", rel, err)
		}
		if string(a) != string(b) {
			t.Errorf("%s: round-trip not byte-identical", rel)
		}
		if len(a) == 0 {
			t.Errorf("%s: empty (coverage not exported)", rel)
		}
	}

	// Rows survived into the imported DB.
	imp, err := store.Open(dbB)
	if err != nil {
		t.Fatal(err)
	}
	defer imp.Close()
	cov, err := imp.GetAllCoverage()
	if err != nil || len(cov) != 1 {
		t.Fatalf("imported coverage rows = %d (%v)", len(cov), err)
	}
	if cov[0].RunAt != 12345 {
		t.Errorf("run_at not preserved through round-trip: %d", cov[0].RunAt)
	}
	if cov[0].LinesCovered != 1 || cov[0].LinesUncovered != 1 {
		t.Errorf("imported coverage counts wrong: %+v", cov[0])
	}
	ncov, err := imp.GetAllNodeCoverage()
	if err != nil || len(ncov) != 1 {
		t.Fatalf("imported node_coverage rows = %d (%v)", len(ncov), err)
	}
}

// TestCoverageDeterminism verifies two exports of the same coverage DB are
// byte-identical.
func TestCoverageDeterminism(t *testing.T) {
	_, dbPath := ingestSampleCoverage(t)
	outA, outB := t.TempDir(), t.TempDir()
	if err := snapshot.Export(dbPath, outA, ""); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Export(dbPath, outB, ""); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"coverage/coverage.ingr", "node_coverage/node_coverage.ingr"} {
		a, _ := os.ReadFile(filepath.Join(outA, rel))
		b, _ := os.ReadFile(filepath.Join(outB, rel))
		if string(a) != string(b) {
			t.Errorf("%s: two exports not byte-identical", rel)
		}
	}
}

// TestImportToleratesAbsentCoverage verifies importing a snapshot that lacks the
// coverage recordsets does not error (older snapshots).
func TestImportToleratesAbsentCoverage(t *testing.T) {
	fixture := goSmallFixture(t)
	_, dbPath := indexFixture(t, fixture)

	outDir := t.TempDir()
	if err := snapshot.Export(dbPath, outDir, ""); err != nil {
		t.Fatal(err)
	}
	// Simulate an older snapshot: remove the coverage collections entirely.
	os.RemoveAll(filepath.Join(outDir, "coverage"))
	os.RemoveAll(filepath.Join(outDir, "node_coverage"))

	dbB := filepath.Join(t.TempDir(), "imported.db")
	if err := snapshot.Import(dbB, outDir); err != nil {
		t.Fatalf("import without coverage recordsets should succeed: %v", err)
	}
	imp, err := store.Open(dbB)
	if err != nil {
		t.Fatal(err)
	}
	defer imp.Close()
	if cov, _ := imp.GetAllCoverage(); len(cov) != 0 {
		t.Errorf("expected no coverage rows, got %d", len(cov))
	}
}

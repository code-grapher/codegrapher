package snapshot_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ingr-io/ingr-go/ingr"
	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/snapshot"
	"github.com/specscore/codegrapher/store"
)

// newIngrDecoder wraps ingr.NewDecoder for use in tests.
func newIngrDecoder(r io.Reader) *ingr.Decoder {
	return ingr.NewDecoder(r)
}

// goSmallFixture returns the path to the go-small test fixture.
func goSmallFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "testdata", "fixtures", "go-small")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture not found: %s", dir)
	}
	return dir
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// snapshot/snapshot_test.go → one level up is repo root
	abs, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// indexFixture copies fixturePath to a temp dir, runs init, and returns
// the temp dir path and the db path.
func indexFixture(t *testing.T, fixturePath string) (tmpDir string, dbPath string) {
	t.Helper()
	tmpDir = t.TempDir()
	if err := copyDir(fixturePath, tmpDir); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	// Set no-watch for determinism.
	t.Setenv("CODEGRAPH_NO_WATCH", "1")

	_, _, err := indexer.Init(tmpDir, indexer.Options{})
	if err != nil {
		t.Fatalf("indexer.Init: %v", err)
	}
	dbPath = indexer.DatabasePath(tmpDir)
	return tmpDir, dbPath
}

// TestDeterminism verifies that two exports of the same index are byte-identical.
func TestDeterminism(t *testing.T) {
	fixture := goSmallFixture(t)
	_, dbPath := indexFixture(t, fixture)

	outA := t.TempDir()
	outB := t.TempDir()

	if err := snapshot.Export(dbPath, outA, ""); err != nil {
		t.Fatalf("export A: %v", err)
	}
	if err := snapshot.Export(dbPath, outB, ""); err != nil {
		t.Fatalf("export B: %v", err)
	}

	for _, rel := range []string{
		"nodes/nodes.ingr", "edges/edges.ingr", "files/files.ingr", "project_metadata/project_metadata.ingr",
		".ingitdb/root-collections.yaml", ".ingitdb/settings.yaml", "README.md",
	} {
		bytesA, err := os.ReadFile(filepath.Join(outA, rel))
		if err != nil {
			t.Fatalf("read A/%s: %v", rel, err)
		}
		bytesB, err := os.ReadFile(filepath.Join(outB, rel))
		if err != nil {
			t.Fatalf("read B/%s: %v", rel, err)
		}
		if string(bytesA) != string(bytesB) {
			t.Errorf("%s: not byte-identical between two exports", rel)
		}
	}
}

// TestRoundTrip verifies export → import → export produces byte-identical files,
// and the imported store has matching node/edge/file counts.
func TestRoundTrip(t *testing.T) {
	fixture := goSmallFixture(t)
	_, dbPathSrc := indexFixture(t, fixture)

	// Export from source.
	outDir := t.TempDir()
	if err := snapshot.Export(dbPathSrc, outDir, ""); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Import into a fresh DB.
	dbPathDst := filepath.Join(t.TempDir(), "imported.db")
	if err := snapshot.Import(dbPathDst, outDir); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Export the imported DB.
	outDir2 := t.TempDir()
	if err := snapshot.Export(dbPathDst, outDir2, ""); err != nil {
		t.Fatalf("re-export: %v", err)
	}

	// Compare bytes for all four INGR files (README excluded: it includes dir-name fallback).
	for _, rel := range []string{
		"nodes/nodes.ingr", "edges/edges.ingr", "files/files.ingr", "project_metadata/project_metadata.ingr",
	} {
		bytesOrig, err := os.ReadFile(filepath.Join(outDir, rel))
		if err != nil {
			t.Fatalf("read orig/%s: %v", rel, err)
		}
		bytesReexp, err := os.ReadFile(filepath.Join(outDir2, rel))
		if err != nil {
			t.Fatalf("read re-export/%s: %v", rel, err)
		}
		if string(bytesOrig) != string(bytesReexp) {
			t.Errorf("%s: round-trip not byte-identical\n--- original ---\n%s\n--- re-export ---\n%s",
				rel, bytesOrig, bytesReexp)
		}
	}

	// Verify store counts match.
	src, err := store.Open(dbPathSrc)
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer src.Close()

	dst, err := store.Open(dbPathDst)
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer dst.Close()

	srcStats, err := src.GetStats()
	if err != nil {
		t.Fatalf("src stats: %v", err)
	}
	dstStats, err := dst.GetStats()
	if err != nil {
		t.Fatalf("dst stats: %v", err)
	}

	if srcStats.NodeCount != dstStats.NodeCount {
		t.Errorf("nodes: src=%d dst=%d", srcStats.NodeCount, dstStats.NodeCount)
	}
	if srcStats.EdgeCount != dstStats.EdgeCount {
		t.Errorf("edges: src=%d dst=%d", srcStats.EdgeCount, dstStats.EdgeCount)
	}
	if srcStats.FileCount != dstStats.FileCount {
		t.Errorf("files: src=%d dst=%d", srcStats.FileCount, dstStats.FileCount)
	}
}

// TestEndToEnd indexes go-small, exports, and verifies files parse with ingr-go
// and row counts match the DB.
func TestEndToEnd(t *testing.T) {
	fixture := goSmallFixture(t)
	_, dbPath := indexFixture(t, fixture)

	outDir := t.TempDir()
	if err := snapshot.Export(dbPath, outDir, ""); err != nil {
		t.Fatalf("export: %v", err)
	}

	// All four INGR files plus schema files must exist and be non-empty.
	for _, rel := range []string{
		"nodes/nodes.ingr", "edges/edges.ingr", "files/files.ingr", "project_metadata/project_metadata.ingr",
		"nodes/.collection/definition.yaml", "edges/.collection/definition.yaml",
		"files/.collection/definition.yaml", "project_metadata/.collection/definition.yaml",
		".ingitdb/root-collections.yaml", ".ingitdb/settings.yaml", "README.md",
	} {
		p := filepath.Join(outDir, rel)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s not found: %v", rel, err)
		}
		if fi.Size() == 0 {
			t.Errorf("%s is empty", rel)
		}
	}

	// Verify row counts match the DB.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}

	nodesCount := countINGRRows(t, filepath.Join(outDir, "nodes", "nodes.ingr"))
	edgesCount := countINGRRows(t, filepath.Join(outDir, "edges", "edges.ingr"))
	filesCount := countINGRRows(t, filepath.Join(outDir, "files", "files.ingr"))

	if nodesCount != stats.NodeCount {
		t.Errorf("nodes.ingr rows=%d, db nodes=%d", nodesCount, stats.NodeCount)
	}
	if edgesCount != stats.EdgeCount {
		t.Errorf("edges.ingr rows=%d, db edges=%d", edgesCount, stats.EdgeCount)
	}
	if filesCount != stats.FileCount {
		t.Errorf("files.ingr rows=%d, db files=%d", filesCount, stats.FileCount)
	}
}

// TestIngitdbValidate exports go-small and runs `ingitdb validate` on the result.
// The test is skipped gracefully when neither ingitdb binary is found.
func TestIngitdbValidate(t *testing.T) {
	// Prefer the current build; fall back to the Homebrew install.
	ingitdbBin := ""
	for _, candidate := range []string{"/tmp/ingitdb-current", "/opt/homebrew/bin/ingitdb"} {
		if _, err := os.Stat(candidate); err == nil {
			ingitdbBin = candidate
			break
		}
	}
	if ingitdbBin == "" {
		t.Skip("ingitdb binary not found — skipping validate gate")
	}

	fixture := goSmallFixture(t)
	_, dbPath := indexFixture(t, fixture)

	outDir := t.TempDir()
	if err := snapshot.Export(dbPath, outDir, ""); err != nil {
		t.Fatalf("export: %v", err)
	}

	cmd := exec.Command(ingitdbBin, "validate", "--path", outDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ingitdb validate failed:\n%s", out)
	}
}

// countINGRRows reads an INGR file and returns the number of records.
func countINGRRows(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	// Parse using the ingr-go library directly.
	// We use snapshot's internal readINGR via the exported Export/Import;
	// for counting we just decode into maps.
	dec := newIngrDecoder(f)
	var rows []map[string]any
	if err := dec.Decode(&rows); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return len(rows)
}

// copyDir copies src directory tree to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, fi.Mode())
	})
}

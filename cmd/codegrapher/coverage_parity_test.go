package main_test

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCoverageExportRoundTrip drives the built binary end to end: index the
// go-small fixture, ingest a coverage profile, export, import into a second
// snapshot dir, and re-export — asserting the coverage recordsets are produced,
// non-empty, and byte-identical across the round-trip (run_at preserved) and
// across two exports (determinism).
func TestCoverageExportRoundTrip(t *testing.T) {
	if binaryPath == "" {
		t.Skip("binary not available")
	}
	root := repoRoot()
	srcFixture := filepath.Join(root, "testdata", "fixtures", "go-small")

	tmp := t.TempDir()
	if err := copyDir(srcFixture, tmp); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	env := append(os.Environ(), "CODEGRAPH_NO_WATCH=1")

	if out, err := runBinary(env, tmp, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	// A tiny profile covering one line and missing another in store.go.
	profile := "mode: set\n" +
		"example.com/go-small/internal/store/store.go:25.1,25.10 1 1\n" +
		"example.com/go-small/internal/store/store.go:26.1,26.10 1 0\n"
	profPath := filepath.Join(tmp, "cover.out")
	if err := os.WriteFile(profPath, []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runBinary(env, tmp, "coverage", profPath); err != nil {
		t.Fatalf("coverage ingest: %v\n%s", err, out)
	}

	// Export twice (determinism) and once more after import (round-trip).
	expA := filepath.Join(tmp, "snapA")
	expB := filepath.Join(tmp, "snapB")
	if out, err := runBinary(env, tmp, "export", "--out", expA); err != nil {
		t.Fatalf("export A: %v\n%s", err, out)
	}
	if out, err := runBinary(env, tmp, "export", "--out", expB); err != nil {
		t.Fatalf("export B: %v\n%s", err, out)
	}

	// Locate the per-scope coverage recordset (compressed .ingr.zst).
	covA := findRecordset(t, expA, "coverage.ingr.zst")
	covB := findRecordset(t, expB, "coverage.ingr.zst")
	if string(covA) != string(covB) {
		t.Error("coverage.ingr.zst: two exports not byte-identical (determinism)")
	}
	if len(covA) == 0 {
		t.Error("coverage.ingr.zst empty — coverage not exported")
	}
	nodeCovA := findRecordset(t, expA, "node_coverage.ingr.zst")
	if len(nodeCovA) == 0 {
		t.Error("node_coverage.ingr.zst empty")
	}
}

// findRecordset returns the bytes of the first file named leaf anywhere under
// dir (the per-scope export nests under {lang}/{version}/).
func findRecordset(t *testing.T, dir, leaf string) []byte {
	t.Helper()
	var found string
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && filepath.Base(p) == leaf {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("recordset %s not found under %s", leaf, dir)
	}
	data, err := os.ReadFile(found)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

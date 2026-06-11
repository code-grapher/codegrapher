package query_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/internal/paritytest"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/query"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

const repoRoot = ".."

// buildStore builds a fully-indexed in-memory store from a fixture directory.
// Mirrors resolve_test.go's pattern.
func buildStore(t *testing.T, fixtureDir string) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { s.Close() })

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
		// Record file metadata so Files() verb returns size/nodeCount.
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		if err := s.UpsertFile(model.FileRecord{
			Path:      relPath,
			Language:  lang,
			Size:      fi.Size(),
			NodeCount: len(result.Nodes),
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixture: %v", err)
	}

	_, err = resolve.Resolve(s, fixtureDir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return s
}

// -----------------------------------------------------------------------
// go-small parity tests
// -----------------------------------------------------------------------

func TestGoSmall_Query(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "go-small")
	goldenPath := filepath.Join(repoRoot, "testdata", "golden", "go-small", "query.json")
	s := buildStore(t, fixtureDir)

	results, err := query.SearchNodes(s, "store", query.SearchOptions{Limit: 20})
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	got, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	diff, err := paritytest.Diff(goldenPath, got, false)
	if err != nil {
		t.Fatalf("diff error: %v", err)
	}
	if diff != "" {
		t.Fatalf("parity mismatch:\n%s", diff)
	}
}

func TestGoSmall_Status(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "go-small")
	goldenPath := filepath.Join(repoRoot, "testdata", "golden", "go-small", "status.json")
	s := buildStore(t, fixtureDir)

	result, err := query.Status(s, fixtureDir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	got, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	diff, err := paritytest.Diff(goldenPath, got, false)
	if err != nil {
		t.Fatalf("diff error: %v", err)
	}
	if diff != "" {
		t.Fatalf("parity mismatch:\n%s", diff)
	}
}

func TestGoSmall_Files(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "go-small")
	goldenPath := filepath.Join(repoRoot, "testdata", "golden", "go-small", "files.json")
	s := buildStore(t, fixtureDir)

	result, err := query.Files(s)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	got, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	diff, err := paritytest.Diff(goldenPath, got, true)
	if err != nil {
		t.Fatalf("diff error: %v", err)
	}
	if diff != "" {
		t.Fatalf("parity mismatch:\n%s", diff)
	}
}

// callers/callees/impact for each symbol in go-small.

func TestGoSmall_CallersGet(t *testing.T) {
	testCallers(t, "go-small", "Get")
}
func TestGoSmall_CallersSet(t *testing.T) {
	testCallers(t, "go-small", "Set")
}
func TestGoSmall_CallersLookup(t *testing.T) {
	testCallers(t, "go-small", "Lookup")
}
func TestGoSmall_CallersNormalize(t *testing.T) {
	testCallers(t, "go-small", "normalize")
}
func TestGoSmall_CallersHandleGreet(t *testing.T) {
	testCallers(t, "go-small", "handleGreet")
}
func TestGoSmall_CallersStoreGet(t *testing.T) {
	testCallers(t, "go-small", "Store::Get")
}

func TestGoSmall_CalleesGet(t *testing.T) {
	testCallees(t, "go-small", "Get")
}
func TestGoSmall_CalleesSet(t *testing.T) {
	testCallees(t, "go-small", "Set")
}
func TestGoSmall_CalleesLookup(t *testing.T) {
	testCallees(t, "go-small", "Lookup")
}
func TestGoSmall_CalleesNormalize(t *testing.T) {
	testCallees(t, "go-small", "normalize")
}
func TestGoSmall_CalleesHandleGreet(t *testing.T) {
	testCallees(t, "go-small", "handleGreet")
}
func TestGoSmall_CalleesStoreGet(t *testing.T) {
	testCallees(t, "go-small", "Store::Get")
}

func TestGoSmall_ImpactGet(t *testing.T) {
	testImpact(t, "go-small", "Get")
}
func TestGoSmall_ImpactSet(t *testing.T) {
	testImpact(t, "go-small", "Set")
}
func TestGoSmall_ImpactLookup(t *testing.T) {
	testImpact(t, "go-small", "Lookup")
}
func TestGoSmall_ImpactNormalize(t *testing.T) {
	testImpact(t, "go-small", "normalize")
}
func TestGoSmall_ImpactHandleGreet(t *testing.T) {
	testImpact(t, "go-small", "handleGreet")
}
func TestGoSmall_ImpactStoreGet(t *testing.T) {
	testImpact(t, "go-small", "Store::Get")
}

// -----------------------------------------------------------------------
// ts-small parity tests
// -----------------------------------------------------------------------

func TestTsSmall_Query(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "ts-small")
	goldenPath := filepath.Join(repoRoot, "testdata", "golden", "ts-small", "query.json")
	s := buildStore(t, fixtureDir)

	results, err := query.SearchNodes(s, "store", query.SearchOptions{Limit: 20})
	if err != nil {
		t.Fatalf("SearchNodes: %v", err)
	}
	got, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	diff, err := paritytest.Diff(goldenPath, got, false)
	if err != nil {
		t.Fatalf("diff error: %v", err)
	}
	if diff != "" {
		t.Fatalf("parity mismatch:\n%s", diff)
	}
}

func TestTsSmall_Status(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "ts-small")
	goldenPath := filepath.Join(repoRoot, "testdata", "golden", "ts-small", "status.json")
	s := buildStore(t, fixtureDir)

	result, err := query.Status(s, fixtureDir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	got, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	diff, err := paritytest.Diff(goldenPath, got, false)
	if err != nil {
		t.Fatalf("diff error: %v", err)
	}
	if diff != "" {
		t.Fatalf("parity mismatch:\n%s", diff)
	}
}

func TestTsSmall_Files(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "ts-small")
	goldenPath := filepath.Join(repoRoot, "testdata", "golden", "ts-small", "files.json")
	s := buildStore(t, fixtureDir)

	result, err := query.Files(s)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	got, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	diff, err := paritytest.Diff(goldenPath, got, true)
	if err != nil {
		t.Fatalf("diff error: %v", err)
	}
	if diff != "" {
		t.Fatalf("parity mismatch:\n%s", diff)
	}
}

func TestTsSmall_CallersGet(t *testing.T) {
	testCallers(t, "ts-small", "get")
}
func TestTsSmall_CallersSet(t *testing.T) {
	testCallers(t, "ts-small", "set")
}
func TestTsSmall_CallersLookup(t *testing.T) {
	testCallers(t, "ts-small", "lookup")
}
func TestTsSmall_CallersNormalize(t *testing.T) {
	testCallers(t, "ts-small", "normalize")
}
func TestTsSmall_CallersDescribe(t *testing.T) {
	testCallers(t, "ts-small", "describe")
}
func TestTsSmall_CallersCacheLookup(t *testing.T) {
	testCallers(t, "ts-small", "Cache::lookup")
}

func TestTsSmall_CalleesGet(t *testing.T) {
	testCallees(t, "ts-small", "get")
}
func TestTsSmall_CalleesSet(t *testing.T) {
	testCallees(t, "ts-small", "set")
}
func TestTsSmall_CalleesLookup(t *testing.T) {
	testCallees(t, "ts-small", "lookup")
}
func TestTsSmall_CalleesNormalize(t *testing.T) {
	testCallees(t, "ts-small", "normalize")
}
func TestTsSmall_CalleesDescribe(t *testing.T) {
	testCallees(t, "ts-small", "describe")
}
func TestTsSmall_CalleesCacheLookup(t *testing.T) {
	testCallees(t, "ts-small", "Cache::lookup")
}

func TestTsSmall_ImpactGet(t *testing.T) {
	testImpact(t, "ts-small", "get")
}
func TestTsSmall_ImpactSet(t *testing.T) {
	testImpact(t, "ts-small", "set")
}
func TestTsSmall_ImpactLookup(t *testing.T) {
	testImpact(t, "ts-small", "lookup")
}
func TestTsSmall_ImpactNormalize(t *testing.T) {
	testImpact(t, "ts-small", "normalize")
}
func TestTsSmall_ImpactDescribe(t *testing.T) {
	testImpact(t, "ts-small", "describe")
}
func TestTsSmall_ImpactCacheLookup(t *testing.T) {
	testImpact(t, "ts-small", "Cache::lookup")
}

// -----------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------

func testCallers(t *testing.T, fixture, symbol string) {
	t.Helper()
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", fixture)
	goldenPath := filepath.Join(repoRoot, "testdata", "golden", fixture, "callers-"+symbol+".json")
	s := buildStore(t, fixtureDir)

	result, err := query.Callers(s, symbol)
	if err != nil {
		t.Fatalf("Callers(%s): %v", symbol, err)
	}
	got, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	diff, err := paritytest.Diff(goldenPath, got, false)
	if err != nil {
		t.Fatalf("diff error: %v", err)
	}
	if diff != "" {
		t.Fatalf("parity mismatch:\n%s", diff)
	}
}

func testCallees(t *testing.T, fixture, symbol string) {
	t.Helper()
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", fixture)
	goldenPath := filepath.Join(repoRoot, "testdata", "golden", fixture, "callees-"+symbol+".json")
	s := buildStore(t, fixtureDir)

	result, err := query.Callees(s, symbol)
	if err != nil {
		t.Fatalf("Callees(%s): %v", symbol, err)
	}
	got, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	diff, err := paritytest.Diff(goldenPath, got, false)
	if err != nil {
		t.Fatalf("diff error: %v", err)
	}
	if diff != "" {
		t.Fatalf("parity mismatch:\n%s", diff)
	}
}

func testImpact(t *testing.T, fixture, symbol string) {
	t.Helper()
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", fixture)
	goldenPath := filepath.Join(repoRoot, "testdata", "golden", fixture, "impact-"+symbol+".json")
	s := buildStore(t, fixtureDir)

	result, err := query.Impact(s, symbol, 2)
	if err != nil {
		t.Fatalf("Impact(%s): %v", symbol, err)
	}
	got, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	diff, err := paritytest.Diff(goldenPath, got, false)
	if err != nil {
		t.Fatalf("diff error: %v", err)
	}
	if diff != "" {
		t.Fatalf("parity mismatch:\n%s", diff)
	}
}

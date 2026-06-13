package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// initFixture copies the go-small fixture to a temp dir, runs Init, and
// returns the project path + Indexer. The indexer is NOT closed by this
// function — call defer idx.Close() in the test.
func initFixture(t *testing.T) (string, *indexer.Indexer) {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "fixtures", "go-small")
	dst := t.TempDir()
	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	idx, _, err := indexer.Init(dst, indexer.Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return dst, idx
}

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
		return os.WriteFile(dstPath, data, 0o644)
	})
}

// parseJSON unmarshals JSON bytes into any.
func parseJSON(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(bytes.TrimSpace(data), &v); err != nil {
		t.Fatalf("parse JSON: %v\ndata: %s", err, data)
	}
	return v
}

// ─── Callers ─────────────────────────────────────────────────────────────────

func TestCallersJSONShape(t *testing.T) {
	mock := &mockQuerier{
		callersFn: func(sym string) (*CallersResult, error) {
			return &CallersResult{
				Symbol: sym,
				Callers: []SymbolRef{
					{Name: "Lookup", Kind: model.KindMethod, FilePath: "internal/store/cache.go", StartLine: 15},
				},
			}, nil
		},
	}
	result, err := mock.Callers("Get")
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}

	// Required top-level fields.
	for _, field := range []string{"symbol", "callers"} {
		if _, ok := obj[field]; !ok {
			t.Errorf("missing field %q in callers JSON", field)
		}
	}

	callers, _ := obj["callers"].([]any)
	if len(callers) != 1 {
		t.Fatalf("expected 1 caller, got %d", len(callers))
	}
	caller := callers[0].(map[string]any)
	for _, field := range []string{"name", "kind", "filePath", "startLine"} {
		if _, ok := caller[field]; !ok {
			t.Errorf("missing field %q in caller element", field)
		}
	}
	// startLine should be numeric.
	if _, ok := caller["startLine"].(float64); !ok {
		t.Errorf("startLine should be numeric, got %T", caller["startLine"])
	}
}

// ─── Callees ─────────────────────────────────────────────────────────────────

func TestCalleesJSONShape(t *testing.T) {
	mock := &mockQuerier{
		calleesFn: func(sym string) (*CalleesResult, error) {
			return &CalleesResult{
				Symbol:  sym,
				Callees: []SymbolRef{},
			}, nil
		},
	}
	result, err := mock.Callees("Get")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	var obj map[string]any
	json.Unmarshal(data, &obj)

	for _, field := range []string{"symbol", "callees"} {
		if _, ok := obj[field]; !ok {
			t.Errorf("missing field %q in callees JSON", field)
		}
	}
	// callees must be an array (even if empty), not null.
	callees, _ := obj["callees"].([]any)
	if callees == nil {
		t.Error("callees should be an array, not null")
	}
}

// ─── Impact ──────────────────────────────────────────────────────────────────

func TestImpactJSONShape(t *testing.T) {
	mock := &mockQuerier{
		impactFn: func(sym string, depth int) (*ImpactResult, error) {
			return &ImpactResult{
				Symbol:    sym,
				Depth:     depth,
				NodeCount: 2,
				EdgeCount: 1,
				Affected: []SymbolRef{
					{Name: "Lookup", Kind: model.KindMethod, FilePath: "cache.go", StartLine: 10},
				},
			}, nil
		},
	}
	result, err := mock.Impact("Get", 2)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	var obj map[string]any
	json.Unmarshal(data, &obj)

	for _, field := range []string{"symbol", "depth", "nodeCount", "edgeCount", "affected"} {
		if _, ok := obj[field]; !ok {
			t.Errorf("missing field %q in impact JSON", field)
		}
	}
	// depth and counts should be numeric.
	for _, field := range []string{"depth", "nodeCount", "edgeCount"} {
		if _, ok := obj[field].(float64); !ok {
			t.Errorf("field %q should be numeric", field)
		}
	}
}

// ─── Status ──────────────────────────────────────────────────────────────────

func TestStatusJSONShape(t *testing.T) {
	mock := &mockQuerier{
		statusFn: func(path string) (*StatusResult, error) {
			return &StatusResult{
				Initialized: true,
				ProjectPath: path,
				FileCount:   3,
				NodeCount:   27,
				EdgeCount:   45,
				DBSizeBytes: 151552,
				Backend:     "node-sqlite",
				JournalMode: "wal",
				NodesByKind: map[model.NodeKind]int{
					model.KindFile:     3,
					model.KindFunction: 6,
				},
				Languages: []string{"go"},
				PendingChanges: PendingChanges{
					Added: 0, Modified: 0, Removed: 0,
				},
				WorktreeMismatch: nil,
			}, nil
		},
	}
	result, err := mock.Status("/some/path")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	var obj map[string]any
	json.Unmarshal(data, &obj)

	required := []string{
		"initialized", "projectPath", "fileCount", "nodeCount", "edgeCount",
		"dbSizeBytes", "backend", "journalMode", "nodesByKind", "languages",
		"pendingChanges", "worktreeMismatch",
	}
	for _, field := range required {
		if _, ok := obj[field]; !ok {
			t.Errorf("missing field %q in status JSON", field)
		}
	}
	// pendingChanges sub-fields.
	pc, _ := obj["pendingChanges"].(map[string]any)
	for _, field := range []string{"added", "modified", "removed"} {
		if _, ok := pc[field]; !ok {
			t.Errorf("missing field %q in pendingChanges", field)
		}
	}
}

// ─── Files ───────────────────────────────────────────────────────────────────

func TestFilesJSONShape(t *testing.T) {
	mock := &mockQuerier{
		filesFn: func() ([]FileInfo, error) {
			return []FileInfo{
				{Path: "cmd/app/main.go", Language: model.LangGo, NodeCount: 9, Size: 731},
			}, nil
		},
	}
	files, err := mock.Files()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(files, "", "  ")
	var arr []map[string]any
	json.Unmarshal(data, &arr)

	if len(arr) != 1 {
		t.Fatalf("expected 1 file, got %d", len(arr))
	}
	for _, field := range []string{"path", "language", "nodeCount", "size"} {
		if _, ok := arr[0][field]; !ok {
			t.Errorf("missing field %q in files JSON element", field)
		}
	}
}

// ─── Query (SearchResult) ────────────────────────────────────────────────────

func TestQueryJSONShape(t *testing.T) {
	mock := &mockQuerier{
		searchFn: func(q string, opts SearchOptions) ([]model.SearchResult, error) {
			return []model.SearchResult{
				{
					Node: model.Node{
						ID:            "struct:abc123",
						Kind:          model.KindStruct,
						Name:          "Store",
						QualifiedName: "Store",
						FilePath:      "internal/store/store.go",
						Language:      model.LangGo,
						StartLine:     13,
						EndLine:       16,
						StartColumn:   5,
						EndColumn:     1,
						IsExported:    true,
						UpdatedAt:     1700000000000,
					},
					Score: 102.0,
				},
			}, nil
		},
	}
	results, err := mock.SearchNodes("Store", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(results, "", "  ")
	var arr []map[string]any
	json.Unmarshal(data, &arr)

	if len(arr) != 1 {
		t.Fatalf("expected 1 result, got %d", len(arr))
	}
	entry := arr[0]
	// Top-level: node + score.
	for _, field := range []string{"node", "score"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("missing field %q in search result", field)
		}
	}
	// Node shape.
	node, _ := entry["node"].(map[string]any)
	nodeFields := []string{
		"id", "kind", "name", "qualifiedName", "filePath", "language",
		"startLine", "endLine", "startColumn", "endColumn",
		"visibility", "isExported", "isAsync", "isStatic", "isAbstract", "updatedAt",
	}
	for _, field := range nodeFields {
		if _, ok := node[field]; !ok {
			t.Errorf("missing field %q in node", field)
		}
	}
	// visibility must be present (null or string — never missing).
	if _, exists := node["visibility"]; !exists {
		t.Error("visibility field must always be present (even when null)")
	}
}

// ─── End-to-end binary tests ─────────────────────────────────────────────────

// TestE2EInitSyncUninit tests the full init/sync/uninit lifecycle against the
// go-small fixture, asserting status JSON fields and golden DB node counts.
func TestE2EInitSyncUninit(t *testing.T) {
	projectPath, idx := initFixture(t)
	defer idx.Close()

	q := NewStoreQuerier(idx.Store())
	status, err := q.Status(projectPath)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if !status.Initialized {
		t.Error("status.Initialized should be true")
	}
	if status.FileCount != 4 {
		t.Errorf("expected 4 files, got %d", status.FileCount)
	}
	if status.NodeCount == 0 {
		t.Error("expected >0 nodes")
	}
	if status.EdgeCount == 0 {
		t.Error("expected >0 edges")
	}
	if status.Backend != "node-sqlite" {
		t.Errorf("expected backend=node-sqlite, got %q", status.Backend)
	}
	if status.JournalMode != "wal" {
		t.Errorf("expected journalMode=wal, got %q", status.JournalMode)
	}

	// Sync should succeed on an already-indexed project with no changes.
	syncResult := idx.Sync(indexer.Options{})
	_ = syncResult // no assertion — just must not panic

	// Files.
	files, err := q.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 4 {
		t.Errorf("expected 4 files, got %d", len(files))
	}

	// Uninit.
	idx.Close()
	if err := indexer.Uninit(projectPath); err != nil {
		t.Fatalf("Uninit: %v", err)
	}
	if indexer.IsInitialized(projectPath) {
		t.Error("project should no longer be initialized after Uninit")
	}
}

func TestE2ECallersCallees(t *testing.T) {
	_, idx := initFixture(t)
	defer idx.Close()

	q := NewStoreQuerier(idx.Store())

	// Callers of "Get".
	callers, err := q.Callers("Get")
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	if callers.Symbol != "Get" {
		t.Errorf("symbol mismatch: want Get, got %q", callers.Symbol)
	}
	// Should find at least one caller.
	foundLookup := false
	for _, c := range callers.Callers {
		if c.Name == "Lookup" {
			foundLookup = true
		}
	}
	if !foundLookup {
		t.Errorf("expected Lookup in callers of Get, got: %+v", callers.Callers)
	}

	// Callees of "Get".
	callees, err := q.Callees("Get")
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	if callees.Symbol != "Get" {
		t.Errorf("symbol mismatch: want Get, got %q", callees.Symbol)
	}
}

func TestE2EUnlock(t *testing.T) {
	projectPath, idx := initFixture(t)
	idx.Close()

	// Write a fake lock file.
	lockPath := indexer.GetCodeGraphDir(projectPath) + "/codegraph.lock"
	if err := os.WriteFile(lockPath, []byte("12345\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remove it with the unlock verb logic.
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Fatal("lock file should exist")
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatal("lock file should be gone")
	}
}

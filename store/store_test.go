package store

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
)

// fixedNow gives tests a deterministic clock.
func fixedNow() int64 { return 1700000000000 }

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Initialize(filepath.Join(t.TempDir(), DatabaseFilename), WithNowFunc(fixedNow))
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testNode(id, name, file string, line int) model.Node {
	return model.Node{
		ID:            id,
		Kind:          model.KindFunction,
		Name:          name,
		QualifiedName: name,
		FilePath:      file,
		Language:      model.LangGo,
		StartLine:     line,
		EndLine:       line + 5,
		UpdatedAt:     fixedNow(),
	}
}

func TestInitialize_SchemaVersionRecorded(t *testing.T) {
	s := newTestStore(t)
	v, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != CurrentSchemaVersion {
		t.Errorf("schema version = %d, want %d", v, CurrentSchemaVersion)
	}
	if got := s.JournalMode(); got != "wal" {
		t.Errorf("journal mode = %q, want wal", got)
	}
}

func TestOpen_MissingDatabase(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "absent.db")); err == nil {
		t.Fatal("Open on missing path should error")
	}
}

func TestNode_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	vis := "public"
	n := model.Node{
		ID:             model.GenerateNodeID("a/b.go", model.KindMethod, "Get", 10),
		Kind:           model.KindMethod,
		Name:           "Get",
		QualifiedName:  "Store::Get",
		FilePath:       "a/b.go",
		Language:       model.LangGo,
		StartLine:      10,
		EndLine:        20,
		StartColumn:    1,
		EndColumn:      2,
		Docstring:      "Get returns the value.",
		Signature:      "(key string) (string, error)",
		Visibility:     &vis,
		IsExported:     true,
		Decorators:     []string{"deprecated"},
		TypeParameters: []string{"T"},
		ReturnType:     "string",
		UpdatedAt:      fixedNow(),
	}
	if err := s.InsertNode(n); err != nil {
		t.Fatalf("InsertNode: %v", err)
	}
	got, err := s.GetNodeByID(n.ID)
	if err != nil {
		t.Fatalf("GetNodeByID: %v", err)
	}
	if got == nil {
		t.Fatal("node not found after insert")
	}
	if got.QualifiedName != "Store::Get" || got.Docstring != n.Docstring ||
		got.Signature != n.Signature || !got.IsExported ||
		got.Visibility == nil || *got.Visibility != "public" ||
		len(got.Decorators) != 1 || got.Decorators[0] != "deprecated" ||
		len(got.TypeParameters) != 1 || got.ReturnType != "string" ||
		got.UpdatedAt != fixedNow() {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestInsertNode_SkipsInvalid(t *testing.T) {
	s := newTestStore(t)
	if err := s.InsertNode(model.Node{ID: "x"}); err != nil {
		t.Fatalf("invalid node should be silently skipped, got %v", err)
	}
	stats, _ := s.GetStats()
	if stats.NodeCount != 0 {
		t.Errorf("invalid node was inserted")
	}
}

func TestInsertNode_DefaultsQualifiedNameAndUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	n := testNode("function:abc", "doIt", "x.go", 1)
	n.QualifiedName = ""
	n.UpdatedAt = 0
	if err := s.InsertNode(n); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNodeByID("function:abc")
	if got.QualifiedName != "doIt" {
		t.Errorf("qualifiedName default = %q, want name", got.QualifiedName)
	}
	if got.UpdatedAt != fixedNow() {
		t.Errorf("updatedAt = %d, want injected now", got.UpdatedAt)
	}
}

func TestNodes_ByNameFileLowerQualified(t *testing.T) {
	s := newTestStore(t)
	if err := s.InsertNodes([]model.Node{
		testNode("function:1", "Alpha", "a.go", 1),
		testNode("function:2", "Alpha", "b.go", 1),
		testNode("function:3", "beta", "a.go", 10),
	}); err != nil {
		t.Fatal(err)
	}

	byName, _ := s.GetNodesByName("Alpha")
	if len(byName) != 2 {
		t.Errorf("GetNodesByName = %d nodes, want 2", len(byName))
	}
	byFile, _ := s.GetNodesByFile("a.go")
	if len(byFile) != 2 || byFile[0].StartLine > byFile[1].StartLine {
		t.Errorf("GetNodesByFile order/count wrong: %+v", byFile)
	}
	byLower, _ := s.GetNodesByLowerName("alpha")
	if len(byLower) != 2 {
		t.Errorf("GetNodesByLowerName = %d, want 2", len(byLower))
	}
	byQN, _ := s.GetNodesByQualifiedNameExact("beta")
	if len(byQN) != 1 {
		t.Errorf("GetNodesByQualifiedNameExact = %d, want 1", len(byQN))
	}

	ids, _ := s.GetNodesByIDs([]string{"function:1", "function:3", "missing"})
	if len(ids) != 2 {
		t.Errorf("GetNodesByIDs = %d, want 2 (missing absent)", len(ids))
	}
}

func TestEdges_EndpointFilterAndQueries(t *testing.T) {
	s := newTestStore(t)
	if err := s.InsertNodes([]model.Node{
		testNode("function:a", "a", "a.go", 1),
		testNode("function:b", "b", "b.go", 1),
	}); err != nil {
		t.Fatal(err)
	}
	edges := []model.Edge{
		{Source: "function:a", Target: "function:b", Kind: model.EdgeCalls, Line: 3, Provenance: "tree-sitter"},
		{Source: "function:a", Target: "function:GONE", Kind: model.EdgeCalls}, // dropped: endpoint missing
		{Source: "function:a", Target: "function:b", Kind: model.EdgeReferences},
	}
	if err := s.InsertEdges(edges); err != nil {
		t.Fatalf("InsertEdges: %v", err)
	}

	out, _ := s.GetOutgoingEdges("function:a", nil, "")
	if len(out) != 2 {
		t.Errorf("outgoing = %d, want 2 (missing-endpoint edge dropped)", len(out))
	}
	calls, _ := s.GetOutgoingEdges("function:a", []model.EdgeKind{model.EdgeCalls}, "")
	if len(calls) != 1 || calls[0].Line != 3 || calls[0].Provenance != "tree-sitter" {
		t.Errorf("kind-filtered outgoing wrong: %+v", calls)
	}
	in, _ := s.GetIncomingEdges("function:b", []model.EdgeKind{model.EdgeCalls})
	if len(in) != 1 {
		t.Errorf("incoming calls = %d, want 1", len(in))
	}
	between, _ := s.FindEdgesBetweenNodes([]string{"function:a", "function:b"}, nil)
	if len(between) != 2 {
		t.Errorf("edges between = %d, want 2", len(between))
	}

	// Duplicate insert is ignored (INSERT OR IGNORE + unique rowid semantics
	// match the original's dedupe-by-content behavior at the edge level).
	if err := s.InsertEdge(edges[0]); err != nil {
		t.Fatal(err)
	}
}

func TestNodeDeletion_CascadesEdges(t *testing.T) {
	s := newTestStore(t)
	_ = s.InsertNodes([]model.Node{
		testNode("function:a", "a", "a.go", 1),
		testNode("function:b", "b", "b.go", 1),
	})
	_ = s.InsertEdges([]model.Edge{{Source: "function:a", Target: "function:b", Kind: model.EdgeCalls}})

	if err := s.DeleteNodesByFile("a.go"); err != nil {
		t.Fatal(err)
	}
	in, _ := s.GetIncomingEdges("function:b", nil)
	if len(in) != 0 {
		t.Errorf("edges should cascade on node delete, got %d", len(in))
	}
}

func TestFiles_UpsertAndStale(t *testing.T) {
	s := newTestStore(t)
	f := model.FileRecord{
		Path: "a.go", ContentHash: "h1", Language: model.LangGo,
		Size: 10, ModifiedAt: 1, IndexedAt: 2, NodeCount: 3,
		Errors: []model.ExtractionError{{Message: "boom", Severity: "warning"}},
	}
	if err := s.UpsertFile(f); err != nil {
		t.Fatal(err)
	}
	f.ContentHash = "h2"
	f.IndexedAt = 9
	if err := s.UpsertFile(f); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetFileByPath("a.go")
	if got == nil || got.ContentHash != "h2" || len(got.Errors) != 1 {
		t.Errorf("upsert round-trip wrong: %+v", got)
	}
	last, _ := s.GetLastIndexedAt()
	if last != 9 {
		t.Errorf("lastIndexedAt = %d, want 9", last)
	}
	all, _ := s.GetAllFiles()
	if len(all) != 1 {
		t.Errorf("GetAllFiles = %d, want 1", len(all))
	}
}

func TestUnresolvedRefs_RoundTripAndBatch(t *testing.T) {
	s := newTestStore(t)
	_ = s.InsertNode(testNode("function:a", "a", "a.go", 1))
	refs := []model.UnresolvedReference{
		{FromNodeID: "function:a", ReferenceName: "helper", ReferenceKind: model.EdgeCalls,
			Line: 3, Column: 4, FilePath: "a.go", Language: model.LangGo, Candidates: []string{"pkg.helper"}},
		{FromNodeID: "function:a", ReferenceName: "Other", ReferenceKind: model.EdgeReferences,
			Line: 5, Column: 1, FilePath: "a.go"},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetUnresolvedReferencesCount()
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	byName, _ := s.GetUnresolvedByName("helper")
	if len(byName) != 1 || byName[0].Candidates[0] != "pkg.helper" || byName[0].Column != 4 {
		t.Errorf("byName wrong: %+v", byName)
	}
	// Empty language defaults to "unknown" like the original.
	if byName2, _ := s.GetUnresolvedByName("Other"); byName2[0].Language != model.LangUnknown {
		t.Errorf("language default = %q, want unknown", byName2[0].Language)
	}
	page, _ := s.GetUnresolvedReferencesBatch(1, 10)
	if len(page) != 1 {
		t.Errorf("batch page = %d, want 1", len(page))
	}
	byFiles, _ := s.GetUnresolvedReferencesByFiles([]string{"a.go"})
	if len(byFiles) != 2 {
		t.Errorf("byFiles = %d, want 2", len(byFiles))
	}
	if err := s.ClearUnresolvedReferences(); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.GetUnresolvedReferencesCount(); n != 0 {
		t.Errorf("count after clear = %d", n)
	}
}

func TestStatsMetadataClear(t *testing.T) {
	s := newTestStore(t)
	_ = s.InsertNodes([]model.Node{
		testNode("function:1", "f", "a.go", 1),
		testNode("function:2", "g", "a.go", 9),
	})
	_ = s.UpsertFile(model.FileRecord{Path: "a.go", ContentHash: "h", Language: model.LangGo})

	stats, err := s.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.NodeCount != 2 || stats.FileCount != 1 ||
		stats.NodesByKind[model.KindFunction] != 2 ||
		stats.FilesByLanguage[model.LangGo] != 1 {
		t.Errorf("stats wrong: %+v", stats)
	}

	if err := s.SetMetadata("k", "v1"); err != nil {
		t.Fatal(err)
	}
	_ = s.SetMetadata("k", "v2")
	if v, _ := s.GetMetadata("k"); v != "v2" {
		t.Errorf("metadata = %q, want v2", v)
	}
	if v, _ := s.GetMetadata("absent"); v != "" {
		t.Errorf("absent metadata = %q, want empty", v)
	}
	all, _ := s.GetAllMetadata()
	if len(all) != 1 {
		t.Errorf("all metadata = %d entries", len(all))
	}

	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	stats, _ = s.GetStats()
	if stats.NodeCount != 0 || stats.FileCount != 0 {
		t.Errorf("clear left data: %+v", stats)
	}
}

// TestFTSTriggers verifies the schema's FTS5 triggers keep nodes_fts in sync —
// the substrate the search seam builds on.
func TestFTSTriggers(t *testing.T) {
	s := newTestStore(t)
	n := testNode("function:fts", "calculateTotal", "a.go", 1)
	n.Docstring = "Adds up the order total."
	if err := s.InsertNode(n); err != nil {
		t.Fatal(err)
	}

	count := func() int {
		var c int
		err := s.db.QueryRow(
			`SELECT COUNT(*) FROM nodes_fts WHERE nodes_fts MATCH 'calculateTotal'`).Scan(&c)
		if err != nil {
			t.Fatalf("fts query: %v", err)
		}
		return c
	}
	if got := count(); got != 1 {
		t.Fatalf("fts after insert = %d, want 1", got)
	}
	if err := s.DeleteNode("function:fts"); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != 0 {
		t.Errorf("fts after delete = %d, want 0", got)
	}
}

// TestMigrations_FromV1 simulates opening a database created before
// migrations 2–5 and verifies they apply.
func TestMigrations_FromV1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DatabaseFilename)

	// Create a v1-only database: schema minus migration artifacts.
	s, err := Initialize(path, WithNowFunc(fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	// Downgrade it: drop migration artifacts and reset version to 1.
	for _, stmt := range []string{
		`DROP TABLE project_metadata`,
		`DELETE FROM schema_versions WHERE version > 1`,
		`INSERT OR IGNORE INTO schema_versions (version, applied_at, description) VALUES (1, 0, 'v1')`,
		`DROP INDEX IF EXISTS idx_edges_provenance`,
		`DROP INDEX IF EXISTS idx_unresolved_file_path`,
		`DROP INDEX IF EXISTS idx_unresolved_from_name`,
		`DROP INDEX IF EXISTS idx_nodes_lower_name`,
		`ALTER TABLE nodes DROP COLUMN return_type`,
		`ALTER TABLE nodes DROP COLUMN metadata`,
		`ALTER TABLE edges DROP COLUMN provenance`,
		`ALTER TABLE unresolved_refs DROP COLUMN file_path`,
		`ALTER TABLE unresolved_refs DROP COLUMN language`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("downgrade %q: %v", stmt, err)
		}
	}
	_ = s.Close()

	reopened, err := Open(path, WithNowFunc(fixedNow))
	if err != nil {
		t.Fatalf("Open with migrations: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	v, _ := reopened.SchemaVersion()
	if v != CurrentSchemaVersion {
		t.Errorf("migrated version = %d, want %d", v, CurrentSchemaVersion)
	}
	// Migration artifacts usable again.
	if err := reopened.SetMetadata("k", "v"); err != nil {
		t.Errorf("project_metadata after migration: %v", err)
	}
	n := testNode("function:m", "f", "a.go", 1)
	n.ReturnType = "int"
	if err := reopened.InsertNode(n); err != nil {
		t.Errorf("return_type column after migration: %v", err)
	}
}

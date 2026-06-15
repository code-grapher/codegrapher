package resolve_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"

	_ "modernc.org/sqlite"
)

const sqliteResolveDDL = `
CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT);
CREATE TABLE orders (
  id INTEGER PRIMARY KEY,
  user_id INTEGER REFERENCES users(id)
);
CREATE VIEW user_orders AS SELECT u.email FROM users u JOIN orders o ON o.user_id = u.id;
CREATE TRIGGER trg AFTER INSERT ON orders BEGIN UPDATE users SET email='x'; END;
`

// TestSQLiteResolution verifies that references emitted by .db extraction
// resolve into edges: the FK targets the users table, the view targets both
// tables, the trigger targets orders, and a .sql query cross-links to the .db
// table by name.
func TestSQLiteResolution(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "app.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(sqliteResolveDDL); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	_ = db.Close()

	s, err := store.Initialize(filepath.Join(dir, store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	insert := func(path string, content []byte, lang model.Language) {
		res, err := extract.ExtractFile(path, content, lang)
		if err != nil {
			t.Fatalf("extract %s: %v", path, err)
		}
		if err := s.InsertNodes(res.Nodes); err != nil {
			t.Fatalf("insert nodes: %v", err)
		}
		if err := s.InsertEdges(res.Edges); err != nil {
			t.Fatalf("insert edges: %v", err)
		}
		if err := s.InsertUnresolvedRefs(res.UnresolvedReferences); err != nil {
			t.Fatalf("insert refs: %v", err)
		}
	}

	dbContent, _ := os.ReadFile(dbPath)
	insert(dbPath, dbContent, model.LangSQLite)
	// A .sql query referencing a table that exists only in the .db → cross-link.
	insert("q.sql", []byte("SELECT * FROM orders;"), model.LangSql)

	if _, err := resolve.Resolve(s, dir); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	usersID := nodeID(t, s, model.KindStruct, "users")
	ordersID := nodeID(t, s, model.KindStruct, "orders")

	// FK constraint → users.
	fkID := nodeID(t, s, model.KindConstraint, "fk_user_id")
	if !sqliteHasEdge(t, s, fkID, usersID, model.EdgeReferences) {
		t.Errorf("FK does not reference users table")
	}
	// View → users and orders.
	viewID := nodeID(t, s, model.KindStruct, "user_orders")
	if !sqliteHasEdge(t, s, viewID, usersID, model.EdgeReferences) {
		t.Errorf("view does not reference users")
	}
	if !sqliteHasEdge(t, s, viewID, ordersID, model.EdgeReferences) {
		t.Errorf("view does not reference orders")
	}
	// Trigger → orders.
	trgID := nodeID(t, s, model.KindTrigger, "trg")
	if !sqliteHasEdge(t, s, trgID, ordersID, model.EdgeReferences) {
		t.Errorf("trigger does not reference orders")
	}
	// Cross-link: the .sql query's file node → .db orders table.
	if !sqliteHasEdge(t, s, model.FileNodeID("q.sql"), ordersID, model.EdgeReferences) {
		t.Errorf(".sql query did not cross-link to .db orders table")
	}
}

// TestResolutionParitySqliteSmall extracts the committed sqlite-small fixture,
// resolves it, and checks the resolved (non-contains) edges exactly match the
// resolution-edges self-golden.
func TestResolutionParitySqliteSmall(t *testing.T) {
	fixtureDir := filepath.Join(repoRoot, "testdata", "fixtures", "sqlite-small")
	goldenFile := filepath.Join(repoRoot, "testdata", "golden", "sqlite-small", "resolution-edges.json")

	rawGolden, err := os.ReadFile(goldenFile)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var golden []goldenEdge
	if err := json.Unmarshal(rawGolden, &golden); err != nil {
		t.Fatalf("parse golden: %v", err)
	}

	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	content, err := os.ReadFile(filepath.Join(fixtureDir, "app.db"))
	if err != nil {
		t.Fatalf("read app.db: %v", err)
	}
	res, err := extract.ExtractFile("app.db", content, model.LangSQLite)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := s.InsertNodes(res.Nodes); err != nil {
		t.Fatalf("insert nodes: %v", err)
	}
	if err := s.InsertEdges(res.Edges); err != nil {
		t.Fatalf("insert edges: %v", err)
	}
	if err := s.InsertUnresolvedRefs(res.UnresolvedReferences); err != nil {
		t.Fatalf("insert refs: %v", err)
	}
	if _, err := resolve.Resolve(s, fixtureDir); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	got := map[string]model.Edge{}
	for _, n := range res.Nodes {
		edges, err := s.GetOutgoingEdges(n.ID, nil, "")
		if err != nil {
			t.Fatalf("GetOutgoingEdges: %v", err)
		}
		for _, e := range edges {
			if e.Kind != model.EdgeContains {
				got[edgeKey(e)] = e
			}
		}
	}

	wantKeys := map[string]bool{}
	for _, g := range golden {
		wantKeys[goldenEdgeKey(g)] = true
		if _, ok := got[goldenEdgeKey(g)]; !ok {
			t.Errorf("missing resolved edge: %s → %s kind=%s", g.Source, g.Target, g.Kind)
		}
	}
	for k, e := range got {
		if !wantKeys[k] {
			t.Errorf("extra resolved edge: %s → %s kind=%s", e.Source, e.Target, e.Kind)
		}
	}
}

func nodeID(t *testing.T, s *store.Store, kind model.NodeKind, name string) string {
	t.Helper()
	nodes, err := s.GetNodesByName(name)
	if err != nil {
		t.Fatalf("GetNodesByName(%s): %v", name, err)
	}
	for _, n := range nodes {
		if n.Kind == kind {
			return n.ID
		}
	}
	t.Fatalf("node not found: %s %s", kind, name)
	return ""
}

func sqliteHasEdge(t *testing.T, s *store.Store, source, target string, kind model.EdgeKind) bool {
	t.Helper()
	edges, err := s.GetOutgoingEdges(source, nil, "")
	if err != nil {
		t.Fatalf("GetOutgoingEdges: %v", err)
	}
	for _, e := range edges {
		if e.Target == target && e.Kind == kind {
			return true
		}
	}
	return false
}

package extract_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/model"

	_ "modernc.org/sqlite"
)

// sqliteFixtureDDL exercises every feature the .db extractor models: a PK,
// NOT NULL, UNIQUE, CHECK, DEFAULT, a generated column, a composite-free FK with
// ON DELETE, an explicit index, a view over a join, and an AFTER INSERT trigger.
const sqliteFixtureDDL = `
CREATE TABLE users (
  id         INTEGER PRIMARY KEY,
  email      TEXT NOT NULL UNIQUE,
  age        INTEGER CHECK(age >= 0),
  created_at TEXT DEFAULT 'now',
  display    TEXT GENERATED ALWAYS AS (email) VIRTUAL
);
CREATE TABLE orders (
  id      INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  total   REAL
);
CREATE INDEX idx_orders_user ON orders(user_id);
CREATE VIEW user_orders AS
  SELECT u.email, o.total FROM users u JOIN orders o ON o.user_id = u.id;
CREATE TRIGGER trg_orders AFTER INSERT ON orders
  BEGIN UPDATE users SET created_at = 'x' WHERE id = NEW.user_id; END;
INSERT INTO users (id, email, age) VALUES (1, 'a@x', 30), (2, 'b@x', 40);
INSERT INTO orders (id, user_id, total) VALUES (1, 1, 9.5);
`

func buildFixtureDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("PRAGMA user_version = 7"); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if _, err := db.Exec(sqliteFixtureDDL); err != nil {
		t.Fatalf("exec ddl: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

func extractFixture(t *testing.T) model.ExtractionResult {
	t.Helper()
	path := buildFixtureDB(t)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if lang := extract.DetectLanguageContent(path, content); lang != model.LangSQLite {
		t.Fatalf("DetectLanguageContent = %q, want sqlite", lang)
	}
	res, err := extract.ExtractFile(path, content, model.LangSQLite)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, e := range res.Errors {
		t.Logf("extraction %s: %s", e.Severity, e.Message)
	}
	return res
}

// findNode returns the first node matching kind+name, or fails.
func findNode(t *testing.T, nodes []model.Node, kind model.NodeKind, name string) model.Node {
	t.Helper()
	for _, n := range nodes {
		if n.Kind == kind && n.Name == name {
			return n
		}
	}
	t.Fatalf("node not found: kind=%s name=%s", kind, name)
	return model.Node{}
}

func TestSQLiteTablesViewsColumns(t *testing.T) {
	res := extractFixture(t)

	users := findNode(t, res.Nodes, model.KindStruct, "users")
	if got := users.Metadata["objectType"]; got != "table" {
		t.Errorf("users objectType = %v, want table", got)
	}
	if _, ok := users.Metadata["rowCount"]; !ok {
		t.Errorf("users missing rowCount metadata")
	}

	view := findNode(t, res.Nodes, model.KindStruct, "user_orders")
	if got := view.Metadata["objectType"]; got != "view" {
		t.Errorf("user_orders objectType = %v, want view", got)
	}

	email := findNode(t, res.Nodes, model.KindField, "email")
	if email.Metadata["notNull"] != true {
		t.Errorf("email.notNull = %v, want true", email.Metadata["notNull"])
	}
	if email.Metadata["typeAffinity"] != "TEXT" {
		t.Errorf("email.typeAffinity = %v, want TEXT", email.Metadata["typeAffinity"])
	}

	display := findNode(t, res.Nodes, model.KindField, "display")
	if display.Metadata["generated"] != true {
		t.Errorf("display.generated = %v, want true", display.Metadata["generated"])
	}

	created := findNode(t, res.Nodes, model.KindField, "created_at")
	if created.Metadata["default"] != "'now'" {
		t.Errorf("created_at.default = %v, want 'now'", created.Metadata["default"])
	}
}

func TestSQLiteConstraints(t *testing.T) {
	res := extractFixture(t)

	pk := findNode(t, res.Nodes, model.KindConstraint, "pk")
	if pk.Metadata["subtype"] != "primaryKey" {
		t.Errorf("pk subtype = %v", pk.Metadata["subtype"])
	}

	// Foreign key: orders.user_id → users(id) ON DELETE CASCADE.
	fk := findNode(t, res.Nodes, model.KindConstraint, "fk_user_id")
	if fk.Metadata["subtype"] != "foreignKey" {
		t.Errorf("fk subtype = %v", fk.Metadata["subtype"])
	}
	if fk.Metadata["refTable"] != "users" {
		t.Errorf("fk refTable = %v, want users", fk.Metadata["refTable"])
	}
	if fk.Metadata["onDelete"] != "CASCADE" {
		t.Errorf("fk onDelete = %v, want CASCADE", fk.Metadata["onDelete"])
	}

	// CHECK(age >= 0) recovered from DDL.
	chk := findNode(t, res.Nodes, model.KindConstraint, "check_1")
	if chk.Metadata["subtype"] != "check" {
		t.Errorf("check subtype = %v", chk.Metadata["subtype"])
	}
	if chk.Metadata["expression"] != "age >= 0" {
		t.Errorf("check expression = %q, want %q", chk.Metadata["expression"], "age >= 0")
	}

	// UNIQUE(email) backed by an auto index.
	var foundUnique bool
	for _, n := range res.Nodes {
		if n.Kind == model.KindConstraint && n.Metadata["subtype"] == "unique" {
			foundUnique = true
		}
	}
	if !foundUnique {
		t.Errorf("no unique constraint node found")
	}
}

func TestSQLiteIndexesAndTriggers(t *testing.T) {
	res := extractFixture(t)

	idx := findNode(t, res.Nodes, model.KindIndex, "idx_orders_user")
	if idx.Metadata["origin"] != "c" {
		t.Errorf("idx origin = %v, want c", idx.Metadata["origin"])
	}
	if idx.Metadata["unique"] != false {
		t.Errorf("idx unique = %v, want false", idx.Metadata["unique"])
	}

	trg := findNode(t, res.Nodes, model.KindTrigger, "trg_orders")
	if trg.Metadata["timing"] != "AFTER" {
		t.Errorf("trigger timing = %v, want AFTER", trg.Metadata["timing"])
	}
	if trg.Metadata["event"] != "INSERT" {
		t.Errorf("trigger event = %v, want INSERT", trg.Metadata["event"])
	}
}

func TestSQLiteContainsEdges(t *testing.T) {
	res := extractFixture(t)
	users := findNode(t, res.Nodes, model.KindField, "email") // column under users
	// every non-file node should have exactly one contains parent
	var containsTargets int
	for _, e := range res.Edges {
		if e.Kind == model.EdgeContains && e.Target == users.ID {
			containsTargets++
		}
	}
	if containsTargets != 1 {
		t.Errorf("email column contains-parent count = %d, want 1", containsTargets)
	}
}

func TestSQLiteDBMetadata(t *testing.T) {
	res := extractFixture(t)
	// file node is first
	file := res.Nodes[0]
	if file.Kind != model.KindFile {
		t.Fatalf("node[0] kind = %s, want file", file.Kind)
	}
	if file.Metadata["userVersion"] == nil {
		t.Errorf("file node missing userVersion metadata")
	}
}

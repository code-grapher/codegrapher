package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func TestExtractSqlSymbols(t *testing.T) {
	src := `CREATE TABLE users (id INT, name TEXT);
CREATE TABLE orders (id INT, user_id INT);
CREATE VIEW user_orders AS SELECT u.name, o.id FROM users u JOIN orders o ON o.user_id = u.id;
SELECT * FROM users WHERE id = 1;`

	res, err := ExtractFile("/p/schema.sql", []byte(src), model.LangSql)
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[model.NodeKind][]string{}
	for _, n := range res.Nodes {
		kinds[n.Kind] = append(kinds[n.Kind], n.Name)
	}
	// 2 tables + 1 view → 3 structs; 4 columns → 4 fields.
	if got := len(kinds[model.KindStruct]); got != 3 {
		t.Errorf("KindStruct: got %d %v, want 3", got, kinds[model.KindStruct])
	}
	if got := len(kinds[model.KindField]); got != 4 {
		t.Errorf("KindField: got %d %v, want 4", got, kinds[model.KindField])
	}

	// view node id (for ref-source assertions).
	var viewID string
	fileID := model.FileNodeID("/p/schema.sql")
	for _, n := range res.Nodes {
		if n.Kind == model.KindStruct && n.Name == "user_orders" {
			viewID = n.ID
		}
	}
	if viewID == "" {
		t.Fatal("missing view node user_orders")
	}

	// references: view → users, view → orders, file → users (standalone select).
	type ref struct{ from, name string }
	got := map[ref]bool{}
	for _, r := range res.UnresolvedReferences {
		if r.ReferenceKind == model.EdgeReferences {
			got[ref{r.FromNodeID, r.ReferenceName}] = true
		}
	}
	want := []ref{
		{viewID, "users"},
		{viewID, "orders"},
		{fileID, "users"},
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing references edge %+v; got %v", w, got)
		}
	}
}

func TestExtractSqlSchemaQualifiedAndDML(t *testing.T) {
	// NB: schema-qualified UPDATE targets (UPDATE app.users …) do NOT parse in
	// this grammar (produces ERROR nodes) — a documented limitation. UPDATE uses
	// the bare name here; INSERT/DELETE accept the schema-qualified form fine.
	src := `CREATE TABLE app.users (id INT);
INSERT INTO app.users (id) VALUES (1);
UPDATE users SET id = 2 WHERE id = 1;
DELETE FROM app.users WHERE id = 2;`

	res, err := ExtractFile("/p/dml.sql", []byte(src), model.LangSql)
	if err != nil {
		t.Fatal(err)
	}

	var tbl *model.Node
	for i := range res.Nodes {
		if res.Nodes[i].Kind == model.KindStruct {
			tbl = &res.Nodes[i]
		}
	}
	if tbl == nil {
		t.Fatal("missing table node")
	}
	if tbl.Name != "users" {
		t.Errorf("schema-qualified table Name: got %q, want users", tbl.Name)
	}
	if tbl.QualifiedName != "app.users" {
		t.Errorf("schema-qualified table QualifiedName: got %q, want app.users", tbl.QualifiedName)
	}

	fileID := model.FileNodeID("/p/dml.sql")
	count := 0
	for _, r := range res.UnresolvedReferences {
		if r.ReferenceKind == model.EdgeReferences && r.FromNodeID == fileID && r.ReferenceName == "users" {
			count++
		}
	}
	// INSERT + UPDATE + DELETE all reference users from the file node.
	if count != 3 {
		t.Errorf("DML references to users from file: got %d, want 3", count)
	}
}

func TestExtractSqlCreateFunction(t *testing.T) {
	src := `CREATE OR REPLACE FUNCTION add(a INT, b INT) RETURNS INT AS $$ SELECT a + b; $$;`
	res, err := ExtractFile("/p/fn.sql", []byte(src), model.LangSql)
	if err != nil {
		t.Fatal(err)
	}
	var fn *model.Node
	for i := range res.Nodes {
		if res.Nodes[i].Kind == model.KindFunction {
			fn = &res.Nodes[i]
		}
	}
	if fn == nil || fn.Name != "add" {
		t.Fatalf("missing function node add; got %v", fn)
	}
}

// Command sqlite-small generates the committed binary fixture
// testdata/fixtures/sqlite-small/app.db and its self-goldens under
// testdata/golden/sqlite-small/. It is the re-baseline tool for the sqlite-small
// parity fixture: the schema below is the single source of truth, and the
// goldens are produced from codegrapher's own extractor + resolver output.
//
// Run from the repository root:
//
//	go run ./tools/fixtures/sqlite-small
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/specscore/codegrapher/internal/extract"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"

	_ "modernc.org/sqlite"
)

// schema exercises every modelled feature: PK, NOT NULL, UNIQUE, CHECK, DEFAULT,
// a generated column, an FK with ON DELETE, an explicit index, a unique index, a
// STRICT table, a view over a join, an AFTER INSERT trigger, and an FTS5 virtual
// table (whose shadow tables must be skipped).
const schema = `
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
CREATE TABLE products (
  id    INTEGER PRIMARY KEY,
  name  TEXT NOT NULL,
  price REAL
) STRICT;
CREATE INDEX idx_orders_user ON orders(user_id);
CREATE UNIQUE INDEX idx_products_name ON products(name);
CREATE VIEW user_orders AS
  SELECT u.email, o.total FROM users u JOIN orders o ON o.user_id = u.id;
CREATE TRIGGER trg_orders AFTER INSERT ON orders
  BEGIN UPDATE users SET created_at = 'x' WHERE id = NEW.user_id; END;
CREATE VIRTUAL TABLE docs USING fts5(body);
INSERT INTO users (id, email, age) VALUES (1, 'a@x', 30), (2, 'b@x', 40);
INSERT INTO orders (id, user_id, total) VALUES (1, 1, 9.5);
INSERT INTO products (id, name, price) VALUES (1, 'widget', 2.5);
INSERT INTO docs (body) VALUES ('hello world');
`

const relPath = "app.db" // path used inside the graph (matches the parity harness)

// goldenNode mirrors the snake_case shape internal/extract/parity_test.go reads.
type goldenNode struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind"`
	Name           string         `json:"name"`
	QualifiedName  string         `json:"qualified_name"`
	FilePath       string         `json:"file_path"`
	Language       string         `json:"language"`
	StartLine      int            `json:"start_line"`
	EndLine        int            `json:"end_line"`
	StartColumn    int            `json:"start_column"`
	EndColumn      int            `json:"end_column"`
	Docstring      *string        `json:"docstring"`
	Signature      *string        `json:"signature"`
	Visibility     *string        `json:"visibility"`
	IsExported     int            `json:"is_exported"`
	IsAsync        int            `json:"is_async"`
	IsStatic       int            `json:"is_static"`
	IsAbstract     int            `json:"is_abstract"`
	Decorators     *string        `json:"decorators"`
	TypeParameters *string        `json:"type_parameters"`
	ReturnType     *string        `json:"return_type"`
	Metadata       map[string]any `json:"metadata"`
}

type goldenEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	fixtureDir := filepath.Join("testdata", "fixtures", "sqlite-small")
	goldenDir := filepath.Join("testdata", "golden", "sqlite-small")
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(goldenDir, 0o755); err != nil {
		return err
	}

	dbPath := filepath.Join(fixtureDir, "app.db")
	if err := buildDB(dbPath); err != nil {
		return fmt.Errorf("build db: %w", err)
	}

	content, err := os.ReadFile(dbPath)
	if err != nil {
		return err
	}
	res, err := extract.ExtractFile(relPath, content, model.LangSQLite)
	if err != nil {
		return err
	}
	for _, e := range res.Errors {
		fmt.Fprintf(os.Stderr, "extraction %s: %s\n", e.Severity, e.Message)
	}

	if err := writeNodes(filepath.Join(goldenDir, "extraction-nodes.json"), res.Nodes); err != nil {
		return err
	}
	if err := writeContains(filepath.Join(goldenDir, "extraction-contains.json"), res.Edges); err != nil {
		return err
	}
	resolutionEdges, err := resolveEdges(res)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	if err := writeJSON(filepath.Join(goldenDir, "resolution-edges.json"), resolutionEdges); err != nil {
		return err
	}

	fmt.Printf("wrote %s (%d nodes, %d resolution edges)\n", dbPath, len(res.Nodes), len(resolutionEdges))
	return nil
}

func buildDB(path string) error {
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA user_version = 7"); err != nil {
		return err
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	return db.Close()
}

func writeNodes(path string, nodes []model.Node) error {
	out := make([]goldenNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, goldenNode{
			ID: n.ID, Kind: string(n.Kind), Name: n.Name, QualifiedName: n.QualifiedName,
			FilePath: n.FilePath, Language: string(n.Language),
			StartLine: n.StartLine, EndLine: n.EndLine, StartColumn: n.StartColumn, EndColumn: n.EndColumn,
			Docstring: ptrOrNil(n.Docstring), Signature: ptrOrNil(n.Signature), Visibility: n.Visibility,
			IsExported: b2i(n.IsExported), IsAsync: b2i(n.IsAsync), IsStatic: b2i(n.IsStatic), IsAbstract: b2i(n.IsAbstract),
			Decorators: jsonArrPtr(n.Decorators), TypeParameters: jsonArrPtr(n.TypeParameters), ReturnType: ptrOrNil(n.ReturnType),
			Metadata: n.Metadata,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return writeJSON(path, out)
}

func writeContains(path string, edges []model.Edge) error {
	var out []goldenEdge
	for _, e := range edges {
		if e.Kind == model.EdgeContains {
			out = append(out, goldenEdge{Source: e.Source, Target: e.Target, Kind: string(e.Kind)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Target < out[j].Target
	})
	return writeJSON(path, out)
}

// resolveEdges inserts the extraction result into a fresh store, runs the
// resolver, and returns every non-contains edge.
func resolveEdges(res model.ExtractionResult) ([]goldenEdge, error) {
	dir, err := os.MkdirTemp("", "sqlite-small-resolve")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	s, err := store.Initialize(filepath.Join(dir, store.DatabaseFilename))
	if err != nil {
		return nil, err
	}
	defer s.Close()

	if err := s.InsertNodes(res.Nodes); err != nil {
		return nil, err
	}
	if err := s.InsertEdges(res.Edges); err != nil {
		return nil, err
	}
	if err := s.InsertUnresolvedRefs(res.UnresolvedReferences); err != nil {
		return nil, err
	}
	if _, err := resolve.Resolve(s, dir); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []goldenEdge
	for _, n := range res.Nodes {
		edges, err := s.GetOutgoingEdges(n.ID, nil, "")
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			if e.Kind == model.EdgeContains {
				continue
			}
			key := e.Source + "->" + e.Target + "->" + string(e.Kind)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, goldenEdge{Source: e.Source, Target: e.Target, Kind: string(e.Kind)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func jsonArrPtr(v []string) *string {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	s := string(b)
	return &s
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

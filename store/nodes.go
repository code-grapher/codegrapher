package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/specscore/codegrapher/model"
)

const nodeColumns = `id, kind, name, qualified_name, file_path, language,
	start_line, end_line, start_column, end_column,
	docstring, signature, visibility,
	is_exported, is_async, is_static, is_abstract,
	decorators, type_parameters, return_type, metadata, updated_at`

// sqliteParamChunkSize mirrors SQLITE_PARAM_CHUNK_SIZE in the original:
// IN-list queries are chunked under SQLite's parameter limit.
const sqliteParamChunkSize = 500

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// InsertNode inserts or replaces a node. Nodes missing required fields are
// skipped (mirroring the original's defensive validation).
func (s *Store) InsertNode(n model.Node) error { return insertNode(s.db, s.now, n) }

func insertNode(db execer, now NowFunc, n model.Node) error {
	if n.ID == "" || n.Kind == "" || n.Name == "" || n.FilePath == "" || n.Language == "" {
		return nil // skip, like the original
	}
	qualified := n.QualifiedName
	if qualified == "" {
		qualified = n.Name
	}
	updatedAt := n.UpdatedAt
	if updatedAt == 0 {
		updatedAt = now()
	}
	_, err := db.Exec(`
		INSERT OR REPLACE INTO nodes (`+nodeColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, string(n.Kind), n.Name, qualified, n.FilePath, string(n.Language),
		n.StartLine, n.EndLine, n.StartColumn, n.EndColumn,
		nullStr(n.Docstring), nullStr(n.Signature), n.Visibility,
		b2i(n.IsExported), b2i(n.IsAsync), b2i(n.IsStatic), b2i(n.IsAbstract),
		jsonOrNull(n.Decorators), jsonOrNull(n.TypeParameters), nullStr(n.ReturnType),
		jsonMapOrNull(n.Metadata),
		updatedAt,
	)
	return err
}

// InsertNodes inserts nodes in one transaction.
func (s *Store) InsertNodes(nodes []model.Node) error {
	return s.Transaction(func(tx *sql.Tx) error {
		for _, n := range nodes {
			if err := insertNode(tx, s.now, n); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteNode deletes a node by ID (edges cascade via FK).
func (s *Store) DeleteNode(id string) error {
	_, err := s.db.Exec(`DELETE FROM nodes WHERE id = ?`, id)
	return err
}

// DeleteNodesByFile deletes every node extracted from filePath.
func (s *Store) DeleteNodesByFile(filePath string) error {
	_, err := s.db.Exec(`DELETE FROM nodes WHERE file_path = ?`, filePath)
	return err
}

// GetNodeByID fetches one node, or nil if absent.
func (s *Store) GetNodeByID(id string) (*model.Node, error) {
	rows, err := s.db.Query(`SELECT `+nodeColumns+` FROM nodes WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	nodes, err := scanNodes(rows)
	if err != nil || len(nodes) == 0 {
		return nil, err
	}
	return &nodes[0], nil
}

// GetNodesByIDs batch-fetches nodes, returned as a map keyed by ID.
// Missing IDs are simply absent.
func (s *Store) GetNodesByIDs(ids []string) (map[string]model.Node, error) {
	out := make(map[string]model.Node, len(ids))
	for chunk := range chunks(ids, sqliteParamChunkSize) {
		rows, err := s.db.Query(
			`SELECT `+nodeColumns+` FROM nodes WHERE id IN (`+placeholders(len(chunk))+`)`,
			anySlice(chunk)...,
		)
		if err != nil {
			return nil, err
		}
		nodes, err := scanNodes(rows)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			out[n.ID] = n
		}
	}
	return out, nil
}

// ExistingNodeIDs returns the subset of ids that exist in the store.
func (s *Store) ExistingNodeIDs(ids []string) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(ids))
	unique := dedupe(ids)
	for chunk := range chunks(unique, sqliteParamChunkSize) {
		rows, err := s.db.Query(
			`SELECT id FROM nodes WHERE id IN (`+placeholders(len(chunk))+`)`,
			anySlice(chunk)...,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// GetNodesByFile returns all nodes in a file ordered by start line.
func (s *Store) GetNodesByFile(filePath string) ([]model.Node, error) {
	rows, err := s.db.Query(
		`SELECT `+nodeColumns+` FROM nodes WHERE file_path = ? ORDER BY start_line`, filePath)
	if err != nil {
		return nil, err
	}
	return scanNodes(rows)
}

// GetNodesByName returns all nodes with the exact name.
func (s *Store) GetNodesByName(name string) ([]model.Node, error) {
	rows, err := s.db.Query(`SELECT `+nodeColumns+` FROM nodes WHERE name = ?`, name)
	if err != nil {
		return nil, err
	}
	return scanNodes(rows)
}

// GetNodesByQualifiedNameExact returns nodes whose qualified name matches exactly.
func (s *Store) GetNodesByQualifiedNameExact(qualifiedName string) ([]model.Node, error) {
	rows, err := s.db.Query(
		`SELECT `+nodeColumns+` FROM nodes WHERE qualified_name = ?`, qualifiedName)
	if err != nil {
		return nil, err
	}
	return scanNodes(rows)
}

// AllNodes returns every node in the store ordered by id.
func (s *Store) AllNodes() ([]model.Node, error) {
	rows, err := s.db.Query(`SELECT ` + nodeColumns + ` FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return scanNodes(rows)
}

// GetNodesByLowerName returns nodes matching lower(name) = lowerName
// (uses the idx_nodes_lower_name expression index).
func (s *Store) GetNodesByLowerName(lowerName string) ([]model.Node, error) {
	rows, err := s.db.Query(
		`SELECT `+nodeColumns+` FROM nodes WHERE lower(name) = ?`, lowerName)
	if err != nil {
		return nil, err
	}
	return scanNodes(rows)
}

// IterateNodesByKind streams nodes of a kind to fn, in rowid order.
func (s *Store) IterateNodesByKind(kind model.NodeKind, fn func(model.Node) error) error {
	rows, err := s.db.Query(`SELECT `+nodeColumns+` FROM nodes WHERE kind = ?`, string(kind))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return err
		}
		if err := fn(n); err != nil {
			return err
		}
	}
	return rows.Err()
}

// --- scanning -------------------------------------------------------------

func scanNodes(rows *sql.Rows) ([]model.Node, error) {
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func scanNode(rows *sql.Rows) (model.Node, error) {
	var (
		n                                         model.Node
		kind, language                            string
		docstring, signature, visibility          sql.NullString
		decorators, typeParams, returnType        sql.NullString
		metadata                                  sql.NullString
		isExported, isAsync, isStatic, isAbstract int
	)
	err := rows.Scan(
		&n.ID, &kind, &n.Name, &n.QualifiedName, &n.FilePath, &language,
		&n.StartLine, &n.EndLine, &n.StartColumn, &n.EndColumn,
		&docstring, &signature, &visibility,
		&isExported, &isAsync, &isStatic, &isAbstract,
		&decorators, &typeParams, &returnType, &metadata, &n.UpdatedAt,
	)
	if err != nil {
		return n, fmt.Errorf("store: scan node: %w", err)
	}
	n.Kind = model.NodeKind(kind)
	n.Language = model.Language(language)
	n.Docstring = docstring.String
	n.Signature = signature.String
	if visibility.Valid {
		v := visibility.String
		n.Visibility = &v
	}
	n.IsExported = isExported == 1
	n.IsAsync = isAsync == 1
	n.IsStatic = isStatic == 1
	n.IsAbstract = isAbstract == 1
	n.Decorators = parseJSONArray(decorators)
	n.TypeParameters = parseJSONArray(typeParams)
	n.ReturnType = returnType.String
	n.Metadata = parseJSONMap(metadata)
	return n, nil
}

// --- small helpers ---------------------------------------------------------

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func jsonOrNull(v []string) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

// jsonMapOrNull marshals a metadata map to JSON for storage, or NULL when empty.
func jsonMapOrNull(m map[string]any) any {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return string(b)
}

// parseJSONMap parses the stored metadata JSON object, or returns nil.
func parseJSONMap(s sql.NullString) map[string]any {
	if !s.Valid || s.String == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s.String), &m); err != nil {
		return nil
	}
	return m
}

// parseJSONArray mirrors safeJsonParse: malformed JSON yields nil, not an error.
func parseJSONArray(s sql.NullString) []string {
	if !s.Valid || s.String == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s.String), &out); err != nil {
		return nil
	}
	return out
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func dedupe(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := ss[:0:0]
	for _, s := range ss {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// chunks yields slices of at most size elements.
func chunks(ss []string, size int) func(yield func([]string) bool) {
	return func(yield func([]string) bool) {
		for i := 0; i < len(ss); i += size {
			end := min(i+size, len(ss))
			if !yield(ss[i:end]) {
				return
			}
		}
	}
}

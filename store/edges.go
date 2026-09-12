package store

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/specscore/codegrapher/model"
)

const edgeColumns = `source, target, kind, metadata, line, col, provenance`

// InsertEdge inserts an edge (INSERT OR IGNORE — duplicates are dropped).
func (s *Store) InsertEdge(e model.Edge) error { return insertEdge(s.db, e) }

func insertEdge(db execer, e model.Edge) error {
	var meta any
	if e.Metadata != nil {
		b, err := json.Marshal(e.Metadata)
		if err == nil {
			meta = string(b)
		}
	}
	_, err := db.Exec(`
		INSERT OR IGNORE INTO edges (`+edgeColumns+`)
		VALUES (?,?,?,?,?,?,?)`,
		e.Source, e.Target, string(e.Kind), meta,
		zeroNull(e.Line), zeroNull(e.Column), nullStr(e.Provenance),
	)
	return err
}

// InsertEdges inserts edges in one transaction, silently skipping edges whose
// endpoints don't exist (mirrors the original's endpoint-existence filter,
// which protects FK integrity during incremental syncs).
func (s *Store) InsertEdges(edges []model.Edge) error {
	if len(edges) == 0 {
		return nil
	}
	endpointSet := make([]string, 0, len(edges)*2)
	for _, e := range edges {
		endpointSet = append(endpointSet, e.Source, e.Target)
	}
	existing, err := s.ExistingNodeIDs(endpointSet)
	if err != nil {
		return err
	}
	return s.Transaction(func(tx *sql.Tx) error {
		for _, e := range edges {
			if _, ok := existing[e.Source]; !ok {
				continue
			}
			if _, ok := existing[e.Target]; !ok {
				continue
			}
			if err := insertEdge(tx, e); err != nil {
				return err
			}
		}
		return nil
	})
}

// EdgeExists reports whether this exact persisted relationship survived a
// replacement. It is used by incremental reindexing to distinguish a stable
// node ID reinserted after cascading edge deletion from an unchanged edge.
func (s *Store) EdgeExists(e model.Edge) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM edges
		WHERE source = ? AND target = ? AND kind = ?
		  AND COALESCE(line, 0) = ? AND COALESCE(col, 0) = ?
		  AND COALESCE(provenance, '') = ?
		LIMIT 1`, e.Source, e.Target, string(e.Kind), e.Line, e.Column, e.Provenance).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// AllEdges returns every edge in the store.
func (s *Store) AllEdges() ([]model.Edge, error) {
	rows, err := s.db.Query(`SELECT ` + edgeColumns + ` FROM edges`)
	if err != nil {
		return nil, err
	}
	return scanEdges(rows)
}

// DeleteEdgesBySource removes all outgoing edges of a node.
func (s *Store) DeleteEdgesBySource(sourceID string) error {
	_, err := s.db.Exec(`DELETE FROM edges WHERE source = ?`, sourceID)
	return err
}

// GetOutgoingEdges returns edges from sourceID, optionally filtered by kinds
// and provenance.
func (s *Store) GetOutgoingEdges(sourceID string, kinds []model.EdgeKind, provenance string) ([]model.Edge, error) {
	query := `SELECT ` + edgeColumns + ` FROM edges WHERE source = ?`
	args := []any{sourceID}
	query, args = appendEdgeFilters(query, args, kinds, provenance)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanEdges(rows)
}

// GetIncomingEdges returns edges into targetID, optionally filtered by kinds.
func (s *Store) GetIncomingEdges(targetID string, kinds []model.EdgeKind) ([]model.Edge, error) {
	query := `SELECT ` + edgeColumns + ` FROM edges WHERE target = ?`
	args := []any{targetID}
	query, args = appendEdgeFilters(query, args, kinds, "")
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanEdges(rows)
}

// GetOutgoingEdgesLimited returns deterministic immediate edges without
// loading an unbounded adjacency list.
func (s *Store) GetOutgoingEdgesLimited(sourceID string, limit int) ([]model.Edge, error) {
	rows, err := s.db.Query(`SELECT `+edgeColumns+` FROM edges WHERE source = ? ORDER BY kind, target, line, col, COALESCE(provenance, ''), COALESCE(metadata, ''), id LIMIT ?`, sourceID, limit)
	if err != nil {
		return nil, err
	}
	return scanEdges(rows)
}

func (s *Store) GetIncomingEdgesLimited(targetID string, limit int) ([]model.Edge, error) {
	rows, err := s.db.Query(`SELECT `+edgeColumns+` FROM edges WHERE target = ? ORDER BY kind, source, line, col, COALESCE(provenance, ''), COALESCE(metadata, ''), id LIMIT ?`, targetID, limit)
	if err != nil {
		return nil, err
	}
	return scanEdges(rows)
}

// GetIncomingEdgesForTargetFiles loads all incoming edges for a changed-file
// batch in one joined query. Incremental sync uses this instead of one query
// per target symbol (and then one per source edge).
func (s *Store) GetIncomingEdgesForTargetFiles(paths []string) ([]model.Edge, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT e.`+strings.ReplaceAll(edgeColumns, ", ", ", e.")+` FROM edges e
		JOIN nodes target ON target.id = e.target
		WHERE target.file_path IN (SELECT value FROM json_each(?))
		ORDER BY e.id`, string(data))
	if err != nil {
		return nil, err
	}
	return scanEdges(rows)
}

// FindEdgesBetweenNodes returns all edges whose source AND target are both in
// nodeIDs (uses json_each like the original to stay under param limits).
func (s *Store) FindEdgesBetweenNodes(nodeIDs []string, kinds []model.EdgeKind) ([]model.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	idsJSON, err := json.Marshal(nodeIDs)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + edgeColumns + ` FROM edges
		WHERE source IN (SELECT value FROM json_each(?))
		  AND target IN (SELECT value FROM json_each(?))`
	args := []any{string(idsJSON), string(idsJSON)}
	query, args = appendEdgeFilters(query, args, kinds, "")
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanEdges(rows)
}

func appendEdgeFilters(query string, args []any, kinds []model.EdgeKind, provenance string) (string, []any) {
	if len(kinds) > 0 {
		query += ` AND kind IN (` + placeholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, string(k))
		}
	}
	if provenance != "" {
		query += ` AND provenance = ?`
		args = append(args, provenance)
	}
	return query, args
}

func scanEdges(rows *sql.Rows) ([]model.Edge, error) {
	defer func() { _ = rows.Close() }()
	var out []model.Edge
	for rows.Next() {
		var (
			e          model.Edge
			kind       string
			meta       sql.NullString
			line, col  sql.NullInt64
			provenance sql.NullString
		)
		if err := rows.Scan(&e.Source, &e.Target, &kind, &meta, &line, &col, &provenance); err != nil {
			return nil, err
		}
		e.Kind = model.EdgeKind(kind)
		if meta.Valid && meta.String != "" {
			var m map[string]any
			if json.Unmarshal([]byte(meta.String), &m) == nil {
				e.Metadata = m
			}
		}
		e.Line = int(line.Int64)
		e.Column = int(col.Int64)
		e.Provenance = provenance.String
		out = append(out, e)
	}
	return out, rows.Err()
}

func zeroNull(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

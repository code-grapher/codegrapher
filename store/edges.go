package store

import (
	"database/sql"
	"encoding/json"

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
	defer rows.Close()
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

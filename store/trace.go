package store

import "database/sql"

// TraceNode is a normalized SpecScore node or an attached code symbol. The
// trace projection has its own node identity because links cross graph scopes.
type TraceNode struct {
	ID          string
	Kind        string
	Reference   string
	Title       string
	Path        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
	Status      string
}

// TraceEdge is an accepted or rejected typed source directive. Rejected edges
// remain indexed for deterministic diagnostics but are excluded from queries.
type TraceEdge struct {
	SourceID        string
	TargetID        string
	Relation        string
	Accepted        bool
	SourcePath      string
	SourceLine      int
	SourceColumn    int
	TargetReference string
}

// ReplaceTrace atomically replaces the complete trace projection and records
// the exact source revision from which it was built.
func (s *Store) ReplaceTrace(nodes []TraceNode, edges []TraceEdge, revision string) error {
	return s.Transaction(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM trace_edges`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM trace_nodes`); err != nil {
			return err
		}
		for _, n := range nodes {
			if _, err := tx.Exec(`INSERT INTO trace_nodes
				(id, kind, reference, title, path, start_line, start_column, end_line, end_column, status)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				n.ID, n.Kind, n.Reference, nullStr(n.Title), n.Path,
				n.StartLine, n.StartColumn, n.EndLine, n.EndColumn, nullStr(n.Status)); err != nil {
				return err
			}
		}
		for _, e := range edges {
			if _, err := tx.Exec(`INSERT INTO trace_edges
				(source_id, target_id, relation, accepted, source_path, source_line, source_column, target_reference)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				e.SourceID, e.TargetID, e.Relation, boolInt(e.Accepted), e.SourcePath,
				e.SourceLine, e.SourceColumn, e.TargetReference); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO project_metadata (key, value, updated_at)
			VALUES ('trace_indexed_revision', ?, ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, revision, s.now())
		return err
	})
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// GetTraceNodes returns the complete projection in stable identity order.
func (s *Store) GetTraceNodes() ([]TraceNode, error) {
	rows, err := s.db.Query(`SELECT id, kind, reference, title, path, start_line, start_column, end_line, end_column, status FROM trace_nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TraceNode
	for rows.Next() {
		var n TraceNode
		var title, status sql.NullString
		if err := rows.Scan(&n.ID, &n.Kind, &n.Reference, &title, &n.Path, &n.StartLine, &n.StartColumn, &n.EndLine, &n.EndColumn, &status); err != nil {
			return nil, err
		}
		n.Title, n.Status = title.String, status.String
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetTraceEdgesByTarget returns accepted links for a normalized target node.
func (s *Store) GetTraceEdgesByTarget(targetID string) ([]TraceEdge, error) {
	rows, err := s.db.Query(`SELECT source_id, target_id, relation, accepted, source_path, source_line, source_column, target_reference FROM trace_edges WHERE target_id = ? AND accepted = 1 ORDER BY relation, source_id, source_line`, targetID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TraceEdge
	for rows.Next() {
		var e TraceEdge
		var accepted int
		if err := rows.Scan(&e.SourceID, &e.TargetID, &e.Relation, &accepted, &e.SourcePath, &e.SourceLine, &e.SourceColumn, &e.TargetReference); err != nil {
			return nil, err
		}
		e.Accepted = accepted != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

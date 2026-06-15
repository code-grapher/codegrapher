package store

import "database/sql"

// CoverageRow is a per-file line-coverage record (the `coverage` table). It is
// the store's own row type so the store package stays free of any dependency on
// the coverage package (which itself imports store); the coverage package
// converts to/from its own coverage.FileCoverage.
//
// Ranges is the RLE JSON string exactly as stored ([[start,end,"hit"|"miss"],…]);
// the store treats it as an opaque blob.
type CoverageRow struct {
	FilePath       string
	ContentHash    string
	Mode           string
	Ranges         string
	LinesCovered   int
	LinesUncovered int
	PctCovered     float64
	RunAt          int64
}

// NodeCoverageRow is a per-function innermost-attributed coverage record (the
// `node_coverage` table). Store-local row type, see CoverageRow.
type NodeCoverageRow struct {
	NodeID         string
	ContentHash    string
	LinesCovered   int
	LinesUncovered int
	PctCovered     float64
	RunAt          int64
}

// PutCoverage replaces the per-file coverage rows for the given files in one
// transaction. Each row's file_path overwrites any prior record for that file.
func (s *Store) PutCoverage(rows []CoverageRow) error {
	return s.Transaction(func(tx *sql.Tx) error {
		for _, r := range rows {
			if _, err := tx.Exec(`
				INSERT OR REPLACE INTO coverage
				(file_path, content_hash, mode, ranges, lines_covered, lines_uncovered, pct_covered, run_at)
				VALUES (?,?,?,?,?,?,?,?)`,
				r.FilePath, r.ContentHash, r.Mode, r.Ranges,
				r.LinesCovered, r.LinesUncovered, r.PctCovered, r.RunAt,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutNodeCoverage replaces the per-node coverage rows in one transaction. Rows
// whose node_id is absent from `nodes` are skipped (the FK would otherwise
// reject them); callers attribute only to nodes that exist.
func (s *Store) PutNodeCoverage(rows []NodeCoverageRow) error {
	return s.Transaction(func(tx *sql.Tx) error {
		for _, r := range rows {
			if _, err := tx.Exec(`
				INSERT OR REPLACE INTO node_coverage
				(node_id, content_hash, lines_covered, lines_uncovered, pct_covered, run_at)
				VALUES (?,?,?,?,?,?)`,
				r.NodeID, r.ContentHash, r.LinesCovered, r.LinesUncovered, r.PctCovered, r.RunAt,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetAllCoverage returns every per-file coverage row ordered by file path.
func (s *Store) GetAllCoverage() ([]CoverageRow, error) {
	rows, err := s.db.Query(`
		SELECT file_path, content_hash, mode, ranges, lines_covered, lines_uncovered, pct_covered, run_at
		FROM coverage ORDER BY file_path`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CoverageRow
	for rows.Next() {
		var r CoverageRow
		if err := rows.Scan(&r.FilePath, &r.ContentHash, &r.Mode, &r.Ranges,
			&r.LinesCovered, &r.LinesUncovered, &r.PctCovered, &r.RunAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetCoverageByFile returns the coverage row for a file, or nil if absent.
func (s *Store) GetCoverageByFile(filePath string) (*CoverageRow, error) {
	var r CoverageRow
	err := s.db.QueryRow(`
		SELECT file_path, content_hash, mode, ranges, lines_covered, lines_uncovered, pct_covered, run_at
		FROM coverage WHERE file_path = ?`, filePath).Scan(
		&r.FilePath, &r.ContentHash, &r.Mode, &r.Ranges,
		&r.LinesCovered, &r.LinesUncovered, &r.PctCovered, &r.RunAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GetAllNodeCoverage returns every per-node coverage row ordered by node id.
func (s *Store) GetAllNodeCoverage() ([]NodeCoverageRow, error) {
	rows, err := s.db.Query(`
		SELECT node_id, content_hash, lines_covered, lines_uncovered, pct_covered, run_at
		FROM node_coverage ORDER BY node_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []NodeCoverageRow
	for rows.Next() {
		var r NodeCoverageRow
		if err := rows.Scan(&r.NodeID, &r.ContentHash, &r.LinesCovered,
			&r.LinesUncovered, &r.PctCovered, &r.RunAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

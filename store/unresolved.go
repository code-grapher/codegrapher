package store

import (
	"database/sql"
	"encoding/json"

	"github.com/specscore/codegrapher/model"
)

const unresolvedColumns = `from_node_id, reference_name, reference_kind, line, col, candidates, file_path, language`

// InsertUnresolvedRef records a reference for later resolution.
func (s *Store) InsertUnresolvedRef(r model.UnresolvedReference) error {
	return insertUnresolvedRef(s.db, r)
}

func insertUnresolvedRef(db execer, r model.UnresolvedReference) error {
	var candidates any
	if r.Candidates != nil {
		if b, err := json.Marshal(r.Candidates); err == nil {
			candidates = string(b)
		}
	}
	lang := r.Language
	if lang == "" {
		lang = model.LangUnknown
	}
	_, err := db.Exec(`
		INSERT INTO unresolved_refs (`+unresolvedColumns+`)
		VALUES (?,?,?,?,?,?,?,?)`,
		r.FromNodeID, r.ReferenceName, string(r.ReferenceKind),
		r.Line, r.Column, candidates, r.FilePath, string(lang),
	)
	return err
}

// InsertUnresolvedRefs inserts references in one transaction.
func (s *Store) InsertUnresolvedRefs(refs []model.UnresolvedReference) error {
	if len(refs) == 0 {
		return nil
	}
	return s.Transaction(func(tx *sql.Tx) error {
		for _, r := range refs {
			if err := insertUnresolvedRef(tx, r); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteUnresolvedByNode removes references originating from nodeID.
func (s *Store) DeleteUnresolvedByNode(nodeID string) error {
	_, err := s.db.Exec(`DELETE FROM unresolved_refs WHERE from_node_id = ?`, nodeID)
	return err
}

// GetUnresolvedByName returns unresolved references with the given name.
func (s *Store) GetUnresolvedByName(name string) ([]model.UnresolvedReference, error) {
	rows, err := s.db.Query(
		`SELECT `+unresolvedColumns+` FROM unresolved_refs WHERE reference_name = ?`, name)
	if err != nil {
		return nil, err
	}
	return scanUnresolved(rows)
}

// GetUnresolvedReferences returns every unresolved reference.
func (s *Store) GetUnresolvedReferences() ([]model.UnresolvedReference, error) {
	rows, err := s.db.Query(`SELECT ` + unresolvedColumns + ` FROM unresolved_refs`)
	if err != nil {
		return nil, err
	}
	return scanUnresolved(rows)
}

// GetUnresolvedReferencesCount counts unresolved references without loading them.
func (s *Store) GetUnresolvedReferencesCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM unresolved_refs`).Scan(&n)
	return n, err
}

// GetUnresolvedReferencesBatch pages through unresolved references in
// bounded-memory chunks (LIMIT/OFFSET, rowid order — stable across pages).
func (s *Store) GetUnresolvedReferencesBatch(offset, limit int) ([]model.UnresolvedReference, error) {
	rows, err := s.db.Query(
		`SELECT `+unresolvedColumns+` FROM unresolved_refs ORDER BY id LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, err
	}
	return scanUnresolved(rows)
}

// GetUnresolvedReferencesByFiles returns references recorded in the given files.
func (s *Store) GetUnresolvedReferencesByFiles(filePaths []string) ([]model.UnresolvedReference, error) {
	var out []model.UnresolvedReference
	for chunk := range chunks(filePaths, sqliteParamChunkSize) {
		rows, err := s.db.Query(
			`SELECT `+unresolvedColumns+` FROM unresolved_refs WHERE file_path IN (`+
				placeholders(len(chunk))+`)`, anySlice(chunk)...)
		if err != nil {
			return nil, err
		}
		refs, err := scanUnresolved(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, refs...)
	}
	return out, nil
}

// ClearUnresolvedReferences deletes all unresolved references.
func (s *Store) ClearUnresolvedReferences() error {
	_, err := s.db.Exec(`DELETE FROM unresolved_refs`)
	return err
}

func scanUnresolved(rows *sql.Rows) ([]model.UnresolvedReference, error) {
	defer rows.Close()
	var out []model.UnresolvedReference
	for rows.Next() {
		var (
			r          model.UnresolvedReference
			kind, lang string
			candidates sql.NullString
		)
		if err := rows.Scan(&r.FromNodeID, &r.ReferenceName, &kind,
			&r.Line, &r.Column, &candidates, &r.FilePath, &lang); err != nil {
			return nil, err
		}
		r.ReferenceKind = model.EdgeKind(kind)
		r.Language = model.Language(lang)
		if candidates.Valid {
			var cc []string
			if json.Unmarshal([]byte(candidates.String), &cc) == nil {
				r.Candidates = cc
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

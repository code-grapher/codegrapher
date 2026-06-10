package store

import (
	"database/sql"
	"encoding/json"

	"github.com/specscore/codegrapher/model"
)

const fileColumns = `path, content_hash, language, size, modified_at, indexed_at, node_count, errors`

// UpsertFile inserts or updates a file record.
func (s *Store) UpsertFile(f model.FileRecord) error {
	var errs any
	if f.Errors != nil {
		if b, err := json.Marshal(f.Errors); err == nil {
			errs = string(b)
		}
	}
	_, err := s.db.Exec(`
		INSERT INTO files (`+fileColumns+`)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET
			content_hash = excluded.content_hash,
			language = excluded.language,
			size = excluded.size,
			modified_at = excluded.modified_at,
			indexed_at = excluded.indexed_at,
			node_count = excluded.node_count,
			errors = excluded.errors`,
		f.Path, f.ContentHash, string(f.Language), f.Size, f.ModifiedAt,
		f.IndexedAt, f.NodeCount, errs,
	)
	return err
}

// DeleteFile removes a file record and all nodes extracted from it.
func (s *Store) DeleteFile(filePath string) error {
	return s.Transaction(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM nodes WHERE file_path = ?`, filePath); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM files WHERE path = ?`, filePath)
		return err
	})
}

// GetFileByPath returns a file record, or nil if untracked.
func (s *Store) GetFileByPath(filePath string) (*model.FileRecord, error) {
	rows, err := s.db.Query(`SELECT `+fileColumns+` FROM files WHERE path = ?`, filePath)
	if err != nil {
		return nil, err
	}
	files, err := scanFiles(rows)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	return &files[0], nil
}

// GetAllFiles returns every tracked file ordered by path.
func (s *Store) GetAllFiles() ([]model.FileRecord, error) {
	rows, err := s.db.Query(`SELECT ` + fileColumns + ` FROM files ORDER BY path`)
	if err != nil {
		return nil, err
	}
	return scanFiles(rows)
}

// GetLastIndexedAt returns the most recent indexed_at across all files in ms,
// or 0 when nothing is indexed yet.
func (s *Store) GetLastIndexedAt() (int64, error) {
	var last sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(indexed_at) FROM files`).Scan(&last); err != nil {
		return 0, err
	}
	return last.Int64, nil
}

func scanFiles(rows *sql.Rows) ([]model.FileRecord, error) {
	defer rows.Close()
	var out []model.FileRecord
	for rows.Next() {
		var (
			f        model.FileRecord
			language string
			errs     sql.NullString
		)
		if err := rows.Scan(&f.Path, &f.ContentHash, &language, &f.Size,
			&f.ModifiedAt, &f.IndexedAt, &f.NodeCount, &errs); err != nil {
			return nil, err
		}
		f.Language = model.Language(language)
		if errs.Valid && errs.String != "" {
			var ee []model.ExtractionError
			if json.Unmarshal([]byte(errs.String), &ee) == nil {
				f.Errors = ee
			}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

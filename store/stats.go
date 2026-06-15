package store

import (
	"database/sql"
	"errors"

	"github.com/specscore/codegrapher/model"
)

// GraphStats summarizes the index, mirroring GraphStats in the original.
// DBSizeBytes is filled by the caller (Store.Size) like the original.
type GraphStats struct {
	NodeCount       int                    `json:"nodeCount"`
	EdgeCount       int                    `json:"edgeCount"`
	FileCount       int                    `json:"fileCount"`
	NodesByKind     map[model.NodeKind]int `json:"nodesByKind"`
	EdgesByKind     map[model.EdgeKind]int `json:"edgesByKind"`
	FilesByLanguage map[model.Language]int `json:"filesByLanguage"`
	DBSizeBytes     int64                  `json:"dbSizeBytes"`
	LastUpdated     int64                  `json:"lastUpdated"`
}

// GetStats returns aggregate counts for the whole index.
func (s *Store) GetStats() (GraphStats, error) {
	stats := GraphStats{
		NodesByKind:     map[model.NodeKind]int{},
		EdgesByKind:     map[model.EdgeKind]int{},
		FilesByLanguage: map[model.Language]int{},
		LastUpdated:     s.now(),
	}
	err := s.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM nodes),
			(SELECT COUNT(*) FROM edges),
			(SELECT COUNT(*) FROM files)`).
		Scan(&stats.NodeCount, &stats.EdgeCount, &stats.FileCount)
	if err != nil {
		return stats, err
	}

	if err := s.groupCount(`SELECT kind, COUNT(*) FROM nodes GROUP BY kind`, func(k string, n int) {
		stats.NodesByKind[model.NodeKind(k)] = n
	}); err != nil {
		return stats, err
	}
	if err := s.groupCount(`SELECT kind, COUNT(*) FROM edges GROUP BY kind`, func(k string, n int) {
		stats.EdgesByKind[model.EdgeKind(k)] = n
	}); err != nil {
		return stats, err
	}
	if err := s.groupCount(`SELECT language, COUNT(*) FROM files GROUP BY language`, func(k string, n int) {
		stats.FilesByLanguage[model.Language(k)] = n
	}); err != nil {
		return stats, err
	}
	return stats, nil
}

func (s *Store) groupCount(query string, add func(key string, count int)) error {
	rows, err := s.db.Query(query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		add(key, count)
	}
	return rows.Err()
}

// GetMetadata returns a project metadata value, or "" if absent.
func (s *Store) GetMetadata(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM project_metadata WHERE key = ?`, key).Scan(&v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// SetMetadata upserts a project metadata key-value pair.
func (s *Store) SetMetadata(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO project_metadata (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, s.now())
	return err
}

// GetAllMetadata returns every metadata key-value pair.
func (s *Store) GetAllMetadata() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM project_metadata`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Clear deletes all graph data (nodes, edges, files, unresolved refs).
func (s *Store) Clear() error {
	for _, stmt := range []string{
		`DELETE FROM unresolved_refs`,
		`DELETE FROM edges`,
		`DELETE FROM nodes`,
		`DELETE FROM files`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

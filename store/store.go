// Package store implements the .codegraph SQLite knowledge-graph store:
// nodes, edges, files, unresolved references, and project metadata, plus the
// FTS5 search index maintained by schema triggers.
//
// Ported from src/db/ of github.com/colbymchenry/codegraph (MIT). The schema
// (schema.sql) is copied verbatim from the original so indexes remain
// conceptually compatible; the SQLite driver is modernc.org/sqlite (pure Go,
// FTS5 included) per ADR-001's pure-Go mandate.
//
// Like the original (one node:sqlite handle), the Store uses a single
// connection; concurrent use is safe via database/sql's serialization plus
// WAL mode and busy_timeout.
package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// DatabaseFilename is the on-disk name of the index database.
const DatabaseFilename = "codegraph.db"

// CurrentSchemaVersion mirrors CURRENT_SCHEMA_VERSION in the original.
const CurrentSchemaVersion = 6

// NowFunc returns the current time in Unix milliseconds. Injectable for tests.
type NowFunc func() int64

func defaultNow() int64 { return time.Now().UnixMilli() }

// Store is an open codegraph index database.
type Store struct {
	db   *sql.DB
	path string
	now  NowFunc
}

// Option configures a Store.
type Option func(*Store)

// WithNowFunc injects the clock used for updated_at/applied_at timestamps.
func WithNowFunc(now NowFunc) Option {
	return func(s *Store) { s.now = now }
}

// dsn builds the modernc.org/sqlite DSN with the same connection-level
// pragmas the original applies (busy_timeout first — see src/db/index.ts).
func dsn(path string) string {
	pragmas := []string{
		"busy_timeout(5000)",
		"foreign_keys(ON)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"cache_size(-64000)",
		"temp_store(MEMORY)",
		"mmap_size(268435456)",
	}
	parts := make([]string, len(pragmas))
	for i, p := range pragmas {
		parts[i] = "_pragma=" + p
	}
	return "file:" + path + "?" + strings.Join(parts, "&")
}

func openDB(path string, opts []Option) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// Single connection, like the original's single node:sqlite handle.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, path: path, now: defaultNow}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Initialize creates a new database at path (parent directories included),
// applies the schema, and records the current schema version.
func Initialize(path string, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store: mkdir: %w", err)
	}
	s, err := openDB(path, opts)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(schemaSQL); err != nil {
		s.db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	// Fresh schema.sql already includes every migration's effects; record the
	// version so migrations aren't re-applied on open (mirrors initialize()).
	v, err := s.schemaVersion()
	if err != nil {
		s.db.Close()
		return nil, err
	}
	if v < CurrentSchemaVersion {
		if _, err := s.db.Exec(
			`INSERT OR IGNORE INTO schema_versions (version, applied_at, description) VALUES (?, ?, ?)`,
			CurrentSchemaVersion, s.now(), "Initial schema includes all migrations",
		); err != nil {
			s.db.Close()
			return nil, fmt.Errorf("store: record schema version: %w", err)
		}
	}
	return s, nil
}

// Open opens an existing database and applies any pending migrations.
func Open(path string, opts ...Option) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store: database not found: %s", path)
	}
	s, err := openDB(path, opts)
	if err != nil {
		return nil, err
	}
	v, err := s.schemaVersion()
	if err != nil {
		s.db.Close()
		return nil, err
	}
	if v < CurrentSchemaVersion {
		if err := s.runMigrations(v); err != nil {
			s.db.Close()
			return nil, err
		}
	}
	return s, nil
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Size returns the database file size in bytes.
func (s *Store) Size() (int64, error) {
	fi, err := os.Stat(s.path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// JournalMode reports the journal mode actually in effect ("wal", "delete",
// …). SQLite silently keeps the prior mode when WAL can't be enabled (e.g.
// network mounts), so this is surfaced in status for triage (issue #238).
func (s *Store) JournalMode() string {
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return ""
	}
	return strings.ToLower(mode)
}

// Transaction runs fn inside a single SQLite transaction.
func (s *Store) Transaction(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// RunMaintenance performs lightweight post-bulk-write maintenance
// (PRAGMA optimize + passive WAL checkpoint). Best-effort: errors ignored.
func (s *Store) RunMaintenance() {
	s.db.Exec("PRAGMA optimize")                //nolint:errcheck
	s.db.Exec("PRAGMA wal_checkpoint(PASSIVE)") //nolint:errcheck
}

// Optimize vacuums and analyzes the database.
func (s *Store) Optimize() error {
	if _, err := s.db.Exec("VACUUM"); err != nil {
		return err
	}
	_, err := s.db.Exec("ANALYZE")
	return err
}

// SchemaVersion returns the highest applied schema version (0 if none).
func (s *Store) SchemaVersion() (int, error) { return s.schemaVersion() }

func (s *Store) schemaVersion() (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRow("SELECT MAX(version) FROM schema_versions").Scan(&v)
	if err != nil {
		if isMissingTable(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return int(v.Int64), nil
}

func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// migration mirrors src/db/migrations.ts. Version 1 is schema.sql itself.
type migration struct {
	version     int
	description string
	sql         string
}

var migrations = []migration{
	{2, "Add project metadata, provenance tracking, and unresolved ref context", `
		CREATE TABLE IF NOT EXISTS project_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		);
		ALTER TABLE unresolved_refs ADD COLUMN file_path TEXT NOT NULL DEFAULT '';
		ALTER TABLE unresolved_refs ADD COLUMN language TEXT NOT NULL DEFAULT 'unknown';
		ALTER TABLE edges ADD COLUMN provenance TEXT DEFAULT NULL;
		CREATE INDEX IF NOT EXISTS idx_unresolved_file_path ON unresolved_refs(file_path);
		CREATE INDEX IF NOT EXISTS idx_edges_provenance ON edges(provenance);`},
	{3, "Add lower(name) expression index for memory-efficient case-insensitive lookups",
		`CREATE INDEX IF NOT EXISTS idx_nodes_lower_name ON nodes(lower(name));`},
	{4, "Drop redundant idx_edges_source / idx_edges_target (covered by composites)", `
		DROP INDEX IF EXISTS idx_edges_source;
		DROP INDEX IF EXISTS idx_edges_target;`},
	{5, "Add nodes.return_type — normalized return/result type for receiver-type inference (#645)",
		`ALTER TABLE nodes ADD COLUMN return_type TEXT;`},
	{6, "Add coverage + node_coverage tables for Go line-coverage attribution", `
		CREATE TABLE IF NOT EXISTS coverage (
			file_path     TEXT NOT NULL,
			content_hash  TEXT NOT NULL,
			mode          TEXT NOT NULL,
			ranges        TEXT NOT NULL,
			lines_covered   INTEGER NOT NULL,
			lines_uncovered INTEGER NOT NULL,
			pct_covered     REAL NOT NULL,
			run_at        INTEGER NOT NULL,
			PRIMARY KEY (file_path)
		);
		CREATE TABLE IF NOT EXISTS node_coverage (
			node_id         TEXT NOT NULL,
			content_hash    TEXT NOT NULL,
			lines_covered   INTEGER NOT NULL,
			lines_uncovered INTEGER NOT NULL,
			pct_covered     REAL NOT NULL,
			run_at          INTEGER NOT NULL,
			PRIMARY KEY (node_id),
			FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_node_coverage_hash ON node_coverage(content_hash);`},
}

func (s *Store) runMigrations(from int) error {
	for _, m := range migrations {
		if m.version <= from {
			continue
		}
		err := s.Transaction(func(tx *sql.Tx) error {
			if _, err := tx.Exec(m.sql); err != nil {
				return fmt.Errorf("store: migration v%d: %w", m.version, err)
			}
			_, err := tx.Exec(
				`INSERT INTO schema_versions (version, applied_at, description) VALUES (?, ?, ?)`,
				m.version, s.now(), m.description,
			)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

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
// busy_timeout. The index is derived, rebuildable data with rare, short
// writes, so SQLite's default rollback journal is used (no WAL): there are no
// -wal/-shm side files, and an idle connection holds no lock.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io"
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
const CurrentSchemaVersion = 9

// NowFunc returns the current time in Unix milliseconds. Injectable for tests.
type NowFunc func() int64

func defaultNow() int64 { return time.Now().UnixMilli() }

// Store is an open codegraph index database.
type Store struct {
	db   *sql.DB
	path string
	now  NowFunc

	allowStale    bool
	upgradeReason string
}

// Option configures a Store.
type Option func(*Store)

// WithNowFunc injects the clock used for updated_at/applied_at timestamps.
func WithNowFunc(now NowFunc) Option {
	return func(s *Store) { s.now = now }
}

// WithAllowStale lets OpenReadOnly open a database whose schema is older than
// CurrentSchemaVersion (for diagnostics such as `status`) instead of failing.
func WithAllowStale() Option {
	return func(s *Store) { s.allowStale = true }
}

// dsn builds the modernc.org/sqlite DSN with the same connection-level
// pragmas the original applies (busy_timeout first — see src/db/index.ts).
func dsn(path string) string {
	return buildDSN(path, false)
}

// readOnlyDSN is the DSN for a read-only handle: mode=ro plus only the
// connection-local pragmas that never write to the database file.
func readOnlyDSN(path string) string {
	return buildDSN(path, true)
}

func buildDSN(path string, readOnly bool) string {
	pragmas := []string{
		"busy_timeout(5000)",
		"foreign_keys(ON)",
		"synchronous(NORMAL)",
		"cache_size(-64000)",
		"temp_store(MEMORY)",
		"mmap_size(268435456)",
	}
	parts := make([]string, 0, len(pragmas)+1)
	if readOnly {
		parts = append(parts, "mode=ro")
	}
	for _, p := range pragmas {
		parts = append(parts, "_pragma="+p)
	}
	return "file:" + path + "?" + strings.Join(parts, "&")
}

// ErrNeedsUpgrade is matched (errors.Is) by every "index needs upgrade" error.
var ErrNeedsUpgrade = errors.New("index needs upgrade")

// NeedsUpgradeError reports that an index cannot be read without a
// read-write upgrade: its schema is older than CurrentSchemaVersion, or it is
// still in WAL journal mode. Its message names the commands that upgrade it.
type NeedsUpgradeError struct {
	Path   string
	Reason string
}

func (e *NeedsUpgradeError) Error() string {
	return fmt.Sprintf("index needs upgrade: %s (%s); run `codegrapher sync` or pass --refresh to upgrade it", e.Path, e.Reason)
}

// Is makes errors.Is(err, ErrNeedsUpgrade) true.
func (e *NeedsUpgradeError) Is(target error) bool { return target == ErrNeedsUpgrade }

func openDB(path string, opts []Option) (*Store, error) {
	return openDSN(path, dsn(path), opts)
}

func openDSN(path, dataSource string, opts []Option) (*Store, error) {
	db, err := sql.Open("sqlite", dataSource)
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

// convertJournal switches a legacy WAL index to the default rollback journal.
// It runs only on read-write opens: it is the explicit upgrade step.
func (s *Store) convertJournal() error {
	if s.JournalMode() != "wal" {
		return nil
	}
	if _, err := s.db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		return fmt.Errorf("store: convert %s from WAL: %w", s.path, err)
	}
	return nil
}

// fileIsWAL reports whether the SQLite header at path declares WAL mode
// (file-format version bytes 18 and 19 equal 2). Reading the header directly
// avoids opening the database, which for a WAL file would create -shm.
func fileIsWAL(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	hdr := make([]byte, 20)
	if _, err := io.ReadFull(f, hdr); err != nil {
		// Shorter than a header: empty or not yet a database; nothing to convert.
		return false, nil
	}
	return hdr[18] == 2 || hdr[19] == 2, nil
}

// OpenReadOnly opens an existing database without writing to it: no
// migration, no journal conversion, no write pragmas. A database that is
// still in WAL mode, or whose schema is older than CurrentSchemaVersion,
// yields a *NeedsUpgradeError (WAL is never opened; with WithAllowStale a
// stale schema is opened anyway and reported through UpgradeReason).
func OpenReadOnly(path string, opts ...Option) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store: database not found: %s", path)
	}
	wal, err := fileIsWAL(path)
	if err != nil {
		return nil, fmt.Errorf("store: read header %s: %w", path, err)
	}
	if wal {
		return nil, &NeedsUpgradeError{Path: path, Reason: "legacy WAL journal mode"}
	}
	s, err := openDSN(path, readOnlyDSN(path), opts)
	if err != nil {
		return nil, err
	}
	v, err := s.schemaVersion()
	if err != nil {
		_ = s.db.Close()
		return nil, err
	}
	if v < CurrentSchemaVersion {
		s.upgradeReason = fmt.Sprintf("schema v%d, current v%d", v, CurrentSchemaVersion)
		if !s.allowStale {
			_ = s.db.Close()
			return nil, &NeedsUpgradeError{Path: path, Reason: s.upgradeReason}
		}
	}
	return s, nil
}

// UpgradeReason is non-empty when a stale-schema database was opened
// read-only with WithAllowStale; it explains why an upgrade is needed.
func (s *Store) UpgradeReason() string { return s.upgradeReason }

// Initialize creates a database at path when needed, or opens and migrates an
// existing database. Bootstrap runs under an immediate transaction so two
// initializers cannot both observe a partially-created schema.
func Initialize(path string, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store: mkdir: %w", err)
	}
	s, err := openDB(path, opts)
	if err != nil {
		return nil, err
	}
	if err := s.convertJournal(); err != nil {
		_ = s.db.Close()
		return nil, err
	}
	fresh, err := s.bootstrapSchema()
	if err != nil {
		_ = s.db.Close()
		return nil, err
	}
	v, err := s.schemaVersion()
	if err != nil {
		_ = s.db.Close()
		return nil, err
	}
	if !fresh && v < CurrentSchemaVersion {
		if err := s.runMigrations(v); err != nil {
			_ = s.db.Close()
			return nil, err
		}
	}
	return s, nil
}

// bootstrapSchema applies schema.sql only when schema_versions does not exist.
// BEGIN IMMEDIATE serializes that decision across processes before either can
// expose a partial bootstrap to the other.
func (s *Store) bootstrapSchema() (fresh bool, err error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		fresh, err = s.bootstrapSchemaOnce()
		if err == nil || !isSQLiteBusy(err) || time.Now().After(deadline) {
			return fresh, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func isSQLiteBusy(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked"))
}

func (s *Store) bootstrapSchemaOnce() (fresh bool, err error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("store: bootstrap connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false, fmt.Errorf("store: begin bootstrap: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	var exists int
	if err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'schema_versions'`).Scan(&exists); err != nil {
		return false, fmt.Errorf("store: inspect schema: %w", err)
	}
	if exists == 0 {
		if _, err := conn.ExecContext(ctx, schemaSQL); err != nil {
			return false, fmt.Errorf("store: apply schema: %w", err)
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO schema_versions (version, applied_at, description) VALUES (?, ?, ?)
			ON CONFLICT(version) DO NOTHING`, CurrentSchemaVersion, s.now(), "Initial schema includes all migrations"); err != nil {
			return false, fmt.Errorf("store: record schema version: %w", err)
		}
		fresh = true
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return false, fmt.Errorf("store: commit bootstrap: %w", err)
	}
	committed = true
	return fresh, nil
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
	if err := s.convertJournal(); err != nil {
		_ = s.db.Close()
		return nil, err
	}
	v, err := s.schemaVersion()
	if err != nil {
		_ = s.db.Close()
		return nil, err
	}
	if v < CurrentSchemaVersion {
		if err := s.runMigrations(v); err != nil {
			_ = s.db.Close()
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

// JournalMode reports the journal mode actually in effect: "delete" for a
// current index, "wal" for a legacy one not yet upgraded. Surfaced in status.
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
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// RunMaintenance performs lightweight post-bulk-write maintenance
// (PRAGMA optimize). Best-effort: errors ignored.
func (s *Store) RunMaintenance() {
	s.db.Exec("PRAGMA optimize") //nolint:errcheck
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
	{7, "Add nodes.metadata — structured language-specific attributes (SQLite .db schema details)",
		`ALTER TABLE nodes ADD COLUMN metadata TEXT;`},
	{8, "Add cross-scope SpecScore trace projection", `
		CREATE TABLE IF NOT EXISTS trace_nodes (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			reference TEXT NOT NULL,
			title TEXT,
			path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			start_column INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			end_column INTEGER NOT NULL,
			status TEXT
		);
		CREATE TABLE IF NOT EXISTS trace_edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			relation TEXT NOT NULL,
			accepted INTEGER NOT NULL DEFAULT 0,
			source_path TEXT NOT NULL,
			source_line INTEGER NOT NULL,
			source_column INTEGER NOT NULL,
			target_reference TEXT NOT NULL,
			FOREIGN KEY (source_id) REFERENCES trace_nodes(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_trace_nodes_reference ON trace_nodes(reference);
		CREATE INDEX IF NOT EXISTS idx_trace_edges_target ON trace_edges(target_id, relation, accepted);
		CREATE INDEX IF NOT EXISTS idx_trace_edges_source ON trace_edges(source_id, relation);`},
	{9, "Preserve exact Go coverage blocks, statement totals, and ref", `
		ALTER TABLE coverage ADD COLUMN blocks TEXT NOT NULL DEFAULT '[]';
		ALTER TABLE coverage ADD COLUMN statements_covered INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE coverage ADD COLUMN statements_uncovered INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE coverage ADD COLUMN ref TEXT NOT NULL DEFAULT '';
		ALTER TABLE node_coverage ADD COLUMN statements_covered INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE node_coverage ADD COLUMN statements_uncovered INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE node_coverage ADD COLUMN ref TEXT NOT NULL DEFAULT '';`},
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

package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeWALDB creates a current-schema database left in WAL journal mode, like
// an index written by a pre-rollback-journal release.
func makeWALDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), DatabaseFilename)
	s, err := Initialize(path, WithNowFunc(fixedNow))
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := s.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatalf("enable WAL: %v", err)
	}
	// Closing the last connection checkpoints and removes -wal; the WAL header
	// bytes stay set, which is what a legacy index looks like.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return path
}

// makeStaleDB creates a rollback-journal database whose recorded schema
// version is older than CurrentSchemaVersion.
func makeStaleDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), DatabaseFilename)
	s, err := Initialize(path, WithNowFunc(fixedNow))
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if _, err := s.db.Exec("DELETE FROM schema_versions WHERE version > 8"); err != nil {
		t.Fatalf("make stale: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return path
}

func TestDSN_NoWALAndReadOnlyMode(t *testing.T) {
	if strings.Contains(dsn("x.db"), "journal_mode") {
		t.Errorf("read-write dsn must not set journal_mode: %s", dsn("x.db"))
	}
	if strings.Contains(dsn("x.db"), "mode=ro") {
		t.Errorf("read-write dsn must not be read-only: %s", dsn("x.db"))
	}
	ro := readOnlyDSN("x.db")
	if !strings.Contains(ro, "mode=ro") || strings.Contains(ro, "journal_mode") {
		t.Errorf("unexpected read-only dsn: %s", ro)
	}
}

func TestOpenReadOnly_ReadsAndRefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFilename)
	w, err := Initialize(path, WithNowFunc(fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.InsertNode(testNode("n1", "Alpha", "a.go", 1)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = r.Close() }()
	if r.UpgradeReason() != "" {
		t.Errorf("unexpected upgrade reason %q", r.UpgradeReason())
	}
	if got := r.JournalMode(); got != "delete" {
		t.Errorf("journal mode = %q, want delete", got)
	}
	n, err := r.GetNodeByID("n1")
	if err != nil || n == nil || n.Name != "Alpha" {
		t.Fatalf("GetNode = %v, %v", n, err)
	}
	if err := r.InsertNode(testNode("n2", "Beta", "b.go", 1)); err == nil {
		t.Error("write through a read-only store must fail")
	}
	for _, side := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + side); err == nil {
			t.Errorf("read-only open left %s%s", filepath.Base(path), side)
		}
	}
}

func TestOpenReadOnly_Missing(t *testing.T) {
	if _, err := OpenReadOnly(filepath.Join(t.TempDir(), "nope.db")); err == nil {
		t.Error("expected error for missing database")
	}
}

func TestOpenReadOnly_UnreadablePath(t *testing.T) {
	// A directory stats fine but cannot be read as a file header.
	if _, err := OpenReadOnly(t.TempDir()); err == nil {
		t.Error("expected error for a directory")
	}
}

func TestOpenReadOnly_WALNeedsUpgrade(t *testing.T) {
	path := makeWALDB(t)
	before, _ := os.ReadFile(path)
	_, err := OpenReadOnly(path)
	if !errors.Is(err, ErrNeedsUpgrade) {
		t.Fatalf("err = %v, want ErrNeedsUpgrade", err)
	}
	for _, want := range []string{"codegrapher sync", "--refresh", "WAL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %q", err.Error(), want)
		}
	}
	var nu *NeedsUpgradeError
	if !errors.As(err, &nu) || nu.Path != path {
		t.Errorf("errors.As NeedsUpgradeError = %v", nu)
	}
	if after, _ := os.ReadFile(path); string(before) != string(after) {
		t.Error("database bytes changed")
	}
	if _, err := os.Stat(path + "-shm"); err == nil {
		t.Error("-shm created")
	}
	// Even with WithAllowStale a WAL database is never opened.
	if _, err := OpenReadOnly(path, WithAllowStale()); !errors.Is(err, ErrNeedsUpgrade) {
		t.Errorf("allow-stale WAL err = %v", err)
	}
}

func TestOpenReadOnly_StaleSchema(t *testing.T) {
	path := makeStaleDB(t)
	if _, err := OpenReadOnly(path); !errors.Is(err, ErrNeedsUpgrade) {
		t.Fatalf("err = %v, want ErrNeedsUpgrade", err)
	}
	s, err := OpenReadOnly(path, WithAllowStale())
	if err != nil {
		t.Fatalf("allow-stale: %v", err)
	}
	defer func() { _ = s.Close() }()
	if !strings.Contains(s.UpgradeReason(), "schema v1") {
		t.Errorf("UpgradeReason = %q", s.UpgradeReason())
	}
}

func TestOpenReadOnly_NotADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFilename)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReadOnly(path); err == nil || errors.Is(err, ErrNeedsUpgrade) {
		t.Errorf("err = %v, want a plain error", err)
	}
}

func TestFileIsWAL_ShortFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.db")
	if err := os.WriteFile(path, []byte("tiny"), 0o644); err != nil {
		t.Fatal(err)
	}
	wal, err := fileIsWAL(path)
	if err != nil || wal {
		t.Errorf("fileIsWAL(short) = %v, %v", wal, err)
	}
	if _, err := fileIsWAL(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestOpen_ConvertsWALToRollbackJournal(t *testing.T) {
	for name, open := range map[string]func(string) (*Store, error){
		"Open":       func(p string) (*Store, error) { return Open(p, WithNowFunc(fixedNow)) },
		"Initialize": func(p string) (*Store, error) { return Initialize(p, WithNowFunc(fixedNow)) },
	} {
		t.Run(name, func(t *testing.T) {
			path := makeWALDB(t)
			if wal, _ := fileIsWAL(path); !wal {
				t.Fatal("fixture is not WAL")
			}
			s, err := open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if got := s.JournalMode(); got != "delete" {
				t.Errorf("journal mode after upgrade = %q", got)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if wal, _ := fileIsWAL(path); wal {
				t.Error("file header still declares WAL")
			}
			r, err := OpenReadOnly(path)
			if err != nil {
				t.Fatalf("read-only open after upgrade: %v", err)
			}
			_ = r.Close()
		})
	}
}

func TestConvertJournal_BusyReturnsError(t *testing.T) {
	path := makeWALDB(t)
	// A second WAL connection with an open read transaction prevents the
	// switch back to a rollback journal.
	other, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	other.SetMaxOpenConns(1)
	tx, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&n); err != nil {
		t.Fatal(err)
	}

	s, err := openDB(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.db.Close() }()
	if _, err := s.db.Exec("PRAGMA busy_timeout=20"); err != nil {
		t.Fatal(err)
	}
	if err := s.convertJournal(); err == nil {
		t.Fatal("expected a conversion error while another connection reads")
	}
}

func TestRollbackJournal_NoSideFilesAndNoIdleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFilename)
	w, err := Initialize(path, WithNowFunc(fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if err := w.InsertNode(testNode("n1", "Alpha", "a.go", 1)); err != nil {
		t.Fatal(err)
	}
	for _, side := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + side); err == nil {
			t.Errorf("unexpected %s file", side)
		}
	}
	// An idle read-only handle must not block another connection's write.
	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if _, err := r.GetNodeByID("n1"); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertNode(testNode("n2", "Beta", "b.go", 1)); err != nil {
		t.Fatalf("write while idle reader open: %v", err)
	}
}

// ---- WithFastWrites and corrupt-index errors ----

func pragmaInt(t *testing.T, s *Store, pragma string) int {
	t.Helper()
	var v int
	if err := s.db.QueryRow("PRAGMA " + pragma).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", pragma, err)
	}
	return v
}

func TestWithFastWrites_TunesWriteHandleOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), DatabaseFilename)
	w, err := Initialize(path, WithNowFunc(fixedNow), WithFastWrites())
	if err != nil {
		t.Fatal(err)
	}
	if got := w.JournalMode(); got != "memory" {
		t.Errorf("write journal mode = %q, want memory", got)
	}
	if got := pragmaInt(t, w, "synchronous"); got != 0 {
		t.Errorf("synchronous = %d, want 0 (OFF)", got)
	}
	for i := 0; i < 50; i++ {
		if err := w.InsertNode(testNode("n"+string(rune('A'+i)), "Fn", "a.go", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	for _, side := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + side); err == nil {
			t.Errorf("fast write handle left %s", side)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// The tuning is per connection, never persisted: a reopened handle without
	// the option, and a read-only handle, both run on the defaults.
	before, _ := os.ReadFile(path)
	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if got := r.JournalMode(); got != "delete" {
		t.Errorf("read-only journal mode = %q, want delete", got)
	}
	if got := pragmaInt(t, r, "synchronous"); got != 2 && got != 1 {
		t.Errorf("read-only synchronous = %d, want a durable default (NORMAL/FULL)", got)
	}
	if _, err := r.GetNodeByID("nA"); err != nil {
		t.Fatal(err)
	}
	if err := r.InsertNode(testNode("zz", "Fn", "z.go", 1)); err == nil {
		t.Error("read-only handle accepted a write")
	}
	if after, _ := os.ReadFile(path); string(before) != string(after) {
		t.Error("read-only open modified the database")
	}
	plain, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close() }()
	if got := plain.JournalMode(); got != "delete" {
		t.Errorf("untuned handle journal mode = %q", got)
	}
}

func TestWithFastWrites_OpenConvertsLegacyWALThenTunes(t *testing.T) {
	path := makeWALDB(t)
	s, err := Open(path, WithFastWrites())
	if err != nil {
		t.Fatal(err)
	}
	if got := s.JournalMode(); got != "memory" {
		t.Errorf("journal mode = %q", got)
	}
	_ = s.Close()
	if wal, _ := fileIsWAL(path); wal {
		t.Error("legacy WAL header survived the upgrade")
	}
}

func TestTuneWrites_NoopWithoutOption(t *testing.T) {
	s := newTestStore(t)
	if err := s.tuneWrites(); err != nil {
		t.Fatal(err)
	}
	if got := s.JournalMode(); got != "delete" {
		t.Errorf("journal mode = %q", got)
	}
}

func TestTuneWrites_FailsLoudly(t *testing.T) {
	// Closed handle: the first pragma fails.
	s := newTestStore(t)
	s.fastWrites = true
	_ = s.db.Close()
	if err := s.tuneWrites(); err == nil {
		t.Error("expected an error from a closed handle")
	}

	// journal_mode cannot be switched while another connection holds a read
	// transaction on a WAL database: that must be an error, not a silent no-op.
	path := makeWALDB(t)
	other, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	other.SetMaxOpenConns(1)
	tx, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&n); err != nil {
		t.Fatal(err)
	}
	busy, err := openDB(path, []Option{WithFastWrites()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.db.Close() }()
	if _, err := busy.db.Exec("PRAGMA busy_timeout=20"); err != nil {
		t.Fatal(err)
	}
	if err := busy.tuneWrites(); err == nil {
		t.Error("expected an error while the journal mode cannot be switched")
	}
}

func TestCorruptIndexIsReportedWithRebuildAdvice(t *testing.T) {
	garbage := func(t *testing.T) string {
		path := filepath.Join(t.TempDir(), DatabaseFilename)
		if err := os.WriteFile(path, []byte(strings.Repeat("not sqlite ", 500)), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	opens := map[string]func(string) (*Store, error){
		"Open":         func(p string) (*Store, error) { return Open(p, WithFastWrites()) },
		"Initialize":   func(p string) (*Store, error) { return Initialize(p, WithFastWrites()) },
		"OpenReadOnly": func(p string) (*Store, error) { return OpenReadOnly(p) },
	}
	for name, open := range opens {
		t.Run(name, func(t *testing.T) {
			_, err := open(garbage(t))
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("err = %v, want ErrCorrupt", err)
			}
			if !strings.Contains(err.Error(), "codegrapher uninit") || !strings.Contains(err.Error(), "codegrapher init") {
				t.Errorf("message lacks rebuild command: %q", err.Error())
			}
			var ce *CorruptError
			if !errors.As(err, &ce) || ce.Unwrap() == nil {
				t.Errorf("CorruptError = %v", ce)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	if classify("x", nil) != nil {
		t.Error("nil must stay nil")
	}
	plain := errors.New("disk full")
	if classify("x", plain) != plain {
		t.Error("non-corruption errors pass through")
	}
	for _, msg := range []string{"database disk image is malformed", "file is not a database (26)", "SQLITE_CORRUPT", "SQLITE_NOTADB"} {
		if !errors.Is(classify("x", errors.New(msg)), ErrCorrupt) {
			t.Errorf("%q not classified as corrupt", msg)
		}
	}
}

func TestRequireJournalMode(t *testing.T) {
	if err := requireJournalMode("x.db", "memory", "MEMORY"); err != nil {
		t.Errorf("matching mode: %v", err)
	}
	err := requireJournalMode("x.db", "memory", "wal")
	if err == nil || !strings.Contains(err.Error(), `reports "wal"`) {
		t.Errorf("mismatch err = %v", err)
	}
}

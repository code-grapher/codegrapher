package indexer

// Worktree index seeding (code-grapher/codegrapher#67).
//
// A fresh linked Git worktree has no index. Building one from scratch costs a
// full init, while the canonical clone (the main working tree of the same
// repository) usually already holds an index that differs from what the
// worktree needs by a handful of files. SeedFromCanonical copies that index —
// consistently, and copy-on-write where the file system supports it — into the
// worktree, stamps the copy with the worktree's identity, and leaves the
// normal incremental refresh to catch up the difference.
//
// The canonical clone is only ever read: rollback-journal databases are
// opened mode=ro (which creates no side files), legacy WAL-mode ones are only
// byte-copied (a SQLite open would create -wal/-shm beside them), its lock
// file is only read, and nothing is created beside them.
// Seeding never refreshes the canonical index first — the worktree's own
// incremental pass is correct whatever the base looks like.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	graphlock "github.com/specscore/codegrapher/lock"
)

// Metadata keys written on a seeded copy.
const (
	// indexedGitDirtyKey records whether the working tree had uncommitted or
	// untracked changes when indexedGitHeadKey was stamped: "0" clean, "1"
	// dirty, "" unknown. A seeded copy keeps the base revision (and so the
	// git-diff fast path) only when it is known clean.
	indexedGitDirtyKey = "indexed_git_dirty"
	// seededFromKey names the index root a copy was seeded from.
	seededFromKey = "seeded_from"
	// worktreeRootKey is the identity stamp: the working tree an index
	// belongs to.
	worktreeRootKey = "worktree_root"
)

// LinkedWorktree reports, for a path inside a linked Git worktree, that
// worktree's root and the root of the repository's main (canonical) working
// tree. ok is false outside Git, inside the main working tree itself, or when
// the repository is bare (there is no canonical checkout to seed from).
func LinkedWorktree(dir string) (worktreeRoot, canonicalRoot string, ok bool) {
	out, err := gitOutput(dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir", "--git-common-dir")
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		return "", "", false
	}
	worktreeRoot = realpathOrAbs(strings.TrimSpace(lines[0]))
	gitDir := realpathOrAbs(strings.TrimSpace(lines[1]))
	commonDir := realpathOrAbs(strings.TrimSpace(lines[2]))
	if gitDir == commonDir || filepath.Base(commonDir) != ".git" {
		return "", "", false
	}
	canonicalRoot = filepath.Dir(commonDir)
	if canonicalRoot == worktreeRoot {
		return "", "", false
	}
	return worktreeRoot, canonicalRoot, true
}

// SeedMethod says how a database file was copied.
type SeedMethod string

const (
	SeedClone SeedMethod = "clone" // copy-on-write clone (APFS clonefile, FICLONE reflink)
	SeedCopy  SeedMethod = "copy"  // plain byte copy
)

// SeedResult describes a completed seed.
type SeedResult struct {
	Source    string     // index root copied from
	Target    string     // index root written
	Databases int        // database files copied
	Method    SeedMethod // weakest method used across the files
	// Published is true when this call published the copy; false when a
	// concurrent seed of the same target finished first.
	Published bool
	// FastPath is true when the base revision was kept, so the first refresh
	// diffs base..HEAD plus working-tree changes instead of hashing every file.
	FastPath bool
	Duration time.Duration
}

// Seams for tests.
var (
	cloneFileFunc = cloneFile
	// seedLockWait bounds how long seeding waits for a canonical writer.
	seedLockWait = 10 * time.Second
	// seedPeerWait bounds how long a command waits for a concurrent seed of
	// the same target (copy plus catch-up). The lock protocol treats a lock
	// older than two minutes as stale anyway.
	seedPeerWait = 2 * time.Minute
)

// seedLockPath is the per-target lock that serializes seeding and catch-up.
func seedLockPath(targetRoot string) string {
	return filepath.Join(targetRoot, defaultCodeGraphDir+"-seed.lock")
}

// WaitForSeed blocks (stat-only polling, bounded) while another process is
// seeding or catching up the index at targetRoot, so a command started in
// that window never reads a half-caught-up copy.
func WaitForSeed(targetRoot string) {
	lockPath := seedLockPath(targetRoot)
	if _, err := os.Stat(lockPath); err != nil {
		return
	}
	deadline := time.Now().Add(seedPeerWait)
	for writerHoldsLock(lockPath) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
}

// seedAttempts bounds how often seeding retries a copy that a canonical
// writer changed underneath it.
const seedAttempts = 3

// SeedFromCanonical copies the index rooted at sourceRoot into targetRoot,
// which must not be initialized yet. It never writes under sourceRoot.
//
// The copy is consistent across every scope database, not just within each:
// it fingerprints the source set (size, mtime and SQLite change counter of
// each file) before and after copying, requires that no writer held the
// canonical lock at either end, and retries otherwise; a source that keeps
// changing fails the seed. Concurrent seeds of one target are serialized by
// a lock beside the target, and the finished copy is published with one
// directory rename, so a reader never sees a partial set.
//
// afterPublish (may be nil) runs once the copy is in place, still under the
// seed lock, so concurrent commands wait for the catch-up instead of racing
// it. If it fails, the copy this call published is removed and the error
// returned.
func SeedFromCanonical(sourceRoot, targetRoot string, afterPublish func() error) (SeedResult, error) {
	start := time.Now()
	res := SeedResult{Source: sourceRoot, Target: targetRoot}
	if IsInitialized(targetRoot) {
		return res, fmt.Errorf("seed: %s already has an index", targetRoot)
	}
	srcDir := GetCodeGraphDir(sourceRoot)
	targetDir := GetCodeGraphDir(targetRoot)
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return res, fmt.Errorf("seed: %w", err)
	}

	seedLock := graphlock.New(seedLockPath(filepath.Dir(targetDir)))
	if err := acquireWithin(seedLock, seedPeerWait); err != nil {
		if IsInitialized(targetRoot) {
			res.Duration = time.Since(start)
			return res, nil // a concurrent seed finished first
		}
		return res, fmt.Errorf("seed: %w", err)
	}
	defer seedLock.Release()
	if IsInitialized(targetRoot) {
		res.Duration = time.Since(start)
		return res, nil
	}
	// Staging left by a crashed seed of this target (the lock is ours, so no
	// live seed owns them).
	if stale, _ := filepath.Glob(filepath.Join(filepath.Dir(targetDir), defaultCodeGraphDir+"-seed-*")); len(stale) > 0 {
		for _, dir := range stale {
			_ = os.RemoveAll(dir)
		}
	}

	lockPath := filepath.Join(srcDir, "codegraph.lock")
	var tmpDir string
	for attempt := 1; ; attempt++ {
		dbs, err := filepath.Glob(filepath.Join(srcDir, dbPrefix+"*"+dbSuffix))
		if err != nil || len(dbs) == 0 {
			return res, fmt.Errorf("seed: no index databases in %s", srcDir)
		}
		if waitForCanonicalWriter(lockPath, seedLockWait) {
			return res, fmt.Errorf("seed: a canonical index writer is still running")
		}
		before, err := fingerprintSet(dbs)
		if err != nil {
			return res, fmt.Errorf("seed: %w", err)
		}
		tmpDir, err = os.MkdirTemp(filepath.Dir(targetDir), defaultCodeGraphDir+"-seed-")
		if err != nil {
			return res, fmt.Errorf("seed: %w", err)
		}
		method, n, err := copyDatabases(dbs, tmpDir)
		if err != nil {
			_ = os.RemoveAll(tmpDir)
			return res, err
		}
		// Re-list the directory: a writer may also have added a scope.
		nowDBs, _ := filepath.Glob(filepath.Join(srcDir, dbPrefix+"*"+dbSuffix))
		after, ferr := fingerprintSet(nowDBs)
		if ferr == nil && after == before && !writerHoldsLock(lockPath) {
			res.Method, res.Databases = method, n
			break
		}
		_ = os.RemoveAll(tmpDir)
		if attempt == seedAttempts {
			return res, fmt.Errorf("seed: the canonical index changed during %d copy attempts", seedAttempts)
		}
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(dataDirGitignore), 0o644); err != nil {
		return res, fmt.Errorf("seed: %w", err)
	}
	fastPath, err := stampSeededCopy(tmpDir, sourceRoot, targetRoot)
	if err != nil {
		return res, fmt.Errorf("seed: stamp copy: %w", err)
	}
	res.FastPath = fastPath

	published, err := publishSeedDir(tmpDir, targetDir)
	if err != nil {
		return res, err
	}
	cleanup = false
	res.Published = published
	if published && afterPublish != nil {
		if err := afterPublish(); err != nil {
			_ = RemoveDirectory(targetRoot)
			return res, fmt.Errorf("seed: catch up: %w", err)
		}
	}
	res.Duration = time.Since(start)
	return res, nil
}

func copyDatabases(dbs []string, dir string) (SeedMethod, int, error) {
	method := SeedClone
	for _, src := range dbs {
		m, err := snapshotDatabase(src, filepath.Join(dir, filepath.Base(src)))
		if err != nil {
			return "", 0, fmt.Errorf("seed: copy %s: %w", filepath.Base(src), err)
		}
		method = weaker(method, m)
	}
	return method, len(dbs), nil
}

// acquireWithin retries a busy file lock until it is taken or wait runs out;
// any other error fails at once.
func acquireWithin(l *graphlock.FileLock, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		err := l.Acquire()
		if err == nil || !errors.Is(err, graphlock.ErrLockUnavailable) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// fingerprintSet identifies the current generation of a set of SQLite files.
// The header change counter (offset 24) moves on every rollback-journal
// commit, so it catches a write even where mtimes are coarse.
func fingerprintSet(paths []string) (string, error) {
	var b strings.Builder
	for _, p := range paths {
		fp, err := fileFingerprint(p)
		if err != nil {
			return "", err
		}
		b.WriteString(p + " " + fp + "\n")
	}
	return b.String(), nil
}

func fileFingerprint(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	hdr := make([]byte, 28)
	n, err := io.ReadFull(f, hdr)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	return fmt.Sprintf("%d %d %x", fi.Size(), fi.ModTime().UnixNano(), hdr[:n]), nil
}

// stubDataFiles are what an interrupted init or a repair leaves in a data
// directory that holds no databases.
var stubDataFiles = []string{".gitignore", "errors.log"}

// publishSeedDir moves the finished copy into place with one directory
// rename, so a reader never sees a partial set. The caller holds the
// per-target seed lock. A data directory that exists without databases is
// emptied of its stub files and removed (rmdir, empty only) first.
// It never moves or deletes a populated directory: if an init filled the
// target meanwhile, the rename fails and that index wins.
func publishSeedDir(tmpDir, targetDir string) (bool, error) {
	if hasScopeDB(targetDir) {
		_ = os.RemoveAll(tmpDir)
		return false, nil
	}
	if _, err := os.Lstat(targetDir); err == nil {
		for _, name := range stubDataFiles {
			_ = os.Remove(filepath.Join(targetDir, name))
		}
		// rmdir removes only an empty directory, never a populated one.
		_ = os.Remove(targetDir)
	}
	if err := os.Rename(tmpDir, targetDir); err != nil {
		if hasScopeDB(targetDir) {
			_ = os.RemoveAll(tmpDir)
			return false, nil // a concurrent init won
		}
		return false, fmt.Errorf("seed: publish: %w", err)
	}
	return true, nil
}

func weaker(a, b SeedMethod) SeedMethod {
	rank := map[SeedMethod]int{SeedClone: 0, SeedCopy: 1}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// writerHoldsLock reports whether a live, non-stale writer holds lockPath
// right now (read only).
func writerHoldsLock(lockPath string) bool {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return false
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if perr != nil || pid <= 0 || !processAlive(pid) {
		return false
	}
	if fi, err := os.Stat(lockPath); err == nil && time.Since(fi.ModTime()) > 2*time.Minute {
		return false // stale by the lock protocol's own rule
	}
	return true
}

// waitForCanonicalWriter polls (stat only) until the canonical lock file is
// gone or held by a dead process. It reports whether a live writer still
// held it when the wait ran out.
func waitForCanonicalWriter(lockPath string, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for writerHoldsLock(lockPath) {
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// snapshotDatabase takes a consistent copy of one SQLite file without ever
// writing beside it.
//
// For the default rollback journal it opens the source mode=ro and holds a
// read transaction (a SHARED lock, which keeps any writer from committing)
// while it clones the file, so the clone is copy-on-write where supported.
// A rollback-mode reader creates no side files. Caveat: POSIX record locks
// are per process and inode, so opening and closing another descriptor on
// the source (the FICLONE path and the plain-copy fallback do) drops that
// SHARED lock early; the caller's before/after fingerprint of the whole set
// (which includes SQLite's change counter) is what guarantees consistency.
//
// A legacy WAL-mode file is never opened through SQLite: even a mode=ro
// reader creates -wal/-shm files beside a WAL database. It is copied only
// when its WAL is absent or empty (the main file then holds every committed
// page) and the file is unchanged across the copy; the copy's header is then
// switched to the rollback journal, which is exactly what a checkpointed
// `PRAGMA journal_mode=DELETE` leaves. A non-empty WAL fails the seed (the
// caller falls back to a full build).
func snapshotDatabase(src, dst string) (SeedMethod, error) {
	wal, err := sqliteHeaderIsWAL(src)
	if err != nil {
		return "", err
	}
	if wal {
		return snapshotWALDatabase(src, dst)
	}
	if err := waitForJournal(src+"-journal", seedLockWait); err != nil {
		return "", err
	}

	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return "", err
	}
	defer func() { _, _ = conn.ExecContext(ctx, "COMMIT") }()
	var n int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		return "", err // takes the SHARED lock
	}
	return cloneOrCopy(src, dst)
}

// waitForJournal waits (stat only) for a rollback journal beside the source
// to disappear: a writer is mid-commit, or crashed leaving a hot journal that
// only a writer may roll back.
func waitForJournal(journal string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		if _, err := os.Stat(journal); os.IsNotExist(err) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s exists (writer mid-commit or crashed)", filepath.Base(journal))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func snapshotWALDatabase(src, dst string) (SeedMethod, error) {
	before, err := walSnapshotState(src)
	if err != nil {
		return "", err
	}
	method, err := cloneOrCopy(src, dst)
	if err != nil {
		return "", err
	}
	after, err := walSnapshotState(src)
	if err != nil || after != before {
		_ = os.Remove(dst)
		if err == nil {
			err = errors.New("WAL-mode database changed during the copy")
		}
		return "", err
	}
	if err := setRollbackJournalHeader(dst); err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	return method, nil
}

// walSnapshotState fingerprints a WAL-mode file; it errors when the WAL holds
// frames not yet in the main file.
func walSnapshotState(src string) (string, error) {
	if fi, err := os.Stat(src + "-wal"); err == nil && fi.Size() > 0 {
		return "", fmt.Errorf("%s has uncheckpointed WAL frames", filepath.Base(src))
	}
	return fileFingerprint(src)
}

// setRollbackJournalHeader sets the file-format write/read versions (header
// offsets 18/19) to 1, the legacy rollback journal.
func setRollbackJournalHeader(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteAt([]byte{1, 1}, 18); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func cloneOrCopy(src, dst string) (SeedMethod, error) {
	if err := cloneFileFunc(src, dst); err == nil {
		return SeedClone, nil
	}
	_ = os.Remove(dst)
	if err := copyFile(src, dst); err != nil {
		return "", err
	}
	return SeedCopy, nil
}

// sqliteHeaderIsWAL reports whether the file's header selects WAL mode
// (write/read format versions 2 at offsets 18/19).
func sqliteHeaderIsWAL(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	hdr := make([]byte, 20)
	if _, err := io.ReadFull(f, hdr); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return hdr[18] == 2 || hdr[19] == 2, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// stampSeededCopy writes the worktree identity into every copied database
// and decides whether the base revision can be trusted for the git-diff fast
// path: only when every scope agrees on it and it was recorded clean. Otherwise the revision is cleared, so
// the first refresh hashes every file.
func stampSeededCopy(dir, sourceRoot, targetRoot string) (bool, error) {
	dbs, err := filepath.Glob(filepath.Join(dir, dbPrefix+"*"+dbSuffix))
	if err != nil {
		return false, err
	}
	heads := map[string]bool{}
	clean := true
	for _, p := range dbs {
		head, dirty, err := readSeedMetadata(p)
		if err != nil {
			return false, err
		}
		heads[head] = true
		if dirty != "0" {
			clean = false
		}
	}
	fast := clean && len(heads) == 1 && !heads[""]
	for _, p := range dbs {
		if err := writeSeedMetadata(p, sourceRoot, targetRoot, fast); err != nil {
			return false, err
		}
	}
	return fast, nil
}

func readSeedMetadata(path string) (head, dirty string, err error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = db.Close() }()
	get := func(key string) (string, error) {
		var v string
		err := db.QueryRow(`SELECT value FROM project_metadata WHERE key = ?`, key).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return v, err
	}
	if head, err = get(indexedGitHeadKey); err != nil {
		return "", "", err
	}
	dirty, err = get(indexedGitDirtyKey)
	return head, dirty, err
}

func writeSeedMetadata(path, sourceRoot, targetRoot string, keepHead bool) error {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	now := time.Now().UnixMilli()
	set := func(key, value string) error {
		_, err := db.Exec(`
			INSERT INTO project_metadata (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			key, value, now)
		return err
	}
	if err := set(seededFromKey, sourceRoot); err != nil {
		return err
	}
	if err := set(worktreeRootKey, targetRoot); err != nil {
		return err
	}
	if !keepHead {
		if err := set(indexedGitHeadKey, ""); err != nil {
			return err
		}
		if err := set(indexedGitDirtyKey, ""); err != nil {
			return err
		}
	}
	return nil
}

// SeededFrom returns the index root this index was seeded from ("" when it
// was built in place).
func (idx *Indexer) SeededFrom() string {
	for _, s := range idx.Stores() {
		if v, err := s.GetMetadata(seededFromKey); err == nil && v != "" {
			return v
		}
	}
	return ""
}

// FindCanonicalIndex finds the canonical-clone index that corresponds to
// startPath inside worktreeRoot: the nearest initialized directory on the path
// from canonicalRoot/<rel> up to canonicalRoot. It returns the source index
// root and the matching target root inside the worktree, or "" when the
// canonical clone has no index.
func FindCanonicalIndex(startPath, worktreeRoot, canonicalRoot string) (source, target string) {
	rel, err := filepath.Rel(worktreeRoot, realpathOrAbs(startPath))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		rel = "."
	}
	for {
		candidate := filepath.Join(canonicalRoot, rel)
		if IsInitialized(candidate) {
			return candidate, filepath.Join(worktreeRoot, rel)
		}
		if rel == "." {
			return "", ""
		}
		rel = filepath.Dir(rel)
	}
}

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
// The canonical clone is only ever read: its databases are opened with
// mode=ro, its lock file is only stat'ed, and nothing is created beside them.
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
	SeedClone  SeedMethod = "clone"  // copy-on-write clone (APFS clonefile, FICLONE reflink)
	SeedCopy   SeedMethod = "copy"   // plain byte copy
	SeedVacuum SeedMethod = "vacuum" // VACUUM INTO (WAL-mode or hot-journal source)
)

// SeedResult describes a completed seed.
type SeedResult struct {
	Source    string     // index root copied from
	Target    string     // index root written
	Databases int        // database files copied
	Method    SeedMethod // weakest method used across the files
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
)

// SeedFromCanonical copies the index rooted at sourceRoot into targetRoot,
// which must not be initialized yet. It never writes under sourceRoot.
func SeedFromCanonical(sourceRoot, targetRoot string) (SeedResult, error) {
	start := time.Now()
	res := SeedResult{Source: sourceRoot, Target: targetRoot}
	if IsInitialized(targetRoot) {
		return res, fmt.Errorf("seed: %s already has an index", targetRoot)
	}
	srcDir := GetCodeGraphDir(sourceRoot)
	dbs, err := filepath.Glob(filepath.Join(srcDir, dbPrefix+"*"+dbSuffix))
	if err != nil || len(dbs) == 0 {
		return res, fmt.Errorf("seed: no index databases in %s", srcDir)
	}

	// A writer mid-sync updates several scope databases in turn. Wait briefly
	// for it so the copies agree with one another; if it is still running,
	// copy anyway but drop the base revision so the first refresh rechecks
	// every file by content hash.
	writerBusy := waitForCanonicalWriter(filepath.Join(srcDir, "codegraph.lock"), seedLockWait)

	targetDir := GetCodeGraphDir(targetRoot)
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return res, fmt.Errorf("seed: %w", err)
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(targetDir), defaultCodeGraphDir+"-seed-")
	if err != nil {
		return res, fmt.Errorf("seed: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	res.Method = SeedClone
	for _, src := range dbs {
		dst := filepath.Join(tmpDir, filepath.Base(src))
		method, err := snapshotDatabase(src, dst)
		if err != nil {
			return res, fmt.Errorf("seed: copy %s: %w", filepath.Base(src), err)
		}
		res.Method = weaker(res.Method, method)
		res.Databases++
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(dataDirGitignore), 0o644); err != nil {
		return res, fmt.Errorf("seed: %w", err)
	}

	fastPath, err := stampSeededCopy(tmpDir, sourceRoot, targetRoot, writerBusy)
	if err != nil {
		return res, fmt.Errorf("seed: stamp copy: %w", err)
	}
	res.FastPath = fastPath

	if err := publishSeedDir(tmpDir, targetDir); err != nil {
		return res, err
	}
	cleanup = false
	res.Duration = time.Since(start)
	return res, nil
}

// publishSeedDir moves the finished copy into place. A concurrent seed that
// won the race leaves an initialized target; that is success, not an error.
func publishSeedDir(tmpDir, targetDir string) error {
	if _, err := os.Lstat(targetDir); os.IsNotExist(err) {
		if err := os.Rename(tmpDir, targetDir); err == nil {
			return nil
		} else if !hasScopeDB(targetDir) {
			return fmt.Errorf("seed: publish: %w", err)
		}
		_ = os.RemoveAll(tmpDir)
		return nil
	}
	// The data directory exists without databases (e.g. only a .gitignore
	// from an interrupted init): move the files in one by one.
	if hasScopeDB(targetDir) {
		_ = os.RemoveAll(tmpDir)
		return nil
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return fmt.Errorf("seed: publish: %w", err)
	}
	for _, e := range entries {
		dst := filepath.Join(targetDir, e.Name())
		if e.Name() == ".gitignore" {
			if _, err := os.Stat(dst); err == nil {
				continue
			}
		}
		if err := os.Rename(filepath.Join(tmpDir, e.Name()), dst); err != nil {
			return fmt.Errorf("seed: publish: %w", err)
		}
	}
	_ = os.RemoveAll(tmpDir)
	return nil
}

func weaker(a, b SeedMethod) SeedMethod {
	rank := map[SeedMethod]int{SeedClone: 0, SeedCopy: 1, SeedVacuum: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// waitForCanonicalWriter polls (stat only) until the canonical lock file is
// gone or held by a dead process. It reports whether a live writer still
// held it when the wait ran out.
func waitForCanonicalWriter(lockPath string, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
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
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// snapshotDatabase takes a consistent copy of one SQLite file. For the
// default rollback journal it holds a read transaction (a SHARED lock, which
// keeps any writer from committing) while it clones the file, so the clone is
// copy-on-write where supported. A WAL-mode file, or one with a journal
// beside it, goes through VACUUM INTO instead, which reads through SQLite.
// The source is opened mode=ro and never written.
func snapshotDatabase(src, dst string) (SeedMethod, error) {
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

	wal, err := sqliteHeaderIsWAL(src)
	if err != nil {
		return "", err
	}
	_, journalErr := os.Stat(src + "-journal")
	if wal || journalErr == nil {
		if _, err := conn.ExecContext(ctx, "VACUUM INTO ?", dst); err != nil {
			return "", err
		}
		return SeedVacuum, nil
	}

	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return "", err
	}
	defer func() { _, _ = conn.ExecContext(ctx, "COMMIT") }()
	var n int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		return "", err // takes the SHARED lock; fails on a hot journal
	}
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
// path: only when every scope agrees on it, it was recorded clean, and no
// writer was mid-sync during the copy. Otherwise the revision is cleared, so
// the first refresh hashes every file.
func stampSeededCopy(dir, sourceRoot, targetRoot string, writerBusy bool) (bool, error) {
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
	fast := !writerBusy && clean && len(heads) == 1 && !heads[""]
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

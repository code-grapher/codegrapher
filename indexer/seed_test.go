package indexer

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// newSeedRepo creates a canonical clone with two Go files and a linked
// worktree beside it (outside the clone, the wb layout).
func newSeedRepo(t *testing.T) (canonical, worktree string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	canonical = filepath.Join(base, "repo")
	worktree = filepath.Join(base, "wt")
	writeFile(t, filepath.Join(canonical, "a.go"), "package a\n\nfunc Foo() {}\n\nfunc Bar() { Foo() }\n")
	writeFile(t, filepath.Join(canonical, "b.go"), "package a\n\nfunc Qux() { Bar() }\n")
	mustGit(t, canonical, "init", "-q")
	mustGit(t, canonical, "add", ".")
	mustGit(t, canonical, "commit", "-q", "-m", "init")
	return canonical, worktree
}

func addWorktree(t *testing.T, canonical, worktree string) {
	t.Helper()
	mustGit(t, canonical, "worktree", "add", "-q", "-b", "wt-"+filepath.Base(worktree), worktree)
}

func initIndex(t *testing.T, root string) {
	t.Helper()
	idx, res, err := Init(root, Options{})
	if err != nil || !res.Success {
		t.Fatalf("Init(%s): %v %+v", root, err, res.Errors)
	}
	_ = idx.Close()
}

// dirState fingerprints every file under dir (name, size, mtime).
func dirState(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		lines = append(lines, fmt.Sprintf("%s %d %d", rel, fi.Size(), fi.ModTime().UnixNano()))
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// graphSignature summarizes nodes and edges in a root-independent form.
func graphSignature(t *testing.T, root string) string {
	t.Helper()
	idx, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	var lines []string
	for _, s := range idx.Stores() {
		nodes, err := s.AllNodes()
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range nodes {
			lines = append(lines, fmt.Sprintf("node %s %s %s:%d", n.Kind, n.QualifiedName, n.FilePath, n.StartLine))
		}
		edges, err := s.AllEdges()
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edges {
			lines = append(lines, fmt.Sprintf("edge %s %s %s", e.Kind, e.Source, e.Target))
		}
		files, err := s.GetAllFiles()
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			lines = append(lines, "file "+f.Path+" "+f.ContentHash)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func refresh(t *testing.T, root string) SyncResult {
	t.Helper()
	idx, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	res, err := idx.RefreshForRead(Options{})
	if err != nil {
		t.Fatalf("RefreshForRead: %v", err)
	}
	return res
}

func TestLinkedWorktree(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	addWorktree(t, canonical, worktree)
	if _, _, ok := LinkedWorktree(canonical); ok {
		t.Error("canonical clone reported as a linked worktree")
	}
	wt, canon, ok := LinkedWorktree(filepath.Join(worktree))
	if !ok || wt != realpathOrAbs(worktree) || canon != realpathOrAbs(canonical) {
		t.Errorf("LinkedWorktree = %q %q %v", wt, canon, ok)
	}
	if _, _, ok := LinkedWorktree(t.TempDir()); ok {
		t.Error("non-git dir reported as a linked worktree")
	}
}

func TestSeedFromCanonicalMatchesFreshIndexAndNeverWritesCanonical(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	initIndex(t, canonical)
	// The canonical index goes stale: a commit lands after it was built.
	writeFile(t, filepath.Join(canonical, "c.go"), "package a\n\nfunc Committed() { Foo() }\n")
	mustGit(t, canonical, "add", ".")
	mustGit(t, canonical, "commit", "-q", "-m", "second")
	addWorktree(t, canonical, worktree)
	// Worktree-local edits: a new file and a modified one.
	writeFile(t, filepath.Join(worktree, "d.go"), "package a\n\nfunc Local() { Qux() }\n")
	writeFile(t, filepath.Join(worktree, "a.go"), "package a\n\nfunc Foo() {}\n\nfunc Bar() {}\n\nfunc Extra() { Foo() }\n")

	before := dirState(t, canonical)
	res, err := SeedFromCanonical(realpathOrAbs(canonical), realpathOrAbs(worktree))
	if err != nil {
		t.Fatalf("SeedFromCanonical: %v", err)
	}
	if after := dirState(t, canonical); after != before {
		t.Fatalf("canonical clone changed by seeding:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !IsInitialized(worktree) || res.Databases == 0 {
		t.Fatalf("worktree not seeded: %+v", res)
	}
	if !res.FastPath {
		t.Error("clean, stamped canonical index should keep the base revision")
	}
	if _, err := os.Stat(filepath.Join(GetCodeGraphDir(worktree), ".gitignore")); err != nil {
		t.Errorf("seeded data dir lacks .gitignore: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(worktree, defaultCodeGraphDir+"-seed-*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary seed dirs left behind: %v", leftovers)
	}

	idx, err := Open(worktree, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := idx.SeededFrom(); got != realpathOrAbs(canonical) {
		t.Errorf("SeededFrom = %q", got)
	}
	if v, _ := idx.Stores()[0].GetMetadata(worktreeRootKey); v != realpathOrAbs(worktree) {
		t.Errorf("worktree identity stamp = %q", v)
	}
	_ = idx.Close()

	r := refresh(t, worktree)
	if r.FilesAdded+r.FilesModified == 0 {
		t.Errorf("incremental catch-up found nothing: %+v", r)
	}

	fresh := filepath.Join(t.TempDir(), "fresh")
	mustGit(t, canonical, "worktree", "add", "-q", "-b", "fresh", fresh)
	writeFile(t, filepath.Join(fresh, "d.go"), "package a\n\nfunc Local() { Qux() }\n")
	writeFile(t, filepath.Join(fresh, "a.go"), "package a\n\nfunc Foo() {}\n\nfunc Bar() {}\n\nfunc Extra() { Foo() }\n")
	initIndex(t, fresh)

	if got, want := graphSignature(t, worktree), graphSignature(t, fresh); got != want {
		t.Fatalf("seeded graph differs from a fresh index:\nseeded:\n%s\nfresh:\n%s", got, want)
	}
}

func TestSeedFromDirtyCanonicalDropsBaseRevision(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	writeFile(t, filepath.Join(canonical, "untracked.go"), "package a\n\nfunc OnlyInCanonical() {}\n")
	initIndex(t, canonical)
	addWorktree(t, canonical, worktree)

	res, err := SeedFromCanonical(canonical, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if res.FastPath {
		t.Fatal("dirty canonical index must not keep the base revision")
	}
	refresh(t, worktree)
	idx, err := Open(worktree, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	for _, s := range idx.Stores() {
		if n, _ := s.GetNodesByName("OnlyInCanonical"); len(n) != 0 {
			t.Fatal("symbol from the canonical clone's dirty tree survived the catch-up")
		}
	}
}

func TestSeedFallsBackToPlainCopyWhenCloneFails(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	initIndex(t, canonical)
	addWorktree(t, canonical, worktree)
	old := cloneFileFunc
	cloneFileFunc = func(src, dst string) error { return errors.New("no reflink here") }
	t.Cleanup(func() { cloneFileFunc = old })

	res, err := SeedFromCanonical(canonical, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if res.Method != SeedCopy {
		t.Fatalf("Method = %q, want copy", res.Method)
	}
	refresh(t, worktree)
}

func TestSeedRefusesInitializedTargetAndEmptySource(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	addWorktree(t, canonical, worktree)
	if _, err := SeedFromCanonical(canonical, worktree); err == nil {
		t.Fatal("seeding from an unindexed canonical clone succeeded")
	}
	if IsInitialized(worktree) {
		t.Fatal("failed seed left an index")
	}
	initIndex(t, canonical)
	initIndex(t, worktree)
	if _, err := SeedFromCanonical(canonical, worktree); err == nil {
		t.Fatal("seeding over an existing index succeeded")
	}
}

func TestSeedRefusesWhileCanonicalWriterRuns(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	initIndex(t, canonical)
	addWorktree(t, canonical, worktree)
	// A live writer (this process) holds the canonical lock.
	lockPath := filepath.Join(GetCodeGraphDir(canonical), "codegraph.lock")
	writeFile(t, lockPath, fmt.Sprint(os.Getpid()))
	oldWait := seedLockWait
	seedLockWait = 100 * time.Millisecond
	t.Cleanup(func() { seedLockWait = oldWait; _ = os.Remove(lockPath) })

	if _, err := SeedFromCanonical(canonical, worktree); err == nil {
		t.Fatal("seeded while a canonical writer held the lock")
	}
	if IsInitialized(worktree) {
		t.Fatal("refused seed left an index")
	}
	// A dead holder's lock is stale and does not block.
	writeFile(t, lockPath, "999999")
	if _, err := SeedFromCanonical(canonical, worktree); err != nil {
		t.Fatalf("stale lock blocked seeding: %v", err)
	}
	if _, err := os.Stat(filepath.Join(GetCodeGraphDir(worktree), "codegraph.lock")); err == nil {
		t.Fatal("canonical lock file was copied")
	}
}

// bumpMetadata commits a write to one canonical scope database, as a
// concurrent canonical sync would.
func bumpMetadata(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO project_metadata (key, value, updated_at) VALUES ('probe', ?, 0)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, time.Now().String()); err != nil {
		t.Fatal(err)
	}
}

func TestSeedRetriesWhenCanonicalChangesDuringCopy(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	initIndex(t, canonical)
	addWorktree(t, canonical, worktree)
	dbs, _ := filepath.Glob(filepath.Join(GetCodeGraphDir(canonical), dbPrefix+"*"+dbSuffix))
	sort.Strings(dbs)
	calls := 0
	old := cloneFileFunc
	cloneFileFunc = func(src, dst string) error {
		calls++
		if calls == 1 {
			// A writer commits to another scope while the first is copied.
			bumpMetadata(t, dbs[len(dbs)-1])
		}
		return old(src, dst)
	}
	t.Cleanup(func() { cloneFileFunc = old })

	if _, err := SeedFromCanonical(canonical, worktree); err != nil {
		t.Fatalf("SeedFromCanonical: %v", err)
	}
	if calls <= len(dbs) {
		t.Fatalf("copy was not retried after a concurrent write (clone calls %d, dbs %d)", calls, len(dbs))
	}

	// A source that changes on every attempt fails the seed.
	worktree2 := filepath.Join(filepath.Dir(worktree), "wt2")
	addWorktree(t, canonical, worktree2)
	cloneFileFunc = func(src, dst string) error {
		// Commit to a database other than the one under its SHARED lock.
		other := dbs[0]
		if src == other {
			other = dbs[len(dbs)-1]
		}
		bumpMetadata(t, other)
		return old(src, dst)
	}
	if _, err := SeedFromCanonical(canonical, worktree2); err == nil {
		t.Fatal("seeded from a canonical index that never stopped changing")
	}
	if IsInitialized(worktree2) {
		t.Fatal("failed seed left an index")
	}
}

func TestSeedPublishesOverDataDirWithoutDatabases(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	initIndex(t, canonical)
	addWorktree(t, canonical, worktree)
	// An interrupted init left only the data directory's .gitignore.
	writeFile(t, filepath.Join(GetCodeGraphDir(worktree), ".gitignore"), dataDirGitignore)
	if _, err := SeedFromCanonical(canonical, worktree); err != nil {
		t.Fatal(err)
	}
	if !IsInitialized(worktree) {
		t.Fatal("not seeded")
	}
	left, _ := filepath.Glob(filepath.Join(worktree, defaultCodeGraphDir+"-*"))
	if len(left) != 0 {
		t.Fatalf("staging entries left behind: %v", left)
	}
}

func TestConcurrentSeedsProduceOneIndex(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	initIndex(t, canonical)
	addWorktree(t, canonical, worktree)
	errs := make(chan error, 4)
	for range 4 {
		go func() {
			_, err := SeedFromCanonical(canonical, worktree)
			errs <- err
		}()
	}
	for range 4 {
		if err := <-errs; err != nil && !strings.Contains(err.Error(), "already has an index") {
			t.Errorf("concurrent seed: %v", err)
		}
	}
	if !IsInitialized(worktree) {
		t.Fatal("not seeded")
	}
	left, _ := filepath.Glob(filepath.Join(worktree, defaultCodeGraphDir+"-*"))
	if len(left) != 0 {
		t.Fatalf("staging entries left behind: %v", left)
	}
	refresh(t, worktree)
}

func TestFindCanonicalIndexMapsSubprojects(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	addWorktree(t, canonical, worktree)
	if src, _ := FindCanonicalIndex(worktree, worktree, canonical); src != "" {
		t.Fatalf("found %q in an unindexed clone", src)
	}
	sub := filepath.Join(canonical, "svc")
	writeFile(t, filepath.Join(sub, "s.go"), "package svc\n")
	initIndex(t, sub)
	src, dst := FindCanonicalIndex(filepath.Join(worktree, "svc", "pkg"), worktree, canonical)
	if src != sub || dst != filepath.Join(worktree, "svc") {
		t.Fatalf("FindCanonicalIndex = %q %q", src, dst)
	}
}

func TestScanNeverIndexesDataDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n")
	mustGit(t, root, "init", "-q")
	// No info/exclude entry: the data dir's own .gitignore un-ignores itself,
	// so git lists .codegraph/.gitignore as untracked.
	writeFile(t, filepath.Join(root, ".codegraph", ".gitignore"), dataDirGitignore)
	for _, f := range ScanDirectory(root) {
		if strings.HasPrefix(f, ".codegraph/") {
			t.Fatalf("ScanDirectory admitted %s", f)
		}
	}
	changes, ok := gitChangedFiles(root)
	if !ok {
		t.Fatal("git status failed")
	}
	for _, f := range changes.added {
		if strings.HasPrefix(f, ".codegraph/") {
			t.Fatalf("git candidates admitted %s", f)
		}
	}
	if !isIgnoredSyncPath(root, ".codegraph/.gitignore") {
		t.Fatal("sync admitted .codegraph/.gitignore")
	}
	if !NewPathFilter(root).IsIgnored(".codegraph/.gitignore") {
		t.Fatal("watcher admitted .codegraph/.gitignore")
	}
}

// setWALMode converts every scope database under root to the legacy WAL
// journal, as an index from an older release would be.
func setWALMode(t *testing.T, root string) {
	t.Helper()
	dbs, _ := filepath.Glob(filepath.Join(GetCodeGraphDir(root), dbPrefix+"*"+dbSuffix))
	for _, p := range dbs {
		db, err := sql.Open("sqlite", "file:"+p)
		if err != nil {
			t.Fatal(err)
		}
		var mode string
		if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil || mode != "wal" {
			t.Fatalf("journal_mode=WAL: %v %q", err, mode)
		}
		_ = db.Close()
		if wal, _ := sqliteHeaderIsWAL(p); !wal {
			t.Fatalf("%s not WAL after conversion", p)
		}
		for _, side := range []string{"-wal", "-shm"} {
			_ = os.Remove(p + side)
		}
	}
}

func TestSeedFromLegacyWALCanonicalCreatesNoSideFiles(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	initIndex(t, canonical)
	setWALMode(t, canonical)
	addWorktree(t, canonical, worktree)
	before := dirState(t, canonical)

	if _, err := SeedFromCanonical(canonical, worktree); err != nil {
		t.Fatalf("SeedFromCanonical: %v", err)
	}
	if after := dirState(t, canonical); after != before {
		t.Fatalf("seeding a WAL-mode canonical index wrote beside it:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	dbs, _ := filepath.Glob(filepath.Join(GetCodeGraphDir(worktree), dbPrefix+"*"+dbSuffix))
	for _, p := range dbs {
		if wal, _ := sqliteHeaderIsWAL(p); wal {
			t.Errorf("seeded copy %s still in WAL mode", filepath.Base(p))
		}
	}
	idx, err := OpenReadOnly(worktree, Options{})
	if err != nil {
		t.Fatalf("seeded copy needs an upgrade: %v", err)
	}
	_ = idx.Close()
	refresh(t, worktree)
}

func TestSeedRefusesWALCanonicalWithPendingFrames(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	initIndex(t, canonical)
	setWALMode(t, canonical)
	addWorktree(t, canonical, worktree)
	dbs, _ := filepath.Glob(filepath.Join(GetCodeGraphDir(canonical), dbPrefix+"*"+dbSuffix))
	writeFile(t, dbs[0]+"-wal", "frames")
	if _, err := SeedFromCanonical(canonical, worktree); err == nil {
		t.Fatal("seeded from a WAL database with uncheckpointed frames")
	}
	if IsInitialized(worktree) {
		t.Fatal("failed seed left an index")
	}
}

func TestSeedWaitsOutRollbackJournal(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	initIndex(t, canonical)
	addWorktree(t, canonical, worktree)
	dbs, _ := filepath.Glob(filepath.Join(GetCodeGraphDir(canonical), dbPrefix+"*"+dbSuffix))
	writeFile(t, dbs[0]+"-journal", "")
	oldWait := seedLockWait
	seedLockWait = 50 * time.Millisecond
	t.Cleanup(func() { seedLockWait = oldWait })
	if _, err := SeedFromCanonical(canonical, worktree); err == nil {
		t.Fatal("seeded while a rollback journal sat beside the source")
	}
}

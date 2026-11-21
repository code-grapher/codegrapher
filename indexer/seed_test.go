package indexer

import (
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

func TestSeedWaitsForCanonicalWriterThenDropsBaseRevision(t *testing.T) {
	canonical, worktree := newSeedRepo(t)
	initIndex(t, canonical)
	addWorktree(t, canonical, worktree)
	// A live writer (this process) holds the canonical lock.
	lockPath := filepath.Join(GetCodeGraphDir(canonical), "codegraph.lock")
	writeFile(t, lockPath, fmt.Sprint(os.Getpid()))
	oldWait := seedLockWait
	seedLockWait = 100 * time.Millisecond
	t.Cleanup(func() { seedLockWait = oldWait; _ = os.Remove(lockPath) })

	res, err := SeedFromCanonical(canonical, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if res.FastPath {
		t.Fatal("seed taken while a writer was busy must recheck every file")
	}
	if _, err := os.Stat(filepath.Join(GetCodeGraphDir(worktree), "codegraph.lock")); err == nil {
		t.Fatal("canonical lock file was copied")
	}
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

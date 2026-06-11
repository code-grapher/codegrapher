package indexer

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// newSyncProject creates a temp project with src/index.ts, runs Init, and
// returns the open Indexer — the setup used throughout upstream sync.test.ts.
func newSyncProject(t *testing.T) (string, *Indexer) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "index.ts"),
		"export function hello() { return 'world'; }")
	idx, res, err := Init(dir, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	if !res.Success {
		t.Fatalf("Init result: %+v", res)
	}
	return dir, idx
}

func hasNodeNamed(t *testing.T, idx *Indexer, name string) bool {
	t.Helper()
	nodes, err := idx.Store().GetNodesByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return len(nodes) > 0
}

// touchPast backdates a file's mtime so the (size, mtime) stat pre-filter
// can't mask a content change written within the same millisecond.
func touchPast(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// --- getChangedFiles ---------------------------------------------------------

func TestGetChangedFilesDetectsAdded(t *testing.T) {
	dir, idx := newSyncProject(t)
	writeFile(t, filepath.Join(dir, "src", "new.ts"),
		"export function newFunc() { return 42; }")

	changes := idx.GetChangedFiles()
	if !slices.Contains(changes.Added, "src/new.ts") {
		t.Errorf("Added = %v, want to contain src/new.ts", changes.Added)
	}
	if len(changes.Modified) != 0 || len(changes.Removed) != 0 {
		t.Errorf("unexpected modified/removed: %+v", changes)
	}
}

func TestGetChangedFilesDetectsModified(t *testing.T) {
	dir, idx := newSyncProject(t)
	writeFile(t, filepath.Join(dir, "src", "index.ts"),
		"export function hello() { return 'modified'; }")

	changes := idx.GetChangedFiles()
	if len(changes.Added) != 0 {
		t.Errorf("Added = %v, want empty", changes.Added)
	}
	if !slices.Contains(changes.Modified, "src/index.ts") {
		t.Errorf("Modified = %v, want to contain src/index.ts", changes.Modified)
	}
	if len(changes.Removed) != 0 {
		t.Errorf("Removed = %v, want empty", changes.Removed)
	}
}

func TestGetChangedFilesDetectsRemoved(t *testing.T) {
	dir, idx := newSyncProject(t)
	if err := os.Remove(filepath.Join(dir, "src", "index.ts")); err != nil {
		t.Fatal(err)
	}

	changes := idx.GetChangedFiles()
	if len(changes.Added) != 0 || len(changes.Modified) != 0 {
		t.Errorf("unexpected added/modified: %+v", changes)
	}
	if !slices.Contains(changes.Removed, "src/index.ts") {
		t.Errorf("Removed = %v, want to contain src/index.ts", changes.Removed)
	}
}

// --- sync ---------------------------------------------------------------------

func TestSyncReindexesAddedFiles(t *testing.T) {
	dir, idx := newSyncProject(t)
	writeFile(t, filepath.Join(dir, "src", "new.ts"),
		"export function newFunc() { return 42; }")

	res := idx.Sync(Options{})
	if res.FilesAdded != 1 || res.FilesModified != 0 || res.FilesRemoved != 0 {
		t.Fatalf("SyncResult = %+v, want 1 added", res)
	}
	if !hasNodeNamed(t, idx, "newFunc") {
		t.Error("newFunc not in graph after sync")
	}
}

func TestSyncReindexesModifiedFiles(t *testing.T) {
	dir, idx := newSyncProject(t)
	target := filepath.Join(dir, "src", "index.ts")
	touchPast(t, target)
	writeFile(t, target, "export function goodbye() { return 'farewell'; }")

	res := idx.Sync(Options{})
	if res.FilesModified != 1 {
		t.Fatalf("FilesModified = %d, want 1", res.FilesModified)
	}
	if !hasNodeNamed(t, idx, "goodbye") {
		t.Error("goodbye not in graph after sync")
	}
	if hasNodeNamed(t, idx, "hello") {
		t.Error("hello still in graph after replacing the file")
	}
}

func TestSyncRemovesNodesFromDeletedFiles(t *testing.T) {
	dir, idx := newSyncProject(t)
	if err := os.Remove(filepath.Join(dir, "src", "index.ts")); err != nil {
		t.Fatal(err)
	}

	res := idx.Sync(Options{})
	if res.FilesRemoved != 1 {
		t.Fatalf("FilesRemoved = %d, want 1", res.FilesRemoved)
	}
	if hasNodeNamed(t, idx, "hello") {
		t.Error("hello still in graph after file deletion")
	}
}

func TestSyncNoChanges(t *testing.T) {
	_, idx := newSyncProject(t)

	res := idx.Sync(Options{})
	if res.FilesAdded != 0 || res.FilesModified != 0 || res.FilesRemoved != 0 {
		t.Errorf("SyncResult = %+v, want no changes", res)
	}
	if res.FilesChecked == 0 {
		t.Error("FilesChecked = 0, want > 0")
	}
}

func TestSyncLockConflictReturnsZeroResult(t *testing.T) {
	dir, idx := newSyncProject(t)
	other, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.lock.Acquire(); err != nil {
		t.Fatal(err)
	}

	res := idx.Sync(Options{})
	if res.FilesChecked != 0 || res.DurationMs != 0 {
		t.Errorf("SyncResult = %+v, want zero-value (lock signal)", res)
	}
}

// --- git-based sync ------------------------------------------------------------

func newGitSyncProject(t *testing.T) (string, *Indexer) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init")
	writeFile(t, filepath.Join(dir, "src", "index.ts"),
		"export function hello() { return 'world'; }")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "initial")

	idx, res, err := Init(dir, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	if !res.Success {
		t.Fatalf("Init result: %+v", res)
	}
	return dir, idx
}

func TestGitSyncDetectsModified(t *testing.T) {
	dir, idx := newGitSyncProject(t)
	target := filepath.Join(dir, "src", "index.ts")
	touchPast(t, target)
	writeFile(t, target, "export function hello() { return 'modified'; }")

	res := idx.Sync(Options{})
	if res.FilesModified != 1 {
		t.Fatalf("FilesModified = %d, want 1", res.FilesModified)
	}
	if !slices.Contains(res.ChangedFilePaths, "src/index.ts") {
		t.Errorf("ChangedFilePaths = %v, want src/index.ts", res.ChangedFilePaths)
	}
}

func TestGitSyncDetectsUntracked(t *testing.T) {
	dir, idx := newGitSyncProject(t)
	writeFile(t, filepath.Join(dir, "src", "new.ts"),
		"export function newFunc() { return 42; }")

	res := idx.Sync(Options{})
	if res.FilesAdded != 1 {
		t.Fatalf("FilesAdded = %d, want 1", res.FilesAdded)
	}
	if !slices.Contains(res.ChangedFilePaths, "src/new.ts") {
		t.Errorf("ChangedFilePaths = %v, want src/new.ts", res.ChangedFilePaths)
	}
	if !hasNodeNamed(t, idx, "newFunc") {
		t.Error("newFunc not indexed")
	}
}

// Upstream issue #206: untracked files stay `??` in git status even after
// codegraph indexes them — change detection must hash-compare them against
// the DB instead of reporting them as pending forever.
func TestGitSyncUntrackedIdempotent(t *testing.T) {
	dir, idx := newGitSyncProject(t)
	writeFile(t, filepath.Join(dir, "src", "new.ts"),
		"export function newFunc() { return 42; }")

	first := idx.Sync(Options{})
	if first.FilesAdded != 1 {
		t.Fatalf("first sync FilesAdded = %d, want 1", first.FilesAdded)
	}
	if !hasNodeNamed(t, idx, "newFunc") {
		t.Fatal("newFunc not indexed")
	}

	changes := idx.GetChangedFiles()
	if slices.Contains(changes.Added, "src/new.ts") || slices.Contains(changes.Modified, "src/new.ts") {
		t.Errorf("indexed untracked file still reported as pending: %+v", changes)
	}

	second := idx.Sync(Options{})
	if second.FilesAdded != 0 || second.FilesModified != 0 {
		t.Errorf("second sync = %+v, want no-op", second)
	}
}

// --- SyncFiles -----------------------------------------------------------------

func TestSyncFilesBoundedSet(t *testing.T) {
	dir, idx := newSyncProject(t)
	target := filepath.Join(dir, "src", "index.ts")
	touchPast(t, target)
	writeFile(t, target, "export function changed() { return 1; }")
	writeFile(t, filepath.Join(dir, "src", "added.ts"), "export function added() {}")
	writeFile(t, filepath.Join(dir, "src", "untouched.ts"), "export function untouched() {}")

	// Only pass two of the three changes — SyncFiles is scoped to its input.
	res := idx.SyncFiles([]string{"src/index.ts", "src/added.ts"}, Options{})
	if res.FilesModified != 1 || res.FilesAdded != 1 {
		t.Fatalf("SyncFiles result = %+v, want 1 modified + 1 added", res)
	}
	if !hasNodeNamed(t, idx, "changed") || !hasNodeNamed(t, idx, "added") {
		t.Error("changed/added not indexed")
	}
	if hasNodeNamed(t, idx, "untouched") {
		t.Error("untouched indexed despite not being passed to SyncFiles")
	}

	// Deleted file passed to SyncFiles is dropped from the index.
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	res = idx.SyncFiles([]string{"src/index.ts"}, Options{})
	if res.FilesRemoved != 1 {
		t.Fatalf("FilesRemoved = %d, want 1", res.FilesRemoved)
	}
	if hasNodeNamed(t, idx, "changed") {
		t.Error("nodes of deleted file still present")
	}

	// Unchanged file is a no-op.
	res = idx.SyncFiles([]string{"src/added.ts"}, Options{})
	if res.FilesAdded != 0 && res.FilesModified != 0 {
		t.Errorf("unchanged SyncFiles = %+v, want no-op", res)
	}
}

func TestSyncResolvesCrossFileEdges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.go"), "package main\n\nfunc Helper() {}\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() { Helper() }\n")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	mainNodes, err := idx.Store().GetNodesByName("main")
	if err != nil || len(mainNodes) != 1 {
		t.Fatalf("main nodes: %v %d", err, len(mainNodes))
	}
	callEdges := func() int {
		edges, err := idx.Store().GetOutgoingEdges(mainNodes[0].ID, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range edges {
			if string(e.Kind) == "calls" {
				n++
			}
		}
		return n
	}
	if callEdges() != 1 {
		t.Fatalf("calls edges after init = %d, want 1", callEdges())
	}

	// Modify main.go — its outgoing call edge must be re-resolved by Sync.
	target := filepath.Join(dir, "main.go")
	touchPast(t, target)
	writeFile(t, target, "package main\n\nfunc main() { Helper() }\n\nfunc extra() {}\n")
	res := idx.Sync(Options{})
	if res.FilesModified != 1 {
		t.Fatalf("FilesModified = %d, want 1", res.FilesModified)
	}
	if !hasNodeNamed(t, idx, "extra") {
		t.Error("extra not indexed")
	}
	if callEdges() != 1 {
		t.Errorf("calls edges after sync = %d, want 1 (re-resolved)", callEdges())
	}
	if n, _ := idx.Store().GetUnresolvedReferencesCount(); n != 0 {
		t.Errorf("unresolved refs after sync = %d, want 0", n)
	}
}

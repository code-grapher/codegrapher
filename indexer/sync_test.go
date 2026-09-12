package indexer

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/scope"
	"github.com/specscore/codegrapher/trace"
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
	t.Cleanup(func() { _ = idx.Close() })
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

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/specscore-source-traceability#ac:sync-refreshes-feature-and-source-links
func TestSyncRefreshesCanonicalSpecScoreTrace(t *testing.T) {
	dir := t.TempDir()
	featurePath := filepath.Join(dir, "spec", "features", "checkout", "README.md")
	writeFile(t, featurePath, `---
format: https://specscore.md/feature-specification
status: Implementing
---

# Feature: Checkout

**Status:** Implementing

## Behavior

#### REQ: initial

Initial behavior.

## Acceptance Criteria

Not defined yet.
`)
	sourcePath := filepath.Join(dir, "checkout.go")
	writeFile(t, sourcePath, "package checkout\n\nfunc Calculate() {}\n")
	idx, result, err := Init(dir, Options{})
	if err != nil || !result.Success {
		t.Fatalf("Init: %v %+v", err, result)
	}
	t.Cleanup(func() { _ = idx.Close() })

	touchPast(t, sourcePath)
	writeFile(t, sourcePath, "package checkout\n\n// specscore:implements feature/checkout#req:totals\nfunc Calculate() {}\n")
	writeFile(t, featurePath, `---
format: https://specscore.md/feature-specification
status: Implementing
---

# Feature: Checkout

**Status:** Implementing

## Behavior

#### REQ: totals

Totals are calculated.

## Acceptance Criteria

Not defined yet.
`)
	idx.Sync(Options{})
	projection, err := idx.reg.Store(scope.Scope{Language: "trace", Version: traceScopeVersion})
	if err != nil {
		t.Fatal(err)
	}
	got, err := trace.Query("feature/checkout#req:totals", projection, idx.Stores(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Target.Kind != string(model.KindRequirement) || len(got.Implements) != 1 {
		t.Fatalf("trace after sync = target %+v, implements %d", got.Target, len(got.Implements))
	}
}

func TestSyncLockConflictReturnsZeroResult(t *testing.T) {
	dir, idx := newSyncProject(t)
	other, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
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
	t.Cleanup(func() { _ = idx.Close() })
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

func TestGetChangedFilesDetectsCleanCommittedRenameAgainstIndex(t *testing.T) {
	dir, idx := newGitSyncProject(t)
	old := filepath.Join(dir, "src", "index.ts")
	new := filepath.Join(dir, "src", "renamed.ts")
	if err := os.Rename(old, new); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "rename")
	changes := idx.GetChangedFiles()
	if !slices.Contains(changes.Removed, "src/index.ts") || !slices.Contains(changes.Added, "src/renamed.ts") {
		t.Fatalf("clean committed rename changes = %+v", changes)
	}
}

func TestGetChangedFilesFallsBackForCleanEmbeddedRepoRename(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	mustGit(t, root, "init")
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, inner, "init")
	writeFile(t, filepath.Join(inner, "old.go"), "package inner\nfunc Old() {}\n")
	mustGit(t, inner, "add", "-A")
	mustGit(t, inner, "commit", "-m", "initial")
	idx, result, err := Init(root, Options{})
	if err != nil || !result.Success {
		t.Fatalf("Init: %+v %v", result, err)
	}
	defer func() { _ = idx.Close() }()
	if err := os.Rename(filepath.Join(inner, "old.go"), filepath.Join(inner, "new.go")); err != nil {
		t.Fatal(err)
	}
	mustGit(t, inner, "add", "-A")
	mustGit(t, inner, "commit", "-m", "rename")
	changes := idx.GetChangedFiles()
	if !slices.Contains(changes.Removed, "inner/old.go") || !slices.Contains(changes.Added, "inner/new.go") {
		t.Fatalf("embedded rename changes = %+v", changes)
	}
}

func TestGetChangedFilesHashesSameMtimeNestedEmbeddedRepoEdit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	mustGit(t, root, "init")
	inner := filepath.Join(root, "modules", "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, inner, "init")
	path := filepath.Join(inner, "same.go")
	writeFile(t, path, "package inner\nfunc Old() {}\n")
	mustGit(t, inner, "add", "-A")
	mustGit(t, inner, "commit", "-m", "initial")
	idx, result, err := Init(root, Options{})
	if err != nil || !result.Success {
		t.Fatalf("Init: %+v %v", result, err)
	}
	defer func() { _ = idx.Close() }()
	rec, err := idx.fileRecord("modules/inner/same.go")
	if err != nil || rec == nil {
		t.Fatalf("record: %+v %v", rec, err)
	}
	writeFile(t, path, "package inner\nfunc New() {}\n")
	tm := time.UnixMilli(rec.ModifiedAt)
	if err := os.Chtimes(path, tm, tm); err != nil {
		t.Fatal(err)
	}
	mustGit(t, inner, "add", "-A")
	mustGit(t, inner, "commit", "-m", "edit")
	changes := idx.GetChangedFiles()
	if !slices.Contains(changes.Modified, "modules/inner/same.go") {
		t.Fatalf("nested same-mtime changes = %+v", changes)
	}
}

func TestSyncFilesRemovesDeletedIndexedUnknownFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	writeFile(t, path, "one")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	res := idx.SyncFiles([]string{"notes.txt"}, Options{})
	if res.FilesRemoved != 1 {
		t.Fatalf("deleted indexed unknown = %+v, want removed", res)
	}
}

func TestSyncFilesMovesContentDetectedSpecScoreBetweenScopes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spec", "features", "example", "README.md")
	writeFile(t, path, "plain notes\n")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	writeFile(t, path, "---\nformat: https://specscore.md/feature-specification\n---\n\n# Feature: Example\n")
	if res := idx.SyncFiles([]string{"spec/features/example/README.md"}, Options{}); len(res.Errors) != 0 {
		t.Fatalf("to specscore: %+v", res.Errors)
	}
	for _, s := range idx.Stores() {
		rec, err := s.GetFileByPath("spec/features/example/README.md")
		if err != nil {
			t.Fatal(err)
		}
		if rec != nil && rec.Language != model.LangSpecScore {
			t.Fatalf("old scope retained %q", rec.Language)
		}
	}
	writeFile(t, path, "plain notes again\n")
	if res := idx.SyncFiles([]string{"spec/features/example/README.md"}, Options{}); len(res.Errors) != 0 {
		t.Fatalf("to unknown: %+v", res.Errors)
	}
	count := 0
	for _, s := range idx.Stores() {
		if rec, _ := s.GetFileByPath("spec/features/example/README.md"); rec != nil {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("scope transition left %d file records, want one", count)
	}
}

func TestSyncFilesRebuildsWhenPackageManifestCanChangeScopes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"devDependencies":{"typescript":"5.0.0"}}`)
	writeFile(t, filepath.Join(dir, "src", "a.ts"), "export function A() {}")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	writeFile(t, filepath.Join(dir, "package.json"), `{"devDependencies":{"typescript":"4.0.0"}}`)
	res := idx.SyncFiles([]string{"package.json"}, Options{})
	if !res.FullReindex || len(res.Errors) != 0 {
		t.Fatalf("manifest sync = %+v, want successful rebuild", res)
	}
}

func TestSyncRebuildsWhenPackageManifestCanChangeScopes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"devDependencies":{"typescript":"5.0.0"}}`)
	writeFile(t, filepath.Join(dir, "src", "a.ts"), "export function A() {}")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	writeFile(t, filepath.Join(dir, "package.json"), `{"devDependencies":{"typescript":"4.0.0"}}`)
	res := idx.Sync(Options{})
	if !res.FullReindex || len(res.Errors) != 0 {
		t.Fatalf("manifest full Sync = %+v, want successful rebuild", res)
	}
}

func TestGetChangedFilesDetectsDeletedIndexedUntrackedFile(t *testing.T) {
	dir, idx := newGitSyncProject(t)
	path := filepath.Join(dir, "notes.txt")
	writeFile(t, path, "one")
	if res := idx.SyncFiles([]string{"notes.txt"}, Options{}); len(res.Errors) != 0 || res.FilesAdded != 1 {
		t.Fatalf("index untracked file = %+v", res)
	}
	if err := idx.MarkCurrentGitHead(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	changes := idx.GetChangedFiles()
	if !slices.Contains(changes.Removed, "notes.txt") {
		t.Fatalf("deleted indexed untracked changes = %+v", changes)
	}
}

func TestGetChangedFilesDetectsSpecScoreDirtyAndUntrackedFiles(t *testing.T) {
	dir, idx := newGitSyncProject(t)
	tracked := filepath.Join(dir, "spec", "features", "checkout", "README.md")
	writeFile(t, tracked, `---
format: https://specscore.md/feature-specification
status: Draft
---

# Feature: Checkout
`)
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "add spec")

	// This commit happened after indexing, so it must be discovered through
	// the persisted git head rather than a repository scan.
	changes := idx.GetChangedFiles()
	if !slices.Contains(changes.Added, "spec/features/checkout/README.md") {
		t.Fatalf("committed SpecScore file = %+v, want added README.md", changes)
	}
	if res := idx.SyncFiles(changes.Added, Options{}); len(res.Errors) != 0 {
		t.Fatalf("index committed SpecScore file: %+v", res.Errors)
	}
	if err := idx.MarkCurrentGitHead(); err != nil {
		t.Fatalf("mark refreshed git head: %v", err)
	}

	writeFile(t, tracked, `---
format: https://specscore.md/feature-specification
status: Implementing
---

# Feature: Checkout
`)
	untracked := filepath.Join(dir, "spec", "features", "returns", "README.md")
	writeFile(t, untracked, `---
format: https://specscore.md/feature-specification
status: Draft
---

# Feature: Returns
`)
	changes = idx.GetChangedFiles()
	if !slices.Contains(changes.Modified, "spec/features/checkout/README.md") || !slices.Contains(changes.Added, "spec/features/returns/README.md") {
		t.Fatalf("dirty SpecScore changes = %+v", changes)
	}
}

func TestGitChangedFilesPreservesSpaceInPath(t *testing.T) {
	dir, _ := newGitSyncProject(t)
	path := filepath.Join(dir, "src", "a quoted name.ts")
	writeFile(t, path, "export function spaced() {}")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "add spaced path")
	writeFile(t, path, "export function changed() {}")

	changes, ok := gitChangedFiles(dir)
	if !ok || !slices.Contains(changes.modified, "src/a quoted name.ts") {
		t.Fatalf("git changes = %+v, ok=%v; want literal spaced path", changes, ok)
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
	defer func() { _ = idx.Close() }()

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

// A changed definition receives a new node ID when its source range changes.
// Reindexing it must preserve edges from unchanged callers by re-resolving
// their saved unresolved references after the old target is deleted.
func TestSyncChangedCalleePreservesIncomingCallerEdges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.go"), "package main\n\nfunc Helper() {}\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() { Helper() }\n")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()

	caller, err := idx.Store().GetNodesByName("main")
	if err != nil || len(caller) != 1 {
		t.Fatalf("caller: %v %d", err, len(caller))
	}

	// Prefixing a line changes the extracted callee ID without touching main.go.
	writeFile(t, filepath.Join(dir, "lib.go"), "package main\n\n\nfunc Helper() {}\n")
	res := idx.SyncFiles([]string{"lib.go"}, Options{})
	if res.FilesModified != 1 {
		t.Fatalf("SyncFiles result = %+v, want one modified file", res)
	}

	edges, err := idx.Store().GetOutgoingEdges(caller[0].ID, []model.EdgeKind{model.EdgeCalls}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("caller edges after changed callee = %+v, want one call edge", edges)
	}
	target, err := idx.Store().GetNodeByID(edges[0].Target)
	if err != nil || target == nil || target.Name != "Helper" {
		t.Fatalf("edge target = %+v, %v; want Helper", target, err)
	}
}

func TestSyncBodyOnlyCalleeEditPreservesSameIDIncomingEdge(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "callee.go"), "package main\n\nfunc Helper() { _ = 1 }\n")
	writeFile(t, filepath.Join(dir, "caller.go"), "package main\n\nfunc main() { Helper() }\n")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	caller, err := idx.Store().GetNodesByName("main")
	if err != nil || len(caller) != 1 {
		t.Fatalf("caller: %v %d", err, len(caller))
	}
	writeFile(t, filepath.Join(dir, "callee.go"), "package main\n\nfunc Helper() { _ = 2 }\n")
	res := idx.SyncFiles([]string{"callee.go"}, Options{})
	if len(res.Errors) != 0 {
		t.Fatalf("sync: %+v", res.Errors)
	}
	edges, err := idx.Store().GetOutgoingEdges(caller[0].ID, []model.EdgeKind{model.EdgeCalls}, "")
	if err != nil || len(edges) != 1 {
		t.Fatalf("caller edges = %+v, %v; want one", edges, err)
	}
	target, err := idx.Store().GetNodeByID(edges[0].Target)
	if err != nil || target == nil || target.Name != "Helper" {
		t.Fatalf("target = %+v, %v", target, err)
	}
}

func TestSyncChangedCalleeFailureDoesNotRestoreDuplicateEdges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.go"), "package main\n\nfunc Helper() {}\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() { Helper() }\n")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	caller, err := idx.Store().GetNodesByName("main")
	if err != nil || len(caller) != 1 {
		t.Fatalf("caller: %v %d", err, len(caller))
	}

	// A recognized source file above the parser cap cannot replace its old
	// nodes. Both retries must report the failed refresh and leave the one
	// persisted caller edge untouched.
	writeFile(t, filepath.Join(dir, "lib.go"), strings.Repeat("x", MaxFileSize+1))
	for attempt := 0; attempt < 2; attempt++ {
		res := idx.SyncFiles([]string{"lib.go"}, Options{})
		if len(res.Errors) == 0 {
			t.Fatalf("attempt %d errors = none, want extraction failure", attempt)
		}
		edges, err := idx.Store().GetOutgoingEdges(caller[0].ID, []model.EdgeKind{model.EdgeCalls}, "")
		if err != nil || len(edges) != 1 {
			t.Fatalf("attempt %d edges = %+v, %v; want exactly one original edge", attempt, edges, err)
		}
	}
}

func TestSyncMixedBatchPreservesSuccessfulCalleeRelationships(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.go"), "package main\nfunc Helper() {}\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\nfunc main() { Helper() }\n")
	writeFile(t, filepath.Join(dir, "bad.go"), "package main\nfunc Bad() {}\n")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	caller, _ := idx.Store().GetNodesByName("main")
	writeFile(t, filepath.Join(dir, "lib.go"), "package main\n\nfunc Helper() {}\n")
	writeFile(t, filepath.Join(dir, "bad.go"), strings.Repeat("x", MaxFileSize+1))
	res := idx.SyncFiles([]string{"lib.go", "bad.go"}, Options{})
	if len(res.Errors) == 0 {
		t.Fatal("mixed sync should report failed file")
	}
	edges, err := idx.Store().GetOutgoingEdges(caller[0].ID, []model.EdgeKind{model.EdgeCalls}, "")
	if err != nil || len(edges) != 1 {
		t.Fatalf("successful callee edge = %+v, %v", edges, err)
	}
}

func TestSyncMixedBatchResolvesSuccessfulCallerDespiteFailedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.go"), "package main\nfunc Helper() {}\nfunc Other() {}\n")
	writeFile(t, filepath.Join(dir, "caller.go"), "package main\nfunc Caller() { Helper() }\n")
	writeFile(t, filepath.Join(dir, "bad.go"), "package main\nfunc Bad() {}\n")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	writeFile(t, filepath.Join(dir, "caller.go"), "package main\nfunc Caller() { Other() }\n")
	writeFile(t, filepath.Join(dir, "bad.go"), strings.Repeat("x", MaxFileSize+1))
	for attempt := 0; attempt < 2; attempt++ {
		res := idx.SyncFiles([]string{"caller.go", "bad.go"}, Options{})
		if len(res.Errors) == 0 {
			t.Fatalf("attempt %d should report bad.go", attempt)
		}
		caller, err := idx.Store().GetNodesByName("Caller")
		if err != nil || len(caller) != 1 {
			t.Fatalf("caller: %v %d", err, len(caller))
		}
		edges, err := idx.Store().GetOutgoingEdges(caller[0].ID, []model.EdgeKind{model.EdgeCalls}, "")
		if err != nil || len(edges) != 1 {
			t.Fatalf("attempt %d edges = %+v, %v", attempt, edges, err)
		}
		target, err := idx.Store().GetNodeByID(edges[0].Target)
		if err != nil || target == nil || target.Name != "Other" {
			t.Fatalf("attempt %d target = %+v, %v", attempt, target, err)
		}
		if unresolved, err := idx.Store().GetUnresolvedReferencesCount(); err != nil || unresolved != 0 {
			t.Fatalf("attempt %d unresolved = %d, %v", attempt, unresolved, err)
		}
	}
}

func TestRestoreIncomingEdgesKeepsSameQualifiedOverloadsDistinct(t *testing.T) {
	_, idx := newSyncProject(t)
	s := idx.Store()
	oldInt := model.Node{ID: "method:old-int", Kind: model.KindMethod, Name: "Run", QualifiedName: "Service::Run", FilePath: "Service.cs", Language: model.LangCSharp, Signature: "Run(int value)", ReturnType: "void", StartLine: 1, EndLine: 2}
	oldString := oldInt
	oldString.ID, oldString.Signature = "method:old-string", "Run(string value)"
	caller := model.Node{ID: "method:caller", Kind: model.KindMethod, Name: "Call", QualifiedName: "Caller::Call", FilePath: "Caller.cs", Language: model.LangCSharp, StartLine: 1, EndLine: 2}
	if err := s.InsertNodes([]model.Node{oldInt, oldString, caller}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertEdges([]model.Edge{{Source: caller.ID, Target: oldInt.ID, Kind: model.EdgeCalls}, {Source: caller.ID, Target: oldString.ID, Kind: model.EdgeCalls}}); err != nil {
		t.Fatal(err)
	}
	backups := []incomingEdgeBackup{
		{store: s, target: oldInt, edge: model.Edge{Source: caller.ID, Target: oldInt.ID, Kind: model.EdgeCalls}},
		{store: s, target: oldString, edge: model.Edge{Source: caller.ID, Target: oldString.ID, Kind: model.EdgeCalls}},
	}
	if err := s.DeleteNodesByFile("Service.cs"); err != nil {
		t.Fatal(err)
	}
	newInt := oldInt
	newInt.ID = "method:new-int"
	newString := oldString
	newString.ID = "method:new-string"
	if err := s.InsertNodes([]model.Node{newInt, newString}); err != nil {
		t.Fatal(err)
	}
	if err := idx.restoreIncomingEdges(backups); err != nil {
		t.Fatal(err)
	}
	edges, err := s.GetOutgoingEdges(caller.ID, []model.EdgeKind{model.EdgeCalls}, "")
	if err != nil || len(edges) != 2 {
		t.Fatalf("restored overload edges = %+v, %v", edges, err)
	}
	got := map[string]bool{}
	for _, edge := range edges {
		got[edge.Target] = true
	}
	if !got[newInt.ID] || !got[newString.ID] {
		t.Fatalf("overload targets = %v, want distinct int/string targets", got)
	}
}

func TestSyncHashesSameSizeSameMillisecondFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	writeFile(t, path, "package main\n\nfunc Old() {}\n")
	mustGit(t, dir, "init")
	mustGit(t, dir, "add", "main.go")
	mustGit(t, dir, "commit", "-m", "initial")
	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	rec, err := idx.Store().GetFileByPath("main.go")
	if err != nil || rec == nil {
		t.Fatalf("file record: %v, %+v", err, rec)
	}

	writeFile(t, path, "package main\n\nfunc New() {}\n") // same byte length
	oldTime := time.UnixMilli(rec.ModifiedAt)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	changes := idx.GetChangedFiles()
	if !slices.Contains(changes.Modified, "main.go") {
		t.Fatalf("same-size/same-millisecond changes = %+v, want main.go modified", changes)
	}
	res := idx.Sync(Options{})
	if res.FilesModified != 1 || !hasNodeNamed(t, idx, "New") {
		t.Fatalf("Sync result = %+v; New indexed = %v, want changed file indexed", res, hasNodeNamed(t, idx, "New"))
	}
}

func TestGetChangedFilesHashesSameMtimeNonGitEdit(t *testing.T) {
	dir, idx := newSyncProject(t)
	path := filepath.Join(dir, "src", "index.ts")
	rec, err := idx.fileRecord("src/index.ts")
	if err != nil || rec == nil {
		t.Fatalf("record: %+v %v", rec, err)
	}
	writeFile(t, path, "export function cello() { return 'world'; }")
	tm := time.UnixMilli(rec.ModifiedAt)
	if err := os.Chtimes(path, tm, tm); err != nil {
		t.Fatal(err)
	}
	changes := idx.GetChangedFiles()
	if !slices.Contains(changes.Modified, "src/index.ts") {
		t.Fatalf("non-git same-mtime changes = %+v", changes)
	}
}

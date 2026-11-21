package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/indexer"
)

// seedRepo builds a canonical clone (optionally indexed) and returns it with
// a path for a linked worktree beside it.
func seedRepo(t *testing.T, indexed bool) (canonical, worktree string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	canonical = filepath.Join(base, "repo")
	worktree = filepath.Join(base, "wt")
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSeedFile(t, filepath.Join(canonical, "a.go"), "package a\n\nfunc Foo() {}\n\nfunc Bar() { Foo() }\n")
	gitRun(t, canonical, "init", "-q")
	gitRun(t, canonical, "add", ".")
	gitRun(t, canonical, "commit", "-q", "-m", "init")
	if indexed {
		idx, res, err := indexer.Init(canonical, indexer.Options{})
		if err != nil || !res.Success {
			t.Fatalf("init canonical: %v %+v", err, res.Errors)
		}
		_ = idx.Close()
	}
	return canonical, worktree
}

func writeSeedFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func treeState(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == filepath.Join(".git", "worktrees") {
			// Git keeps each linked worktree's own index and HEAD here; git
			// commands run in the worktree update them, not codegrapher.
			return filepath.SkipDir
		}
		lines = append(lines, fmt.Sprintf("%s %d %d", rel, fi.Size(), fi.ModTime().UnixNano()))
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// withSeams restores the provisioning seams and the opt-out after a test.
func withSeams(t *testing.T) {
	t.Helper()
	seed, full, catchUp, flag := seedIndexFunc, fullInitFunc, catchUpFunc, noWorktreeSeed
	t.Setenv(worktreeSeedEnv, "")
	t.Cleanup(func() {
		seedIndexFunc, fullInitFunc, catchUpFunc, noWorktreeSeed = seed, full, catchUp, flag
	})
}

// Issue #67 acceptance: seeded
func TestQueryInNewWorktreeSeedsFromCanonicalWithoutWritingIt(t *testing.T) {
	withSeams(t)
	canonical, worktree := seedRepo(t, true)
	gitRun(t, canonical, "worktree", "add", "-q", "-b", "wt", worktree)
	writeSeedFile(t, filepath.Join(worktree, "b.go"), "package a\n\nfunc Baz() { Foo() }\n")
	before := treeState(t, canonical)

	out, err := runCmd(t, newCallersCmd(), "Foo", "--path", worktree, "--format", "json")
	if err != nil {
		t.Fatalf("callers: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Baz") || !strings.Contains(out, "Bar") {
		t.Fatalf("callers in seeded worktree missed a caller:\n%s", out)
	}
	if after := treeState(t, canonical); after != before {
		t.Fatalf("canonical clone changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	idx, err := indexer.Open(worktree, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	if got := idx.SeededFrom(); got != canonical {
		t.Fatalf("SeededFrom = %q, want %q", got, canonical)
	}
	if changes := idx.GetChangedFiles(); len(changes.Added)+len(changes.Modified)+len(changes.Removed) != 0 {
		t.Fatalf("seeded index not caught up: %+v", changes)
	}
}

// Issue #67 acceptance: no canonical index
func TestProvisionWithoutCanonicalIndexBuildsFreshIndex(t *testing.T) {
	withSeams(t)
	canonical, worktree := seedRepo(t, false)
	gitRun(t, canonical, "worktree", "add", "-q", "-b", "wt", worktree)
	seedIndexFunc = func(string, string) (indexer.SeedResult, error) {
		t.Fatal("seed attempted without a canonical index")
		return indexer.SeedResult{}, nil
	}
	_, stderr := captureOutput(t, func() {
		if !provisionWorktreeIndex(worktree, false) {
			t.Error("provisionWorktreeIndex reported no index created")
		}
	})
	if !indexer.IsInitialized(worktree) {
		t.Fatalf("worktree not initialized; stderr:\n%s", stderr)
	}
	if indexer.IsInitialized(canonical) {
		t.Fatal("fallback init wrote an index into the canonical clone")
	}
}

// Issue #67 acceptance: copy failure falls back to init
func TestProvisionSeedFailureFallsBackToInit(t *testing.T) {
	withSeams(t)
	canonical, worktree := seedRepo(t, true)
	gitRun(t, canonical, "worktree", "add", "-q", "-b", "wt", worktree)
	seedIndexFunc = func(string, string) (indexer.SeedResult, error) {
		return indexer.SeedResult{}, errors.New("disk full")
	}
	var initRoot string
	fullInitFunc = func(root string) error { initRoot = root; return nil }
	captureOutput(t, func() { provisionWorktreeIndex(worktree, false) })
	if initRoot != worktree {
		t.Fatalf("fallback init root = %q, want %q", initRoot, worktree)
	}
}

func TestProvisionCatchUpFailureDiscardsSeedAndFallsBackToInit(t *testing.T) {
	withSeams(t)
	canonical, worktree := seedRepo(t, true)
	gitRun(t, canonical, "worktree", "add", "-q", "-b", "wt", worktree)
	catchUpFunc = func(string) error { return errors.New("locked") }
	var initRoot string
	fullInitFunc = func(root string) error {
		initRoot = root
		if indexer.IsInitialized(root) {
			t.Error("seeded copy not discarded before the fallback init")
		}
		return nil
	}
	captureOutput(t, func() { provisionWorktreeIndex(worktree, false) })
	if initRoot != worktree {
		t.Fatalf("fallback init root = %q", initRoot)
	}
}

// Issue #67 acceptance: opt-out
func TestProvisionOptOut(t *testing.T) {
	for _, mode := range []string{"env", "flag"} {
		t.Run(mode, func(t *testing.T) {
			withSeams(t)
			canonical, worktree := seedRepo(t, true)
			gitRun(t, canonical, "worktree", "add", "-q", "-b", "wt", worktree)
			if mode == "env" {
				t.Setenv(worktreeSeedEnv, "0")
			} else {
				root := NewRootCmd()
				root.SetArgs([]string{"--no-worktree-seed", "status", worktree})
				captureOutput(t, func() { _ = root.Execute() })
				if !noWorktreeSeed {
					t.Fatal("--no-worktree-seed did not set the opt-out")
				}
			}
			captureOutput(t, func() {
				if provisionWorktreeIndex(worktree, false) {
					t.Error("opted-out provisioning created an index")
				}
			})
			if indexer.IsInitialized(worktree) {
				t.Fatal("opted-out worktree got an index")
			}
		})
	}
}

func TestProvisionIgnoresCanonicalCloneAndPlainDirs(t *testing.T) {
	withSeams(t)
	canonical, _ := seedRepo(t, false)
	fullInitFunc = func(string) error { t.Fatal("init attempted"); return nil }
	if provisionWorktreeIndex(canonical, false) || provisionWorktreeIndex(t.TempDir(), false) {
		t.Fatal("provisioning acted outside a linked worktree")
	}
}

// Issue #67 acceptance: guard still refuses an unseeded index
func TestMismatchGuardRefusesUnseededBorrowedIndex(t *testing.T) {
	withSeams(t)
	t.Setenv(worktreeSeedEnv, "0")
	canonical, _ := seedRepo(t, true)
	// A worktree nested inside the canonical clone borrows its index by the
	// upward walk (#40).
	nested := filepath.Join(canonical, ".claude", "worktrees", "feature")
	gitRun(t, canonical, "worktree", "add", "-q", "-b", "feature", nested)

	if _, err := runCmd(t, newContextCmd(), "Foo", "--path", nested); err == nil ||
		!strings.Contains(err.Error(), "different git worktree") {
		t.Fatalf("context err = %v, want worktree mismatch", err)
	}
	if _, err := runCmd(t, newSyncCmd(), nested); err == nil ||
		!strings.Contains(err.Error(), "different git worktree") {
		t.Fatalf("sync err = %v, want worktree mismatch", err)
	}
	out, err := runCmd(t, newStatusCmd(), "--format", "json", nested)
	if err != nil {
		t.Fatal(err)
	}
	if decodeStatus(t, out)["worktreeMismatch"] == nil {
		t.Fatalf("status in a borrowing worktree reported no mismatch (#40):\n%s", out)
	}
	if _, err := runCmd(t, newStatusCmd(), "--refresh", nested); err == nil {
		t.Fatal("status --refresh refreshed another worktree's index")
	}

	// With seeding on, the same worktree gets its own index instead.
	t.Setenv(worktreeSeedEnv, "")
	if _, err := runCmd(t, newSyncCmd(), "--quiet", nested); err != nil {
		t.Fatalf("sync with seeding: %v", err)
	}
	if !indexer.IsInitialized(nested) {
		t.Fatal("sync did not seed the nested worktree")
	}
	if m := indexer.DetectWorktreeIndexMismatch(nested, findNearestExisting(nested)); m != nil {
		t.Fatalf("seeded worktree still mismatched: %+v", m)
	}
}

func TestInitInWorktreeSeedsFromCanonical(t *testing.T) {
	withSeams(t)
	canonical, worktree := seedRepo(t, true)
	gitRun(t, canonical, "worktree", "add", "-q", "-b", "wt", worktree)
	if _, err := runCmd(t, newInitCmd(), worktree); err != nil {
		t.Fatal(err)
	}
	idx, err := indexer.Open(worktree, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idx.Close() }()
	if idx.SeededFrom() != canonical {
		t.Fatal("init in a worktree did not seed from the canonical clone")
	}
}

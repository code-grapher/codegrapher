package indexer

// Ported from __tests__/worktree-detection.test.ts (issue #155 upstream).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newWorktreePair creates a main repo with a commit and a linked worktree
// inside it (under .claude/worktrees/feature — the layout that triggers the
// borrowed-index walk).
func newWorktreePair(t *testing.T) (mainRepo, worktree string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	mainRepo = t.TempDir()
	writeFile(t, filepath.Join(mainRepo, "a.go"), "package a\n")
	mustGit(t, mainRepo, "init")
	mustGit(t, mainRepo, "config", "commit.gpgsign", "false")
	mustGit(t, mainRepo, "add", ".")
	mustGit(t, mainRepo, "commit", "-q", "-m", "init")

	worktree = filepath.Join(mainRepo, ".claude", "worktrees", "feature")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, mainRepo, "worktree", "add", "-q", "-b", "feature", worktree)
	t.Cleanup(func() {
		cmd := exec.Command("git", "-C", mainRepo, "worktree", "remove", "--force", worktree)
		_ = cmd.Run() // best effort
	})
	return mainRepo, worktree
}

func TestDetectWorktreeMismatchFlagsBorrowedIndex(t *testing.T) {
	mainRepo, worktree := newWorktreePair(t)

	m := DetectWorktreeIndexMismatch(worktree, mainRepo)
	if m == nil {
		t.Fatal("mismatch = nil, want detected")
	}
	if m.WorktreeRoot != realpathOrAbs(worktree) {
		t.Errorf("WorktreeRoot = %q, want %q", m.WorktreeRoot, realpathOrAbs(worktree))
	}
	if m.IndexRoot != realpathOrAbs(mainRepo) {
		t.Errorf("IndexRoot = %q, want %q", m.IndexRoot, realpathOrAbs(mainRepo))
	}
}

func TestDetectWorktreeMismatchSameTree(t *testing.T) {
	mainRepo, _ := newWorktreePair(t)
	if m := DetectWorktreeIndexMismatch(mainRepo, mainRepo); m != nil {
		t.Errorf("mismatch = %+v, want nil for same tree", m)
	}
}

func TestDetectWorktreeMismatchSubdirectory(t *testing.T) {
	mainRepo, _ := newWorktreePair(t)
	sub := filepath.Join(mainRepo, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if m := DetectWorktreeIndexMismatch(sub, mainRepo); m != nil {
		t.Errorf("mismatch = %+v, want nil for subdir of same tree", m)
	}
}

func TestDetectWorktreeMismatchNonGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	plain := t.TempDir()
	if m := DetectWorktreeIndexMismatch(plain, plain); m != nil {
		t.Errorf("mismatch = %+v, want nil outside git", m)
	}
}

func TestDetectWorktreeMismatchPlainIndexRoot(t *testing.T) {
	_, worktree := newWorktreePair(t)
	// indexRoot is a plain directory that is NOT a working-tree root: no warning
	// (keeps non-git and monorepo-subdir layouts from producing false alarms).
	plain := t.TempDir()
	if m := DetectWorktreeIndexMismatch(worktree, plain); m != nil {
		t.Errorf("mismatch = %+v, want nil for non-worktree index root", m)
	}
}

func TestGitWorktreeRootDistinctTrees(t *testing.T) {
	mainRepo, worktree := newWorktreePair(t)
	mainRoot := GitWorktreeRoot(mainRepo)
	wtRoot := GitWorktreeRoot(worktree)
	if mainRoot == "" || wtRoot == "" {
		t.Fatalf("roots: main=%q worktree=%q", mainRoot, wtRoot)
	}
	if mainRoot == wtRoot {
		t.Error("main and linked worktree report the same root")
	}
	if GitWorktreeRoot(t.TempDir()) != "" {
		t.Error("GitWorktreeRoot != \"\" for non-repo")
	}
}

func TestWorktreeMismatchMessages(t *testing.T) {
	m := WorktreeIndexMismatch{WorktreeRoot: "/work/tree", IndexRoot: "/main/tree"}

	warning := WorktreeMismatchWarning(m)
	for _, want := range []string{"/work/tree", "/main/tree", "codegraph init -i"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning missing %q:\n%s", want, warning)
		}
	}
	notice := WorktreeMismatchNotice(m)
	for _, want := range []string{"/work/tree", "/main/tree", "codegraph init -i"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice missing %q:\n%s", want, notice)
		}
	}
	if strings.Contains(notice, "\n") {
		t.Error("notice must be single-line")
	}
}

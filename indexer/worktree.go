package indexer

// Git worktree awareness, ported from src/sync/worktree.ts.
//
// A CodeGraph index lives in a .codegraph/ directory and is resolved by
// walking up parent directories to the nearest one (FindNearestCodeGraphRoot).
// That walk is unaware of git worktrees: when a worktree is created *inside*
// the main checkout (e.g. under .claude/worktrees/<name>/), a command run
// from the worktree walks up and silently resolves the MAIN checkout's index.
// This module detects that "borrowed index" situation so callers can warn.
//
// Detection is best-effort: when git is unavailable or the path isn't a repo,
// it reports "no mismatch" and callers carry on unchanged.

import (
	"fmt"
	"path/filepath"
)

// GitWorktreeRoot returns the absolute, symlink-resolved toplevel of the git
// working tree dir belongs to, or "" when dir isn't inside a git repo (or
// git is missing). `git rev-parse --show-toplevel` returns the per-worktree
// root: the main checkout and each linked worktree report their own distinct
// directory.
func GitWorktreeRoot(dir string) string {
	out, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil || out == "" {
		return ""
	}
	return realpathOrAbs(out)
}

// WorktreeIndexMismatch describes a query borrowing another tree's index.
type WorktreeIndexMismatch struct {
	// WorktreeRoot is the git working tree the command was run from.
	WorktreeRoot string
	// IndexRoot is the (different) working tree whose .codegraph index is
	// being used.
	IndexRoot string
}

// DetectWorktreeIndexMismatch detects when startPath lives in one git working
// tree but the resolved CodeGraph index (indexRoot) belongs to a *different*
// working tree.
//
// Returns nil — meaning "nothing to warn about" — when startPath isn't in a
// git repo (or git is unavailable), the index already lives in startPath's
// own working tree, or indexRoot isn't itself a working-tree root (an
// unrelated parent dir that merely happens to contain a .codegraph/), which
// keeps non-git and monorepo-subdir layouts from producing false warnings.
func DetectWorktreeIndexMismatch(startPath, indexRoot string) *WorktreeIndexMismatch {
	worktreeRoot := GitWorktreeRoot(startPath)
	if worktreeRoot == "" {
		return nil
	}

	resolvedIndexRoot := realpathOrAbs(indexRoot)
	if worktreeRoot == resolvedIndexRoot {
		return nil
	}

	// Only flag it when the index root is itself a real working-tree root.
	if GitWorktreeRoot(resolvedIndexRoot) != resolvedIndexRoot {
		return nil
	}

	return &WorktreeIndexMismatch{WorktreeRoot: worktreeRoot, IndexRoot: resolvedIndexRoot}
}

// WorktreeMismatchWarning is the one-line-per-fact warning describing a
// detected mismatch.
func WorktreeMismatchWarning(m WorktreeIndexMismatch) string {
	return fmt.Sprintf(
		"This CodeGraph index belongs to a different git working tree.\n"+
			"  Running in: %s\n"+
			"  Index from: %s\n"+
			"Results reflect that tree's code (often a different branch), not this worktree — "+
			"symbols changed only here are missing. Run \"codegraph init -i\" in this worktree "+
			"for a worktree-local index.",
		m.WorktreeRoot, m.IndexRoot)
}

// WorktreeMismatchNotice is the compact, single-line variant for prefixing a
// tool's result.
func WorktreeMismatchNotice(m WorktreeIndexMismatch) string {
	return fmt.Sprintf(
		"⚠ CodeGraph results below come from a different git worktree (%s), "+
			"not where you're working (%s) — they may reflect another branch, "+
			"and symbols changed only here are missing. Run \"codegraph init -i\" here for a "+
			"worktree-local index.",
		m.IndexRoot, m.WorktreeRoot)
}

// realpathOrAbs resolves symlinks where possible so tmp/realpath quirks don't
// break equality.
func realpathOrAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

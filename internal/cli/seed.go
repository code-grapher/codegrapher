package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/specscore/codegrapher/indexer"
)

// noWorktreeSeed is the root --no-worktree-seed flag.
var noWorktreeSeed bool

// worktreeSeedEnv is the environment opt-out: CODEGRAPHER_WORKTREE_SEED=0
// (or false/off/no) disables seeding, like --no-worktree-seed.
const worktreeSeedEnv = "CODEGRAPHER_WORKTREE_SEED"

func worktreeSeedDisabled() bool {
	if noWorktreeSeed {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(worktreeSeedEnv))) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// Seams for tests.
var (
	seedIndexFunc = indexer.SeedFromCanonical
	fullInitFunc  = func(root string) error {
		idx, res, err := indexer.Init(root, indexer.Options{})
		if err != nil {
			return err
		}
		defer func() { _ = idx.Close() }()
		if !res.Success {
			return fmt.Errorf("index build reported failure")
		}
		return nil
	}
	// catchUpFunc brings a freshly seeded copy up to date with the worktree.
	catchUpFunc = func(root string) error {
		_, err := refreshIndex(root)
		return err
	}
)

// provisionWorktreeIndex gives a linked Git worktree its own index on first
// use (code-grapher/codegrapher#67). When startPath lies in a linked worktree
// that has no index of its own — none at all, or only one borrowed by walking
// up into an enclosing canonical clone (#40) — it seeds one from the canonical
// clone's index and catches it up incrementally. Without a canonical index, or
// when seeding fails, it falls back to a full build when forInit is false.
// forInit (the init command) also seeds only an index at exactly startPath,
// leaving every fallback to init's own full build.
// It never writes to the canonical clone, and is a no-op outside linked
// worktrees or when opted out. Notices go to stderr so JSON stdout stays
// clean. It reports whether an index was created.
func provisionWorktreeIndex(startPath string, forInit bool) bool {
	allowFullInit := !forInit
	if indexer.IsInitialized(startPath) || worktreeSeedDisabled() {
		return false
	}
	worktreeRoot, canonicalRoot, ok := indexer.LinkedWorktree(startPath)
	if !ok {
		return false
	}
	if nearest := indexer.FindNearestCodeGraphRoot(startPath); nearest != "" && within(nearest, worktreeRoot) {
		return false // the worktree already has its own index
	}

	source, target := indexer.FindCanonicalIndex(startPath, worktreeRoot, canonicalRoot)
	if forInit && source != "" && realpath(startPath) != realpath(target) {
		return false
	}
	if source == "" {
		if !allowFullInit {
			return false
		}
		return fullInitWorktree(worktreeRoot, "the canonical clone "+canonicalRoot+" has no index")
	}

	res, err := seedIndexFunc(source, target)
	if err != nil {
		if !allowFullInit {
			fmt.Fprintf(os.Stderr, "codegrapher: could not seed this worktree's index from %s: %s\n", source, err)
			return false
		}
		return fullInitWorktree(target, fmt.Sprintf("seeding from %s failed: %s", source, err))
	}
	if err := catchUpFunc(target); err != nil {
		_ = indexer.RemoveDirectory(target)
		if !allowFullInit {
			fmt.Fprintf(os.Stderr, "codegrapher: seeded index from %s could not be caught up: %s\n", source, err)
			return false
		}
		return fullInitWorktree(target, fmt.Sprintf("catching up the index seeded from %s failed: %s", source, err))
	}
	mode := "full hash check"
	if res.FastPath {
		mode = "git diff from the base revision"
	}
	fmt.Fprintf(os.Stderr, "codegrapher: seeded this worktree's index from %s (%s, %d databases) and caught it up by %s\n",
		source, res.Method, res.Databases, mode)
	return true
}

func fullInitWorktree(root, why string) bool {
	fmt.Fprintf(os.Stderr, "codegrapher: building a fresh index for this worktree (%s)\n", why)
	if err := fullInitFunc(root); err != nil {
		fmt.Fprintf(os.Stderr, "codegrapher: building the worktree index failed: %s\n", err)
		return false
	}
	return true
}

// within reports whether path equals root or lies below it (both resolved).
func within(path, root string) bool {
	path, root = realpath(path), realpath(root)
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func realpath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Package indexer orchestrates building and maintaining a codegraph index:
// directory management, file scanning, full indexing (Init), incremental
// sync, git-hook installation, and git-worktree awareness.
//
// Ported from src/index.ts, src/directory.ts, src/extraction/index.ts and
// src/sync/ of github.com/colbymchenry/codegraph (MIT). Library-first: no UI,
// progress is reported through plain callbacks.
package indexer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// defaultCodeGraphDir is the default per-project data directory name.
const defaultCodeGraphDir = ".codegraph"

var warnBadDirOnce sync.Once

// CodeGraphDirName resolves the per-project data directory name, honoring the
// CODEGRAPH_DIR environment override (default ".codegraph"). The override must
// be a plain directory name; anything containing path separators, "..", or an
// absolute path is ignored (with a one-time stderr warning), mirroring
// codeGraphDirName() in src/directory.ts.
func CodeGraphDirName() string {
	raw := strings.TrimSpace(os.Getenv("CODEGRAPH_DIR"))
	if raw == "" {
		return defaultCodeGraphDir
	}
	invalid := raw == "." ||
		strings.Contains(raw, "..") ||
		strings.Contains(raw, "/") ||
		strings.Contains(raw, "\\") ||
		filepath.IsAbs(raw)
	if invalid {
		warnBadDirOnce.Do(func() {
			fmt.Fprintf(os.Stderr,
				"[codegraph] Ignoring invalid CODEGRAPH_DIR=%q — it must be a plain "+
					"directory name (no path separators, no \"..\", not absolute). Using %q.\n",
				raw, defaultCodeGraphDir)
		})
		return defaultCodeGraphDir
	}
	return raw
}

// IsCodeGraphDataDir reports whether name (a single path segment) is a
// CodeGraph data directory: the default ".codegraph", the active
// CODEGRAPH_DIR override, or any ".codegraph-*" sibling.
func IsCodeGraphDataDir(name string) bool {
	return name == defaultCodeGraphDir ||
		name == CodeGraphDirName() ||
		strings.HasPrefix(name, defaultCodeGraphDir+"-")
}

// GetCodeGraphDir returns the .codegraph directory path for a project.
func GetCodeGraphDir(projectRoot string) string {
	return filepath.Join(projectRoot, CodeGraphDirName())
}

// DatabasePath returns the path of the index database for a project.
func DatabasePath(projectRoot string) string {
	return filepath.Join(GetCodeGraphDir(projectRoot), "codegraph.db")
}

// IsInitialized reports whether a project has been initialized: both the
// .codegraph/ directory AND codegraph.db must exist.
func IsInitialized(projectRoot string) bool {
	dir := GetCodeGraphDir(projectRoot)
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "codegraph.db"))
	return err == nil
}

// FindNearestCodeGraphRoot walks up from startPath to find the nearest
// CodeGraph-initialized project root, like git finding .git/. Returns ""
// when none is found.
func FindNearestCodeGraphRoot(startPath string) string {
	current, err := filepath.Abs(startPath)
	if err != nil {
		return ""
	}
	for {
		if IsInitialized(current) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "" // reached filesystem root
		}
		current = parent
	}
}

// dataDirGitignore is written inside .codegraph so its transient files never
// show up in git. Verbatim from createDirectory() in src/directory.ts.
const dataDirGitignore = `# CodeGraph data files — local to each machine, not for committing.
# Ignore everything in .codegraph/ except this file itself, so transient
# files (the database, daemon.pid, sockets, logs) never show up in git.
*
!.gitignore
`

// CreateDirectory creates the .codegraph directory structure. It errors only
// when codegraph.db already exists (the directory alone is fine).
func CreateDirectory(projectRoot string) error {
	dir := GetCodeGraphDir(projectRoot)
	dbPath := filepath.Join(dir, "codegraph.db")
	if _, err := os.Stat(dbPath); err == nil {
		return fmt.Errorf("CodeGraph already initialized in %s", projectRoot)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	giPath := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(giPath); os.IsNotExist(err) {
		if err := os.WriteFile(giPath, []byte(dataDirGitignore), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// RemoveDirectory removes the .codegraph directory. A symlinked .codegraph is
// unlinked, never followed (mirrors removeDirectory in src/directory.ts).
func RemoveDirectory(projectRoot string) error {
	dir := GetCodeGraphDir(projectRoot)
	fi, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return os.Remove(dir)
	}
	return os.RemoveAll(dir)
}

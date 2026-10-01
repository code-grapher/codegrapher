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
	"os/exec"
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

// DatabasePath returns the legacy single-database path for a project. The index
// is now partitioned into per-scope databases (see ScopedDatabasePath); this
// helper remains only for the `import` command pending scoped-import support.
func DatabasePath(projectRoot string) string {
	return filepath.Join(GetCodeGraphDir(projectRoot), "codegraph.db")
}

// hasScopeDB reports whether dir contains at least one per-scope database
// (codegraph-{lang}-{version}.db).
func hasScopeDB(dir string) bool {
	matches, err := filepath.Glob(filepath.Join(dir, dbPrefix+"*"+dbSuffix))
	return err == nil && len(matches) > 0
}

// IsInitialized reports whether a project has been initialized: the .codegraph/
// directory exists AND it holds at least one per-scope database.
func IsInitialized(projectRoot string) bool {
	dir := GetCodeGraphDir(projectRoot)
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	return hasScopeDB(dir)
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
// when a per-scope database already exists (the directory alone is fine).
func CreateDirectory(projectRoot string) error {
	dir := GetCodeGraphDir(projectRoot)
	if hasScopeDB(dir) {
		return fmt.Errorf("CodeGraph already initialized in %s", projectRoot)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := ensureDataDirectoryGitExclude(projectRoot); err != nil {
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

// ensureDataDirectoryGitExclude keeps the local index out of Git status without
// modifying a repository's tracked .gitignore. Git resolves this path correctly
// for both ordinary checkouts and linked worktrees.
func ensureDataDirectoryGitExclude(projectRoot string) error {
	output, err := exec.Command("git", "-C", projectRoot, "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		// Indexing non-Git directories remains supported.
		return nil
	}

	excludePath := strings.TrimSpace(string(output))
	if excludePath == "" {
		return nil
	}
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(projectRoot, excludePath)
	}
	entry := filepath.ToSlash(CodeGraphDirName()) + "/"
	contents, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read Git exclude file: %w", err)
	}
	for _, line := range strings.Split(string(contents), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return fmt.Errorf("create Git exclude directory: %w", err)
	}
	if len(contents) > 0 && !strings.HasSuffix(string(contents), "\n") {
		contents = append(contents, '\n')
	}
	contents = append(contents, entry...)
	contents = append(contents, '\n')
	if err := os.WriteFile(excludePath, contents, 0o644); err != nil {
		return fmt.Errorf("write Git exclude file: %w", err)
	}
	return nil
}

// gitIgnoreState probes whether the data directory's contents are ignored by
// Git. isRepo is false outside a Git repository (nothing to check or repair).
// The probe path is never a real file (so it is never tracked, which would
// mask the ignore rules); check-ignore evaluates patterns only.
func gitIgnoreState(projectRoot string) (ignored, isRepo bool) {
	probe := filepath.ToSlash(filepath.Join(CodeGraphDirName(), ".ignore-probe"))
	err := exec.Command("git", "-C", projectRoot, "check-ignore", "-q", "--", probe).Run()
	if err == nil {
		return true, true
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false, true
	}
	return false, false
}

// trackedDataFiles lists data-directory files Git tracks, excluding the
// directory's own .gitignore (committing that file is harmless and allowed).
func trackedDataFiles(projectRoot string) []string {
	dir := filepath.ToSlash(CodeGraphDirName())
	out, err := exec.Command("git", "-C", projectRoot, "ls-files", "--", dir).Output()
	if err != nil {
		return nil
	}
	var tracked []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != dir+"/.gitignore" {
			tracked = append(tracked, line)
		}
	}
	return tracked
}

func dataDirWarnings(projectRoot string, ignored bool, repairHint string) []string {
	dir := CodeGraphDirName()
	var warnings []string
	if !ignored {
		warnings = append(warnings, fmt.Sprintf("%s/ is not ignored by Git; %s", dir, repairHint))
	}
	if tracked := trackedDataFiles(projectRoot); len(tracked) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%s/ has %d file(s) tracked by Git (e.g. %s); untrack with `git rm -r --cached %s`",
			dir, len(tracked), tracked[0], dir))
	}
	return warnings
}

// CheckDataDirIgnored reports, without writing anything, whether the data
// directory is ignored by Git and whether Git tracks any of its files. Outside
// a Git repository it returns nil.
func CheckDataDirIgnored(projectRoot string) []string {
	ignored, isRepo := gitIgnoreState(projectRoot)
	if !isRepo {
		return nil
	}
	return dataDirWarnings(projectRoot, ignored, "run `codegrapher sync` to repair")
}

// EnsureDataDirIgnored repairs the cheap, local ways the data directory can
// end up visible to Git: it writes <dir>/.gitignore when missing and adds the
// <dir>/ entry to .git/info/exclude. It never edits a tracked .gitignore. The
// returned warnings describe what is still wrong (tracked data files, a repair
// that did not take effect); nil outside a Git repository.
func EnsureDataDirIgnored(projectRoot string) []string {
	ignored, isRepo := gitIgnoreState(projectRoot)
	if !isRepo {
		return nil
	}
	if !ignored {
		if fi, err := os.Stat(GetCodeGraphDir(projectRoot)); err == nil && fi.IsDir() {
			giPath := filepath.Join(GetCodeGraphDir(projectRoot), ".gitignore")
			if _, err := os.Stat(giPath); os.IsNotExist(err) {
				_ = os.WriteFile(giPath, []byte(dataDirGitignore), 0o644)
			}
		}
		_ = ensureDataDirectoryGitExclude(projectRoot)
		ignored, _ = gitIgnoreState(projectRoot)
	}
	return dataDirWarnings(projectRoot, ignored, "automatic repair failed; add it to .git/info/exclude")
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

package indexer

// Git sync hooks, ported from src/sync/git-hooks.ts.
//
// When the live file watcher is disabled (e.g. on WSL2 /mnt/* drives), the
// index would go stale until the user runs sync by hand. As an opt-in
// alternative, git hooks refresh the index after the operations that change
// files on disk: commit, merge (covers `git pull`), and checkout.
//
// The hooks run `codegraph sync` in the background so they never block git,
// and are guarded by `command -v codegraph` so they no-op cleanly when the
// CLI isn't on PATH. The snippet is delimited by marker comments so install
// is idempotent and removal preserves any user-authored hook content.

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	hookMarkerBegin = "# >>> codegraph sync hook >>>"
	hookMarkerEnd   = "# <<< codegraph sync hook <<<"
)

// GitHookName is a git hook the sync snippet can be installed into.
type GitHookName string

// The supported sync hooks.
const (
	HookPostCommit   GitHookName = "post-commit"
	HookPostMerge    GitHookName = "post-merge"
	HookPostCheckout GitHookName = "post-checkout"
)

// DefaultSyncHooks are installed by default: commit, merge (git pull), and
// checkout.
var DefaultSyncHooks = []GitHookName{HookPostCommit, HookPostMerge, HookPostCheckout}

// GitHookResult reports what an install/remove call did.
type GitHookResult struct {
	// Installed holds the hook names created, updated, or removed.
	Installed []GitHookName
	// HooksDir is the resolved hooks directory ("" when not a git repo).
	HooksDir string
	// Skipped explains why nothing happened (e.g. not a git repository).
	Skipped string
}

// IsGitRepo reports whether projectRoot is inside a git working tree.
// Returns false when git isn't installed or the path isn't a repo.
func IsGitRepo(projectRoot string) bool {
	out, err := gitOutput(projectRoot, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// gitHooksDir resolves the git hooks directory for a project, honoring
// core.hooksPath and git worktrees. Returns "" when not a repo.
func gitHooksDir(projectRoot string) string {
	out, err := gitOutput(projectRoot, "rev-parse", "--git-path", "hooks")
	if err != nil || out == "" {
		return ""
	}
	if filepath.IsAbs(out) {
		return out
	}
	abs, err := filepath.Abs(filepath.Join(projectRoot, out))
	if err != nil {
		return ""
	}
	return abs
}

// hookMarkerBlock is the shell snippet (between markers) injected into each
// hook — verbatim from markerBlock() in src/sync/git-hooks.ts.
func hookMarkerBlock() string {
	return strings.Join([]string{
		hookMarkerBegin,
		"# Keeps the CodeGraph index fresh while the live file watcher is off",
		"# (e.g. WSL2 /mnt drives). Runs in the background so it never blocks git.",
		"# Managed by codegraph; remove with `codegraph uninit` or delete this block.",
		"if command -v codegraph >/dev/null 2>&1; then",
		"  ( codegraph sync >/dev/null 2>&1 & ) >/dev/null 2>&1",
		"fi",
		hookMarkerEnd,
	}, "\n")
}

// stripHookMarkerBlock removes the marker block (and the marker lines) from
// hook content.
func stripHookMarkerBlock(content string) string {
	var kept []string
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == hookMarkerBegin {
			inBlock = true
			continue
		}
		if trimmed == hookMarkerEnd {
			inBlock = false
			continue
		}
		if !inBlock {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// isEffectivelyEmptyHook reports whether a hook body is just a shebang and
// blank lines (i.e. it only ever held our block).
func isEffectivelyEmptyHook(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#!") {
			return false
		}
	}
	return true
}

// trimTrailingSpace mirrors the original's .replace(/\s*$/, "").
func trimTrailingSpace(s string) string {
	return strings.TrimRight(s, " \t\r\n")
}

// InstallGitSyncHook installs (or updates) the CodeGraph sync snippet in the
// given git hooks. Idempotent: re-running replaces the marker block rather
// than duplicating it, and user-authored hook content is preserved.
func InstallGitSyncHook(projectRoot string, hooks []GitHookName) GitHookResult {
	if len(hooks) == 0 {
		hooks = DefaultSyncHooks
	}
	hooksDir := gitHooksDir(projectRoot)
	if hooksDir == "" {
		return GitHookResult{Skipped: "not a git repository"}
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return GitHookResult{HooksDir: hooksDir, Skipped: "could not access the git hooks directory"}
	}

	block := hookMarkerBlock()
	result := GitHookResult{HooksDir: hooksDir}

	for _, hook := range hooks {
		file := filepath.Join(hooksDir, string(hook))
		var content string
		if data, err := os.ReadFile(file); err == nil {
			base := trimTrailingSpace(stripHookMarkerBlock(string(data)))
			if base != "" {
				content = base + "\n\n" + block + "\n"
			} else {
				content = "#!/bin/sh\n" + block + "\n"
			}
		} else {
			content = "#!/bin/sh\n" + block + "\n"
		}
		if err := os.WriteFile(file, []byte(content), 0o755); err != nil {
			continue
		}
		_ = os.Chmod(file, 0o755) // no-op on platforms without chmod
		result.Installed = append(result.Installed, hook)
	}
	return result
}

// RemoveGitSyncHook removes the CodeGraph sync snippet from the given hooks.
// It strips only the marker block; the hook file is deleted entirely when
// nothing but a shebang remains, otherwise the user's content is rewritten
// untouched.
func RemoveGitSyncHook(projectRoot string, hooks []GitHookName) GitHookResult {
	if len(hooks) == 0 {
		hooks = DefaultSyncHooks
	}
	hooksDir := gitHooksDir(projectRoot)
	if hooksDir == "" {
		return GitHookResult{Skipped: "not a git repository"}
	}

	result := GitHookResult{HooksDir: hooksDir}
	for _, hook := range hooks {
		file := filepath.Join(hooksDir, string(hook))
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		original := string(data)
		if !strings.Contains(original, hookMarkerBegin) {
			continue
		}
		stripped := stripHookMarkerBlock(original)
		if isEffectivelyEmptyHook(stripped) {
			if err := os.Remove(file); err != nil {
				continue
			}
		} else {
			if err := os.WriteFile(file, []byte(trimTrailingSpace(stripped)+"\n"), 0o755); err != nil {
				continue
			}
			_ = os.Chmod(file, 0o755)
		}
		result.Installed = append(result.Installed, hook)
	}
	return result
}

// IsSyncHookInstalled reports whether any CodeGraph sync hook is currently
// installed.
func IsSyncHookInstalled(projectRoot string, hooks []GitHookName) bool {
	if len(hooks) == 0 {
		hooks = DefaultSyncHooks
	}
	hooksDir := gitHooksDir(projectRoot)
	if hooksDir == "" {
		return false
	}
	for _, hook := range hooks {
		data, err := os.ReadFile(filepath.Join(hooksDir, string(hook)))
		if err == nil && strings.Contains(string(data), hookMarkerBegin) {
			return true
		}
	}
	return false
}

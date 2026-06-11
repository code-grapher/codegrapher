package indexer

// Ported from __tests__/git-hooks.test.ts.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init")
	return dir
}

func readHook(t *testing.T, dir string, hook GitHookName) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", string(hook)))
	if err != nil {
		t.Fatalf("read hook %s: %v", hook, err)
	}
	return string(data)
}

func TestInstallGitSyncHookDefaults(t *testing.T) {
	repo := newGitRepo(t)

	res := InstallGitSyncHook(repo, nil)
	if res.Skipped != "" {
		t.Fatalf("skipped: %s", res.Skipped)
	}
	if len(res.Installed) != 3 {
		t.Fatalf("Installed = %v, want all 3 defaults", res.Installed)
	}

	for _, hook := range DefaultSyncHooks {
		body := readHook(t, repo, hook)
		if !strings.Contains(body, "codegraph sync") {
			t.Errorf("%s: missing sync invocation", hook)
		}
		if !strings.Contains(body, hookMarkerBegin) || !strings.Contains(body, hookMarkerEnd) {
			t.Errorf("%s: missing marker block", hook)
		}
		if !strings.HasPrefix(body, "#!/bin/sh\n") {
			t.Errorf("%s: missing shebang", hook)
		}
		if runtime.GOOS != "windows" {
			fi, err := os.Stat(filepath.Join(repo, ".git", "hooks", string(hook)))
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode()&0o111 == 0 {
				t.Errorf("%s: not executable", hook)
			}
		}
	}

	if !IsSyncHookInstalled(repo, nil) {
		t.Error("IsSyncHookInstalled = false after install")
	}
}

func TestInstallGitSyncHookIdempotent(t *testing.T) {
	repo := newGitRepo(t)
	InstallGitSyncHook(repo, nil)
	InstallGitSyncHook(repo, nil)

	body := readHook(t, repo, HookPostCommit)
	if n := strings.Count(body, hookMarkerBegin); n != 1 {
		t.Errorf("marker block appears %d times, want 1", n)
	}
}

func TestInstallGitSyncHookPreservesUserHook(t *testing.T) {
	repo := newGitRepo(t)
	userContent := "#!/bin/sh\necho user-hook\n"
	hookFile := filepath.Join(repo, ".git", "hooks", "post-commit")
	if err := os.MkdirAll(filepath.Dir(hookFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookFile, []byte(userContent), 0o755); err != nil {
		t.Fatal(err)
	}

	InstallGitSyncHook(repo, []GitHookName{HookPostCommit})
	body := readHook(t, repo, HookPostCommit)
	if !strings.Contains(body, "echo user-hook") {
		t.Error("user content lost on install")
	}
	if !strings.Contains(body, hookMarkerBegin) {
		t.Error("marker block not appended")
	}
	if !strings.HasPrefix(body, "#!/bin/sh") {
		t.Error("shebang lost")
	}
}

func TestRemoveGitSyncHookDeletesOursKeepsTheirs(t *testing.T) {
	repo := newGitRepo(t)
	InstallGitSyncHook(repo, nil)

	res := RemoveGitSyncHook(repo, nil)
	if len(res.Installed) != 3 {
		t.Fatalf("Removed = %v, want all 3", res.Installed)
	}
	// Hook files that were only ours are deleted entirely.
	for _, hook := range DefaultSyncHooks {
		if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", string(hook))); !os.IsNotExist(err) {
			t.Errorf("%s still exists after removal", hook)
		}
	}
	if IsSyncHookInstalled(repo, nil) {
		t.Error("IsSyncHookInstalled = true after removal")
	}
}

func TestRemoveGitSyncHookKeepsSharedHook(t *testing.T) {
	repo := newGitRepo(t)
	hookFile := filepath.Join(repo, ".git", "hooks", "post-merge")
	if err := os.MkdirAll(filepath.Dir(hookFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookFile, []byte("#!/bin/sh\necho keep-me\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	InstallGitSyncHook(repo, []GitHookName{HookPostMerge})
	RemoveGitSyncHook(repo, []GitHookName{HookPostMerge})

	body := readHook(t, repo, HookPostMerge)
	if !strings.Contains(body, "echo keep-me") {
		t.Error("user content lost on removal")
	}
	if strings.Contains(body, hookMarkerBegin) {
		t.Error("marker block still present after removal")
	}
}

func TestGitSyncHookHonorsCoreHooksPath(t *testing.T) {
	repo := newGitRepo(t)
	custom := filepath.Join(repo, "custom-hooks")
	mustGit(t, repo, "config", "core.hooksPath", "custom-hooks")

	res := InstallGitSyncHook(repo, []GitHookName{HookPostCommit})
	if res.Skipped != "" {
		t.Fatalf("skipped: %s", res.Skipped)
	}
	if _, err := os.Stat(filepath.Join(custom, "post-commit")); err != nil {
		t.Errorf("hook not installed under core.hooksPath: %v", err)
	}
}

func TestGitSyncHookSkipsNonRepo(t *testing.T) {
	dir := t.TempDir()
	res := InstallGitSyncHook(dir, nil)
	if res.Skipped != "not a git repository" {
		t.Errorf("Skipped = %q, want not-a-git-repository", res.Skipped)
	}
	if len(res.Installed) != 0 {
		t.Errorf("Installed = %v, want none", res.Installed)
	}
	if IsSyncHookInstalled(dir, nil) {
		t.Error("IsSyncHookInstalled = true outside a repo")
	}
	if rm := RemoveGitSyncHook(dir, nil); rm.Skipped != "not a git repository" {
		t.Errorf("remove Skipped = %q", rm.Skipped)
	}
}

func TestIsGitRepo(t *testing.T) {
	repo := newGitRepo(t)
	if !IsGitRepo(repo) {
		t.Error("IsGitRepo = false for a repo")
	}
	if IsGitRepo(t.TempDir()) {
		t.Error("IsGitRepo = true for a plain dir")
	}
}

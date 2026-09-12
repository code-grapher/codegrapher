package indexer

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// repoRootDir walks up from the package directory until it finds the
// directory containing testdata/golden — robust to where the package sits in
// the tree (worktree vs. merged location).
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "testdata", "golden")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("testdata/golden not found in any parent directory")
		}
		dir = parent
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanDirectoryWalkNonGit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "a.go"), "package a\n")
	writeFile(t, filepath.Join(dir, "src", "b.ts"), "export {}\n")
	writeFile(t, filepath.Join(dir, "README.md"), "# x\n")
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), "x\n")
	writeFile(t, filepath.Join(dir, "vendor", "v.go"), "package v\n")
	writeFile(t, filepath.Join(dir, "dist", "out.js"), "x\n")

	// Admission is decoupled from language detection: README.md is admitted as
	// a bare-node candidate; node_modules/dist/vendor stay default-ignored.
	got := ScanDirectory(dir)
	want := []string{"README.md", "src/a.go", "src/b.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanDirectory = %v, want %v", got, want)
	}
}

func TestScanDirectoryGitignoreNegationOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"), "!vendor/\nsecret/\n")
	writeFile(t, filepath.Join(dir, "vendor", "v.go"), "package v\n")
	writeFile(t, filepath.Join(dir, "secret", "s.go"), "package s\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n")

	// .gitignore itself is a non-gitignored file, so it is now admitted.
	got := ScanDirectory(dir)
	want := []string{".gitignore", "main.go", "vendor/v.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanDirectory = %v, want %v", got, want)
	}
}

func TestScanDirectoryNestedGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pkg", ".gitignore"), "gen.go\n")
	writeFile(t, filepath.Join(dir, "pkg", "gen.go"), "package p\n")
	writeFile(t, filepath.Join(dir, "pkg", "real.go"), "package p\n")

	// The nested .gitignore is itself admitted; gen.go stays ignored by it.
	got := ScanDirectory(dir)
	want := []string{"pkg/.gitignore", "pkg/real.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanDirectory = %v, want %v", got, want)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness#ac:watch-coverage-is-complete-or-start-fails
func TestPathFilterMatchesBuiltInAndNestedIgnoreRules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pkg", ".gitignore"), "generated/\n")
	filter := NewPathFilter(dir)

	for _, path := range []string{"node_modules/", "node_modules/dep/x.js", "pkg/generated/", "pkg/generated/x.go"} {
		if !filter.IsIgnored(path) {
			t.Errorf("IsIgnored(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"pkg/.gitignore", "pkg/real.go"} {
		if filter.IsIgnored(path) {
			t.Errorf("IsIgnored(%q) = true, want false", path)
		}
	}
}

func TestPathFilterReloadsChangedGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pkg", ".gitignore"), "old/\n")
	filter := NewPathFilter(dir)
	if !filter.IsIgnored("pkg/old/x.go") {
		t.Fatal("old rule was not applied")
	}
	writeFile(t, filepath.Join(dir, "pkg", ".gitignore"), "new/\n")
	if filter.IsIgnored("pkg/.gitignore") {
		t.Fatal(".gitignore event must remain admitted")
	}
	if !filter.IsIgnored("pkg/new/x.go") || filter.IsIgnored("pkg/old/x.go") {
		t.Fatal("nested matcher was not refreshed after .gitignore event")
	}
}

func TestGitPathFilterUsesExcludeStandardAndKeepsTrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if output, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	writeFile(t, filepath.Join(dir, "pkg", ".gitignore"), "tracked.go\n")
	writeFile(t, filepath.Join(dir, "pkg", "tracked.go"), "package pkg\n")
	if output, err := exec.Command("git", "-C", dir, "add", "pkg/.gitignore").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, output)
	}
	if output, err := exec.Command("git", "-C", dir, "add", "-f", "pkg/tracked.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, output)
	}
	writeFile(t, filepath.Join(dir, ".git", "info", "exclude"), "private/\n")
	writeFile(t, filepath.Join(dir, "private", "hidden.go"), "package private\n")
	filter := NewPathFilter(dir)

	if filter.IsIgnored("pkg/tracked.go") {
		t.Fatal("tracked file covered by nested .gitignore must remain admitted")
	}
	if !filter.IsIgnored("private/") || !filter.IsIgnored("private/hidden.go") {
		t.Fatal(".git/info/exclude was not honored")
	}
	if filter.IsIgnored("new-visible.go") {
		t.Fatal("unignored new path was rejected")
	}
}

func TestScanDirectorySkipsCodeGraphDataDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".codegraph", "x.go"), "package x\n")
	writeFile(t, filepath.Join(dir, ".codegraph-win", "y.go"), "package y\n")
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n")

	got := ScanDirectory(dir)
	want := []string{"a.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanDirectory = %v, want %v", got, want)
	}
}

func TestScanDirectoryGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "a.go"), "package a\n")
	writeFile(t, filepath.Join(dir, "ignored.go"), "package i\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "ignored.go\n")
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), "x\n")
	mustGit(t, dir, "init")
	mustGit(t, dir, "add", "-A")

	// Untracked file appears too.
	writeFile(t, filepath.Join(dir, "untracked.ts"), "export {}\n")

	// .gitignore is tracked and non-gitignored, so it is admitted too.
	got := ScanDirectory(dir)
	want := []string{".gitignore", "src/a.go", "untracked.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanDirectory = %v, want %v", got, want)
	}
}

// TestScanDirectoryGitignoredFileExcluded covers the whole-repo-file-nodes AC
// "gitignored file produces no node": admission is decoupled from language
// detection (a tracked unknown-language file is admitted), while a file matched
// by .gitignore is still excluded from the scan entirely.
func TestScanDirectoryGitignoredFileExcluded(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	// Tracked, non-gitignored, unknown-language file: must be admitted now that
	// admission is decoupled from DetectLanguage.
	writeFile(t, filepath.Join(dir, "notes.txt"), "hello\n")
	// File matched by .gitignore: must produce no node of any kind.
	writeFile(t, filepath.Join(dir, "secret.txt"), "ssh\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "secret.txt\n")
	mustGit(t, dir, "init")
	mustGit(t, dir, "add", "-A")

	got := ScanDirectory(dir)
	want := []string{".gitignore", "notes.txt"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanDirectory = %v, want %v", got, want)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestScanDirectoryFixture(t *testing.T) {
	root := repoRootDir(t)
	got := ScanDirectory(filepath.Join(root, "testdata", "fixtures", "go-small"))
	want := []string{"cmd/app/main.go", "go.mod", "internal/store/cache.go", "internal/store/store.go"}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanDirectory(go-small) = %v, want %v", got, want)
	}
}

func TestIsSourceFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"a.go", true}, {"a.ts", true}, {"a.tsx", true}, {"a.js", true}, {"a.jsx", true},
		{"a.py", true}, {"a.cs", true},
		{"a.py", true},
		{"A.java", true},
		{"A.kt", true}, {"build.gradle.kts", true},
		{"a.rb", true}, {"a.php", true},
		{"a.c", true}, {"a.h", true}, {"a.hpp", true},
		{"a.md", false}, {"Makefile", false},
	}
	for _, tc := range cases {
		if got := IsSourceFile(tc.path); got != tc.want {
			t.Errorf("IsSourceFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

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

	got := ScanDirectory(dir)
	want := []string{"src/a.go", "src/b.ts"}
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

	got := ScanDirectory(dir)
	want := []string{"main.go", "vendor/v.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanDirectory = %v, want %v", got, want)
	}
}

func TestScanDirectoryNestedGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pkg", ".gitignore"), "gen.go\n")
	writeFile(t, filepath.Join(dir, "pkg", "gen.go"), "package p\n")
	writeFile(t, filepath.Join(dir, "pkg", "real.go"), "package p\n")

	got := ScanDirectory(dir)
	want := []string{"pkg/real.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanDirectory = %v, want %v", got, want)
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

	got := ScanDirectory(dir)
	want := []string{"src/a.go", "untracked.ts"}
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
		{"a.md", false}, {"a.py", false}, {"Makefile", false},
	}
	for _, tc := range cases {
		if got := IsSourceFile(tc.path); got != tc.want {
			t.Errorf("IsSourceFile(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

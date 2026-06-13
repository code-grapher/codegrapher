package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodeGraphDirName(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"default", "", ".codegraph"},
		{"override", ".codegraph-win", ".codegraph-win"},
		{"plain name", "mygraph", "mygraph"},
		{"dot rejected", ".", ".codegraph"},
		{"traversal rejected", "..", ".codegraph"},
		{"slash rejected", "a/b", ".codegraph"},
		{"backslash rejected", `a\b`, ".codegraph"},
		{"absolute rejected", "/tmp/x", ".codegraph"},
		{"whitespace only", "   ", ".codegraph"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CODEGRAPH_DIR", tc.env)
			if got := CodeGraphDirName(); got != tc.want {
				t.Errorf("CodeGraphDirName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsCodeGraphDataDir(t *testing.T) {
	cases := []struct {
		name string
		env  string
		arg  string
		want bool
	}{
		{"default", "", ".codegraph", true},
		{"sibling", "", ".codegraph-win", true},
		{"override", "customdir", "customdir", true},
		{"other", "", "src", false},
		{"hidden other", "", ".git", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CODEGRAPH_DIR", tc.env)
			if got := IsCodeGraphDataDir(tc.arg); got != tc.want {
				t.Errorf("IsCodeGraphDataDir(%q) = %v, want %v", tc.arg, got, tc.want)
			}
		})
	}
}

// scopeDBFile returns a representative per-scope database path under dir's
// .codegraph directory, used to simulate an initialized project in tests.
func scopeDBFile(dir string) string {
	return filepath.Join(GetCodeGraphDir(dir), "codegraph-go-1.22.db")
}

func TestIsInitialized(t *testing.T) {
	dir := t.TempDir()
	if IsInitialized(dir) {
		t.Fatal("empty dir reported initialized")
	}
	// .codegraph alone is not enough — a per-scope db must exist too.
	if err := os.MkdirAll(GetCodeGraphDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if IsInitialized(dir) {
		t.Fatal(".codegraph without db reported initialized")
	}
	if err := os.WriteFile(scopeDBFile(dir), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsInitialized(dir) {
		t.Fatal(".codegraph with scope db not reported initialized")
	}
}

func TestFindNearestCodeGraphRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindNearestCodeGraphRoot(nested); got != "" {
		t.Fatalf("found root %q in uninitialized tree", got)
	}
	if err := os.MkdirAll(GetCodeGraphDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scopeDBFile(root), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := FindNearestCodeGraphRoot(nested)
	// Resolve symlinks on both sides (macOS /tmp is a symlink to /private/tmp).
	wantReal, _ := filepath.EvalSymlinks(root)
	gotReal, _ := filepath.EvalSymlinks(got)
	if gotReal != wantReal {
		t.Errorf("FindNearestCodeGraphRoot = %q, want %q", got, root)
	}
}

func TestCreateAndRemoveDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := CreateDirectory(dir); err != nil {
		t.Fatalf("CreateDirectory: %v", err)
	}
	gi := filepath.Join(GetCodeGraphDir(dir), ".gitignore")
	data, err := os.ReadFile(gi)
	if err != nil {
		t.Fatalf(".gitignore not written: %v", err)
	}
	if string(data) != dataDirGitignore {
		t.Errorf(".gitignore content mismatch")
	}

	// Idempotent while db doesn't exist.
	if err := CreateDirectory(dir); err != nil {
		t.Fatalf("CreateDirectory (second): %v", err)
	}

	// Errors once a per-scope db exists.
	if err := os.WriteFile(scopeDBFile(dir), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CreateDirectory(dir); err == nil {
		t.Fatal("CreateDirectory succeeded over an existing db")
	}

	if err := RemoveDirectory(dir); err != nil {
		t.Fatalf("RemoveDirectory: %v", err)
	}
	if _, err := os.Stat(GetCodeGraphDir(dir)); !os.IsNotExist(err) {
		t.Fatal(".codegraph still exists after RemoveDirectory")
	}
	// Removing again is a no-op.
	if err := RemoveDirectory(dir); err != nil {
		t.Fatalf("RemoveDirectory (absent): %v", err)
	}
}

func TestRemoveDirectorySymlink(t *testing.T) {
	target := t.TempDir()
	probe := filepath.Join(target, "probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	link := GetCodeGraphDir(project)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := RemoveDirectory(project); err != nil {
		t.Fatalf("RemoveDirectory: %v", err)
	}
	// The symlink itself is gone, but the target survives.
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("symlink still present")
	}
	if _, err := os.Stat(probe); err != nil {
		t.Fatal("symlink target was deleted — must never follow the link")
	}
}

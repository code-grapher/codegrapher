package indexer

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/scope"
	"github.com/specscore/codegrapher/store"
)

// initGoProject creates and indexes a tiny Go project, closes it, and
// returns the project root and its Go scope database path.
func initGoProject(t *testing.T) (root, dbPath string) {
	t.Helper()
	root = t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/p\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc A() {}\n")
	idx, _, err := Init(root, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(GetCodeGraphDir(root), "codegraph-go-*.db"))
	if len(matches) != 1 {
		t.Fatalf("expected one go scope db, got %v", matches)
	}
	return root, matches[0]
}

func rawExec(t *testing.T, dbPath, stmt string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(stmt); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenReadOnly_ReadsWithoutWriting(t *testing.T) {
	root, dbPath := initGoProject(t)
	before, _ := os.ReadFile(dbPath)

	idx, err := OpenReadOnly(root, Options{})
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = idx.Close() }()
	if !idx.Registry().ReadOnly() {
		t.Error("registry should be read-only")
	}
	if idx.UpgradeNeeded() != nil {
		t.Errorf("UpgradeNeeded = %v", idx.UpgradeNeeded())
	}
	if len(idx.Stores()) == 0 {
		t.Fatal("no stores")
	}
	_ = idx.GetChangedFiles()

	// A scope with no database cannot be created through a read-only registry.
	if _, err := idx.Registry().Store(scope.Scope{Language: model.LangPython, Version: "9"}); err == nil {
		t.Error("read-only registry created a scope database")
	}
	_ = idx.Close()
	after, _ := os.ReadFile(dbPath)
	if string(before) != string(after) {
		t.Error("read-only open modified the database")
	}
}

func TestOpenReadOnly_Uninitialized(t *testing.T) {
	if _, err := OpenReadOnly(t.TempDir(), Options{}); err == nil {
		t.Error("expected error for an uninitialized project")
	}
}

func TestOpenReadOnly_StaleSchema(t *testing.T) {
	root, dbPath := initGoProject(t)
	rawExec(t, dbPath, "DELETE FROM schema_versions WHERE version >= 9")

	_, err := OpenReadOnly(root, Options{})
	if !errors.Is(err, store.ErrNeedsUpgrade) {
		t.Fatalf("err = %v, want ErrNeedsUpgrade", err)
	}

	idx, err := OpenReadOnly(root, Options{AllowStale: true})
	if err != nil {
		t.Fatalf("AllowStale: %v", err)
	}
	if !errors.Is(idx.UpgradeNeeded(), store.ErrNeedsUpgrade) {
		t.Errorf("UpgradeNeeded = %v", idx.UpgradeNeeded())
	}
	if len(idx.Stores()) == 0 {
		t.Error("stale store should be readable for diagnostics")
	}
	_ = idx.Close()

	// Re-stamping the version makes the index current again.
	rawExec(t, dbPath, "INSERT INTO schema_versions (version, applied_at, description) VALUES (9, 0, 'x')")
	ro, err := OpenReadOnly(root, Options{})
	if err != nil {
		t.Fatalf("after upgrade: %v", err)
	}
	_ = ro.Close()
}

func TestOpenReadOnly_LegacyWAL(t *testing.T) {
	root, dbPath := initGoProject(t)
	rawExec(t, dbPath, "PRAGMA journal_mode=WAL")

	if _, err := OpenReadOnly(root, Options{}); !errors.Is(err, store.ErrNeedsUpgrade) {
		t.Fatalf("err = %v, want ErrNeedsUpgrade", err)
	}
	idx, err := OpenReadOnly(root, Options{AllowStale: true})
	if err != nil {
		t.Fatalf("AllowStale: %v", err)
	}
	if !errors.Is(idx.UpgradeNeeded(), store.ErrNeedsUpgrade) {
		t.Errorf("UpgradeNeeded = %v", idx.UpgradeNeeded())
	}
	for _, st := range idx.Stores() {
		if st.Path() == dbPath {
			t.Error("a WAL database must be skipped, not opened")
		}
	}
	_ = idx.Close()
	if _, err := os.Stat(dbPath + "-shm"); err == nil {
		t.Error("-shm created by a read-only open")
	}

	rw, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = rw.Close()
	ro, err := OpenReadOnly(root, Options{})
	if err != nil {
		t.Fatalf("after upgrade: %v", err)
	}
	_ = ro.Close()
}

func TestOpenReadOnly_CorruptDatabaseStillFailsWithAllowStale(t *testing.T) {
	root := t.TempDir()
	if err := CreateDirectory(root); err != nil {
		t.Fatal(err)
	}
	bad := ScopedDatabasePath(root, scope.Scope{Language: model.LangGo, Version: "1.22"})
	if err := os.WriteFile(bad, []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReadOnly(root, Options{AllowStale: true}); err == nil || errors.Is(err, store.ErrNeedsUpgrade) {
		t.Errorf("err = %v, want a non-upgrade error", err)
	}
}

func TestRegistryUpgradeNeededNilWhenCurrent(t *testing.T) {
	root := t.TempDir()
	if err := CreateDirectory(root); err != nil {
		t.Fatal(err)
	}
	reg, err := OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Close() }()
	if reg.ReadOnly() || reg.UpgradeNeeded() != nil {
		t.Error("read-write registry: ReadOnly/UpgradeNeeded should be false/nil")
	}
}

// ---- Git ignore check and repair ----

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	return dir
}

func TestEnsureDataDirIgnored_RepairsMissingIgnores(t *testing.T) {
	dir := gitRepo(t)
	if err := os.MkdirAll(GetCodeGraphDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if w := CheckDataDirIgnored(dir); len(w) != 1 || !strings.Contains(w[0], "not ignored") {
		t.Fatalf("Check warnings = %v", w)
	}
	// Check never writes.
	if _, err := os.Stat(filepath.Join(GetCodeGraphDir(dir), ".gitignore")); err == nil {
		t.Fatal("CheckDataDirIgnored wrote .gitignore")
	}

	if w := EnsureDataDirIgnored(dir); len(w) != 0 {
		t.Fatalf("Ensure warnings = %v", w)
	}
	if _, err := os.Stat(filepath.Join(GetCodeGraphDir(dir), ".gitignore")); err != nil {
		t.Errorf(".gitignore not written: %v", err)
	}
	exclude, _ := os.ReadFile(gitExcludePath(t, dir))
	if !strings.Contains(string(exclude), ".codegraph/") {
		t.Errorf("info/exclude lacks entry: %q", exclude)
	}
	if w := CheckDataDirIgnored(dir); len(w) != 0 {
		t.Errorf("Check after repair = %v", w)
	}
	// Second run is a no-op.
	if w := EnsureDataDirIgnored(dir); len(w) != 0 {
		t.Errorf("idempotent Ensure warnings = %v", w)
	}
}

func TestEnsureDataDirIgnored_NeverTouchesTrackedGitignore(t *testing.T) {
	dir := gitRepo(t)
	tracked := filepath.Join(dir, ".gitignore")
	writeFile(t, tracked, "node_modules/\n")
	mustGit(t, dir, "add", ".gitignore")
	mustGit(t, dir, "commit", "-q", "-m", "gitignore")

	_ = os.MkdirAll(GetCodeGraphDir(dir), 0o755)
	if w := EnsureDataDirIgnored(dir); len(w) != 0 {
		t.Fatalf("warnings = %v", w)
	}
	got, _ := os.ReadFile(tracked)
	if string(got) != "node_modules/\n" {
		t.Errorf("tracked .gitignore was edited: %q", got)
	}
}

func TestEnsureDataDirIgnored_WarnsWhenTracked(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, filepath.Join(GetCodeGraphDir(dir), "codegraph.db"), "x")
	mustGit(t, dir, "add", "-f", ".codegraph/codegraph.db")
	mustGit(t, dir, "commit", "-q", "-m", "oops")

	w := EnsureDataDirIgnored(dir)
	if len(w) != 1 || !strings.Contains(w[0], "tracked by Git") || !strings.Contains(w[0], "git rm -r --cached") {
		t.Fatalf("warnings = %v", w)
	}
	// The directory's own .gitignore may be committed without a warning.
	mustGit(t, dir, "rm", "-q", "--cached", ".codegraph/codegraph.db")
	writeFile(t, filepath.Join(GetCodeGraphDir(dir), ".gitignore"), dataDirGitignore)
	mustGit(t, dir, "add", "-f", ".codegraph/.gitignore")
	if w := CheckDataDirIgnored(dir); len(w) != 0 {
		t.Errorf("tracked .codegraph/.gitignore warned: %v", w)
	}
}

func TestEnsureDataDirIgnored_NonGitIsSilent(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(GetCodeGraphDir(dir), 0o755)
	if w := EnsureDataDirIgnored(dir); w != nil {
		t.Errorf("Ensure = %v", w)
	}
	if w := CheckDataDirIgnored(dir); w != nil {
		t.Errorf("Check = %v", w)
	}
	if got := trackedDataFiles(dir); got != nil {
		t.Errorf("trackedDataFiles = %v", got)
	}
}

func TestEnsureDataDirIgnored_RepairFailureWarns(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := gitRepo(t)
	// An existing but read-only exclude file: the repair cannot write it.
	exclude := gitExcludePath(t, dir)
	if err := os.WriteFile(exclude, []byte("# none\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(exclude, 0o444); err != nil {
		t.Fatal(err)
	}
	w := EnsureDataDirIgnored(dir)
	if len(w) != 1 || !strings.Contains(w[0], "automatic repair failed") {
		t.Errorf("warnings = %v", w)
	}
}

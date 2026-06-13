package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/scope"
)

func TestScopedDatabasePath(t *testing.T) {
	root := "/proj"
	sc := scope.Scope{Language: model.LangGo, Version: "1.22"}
	want := filepath.Join(root, CodeGraphDirName(), "codegraph-go-1.22.db")
	if got := ScopedDatabasePath(root, sc); got != want {
		t.Errorf("ScopedDatabasePath() = %q, want %q", got, want)
	}
}

func TestParseScopeFromDBName(t *testing.T) {
	cases := []struct {
		name   string
		want   scope.Scope
		wantOK bool
	}{
		{"codegraph-go-1.22.db", scope.Scope{Language: model.LangGo, Version: "1.22"}, true},
		{"codegraph-typescript-5.4.0-beta.db", scope.Scope{Language: model.LangTypeScript, Version: "5.4.0-beta"}, true},
		{"codegraph-yaml-v0.db", scope.Scope{Language: model.LangYAML, Version: "v0"}, true},
		{"codegraph.db", scope.Scope{}, false},
		{"codegraph-go.db", scope.Scope{}, false},
		{"notes.txt", scope.Scope{}, false},
	}
	for _, tc := range cases {
		got, ok := parseScopeFromDBName(tc.name)
		if ok != tc.wantOK || (ok && got != tc.want) {
			t.Errorf("parseScopeFromDBName(%q) = (%+v, %v), want (%+v, %v)", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestRegistryStoreGetOrCreate(t *testing.T) {
	root := t.TempDir()
	if err := CreateDirectory(root); err != nil {
		t.Fatal(err)
	}
	reg, err := OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	sc := scope.Scope{Language: model.LangGo, Version: "1.22"}
	s1, err := reg.Store(sc)
	if err != nil {
		t.Fatal(err)
	}
	// The DB file is created on first request.
	if _, err := os.Stat(ScopedDatabasePath(root, sc)); err != nil {
		t.Fatalf("expected DB file created: %v", err)
	}
	// Second request returns the same handle.
	s2, err := reg.Store(sc)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Error("expected cached store handle on repeat request")
	}
}

func TestRegistryStores(t *testing.T) {
	root := t.TempDir()
	if err := CreateDirectory(root); err != nil {
		t.Fatal(err)
	}
	reg, err := OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	sc := scope.Scope{Language: model.LangGo, Version: "1.22"}
	s, err := reg.Store(sc)
	if err != nil {
		t.Fatal(err)
	}
	stores := reg.Stores()
	if stores[sc] != s {
		t.Errorf("Stores()[%v] = %v, want %v", sc, stores[sc], s)
	}
}

func TestOpenRegistryIgnoresNonScopeFiles(t *testing.T) {
	root := t.TempDir()
	if err := CreateDirectory(root); err != nil {
		t.Fatal(err)
	}
	dir := GetCodeGraphDir(root)
	for _, name := range []string{"codegraph.db", "notes.txt", "codegraph-go.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if got := reg.Scopes(); len(got) != 0 {
		t.Errorf("Scopes() = %+v, want none", got)
	}
}

func TestOpenRegistryErrorsOnCorruptScopeDB(t *testing.T) {
	root := t.TempDir()
	if err := CreateDirectory(root); err != nil {
		t.Fatal(err)
	}
	bad := ScopedDatabasePath(root, scope.Scope{Language: model.LangGo, Version: "1.22"})
	if err := os.WriteFile(bad, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRegistry(root); err == nil {
		t.Error("expected error opening corrupt scope DB, got nil")
	}
}

func TestRegistryEnumeratesExistingScopes(t *testing.T) {
	root := t.TempDir()
	if err := CreateDirectory(root); err != nil {
		t.Fatal(err)
	}
	want := []scope.Scope{
		{Language: model.LangGo, Version: "1.22"},
		{Language: model.LangTypeScript, Version: "5.4.0"},
	}

	reg, err := OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, sc := range want {
		if _, err := reg.Store(sc); err != nil {
			t.Fatal(err)
		}
	}
	reg.Close()

	// Re-open: the registry discovers both DBs on disk.
	reg2, err := OpenRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reg2.Close()

	got := reg2.Scopes()
	if len(got) != len(want) {
		t.Fatalf("Scopes() returned %d, want %d: %+v", len(got), len(want), got)
	}
	seen := map[scope.Scope]bool{}
	for _, sc := range got {
		seen[sc] = true
	}
	for _, sc := range want {
		if !seen[sc] {
			t.Errorf("missing scope %+v in %+v", sc, got)
		}
	}
}

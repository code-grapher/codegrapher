package snapshot

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/scope"
	"github.com/specscore/codegrapher/store"
)

func mustStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestExportScopedEmpty(t *testing.T) {
	out := t.TempDir()
	m, sizes, err := ExportScoped(map[scope.Scope]*store.Store{}, out, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Scopes) != 0 || len(sizes) != 0 {
		t.Errorf("expected empty manifest, got %+v", m)
	}
	if _, err := os.Stat(filepath.Join(out, "manifest.json")); err != nil {
		t.Errorf("manifest.json should still be written: %v", err)
	}
}

func TestExportScopedMkdirError(t *testing.T) {
	// baseOutDir under a regular file cannot be created.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExportScoped(map[scope.Scope]*store.Store{}, filepath.Join(f, "sub"), "main"); err == nil {
		t.Error("expected mkdir error")
	}
}

func TestExportScoped(t *testing.T) {
	goStore := mustStore(t)
	if err := goStore.UpsertFile(model.FileRecord{Path: "main.go", Language: model.LangGo}); err != nil {
		t.Fatal(err)
	}
	if err := goStore.InsertNode(model.Node{
		ID: "n1", Kind: model.KindFunction, Name: "Main", FilePath: "main.go", Language: model.LangGo,
	}); err != nil {
		t.Fatal(err)
	}

	tsStore := mustStore(t)
	if err := tsStore.UpsertFile(model.FileRecord{Path: "app.ts", Language: model.LangTypeScript}); err != nil {
		t.Fatal(err)
	}

	stores := map[scope.Scope]*store.Store{
		{Language: model.LangGo, Version: "1.22"}:        goStore,
		{Language: model.LangTypeScript, Version: "5.4"}: tsStore,
	}
	out := t.TempDir()

	m, sizes, err := ExportScoped(stores, out, "main")
	if err != nil {
		t.Fatal(err)
	}

	if m.Ref != "main" {
		t.Errorf("ref = %q, want main", m.Ref)
	}
	if len(m.Scopes) != 2 {
		t.Fatalf("scopes = %d, want 2", len(m.Scopes))
	}
	// Sorted by key: go-1.22 before typescript-5.4.
	if m.Scopes[0].Key != "go-1.22" || m.Scopes[1].Key != "typescript-5.4" {
		t.Errorf("scope keys = %q,%q", m.Scopes[0].Key, m.Scopes[1].Key)
	}
	if m.Scopes[0].Counts.Nodes != 1 || m.Scopes[0].Counts.Files != 1 {
		t.Errorf("go counts = %+v, want nodes=1 files=1", m.Scopes[0].Counts)
	}

	// Compressed-only, flat layout: variants exist directly under the scope
	// dir; neither the plain .ingr nor the nested collection dir remain.
	base := filepath.Join(out, "go", "1.22", "nodes.ingr")
	for _, ext := range []string{".zst", ".gz"} {
		if _, err := os.Stat(base + ext); err != nil {
			t.Errorf("missing %s: %v", base+ext, err)
		}
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Errorf("plain .ingr should be removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "go", "1.22", "nodes")); !os.IsNotExist(err) {
		t.Errorf("nested collection dir should be removed: %v", err)
	}

	// The gzip variant decompresses to non-empty INGR text.
	gzData, err := os.ReadFile(base + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(gzData))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(r)
	if err != nil || len(plain) == 0 {
		t.Errorf("empty/invalid gzip payload: %v", err)
	}

	// manifest.json on disk matches the returned manifest.
	mf, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk Manifest
	if err := json.Unmarshal(mf, &onDisk); err != nil {
		t.Fatal(err)
	}
	if len(onDisk.Scopes) != 2 || onDisk.Ref != "main" {
		t.Errorf("manifest.json mismatch: %+v", onDisk)
	}

	if len(sizes) == 0 {
		t.Error("expected per-recordset sizes")
	}
	for _, s := range sizes {
		if s.Original == 0 || s.Zstd == 0 || s.Gzip == 0 {
			t.Errorf("zero size in report: %+v", s)
		}
	}
}

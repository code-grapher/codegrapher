package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoModFoldsIntoGoScope(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/proj\n\ngo 1.22\n\nrequire github.com/spf13/cobra v1.10.2\n")
	write("main.go", "package main\n\nfunc main() {}\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !hasNodeNamed(t, idx, "example.com/proj") {
		t.Error("module node not found in any store")
	}
	scoped := idx.StoresFiltered([]string{"go-v1"})
	if len(scoped) != 1 {
		t.Fatalf("expected exactly one go-v1 store, got %d", len(scoped))
	}
}

func TestPackageJSONFoldsIntoNodeScope(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"name":"demo","dependencies":{"left-pad":"^1.3.0"}}`)
	write("index.js", "module.exports = 1;\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// package.json (no typescript dep / engines) buckets to v0 -> node-v0.
	scoped := idx.StoresFiltered([]string{"node-v0"})
	if len(scoped) != 1 {
		t.Fatalf("expected exactly one node-v0 store, got %d", len(scoped))
	}
	// The module node lives in the node-v0 scope, not the default Store()
	// (index.js stays in its own javascript-v0 scope, which sorts first).
	nodes, err := scoped[0].GetNodesByName("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Error("package.json module node not found in node-v0 store")
	}
}

func TestInitIndexesProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() { helper() }\n\nfunc helper() {}\n")

	idx, res, err := Init(dir, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer idx.Close()

	if !IsInitialized(dir) {
		t.Fatal("project not initialized after Init")
	}
	if !res.Success {
		t.Fatalf("IndexResult not successful: %+v", res)
	}
	if res.FilesIndexed != 1 {
		t.Errorf("FilesIndexed = %d, want 1", res.FilesIndexed)
	}
	if res.NodesCreated == 0 {
		t.Error("NodesCreated = 0, want > 0")
	}

	files, err := idx.Store().GetAllFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "main.go" {
		t.Errorf("files = %+v, want one main.go record", files)
	}
	nodes, err := idx.Store().GetNodesByName("helper")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Errorf("helper nodes = %d, want 1", len(nodes))
	}

	// Resolution ran and cleared the unresolved table.
	n, err := idx.Store().GetUnresolvedReferencesCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("unresolved refs after Init = %d, want 0", n)
	}

	// Metadata stamped.
	v, err := idx.Store().GetMetadata("indexed_with_version")
	if err != nil || v != PackageVersion {
		t.Errorf("indexed_with_version = %q (%v), want %q", v, err, PackageVersion)
	}
	ev, err := idx.Store().GetMetadata("indexed_with_extraction_version")
	if err != nil || ev != "14" {
		t.Errorf("indexed_with_extraction_version = %q (%v), want \"14\"", ev, err)
	}
}

func TestInitTwiceFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer idx.Close()

	if _, _, err := Init(dir, Options{}); err == nil ||
		!strings.Contains(err.Error(), "already initialized") {
		t.Fatalf("second Init err = %v, want already-initialized error", err)
	}
}

func TestUninit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := idx.Uninit(); err != nil {
		t.Fatalf("Uninit: %v", err)
	}
	if IsInitialized(dir) {
		t.Fatal("still initialized after Uninit")
	}
	if _, err := os.Stat(GetCodeGraphDir(dir)); !os.IsNotExist(err) {
		t.Fatal(".codegraph still present after Uninit")
	}
	// Source files are untouched.
	if _, err := os.Stat(filepath.Join(dir, "a.go")); err != nil {
		t.Fatal("source file removed by Uninit")
	}
}

func TestIndexAllLockConflict(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package a\nfunc A() {}\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer idx.Close()

	// Simulate another live process holding the lock.
	other, err := Open(dir, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer other.Close()
	if err := other.lock.Acquire(); err != nil {
		t.Fatalf("acquire lock: %v", err)
	}

	res := idx.IndexAll(Options{})
	if res.Success {
		t.Fatal("IndexAll succeeded despite held lock")
	}
	if len(res.Errors) != 1 || res.Errors[0].Message != lockUnavailableMessage {
		t.Errorf("errors = %+v, want single lock-unavailable error", res.Errors)
	}
}

func TestOpenRequiresInit(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir, Options{}); err == nil {
		t.Fatal("Open succeeded on uninitialized dir")
	}
}

func TestInitProgressCallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package a\nfunc A() {}\n")

	var phases []Phase
	idx, _, err := Init(dir, Options{OnProgress: func(p IndexProgress) {
		if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
			phases = append(phases, p.Phase)
		}
	}})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer idx.Close()

	want := []Phase{PhaseScanning, PhaseParsing, PhaseResolving}
	if len(phases) != len(want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phases = %v, want %v", phases, want)
		}
	}
}

func TestStoresFilteredMergesNodeScope(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// package.json (no typescript dep) -> node-v0; a .ts file -> typescript-v0.
	write("package.json", `{"name":"demo","dependencies":{"left-pad":"^1.3.0"}}`)
	write("main.ts", "export const x = 1;\n")

	idx, _, err := Init(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// Requesting only the typescript scope must auto-include node-v0 (the merge).
	got := idx.StoresFiltered([]string{"typescript-v0"})
	if len(got) != 2 {
		t.Fatalf("typescript-v0 filter returned %d stores, want 2 (typescript-v0 + node-v0)", len(got))
	}
	// Requesting node alone returns just node.
	if n := len(idx.StoresFiltered([]string{"node-v0"})); n != 1 {
		t.Errorf("node-v0 filter returned %d stores, want 1", n)
	}
}

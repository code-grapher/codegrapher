package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specscore/codegrapher/coverage"
	"github.com/specscore/codegrapher/indexer"
)

// newContextFixture builds a small real Go module — two functions sharing a
// parameter type, a receiver method with an interface-seam callee, a
// package-level test with a helper and a fake — and indexes it for real
// (through the actual extractor), so context's graph queries exercise real
// edges rather than hand-built rows.
func newContextFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGitForWatchTest(t, root, "init", "-q")
	runGitForWatchTest(t, root, "config", "user.email", "codegrapher-test@example.invalid")
	runGitForWatchTest(t, root, "config", "user.name", "CodeGrapher Test")

	mustWrite := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(root, "go.mod"), "module example.com/fx\n\ngo 1.22\n")
	mustWrite(filepath.Join(root, "widget.go"), `package fx

// Store is an interface seam for persistence.
type Store interface {
	Save(id string) error
}

// Widget holds a name and a store.
type Widget struct {
	Name  string
	store Store
}

// NewWidget builds a ready Widget.
func NewWidget(name string, s Store) *Widget {
	return &Widget{Name: name, store: s}
}

// Rename changes the widget's name and persists it through the seam.
func Rename(w *Widget, newName string) error {
	w.Name = newName
	return w.store.Save(newName)
}

// Describe reads the widget's name; shares the *Widget parameter type with
// Rename so a bundle over both symbols must dedup the Widget declaration.
func Describe(w *Widget) string {
	return w.Name
}
`)
	mustWrite(filepath.Join(root, "widget_test.go"), `package fx

import "testing"

func TestRename(t *testing.T) {
	w := NewWidget("a", fakeStore{})
	if err := Rename(w, "b"); err != nil {
		t.Fatal(err)
	}
}

// newFakeWidget is a package test helper, not a Test entrypoint.
func newFakeWidget() *Widget {
	return NewWidget("fake", fakeStore{})
}

type fakeStore struct{}

func (fakeStore) Save(id string) error { return nil }
`)
	runGitForWatchTest(t, root, "add", ".")
	runGitForWatchTest(t, root, "commit", "-qm", "initial")

	idx, _, err := indexer.Init(root, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func runContext(t *testing.T, root string, args ...string) ContextResult {
	t.Helper()
	cmd := newContextCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	fullArgs := append(append([]string{}, args...), "--format", "json", "-p", root)
	cmd.SetArgs(fullArgs)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("context command failed: %v\n%s", err, out.String())
	}
	var result ContextResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode context JSON: %v\n%s", err, out.String())
	}
	return result
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:dedup-shared-type-across-two-symbols
func TestContextDedupsSharedTypeAcrossSymbols(t *testing.T) {
	root := newContextFixture(t)
	result := runContext(t, root, "Rename", "Describe")

	if len(result.Sources) != 2 {
		t.Fatalf("sources = %d, want 2 (one per requested symbol): %+v", len(result.Sources), result.Sources)
	}
	widgetCount := 0
	for _, ty := range result.Types {
		if ty.Symbol.Name == "Widget" {
			widgetCount++
		}
	}
	if widgetCount != 1 {
		t.Fatalf("Widget declaration appeared %d times, want exactly 1 (deduplicated across both symbols): %+v", widgetCount, result.Types)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:budget-cutoff-lists-omitted
func TestContextBudgetCutoffListsOmitted(t *testing.T) {
	root := newContextFixture(t)
	result := runContext(t, root, "Rename", "--budget", "40")

	if len(result.Omitted) == 0 {
		t.Fatalf("expected an omitted list when the budget is tiny, got none: %+v", result)
	}
	// The source of the one requested symbol is always kept, even over
	// budget, so the bundle is never empty.
	if len(result.Sources) != 1 {
		t.Fatalf("sources = %d, want 1 (kept even though the budget was exceeded)", len(result.Sources))
	}
	// Something from a later section (types/callees/tests) must have been
	// pushed into the omitted list rather than silently dropped.
	found := false
	for _, o := range result.Omitted {
		if strings.HasPrefix(o, "b:") || strings.HasPrefix(o, "c:") || strings.HasPrefix(o, "d:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("omitted list has no b/c/d entries: %v", result.Omitted)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:seam-flagged-callee
func TestContextFlagsInterfaceSeamCallee(t *testing.T) {
	root := newContextFixture(t)
	result := runContext(t, root, "Rename")

	var saveCallee *ContextCallee
	for i := range result.Callees {
		if result.Callees[i].Symbol.QualifiedName == "Store::Save" {
			saveCallee = &result.Callees[i]
		}
	}
	if saveCallee == nil {
		t.Fatalf("Store::Save not found in callees: %+v", result.Callees)
	}
	if saveCallee.Seam != "interface" {
		t.Fatalf("Store::Save seam = %q, want %q", saveCallee.Seam, "interface")
	}
	if saveCallee.Symbol.Signature == "" {
		t.Fatalf("callee signature is empty; section c must be signatures only, not blank")
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:uncovered-lines-marked-from-ingested-profile
func TestContextMarksUncoveredLinesFromIngestedProfile(t *testing.T) {
	root := newContextFixture(t)

	idx, err := indexer.Open(root, indexer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	profile := "mode: set\n" +
		"example.com/fx/widget.go:26.1,27.24 1 1\n" + // "w.Name = newName" hit
		"example.com/fx/widget.go:28.1,28.25 1 0\n" // "return w.store.Save(...)" missed
	for _, s := range idx.Stores() {
		if _, err := coverage.NewIngestor().Ingest(context.Background(), s, strings.NewReader(profile), coverage.Options{Root: root}); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	result := runContext(t, root, "Rename", "--uncovered")
	if len(result.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(result.Sources))
	}
	src := result.Sources[0].Source
	lines := strings.Split(src, "\n")
	// Rename spans widget.go:26-29 (func line, w.Name=, return, closing
	// brace); line 28 is the "return ... Save(...)" miss.
	for i, line := range lines {
		lineNo := result.Sources[0].Symbol.StartLine + i
		wantUncovered := lineNo == 28
		gotUncovered := strings.Contains(line, "// UNCOVERED")
		if gotUncovered != wantUncovered {
			t.Fatalf("line %d = %q, uncovered-marked=%v, want %v\nfull source:\n%s", lineNo, line, gotUncovered, wantUncovered, src)
		}
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:for-test-lists-package-helpers
func TestContextForTestListsPackageHelpers(t *testing.T) {
	root := newContextFixture(t)
	result := runContext(t, root, "Rename", "--for", "test")

	names := map[string]bool{}
	for _, h := range result.Helpers {
		names[h.Symbol.Name] = true
	}
	if !names["newFakeWidget"] {
		t.Fatalf("helper newFakeWidget not listed: %+v", result.Helpers)
	}
	if !names["fakeStore"] {
		t.Fatalf("fake type fakeStore not listed: %+v", result.Helpers)
	}
	if names["TestRename"] {
		t.Fatalf("Test entrypoint TestRename must not be listed as a helper: %+v", result.Helpers)
	}

	// Without --for test, section e must be empty.
	plain := runContext(t, root, "Rename")
	if len(plain.Helpers) != 0 {
		t.Fatalf("helpers present without --for test: %+v", plain.Helpers)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:unknown-symbol-reported-without-blocking-others
func TestContextUnknownSymbolDoesNotBlockOthers(t *testing.T) {
	root := newContextFixture(t)
	result := runContext(t, root, "Rename", "NoSuchSymbolXYZ")

	if len(result.Unresolved) != 1 || result.Unresolved[0].Requested != "NoSuchSymbolXYZ" {
		t.Fatalf("unresolved = %+v, want exactly one not_found entry for NoSuchSymbolXYZ", result.Unresolved)
	}
	if result.Unresolved[0].Status != "not_found" {
		t.Fatalf("status = %q, want not_found", result.Unresolved[0].Status)
	}
	if len(result.Sources) != 1 || result.Sources[0].Symbol.Name != "Rename" {
		t.Fatalf("Rename's bundle was not returned alongside the unresolved symbol: %+v", result.Sources)
	}
}

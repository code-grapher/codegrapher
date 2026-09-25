package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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

// Notifier is a seam interface used as a struct field's type on Recorder,
// below — separate from Store/Widget above so nothing before this point
// shifts line numbers (TestContextMarksUncoveredLinesFromIngestedProfile
// hardcodes Rename's line numbers).
type Notifier interface {
	Notify(msg string)
}

// Recorder holds a label and a notifier field; NewRecorder is its
// constructor, matched by findConstructors' New<T> name rule.
type Recorder struct {
	Label  string
	notify Notifier
}

// NewRecorder builds a ready Recorder.
func NewRecorder(label string, n Notifier) *Recorder {
	return &Recorder{Label: label, notify: n}
}

// notify is a real package-level function that happens to share its name
// with Recorder's own "notify" field (of interface type Notifier).
// context's call-graph resolver matches calls by name, not by static type,
// so a call resolving to this function could really be going through the
// field at runtime — the field-seam heuristic flags it as such rather than
// asserting it as a graph fact.
func notify() {}

// retry is a real package-level function that happens to share its name
// with Record's own func-typed parameter below, for the same reason: a
// call resolving to it could really be invoking the parameter.
func retry() error { return nil }

// Record is a method (real receiver) that reads its own receiver field
// (notify) through an interface-typed field, and takes a func-typed
// parameter — exercising receiver/constructor/field-type roles (section b)
// and field/parameter seam flags (section c), none of which the
// plain-function symbols above ever touch (they have no receiver).
func (r *Recorder) Record(retry func() error) error {
	r.notify.Notify(r.Label)
	notify()
	return retry()
}

// RenameViaHelper is production code that calls Rename — used to exercise
// F1's opt-in --test-hops 2: TestRenameViaHelperIndirect (widget_test.go)
// calls this, not Rename, directly.
func RenameViaHelper(w *Widget, newName string) error {
	return Rename(w, newName)
}

// ForwardsToHelper is a thin wrapper: its body is a single return'd call.
// context must surface computeReal's full source too, as if it had been
// requested (E1) — a 1-line forwarding wrapper tells a test writer nothing
// about the real logic living in the function it forwards to.
func ForwardsToHelper() int {
	return computeReal()
}

// computeReal is ForwardsToHelper's real logic.
func computeReal() int {
	return 42
}

// retryForClosure is a leaf function called only from WithClosure's nested
// literal below, so its call site is a unique, greppable marker a test can
// locate without hardcoding a line number.
func retryForClosure() error { return nil }

// WithClosure has a nested anonymous closure with no name of its own — the
// coverage/worklist file:line an agent has for it can only resolve through
// A1 (context --file/--line narrows to the innermost function literal).
func WithClosure() error {
	run := func() error {
		return retryForClosure()
	}
	return run()
}
`)
	mustWrite(filepath.Join(root, "widget_test.go"), `package fx

import "testing"

func TestRename(t *testing.T) {
	w := NewWidget("a", fakeStore{})
	if err := Rename(w, "b"); err != nil {
		t.Fatal(err)
	}
	assertWidgetName(t, w, "b")
}

// TestRenameViaHelperIndirect never calls Rename directly, only through the
// production helper RenameViaHelper (widget.go) — exercises F1's opt-in
// --test-hops 2 (default context Rename must NOT list this test; --test-hops
// 2 must).
func TestRenameViaHelperIndirect(t *testing.T) {
	w := NewWidget("a", fakeStore{})
	if err := RenameViaHelper(w, "b"); err != nil {
		t.Fatal(err)
	}
}

// newFakeWidget is a package test helper, not a Test entrypoint.
func newFakeWidget() *Widget {
	return NewWidget("fake", fakeStore{})
}

type fakeStore struct{}

func (fakeStore) Save(id string) error { return nil }

// assertWidgetName is called directly by TestRename above — B1's tier-1
// "referenced by section d's tests" ranking must place it ahead of an
// unreferenced, dissimilarly-named helper such as zzzUnrelatedHelper below.
func assertWidgetName(t *testing.T, w *Widget, want string) {
	t.Helper()
	if w.Name != want {
		t.Fatalf("name = %q, want %q", w.Name, want)
	}
}

// zzzUnrelatedHelper is never called by any test and shares no meaningful
// substring with any requested symbol in TestContextForTestRanksHelpers
// AndIncludesTopBodies — B1's tier-2 name-similarity ranking must sort it
// behind assertWidgetName, and with a small --helper-bodies cap it must be
// the one left with a signature-only (no Source) rendering.
func zzzUnrelatedHelper() {}
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

// runContextText runs `context` with no --format flag, i.e. the default
// markdown/text renderer a human or agent gets from a plain `codegrapher
// context <symbol>` invocation, and returns the raw rendered output.
func runContextText(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := newContextCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	fullArgs := append(append([]string{}, args...), "-p", root)
	cmd.SetArgs(fullArgs)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("context command failed: %v\n%s", err, out.String())
	}
	return out.String()
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

// TestContextMethodExercisesReceiverConstructorFieldAndSeamHeuristics runs
// context on Record — a METHOD, not a plain function — so the receiver,
// constructor, field-type, field-seam, and parameter-seam code paths all
// run (they never do for Rename/Describe, which are plain functions with
// no receiver). It asserts both the graph-verified facts (receiver,
// constructor, interface seam) and the text-scan-derived ones (the "field"
// role, and the "field"/"parameter" seams) come back correctly, and that
// only the latter carry the B1 inferred marking (HeuristicRoles/SeamSource)
// — a graph fact must never be marked, and a heuristic fact must always be.
func TestContextMethodExercisesReceiverConstructorFieldAndSeamHeuristics(t *testing.T) {
	root := newContextFixture(t)
	result := runContext(t, root, "Record")

	// (b) receiver role: graph-verified, must not be in HeuristicRoles.
	var recorderDecl, newRecorderDecl, notifierDecl *ContextType
	for i := range result.Types {
		switch result.Types[i].Symbol.Name {
		case "Recorder":
			recorderDecl = &result.Types[i]
		case "NewRecorder":
			newRecorderDecl = &result.Types[i]
		case "Notifier":
			notifierDecl = &result.Types[i]
		}
	}
	if recorderDecl == nil || !containsRole(recorderDecl.Roles, "receiver") {
		t.Fatalf("Recorder decl missing receiver role: %+v", result.Types)
	}
	if containsRole(recorderDecl.HeuristicRoles, "receiver") || len(recorderDecl.HeuristicRoles) != 0 {
		t.Fatalf("Recorder's receiver role must not be marked heuristic: %+v", recorderDecl)
	}

	// (b) constructor role: graph-verified (findConstructors), via New<T>.
	if newRecorderDecl == nil || !containsRole(newRecorderDecl.Roles, "constructor") || newRecorderDecl.For != "Recorder" {
		t.Fatalf("NewRecorder decl missing constructor role for Recorder: %+v", result.Types)
	}
	if len(newRecorderDecl.HeuristicRoles) != 0 {
		t.Fatalf("NewRecorder's constructor role must not be marked heuristic: %+v", newRecorderDecl)
	}

	// (b) field-type role: text-scan-derived (fieldReadsOf/parseStructFields
	// over Record's own source, resolving r.notify's declared type), must be
	// marked heuristic.
	if notifierDecl == nil || !containsRole(notifierDecl.Roles, "field") {
		t.Fatalf("Notifier decl missing field role: %+v", result.Types)
	}
	if !containsRole(notifierDecl.HeuristicRoles, "field") {
		t.Fatalf("Notifier's field role must be marked heuristic (HeuristicRoles): %+v", notifierDecl)
	}

	// (c) callees: interface seam is graph-verified; field/parameter seams
	// are text-scan-derived and must be marked inferred via SeamSource.
	seams := map[string]ContextCallee{}
	for _, c := range result.Callees {
		seams[c.Symbol.QualifiedName] = c
	}
	notifyCall, ok := seams["notify"]
	if !ok {
		t.Fatalf("notify() callee not found: %+v", result.Callees)
	}
	if notifyCall.Seam != "field" || notifyCall.SeamSource != "inferred" {
		t.Fatalf("notify() seam = %q/%q, want field/inferred: %+v", notifyCall.Seam, notifyCall.SeamSource, notifyCall)
	}
	retryCall, ok := seams["retry"]
	if !ok {
		t.Fatalf("retry() callee not found: %+v", result.Callees)
	}
	if retryCall.Seam != "parameter" || retryCall.SeamSource != "inferred" {
		t.Fatalf("retry() seam = %q/%q, want parameter/inferred: %+v", retryCall.Seam, retryCall.SeamSource, retryCall)
	}
	notifyMethodCall, ok := seams["Notifier::Notify"]
	if !ok {
		t.Fatalf("Notifier::Notify callee not found: %+v", result.Callees)
	}
	if notifyMethodCall.Seam != "interface" || notifyMethodCall.SeamSource != "graph" {
		t.Fatalf("Notifier::Notify seam = %q/%q, want interface/graph: %+v", notifyMethodCall.Seam, notifyMethodCall.SeamSource, notifyMethodCall)
	}
}

// TestContextDefaultMarkdownRendersSectionsAndInferredMarkers runs context
// with no --format flag — the default output a human or agent actually gets
// from `codegrapher context <symbol>` — which, before this test, had never
// executed in CI (every other test passes --format json). It asserts the
// section structure renders and that the B1 inferred markers show up in the
// human-facing text exactly where the JSON assertions above say they should
// (and nowhere else).
func TestContextDefaultMarkdownRendersSectionsAndInferredMarkers(t *testing.T) {
	root := newContextFixture(t)
	out := runContextText(t, root, "Record")

	for _, want := range []string{
		"# Context — Record",
		"\n## a. Source",
		"\n## b. Types touched",
		"\n## c. Direct callees",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("default markdown output missing %q:\n%s", want, out)
		}
	}

	// Heuristic-derived facts carry the (inferred) tag.
	if !strings.Contains(out, "field (inferred)") {
		t.Fatalf("markdown missing (inferred) tag on the text-scan-derived field role:\n%s", out)
	}
	if !strings.Contains(out, "[seam: field (inferred)]") {
		t.Fatalf("markdown missing (inferred) tag on the field seam:\n%s", out)
	}
	if !strings.Contains(out, "[seam: parameter (inferred)]") {
		t.Fatalf("markdown missing (inferred) tag on the parameter seam:\n%s", out)
	}

	// Graph-verified facts never carry the tag.
	if !strings.Contains(out, "receiver") || strings.Contains(out, "receiver (inferred)") {
		t.Fatalf("graph-verified receiver role must render unmarked:\n%s", out)
	}
	if !strings.Contains(out, "[seam: interface]") || strings.Contains(out, "[seam: interface (inferred)]") {
		t.Fatalf("graph-verified interface seam must render unmarked:\n%s", out)
	}
}

// lineOf locates the 1-indexed source line containing substr in root/relPath
// — used instead of hardcoded line numbers so the new fixture functions
// below can move freely without breaking a --line-based test.
func lineOf(t *testing.T, root, relPath, substr string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, substr) {
			return i + 1
		}
	}
	t.Fatalf("substring %q not found in %s", substr, relPath)
	return 0
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:line-resolves-nested-closure
func TestContextResolvesLineIntoClosure(t *testing.T) {
	root := newContextFixture(t)
	closureLine := lineOf(t, root, "widget.go", "return retryForClosure()")

	result := runContext(t, root, "closure", "--file", "widget.go", "--line", strconv.Itoa(closureLine))

	if len(result.Unresolved) != 0 {
		t.Fatalf("closure at widget.go:%d did not resolve: %+v", closureLine, result.Unresolved)
	}
	if len(result.Sources) != 1 {
		t.Fatalf("sources = %d, want 1: %+v", len(result.Sources), result.Sources)
	}
	src := result.Sources[0]
	if src.ClosureOf == "" {
		t.Fatalf("ClosureOf not set for a line resolved inside a nested literal: %+v", src)
	}
	if !strings.Contains(src.Source, "retryForClosure()") {
		t.Fatalf("closure source missing its own body:\n%s", src.Source)
	}
	if strings.Contains(src.Source, "func WithClosure") {
		t.Fatalf("closure source must be narrowed to the literal, not the whole enclosing function:\n%s", src.Source)
	}
	if !strings.Contains(src.ClosureOf, "WithClosure") {
		t.Fatalf("ClosureOf must name the enclosing function for orientation: %q", src.ClosureOf)
	}
	// The enclosing symbol identity (for b/c/d/e lookups) is still the named
	// WithClosure function, not the anonymous literal.
	if src.Symbol.Name != "WithClosure" {
		t.Fatalf("resolved symbol = %q, want WithClosure (the enclosing named function)", src.Symbol.Name)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:thin-wrapper-surfaces-callee-source
func TestContextThinWrapperSurfacesCalleeSource(t *testing.T) {
	root := newContextFixture(t)
	result := runContext(t, root, "ForwardsToHelper")

	names := map[string]bool{}
	for _, s := range result.Sources {
		names[s.Symbol.Name] = true
	}
	if !names["ForwardsToHelper"] {
		t.Fatalf("requested symbol itself missing from sources: %+v", result.Sources)
	}
	if !names["computeReal"] {
		t.Fatalf("thin wrapper's sole callee computeReal was not surfaced as if requested: %+v", result.Sources)
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:test-hops-default-direct-only
func TestContextDefaultsToDirectTestCallersOnly(t *testing.T) {
	root := newContextFixture(t)

	direct := runContext(t, root, "Rename")
	names := map[string]bool{}
	for _, tr := range direct.Tests {
		names[tr.Name] = true
	}
	if !names["TestRename"] {
		t.Fatalf("direct 1-hop caller TestRename missing by default: %+v", direct.Tests)
	}
	if names["TestRenameViaHelperIndirect"] {
		t.Fatalf("2-hop caller TestRenameViaHelperIndirect must NOT appear without --test-hops 2: %+v", direct.Tests)
	}

	withHops := runContext(t, root, "Rename", "--test-hops", "2")
	names = map[string]bool{}
	hops := map[string]int{}
	for _, tr := range withHops.Tests {
		names[tr.Name] = true
		hops[tr.Name] = tr.Hops
	}
	if !names["TestRenameViaHelperIndirect"] {
		t.Fatalf("--test-hops 2 must include the 2-hop caller: %+v", withHops.Tests)
	}
	if hops["TestRenameViaHelperIndirect"] != 2 {
		t.Fatalf("TestRenameViaHelperIndirect hops = %d, want 2", hops["TestRenameViaHelperIndirect"])
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:helpers-ranked-and-capped
func TestContextForTestRanksHelpersAndIncludesTopBodies(t *testing.T) {
	root := newContextFixture(t)
	result := runContext(t, root, "Rename", "--for", "test", "--helper-bodies", "1")

	var referencedIdx, unrelatedIdx = -1, -1
	bodies := 0
	for i, h := range result.Helpers {
		switch h.Symbol.Name {
		case "assertWidgetName":
			referencedIdx = i
		case "zzzUnrelatedHelper":
			unrelatedIdx = i
		}
		if h.Source != "" {
			bodies++
		}
	}
	if referencedIdx == -1 {
		t.Fatalf("assertWidgetName (called directly by TestRename) missing from helpers: %+v", result.Helpers)
	}
	if unrelatedIdx == -1 {
		t.Fatalf("zzzUnrelatedHelper missing from helpers: %+v", result.Helpers)
	}
	if referencedIdx >= unrelatedIdx {
		t.Fatalf("referenced helper assertWidgetName (index %d) must rank ahead of unreferenced zzzUnrelatedHelper (index %d): %+v", referencedIdx, unrelatedIdx, result.Helpers)
	}
	if bodies != 1 {
		t.Fatalf("helper bodies included = %d, want exactly 1 (--helper-bodies 1): %+v", bodies, result.Helpers)
	}
	if result.Helpers[referencedIdx].Source == "" {
		t.Fatalf("top-ranked helper assertWidgetName must carry its full body with --helper-bodies 1: %+v", result.Helpers[referencedIdx])
	}
	if result.Helpers[unrelatedIdx].Source != "" {
		t.Fatalf("lower-ranked helper zzzUnrelatedHelper must be signature-only beyond the --helper-bodies cap: %+v", result.Helpers[unrelatedIdx])
	}
}

// specscore:verifies https://specscore.org/github.com/code-grapher/codegrapher/spec/features/test-context#ac:symbols-file-spans-several-targets
func TestContextSymbolsFileSpansMultipleTargets(t *testing.T) {
	root := newContextFixture(t)
	descLine := lineOf(t, root, "widget.go", "func Describe(w *Widget) string {")

	symbolsFile := filepath.Join(t.TempDir(), "symbols.txt")
	content := "widget.go\tRename\nwidget.go\t" + strconv.Itoa(descLine) + "\n"
	if err := os.WriteFile(symbolsFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result := runContext(t, root, "--symbols-file", symbolsFile)

	if len(result.Requested) != 2 {
		t.Fatalf("requested = %+v, want 2 targets from --symbols-file", result.Requested)
	}
	if len(result.Unresolved) != 0 {
		t.Fatalf("unresolved = %+v, want both --symbols-file targets to resolve", result.Unresolved)
	}
	names := map[string]bool{}
	for _, s := range result.Sources {
		names[s.Symbol.Name] = true
	}
	if !names["Rename"] || !names["Describe"] {
		t.Fatalf("sources = %+v, want both Rename (by name) and Describe (by file:line)", result.Sources)
	}
}

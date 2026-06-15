package resolve_test

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

// newPyStore builds an empty in-memory store for hand-built Python tests.
func newPyStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func pyNode(id string, kind model.NodeKind, name, qual, file string) model.Node {
	return model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: qual,
		FilePath:      file,
		Language:      model.LangPython,
	}
}

// edgeTo reports whether some edge with the given source/target/kind exists.
func hasEdge(edges []model.Edge, src, tgt string, kind model.EdgeKind) bool {
	for _, e := range edges {
		if e.Source == src && e.Target == tgt && e.Kind == kind {
			return true
		}
	}
	return false
}

func collectEdges(t *testing.T, s *store.Store, sourceID string) []model.Edge {
	t.Helper()
	edges, err := s.GetOutgoingEdges(sourceID, nil, "")
	if err != nil {
		t.Fatalf("GetOutgoingEdges: %v", err)
	}
	return edges
}

// (a) cross-file: `from a import f` + a calls ref to f from a node in file b.
func TestPythonResolveCrossFileImportCall(t *testing.T) {
	s := newPyStore(t)
	nodes := []model.Node{
		pyNode("f", model.KindFunction, "f", "f", "a.py"),
		pyNode("caller", model.KindFunction, "caller", "caller", "b.py"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "caller", ReferenceName: "f", ReferenceKind: model.EdgeCalls, FilePath: "b.py", Language: model.LangPython},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "caller")
	if !hasEdge(edges, "caller", "f", model.EdgeCalls) {
		t.Fatalf("expected calls edge caller→f, got %+v", edges)
	}
}

// (b) class Dog(Animal) extends ref → resolves to Animal node.
func TestPythonResolveExtends(t *testing.T) {
	s := newPyStore(t)
	nodes := []model.Node{
		pyNode("animal", model.KindClass, "Animal", "Animal", "a.py"),
		pyNode("dog", model.KindClass, "Dog", "Dog", "b.py"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "dog", ReferenceName: "Animal", ReferenceKind: model.EdgeExtends, FilePath: "b.py", Language: model.LangPython},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "dog")
	if !hasEdge(edges, "dog", "animal", model.EdgeExtends) {
		t.Fatalf("expected extends edge dog→animal, got %+v", edges)
	}
}

// (c) x = Widget() (variable signature `= Widget()`) + a calls ref `x.method`
// → resolves to Widget::method via type inference.
func TestPythonResolveAttrInference(t *testing.T) {
	s := newPyStore(t)
	nodes := []model.Node{
		pyNode("widget", model.KindClass, "Widget", "Widget", "w.py"),
		pyNode("wmethod", model.KindMethod, "method", "Widget::method", "w.py"),
		// Another class with a same-named method that must NOT be chosen.
		pyNode("gadget", model.KindClass, "Gadget", "Gadget", "g.py"),
		pyNode("gmethod", model.KindMethod, "method", "Gadget::method", "g.py"),
		// The local variable node carrying the constructor signature.
		func() model.Node {
			n := pyNode("xvar", model.KindVariable, "x", "x", "m.py")
			n.Signature = "= Widget()"
			return n
		}(),
		pyNode("user", model.KindFunction, "use", "use", "m.py"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "user", ReferenceName: "x.method", ReferenceKind: model.EdgeCalls, FilePath: "m.py", Language: model.LangPython},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "user")
	if !hasEdge(edges, "user", "wmethod", model.EdgeCalls) {
		t.Fatalf("expected calls edge user→Widget::method, got %+v", edges)
	}
	if hasEdge(edges, "user", "gmethod", model.EdgeCalls) {
		t.Fatalf("did not expect edge to Gadget::method, got %+v", edges)
	}
}

// Imported-class constructor call resolves THROUGH the local import node to the
// real cross-file class, promoted to `instantiates`.
func TestPythonResolveImportedClassConstructor(t *testing.T) {
	s := newPyStore(t)
	nodes := []model.Node{
		// Real class in models.py.
		pyNode("dog", model.KindClass, "Dog", "Dog", "models.py"),
		// Local import node `Dog` in service.py (from models import Dog).
		pyNode("dogimport", model.KindImport, "Dog", "Dog", "service.py"),
		pyNode("makedog", model.KindFunction, "make_dog", "make_dog", "service.py"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "makedog", ReferenceName: "Dog", ReferenceKind: model.EdgeCalls, FilePath: "service.py", Language: model.LangPython},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "makedog")
	if !hasEdge(edges, "makedog", "dog", model.EdgeInstantiates) {
		t.Fatalf("expected instantiates edge make_dog→class Dog (models.py), got %+v", edges)
	}
	if hasEdge(edges, "makedog", "dogimport", model.EdgeCalls) ||
		hasEdge(edges, "makedog", "dogimport", model.EdgeInstantiates) {
		t.Fatalf("did not expect edge to the local import node, got %+v", edges)
	}
}

// Imported-function call resolves THROUGH the import node to the cross-file
// function as `calls`.
func TestPythonResolveImportedFunctionCall(t *testing.T) {
	s := newPyStore(t)
	nodes := []model.Node{
		pyNode("helper", model.KindFunction, "helper", "helper", "util.py"),
		pyNode("helperimport", model.KindImport, "helper", "helper", "service.py"),
		pyNode("caller", model.KindFunction, "caller", "caller", "service.py"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "caller", ReferenceName: "helper", ReferenceKind: model.EdgeCalls, FilePath: "service.py", Language: model.LangPython},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "caller")
	if !hasEdge(edges, "caller", "helper", model.EdgeCalls) {
		t.Fatalf("expected calls edge caller→helper (util.py), got %+v", edges)
	}
}

// An `imports` ref resolves to the real definition in the source module when it
// exists, not the local import node.
func TestPythonResolveImportsRefThrough(t *testing.T) {
	s := newPyStore(t)
	nodes := []model.Node{
		pyNode("dog", model.KindClass, "Dog", "Dog", "models.py"),
		pyNode("dogimport", model.KindImport, "Dog", "Dog", "service.py"),
		pyNode("svcfile", model.KindFile, "service.py", "service.py", "service.py"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "svcfile", ReferenceName: "Dog", ReferenceKind: model.EdgeImports, FilePath: "service.py", Language: model.LangPython},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "svcfile")
	if !hasEdge(edges, "svcfile", "dog", model.EdgeImports) {
		t.Fatalf("expected imports edge service.py→class Dog (models.py), got %+v", edges)
	}
	if hasEdge(edges, "svcfile", "dogimport", model.EdgeImports) {
		t.Fatalf("did not expect self-referential imports edge to local import node, got %+v", edges)
	}
}

// Whole-module import with no in-repo definition stays on the import node (or
// unresolved) — no crash, no bogus cross-file edge.
func TestPythonResolveWholeModuleImportNoDefinition(t *testing.T) {
	s := newPyStore(t)
	nodes := []model.Node{
		pyNode("fnimport", model.KindImport, "functools", "functools", "service.py"),
		pyNode("svcfile", model.KindFile, "service.py", "service.py", "service.py"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "svcfile", ReferenceName: "functools", ReferenceKind: model.EdgeImports, FilePath: "service.py", Language: model.LangPython},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "svcfile")
	// Either no edge, or an imports edge to the local import node — never a crash.
	for _, e := range edges {
		if e.Target != "fnimport" {
			t.Fatalf("unexpected edge target for whole-module import: %+v", e)
		}
	}
}

// (d) y.method where y has no known type → stays unresolved (no edge).
func TestPythonResolveUnknownReceiverUnresolved(t *testing.T) {
	s := newPyStore(t)
	nodes := []model.Node{
		pyNode("widget", model.KindClass, "Widget", "Widget", "w.py"),
		pyNode("wmethod", model.KindMethod, "method", "Widget::method", "w.py"),
		pyNode("gadget", model.KindClass, "Gadget", "Gadget", "g.py"),
		pyNode("gmethod", model.KindMethod, "method", "Gadget::method", "g.py"),
		pyNode("user", model.KindFunction, "use", "use", "m.py"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "user", ReferenceName: "y.method", ReferenceKind: model.EdgeCalls, FilePath: "m.py", Language: model.LangPython},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "user")
	// y has no inferred type, and `method` is ambiguous (two classes) → no edge.
	if len(edges) != 0 {
		t.Fatalf("expected no edges for unknown receiver, got %+v", edges)
	}
}

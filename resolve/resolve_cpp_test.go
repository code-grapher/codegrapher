package resolve_test

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

func newCppStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func cppNode(id string, kind model.NodeKind, name, qual, file string) model.Node {
	return model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: qual,
		FilePath:      file,
		Language:      model.LangCPP,
	}
}

func cppResolve(t *testing.T, s *store.Store, nodes []model.Node, refs []model.UnresolvedReference) {
	t.Helper()
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

// base class clause → extends edge.
func TestCppResolveExtends(t *testing.T) {
	s := newCppStore(t)
	cppResolve(t, s,
		[]model.Node{
			cppNode("shape", model.KindClass, "Shape", "geo::Shape", "shapes.hpp"),
			cppNode("circle", model.KindClass, "Circle", "geo::Circle", "shapes.hpp"),
		},
		[]model.UnresolvedReference{
			{FromNodeID: "circle", ReferenceName: "Shape", ReferenceKind: model.EdgeExtends, FilePath: "shapes.hpp", Language: model.LangCPP},
		},
	)
	edges := collectEdges(t, s, "circle")
	if !hasEdge(edges, "circle", "shape", model.EdgeExtends) {
		t.Fatalf("expected extends circle→shape, got %+v", edges)
	}
}

// virtual override resolves Circle::area overrides → Shape::area (member on base).
func TestCppResolveOverrides(t *testing.T) {
	s := newCppStore(t)
	cppResolve(t, s,
		[]model.Node{
			cppNode("shape", model.KindClass, "Shape", "geo::Shape", "shapes.hpp"),
			cppNode("circle", model.KindClass, "Circle", "geo::Circle", "shapes.hpp"),
			cppNode("sarea", model.KindMethod, "area", "geo::Shape::area", "shapes.hpp"),
			cppNode("carea", model.KindMethod, "area", "geo::Circle::area", "shapes.hpp"),
		},
		[]model.UnresolvedReference{
			{FromNodeID: "carea", ReferenceName: "Shape::area", ReferenceKind: model.EdgeOverrides, FilePath: "shapes.hpp", Language: model.LangCPP},
		},
	)
	edges := collectEdges(t, s, "carea")
	if !hasEdge(edges, "carea", "sarea", model.EdgeOverrides) {
		t.Fatalf("expected overrides carea→sarea, got %+v", edges)
	}
}

// new Circle(...) → instantiates the Circle class.
func TestCppResolveInstantiates(t *testing.T) {
	s := newCppStore(t)
	cppResolve(t, s,
		[]model.Node{
			cppNode("circle", model.KindClass, "Circle", "geo::Circle", "shapes.hpp"),
			cppNode("run", model.KindFunction, "run", "run", "app.cpp"),
		},
		[]model.UnresolvedReference{
			{FromNodeID: "run", ReferenceName: "Circle", ReferenceKind: model.EdgeInstantiates, FilePath: "app.cpp", Language: model.LangCPP},
		},
	)
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "circle", model.EdgeInstantiates) {
		t.Fatalf("expected instantiates run→circle, got %+v", edges)
	}
}

// s->area() where s is Shape* but area is overridden: the bare "area" call
// resolves to a concrete member (deterministic same-file pick).
func TestCppResolveMethodCallByName(t *testing.T) {
	s := newCppStore(t)
	cppResolve(t, s,
		[]model.Node{
			cppNode("carea", model.KindMethod, "area", "geo::Circle::area", "shapes.hpp"),
			cppNode("run", model.KindFunction, "run", "run", "shapes.cpp"),
		},
		[]model.UnresolvedReference{
			{FromNodeID: "run", ReferenceName: "area", ReferenceKind: model.EdgeCalls, FilePath: "shapes.cpp", Language: model.LangCPP},
		},
	)
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "carea", model.EdgeCalls) {
		t.Fatalf("expected call run→carea, got %+v", edges)
	}
}

// An inherited method call resolves through the base table: Base::helper
// reachable from a Derived receiver. Here the qualified call Base::helper.
func TestCppResolveInheritedQualifiedCall(t *testing.T) {
	s := newCppStore(t)
	cppResolve(t, s,
		[]model.Node{
			cppNode("base", model.KindClass, "Base", "Base", "b.hpp"),
			cppNode("derived", model.KindClass, "Derived", "Derived", "b.hpp"),
			cppNode("helper", model.KindMethod, "helper", "Base::helper", "b.hpp"),
			cppNode("run", model.KindFunction, "run", "run", "app.cpp"),
		},
		[]model.UnresolvedReference{
			// Derived extends Base.
			{FromNodeID: "derived", ReferenceName: "Base", ReferenceKind: model.EdgeExtends, FilePath: "b.hpp", Language: model.LangCPP},
			// Call Derived::helper — helper lives on Base, reached via base table.
			{FromNodeID: "run", ReferenceName: "Derived::helper", ReferenceKind: model.EdgeCalls, FilePath: "app.cpp", Language: model.LangCPP},
		},
	)
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "helper", model.EdgeCalls) {
		t.Fatalf("expected inherited call run→Base::helper, got %+v", edges)
	}
}

// #include resolves to the in-repo header file node.
func TestCppResolveInclude(t *testing.T) {
	s := newCppStore(t)
	cppResolve(t, s,
		[]model.Node{
			cppNode(model.FileNodeID("shapes.hpp"), model.KindFile, "shapes.hpp", "shapes.hpp", "shapes.hpp"),
			cppNode(model.FileNodeID("shapes.cpp"), model.KindFile, "shapes.cpp", "shapes.cpp", "shapes.cpp"),
		},
		[]model.UnresolvedReference{
			{FromNodeID: model.FileNodeID("shapes.cpp"), ReferenceName: "shapes.hpp", ReferenceKind: model.EdgeImports, FilePath: "shapes.cpp", Language: model.LangCPP},
		},
	)
	edges := collectEdges(t, s, model.FileNodeID("shapes.cpp"))
	if !hasEdge(edges, model.FileNodeID("shapes.cpp"), model.FileNodeID("shapes.hpp"), model.EdgeImports) {
		t.Fatalf("expected #include edge shapes.cpp→shapes.hpp, got %+v", edges)
	}
}

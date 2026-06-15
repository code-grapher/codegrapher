package resolve_test

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

func newFsStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func fsNode(id string, kind model.NodeKind, name, qual, file string) model.Node {
	return model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: qual,
		FilePath:      file,
		Language:      model.LangFSharp,
	}
}

func fsResolve(t *testing.T, s *store.Store, nodes []model.Node, refs []model.UnresolvedReference) {
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

// open Shapes → imports resolves to the module node by last segment.
func TestFSharpResolveImport(t *testing.T) {
	s := newFsStore(t)
	nodes := []model.Node{
		fsNode("shapes", model.KindModule, "Shapes", "Shapes", "shapes.fs"),
		fsNode("app", model.KindModule, "App", "App", "app.fs"),
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "app", ReferenceName: "Shapes", ReferenceKind: model.EdgeImports, FilePath: "app.fs", Language: model.LangFSharp},
	}
	fsResolve(t, s, nodes, refs)
	edges := collectEdges(t, s, "app")
	if !hasEdge(edges, "app", "shapes", model.EdgeImports) {
		t.Fatalf("expected imports edge app→shapes, got %+v", edges)
	}
}

// Cross-module call after open: App calls area defined in Shapes.
func TestFSharpResolveCrossModuleCall(t *testing.T) {
	s := newFsStore(t)
	nodes := []model.Node{
		fsNode("shapes", model.KindModule, "Shapes", "Shapes", "shapes.fs"),
		fsNode("area", model.KindFunction, "area", "Shapes::area", "shapes.fs"),
		fsNode("app", model.KindModule, "App", "App", "app.fs"),
		fsNode("run", model.KindFunction, "run", "App::run", "app.fs"),
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "run", ReferenceName: "area", ReferenceKind: model.EdgeCalls, FilePath: "app.fs", Language: model.LangFSharp},
	}
	fsResolve(t, s, nodes, refs)
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "area", model.EdgeCalls) {
		t.Fatalf("expected call edge run→area, got %+v", edges)
	}
}

// Dog("x") → instantiates resolves to the Dog class.
func TestFSharpResolveInstantiate(t *testing.T) {
	s := newFsStore(t)
	nodes := []model.Node{
		fsNode("dog", model.KindClass, "Dog", "Shapes::Dog", "shapes.fs"),
		fsNode("run", model.KindFunction, "run", "App::run", "app.fs"),
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "run", ReferenceName: "Dog", ReferenceKind: model.EdgeInstantiates, FilePath: "app.fs", Language: model.LangFSharp},
	}
	fsResolve(t, s, nodes, refs)
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "dog", model.EdgeInstantiates) {
		t.Fatalf("expected instantiates edge run→dog, got %+v", edges)
	}
}

// inherit Base() → extends; interface I → implements.
func TestFSharpResolveExtendsImplements(t *testing.T) {
	s := newFsStore(t)
	nodes := []model.Node{
		fsNode("base", model.KindClass, "Base", "Base", "base.fs"),
		fsNode("iface", model.KindInterface, "IThing", "IThing", "base.fs"),
		fsNode("dog", model.KindClass, "Dog", "Dog", "dog.fs"),
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "dog", ReferenceName: "Base", ReferenceKind: model.EdgeExtends, FilePath: "dog.fs", Language: model.LangFSharp},
		{FromNodeID: "dog", ReferenceName: "IThing", ReferenceKind: model.EdgeImplements, FilePath: "dog.fs", Language: model.LangFSharp},
	}
	fsResolve(t, s, nodes, refs)
	edges := collectEdges(t, s, "dog")
	if !hasEdge(edges, "dog", "base", model.EdgeExtends) {
		t.Errorf("expected extends edge dog→base, got %+v", edges)
	}
	if !hasEdge(edges, "dog", "iface", model.EdgeImplements) {
		t.Errorf("expected implements edge dog→iface, got %+v", edges)
	}
}

// printfn is an F# core builtin: its call must NOT resolve to a user node.
func TestFSharpResolveBuiltinSkipped(t *testing.T) {
	s := newFsStore(t)
	nodes := []model.Node{
		fsNode("printfn", model.KindFunction, "printfn", "M::printfn", "m.fs"),
		fsNode("run", model.KindFunction, "run", "M::run", "m.fs"),
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "run", ReferenceName: "printfn", ReferenceKind: model.EdgeCalls, FilePath: "m.fs", Language: model.LangFSharp},
	}
	fsResolve(t, s, nodes, refs)
	edges := collectEdges(t, s, "run")
	if hasEdge(edges, "run", "printfn", model.EdgeCalls) {
		t.Fatalf("printfn builtin should not resolve, got %+v", edges)
	}
}

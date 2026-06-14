package resolve_test

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

func newRsStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func rsNode(id string, kind model.NodeKind, name, qual, file string) model.Node {
	return model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: qual,
		FilePath:      file,
		Language:      model.LangRust,
	}
}

// impl Shape for Circle → implements resolves Circle→Shape (trait/interface).
func TestRustResolveImplements(t *testing.T) {
	s := newRsStore(t)
	nodes := []model.Node{
		rsNode("shape", model.KindInterface, "Shape", "Shape", "shapes.rs"),
		rsNode("circle", model.KindStruct, "Circle", "Circle", "shapes.rs"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "circle", ReferenceName: "Shape", ReferenceKind: model.EdgeImplements, FilePath: "shapes.rs", Language: model.LangRust},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "circle")
	if !hasEdge(edges, "circle", "shape", model.EdgeImplements) {
		t.Fatalf("expected implements edge circle→Shape, got %+v", edges)
	}
}

// Circle::area overrides Shape::area (trait method resolution by qualified name).
func TestRustResolveOverrides(t *testing.T) {
	s := newRsStore(t)
	nodes := []model.Node{
		rsNode("shapearea", model.KindMethod, "area", "Shape::area", "shapes.rs"),
		rsNode("circlearea", model.KindMethod, "area", "Circle::area", "shapes.rs"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "circlearea", ReferenceName: "Shape::area", ReferenceKind: model.EdgeOverrides, FilePath: "shapes.rs", Language: model.LangRust},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "circlearea")
	if !hasEdge(edges, "circlearea", "shapearea", model.EdgeOverrides) {
		t.Fatalf("expected overrides edge Circle::area→Shape::area, got %+v", edges)
	}
}

// Circle::new(...) → instantiates the Circle struct (constructor promotion).
func TestRustResolveAssocNewInstantiates(t *testing.T) {
	s := newRsStore(t)
	nodes := []model.Node{
		rsNode("circle", model.KindStruct, "Circle", "Circle", "shapes.rs"),
		rsNode("circlenew", model.KindMethod, "new", "Circle::new", "shapes.rs"),
		rsNode("run", model.KindFunction, "run", "run", "app.rs"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "run", ReferenceName: "Circle::new", ReferenceKind: model.EdgeCalls, FilePath: "app.rs", Language: model.LangRust},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "circle", model.EdgeInstantiates) {
		t.Fatalf("expected instantiates edge run→Circle, got %+v", edges)
	}
}

// Circle { .. } struct literal → instantiates Circle.
func TestRustResolveStructLiteralInstantiates(t *testing.T) {
	s := newRsStore(t)
	nodes := []model.Node{
		rsNode("circle", model.KindStruct, "Circle", "Circle", "shapes.rs"),
		rsNode("run", model.KindFunction, "run", "run", "app.rs"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "run", ReferenceName: "Circle", ReferenceKind: model.EdgeInstantiates, FilePath: "app.rs", Language: model.LangRust},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "circle", model.EdgeInstantiates) {
		t.Fatalf("expected instantiates edge run→Circle, got %+v", edges)
	}
}

// x.area() → bare method "area" resolves to the single Circle::area method.
func TestRustResolveMethodCall(t *testing.T) {
	s := newRsStore(t)
	nodes := []model.Node{
		rsNode("circlearea", model.KindMethod, "area", "Circle::area", "shapes.rs"),
		rsNode("run", model.KindFunction, "run", "run", "app.rs"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "run", ReferenceName: "area", ReferenceKind: model.EdgeCalls, FilePath: "app.rs", Language: model.LangRust},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "circlearea", model.EdgeCalls) {
		t.Fatalf("expected calls edge run→Circle::area, got %+v", edges)
	}
}

// use crate::shapes::Circle imports ref resolves to the real cross-file struct.
func TestRustResolveImportsThrough(t *testing.T) {
	s := newRsStore(t)
	nodes := []model.Node{
		rsNode("circle", model.KindStruct, "Circle", "Circle", "shapes.rs"),
		rsNode("appmod", model.KindModule, "app", "app", "app.rs"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "appmod", ReferenceName: "Circle", ReferenceKind: model.EdgeImports, FilePath: "app.rs", Language: model.LangRust},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "appmod")
	if !hasEdge(edges, "appmod", "circle", model.EdgeImports) {
		t.Fatalf("expected imports edge app→Circle, got %+v", edges)
	}
}

// Bare call to a free function resolves to the function (not instantiated).
func TestRustResolveBareFunctionCall(t *testing.T) {
	s := newRsStore(t)
	nodes := []model.Node{
		rsNode("helper", model.KindFunction, "helper", "helper", "util.rs"),
		rsNode("run", model.KindFunction, "run", "run", "util.rs"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "run", ReferenceName: "helper", ReferenceKind: model.EdgeCalls, FilePath: "util.rs", Language: model.LangRust},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "helper", model.EdgeCalls) {
		t.Fatalf("expected calls edge run→helper, got %+v", edges)
	}
}

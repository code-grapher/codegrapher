package resolve_test

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

func newRbStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func rbNode(id string, kind model.NodeKind, name, qual, file string) model.Node {
	return model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: qual,
		FilePath:      file,
		Language:      model.LangRuby,
	}
}

// class Dog < Base → extends resolves to Base node.
func TestRubyResolveExtends(t *testing.T) {
	s := newRbStore(t)
	nodes := []model.Node{
		rbNode("base", model.KindClass, "Base", "Base", "base.rb"),
		rbNode("dog", model.KindClass, "Dog", "Dog", "dog.rb"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "dog", ReferenceName: "Base", ReferenceKind: model.EdgeExtends, FilePath: "dog.rb", Language: model.LangRuby},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "dog")
	if !hasEdge(edges, "dog", "base", model.EdgeExtends) {
		t.Fatalf("expected extends edge dog→base, got %+v", edges)
	}
}

// include Walkable → implements resolves to the module node.
func TestRubyResolveIncludeImplements(t *testing.T) {
	s := newRbStore(t)
	nodes := []model.Node{
		rbNode("walkable", model.KindModule, "Walkable", "Walkable", "walkable.rb"),
		rbNode("dog", model.KindClass, "Dog", "Dog", "dog.rb"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "dog", ReferenceName: "Walkable", ReferenceKind: model.EdgeImplements, FilePath: "dog.rb", Language: model.LangRuby},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "dog")
	if !hasEdge(edges, "dog", "walkable", model.EdgeImplements) {
		t.Fatalf("expected implements edge dog→Walkable, got %+v", edges)
	}
}

// require_relative 'dog'; Dog.new resolves THROUGH to the real class as
// instantiates (constant receiver + new).
func TestRubyResolveConstructorInstantiates(t *testing.T) {
	s := newRbStore(t)
	nodes := []model.Node{
		rbNode("dog", model.KindClass, "Dog", "Dog", "dog.rb"),
		rbNode("dogimport", model.KindImport, "dog", "dog", "main.rb"),
		rbNode("main", model.KindFunction, "run", "run", "main.rb"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "main", ReferenceName: "Dog.new", ReferenceKind: model.EdgeCalls, FilePath: "main.rb", Language: model.LangRuby},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "main")
	if !hasEdge(edges, "main", "dog", model.EdgeInstantiates) {
		t.Fatalf("expected instantiates edge run→class Dog, got %+v", edges)
	}
}

// x = Dog.new; x.speak resolves to Dog::speak via constructor-assignment
// inference; a same-named method on another class must NOT be chosen.
func TestRubyResolveAttrInference(t *testing.T) {
	s := newRbStore(t)
	nodes := []model.Node{
		rbNode("dog", model.KindClass, "Dog", "Dog", "dog.rb"),
		rbNode("dogspeak", model.KindMethod, "speak", "Dog::speak", "dog.rb"),
		rbNode("cat", model.KindClass, "Cat", "Cat", "cat.rb"),
		rbNode("catspeak", model.KindMethod, "speak", "Cat::speak", "cat.rb"),
		func() model.Node {
			n := rbNode("xvar", model.KindVariable, "x", "x", "main.rb")
			n.Signature = `= Dog.new("rex")`
			return n
		}(),
		rbNode("main", model.KindFunction, "run", "run", "main.rb"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "main", ReferenceName: "x.speak", ReferenceKind: model.EdgeCalls, FilePath: "main.rb", Language: model.LangRuby},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "main")
	if !hasEdge(edges, "main", "dogspeak", model.EdgeCalls) {
		t.Fatalf("expected calls edge run→Dog::speak, got %+v", edges)
	}
	if hasEdge(edges, "main", "catspeak", model.EdgeCalls) {
		t.Fatalf("did not expect edge to Cat::speak, got %+v", edges)
	}
}

// require_relative 'dog' imports ref resolves to the real cross-file class.
func TestRubyResolveImportsThrough(t *testing.T) {
	s := newRbStore(t)
	nodes := []model.Node{
		rbNode("dog", model.KindClass, "dog", "dog", "dog.rb"),
		rbNode("mainfile", model.KindFile, "main.rb", "main.rb", "main.rb"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "mainfile", ReferenceName: "dog", ReferenceKind: model.EdgeImports, FilePath: "main.rb", Language: model.LangRuby},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "mainfile")
	if !hasEdge(edges, "mainfile", "dog", model.EdgeImports) {
		t.Fatalf("expected imports edge main.rb→dog, got %+v", edges)
	}
}

// y.method where y has no known type and method is ambiguous → unresolved.
func TestRubyResolveUnknownReceiverUnresolved(t *testing.T) {
	s := newRbStore(t)
	nodes := []model.Node{
		rbNode("dog", model.KindClass, "Dog", "Dog", "dog.rb"),
		rbNode("dogspeak", model.KindMethod, "speak", "Dog::speak", "dog.rb"),
		rbNode("cat", model.KindClass, "Cat", "Cat", "cat.rb"),
		rbNode("catspeak", model.KindMethod, "speak", "Cat::speak", "cat.rb"),
		rbNode("main", model.KindFunction, "run", "run", "main.rb"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "main", ReferenceName: "y.speak", ReferenceKind: model.EdgeCalls, FilePath: "main.rb", Language: model.LangRuby},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "main")
	if len(edges) != 0 {
		t.Fatalf("expected no edges for unknown ambiguous receiver, got %+v", edges)
	}
}

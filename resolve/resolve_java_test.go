package resolve_test

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

func newJavaStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func javaNode(id string, kind model.NodeKind, name, qual, file string) model.Node {
	return model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: qual,
		FilePath:      file,
		Language:      model.LangJava,
	}
}

// jvmImport builds a Java import node with the import-statement signature so the
// resolver's per-file context can parse the FQN/wildcard.
func jvmImport(id, name, sig, file string) model.Node {
	n := javaNode(id, model.KindImport, name, name, file)
	n.Signature = sig
	return n
}

// jvmPkg builds the namespace (package) node for a file.
func jvmPkg(id, pkg, file string) model.Node {
	return javaNode(id, model.KindNamespace, pkg, pkg, file)
}

// (a) imported type's `new T()` lands on the real cross-file class as instantiates.
func TestJavaResolveImportedConstructor(t *testing.T) {
	s := newJavaStore(t)
	nodes := []model.Node{
		jvmPkg("pkgmodels", "com.example.models", "models/Dog.java"),
		javaNode("dog", model.KindClass, "Dog", "Dog", "models/Dog.java"),
		jvmPkg("pkgsvc", "com.example.svc", "svc/Service.java"),
		jvmImport("dogimport", "Dog", "import com.example.models.Dog;", "svc/Service.java"),
		javaNode("makedog", model.KindMethod, "makeDog", "Service::makeDog", "svc/Service.java"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "makedog", ReferenceName: "Dog", ReferenceKind: model.EdgeInstantiates, FilePath: "svc/Service.java", Language: model.LangJava},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "makedog")
	if !hasEdge(edges, "makedog", "dog", model.EdgeInstantiates) {
		t.Fatalf("expected instantiates makeDog→Dog (models), got %+v", edges)
	}
	if hasEdge(edges, "makedog", "dogimport", model.EdgeInstantiates) {
		t.Fatalf("did not expect edge to the local import node, got %+v", edges)
	}
}

// (b) calls promoted to instantiates for a same-package class.
func TestJavaResolveCallPromotedToInstantiates(t *testing.T) {
	s := newJavaStore(t)
	nodes := []model.Node{
		jvmPkg("pkga", "com.app", "a/Widget.java"),
		javaNode("widget", model.KindClass, "Widget", "Widget", "a/Widget.java"),
		jvmPkg("pkgb", "com.app", "a/Factory.java"),
		javaNode("make", model.KindMethod, "make", "Factory::make", "a/Factory.java"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "make", ReferenceName: "Widget", ReferenceKind: model.EdgeCalls, FilePath: "a/Factory.java", Language: model.LangJava},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "make")
	if !hasEdge(edges, "make", "widget", model.EdgeInstantiates) {
		t.Fatalf("expected calls→instantiates make→Widget, got %+v", edges)
	}
}

// (c) extends resolves to the base class.
func TestJavaResolveExtends(t *testing.T) {
	s := newJavaStore(t)
	nodes := []model.Node{
		jvmPkg("pkga", "com.app", "Base.java"),
		javaNode("base", model.KindClass, "Base", "Base", "Base.java"),
		jvmPkg("pkgb", "com.app", "Circle.java"),
		javaNode("circle", model.KindClass, "Circle", "Circle", "Circle.java"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "circle", ReferenceName: "Base", ReferenceKind: model.EdgeExtends, FilePath: "Circle.java", Language: model.LangJava},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "circle")
	if !hasEdge(edges, "circle", "base", model.EdgeExtends) {
		t.Fatalf("expected extends circle→base, got %+v", edges)
	}
}

// (d) implements resolves to the interface, preferring the explicitly imported
// type over a same-named type in an unrelated package.
func TestJavaResolveImplementsPrefersImport(t *testing.T) {
	s := newJavaStore(t)
	nodes := []model.Node{
		// The intended interface, in a package the consumer imports.
		jvmPkg("pkgshape", "com.example.geom", "geom/Shape.java"),
		javaNode("shape", model.KindInterface, "Shape", "Shape", "geom/Shape.java"),
		// A decoy same-named interface in an unrelated package.
		jvmPkg("pkgdecoy", "com.other", "other/Shape.java"),
		javaNode("decoy", model.KindInterface, "Shape", "Shape", "other/Shape.java"),
		// The consumer importing geom.Shape explicitly.
		jvmPkg("pkgcirc", "com.example.app", "app/Circle.java"),
		jvmImport("shapeimport", "Shape", "import com.example.geom.Shape;", "app/Circle.java"),
		javaNode("circle", model.KindClass, "Circle", "Circle", "app/Circle.java"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "circle", ReferenceName: "Shape", ReferenceKind: model.EdgeImplements, FilePath: "app/Circle.java", Language: model.LangJava},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "circle")
	if !hasEdge(edges, "circle", "shape", model.EdgeImplements) {
		t.Fatalf("expected implements circle→Shape (geom, imported), got %+v", edges)
	}
	if hasEdge(edges, "circle", "decoy", model.EdgeImplements) {
		t.Fatalf("did not expect implements to the decoy Shape, got %+v", edges)
	}
}

// (e) cross-file method call resolves to the single matching method.
func TestJavaResolveMethodCall(t *testing.T) {
	s := newJavaStore(t)
	nodes := []model.Node{
		jvmPkg("pkga", "com.app", "a/Dog.java"),
		javaNode("speak", model.KindMethod, "speak", "Dog::speak", "a/Dog.java"),
		jvmPkg("pkgb", "com.app", "b/Service.java"),
		javaNode("describe", model.KindMethod, "describe", "Service::describe", "b/Service.java"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "describe", ReferenceName: "d.speak", ReferenceKind: model.EdgeCalls, FilePath: "b/Service.java", Language: model.LangJava},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "describe")
	if !hasEdge(edges, "describe", "speak", model.EdgeCalls) {
		t.Fatalf("expected calls describe→Dog::speak, got %+v", edges)
	}
}

// (f) imports ref resolves through to the real type, not the local import shim.
func TestJavaResolveImportsRefThrough(t *testing.T) {
	s := newJavaStore(t)
	nodes := []model.Node{
		jvmPkg("pkgmodels", "com.example.models", "models/Dog.java"),
		javaNode("dog", model.KindClass, "Dog", "Dog", "models/Dog.java"),
		jvmPkg("pkgsvc", "com.example.svc", "svc/Service.java"),
		jvmImport("dogimport", "Dog", "import com.example.models.Dog;", "svc/Service.java"),
		javaNode("svcfile", model.KindFile, "Service.java", "svc/Service.java", "svc/Service.java"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "svcfile", ReferenceName: "Dog", ReferenceKind: model.EdgeImports, FilePath: "svc/Service.java", Language: model.LangJava},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "svcfile")
	if !hasEdge(edges, "svcfile", "dog", model.EdgeImports) {
		t.Fatalf("expected imports service→Dog (models), got %+v", edges)
	}
	if hasEdge(edges, "svcfile", "dogimport", model.EdgeImports) {
		t.Fatalf("did not expect self-referential imports to local import node, got %+v", edges)
	}
}

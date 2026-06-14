package resolve_test

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

func newCSStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func csNode(id string, kind model.NodeKind, name, qual, file string) model.Node {
	return model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: qual,
		FilePath:      file,
		Language:      model.LangCSharp,
	}
}

func resolveCS(t *testing.T, s *store.Store, nodes []model.Node, refs []model.UnresolvedReference) {
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

// Base class → extends; base interface → reclassified to implements.
func TestCSharpExtendsAndImplements(t *testing.T) {
	s := newCSStore(t)
	nodes := []model.Node{
		csNode("animal", model.KindClass, "Animal", "N::Animal", "Models.cs"),
		csNode("igreeter", model.KindInterface, "IGreeter", "N::IGreeter", "Models.cs"),
		csNode("dog", model.KindClass, "Dog", "N::Dog", "Models.cs"),
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "dog", ReferenceName: "Animal", ReferenceKind: model.EdgeExtends, FilePath: "Models.cs", Language: model.LangCSharp},
		{FromNodeID: "dog", ReferenceName: "IGreeter", ReferenceKind: model.EdgeExtends, FilePath: "Models.cs", Language: model.LangCSharp},
	}
	resolveCS(t, s, nodes, refs)
	edges := collectEdges(t, s, "dog")
	if !hasEdge(edges, "dog", "animal", model.EdgeExtends) {
		t.Errorf("expected extends edge dog→Animal, got %+v", edges)
	}
	if !hasEdge(edges, "dog", "igreeter", model.EdgeImplements) {
		t.Errorf("expected implements edge dog→IGreeter, got %+v", edges)
	}
}

// new T() → instantiates to the cross-file class.
func TestCSharpInstantiatesCrossFile(t *testing.T) {
	s := newCSStore(t)
	nodes := []model.Node{
		csNode("dog", model.KindClass, "Dog", "N::Dog", "Models.cs"),
		csNode("svcM", model.KindMethod, "MakeDog", "N::Service::MakeDog", "Service.cs"),
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "svcM", ReferenceName: "Dog", ReferenceKind: model.EdgeInstantiates, FilePath: "Service.cs", Language: model.LangCSharp},
	}
	resolveCS(t, s, nodes, refs)
	edges := collectEdges(t, s, "svcM")
	if !hasEdge(edges, "svcM", "dog", model.EdgeInstantiates) {
		t.Errorf("expected instantiates edge svcM→Dog, got %+v", edges)
	}
}

// using-alias: new Svc() resolves through the alias import to Service.
func TestCSharpInstantiatesThroughAlias(t *testing.T) {
	s := newCSStore(t)
	nodes := []model.Node{
		csNode("service", model.KindClass, "Service", "App::Service", "Service.cs"),
		csNode("svcImport", model.KindImport, "Svc", "", "Util.cs"),
		csNode("kennelM", model.KindMethod, "Boast", "Util::Kennel::Boast", "Util.cs"),
	}
	nodes[1].Signature = "using Svc = CsSmall.App.Service;"
	refs := []model.UnresolvedReference{
		{FromNodeID: "kennelM", ReferenceName: "Svc", ReferenceKind: model.EdgeInstantiates, FilePath: "Util.cs", Language: model.LangCSharp},
	}
	resolveCS(t, s, nodes, refs)
	edges := collectEdges(t, s, "kennelM")
	if !hasEdge(edges, "kennelM", "service", model.EdgeInstantiates) {
		t.Errorf("expected instantiates edge kennelM→Service via alias, got %+v", edges)
	}
}

// dotted call recv.Method → resolves to the method node.
func TestCSharpMethodCall(t *testing.T) {
	s := newCSStore(t)
	nodes := []model.Node{
		csNode("speak", model.KindMethod, "Speak", "N::Dog::Speak", "Models.cs"),
		csNode("desc", model.KindMethod, "Describe", "N::Service::Describe", "Service.cs"),
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "desc", ReferenceName: "d.Speak", ReferenceKind: model.EdgeCalls, FilePath: "Service.cs", Language: model.LangCSharp},
	}
	resolveCS(t, s, nodes, refs)
	edges := collectEdges(t, s, "desc")
	if !hasEdge(edges, "desc", "speak", model.EdgeCalls) {
		t.Errorf("expected calls edge desc→Speak, got %+v", edges)
	}
}

// using `imports` ref targets the real cross-file definition when one exists.
func TestCSharpImportsThroughToDefinition(t *testing.T) {
	s := newCSStore(t)
	nodes := []model.Node{
		csNode("dog", model.KindClass, "Dog", "N::Dog", "Models.cs"),
		csNode("fileB", model.KindFile, "Service.cs", "Service.cs", "Service.cs"),
		csNode("dogImport", model.KindImport, "Dog", "", "Service.cs"),
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "fileB", ReferenceName: "Dog", ReferenceKind: model.EdgeImports, FilePath: "Service.cs", Language: model.LangCSharp},
	}
	resolveCS(t, s, nodes, refs)
	edges := collectEdges(t, s, "fileB")
	if !hasEdge(edges, "fileB", "dog", model.EdgeImports) {
		t.Errorf("expected imports edge fileB→Dog (real def), got %+v", edges)
	}
}

// bare call promotes to instantiates when name resolves to a class.
func TestCSharpBareCallClassPromotion(t *testing.T) {
	s := newCSStore(t)
	nodes := []model.Node{
		csNode("greeter", model.KindClass, "Greeter", "N::Greeter", "Models.cs"),
		csNode("m", model.KindMethod, "M", "N::C::M", "Other.cs"),
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "m", ReferenceName: "Greeter", ReferenceKind: model.EdgeInstantiates, FilePath: "Other.cs", Language: model.LangCSharp},
	}
	resolveCS(t, s, nodes, refs)
	edges := collectEdges(t, s, "m")
	if !hasEdge(edges, "m", "greeter", model.EdgeInstantiates) {
		t.Errorf("expected instantiates edge m→Greeter, got %+v", edges)
	}
}

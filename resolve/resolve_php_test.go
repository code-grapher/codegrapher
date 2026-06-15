package resolve_test

import (
	"path/filepath"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/resolve"
	"github.com/specscore/codegrapher/store"
)

func newPhpStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Initialize(filepath.Join(t.TempDir(), store.DatabaseFilename))
	if err != nil {
		t.Fatalf("store.Initialize: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func phpNode(id string, kind model.NodeKind, name, qual, file string) model.Node {
	return model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: qual,
		FilePath:      file,
		Language:      model.LangPHP,
	}
}

func phpNodeSig(id string, kind model.NodeKind, name, qual, file, sig string) model.Node {
	n := phpNode(id, kind, name, qual, file)
	n.Signature = sig
	return n
}

// class Dog extends Animal implements Speaker → extends/implements resolve.
func TestPhpResolveExtendsImplements(t *testing.T) {
	s := newPhpStore(t)
	nodes := []model.Node{
		phpNode("animal", model.KindClass, "Animal", "App::Animals::Animal", "animal.php"),
		phpNode("speaker", model.KindInterface, "Speaker", "App::Animals::Speaker", "speaker.php"),
		phpNode("dog", model.KindClass, "Dog", "App::Animals::Dog", "dog.php"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "dog", ReferenceName: "Animal", ReferenceKind: model.EdgeExtends, FilePath: "dog.php", Language: model.LangPHP},
		{FromNodeID: "dog", ReferenceName: "Speaker", ReferenceKind: model.EdgeImplements, FilePath: "dog.php", Language: model.LangPHP},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "dog")
	if !hasEdge(edges, "dog", "animal", model.EdgeExtends) {
		t.Errorf("expected extends dog→animal, got %+v", edges)
	}
	if !hasEdge(edges, "dog", "speaker", model.EdgeImplements) {
		t.Errorf("expected implements dog→speaker, got %+v", edges)
	}
}

// trait-use → implements resolves to the trait node.
func TestPhpResolveTraitUse(t *testing.T) {
	s := newPhpStore(t)
	nodes := []model.Node{
		phpNode("named", model.KindTrait, "Named", "Named", "named.php"),
		phpNode("dog", model.KindClass, "Dog", "Dog", "dog.php"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "dog", ReferenceName: "Named", ReferenceKind: model.EdgeImplements, FilePath: "dog.php", Language: model.LangPHP},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "dog")
	if !hasEdge(edges, "dog", "named", model.EdgeImplements) {
		t.Errorf("expected implements dog→named, got %+v", edges)
	}
}

// use App\Dog; new Dog() → instantiates resolves THROUGH the use import to the
// real cross-file class.
func TestPhpResolveNewThroughUseImport(t *testing.T) {
	s := newPhpStore(t)
	nodes := []model.Node{
		phpNode("dog", model.KindClass, "Dog", "App::Animals::Dog", "dog.php"),
		phpNode("dogimport", model.KindImport, "Dog", "Dog", "main.php"),
		phpNode("run", model.KindFunction, "run", "run", "main.php"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "run", ReferenceName: "Dog", ReferenceKind: model.EdgeInstantiates, FilePath: "main.php", Language: model.LangPHP},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "dog", model.EdgeInstantiates) {
		t.Errorf("expected instantiates run→dog, got %+v", edges)
	}
}

// $d = new Dog(); $d->speak() → call resolves to Dog::speak via var→class inference.
func TestPhpResolveVarTypeInference(t *testing.T) {
	s := newPhpStore(t)
	nodes := []model.Node{
		phpNode("dog", model.KindClass, "Dog", "App::Animals::Dog", "dog.php"),
		phpNode("speak", model.KindMethod, "speak", "App::Animals::Dog::speak", "dog.php"),
		phpNodeSig("vard", model.KindVariable, "d", "run::d", "main.php", `= Dog("rex")`),
		phpNode("run", model.KindFunction, "run", "run", "main.php"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "run", ReferenceName: "d.speak", ReferenceKind: model.EdgeCalls, FilePath: "main.php", Language: model.LangPHP},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "speak", model.EdgeCalls) {
		t.Errorf("expected calls run→Dog::speak, got %+v", edges)
	}
}

// Logger::log() (scoped call) → calls resolves to Logger::log.
func TestPhpResolveScopedCall(t *testing.T) {
	s := newPhpStore(t)
	nodes := []model.Node{
		phpNode("logger", model.KindClass, "Logger", "Logger", "logger.php"),
		phpNode("log", model.KindMethod, "log", "Logger::log", "logger.php"),
		phpNode("run", model.KindFunction, "run", "run", "main.php"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "run", ReferenceName: "Logger.log", ReferenceKind: model.EdgeCalls, FilePath: "main.php", Language: model.LangPHP},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "run")
	if !hasEdge(edges, "run", "log", model.EdgeCalls) {
		t.Errorf("expected calls run→Logger::log, got %+v", edges)
	}
}

// use import ref → imports resolves to the real cross-file class definition.
func TestPhpResolveImportsRef(t *testing.T) {
	s := newPhpStore(t)
	nodes := []model.Node{
		phpNode("dog", model.KindClass, "Dog", "App::Animals::Dog", "dog.php"),
		phpNode("dogimport", model.KindImport, "Dog", "Dog", "main.php"),
		phpNode("file", model.KindFile, "main.php", "main.php", "main.php"),
	}
	if err := s.InsertNodes(nodes); err != nil {
		t.Fatalf("InsertNodes: %v", err)
	}
	refs := []model.UnresolvedReference{
		{FromNodeID: "file", ReferenceName: "Dog", ReferenceKind: model.EdgeImports, FilePath: "main.php", Language: model.LangPHP},
	}
	if err := s.InsertUnresolvedRefs(refs); err != nil {
		t.Fatalf("InsertUnresolvedRefs: %v", err)
	}
	if _, err := resolve.Resolve(s, t.TempDir()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	edges := collectEdges(t, s, "file")
	if !hasEdge(edges, "file", "dog", model.EdgeImports) {
		t.Errorf("expected imports file→dog, got %+v", edges)
	}
}

package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractFSharpRes(t *testing.T, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile("mod.fs", []byte(src), model.LangFSharp)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func fsHasRef(res model.ExtractionResult, name string, kind model.EdgeKind) bool {
	for _, r := range res.UnresolvedReferences {
		if r.ReferenceName == name && r.ReferenceKind == kind {
			return true
		}
	}
	return false
}

func TestFSharpFileNodeEmitted(t *testing.T) {
	res := extractFSharpRes(t, "module M\nlet x = 1\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangFSharp {
		t.Fatalf("file node language = %q, want fsharp", res.Nodes[0].Language)
	}
}

func TestFSharpModuleAndFunction(t *testing.T) {
	res := extractFSharpRes(t, `module Shapes
let area w h = w * h
`)
	if findNode(res, model.KindModule, "Shapes") == nil {
		t.Fatal("module Shapes not found")
	}
	fn := findNode(res, model.KindFunction, "area")
	if fn == nil {
		t.Fatal("function area not found")
	}
	if fn.QualifiedName != "Shapes::area" {
		t.Errorf("area qualified name = %q", fn.QualifiedName)
	}
}

func TestFSharpNamespaceAndModule(t *testing.T) {
	res := extractFSharpRes(t, `namespace A.B
module Shapes =
  let run () = ()
`)
	if findNode(res, model.KindNamespace, "A.B") == nil {
		t.Fatal("namespace A.B not found")
	}
	m := findNode(res, model.KindModule, "Shapes")
	if m == nil || m.QualifiedName != "A.B::Shapes" {
		t.Errorf("module Shapes qn = %v", m)
	}
}

func TestFSharpRecord(t *testing.T) {
	res := extractFSharpRes(t, `module M
type Point = { X: int; Y: int }
`)
	if findNode(res, model.KindStruct, "Point") == nil {
		t.Fatal("record Point should be a struct")
	}
	if findNode(res, model.KindField, "X") == nil || findNode(res, model.KindField, "Y") == nil {
		t.Error("record fields X/Y missing")
	}
}

func TestFSharpUnion(t *testing.T) {
	res := extractFSharpRes(t, `module M
type Color = Red | Green | Blue of int
`)
	if findNode(res, model.KindEnum, "Color") == nil {
		t.Fatal("union Color should be an enum")
	}
	for _, c := range []string{"Red", "Green", "Blue"} {
		if findNode(res, model.KindEnumMember, c) == nil {
			t.Errorf("enum member %s missing", c)
		}
	}
}

func TestFSharpAbstractInterface(t *testing.T) {
	res := extractFSharpRes(t, `module M
type Shape =
  abstract Area: float
`)
	if findNode(res, model.KindInterface, "Shape") == nil {
		t.Fatal("abstract type Shape should be an interface")
	}
}

func TestFSharpClassWithMembers(t *testing.T) {
	res := extractFSharpRes(t, `module M
type Dog(name: string) =
  member _.Name = name
  member this.Bark() = ()
`)
	c := findNode(res, model.KindClass, "Dog")
	if c == nil {
		t.Fatal("Dog should be a class")
	}
	if findNode(res, model.KindProperty, "Name") == nil {
		t.Error("property Name missing")
	}
	m := findNode(res, model.KindMethod, "Bark")
	if m == nil {
		t.Error("method Bark missing")
	}
	if findNode(res, model.KindField, "name") == nil {
		t.Error("ctor field name missing")
	}
}

func TestFSharpConstantVsVariable(t *testing.T) {
	res := extractFSharpRes(t, `module M
let MAX = 100
let lower = 5
`)
	if findNode(res, model.KindConstant, "MAX") == nil {
		t.Error("MAX should be a constant")
	}
	if findNode(res, model.KindVariable, "lower") == nil {
		t.Error("lower should be a variable")
	}
}

func TestFSharpCallAndInstantiate(t *testing.T) {
	res := extractFSharpRes(t, `module M
type Dog(name: string) =
  member this.Bark() = ()
let make () = Dog("rex")
let run () =
  let d = Dog("x")
  d.Bark()
`)
	if !fsHasRef(res, "Dog", model.EdgeInstantiates) {
		t.Error("expected instantiates ref for Dog")
	}
	if !fsHasRef(res, "d.Bark", model.EdgeCalls) {
		t.Error("expected call ref for d.Bark")
	}
}

func TestFSharpInheritAndInterface(t *testing.T) {
	res := extractFSharpRes(t, `module M
type Dog(name: string) =
  inherit System.Object()
  interface IDisposable with
    member _.Dispose() = ()
`)
	if !fsHasRef(res, "Object", model.EdgeExtends) {
		t.Error("expected extends ref for Object")
	}
	if !fsHasRef(res, "IDisposable", model.EdgeImplements) {
		t.Error("expected implements ref for IDisposable")
	}
}

func TestFSharpOpenImport(t *testing.T) {
	res := extractFSharpRes(t, `module M
open System.Collections
`)
	if findNode(res, model.KindImport, "Collections") == nil {
		t.Error("open import node missing")
	}
	if !fsHasRef(res, "System.Collections", model.EdgeImports) {
		t.Error("expected imports ref")
	}
}

func TestFSharpBuiltinCallSkipped(t *testing.T) {
	// printfn should be emitted as a call ref by the walker, but the resolver
	// skips it — here we just verify the walker emits the application without
	// crashing and that this/self receivers are stripped.
	res := extractFSharpRes(t, `module M
type C() =
  member this.Run() = this.Helper()
  member _.Helper() = ()
`)
	if !fsHasRef(res, "Helper", model.EdgeCalls) {
		t.Error("expected this.Helper stripped to Helper call ref")
	}
}

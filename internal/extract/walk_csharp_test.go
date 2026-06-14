package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractCSRes(t *testing.T, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile("M.cs", []byte(src), model.LangCSharp)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestCSFileNodeEmitted(t *testing.T) {
	res := extractCSRes(t, "class C {}\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangCSharp {
		t.Fatalf("file node language = %q, want csharp", res.Nodes[0].Language)
	}
}

func TestCSNamespaceAndClass(t *testing.T) {
	res := extractCSRes(t, `namespace App.Models
{
    public class Animal { }
}
`)
	ns := findNode(res, model.KindNamespace, "App.Models")
	if ns == nil {
		t.Fatal("namespace App.Models not found")
	}
	cls := findNode(res, model.KindClass, "Animal")
	if cls == nil {
		t.Fatal("class Animal not found")
	}
	if cls.QualifiedName != "App.Models::Animal" {
		t.Errorf("qualifiedName = %q, want App.Models::Animal", cls.QualifiedName)
	}
	if !cls.IsExported {
		t.Error("public class should be exported")
	}
}

func TestCSFileScopedNamespace(t *testing.T) {
	res := extractCSRes(t, `namespace App.Util;

public class Helper { }
`)
	ns := findNode(res, model.KindNamespace, "App.Util")
	if ns == nil {
		t.Fatal("file-scoped namespace not found")
	}
	cls := findNode(res, model.KindClass, "Helper")
	if cls == nil {
		t.Fatal("class Helper not found")
	}
	if cls.QualifiedName != "App.Util::Helper" {
		t.Errorf("qualifiedName = %q, want App.Util::Helper", cls.QualifiedName)
	}
}

func TestCSStructInterfaceRecordEnum(t *testing.T) {
	res := extractCSRes(t, `interface IFoo { }
struct Vec { }
record Point(int X, int Y);
enum Color { Red, Green }
`)
	if findNode(res, model.KindInterface, "IFoo") == nil {
		t.Error("interface IFoo missing")
	}
	if findNode(res, model.KindStruct, "Vec") == nil {
		t.Error("struct Vec missing")
	}
	if findNode(res, model.KindClass, "Point") == nil {
		t.Error("record Point should be a class")
	}
	if findNode(res, model.KindEnum, "Color") == nil {
		t.Error("enum Color missing")
	}
	if findNode(res, model.KindEnumMember, "Red") == nil {
		t.Error("enum member Red missing")
	}
	if findNode(res, model.KindEnumMember, "Green") == nil {
		t.Error("enum member Green missing")
	}
}

func TestCSMethodFlags(t *testing.T) {
	res := extractCSRes(t, `class C {
    public async Task<string> Fetch() { return null; }
    private static void Helper() { }
    public abstract void Speak();
}
`)
	fetch := findNode(res, model.KindMethod, "Fetch")
	if fetch == nil {
		t.Fatal("method Fetch missing")
	}
	if !fetch.IsAsync {
		t.Error("Fetch should be async")
	}
	if !fetch.IsExported {
		t.Error("Fetch should be exported (public)")
	}
	if fetch.ReturnType != "Task<string>" {
		t.Errorf("returnType = %q", fetch.ReturnType)
	}
	helper := findNode(res, model.KindMethod, "Helper")
	if helper == nil || !helper.IsStatic {
		t.Error("Helper should be static")
	}
	speak := findNode(res, model.KindMethod, "Speak")
	if speak == nil || !speak.IsAbstract {
		t.Error("Speak should be abstract")
	}
}

func TestCSPropertyAndFields(t *testing.T) {
	res := extractCSRes(t, `class C {
    public string Name { get; set; }
    private const int Legs = 4;
    public int A, B;
}
`)
	if findNode(res, model.KindProperty, "Name") == nil {
		t.Error("property Name missing")
	}
	legs := findNode(res, model.KindConstant, "Legs")
	if legs == nil {
		t.Error("const Legs should be a constant")
	}
	if findNode(res, model.KindField, "A") == nil || findNode(res, model.KindField, "B") == nil {
		t.Error("fields A and B missing")
	}
}

func TestCSAttributesDecorate(t *testing.T) {
	res := extractCSRes(t, `[Serializable]
[Route("api")]
public class C { }
`)
	cls := findNode(res, model.KindClass, "C")
	if cls == nil {
		t.Fatal("class C missing")
	}
	if len(cls.Decorators) != 2 || cls.Decorators[0] != "Serializable" || cls.Decorators[1] != "Route" {
		t.Errorf("decorators = %v, want [Serializable Route]", cls.Decorators)
	}
	if !refFrom(res, cls.ID, "Serializable", model.EdgeDecorates) {
		t.Error("missing decorates ref to Serializable")
	}
	if !refFrom(res, cls.ID, "Route", model.EdgeDecorates) {
		t.Error("missing decorates ref to Route")
	}
}

func TestCSBaseListExtends(t *testing.T) {
	res := extractCSRes(t, `class Dog : Animal, IGreeter { }`)
	cls := findNode(res, model.KindClass, "Dog")
	if cls == nil {
		t.Fatal("class Dog missing")
	}
	if !refFrom(res, cls.ID, "Animal", model.EdgeExtends) {
		t.Error("missing extends ref to Animal")
	}
	if !refFrom(res, cls.ID, "IGreeter", model.EdgeExtends) {
		t.Error("missing base ref to IGreeter (resolver reclassifies to implements)")
	}
}

func TestCSUsingImport(t *testing.T) {
	res := extractCSRes(t, `using System;
using System.Collections.Generic;
using Alias = Foo.Bar.Baz;

namespace N { }
`)
	if findNode(res, model.KindImport, "System") == nil {
		t.Error("import System missing")
	}
	if findNode(res, model.KindImport, "Generic") == nil {
		t.Error("import Generic (last segment) missing")
	}
	if findNode(res, model.KindImport, "Alias") == nil {
		t.Error("alias import Alias missing")
	}
	if !hasRef(res, "System", model.EdgeImports) {
		t.Error("missing imports ref for System")
	}
}

func TestCSCallsAndInstantiates(t *testing.T) {
	res := extractCSRes(t, `class C {
    void M() {
        var g = new Greeter();
        g.Greet();
        Helper();
        new Foo().Bar();
    }
}
`)
	m := findNode(res, model.KindMethod, "M")
	if m == nil {
		t.Fatal("method M missing")
	}
	if !refFrom(res, m.ID, "Greeter", model.EdgeInstantiates) {
		t.Error("missing instantiates ref to Greeter")
	}
	if !refFrom(res, m.ID, "Foo", model.EdgeInstantiates) {
		t.Error("missing instantiates ref to Foo")
	}
	if !refFrom(res, m.ID, "g.Greet", model.EdgeCalls) {
		t.Error("missing calls ref g.Greet")
	}
	if !refFrom(res, m.ID, "Helper", model.EdgeCalls) {
		t.Error("missing calls ref Helper")
	}
	if !refFrom(res, m.ID, "Bar", model.EdgeCalls) {
		t.Error("missing calls ref Bar")
	}
}

func TestCSContainsEdges(t *testing.T) {
	res := extractCSRes(t, `namespace N { class C { void M() { } } }`)
	ns := findNode(res, model.KindNamespace, "N")
	cls := findNode(res, model.KindClass, "C")
	m := findNode(res, model.KindMethod, "M")
	if ns == nil || cls == nil || m == nil {
		t.Fatal("missing nodes")
	}
	want := map[string]string{ns.ID: cls.ID, cls.ID: m.ID}
	got := map[string]string{}
	for _, e := range res.Edges {
		if e.Kind == model.EdgeContains {
			got[e.Source] = e.Target
		}
	}
	for src, tgt := range want {
		if got[src] != tgt {
			t.Errorf("missing contains edge %s -> %s", src, tgt)
		}
	}
}

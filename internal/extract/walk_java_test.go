package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractJavaRes(t *testing.T, file, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile(file, []byte(src), model.LangJava)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestJavaFileNodeEmitted(t *testing.T) {
	res := extractJavaRes(t, "C.java", "class C {}\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangJava {
		t.Fatalf("file node language = %q, want java", res.Nodes[0].Language)
	}
}

// 1. Package → namespace
func TestJavaPackage(t *testing.T) {
	res := extractJavaRes(t, "C.java", "package com.example.app;\nclass C {}\n")
	if findNode(res, model.KindNamespace, "com.example.app") == nil {
		t.Error("package namespace node not found")
	}
}

// 2. Imports (type + wildcard)
func TestJavaImports(t *testing.T) {
	res := extractJavaRes(t, "C.java", `package p;
import com.example.geom.Point;
import com.example.util.*;
class C {}
`)
	if findNode(res, model.KindImport, "Point") == nil {
		t.Error("type import Point not found")
	}
	if findNode(res, model.KindImport, "com.example.util") == nil {
		t.Error("wildcard import not found")
	}
	if !hasRef(res, "Point", model.EdgeImports) {
		t.Error("imports ref Point not found")
	}
}

// 3. Interface + method
func TestJavaInterface(t *testing.T) {
	res := extractJavaRes(t, "Shape.java", `public interface Shape {
    double area();
}
`)
	in := findNode(res, model.KindInterface, "Shape")
	if in == nil {
		t.Fatal("interface Shape not found")
	}
	if !in.IsExported {
		t.Error("public interface should be exported")
	}
	if findNode(res, model.KindMethod, "area") == nil {
		t.Error("interface method area not found")
	}
}

// 4. Class with extends + implements + @Override method + field
func TestJavaClassExtendsImplements(t *testing.T) {
	res := extractJavaRes(t, "Circle.java", `public class Circle extends Base implements Shape {
    private final double radius;
    @Override
    public double area() { return 0; }
}
`)
	cn := findNode(res, model.KindClass, "Circle")
	if cn == nil {
		t.Fatal("class Circle not found")
	}
	if !refFrom(res, cn.ID, "Base", model.EdgeExtends) {
		t.Error("missing extends Base")
	}
	if !refFrom(res, cn.ID, "Shape", model.EdgeImplements) {
		t.Error("missing implements Shape")
	}
	f := findNode(res, model.KindField, "radius")
	if f == nil {
		t.Fatal("field radius not found")
	}
	if f.QualifiedName != "Circle::radius" {
		t.Errorf("field qualified name = %q", f.QualifiedName)
	}
	m := findNode(res, model.KindMethod, "area")
	if m == nil {
		t.Fatal("method area not found")
	}
	if len(m.Decorators) != 1 || m.Decorators[0] != "Override" {
		t.Errorf("decorators = %v, want [Override]", m.Decorators)
	}
	if !refFrom(res, m.ID, "Override", model.EdgeDecorates) {
		t.Error("missing decorates Override")
	}
}

// 5. static final field → constant
func TestJavaConstant(t *testing.T) {
	res := extractJavaRes(t, "C.java", `class C {
    public static final double PI = 3.14;
    private int count;
}
`)
	if findNode(res, model.KindConstant, "PI") == nil {
		t.Error("PI should be a constant")
	}
	if findNode(res, model.KindField, "count") == nil {
		t.Error("count should be a field")
	}
	if findNode(res, model.KindConstant, "count") != nil {
		t.Error("count should not be a constant")
	}
}

// 6. Enum + members
func TestJavaEnum(t *testing.T) {
	res := extractJavaRes(t, "Size.java", `public enum Size {
    SMALL, LARGE;
}
`)
	if findNode(res, model.KindEnum, "Size") == nil {
		t.Error("enum Size not found")
	}
	if findNode(res, model.KindEnumMember, "SMALL") == nil {
		t.Error("enum member SMALL not found")
	}
	if findNode(res, model.KindEnumMember, "LARGE") == nil {
		t.Error("enum member LARGE not found")
	}
}

// 7. Calls + new (instantiation)
func TestJavaCallsAndNew(t *testing.T) {
	res := extractJavaRes(t, "C.java", `class C {
    void run() {
        Point p = new Point(1, 2);
        helper();
        obj.doThing();
        this.local();
    }
}
`)
	m := findNode(res, model.KindMethod, "run")
	if m == nil {
		t.Fatal("method run not found")
	}
	if !refFrom(res, m.ID, "Point", model.EdgeInstantiates) {
		t.Error("missing instantiates Point")
	}
	if !refFrom(res, m.ID, "helper", model.EdgeCalls) {
		t.Error("missing call helper")
	}
	if !refFrom(res, m.ID, "obj.doThing", model.EdgeCalls) {
		t.Error("missing call obj.doThing")
	}
	if !refFrom(res, m.ID, "local", model.EdgeCalls) {
		t.Error("this.local should strip receiver → local")
	}
	if findNode(res, model.KindVariable, "p") == nil {
		t.Error("local variable p not found")
	}
}

// 8. Constructor → method
func TestJavaConstructor(t *testing.T) {
	res := extractJavaRes(t, "C.java", `class C {
    public C(int x) {}
}
`)
	m := findNode(res, model.KindMethod, "C")
	if m == nil {
		t.Fatal("constructor C not found")
	}
	if m.QualifiedName != "C::C" {
		t.Errorf("constructor qualified name = %q", m.QualifiedName)
	}
}

// 9. Interface extends interface
func TestJavaInterfaceExtends(t *testing.T) {
	res := extractJavaRes(t, "Sub.java", `public interface Sub extends Base {
}
`)
	in := findNode(res, model.KindInterface, "Sub")
	if in == nil {
		t.Fatal("interface Sub not found")
	}
	if !refFrom(res, in.ID, "Base", model.EdgeExtends) {
		t.Error("missing extends Base for interface")
	}
}

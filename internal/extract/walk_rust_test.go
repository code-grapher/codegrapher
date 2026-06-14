package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractRsRes(t *testing.T, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile("mod.rs", []byte(src), model.LangRust)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestRsFileNodeEmitted(t *testing.T) {
	res := extractRsRes(t, "fn main() {}\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangRust {
		t.Fatalf("file node language = %q, want rust", res.Nodes[0].Language)
	}
}

// 1. Module → KindModule, nested items qualified under it.
func TestRsModule(t *testing.T) {
	res := extractRsRes(t, `pub mod geo {
    pub struct Point {}
}
`)
	mn := findNode(res, model.KindModule, "geo")
	if mn == nil {
		t.Fatal("module geo not found")
	}
	if !mn.IsExported {
		t.Error("pub mod should be exported")
	}
	sn := findNode(res, model.KindStruct, "Point")
	if sn == nil || sn.QualifiedName != "geo::Point" {
		t.Errorf("struct Point qualified = %v", sn)
	}
}

// 2. Trait → KindInterface with method signatures.
func TestRsTrait(t *testing.T) {
	res := extractRsRes(t, `pub trait Shape {
    fn area(&self) -> f64;
    fn label(&self) -> String;
}
`)
	tn := findNode(res, model.KindInterface, "Shape")
	if tn == nil {
		t.Fatal("trait Shape (interface) not found")
	}
	m := findNode(res, model.KindMethod, "area")
	if m == nil || m.QualifiedName != "Shape::area" {
		t.Errorf("trait method area qualified = %v", m)
	}
}

// 3. Struct with fields.
func TestRsStructFields(t *testing.T) {
	res := extractRsRes(t, `pub struct Circle {
    pub radius: f64,
    center: f64,
}
`)
	sn := findNode(res, model.KindStruct, "Circle")
	if sn == nil {
		t.Fatal("struct Circle not found")
	}
	rf := findNode(res, model.KindField, "radius")
	if rf == nil || rf.QualifiedName != "Circle::radius" {
		t.Errorf("field radius qualified = %v", rf)
	}
	if findNode(res, model.KindField, "center") == nil {
		t.Error("field center not found")
	}
}

// 4. Enum with variants.
func TestRsEnum(t *testing.T) {
	res := extractRsRes(t, `pub enum Color {
    Red,
    Green,
    Custom(u8),
}
`)
	en := findNode(res, model.KindEnum, "Color")
	if en == nil {
		t.Fatal("enum Color not found")
	}
	for _, v := range []string{"Red", "Green", "Custom"} {
		m := findNode(res, model.KindEnumMember, v)
		if m == nil {
			t.Errorf("enum variant %q not found", v)
			continue
		}
		if m.QualifiedName != "Color::"+v {
			t.Errorf("variant %q qualified = %q", v, m.QualifiedName)
		}
	}
}

// 5. inherent impl: methods attach to the type as Type::method.
func TestRsInherentImpl(t *testing.T) {
	res := extractRsRes(t, `pub struct Circle { pub radius: f64 }
impl Circle {
    pub fn new(radius: f64) -> Circle { Circle { radius } }
    pub fn diameter(&self) -> f64 { self.radius * 2.0 }
}
`)
	nw := findNode(res, model.KindMethod, "new")
	if nw == nil || nw.QualifiedName != "Circle::new" {
		t.Errorf("Circle::new method = %v", nw)
	}
	if findNode(res, model.KindMethod, "diameter") == nil {
		t.Error("diameter method not found")
	}
	// new builds a Circle struct literal → instantiates.
	if !refFrom(res, nw.ID, "Circle", model.EdgeInstantiates) {
		t.Error("Circle::new should instantiate Circle (struct literal)")
	}
}

// 6. trait impl: implements (Type→Trait) + overrides (Type::m→Trait::m).
func TestRsTraitImpl(t *testing.T) {
	res := extractRsRes(t, `pub trait Shape { fn area(&self) -> f64; }
pub struct Circle { pub radius: f64 }
impl Shape for Circle {
    fn area(&self) -> f64 { 3.14 }
}
`)
	cn := findNode(res, model.KindStruct, "Circle")
	if cn == nil {
		t.Fatal("struct Circle not found")
	}
	if !refFrom(res, cn.ID, "Shape", model.EdgeImplements) {
		t.Error("impl Shape for Circle should emit implements Circle→Shape")
	}
	// overrides ref from Circle::area → Shape::area.
	am := findRustNodeQN(res, model.KindMethod, "area", "Circle::area")
	if am == nil {
		t.Fatal("Circle::area method not found")
	}
	if !refFrom(res, am.ID, "Shape::area", model.EdgeOverrides) {
		t.Error("Circle::area should override Shape::area")
	}
}

// 7. use declarations → imports (path, list, alias, wildcard).
func TestRsUse(t *testing.T) {
	res := extractRsRes(t, `use crate::shapes::Circle;
use crate::shapes::{Square, Triangle as Tri};
use std::collections::*;
`)
	for _, n := range []string{"Circle", "Square", "Tri", "collections"} {
		if findNode(res, model.KindImport, n) == nil {
			t.Errorf("import %q not found", n)
		}
		if !hasRef(res, n, model.EdgeImports) {
			t.Errorf("imports ref %q missing", n)
		}
	}
}

// 8. const / static / type alias.
func TestRsConstTypeAlias(t *testing.T) {
	res := extractRsRes(t, `pub const PI: f64 = 3.14;
static COUNT: u32 = 0;
pub type Radius = f64;
`)
	if findNode(res, model.KindConstant, "PI") == nil {
		t.Error("const PI not found")
	}
	if findNode(res, model.KindConstant, "COUNT") == nil {
		t.Error("static COUNT not found")
	}
	if findNode(res, model.KindTypeAlias, "Radius") == nil {
		t.Error("type alias Radius not found")
	}
}

// 9. calls: scoped assoc call, method call (receiver stripped), free call.
func TestRsCalls(t *testing.T) {
	res := extractRsRes(t, `fn run() {
    let c = Circle::new(2.0);
    let a = c.area();
    helper();
}
`)
	fn := findNode(res, model.KindFunction, "run")
	if fn == nil {
		t.Fatal("run not found")
	}
	if !refFrom(res, fn.ID, "Circle::new", model.EdgeCalls) {
		t.Error("missing scoped call Circle::new")
	}
	if !refFrom(res, fn.ID, "area", model.EdgeCalls) {
		t.Error("c.area() should strip receiver → area")
	}
	if !refFrom(res, fn.ID, "helper", model.EdgeCalls) {
		t.Error("missing free call helper")
	}
}

// 10. async fn flag + signature.
func TestRsAsyncFn(t *testing.T) {
	res := extractRsRes(t, `pub async fn fetch() -> u32 { 0 }
`)
	fn := findNode(res, model.KindFunction, "fetch")
	if fn == nil {
		t.Fatal("fetch not found")
	}
	if !fn.IsAsync {
		t.Error("async fn should set isAsync")
	}
	if fn.Signature != "async fn fetch() -> u32" {
		t.Errorf("signature = %q", fn.Signature)
	}
}

// findNodeQN finds a node by kind, name, and qualified name.
func findRustNodeQN(res model.ExtractionResult, kind model.NodeKind, name, qn string) *model.Node {
	for i := range res.Nodes {
		n := &res.Nodes[i]
		if n.Kind == kind && n.Name == name && n.QualifiedName == qn {
			return n
		}
	}
	return nil
}

package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractJlRes(t *testing.T, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile("mod.jl", []byte(src), model.LangJulia)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestJlFileNodeEmitted(t *testing.T) {
	res := extractJlRes(t, "x = 1\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangJulia {
		t.Fatalf("file node language = %q, want julia", res.Nodes[0].Language)
	}
}

// Module, abstract type, struct + supertype, fields, function.
func TestJlModuleStructAbstract(t *testing.T) {
	res := extractJlRes(t, `module Shapes
abstract type Shape end
struct Circle <: Shape
  r::Float64
end
area(c::Circle) = 3.14*c.r
end
`)
	mn := findNode(res, model.KindModule, "Shapes")
	if mn == nil {
		t.Fatal("module Shapes not found")
	}
	ab := findNode(res, model.KindInterface, "Shape")
	if ab == nil || ab.QualifiedName != "Shapes::Shape" {
		t.Fatalf("abstract Shape = %v", ab)
	}
	st := findNode(res, model.KindStruct, "Circle")
	if st == nil || st.QualifiedName != "Shapes::Circle" {
		t.Fatalf("struct Circle = %v", st)
	}
	if !refFrom(res, st.ID, "Shape", model.EdgeExtends) {
		t.Error("missing extends Circle<:Shape")
	}
	fld := findNode(res, model.KindField, "r")
	if fld == nil || fld.QualifiedName != "Shapes::Circle::r" {
		t.Fatalf("field r = %v", fld)
	}
	if !refFrom(res, fld.ID, "Float64", model.EdgeReferences) {
		t.Error("missing field-type reference Float64")
	}
	fn := findNode(res, model.KindFunction, "area")
	if fn == nil || fn.QualifiedName != "Shapes::area" {
		t.Fatalf("short-form function area = %v", fn)
	}
	if !refFrom(res, fn.ID, "Circle", model.EdgeReferences) {
		t.Error("missing param-type reference Circle")
	}
}

// Long-form function, const, variable, using/import, calls.
func TestJlImportsConstCalls(t *testing.T) {
	res := extractJlRes(t, `module Geometry
using Shapes
import Shapes: area
const PI = 3.14
scale = 2.0
function run()
  c = Circle(1.0)
  area(c)
  Shapes.area(c)
end
end
`)
	if findNode(res, model.KindImport, "Shapes") == nil {
		t.Error("using Shapes import node missing")
	}
	if findNode(res, model.KindImport, "area") == nil {
		t.Error("import Shapes: area node missing")
	}
	if findNode(res, model.KindConstant, "PI") == nil {
		t.Error("const PI missing")
	}
	if findNode(res, model.KindVariable, "scale") == nil {
		t.Error("variable scale missing")
	}
	fn := findNode(res, model.KindFunction, "run")
	if fn == nil {
		t.Fatal("function run missing")
	}
	if !refFrom(res, fn.ID, "Circle", model.EdgeCalls) {
		t.Error("missing call Circle(1.0)")
	}
	if !refFrom(res, fn.ID, "area", model.EdgeCalls) {
		t.Error("missing bare call area(c)")
	}
	if !refFrom(res, fn.ID, "Shapes.area", model.EdgeCalls) {
		t.Error("missing qualified call Shapes.area(c)")
	}
}

// Multiple dispatch: two methods of the same name collapse to one node.
func TestJlMultiDispatchDedup(t *testing.T) {
	res := extractJlRes(t, `f(x::Int) = x + 1
f(x::Float64) = x + 2.0
`)
	var count int
	for _, n := range res.Nodes {
		if n.Kind == model.KindFunction && n.Name == "f" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("multi-dispatch f should collapse to 1 node, got %d", count)
	}
}

// Builtin calls (println) are skipped.
func TestJlBuiltinSkipped(t *testing.T) {
	res := extractJlRes(t, `function g()
  println("hi")
  custom(1)
end
`)
	fn := findNode(res, model.KindFunction, "g")
	if fn == nil {
		t.Fatal("g missing")
	}
	if refFrom(res, fn.ID, "println", model.EdgeCalls) {
		t.Error("println builtin should be skipped")
	}
	if !refFrom(res, fn.ID, "custom", model.EdgeCalls) {
		t.Error("custom call should be recorded")
	}
}

package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractDartRes(t *testing.T, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile("lib/m.dart", []byte(src), model.LangDart)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestDartFileNodeEmitted(t *testing.T) {
	res := extractDartRes(t, "class C {}\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangDart {
		t.Fatalf("file node language = %q, want dart", res.Nodes[0].Language)
	}
}

func TestDartClassExtendsMixinImplements(t *testing.T) {
	res := extractDartRes(t, `abstract class Shape {
  double area();
}

mixin Logger {
  void log(String m) {}
}

class Circle extends Shape with Logger implements Comparable {
  final double radius;
  static const pi = 3.14;
  Circle(this.radius);
  Circle.unit() : radius = 1.0;
  @override
  double area() {
    log('computing');
    return pi * radius * radius;
  }
  double get diameter => radius * 2;
}
`)
	shape := findNode(res, model.KindClass, "Shape")
	if shape == nil || !shape.IsAbstract {
		t.Fatalf("abstract class Shape not found/abstract: %+v", shape)
	}
	if findNode(res, model.KindClass, "Logger") == nil {
		t.Fatal("mixin Logger (KindClass) not found")
	}
	circle := findNode(res, model.KindClass, "Circle")
	if circle == nil {
		t.Fatal("class Circle not found")
	}
	if !hasRef(res, "Shape", model.EdgeExtends) {
		t.Error("missing extends Shape")
	}
	if !hasRef(res, "Logger", model.EdgeImplements) {
		t.Error("missing with-mixin Logger as implements")
	}
	if !hasRef(res, "Comparable", model.EdgeImplements) {
		t.Error("missing implements Comparable")
	}
	if findNode(res, model.KindField, "radius") == nil {
		t.Error("field radius not found")
	}
	if findNode(res, model.KindConstant, "pi") == nil {
		t.Error("const pi not found")
	}
	if findNode(res, model.KindMethod, "Circle") == nil {
		t.Error("default constructor Circle not found")
	}
	if findNode(res, model.KindMethod, "Circle.unit") == nil {
		t.Error("named constructor Circle.unit not found")
	}
	if findNode(res, model.KindMethod, "area") == nil {
		t.Error("method area not found")
	}
	if findNode(res, model.KindProperty, "diameter") == nil {
		t.Error("getter diameter not found")
	}
	if !hasRef(res, "log", model.EdgeCalls) {
		t.Error("missing call to log()")
	}
}

func TestDartPrivacyUnderscore(t *testing.T) {
	res := extractDartRes(t, `class _Hidden {
  int _count = 0;
  void _tick() {}
}
`)
	h := findNode(res, model.KindClass, "_Hidden")
	if h == nil {
		t.Fatal("class _Hidden not found")
	}
	if h.Visibility == nil || *h.Visibility != "private" {
		t.Errorf("_Hidden visibility = %v, want private", h.Visibility)
	}
	if h.IsExported {
		t.Error("_Hidden should not be exported")
	}
}

func TestDartEnumTypeAliasFunction(t *testing.T) {
	res := extractDartRes(t, `enum Color { red, green, blue }
typedef IntList = List<int>;
double topLevel(int x) => x.toDouble();
`)
	if findNode(res, model.KindEnum, "Color") == nil {
		t.Error("enum Color not found")
	}
	for _, m := range []string{"red", "green", "blue"} {
		if findNode(res, model.KindEnumMember, m) == nil {
			t.Errorf("enum member %s not found", m)
		}
	}
	if findNode(res, model.KindTypeAlias, "IntList") == nil {
		t.Error("typedef IntList not found")
	}
	if findNode(res, model.KindFunction, "topLevel") == nil {
		t.Error("top-level function topLevel not found")
	}
}

func TestDartImportsAndInstantiation(t *testing.T) {
	res := extractDartRes(t, `import 'dart:math';
import 'shape.dart';

void main() {
  var c = Circle(2.0);
  var u = Circle.unit();
  c.area();
  new Circle(3.0);
}
`)
	if findNode(res, model.KindImport, "math") == nil {
		t.Error("import dart:math (name math) not found")
	}
	if findNode(res, model.KindImport, "shape.dart") == nil {
		t.Error("import shape.dart not found")
	}
	if !hasRef(res, "shape.dart", model.EdgeImports) {
		t.Error("missing imports ref for shape.dart")
	}
	if !hasRef(res, "Circle", model.EdgeCalls) {
		t.Error("missing Circle(...) call (promoted to instantiates by resolver)")
	}
	if !hasRef(res, "Circle.unit", model.EdgeCalls) {
		t.Error("missing Circle.unit(...) named-constructor call")
	}
	if !hasRef(res, "Circle", model.EdgeInstantiates) {
		t.Error("missing new Circle(...) instantiation")
	}
}

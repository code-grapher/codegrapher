package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractCppRes(t *testing.T, name, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile(name, []byte(src), model.LangCPP)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func cppHasRef(res model.ExtractionResult, kind model.EdgeKind, name string) bool {
	for _, r := range res.UnresolvedReferences {
		if r.ReferenceKind == kind && r.ReferenceName == name {
			return true
		}
	}
	return false
}

func TestCppFileNode(t *testing.T) {
	res := extractCppRes(t, "a.cpp", "int x;\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangCPP {
		t.Fatalf("file node lang = %q, want cpp", res.Nodes[0].Language)
	}
}

// Namespace + class + base/derived + virtual override + ctor/dtor + members +
// static const + enum class + template fn.
func TestCppClassFull(t *testing.T) {
	res := extractCppRes(t, "shapes.cpp", `
namespace geo {
enum class Color { Red, Green };

class Point {
public:
    Point(double x, double y);
    double distanceTo(const Point& other) const;
    static const int dimensions = 2;
private:
    double x_;
};

class Shape {
public:
    virtual double area() const = 0;
    virtual ~Shape();
};

class Circle : public Shape {
public:
    Circle(double r);
    double area() const override;
};

template <typename T>
T scale(T v, T f) { return v * f; }
}
`)

	if findNodeQN(res, model.KindNamespace, "geo") == nil {
		t.Error("missing namespace geo")
	}
	if findNodeQN(res, model.KindEnum, "geo::Color") == nil {
		t.Error("missing enum class geo::Color")
	}
	if findNodeQN(res, model.KindEnumMember, "geo::Color::Red") == nil {
		t.Error("missing enum member Red")
	}
	if findNodeQN(res, model.KindClass, "geo::Point") == nil {
		t.Error("missing class geo::Point")
	}
	// constructor → method named like the class.
	if findNodeQN(res, model.KindMethod, "geo::Point::Point") == nil {
		t.Error("missing constructor geo::Point::Point")
	}
	if findNodeQN(res, model.KindMethod, "geo::Point::distanceTo") == nil {
		t.Error("missing method distanceTo")
	}
	// static const data member → constant.
	if findNodeQN(res, model.KindConstant, "geo::Point::dimensions") == nil {
		t.Error("missing static const dimensions")
	}
	// data member → field.
	if findNodeQN(res, model.KindField, "geo::Point::x_") == nil {
		t.Error("missing field x_")
	}
	// destructor.
	if findNodeQN(res, model.KindMethod, "geo::Shape::~Shape") == nil {
		t.Error("missing destructor ~Shape")
	}
	// pure virtual → abstract.
	pv := findNodeQN(res, model.KindMethod, "geo::Shape::area")
	if pv == nil || !pv.IsAbstract {
		t.Errorf("Shape::area should be abstract pure-virtual, got %+v", pv)
	}
	// templated function records type params.
	sc := findNodeQN(res, model.KindFunction, "geo::scale")
	if sc == nil || len(sc.TypeParameters) != 1 || sc.TypeParameters[0] != "T" {
		t.Errorf("scale type params = %+v, want [T]", sc)
	}

	// extends edge: Circle → Shape.
	if !cppHasRef(res, model.EdgeExtends, "Shape") {
		t.Error("missing extends ref Circle→Shape")
	}
	// overrides edge: Circle::area → Shape::area.
	if !cppHasRef(res, model.EdgeOverrides, "Shape::area") {
		t.Error("missing overrides ref for Circle::area")
	}
}

// struct with no methods stays a struct; struct with methods becomes a class.
func TestCppStructHeuristic(t *testing.T) {
	res := extractCppRes(t, "s.cpp", `
struct Plain { int a; double b; };
struct WithMethod { int a; int get() const; };
`)
	if findNodeQN(res, model.KindStruct, "Plain") == nil {
		t.Error("Plain should be a struct")
	}
	if findNodeQN(res, model.KindClass, "WithMethod") == nil {
		t.Error("WithMethod (has method) should be promoted to class")
	}
}

// calls, new construction, stack construction, using-namespace import.
func TestCppCallsAndConstruction(t *testing.T) {
	res := extractCppRes(t, "run.cpp", `
class Shape { public: virtual double area() const; };
class Circle : public Shape { public: double area() const override; };

double run() {
    using namespace geo;
    Circle c(2.0);
    Shape* s = new Circle(3.0);
    double a = s->area();
    double b = geo::add(1, 2);
    return a + b;
}
`)
	if !cppHasRef(res, model.EdgeInstantiates, "Circle") {
		t.Error("missing instantiates Circle (new + stack)")
	}
	if !cppHasRef(res, model.EdgeCalls, "area") {
		t.Error("missing call to area (s->area())")
	}
	if !cppHasRef(res, model.EdgeCalls, "geo::add") {
		t.Error("missing qualified call geo::add")
	}
	// using namespace → import node.
	if findNode(res, model.KindImport, "geo") == nil {
		t.Error("missing using-namespace import geo")
	}
}

// alias_declaration (using X = Y) → type alias + reference.
func TestCppAlias(t *testing.T) {
	res := extractCppRes(t, "a.cpp", `
class Circle {};
using Round = Circle;
`)
	if findNode(res, model.KindTypeAlias, "Round") == nil {
		t.Error("missing type alias Round")
	}
	if !cppHasRef(res, model.EdgeReferences, "Circle") {
		t.Error("alias should reference Circle")
	}
}

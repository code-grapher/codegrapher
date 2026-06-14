package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractPy(t *testing.T, src string) ([]model.Node, []model.Edge, []model.UnresolvedReference) {
	t.Helper()
	res, err := ExtractFile("m.py", []byte(src), model.LangPython)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return res.Nodes, res.Edges, res.UnresolvedReferences
}

func TestPyFileNodeEmitted(t *testing.T) {
	nodes, _, _ := extractPy(t, "x = 1\n")
	if len(nodes) == 0 || nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", nodes)
	}
	if nodes[0].Language != model.LangPython {
		t.Fatalf("file node language = %q, want python", nodes[0].Language)
	}
}

func extractPyRes(t *testing.T, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile("mod.py", []byte(src), model.LangPython)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func nodesByKind(res model.ExtractionResult, kind model.NodeKind) []model.Node {
	var out []model.Node
	for _, n := range res.Nodes {
		if n.Kind == kind {
			out = append(out, n)
		}
	}
	return out
}

func findNode(res model.ExtractionResult, kind model.NodeKind, name string) *model.Node {
	for i := range res.Nodes {
		if res.Nodes[i].Kind == kind && res.Nodes[i].Name == name {
			return &res.Nodes[i]
		}
	}
	return nil
}

func hasRef(res model.ExtractionResult, name string, kind model.EdgeKind) bool {
	for _, r := range res.UnresolvedReferences {
		if r.ReferenceName == name && r.ReferenceKind == kind {
			return true
		}
	}
	return false
}

func refFrom(res model.ExtractionResult, fromID, name string, kind model.EdgeKind) bool {
	for _, r := range res.UnresolvedReferences {
		if r.FromNodeID == fromID && r.ReferenceName == name && r.ReferenceKind == kind {
			return true
		}
	}
	return false
}

// 1. Functions & methods
func TestPyFunction(t *testing.T) {
	res := extractPyRes(t, `async def fetch(url, timeout):
    """get data."""
    pass
`)
	fn := findNode(res, model.KindFunction, "fetch")
	if fn == nil {
		t.Fatal("function fetch not found")
	}
	if !fn.IsAsync {
		t.Error("expected isAsync")
	}
	if fn.Docstring != "get data." {
		t.Errorf("docstring = %q", fn.Docstring)
	}
	if fn.Signature != "async def fetch(url, timeout)" {
		t.Errorf("signature = %q", fn.Signature)
	}
}

func TestPyMethod(t *testing.T) {
	res := extractPyRes(t, `class C:
    def run(self):
        pass
`)
	if findNode(res, model.KindMethod, "run") == nil {
		t.Error("method run not found")
	}
	if len(nodesByKind(res, model.KindFunction)) != 0 {
		t.Error("method should not be a function")
	}
}

// 2. Classes & inheritance
func TestPyClassInheritance(t *testing.T) {
	res := extractPyRes(t, `class Widget(Base, mixins.Mixin):
    """w."""
    pass
`)
	cn := findNode(res, model.KindClass, "Widget")
	if cn == nil {
		t.Fatal("class Widget not found")
	}
	if cn.Docstring != "w." {
		t.Errorf("docstring = %q", cn.Docstring)
	}
	if !refFrom(res, cn.ID, "Base", model.EdgeExtends) {
		t.Error("missing extends Base")
	}
	if !refFrom(res, cn.ID, "Mixin", model.EdgeExtends) {
		t.Error("missing extends Mixin (dotted reduced to last segment)")
	}
}

// 3. Decorators
func TestPyDecorators(t *testing.T) {
	res := extractPyRes(t, `@app.route("/x")
@staticmethod
def handler():
    pass
`)
	fn := findNode(res, model.KindFunction, "handler")
	if fn == nil {
		t.Fatal("handler not found")
	}
	if len(fn.Decorators) != 2 || fn.Decorators[0] != "app.route" || fn.Decorators[1] != "staticmethod" {
		t.Errorf("decorators = %v", fn.Decorators)
	}
	if !refFrom(res, fn.ID, "app.route", model.EdgeDecorates) {
		t.Error("missing decorates app.route")
	}
	if !refFrom(res, fn.ID, "staticmethod", model.EdgeDecorates) {
		t.Error("missing decorates staticmethod")
	}
}

// 4. @property
func TestPyProperty(t *testing.T) {
	res := extractPyRes(t, `class C:
    @property
    def name(self):
        return 1
`)
	if findNode(res, model.KindProperty, "name") == nil {
		t.Error("property name not found")
	}
	if findNode(res, model.KindMethod, "name") != nil {
		t.Error("property should not also be a method")
	}
}

// 5. Imports
func TestPyImports(t *testing.T) {
	res := extractPyRes(t, `import os
import a.b as c
from x.y import d, e as f
`)
	for _, name := range []string{"os", "c", "d", "f"} {
		if findNode(res, model.KindImport, name) == nil {
			t.Errorf("import node %q not found", name)
		}
		if !hasRef(res, name, model.EdgeImports) {
			t.Errorf("import ref %q not found", name)
		}
	}
	if findNode(res, model.KindImport, "a.b") != nil {
		t.Error("aliased import should use alias name, not module path")
	}
}

// 6. Assignments / vars / constants / fields
func TestPyModuleAssignments(t *testing.T) {
	res := extractPyRes(t, `DEBUG = True
__version__ = "1.0"
count = 0
`)
	if findNode(res, model.KindConstant, "DEBUG") == nil {
		t.Error("DEBUG should be constant")
	}
	if findNode(res, model.KindConstant, "__version__") == nil {
		t.Error("__version__ should be constant")
	}
	if findNode(res, model.KindVariable, "count") == nil {
		t.Error("count should be variable")
	}
}

func TestPyClassFields(t *testing.T) {
	res := extractPyRes(t, `class C:
    NAME = "c"
    def __init__(self):
        self.value = 1
`)
	if findNode(res, model.KindField, "NAME") == nil {
		t.Error("class body NAME should be field")
	}
	f := findNode(res, model.KindField, "value")
	if f == nil {
		t.Fatal("self.value should be field")
	}
	if f.QualifiedName != "C::value" {
		t.Errorf("self field qualified name = %q", f.QualifiedName)
	}
}

func TestPyVarTypeHint(t *testing.T) {
	res := extractPyRes(t, `w = Widget()
`)
	if findNode(res, model.KindVariable, "w") == nil {
		t.Error("w should be variable")
	}
	if !hasRef(res, "Widget", model.EdgeCalls) {
		t.Error("constructor call ref missing")
	}
}

// 7. Calls & instantiations
func TestPyCalls(t *testing.T) {
	res := extractPyRes(t, `def f():
    foo(1)
    self.helper()
    obj.method()
`)
	fn := findNode(res, model.KindFunction, "f")
	if fn == nil {
		t.Fatal("f not found")
	}
	if !refFrom(res, fn.ID, "foo", model.EdgeCalls) {
		t.Error("missing call foo")
	}
	if !refFrom(res, fn.ID, "helper", model.EdgeCalls) {
		t.Error("self.helper should strip receiver → helper")
	}
	if !refFrom(res, fn.ID, "obj.method", model.EdgeCalls) {
		t.Error("missing call obj.method")
	}
}

func TestPyCallInControlFlow(t *testing.T) {
	res := extractPyRes(t, `def f():
    if True:
        for x in y:
            doit()
`)
	fn := findNode(res, model.KindFunction, "f")
	if fn == nil || !refFrom(res, fn.ID, "doit", model.EdgeCalls) {
		t.Error("call nested in control flow not captured")
	}
}

// 8. Nested functions
func TestPyNestedFunction(t *testing.T) {
	res := extractPyRes(t, `def outer():
    def inner():
        pass
`)
	inner := findNode(res, model.KindFunction, "inner")
	outer := findNode(res, model.KindFunction, "outer")
	if inner == nil || outer == nil {
		t.Fatal("missing outer/inner")
	}
	if inner.QualifiedName != "outer::inner" {
		t.Errorf("nested function qualified name = %q", inner.QualifiedName)
	}
	found := false
	for _, ed := range res.Edges {
		if ed.Kind == model.EdgeContains && ed.Source == outer.ID && ed.Target == inner.ID {
			found = true
		}
	}
	if !found {
		t.Error("missing contains edge outer→inner")
	}
}

package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractRbRes(t *testing.T, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile("mod.rb", []byte(src), model.LangRuby)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestRbFileNodeEmitted(t *testing.T) {
	res := extractRbRes(t, "x = 1\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangRuby {
		t.Fatalf("file node language = %q, want ruby", res.Nodes[0].Language)
	}
}

// 1. Module + class + inheritance
func TestRbModuleClassInheritance(t *testing.T) {
	res := extractRbRes(t, `module Animals
  class Dog < Base
    def speak
    end
  end
end
`)
	mn := findNode(res, model.KindModule, "Animals")
	if mn == nil {
		t.Fatal("module Animals not found")
	}
	cn := findNode(res, model.KindClass, "Dog")
	if cn == nil {
		t.Fatal("class Dog not found")
	}
	if cn.QualifiedName != "Animals::Dog" {
		t.Errorf("class qualified name = %q", cn.QualifiedName)
	}
	if !refFrom(res, cn.ID, "Base", model.EdgeExtends) {
		t.Error("missing extends Base")
	}
	m := findNode(res, model.KindMethod, "speak")
	if m == nil || m.QualifiedName != "Animals::Dog::speak" {
		t.Errorf("method speak qualified name = %v", m)
	}
}

// 2. Top-level method is a function; method in class is a method
func TestRbTopLevelMethod(t *testing.T) {
	res := extractRbRes(t, `def helper(x)
end
`)
	fn := findNode(res, model.KindFunction, "helper")
	if fn == nil {
		t.Fatal("top-level def should be a function")
	}
	if fn.Signature != "def helper(x)" {
		t.Errorf("signature = %q", fn.Signature)
	}
}

// 3. singleton_method (def self.x) → static method
func TestRbSingletonMethod(t *testing.T) {
	res := extractRbRes(t, `class C
  def self.create
  end
end
`)
	m := findNode(res, model.KindMethod, "create")
	if m == nil {
		t.Fatal("singleton method create not found")
	}
	if !m.IsStatic {
		t.Error("def self.create should be static")
	}
	if m.Signature != "def self.create" {
		t.Errorf("signature = %q", m.Signature)
	}
}

// 4. attr_accessor → properties
func TestRbAttrAccessor(t *testing.T) {
	res := extractRbRes(t, `class C
  attr_accessor :breed, :age
  attr_reader :color
end
`)
	for _, name := range []string{"breed", "age", "color"} {
		p := findNode(res, model.KindProperty, name)
		if p == nil {
			t.Errorf("property %q not found", name)
			continue
		}
		if p.QualifiedName != "C::"+name {
			t.Errorf("property %q qualified name = %q", name, p.QualifiedName)
		}
	}
}

// 5. include mixin → implements
func TestRbInclude(t *testing.T) {
	res := extractRbRes(t, `class C
  include Walkable
  prepend Logging
end
`)
	cn := findNode(res, model.KindClass, "C")
	if cn == nil {
		t.Fatal("class C not found")
	}
	if !refFrom(res, cn.ID, "Walkable", model.EdgeImplements) {
		t.Error("include Walkable should emit implements")
	}
	if !refFrom(res, cn.ID, "Logging", model.EdgeImplements) {
		t.Error("prepend Logging should emit implements")
	}
}

// 6. require / require_relative → imports
func TestRbRequire(t *testing.T) {
	res := extractRbRes(t, `require 'json'
require_relative 'lib/dog'
`)
	if findNode(res, model.KindImport, "json") == nil {
		t.Error("import json not found")
	}
	if !hasRef(res, "json", model.EdgeImports) {
		t.Error("imports json ref missing")
	}
	if findNode(res, model.KindImport, "dog") == nil {
		t.Error("require_relative 'lib/dog' should yield import node 'dog'")
	}
	if !hasRef(res, "dog", model.EdgeImports) {
		t.Error("imports dog ref missing")
	}
}

// 7. Instance/class variables → fields on enclosing class
func TestRbInstanceVarFields(t *testing.T) {
	res := extractRbRes(t, `class C
  def initialize(name)
    @name = name
    @@count = 1
  end
end
`)
	f := findNode(res, model.KindField, "@name")
	if f == nil {
		t.Fatal("@name should be a field")
	}
	if f.QualifiedName != "C::@name" {
		t.Errorf("@name qualified name = %q", f.QualifiedName)
	}
	if findNode(res, model.KindField, "@@count") == nil {
		t.Error("@@count should be a field")
	}
}

// 8. Constants and variables
func TestRbConstantsAndVars(t *testing.T) {
	res := extractRbRes(t, `GREETING = "hi"
count = 0
`)
	if findNode(res, model.KindConstant, "GREETING") == nil {
		t.Error("GREETING should be a constant")
	}
	if findNode(res, model.KindVariable, "count") == nil {
		t.Error("count should be a variable")
	}
}

// 9. Calls: bare, self-stripped, constant receiver, dotted
func TestRbCalls(t *testing.T) {
	res := extractRbRes(t, `def f
  foo(1)
  self.helper
  Dog.new
  obj.method
end
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
	if !refFrom(res, fn.ID, "Dog.new", model.EdgeCalls) {
		t.Error("missing call Dog.new")
	}
	if !refFrom(res, fn.ID, "obj.method", model.EdgeCalls) {
		t.Error("missing call obj.method")
	}
}

// 10. Var type inference via constructor assignment
func TestRbVarTypeInferenceSignature(t *testing.T) {
	res := extractRbRes(t, `d = Dog.new("rex")
`)
	v := findNode(res, model.KindVariable, "d")
	if v == nil {
		t.Fatal("d should be a variable")
	}
	if v.Signature != `= Dog.new("rex")` {
		t.Errorf("signature = %q", v.Signature)
	}
}

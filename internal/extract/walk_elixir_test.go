package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractExRes(t *testing.T, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile("mod.ex", []byte(src), model.LangElixir)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestExFileNodeEmitted(t *testing.T) {
	res := extractExRes(t, "x = 1\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangElixir {
		t.Fatalf("file node language = %q, want elixir", res.Nodes[0].Language)
	}
}

// Module + public/private functions, qualified names.
func TestExModuleFunctions(t *testing.T) {
	res := extractExRes(t, `defmodule Geometry.Circle do
  def area(r) do
    r * r
  end

  defp helper(x), do: x + 1
end
`)
	mn := findNode(res, model.KindModule, "Geometry.Circle")
	if mn == nil {
		t.Fatal("module Geometry.Circle not found")
	}
	area := findNode(res, model.KindFunction, "area")
	if area == nil || area.QualifiedName != "Geometry.Circle::area" {
		t.Fatalf("area qualified name = %v", area)
	}
	if area.Visibility == nil || *area.Visibility != "public" {
		t.Errorf("area visibility = %v, want public", area.Visibility)
	}
	helper := findNode(res, model.KindFunction, "helper")
	if helper == nil || helper.Visibility == nil || *helper.Visibility != "private" {
		t.Errorf("helper should be private, got %v", helper)
	}
}

// defstruct → struct named after module + fields.
func TestExStruct(t *testing.T) {
	res := extractExRes(t, `defmodule Point do
  defstruct [:x, :y]
end
`)
	sn := findNode(res, model.KindStruct, "Point")
	if sn == nil {
		t.Fatal("struct Point not found")
	}
	if findNode(res, model.KindField, "x") == nil || findNode(res, model.KindField, "y") == nil {
		t.Error("missing struct fields x/y")
	}
}

// defprotocol → interface; its def sigs → methods.
func TestExProtocol(t *testing.T) {
	res := extractExRes(t, `defprotocol Shape do
  def area(shape)
end
`)
	pn := findNode(res, model.KindInterface, "Shape")
	if pn == nil {
		t.Fatal("protocol Shape (interface) not found")
	}
	m := findNode(res, model.KindMethod, "area")
	if m == nil || m.QualifiedName != "Shape::area" {
		t.Errorf("protocol method area = %v", m)
	}
}

// defimpl → implements ref (Type → Proto).
func TestExImpl(t *testing.T) {
	res := extractExRes(t, `defimpl Shape, for: Circle do
  def area(c), do: c.radius
end
`)
	in := findNode(res, model.KindModule, "Shape.Circle")
	if in == nil {
		t.Fatal("impl module Shape.Circle not found")
	}
	if !refFrom(res, in.ID, "Shape", model.EdgeImplements) {
		t.Error("missing implements Shape")
	}
}

// alias binds a local name; import refs emitted.
func TestExAliasImport(t *testing.T) {
	res := extractExRes(t, `defmodule Caller do
  alias Geometry.Util, as: U

  def go(x) do
    U.square(x)
  end
end
`)
	imp := findNodeQN(res, model.KindImport, "Geometry.Util")
	if imp == nil {
		t.Fatal("alias import node (qn Geometry.Util) not found")
	}
	if imp.Name != "U" {
		t.Errorf("alias local name = %q, want U", imp.Name)
	}
	// The call U.square keeps the dotted form on a calls ref.
	goFn := findNode(res, model.KindFunction, "go")
	if goFn == nil {
		t.Fatal("function go not found")
	}
	if !refFrom(res, goFn.ID, "U.square", model.EdgeCalls) {
		t.Error("missing call U.square")
	}
}

// Kernel builtins are skipped on bare calls.
func TestExKernelSkipped(t *testing.T) {
	res := extractExRes(t, `defmodule M do
  def f(x) do
    if is_nil(x), do: custom_fun(x)
  end
end
`)
	fn := findNode(res, model.KindFunction, "f")
	if fn == nil {
		t.Fatal("function f not found")
	}
	if refFrom(res, fn.ID, "is_nil", model.EdgeCalls) {
		t.Error("is_nil should be skipped as a Kernel builtin")
	}
	if !refFrom(res, fn.ID, "custom_fun", model.EdgeCalls) {
		t.Error("custom_fun call should be recorded")
	}
}

// Module attribute @x value → constant; @doc skipped.
func TestExAttributes(t *testing.T) {
	res := extractExRes(t, `defmodule M do
  @moduledoc "skip me"
  @pi 3.14
end
`)
	if findNode(res, model.KindConstant, "pi") == nil {
		t.Error("constant @pi not found")
	}
	if findNode(res, model.KindConstant, "moduledoc") != nil {
		t.Error("@moduledoc should be skipped")
	}
}

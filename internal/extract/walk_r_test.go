package extract

import (
	"testing"

	"github.com/specscore/codegrapher/model"
)

func extractRRes(t *testing.T, name, src string) model.ExtractionResult {
	t.Helper()
	res, err := ExtractFile(name, []byte(src), model.LangR)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return res
}

func TestRFileNodeEmitted(t *testing.T) {
	res := extractRRes(t, "m.R", "x <- 1\n")
	if len(res.Nodes) == 0 || res.Nodes[0].Kind != model.KindFile {
		t.Fatalf("expected a file node first, got %+v", res.Nodes)
	}
	if res.Nodes[0].Language != model.LangR {
		t.Fatalf("file node language = %q, want r", res.Nodes[0].Language)
	}
}

func TestRFunctionAndCall(t *testing.T) {
	res := extractRRes(t, "m.R", `f <- function(x) { g(x) }
g <- function(y) y + 1
`)
	f := findNode(res, model.KindFunction, "f")
	g := findNode(res, model.KindFunction, "g")
	if f == nil || g == nil {
		t.Fatalf("functions f/g missing: f=%v g=%v", f, g)
	}
	if !refFrom(res, f.ID, "g", model.EdgeCalls) {
		t.Error("missing call f -> g")
	}
}

func TestRFunctionAltAssign(t *testing.T) {
	res := extractRRes(t, "m.R", `a = function() 1
b <<- function() 2
`)
	if findNode(res, model.KindFunction, "a") == nil {
		t.Error("a (= function) not a function")
	}
	if findNode(res, model.KindFunction, "b") == nil {
		t.Error("b (<<- function) not a function")
	}
}

func TestRVariableAndConstant(t *testing.T) {
	res := extractRRes(t, "m.R", `X <- 42
y <- 7
`)
	if findNode(res, model.KindConstant, "X") == nil {
		t.Error("X should be a constant")
	}
	if findNode(res, model.KindVariable, "y") == nil {
		t.Error("y should be a variable")
	}
}

func TestRLibraryImport(t *testing.T) {
	res := extractRRes(t, "m.R", "library(stats)\nrequire(utils)\n")
	if findNode(res, model.KindImport, "stats") == nil {
		t.Error("library(stats) import missing")
	}
	if findNode(res, model.KindImport, "utils") == nil {
		t.Error("require(utils) import missing")
	}
	fileID := res.Nodes[0].ID
	if !refFrom(res, fileID, "stats", model.EdgeImports) {
		t.Error("missing imports edge file -> stats")
	}
}

func TestRSourceImport(t *testing.T) {
	res := extractRRes(t, "main.R", `source("util.R")
`)
	imp := findNode(res, model.KindImport, "util")
	if imp == nil {
		t.Fatal("source(\"util.R\") import node (named util) missing")
	}
	fileID := res.Nodes[0].ID
	if !refFrom(res, fileID, "util", model.EdgeImports) {
		t.Error("missing imports edge file -> util")
	}
}

func TestRNamespacedCall(t *testing.T) {
	res := extractRRes(t, "m.R", `f <- function() stats::rnorm(5)
`)
	f := findNode(res, model.KindFunction, "f")
	if f == nil {
		t.Fatal("f missing")
	}
	if !refFrom(res, f.ID, "stats::rnorm", model.EdgeCalls) {
		t.Error("namespaced call stats::rnorm should keep namespace")
	}
}

func TestRS4Class(t *testing.T) {
	res := extractRRes(t, "m.R", `setClass("Account", representation(balance="numeric"))
setGeneric("deposit", function(acc, amt) standardGeneric("deposit"))
`)
	if findNode(res, model.KindClass, "Account") == nil {
		t.Error("setClass Account should be a class")
	}
	if findNode(res, model.KindMethod, "deposit") == nil {
		t.Error("setGeneric deposit should be a method")
	}
}

func TestRBuiltinsSkipped(t *testing.T) {
	res := extractRRes(t, "m.R", `f <- function(x) print(length(x))
`)
	f := findNode(res, model.KindFunction, "f")
	if f == nil {
		t.Fatal("f missing")
	}
	if refFrom(res, f.ID, "print", model.EdgeCalls) || refFrom(res, f.ID, "length", model.EdgeCalls) {
		t.Error("builtins print/length should not produce call edges")
	}
}

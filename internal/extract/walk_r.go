package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkR walks a parsed R (tree-sitter `r`) file root and extracts symbols. R is
// dynamically typed and structurally thin: it has no native classes (OOP is
// convention via S3/S4/R5), so the symbol graph is intentionally thin —
// functions + calls + library/source edges are the meat. Class recovery
// (setClass/setGeneric/setMethod) is best-effort name capture only.
//
// Node type reference (tree-sitter-r), confirmed by probe:
//
//	program (root)
//	binary_operator (fields lhs/rhs; the assignment operator <-, =, <<-, ->
//	    is an UNNAMED child between them)
//	function_definition (fields parameters, body)
//	call (fields function, arguments); function is identifier /
//	    namespace_operator (pkg::f, fields lhs/rhs) / extract_operator (obj$m)
//	arguments → argument (field value); string → string_content
func (e *extractor) walkR(root *tsparse.Node) {
	e.rWalkChildren(root)
}

// rWalkChildren visits every named child of node, dispatching statements.
func (e *extractor) rWalkChildren(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		e.visitNodeR(child)
	}
}

// visitNodeR dispatches a single node. Unknown kinds descend so calls/defs
// nested inside control flow (braced_expression, if, for, ...) are still seen.
func (e *extractor) visitNodeR(node *tsparse.Node) {
	switch node.Kind() {
	case "binary_operator":
		if rAssignOp(node) != "" {
			e.extractRAssignment(node)
			return
		}
		e.rWalkChildren(node)
	case "call":
		e.extractRCall(node)
	default:
		e.rWalkChildren(node)
	}
}

// rAssignOp returns the assignment operator text of a binary_operator
// (<-, =, <<-), or "" if it is not a left-assignment. The operator is the
// unnamed child between lhs and rhs.
func rAssignOp(node *tsparse.Node) string {
	for i := 0; i < node.ChildCount(); i++ {
		c := node.Child(i)
		if c == nil || c.IsNamed() {
			continue
		}
		switch c.Kind() {
		case "<-", "=", "<<-":
			return c.Kind()
		}
	}
	return ""
}

// extractRAssignment handles `name <- value` (and = / <<-). A function RHS →
// KindFunction (the dominant construct); otherwise a top-level variable /
// constant. Only a plain identifier LHS produces a node. The RHS is always
// walked for nested calls.
func (e *extractor) extractRAssignment(node *tsparse.Node) {
	lhs := node.ChildByFieldName("lhs")
	rhs := node.ChildByFieldName("rhs")
	if lhs == nil || lhs.Kind() != "identifier" {
		// Non-identifier target (e.g. x[1] <- v, obj$f <- v): still walk RHS.
		if rhs != nil {
			e.visitNodeR(rhs)
		}
		return
	}
	name := lhs.Text()
	if name == "" {
		return
	}

	if rhs != nil && rhs.Kind() == "function_definition" {
		e.extractRFunction(name, node, rhs)
		return
	}

	kind := model.KindVariable
	if rIsConstantName(name) {
		kind = model.KindConstant
	}
	e.createNode(kind, name, node, nodeExtra{signature: rAssignSignature(rhs)})

	if rhs != nil {
		e.visitNodeR(rhs)
	}
}

// extractRFunction emits a KindFunction for `name <- function(args) body` and
// walks the body under the function's scope (nested functions → contains).
func (e *extractor) extractRFunction(name string, assign, fnDef *tsparse.Node) {
	extra := nodeExtra{signature: rFunctionSignature(name, fnDef)}
	fn := e.createNode(model.KindFunction, name, assign, extra)
	if fn == nil {
		return
	}
	if body := fnDef.ChildByFieldName("body"); body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		e.visitNodeR(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractRCall handles a call node. library/require → package import;
// source("x.R") → file import; setClass/setGeneric/setMethod → best-effort
// class/method node; everything else → an EdgeCalls reference. Arguments are
// always walked for nested calls.
func (e *extractor) extractRCall(node *tsparse.Node) {
	fnNode := node.ChildByFieldName("function")
	args := node.ChildByFieldName("arguments")

	if fnNode != nil && fnNode.Kind() == "identifier" {
		switch fnNode.Text() {
		case "library", "require":
			e.extractRPackageImport(node, args)
			e.rWalkArgs(args)
			return
		case "source":
			e.extractRSourceImport(node, args)
			e.rWalkArgs(args)
			return
		case "setClass":
			e.emitRNamedNode(model.KindClass, args, node)
			e.rWalkArgs(args)
			return
		case "setGeneric", "setMethod":
			e.emitRNamedNode(model.KindMethod, args, node)
			e.rWalkArgs(args)
			return
		}
	}

	if len(e.nodeStack) > 0 && fnNode != nil {
		if name := rCalleeName(fnNode); name != "" && !rIsBuiltin(name) {
			callerID := e.nodeStack[len(e.nodeStack)-1]
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: name,
				ReferenceKind: model.EdgeCalls,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
		}
	}

	e.rWalkArgs(args)
}

// rWalkArgs descends into call arguments for nested calls/definitions.
func (e *extractor) rWalkArgs(args *tsparse.Node) {
	if args == nil {
		return
	}
	for i := 0; i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg == nil {
			continue
		}
		if v := arg.ChildByFieldName("value"); v != nil {
			e.visitNodeR(v)
		} else {
			e.visitNodeR(arg)
		}
	}
}

// emitRNamedNode emits a node of kind named after the first string argument
// (setClass("T", ...) → class T; setMethod("g", ...) → method g). Best-effort.
func (e *extractor) emitRNamedNode(kind model.NodeKind, args, node *tsparse.Node) {
	name := rFirstStringArg(args)
	if name == "" {
		return
	}
	e.createNode(kind, name, node, nodeExtra{signature: strings.TrimSpace(node.Text())})
}

// extractRPackageImport handles library(pkg) / require(pkg): emits a KindImport
// for the package and an EdgeImports ref from the current scope. The package
// name is the first argument, which is normally a bare identifier.
func (e *extractor) extractRPackageImport(node, args *tsparse.Node) {
	pkg := rFirstArgName(args)
	if pkg == "" {
		return
	}
	e.emitRImport(pkg, node, args)
}

// extractRSourceImport handles source("x.R"): emits a KindImport named after the
// file basename minus .R, so it resolves through-source to the in-repo file.
func (e *extractor) extractRSourceImport(node, args *tsparse.Node) {
	path := rFirstStringArg(args)
	if path == "" {
		return
	}
	name := rSourceName(path)
	if name == "" {
		return
	}
	e.emitRImport(name, node, args)
}

// emitRImport creates a KindImport node named name and an EdgeImports ref from
// the current scope.
func (e *extractor) emitRImport(name string, node, args *tsparse.Node) {
	sig := strings.TrimSpace(node.Text())
	e.createNode(model.KindImport, name, node, nodeExtra{signature: sig})
	if len(e.nodeStack) == 0 {
		return
	}
	parentID := e.nodeStack[len(e.nodeStack)-1]
	line := int(node.StartPoint().Row) + 1
	col := int(node.StartPoint().Column)
	if args != nil {
		line = int(args.StartPoint().Row) + 1
		col = int(args.StartPoint().Column)
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    parentID,
		ReferenceName: name,
		ReferenceKind: model.EdgeImports,
		Line:          line,
		Column:        col,
	})
}

// rCalleeName resolves a call's function node to a callee name.
//   - identifier f → "f"
//   - namespace_operator pkg::f → "pkg::f" (namespace kept; usually external)
//   - extract_operator obj$method → "method" (receiver stripped, best-effort)
func rCalleeName(fnNode *tsparse.Node) string {
	switch fnNode.Kind() {
	case "identifier":
		return fnNode.Text()
	case "namespace_operator":
		return strings.TrimSpace(fnNode.Text())
	case "extract_operator":
		if m := fnNode.ChildByFieldName("rhs"); m != nil {
			return m.Text()
		}
		return ""
	default:
		return ""
	}
}

// rFirstArgName returns the text of the first argument's value when it is a
// bare identifier (library(pkg)), else "".
func rFirstArgName(args *tsparse.Node) string {
	if args == nil {
		return ""
	}
	for i := 0; i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg == nil || arg.Kind() != "argument" {
			continue
		}
		v := arg.ChildByFieldName("value")
		if v != nil && v.Kind() == "identifier" {
			return v.Text()
		}
		if v != nil && v.Kind() == "string" {
			return rStringContent(v)
		}
		return ""
	}
	return ""
}

// rFirstStringArg returns the inner content of the first string argument, else "".
func rFirstStringArg(args *tsparse.Node) string {
	if args == nil {
		return ""
	}
	for i := 0; i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg == nil || arg.Kind() != "argument" {
			continue
		}
		if v := arg.ChildByFieldName("value"); v != nil && v.Kind() == "string" {
			return rStringContent(v)
		}
		return ""
	}
	return ""
}

// rStringContent returns the inner text of a string node.
func rStringContent(node *tsparse.Node) string {
	if c := node.ChildByFieldName("content"); c != nil {
		return c.Text()
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "string_content" {
			return c.Text()
		}
	}
	return ""
}

// rSourceName reduces a sourced path to its basename minus the .R/.r extension
// so it matches the defining file (e.g. "util.R" → "util", "lib/util.R" → "util").
func rSourceName(path string) string {
	if idx := strings.LastIndexAny(path, "/\\"); idx >= 0 {
		path = path[idx+1:]
	}
	if idx := strings.LastIndex(path, "."); idx > 0 {
		ext := strings.ToLower(path[idx:])
		if ext == ".r" {
			path = path[:idx]
		}
	}
	return path
}

// rFunctionSignature renders a function header: name <- function(params).
func rFunctionSignature(name string, fnDef *tsparse.Node) string {
	sig := name + " <- function"
	if params := fnDef.ChildByFieldName("parameters"); params != nil {
		sig += params.Text()
	}
	return sig
}

// rAssignSignature renders the right-hand side of an assignment as a signature.
func rAssignSignature(rhs *tsparse.Node) string {
	if rhs == nil {
		return ""
	}
	val := strings.TrimSpace(rhs.Text())
	if val == "" {
		return ""
	}
	if len(val) > 100 {
		return "= " + val[:100] + "..."
	}
	return "= " + val
}

// rIsConstantName reports whether a name is UPPER_SNAKE (only [A-Z0-9_], at
// least one letter).
func rIsConstantName(name string) bool {
	if name == "" {
		return false
	}
	hasLetter := false
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9', r == '_', r == '.':
		default:
			return false
		}
	}
	return hasLetter
}

// rBuiltins is a small set of R base functions whose calls produce no edges.
var rBuiltins = map[string]bool{
	"c": true, "length": true, "print": true, "cat": true, "paste": true,
	"paste0": true, "lapply": true, "sapply": true, "vapply": true,
	"mapply": true, "apply": true, "tapply": true, "return": true,
	"list": true, "data.frame": true, "vector": true, "matrix": true,
	"names": true, "nrow": true, "ncol": true, "seq": true, "seq_len": true,
	"seq_along": true, "rep": true, "sum": true, "mean": true, "max": true,
	"min": true, "is.null": true, "is.na": true, "as.numeric": true,
	"as.character": true, "as.integer": true, "as.logical": true,
	"function": true, "if": true, "for": true, "while": true, "stop": true,
	"warning": true, "stopifnot": true, "invisible": true, "unlist": true,
	"do.call": true, "match.arg": true, "Reduce": true, "Filter": true,
	"Map": true, "sort": true, "order": true, "which": true, "ifelse": true,
}

// rIsBuiltin reports whether a callee name refers to an R base builtin.
func rIsBuiltin(name string) bool {
	return rBuiltins[name]
}

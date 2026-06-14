package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkPerl walks a parsed Perl (tree-sitter `perl`) file root and extracts
// symbols. Perl is dynamically typed and package-based, so resolution is
// heuristic (use/require deps + name + constructor-assignment type inference),
// mirroring walkRuby/walkPython.
//
// Perl packages are flat-in-file: a `package Foo;` statement opens a scope that
// runs until the next package statement or EOF (there is no body block). The
// walker tracks the current package scope on the node stack and resets it when a
// new package statement appears.
//
// Node type reference (tree-sitter-perl, verified via probe):
//
//	package_statement (field: name → package node)
//	subroutine_declaration_statement (fields: name → bareword, body → block)
//	use_statement (field: module → package node; trailing args)
//	method_call_expression (fields: invocant, method)
//	ambiguous_function_call_expression (fields: function, arguments)
//	func1op_call_expression (field: function — builtins)
//	assignment_expression (fields: left, operator, right)
//	variable_declaration (my/our + field: variable → scalar/array/hash)
func (e *extractor) walkPerl(root *tsparse.Node) {
	// fileID is the bottom of the stack; package scopes are pushed on top of it.
	fileID := ""
	if len(e.nodeStack) > 0 {
		fileID = e.nodeStack[0]
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "package_statement" {
			// Flat package: reset the stack to file scope, then push the package.
			e.nodeStack = e.nodeStack[:0]
			if fileID != "" {
				e.nodeStack = append(e.nodeStack, fileID)
			}
			e.extractPerlPackage(child)
			continue
		}
		e.visitNodePerl(child)
	}
}

// extractPerlPackage emits a KindModule for `package Foo::Bar;` and pushes it
// onto the node stack (it stays pushed until the next package statement).
func (e *extractor) extractPerlPackage(node *tsparse.Node) {
	name := perlPackageName(node)
	if name == "" {
		return
	}
	mn := e.createNode(model.KindModule, name, node, nodeExtra{})
	if mn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, mn.ID)
}

// visitNodePerl dispatches a single statement node. Unknown kinds descend into
// their named children so calls/definitions nested inside control flow are seen.
func (e *extractor) visitNodePerl(node *tsparse.Node) {
	switch node.Kind() {
	case "subroutine_declaration_statement":
		e.extractPerlSub(node)
	case "use_statement", "require_expression":
		e.extractPerlUse(node)
	case "method_call_expression":
		e.extractPerlMethodCall(node)
	case "ambiguous_function_call_expression", "function_call_expression":
		e.extractPerlFuncCall(node)
	case "assignment_expression":
		e.extractPerlAssignment(node)
	default:
		e.visitPerlBody(node)
	}
}

// visitPerlBody descends into a node's named children without emitting a node
// for the container itself.
func (e *extractor) visitPerlBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodePerl(child)
		}
	}
}

// extractPerlSub handles a subroutine_declaration_statement. Inside a package it
// is a KindMethod; at file scope it is a KindFunction.
func (e *extractor) extractPerlSub(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	kind := model.KindFunction
	if e.isInsideClassLike() {
		kind = model.KindMethod
	}

	fn := e.createNode(kind, name, node, nodeExtra{signature: "sub " + name})
	if fn == nil {
		return
	}

	if body := node.ChildByFieldName("body"); body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		for i := 0; i < body.NamedChildCount(); i++ {
			if child := body.NamedChild(i); child != nil {
				e.visitNodePerl(child)
			}
		}
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractPerlUse handles use/require. Special forms (use parent/base → extends,
// use constant → constant) are handled distinctly; a plain `use Foo::Bar` /
// `require Foo::Bar` emits a KindImport plus an EdgeImports reference.
func (e *extractor) extractPerlUse(node *tsparse.Node) {
	moduleNode := node.ChildByFieldName("module")
	module := ""
	if moduleNode != nil {
		module = moduleNode.Text()
	}

	switch module {
	case "parent", "base":
		e.extractPerlParent(node)
		return
	case "constant":
		e.extractPerlConstant(node)
		return
	case "strict", "warnings", "utf8", "lib", "vars", "feature", "v5":
		// Pragmas — no symbol.
		return
	}
	if module == "" {
		return
	}

	sig := strings.TrimSpace(node.Text())
	name := perlLastSegment(module)
	if name == "" {
		return
	}
	e.createNode(model.KindImport, name, node, nodeExtra{signature: sig})
	if len(e.nodeStack) > 0 {
		parentID := e.nodeStack[len(e.nodeStack)-1]
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    parentID,
			ReferenceName: name,
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// extractPerlParent handles `use parent 'Base'` / `use base qw(Base)`: emits an
// EdgeExtends reference per base package from the current package scope.
func (e *extractor) extractPerlParent(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	fromID := e.nodeStack[len(e.nodeStack)-1]
	for _, base := range perlStringArgs(node) {
		// `use parent -norequire, 'Base'` — skip the option word.
		if base == "" || base == "-norequire" {
			continue
		}
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: perlLastSegment(base),
			ReferenceKind: model.EdgeExtends,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// extractPerlConstant handles `use constant NAME => value;` → KindConstant.
func (e *extractor) extractPerlConstant(node *tsparse.Node) {
	// The first bareword/autoquoted_bareword after the constant module is the name.
	var name string
	tsparse.Walk(node, func(n *tsparse.Node) {
		if name != "" {
			return
		}
		switch n.Kind() {
		case "autoquoted_bareword", "bareword":
			t := n.Text()
			if t != "" && t != "constant" {
				name = t
			}
		}
	})
	if name == "" {
		return
	}
	e.createNode(model.KindConstant, name, node, nodeExtra{signature: strings.TrimSpace(node.Text())})
}

// extractPerlAssignment handles `my`/`our` variable declarations at the current
// scope. `@ISA = (...)` is recognised as inheritance. Otherwise an ALL-CAPS name
// is a KindConstant and anything else a KindVariable. The RHS is walked for calls.
func (e *extractor) extractPerlAssignment(node *tsparse.Node) {
	left := node.ChildByFieldName("left")
	right := node.ChildByFieldName("right")

	if left != nil && left.Kind() == "variable_declaration" {
		varNode := left.ChildByFieldName("variable")
		if varNode != nil {
			name := perlVarName(varNode)
			if name != "" {
				// @ISA = ('Base') → extends edges.
				if name == "ISA" && varNode.Kind() == "array" {
					e.extractPerlISA(node)
				} else {
					kind := model.KindVariable
					if perlIsConstantName(name) {
						kind = model.KindConstant
					}
					e.createNode(kind, name, node, nodeExtra{signature: perlAssignSignature(node)})
				}
			}
		}
	}

	if right != nil {
		e.visitNodePerl(right)
	}
}

// extractPerlISA handles `our @ISA = ('Base', 'Other')` → EdgeExtends per base.
func (e *extractor) extractPerlISA(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	fromID := e.nodeStack[len(e.nodeStack)-1]
	for _, base := range perlStringArgs(node) {
		if base == "" {
			continue
		}
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: perlLastSegment(base),
			ReferenceKind: model.EdgeExtends,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// extractPerlMethodCall handles `$obj->method(...)` and `Foo->new(...)`.
// `$self`/`$class` receivers are stripped (like Python self). A bareword
// invocant (`Foo->new`) keeps the receiver prefix so the resolver can promote
// `Foo->new` → instantiates. A scalar invocant becomes "$var.method" (var name
// only) for constructor type inference.
func (e *extractor) extractPerlMethodCall(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		if name := perlMethodCalleeName(node); name != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: name,
				ReferenceKind: model.EdgeCalls,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
		}
	}
	// Descend into the invocant for chained calls (e.g. Foo->new->method).
	if inv := node.ChildByFieldName("invocant"); inv != nil && inv.Kind() == "method_call_expression" {
		e.extractPerlMethodCall(inv)
	}
}

// extractPerlFuncCall handles `foo(...)` / `Foo::bar(...)`. Builtins are skipped.
func (e *extractor) extractPerlFuncCall(node *tsparse.Node) {
	fn := node.ChildByFieldName("function")
	if fn != nil {
		name := fn.Text()
		if name != "" && !perlIsBuiltin(name) && len(e.nodeStack) > 0 {
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
	// Descend into the call's named children (other than the function name) for
	// nested calls. Builtin list operators like `print $d->speak` carry their
	// argument as a direct child rather than under an `arguments` field.
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil || child == fn {
			continue
		}
		e.visitNodePerl(child)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// perlPackageName returns the package name from a package_statement's name field.
func perlPackageName(node *tsparse.Node) string {
	if nm := node.ChildByFieldName("name"); nm != nil {
		return nm.Text()
	}
	return ""
}

// perlVarName returns the variable name (sigil stripped) from a scalar/array/hash
// node: its `varname` child.
func perlVarName(node *tsparse.Node) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "varname" {
			return c.Text()
		}
	}
	return ""
}

// perlMethodCalleeName builds the callee name for a method_call_expression.
// `$self`/`$class` invocants strip to the bare method name; a bareword invocant
// keeps "Recv.method"; a scalar invocant becomes "var.method" (sigil stripped).
func perlMethodCalleeName(node *tsparse.Node) string {
	methodNode := node.ChildByFieldName("method")
	if methodNode == nil {
		return ""
	}
	method := methodNode.Text()
	if method == "" {
		return ""
	}
	inv := node.ChildByFieldName("invocant")
	if inv == nil {
		return method
	}
	switch inv.Kind() {
	case "bareword", "package":
		return inv.Text() + "." + method
	case "scalar":
		v := perlVarName(inv)
		if v == "self" || v == "class" {
			return method
		}
		if v == "" {
			return method
		}
		return v + "." + method
	default:
		return method
	}
}

// perlStringArgs returns the inner text of every string_literal / qw word /
// bareword argument under node (used for use parent/base and @ISA lists).
func perlStringArgs(node *tsparse.Node) []string {
	var out []string
	tsparse.Walk(node, func(n *tsparse.Node) {
		switch n.Kind() {
		case "string_content":
			out = append(out, n.Text())
		}
	})
	return out
}

// perlLastSegment reduces a package path Foo::Bar to its last segment (Bar) so
// imports/extends match a defining package/file.
func perlLastSegment(name string) string {
	name = strings.TrimSpace(name)
	if idx := strings.LastIndex(name, "::"); idx >= 0 {
		return name[idx+2:]
	}
	return name
}

// perlAssignSignature renders the RHS of a `my`/`our` assignment as a signature.
func perlAssignSignature(node *tsparse.Node) string {
	right := node.ChildByFieldName("right")
	if right == nil {
		return ""
	}
	val := strings.TrimSpace(right.Text())
	if val == "" {
		return ""
	}
	if len(val) > 100 {
		return "= " + val[:100] + "..."
	}
	return "= " + val
}

// perlIsConstantName reports whether a top-level variable name should be a
// constant: ALL-CAPS (only [A-Z0-9_], at least one letter).
func perlIsConstantName(name string) bool {
	if name == "" {
		return false
	}
	hasLetter := false
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return hasLetter
}

// perlBuiltins is the small skip-set of Perl builtins that should not become
// call edges.
var perlBuiltins = map[string]bool{
	"print": true, "printf": true, "say": true, "shift": true, "unshift": true,
	"push": true, "pop": true, "scalar": true, "keys": true, "values": true,
	"map": true, "grep": true, "join": true, "split": true, "sort": true,
	"reverse": true, "bless": true, "ref": true, "defined": true, "return": true,
	"die": true, "warn": true, "wantarray": true, "exists": true, "delete": true,
	"length": true, "chomp": true, "chop": true, "sprintf": true,
}

// perlIsBuiltin reports whether name is a Perl builtin in the skip-set.
func perlIsBuiltin(name string) bool {
	return perlBuiltins[name]
}

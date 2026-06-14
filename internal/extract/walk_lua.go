package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkLua walks a parsed Lua (tree-sitter `lua`) file root and extracts
// symbols. Lua is dynamically typed and has no native classes, so the symbol
// graph is intentionally thin: functions, calls, and `require` edges are the
// meat. OOP via tables/metatables is recovered only partially — a table
// assigned `local M = {}` that later receives `function M.f()` defs is emitted
// as a KindModule with its functions attached as methods (qualified M::f).
//
// Node type reference (tree-sitter-lua):
//
//	chunk (root)
//	variable_declaration (wraps assignment_statement; field local_declaration when `local`)
//	assignment_statement (fields: variable_list, expression_list)
//	function_declaration (fields: name, parameters, body; name is
//	    identifier / dot_index_expression (M.f) / method_index_expression (M:f);
//	    the local_declaration field on the parent marks `local function`)
//	function_call (fields: name, arguments; name is identifier /
//	    dot_index_expression / method_index_expression)
//	dot_index_expression (fields: table, field) — a.b
//	method_index_expression (fields: table, method) — a:b
//	return_statement, block, parameters, identifier, string, table_constructor, field
func (e *extractor) walkLua(root *tsparse.Node) {
	e.luaWalkChildren(root)
}

// luaWalkChildren visits every child of node, tracking whether each child is a
// `local` declaration via the parent field name (local_declaration).
func (e *extractor) luaWalkChildren(node *tsparse.Node) {
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child == nil || !child.IsNamed() {
			continue
		}
		isLocal := node.FieldNameForChild(i) == "local_declaration"
		e.visitNodeLua(child, isLocal)
	}
}

// visitNodeLua dispatches a single statement node. Unknown kinds descend into
// their children so calls/definitions nested inside control flow are still seen.
func (e *extractor) visitNodeLua(node *tsparse.Node, isLocal bool) {
	switch node.Kind() {
	case "function_declaration":
		e.extractLuaFunction(node, isLocal)
	case "variable_declaration":
		e.extractLuaVariableDecl(node, isLocal)
	case "assignment_statement":
		e.extractLuaAssignment(node, isLocal)
	case "function_call":
		e.extractLuaCall(node)
	default:
		e.luaWalkChildren(node)
	}
}

// extractLuaFunction handles a function_declaration. The name node decides the
// kind and qualified name:
//   - identifier (function f / local function f) → KindFunction
//   - dot_index_expression (function M.f) → KindMethod, qualified M::f
//   - method_index_expression (function M:f) → KindMethod (implicit self),
//     qualified M::f
//
// For the dotted/method forms the function is attached under the table M when M
// was emitted as a KindModule in this file; otherwise it is created at the
// current scope with an explicit M::f qualified name.
func (e *extractor) extractLuaFunction(node *tsparse.Node, isLocal bool) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}

	var table, member string
	kind := model.KindFunction
	switch nameNode.Kind() {
	case "identifier":
		member = nameNode.Text()
	case "dot_index_expression":
		table, member = luaIndexParts(nameNode, "field")
		kind = model.KindMethod
	case "method_index_expression":
		table, member = luaIndexParts(nameNode, "method")
		kind = model.KindMethod
	default:
		member = strings.TrimSpace(nameNode.Text())
	}
	if member == "" {
		return
	}

	extra := nodeExtra{signature: luaSignature(node, table, member)}
	if isLocal {
		vis := "private"
		extra.visibility = &vis
	}

	// Attach M.f / M:f under the module table M when M was emitted as a module
	// node in this file (contains edge module→fn, qualified M::f); otherwise
	// qualify the member explicitly as Table::member at the current scope.
	saved := e.nodeStack
	if table != "" {
		if moduleID := e.luaModuleID(table); moduleID != "" {
			e.nodeStack = append([]string{}, saved...)
			e.nodeStack = append(e.nodeStack, moduleID)
		} else {
			extra.qualifiedName = table + "::" + member
		}
	}

	fn := e.createNode(kind, member, node, extra)
	e.nodeStack = saved
	if fn == nil {
		return
	}

	body := node.ChildByFieldName("body")
	if body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		e.luaWalkChildren(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractLuaVariableDecl handles a variable_declaration, which wraps one
// assignment_statement (and carries the `local` distinction via its parent
// field, already resolved into isLocal).
func (e *extractor) extractLuaVariableDecl(node *tsparse.Node, isLocal bool) {
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "assignment_statement" {
			e.extractLuaAssignment(child, isLocal)
		}
	}
}

// extractLuaAssignment handles an assignment_statement: pairs each name in the
// variable_list with the matching expression in the expression_list. A plain
// identifier target becomes a variable/constant/module node; `require(...)` on
// the right emits an import. The right-hand side is walked for nested calls.
func (e *extractor) extractLuaAssignment(node *tsparse.Node, isLocal bool) {
	// assignment_statement names variable_list / expression_list as plain named
	// children (only `operator` carries a field name), so locate them by kind.
	var varList, exprList *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "variable_list":
			varList = c
		case "expression_list":
			exprList = c
		}
	}

	var names []*tsparse.Node
	if varList != nil {
		for i := 0; i < varList.NamedChildCount(); i++ {
			names = append(names, varList.NamedChild(i))
		}
	}
	var exprs []*tsparse.Node
	if exprList != nil {
		for i := 0; i < exprList.NamedChildCount(); i++ {
			exprs = append(exprs, exprList.NamedChild(i))
		}
	}

	for i, nameNode := range names {
		if nameNode == nil || nameNode.Kind() != "identifier" {
			continue
		}
		name := nameNode.Text()
		if name == "" {
			continue
		}
		var rhs *tsparse.Node
		if i < len(exprs) {
			rhs = exprs[i]
		}

		// `local X = require("mod")` — emit an import for the required module.
		if call := luaRequireCall(rhs); call != nil {
			e.extractLuaRequire(call)
			continue
		}

		kind := luaVarKind(name, rhs, e.luaAtFileScope())
		extra := nodeExtra{signature: luaAssignSignature(rhs)}
		if isLocal {
			vis := "private"
			extra.visibility = &vis
		}
		e.createNode(kind, name, node, extra)
	}

	// Walk every right-hand expression for nested calls (e.g. x = foo()).
	for _, rhs := range exprs {
		if rhs != nil && luaRequireCall(rhs) == nil {
			e.visitNodeLua(rhs, false)
		}
	}
}

// extractLuaCall handles a function_call node. `require "mod"` / `require("mod")`
// emits an import; everything else emits an EdgeCalls reference from the current
// scope. Stdlib globals produce no call edge.
func (e *extractor) extractLuaCall(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode != nil && nameNode.Kind() == "identifier" && nameNode.Text() == "require" {
		e.extractLuaRequire(node)
		return
	}

	if len(e.nodeStack) > 0 && nameNode != nil {
		if name := luaCalleeName(nameNode); name != "" && !luaIsBuiltin(name) {
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

	// Descend into arguments for nested calls.
	if args := node.ChildByFieldName("arguments"); args != nil {
		e.luaWalkChildren(args)
	}
}

// extractLuaRequire handles a require call: emits a KindImport node per required
// module plus an EdgeImports reference from the current scope. The import name is
// the module path's last segment (Lua module names use "." / "/" separators) so
// it can resolve against a same-named definition file.
func (e *extractor) extractLuaRequire(node *tsparse.Node) {
	sig := strings.TrimSpace(node.Text())
	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}
	args := node.ChildByFieldName("arguments")
	if args == nil {
		return
	}
	for i := 0; i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg == nil || arg.Kind() != "string" {
			continue
		}
		path := luaStringContent(arg)
		if path == "" {
			continue
		}
		name := luaRequireName(path)
		if name == "" {
			continue
		}
		e.createNode(model.KindImport, name, node, nodeExtra{signature: sig})
		if parentID != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    parentID,
				ReferenceName: name,
				ReferenceKind: model.EdgeImports,
				Line:          int(arg.StartPoint().Row) + 1,
				Column:        int(arg.StartPoint().Column),
			})
		}
	}
}

// luaAtFileScope reports whether the current scope is the top-level file node
// (i.e. nothing but the file is on the node stack).
func (e *extractor) luaAtFileScope() bool {
	return len(e.nodeStack) == 1
}

// luaModuleID returns the ID of the KindModule node named tableName emitted in
// this file (the `local M = {}` table that `function M.f()` attaches under), or
// "" if none.
func (e *extractor) luaModuleID(tableName string) string {
	for j := range e.nodes {
		n := &e.nodes[j]
		if n.Kind == model.KindModule && n.Name == tableName && n.FilePath == e.filePath {
			return n.ID
		}
	}
	return ""
}

// luaIndexParts returns the table and member names of a dot_index_expression
// (memberField "field") or method_index_expression (memberField "method").
func luaIndexParts(node *tsparse.Node, memberField string) (table, member string) {
	if t := node.ChildByFieldName("table"); t != nil {
		table = t.Text()
	}
	if m := node.ChildByFieldName(memberField); m != nil {
		member = m.Text()
	}
	return table, member
}

// luaCalleeName resolves a call's name node to a callee name. For obj:method()
// and obj.method() the receiver is stripped to the bare method name (mirroring
// Python's self stripping); a module-table call M.f() keeps "M.f".
func luaCalleeName(nameNode *tsparse.Node) string {
	switch nameNode.Kind() {
	case "identifier":
		return nameNode.Text()
	case "method_index_expression":
		// obj:method() — strip the receiver, keep the method name.
		_, member := luaIndexParts(nameNode, "method")
		return member
	case "dot_index_expression":
		table, member := luaIndexParts(nameNode, "field")
		if table == "" || member == "" {
			return member
		}
		// A simple receiver keeps the "Table.member" form so the resolver can
		// match it against a module table's member; deeper chains (a.b.c) reduce
		// to the trailing member.
		if strings.ContainsAny(table, ".:") {
			return member
		}
		return table + "." + member
	default:
		return ""
	}
}

// luaVarKind classifies an assignment target. A top-level table-constructor
// (`local M = {}`) is the closest thing Lua has to a class/module namespace →
// KindModule; an UPPER_SNAKE name → KindConstant; everything else → KindVariable.
// Nested tables (e.g. a local `self` inside a function) are plain variables.
func luaVarKind(name string, rhs *tsparse.Node, atFileScope bool) model.NodeKind {
	if atFileScope && rhs != nil && rhs.Kind() == "table_constructor" {
		return model.KindModule
	}
	if luaIsConstantName(name) {
		return model.KindConstant
	}
	return model.KindVariable
}

// luaRequireCall returns node when it is a `require(...)` / `require "..."` call,
// else nil.
func luaRequireCall(node *tsparse.Node) *tsparse.Node {
	if node == nil || node.Kind() != "function_call" {
		return nil
	}
	nameNode := node.ChildByFieldName("name")
	if nameNode != nil && nameNode.Kind() == "identifier" && nameNode.Text() == "require" {
		return node
	}
	return nil
}

// luaStringContent returns the inner text of a string node.
func luaStringContent(node *tsparse.Node) string {
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

// luaRequireName reduces a module path to its last segment so it can match a
// defining file (e.g. "geo.util" → "util", "shape" → "shape", "a/b" → "b").
func luaRequireName(path string) string {
	path = strings.ReplaceAll(path, ".", "/")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		path = path[idx+1:]
	}
	return path
}

// luaSignature renders a function's header (function name(params)). table is the
// owning table (if any) and member the function name.
func luaSignature(node *tsparse.Node, table, member string) string {
	name := member
	if table != "" {
		sep := "."
		if node.ChildByFieldName("name") != nil &&
			node.ChildByFieldName("name").Kind() == "method_index_expression" {
			sep = ":"
		}
		name = table + sep + member
	}
	sig := "function " + name
	if params := node.ChildByFieldName("parameters"); params != nil {
		sig += params.Text()
	}
	return sig
}

// luaAssignSignature renders the right-hand side of an assignment as a signature.
func luaAssignSignature(rhs *tsparse.Node) string {
	if rhs == nil {
		return ""
	}
	val := rhs.Text()
	if val == "" {
		return ""
	}
	if len(val) > 100 {
		return "= " + val[:100] + "..."
	}
	return "= " + val
}

// luaIsConstantName reports whether a name is UPPER_SNAKE (only [A-Z0-9_], at
// least one letter).
func luaIsConstantName(name string) bool {
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

// luaBuiltins is the set of Lua stdlib globals/namespaces whose calls produce no
// edges (mirrors the design's skip set).
var luaBuiltins = map[string]bool{
	"print": true, "pairs": true, "ipairs": true, "type": true,
	"tostring": true, "tonumber": true, "setmetatable": true,
	"getmetatable": true, "rawget": true, "rawset": true, "rawequal": true,
	"rawlen": true, "next": true, "select": true, "error": true,
	"assert": true, "pcall": true, "xpcall": true, "require": true,
	"table": true, "string": true, "math": true, "io": true, "os": true,
	"coroutine": true, "debug": true,
}

// luaIsBuiltin reports whether a callee name refers to a Lua stdlib global. A
// dotted name (table.method) is a builtin when its receiver table is a stdlib
// namespace (table/string/math/io/os/...).
func luaIsBuiltin(name string) bool {
	if luaBuiltins[name] {
		return true
	}
	if idx := strings.Index(name, "."); idx > 0 {
		return luaBuiltins[name[:idx]]
	}
	return false
}

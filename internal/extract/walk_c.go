package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkC walks a parsed C (tree-sitter `c`) translation unit and extracts
// symbols. Called by ExtractFile after the file node is emitted.
//
// C is the simplest language of the batch: a single global namespace, no
// classes or modules. The whole story is a global symbol table plus `#include`
// file-dependency resolution. Resolution (resolveCRef) is name-based against the
// global table, with `#include` edges pointing at the in-repo header file node.
//
// Each C construct lives in its own extractC* helper so the forthcoming C++
// walker can reuse them (C++ is a superset: it adds classes/namespaces/templates
// on top of the same declaration/struct/enum/function machinery). The dispatch
// (visitNodeC) and body visitor (visitCBody) are likewise reusable.
//
// Node type reference (tree-sitter-c), confirmed by AST probe:
//
//	translation_unit
//	preproc_include (field "path": string_literal / system_lib_string)
//	preproc_def (field "name" identifier, "value" preproc_arg)
//	preproc_function_def (fields "name", "parameters" preproc_params, "value")
//	function_definition (fields "type", "declarator" function_declarator, "body")
//	declaration (field "type", "declarator": function_declarator (prototype) /
//	    init_declarator / identifier / pointer_declarator)
//	struct_specifier / union_specifier (fields "name" type_identifier,
//	    "body" field_declaration_list)
//	enum_specifier (fields "name", "body" enumerator_list)
//	field_declaration (fields "type", "declarator" field_identifier)
//	enumerator (field "name", optional "value")
//	type_definition (fields "type", "declarator" type_identifier)
//	call_expression (fields "function", "arguments")
//	field_expression (fields "argument", "field")
//	function_declarator (fields "declarator", "parameters")
//	pointer_declarator (field "declarator")
func (e *extractor) walkC(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeC(child)
		}
	}
}

// visitNodeC dispatches a single top-level (or nested) C declaration node.
// Unknown kinds descend into children so declarations wrapped in linkage or
// ERROR subtrees are still seen. Reused by the C++ walker.
func (e *extractor) visitNodeC(node *tsparse.Node) {
	switch node.Kind() {
	case "preproc_include":
		e.extractCInclude(node)
	case "preproc_def":
		e.extractCDefine(node)
	case "preproc_function_def":
		e.extractCFunctionMacro(node)
	case "function_definition":
		e.extractCFunctionDefinition(node)
	case "declaration":
		e.extractCDeclaration(node)
	case "struct_specifier", "union_specifier":
		e.extractCStruct(node)
	case "enum_specifier":
		e.extractCEnum(node)
	case "type_definition":
		e.extractCTypedef(node)
	default:
		for i := 0; i < node.NamedChildCount(); i++ {
			if child := node.NamedChild(i); child != nil {
				e.visitNodeC(child)
			}
		}
	}
}

// extractCInclude handles a preproc_include (`#include "x.h"` / `<x.h>`):
// one KindImport node named by the header path, plus an EdgeImports reference
// from the file node so the resolver can link to the in-repo header.
func (e *extractor) extractCInclude(node *tsparse.Node) {
	pathNode := node.ChildByFieldName("path")
	if pathNode == nil {
		return
	}
	header := cIncludePath(pathNode)
	if header == "" {
		return
	}
	sig := strings.TrimSpace(node.Text())
	e.createNode(model.KindImport, header, node, nodeExtra{signature: sig})

	var fromID string
	if len(e.nodeStack) > 0 {
		fromID = e.nodeStack[len(e.nodeStack)-1]
	}
	if fromID != "" {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: header,
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// extractCDefine handles an object-like macro `#define NAME value` → KindConstant.
func (e *extractor) extractCDefine(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	sig := ""
	if v := node.ChildByFieldName("value"); v != nil {
		sig = "= " + strings.TrimSpace(v.Text())
	}
	e.createNode(model.KindConstant, nameNode.Text(), node, nodeExtra{signature: sig})
}

// extractCFunctionMacro handles a function-like macro `#define NAME(args) body`
// → KindFunction (treated as a callable).
func (e *extractor) extractCFunctionMacro(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	sig := nameNode.Text()
	if p := node.ChildByFieldName("parameters"); p != nil {
		sig += p.Text()
	}
	e.createNode(model.KindFunction, nameNode.Text(), node, nodeExtra{signature: sig})
}

// extractCFunctionDefinition handles a function_definition → KindFunction, and
// walks its body for calls and type references.
func (e *extractor) extractCFunctionDefinition(node *tsparse.Node) {
	decl := node.ChildByFieldName("declarator")
	name := cDeclaratorName(decl)
	if name == "" {
		return
	}
	fn := e.createNode(model.KindFunction, name, node, nodeExtra{
		isStatic:   cIsStatic(node),
		returnType: cTypeName(node.ChildByFieldName("type")),
		signature:  cFunctionSignature(node),
		docstring:  e.lookupDoc(node),
	})
	if fn == nil {
		return
	}
	// references to the return type and parameter types.
	e.emitCTypeRef(fn.ID, node.ChildByFieldName("type"), node)
	e.emitCParamTypeRefs(fn.ID, decl, node)

	body := node.ChildByFieldName("body")
	if body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		e.visitCBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractCDeclaration handles a top-level `declaration`. It is one of:
//   - a function prototype (declarator is a function_declarator) → KindFunction
//   - a variable/constant definition (init_declarator / identifier) →
//     KindConstant when `const`, else KindVariable
func (e *extractor) extractCDeclaration(node *tsparse.Node) {
	decl := node.ChildByFieldName("declarator")
	if decl == nil {
		return
	}
	if cIsFunctionDeclarator(decl) {
		name := cDeclaratorName(decl)
		if name == "" {
			return
		}
		fn := e.createNode(model.KindFunction, name, node, nodeExtra{
			isStatic:   cIsStatic(node),
			returnType: cTypeName(node.ChildByFieldName("type")),
			signature:  cFunctionSignature(node),
			docstring:  e.lookupDoc(node),
		})
		if fn == nil {
			return
		}
		e.emitCTypeRef(fn.ID, node.ChildByFieldName("type"), node)
		e.emitCParamTypeRefs(fn.ID, decl, node)
		return
	}

	// Variable / constant definition.
	name := cDeclaratorName(decl)
	if name == "" {
		return
	}
	kind := model.KindVariable
	if cIsConst(node) {
		kind = model.KindConstant
	}
	vn := e.createNode(kind, name, node, nodeExtra{
		isStatic:  cIsStatic(node),
		signature: cVariableSignature(node),
		docstring: e.lookupDoc(node),
	})
	if vn != nil {
		e.emitCTypeRef(vn.ID, node.ChildByFieldName("type"), node)
	}
}

// extractCStruct handles struct_specifier / union_specifier → KindStruct, with
// KindField members. Anonymous (no name) specifiers are skipped.
func (e *extractor) extractCStruct(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	sn := e.createNode(model.KindStruct, nameNode.Text(), node, nodeExtra{
		docstring: e.lookupDoc(node),
	})
	if sn == nil {
		return
	}
	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, sn.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		fd := body.NamedChild(i)
		if fd == nil || fd.Kind() != "field_declaration" {
			continue
		}
		e.extractCField(fd)
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractCField handles a struct/union field_declaration → KindField, plus a
// reference to the field's type.
func (e *extractor) extractCField(fd *tsparse.Node) {
	decl := fd.ChildByFieldName("declarator")
	name := cDeclaratorName(decl)
	if name == "" {
		return
	}
	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}
	fn := e.createNode(model.KindField, name, fd, nodeExtra{
		signature: strings.TrimSpace(fd.Text()),
	})
	if fn == nil {
		return
	}
	if parentID != "" {
		e.emitCTypeRef(fn.ID, fd.ChildByFieldName("type"), fd)
	}
}

// extractCEnum handles enum_specifier → KindEnum with KindEnumMember members.
// Anonymous enums (no name) are skipped.
func (e *extractor) extractCEnum(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	en := e.createNode(model.KindEnum, nameNode.Text(), node, nodeExtra{
		docstring: e.lookupDoc(node),
	})
	if en == nil {
		return
	}
	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, en.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		if m == nil || m.Kind() != "enumerator" {
			continue
		}
		mn := m.ChildByFieldName("name")
		if mn == nil {
			continue
		}
		e.createNode(model.KindEnumMember, mn.Text(), m, nodeExtra{})
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractCTypedef handles a type_definition (`typedef`) → KindTypeAlias. When
// the typedef wraps a named struct/union/enum, that aggregate is also extracted
// (so `typedef struct Point {...} Point;` yields both the struct and the alias).
func (e *extractor) extractCTypedef(node *tsparse.Node) {
	if t := node.ChildByFieldName("type"); t != nil {
		switch t.Kind() {
		case "struct_specifier", "union_specifier":
			if t.ChildByFieldName("name") != nil {
				e.extractCStruct(t)
			}
		case "enum_specifier":
			if t.ChildByFieldName("name") != nil {
				e.extractCEnum(t)
			}
		}
	}
	decl := node.ChildByFieldName("declarator")
	name := cDeclaratorName(decl)
	if name == "" {
		return
	}
	e.createNode(model.KindTypeAlias, name, node, nodeExtra{
		signature: strings.TrimSpace(node.Text()),
		docstring: e.lookupDoc(node),
	})
}

// visitCBody walks a function body for call_expression nodes, emitting EdgeCalls
// references from the top of the node stack. Reused by the C++ walker.
func (e *extractor) visitCBody(body *tsparse.Node) {
	tsparse.Walk(body, func(node *tsparse.Node) {
		if node.Kind() == "call_expression" {
			e.extractCCall(node)
		}
	})
}

// extractCCall handles a call_expression. The callee name is the bare function
// identifier; `obj->method` / `obj.field` call forms in C are calls through a
// function pointer, so we record the trailing field name.
func (e *extractor) extractCCall(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	callerID := e.nodeStack[len(e.nodeStack)-1]
	fn := node.ChildByFieldName("function")
	name := cCalleeName(fn)
	if name == "" {
		return
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    callerID,
		ReferenceName: name,
		ReferenceKind: model.EdgeCalls,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// emitCTypeRef emits an EdgeReferences ref from fromID to the user-defined type
// named in typeNode (struct/union/enum/typedef names). Built-in primitive types
// are skipped.
func (e *extractor) emitCTypeRef(fromID string, typeNode, at *tsparse.Node) {
	name := cTypeName(typeNode)
	if name == "" || cBuiltinTypes[name] {
		return
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    fromID,
		ReferenceName: name,
		ReferenceKind: model.EdgeReferences,
		Line:          int(at.StartPoint().Row) + 1,
		Column:        int(at.StartPoint().Column),
	})
}

// emitCParamTypeRefs emits EdgeReferences refs for each parameter's user-defined
// type in a function/prototype declarator.
func (e *extractor) emitCParamTypeRefs(fromID string, decl, at *tsparse.Node) {
	fdecl := cFunctionDeclarator(decl)
	if fdecl == nil {
		return
	}
	params := fdecl.ChildByFieldName("parameters")
	if params == nil {
		return
	}
	for i := 0; i < params.NamedChildCount(); i++ {
		p := params.NamedChild(i)
		if p == nil || p.Kind() != "parameter_declaration" {
			continue
		}
		e.emitCTypeRef(fromID, p.ChildByFieldName("type"), at)
	}
}

// ── helpers (reused by the C++ walker) ──────────────────────────────────────

// cIncludePath extracts the header path from a preproc_include's path node,
// stripping the surrounding quotes / angle brackets.
func cIncludePath(pathNode *tsparse.Node) string {
	t := strings.TrimSpace(pathNode.Text())
	t = strings.Trim(t, `"<>`)
	return strings.TrimSpace(t)
}

// cDeclaratorName unwraps a declarator chain (pointer_declarator,
// function_declarator, init_declarator, array_declarator, parenthesized_
// declarator) down to the bound identifier / field_identifier / type_identifier.
func cDeclaratorName(decl *tsparse.Node) string {
	if decl == nil {
		return ""
	}
	switch decl.Kind() {
	case "identifier", "field_identifier", "type_identifier":
		return decl.Text()
	case "pointer_declarator", "function_declarator", "init_declarator",
		"array_declarator", "parenthesized_declarator":
		if inner := decl.ChildByFieldName("declarator"); inner != nil {
			return cDeclaratorName(inner)
		}
		// parenthesized_declarator has no "declarator" field; descend.
		for i := 0; i < decl.NamedChildCount(); i++ {
			if n := cDeclaratorName(decl.NamedChild(i)); n != "" {
				return n
			}
		}
	}
	return ""
}

// cFunctionDeclarator returns the function_declarator inside a declarator chain,
// or nil if the declarator is not a function.
func cFunctionDeclarator(decl *tsparse.Node) *tsparse.Node {
	if decl == nil {
		return nil
	}
	switch decl.Kind() {
	case "function_declarator":
		return decl
	case "pointer_declarator", "init_declarator", "parenthesized_declarator":
		if inner := decl.ChildByFieldName("declarator"); inner != nil {
			return cFunctionDeclarator(inner)
		}
		for i := 0; i < decl.NamedChildCount(); i++ {
			if f := cFunctionDeclarator(decl.NamedChild(i)); f != nil {
				return f
			}
		}
	}
	return nil
}

// cIsFunctionDeclarator reports whether decl declares a function (prototype).
func cIsFunctionDeclarator(decl *tsparse.Node) bool {
	return cFunctionDeclarator(decl) != nil
}

// cTypeName extracts the bare user-type name from a type node. Returns "" for
// primitive types' bare text (still useful as a name) — primitive filtering is
// the caller's job via cBuiltinTypes. struct/union/enum specifiers yield their
// tag name.
func cTypeName(typeNode *tsparse.Node) string {
	if typeNode == nil {
		return ""
	}
	switch typeNode.Kind() {
	case "type_identifier", "primitive_type", "sized_type_specifier":
		return strings.TrimSpace(typeNode.Text())
	case "struct_specifier", "union_specifier", "enum_specifier":
		if n := typeNode.ChildByFieldName("name"); n != nil {
			return n.Text()
		}
	}
	return ""
}

// cCalleeName resolves a call's function node to a callee name.
//   - identifier      → bare name
//   - field_expression (fp->call / s.call) → the trailing field name
//   - parenthesized_expression ((*fp)())    → descend
func cCalleeName(fn *tsparse.Node) string {
	if fn == nil {
		return ""
	}
	switch fn.Kind() {
	case "identifier":
		return fn.Text()
	case "field_expression":
		if f := fn.ChildByFieldName("field"); f != nil {
			return f.Text()
		}
	case "parenthesized_expression", "pointer_expression":
		for i := 0; i < fn.NamedChildCount(); i++ {
			if n := cCalleeName(fn.NamedChild(i)); n != "" {
				return n
			}
		}
	}
	return ""
}

// cIsStatic reports whether a declaration carries the `static` storage class.
func cIsStatic(node *tsparse.Node) bool {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "storage_class_specifier" &&
			strings.TrimSpace(c.Text()) == "static" {
			return true
		}
	}
	return false
}

// cIsConst reports whether a declaration carries a `const` type qualifier.
func cIsConst(node *tsparse.Node) bool {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "type_qualifier" &&
			strings.TrimSpace(c.Text()) == "const" {
			return true
		}
	}
	return false
}

// cFunctionSignature builds a "ret name(params)" line (no body).
func cFunctionSignature(node *tsparse.Node) string {
	var b strings.Builder
	if t := node.ChildByFieldName("type"); t != nil {
		b.WriteString(strings.TrimSpace(t.Text()))
		b.WriteString(" ")
	}
	if d := node.ChildByFieldName("declarator"); d != nil {
		b.WriteString(strings.TrimSpace(d.Text()))
	}
	return strings.TrimSpace(b.String())
}

// cVariableSignature renders a variable/constant declaration as a signature,
// trimming the trailing semicolon.
func cVariableSignature(node *tsparse.Node) string {
	s := strings.TrimSpace(node.Text())
	s = strings.TrimSuffix(s, ";")
	return strings.TrimSpace(s)
}

// cBuiltinTypes is the set of C primitive / fixed-width type names that never
// resolve to a user-defined node (so type references to them produce no edge).
var cBuiltinTypes = map[string]bool{
	"void": true, "char": true, "short": true, "int": true, "long": true,
	"float": true, "double": true, "signed": true, "unsigned": true,
	"_Bool": true, "bool": true, "size_t": true, "ssize_t": true,
	"ptrdiff_t": true, "wchar_t": true, "intptr_t": true, "uintptr_t": true,
	"int8_t": true, "int16_t": true, "int32_t": true, "int64_t": true,
	"uint8_t": true, "uint16_t": true, "uint32_t": true, "uint64_t": true,
	"FILE": true, "va_list": true,
}

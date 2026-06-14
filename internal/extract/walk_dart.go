package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkDart walks a parsed Dart (tree-sitter `dart`) file root and extracts
// symbols. Called by ExtractFile after the file node is emitted.
//
// Node type reference (tree-sitter-dart), verified by probe:
//
//	program — top-level container
//	import_or_export → library_import → import_specification → configurable_uri → uri
//	class_definition (fields: name, superclass, interfaces, body; `abstract` token)
//	  superclass → type_identifier + mixins(type_identifier…)
//	  interfaces → type_identifier…
//	mixin_declaration (identifier child + class_body)
//	enum_declaration (fields: name, body=enum_body; enum_constant fields: name)
//	type_alias (type_identifier name + RHS type)
//	function_signature (fields: name) + sibling function_body — top-level fn / method
//	method_signature → function_signature / getter_signature / setter_signature
//	getter_signature / setter_signature (fields: name)
//	constructor_signature (field name; optional `.` name for named ctors)
//	declaration — wraps class members: fields, ctors, methods
//	  field via final_builtin/const_builtin/inferred_type/type + initialized_identifier_list
//	  or static_final_declaration_list → static_final_declaration
//	annotation (field name) → @override etc.
//	new_expression (type_identifier) — `new Type(...)`
//	selector / unconditional_assignable_selector — `.method`, argument_part for calls
//	initialized_variable_definition (fields: name, value) — `var x = Type(...)`
//
// Dart is library-flat (no namespaces). Privacy is by leading underscore.
func (e *extractor) walkDart(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeDart(child)
		}
	}
}

// visitNodeDart dispatches a single top-level or nested Dart node. Unknown kinds
// descend into named children so calls/instantiations nested inside other
// constructs are seen.
func (e *extractor) visitNodeDart(node *tsparse.Node) {
	switch node.Kind() {
	case "import_or_export":
		e.extractDartImport(node)
	case "class_definition":
		e.extractDartClass(node)
	case "mixin_declaration":
		e.extractDartMixin(node)
	case "extension_declaration":
		e.extractDartExtension(node)
	case "enum_declaration":
		e.extractDartEnum(node)
	case "type_alias":
		e.extractDartTypeAlias(node)
	case "function_signature":
		// Top-level function: name in this node, body is the following sibling
		// (handled by the parent loop visiting function_body separately, which we
		// attach to this function via a pending stack). Simpler: a top-level
		// function_signature is its own function; we descend into the sibling body
		// when we encounter it. To keep the call graph correct we treat the
		// function_signature + immediate function_body as a unit handled here is
		// not possible (sibling). Instead emit the function node and let the
		// following function_body be associated by extractDartTopLevelBody.
		e.extractDartTopLevelFunction(node)
	case "function_body":
		// A top-level function_body following a function_signature: descend for
		// calls, attributing them to the pending top-level function.
		e.visitDartMemberBody(node)
	case "new_expression":
		e.extractDartNewExpression(node)
	case "initialized_variable_definition", "local_variable_declaration":
		e.extractDartLocalVar(node)
	case "expression_statement", "return_statement":
		e.visitDartBody(node)
	case "identifier":
		// bare identifier; nothing to do (selectors handle calls)
	default:
		e.visitDartBody(node)
	}
}

// visitDartBody descends into a node's named children, extracting call /
// instantiation references but emitting no node for the container itself.
func (e *extractor) visitDartBody(node *tsparse.Node) {
	// Detect call / instantiation patterns: identifier followed by selector(s).
	e.extractDartCallChain(node)
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodeDart(child)
		}
	}
}

// extractDartImport handles import_or_export. The imported URI's last path
// segment becomes a KindImport node name; an EdgeImports ref carries the raw URI
// (relative imports resolve to the in-repo file; package:/dart: stay at the node).
func (e *extractor) extractDartImport(node *tsparse.Node) {
	uri := dartImportURI(node)
	if uri == "" {
		return
	}
	name := dartImportName(uri)
	if name == "" {
		return
	}
	e.createNode(model.KindImport, name, node, nodeExtra{signature: strings.TrimSpace(node.Text())})

	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}
	if parentID != "" {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    parentID,
			ReferenceName: uri,
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// dartImportName reduces an import URI to the simple imported-library name: the
// last path segment after the final '/' or ':' (`dart:math`→"math",
// `package:flutter/material.dart`→"material.dart", `shape.dart`→"shape.dart").
func dartImportName(uri string) string {
	name := uri
	if idx := strings.LastIndexAny(name, "/:"); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

// dartImportURI extracts the URI string (without quotes) from an import_or_export.
func dartImportURI(node *tsparse.Node) string {
	var uri string
	tsparse.Walk(node, func(n *tsparse.Node) {
		if uri != "" {
			return
		}
		if n.Kind() == "uri" {
			t := n.Text()
			t = strings.Trim(t, "'\"")
			uri = t
		}
	})
	return uri
}

// extractDartClass handles class_definition (including abstract classes).
func (e *extractor) extractDartClass(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	decorators := dartAnnotations(node)
	cn := e.createNode(model.KindClass, name, node, nodeExtra{
		signature:      dartClassSignature(node, name),
		visibility:     dartVisibility(name),
		isExported:     dartIsPublic(name),
		isAbstract:     dartHasToken(node, "abstract"),
		decorators:     decorators,
		typeParameters: dartTypeParameters(node),
	})
	if cn == nil {
		return
	}
	e.emitDartDecorates(cn.ID, decorators, node)

	// superclass: `extends Base with Mixin…`
	if sc := node.ChildByFieldName("superclass"); sc != nil {
		for i := 0; i < sc.NamedChildCount(); i++ {
			c := sc.NamedChild(i)
			if c == nil {
				continue
			}
			switch c.Kind() {
			case "type_identifier":
				e.emitDartTypeRef(cn.ID, dartTypeName(c), model.EdgeExtends, c)
			case "mixins":
				// `with Mixin…` — treat mixins as implements (spec).
				for j := 0; j < c.NamedChildCount(); j++ {
					m := c.NamedChild(j)
					if m != nil && m.Kind() == "type_identifier" {
						e.emitDartTypeRef(cn.ID, dartTypeName(m), model.EdgeImplements, m)
					}
				}
			}
		}
	}

	// interfaces: `implements Iface…`
	if ifc := node.ChildByFieldName("interfaces"); ifc != nil {
		for i := 0; i < ifc.NamedChildCount(); i++ {
			c := ifc.NamedChild(i)
			if c != nil && c.Kind() == "type_identifier" {
				e.emitDartTypeRef(cn.ID, dartTypeName(c), model.EdgeImplements, c)
			}
		}
	}

	if body := node.ChildByFieldName("body"); body != nil {
		e.nodeStack = append(e.nodeStack, cn.ID)
		e.walkDartClassBody(body, name)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractDartMixin handles mixin_declaration. The name is an identifier child.
func (e *extractor) extractDartMixin(node *tsparse.Node) {
	var name string
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "identifier" {
			name = c.Text()
			break
		}
	}
	if name == "" {
		return
	}
	mn := e.createNode(model.KindClass, name, node, nodeExtra{
		signature:  "mixin " + name,
		visibility: dartVisibility(name),
		isExported: dartIsPublic(name),
	})
	if mn == nil {
		return
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if body := node.NamedChild(i); body != nil && body.Kind() == "class_body" {
			e.nodeStack = append(e.nodeStack, mn.ID)
			e.walkDartClassBody(body, name)
			e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
		}
	}
}

// extractDartExtension handles extension_declaration: members attach to the
// extension node (named or anonymous). Documented limitation: receiver-precise
// resolution is out of scope.
func (e *extractor) extractDartExtension(node *tsparse.Node) {
	var name string
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "identifier" {
			name = c.Text()
			break
		}
	}
	if name == "" {
		name = "extension"
	}
	xn := e.createNode(model.KindClass, name, node, nodeExtra{
		signature:  "extension " + name,
		visibility: dartVisibility(name),
		isExported: dartIsPublic(name),
	})
	if xn == nil {
		return
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if body := node.NamedChild(i); body != nil && body.Kind() == "class_body" {
			e.nodeStack = append(e.nodeStack, xn.ID)
			e.walkDartClassBody(body, name)
			e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
		}
	}
}

// walkDartClassBody walks a class_body, emitting members. Dart nests members as
// `declaration` wrappers, or as `method_signature`+`function_body` sibling pairs.
func (e *extractor) walkDartClassBody(body *tsparse.Node, className string) {
	for i := 0; i < body.NamedChildCount(); i++ {
		c := body.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "declaration":
			e.extractDartDeclaration(c, className)
		case "method_signature":
			e.extractDartMethodSig(c, className)
		case "function_body":
			// body following the previous method_signature: descend for calls
			// using the most-recently-pushed member as caller.
			e.visitDartMemberBody(c)
		}
	}
}

// extractDartDeclaration handles a `declaration` wrapper inside a class body:
// fields, constructors, or inline methods.
func (e *extractor) extractDartDeclaration(node *tsparse.Node, className string) {
	// Constructor: constructor_signature child.
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "constructor_signature":
			e.extractDartConstructor(node, c, className)
			return
		case "function_signature":
			// inline method: `double area() => …` with body sibling in declaration.
			e.extractDartMethodFromSig(node, c)
			return
		case "getter_signature", "setter_signature":
			e.extractDartGetterSetter(c)
			return
		}
	}
	// Otherwise a field declaration.
	e.extractDartField(node)
}

// extractDartConstructor handles a constructor_signature → KindMethod.
func (e *extractor) extractDartConstructor(decl, sig *tsparse.Node, className string) {
	name := className
	// named constructor: `Circle.unit` → two identifier children; use full form.
	var ids []string
	for i := 0; i < sig.NamedChildCount(); i++ {
		if c := sig.NamedChild(i); c != nil && c.Kind() == "identifier" {
			ids = append(ids, c.Text())
		}
	}
	if len(ids) >= 2 {
		name = ids[0] + "." + ids[1]
	} else if len(ids) == 1 {
		name = ids[0]
	}
	fn := e.createNode(model.KindMethod, name, sig, nodeExtra{
		signature:  strings.TrimSpace(sig.Text()),
		visibility: dartVisibility(name),
		isExported: dartIsPublic(name),
	})
	if fn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, fn.ID)
	e.descendDartDeclBodies(decl)
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractDartMethodFromSig handles an inline method declaration (function_signature
// inside a declaration, body as a sibling within the same declaration).
func (e *extractor) extractDartMethodFromSig(decl, sig *tsparse.Node) {
	name := dartSigName(sig)
	if name == "" {
		return
	}
	fn := e.createNode(model.KindMethod, name, sig, nodeExtra{
		signature:  strings.TrimSpace(sig.Text()),
		visibility: dartVisibility(name),
		isExported: dartIsPublic(name),
		returnType: dartReturnType(sig),
	})
	if fn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, fn.ID)
	e.descendDartDeclBodies(decl)
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractDartMethodSig handles a method_signature class member (its body is the
// following function_body sibling in the class body).
func (e *extractor) extractDartMethodSig(node *tsparse.Node, className string) {
	decorators := dartAnnotations(node)
	var sig *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "function_signature", "getter_signature", "setter_signature", "factory_constructor_signature", "constructor_signature":
			sig = c
		}
	}
	if sig == nil {
		return
	}
	if sig.Kind() == "getter_signature" || sig.Kind() == "setter_signature" {
		e.extractDartGetterSetter(sig)
		return
	}
	name := dartSigName(sig)
	if name == "" {
		return
	}
	fn := e.createNode(model.KindMethod, name, node, nodeExtra{
		signature:  strings.TrimSpace(sig.Text()),
		visibility: dartVisibility(name),
		isExported: dartIsPublic(name),
		isAbstract: true, // method_signature with no inline body is abstract-ish
		returnType: dartReturnType(sig),
		decorators: decorators,
	})
	if fn == nil {
		return
	}
	e.emitDartDecorates(fn.ID, decorators, node)
	// Push so the following function_body sibling (handled in walkDartClassBody)
	// associates its calls with this method.
	e.dartPendingMember = fn.ID
}

// extractDartGetterSetter handles getter_signature / setter_signature → KindProperty.
func (e *extractor) extractDartGetterSetter(sig *tsparse.Node) {
	name := dartSigName(sig)
	if name == "" {
		return
	}
	e.createNode(model.KindProperty, name, sig, nodeExtra{
		signature:  strings.TrimSpace(sig.Text()),
		visibility: dartVisibility(name),
		isExported: dartIsPublic(name),
		returnType: dartReturnType(sig),
	})
}

// extractDartField handles a field declaration inside a class body. Each
// identifier in initialized_identifier_list / static_final_declaration_list
// becomes a KindField (or KindConstant when const/final-static).
func (e *extractor) extractDartField(node *tsparse.Node) {
	isConst := dartHasToken(node, "const") || dartHasChildKind(node, "const_builtin")
	isStatic := dartHasToken(node, "static")
	typ := dartFieldType(node)

	emit := func(name string, anchor *tsparse.Node) {
		if name == "" {
			return
		}
		kind := model.KindField
		if isConst {
			kind = model.KindConstant
		}
		e.createNode(kind, name, anchor, nodeExtra{
			signature:  strings.TrimSpace(typ + " " + name),
			visibility: dartVisibility(name),
			isExported: dartIsPublic(name),
			isStatic:   isStatic,
			returnType: typ,
		})
	}

	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "initialized_identifier_list":
			for j := 0; j < c.NamedChildCount(); j++ {
				ii := c.NamedChild(j)
				if ii == nil || ii.Kind() != "initialized_identifier" {
					continue
				}
				if id := dartFirstIdentifier(ii); id != "" {
					emit(id, ii)
				}
			}
		case "static_final_declaration_list":
			for j := 0; j < c.NamedChildCount(); j++ {
				sd := c.NamedChild(j)
				if sd == nil || sd.Kind() != "static_final_declaration" {
					continue
				}
				if id := dartFirstIdentifier(sd); id != "" {
					emit(id, sd)
				}
			}
		case "initialized_identifier":
			if id := dartFirstIdentifier(c); id != "" {
				emit(id, c)
			}
		}
	}
}

// extractDartEnum handles enum_declaration: the enum node plus an EnumMember per
// enum_constant.
func (e *extractor) extractDartEnum(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	en := e.createNode(model.KindEnum, name, node, nodeExtra{
		visibility: dartVisibility(name),
		isExported: dartIsPublic(name),
		decorators: dartAnnotations(node),
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
		c := body.NamedChild(i)
		if c == nil || c.Kind() != "enum_constant" {
			continue
		}
		mn := c.ChildByFieldName("name")
		if mn == nil {
			mn = c
		}
		e.createNode(model.KindEnumMember, mn.Text(), c, nodeExtra{})
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractDartTypeAlias handles type_alias → KindTypeAlias. The name is the first
// type_identifier child.
func (e *extractor) extractDartTypeAlias(node *tsparse.Node) {
	var name string
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "type_identifier" {
			name = c.Text()
			break
		}
	}
	if name == "" {
		return
	}
	e.createNode(model.KindTypeAlias, name, node, nodeExtra{
		signature:  strings.TrimSpace(node.Text()),
		visibility: dartVisibility(name),
		isExported: dartIsPublic(name),
	})
}

// extractDartTopLevelFunction handles a top-level function_signature. Its body is
// the following sibling function_body, handled by the root loop; we associate it
// via dartPendingMember.
func (e *extractor) extractDartTopLevelFunction(node *tsparse.Node) {
	name := dartSigName(node)
	if name == "" {
		return
	}
	if node.Kind() == "function_signature" {
		// only emit if this is truly a function_signature (not a getter/setter).
	}
	fn := e.createNode(model.KindFunction, name, node, nodeExtra{
		signature:  strings.TrimSpace(node.Text()),
		visibility: dartVisibility(name),
		isExported: dartIsPublic(name),
		returnType: dartReturnType(node),
	})
	if fn == nil {
		return
	}
	e.dartPendingMember = fn.ID
}

// visitDartMemberBody descends a class member's function_body, attributing calls
// to dartPendingMember (the most recently emitted method).
func (e *extractor) visitDartMemberBody(body *tsparse.Node) {
	popped := false
	if e.dartPendingMember != "" {
		e.nodeStack = append(e.nodeStack, e.dartPendingMember)
		e.dartPendingMember = ""
		popped = true
	}
	e.visitDartBody(body)
	if popped {
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// descendDartDeclBodies descends into the bodies/initializers of a class-member
// declaration (function_body, initializers) for calls/instantiations.
func (e *extractor) descendDartDeclBodies(decl *tsparse.Node) {
	for i := 0; i < decl.NamedChildCount(); i++ {
		c := decl.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "function_body", "initializers", "block":
			e.visitDartBody(c)
		}
	}
}

// extractDartLocalVar handles initialized_variable_definition /
// local_variable_declaration, descending into the value for instantiations/calls.
func (e *extractor) extractDartLocalVar(node *tsparse.Node) {
	// `var c = Circle(2.0)` — value is identifier + selector(args).
	e.extractDartCallChain(node)
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil {
			switch c.Kind() {
			case "new_expression":
				e.extractDartNewExpression(c)
			case "identifier", "selector", "inferred_type", "type_identifier":
				// handled by extractDartCallChain
			default:
				e.visitNodeDart(c)
			}
		}
	}
}

// extractDartNewExpression handles `new Type(...)` → EdgeInstantiates.
func (e *extractor) extractDartNewExpression(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	callerID := e.nodeStack[len(e.nodeStack)-1]
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "type_identifier" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: dartTypeName(c),
				ReferenceKind: model.EdgeInstantiates,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
			break
		}
	}
	if args := dartFindChild(node, "arguments"); args != nil {
		e.visitDartBody(args)
	}
}

// extractDartCallChain detects an identifier followed by selector children that
// form a call or constructor invocation: `foo(...)`, `recv.method(...)`,
// `Type(...)`, `Type.named(...)`. Emits EdgeCalls refs (the resolver promotes
// calls whose target is a class to EdgeInstantiates).
func (e *extractor) extractDartCallChain(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	callerID := e.nodeStack[len(e.nodeStack)-1]

	// Scan named children for the [identifier, selector…] pattern at this level.
	n := node.NamedChildCount()
	for i := 0; i < n; i++ {
		head := node.NamedChild(i)
		if head == nil || head.Kind() != "identifier" {
			continue
		}
		// Collect the following selector run.
		base := head.Text()
		dottedName := base
		invoked := false
		j := i + 1
		for ; j < n; j++ {
			sel := node.NamedChild(j)
			if sel == nil || sel.Kind() != "selector" {
				break
			}
			if as := dartFindChild(sel, "unconditional_assignable_selector"); as != nil {
				if id := dartFirstIdentifier(as); id != "" {
					dottedName = dottedName + "." + id
				}
			} else if dartFindChild(sel, "argument_part") != nil {
				invoked = true
				j++ // consume the argument selector too
				break
			}
		}
		if invoked {
			// Build callee name: for `recv.method(...)` use `recv.method`; for a
			// bare `foo(...)` use `foo`; for `Type.named(...)` use `Type.named`.
			callee := dottedName
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: callee,
				ReferenceKind: model.EdgeCalls,
				Line:          int(head.StartPoint().Row) + 1,
				Column:        int(head.StartPoint().Column),
			})
		}
	}
}

// emitDartTypeRef appends a type reference edge of the given kind.
func (e *extractor) emitDartTypeRef(fromID, name string, kind model.EdgeKind, node *tsparse.Node) {
	if name == "" {
		return
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    fromID,
		ReferenceName: name,
		ReferenceKind: kind,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// emitDartDecorates emits an EdgeDecorates ref per annotation head name.
func (e *extractor) emitDartDecorates(fromID string, decorators []string, node *tsparse.Node) {
	for _, d := range decorators {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: d,
			ReferenceKind: model.EdgeDecorates,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// dartVisibility returns "private" for leading-underscore names, else nil
// (Dart's only access control is library-private via leading underscore).
func dartVisibility(name string) *string {
	if strings.HasPrefix(name, "_") {
		v := "private"
		return &v
	}
	return nil
}

func dartIsPublic(name string) bool {
	return name != "" && !strings.HasPrefix(name, "_")
}

// dartHasToken reports whether the declaration carries the given keyword token
// among its direct (named or anonymous) children.
func dartHasToken(node *tsparse.Node, tok string) bool {
	for i := 0; i < node.ChildCount(); i++ {
		if c := node.Child(i); c != nil && !c.IsNamed() && c.Text() == tok {
			return true
		}
	}
	// Also check named keyword nodes (e.g. const_builtin renders "const").
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Text() == tok {
			return true
		}
	}
	return false
}

func dartHasChildKind(node *tsparse.Node, kind string) bool {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == kind {
			return true
		}
	}
	return false
}

// dartTypeName reduces a type node to its simple name (strips generics/library
// prefixes).
func dartTypeName(node *tsparse.Node) string {
	t := node.Text()
	if idx := strings.IndexByte(t, '<'); idx >= 0 {
		t = t[:idx]
	}
	if idx := strings.LastIndexByte(t, '.'); idx >= 0 {
		t = t[idx+1:]
	}
	return strings.TrimSpace(t)
}

// dartSigName returns the declared name of a function/getter/setter signature.
func dartSigName(sig *tsparse.Node) string {
	if nn := sig.ChildByFieldName("name"); nn != nil {
		return nn.Text()
	}
	for i := 0; i < sig.NamedChildCount(); i++ {
		if c := sig.NamedChild(i); c != nil && c.Kind() == "identifier" {
			return c.Text()
		}
	}
	return ""
}

// dartReturnType returns the leading type of a signature (type_identifier /
// void_type), or "".
func dartReturnType(sig *tsparse.Node) string {
	for i := 0; i < sig.NamedChildCount(); i++ {
		c := sig.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "type_identifier", "void_type":
			return c.Text()
		}
	}
	return ""
}

// dartFieldType returns the type text of a field declaration, or "".
func dartFieldType(node *tsparse.Node) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "type_identifier", "void_type", "function_type":
			return c.Text()
		}
	}
	return ""
}

// dartFirstIdentifier returns the text of the first identifier descendant.
func dartFirstIdentifier(node *tsparse.Node) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "identifier" {
			return c.Text()
		}
	}
	if node.Kind() == "identifier" {
		return node.Text()
	}
	return ""
}

// dartFindChild returns the first named child of the given kind, or nil.
func dartFindChild(node *tsparse.Node, kind string) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// dartAnnotations returns the head names of all annotations on a declaration
// (`@override` → "override", `@Deprecated("…")` → "Deprecated").
func dartAnnotations(node *tsparse.Node) []string {
	var out []string
	for i := 0; i < node.NamedChildCount(); i++ {
		a := node.NamedChild(i)
		if a == nil || a.Kind() != "annotation" {
			continue
		}
		if nn := a.ChildByFieldName("name"); nn != nil {
			out = append(out, nn.Text())
		} else if id := dartFirstIdentifier(a); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// dartTypeParameters returns generic type-parameter names of a declaration.
func dartTypeParameters(node *tsparse.Node) []string {
	var tpl *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "type_parameters" {
			tpl = c
			break
		}
	}
	if tpl == nil {
		return nil
	}
	var out []string
	for i := 0; i < tpl.NamedChildCount(); i++ {
		p := tpl.NamedChild(i)
		if p == nil {
			continue
		}
		if id := dartFirstIdentifier(p); id != "" {
			out = append(out, id)
		} else if p.Kind() == "type_identifier" {
			out = append(out, p.Text())
		}
	}
	return out
}

// dartClassSignature renders a one-line header for a class declaration.
func dartClassSignature(node *tsparse.Node, name string) string {
	sig := "class " + name
	if dartHasToken(node, "abstract") {
		sig = "abstract " + sig
	}
	return sig
}

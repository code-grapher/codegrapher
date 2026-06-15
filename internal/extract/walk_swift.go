package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkSwift walks a parsed Swift (tree-sitter `swift`) source file root and
// extracts symbols. Called by ExtractFile after the file node is emitted.
//
// Swift is statically typed and protocol-oriented with NO source-level
// namespaces (modules are compilation units, not declared in source), so the
// symbol space is module-flat. Resolution (resolve.go: resolveSwiftRef) is
// by-name against the global table with same-file/same-dir preference.
//
// Node type reference (tree-sitter-swift), confirmed by AST probe:
//
//	source_file
//	import_declaration (identifier → simple_identifier head)
//	protocol_declaration (field "name" type_identifier, "body" protocol_body)
//	  protocol_function_declaration (field "name" simple_identifier)
//	class_declaration — covers class/struct/actor/enum/extension via the
//	  "declaration_kind" field child ("class"/"struct"/"actor"/"enum"/
//	  "extension"). For class/struct/actor/enum the "name" field is a
//	  type_identifier; for extension the "name" field is a user_type (the
//	  EXTENDED type). Body is "class_body" (or "enum_class_body" for enum).
//	  inheritance_specifier (field "inherits_from" user_type) lists supertypes.
//	function_declaration (field "name" simple_identifier, "return_type",
//	  "body" function_body; "modifiers" child holds visibility/static/override/
//	  attributes)
//	init_declaration ("init" keyword; "body" function_body) → constructor method
//	property_declaration (value_binding_pattern mutability let/var; "name" field
//	  is a pattern → bound_identifier simple_identifier; optional type_annotation;
//	  optional "value")
//	enum_entry (field "name" simple_identifier)
//	typealias_declaration (field "name" type_identifier, "value")
//	call_expression: function is a simple_identifier (bare call / Type(...)) or a
//	  navigation_expression (target . navigation_suffix.suffix → method name)
//	modifiers → visibility_modifier / property_modifier (static) /
//	  member_modifier (override) / attribute (@objc …)
func (e *extractor) walkSwift(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeSwift(child)
		}
	}
}

// visitNodeSwift dispatches a single declaration node. Unknown kinds descend
// into children so nested declarations are still seen.
func (e *extractor) visitNodeSwift(node *tsparse.Node) {
	switch node.Kind() {
	case "import_declaration":
		e.extractSwiftImport(node)
	case "protocol_declaration":
		e.extractSwiftProtocol(node)
	case "class_declaration":
		e.extractSwiftClassLike(node)
	case "function_declaration":
		e.extractSwiftFunction(node)
	case "property_declaration":
		e.extractSwiftProperty(node)
	case "typealias_declaration":
		e.extractSwiftTypeAlias(node)
	default:
		for i := 0; i < node.NamedChildCount(); i++ {
			if child := node.NamedChild(i); child != nil {
				e.visitNodeSwift(child)
			}
		}
	}
}

// extractSwiftImport handles `import Foo` → one KindImport node named after the
// head module segment, plus an EdgeImports ref from the current scope. Swift
// imports usually name an external module, so this typically stays unresolved
// (resolved to the import node) — which is fine.
func (e *extractor) extractSwiftImport(node *tsparse.Node) {
	name := ""
	if id := swiftChildOfKind(node, "identifier"); id != nil {
		// `import Foo.Bar` → head segment "Foo".
		if si := swiftChildOfKind(id, "simple_identifier"); si != nil {
			name = si.Text()
		} else {
			name = swiftFirstPathSegment(id.Text())
		}
	}
	if name == "" {
		name = swiftFirstPathSegment(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(node.Text()), "import")))
	}
	if name == "" {
		return
	}
	sig := strings.TrimSpace(node.Text())
	e.createNode(model.KindImport, name, node, nodeExtra{signature: sig})
	if len(e.nodeStack) > 0 {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    e.nodeStack[len(e.nodeStack)-1],
			ReferenceName: name,
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// extractSwiftProtocol handles a protocol_declaration → KindInterface. Its
// method/property requirements become contained member nodes; inheritance
// specifiers (protocol refinement) become implements edges.
func (e *extractor) extractSwiftProtocol(node *tsparse.Node) {
	name := swiftDeclName(node)
	if name == "" {
		return
	}
	tn := e.createNode(model.KindInterface, name, node, nodeExtra{
		visibility:     swiftVisibility(node),
		isExported:     swiftIsExported(node),
		decorators:     swiftAttributes(node),
		typeParameters: swiftTypeParams(node),
	})
	if tn == nil {
		return
	}
	e.emitSwiftInheritance(node, tn.ID)

	body := swiftChildOfKind(node, "protocol_body")
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, tn.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "protocol_function_declaration", "function_declaration":
			e.extractSwiftFunction(child)
		case "protocol_property_declaration", "property_declaration":
			e.extractSwiftProperty(child)
		default:
			e.visitNodeSwift(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractSwiftClassLike handles a class_declaration, which the grammar uses for
// class / struct / actor / enum / extension — distinguished by the
// "declaration_kind" field. struct → KindStruct; actor/class → KindClass;
// enum → KindEnum; extension → members attach to the EXTENDED type (qualified
// Type::member) like a Rust impl block.
func (e *extractor) extractSwiftClassLike(node *tsparse.Node) {
	declKind := ""
	if dk := node.ChildByFieldName("declaration_kind"); dk != nil {
		declKind = strings.TrimSpace(dk.Text())
	}
	if declKind == "extension" {
		e.extractSwiftExtension(node)
		return
	}

	name := swiftDeclName(node)
	if name == "" {
		return
	}
	var kind model.NodeKind
	switch declKind {
	case "struct":
		kind = model.KindStruct
	case "enum":
		kind = model.KindEnum
	default: // "class", "actor" (actor → class)
		kind = model.KindClass
	}
	tn := e.createNode(kind, name, node, nodeExtra{
		visibility:     swiftVisibility(node),
		isExported:     swiftIsExported(node),
		decorators:     swiftAttributes(node),
		typeParameters: swiftTypeParams(node),
	})
	if tn == nil {
		return
	}
	e.emitSwiftInheritance(node, tn.ID)

	body := swiftClassBody(node)
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, tn.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "enum_entry":
			e.extractSwiftEnumEntry(child)
		default:
			e.visitNodeSwift(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractSwiftExtension handles `extension Type [: Protocol…] { … }`. Members
// attach to the extended type (qualified Type::member) like a Rust impl block;
// inheritance specifiers add implements edges on the extended type's node when
// present in this file.
func (e *extractor) extractSwiftExtension(node *tsparse.Node) {
	extType := swiftBaseTypeName(node.ChildByFieldName("name"))
	if extType == "" {
		return
	}

	// implements edges from the extended type to each conformed protocol.
	if typeNode := e.findSwiftTypeNode(extType); typeNode != nil {
		e.emitSwiftInheritance(node, typeNode.ID)
	}

	body := swiftClassBody(node)
	if body == nil {
		return
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "function_declaration":
			e.extractSwiftExtMethod(child, extType)
		case "init_declaration":
			e.extractSwiftExtInit(child, extType)
		case "property_declaration":
			e.extractSwiftExtProperty(child, extType)
		case "typealias_declaration":
			e.extractSwiftTypeAlias(child)
		default:
			e.visitNodeSwift(child)
		}
	}
}

// emitSwiftInheritance walks inheritance_specifier children and emits one
// reference per supertype from fromID. Classification (extends vs implements)
// is deferred to the resolver: the FIRST inheritance entry that resolves to a
// class becomes extends; protocol-resolving entries become implements. We tag
// only the first entry as a candidate superclass (EdgeExtends) and the rest as
// implements; the resolver reclassifies an EdgeExtends whose target is an
// interface into implements (mirrors C#). Unresolved first-entry extends stays
// extends only if it actually resolves to a class — otherwise it drops.
func (e *extractor) emitSwiftInheritance(node *tsparse.Node, fromID string) {
	first := true
	for i := 0; i < node.NamedChildCount(); i++ {
		spec := node.NamedChild(i)
		if spec == nil || spec.Kind() != "inheritance_specifier" {
			continue
		}
		tn := spec.ChildByFieldName("inherits_from")
		if tn == nil {
			tn = spec
		}
		typeName := swiftBaseTypeName(tn)
		if typeName == "" {
			continue
		}
		kind := model.EdgeImplements
		if first {
			kind = model.EdgeExtends
		}
		first = false
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: typeName,
			ReferenceKind: kind,
			Line:          int(spec.StartPoint().Row) + 1,
			Column:        int(spec.StartPoint().Column),
			FilePath:      e.filePath,
			Language:      e.lang,
		})
	}
}

// findSwiftTypeNode returns the class/struct/enum/interface node named name
// defined in this file, or nil.
func (e *extractor) findSwiftTypeNode(name string) *model.Node {
	for i := range e.nodes {
		n := &e.nodes[i]
		if n.Name == name && n.FilePath == e.filePath &&
			(n.Kind == model.KindClass || n.Kind == model.KindStruct ||
				n.Kind == model.KindEnum || n.Kind == model.KindInterface) {
			return n
		}
	}
	return nil
}

// extractSwiftEnumEntry handles an enum_entry (`case foo`) → KindEnumMember.
func (e *extractor) extractSwiftEnumEntry(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil || c.Kind() != "simple_identifier" {
			continue
		}
		e.createNode(model.KindEnumMember, c.Text(), node, nodeExtra{})
	}
}

// extractSwiftFunction handles a function_declaration / init_declaration /
// protocol_function_declaration. Inside a type it is a KindMethod; at top level,
// KindFunction.
func (e *extractor) extractSwiftFunction(node *tsparse.Node) {
	name := ""
	if nn := node.ChildByFieldName("name"); nn != nil {
		name = nn.Text()
	} else if nn := swiftChildOfKind(node, "simple_identifier"); nn != nil {
		name = nn.Text()
	}
	if name == "" {
		return
	}
	kind := model.KindFunction
	if e.isInsideClassLike() {
		kind = model.KindMethod
	}
	mn := e.createNode(kind, name, node, nodeExtra{
		visibility: swiftVisibility(node),
		isExported: swiftIsExported(node),
		isStatic:   swiftIsStatic(node),
		isAsync:    swiftIsAsync(node),
		decorators: swiftAttributes(node),
		signature:  swiftFnSignature(node, name),
		returnType: swiftBaseTypeName(node.ChildByFieldName("return_type")),
	})
	if mn == nil {
		return
	}
	// override → overrides ref to the supertype method of the same name.
	if swiftIsOverride(node) {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    mn.ID,
			ReferenceName: name,
			ReferenceKind: model.EdgeOverrides,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
			FilePath:      e.filePath,
			Language:      e.lang,
		})
	}
	if body := swiftFnBody(node); body != nil {
		e.nodeStack = append(e.nodeStack, mn.ID)
		e.visitSwiftBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractSwiftExtMethod extracts a method declared in an extension, attached to
// extType under the qualified name "extType::name".
func (e *extractor) extractSwiftExtMethod(node *tsparse.Node, extType string) {
	name := ""
	if nn := node.ChildByFieldName("name"); nn != nil {
		name = nn.Text()
	} else if nn := swiftChildOfKind(node, "simple_identifier"); nn != nil {
		name = nn.Text()
	}
	if name == "" {
		return
	}
	mn := e.createNode(model.KindMethod, name, node, nodeExtra{
		visibility:    swiftVisibility(node),
		isExported:    swiftIsExported(node),
		isStatic:      swiftIsStatic(node),
		isAsync:       swiftIsAsync(node),
		decorators:    swiftAttributes(node),
		signature:     swiftFnSignature(node, name),
		returnType:    swiftBaseTypeName(node.ChildByFieldName("return_type")),
		qualifiedName: extType + "::" + name,
	})
	if mn == nil {
		return
	}
	e.addReceiverContains(extType, mn.ID)
	if swiftIsOverride(node) {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    mn.ID,
			ReferenceName: name,
			ReferenceKind: model.EdgeOverrides,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
			FilePath:      e.filePath,
			Language:      e.lang,
		})
	}
	if body := swiftFnBody(node); body != nil {
		e.nodeStack = append(e.nodeStack, mn.ID)
		e.visitSwiftBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractSwiftExtInit extracts an init declared in an extension.
func (e *extractor) extractSwiftExtInit(node *tsparse.Node, extType string) {
	mn := e.createNode(model.KindMethod, "init", node, nodeExtra{
		visibility:    swiftVisibility(node),
		isExported:    swiftIsExported(node),
		decorators:    swiftAttributes(node),
		signature:     swiftFnSignature(node, "init"),
		qualifiedName: extType + "::init",
	})
	if mn == nil {
		return
	}
	e.addReceiverContains(extType, mn.ID)
	if body := swiftFnBody(node); body != nil {
		e.nodeStack = append(e.nodeStack, mn.ID)
		e.visitSwiftBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractSwiftExtProperty extracts a property declared in an extension.
func (e *extractor) extractSwiftExtProperty(node *tsparse.Node, extType string) {
	name := swiftPropertyName(node)
	if name == "" {
		return
	}
	kind := model.KindProperty // computed-ish; extension stored props are illegal
	pn := e.createNode(kind, name, node, nodeExtra{
		visibility:    swiftVisibility(node),
		isExported:    swiftIsExported(node),
		isStatic:      swiftIsStatic(node),
		decorators:    swiftAttributes(node),
		signature:     strings.TrimSpace(node.Text()),
		qualifiedName: extType + "::" + name,
	})
	if pn != nil {
		e.addReceiverContains(extType, pn.ID)
	}
}

// extractSwiftProperty handles a property_declaration. Inside a type, a stored
// property (no accessor block) → KindField, a computed property (with a
// getter/setter block) → KindProperty. At top level, an UPPER/const-shaped
// `let` → KindConstant, else KindVariable.
func (e *extractor) extractSwiftProperty(node *tsparse.Node) {
	name := swiftPropertyName(node)
	if name == "" {
		return
	}
	inType := e.isInsideClassLike()
	var kind model.NodeKind
	if inType {
		if swiftHasComputedAccessor(node) {
			kind = model.KindProperty
		} else {
			kind = model.KindField
		}
	} else {
		if swiftIsLet(node) {
			kind = model.KindConstant
		} else {
			kind = model.KindVariable
		}
	}
	e.createNode(kind, name, node, nodeExtra{
		visibility: swiftVisibility(node),
		isExported: swiftIsExported(node),
		isStatic:   swiftIsStatic(node),
		decorators: swiftAttributes(node),
		signature:  strings.TrimSpace(node.Text()),
	})
}

// extractSwiftTypeAlias handles a typealias_declaration → KindTypeAlias.
func (e *extractor) extractSwiftTypeAlias(node *tsparse.Node) {
	name := swiftDeclName(node)
	if name == "" {
		return
	}
	e.createNode(model.KindTypeAlias, name, node, nodeExtra{
		visibility: swiftVisibility(node),
		isExported: swiftIsExported(node),
		signature:  strings.TrimSpace(node.Text()),
	})
}

// visitSwiftBody walks a function/init body for call expressions.
func (e *extractor) visitSwiftBody(body *tsparse.Node) {
	tsparse.Walk(body, func(node *tsparse.Node) {
		if node.Kind() == "call_expression" {
			e.extractSwiftCall(node)
		}
	})
}

// extractSwiftCall handles a call_expression: emits an EdgeCalls reference from
// the top of the node stack. A bare `Foo(...)` keeps its name "Foo" so the
// resolver can promote it to instantiates when Foo names a type. A method call
// `x.method()` strips the receiver to the bare method name (no `self`).
func (e *extractor) extractSwiftCall(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	callerID := e.nodeStack[len(e.nodeStack)-1]
	name := swiftCalleeName(node)
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

// swiftCalleeName resolves a call_expression's callee to a name. The function
// expression is the first named child that is not the call_suffix:
//   - simple_identifier         → bare name (free call or Type(...))
//   - navigation_expression     → method name (target.suffix → suffix, strip self)
func swiftCalleeName(node *tsparse.Node) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil || c.Kind() == "call_suffix" {
			continue
		}
		switch c.Kind() {
		case "simple_identifier", "user_type", "type_identifier":
			return swiftLastIdent(c.Text())
		case "navigation_expression":
			if suf := c.ChildByFieldName("suffix"); suf != nil {
				if id := suf.ChildByFieldName("suffix"); id != nil {
					return id.Text()
				}
				return swiftLastIdent(suf.Text())
			}
			return ""
		default:
			return swiftLastIdent(c.Text())
		}
	}
	return ""
}

// ── helpers ────────────────────────────────────────────────────────────────

// swiftDeclName returns the declared name (type_identifier / simple_identifier)
// of a declaration with a "name" field.
func swiftDeclName(node *tsparse.Node) string {
	if n := node.ChildByFieldName("name"); n != nil {
		return swiftLastIdent(n.Text())
	}
	if n := swiftChildOfKind(node, "type_identifier"); n != nil {
		return n.Text()
	}
	return ""
}

// swiftPropertyName extracts the bound identifier name from a
// property_declaration's "name" pattern.
func swiftPropertyName(node *tsparse.Node) string {
	pat := node.ChildByFieldName("name")
	if pat == nil {
		pat = swiftChildOfKind(node, "pattern")
	}
	if pat == nil {
		return ""
	}
	if bi := pat.ChildByFieldName("bound_identifier"); bi != nil {
		return bi.Text()
	}
	if si := swiftChildOfKind(pat, "simple_identifier"); si != nil {
		return si.Text()
	}
	return strings.TrimSpace(pat.Text())
}

// swiftClassBody returns the class_body / enum_class_body child of a
// class_declaration, or nil.
func swiftClassBody(node *tsparse.Node) *tsparse.Node {
	if b := node.ChildByFieldName("body"); b != nil {
		return b
	}
	if b := swiftChildOfKind(node, "class_body"); b != nil {
		return b
	}
	return swiftChildOfKind(node, "enum_class_body")
}

// swiftFnBody returns the function_body child of a function/init declaration.
func swiftFnBody(node *tsparse.Node) *tsparse.Node {
	if b := node.ChildByFieldName("body"); b != nil {
		return b
	}
	return swiftChildOfKind(node, "function_body")
}

// swiftChildOfKind returns the first named child of node with the given kind.
func swiftChildOfKind(node *tsparse.Node, kind string) *tsparse.Node {
	if node == nil {
		return nil
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// swiftModifiers returns the `modifiers` child node, or nil.
func swiftModifiers(node *tsparse.Node) *tsparse.Node {
	return swiftChildOfKind(node, "modifiers")
}

// swiftVisibility returns a pointer to the visibility keyword
// (public/private/fileprivate/internal/open) or "internal" (Swift's default).
func swiftVisibility(node *tsparse.Node) *string {
	v := "internal"
	if mods := swiftModifiers(node); mods != nil {
		if vm := swiftChildOfKind(mods, "visibility_modifier"); vm != nil {
			v = strings.TrimSpace(vm.Text())
		}
	}
	return &v
}

// swiftIsExported reports whether the declaration is public or open.
func swiftIsExported(node *tsparse.Node) bool {
	if mods := swiftModifiers(node); mods != nil {
		if vm := swiftChildOfKind(mods, "visibility_modifier"); vm != nil {
			t := strings.TrimSpace(vm.Text())
			return t == "public" || t == "open"
		}
	}
	return false
}

// swiftIsStatic reports whether a member carries a static/class property modifier.
func swiftIsStatic(node *tsparse.Node) bool {
	if mods := swiftModifiers(node); mods != nil {
		return strings.Contains(mods.Text(), "static") || strings.Contains(mods.Text(), "class")
	}
	return false
}

// swiftIsOverride reports whether a member carries the override member modifier.
func swiftIsOverride(node *tsparse.Node) bool {
	if mods := swiftModifiers(node); mods != nil {
		return strings.Contains(mods.Text(), "override")
	}
	return false
}

// swiftIsAsync reports whether a function is declared async.
func swiftIsAsync(node *tsparse.Node) bool {
	for i := 0; i < node.ChildCount(); i++ {
		if c := node.Child(i); c != nil && c.Text() == "async" {
			return true
		}
	}
	return false
}

// swiftIsLet reports whether a property binds with `let` (immutable).
func swiftIsLet(node *tsparse.Node) bool {
	if vbp := swiftChildOfKind(node, "value_binding_pattern"); vbp != nil {
		if m := vbp.ChildByFieldName("mutability"); m != nil {
			return strings.TrimSpace(m.Text()) == "let"
		}
		return strings.HasPrefix(strings.TrimSpace(vbp.Text()), "let")
	}
	return false
}

// swiftHasComputedAccessor reports whether a property has a computed-property
// accessor block ({ get … } / { … }), making it a KindProperty rather than a
// stored KindField.
func swiftHasComputedAccessor(node *tsparse.Node) bool {
	return swiftChildOfKind(node, "computed_property") != nil ||
		swiftChildOfKind(node, "computed_getter") != nil ||
		swiftChildOfKind(node, "protocol_property_requirements") != nil
}

// swiftAttributes collects attribute head names (@objc, @MainActor → "objc",
// "MainActor") from the modifiers block.
func swiftAttributes(node *tsparse.Node) []string {
	mods := swiftModifiers(node)
	if mods == nil {
		return nil
	}
	var out []string
	for i := 0; i < mods.NamedChildCount(); i++ {
		c := mods.NamedChild(i)
		if c == nil || c.Kind() != "attribute" {
			continue
		}
		name := strings.TrimPrefix(strings.TrimSpace(c.Text()), "@")
		if idx := strings.IndexAny(name, "( \t"); idx > 0 {
			name = name[:idx]
		}
		name = swiftLastIdent(name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// swiftTypeParams extracts generic parameter names from a type_parameters child.
func swiftTypeParams(node *tsparse.Node) []string {
	tp := swiftChildOfKind(node, "type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < tp.NamedChildCount(); i++ {
		c := tp.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == "type_parameter" {
			t := strings.TrimSpace(c.Text())
			if idx := strings.IndexByte(t, ':'); idx > 0 {
				t = strings.TrimSpace(t[:idx])
			}
			if t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// swiftFnSignature renders a "func name(...) -> Ret" line (no body).
func swiftFnSignature(node *tsparse.Node, name string) string {
	var b strings.Builder
	if name == "init" {
		b.WriteString("init")
	} else {
		b.WriteString("func ")
		b.WriteString(name)
	}
	// Collect parameter list text from the declaration's parameter children.
	var params []string
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil || c.Kind() != "parameter" {
			continue
		}
		params = append(params, strings.TrimSpace(c.Text()))
	}
	b.WriteString("(")
	b.WriteString(strings.Join(params, ", "))
	b.WriteString(")")
	if rt := node.ChildByFieldName("return_type"); rt != nil {
		b.WriteString(" -> ")
		b.WriteString(strings.TrimSpace(rt.Text()))
	}
	return b.String()
}

// swiftBaseTypeName extracts the bare type name from a type node, stripping
// generic args and optional markers (a.b.C → C, Foo<T> → Foo, Bar? → Bar).
func swiftBaseTypeName(node *tsparse.Node) string {
	if node == nil {
		return ""
	}
	text := strings.TrimSpace(node.Text())
	text = strings.TrimRight(text, "?!")
	if idx := strings.IndexByte(text, '<'); idx > 0 {
		text = strings.TrimSpace(text[:idx])
	}
	text = swiftLastIdent(text)
	if !reValidIdent.MatchString(text) {
		return ""
	}
	return text
}

// swiftLastIdent returns the last dotted segment of a (possibly qualified) name.
func swiftLastIdent(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.LastIndexByte(s, '.'); idx >= 0 {
		return strings.TrimSpace(s[idx+1:])
	}
	return s
}

// swiftFirstPathSegment returns the first dotted segment of a path
// (Foo.Bar → Foo).
func swiftFirstPathSegment(s string) string {
	s = strings.TrimSpace(s)
	if before, _, ok := strings.Cut(s, "."); ok {
		return strings.TrimSpace(before)
	}
	return s
}

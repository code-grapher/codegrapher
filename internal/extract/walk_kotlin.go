package extract

import (
	"slices"
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkKotlin walks a parsed Kotlin (tree-sitter `kotlin`) file root and extracts
// symbols. Kotlin is a JVM language; its resolver reuses Java's JVM helpers.
//
// Node type reference (tree-sitter-kotlin) — this grammar uses NO field names,
// so children are found by iterating named children and matching node kinds:
//
//	source_file → package_header, import_list(import_header*), declarations
//	package_header → identifier (dotted)
//	import_header → identifier (+ optional wildcard_import "*")
//	class_declaration → modifiers?, type_identifier(name), primary_constructor?,
//	    delegation_specifier*, class_body | enum_class_body
//	    (anonymous "interface"/"enum"/"class" tokens distinguish the kind;
//	     `annotation` class_modifier marks an annotation class)
//	object_declaration / companion_object → type_identifier?, class_body
//	function_declaration → modifiers?, receiver_type?(extension), simple_identifier(name),
//	    function_value_parameters, user_type?(return), function_body?
//	property_declaration → modifiers?, binding_pattern_kind(val/var),
//	    variable_declaration(simple_identifier + user_type?), initializer-expr?
//	primary_constructor → class_parameter*(binding_pattern_kind? simple_identifier user_type)
//	enum_class_body → enum_entry*(simple_identifier)
//	type_alias → type_identifier(name), user_type
//	call_expression → (simple_identifier | navigation_expression), call_suffix
//	navigation_expression → receiver, navigation_suffix(.simple_identifier)
//	delegation_specifier → user_type (interface→implements) |
//	    constructor_invocation(user_type + value_arguments) (superclass→extends)
func (e *extractor) walkKotlin(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeKotlin(child)
		}
	}
}

// visitNodeKotlin dispatches a single node. Unknown kinds descend into their
// named children so calls/declarations nested inside statements are still seen.
func (e *extractor) visitNodeKotlin(node *tsparse.Node) {
	switch node.Kind() {
	case "package_header":
		e.extractKotlinPackage(node)
	case "import_list":
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); c != nil && c.Kind() == "import_header" {
				e.extractKotlinImport(c)
			}
		}
	case "import_header":
		e.extractKotlinImport(node)
	case "class_declaration":
		e.extractKotlinClass(node)
	case "object_declaration":
		e.extractKotlinObject(node)
	case "function_declaration":
		e.extractKotlinFunction(node)
	case "property_declaration":
		e.extractKotlinProperty(node)
	case "type_alias":
		e.extractKotlinTypeAlias(node)
	case "call_expression":
		e.extractKotlinCall(node)
	default:
		e.visitKotlinBody(node)
	}
}

// visitKotlinBody descends into a node's named children looking for calls and
// nested declarations without emitting a node for the container itself.
func (e *extractor) visitKotlinBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodeKotlin(child)
		}
	}
}

// extractKotlinPackage emits a KindNamespace node for the package header.
func (e *extractor) extractKotlinPackage(node *tsparse.Node) {
	name := ktDottedIdentifier(node)
	if name == "" {
		return
	}
	e.createNode(model.KindNamespace, name, node, nodeExtra{
		signature: strings.TrimSpace(node.Text()),
	})
}

// extractKotlinImport emits a KindImport node and an EdgeImports reference.
// `import a.b.C` binds the simple name "C"; `import a.b.*` binds the package path.
func (e *extractor) extractKotlinImport(node *tsparse.Node) {
	fq := ktDottedIdentifier(node)
	if fq == "" {
		return
	}
	wildcard := false
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "wildcard_import" {
			wildcard = true
			break
		}
	}

	name := fq
	if !wildcard {
		if idx := strings.LastIndex(fq, "."); idx >= 0 {
			name = fq[idx+1:]
		}
	}
	if name == "" {
		return
	}

	e.createNode(model.KindImport, name, node, nodeExtra{signature: strings.TrimSpace(node.Text())})

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

// extractKotlinClass handles class_declaration: a class, interface, enum, or
// annotation class depending on the leading keyword/modifier. Emits the symbol
// node, extends/implements + decorates references, primary-constructor val/var
// fields, then walks the body.
func (e *extractor) extractKotlinClass(node *tsparse.Node) {
	nameNode := ktNamedChildOfKind(node, "type_identifier")
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	name := nameNode.Text()

	mods := ktModifiers(node)
	isInterface := ktHasToken(node, "interface")
	isEnum := ktHasToken(node, "enum")

	kind := model.KindClass
	switch {
	case isEnum:
		kind = model.KindEnum
	case isInterface:
		kind = model.KindInterface
	}

	cn := e.createNode(kind, name, node, nodeExtra{
		visibility:     ktVisibility(mods),
		isExported:     mods.isPublic(),
		isAbstract:     mods.isAbstract,
		decorators:     mods.annotations,
		typeParameters: ktTypeParameters(node),
	})
	if cn == nil {
		return
	}

	e.emitKotlinDecorates(cn.ID, mods.annotations, node)
	e.emitKotlinDelegations(cn.ID, node)

	e.nodeStack = append(e.nodeStack, cn.ID)
	// Primary-constructor val/var parameters become fields.
	if pc := ktNamedChildOfKind(node, "primary_constructor"); pc != nil {
		e.extractKotlinPrimaryCtor(pc)
	}
	if isEnum {
		e.walkKotlinEnumBody(node)
	} else {
		if body := ktNamedChildOfKind(node, "class_body"); body != nil {
			e.walkKotlinClassBody(body)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractKotlinObject handles object_declaration (`object X { ... }`), a Kotlin
// singleton, modeled as a KindClass whose members are static.
func (e *extractor) extractKotlinObject(node *tsparse.Node) {
	nameNode := ktNamedChildOfKind(node, "type_identifier")
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	mods := ktModifiers(node)
	cn := e.createNode(model.KindClass, nameNode.Text(), node, nodeExtra{
		visibility: ktVisibility(mods),
		isExported: mods.isPublic(),
		isStatic:   true, // Kotlin object: a singleton; members resolve statically.
		decorators: mods.annotations,
	})
	if cn == nil {
		return
	}
	e.emitKotlinDecorates(cn.ID, mods.annotations, node)
	e.emitKotlinDelegations(cn.ID, node)

	e.nodeStack = append(e.nodeStack, cn.ID)
	if body := ktNamedChildOfKind(node, "class_body"); body != nil {
		e.walkKotlinClassBody(body)
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractKotlinCompanion handles a companion_object inside a class body. The
// companion's members are emitted directly under the enclosing class as static
// members (Type.member() resolution lands on them).
func (e *extractor) extractKotlinCompanion(node *tsparse.Node) {
	if body := ktNamedChildOfKind(node, "class_body"); body != nil {
		e.walkKotlinClassBody(body)
	}
}

// walkKotlinClassBody visits the members of a class_body. The enclosing node ID
// must already be on the stack.
func (e *extractor) walkKotlinClassBody(body *tsparse.Node) {
	for i := 0; i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "companion_object" {
			e.extractKotlinCompanion(child)
			continue
		}
		e.visitNodeKotlin(child)
	}
}

// walkKotlinEnumBody emits enum entries (KindEnumMember) plus any methods/props
// declared in the enum body. The enum node ID must already be on the stack.
func (e *extractor) walkKotlinEnumBody(classNode *tsparse.Node) {
	body := ktNamedChildOfKind(classNode, "enum_class_body")
	if body == nil {
		return
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "enum_entry" {
			if id := ktNamedChildOfKind(child, "simple_identifier"); id != nil && id.Text() != "" {
				e.createNode(model.KindEnumMember, id.Text(), child, nodeExtra{})
			}
			continue
		}
		e.visitNodeKotlin(child)
	}
}

// extractKotlinPrimaryCtor emits a KindField per `val`/`var` class_parameter of a
// primary constructor (plain parameters without val/var are not properties).
func (e *extractor) extractKotlinPrimaryCtor(pc *tsparse.Node) {
	for i := 0; i < pc.NamedChildCount(); i++ {
		cp := pc.NamedChild(i)
		if cp == nil || cp.Kind() != "class_parameter" {
			continue
		}
		// Only val/var parameters become properties/fields.
		if ktNamedChildOfKind(cp, "binding_pattern_kind") == nil {
			continue
		}
		nameNode := ktNamedChildOfKind(cp, "simple_identifier")
		if nameNode == nil || nameNode.Text() == "" {
			continue
		}
		var typeName string
		if t := ktNamedChildOfKind(cp, "user_type"); t != nil {
			typeName = ktSimpleType(t)
		}
		e.createNode(model.KindField, nameNode.Text(), cp, nodeExtra{
			signature:  strings.TrimSpace(cp.Text()),
			returnType: typeName,
		})
	}
}

// extractKotlinFunction handles function_declaration. A function inside a class
// or object is a KindMethod; a top-level function is a KindFunction. The
// `suspend` modifier sets isAsync. Extension functions (with a receiver_type)
// are emitted like any function.
func (e *extractor) extractKotlinFunction(node *tsparse.Node) {
	nameNode := ktFunctionName(node)
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	mods := ktModifiers(node)

	kind := model.KindFunction
	if e.isInsideClassLike() {
		kind = model.KindMethod
	}

	var returnType string
	if t := ktFunctionReturnType(node); t != nil {
		returnType = ktSimpleType(t)
	}

	fn := e.createNode(kind, nameNode.Text(), node, nodeExtra{
		signature:      ktFunctionSignature(node),
		visibility:     ktVisibility(mods),
		isExported:     mods.isPublic(),
		isAsync:        mods.isSuspend,
		isStatic:       mods.isStatic(),
		isAbstract:     mods.isAbstract,
		returnType:     returnType,
		decorators:     mods.annotations,
		typeParameters: ktTypeParameters(node),
	})
	if fn == nil {
		return
	}
	e.emitKotlinDecorates(fn.ID, mods.annotations, node)

	if body := ktNamedChildOfKind(node, "function_body"); body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		e.visitKotlinBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractKotlinProperty handles property_declaration. A `const val` or a
// top-level UPPER-case `val` is a KindConstant; a property inside a class is a
// KindProperty; otherwise KindField. Walks the initializer for calls.
func (e *extractor) extractKotlinProperty(node *tsparse.Node) {
	vd := ktNamedChildOfKind(node, "variable_declaration")
	if vd == nil {
		return
	}
	nameNode := ktNamedChildOfKind(vd, "simple_identifier")
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	name := nameNode.Text()
	mods := ktModifiers(node)

	var typeName string
	if t := ktNamedChildOfKind(vd, "user_type"); t != nil {
		typeName = ktSimpleType(t)
	}

	kind := model.KindField
	switch {
	case mods.isConst:
		kind = model.KindConstant
	case e.isInsideClassLike():
		kind = model.KindProperty
	case e.isInsideFunctionLike():
		// A `val`/`var` inside a function/method body is a local variable.
		kind = model.KindVariable
	}

	pn := e.createNode(kind, name, node, nodeExtra{
		signature:  strings.TrimSpace(node.Text()),
		visibility: ktVisibility(mods),
		isExported: mods.isPublic(),
		isStatic:   mods.isStatic(),
		isAbstract: mods.isAbstract,
		returnType: typeName,
		decorators: mods.annotations,
	})
	if pn == nil {
		return
	}
	e.emitKotlinDecorates(pn.ID, mods.annotations, node)

	// Walk the initializer expression (the named child after the var decl) for
	// calls / constructor invocations.
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "modifiers", "binding_pattern_kind", "variable_declaration":
			continue
		}
		e.visitNodeKotlin(c)
	}
}

// extractKotlinTypeAlias handles type_alias (`typealias X = ...`).
func (e *extractor) extractKotlinTypeAlias(node *tsparse.Node) {
	nameNode := ktNamedChildOfKind(node, "type_identifier")
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	mods := ktModifiers(node)
	e.createNode(model.KindTypeAlias, nameNode.Text(), node, nodeExtra{
		signature:  strings.TrimSpace(node.Text()),
		visibility: ktVisibility(mods),
		isExported: mods.isPublic(),
	})
}

// extractKotlinCall handles a call_expression: emits an EdgeCalls reference from
// the top of the node stack. Callee may be a bare `simple_identifier` ("m") or a
// `navigation_expression` ("recv.m" → "recv.m" / chained → "m").
func (e *extractor) extractKotlinCall(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		if name := ktCalleeName(node); name != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: name,
				ReferenceKind: model.EdgeCalls,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
		}
	}
	// Descend into the callee receiver and the argument list for nested calls.
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "simple_identifier":
			continue // the callee name itself
		case "navigation_expression":
			// Walk the receiver (skip the trailing navigation_suffix name).
			if recv := c.NamedChild(0); recv != nil {
				e.visitNodeKotlin(recv)
			}
		default:
			e.visitNodeKotlin(c)
		}
	}
}

// emitKotlinDelegations classifies a class's delegation_specifier list:
// a constructor_invocation (`Base(1)`) → extends; a plain user_type → implements.
func (e *extractor) emitKotlinDelegations(fromID string, node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		ds := node.NamedChild(i)
		if ds == nil || ds.Kind() != "delegation_specifier" {
			continue
		}
		if ci := ktNamedChildOfKind(ds, "constructor_invocation"); ci != nil {
			if ut := ktNamedChildOfKind(ci, "user_type"); ut != nil {
				if name := ktSimpleType(ut); name != "" {
					e.emitKotlinTypeRef(fromID, name, model.EdgeExtends, ds)
				}
			}
			continue
		}
		if ut := ktNamedChildOfKind(ds, "user_type"); ut != nil {
			if name := ktSimpleType(ut); name != "" {
				e.emitKotlinTypeRef(fromID, name, model.EdgeImplements, ds)
			}
		}
	}
}

// emitKotlinTypeRef appends an unresolved reference of kind for a base type name.
func (e *extractor) emitKotlinTypeRef(fromID, name string, kind model.EdgeKind, at *tsparse.Node) {
	if name == "" {
		return
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    fromID,
		ReferenceName: name,
		ReferenceKind: kind,
		Line:          int(at.StartPoint().Row) + 1,
		Column:        int(at.StartPoint().Column),
	})
}

// emitKotlinDecorates emits an EdgeDecorates reference per annotation name.
func (e *extractor) emitKotlinDecorates(fromID string, annotations []string, node *tsparse.Node) {
	for _, a := range annotations {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: a,
			ReferenceKind: model.EdgeDecorates,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Kotlin helpers
// ──────────────────────────────────────────────────────────────────────────────

// ktMods holds parsed modifiers/annotations of a Kotlin declaration.
type ktMods struct {
	visibility  string // "public"/"private"/"protected"/"internal"/""
	isAbstract  bool
	isOpen      bool
	isOverride  bool
	isConst     bool
	isSuspend   bool
	annotations []string
}

// isPublic reports whether the declaration is public (Kotlin's default).
func (m ktMods) isPublic() bool {
	return m.visibility == "" || m.visibility == "public"
}

// isStatic is false for ordinary Kotlin declarations; object/companion members
// are marked static by their container, not by a modifier.
func (m ktMods) isStatic() bool { return false }

// ktModifiers parses the `modifiers` child of a declaration node.
func ktModifiers(node *tsparse.Node) ktMods {
	var m ktMods
	modsNode := ktNamedChildOfKind(node, "modifiers")
	if modsNode == nil {
		return m
	}
	for i := 0; i < modsNode.NamedChildCount(); i++ {
		c := modsNode.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "annotation":
			if head := ktAnnotationHead(c); head != "" {
				m.annotations = append(m.annotations, head)
			}
		case "visibility_modifier":
			m.visibility = strings.TrimSpace(c.Text())
		case "inheritance_modifier":
			switch strings.TrimSpace(c.Text()) {
			case "abstract":
				m.isAbstract = true
			case "open":
				m.isOpen = true
			}
		case "member_modifier":
			if strings.TrimSpace(c.Text()) == "override" {
				m.isOverride = true
			}
		case "property_modifier":
			if strings.TrimSpace(c.Text()) == "const" {
				m.isConst = true
			}
		case "function_modifier":
			if strings.TrimSpace(c.Text()) == "suspend" {
				m.isSuspend = true
			}
		case "platform_modifier", "class_modifier":
			// annotation/enum/data/etc. handled via ktHasToken on the parent.
		}
	}
	return m
}

// ktVisibility maps modifiers to a visibility pointer (always emitted; Kotlin's
// default is public).
func ktVisibility(m ktMods) *string {
	v := m.visibility
	if v == "" {
		v = "public"
	}
	return &v
}

// ktAnnotationHead returns the annotation's name. An `annotation` node wraps a
// `user_type` (possibly via constructor_invocation) — return its simple name.
func ktAnnotationHead(node *tsparse.Node) string {
	var walk func(n *tsparse.Node) string
	walk = func(n *tsparse.Node) string {
		if n == nil {
			return ""
		}
		if n.Kind() == "user_type" {
			return ktSimpleType(n)
		}
		for i := 0; i < n.NamedChildCount(); i++ {
			if r := walk(n.NamedChild(i)); r != "" {
				return r
			}
		}
		return ""
	}
	return walk(node)
}

// ktTypeParameters returns generic type-parameter names (the `type_parameters`
// child), e.g. ["T", "K"].
func ktTypeParameters(node *tsparse.Node) []string {
	tp := ktNamedChildOfKind(node, "type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < tp.NamedChildCount(); i++ {
		c := tp.NamedChild(i)
		if c == nil || c.Kind() != "type_parameter" {
			continue
		}
		if id := ktNamedChildOfKind(c, "type_identifier"); id != nil {
			out = append(out, id.Text())
		}
	}
	return out
}

// ktFunctionName returns a function_declaration's name (simple_identifier),
// skipping a leading receiver_type's own identifiers (extension functions).
func ktFunctionName(node *tsparse.Node) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "modifiers", "type_parameters", "receiver_type":
			continue
		case "simple_identifier":
			return c
		}
	}
	return nil
}

// ktFunctionReturnType returns the user_type that follows the parameter list
// (the declared return type), or nil for Unit-returning functions.
func ktFunctionReturnType(node *tsparse.Node) *tsparse.Node {
	seenParams := false
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == "function_value_parameters" {
			seenParams = true
			continue
		}
		if seenParams && (c.Kind() == "user_type" || c.Kind() == "nullable_type") {
			return c
		}
	}
	return nil
}

// ktFunctionSignature renders a function header up to (and including) the
// parameter list and return type.
func ktFunctionSignature(node *tsparse.Node) string {
	nameNode := ktFunctionName(node)
	if nameNode == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("fun ")
	if recv := ktNamedChildOfKind(node, "receiver_type"); recv != nil {
		sb.WriteString(ktSimpleType(recv))
		sb.WriteString(".")
	}
	sb.WriteString(nameNode.Text())
	if params := ktNamedChildOfKind(node, "function_value_parameters"); params != nil {
		sb.WriteString(params.Text())
	}
	if rt := ktFunctionReturnType(node); rt != nil {
		sb.WriteString(": ")
		sb.WriteString(ktSimpleType(rt))
	}
	return strings.TrimSpace(sb.String())
}

// ktCalleeName resolves a call_expression to its callee reference name.
// Bare `m()` → "m"; `recv.m()` → "recv.m"; chained `a.b.m()` → "m".
func ktCalleeName(node *tsparse.Node) string {
	callee := node.NamedChild(0)
	if callee == nil {
		return ""
	}
	switch callee.Kind() {
	case "simple_identifier":
		return callee.Text()
	case "navigation_expression":
		recv := callee.NamedChild(0)
		suffix := ktNamedChildOfKind(callee, "navigation_suffix")
		if suffix == nil {
			return ""
		}
		method := ""
		if id := ktNamedChildOfKind(suffix, "simple_identifier"); id != nil {
			method = id.Text()
		}
		if method == "" {
			return ""
		}
		if recv != nil && recv.Kind() == "simple_identifier" {
			return recv.Text() + "." + method
		}
		// Chained / complex receiver: resolve by bare method name.
		return method
	}
	return ""
}

// ktDottedIdentifier returns the dotted name from a package_header /
// import_header's identifier child.
func ktDottedIdentifier(node *tsparse.Node) string {
	id := ktNamedChildOfKind(node, "identifier")
	if id == nil {
		return ""
	}
	// The identifier's text is the dotted path (e.g. "com.example.Foo").
	return strings.TrimSpace(id.Text())
}

// ktSimpleType reduces a type node (user_type, nullable_type, receiver_type,
// type_identifier) to its simple last-segment name, stripping generics and a
// trailing nullable `?`.
func ktSimpleType(node *tsparse.Node) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "type_identifier", "simple_identifier":
		return node.Text()
	}
	// Find the first type_identifier descendant (the base type).
	if id := ktFirstTypeIdentifier(node); id != "" {
		return id
	}
	t := strings.TrimSpace(node.Text())
	t = strings.TrimSuffix(t, "?")
	if idx := strings.IndexByte(t, '<'); idx >= 0 {
		t = t[:idx]
	}
	if idx := strings.LastIndex(t, "."); idx >= 0 {
		t = t[idx+1:]
	}
	return strings.TrimSpace(t)
}

// ktFirstTypeIdentifier returns the first type_identifier in a type subtree.
func ktFirstTypeIdentifier(node *tsparse.Node) string {
	if node == nil {
		return ""
	}
	if node.Kind() == "type_identifier" {
		return node.Text()
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if r := ktFirstTypeIdentifier(node.NamedChild(i)); r != "" {
			return r
		}
	}
	return ""
}

// isInsideFunctionLike reports whether the top of the node stack is a function
// or method (so a property_declaration there is a local variable).
func (e *extractor) isInsideFunctionLike() bool {
	if len(e.nodeStack) == 0 {
		return false
	}
	topID := e.nodeStack[len(e.nodeStack)-1]
	for _, v := range slices.Backward(e.nodes) {
		if v.ID == topID {
			return v.Kind == model.KindFunction || v.Kind == model.KindMethod
		}
	}
	return false
}

// ktNamedChildOfKind returns the first named child of node whose kind matches.
func ktNamedChildOfKind(node *tsparse.Node, kind string) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// ktHasToken reports whether node has an anonymous child token with the given
// text (e.g. "interface", "enum") — used to distinguish class_declaration kinds.
func ktHasToken(node *tsparse.Node, token string) bool {
	for i := 0; i < node.ChildCount(); i++ {
		c := node.Child(i)
		if c == nil || c.IsNamed() {
			continue
		}
		if c.Text() == token {
			return true
		}
	}
	// `enum` appears as a class_modifier in some grammar variants.
	if token == "enum" {
		if mods := ktNamedChildOfKind(node, "modifiers"); mods != nil {
			for i := 0; i < mods.NamedChildCount(); i++ {
				c := mods.NamedChild(i)
				if c != nil && c.Kind() == "class_modifier" && strings.TrimSpace(c.Text()) == "enum" {
					return true
				}
			}
		}
	}
	return false
}

package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkJava walks a parsed Java (tree-sitter `java`) file root and extracts
// symbols. Called by ExtractFile after the file node is emitted.
//
// Node type reference (tree-sitter-java):
//
//	package_declaration (→ KindNamespace)
//	import_declaration   (→ KindImport)
//	class_declaration  (fields: name, superclass, interfaces, body)
//	interface_declaration (fields: name, body; extends_interfaces)
//	enum_declaration (fields: name, body) / enum_constant (field: name)
//	record_declaration / annotation_type_declaration
//	method_declaration / constructor_declaration (fields: name, parameters, body)
//	field_declaration (fields: modifiers, type, declarator=variable_declarator)
//	local_variable_declaration / variable_declarator
//	method_invocation (fields: object, name, arguments)
//	object_creation_expression (fields: type, arguments) — new T()
//	marker_annotation / annotation (field: name) — @Override etc.
func (e *extractor) walkJava(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeJava(child)
		}
	}
}

// visitNodeJava dispatches a single node. Unknown kinds descend into their
// children so calls/declarations nested inside statements are still seen.
func (e *extractor) visitNodeJava(node *tsparse.Node) {
	switch node.Kind() {
	case "package_declaration":
		e.extractJavaPackage(node)
	case "import_declaration":
		e.extractJavaImport(node)
	case "class_declaration", "record_declaration":
		e.extractJavaClass(node)
	case "interface_declaration", "annotation_type_declaration":
		e.extractJavaInterface(node)
	case "enum_declaration":
		e.extractJavaEnum(node)
	case "method_declaration", "constructor_declaration":
		e.extractJavaMethod(node)
	case "field_declaration":
		e.extractJavaField(node)
	case "local_variable_declaration":
		e.extractJavaLocalVar(node)
	case "method_invocation":
		e.extractJavaCall(node)
	case "object_creation_expression":
		e.extractJavaNew(node)
	default:
		e.visitJavaBody(node)
	}
}

// visitJavaBody descends into a node's named children looking for calls and
// nested declarations without emitting a node for the container itself.
func (e *extractor) visitJavaBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodeJava(child)
		}
	}
}

// extractJavaPackage emits a KindNamespace node for the package declaration.
func (e *extractor) extractJavaPackage(node *tsparse.Node) {
	name := javaScopedName(node)
	if name == "" {
		return
	}
	e.createNode(model.KindNamespace, name, node, nodeExtra{
		signature: strings.TrimSpace(node.Text()),
	})
}

// extractJavaImport emits a KindImport node per import declaration plus an
// EdgeImports reference from the current parent scope. Wildcard imports
// (`import a.b.*;`) use the package path as the name; type imports use the
// fully-qualified type name (the simple last segment is the binding).
func (e *extractor) extractJavaImport(node *tsparse.Node) {
	sig := strings.TrimSpace(node.Text())
	fq := javaScopedName(node)
	if fq == "" {
		return
	}
	wildcard := false
	for i := 0; i < node.ChildCount(); i++ {
		if c := node.Child(i); c != nil && c.Kind() == "asterisk" {
			wildcard = true
			break
		}
	}

	// Binding name: simple type name for type imports, full package for wildcards.
	name := fq
	if !wildcard {
		if idx := strings.LastIndex(fq, "."); idx >= 0 {
			name = fq[idx+1:]
		}
	}
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

// extractJavaClass handles class_declaration / record_declaration: emits a
// KindClass node, extends/implements references, decorates references for
// annotations, then walks the body.
func (e *extractor) extractJavaClass(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	mods := javaModifiers(node)
	cn := e.createNode(model.KindClass, name, node, nodeExtra{
		visibility:     javaVisibility(mods),
		isExported:     mods.isPublic,
		isStatic:       mods.isStatic,
		isAbstract:     mods.isAbstract,
		decorators:     mods.annotations,
		typeParameters: javaTypeParameters(node),
	})
	if cn == nil {
		return
	}

	e.emitJavaDecorates(cn.ID, mods.annotations, node)

	// extends (single superclass).
	if sc := node.ChildByFieldName("superclass"); sc != nil {
		for _, base := range javaTypeNames(sc) {
			e.emitJavaTypeRef(cn.ID, base, model.EdgeExtends, sc)
		}
	}
	// implements (super_interfaces → type_list).
	if ifaces := node.ChildByFieldName("interfaces"); ifaces != nil {
		for _, base := range javaTypeNames(ifaces) {
			e.emitJavaTypeRef(cn.ID, base, model.EdgeImplements, ifaces)
		}
	}

	e.walkJavaBody(cn.ID, node.ChildByFieldName("body"))
}

// extractJavaInterface handles interface_declaration / annotation_type_declaration.
func (e *extractor) extractJavaInterface(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	mods := javaModifiers(node)
	in := e.createNode(model.KindInterface, name, node, nodeExtra{
		visibility:     javaVisibility(mods),
		isExported:     mods.isPublic,
		isStatic:       mods.isStatic,
		isAbstract:     mods.isAbstract,
		decorators:     mods.annotations,
		typeParameters: javaTypeParameters(node),
	})
	if in == nil {
		return
	}

	e.emitJavaDecorates(in.ID, mods.annotations, node)

	// Interfaces use `extends` for their parent interface list (extends_interfaces).
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "extends_interfaces" {
			for _, base := range javaTypeNames(c) {
				e.emitJavaTypeRef(in.ID, base, model.EdgeExtends, c)
			}
		}
	}

	e.walkJavaBody(in.ID, node.ChildByFieldName("body"))
}

// extractJavaEnum handles enum_declaration: KindEnum + enum_constant members.
func (e *extractor) extractJavaEnum(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	mods := javaModifiers(node)
	en := e.createNode(model.KindEnum, name, node, nodeExtra{
		visibility: javaVisibility(mods),
		isExported: mods.isPublic,
		isStatic:   mods.isStatic,
		decorators: mods.annotations,
	})
	if en == nil {
		return
	}

	e.emitJavaDecorates(en.ID, mods.annotations, node)

	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, en.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "enum_constant" {
			if cn := child.ChildByFieldName("name"); cn != nil && cn.Text() != "" {
				e.createNode(model.KindEnumMember, cn.Text(), child, nodeExtra{})
			}
			continue
		}
		// enum_body_declarations: methods/fields inside the enum.
		e.visitNodeJava(child)
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractJavaMethod handles method_declaration and constructor_declaration.
func (e *extractor) extractJavaMethod(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	mods := javaModifiers(node)
	var returnType string
	if t := node.ChildByFieldName("type"); t != nil {
		returnType = t.Text()
	}

	fn := e.createNode(model.KindMethod, name, node, nodeExtra{
		signature:      javaMethodSignature(node),
		visibility:     javaVisibility(mods),
		isExported:     mods.isPublic,
		isStatic:       mods.isStatic,
		isAbstract:     mods.isAbstract,
		returnType:     returnType,
		decorators:     mods.annotations,
		typeParameters: javaTypeParameters(node),
	})
	if fn == nil {
		return
	}

	e.emitJavaDecorates(fn.ID, mods.annotations, node)

	body := node.ChildByFieldName("body")
	if body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		for i := 0; i < body.NamedChildCount(); i++ {
			if child := body.NamedChild(i); child != nil {
				e.visitNodeJava(child)
			}
		}
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractJavaField handles field_declaration. A `static final` field becomes a
// KindConstant; other fields are KindField. A declaration may declare several
// names (`int a, b;`) — one node per variable_declarator.
func (e *extractor) extractJavaField(node *tsparse.Node) {
	mods := javaModifiers(node)
	kind := model.KindField
	if mods.isStatic && mods.isFinal {
		kind = model.KindConstant
	}
	var typeName string
	if t := node.ChildByFieldName("type"); t != nil {
		typeName = t.Text()
	}

	for i := 0; i < node.NamedChildCount(); i++ {
		d := node.NamedChild(i)
		if d == nil || d.Kind() != "variable_declarator" {
			continue
		}
		nameNode := d.ChildByFieldName("name")
		if nameNode == nil || nameNode.Text() == "" {
			continue
		}
		e.createNode(kind, nameNode.Text(), d, nodeExtra{
			signature:  javaFieldSignature(typeName, d),
			visibility: javaVisibility(mods),
			isExported: mods.isPublic,
			isStatic:   mods.isStatic,
			returnType: typeName,
			decorators: mods.annotations,
		})
		// A field initialized with `new T()` instantiates T.
		e.emitJavaNewFromDeclarator(d)
	}
}

// extractJavaLocalVar handles a local_variable_declaration inside a method
// body: emits a KindVariable per declarator (call-scoping only) and records
// any `new T()` instantiation in the initializer.
func (e *extractor) extractJavaLocalVar(node *tsparse.Node) {
	var typeName string
	if t := node.ChildByFieldName("type"); t != nil {
		typeName = t.Text()
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		d := node.NamedChild(i)
		if d == nil || d.Kind() != "variable_declarator" {
			continue
		}
		nameNode := d.ChildByFieldName("name")
		if nameNode == nil || nameNode.Text() == "" {
			continue
		}
		e.createNode(model.KindVariable, nameNode.Text(), d, nodeExtra{
			signature:  javaFieldSignature(typeName, d),
			returnType: typeName,
		})
		// Walk the initializer for calls / new expressions.
		if v := d.ChildByFieldName("value"); v != nil {
			e.visitNodeJava(v)
		}
	}
}

// extractJavaCall handles a method_invocation: emits an EdgeCalls reference from
// the top of the node stack. A simple-receiver call `obj.m()` yields "obj.m";
// `this.m()` / `super.m()` and bare `m()` yield "m".
func (e *extractor) extractJavaCall(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		if name := javaCalleeName(node); name != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: name,
				ReferenceKind: model.EdgeCalls,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
		}
	}
	// Descend into the receiver and arguments for nested calls / new exprs.
	if obj := node.ChildByFieldName("object"); obj != nil {
		e.visitNodeJava(obj)
	}
	if args := node.ChildByFieldName("arguments"); args != nil {
		e.visitJavaBody(args)
	}
}

// extractJavaNew handles a bare object_creation_expression `new T(...)`: emits
// an EdgeInstantiates reference from the current scope.
func (e *extractor) extractJavaNew(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		fromID := e.nodeStack[len(e.nodeStack)-1]
		if t := node.ChildByFieldName("type"); t != nil {
			if name := javaSimpleType(t); name != "" {
				e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
					FromNodeID:    fromID,
					ReferenceName: name,
					ReferenceKind: model.EdgeInstantiates,
					Line:          int(node.StartPoint().Row) + 1,
					Column:        int(node.StartPoint().Column),
				})
			}
		}
	}
	if args := node.ChildByFieldName("arguments"); args != nil {
		e.visitJavaBody(args)
	}
}

// emitJavaNewFromDeclarator records an instantiates ref for a variable_declarator
// whose value is `new T()` (so field/var initializers instantiate their type).
func (e *extractor) emitJavaNewFromDeclarator(d *tsparse.Node) {
	v := d.ChildByFieldName("value")
	if v == nil || v.Kind() != "object_creation_expression" {
		return
	}
	e.extractJavaNew(v)
}

// walkJavaBody pushes id onto the node stack and visits the members of a class /
// interface body, then pops.
func (e *extractor) walkJavaBody(id string, body *tsparse.Node) {
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, id)
	for i := 0; i < body.NamedChildCount(); i++ {
		if child := body.NamedChild(i); child != nil {
			e.visitNodeJava(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// emitJavaTypeRef appends an unresolved reference of kind for a base type name.
func (e *extractor) emitJavaTypeRef(fromID, name string, kind model.EdgeKind, at *tsparse.Node) {
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

// emitJavaDecorates emits an EdgeDecorates reference per annotation head name.
func (e *extractor) emitJavaDecorates(fromID string, annotations []string, node *tsparse.Node) {
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
// Java helpers
// ──────────────────────────────────────────────────────────────────────────────

// javaMods holds the parsed modifiers/annotations of a declaration.
type javaMods struct {
	isPublic    bool
	isPrivate   bool
	isProtected bool
	isStatic    bool
	isAbstract  bool
	isFinal     bool
	annotations []string
}

// javaModifiers parses the `modifiers` child of a declaration node, collecting
// access/static/abstract/final flags and annotation head names (@Override → Override).
func javaModifiers(node *tsparse.Node) javaMods {
	var m javaMods
	var modsNode *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "modifiers" {
			modsNode = c
			break
		}
	}
	if modsNode == nil {
		return m
	}
	for i := 0; i < modsNode.ChildCount(); i++ {
		c := modsNode.Child(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "public":
			m.isPublic = true
		case "private":
			m.isPrivate = true
		case "protected":
			m.isProtected = true
		case "static":
			m.isStatic = true
		case "abstract":
			m.isAbstract = true
		case "final":
			m.isFinal = true
		case "marker_annotation", "annotation":
			if head := javaAnnotationHead(c); head != "" {
				m.annotations = append(m.annotations, head)
			}
		}
	}
	return m
}

// javaVisibility maps modifiers to a visibility string. Absent access modifiers
// mean package-private. Returns nil when public (the default-export marker is
// isExported); otherwise a pointer to the visibility string.
func javaVisibility(m javaMods) *string {
	var v string
	switch {
	case m.isPublic:
		v = "public"
	case m.isPrivate:
		v = "private"
	case m.isProtected:
		v = "protected"
	default:
		v = "package"
	}
	return &v
}

// javaAnnotationHead returns the annotation's name (the `name` field).
func javaAnnotationHead(node *tsparse.Node) string {
	if n := node.ChildByFieldName("name"); n != nil {
		return n.Text()
	}
	return ""
}

// javaTypeParameters returns the generic type-parameter names of a declaration
// (the `type_parameters` child), e.g. ["T", "K"].
func javaTypeParameters(node *tsparse.Node) []string {
	var tp *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "type_parameters" {
			tp = c
			break
		}
	}
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < tp.NamedChildCount(); i++ {
		c := tp.NamedChild(i)
		if c == nil || c.Kind() != "type_parameter" {
			continue
		}
		for j := 0; j < c.NamedChildCount(); j++ {
			id := c.NamedChild(j)
			if id != nil && id.Kind() == "type_identifier" {
				out = append(out, id.Text())
				break
			}
		}
	}
	return out
}

// javaTypeNames returns the simple type names contained in a superclass /
// super_interfaces / extends_interfaces node (reducing each to its last segment).
func javaTypeNames(node *tsparse.Node) []string {
	var out []string
	var walk func(n *tsparse.Node)
	walk = func(n *tsparse.Node) {
		if n == nil {
			return
		}
		switch n.Kind() {
		case "type_identifier":
			out = append(out, n.Text())
			return
		case "scoped_type_identifier", "generic_type":
			if name := javaSimpleType(n); name != "" {
				out = append(out, name)
			}
			return
		}
		for i := 0; i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(node)
	return out
}

// javaSimpleType reduces a type node (type_identifier, scoped_type_identifier,
// generic_type, or scoped_identifier) to its simple last-segment name.
func javaSimpleType(node *tsparse.Node) string {
	switch node.Kind() {
	case "type_identifier", "identifier":
		return node.Text()
	case "generic_type":
		// generic_type's first child is the base type.
		for i := 0; i < node.NamedChildCount(); i++ {
			c := node.NamedChild(i)
			if c != nil && (c.Kind() == "type_identifier" || c.Kind() == "scoped_type_identifier") {
				return javaSimpleType(c)
			}
		}
	}
	t := node.Text()
	// Strip generics and array suffixes, then take last dotted segment.
	if idx := strings.IndexByte(t, '<'); idx >= 0 {
		t = t[:idx]
	}
	t = strings.TrimSuffix(t, "[]")
	t = strings.TrimSpace(t)
	if idx := strings.LastIndex(t, "."); idx >= 0 {
		t = t[idx+1:]
	}
	return t
}

// javaCalleeName resolves a method_invocation to its callee reference name.
// `obj.m()` → "obj.m"; `this.m()` / `super.m()` / bare `m()` → "m".
func javaCalleeName(node *tsparse.Node) string {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return ""
	}
	method := nameNode.Text()
	if method == "" {
		return ""
	}
	obj := node.ChildByFieldName("object")
	if obj == nil {
		return method
	}
	switch obj.Kind() {
	case "this", "super":
		return method
	case "identifier":
		return obj.Text() + "." + method
	default:
		// Chained / complex receivers: resolve by bare method name.
		return method
	}
}

// javaScopedName returns the dotted name from a package_declaration or
// import_declaration's scoped_identifier child.
func javaScopedName(node *tsparse.Node) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "scoped_identifier", "identifier":
			return c.Text()
		}
	}
	return ""
}

// javaMethodSignature renders a method/constructor header: modifiers (minus
// annotations) + return type + name + parameter list.
func javaMethodSignature(node *tsparse.Node) string {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return ""
	}
	var parts []string
	if t := node.ChildByFieldName("type"); t != nil {
		parts = append(parts, t.Text())
	}
	sig := strings.Join(parts, " ")
	if sig != "" {
		sig += " "
	}
	sig += nameNode.Text()
	if params := node.ChildByFieldName("parameters"); params != nil {
		sig += params.Text()
	}
	return strings.TrimSpace(sig)
}

// javaFieldSignature renders a field/variable signature: "Type name = init".
func javaFieldSignature(typeName string, declarator *tsparse.Node) string {
	name := ""
	if n := declarator.ChildByFieldName("name"); n != nil {
		name = n.Text()
	}
	sig := strings.TrimSpace(typeName + " " + name)
	if v := declarator.ChildByFieldName("value"); v != nil {
		val := v.Text()
		if len(val) > 100 {
			val = val[:100] + "..."
		}
		sig += " = " + val
	}
	return strings.TrimSpace(sig)
}

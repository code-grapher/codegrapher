package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkObjC walks a parsed Objective-C (tree-sitter `objc`) translation unit.
// Objective-C is a strict superset of C, so the shared
// declaration/struct/enum/typedef/#include/#import/call machinery is REUSED from
// walk_c.go (the extractC* helpers); this file adds only the Obj-C object layer:
// @interface/@implementation (classes), @protocol (interfaces), categories,
// methods (selectors), properties, instance variables, message-send calls, and
// alloc/init instantiation.
//
// Node-kind reference (tree-sitter-objc), confirmed by AST probe:
//
//	class_interface (identifier name; optional "superclass" field; optional
//	    "category" field; protocol_reference_list / parameterized_arguments for
//	    adopted protocols <P1,P2>; instance_variables; property_declaration;
//	    method_declaration members)
//	class_implementation (identifier name; implementation_definition wrapping
//	    method_definition)
//	protocol_declaration (identifier name; protocol_reference_list; method_declaration)
//	method_declaration / method_definition (leading -/+ token = instance/class;
//	    method_type return; identifier selector parts; method_parameter ":(T)name")
//	property_declaration (property_attributes_declaration; struct_declaration type+name)
//	instance_variables → instance_variable → struct_declaration (ivar type+name)
//	message_expression ("receiver" field; one or more "method" fields)
//	preproc_include (#include and #import both)
func (e *extractor) walkObjC(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeObjC(child)
		}
	}
}

// visitNodeObjC dispatches a single top-level (or nested) Obj-C node. Obj-C-only
// kinds are handled here; everything in the shared C subset delegates to the
// walk_c.go helpers. Unknown kinds descend into children.
func (e *extractor) visitNodeObjC(node *tsparse.Node) {
	switch node.Kind() {
	case "class_interface":
		e.extractObjCInterface(node)
	case "class_implementation":
		e.extractObjCImplementation(node)
	case "protocol_declaration":
		e.extractObjCProtocol(node)

	// Shared C subset — reuse walk_c.go.
	case "preproc_include":
		e.extractCInclude(node)
	case "preproc_def":
		e.extractCDefine(node)
	case "preproc_function_def":
		e.extractCFunctionMacro(node)
	case "function_definition":
		e.extractObjCFunctionDefinition(node)
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
				e.visitNodeObjC(child)
			}
		}
	}
}

// extractObjCFunctionDefinition handles a C function_definition at file scope in
// an Obj-C file. It mirrors extractCFunctionDefinition (reusing the C node
// creation and type-ref helpers) but walks the body with visitObjCBody so
// Objective-C message-sends inside a plain C function are captured.
func (e *extractor) extractObjCFunctionDefinition(node *tsparse.Node) {
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
	e.emitCTypeRef(fn.ID, node.ChildByFieldName("type"), node)
	e.emitCParamTypeRefs(fn.ID, decl, node)
	if body := node.ChildByFieldName("body"); body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		e.visitObjCBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractObjCInterface handles a class_interface. A plain @interface (no
// category) → KindClass with extends/implements edges; its methods/properties/
// ivars are members. A category @interface X (Cat) attaches its methods to the
// existing class X by qualified name (no separate node).
func (e *extractor) extractObjCInterface(node *tsparse.Node) {
	nameNode := objcFirstIdentifier(node)
	if nameNode == nil {
		return
	}
	className := nameNode.Text()

	if cat := node.ChildByFieldName("category"); cat != nil {
		// Category: attach methods to the base class by qualified name.
		e.extractObjCCategoryMembers(node, className)
		return
	}

	cn := e.createNode(model.KindClass, className, node, nodeExtra{
		docstring: e.lookupDoc(node),
		signature: objcInterfaceSignature(node),
	})
	if cn == nil {
		return
	}

	// extends: superclass.
	if sup := node.ChildByFieldName("superclass"); sup != nil {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    cn.ID,
			ReferenceName: sup.Text(),
			ReferenceKind: model.EdgeExtends,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}

	// implements: adopted protocols <P1,P2>.
	for _, p := range objcAdoptedProtocols(node) {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    cn.ID,
			ReferenceName: p,
			ReferenceKind: model.EdgeImplements,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}

	e.nodeStack = append(e.nodeStack, cn.ID)
	e.extractObjCMembers(node)
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractObjCCategoryMembers extracts a category's methods as members of the
// base class X, naming them X::selector so the resolver attaches them to X.
func (e *extractor) extractObjCCategoryMembers(node *tsparse.Node, className string) {
	for i := 0; i < node.NamedChildCount(); i++ {
		m := node.NamedChild(i)
		if m == nil {
			continue
		}
		switch m.Kind() {
		case "method_declaration", "method_definition":
			e.extractObjCCategoryMethod(m, className)
		}
	}
}

// extractObjCCategoryMethod extracts a category method → KindMethod with the
// qualified name X::selector (the base class X is not on the node stack, so the
// prefix is set explicitly).
func (e *extractor) extractObjCCategoryMethod(node *tsparse.Node, className string) {
	selector := objcSelector(node)
	if selector == "" {
		return
	}
	isClass := strings.HasPrefix(strings.TrimSpace(node.Text()), "+")
	mn := e.createNode(model.KindMethod, selector, node, nodeExtra{
		signature:     objcMethodSignature(node),
		returnType:    objcMethodReturnType(node),
		isStatic:      isClass,
		qualifiedName: className + "::" + selector,
		docstring:     e.lookupDoc(node),
	})
	if mn == nil {
		return
	}
	if body := objcMethodBody(node); body != nil {
		e.nodeStack = append(e.nodeStack, mn.ID)
		e.visitObjCBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractObjCImplementation handles a class_implementation → it contributes the
// same class name's method bodies. A separate KindClass node is created (merged
// by NAME during resolution: both @interface and @implementation nodes share the
// class name, and edges resolve by name).
func (e *extractor) extractObjCImplementation(node *tsparse.Node) {
	nameNode := objcFirstIdentifier(node)
	if nameNode == nil {
		return
	}
	className := nameNode.Text()

	// A category implementation @implementation X (Cat) attaches to X too.
	if node.ChildByFieldName("category") != nil {
		e.extractObjCImplMembers(node)
		return
	}

	cn := e.createNode(model.KindClass, className, node, nodeExtra{
		docstring: e.lookupDoc(node),
		signature: "@implementation " + className,
	})
	if cn == nil {
		// A node with the same id already exists (interface+impl on the same
		// line is impossible across files); still walk members for calls.
		e.extractObjCImplMembers(node)
		return
	}
	e.nodeStack = append(e.nodeStack, cn.ID)
	e.extractObjCImplMembers(node)
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractObjCImplMembers walks an implementation's method_definition members
// (wrapped in implementation_definition).
func (e *extractor) extractObjCImplMembers(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		m := node.NamedChild(i)
		if m == nil {
			continue
		}
		switch m.Kind() {
		case "implementation_definition":
			for j := 0; j < m.NamedChildCount(); j++ {
				d := m.NamedChild(j)
				if d != nil && d.Kind() == "method_definition" {
					e.extractObjCMethod(d)
				}
			}
		case "method_definition", "method_declaration":
			e.extractObjCMethod(m)
		}
	}
}

// extractObjCProtocol handles a protocol_declaration → KindInterface (per the
// symbol model), with its method declarations as members.
func (e *extractor) extractObjCProtocol(node *tsparse.Node) {
	nameNode := objcFirstIdentifier(node)
	if nameNode == nil {
		return
	}
	protoName := nameNode.Text()
	pn := e.createNode(model.KindInterface, protoName, node, nodeExtra{
		docstring: e.lookupDoc(node),
		signature: "@protocol " + protoName,
	})
	if pn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, pn.ID)
	for i := 0; i < node.NamedChildCount(); i++ {
		m := node.NamedChild(i)
		if m == nil {
			continue
		}
		switch m.Kind() {
		case "method_declaration", "method_definition":
			e.extractObjCMethod(m)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractObjCMembers walks an @interface body: instance variables, properties,
// and method declarations.
func (e *extractor) extractObjCMembers(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		m := node.NamedChild(i)
		if m == nil {
			continue
		}
		switch m.Kind() {
		case "instance_variables":
			e.extractObjCInstanceVars(m)
		case "property_declaration":
			e.extractObjCProperty(m)
		case "method_declaration", "method_definition":
			e.extractObjCMethod(m)
		}
	}
}

// extractObjCInstanceVars handles an instance_variables block → KindField per
// ivar.
func (e *extractor) extractObjCInstanceVars(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		iv := node.NamedChild(i)
		if iv == nil || iv.Kind() != "instance_variable" {
			continue
		}
		for j := 0; j < iv.NamedChildCount(); j++ {
			sd := iv.NamedChild(j)
			if sd == nil || sd.Kind() != "struct_declaration" {
				continue
			}
			name := objcStructDeclaratorName(sd)
			if name == "" {
				continue
			}
			fn := e.createNode(model.KindField, name, sd, nodeExtra{
				signature: strings.TrimSpace(sd.Text()),
			})
			if fn != nil {
				e.emitCTypeRef(fn.ID, objcStructDeclType(sd), sd)
			}
		}
	}
}

// extractObjCProperty handles a property_declaration → KindProperty, referencing
// the property type.
func (e *extractor) extractObjCProperty(node *tsparse.Node) {
	var sd *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "struct_declaration" {
			sd = c
			break
		}
	}
	if sd == nil {
		return
	}
	name := objcStructDeclaratorName(sd)
	if name == "" {
		return
	}
	pn := e.createNode(model.KindProperty, name, node, nodeExtra{
		signature: strings.TrimSpace(node.Text()),
	})
	if pn != nil {
		e.emitCTypeRef(pn.ID, objcStructDeclType(sd), sd)
	}
}

// extractObjCMethod handles a method_declaration / method_definition →
// KindMethod, named by selector (colons kept). A `+` method is a class method
// (isStatic). For a method_definition the body is walked for message-send calls
// and instantiation.
func (e *extractor) extractObjCMethod(node *tsparse.Node) {
	selector := objcSelector(node)
	if selector == "" {
		return
	}
	isClass := strings.HasPrefix(strings.TrimSpace(node.Text()), "+")
	mn := e.createNode(model.KindMethod, selector, node, nodeExtra{
		signature:  objcMethodSignature(node),
		returnType: objcMethodReturnType(node),
		isStatic:   isClass,
		docstring:  e.lookupDoc(node),
	})
	if mn == nil {
		return
	}
	if body := objcMethodBody(node); body != nil {
		e.nodeStack = append(e.nodeStack, mn.ID)
		e.visitObjCBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// visitObjCBody walks a method body for message-send calls (and the C
// call_expression form, reused). A message-send whose method is alloc/new on a
// class receiver is an instantiation; otherwise it is a call to the selector.
func (e *extractor) visitObjCBody(body *tsparse.Node) {
	tsparse.Walk(body, func(node *tsparse.Node) {
		switch node.Kind() {
		case "message_expression":
			e.extractObjCMessage(node)
		case "call_expression":
			e.extractCCall(node)
		}
	})
}

// extractObjCMessage handles a message_expression. `[Class alloc]` / `[Class new]`
// → instantiates the receiver class. Any other selector → calls the selector
// method; a `self`/`super` receiver is stripped (resolve by selector name).
func (e *extractor) extractObjCMessage(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	fromID := e.nodeStack[len(e.nodeStack)-1]
	selector := objcMessageSelector(node)
	if selector == "" {
		return
	}
	receiver := node.ChildByFieldName("receiver")

	// alloc / new on a plain class identifier → instantiates.
	if (selector == "alloc" || selector == "new") && receiver != nil &&
		receiver.Kind() == "identifier" {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: receiver.Text(),
			ReferenceKind: model.EdgeInstantiates,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
		return
	}

	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    fromID,
		ReferenceName: selector,
		ReferenceKind: model.EdgeCalls,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// ── stateless helpers ───────────────────────────────────────────────────────

// objcFirstIdentifier returns the first named `identifier` child (the class /
// protocol name).
func objcFirstIdentifier(node *tsparse.Node) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "identifier" {
			return c
		}
	}
	return nil
}

// objcAdoptedProtocols returns the protocol names adopted in <P1,P2> on an
// @interface (a protocol_reference_list or parameterized_arguments child).
func objcAdoptedProtocols(node *tsparse.Node) []string {
	var protos []string
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "protocol_reference_list", "parameterized_arguments":
			for j := 0; j < c.NamedChildCount(); j++ {
				p := c.NamedChild(j)
				if p == nil {
					continue
				}
				switch p.Kind() {
				case "identifier":
					protos = append(protos, p.Text())
				case "type_name":
					if id := objcFirstIdentifier(p); id != nil {
						protos = append(protos, id.Text())
					}
				}
			}
		}
	}
	return protos
}

// objcSelector builds a method's selector name from a method_declaration /
// method_definition: the joined keyword `identifier` parts, with `:` kept where
// a method_parameter follows. A unary selector has a single identifier and no
// colon.
func objcSelector(node *tsparse.Node) string {
	var b strings.Builder
	hasParam := false
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "identifier":
			b.WriteString(c.Text())
		case "method_parameter":
			// The keyword part for this parameter is the immediately preceding
			// identifier; append the colon for it.
			b.WriteString(":")
			hasParam = true
		}
	}
	sel := b.String()
	_ = hasParam
	return sel
}

// objcMessageSelector builds the selector from a message_expression's "method"
// fields. Multiple keyword parts join with `:` (one per keyword, trailing colon
// kept). A unary message has one method part and no colon.
func objcMessageSelector(node *tsparse.Node) string {
	var parts []string
	for i := 0; i < node.ChildCount(); i++ {
		c := node.Child(i)
		if c == nil {
			continue
		}
		if node.FieldNameForChild(i) == "method" {
			parts = append(parts, c.Text())
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		// Unary selector (no colon) unless arguments were present. Detect a
		// keyword selector with a single keyword by scanning for an argument.
		if objcMessageHasArgs(node) {
			return parts[0] + ":"
		}
		return parts[0]
	}
	return strings.Join(parts, ":") + ":"
}

// objcMessageHasArgs reports whether a message_expression carries argument
// expressions after the first method keyword (→ keyword selector with colon).
func objcMessageHasArgs(node *tsparse.Node) bool {
	seenMethod := false
	for i := 0; i < node.ChildCount(); i++ {
		c := node.Child(i)
		if c == nil {
			continue
		}
		f := node.FieldNameForChild(i)
		if f == "method" {
			seenMethod = true
			continue
		}
		if f == "receiver" {
			continue
		}
		// A named non-method, non-receiver child after a method keyword is an
		// argument expression.
		if seenMethod && c.IsNamed() {
			return true
		}
	}
	return false
}

// objcMethodReturnType returns the method's declared return type (the
// method_type's inner type text), or "".
func objcMethodReturnType(node *tsparse.Node) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "method_type" {
			return strings.TrimSpace(strings.Trim(strings.TrimSpace(c.Text()), "()"))
		}
	}
	return ""
}

// objcMethodSignature renders a method's declaration line (selector + types),
// trimming a trailing body / semicolon.
func objcMethodSignature(node *tsparse.Node) string {
	s := strings.TrimSpace(node.Text())
	if idx := strings.Index(s, "{"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	s = strings.TrimSuffix(s, ";")
	return strings.TrimSpace(s)
}

// objcMethodBody returns the compound_statement body of a method_definition, or
// nil for a bare declaration.
func objcMethodBody(node *tsparse.Node) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "compound_statement" {
			return c
		}
	}
	return nil
}

// objcInterfaceSignature renders the @interface header line.
func objcInterfaceSignature(node *tsparse.Node) string {
	s := strings.TrimSpace(node.Text())
	if idx := strings.IndexAny(s, "{\n"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	return s
}

// objcStructDeclaratorName extracts the declared name from a struct_declaration
// (used for ivars and properties), unwrapping the struct_declarator wrapper and
// then the pointer/identifier declarator chain (via cDeclaratorName).
func objcStructDeclaratorName(sd *tsparse.Node) string {
	for i := 0; i < sd.NamedChildCount(); i++ {
		c := sd.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "struct_declarator":
			// struct_declarator wraps the actual declarator chain.
			for j := 0; j < c.NamedChildCount(); j++ {
				if n := cDeclaratorName(c.NamedChild(j)); n != "" {
					return n
				}
			}
		case "pointer_declarator", "identifier", "field_identifier":
			if n := cDeclaratorName(c); n != "" {
				return n
			}
		}
	}
	return ""
}

// objcStructDeclType returns the type node of a struct_declaration (the
// type_identifier / primitive_type before the declarator).
func objcStructDeclType(sd *tsparse.Node) *tsparse.Node {
	for i := 0; i < sd.NamedChildCount(); i++ {
		c := sd.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "type_identifier", "primitive_type", "sized_type_specifier":
			return c
		}
	}
	return nil
}

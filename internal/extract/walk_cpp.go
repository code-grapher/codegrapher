package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkCPP walks a parsed C++ (tree-sitter `cpp`) translation unit. C++ is a
// superset of C, so the shared declaration/struct/enum/typedef/#include/call
// machinery is REUSED from walk_c.go (the extractC* helpers); this file adds
// only the C++-specific constructs: namespaces, classes (with methods,
// constructors, destructors, access sections, base classes, virtual overrides),
// templates, enum-class, using-declarations and alias-declarations.
//
// Node-kind reference (tree-sitter-cpp), confirmed by AST probe:
//
//	namespace_definition (fields "name" namespace_identifier, "body"
//	    declaration_list)
//	class_specifier / struct_specifier (fields "name" type_identifier,
//	    "body" field_declaration_list, optional base_class_clause child)
//	base_class_clause (access_specifier* + type_identifier+)
//	field_declaration (a data member, OR a method declaration whose declarator
//	    is a function_declarator; "default_value" number_literal == 0 → pure
//	    virtual; a virtual_specifier child "override"/"final")
//	function_definition (an inline method body inside a class)
//	declaration (a constructor/destructor declaration inside a class:
//	    declarator is a function_declarator over an identifier / destructor_name)
//	template_declaration (field "parameters" template_parameter_list) wrapping a
//	    class_specifier / function_definition / declaration
//	enum_specifier (enum class) — same shape as C
//	using_declaration ("using N::x;" qualified_identifier, or "using namespace N;"
//	    bare identifier)
//	alias_declaration (fields "name" type_identifier, "type" type_descriptor) —
//	    "using X = Y;"
//	new_expression (field "type") — instantiates
//	call_expression with qualified_identifier ("A::f") / field_expression
//	    ("obj.m") function
func (e *extractor) walkCPP(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeCPP(child)
		}
	}
}

// visitNodeCPP dispatches a single C++ node. C++-only kinds are handled here;
// everything in the shared C subset delegates to the walk_c.go helpers. Unknown
// kinds descend into children.
func (e *extractor) visitNodeCPP(node *tsparse.Node) {
	switch node.Kind() {
	case "namespace_definition":
		e.extractCPPNamespace(node)
	case "class_specifier", "struct_specifier":
		e.extractCPPClass(node)
	case "template_declaration":
		e.extractCPPTemplate(node)
	case "enum_specifier":
		e.extractCEnum(node)
	case "using_declaration":
		e.extractCPPUsing(node)
	case "alias_declaration":
		e.extractCPPAlias(node)

	// Shared C subset — reuse walk_c.go.
	case "preproc_include":
		e.extractCInclude(node)
	case "preproc_def":
		e.extractCDefine(node)
	case "preproc_function_def":
		e.extractCFunctionMacro(node)
	case "function_definition":
		e.extractCPPFunctionDefinition(node)
	case "declaration":
		e.extractCPPDeclaration(node)
	case "type_definition":
		e.extractCTypedef(node)

	default:
		for i := 0; i < node.NamedChildCount(); i++ {
			if child := node.NamedChild(i); child != nil {
				e.visitNodeCPP(child)
			}
		}
	}
}

// extractCPPNamespace handles namespace_definition → KindNamespace, recursing
// into the body so nested declarations get the namespace prefix in their
// qualified names. An anonymous namespace (no name) is skipped but its body is
// still walked.
func (e *extractor) extractCPPNamespace(node *tsparse.Node) {
	body := node.ChildByFieldName("body")
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		// anonymous namespace: walk body without a scope node.
		if body != nil {
			for i := 0; i < body.NamedChildCount(); i++ {
				if c := body.NamedChild(i); c != nil {
					e.visitNodeCPP(c)
				}
			}
		}
		return
	}
	ns := e.createNode(model.KindNamespace, nameNode.Text(), node, nodeExtra{
		docstring: e.lookupDoc(node),
	})
	if ns == nil {
		return
	}
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, ns.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		if c := body.NamedChild(i); c != nil {
			e.visitNodeCPP(c)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractCPPTemplate unwraps a template_declaration to the class/function it
// templates, recording the template parameters. A class/struct becomes a
// templated class; a function_definition/declaration becomes a templated
// function (or method when nested in a class).
func (e *extractor) extractCPPTemplate(node *tsparse.Node) {
	params := cppTemplateParams(node.ChildByFieldName("parameters"))
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "class_specifier", "struct_specifier":
			e.extractCPPClassTpl(c, params)
			return
		case "function_definition":
			e.extractCPPFunctionDefinitionTpl(c, params)
			return
		case "declaration":
			e.extractCPPDeclarationTpl(c, params)
			return
		}
	}
}

// extractCPPClass handles class_specifier / struct_specifier → KindClass (a
// struct with methods/access-specifiers is treated as a class; a plain C-style
// struct is left to extractCStruct). Members are walked with the current access
// section tracked for visibility. Base classes emit extends + the class's base
// names are recorded so virtual overrides can be detected.
func (e *extractor) extractCPPClass(node *tsparse.Node) {
	e.extractCPPClassTpl(node, nil)
}

func (e *extractor) extractCPPClassTpl(node *tsparse.Node, typeParams []string) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	body := node.ChildByFieldName("body")

	// A struct_specifier with no methods/access-specifiers is a plain C struct.
	if node.Kind() == "struct_specifier" && !cppBodyHasMethods(body) {
		e.extractCStruct(node)
		return
	}

	cn := e.createNode(model.KindClass, nameNode.Text(), node, nodeExtra{
		docstring:      e.lookupDoc(node),
		signature:      cppClassSignature(node),
		typeParameters: typeParams,
	})
	if cn == nil {
		return
	}

	bases := cppBaseClassNames(node)
	for _, base := range bases {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    cn.ID,
			ReferenceName: base,
			ReferenceKind: model.EdgeExtends,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}

	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, cn.ID)
	visibility := "private"
	if node.Kind() == "struct_specifier" {
		visibility = "public"
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		if m == nil {
			continue
		}
		switch m.Kind() {
		case "access_specifier":
			visibility = strings.TrimSpace(m.Text())
		case "field_declaration":
			e.extractCPPMember(m, nameNode.Text(), bases, visibility)
		case "function_definition":
			e.extractCPPMethod(m, nameNode.Text(), bases, visibility)
		case "declaration":
			e.extractCPPMethodDecl(m, nameNode.Text(), bases, visibility)
		case "template_declaration":
			e.extractCPPTemplate(m)
		case "using_declaration":
			e.extractCPPUsing(m)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractCPPMember handles a field_declaration inside a class body. When its
// declarator is a function_declarator it is a method declaration; otherwise it
// is a data member (KindField, or KindConstant for static const/constexpr).
func (e *extractor) extractCPPMember(fd *tsparse.Node, className string, bases []string, visibility string) {
	decl := fd.ChildByFieldName("declarator")
	if cIsFunctionDeclarator(decl) {
		e.extractCPPMethodDecl(fd, className, bases, visibility)
		return
	}
	// Data member(s). A field_declaration may declare several field_identifiers.
	vis := visibility
	kind := model.KindField
	if cppIsStaticConst(fd) {
		kind = model.KindConstant
	}
	emitted := false
	for i := 0; i < fd.NamedChildCount(); i++ {
		c := fd.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == "field_identifier" {
			fn := e.createNode(kind, c.Text(), fd, nodeExtra{
				signature:  strings.TrimSpace(fd.Text()),
				isStatic:   cIsStatic(fd),
				visibility: &vis,
			})
			if fn != nil {
				e.emitCTypeRef(fn.ID, fd.ChildByFieldName("type"), fd)
			}
			emitted = true
		}
	}
	if !emitted {
		// declarator-wrapped single field (e.g. pointer/array): fall back.
		name := cDeclaratorName(decl)
		if name == "" {
			return
		}
		fn := e.createNode(kind, name, fd, nodeExtra{
			signature:  strings.TrimSpace(fd.Text()),
			isStatic:   cIsStatic(fd),
			visibility: &vis,
		})
		if fn != nil {
			e.emitCTypeRef(fn.ID, fd.ChildByFieldName("type"), fd)
		}
	}
}

// extractCPPMethodDecl handles a method declaration (a field_declaration or
// declaration whose declarator is a function_declarator) → KindMethod. Detects
// pure-virtual (= 0 → isAbstract), constructors/destructors, override, and emits
// an overrides ref when the method name matches a base-class method.
func (e *extractor) extractCPPMethodDecl(node *tsparse.Node, className string, bases []string, visibility string) {
	decl := node.ChildByFieldName("declarator")
	fdecl := cFunctionDeclarator(decl)
	if fdecl == nil {
		return
	}
	name := cppMethodName(fdecl, className)
	if name == "" {
		return
	}
	vis := visibility
	mn := e.createNode(model.KindMethod, name, node, nodeExtra{
		signature:  cppMethodSignature(node),
		returnType: cTypeName(node.ChildByFieldName("type")),
		isStatic:   cIsStatic(node),
		isAbstract: cppIsPureVirtual(node),
		visibility: &vis,
		docstring:  e.lookupDoc(node),
	})
	if mn == nil {
		return
	}
	e.emitCTypeRef(mn.ID, node.ChildByFieldName("type"), node)
	e.emitCParamTypeRefs(mn.ID, decl, node)
	e.emitCPPOverride(mn.ID, name, bases, node)
}

// extractCPPMethod handles an inline method body (function_definition) inside a
// class → KindMethod, then walks the body for calls.
func (e *extractor) extractCPPMethod(node *tsparse.Node, className string, bases []string, visibility string) {
	decl := node.ChildByFieldName("declarator")
	fdecl := cFunctionDeclarator(decl)
	if fdecl == nil {
		return
	}
	name := cppMethodName(fdecl, className)
	if name == "" {
		return
	}
	vis := visibility
	mn := e.createNode(model.KindMethod, name, node, nodeExtra{
		signature:  cppMethodSignature(node),
		returnType: cTypeName(node.ChildByFieldName("type")),
		isStatic:   cIsStatic(node),
		isAbstract: cppIsPureVirtual(node),
		visibility: &vis,
		docstring:  e.lookupDoc(node),
	})
	if mn == nil {
		return
	}
	e.emitCTypeRef(mn.ID, node.ChildByFieldName("type"), node)
	e.emitCParamTypeRefs(mn.ID, decl, node)
	e.emitCPPOverride(mn.ID, name, bases, node)

	if body := node.ChildByFieldName("body"); body != nil {
		e.nodeStack = append(e.nodeStack, mn.ID)
		e.visitCPPBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// emitCPPOverride emits an EdgeOverrides ref ("Base::method") when the method is
// declared override/final OR a base class exists (best-effort: the resolver only
// produces an edge if a base actually declares a same-named method). Constructors
// and destructors never override.
func (e *extractor) emitCPPOverride(fromID, name string, bases []string, node *tsparse.Node) {
	if len(bases) == 0 || strings.HasPrefix(name, "~") {
		return
	}
	for _, base := range bases {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: base + "::" + name,
			ReferenceKind: model.EdgeOverrides,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// extractCPPFunctionDefinition handles a free function definition at namespace /
// file scope. It is the C extractor with C++ call/qualified-name awareness in the
// body walk.
func (e *extractor) extractCPPFunctionDefinition(node *tsparse.Node) {
	e.extractCPPFunctionDefinitionTpl(node, nil)
}

func (e *extractor) extractCPPFunctionDefinitionTpl(node *tsparse.Node, typeParams []string) {
	decl := node.ChildByFieldName("declarator")
	name := cDeclaratorName(decl)
	if name == "" {
		return
	}
	fn := e.createNode(model.KindFunction, name, node, nodeExtra{
		isStatic:       cIsStatic(node),
		returnType:     cTypeName(node.ChildByFieldName("type")),
		signature:      cFunctionSignature(node),
		typeParameters: typeParams,
		docstring:      e.lookupDoc(node),
	})
	if fn == nil {
		return
	}
	e.emitCTypeRef(fn.ID, node.ChildByFieldName("type"), node)
	e.emitCParamTypeRefs(fn.ID, decl, node)
	if body := node.ChildByFieldName("body"); body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		e.visitCPPBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractCPPDeclaration handles a namespace/file-scope declaration: a function
// prototype or a variable/constant. Reuses the C declaration logic.
func (e *extractor) extractCPPDeclaration(node *tsparse.Node) {
	e.extractCPPDeclarationTpl(node, nil)
}

func (e *extractor) extractCPPDeclarationTpl(node *tsparse.Node, typeParams []string) {
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
			isStatic:       cIsStatic(node),
			returnType:     cTypeName(node.ChildByFieldName("type")),
			signature:      cFunctionSignature(node),
			typeParameters: typeParams,
			docstring:      e.lookupDoc(node),
		})
		if fn == nil {
			return
		}
		e.emitCTypeRef(fn.ID, node.ChildByFieldName("type"), node)
		e.emitCParamTypeRefs(fn.ID, decl, node)
		return
	}
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
		// A namespace/file-scope `Type x(...)` / `Type x{...}` constructs an object.
		e.emitCPPConstruction(vn.ID, node)
	}
}

// extractCPPUsing handles a using_declaration:
//   - "using namespace N;" / "using N::sym;" → KindImport (the resolver treats it
//     as a namespace-bringing import). The reference name is the namespace/symbol.
func (e *extractor) extractCPPUsing(node *tsparse.Node) {
	sig := strings.TrimSpace(node.Text())
	var name string
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "qualified_identifier", "identifier", "namespace_identifier":
			name = strings.TrimSpace(c.Text())
		}
		if name != "" {
			break
		}
	}
	if name == "" {
		return
	}
	e.createNode(model.KindImport, name, node, nodeExtra{signature: sig})
}

// extractCPPAlias handles an alias_declaration ("using X = Y;") → KindTypeAlias,
// referencing the aliased type.
func (e *extractor) extractCPPAlias(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	an := e.createNode(model.KindTypeAlias, nameNode.Text(), node, nodeExtra{
		signature: strings.TrimSpace(node.Text()),
		docstring: e.lookupDoc(node),
	})
	if an == nil {
		return
	}
	if t := node.ChildByFieldName("type"); t != nil {
		if name := cppTypeDescriptorName(t); name != "" && !cBuiltinTypes[name] {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    an.ID,
				ReferenceName: name,
				ReferenceKind: model.EdgeReferences,
				Line:          int(t.StartPoint().Row) + 1,
				Column:        int(t.StartPoint().Column),
			})
		}
	}
}

// visitCPPBody walks a method/function body for call_expression and
// new_expression / construction declarations, emitting calls / instantiates.
func (e *extractor) visitCPPBody(body *tsparse.Node) {
	tsparse.Walk(body, func(node *tsparse.Node) {
		switch node.Kind() {
		case "call_expression":
			e.extractCPPCall(node)
		case "new_expression":
			e.extractCPPNew(node)
		case "declaration":
			// local `Type x(args);` / `Type x{args};` construction.
			if len(e.nodeStack) > 0 {
				e.emitCPPConstructionDecl(e.nodeStack[len(e.nodeStack)-1], node)
			}
		case "using_declaration":
			// function-body `using namespace N;` / `using N::sym;` → import.
			e.extractCPPUsing(node)
		}
	})
}

// extractCPPCall handles a call_expression. The callee name comes from a bare
// identifier, a qualified_identifier ("A::B::f" → kept qualified), or a
// field_expression ("obj.m" / "ptr->m" → trailing member name, `this` stripped).
func (e *extractor) extractCPPCall(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	callerID := e.nodeStack[len(e.nodeStack)-1]
	fn := node.ChildByFieldName("function")
	name := cppCalleeName(fn)
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

// extractCPPNew handles a new_expression (`new T(...)`) → instantiates T.
func (e *extractor) extractCPPNew(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	t := node.ChildByFieldName("type")
	name := cppTypeDescriptorName(t)
	if name == "" || cBuiltinTypes[name] {
		return
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    e.nodeStack[len(e.nodeStack)-1],
		ReferenceName: name,
		ReferenceKind: model.EdgeInstantiates,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// emitCPPConstructionDecl emits an instantiates ref for a stack construction
// `Type x(args);` / `Type x{args};` where the init_declarator carries an
// argument_list / initializer_list value.
func (e *extractor) emitCPPConstructionDecl(fromID string, node *tsparse.Node) {
	t := node.ChildByFieldName("type")
	name := cTypeName(t)
	if name == "" || cBuiltinTypes[name] {
		return
	}
	// Only treat as construction when an init_declarator has a call/brace init.
	constructed := false
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil || c.Kind() != "init_declarator" {
			continue
		}
		if v := c.ChildByFieldName("value"); v != nil {
			switch v.Kind() {
			case "argument_list", "initializer_list":
				constructed = true
			}
		}
	}
	if !constructed {
		return
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    fromID,
		ReferenceName: name,
		ReferenceKind: model.EdgeInstantiates,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// emitCPPConstruction is the namespace/file-scope variant (caller already has
// the from node ID via the variable's node).
func (e *extractor) emitCPPConstruction(fromID string, node *tsparse.Node) {
	e.emitCPPConstructionDecl(fromID, node)
}

// ── stateless helpers ───────────────────────────────────────────────────────

// cppBaseClassNames returns the base-class type names from a class's
// base_class_clause (the type_identifier / qualified_identifier entries).
func cppBaseClassNames(classNode *tsparse.Node) []string {
	var bases []string
	for i := 0; i < classNode.NamedChildCount(); i++ {
		c := classNode.NamedChild(i)
		if c == nil || c.Kind() != "base_class_clause" {
			continue
		}
		for j := 0; j < c.NamedChildCount(); j++ {
			b := c.NamedChild(j)
			if b == nil {
				continue
			}
			switch b.Kind() {
			case "type_identifier":
				bases = append(bases, b.Text())
			case "qualified_identifier":
				bases = append(bases, cppQualifiedTail(b))
			}
		}
	}
	return bases
}

// cppBodyHasMethods reports whether a class/struct body contains a method
// (function declarator) or an access_specifier — the heuristic that promotes a
// struct_specifier to a class.
func cppBodyHasMethods(body *tsparse.Node) bool {
	if body == nil {
		return false
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		c := body.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "access_specifier", "function_definition":
			return true
		case "field_declaration", "declaration":
			if cIsFunctionDeclarator(c.ChildByFieldName("declarator")) {
				return true
			}
		}
	}
	return false
}

// cppMethodName resolves a method's name from its function_declarator. Handles
// plain identifiers, field_identifiers, constructors (identifier == className),
// destructors (destructor_name → "~Name"), and operators (operator_name).
func cppMethodName(fdecl *tsparse.Node, className string) string {
	inner := fdecl.ChildByFieldName("declarator")
	if inner == nil {
		return ""
	}
	switch inner.Kind() {
	case "identifier", "field_identifier", "type_identifier":
		return inner.Text()
	case "destructor_name":
		return strings.TrimSpace(inner.Text())
	case "operator_name":
		return strings.TrimSpace(inner.Text())
	case "qualified_identifier":
		return cppQualifiedTail(inner)
	}
	if n := cDeclaratorName(inner); n != "" {
		return n
	}
	return ""
}

// cppCalleeName resolves a call's function node to a callee name.
//   - identifier            → bare name
//   - qualified_identifier  → kept qualified ("A::B::f")
//   - field_expression      → trailing member ("obj.m" → "m"); a `this->m`
//     receiver is stripped to the bare member name.
func cppCalleeName(fn *tsparse.Node) string {
	if fn == nil {
		return ""
	}
	switch fn.Kind() {
	case "identifier":
		return fn.Text()
	case "qualified_identifier":
		return strings.TrimSpace(fn.Text())
	case "field_expression":
		if f := fn.ChildByFieldName("field"); f != nil {
			return f.Text()
		}
	case "parenthesized_expression", "pointer_expression":
		for i := 0; i < fn.NamedChildCount(); i++ {
			if n := cppCalleeName(fn.NamedChild(i)); n != "" {
				return n
			}
		}
	}
	return ""
}

// cppQualifiedTail returns the trailing name of a qualified_identifier
// ("A::B::sym" → "sym").
func cppQualifiedTail(q *tsparse.Node) string {
	if n := q.ChildByFieldName("name"); n != nil {
		if n.Kind() == "qualified_identifier" {
			return cppQualifiedTail(n)
		}
		return n.Text()
	}
	t := strings.TrimSpace(q.Text())
	if idx := strings.LastIndex(t, "::"); idx >= 0 {
		return t[idx+2:]
	}
	return t
}

// cppTypeDescriptorName extracts a type name from a type node or type_descriptor
// (used by new_expression "type" and alias_declaration "type"). qualified types
// yield their trailing name.
func cppTypeDescriptorName(t *tsparse.Node) string {
	if t == nil {
		return ""
	}
	switch t.Kind() {
	case "type_identifier", "primitive_type", "sized_type_specifier":
		return strings.TrimSpace(t.Text())
	case "qualified_identifier":
		return cppQualifiedTail(t)
	case "type_descriptor", "template_type":
		for i := 0; i < t.NamedChildCount(); i++ {
			if n := cppTypeDescriptorName(t.NamedChild(i)); n != "" {
				return n
			}
		}
	}
	return ""
}

// cppTemplateParams returns the parameter names from a template_parameter_list.
func cppTemplateParams(list *tsparse.Node) []string {
	if list == nil {
		return nil
	}
	var params []string
	for i := 0; i < list.NamedChildCount(); i++ {
		c := list.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "type_parameter_declaration", "optional_type_parameter_declaration",
			"variadic_type_parameter_declaration":
			for j := 0; j < c.NamedChildCount(); j++ {
				if id := c.NamedChild(j); id != nil && id.Kind() == "type_identifier" {
					params = append(params, id.Text())
				}
			}
		case "parameter_declaration":
			if n := cDeclaratorName(c.ChildByFieldName("declarator")); n != "" {
				params = append(params, n)
			}
		}
	}
	return params
}

// cppIsPureVirtual reports whether a method declaration is pure virtual (`= 0`):
// a field_declaration / declaration with a "default_value" of literal 0.
func cppIsPureVirtual(node *tsparse.Node) bool {
	v := node.ChildByFieldName("default_value")
	if v == nil {
		return false
	}
	return strings.TrimSpace(v.Text()) == "0"
}

// cppIsStaticConst reports whether a field_declaration is a static const /
// constexpr data member (→ KindConstant).
func cppIsStaticConst(fd *tsparse.Node) bool {
	static, constish := false, false
	for i := 0; i < fd.NamedChildCount(); i++ {
		c := fd.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "storage_class_specifier":
			t := strings.TrimSpace(c.Text())
			if t == "static" {
				static = true
			}
			if t == "constexpr" {
				constish = true
			}
		case "type_qualifier":
			if strings.TrimSpace(c.Text()) == "const" {
				constish = true
			}
		}
	}
	return static && constish
}

// cppClassSignature renders a class header line ("class X : public Base").
func cppClassSignature(node *tsparse.Node) string {
	var b strings.Builder
	if node.Kind() == "struct_specifier" {
		b.WriteString("struct ")
	} else {
		b.WriteString("class ")
	}
	if n := node.ChildByFieldName("name"); n != nil {
		b.WriteString(n.Text())
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "base_class_clause" {
			b.WriteString(" ")
			b.WriteString(strings.TrimSpace(c.Text()))
		}
	}
	return strings.TrimSpace(b.String())
}

// cppMethodSignature renders a method declaration/definition signature (no body).
func cppMethodSignature(node *tsparse.Node) string {
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

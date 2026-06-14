package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkCSharp walks a parsed C# (tree-sitter `c-sharp`) file root and extracts
// symbols. Called by ExtractFile after the file node is emitted.
//
// Node type reference (tree-sitter-c-sharp), verified by probe:
//
//	namespace_declaration / file_scoped_namespace_declaration (fields: name, body)
//	class_declaration / struct_declaration / record_declaration /
//	  interface_declaration / enum_declaration (fields: name, body; base_list child)
//	method_declaration / constructor_declaration / operator_declaration
//	  (fields: name, returns, parameters, body)
//	property_declaration / indexer_declaration (fields: type, name)
//	field_declaration → variable_declaration (field type) + variable_declarator (field name)
//	enum_member_declaration (field name)
//	using_directive (alias child + name)
//	invocation_expression (fields: function, arguments)
//	object_creation_expression (fields: type, arguments)
//	member_access_expression (fields: expression, name)
//	attribute_list → attribute (field name)
//	base_list (named children: base type names)
//	modifier (public/private/protected/internal/static/abstract/async/const/override)
func (e *extractor) walkCSharp(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeCSharp(child)
		}
	}
}

// visitNodeCSharp dispatches a single C# node. Unknown kinds descend into their
// named children so calls/declarations nested inside other constructs are seen.
func (e *extractor) visitNodeCSharp(node *tsparse.Node) {
	switch node.Kind() {
	case "namespace_declaration", "file_scoped_namespace_declaration":
		e.extractCSNamespace(node)
	case "class_declaration", "record_declaration":
		e.extractCSType(node, model.KindClass)
	case "struct_declaration":
		e.extractCSType(node, model.KindStruct)
	case "interface_declaration":
		e.extractCSType(node, model.KindInterface)
	case "enum_declaration":
		e.extractCSEnum(node)
	case "method_declaration", "constructor_declaration", "operator_declaration":
		e.extractCSMethod(node)
	case "property_declaration", "indexer_declaration":
		e.extractCSProperty(node)
	case "field_declaration", "event_field_declaration":
		e.extractCSField(node)
	case "delegate_declaration", "event_declaration":
		e.extractCSDelegate(node)
	case "using_directive":
		e.extractCSUsing(node)
	case "invocation_expression":
		e.extractCSInvocation(node)
	case "object_creation_expression":
		e.extractCSObjectCreation(node)
	default:
		e.visitCSBody(node)
	}
}

// visitCSBody descends into a node's named children without emitting a node for
// the container itself.
func (e *extractor) visitCSBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodeCSharp(child)
		}
	}
}

// extractCSNamespace handles namespace_declaration and
// file_scoped_namespace_declaration. The qualified dotted name becomes a single
// KindNamespace node; its members are emitted as contained children.
func (e *extractor) extractCSNamespace(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	ns := e.createNode(model.KindNamespace, name, node, nodeExtra{})
	if ns == nil {
		return
	}

	pushed := false
	if ns != nil {
		e.nodeStack = append(e.nodeStack, ns.ID)
		pushed = true
	}

	// file_scoped_namespace has no body field — its members are the following
	// top-level siblings, handled by the root walk. The block-scoped form has a
	// `body` declaration_list.
	if body := node.ChildByFieldName("body"); body != nil {
		for i := 0; i < body.NamedChildCount(); i++ {
			if child := body.NamedChild(i); child != nil {
				e.visitNodeCSharp(child)
			}
		}
		if pushed {
			e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
		}
		return
	}

	// file_scoped: leave the namespace on the stack for the remaining siblings.
	// (Do not pop — subsequent top-level declarations belong to it.)
}

// extractCSType handles class/struct/record/interface declarations.
func (e *extractor) extractCSType(node *tsparse.Node, kind model.NodeKind) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()

	vis := csVisibility(node)
	decorators := csAttributes(node)
	tn := e.createNode(kind, name, node, nodeExtra{
		signature:      csTypeSignature(node, name),
		visibility:     vis,
		isExported:     csIsPublic(vis),
		isStatic:       csHasModifier(node, "static"),
		isAbstract:     csHasModifier(node, "abstract"),
		decorators:     decorators,
		typeParameters: csTypeParameters(node),
	})
	if tn == nil {
		return
	}

	e.emitCSDecorates(tn.ID, decorators, node)
	e.emitCSBaseList(tn.ID, node)

	if body := node.ChildByFieldName("body"); body != nil {
		e.nodeStack = append(e.nodeStack, tn.ID)
		for i := 0; i < body.NamedChildCount(); i++ {
			if child := body.NamedChild(i); child != nil {
				e.visitNodeCSharp(child)
			}
		}
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}

	// record positional parameters (e.g. `record Point(int X, int Y)`) carry
	// type references but no member nodes this pass — descend for type refs only
	// via parameters is out of scope; skip to keep minimal.
}

// extractCSEnum handles enum_declaration: the enum node plus an EnumMember per
// member in the enum_member_declaration_list.
func (e *extractor) extractCSEnum(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	vis := csVisibility(node)
	en := e.createNode(model.KindEnum, name, node, nodeExtra{
		visibility: vis,
		isExported: csIsPublic(vis),
		decorators: csAttributes(node),
	})
	if en == nil {
		return
	}
	e.emitCSDecorates(en.ID, en.Decorators, node)

	body := node.ChildByFieldName("body")
	if body == nil {
		// fall back to scanning children for the member list.
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); c != nil && c.Kind() == "enum_member_declaration_list" {
				body = c
				break
			}
		}
	}
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, en.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		if m == nil || m.Kind() != "enum_member_declaration" {
			continue
		}
		mn := m.ChildByFieldName("name")
		if mn == nil {
			// name may be the first identifier child.
			for j := 0; j < m.NamedChildCount(); j++ {
				if c := m.NamedChild(j); c != nil && c.Kind() == "identifier" {
					mn = c
					break
				}
			}
		}
		if mn != nil {
			e.createNode(model.KindEnumMember, mn.Text(), m, nodeExtra{})
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractCSMethod handles method/constructor/operator declarations.
func (e *extractor) extractCSMethod(node *tsparse.Node) {
	var name string
	if nn := node.ChildByFieldName("name"); nn != nil {
		name = nn.Text()
	} else {
		// constructor_declaration uses the type identifier as its name.
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); c != nil && c.Kind() == "identifier" {
				name = c.Text()
				break
			}
		}
	}
	if name == "" {
		return
	}

	vis := csVisibility(node)
	decorators := csAttributes(node)
	var retType string
	if rt := node.ChildByFieldName("returns"); rt != nil {
		retType = rt.Text()
	} else if rt := node.ChildByFieldName("type"); rt != nil {
		retType = rt.Text()
	}

	fn := e.createNode(model.KindMethod, name, node, nodeExtra{
		signature:      csMethodSignature(node, name),
		visibility:     vis,
		isExported:     csIsPublic(vis),
		isAsync:        csHasModifier(node, "async"),
		isStatic:       csHasModifier(node, "static"),
		isAbstract:     csHasModifier(node, "abstract"),
		returnType:     retType,
		decorators:     decorators,
		typeParameters: csTypeParameters(node),
	})
	if fn == nil {
		return
	}
	e.emitCSDecorates(fn.ID, decorators, node)

	// Descend into the body (block) or arrow expression for calls / new.
	e.nodeStack = append(e.nodeStack, fn.ID)
	if body := node.ChildByFieldName("body"); body != nil {
		e.visitCSBody(body)
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "arrow_expression_clause" {
			e.visitCSBody(c)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractCSProperty handles property_declaration and indexer_declaration.
func (e *extractor) extractCSProperty(node *tsparse.Node) {
	var name string
	if nn := node.ChildByFieldName("name"); nn != nil {
		name = nn.Text()
	}
	if name == "" {
		// indexer uses `this`; fall back to last identifier before accessor_list.
		for i := node.NamedChildCount() - 1; i >= 0; i-- {
			if c := node.NamedChild(i); c != nil && c.Kind() == "identifier" {
				name = c.Text()
				break
			}
		}
	}
	if name == "" {
		return
	}
	vis := csVisibility(node)
	var typ string
	if t := node.ChildByFieldName("type"); t != nil {
		typ = t.Text()
	}
	e.createNode(model.KindProperty, name, node, nodeExtra{
		signature:  strings.TrimSpace(typ + " " + name),
		visibility: vis,
		isExported: csIsPublic(vis),
		isStatic:   csHasModifier(node, "static"),
		isAbstract: csHasModifier(node, "abstract"),
		returnType: typ,
		decorators: csAttributes(node),
	})
}

// extractCSField handles field_declaration: each variable_declarator becomes a
// KindField (or KindConstant when the declaration is const).
func (e *extractor) extractCSField(node *tsparse.Node) {
	kind := model.KindField
	if csHasModifier(node, "const") {
		kind = model.KindConstant
	}
	vis := csVisibility(node)
	var typ string
	var decl *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "variable_declaration" {
			decl = c
			break
		}
	}
	if decl == nil {
		return
	}
	if t := decl.ChildByFieldName("type"); t != nil {
		typ = t.Text()
	}
	for i := 0; i < decl.NamedChildCount(); i++ {
		d := decl.NamedChild(i)
		if d == nil || d.Kind() != "variable_declarator" {
			continue
		}
		var fname string
		if nn := d.ChildByFieldName("name"); nn != nil {
			fname = nn.Text()
		} else {
			for j := 0; j < d.NamedChildCount(); j++ {
				if c := d.NamedChild(j); c != nil && c.Kind() == "identifier" {
					fname = c.Text()
					break
				}
			}
		}
		if fname == "" {
			continue
		}
		e.createNode(kind, fname, d, nodeExtra{
			signature:  strings.TrimSpace(typ + " " + fname),
			visibility: vis,
			isExported: csIsPublic(vis),
			isStatic:   csHasModifier(node, "static"),
			returnType: typ,
		})
	}
}

// extractCSDelegate handles delegate_declaration and event_declaration, both
// mapped to KindField (closest existing kind — see the C# extraction design).
func (e *extractor) extractCSDelegate(node *tsparse.Node) {
	var name string
	if nn := node.ChildByFieldName("name"); nn != nil {
		name = nn.Text()
	}
	if name == "" {
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); c != nil && c.Kind() == "identifier" {
				name = c.Text()
			}
		}
	}
	if name == "" {
		return
	}
	vis := csVisibility(node)
	e.createNode(model.KindField, name, node, nodeExtra{
		visibility: vis,
		isExported: csIsPublic(vis),
		isStatic:   csHasModifier(node, "static"),
	})
}

// extractCSUsing handles a using_directive: emits a KindImport node named after
// the imported namespace/type (or alias) plus an EdgeImports ref from the
// current scope.
func (e *extractor) extractCSUsing(node *tsparse.Node) {
	sig := strings.TrimSpace(node.Text())

	// using Alias = A.B.C;  → identifier(alias) "=" qualified_name/identifier.
	// using A.B.C;          → qualified_name / identifier.
	// The alias form is detected by an "=" token among the direct children.
	hasEq := false
	for i := 0; i < node.ChildCount(); i++ {
		if c := node.Child(i); c != nil && c.Text() == "=" {
			hasEq = true
			break
		}
	}
	var aliasNode, nameNode *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "identifier", "qualified_name":
			if hasEq && aliasNode == nil {
				aliasNode = c
			} else {
				nameNode = c
			}
		}
	}
	if nameNode == nil {
		return
	}

	// The node name is the alias if present, else the last segment of the import
	// path (so `using A.B.C;` exposes `C` as the imported simple name, matching
	// how a using-imported type is referenced unqualified in code).
	full := nameNode.Text()
	importName := full
	if aliasNode != nil {
		importName = aliasNode.Text()
	} else if idx := strings.LastIndex(full, "."); idx >= 0 {
		importName = full[idx+1:]
	}
	if importName == "" {
		return
	}

	e.createNode(model.KindImport, importName, node, nodeExtra{signature: sig})

	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}
	if parentID != "" {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    parentID,
			ReferenceName: importName,
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// extractCSInvocation handles invocation_expression: emits an EdgeCalls ref from
// the top of the node stack, then descends into arguments for nested calls.
func (e *extractor) extractCSInvocation(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		if fn := node.ChildByFieldName("function"); fn != nil {
			if name := csCalleeName(fn); name != "" {
				e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
					FromNodeID:    callerID,
					ReferenceName: name,
					ReferenceKind: model.EdgeCalls,
					Line:          int(node.StartPoint().Row) + 1,
					Column:        int(node.StartPoint().Column),
				})
			}
		}
	}
	if fn := node.ChildByFieldName("function"); fn != nil {
		// A `new Foo().Bar()` callee contains an object_creation_expression —
		// descend so the instantiation is recorded too.
		e.visitCSBody(fn)
	}
	if args := node.ChildByFieldName("arguments"); args != nil {
		e.visitCSBody(args)
	}
}

// extractCSObjectCreation handles object_creation_expression `new T(...)`: emits
// an EdgeInstantiates ref to T from the top of the node stack.
func (e *extractor) extractCSObjectCreation(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		if t := node.ChildByFieldName("type"); t != nil {
			if name := csTypeName(t); name != "" {
				e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
					FromNodeID:    callerID,
					ReferenceName: name,
					ReferenceKind: model.EdgeInstantiates,
					Line:          int(node.StartPoint().Row) + 1,
					Column:        int(node.StartPoint().Column),
				})
			}
		}
	}
	if args := node.ChildByFieldName("arguments"); args != nil {
		e.visitCSBody(args)
	}
}

// emitCSBaseList emits an EdgeExtends ref per base type in a type's base_list.
// The resolver reclassifies a base that resolves to an interface as
// EdgeImplements (see resolveCSharpRef).
func (e *extractor) emitCSBaseList(fromID string, node *tsparse.Node) {
	var bl *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "base_list" {
			bl = c
			break
		}
	}
	if bl == nil {
		return
	}
	for i := 0; i < bl.NamedChildCount(); i++ {
		b := bl.NamedChild(i)
		if b == nil {
			continue
		}
		name := csTypeName(b)
		if name == "" {
			continue
		}
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: name,
			ReferenceKind: model.EdgeExtends,
			Line:          int(b.StartPoint().Row) + 1,
			Column:        int(b.StartPoint().Column),
		})
	}
}

// emitCSDecorates emits an EdgeDecorates ref per attribute head name.
func (e *extractor) emitCSDecorates(fromID string, decorators []string, node *tsparse.Node) {
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

// csCalleeName resolves an invocation's function node to a callee name. A
// member_access_expression `recv.Method` returns "recv.Method"; `this.M`/`base.M`
// reduce to "M"; a bare identifier returns its text.
func csCalleeName(fn *tsparse.Node) string {
	switch fn.Kind() {
	case "identifier":
		return fn.Text()
	case "member_access_expression":
		nameNode := fn.ChildByFieldName("name")
		if nameNode == nil {
			return ""
		}
		method := nameNode.Text()
		recv := fn.ChildByFieldName("expression")
		if recv != nil && recv.Kind() == "identifier" {
			r := recv.Text()
			if r == "this" || r == "base" {
				return method
			}
			return r + "." + method
		}
		// receiver is `this`/`base` keyword (anonymous) or a complex expression.
		return method
	case "generic_name":
		if id := fn.ChildByFieldName("name"); id != nil {
			return id.Text()
		}
		for i := 0; i < fn.NamedChildCount(); i++ {
			if c := fn.NamedChild(i); c != nil && c.Kind() == "identifier" {
				return c.Text()
			}
		}
		return ""
	default:
		return ""
	}
}

// csTypeName reduces a type node (identifier / qualified_name / generic_name) to
// its simple (last-segment, un-generic) name.
func csTypeName(node *tsparse.Node) string {
	switch node.Kind() {
	case "identifier":
		return node.Text()
	case "qualified_name":
		// last identifier segment.
		t := node.Text()
		if idx := strings.LastIndex(t, "."); idx >= 0 {
			return t[idx+1:]
		}
		return t
	case "generic_name":
		if id := node.ChildByFieldName("name"); id != nil {
			return id.Text()
		}
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); c != nil && c.Kind() == "identifier" {
				return c.Text()
			}
		}
	}
	t := node.Text()
	if idx := strings.IndexAny(t, "<"); idx >= 0 {
		t = t[:idx]
	}
	if idx := strings.LastIndex(t, "."); idx >= 0 {
		t = t[idx+1:]
	}
	return strings.TrimSpace(t)
}

// csAttributes returns the head names of all attributes on a declaration
// (`[Serializable]`, `[Route("…")]` → "Route").
func csAttributes(node *tsparse.Node) []string {
	var out []string
	for i := 0; i < node.NamedChildCount(); i++ {
		al := node.NamedChild(i)
		if al == nil || al.Kind() != "attribute_list" {
			continue
		}
		for j := 0; j < al.NamedChildCount(); j++ {
			a := al.NamedChild(j)
			if a == nil || a.Kind() != "attribute" {
				continue
			}
			if nn := a.ChildByFieldName("name"); nn != nil {
				out = append(out, csTypeName(nn))
			}
		}
	}
	return out
}

// csVisibility returns the C# access modifier (public/private/protected/internal)
// or nil when none is present.
func csVisibility(node *tsparse.Node) *string {
	for _, m := range []string{"public", "private", "protected", "internal"} {
		if csHasModifier(node, m) {
			v := m
			return &v
		}
	}
	return nil
}

func csIsPublic(vis *string) bool {
	return vis != nil && *vis == "public"
}

// csHasModifier reports whether the declaration carries the given modifier token.
func csHasModifier(node *tsparse.Node, mod string) bool {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "modifier" && c.Text() == mod {
			return true
		}
	}
	return false
}

// csTypeParameters returns the generic type-parameter names of a declaration
// (e.g. `class Box<T, U>` → ["T", "U"]).
func csTypeParameters(node *tsparse.Node) []string {
	var tpl *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "type_parameter_list" {
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
		if p == nil || p.Kind() != "type_parameter" {
			continue
		}
		if nn := p.ChildByFieldName("name"); nn != nil {
			out = append(out, nn.Text())
		} else {
			out = append(out, strings.TrimSpace(p.Text()))
		}
	}
	return out
}

// csTypeSignature renders a one-line header for a type declaration: modifiers +
// kind keyword + name (+ base list).
func csTypeSignature(node *tsparse.Node, name string) string {
	keyword := "class"
	switch node.Kind() {
	case "struct_declaration":
		keyword = "struct"
	case "interface_declaration":
		keyword = "interface"
	case "record_declaration":
		keyword = "record"
	case "enum_declaration":
		keyword = "enum"
	}
	sig := keyword + " " + name
	if vis := csVisibility(node); vis != nil {
		sig = *vis + " " + sig
	}
	return sig
}

// csMethodSignature renders `ret Name(params)` for a method declaration.
func csMethodSignature(node *tsparse.Node, name string) string {
	var ret string
	if rt := node.ChildByFieldName("returns"); rt != nil {
		ret = rt.Text() + " "
	} else if rt := node.ChildByFieldName("type"); rt != nil {
		ret = rt.Text() + " "
	}
	sig := ret + name
	if params := node.ChildByFieldName("parameters"); params != nil {
		sig += params.Text()
	}
	return strings.TrimSpace(sig)
}

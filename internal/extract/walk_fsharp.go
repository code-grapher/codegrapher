package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkFSharp walks a parsed F# (tree-sitter `fsharp`) file root and extracts
// symbols. F# is a .NET functional language with namespaces, modules, records,
// discriminated unions, classes and interfaces.
//
// Node type reference (tree-sitter-fsharp), verified by probe — see
// docs/superpowers/specs/2026-06-14-fsharp-extraction-design.md:
//
//	file → namespace* | named_module | <decls>
//	namespace → "namespace", long_identifier, <decls...>
//	named_module / module_defn → "module", (long_)identifier, <decls...>
//	import_decl → "open", long_identifier
//	type_definition → record_type_defn | union_type_defn | anon_type_defn
//	record_type_defn → type_name, record_fields → record_field(identifier)
//	union_type_defn → type_name, union_type_cases → union_type_case(identifier)
//	anon_type_defn → type_name, primary_constr_args?, class_inherits_decl?,
//	    type_extension_elements(member_defn | interface_implementation)
//	member_defn → ("abstract")? member_signature | method_or_prop_defn
//	method_or_prop_defn → property_or_ident("recv.Name"|"Name"), (unit)? "=" body
//	interface_implementation → "interface", simple_type, member_defn*
//	class_inherits_decl → "inherit", simple_type
//	declaration_expression / function_or_value_defn → "let",
//	    value_declaration_left(identifier_pattern (+ identifier_pattern args)), body
//	application_expression → long_identifier_or_op(callee), args
//	dot_expression → application_expression "." long_identifier_or_op
//	prefixed_expression → "new", application_expression
func (e *extractor) walkFSharp(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeFSharp(child)
		}
	}
}

// visitNodeFSharp dispatches a single node. Unknown kinds descend into their
// named children so calls/declarations nested inside statements are still seen
// even when the grammar produced an ERROR node above them.
func (e *extractor) visitNodeFSharp(node *tsparse.Node) {
	switch node.Kind() {
	case "namespace":
		e.extractFSharpNamespace(node)
	case "named_module", "module_defn":
		e.extractFSharpModule(node)
	case "import_decl":
		e.extractFSharpImport(node)
	case "type_definition":
		e.extractFSharpType(node)
	case "declaration_expression", "function_or_value_defn":
		e.extractFSharpLet(node)
	case "application_expression":
		e.extractFSharpApplication(node)
	case "prefixed_expression":
		e.extractFSharpPrefixed(node)
	case "dot_expression":
		e.extractFSharpDot(node)
	default:
		e.visitFSharpBody(node)
	}
}

// visitFSharpBody descends into a node's named children without emitting a node
// for the container itself.
func (e *extractor) visitFSharpBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodeFSharp(child)
		}
	}
}

// extractFSharpNamespace emits a KindNamespace node and walks its declarations.
func (e *extractor) extractFSharpNamespace(node *tsparse.Node) {
	id := fsNamedChildOfKind(node, "long_identifier")
	if id == nil || strings.TrimSpace(id.Text()) == "" {
		// Anonymous/global namespace: walk children without pushing.
		e.visitFSharpBody(node)
		return
	}
	name := strings.TrimSpace(id.Text())
	ns := e.createNode(model.KindNamespace, name, node, nodeExtra{
		signature:  "namespace " + name,
		isExported: true,
	})
	if ns == nil {
		e.visitFSharpBody(node)
		return
	}
	e.nodeStack = append(e.nodeStack, ns.ID)
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil || c.Kind() == "long_identifier" {
			continue
		}
		e.visitNodeFSharp(c)
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractFSharpModule emits a KindModule node and walks its members.
func (e *extractor) extractFSharpModule(node *tsparse.Node) {
	var name string
	if id := fsNamedChildOfKind(node, "identifier"); id != nil {
		name = strings.TrimSpace(id.Text())
	}
	if name == "" {
		if id := fsNamedChildOfKind(node, "long_identifier"); id != nil {
			name = fsLastSeg(strings.TrimSpace(id.Text()))
		}
	}
	if name == "" {
		e.visitFSharpBody(node)
		return
	}
	mod := e.createNode(model.KindModule, name, node, nodeExtra{
		signature:  "module " + name,
		isExported: true,
	})
	if mod == nil {
		e.visitFSharpBody(node)
		return
	}
	e.nodeStack = append(e.nodeStack, mod.ID)
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "identifier", "long_identifier":
			continue
		}
		e.visitNodeFSharp(c)
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractFSharpImport emits a KindImport node and an EdgeImports reference for
// `open A.B`.
func (e *extractor) extractFSharpImport(node *tsparse.Node) {
	id := fsNamedChildOfKind(node, "long_identifier")
	if id == nil {
		return
	}
	full := strings.TrimSpace(id.Text())
	if full == "" {
		return
	}
	local := fsLastSeg(full)
	e.createNode(model.KindImport, local, node, nodeExtra{signature: "open " + full})

	if len(e.nodeStack) > 0 {
		parentID := e.nodeStack[len(e.nodeStack)-1]
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    parentID,
			ReferenceName: full,
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// extractFSharpType handles type_definition, dispatching on the inner defn kind.
func (e *extractor) extractFSharpType(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "record_type_defn":
			e.extractFSharpRecord(c)
		case "union_type_defn":
			e.extractFSharpUnion(c)
		case "anon_type_defn":
			e.extractFSharpAnonType(c)
		}
	}
}

// extractFSharpRecord handles `type T = { a: int }` → KindStruct + KindField.
func (e *extractor) extractFSharpRecord(node *tsparse.Node) {
	name := fsTypeName(node)
	if name == "" {
		return
	}
	cn := e.createNode(model.KindStruct, name, node, nodeExtra{
		signature:  "type " + name,
		isExported: true,
	})
	if cn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, cn.ID)
	if rf := fsNamedChildOfKind(node, "record_fields"); rf != nil {
		for i := 0; i < rf.NamedChildCount(); i++ {
			f := rf.NamedChild(i)
			if f == nil || f.Kind() != "record_field" {
				continue
			}
			fname := ""
			if id := fsNamedChildOfKind(f, "identifier"); id != nil {
				fname = id.Text()
			}
			if fname == "" {
				continue
			}
			var typeName string
			if t := fsNamedChildOfKind(f, "simple_type"); t != nil {
				typeName = fsSimpleTypeName(t.Text())
			}
			e.createNode(model.KindField, fname, f, nodeExtra{
				signature:  strings.TrimSpace(f.Text()),
				returnType: typeName,
			})
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractFSharpUnion handles a discriminated union `type T = A | B of int` →
// KindEnum + KindEnumMember.
func (e *extractor) extractFSharpUnion(node *tsparse.Node) {
	name := fsTypeName(node)
	if name == "" {
		return
	}
	cn := e.createNode(model.KindEnum, name, node, nodeExtra{
		signature:  "type " + name,
		isExported: true,
	})
	if cn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, cn.ID)
	if uc := fsNamedChildOfKind(node, "union_type_cases"); uc != nil {
		for i := 0; i < uc.NamedChildCount(); i++ {
			c := uc.NamedChild(i)
			if c == nil || c.Kind() != "union_type_case" {
				continue
			}
			if id := fsNamedChildOfKind(c, "identifier"); id != nil && id.Text() != "" {
				e.createNode(model.KindEnumMember, id.Text(), c, nodeExtra{})
			}
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractFSharpAnonType handles `anon_type_defn`, used for both classes and
// abstract (interface) types. A type whose members are ALL abstract
// member_signatures (and which has no constructor) is a KindInterface;
// otherwise it is a KindClass.
func (e *extractor) extractFSharpAnonType(node *tsparse.Node) {
	name := fsTypeName(node)
	if name == "" {
		return
	}
	hasCtor := fsNamedChildOfKind(node, "primary_constr_args") != nil
	isInterface := !hasCtor && fsAllAbstractMembers(node)

	kind := model.KindClass
	if isInterface {
		kind = model.KindInterface
	}
	cn := e.createNode(kind, name, node, nodeExtra{
		signature:  "type " + name,
		isExported: true,
		isAbstract: isInterface,
	})
	if cn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, cn.ID)

	// Constructor args become fields (like Scala class params).
	if pc := fsNamedChildOfKind(node, "primary_constr_args"); pc != nil {
		e.extractFSharpCtorArgs(pc)
	}

	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "class_inherits_decl":
			e.extractFSharpInherit(cn.ID, c)
		case "type_extension_elements":
			e.walkFSharpTypeBody(c)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractFSharpCtorArgs emits a KindField per typed constructor parameter.
func (e *extractor) extractFSharpCtorArgs(pc *tsparse.Node) {
	for i := 0; i < pc.NamedChildCount(); i++ {
		p := pc.NamedChild(i)
		if p == nil || p.Kind() != "typed_pattern" {
			continue
		}
		var fname string
		if ip := fsNamedChildOfKind(p, "identifier_pattern"); ip != nil {
			fname = fsLastSeg(strings.TrimSpace(ip.Text()))
		}
		if fname == "" {
			continue
		}
		var typeName string
		if t := fsNamedChildOfKind(p, "simple_type"); t != nil {
			typeName = fsSimpleTypeName(t.Text())
		}
		e.createNode(model.KindField, fname, p, nodeExtra{
			signature:  strings.TrimSpace(p.Text()),
			returnType: typeName,
		})
	}
}

// walkFSharpTypeBody walks a type_extension_elements body: member definitions
// and interface implementations.
func (e *extractor) walkFSharpTypeBody(body *tsparse.Node) {
	for i := 0; i < body.NamedChildCount(); i++ {
		c := body.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "member_defn":
			e.extractFSharpMember(c)
		case "interface_implementation":
			e.extractFSharpInterfaceImpl(c)
		default:
			e.visitNodeFSharp(c)
		}
	}
}

// extractFSharpMember handles a member_defn: an abstract member_signature, or a
// concrete method_or_prop_defn. A definition with `()` args is a KindMethod;
// without args it is a KindProperty.
func (e *extractor) extractFSharpMember(node *tsparse.Node) {
	// Abstract member: `abstract Name: T` → member_signature.
	if ms := fsNamedChildOfKind(node, "member_signature"); ms != nil {
		name := ""
		if id := fsNamedChildOfKind(ms, "identifier"); id != nil {
			name = id.Text()
		}
		if name == "" {
			return
		}
		// abstract method (has arguments_spec) vs property.
		kind := model.KindProperty
		if fsSignatureHasArgs(ms) {
			kind = model.KindMethod
		}
		e.createNode(kind, name, node, nodeExtra{
			signature:  strings.TrimSpace(node.Text()),
			isExported: true,
			isAbstract: true,
		})
		return
	}

	mp := fsNamedChildOfKind(node, "method_or_prop_defn")
	if mp == nil {
		return
	}
	poi := fsNamedChildOfKind(mp, "property_or_ident")
	if poi == nil {
		return
	}
	name := fsMemberName(poi)
	if name == "" {
		return
	}
	// A method has a `unit`/`const` args child before "="; a property does not.
	isMethod := fsNamedChildOfKind(mp, "const") != nil
	kind := model.KindProperty
	if isMethod {
		kind = model.KindMethod
	}
	mn := e.createNode(kind, name, node, nodeExtra{
		signature:  fsFirstLine(node.Text()),
		isExported: true,
	})
	if mn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, mn.ID)
	// Walk the body (after "=") for calls/instantiations.
	for i := 0; i < mp.NamedChildCount(); i++ {
		c := mp.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "property_or_ident", "const":
			continue
		}
		e.visitNodeFSharp(c)
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractFSharpInterfaceImpl handles `interface I with …` → EdgeImplements plus
// the member definitions inside.
func (e *extractor) extractFSharpInterfaceImpl(node *tsparse.Node) {
	if t := fsNamedChildOfKind(node, "simple_type"); t != nil {
		name := fsSimpleTypeName(t.Text())
		if name != "" && len(e.nodeStack) > 0 {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    e.nodeStack[len(e.nodeStack)-1],
				ReferenceName: name,
				ReferenceKind: model.EdgeImplements,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
		}
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "member_defn" {
			e.extractFSharpMember(c)
		}
	}
}

// extractFSharpInherit handles `inherit Base()` → EdgeExtends.
func (e *extractor) extractFSharpInherit(fromID string, node *tsparse.Node) {
	t := fsNamedChildOfKind(node, "simple_type")
	if t == nil {
		return
	}
	name := fsSimpleTypeName(t.Text())
	if name == "" {
		return
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    fromID,
		ReferenceName: name,
		ReferenceKind: model.EdgeExtends,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// extractFSharpLet handles a let-binding (declaration_expression /
// function_or_value_defn). A binding with parameters is a function (KindFunction
// at module scope, KindMethod inside a type); a value binding is a
// KindConstant (UPPER/simple) or KindVariable.
func (e *extractor) extractFSharpLet(node *tsparse.Node) {
	fv := node
	if node.Kind() == "declaration_expression" {
		fv = fsNamedChildOfKind(node, "function_or_value_defn")
		if fv == nil {
			e.visitFSharpBody(node)
			return
		}
	}
	left := fsNamedChildOfKind(fv, "value_declaration_left")
	if left == nil {
		return
	}
	ip := fsNamedChildOfKind(left, "identifier_pattern")
	if ip == nil {
		return
	}
	name := fsLetName(ip)
	if name == "" {
		return
	}
	isFunc := fsLetIsFunction(left, ip)

	var kind model.NodeKind
	switch {
	case isFunc && e.fsInsideType():
		kind = model.KindMethod
	case isFunc:
		kind = model.KindFunction
	case e.isInsideFunctionLike():
		kind = model.KindVariable
	case e.fsInsideType():
		kind = model.KindField
	case fsIsConstantName(name):
		kind = model.KindConstant
	default:
		kind = model.KindVariable
	}

	vn := e.createNode(kind, name, node, nodeExtra{
		signature:  fsFirstLine(fv.Text()),
		isExported: true,
	})
	if vn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, vn.ID)
	// Walk the body (after "=") for calls / instantiations.
	for i := 0; i < fv.NamedChildCount(); i++ {
		c := fv.NamedChild(i)
		if c == nil || c.Kind() == "value_declaration_left" {
			continue
		}
		e.visitNodeFSharp(c)
	}
	// A declaration_expression carries trailing statements (e.g. the rest of a
	// function body after a nested `let`) as siblings of function_or_value_defn.
	if node.Kind() == "declaration_expression" {
		for i := 0; i < node.NamedChildCount(); i++ {
			c := node.NamedChild(i)
			if c == nil || c.Kind() == "function_or_value_defn" {
				continue
			}
			e.visitNodeFSharp(c)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractFSharpApplication handles `f x` / `T(...)`: emits an EdgeCalls (lower-
// case callee) or EdgeInstantiates (capitalized callee, i.e. a constructor),
// then walks the arguments.
func (e *extractor) extractFSharpApplication(node *tsparse.Node) {
	callee := fsApplicationCallee(node)
	if callee != "" && len(e.nodeStack) > 0 {
		kind := model.EdgeCalls
		if fsIsConstructorName(callee) {
			kind = model.EdgeInstantiates
		}
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    e.nodeStack[len(e.nodeStack)-1],
			ReferenceName: fsStripReceiverSelf(callee),
			ReferenceKind: kind,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
	// Walk children for nested calls. When the callee (child 0) is itself an
	// application_expression (curried application `f a b`), descend into it so
	// the real callee is seen; a plain long_identifier_or_op callee is skipped.
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if i == 0 {
			switch c.Kind() {
			case "long_identifier_or_op", "long_identifier", "identifier":
				continue // already recorded as the callee
			}
		}
		e.visitNodeFSharp(c)
	}
}

// extractFSharpPrefixed handles `new T(...)` (prefixed_expression): the inner
// application's callee is forced to an EdgeInstantiates.
func (e *extractor) extractFSharpPrefixed(node *tsparse.Node) {
	app := fsNamedChildOfKind(node, "application_expression")
	if app == nil {
		e.visitFSharpBody(node)
		return
	}
	callee := fsApplicationCallee(app)
	if callee != "" && len(e.nodeStack) > 0 {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    e.nodeStack[len(e.nodeStack)-1],
			ReferenceName: fsSimpleTypeName(callee),
			ReferenceKind: model.EdgeInstantiates,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
	for i := 1; i < app.NamedChildCount(); i++ {
		if c := app.NamedChild(i); c != nil {
			e.visitNodeFSharp(c)
		}
	}
}

// extractFSharpDot handles `recv.Member` (possibly `app().Member`): walks the
// receiver application for its own call/instantiation.
func (e *extractor) extractFSharpDot(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == "long_identifier_or_op" || c.Kind() == "long_identifier" || c.Kind() == "identifier" {
			continue // the member name segment
		}
		e.visitNodeFSharp(c)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// F# helpers
// ──────────────────────────────────────────────────────────────────────────────

// fsInsideType reports whether the top of the node stack is a type-like kind
// (class/struct/interface/enum) — but NOT a module/namespace. A `let` inside a
// module is a module-scope function, not a method; only a `let`/member inside a
// type is a method.
func (e *extractor) fsInsideType() bool {
	if len(e.nodeStack) == 0 {
		return false
	}
	topID := e.nodeStack[len(e.nodeStack)-1]
	for i := len(e.nodes) - 1; i >= 0; i-- {
		if e.nodes[i].ID == topID {
			switch e.nodes[i].Kind {
			case model.KindClass, model.KindStruct, model.KindInterface, model.KindEnum:
				return true
			}
			return false
		}
	}
	return false
}

// fsTypeName returns the type's declared name from its `type_name` child.
func fsTypeName(node *tsparse.Node) string {
	tn := fsNamedChildOfKind(node, "type_name")
	if tn == nil {
		return ""
	}
	if id := fsNamedChildOfKind(tn, "identifier"); id != nil {
		return strings.TrimSpace(id.Text())
	}
	return strings.TrimSpace(tn.Text())
}

// fsAllAbstractMembers reports whether every member in the type's extension
// elements is an abstract member_signature (no concrete method_or_prop_defn).
// Used to classify an anon_type_defn as an interface.
func fsAllAbstractMembers(node *tsparse.Node) bool {
	found := false
	for i := 0; i < node.NamedChildCount(); i++ {
		te := node.NamedChild(i)
		if te == nil || te.Kind() != "type_extension_elements" {
			continue
		}
		for j := 0; j < te.NamedChildCount(); j++ {
			m := te.NamedChild(j)
			if m == nil {
				continue
			}
			if m.Kind() != "member_defn" {
				return false
			}
			if fsNamedChildOfKind(m, "member_signature") == nil {
				return false
			}
			found = true
		}
	}
	return found
}

// fsSignatureHasArgs reports whether an abstract member_signature declares
// arguments (`Name: arg -> ret`) → it is a method, not a property.
func fsSignatureHasArgs(ms *tsparse.Node) bool {
	if cs := fsNamedChildOfKind(ms, "curried_spec"); cs != nil {
		return fsNamedChildOfKind(cs, "arguments_spec") != nil
	}
	return false
}

// fsMemberName extracts the member name from a property_or_ident node
// (`this.Bark` → "Bark"; `_.Name` → "Name"; "Foo" → "Foo").
func fsMemberName(poi *tsparse.Node) string {
	var ids []string
	for i := 0; i < poi.NamedChildCount(); i++ {
		c := poi.NamedChild(i)
		if c != nil && c.Kind() == "identifier" {
			ids = append(ids, c.Text())
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return ids[len(ids)-1]
}

// fsLetName returns a let-binding's name from its identifier_pattern's leading
// long_identifier_or_op.
func fsLetName(ip *tsparse.Node) string {
	if op := fsNamedChildOfKind(ip, "long_identifier_or_op"); op != nil {
		return fsLastSeg(strings.TrimSpace(op.Text()))
	}
	return ""
}

// fsLetIsFunction reports whether a let-binding declares parameters (making it a
// function) rather than a plain value. Parameters appear as extra
// identifier_pattern / const(unit) children of the value_declaration_left after
// the name, or as nested identifier_patterns inside the leading pattern.
func fsLetIsFunction(left, ip *tsparse.Node) bool {
	// Extra identifier_pattern children inside the leading identifier_pattern
	// are the parameters (e.g. `area w h`).
	for i := 0; i < ip.NamedChildCount(); i++ {
		c := ip.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "identifier_pattern":
			return true
		case "const":
			// `()` unit param → a parameterless function (`let run () = …`).
			if fsNamedChildOfKind(c, "unit") != nil {
				return true
			}
		}
	}
	// Some shapes place params as siblings of the identifier_pattern: a second
	// identifier_pattern (beyond the leading one) means parameters.
	count := 0
	for i := 0; i < left.NamedChildCount(); i++ {
		c := left.NamedChild(i)
		if c != nil && c.Kind() == "identifier_pattern" {
			count++
		}
	}
	return count > 1
}

// fsApplicationCallee returns the callee name of an application_expression: its
// first named child, which is a long_identifier_or_op.
func fsApplicationCallee(node *tsparse.Node) string {
	c := node.NamedChild(0)
	if c == nil {
		return ""
	}
	switch c.Kind() {
	case "long_identifier_or_op", "long_identifier", "identifier":
		return strings.TrimSpace(c.Text())
	}
	return ""
}

// fsStripReceiverSelf strips a leading `this.`/`self.` receiver from a dotted
// callee, leaving the member name (`this.Bark` → "Bark"). A dotted receiver that
// is not self (`d.Bark`) is preserved so the resolver can use it.
func fsStripReceiverSelf(name string) string {
	if idx := strings.IndexByte(name, '.'); idx > 0 {
		recv := name[:idx]
		if recv == "this" || recv == "self" {
			return name[idx+1:]
		}
	}
	return name
}

// fsIsConstructorName reports whether a callee name denotes a constructor: the
// final dotted segment begins with an uppercase letter (F# convention for
// types). `Circle` / `System.Object` → true; `printfn` / `d.bark` → false.
func fsIsConstructorName(name string) bool {
	seg := fsLastSeg(name)
	if seg == "" {
		return false
	}
	// A dotted call whose receiver is a value (`d.Bark`) is a method call, not a
	// constructor, even if the method is capitalized. Only a bare capitalized
	// name (or a dotted *type* path) is a constructor; we treat any dot as a
	// method call unless the whole thing is a type path. Keep it simple: only
	// bare names are constructors here.
	if strings.Contains(name, ".") {
		return false
	}
	r := rune(seg[0])
	return r >= 'A' && r <= 'Z'
}

// fsIsConstantName reports whether a value name should be a KindConstant: all
// upper-case (with digits/underscores), e.g. MAX, MAX_SIZE.
func fsIsConstantName(name string) bool {
	if name == "" {
		return false
	}
	hasUpper := false
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return hasUpper
}

// fsSimpleTypeName reduces a type expression to its simple last-segment name,
// stripping generics and namespace qualifiers (`System.Object` → "Object").
func fsSimpleTypeName(t string) string {
	t = strings.TrimSpace(t)
	if idx := strings.IndexAny(t, "<("); idx >= 0 {
		t = t[:idx]
	}
	return fsLastSeg(strings.TrimSpace(t))
}

// fsLastSeg returns the final dotted segment of a name (A.B.C → "C").
func fsLastSeg(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// fsNamedChildOfKind returns the first named child of node whose kind matches.
func fsNamedChildOfKind(node *tsparse.Node, kind string) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// fsFirstLine returns the trimmed first line of s.
func fsFirstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

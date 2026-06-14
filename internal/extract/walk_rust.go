package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkRust walks a parsed Rust (tree-sitter `rust`) source file root and
// extracts symbols. Called by ExtractFile after the file node is emitted.
//
// Rust is statically typed; resolution is the hardest of the batch (traits,
// impl blocks, modules). The key structural novelty is that `impl_item` blocks
// are NOT their own node: their methods attach to the implementing TYPE under a
// qualified `Type::method` name. For `impl Trait for Type` blocks we additionally
// emit `implements` (Type→Trait) and `overrides` (Type::method→Trait::method)
// references — Rust trait satisfaction is explicit, so these are recorded
// directly from the impl block rather than synthesized by structural matching.
//
// Node type reference (tree-sitter-rust), confirmed by AST probe:
//
//	source_file
//	mod_item (visibility_modifier, identifier name, declaration_list body)
//	use_declaration (field "argument": scoped_identifier / scoped_use_list /
//	    use_wildcard / use_as_clause / identifier)
//	trait_item (type_identifier name, declaration_list body)
//	struct_item / union_item (type_identifier name, field_declaration_list)
//	enum_item (type_identifier name, enum_variant_list)
//	enum_variant (identifier name)
//	impl_item (fields "trait"?, "type", "body" declaration_list)
//	function_item (fields "name", "parameters", "body" block; return type child)
//	function_signature_item (trait method decl, no body)
//	const_item / static_item (identifier name)
//	type_item (type_identifier name)
//	field_declaration (field_identifier name)
//	call_expression (field "function": scoped_identifier / field_expression / identifier)
//	struct_expression (field "name": type_identifier)
//	scoped_identifier (fields "path", "name")
func (e *extractor) walkRust(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeRust(child)
		}
	}
}

// visitNodeRust dispatches a single item node. Unknown kinds descend into
// children so nested items inside blocks/expressions are still seen.
func (e *extractor) visitNodeRust(node *tsparse.Node) {
	switch node.Kind() {
	case "mod_item":
		e.extractRustMod(node)
	case "use_declaration":
		e.extractRustUse(node)
	case "trait_item":
		e.extractRustTrait(node)
	case "struct_item", "union_item":
		e.extractRustStruct(node)
	case "enum_item":
		e.extractRustEnum(node)
	case "impl_item":
		e.extractRustImpl(node)
	case "function_item":
		e.extractRustFunction(node)
	case "function_signature_item":
		e.extractRustFunctionSig(node)
	case "const_item", "static_item":
		e.extractRustConst(node)
	case "type_item":
		e.extractRustTypeAlias(node)
	default:
		for i := 0; i < node.NamedChildCount(); i++ {
			if child := node.NamedChild(i); child != nil {
				e.visitNodeRust(child)
			}
		}
	}
}

// extractRustMod handles a mod_item: a module node containing nested items.
func (e *extractor) extractRustMod(node *tsparse.Node) {
	name := rustItemName(node, "identifier")
	if name == "" {
		return
	}
	mn := e.createNode(model.KindModule, name, node, nodeExtra{
		visibility: rustVisibility(node),
		isExported: rustIsPub(node),
		docstring:  e.lookupDoc(node),
	})
	if mn == nil {
		return
	}
	body := node.ChildByFieldName("body")
	if body == nil {
		body = rustChildOfKind(node, "declaration_list")
	}
	if body != nil {
		e.nodeStack = append(e.nodeStack, mn.ID)
		for i := 0; i < body.NamedChildCount(); i++ {
			if child := body.NamedChild(i); child != nil {
				e.visitNodeRust(child)
			}
		}
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractRustTrait handles a trait_item → KindInterface (traits ≈ interfaces).
// Its method signatures/defaults become KindMethod nodes contained by the trait.
func (e *extractor) extractRustTrait(node *tsparse.Node) {
	name := rustItemName(node, "type_identifier")
	if name == "" {
		return
	}
	tn := e.createNode(model.KindInterface, name, node, nodeExtra{
		visibility:     rustVisibility(node),
		isExported:     rustIsPub(node),
		docstring:      e.lookupDoc(node),
		typeParameters: rustTypeParams(node),
	})
	if tn == nil {
		return
	}
	body := rustChildOfKind(node, "declaration_list")
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
		case "function_signature_item":
			e.extractRustFunctionSig(child)
		case "function_item":
			e.extractRustFunction(child)
		default:
			e.visitNodeRust(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractRustStruct handles struct_item / union_item → KindStruct. Named fields
// become KindField nodes.
func (e *extractor) extractRustStruct(node *tsparse.Node) {
	name := rustItemName(node, "type_identifier")
	if name == "" {
		return
	}
	sn := e.createNode(model.KindStruct, name, node, nodeExtra{
		visibility:     rustVisibility(node),
		isExported:     rustIsPub(node),
		docstring:      e.lookupDoc(node),
		typeParameters: rustTypeParams(node),
	})
	if sn == nil {
		return
	}
	fields := rustChildOfKind(node, "field_declaration_list")
	if fields == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, sn.ID)
	for i := 0; i < fields.NamedChildCount(); i++ {
		fd := fields.NamedChild(i)
		if fd == nil || fd.Kind() != "field_declaration" {
			continue
		}
		fname := ""
		if n := fd.ChildByFieldName("name"); n != nil {
			fname = n.Text()
		} else if n := rustChildOfKind(fd, "field_identifier"); n != nil {
			fname = n.Text()
		}
		if fname == "" {
			continue
		}
		e.createNode(model.KindField, fname, fd, nodeExtra{
			visibility: rustVisibility(fd),
			isExported: rustIsPub(fd),
		})
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractRustEnum handles an enum_item → KindEnum with KindEnumMember variants.
func (e *extractor) extractRustEnum(node *tsparse.Node) {
	name := rustItemName(node, "type_identifier")
	if name == "" {
		return
	}
	en := e.createNode(model.KindEnum, name, node, nodeExtra{
		visibility:     rustVisibility(node),
		isExported:     rustIsPub(node),
		docstring:      e.lookupDoc(node),
		typeParameters: rustTypeParams(node),
	})
	if en == nil {
		return
	}
	variants := rustChildOfKind(node, "enum_variant_list")
	if variants == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, en.ID)
	for i := 0; i < variants.NamedChildCount(); i++ {
		v := variants.NamedChild(i)
		if v == nil || v.Kind() != "enum_variant" {
			continue
		}
		vname := ""
		if n := v.ChildByFieldName("name"); n != nil {
			vname = n.Text()
		} else if n := rustChildOfKind(v, "identifier"); n != nil {
			vname = n.Text()
		}
		if vname == "" {
			continue
		}
		e.createNode(model.KindEnumMember, vname, v, nodeExtra{})
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractRustImpl handles an impl_item. The block is NOT its own node: its
// methods attach to the implementing type (qualified Type::method) via the same
// receiver-contains mechanism as Go methods. For `impl Trait for Type` blocks we
// also emit `implements` (Type→Trait) and `overrides` (Type::method→Trait::method)
// references — recorded directly because Rust satisfaction is explicit.
func (e *extractor) extractRustImpl(node *tsparse.Node) {
	implType := rustBaseTypeName(node.ChildByFieldName("type"))
	traitName := rustBaseTypeName(node.ChildByFieldName("trait"))
	if implType == "" {
		return
	}

	// implements edge from the implementing type to the trait.
	if traitName != "" {
		if typeNode := e.findRustTypeNode(implType); typeNode != nil {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    typeNode.ID,
				ReferenceName: traitName,
				ReferenceKind: model.EdgeImplements,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
				FilePath:      e.filePath,
				Language:      e.lang,
			})
		}
	}

	body := node.ChildByFieldName("body")
	if body == nil {
		body = rustChildOfKind(node, "declaration_list")
	}
	if body == nil {
		return
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "function_item":
			e.extractRustImplMethod(child, implType, traitName)
		case "const_item", "static_item":
			e.extractRustImplConst(child, implType)
		case "type_item":
			e.extractRustTypeAlias(child)
		default:
			e.visitNodeRust(child)
		}
	}
}

// extractRustImplMethod extracts a method declared inside an impl block, attached
// to implType under the qualified name "implType::name". When traitName is set
// (impl Trait for Type), also emits an `overrides` ref to the trait method.
func (e *extractor) extractRustImplMethod(node *tsparse.Node, implType, traitName string) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		nameNode = rustChildOfKind(node, "identifier")
	}
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}
	mn := e.createNode(model.KindMethod, name, node, nodeExtra{
		visibility:     rustVisibility(node),
		isExported:     rustIsPub(node),
		isAsync:        rustIsAsync(node),
		docstring:      e.lookupDoc(node),
		signature:      rustFnSignature(node),
		returnType:     rustBaseTypeName(rustReturnTypeNode(node)),
		typeParameters: rustTypeParams(node),
		qualifiedName:  implType + "::" + name,
	})
	if mn == nil {
		return
	}
	// Contain the method under its type node if present in this file.
	e.addReceiverContains(implType, mn.ID)

	// overrides edge: Type::method → Trait::method.
	if traitName != "" {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    mn.ID,
			ReferenceName: traitName + "::" + name,
			ReferenceKind: model.EdgeOverrides,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
			FilePath:      e.filePath,
			Language:      e.lang,
		})
	}

	body := node.ChildByFieldName("body")
	if body != nil {
		e.nodeStack = append(e.nodeStack, mn.ID)
		e.visitRustBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractRustImplConst extracts an associated const/static inside an impl block,
// qualified as implType::name and contained under the type.
func (e *extractor) extractRustImplConst(node *tsparse.Node, implType string) {
	name := rustItemName(node, "identifier")
	if name == "" {
		return
	}
	cn := e.createNode(model.KindConstant, name, node, nodeExtra{
		visibility:    rustVisibility(node),
		isExported:    rustIsPub(node),
		signature:     rustConstSignature(node),
		qualifiedName: implType + "::" + name,
	})
	if cn != nil {
		e.addReceiverContains(implType, cn.ID)
	}
}

// findRustTypeNode returns the struct/enum/trait node named name defined in this
// file, or nil.
func (e *extractor) findRustTypeNode(name string) *model.Node {
	for i := range e.nodes {
		n := &e.nodes[i]
		if n.Name == name && n.FilePath == e.filePath &&
			(n.Kind == model.KindStruct || n.Kind == model.KindEnum ||
				n.Kind == model.KindClass || n.Kind == model.KindInterface) {
			return n
		}
	}
	return nil
}

// extractRustFunction handles a module/trait/nested function_item → KindFunction
// (or KindMethod inside a trait body).
func (e *extractor) extractRustFunction(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		nameNode = rustChildOfKind(node, "identifier")
	}
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}
	kind := model.KindFunction
	if e.isInsideClassLike() {
		kind = model.KindMethod
	}
	fn := e.createNode(kind, name, node, nodeExtra{
		visibility:     rustVisibility(node),
		isExported:     rustIsPub(node),
		isAsync:        rustIsAsync(node),
		docstring:      e.lookupDoc(node),
		signature:      rustFnSignature(node),
		returnType:     rustBaseTypeName(rustReturnTypeNode(node)),
		typeParameters: rustTypeParams(node),
	})
	if fn == nil {
		return
	}
	body := node.ChildByFieldName("body")
	if body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		e.visitRustBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractRustFunctionSig handles a function_signature_item: a trait method
// declaration without a body → KindMethod.
func (e *extractor) extractRustFunctionSig(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		nameNode = rustChildOfKind(node, "identifier")
	}
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}
	e.createNode(model.KindMethod, name, node, nodeExtra{
		visibility:     rustVisibility(node),
		isExported:     rustIsPub(node),
		isAsync:        rustIsAsync(node),
		docstring:      e.lookupDoc(node),
		signature:      rustFnSignature(node),
		returnType:     rustBaseTypeName(rustReturnTypeNode(node)),
		typeParameters: rustTypeParams(node),
	})
}

// extractRustConst handles a module-scope const_item / static_item → KindConstant.
func (e *extractor) extractRustConst(node *tsparse.Node) {
	name := rustItemName(node, "identifier")
	if name == "" {
		return
	}
	e.createNode(model.KindConstant, name, node, nodeExtra{
		visibility: rustVisibility(node),
		isExported: rustIsPub(node),
		docstring:  e.lookupDoc(node),
		signature:  rustConstSignature(node),
	})
}

// extractRustTypeAlias handles a type_item → KindTypeAlias.
func (e *extractor) extractRustTypeAlias(node *tsparse.Node) {
	name := rustItemName(node, "type_identifier")
	if name == "" {
		return
	}
	e.createNode(model.KindTypeAlias, name, node, nodeExtra{
		visibility: rustVisibility(node),
		isExported: rustIsPub(node),
		docstring:  e.lookupDoc(node),
	})
}

// extractRustUse handles a use_declaration → one KindImport per imported item,
// plus an EdgeImports reference from the current scope.
func (e *extractor) extractRustUse(node *tsparse.Node) {
	sig := strings.TrimSpace(node.Text())
	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}
	emit := func(name string, at *tsparse.Node) {
		if name == "" {
			return
		}
		e.createNode(model.KindImport, name, node, nodeExtra{signature: sig})
		if parentID != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    parentID,
				ReferenceName: name,
				ReferenceKind: model.EdgeImports,
				Line:          int(at.StartPoint().Row) + 1,
				Column:        int(at.StartPoint().Column),
			})
		}
	}

	arg := node.ChildByFieldName("argument")
	if arg == nil {
		return
	}
	e.collectRustUse(arg, emit)
}

// collectRustUse walks a use-tree argument and calls emit(localName, node) for
// each imported item. localName is the binding introduced into the current scope:
// the last path segment, the alias for `as` clauses, or each list member.
func (e *extractor) collectRustUse(arg *tsparse.Node, emit func(string, *tsparse.Node)) {
	switch arg.Kind() {
	case "identifier", "type_identifier":
		emit(arg.Text(), arg)
	case "scoped_identifier":
		if n := arg.ChildByFieldName("name"); n != nil {
			emit(n.Text(), arg)
		}
	case "use_as_clause":
		// path as alias — bind the alias.
		if alias := arg.ChildByFieldName("alias"); alias != nil {
			emit(alias.Text(), arg)
		} else if arg.NamedChildCount() >= 2 {
			emit(arg.NamedChild(arg.NamedChildCount()-1).Text(), arg)
		}
	case "use_wildcard":
		// use a::b::* — record the glob under its last real path segment
		// (the child scoped_identifier's name, e.g. std::collections::* →
		// "collections").
		if si := rustChildOfKind(arg, "scoped_identifier"); si != nil {
			if n := si.ChildByFieldName("name"); n != nil {
				emit(n.Text(), arg)
				return
			}
		}
		emit(rustLastPathSegment(strings.TrimSuffix(strings.TrimSpace(arg.Text()), "::*")), arg)
	case "scoped_use_list":
		list := rustChildOfKind(arg, "use_list")
		if list != nil {
			for i := 0; i < list.NamedChildCount(); i++ {
				if c := list.NamedChild(i); c != nil {
					e.collectRustUse(c, emit)
				}
			}
		}
	case "use_list":
		for i := 0; i < arg.NamedChildCount(); i++ {
			if c := arg.NamedChild(i); c != nil {
				e.collectRustUse(c, emit)
			}
		}
	}
}

// visitRustBody walks a function/method body for calls, struct literals, and
// scoped constructor calls. It descends through control-flow/expression nodes.
func (e *extractor) visitRustBody(body *tsparse.Node) {
	tsparse.Walk(body, func(node *tsparse.Node) {
		switch node.Kind() {
		case "call_expression":
			e.extractRustCall(node)
		case "struct_expression":
			e.extractRustStructLiteral(node)
		}
	})
}

// extractRustCall handles a call_expression: emits an EdgeCalls reference from
// the top of the node stack. `Type::method` / `Type::new` keep their scope so
// the resolver can promote constructor calls to instantiates. Method calls
// `x.method()` strip the receiver to the bare method name (no `self`).
func (e *extractor) extractRustCall(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	callerID := e.nodeStack[len(e.nodeStack)-1]
	fn := node.ChildByFieldName("function")
	if fn == nil {
		return
	}
	name := rustCalleeName(fn)
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

// extractRustStructLiteral handles a struct_expression `Type { ... }` → an
// instantiates reference to Type.
func (e *extractor) extractRustStructLiteral(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	fromID := e.nodeStack[len(e.nodeStack)-1]
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	typeName := rustBaseTypeName(nameNode)
	if typeName == "" {
		return
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    fromID,
		ReferenceName: typeName,
		ReferenceKind: model.EdgeInstantiates,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// rustCalleeName resolves a call's function node to a callee name.
//   - identifier            → bare name
//   - scoped_identifier     → "Path::name" (e.g. Circle::new, Type::assoc)
//   - field_expression      → method name (x.method → method)
func rustCalleeName(fn *tsparse.Node) string {
	switch fn.Kind() {
	case "identifier":
		return fn.Text()
	case "scoped_identifier":
		path := ""
		if p := fn.ChildByFieldName("path"); p != nil {
			path = rustLastPathSegment(p.Text())
		}
		nm := ""
		if n := fn.ChildByFieldName("name"); n != nil {
			nm = n.Text()
		}
		if path != "" && nm != "" {
			return path + "::" + nm
		}
		return nm
	case "field_expression":
		if f := fn.ChildByFieldName("field"); f != nil {
			return f.Text()
		}
		return ""
	case "generic_function":
		// foo::<T>() — unwrap to the inner function node.
		if inner := fn.ChildByFieldName("function"); inner != nil {
			return rustCalleeName(inner)
		}
		if fn.NamedChildCount() > 0 {
			return rustCalleeName(fn.NamedChild(0))
		}
		return ""
	default:
		return ""
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

// rustItemName returns the text of the first named child of the given kind that
// is the item's declared name. nameKind is "identifier" or "type_identifier".
func rustItemName(node *tsparse.Node, nameKind string) string {
	if n := node.ChildByFieldName("name"); n != nil {
		return n.Text()
	}
	if n := rustChildOfKind(node, nameKind); n != nil {
		return n.Text()
	}
	return ""
}

// rustChildOfKind returns the first named child of node with the given kind.
func rustChildOfKind(node *tsparse.Node, kind string) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// rustIsPub reports whether node carries a visibility_modifier (pub / pub(...)).
func rustIsPub(node *tsparse.Node) bool {
	return rustChildOfKind(node, "visibility_modifier") != nil
}

// rustVisibility returns a pointer to the visibility string ("pub", "pub(crate)",
// …) or to "private" when no modifier is present.
func rustVisibility(node *tsparse.Node) *string {
	v := "private"
	if vm := rustChildOfKind(node, "visibility_modifier"); vm != nil {
		v = strings.TrimSpace(vm.Text())
	}
	return &v
}

// rustIsAsync reports whether a function carries an async modifier.
func rustIsAsync(node *tsparse.Node) bool {
	if fm := rustChildOfKind(node, "function_modifiers"); fm != nil {
		return strings.Contains(fm.Text(), "async")
	}
	return false
}

// rustTypeParams extracts generic/lifetime parameter names from a type_parameters
// child (names only; no bounds).
func rustTypeParams(node *tsparse.Node) []string {
	tp := rustChildOfKind(node, "type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < tp.NamedChildCount(); i++ {
		c := tp.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "type_identifier", "lifetime", "constrained_type_parameter":
			t := strings.TrimSpace(c.Text())
			if c.Kind() == "constrained_type_parameter" {
				// `T: Bound` → keep just T.
				if idx := strings.IndexByte(t, ':'); idx > 0 {
					t = strings.TrimSpace(t[:idx])
				}
			}
			if t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// rustReturnTypeNode returns the return-type node of a function_item /
// function_signature_item: the named child after "parameters" that is not the
// body, name, or a modifier. The tree-sitter-rust grammar places the return type
// as a bare type child between the parameters and the block.
func rustReturnTypeNode(node *tsparse.Node) *tsparse.Node {
	// tree-sitter Node wrappers are recreated per call, so identify the
	// parameters node by its start position rather than pointer identity.
	params := node.ChildByFieldName("parameters")
	paramsRow, paramsCol := -1, -1
	if params != nil {
		paramsRow = int(params.StartPoint().Row)
		paramsCol = int(params.StartPoint().Column)
	}
	seenParams := params == nil
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if !seenParams {
			if int(c.StartPoint().Row) == paramsRow && int(c.StartPoint().Column) == paramsCol {
				seenParams = true
			}
			continue
		}
		switch c.Kind() {
		case "block", "where_clause", "type_parameters", "visibility_modifier",
			"function_modifiers", "parameters", "identifier", "generic_type_with_turbofish":
			continue
		}
		return c
	}
	return nil
}

// rustFnSignature builds a "fn name(params) -> ret" signature line (no body).
func rustFnSignature(node *tsparse.Node) string {
	name := ""
	if n := node.ChildByFieldName("name"); n != nil {
		name = n.Text()
	} else if n := rustChildOfKind(node, "identifier"); n != nil {
		name = n.Text()
	}
	if name == "" {
		return ""
	}
	var b strings.Builder
	if rustIsAsync(node) {
		b.WriteString("async ")
	}
	b.WriteString("fn ")
	b.WriteString(name)
	if p := node.ChildByFieldName("parameters"); p != nil {
		b.WriteString(p.Text())
	}
	if rt := rustReturnTypeNode(node); rt != nil {
		b.WriteString(" -> ")
		b.WriteString(strings.TrimSpace(rt.Text()))
	}
	return b.String()
}

// rustConstSignature renders a const/static declaration's value as a signature.
func rustConstSignature(node *tsparse.Node) string {
	if v := node.ChildByFieldName("value"); v != nil {
		val := strings.TrimSpace(v.Text())
		if len(val) > 100 {
			val = val[:100] + "..."
		}
		if val != "" {
			return "= " + val
		}
	}
	return ""
}

// rustBaseTypeName extracts the bare type name from a type node, stripping
// generic args, references, and path qualifiers (a::b::C → C).
func rustBaseTypeName(node *tsparse.Node) string {
	if node == nil {
		return ""
	}
	text := strings.TrimSpace(node.Text())
	if text == "" {
		return ""
	}
	// Strip leading reference/pointer markers.
	text = strings.TrimLeft(text, "&*")
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "mut ")
	text = strings.TrimSpace(text)
	// Strip generic args Foo<T> → Foo.
	if idx := strings.IndexByte(text, '<'); idx > 0 {
		text = strings.TrimSpace(text[:idx])
	}
	// Last path segment a::b::C → C.
	text = rustLastPathSegment(text)
	if !reValidIdent.MatchString(text) {
		return ""
	}
	return text
}

// rustLastPathSegment returns the last "::"-separated segment of a path.
func rustLastPathSegment(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.LastIndex(s, "::"); idx >= 0 {
		return strings.TrimSpace(s[idx+2:])
	}
	return s
}

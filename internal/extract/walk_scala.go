package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkScala walks a parsed Scala (tree-sitter `scala`) file root and extracts
// symbols. Scala is a JVM language; its resolver reuses the shared JVM helpers
// (resolve/resolve_jvm.go) parameterized by model.LangScala, exactly like Kotlin.
//
// Node type reference (tree-sitter-scala) — children are found by iterating
// named children and matching node kinds (a few fields exist but kind-matching
// is used uniformly):
//
//	compilation_unit → package_clause, import_declaration*, <definitions>
//	package_clause → "package", package_identifier
//	import_declaration → "import", identifier "." identifier ...,
//	    optional trailing namespace_selectors ("{A, B}", with arrow_renamed_identifier
//	    for "{Foo => Bar}") or namespace_wildcard ("_")
//	trait_definition → modifiers?, "trait", identifier, type_parameters?,
//	    extends_clause?, template_body?
//	class_definition → modifiers?, ("case")?, "class", identifier, type_parameters?,
//	    class_parameters?, extends_clause?, template_body?
//	object_definition → modifiers?, "object", identifier, extends_clause?, template_body?
//	enum_definition → modifiers?, "enum", identifier, extends_clause?, enum_body
//	enum_body → enum_case_definitions → "case", simple_enum_case(identifier)*
//	extends_clause → "extends", type_identifier (arguments?), ("with" type_identifier)*
//	function_definition → modifiers?, "def", identifier, parameters?, type_identifier?(return), "=", body
//	function_declaration → modifiers?, "def", identifier, parameters?, type_identifier?(return)  [abstract, no "="]
//	val_definition/var_definition → modifiers?, "val"/"var", identifier, type_identifier?, "=", expr
//	type_definition → modifiers?, "type", type_identifier(name), "=", type
//	class_parameter → ("val"/"var")?, identifier, type_identifier
//	call_expression → (identifier | field_expression), arguments
//	instance_expression → "new", type_identifier, arguments?
//	field_expression → identifier "." identifier
//	modifiers → access_modifier("private"/"protected")?, "abstract"/"override"/...
func (e *extractor) walkScala(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeScala(child)
		}
	}
}

// visitNodeScala dispatches a single node. Unknown kinds descend into their
// named children so calls/declarations nested inside statements are still seen.
func (e *extractor) visitNodeScala(node *tsparse.Node) {
	switch node.Kind() {
	case "package_clause":
		e.extractScalaPackage(node)
	case "import_declaration":
		e.extractScalaImport(node)
	case "trait_definition":
		e.extractScalaType(node, model.KindInterface)
	case "class_definition":
		e.extractScalaType(node, model.KindClass)
	case "object_definition":
		e.extractScalaObject(node)
	case "enum_definition":
		e.extractScalaEnum(node)
	case "function_definition", "function_declaration":
		e.extractScalaFunction(node)
	case "val_definition", "var_definition":
		e.extractScalaValVar(node)
	case "type_definition":
		e.extractScalaTypeAlias(node)
	case "call_expression":
		e.extractScalaCall(node)
	case "instance_expression":
		e.extractScalaInstance(node)
	default:
		e.visitScalaBody(node)
	}
}

// visitScalaBody descends into a node's named children looking for calls and
// nested declarations without emitting a node for the container itself.
func (e *extractor) visitScalaBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodeScala(child)
		}
	}
}

// extractScalaPackage emits a KindNamespace node for the package clause.
func (e *extractor) extractScalaPackage(node *tsparse.Node) {
	id := scNamedChildOfKind(node, "package_identifier")
	if id == nil {
		return
	}
	name := strings.TrimSpace(id.Text())
	if name == "" {
		return
	}
	e.createNode(model.KindNamespace, name, node, nodeExtra{
		signature: strings.TrimSpace(node.Text()),
	})
}

// extractScalaImport emits a KindImport node and an EdgeImports reference.
//
//   - `import a.b.C`         → binds simple name "C"
//   - `import a.b.{C, D}`    → one import node per selected name
//   - `import a.b.{X => Y}`  → binds the renamed (local) name "Y"
//   - `import a.b._`         → wildcard; signature carries a ".*" suffix so the
//     JVM context builder treats it as a wildcard-package import
func (e *extractor) extractScalaImport(node *tsparse.Node) {
	prefix := scImportPrefix(node) // dotted "a.b" path before any selector/wildcard

	if sel := scNamedChildOfKind(node, "namespace_selectors"); sel != nil {
		for i := 0; i < sel.NamedChildCount(); i++ {
			c := sel.NamedChild(i)
			if c == nil {
				continue
			}
			switch c.Kind() {
			case "identifier":
				e.emitScalaImport(prefix, c.Text(), c.Text(), node)
			case "arrow_renamed_identifier":
				orig, renamed := scRenamedNames(c)
				if renamed != "" {
					e.emitScalaImport(prefix, orig, renamed, node)
				}
			}
		}
		return
	}

	if scNamedChildOfKind(node, "namespace_wildcard") != nil {
		// Wildcard import: signature ends in ".*" so buildJVMContext records the
		// whole package as a wildcard. The node name is the package path.
		sig := "import " + prefix + ".*"
		e.createNode(model.KindImport, prefix, node, nodeExtra{signature: sig})
		return
	}

	// Plain `import a.b.C`: prefix already includes the trailing name segment.
	simple := prefix
	if idx := strings.LastIndex(prefix, "."); idx >= 0 {
		simple = prefix[idx+1:]
	}
	e.emitScalaImport(prefix, simple, simple, node)
}

// emitScalaImport creates a KindImport node bound to localName and emits the
// EdgeImports reference (under origName, so resolution finds the real def).
func (e *extractor) emitScalaImport(prefix, origName, localName string, node *tsparse.Node) {
	if localName == "" {
		return
	}
	fq := origName
	if prefix != "" && !strings.Contains(prefix, "{") {
		// For `import a.b.{C}` prefix is "a.b"; build "a.b.C".
		if strings.HasSuffix(prefix, "."+origName) || prefix == origName {
			fq = prefix
		} else {
			fq = prefix + "." + origName
		}
	}
	e.createNode(model.KindImport, localName, node, nodeExtra{signature: "import " + fq})

	if len(e.nodeStack) > 0 {
		parentID := e.nodeStack[len(e.nodeStack)-1]
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    parentID,
			ReferenceName: origName,
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// extractScalaType handles class_definition / trait_definition. defaultKind is
// KindClass for classes (incl. `case class`) and KindInterface for traits.
func (e *extractor) extractScalaType(node *tsparse.Node, defaultKind model.NodeKind) {
	nameNode := scNamedChildOfKind(node, "identifier")
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	mods := scModifiers(node)

	cn := e.createNode(defaultKind, nameNode.Text(), node, nodeExtra{
		visibility:     scVisibility(mods),
		isExported:     mods.isPublic(),
		isAbstract:     mods.isAbstract || defaultKind == model.KindInterface,
		typeParameters: scTypeParameters(node),
	})
	if cn == nil {
		return
	}

	e.emitScalaExtends(cn.ID, node)

	e.nodeStack = append(e.nodeStack, cn.ID)
	// `val`/`var` class parameters become fields.
	if cp := scNamedChildOfKind(node, "class_parameters"); cp != nil {
		e.extractScalaClassParams(cp)
	}
	if body := scNamedChildOfKind(node, "template_body"); body != nil {
		e.visitScalaBody(body)
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractScalaObject handles object_definition (`object X { ... }`), a Scala
// singleton (companion or standalone), modeled as a static KindClass.
func (e *extractor) extractScalaObject(node *tsparse.Node) {
	nameNode := scNamedChildOfKind(node, "identifier")
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	mods := scModifiers(node)
	cn := e.createNode(model.KindClass, nameNode.Text(), node, nodeExtra{
		visibility: scVisibility(mods),
		isExported: mods.isPublic(),
		isStatic:   true, // Scala object: a singleton; members resolve statically.
	})
	if cn == nil {
		return
	}
	e.emitScalaExtends(cn.ID, node)

	e.nodeStack = append(e.nodeStack, cn.ID)
	if body := scNamedChildOfKind(node, "template_body"); body != nil {
		e.visitScalaBody(body)
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractScalaEnum handles enum_definition (Scala 3). The enum is a KindEnum;
// its `case` entries are KindEnumMember.
func (e *extractor) extractScalaEnum(node *tsparse.Node) {
	nameNode := scNamedChildOfKind(node, "identifier")
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	mods := scModifiers(node)
	cn := e.createNode(model.KindEnum, nameNode.Text(), node, nodeExtra{
		visibility: scVisibility(mods),
		isExported: mods.isPublic(),
	})
	if cn == nil {
		return
	}
	e.emitScalaExtends(cn.ID, node)

	e.nodeStack = append(e.nodeStack, cn.ID)
	if body := scNamedChildOfKind(node, "enum_body"); body != nil {
		e.walkScalaEnumBody(body)
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// walkScalaEnumBody emits enum members and walks any methods declared in the body.
func (e *extractor) walkScalaEnumBody(body *tsparse.Node) {
	for i := 0; i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "enum_case_definitions" {
			for j := 0; j < child.NamedChildCount(); j++ {
				ec := child.NamedChild(j)
				if ec == nil {
					continue
				}
				if ec.Kind() == "simple_enum_case" || ec.Kind() == "full_enum_case" {
					if id := scNamedChildOfKind(ec, "identifier"); id != nil && id.Text() != "" {
						e.createNode(model.KindEnumMember, id.Text(), ec, nodeExtra{})
					}
				}
			}
			continue
		}
		e.visitNodeScala(child)
	}
}

// extractScalaClassParams emits a KindField per `val`/`var` class_parameter.
// Plain parameters (no val/var) are not fields.
func (e *extractor) extractScalaClassParams(cp *tsparse.Node) {
	for i := 0; i < cp.NamedChildCount(); i++ {
		param := cp.NamedChild(i)
		if param == nil || param.Kind() != "class_parameter" {
			continue
		}
		if !scHasToken(param, "val") && !scHasToken(param, "var") {
			continue
		}
		nameNode := scNamedChildOfKind(param, "identifier")
		if nameNode == nil || nameNode.Text() == "" {
			continue
		}
		var typeName string
		if t := scNamedChildOfKind(param, "type_identifier"); t != nil {
			typeName = t.Text()
		}
		e.createNode(model.KindField, nameNode.Text(), param, nodeExtra{
			signature:  strings.TrimSpace(param.Text()),
			returnType: typeName,
		})
	}
}

// extractScalaFunction handles function_definition / function_declaration (`def`).
// A def inside a class/trait/object is a KindMethod; a top-level def is a
// KindFunction.
func (e *extractor) extractScalaFunction(node *tsparse.Node) {
	nameNode := scNamedChildOfKind(node, "identifier")
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	mods := scModifiers(node)

	kind := model.KindFunction
	if e.isInsideClassLike() {
		kind = model.KindMethod
	}

	var returnType string
	if t := scFunctionReturnType(node); t != "" {
		returnType = t
	}

	fn := e.createNode(kind, nameNode.Text(), node, nodeExtra{
		signature:      scFunctionSignature(node),
		visibility:     scVisibility(mods),
		isExported:     mods.isPublic(),
		isAbstract:     node.Kind() == "function_declaration" || mods.isAbstract,
		returnType:     returnType,
		typeParameters: scTypeParameters(node),
	})
	if fn == nil {
		return
	}

	e.nodeStack = append(e.nodeStack, fn.ID)
	// Walk the body for calls / instantiations (everything after the "=").
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "modifiers", "identifier", "parameters", "type_parameters", "type_identifier":
			continue
		}
		e.visitNodeScala(c)
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractScalaValVar handles val_definition / var_definition. A `val`/`var`
// inside a template body is a KindField; an UPPER-case top-level `val` is a
// KindConstant; inside a function body it is a KindVariable.
func (e *extractor) extractScalaValVar(node *tsparse.Node) {
	nameNode := scNamedChildOfKind(node, "identifier")
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	name := nameNode.Text()
	mods := scModifiers(node)
	isVal := node.Kind() == "val_definition"

	var typeName string
	if t := scNamedChildOfKind(node, "type_identifier"); t != nil {
		typeName = t.Text()
	}

	kind := model.KindField
	switch {
	case e.isInsideFunctionLike():
		// A `val`/`var` inside a def body is a local variable.
		kind = model.KindVariable
	case e.isInsideClassLike():
		// A field of a class/trait/object.
		kind = model.KindField
	case isVal:
		// A top-level `val` is effectively a constant.
		kind = model.KindConstant
	default:
		// A top-level `var`.
		kind = model.KindVariable
	}

	vn := e.createNode(kind, name, node, nodeExtra{
		signature:  strings.TrimSpace(firstLine(node.Text())),
		visibility: scVisibility(mods),
		isExported: mods.isPublic(),
		returnType: typeName,
	})
	if vn == nil {
		return
	}

	// Walk the initializer expression for calls / constructor invocations.
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "modifiers", "identifier", "type_identifier":
			continue
		}
		e.visitNodeScala(c)
	}
}

// extractScalaTypeAlias handles type_definition (`type X = ...`).
func (e *extractor) extractScalaTypeAlias(node *tsparse.Node) {
	nameNode := scNamedChildOfKind(node, "type_identifier")
	if nameNode == nil || nameNode.Text() == "" {
		return
	}
	mods := scModifiers(node)
	e.createNode(model.KindTypeAlias, nameNode.Text(), node, nodeExtra{
		signature:  strings.TrimSpace(node.Text()),
		visibility: scVisibility(mods),
		isExported: mods.isPublic(),
	})
}

// extractScalaCall handles a call_expression: emits an EdgeCalls reference from
// the top of the node stack. Callee may be a bare `identifier` ("m") or a
// `field_expression` ("recv.m").
func (e *extractor) extractScalaCall(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		if name := scCalleeName(node); name != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: name,
				ReferenceKind: model.EdgeCalls,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
		}
	}
	// Descend into the callee receiver and arguments for nested calls.
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "identifier":
			continue // bare callee name
		case "field_expression":
			// Walk the receiver (the first identifier) for nested calls.
			if recv := c.NamedChild(0); recv != nil && recv.Kind() != "identifier" {
				e.visitNodeScala(recv)
			}
		default:
			e.visitNodeScala(c)
		}
	}
}

// extractScalaInstance handles instance_expression (`new T(...)`): emits an
// EdgeInstantiates reference for T, then walks the arguments.
func (e *extractor) extractScalaInstance(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		if t := scNamedChildOfKind(node, "type_identifier"); t != nil && t.Text() != "" {
			callerID := e.nodeStack[len(e.nodeStack)-1]
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: scSimpleTypeName(t.Text()),
				ReferenceKind: model.EdgeInstantiates,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
		}
	}
	if args := scNamedChildOfKind(node, "arguments"); args != nil {
		e.visitScalaBody(args)
	}
}

// emitScalaExtends classifies an extends_clause: the first type is the primary
// parent (extends — superclass for a class, or the first trait); every type
// after a `with` keyword is a mixin (implements). This mirrors the spec: first
// parent → extends, `with` mixins → implements.
func (e *extractor) emitScalaExtends(fromID string, node *tsparse.Node) {
	ec := scNamedChildOfKind(node, "extends_clause")
	if ec == nil {
		return
	}
	first := true
	for i := 0; i < ec.ChildCount(); i++ {
		c := ec.Child(i)
		if c == nil || !c.IsNamed() {
			continue
		}
		if c.Kind() != "type_identifier" && c.Kind() != "generic_type" {
			continue
		}
		name := scSimpleTypeName(c.Text())
		if name == "" {
			continue
		}
		kind := model.EdgeImplements
		if first {
			kind = model.EdgeExtends
			first = false
		}
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: name,
			ReferenceKind: kind,
			Line:          int(c.StartPoint().Row) + 1,
			Column:        int(c.StartPoint().Column),
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Scala helpers
// ──────────────────────────────────────────────────────────────────────────────

// scMods holds parsed modifiers of a Scala declaration.
type scMods struct {
	visibility string // "public"/"private"/"protected"/""
	isAbstract bool
	isOverride bool
}

func (m scMods) isPublic() bool { return m.visibility == "" || m.visibility == "public" }

// scModifiers parses the `modifiers` child of a declaration node. Scala access
// modifiers appear as an `access_modifier` ("private"/"protected"); `abstract`
// and `override` appear as bare tokens inside `modifiers`.
func scModifiers(node *tsparse.Node) scMods {
	var m scMods
	mods := scNamedChildOfKind(node, "modifiers")
	if mods == nil {
		return m
	}
	for i := 0; i < mods.ChildCount(); i++ {
		c := mods.Child(i)
		if c == nil {
			continue
		}
		if c.Kind() == "access_modifier" {
			m.visibility = strings.TrimSpace(firstWord(c.Text()))
			continue
		}
		switch strings.TrimSpace(c.Text()) {
		case "abstract":
			m.isAbstract = true
		case "override":
			m.isOverride = true
		}
	}
	return m
}

// scVisibility maps modifiers to a visibility pointer (always emitted; Scala's
// default is public).
func scVisibility(m scMods) *string {
	v := m.visibility
	if v == "" {
		v = "public"
	}
	return &v
}

// scTypeParameters returns generic type-parameter names (the `type_parameters`
// child), e.g. ["T", "K"].
func scTypeParameters(node *tsparse.Node) []string {
	tp := scNamedChildOfKind(node, "type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < tp.NamedChildCount(); i++ {
		c := tp.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == "type_identifier" || c.Kind() == "type_parameter" {
			name := scSimpleTypeName(c.Text())
			if name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// scFunctionReturnType returns the declared return type — the first type node
// after the name/params and before the "=" — or "" when none.
func scFunctionReturnType(node *tsparse.Node) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if isScalaTypeNode(c.Kind()) {
			return scSimpleTypeName(c.Text())
		}
	}
	return ""
}

// isScalaTypeNode reports whether kind is a type-bearing node used as a return type.
func isScalaTypeNode(kind string) bool {
	switch kind {
	case "type_identifier", "generic_type", "tuple_type", "function_type":
		return true
	}
	return false
}

// scFunctionSignature renders a def header up to the return type.
func scFunctionSignature(node *tsparse.Node) string {
	nameNode := scNamedChildOfKind(node, "identifier")
	if nameNode == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("def ")
	sb.WriteString(nameNode.Text())
	if params := scNamedChildOfKind(node, "parameters"); params != nil {
		sb.WriteString(params.Text())
	}
	if rt := scFunctionReturnType(node); rt != "" {
		sb.WriteString(": ")
		sb.WriteString(rt)
	}
	return strings.TrimSpace(sb.String())
}

// scCalleeName resolves a call_expression to its callee reference name.
// Bare `m()` → "m"; `recv.m()` → "recv.m".
func scCalleeName(node *tsparse.Node) string {
	callee := node.NamedChild(0)
	if callee == nil {
		return ""
	}
	switch callee.Kind() {
	case "identifier":
		return callee.Text()
	case "field_expression":
		recv := callee.NamedChild(0)
		method := ""
		if n := callee.NamedChildCount(); n > 0 {
			if last := callee.NamedChild(n - 1); last != nil {
				method = last.Text()
			}
		}
		if method == "" {
			return ""
		}
		if recv != nil && recv.Kind() == "identifier" {
			return recv.Text() + "." + method
		}
		// Chained / complex receiver: resolve by bare method name.
		return method
	}
	return ""
}

// scImportPrefix returns the dotted path of identifiers up to (but not
// including) a trailing namespace_selectors / namespace_wildcard. For
// `import a.b.C` it returns "a.b.C"; for `import a.b.{X,Y}` it returns "a.b".
func scImportPrefix(node *tsparse.Node) string {
	var parts []string
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == "identifier" {
			parts = append(parts, c.Text())
		}
	}
	return strings.Join(parts, ".")
}

// scRenamedNames extracts (original, renamed) from an arrow_renamed_identifier
// ("Foo => Bar" → ("Foo", "Bar")).
func scRenamedNames(node *tsparse.Node) (string, string) {
	var ids []string
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "identifier" {
			ids = append(ids, c.Text())
		}
	}
	if len(ids) == 2 {
		return ids[0], ids[1]
	}
	if len(ids) == 1 {
		return ids[0], ids[0]
	}
	return "", ""
}

// scSimpleTypeName reduces a type expression text to its simple last-segment
// name, stripping generics and package qualifiers.
func scSimpleTypeName(t string) string {
	t = strings.TrimSpace(t)
	if idx := strings.IndexByte(t, '['); idx >= 0 {
		t = t[:idx]
	}
	if idx := strings.LastIndex(t, "."); idx >= 0 {
		t = t[idx+1:]
	}
	return strings.TrimSpace(t)
}

// scNamedChildOfKind returns the first named child of node whose kind matches.
func scNamedChildOfKind(node *tsparse.Node, kind string) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// scHasToken reports whether node has an anonymous child token with the given text.
func scHasToken(node *tsparse.Node, token string) bool {
	for i := 0; i < node.ChildCount(); i++ {
		c := node.Child(i)
		if c == nil || c.IsNamed() {
			continue
		}
		if c.Text() == token {
			return true
		}
	}
	return false
}

// firstWord returns the first whitespace-delimited word of s.
func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, " \t\n"); idx >= 0 {
		return s[:idx]
	}
	return s
}

// firstLine returns the first line of s.
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

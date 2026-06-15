package extract

import (
	"slices"
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkPHP walks a parsed PHP (tree-sitter `php`) file root and extracts symbols.
// PHP is dynamically typed with namespaces, so resolution mirrors Python/Ruby:
// name + use-alias imports + constructor-assignment ($x = new T) type inference.
//
// Node type reference (tree-sitter-php):
//
//	namespace_definition (field: name=namespace_name)
//	namespace_use_declaration → namespace_use_clause (child qualified_name/name; field alias=name)
//	class_declaration / interface_declaration / trait_declaration / enum_declaration
//	    (fields: name, body; attributes=attribute_list; base_clause; class_interface_clause)
//	enum_case (fields: name, value)
//	method_declaration (fields: name, parameters, return_type, body; visibility/static/abstract modifiers)
//	function_definition (fields: name, parameters, body)
//	property_declaration → property_element (field name=variable_name)
//	const_declaration / class_const_declaration → const_element (child name)
//	use_declaration (trait use inside a class body; child name)
//	object_creation_expression (new T(...))
//	function_call_expression (field function); member_call_expression (fields object, name)
//	scoped_call_expression (fields scope, name) — Foo::bar / self::bar / parent::bar
//	member_access_expression ($this->prop; fields object, name)
//	assignment_expression (fields left, right)
//	variable_name → name; qualified_name (field prefix=namespace_name)
func (e *extractor) walkPHP(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodePHP(child)
		}
	}
}

// visitNodePHP dispatches a single statement node. Unknown kinds descend into
// their named children so calls/definitions nested in control flow are seen.
func (e *extractor) visitNodePHP(node *tsparse.Node) {
	switch node.Kind() {
	case "namespace_definition":
		e.extractPHPNamespace(node)
	case "class_declaration":
		e.extractPHPClassLike(node, model.KindClass)
	case "interface_declaration":
		e.extractPHPClassLike(node, model.KindInterface)
	case "trait_declaration":
		e.extractPHPClassLike(node, model.KindTrait)
	case "enum_declaration":
		e.extractPHPEnum(node)
	case "namespace_use_declaration":
		e.extractPHPUse(node)
	case "method_declaration":
		e.extractPHPMethod(node)
	case "function_definition":
		e.extractPHPFunction(node)
	case "property_declaration":
		e.extractPHPProperty(node)
	case "const_declaration", "class_const_declaration":
		e.extractPHPConst(node)
	case "use_declaration":
		e.extractPHPTraitUse(node)
	case "enum_case":
		e.extractPHPEnumCase(node)
	case "object_creation_expression":
		e.extractPHPNew(node)
	case "function_call_expression", "member_call_expression", "scoped_call_expression":
		e.extractPHPCall(node)
	case "assignment_expression":
		e.extractPHPAssignment(node)
	default:
		e.visitPHPBody(node)
	}
}

// visitPHPBody descends into a node's named children without emitting a node for
// the container itself.
func (e *extractor) visitPHPBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodePHP(child)
		}
	}
}

// extractPHPNamespace handles namespace_definition → KindNamespace. When the
// namespace has a body block (braced form) its children are walked under the
// namespace scope; the semicolon form has no body and its siblings stay at file
// scope (the namespace still becomes a node).
func (e *extractor) extractPHPNamespace(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	name := ""
	if nameNode != nil {
		name = nameNode.Text()
	}
	if name == "" {
		// Anonymous/global namespace block: just descend into the body.
		if body := node.ChildByFieldName("body"); body != nil {
			e.visitPHPBody(body)
		}
		return
	}
	ns := e.createNode(model.KindNamespace, name, node, nodeExtra{})
	if ns == nil {
		return
	}
	if body := node.ChildByFieldName("body"); body != nil {
		e.nodeStack = append(e.nodeStack, ns.ID)
		e.visitPHPBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractPHPUse handles namespace_use_declaration (`use A\B\C;`, `use A\B\C as D;`).
// Emits one KindImport node per clause named by the imported alias (or last
// segment) plus an EdgeImports reference from the enclosing scope.
func (e *extractor) extractPHPUse(node *tsparse.Node) {
	sig := strings.TrimSpace(node.Text())
	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		clause := node.NamedChild(i)
		if clause == nil || clause.Kind() != "namespace_use_clause" {
			continue
		}
		name := ""
		if alias := clause.ChildByFieldName("alias"); alias != nil {
			name = alias.Text()
		} else {
			// No alias: import name is the last segment of the qualified name.
			name = phpLastSegment(phpUseClausePath(clause))
		}
		if name == "" {
			continue
		}
		e.createNode(model.KindImport, name, clause, nodeExtra{signature: sig})
		if parentID != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    parentID,
				ReferenceName: name,
				ReferenceKind: model.EdgeImports,
				Line:          int(clause.StartPoint().Row) + 1,
				Column:        int(clause.StartPoint().Column),
			})
		}
	}
}

// extractPHPClassLike handles class_declaration / interface_declaration /
// trait_declaration. Emits extends (base_clause) and implements
// (class_interface_clause) references, attribute decorates, then walks the body.
func (e *extractor) extractPHPClassLike(node *tsparse.Node, kind model.NodeKind) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	decorators := phpAttributes(node)
	cn := e.createNode(kind, name, node, nodeExtra{
		isAbstract: phpHasModifier(node, "abstract_modifier"),
		decorators: decorators,
	})
	if cn == nil {
		return
	}
	e.emitPHPDecorates(cn.ID, decorators, node)

	// extends — base_clause lists one (class) or more (interface) names.
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "base_clause":
			e.emitPHPNameRefs(cn.ID, c, model.EdgeExtends)
		case "class_interface_clause":
			e.emitPHPNameRefs(cn.ID, c, model.EdgeImplements)
		}
	}

	e.walkPHPBody(node, cn.ID)
}

// extractPHPEnum handles enum_declaration → KindEnum, walking its
// enum_declaration_list body for cases and methods.
func (e *extractor) extractPHPEnum(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}
	decorators := phpAttributes(node)
	en := e.createNode(model.KindEnum, name, node, nodeExtra{decorators: decorators})
	if en == nil {
		return
	}
	e.emitPHPDecorates(en.ID, decorators, node)

	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "class_interface_clause" {
			e.emitPHPNameRefs(en.ID, c, model.EdgeImplements)
		}
	}

	e.walkPHPBody(node, en.ID)
}

// extractPHPEnumCase handles enum_case → KindEnumMember.
func (e *extractor) extractPHPEnumCase(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	if nameNode.Text() == "" {
		return
	}
	e.createNode(model.KindEnumMember, nameNode.Text(), node, nodeExtra{})
}

// walkPHPBody pushes scopeID and visits the declaration_list / enum_declaration_list
// body's named children.
func (e *extractor) walkPHPBody(node *tsparse.Node, scopeID string) {
	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, scopeID)
	for i := 0; i < body.NamedChildCount(); i++ {
		if child := body.NamedChild(i); child != nil {
			e.visitNodePHP(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractPHPMethod handles method_declaration → KindMethod (always inside a
// class-like). Captures visibility / static / abstract modifiers.
func (e *extractor) extractPHPMethod(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}
	decorators := phpAttributes(node)
	vis := phpVisibility(node)
	fn := e.createNode(model.KindMethod, name, node, nodeExtra{
		signature:  phpFuncSignature(node),
		visibility: vis,
		isStatic:   phpHasModifier(node, "static_modifier"),
		isAbstract: phpHasModifier(node, "abstract_modifier"),
		decorators: decorators,
	})
	if fn == nil {
		return
	}
	e.emitPHPDecorates(fn.ID, decorators, node)
	e.walkPHPFuncBody(node, fn.ID)
}

// extractPHPFunction handles top-level function_definition → KindFunction.
func (e *extractor) extractPHPFunction(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}
	decorators := phpAttributes(node)
	fn := e.createNode(model.KindFunction, name, node, nodeExtra{
		signature:  phpFuncSignature(node),
		decorators: decorators,
	})
	if fn == nil {
		return
	}
	e.emitPHPDecorates(fn.ID, decorators, node)
	e.walkPHPFuncBody(node, fn.ID)
}

// walkPHPFuncBody walks a function/method's compound_statement body under fnID.
func (e *extractor) walkPHPFuncBody(node *tsparse.Node, fnID string) {
	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, fnID)
	e.visitPHPBody(body)
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractPHPProperty handles property_declaration → one KindField per
// property_element, attributed to the enclosing class.
func (e *extractor) extractPHPProperty(node *tsparse.Node) {
	vis := phpVisibility(node)
	isStatic := phpHasModifier(node, "static_modifier")
	for i := 0; i < node.NamedChildCount(); i++ {
		el := node.NamedChild(i)
		if el == nil || el.Kind() != "property_element" {
			continue
		}
		nameNode := el.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := phpVarName(nameNode)
		if name == "" {
			continue
		}
		e.createNode(model.KindField, name, el, nodeExtra{
			visibility: vis,
			isStatic:   isStatic,
		})
	}
}

// extractPHPConst handles const_declaration / class_const_declaration → one
// KindConstant per const_element.
func (e *extractor) extractPHPConst(node *tsparse.Node) {
	vis := phpVisibility(node)
	for i := 0; i < node.NamedChildCount(); i++ {
		el := node.NamedChild(i)
		if el == nil || el.Kind() != "const_element" {
			continue
		}
		// const_element's first named child is the name.
		var nm *tsparse.Node
		for j := 0; j < el.NamedChildCount(); j++ {
			if c := el.NamedChild(j); c != nil && c.Kind() == "name" {
				nm = c
				break
			}
		}
		if nm == nil || nm.Text() == "" {
			continue
		}
		e.createNode(model.KindConstant, nm.Text(), el, nodeExtra{visibility: vis})
	}
}

// extractPHPTraitUse handles a `use TraitName;` declaration inside a class body.
// A trait composed into a class is modeled as an EdgeImplements reference from
// the enclosing class to the trait (the nearest structural analog; documented in
// the design — PHP has no separate "uses" edge kind).
func (e *extractor) extractPHPTraitUse(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	fromID := e.nodeStack[len(e.nodeStack)-1]
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		name := phpTypeName(c)
		if name == "" {
			continue
		}
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: name,
			ReferenceKind: model.EdgeImplements,
			Line:          int(c.StartPoint().Row) + 1,
			Column:        int(c.StartPoint().Column),
		})
	}
}

// extractPHPNew handles object_creation_expression (new T(...)). Emits an
// EdgeInstantiates reference from the current scope to the class T. Descends into
// constructor arguments for nested calls.
func (e *extractor) extractPHPNew(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		fromID := e.nodeStack[len(e.nodeStack)-1]
		// The class is the first named child that is a name/qualified_name.
		for i := 0; i < node.NamedChildCount(); i++ {
			c := node.NamedChild(i)
			if c == nil {
				continue
			}
			if c.Kind() == "name" || c.Kind() == "qualified_name" {
				if name := phpTypeName(c); name != "" {
					e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
						FromNodeID:    fromID,
						ReferenceName: name,
						ReferenceKind: model.EdgeInstantiates,
						Line:          int(node.StartPoint().Row) + 1,
						Column:        int(node.StartPoint().Column),
					})
				}
				break
			}
		}
	}
	if args := node.ChildByFieldName("arguments"); args != nil {
		e.visitPHPBody(args)
	}
}

// extractPHPCall handles function_call_expression / member_call_expression /
// scoped_call_expression → an EdgeCalls reference from the current scope. The
// callee name carries a receiver prefix ("recv.method") for member calls so the
// resolver can apply var→class inference; $this/self/static/parent receivers are
// stripped (implicit-receiver calls), mirroring Python's self stripping.
func (e *extractor) extractPHPCall(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		if name := phpCalleeName(node); name != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: name,
				ReferenceKind: model.EdgeCalls,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
		}
	}
	// Descend into arguments and the receiver for nested/chained calls.
	if args := node.ChildByFieldName("arguments"); args != nil {
		e.visitPHPBody(args)
	}
	if obj := node.ChildByFieldName("object"); obj != nil {
		switch obj.Kind() {
		case "member_call_expression", "scoped_call_expression", "function_call_expression":
			e.extractPHPCall(obj)
		}
	}
}

// extractPHPAssignment handles assignment_expression. A `$x = new T(...)` whose
// LHS is a plain variable becomes a KindVariable whose signature records the
// constructor so the resolver can infer $x's class. A `$this->prop = ...` LHS
// becomes a KindField on the enclosing class (the PHP analog of Python self.x).
func (e *extractor) extractPHPAssignment(node *tsparse.Node) {
	left := node.ChildByFieldName("left")
	right := node.ChildByFieldName("right")
	if left == nil {
		if right != nil {
			e.visitNodePHP(right)
		}
		return
	}

	switch left.Kind() {
	case "variable_name":
		name := phpVarName(left)
		if name != "" {
			kind := model.KindVariable
			if e.isInsideClassLike() {
				kind = model.KindField
			}
			e.createNode(kind, name, node, nodeExtra{signature: phpAssignSignature(right)})
		}
	case "member_access_expression":
		// $this->prop = ... → field on the enclosing class.
		obj := left.ChildByFieldName("object")
		prop := left.ChildByFieldName("name")
		if obj != nil && prop != nil && obj.Kind() == "variable_name" && phpVarName(obj) == "this" {
			e.createPHPThisField(prop.Text(), node)
		}
	}

	if right != nil {
		e.visitNodePHP(right)
	}
}

// createPHPThisField creates a KindField attributed to the nearest enclosing
// class-like on the node stack (so its qualified name is Class::prop).
func (e *extractor) createPHPThisField(name string, node *tsparse.Node) {
	if name == "" {
		return
	}
	classID := e.nearestPHPClassID()
	if classID == "" {
		e.createNode(model.KindField, name, node, nodeExtra{})
		return
	}
	saved := e.nodeStack
	idx := -1
	for i, v := range slices.Backward(e.nodeStack) {
		if v == classID {
			idx = i
			break
		}
	}
	if idx >= 0 {
		e.nodeStack = e.nodeStack[:idx+1]
	}
	e.createNode(model.KindField, name, node, nodeExtra{})
	e.nodeStack = saved
}

// nearestPHPClassID returns the ID of the nearest class/interface/trait/enum node
// on the stack.
func (e *extractor) nearestPHPClassID() string {
	for _, id := range slices.Backward(e.nodeStack) {

		for _, n := range e.nodes {
			if n.ID == id {
				switch n.Kind {
				case model.KindClass, model.KindInterface, model.KindTrait, model.KindEnum:
					return id
				}
			}
		}
	}
	return ""
}

// emitPHPNameRefs emits one reference of kind k per name/qualified_name child of
// a base_clause / class_interface_clause.
func (e *extractor) emitPHPNameRefs(fromID string, clause *tsparse.Node, k model.EdgeKind) {
	for i := 0; i < clause.NamedChildCount(); i++ {
		c := clause.NamedChild(i)
		if c == nil {
			continue
		}
		name := phpTypeName(c)
		if name == "" {
			continue
		}
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: name,
			ReferenceKind: k,
			Line:          int(c.StartPoint().Row) + 1,
			Column:        int(c.StartPoint().Column),
		})
	}
}

// emitPHPDecorates emits an EdgeDecorates reference per attribute head name.
func (e *extractor) emitPHPDecorates(fromID string, decorators []string, node *tsparse.Node) {
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

// ── helpers ──────────────────────────────────────────────────────────────────

// phpCalleeName resolves a call node to a callee name. function_call_expression →
// bare function name. member_call_expression → "recv.method" (receiver $-stripped;
// $this/self/static/parent stripped to bare method). scoped_call_expression →
// "Class.method" when the scope is a class name, else bare method.
func phpCalleeName(node *tsparse.Node) string {
	switch node.Kind() {
	case "function_call_expression":
		fn := node.ChildByFieldName("function")
		if fn == nil {
			return ""
		}
		return phpTypeName(fn)
	case "member_call_expression":
		nm := node.ChildByFieldName("name")
		if nm == nil {
			return ""
		}
		method := nm.Text()
		if method == "" {
			return ""
		}
		obj := node.ChildByFieldName("object")
		if obj != nil && obj.Kind() == "variable_name" {
			recv := phpVarName(obj)
			if recv == "this" {
				return method
			}
			return recv + "." + method
		}
		return method
	case "scoped_call_expression":
		nm := node.ChildByFieldName("name")
		if nm == nil {
			return ""
		}
		method := nm.Text()
		if method == "" {
			return ""
		}
		scope := node.ChildByFieldName("scope")
		if scope != nil {
			s := scope.Text()
			if s == "self" || s == "static" || s == "parent" {
				return method
			}
			if name := phpTypeName(scope); name != "" {
				return name + "." + method
			}
		}
		return method
	}
	return ""
}

// phpTypeName returns the simple type name from a name / qualified_name node
// (last `\`-segment for qualified names).
func phpTypeName(node *tsparse.Node) string {
	switch node.Kind() {
	case "name":
		return node.Text()
	case "qualified_name":
		if nm := node.ChildByFieldName("name"); nm != nil {
			return nm.Text()
		}
		// Fall back to the last named child (the unqualified name).
		if c := node.NamedChild(node.NamedChildCount() - 1); c != nil {
			return c.Text()
		}
	}
	return phpLastSegment(strings.TrimPrefix(node.Text(), "\\"))
}

// phpUseClausePath returns the full path text of a use clause's qualified_name /
// name child.
func phpUseClausePath(clause *tsparse.Node) string {
	for i := 0; i < clause.NamedChildCount(); i++ {
		c := clause.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == "qualified_name" || c.Kind() == "name" {
			return c.Text()
		}
	}
	return ""
}

// phpLastSegment returns the last `\`-separated segment of a namespace path.
func phpLastSegment(path string) string {
	path = strings.TrimSpace(path)
	if idx := strings.LastIndex(path, "\\"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// phpNewArgs returns the `(...)` argument text of an object_creation_expression
// ("" when absent). `arguments` is a plain child of new-expressions, not a field.
func phpNewArgs(node *tsparse.Node) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "arguments" {
			return c.Text()
		}
	}
	return ""
}

// phpVarName returns the bare name of a variable_name node ($foo → foo).
func phpVarName(node *tsparse.Node) string {
	if node == nil {
		return ""
	}
	return strings.TrimPrefix(node.Text(), "$")
}

// phpVisibility returns the visibility modifier (public/private/protected) of a
// declaration, or nil when none is present.
func phpVisibility(node *tsparse.Node) *string {
	for i := 0; i < node.ChildCount(); i++ {
		c := node.Child(i)
		if c != nil && c.Kind() == "visibility_modifier" {
			v := c.Text()
			return &v
		}
	}
	return nil
}

// phpHasModifier reports whether the declaration has a direct child of the given
// modifier kind (e.g. "static_modifier", "abstract_modifier").
func phpHasModifier(node *tsparse.Node, kind string) bool {
	for i := 0; i < node.ChildCount(); i++ {
		if c := node.Child(i); c != nil && c.Kind() == kind {
			return true
		}
	}
	return false
}

// phpAttributes returns the head names of PHP 8 attributes (#[Route(...)] → Route)
// declared on a class/method/function/enum node.
func phpAttributes(node *tsparse.Node) []string {
	var out []string
	for i := 0; i < node.NamedChildCount(); i++ {
		al := node.NamedChild(i)
		if al == nil || al.Kind() != "attribute_list" {
			continue
		}
		tsparse.Walk(al, func(n *tsparse.Node) {
			if n.Kind() == "attribute" {
				if nm := n.ChildByFieldName("name"); nm != nil {
					if name := phpTypeName(nm); name != "" {
						out = append(out, name)
					}
				} else {
					// name may be a plain child rather than a field.
					for j := 0; j < n.NamedChildCount(); j++ {
						c := n.NamedChild(j)
						if c != nil && (c.Kind() == "name" || c.Kind() == "qualified_name") {
							if name := phpTypeName(c); name != "" {
								out = append(out, name)
							}
							break
						}
					}
				}
			}
		})
	}
	return out
}

// phpFuncSignature renders a function/method header (name + parameters + return).
func phpFuncSignature(node *tsparse.Node) string {
	name := node.ChildByFieldName("name")
	if name == nil {
		return ""
	}
	sig := "function " + name.Text()
	if params := node.ChildByFieldName("parameters"); params != nil {
		sig += params.Text()
	}
	if ret := node.ChildByFieldName("return_type"); ret != nil {
		sig += ": " + ret.Text()
	}
	return sig
}

// phpAssignSignature renders the right-hand side of an assignment as a signature,
// normalizing `new ClassName(...)` to `= ClassName(...)` so the shared
// constructor-class parser (pyConstructorClass) can extract the class name.
func phpAssignSignature(right *tsparse.Node) string {
	if right == nil {
		return ""
	}
	if right.Kind() == "object_creation_expression" {
		// new T(args) → "= T(args)" (drop the `new ` and any `\` prefix so the
		// last-segment class name parses).
		for i := 0; i < right.NamedChildCount(); i++ {
			c := right.NamedChild(i)
			if c == nil {
				continue
			}
			if c.Kind() == "name" || c.Kind() == "qualified_name" {
				cls := phpTypeName(c)
				args := phpNewArgs(right)
				return "= " + cls + args
			}
		}
	}
	val := right.Text()
	if val == "" {
		return ""
	}
	if len(val) > 100 {
		return "= " + val[:100] + "..."
	}
	return "= " + val
}

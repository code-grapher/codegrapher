package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkPython walks a parsed Python (tree-sitter `python`) file root and
// extracts symbols. Called by ExtractFile after the file node is emitted.
//
// Node type reference (tree-sitter-python):
//
//	function_definition (fields: name, parameters, body; async token child)
//	class_definition (fields: name, superclasses, body)
//	decorated_definition (decorator children + definition field)
//	import_statement / import_from_statement
//	assignment (fields: left, right)
//	attribute (fields: object, attribute)
//	call (fields: function, arguments)
//	block / identifier / string
func (e *extractor) walkPython(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodePython(child)
		}
	}
}

// visitNodePython dispatches a single statement node. Unknown kinds descend
// into their children so calls/definitions nested inside control flow
// (if/for/while/with/try/expression_statement) are still seen.
func (e *extractor) visitNodePython(node *tsparse.Node) {
	switch node.Kind() {
	case "function_definition":
		e.extractPyFunction(node, nil)
	case "class_definition":
		e.extractPyClass(node, nil)
	case "decorated_definition":
		e.extractPyDecorated(node)
	case "import_statement", "import_from_statement":
		e.extractPyImport(node)
	case "assignment":
		e.extractPyAssignment(node)
	case "call":
		e.extractPyCall(node)
	default:
		e.visitPyBody(node)
	}
}

// visitPyBody descends into a node's named children looking for calls and
// nested definitions without emitting a node for the container itself.
func (e *extractor) visitPyBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodePython(child)
		}
	}
}

// extractPyFunction handles function_definition. decorators is the decorator
// head-name list collected from a wrapping decorated_definition (or nil).
func (e *extractor) extractPyFunction(node *tsparse.Node, decorators []string) {
	nameNode := node.ChildByFieldName("name")
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
		// @property / @cached_property methods become properties.
		for _, d := range decorators {
			if d == "property" || d == "cached_property" {
				kind = model.KindProperty
				break
			}
		}
	}

	fn := e.createNode(kind, name, node, nodeExtra{
		docstring:  pyDocstring(node),
		signature:  pySignature(node),
		isAsync:    pyBoolChild(node, "async"),
		decorators: decorators,
	})
	if fn == nil {
		return
	}

	// Emit decorates references.
	e.emitPyDecorates(fn.ID, decorators, node)

	body := node.ChildByFieldName("body")
	if body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		for i := 0; i < body.NamedChildCount(); i++ {
			if child := body.NamedChild(i); child != nil {
				e.visitNodePython(child)
			}
		}
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractPyClass handles class_definition. decorators is the head-name list
// from a wrapping decorated_definition (or nil).
func (e *extractor) extractPyClass(node *tsparse.Node, decorators []string) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	cn := e.createNode(model.KindClass, name, node, nodeExtra{
		docstring:  pyDocstring(node),
		decorators: decorators,
	})
	if cn == nil {
		return
	}

	// Inheritance: emit an extends reference per base name.
	if supers := node.ChildByFieldName("superclasses"); supers != nil {
		for i := 0; i < supers.NamedChildCount(); i++ {
			base := supers.NamedChild(i)
			if base == nil {
				continue
			}
			baseName := pyBaseName(base)
			if baseName == "" {
				continue
			}
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    cn.ID,
				ReferenceName: baseName,
				ReferenceKind: model.EdgeExtends,
				Line:          int(base.StartPoint().Row) + 1,
				Column:        int(base.StartPoint().Column),
			})
		}
	}

	e.emitPyDecorates(cn.ID, decorators, node)

	body := node.ChildByFieldName("body")
	if body != nil {
		e.nodeStack = append(e.nodeStack, cn.ID)
		for i := 0; i < body.NamedChildCount(); i++ {
			if child := body.NamedChild(i); child != nil {
				e.visitNodePython(child)
			}
		}
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractPyDecorated handles decorated_definition: collects decorator head
// names and dispatches the inner def/class with them.
func (e *extractor) extractPyDecorated(node *tsparse.Node) {
	var decorators []string
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child != nil && child.Kind() == "decorator" {
			if head := pyDecoratorHead(child); head != "" {
				decorators = append(decorators, head)
			}
		}
	}

	def := node.ChildByFieldName("definition")
	if def == nil {
		// Fall back to the last non-decorator child.
		for i := node.NamedChildCount() - 1; i >= 0; i-- {
			if c := node.NamedChild(i); c != nil && c.Kind() != "decorator" {
				def = c
				break
			}
		}
	}
	if def == nil {
		return
	}
	switch def.Kind() {
	case "function_definition":
		e.extractPyFunction(def, decorators)
	case "class_definition":
		e.extractPyClass(def, decorators)
	}
}

// emitPyDecorates emits an EdgeDecorates reference per decorator head name.
func (e *extractor) emitPyDecorates(fromID string, decorators []string, node *tsparse.Node) {
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

// extractPyImport handles import_statement and import_from_statement. Emits a
// KindImport node per imported module/name plus an EdgeImports reference from
// the current parent scope.
func (e *extractor) extractPyImport(node *tsparse.Node) {
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

	if node.Kind() == "import_from_statement" {
		// from a.b import c, d — skip the module_name field child; emit the
		// imported names.
		moduleField := node.ChildByFieldName("module_name")
		for i := 0; i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child == nil || child == moduleField {
				continue
			}
			switch child.Kind() {
			case "dotted_name":
				emit(child.Text(), child)
			case "aliased_import":
				if alias := child.ChildByFieldName("alias"); alias != nil {
					emit(alias.Text(), child)
				} else if nm := child.ChildByFieldName("name"); nm != nil {
					emit(nm.Text(), child)
				}
			case "wildcard_import":
				if moduleField != nil {
					emit(moduleField.Text(), child)
				}
			}
		}
		return
	}

	// import a / import a.b as c / import a, b
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "dotted_name":
			emit(child.Text(), child)
		case "aliased_import":
			if alias := child.ChildByFieldName("alias"); alias != nil {
				emit(alias.Text(), child)
			} else if nm := child.ChildByFieldName("name"); nm != nil {
				emit(nm.Text(), child)
			}
		}
	}
}

// extractPyAssignment handles an assignment node (module/class/method scope).
func (e *extractor) extractPyAssignment(node *tsparse.Node) {
	left := node.ChildByFieldName("left")
	right := node.ChildByFieldName("right")
	if left == nil {
		return
	}

	switch left.Kind() {
	case "identifier":
		name := left.Text()
		var kind model.NodeKind
		if e.isInsideClassLike() {
			kind = model.KindField
		} else if pyIsConstantName(name) {
			kind = model.KindConstant
		} else {
			kind = model.KindVariable
		}
		e.createNode(kind, name, node, nodeExtra{signature: pyAssignSignature(right)})

	case "attribute":
		// self.<attr> = ... inside a method → field on the enclosing class.
		obj := left.ChildByFieldName("object")
		attr := left.ChildByFieldName("attribute")
		if obj != nil && attr != nil && obj.Kind() == "identifier" && obj.Text() == "self" {
			e.createPySelfField(attr.Text(), node)
		}
	}

	// Walk the right-hand side for calls (e.g. x = foo()).
	if right != nil {
		e.visitNodePython(right)
	}
}

// createPySelfField creates a KindField attributed to the nearest enclosing
// class on the node stack (so its qualified name is Class::attr).
func (e *extractor) createPySelfField(name string, node *tsparse.Node) {
	if name == "" {
		return
	}
	classID := e.nearestPyClassID()
	if classID == "" {
		// No enclosing class — create at current scope.
		e.createNode(model.KindField, name, node, nodeExtra{})
		return
	}
	// Temporarily restore the stack to end at the class so qualified-name and
	// the contains edge attribute to the class.
	saved := e.nodeStack
	idx := -1
	for i := len(e.nodeStack) - 1; i >= 0; i-- {
		if e.nodeStack[i] == classID {
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

// nearestPyClassID returns the ID of the nearest class-kind node on the stack.
func (e *extractor) nearestPyClassID() string {
	for i := len(e.nodeStack) - 1; i >= 0; i-- {
		id := e.nodeStack[i]
		for _, n := range e.nodes {
			if n.ID == id && n.Kind == model.KindClass {
				return id
			}
		}
	}
	return ""
}

// extractPyCall handles a call node: emits an EdgeCalls reference from the top
// of the node stack. Strips self/cls/super receivers from attribute callees.
func (e *extractor) extractPyCall(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		if fn := node.ChildByFieldName("function"); fn != nil {
			if name := pyCalleeName(fn); name != "" {
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
	// Descend into arguments for nested calls.
	if args := node.ChildByFieldName("arguments"); args != nil {
		e.visitPyBody(args)
	}
}

// pyCalleeName resolves a call's function node to a callee name.
func pyCalleeName(fn *tsparse.Node) string {
	switch fn.Kind() {
	case "identifier":
		return fn.Text()
	case "attribute":
		attr := fn.ChildByFieldName("attribute")
		if attr == nil {
			return ""
		}
		method := attr.Text()
		obj := fn.ChildByFieldName("object")
		if obj != nil && obj.Kind() == "identifier" {
			recv := obj.Text()
			if recv == "self" || recv == "cls" || recv == "super" {
				return method
			}
			return recv + "." + method
		}
		return method
	default:
		return ""
	}
}

// pyDocstring returns the docstring of a function/class node: the body block's
// first statement if it is a bare string. Quotes are stripped.
func pyDocstring(node *tsparse.Node) string {
	body := node.ChildByFieldName("body")
	if body == nil || body.NamedChildCount() == 0 {
		return ""
	}
	first := body.NamedChild(0)
	if first == nil {
		return ""
	}
	// The string may be a direct child or wrapped in expression_statement.
	str := first
	if first.Kind() == "expression_statement" && first.NamedChildCount() > 0 {
		str = first.NamedChild(0)
	}
	if str == nil || str.Kind() != "string" {
		return ""
	}
	for i := 0; i < str.NamedChildCount(); i++ {
		c := str.NamedChild(i)
		if c != nil && c.Kind() == "string_content" {
			return c.Text()
		}
	}
	return ""
}

// pySignature returns the `def ...:` header line: text up to and including the
// parameter list (no body).
func pySignature(node *tsparse.Node) string {
	name := node.ChildByFieldName("name")
	params := node.ChildByFieldName("parameters")
	if name == nil {
		return ""
	}
	prefix := "def "
	if pyBoolChild(node, "async") {
		prefix = "async def "
	}
	sig := prefix + name.Text()
	if params != nil {
		sig += params.Text()
	}
	return sig
}

// pyAssignSignature renders the right-hand side of an assignment as a signature.
func pyAssignSignature(right *tsparse.Node) string {
	if right == nil {
		return ""
	}
	val := right.Text()
	if len(val) > 100 {
		return "= " + val[:100] + "..."
	}
	if val == "" {
		return ""
	}
	return "= " + val
}

// pyBaseName returns the simple base-class name from a superclass argument.
// dotted bases (a.b.Base) reduce to the last segment.
func pyBaseName(node *tsparse.Node) string {
	switch node.Kind() {
	case "identifier":
		return node.Text()
	case "attribute":
		if attr := node.ChildByFieldName("attribute"); attr != nil {
			return attr.Text()
		}
	}
	t := node.Text()
	if idx := strings.LastIndex(t, "."); idx >= 0 {
		return t[idx+1:]
	}
	return t
}

// pyDecoratorHead returns the head name of a decorator: the dotted/identifier
// head before any call arguments. @app.route(...) → app.route; @dec → dec.
func pyDecoratorHead(decorator *tsparse.Node) string {
	// The decorator's named child is the expression (identifier / attribute /
	// call).
	var expr *tsparse.Node
	for i := 0; i < decorator.NamedChildCount(); i++ {
		if c := decorator.NamedChild(i); c != nil {
			expr = c
			break
		}
	}
	if expr == nil {
		return ""
	}
	if expr.Kind() == "call" {
		if fn := expr.ChildByFieldName("function"); fn != nil {
			expr = fn
		}
	}
	switch expr.Kind() {
	case "identifier":
		return expr.Text()
	case "attribute":
		return expr.Text()
	default:
		return strings.TrimSpace(expr.Text())
	}
}

// pyBoolChild reports whether the node has a direct child token of the given
// kind (e.g. "async").
func pyBoolChild(node *tsparse.Node, kind string) bool {
	for i := 0; i < node.ChildCount(); i++ {
		if c := node.Child(i); c != nil && c.Kind() == kind {
			return true
		}
	}
	return false
}

// pyIsConstantName reports whether a module-scope assignment target name should
// be a constant: UPPER_SNAKE (only [A-Z0-9_], at least one letter) or __dunder__.
func pyIsConstantName(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") && len(name) > 4 {
		return true
	}
	hasLetter := false
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return hasLetter
}

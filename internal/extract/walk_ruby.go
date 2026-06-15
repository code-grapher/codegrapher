package extract

import (
	"slices"
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkRuby walks a parsed Ruby (tree-sitter `ruby`) file root and extracts
// symbols. Mirrors walkPython: Ruby is dynamically typed, so resolution is
// heuristic (require deps + name + constructor-assignment type inference).
//
// Node type reference (tree-sitter-ruby):
//
//	module (fields: name, body)
//	class (fields: name, superclass, body)
//	method (def; fields: name, parameters, body)
//	singleton_method (def self.x; fields: object, name, parameters, body)
//	assignment (fields: left, right; left: constant/identifier/instance_variable/class_variable)
//	call (fields: receiver, method, arguments)
//	body_statement (block container)
//	constant / identifier / instance_variable (@x) / class_variable (@@x) / simple_symbol (:x)
func (e *extractor) walkRuby(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeRuby(child)
		}
	}
}

// visitNodeRuby dispatches a single statement node. Unknown kinds descend into
// their children so calls/definitions nested inside control flow are still seen.
func (e *extractor) visitNodeRuby(node *tsparse.Node) {
	switch node.Kind() {
	case "module":
		e.extractRubyModule(node)
	case "class":
		e.extractRubyClass(node)
	case "method":
		e.extractRubyMethod(node, false)
	case "singleton_method":
		e.extractRubyMethod(node, true)
	case "assignment":
		e.extractRubyAssignment(node)
	case "call", "method_call":
		e.extractRubyCall(node)
	default:
		e.visitRubyBody(node)
	}
}

// visitRubyBody descends into a node's named children looking for calls and
// nested definitions without emitting a node for the container itself.
func (e *extractor) visitRubyBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodeRuby(child)
		}
	}
}

// extractRubyModule handles a module node → KindModule.
func (e *extractor) extractRubyModule(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}
	mn := e.createNode(model.KindModule, name, node, nodeExtra{})
	if mn == nil {
		return
	}
	e.walkRubyBody(node, mn.ID)
}

// extractRubyClass handles a class node → KindClass, with superclass → extends
// and include/prepend/extend mixins → implements (emitted while walking body).
func (e *extractor) extractRubyClass(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}
	cn := e.createNode(model.KindClass, name, node, nodeExtra{})
	if cn == nil {
		return
	}

	// Superclass: `class A < B` → extends B.
	if sup := node.ChildByFieldName("superclass"); sup != nil {
		if base := rubyConstantName(sup); base != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    cn.ID,
				ReferenceName: base,
				ReferenceKind: model.EdgeExtends,
				Line:          int(sup.StartPoint().Row) + 1,
				Column:        int(sup.StartPoint().Column),
			})
		}
	}

	e.walkRubyBody(node, cn.ID)
}

// walkRubyBody pushes scopeID onto the node stack and visits the node's body
// statement children.
func (e *extractor) walkRubyBody(node *tsparse.Node, scopeID string) {
	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, scopeID)
	for i := 0; i < body.NamedChildCount(); i++ {
		if child := body.NamedChild(i); child != nil {
			e.visitNodeRuby(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractRubyMethod handles method / singleton_method nodes. At top level a
// method is a KindFunction; inside a class/module it is a KindMethod.
// singleton_method (def self.x) is marked isStatic.
func (e *extractor) extractRubyMethod(node *tsparse.Node, isStatic bool) {
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
	}

	fn := e.createNode(kind, name, node, nodeExtra{
		signature: rubySignature(node, isStatic),
		isStatic:  isStatic,
	})
	if fn == nil {
		return
	}

	body := node.ChildByFieldName("body")
	if body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		for i := 0; i < body.NamedChildCount(); i++ {
			if child := body.NamedChild(i); child != nil {
				e.visitNodeRuby(child)
			}
		}
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractRubyAssignment handles an assignment node.
//   - constant LHS → KindConstant
//   - instance_variable (@x) / class_variable (@@x) → KindField on enclosing class
//   - identifier LHS → KindField inside a class-like, else KindVariable
func (e *extractor) extractRubyAssignment(node *tsparse.Node) {
	left := node.ChildByFieldName("left")
	right := node.ChildByFieldName("right")
	if left == nil {
		return
	}

	switch left.Kind() {
	case "constant":
		e.createNode(model.KindConstant, left.Text(), node, nodeExtra{signature: rubyAssignSignature(right)})
	case "instance_variable", "class_variable":
		e.createRubyField(left.Text(), node)
	case "identifier":
		name := left.Text()
		kind := model.KindVariable
		if e.isInsideClassLike() {
			kind = model.KindField
		}
		e.createNode(kind, name, node, nodeExtra{signature: rubyAssignSignature(right)})
	}

	// Walk the right-hand side for calls (e.g. x = foo.bar).
	if right != nil {
		e.visitNodeRuby(right)
	}
}

// createRubyField creates a KindField attributed to the nearest enclosing class
// (the Ruby analog of Python's self.<attr>). Falls back to current scope when
// no enclosing class exists.
func (e *extractor) createRubyField(name string, node *tsparse.Node) {
	if name == "" {
		return
	}
	classID := e.nearestRubyClassID()
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

// nearestRubyClassID returns the ID of the nearest class-kind node on the stack.
func (e *extractor) nearestRubyClassID() string {
	for _, id := range slices.Backward(e.nodeStack) {

		for _, n := range e.nodes {
			if n.ID == id && n.Kind == model.KindClass {
				return id
			}
		}
	}
	return ""
}

// extractRubyCall handles a call node. Special call forms (require,
// require_relative, attr_*, include/prepend/extend) are handled distinctly;
// everything else emits an EdgeCalls reference from the top of the node stack.
func (e *extractor) extractRubyCall(node *tsparse.Node) {
	methodNode := node.ChildByFieldName("method")
	recv := node.ChildByFieldName("receiver")

	// Bare-method calls (no receiver) with known special names.
	if recv == nil && methodNode != nil && methodNode.Kind() == "identifier" {
		switch methodNode.Text() {
		case "require", "require_relative", "load":
			e.extractRubyRequire(node)
			return
		case "attr_accessor", "attr_reader", "attr_writer":
			e.extractRubyAttr(node)
			return
		case "include", "prepend", "extend":
			e.extractRubyMixin(node)
			return
		}
	}

	// Generic call → EdgeCalls from the current scope.
	if len(e.nodeStack) > 0 && methodNode != nil {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		if name := rubyCalleeName(node); name != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: name,
				ReferenceKind: model.EdgeCalls,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
		}
	}

	// Descend into arguments for nested calls.
	if args := node.ChildByFieldName("arguments"); args != nil {
		e.visitRubyBody(args)
	}
	// Descend into the receiver for chained calls (e.g. foo.bar.baz).
	if recv != nil && (recv.Kind() == "call" || recv.Kind() == "method_call") {
		e.extractRubyCall(recv)
	}
}

// extractRubyRequire handles require / require_relative / load: emits a
// KindImport node per required path plus an EdgeImports reference from the
// current scope. The import name is the path's basename without extension so it
// can resolve against same-named definition files (mirrors Python imports).
func (e *extractor) extractRubyRequire(node *tsparse.Node) {
	sig := strings.TrimSpace(node.Text())
	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}
	args := node.ChildByFieldName("arguments")
	if args == nil {
		return
	}
	for i := 0; i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg == nil || arg.Kind() != "string" {
			continue
		}
		path := rubyStringContent(arg)
		if path == "" {
			continue
		}
		name := rubyRequireName(path)
		if name == "" {
			continue
		}
		e.createNode(model.KindImport, name, node, nodeExtra{signature: sig})
		if parentID != "" {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    parentID,
				ReferenceName: name,
				ReferenceKind: model.EdgeImports,
				Line:          int(arg.StartPoint().Row) + 1,
				Column:        int(arg.StartPoint().Column),
			})
		}
	}
}

// extractRubyAttr handles attr_accessor / attr_reader / attr_writer: emits one
// KindProperty per symbol argument, attributed to the enclosing class.
func (e *extractor) extractRubyAttr(node *tsparse.Node) {
	args := node.ChildByFieldName("arguments")
	if args == nil {
		return
	}
	for i := 0; i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg == nil {
			continue
		}
		name := rubySymbolName(arg)
		if name == "" {
			continue
		}
		e.createNode(model.KindProperty, name, arg, nodeExtra{})
	}
}

// extractRubyMixin handles include / prepend / extend ModuleName: emits an
// EdgeImplements reference per module argument (the nearest analog for Ruby
// mixins; documented in the design).
func (e *extractor) extractRubyMixin(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	fromID := e.nodeStack[len(e.nodeStack)-1]
	args := node.ChildByFieldName("arguments")
	if args == nil {
		return
	}
	for i := 0; i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg == nil {
			continue
		}
		name := rubyConstantName(arg)
		if name == "" {
			continue
		}
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fromID,
			ReferenceName: name,
			ReferenceKind: model.EdgeImplements,
			Line:          int(arg.StartPoint().Row) + 1,
			Column:        int(arg.StartPoint().Column),
		})
	}
}

// rubyCalleeName resolves a call node to a callee name. Receivers `self` and
// `super` are stripped (implicit-receiver calls), mirroring Python's self/cls
// stripping. A constant receiver (Foo.bar) keeps the receiver prefix so the
// resolver can promote Foo.new → instantiates.
func rubyCalleeName(node *tsparse.Node) string {
	methodNode := node.ChildByFieldName("method")
	if methodNode == nil {
		return ""
	}
	method := methodNode.Text()
	if method == "" {
		return ""
	}
	recv := node.ChildByFieldName("receiver")
	if recv == nil {
		return method
	}
	switch recv.Kind() {
	case "self":
		return method
	case "identifier", "constant":
		r := recv.Text()
		if r == "self" {
			return method
		}
		return r + "." + method
	default:
		return method
	}
}

// rubyConstantName returns the simple constant name from a node that is a
// constant, a superclass wrapper, or a scope_resolution (A::B → B).
func rubyConstantName(node *tsparse.Node) string {
	switch node.Kind() {
	case "constant":
		return node.Text()
	case "superclass":
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); c != nil {
				if name := rubyConstantName(c); name != "" {
					return name
				}
			}
		}
		return ""
	case "scope_resolution":
		if nm := node.ChildByFieldName("name"); nm != nil {
			return nm.Text()
		}
	}
	t := node.Text()
	if idx := strings.LastIndex(t, "::"); idx >= 0 {
		return t[idx+2:]
	}
	return t
}

// rubySymbolName returns the attribute name from a :symbol argument.
func rubySymbolName(node *tsparse.Node) string {
	switch node.Kind() {
	case "simple_symbol":
		return strings.TrimPrefix(node.Text(), ":")
	case "string":
		return rubyStringContent(node)
	}
	return ""
}

// rubyStringContent returns the inner text of a string node.
func rubyStringContent(node *tsparse.Node) string {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "string_content" {
			return c.Text()
		}
	}
	return ""
}

// rubyRequireName reduces a require path to the basename without a .rb suffix
// (e.g. "lib/dog" → "dog", "json" → "json") so it can match a defining file.
func rubyRequireName(path string) string {
	path = strings.TrimSuffix(path, ".rb")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		path = path[idx+1:]
	}
	return path
}

// rubySignature renders a method's `def ...` header (name + parameters).
func rubySignature(node *tsparse.Node, isStatic bool) string {
	name := node.ChildByFieldName("name")
	if name == nil {
		return ""
	}
	prefix := "def "
	if isStatic {
		prefix = "def self."
	}
	sig := prefix + name.Text()
	if params := node.ChildByFieldName("parameters"); params != nil {
		sig += params.Text()
	}
	return sig
}

// rubyAssignSignature renders the right-hand side of an assignment as a
// signature ("= ClassName.new(...)"), mirroring pyAssignSignature.
func rubyAssignSignature(right *tsparse.Node) string {
	if right == nil {
		return ""
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

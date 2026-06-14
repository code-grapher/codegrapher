package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkJulia walks a parsed Julia (tree-sitter `julia`) file root and extracts
// symbols. Called by ExtractFile after the file node is emitted.
//
// Node type reference (tree-sitter-julia, verified via probe):
//
//	module_definition (field name=identifier; block child) → KindModule
//	function_definition (function token; signature=call_expression; block) → KindFunction
//	assignment with left=call_expression (short-form f(x)=…)        → KindFunction
//	struct_definition (optional mutable; type_head; block of typed_expression)
//	abstract_definition (type_head)                                 → KindInterface
//	const_statement (wraps assignment)                              → KindConstant
//	assignment with left=identifier (top-level x=…)                 → KindVariable
//	using_statement / import_statement (+ selected_import Mod: f)   → KindImport
//	call_expression (callee identifier or field_expression Mod.f; argument_list)
//	typed_expression (ident :: TypeIdent) — field/param/var type annotation
//	binary_expression with "<:" operator — subtype (T <: Super)
//	field_expression (field value; '.'; attribute)
//
// Multiple dispatch: many definitions of the same function name within one
// scope are collapsed onto a single KindFunction node (first occurrence wins;
// duplicate IDs are skipped by createNode's seenNodeIDs). Deterministic by
// source order. No per-overload signature matching.
func (e *extractor) walkJulia(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeJulia(child)
		}
	}
}

// visitNodeJulia dispatches a single statement node. Unknown kinds descend into
// their named children so calls/definitions nested inside control flow are
// still seen.
func (e *extractor) visitNodeJulia(node *tsparse.Node) {
	switch node.Kind() {
	case "module_definition":
		e.extractJuliaModule(node)
	case "function_definition":
		e.extractJuliaFunction(node)
	case "struct_definition":
		e.extractJuliaStruct(node)
	case "abstract_definition":
		e.extractJuliaAbstract(node)
	case "const_statement":
		e.extractJuliaConst(node)
	case "assignment":
		e.extractJuliaAssignment(node)
	case "using_statement", "import_statement":
		e.extractJuliaImport(node)
	case "call_expression":
		e.extractJuliaCall(node)
	default:
		e.visitJuliaBody(node)
	}
}

// visitJuliaBody descends into a node's named children looking for calls and
// nested definitions without emitting a node for the container itself.
func (e *extractor) visitJuliaBody(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodeJulia(child)
		}
	}
}

// extractJuliaModule handles module_definition → KindModule, pushed so members
// qualify as Module::name.
func (e *extractor) extractJuliaModule(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	mod := e.createNode(model.KindModule, name, node, nodeExtra{})
	if mod == nil {
		return
	}
	if body := juliaBlock(node); body != nil {
		e.nodeStack = append(e.nodeStack, mod.ID)
		for i := 0; i < body.NamedChildCount(); i++ {
			if child := body.NamedChild(i); child != nil {
				e.visitNodeJulia(child)
			}
		}
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractJuliaFunction handles a long-form function_definition.
func (e *extractor) extractJuliaFunction(node *tsparse.Node) {
	sig := juliaNamedChildOfKind(node, "signature")
	if sig == nil {
		return
	}
	// signature wraps a call_expression giving the name + params.
	call := juliaNamedChildOfKind(sig, "call_expression")
	if call == nil {
		return
	}
	name, params := juliaCallNameParams(call)
	if name == "" {
		return
	}
	// Multiple-dispatch dedup: collapse later methods of the same name onto the
	// first node in this scope.
	callerID := e.juliaExistingFuncID(name)
	if callerID == "" {
		fn := e.createNode(model.KindFunction, name, node, nodeExtra{
			signature: "function " + name + params,
		})
		if fn != nil {
			callerID = fn.ID
		}
	}
	// Emit ::T references for parameter type annotations.
	e.emitJuliaParamRefs(call, callerID)

	body := juliaBlock(node)
	if body != nil && callerID != "" {
		e.nodeStack = append(e.nodeStack, callerID)
		for i := 0; i < body.NamedChildCount(); i++ {
			if child := body.NamedChild(i); child != nil {
				e.visitNodeJulia(child)
			}
		}
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractJuliaStruct handles struct_definition → KindStruct (+ fields).
func (e *extractor) extractJuliaStruct(node *tsparse.Node) {
	head := node.ChildByFieldName("type_head")
	if head == nil {
		// type_head is not always a named field; find it positionally.
		head = juliaNamedChildOfKind(node, "type_head")
	}
	if head == nil {
		return
	}
	name, super := juliaTypeHead(head)
	if name == "" {
		return
	}
	st := e.createNode(model.KindStruct, name, node, nodeExtra{})
	if st == nil {
		return
	}
	// Supertype → extends.
	if super != "" {
		e.emitJuliaRef(st.ID, super, model.EdgeExtends, head)
	}
	// Fields.
	if body := juliaNamedChildOfKind(node, "block"); body != nil {
		e.nodeStack = append(e.nodeStack, st.ID)
		for i := 0; i < body.NamedChildCount(); i++ {
			child := body.NamedChild(i)
			if child == nil {
				continue
			}
			if child.Kind() == "typed_expression" {
				fname, ftype := juliaTypedExpr(child)
				if fname == "" {
					continue
				}
				fld := e.createNode(model.KindField, fname, child, nodeExtra{})
				if fld != nil && ftype != "" {
					e.emitJuliaRef(fld.ID, ftype, model.EdgeReferences, child)
				}
			} else if child.Kind() == "identifier" {
				// Untyped field `x`.
				e.createNode(model.KindField, child.Text(), child, nodeExtra{})
			}
		}
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractJuliaAbstract handles abstract_definition → KindInterface (closest
// available kind for an abstract type).
func (e *extractor) extractJuliaAbstract(node *tsparse.Node) {
	head := juliaNamedChildOfKind(node, "type_head")
	if head == nil {
		return
	}
	name, super := juliaTypeHead(head)
	if name == "" {
		return
	}
	ab := e.createNode(model.KindInterface, name, node, nodeExtra{isAbstract: true})
	if ab == nil {
		return
	}
	if super != "" {
		e.emitJuliaRef(ab.ID, super, model.EdgeExtends, head)
	}
}

// extractJuliaConst handles const_statement (wraps an assignment) → KindConstant.
func (e *extractor) extractJuliaConst(node *tsparse.Node) {
	assign := juliaNamedChildOfKind(node, "assignment")
	if assign == nil {
		return
	}
	left := assign.NamedChild(0)
	if left == nil || left.Kind() != "identifier" {
		return
	}
	e.createNode(model.KindConstant, left.Text(), node, nodeExtra{
		signature: juliaAssignSignature(assign),
	})
}

// extractJuliaAssignment handles a top-level/in-scope assignment. A left-hand
// call_expression is a short-form function definition; an identifier is a
// variable. The right-hand side is walked for calls.
func (e *extractor) extractJuliaAssignment(node *tsparse.Node) {
	left := node.NamedChild(0)
	if left == nil {
		return
	}

	switch left.Kind() {
	case "call_expression":
		// Short-form function: f(x) = …
		name, params := juliaCallNameParams(left)
		if name == "" {
			return
		}
		// Multiple-dispatch dedup: collapse later short-form methods onto the
		// first node of this name in scope.
		callerID := e.juliaExistingFuncID(name)
		if callerID == "" {
			fn := e.createNode(model.KindFunction, name, node, nodeExtra{
				signature: name + params,
			})
			if fn != nil {
				callerID = fn.ID
			}
		}
		e.emitJuliaParamRefs(left, callerID)
		// Walk RHS for calls attributed to the function.
		if right := juliaAssignRHS(node); right != nil && callerID != "" {
			e.nodeStack = append(e.nodeStack, callerID)
			e.visitNodeJulia(right)
			e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
		}
	case "identifier":
		e.createNode(model.KindVariable, left.Text(), node, nodeExtra{
			signature: juliaAssignSignature(node),
		})
		if right := juliaAssignRHS(node); right != nil {
			e.visitNodeJulia(right)
		}
	default:
		// Walk RHS for nested calls.
		if right := juliaAssignRHS(node); right != nil {
			e.visitNodeJulia(right)
		}
	}
}

// extractJuliaImport handles using_statement and import_statement. Emits a
// KindImport node per imported module/name plus an EdgeImports reference from
// the current parent scope.
func (e *extractor) extractJuliaImport(node *tsparse.Node) {
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

	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "identifier":
			emit(child.Text(), child)
		case "selected_import":
			// import Mod: f, g — emit each selected name.
			for j := 1; j < child.NamedChildCount(); j++ {
				sel := child.NamedChild(j)
				if sel != nil && sel.Kind() == "identifier" {
					emit(sel.Text(), sel)
				}
			}
		case "scoped_identifier", "field_expression":
			emit(child.Text(), child)
		}
	}
}

// extractJuliaCall handles a call_expression: emits an EdgeCalls reference from
// the top of the node stack, then descends into arguments for nested calls.
func (e *extractor) extractJuliaCall(node *tsparse.Node) {
	if len(e.nodeStack) > 0 {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		if name := juliaCalleeName(node); name != "" && !juliaBuiltins[name] {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    callerID,
				ReferenceName: name,
				ReferenceKind: model.EdgeCalls,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
			})
		}
	}
	if args := juliaNamedChildOfKind(node, "argument_list"); args != nil {
		e.visitJuliaBody(args)
	}
}

// emitJuliaParamRefs emits EdgeReferences for ::T type annotations in a
// signature's argument list.
func (e *extractor) emitJuliaParamRefs(sig *tsparse.Node, fromID string) {
	if fromID == "" {
		return
	}
	args := juliaNamedChildOfKind(sig, "argument_list")
	if args == nil {
		return
	}
	for i := 0; i < args.NamedChildCount(); i++ {
		a := args.NamedChild(i)
		if a == nil || a.Kind() != "typed_expression" {
			continue
		}
		if _, t := juliaTypedExpr(a); t != "" {
			e.emitJuliaRef(fromID, t, model.EdgeReferences, a)
		}
	}
}

// emitJuliaRef appends an unresolved reference of the given kind.
func (e *extractor) emitJuliaRef(fromID, name string, kind model.EdgeKind, at *tsparse.Node) {
	if fromID == "" || name == "" {
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

// juliaExistingFuncID returns the ID of an already-emitted function with the
// given name in the current scope (so dispatch-collapsed methods still
// contribute their body's calls). Empty when none exists.
func (e *extractor) juliaExistingFuncID(name string) string {
	want := e.buildQualifiedName(name)
	for i := len(e.nodes) - 1; i >= 0; i-- {
		if e.nodes[i].Kind == model.KindFunction && e.nodes[i].QualifiedName == want {
			return e.nodes[i].ID
		}
	}
	return ""
}

// juliaBlock returns the `block` child of a module/function definition.
func juliaBlock(node *tsparse.Node) *tsparse.Node {
	return juliaNamedChildOfKind(node, "block")
}

// juliaNamedChildOfKind returns the first named child of the given kind, or nil.
func juliaNamedChildOfKind(node *tsparse.Node, kind string) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// juliaCallNameParams extracts the function name and parameter-list text from a
// call_expression (the signature of a function definition).
func juliaCallNameParams(call *tsparse.Node) (name, params string) {
	if call.Kind() != "call_expression" {
		return "", ""
	}
	for i := 0; i < call.NamedChildCount(); i++ {
		c := call.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "identifier":
			if name == "" {
				name = c.Text()
			}
		case "argument_list":
			params = c.Text()
		}
	}
	return name, params
}

// juliaCalleeName resolves a call_expression's callee to a name. A bare
// identifier returns itself; a field_expression `Mod.f` returns "Mod.f".
func juliaCalleeName(call *tsparse.Node) string {
	for i := 0; i < call.NamedChildCount(); i++ {
		c := call.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "identifier":
			return c.Text()
		case "field_expression":
			return c.Text()
		case "argument_list":
			return ""
		}
	}
	return ""
}

// juliaTypeHead splits a struct/abstract type_head into the type name and its
// supertype (empty when none). type_head is either a bare identifier or a
// binary_expression `T <: Super`.
func juliaTypeHead(head *tsparse.Node) (name, super string) {
	if head.NamedChildCount() == 0 {
		return strings.TrimSpace(head.Text()), ""
	}
	inner := head.NamedChild(0)
	if inner == nil {
		return "", ""
	}
	switch inner.Kind() {
	case "identifier":
		return inner.Text(), ""
	case "binary_expression":
		// T <: Super
		var ids []string
		for i := 0; i < inner.NamedChildCount(); i++ {
			c := inner.NamedChild(i)
			if c != nil && c.Kind() == "identifier" {
				ids = append(ids, c.Text())
			}
		}
		if len(ids) >= 2 {
			return ids[0], ids[len(ids)-1]
		}
		if len(ids) == 1 {
			return ids[0], ""
		}
	}
	return strings.TrimSpace(inner.Text()), ""
}

// juliaTypedExpr splits a typed_expression `ident :: Type` into name and type.
func juliaTypedExpr(node *tsparse.Node) (name, typ string) {
	var ids []string
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && c.Kind() == "identifier" {
			ids = append(ids, c.Text())
		}
	}
	if len(ids) >= 2 {
		return ids[0], ids[len(ids)-1]
	}
	if len(ids) == 1 {
		return ids[0], ""
	}
	return "", ""
}

// juliaAssignRHS returns the right-hand side (last named child) of an assignment.
func juliaAssignRHS(node *tsparse.Node) *tsparse.Node {
	if node.NamedChildCount() < 2 {
		return nil
	}
	return node.NamedChild(node.NamedChildCount() - 1)
}

// juliaAssignSignature renders the RHS of an assignment as a "= value" signature.
func juliaAssignSignature(node *tsparse.Node) string {
	right := juliaAssignRHS(node)
	if right == nil {
		return ""
	}
	val := strings.TrimSpace(right.Text())
	if val == "" {
		return ""
	}
	if len(val) > 100 {
		return "= " + val[:100] + "..."
	}
	return "= " + val
}

// juliaBuiltins is a small set of Julia Base names that never resolve to a user
// node, so call references to them are skipped at extraction time.
var juliaBuiltins = map[string]bool{
	"println": true, "print": true, "push!": true, "pop!": true,
	"length": true, "map": true, "filter": true, "typeof": true,
	"reduce": true, "collect": true, "sort": true, "sort!": true,
	"string": true, "error": true, "throw": true, "isa": true,
	"convert": true, "zeros": true, "ones": true, "sum": true,
}

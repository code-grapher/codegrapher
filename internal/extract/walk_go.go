package extract

// walkGo walks the root node of a parsed Go source file and extracts all
// symbols. Called by ExtractFile after the file node has been emitted.
//
// Port of the Go-language branches in src/extraction/tree-sitter.ts, guided
// by the goExtractor config in src/extraction/languages/go.ts.
//
// Node type reference (tree-sitter-go):
//   source_file
//   function_declaration  — func foo(...)
//   method_declaration    — func (r *T) foo(...)
//   type_spec             — type Foo struct/interface/alias
//   struct_type
//   interface_type
//   import_declaration / import_spec / import_spec_list
//   var_declaration / var_spec
//   const_declaration / const_spec
//   call_expression       — used inside visitBodyGo for calls

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

func (e *extractor) walkGo(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		if child == nil {
			continue
		}
		e.visitNodeGo(child)
	}
}

func (e *extractor) visitNodeGo(node *tsparse.Node) {
	switch node.Kind() {
	case "function_declaration":
		e.extractGoFunction(node)
	case "method_declaration":
		e.extractGoMethod(node)
	case "type_declaration":
		// type_declaration wraps type_spec; visit all type_spec children
		for i := 0; i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child != nil && child.Kind() == "type_spec" {
				// Pass the type_declaration as the docstring anchor
				e.extractGoTypeSpec(child, node)
			}
		}
	case "import_declaration":
		e.extractGoImport(node)
	case "var_declaration", "const_declaration":
		e.extractGoVarConst(node)
	case "call_expression":
		// Top-level call_expression shouldn't happen in Go, but handle it.
		e.extractGoCall(node)
	default:
		// Recurse into named children for things like source_file's top-level blocks.
		for i := 0; i < node.NamedChildCount(); i++ {
			if child := node.NamedChild(i); child != nil {
				e.visitNodeGo(child)
			}
		}
	}
}

// extractGoFunction handles function_declaration nodes.
func (e *extractor) extractGoFunction(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	docstring := e.lookupDoc(node)
	sig := e.goSignature(node)
	isExported := isGoExported(name)
	returnType := e.goReturnType(node)

	fn := e.createNode(model.KindFunction, name, node, nodeExtra{
		docstring:  docstring,
		signature:  sig,
		isExported: isExported,
		returnType: returnType,
	})
	if fn == nil {
		return
	}

	body := node.ChildByFieldName("body")
	if body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		e.visitBodyGo(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractGoMethod handles method_declaration nodes.
func (e *extractor) extractGoMethod(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	receiverType := goReceiverType(node)
	docstring := e.lookupDoc(node)
	sig := e.goSignature(node)
	returnType := e.goReturnType(node)

	extra := nodeExtra{
		docstring:  docstring,
		signature:  sig,
		returnType: returnType,
	}
	if receiverType != "" {
		extra.qualifiedName = receiverType + "::" + name
	}

	mn := e.createNode(model.KindMethod, name, node, extra)
	if mn == nil {
		return
	}

	// Add a contains edge from the owning struct if it exists in this file.
	if receiverType != "" {
		e.addReceiverContains(receiverType, mn.ID)
	}

	body := node.ChildByFieldName("body")
	if body != nil {
		e.nodeStack = append(e.nodeStack, mn.ID)
		e.visitBodyGo(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// addReceiverContains adds a contains edge from the named struct/class/enum
// in the same file to targetID, mirroring the upstream behavior for
// receiver-typed methods (Go, Rust).
func (e *extractor) addReceiverContains(receiverType, targetID string) {
	for _, n := range e.nodes {
		if n.Name == receiverType && n.FilePath == e.filePath &&
			(n.Kind == model.KindStruct || n.Kind == model.KindClass ||
				n.Kind == model.KindEnum || n.Kind == model.KindTrait) {
			e.edges = append(e.edges, model.Edge{
				Source: n.ID,
				Target: targetID,
				Kind:   model.EdgeContains,
			})
			return
		}
	}
}

// extractGoTypeSpec handles type_spec nodes (type Foo struct/interface/alias).
// docstringAnchor is the parent type_declaration node; docstrings are looked up
// from the anchor's start line so they match the upstream's
// getPrecedingDocstring(type_spec) == null behavior (the comment is a sibling of
// type_declaration, not type_spec, so type_spec has no preceding named sibling in
// web-tree-sitter). By using the type_declaration as anchor we would still find it,
// but the upstream never does — so we pass nil to skip docstrings for type nodes.
func (e *extractor) extractGoTypeSpec(node *tsparse.Node, _ *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	typeChild := node.ChildByFieldName("type")
	if typeChild == nil {
		// plain type alias with no explicit type field
		e.extractGoTypeAlias(node, name)
		return
	}

	switch typeChild.Kind() {
	case "struct_type":
		e.extractGoStruct(node, name, typeChild)
	case "interface_type":
		e.extractGoInterface(node, name, typeChild)
	default:
		e.extractGoTypeAlias(node, name)
	}
}

func (e *extractor) extractGoStruct(typeSpecNode *tsparse.Node, name string, structType *tsparse.Node) {
	// Docstrings for struct/interface/type_alias are NOT extracted: the upstream's
	// getPrecedingDocstring(type_spec) returns null because type_spec has no
	// preceding named sibling within type_declaration. The comment is a sibling of
	// type_declaration at source_file level, not of type_spec.
	docstring := ""
	isExported := isGoExported(name)
	sn := e.createNode(model.KindStruct, name, typeSpecNode, nodeExtra{
		docstring:  docstring,
		isExported: isExported,
	})
	if sn == nil {
		return
	}

	// Extract embedding (Go struct embedding)
	e.extractGoStructEmbedding(structType, sn.ID)

	// Push scope and visit struct body for fields (we don't extract fields as
	// first-class nodes per the golden output — only methods later reference them).
	// Actually the golden shows no field nodes; the upstream doesn't extract Go struct fields.
	// Just visit body children so nested types get extracted.
	body := structType.ChildByFieldName("body")
	if body == nil {
		body = structType
	}
	e.nodeStack = append(e.nodeStack, sn.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		if child := body.NamedChild(i); child != nil {
			// Visit for nested types only (Go structs don't extract field nodes)
			e.visitNodeGo(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

func (e *extractor) extractGoInterface(typeSpecNode *tsparse.Node, name string, ifaceType *tsparse.Node) {
	// Same as extractGoStruct: upstream's getPrecedingDocstring(type_spec) returns null.
	docstring := ""
	isExported := isGoExported(name)
	ifn := e.createNode(model.KindInterface, name, typeSpecNode, nodeExtra{
		docstring:  docstring,
		isExported: isExported,
	})
	if ifn == nil {
		return
	}

	// Extract interface method specs as method nodes.
	e.extractGoInterfaceMethods(ifaceType, ifn.ID)
}

// extractGoInterfaceMethods extracts method_elem/method_spec nodes from an
// interface_type body as method nodes contained by the interface.
// Port of extractGoInterfaceMethods in tree-sitter.ts.
func (e *extractor) extractGoInterfaceMethods(ifaceType *tsparse.Node, ifaceID string) {
	e.nodeStack = append(e.nodeStack, ifaceID)
	for i := 0; i < ifaceType.NamedChildCount(); i++ {
		m := ifaceType.NamedChild(i)
		if m == nil {
			continue
		}
		if m.Kind() != "method_elem" && m.Kind() != "method_spec" {
			continue
		}
		nameNode := m.ChildByFieldName("name")
		if nameNode == nil && m.NamedChildCount() > 0 {
			nameNode = m.NamedChild(0)
		}
		if nameNode == nil {
			continue
		}
		mname := nameNode.Text()
		if mname == "" {
			continue
		}
		sig := e.goSignature(m)
		e.createNode(model.KindMethod, mname, m, nodeExtra{signature: sig})
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

func (e *extractor) extractGoTypeAlias(node *tsparse.Node, name string) {
	// Same as extractGoStruct: upstream's getPrecedingDocstring(type_spec) returns null.
	docstring := ""
	isExported := isGoExported(name)
	e.createNode(model.KindTypeAlias, name, node, nodeExtra{
		docstring:  docstring,
		isExported: isExported,
	})
}

// extractGoStructEmbedding emits extends/implements references for embedded
// types in a Go struct body (e.g. `type DB struct { *Head; Queryable }`).
func (e *extractor) extractGoStructEmbedding(structType *tsparse.Node, structID string) {
	body := structType.ChildByFieldName("body")
	if body == nil {
		return
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		field := body.NamedChild(i)
		if field == nil || field.Kind() != "field_declaration" {
			continue
		}
		// An embedded field has no explicit name — only a type.
		// Check if the field_declaration has only a type child (no name field).
		nameNode := field.ChildByFieldName("name")
		if nameNode != nil {
			continue // named field, not embedding
		}
		typeNode := field.ChildByFieldName("type")
		if typeNode == nil {
			continue
		}
		typeName := goBaseTypeName(typeNode)
		if typeName == "" {
			continue
		}
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    structID,
			ReferenceName: typeName,
			ReferenceKind: model.EdgeExtends,
			Line:          int(typeNode.StartPoint().Row) + 1,
			Column:        int(typeNode.StartPoint().Column),
		})
	}
}

// extractGoImport handles import_declaration nodes.
func (e *extractor) extractGoImport(node *tsparse.Node) {
	parentID := ""
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}

	extractSpec := func(spec *tsparse.Node) {
		// Find the interpreted_string_literal child
		for i := 0; i < spec.NamedChildCount(); i++ {
			child := spec.NamedChild(i)
			if child == nil {
				continue
			}
			if child.Kind() == "interpreted_string_literal" {
				importPath := strings.Trim(child.Text(), `"`)
				if importPath == "" {
					continue
				}
				sig := strings.TrimSpace(spec.Text())
				e.createNode(model.KindImport, importPath, spec, nodeExtra{
					signature: sig,
				})
				if parentID != "" {
					e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
						FromNodeID:    parentID,
						ReferenceName: importPath,
						ReferenceKind: model.EdgeImports,
						Line:          int(spec.StartPoint().Row) + 1,
						Column:        int(spec.StartPoint().Column),
					})
				}
				return
			}
		}
	}

	// Check for grouped imports (import_spec_list)
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "import_spec_list" {
			for j := 0; j < child.NamedChildCount(); j++ {
				spec := child.NamedChild(j)
				if spec != nil && spec.Kind() == "import_spec" {
					extractSpec(spec)
				}
			}
			return
		}
		if child.Kind() == "import_spec" {
			extractSpec(child)
			return
		}
	}
}

// extractGoVarConst handles var_declaration and const_declaration nodes.
func (e *extractor) extractGoVarConst(node *tsparse.Node) {
	isConst := node.Kind() == "const_declaration"
	docstring := e.lookupDoc(node)

	for i := 0; i < node.NamedChildCount(); i++ {
		spec := node.NamedChild(i)
		if spec == nil {
			continue
		}
		specKind := spec.Kind()
		if specKind != "var_spec" && specKind != "const_spec" {
			continue
		}

		nameNode := spec.NamedChild(0)
		if nameNode == nil || nameNode.Kind() != "identifier" {
			continue
		}
		name := nameNode.Text()

		kind := model.KindVariable
		if isConst || specKind == "const_spec" {
			kind = model.KindConstant
		}

		// Build signature from value if present
		var sig string
		if spec.NamedChildCount() > 1 {
			valueNode := spec.NamedChild(spec.NamedChildCount() - 1)
			if valueNode != nil {
				initValue := valueNode.Text()
				if len(initValue) > 100 {
					sig = "= " + initValue[:100] + "..."
				} else if initValue != "" {
					sig = "= " + initValue
				}
			}
		}

		varNode := e.createNode(kind, name, spec, nodeExtra{
			docstring: docstring,
			signature: sig,
		})

		// Walk the value field for calls (e.g. package-level var initialized with a function call)
		valueField := spec.ChildByFieldName("value")
		if valueField != nil {
			if varNode != nil {
				e.nodeStack = append(e.nodeStack, varNode.ID)
			}
			e.visitBodyGo(valueField)
			if varNode != nil {
				e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
			}
		}
	}

	// Handle short_var_declaration (:=) — these appear inside function bodies,
	// not at top level, so they're handled in visitBodyGo instead.
}

// visitBodyGo walks a function/method body for calls, instantiations, and
// nested function/struct/interface declarations.
func (e *extractor) visitBodyGo(body *tsparse.Node) {
	tsparse.Walk(body, func(node *tsparse.Node) {
		switch node.Kind() {
		case "call_expression":
			e.extractGoCall(node)
		case "composite_literal":
			e.extractGoCompositeInstantiation(node)
		case "function_declaration":
			// Named nested function — extract as a node and skip further recursion
			// (Walk will still visit children, but extractGoFunction handles body itself)
			// Actually Walk doesn't support early termination, so we'd double-count.
			// The upstream only extracts named functions at body scope if they're
			// function_declaration type with a non-anonymous name. Since Go doesn't
			// have nested named function declarations (only func literals), this is
			// mainly for completeness.
		case "func_literal":
			// Anonymous function literals don't get nodes, but their calls do.
			// Walk will naturally recurse into them.
		}
	})
}

// extractGoCall extracts a call_expression for Go.
// Port of extractCall for Go in tree-sitter.ts.
func (e *extractor) extractGoCall(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	callerID := e.nodeStack[len(e.nodeStack)-1]

	funcNode := node.ChildByFieldName("function")
	if funcNode == nil && node.NamedChildCount() > 0 {
		funcNode = node.NamedChild(0)
	}
	if funcNode == nil {
		return
	}

	var calleeName string

	switch funcNode.Kind() {
	case "selector_expression":
		// Method call: obj.method() or pkg.Function()
		fieldNode := funcNode.ChildByFieldName("field")
		if fieldNode == nil {
			break
		}
		methodName := fieldNode.Text()

		operandNode := funcNode.ChildByFieldName("operand")
		if operandNode == nil && funcNode.NamedChildCount() > 0 {
			operandNode = funcNode.NamedChild(0)
		}

		if operandNode != nil {
			switch operandNode.Kind() {
			case "identifier", "field_identifier":
				receiverName := operandNode.Text()
				skipReceivers := map[string]bool{"self": true, "this": true, "cls": true, "super": true}
				if skipReceivers[receiverName] {
					calleeName = methodName
				} else {
					calleeName = receiverName + "." + methodName
				}
			case "call_expression":
				// Chained factory call: New().Method()
				innerFuncNode := operandNode.ChildByFieldName("function")
				if innerFuncNode != nil && innerFuncNode.Kind() == "identifier" {
					innerCallee := innerFuncNode.Text()
					calleeName = innerCallee + "()." + methodName
				} else {
					calleeName = methodName
				}
			default:
				calleeName = methodName
			}
		} else {
			calleeName = methodName
		}

	case "identifier":
		calleeName = funcNode.Text()
		// Normalize parenthesized type conversions like (*T)
		if m := reGoTypeConv.FindStringSubmatch(calleeName); m != nil {
			calleeName = m[1]
		}

	default:
		calleeName = funcNode.Text()
		if m := reGoTypeConv.FindStringSubmatch(calleeName); m != nil {
			calleeName = m[1]
		}
	}

	if calleeName == "" {
		return
	}

	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    callerID,
		ReferenceName: calleeName,
		ReferenceKind: model.EdgeCalls,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// reGoTypeConv matches `(*T)` or `(T)` parenthesized type conversion callees.
var reGoTypeConv = regexp.MustCompile(`^\(\s*\*?\s*([A-Za-z_][\w.]*)\s*\)$`)

// extractGoCompositeInstantiation handles composite_literal nodes in Go:
// Widget{...} → instantiates reference to Widget.
func (e *extractor) extractGoCompositeInstantiation(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	fromID := e.nodeStack[len(e.nodeStack)-1]

	typeNode := node.ChildByFieldName("type")
	if typeNode == nil && node.NamedChildCount() > 0 {
		typeNode = node.NamedChild(0)
	}
	if typeNode == nil {
		return
	}
	if typeNode.Kind() != "type_identifier" && typeNode.Kind() != "qualified_type" {
		return
	}
	goType := strings.TrimSpace(typeNode.Text())
	// Strip generic args: Box[T]{} → Box
	if idx := strings.Index(goType, "["); idx > 0 {
		goType = strings.TrimSpace(goType[:idx])
	}
	if goType == "" {
		return
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    fromID,
		ReferenceName: goType,
		ReferenceKind: model.EdgeInstantiates,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// goSignature builds the signature string for a Go function/method node:
// "<params> <result>" (both from field names "parameters" and "result").
func (e *extractor) goSignature(node *tsparse.Node) string {
	params := node.ChildByFieldName("parameters")
	result := node.ChildByFieldName("result")
	if params == nil {
		return ""
	}
	sig := params.Text()
	if result != nil {
		sig += " " + result.Text()
	}
	return sig
}

// goReturnType extracts the normalized bare return type name for a Go function.
// Port of extractGoReturnType in languages/go.ts.
func (e *extractor) goReturnType(node *tsparse.Node) string {
	result := node.ChildByFieldName("result")
	if result == nil {
		return ""
	}
	// Multi-return (parameter_list): take first parameter_declaration's type
	if result.Kind() == "parameter_list" {
		for i := 0; i < result.NamedChildCount(); i++ {
			first := result.NamedChild(i)
			if first != nil && first.Kind() == "parameter_declaration" {
				typeNode := first.ChildByFieldName("type")
				if typeNode == nil && first.NamedChildCount() > 0 {
					typeNode = first.NamedChild(0)
				}
				if typeNode != nil {
					return goBaseTypeName(typeNode)
				}
			}
		}
		return ""
	}
	// Unwrap pointer type
	if result.Kind() == "pointer_type" {
		for i := 0; i < result.NamedChildCount(); i++ {
			child := result.NamedChild(i)
			if child != nil {
				return goBaseTypeName(child)
			}
		}
	}
	return goBaseTypeName(result)
}

// goBaseTypeName extracts the bare type name from a Go type node,
// stripping pointer *, generic args, and package qualifiers.
func goBaseTypeName(node *tsparse.Node) string {
	text := strings.TrimSpace(node.Text())
	text = strings.TrimPrefix(text, "*")
	// Strip generic args Foo[T] → Foo
	if idx := strings.Index(text, "["); idx > 0 {
		text = strings.TrimSpace(text[:idx])
	}
	// Strip generic angle brackets Foo<T> → Foo (shouldn't appear in Go but just in case)
	if idx := strings.Index(text, "<"); idx > 0 {
		text = strings.TrimSpace(text[:idx])
	}
	// Take last segment of qualified type: pkg.Foo → Foo
	if last := lastIdentSegment(text); last != "" {
		text = last
	}
	if !reValidIdent.MatchString(text) {
		return ""
	}
	return text
}

var reValidIdent = regexp.MustCompile(`^[A-Za-z_]\w*$`)

// lastIdentSegment returns the last dot-separated identifier segment.
func lastIdentSegment(s string) string {
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		return s[idx+1:]
	}
	return s
}

// goReceiverType extracts the receiver type name from a Go method_declaration.
// Port of getReceiverType in languages/go.ts.
func goReceiverType(node *tsparse.Node) string {
	receiver := node.ChildByFieldName("receiver")
	if receiver == nil {
		return ""
	}
	text := receiver.Text()
	// Match (sl *Type), (sl Type), (*Type), (Type), (s *Stack[T])
	m := reGoReceiver.FindStringSubmatch(text)
	if m != nil {
		return m[1]
	}
	return ""
}

var reGoReceiver = regexp.MustCompile(`\(\s*(?:[A-Za-z_]\w*\s+)?\*?\s*([A-Za-z_]\w*)`)

// isGoExported reports whether a Go symbol name is exported (starts with uppercase).
func isGoExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	c := name[0]
	return c >= 'A' && c <= 'Z'
}

// lookupDoc finds the docstring for a node by looking for a comment ending on
// startLine-1 in the comment index. A richer approach would also check
// the first non-blank comment in the same block, but the end-line index
// matches the upstream's getPrecedingDocstring behavior for Go.
func (e *extractor) lookupDoc(node *tsparse.Node) string {
	startLine := int(node.StartPoint().Row) + 1
	return lookupDocstring(e.commentByEndLine, startLine)
}

// ExtractGoRoutes extracts net/http HandleFunc/Handle route registrations
// from Go source, mirroring the goResolver.extract() in
// src/resolution/frameworks/go.ts.
//
// This is called from ExtractFile after the main tree-sitter walk.
func ExtractGoRoutes(filePath string, content []byte, fileNodeID string) ([]model.Node, []model.UnresolvedReference) {
	safe := stripGoComments(string(content))

	routeRegex := regexp.MustCompile(`\b\w+\.(GET|POST|PUT|PATCH|DELETE|OPTIONS|HEAD|Get|Post|Put|Patch|Delete|Handle|HandleFunc)\s*\(\s*"([^"]+)"\s*,\s*([^)]+)\)`)
	matches := routeRegex.FindAllStringSubmatchIndex(safe, -1)

	var nodes []model.Node
	var refs []model.UnresolvedReference

	for _, loc := range matches {
		full := safe[loc[0]:loc[1]]
		rawMethod := safe[loc[2]:loc[3]]
		routePath := safe[loc[4]:loc[5]]
		handlerExpr := safe[loc[6]:loc[7]]

		// Count newlines before match to get 1-indexed line
		line := strings.Count(safe[:loc[0]], "\n") + 1

		method := rawMethod
		if method == "Handle" || method == "HandleFunc" {
			method = "ANY"
		} else {
			method = strings.ToUpper(method)
		}

		id := model.RouteNodeID(filePath, line, method, routePath)
		qualName := fmt.Sprintf("%s::route:%s", filePath, routePath)

		n := model.Node{
			ID:            id,
			Kind:          model.KindRoute,
			Name:          method + " " + routePath,
			QualifiedName: qualName,
			FilePath:      filePath,
			Language:      model.LangGo,
			StartLine:     line,
			EndLine:       line,
			StartColumn:   0,
			EndColumn:     len(full),
		}
		nodes = append(nodes, n)

		// Add contains edge via a containment reference (we emit as edges later)
		handlerName := extractGoTailIdent(handlerExpr)
		if handlerName != "" {
			refs = append(refs, model.UnresolvedReference{
				FromNodeID:    id,
				ReferenceName: handlerName,
				ReferenceKind: model.EdgeReferences,
				Line:          line,
				Column:        0,
				FilePath:      filePath,
				Language:      model.LangGo,
			})
		}
	}
	return nodes, refs
}

// extractGoTailIdent extracts the last identifier from an expression like
// `pkg.Sub.handler` or `handler`. Port of extractGoTailIdent in go.ts.
func extractGoTailIdent(expr string) string {
	cleaned := strings.TrimSpace(expr)
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.TrimSuffix(cleaned, "()")
	m := reGoTailIdent.FindStringSubmatch(cleaned)
	if m != nil {
		return m[1]
	}
	return ""
}

var reGoTailIdent = regexp.MustCompile(`(?:\.|^)([A-Za-z_][A-Za-z0-9_]*)$`)

// stripGoComments replaces line comments with spaces (preserving newlines and
// byte offsets) so the route regex doesn't match inside comments.
// This is a simplified version of stripCommentsForRegex from the upstream.
func stripGoComments(src string) string {
	var sb strings.Builder
	sb.Grow(len(src))
	i := 0
	for i < len(src) {
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			// Line comment: replace until newline
			for i < len(src) && src[i] != '\n' {
				sb.WriteByte(' ')
				i++
			}
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			// Block comment: replace with spaces, preserve newlines
			sb.WriteByte(' ')
			sb.WriteByte(' ')
			i += 2
			for i < len(src) {
				if i+1 < len(src) && src[i] == '*' && src[i+1] == '/' {
					sb.WriteByte(' ')
					sb.WriteByte(' ')
					i += 2
					break
				}
				if src[i] == '\n' {
					sb.WriteByte('\n')
				} else {
					sb.WriteByte(' ')
				}
				i++
			}
			continue
		}
		// String literal: preserve verbatim (route paths are in strings)
		if src[i] == '"' {
			sb.WriteByte(src[i])
			i++
			for i < len(src) && src[i] != '"' {
				if src[i] == '\\' && i+1 < len(src) {
					sb.WriteByte(src[i])
					sb.WriteByte(src[i+1])
					i += 2
					continue
				}
				sb.WriteByte(src[i])
				i++
			}
			if i < len(src) {
				sb.WriteByte(src[i]) // closing "
				i++
			}
			continue
		}
		sb.WriteByte(src[i])
		i++
	}
	return sb.String()
}

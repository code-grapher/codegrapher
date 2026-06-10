package extract

// walkTS walks the root node of a parsed TypeScript/JavaScript file and
// extracts all symbols. Called by ExtractFile after the file node has been
// emitted.
//
// Port of the TypeScript/JavaScript branches in src/extraction/tree-sitter.ts,
// guided by the typescriptExtractor / javascriptExtractor configs in
// src/extraction/languages/{typescript,javascript}.ts.
//
// Node type reference (tree-sitter-typescript):
//   program
//   function_declaration / arrow_function / function_expression
//   class_declaration / abstract_class_declaration
//   method_definition / public_field_definition / field_definition
//   interface_declaration
//   enum_declaration
//   type_alias_declaration
//   import_statement
//   lexical_declaration / variable_declaration
//   call_expression / new_expression
//   export_statement

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

func (e *extractor) walkTS(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		if child == nil {
			continue
		}
		e.visitNodeTS(child)
	}
}

func (e *extractor) visitNodeTS(node *tsparse.Node) {
	kind := node.Kind()

	switch kind {
	case "function_declaration", "arrow_function", "function_expression":
		e.extractTSFunction(node, "")
	case "class_declaration", "abstract_class_declaration":
		e.extractTSClass(node)
	case "method_definition", "public_field_definition", "field_definition":
		if e.isInsideClassLike() {
			e.extractTSMethod(node)
		} else {
			// Object literal method outside class — skip (noise)
			body := node.ChildByFieldName("body")
			if body != nil {
				e.visitTSBody(body)
			}
		}
	case "interface_declaration":
		e.extractTSInterface(node)
	case "enum_declaration":
		e.extractTSEnum(node)
	case "type_alias_declaration":
		e.extractTSTypeAlias(node)
	case "import_statement":
		e.extractTSImport(node)
	case "export_statement":
		// Re-export: export { X } from './y'
		sourceField := node.ChildByFieldName("source")
		if sourceField != nil {
			if len(e.nodeStack) > 0 {
				parentID := e.nodeStack[len(e.nodeStack)-1]
				e.emitTSReExportRefs(node, parentID)
			}
		} else {
			// Descend into the inner declaration, marking export context.
			prev := e.insideExport
			e.insideExport = true
			for i := 0; i < node.NamedChildCount(); i++ {
				if child := node.NamedChild(i); child != nil {
					e.visitNodeTS(child)
				}
			}
			e.insideExport = prev
		}
	case "lexical_declaration", "variable_declaration":
		if !e.isInsideClassLike() {
			e.extractTSVariable(node)
		} else {
			// Inside class — skip (handled by method_definition/field_definition)
			for i := 0; i < node.NamedChildCount(); i++ {
				if child := node.NamedChild(i); child != nil {
					e.visitNodeTS(child)
				}
			}
		}
	case "call_expression":
		e.extractTSCall(node)
	case "new_expression":
		e.extractTSInstantiation(node)
	default:
		// Recurse into named children
		for i := 0; i < node.NamedChildCount(); i++ {
			if child := node.NamedChild(i); child != nil {
				e.visitNodeTS(child)
			}
		}
	}
}

// extractTSFunction handles function_declaration, arrow_function, function_expression.
// nameOverride is set when the function is extracted from a variable declarator.
func (e *extractor) extractTSFunction(node *tsparse.Node, nameOverride string) {
	var name string
	if nameOverride != "" {
		name = nameOverride
	} else {
		switch node.Kind() {
		case "arrow_function", "function_expression":
			// These get their names from the parent variable_declarator (handled in extractTSVariable)
			name = "<anonymous>"
		default:
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil {
				name = nameNode.Text()
			}
		}
	}

	if name == "" || name == "<anonymous>" {
		// Visit body for calls but don't emit a node
		body := resolveTSBody(node)
		if body != nil {
			e.visitTSBody(body)
		}
		return
	}

	// When inside an export_statement, the function node is a child of export_statement
	// and has no preceding named sibling within it, so the upstream's
	// getPrecedingDocstring returns null.
	docstring := ""
	if !e.insideExport {
		docstring = e.lookupDoc(node)
	}
	sig := e.tsSignature(node)
	isExported := e.isTSExported(node)
	isAsync := tsBoolChild(node, "async")

	fn := e.createNode(model.KindFunction, name, node, nodeExtra{
		docstring:  docstring,
		signature:  sig,
		isExported: isExported,
		isAsync:    isAsync,
	})
	if fn == nil {
		return
	}

	body := resolveTSBody(node)
	if body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		e.visitTSBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractTSClass handles class_declaration and abstract_class_declaration.
func (e *extractor) extractTSClass(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	// When inside an export_statement, class_declaration has no previousNamedSibling
	// within export_statement, so the upstream's getPrecedingDocstring returns null.
	docstring := ""
	if !e.insideExport {
		docstring = e.lookupDoc(node)
	}
	isExported := e.isTSExported(node)

	cn := e.createNode(model.KindClass, name, node, nodeExtra{
		docstring:  docstring,
		isExported: isExported,
	})
	if cn == nil {
		return
	}

	// Visit body
	body := node.ChildByFieldName("body")
	if body == nil {
		body = node
	}
	e.nodeStack = append(e.nodeStack, cn.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		if child := body.NamedChild(i); child != nil {
			e.visitNodeTS(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractTSMethod handles method_definition, public_field_definition, field_definition.
func (e *extractor) extractTSMethod(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	// Skip if it's an arrow function/function expression field — extract as function with name
	kind := model.KindMethod
	valueNode := node.ChildByFieldName("value")
	if valueNode != nil &&
		(valueNode.Kind() == "arrow_function" || valueNode.Kind() == "function_expression") &&
		(node.Kind() == "public_field_definition" || node.Kind() == "field_definition") {
		// Extract as function under the class scope
		e.extractTSFunction(valueNode, name)
		return
	}

	docstring := e.lookupDoc(node)
	sig := e.tsSignature(node)
	isAsync := tsBoolChild(node, "async")
	isStatic := tsBoolChild(node, "static")
	vis := e.tsVisibility(node)

	mn := e.createNode(kind, name, node, nodeExtra{
		docstring:  docstring,
		signature:  sig,
		isAsync:    isAsync,
		isStatic:   isStatic,
		visibility: vis,
	})
	if mn == nil {
		return
	}

	body := resolveTSBody(node)
	if body != nil {
		e.nodeStack = append(e.nodeStack, mn.ID)
		e.visitTSBody(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractTSInterface handles interface_declaration.
func (e *extractor) extractTSInterface(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	// When inside an export_statement, interface_declaration has no preceding named sibling
	// within export_statement, so the upstream's getPrecedingDocstring returns null.
	docstring := ""
	if !e.insideExport {
		docstring = e.lookupDoc(node)
	}
	isExported := e.isTSExported(node)

	ifn := e.createNode(model.KindInterface, name, node, nodeExtra{
		docstring:  docstring,
		isExported: isExported,
	})
	if ifn == nil {
		return
	}

	// Visit body
	body := node.ChildByFieldName("body")
	if body == nil {
		body = node
	}
	e.nodeStack = append(e.nodeStack, ifn.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		if child := body.NamedChild(i); child != nil {
			e.visitNodeTS(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractTSEnum handles enum_declaration.
func (e *extractor) extractTSEnum(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	// When inside an export_statement, enum_declaration has no preceding named sibling
	// within export_statement, so the upstream's getPrecedingDocstring returns null.
	docstring := ""
	if !e.insideExport {
		docstring = e.lookupDoc(node)
	}
	isExported := e.isTSExported(node)

	en := e.createNode(model.KindEnum, name, node, nodeExtra{
		docstring:  docstring,
		isExported: isExported,
	})
	if en == nil {
		return
	}

	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, en.ID)
	// Extract enum members
	for i := 0; i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "property_identifier" || child.Kind() == "enum_assignment" {
			e.extractTSEnumMember(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

func (e *extractor) extractTSEnumMember(node *tsparse.Node) {
	// Try field name first
	nameNode := node.ChildByFieldName("name")
	if nameNode != nil {
		e.createNode(model.KindEnumMember, nameNode.Text(), node, nodeExtra{})
		return
	}
	// Identifier-like children
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child != nil {
			switch child.Kind() {
			case "simple_identifier", "identifier", "property_identifier":
				e.createNode(model.KindEnumMember, child.Text(), child, nodeExtra{})
				return
			}
		}
	}
	// Leaf with no named children
	if node.NamedChildCount() == 0 {
		e.createNode(model.KindEnumMember, node.Text(), node, nodeExtra{})
	}
}

// extractTSTypeAlias handles type_alias_declaration.
func (e *extractor) extractTSTypeAlias(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}

	// When inside an export_statement, type_alias_declaration has no preceding named sibling
	// within export_statement, so the upstream's getPrecedingDocstring returns null.
	docstring := ""
	if !e.insideExport {
		docstring = e.lookupDoc(node)
	}
	isExported := e.isTSExported(node)

	e.createNode(model.KindTypeAlias, name, node, nodeExtra{
		docstring:  docstring,
		isExported: isExported,
	})
}

// extractTSImport handles import_statement.
func (e *extractor) extractTSImport(node *tsparse.Node) {
	sourceField := node.ChildByFieldName("source")
	if sourceField == nil {
		return
	}
	moduleName := strings.Trim(sourceField.Text(), `'"`)
	if moduleName == "" {
		return
	}
	sig := strings.TrimSpace(node.Text())
	e.createNode(model.KindImport, moduleName, node, nodeExtra{
		signature: sig,
	})

	if len(e.nodeStack) > 0 {
		parentID := e.nodeStack[len(e.nodeStack)-1]
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    parentID,
			ReferenceName: moduleName,
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
		// Emit import binding refs (link each imported name to its definition)
		e.emitTSImportBindingRefs(node, parentID)
	}
}

// extractTSVariable handles lexical_declaration and variable_declaration.
func (e *extractor) extractTSVariable(node *tsparse.Node) {
	isConst := false
	// For lexical_declaration, check for 'const' token
	if node.Kind() == "lexical_declaration" {
		for i := 0; i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child != nil && child.Kind() == "const" {
				isConst = true
				break
			}
		}
	}

	// When inside an export_statement, the declaration node has no preceding named sibling,
	// so the upstream's getPrecedingDocstring returns null.
	docstring := ""
	if !e.insideExport {
		docstring = e.lookupDoc(node)
	}
	isExported := e.isTSExported(node)

	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil || child.Kind() != "variable_declarator" {
			continue
		}

		nameNode := child.ChildByFieldName("name")
		valueNode := child.ChildByFieldName("value")

		if nameNode == nil {
			continue
		}
		// Skip destructured patterns
		if nameNode.Kind() == "object_pattern" || nameNode.Kind() == "array_pattern" {
			continue
		}
		name := nameNode.Text()

		// Arrow functions / function expressions: extract as function
		if valueNode != nil &&
			(valueNode.Kind() == "arrow_function" || valueNode.Kind() == "function_expression") {
			e.extractTSFunction(valueNode, name)
			continue
		}

		kind := model.KindVariable
		if isConst {
			kind = model.KindConstant
		}

		var sig string
		if valueNode != nil {
			initVal := valueNode.Text()
			if len(initVal) > 100 {
				sig = "= " + initVal[:100] + "..."
			} else if initVal != "" {
				sig = "= " + initVal
			}
		}

		varNode := e.createNode(kind, name, child, nodeExtra{
			docstring:  docstring,
			signature:  sig,
			isExported: isExported,
		})
		if varNode == nil {
			continue
		}

		// Visit initializer for calls (but not object/store patterns)
		if valueNode != nil &&
			valueNode.Kind() != "object" &&
			valueNode.Kind() != "object_expression" {
			e.visitTSBody(valueNode)
		}
	}
}

// extractTSCall handles call_expression for TS/JS.
func (e *extractor) extractTSCall(node *tsparse.Node) {
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
	case "member_expression":
		// obj.method() — JS
		propNode := funcNode.ChildByFieldName("property")
		if propNode == nil {
			break
		}
		methodName := propNode.Text()
		objNode := funcNode.ChildByFieldName("object")
		if objNode != nil {
			switch objNode.Kind() {
			case "identifier", "field_identifier":
				recv := objNode.Text()
				skipReceivers := map[string]bool{"self": true, "this": true, "cls": true, "super": true}
				if skipReceivers[recv] {
					calleeName = methodName
				} else {
					calleeName = recv + "." + methodName
				}
			default:
				calleeName = methodName
			}
		} else {
			calleeName = methodName
		}
	default:
		calleeName = funcNode.Text()
	}

	// Normalize parenthesized type conversions
	if m := reGoTypeConv.FindStringSubmatch(calleeName); m != nil {
		calleeName = m[1]
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

// extractTSInstantiation handles new_expression.
func (e *extractor) extractTSInstantiation(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	fromID := e.nodeStack[len(e.nodeStack)-1]

	ctorNode := node.ChildByFieldName("constructor")
	if ctorNode == nil && node.NamedChildCount() > 0 {
		ctorNode = node.NamedChild(0)
	}
	if ctorNode == nil {
		return
	}

	className := ctorNode.Text()
	// Strip type arguments: Map<K,V> → Map
	if idx := strings.Index(className, "<"); idx > 0 {
		className = className[:idx]
	}
	// For qualified names ns.Foo → Foo
	if last := lastIdentSegment(className); last != "" {
		className = last
	}
	className = strings.TrimSpace(className)
	if className == "" {
		return
	}

	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    fromID,
		ReferenceName: className,
		ReferenceKind: model.EdgeInstantiates,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// visitTSBody walks a function/method body for calls and nested declarations.
func (e *extractor) visitTSBody(body *tsparse.Node) {
	tsparse.Walk(body, func(node *tsparse.Node) {
		switch node.Kind() {
		case "call_expression":
			e.extractTSCall(node)
		case "new_expression":
			e.extractTSInstantiation(node)
		case "function_declaration":
			// Named nested function — extract
			nameNode := node.ChildByFieldName("name")
			if nameNode != nil && nameNode.Text() != "" && nameNode.Text() != "<anonymous>" {
				// We can't easily avoid double-visiting with Walk, so we use a different approach:
				// don't use Walk for bodies, use recursive visitNodeTS instead.
			}
		}
	})
}

// emitTSImportBindingRefs emits one imports reference per named import binding.
// Port of emitImportBindingRefs in tree-sitter.ts.
func (e *extractor) emitTSImportBindingRefs(node *tsparse.Node, fromNodeID string) {
	// Find import_clause
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil || child.Kind() != "import_clause" {
			continue
		}
		e.emitTSClauseBindingRefs(child, fromNodeID)
	}
}

func (e *extractor) emitTSClauseBindingRefs(clause *tsparse.Node, fromNodeID string) {
	for i := 0; i < clause.NamedChildCount(); i++ {
		child := clause.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "identifier":
			// default import
			name := child.Text()
			if name != "" {
				e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
					FromNodeID:    fromNodeID,
					ReferenceName: name,
					ReferenceKind: model.EdgeImports,
					Line:          int(child.StartPoint().Row) + 1,
					Column:        int(child.StartPoint().Column),
				})
			}
		case "named_imports":
			// import { A, B as C } from './x'
			for j := 0; j < child.NamedChildCount(); j++ {
				spec := child.NamedChild(j)
				if spec == nil || spec.Kind() != "import_specifier" {
					continue
				}
				// Use alias if present, otherwise name
				aliasNode := spec.ChildByFieldName("alias")
				nameNode := spec.ChildByFieldName("name")
				target := aliasNode
				if target == nil {
					target = nameNode
				}
				if target == nil && spec.NamedChildCount() > 0 {
					target = spec.NamedChild(0)
				}
				if target != nil {
					name := target.Text()
					if name != "" {
						e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
							FromNodeID:    fromNodeID,
							ReferenceName: name,
							ReferenceKind: model.EdgeImports,
							Line:          int(target.StartPoint().Row) + 1,
							Column:        int(target.StartPoint().Column),
						})
					}
				}
			}
		case "namespace_import":
			// import * as NS from './x'
			for j := 0; j < child.NamedChildCount(); j++ {
				id := child.NamedChild(j)
				if id != nil && id.Kind() == "identifier" {
					name := id.Text()
					if name != "" {
						e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
							FromNodeID:    fromNodeID,
							ReferenceName: name,
							ReferenceKind: model.EdgeImports,
							Line:          int(id.StartPoint().Row) + 1,
							Column:        int(id.StartPoint().Column),
						})
					}
					break
				}
			}
		}
	}
}

// emitTSReExportRefs emits references for `export { A, B } from './y'` statements.
// Port of emitReExportRefs in tree-sitter.ts.
func (e *extractor) emitTSReExportRefs(node *tsparse.Node, fromNodeID string) {
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil || child.Kind() != "export_clause" {
			continue
		}
		for j := 0; j < child.NamedChildCount(); j++ {
			spec := child.NamedChild(j)
			if spec == nil || spec.Kind() != "export_specifier" {
				continue
			}
			nameNode := spec.ChildByFieldName("name")
			if nameNode == nil && spec.NamedChildCount() > 0 {
				nameNode = spec.NamedChild(0)
			}
			if nameNode == nil {
				continue
			}
			name := nameNode.Text()
			if name == "" || name == "default" {
				continue
			}
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    fromNodeID,
				ReferenceName: name,
				ReferenceKind: model.EdgeImports,
				Line:          int(nameNode.StartPoint().Row) + 1,
				Column:        int(nameNode.StartPoint().Column),
			})
		}
	}
}

// tsSignature builds the signature for a TS function/method.
func (e *extractor) tsSignature(node *tsparse.Node) string {
	params := node.ChildByFieldName("parameters")
	if params == nil {
		return ""
	}
	sig := params.Text()
	// return_type field
	retType := node.ChildByFieldName("return_type")
	if retType != nil {
		text := retType.Text()
		// Strip leading ": " if present
		text = strings.TrimPrefix(text, ":")
		text = strings.TrimSpace(text)
		sig += ": " + text
	}
	return sig
}

// isTSExported reports whether the current node is exported.
// Port of isExported in typescript.ts: checks if an export_statement ancestor exists.
// We track this via the insideExport flag set when descending into export_statement.
func (e *extractor) isTSExported(_ *tsparse.Node) bool {
	return e.insideExport
}

// tsVisibility extracts the accessibility modifier (public/private/protected).
func (e *extractor) tsVisibility(node *tsparse.Node) *string {
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		if child.Kind() == "accessibility_modifier" {
			text := child.Text()
			switch text {
			case "public", "private", "protected":
				v := text
				return &v
			}
		}
	}
	return nil
}

// tsBoolChild checks if the node has a direct child with the given kind.
func tsBoolChild(node *tsparse.Node, kind string) bool {
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil && child.Kind() == kind {
			return true
		}
	}
	return false
}

// resolveTSBody resolves the body node for a TS function/method.
// Handles public_field_definition and field_definition specially.
// Port of resolveBody in typescript.ts / javascript.ts.
func resolveTSBody(node *tsparse.Node) *tsparse.Node {
	switch node.Kind() {
	case "public_field_definition", "field_definition":
		// Look for arrow_function or function_expression child
		for i := 0; i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child == nil {
				continue
			}
			switch child.Kind() {
			case "arrow_function", "function_expression":
				return child.ChildByFieldName("body")
			case "call_expression":
				// HOF wrapper: field = withBatch((e) => { ... })
				args := child.ChildByFieldName("arguments")
				if args != nil {
					for j := 0; j < args.NamedChildCount(); j++ {
						arg := args.NamedChild(j)
						if arg != nil && (arg.Kind() == "arrow_function" || arg.Kind() == "function_expression") {
							return arg.ChildByFieldName("body")
						}
					}
				}
			}
		}
		return nil
	default:
		return node.ChildByFieldName("body")
	}
}

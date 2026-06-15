package extract

// walkGoFallback is the primary Go scanner (ADR-003). It uses the standard
// library go/parser — which correctly handles all valid Go including
// []struct{...} table-driven test patterns that triggered a gotreesitter bug —
// to extract top-level symbol nodes. walkGo (gotreesitter) is retained as a
// test oracle only.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/specscore/codegrapher/model"
)

// walkGoFallback extracts all top-level declarations from Go source using
// go/parser. When used as the primary walk (fullFallback=true) all function
// bodies are walked to emit call/instantiate refs.
//
// When used as a supplemental pass (fullFallback=false, now unused) only
// newly-added function bodies are walked to avoid double-emitting refs.
func (e *extractor) walkGoFallback(src []byte, fullFallback bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, e.filePath, src, parser.ParseComments)
	if err != nil {
		// go/parser may return a partial AST even on error — use it anyway.
		if f == nil {
			return
		}
	}

	// Build existing-ID set to avoid duplicating nodes a prior walk already emitted.
	existingIDs := make(map[string]bool, len(e.nodes))
	for _, n := range e.nodes {
		existingIDs[n.ID] = true
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			e.fallbackFunc(d, src, fset, existingIDs, fullFallback)
		case *ast.GenDecl:
			e.fallbackGenDecl(d, src, fset, existingIDs)
		}
	}
}

// fallbackFunc extracts a single top-level function or method declaration.
// When fullFallback is true or the node is newly added, it also walks the
// function body to emit call/instantiate refs.
func (e *extractor) fallbackFunc(d *ast.FuncDecl, src []byte, fset *token.FileSet, existing map[string]bool, fullFallback bool) {
	if d.Name == nil {
		return
	}
	name := d.Name.Name
	startPos := fset.Position(d.Pos())
	startLine := startPos.Line

	kind := model.KindFunction
	var qualName string
	isMethod := d.Recv != nil && len(d.Recv.List) > 0
	if isMethod {
		kind = model.KindMethod
		recvType := fallbackReceiverType(d.Recv)
		if recvType != "" {
			qualName = recvType + "::" + name
		}
	}

	id := model.GenerateNodeID(e.filePath, kind, name, startLine)
	isNew := !existing[id]

	if isNew {
		endPos := fset.Position(d.End())
		endLine := endPos.Line
		endCol := max(
			// go/token is 1-based; tree-sitter EndPoint.Column is 0-based exclusive
			endPos.Column-1, 0)

		var docstring string
		if d.Doc != nil {
			docstring = strings.TrimSpace(d.Doc.Text())
		}

		sig := fallbackFuncSignature(d.Type, src, fset)
		returnType := fallbackFuncReturnType(d.Type)

		node := model.Node{
			ID:            id,
			Kind:          kind,
			Name:          name,
			QualifiedName: fallbackQualName(qualName, name),
			FilePath:      e.filePath,
			Language:      e.lang,
			StartLine:     startLine,
			EndLine:       endLine,
			EndColumn:     endCol,
			// IsExported is set only for functions, not methods — matching
			// the tree-sitter walk (extractGoMethod doesn't set it).
			IsExported: !isMethod && isGoExported(name),
			Docstring:  docstring,
			Signature:  sig,
			ReturnType: returnType,
		}
		e.nodes = append(e.nodes, node)
		existing[id] = true

		// containment edge from file node (first node = file node)
		if len(e.nodes) > 1 {
			fileID := e.nodes[0].ID
			e.edges = append(e.edges, model.Edge{
				Source: fileID,
				Target: id,
				Kind:   model.EdgeContains,
			})
		}

		// If it's a method, also try to add a contains edge from the receiver struct.
		if isMethod {
			recvType := fallbackReceiverType(d.Recv)
			if recvType != "" {
				e.addReceiverContains(recvType, id)
			}
		}
	}

	// Emit type annotation references for parameter and return types,
	// mirroring extractGoTypeAnnotations + walkGoTypeRefs in walk_go.go.
	e.fallbackTypeAnnotationRefs(d.Type, id, fset)

	// Walk the function body to emit call/instantiate unresolved refs.
	if d.Body != nil && (fullFallback || isNew) {
		e.fallbackWalkBody(d.Body, id, fset)
	}
}

// fallbackGenDecl extracts var/const/type/import declarations.
func (e *extractor) fallbackGenDecl(d *ast.GenDecl, src []byte, fset *token.FileSet, existing map[string]bool) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			e.fallbackValueSpec(s, d, src, fset, existing)
		case *ast.TypeSpec:
			e.fallbackTypeSpec(s, d.Doc, src, fset, existing)
		case *ast.ImportSpec:
			e.fallbackImportSpec(s, fset, existing)
		}
	}
}

func (e *extractor) fallbackValueSpec(s *ast.ValueSpec, d *ast.GenDecl, src []byte, fset *token.FileSet, existing map[string]bool) {
	isConst := d.Tok.String() == "const"
	kind := model.KindVariable
	if isConst {
		kind = model.KindConstant
	}

	// Docstring: prefer spec-level doc, fall back to GenDecl doc.
	var docstring string
	if s.Doc != nil {
		docstring = strings.TrimSpace(s.Doc.Text())
	} else if d.Doc != nil {
		docstring = strings.TrimSpace(d.Doc.Text())
	}

	for _, name := range s.Names {
		if name == nil || name.Name == "_" {
			continue
		}
		startLine := fset.Position(name.Pos()).Line
		id := model.GenerateNodeID(e.filePath, kind, name.Name, startLine)
		if existing[id] {
			continue
		}

		// Signature: "= <value>" if a value is present.
		var sig string
		if len(s.Values) > 0 {
			valStart := fset.Position(s.Values[0].Pos()).Offset
			valEnd := fset.Position(s.Values[len(s.Values)-1].End()).Offset
			if valStart >= 0 && valEnd <= len(src) && valEnd > valStart {
				initValue := strings.TrimSpace(string(src[valStart:valEnd]))
				if len(initValue) > 100 {
					sig = "= " + initValue[:100] + "..."
				} else if initValue != "" {
					sig = "= " + initValue
				}
			}
		}

		startCol := max(fset.Position(name.Pos()).Column-1, 0)
		endCol := max(fset.Position(s.End()).Column-1, 0)
		node := model.Node{
			ID:            id,
			Kind:          kind,
			Name:          name.Name,
			QualifiedName: name.Name,
			FilePath:      e.filePath,
			Language:      e.lang,
			StartLine:     startLine,
			EndLine:       fset.Position(s.End()).Line,
			StartColumn:   startCol,
			EndColumn:     endCol,
			// IsExported is NOT set for variables/constants — matching the
			// tree-sitter walk (extractGoVarConst doesn't set it in nodeExtra).
			Docstring: docstring,
			Signature: sig,
		}
		e.nodes = append(e.nodes, node)
		existing[id] = true
		if len(e.nodes) > 1 {
			fileID := e.nodes[0].ID
			e.edges = append(e.edges, model.Edge{
				Source: fileID,
				Target: id,
				Kind:   model.EdgeContains,
			})
		}

		// Walk value expressions for calls (mirrors the tree-sitter walk's
		// visitBodyGo(valueField) in extractGoVarConst).
		for _, val := range s.Values {
			e.nodeStack = append(e.nodeStack, id)
			e.fallbackWalkBody(&ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: val}}}, id, fset)
			e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
		}
	}
}

func (e *extractor) fallbackTypeSpec(s *ast.TypeSpec, genDeclDoc *ast.CommentGroup, src []byte, fset *token.FileSet, existing map[string]bool) {
	if s.Name == nil {
		return
	}
	name := s.Name.Name
	startLine := fset.Position(s.Pos()).Line

	var kind model.NodeKind
	var ifaceType *ast.InterfaceType
	switch t := s.Type.(type) {
	case *ast.StructType:
		kind = model.KindStruct
	case *ast.InterfaceType:
		kind = model.KindInterface
		ifaceType = t
	default:
		kind = model.KindTypeAlias
	}

	// UB-1 fix: extract doc comment from spec (spec-level doc takes priority,
	// fall back to GenDecl-level doc — matching go/ast CommentMap semantics).
	var docstring string
	if s.Doc != nil {
		docstring = strings.TrimSpace(s.Doc.Text())
	} else if genDeclDoc != nil {
		docstring = strings.TrimSpace(genDeclDoc.Text())
	}

	startCol := max(fset.Position(s.Pos()).Column-1, 0)
	endCol := max(fset.Position(s.End()).Column-1, 0)

	id := model.GenerateNodeID(e.filePath, kind, name, startLine)
	if existing[id] {
		// Even if the type node already exists, we still need to extract its
		// interface methods below (they may not have been extracted yet).
		if ifaceType != nil {
			e.fallbackInterfaceMethods(ifaceType, id, name, src, fset, existing)
		}
		return
	}
	node := model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: name,
		FilePath:      e.filePath,
		Language:      e.lang,
		StartLine:     startLine,
		EndLine:       fset.Position(s.End()).Line,
		StartColumn:   startCol,
		EndColumn:     endCol,
		IsExported:    isGoExported(name),
		Docstring:     docstring,
	}
	e.nodes = append(e.nodes, node)
	existing[id] = true
	if len(e.nodes) > 1 {
		fileID := e.nodes[0].ID
		e.edges = append(e.edges, model.Edge{
			Source: fileID,
			Target: id,
			Kind:   model.EdgeContains,
		})
	}

	// Extract interface methods as method nodes contained by the interface.
	if ifaceType != nil {
		e.fallbackInterfaceMethods(ifaceType, id, name, src, fset, existing)
	}
}

// fallbackInterfaceMethods extracts method_spec nodes from an interface type
// body as method nodes contained by the interface, mirroring
// extractGoInterfaceMethods in walk_go.go.
func (e *extractor) fallbackInterfaceMethods(ifaceType *ast.InterfaceType, ifaceID string, ifaceName string, src []byte, fset *token.FileSet, existing map[string]bool) {
	if ifaceType.Methods == nil {
		return
	}
	for _, method := range ifaceType.Methods.List {
		if method == nil {
			continue
		}
		ft, ok := method.Type.(*ast.FuncType)
		if !ok {
			continue
		}
		if len(method.Names) == 0 {
			continue
		}
		mname := method.Names[0].Name
		if mname == "" {
			continue
		}
		startPos := fset.Position(method.Pos())
		startLine := startPos.Line
		startCol := max(startPos.Column-1, 0)
		endPos := fset.Position(method.End())
		endCol := max(endPos.Column-1, 0)
		mID := model.GenerateNodeID(e.filePath, model.KindMethod, mname, startLine)
		if existing[mID] {
			continue
		}
		sig := fallbackFuncSignature(ft, src, fset)
		node := model.Node{
			ID:            mID,
			Kind:          model.KindMethod,
			Name:          mname,
			QualifiedName: ifaceName + "::" + mname,
			FilePath:      e.filePath,
			Language:      e.lang,
			StartLine:     startLine,
			EndLine:       endPos.Line,
			StartColumn:   startCol,
			EndColumn:     endCol,
			Signature:     sig,
		}
		e.nodes = append(e.nodes, node)
		existing[mID] = true
		e.edges = append(e.edges, model.Edge{
			Source: ifaceID,
			Target: mID,
			Kind:   model.EdgeContains,
		})
	}
}

func (e *extractor) fallbackImportSpec(s *ast.ImportSpec, fset *token.FileSet, existing map[string]bool) {
	if s.Path == nil {
		return
	}
	importPath := strings.Trim(s.Path.Value, `"`)
	if importPath == "" {
		return
	}
	startLine := fset.Position(s.Pos()).Line
	id := model.GenerateNodeID(e.filePath, model.KindImport, importPath, startLine)
	if existing[id] {
		return
	}
	// Signature mirrors what the tree-sitter walk produces: the quoted path
	// (and alias if present), trimmed — i.e. spec.Text() in tree-sitter terms.
	sig := s.Path.Value
	if s.Name != nil {
		sig = s.Name.Name + " " + sig
	}
	sig = strings.TrimSpace(sig)

	startCol := max(fset.Position(s.Pos()).Column-1, 0)
	endCol := max(fset.Position(s.End()).Column-1, 0)

	node := model.Node{
		ID:            id,
		Kind:          model.KindImport,
		Name:          importPath,
		QualifiedName: importPath,
		FilePath:      e.filePath,
		Language:      e.lang,
		StartLine:     startLine,
		EndLine:       fset.Position(s.End()).Line,
		StartColumn:   startCol,
		EndColumn:     endCol,
		Signature:     sig,
	}
	e.nodes = append(e.nodes, node)
	existing[id] = true
	if len(e.nodes) > 1 {
		fileID := e.nodes[0].ID
		e.edges = append(e.edges, model.Edge{
			Source: fileID,
			Target: id,
			Kind:   model.EdgeContains,
		})
		// Emit an imports unresolved ref from the file node, mirroring
		// extractGoImport in walk_go.go which emits from parentID.
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    fileID,
			ReferenceName: importPath,
			ReferenceKind: model.EdgeImports,
			Line:          startLine,
			Column:        fset.Position(s.Pos()).Column - 1,
		})
	}
}

// fallbackTypeAnnotationRefs emits `references` unresolved refs for all
// user-defined types named in a function/method's parameter list and result
// type. Mirrors extractGoTypeAnnotations + walkGoTypeRefs in walk_go.go.
func (e *extractor) fallbackTypeAnnotationRefs(ft *ast.FuncType, fromID string, fset *token.FileSet) {
	if ft == nil {
		return
	}
	if ft.Params != nil {
		e.fallbackWalkTypeRefs(ft.Params, fromID, fset)
	}
	if ft.Results != nil {
		e.fallbackWalkTypeRefs(ft.Results, fromID, fset)
	}
}

// fallbackWalkTypeRefs walks a FieldList and emits `references` unresolved refs
// for every user-defined type identifier in TYPE position. It mirrors
// walkGoTypeRefs (walk_go.go) which visits type_identifier AST nodes —
// crucially NOT parameter names, which are ordinary identifiers, not type_identifiers.
func (e *extractor) fallbackWalkTypeRefs(node ast.Node, fromID string, fset *token.FileSet) {
	fl, ok := node.(*ast.FieldList)
	if !ok || fl == nil {
		return
	}
	for _, field := range fl.List {
		if field == nil {
			continue
		}
		e.fallbackEmitTypeRefs(field.Type, fromID, fset)
	}
}

// fallbackEmitTypeRefs recursively walks a type expression and emits a
// `references` ref for every non-builtin type identifier it finds.
// Mirrors walkGoTypeRefs in walk_go.go.
func (e *extractor) fallbackEmitTypeRefs(expr ast.Expr, fromID string, fset *token.FileSet) {
	if expr == nil {
		return
	}
	switch t := expr.(type) {
	case *ast.Ident:
		if t.Name != "" && !goBuiltinType(t.Name) {
			pos := fset.Position(t.Pos())
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    fromID,
				ReferenceName: t.Name,
				ReferenceKind: model.EdgeReferences,
				Line:          pos.Line,
				Column:        pos.Column - 1,
				FilePath:      e.filePath,
				Language:      e.lang,
			})
		}
	case *ast.SelectorExpr:
		// Qualified type e.g. io.Writer — walk the selector (last segment)
		e.fallbackEmitTypeRefs(t.Sel, fromID, fset)
	case *ast.StarExpr:
		e.fallbackEmitTypeRefs(t.X, fromID, fset)
	case *ast.ArrayType:
		e.fallbackEmitTypeRefs(t.Elt, fromID, fset)
	case *ast.MapType:
		e.fallbackEmitTypeRefs(t.Key, fromID, fset)
		e.fallbackEmitTypeRefs(t.Value, fromID, fset)
	case *ast.ChanType:
		e.fallbackEmitTypeRefs(t.Value, fromID, fset)
	case *ast.Ellipsis:
		e.fallbackEmitTypeRefs(t.Elt, fromID, fset)
	case *ast.FuncType:
		e.fallbackTypeAnnotationRefs(t, fromID, fset)
	case *ast.InterfaceType:
		// anonymous interface — skip
	case *ast.StructType:
		// anonymous struct — skip
	case *ast.IndexExpr:
		// Generic instantiation Foo[T]
		e.fallbackEmitTypeRefs(t.X, fromID, fset)
		e.fallbackEmitTypeRefs(t.Index, fromID, fset)
	case *ast.IndexListExpr:
		e.fallbackEmitTypeRefs(t.X, fromID, fset)
		for _, idx := range t.Indices {
			e.fallbackEmitTypeRefs(idx, fromID, fset)
		}
	}
}

// fallbackWalkBody walks a function body AST using go/ast to emit
// call and instantiate unresolved references. Mirrors the logic of visitBodyGo
// (walk_go.go) but uses the standard library parser's AST.
func (e *extractor) fallbackWalkBody(body *ast.BlockStmt, fromNodeID string, fset *token.FileSet) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			calleeName := fallbackCallName(node)
			if calleeName != "" {
				pos := fset.Position(node.Pos())
				e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
					FromNodeID:    fromNodeID,
					ReferenceName: calleeName,
					ReferenceKind: model.EdgeCalls,
					Line:          pos.Line,
					Column:        pos.Column - 1,
				})
			}
		case *ast.CompositeLit:
			goType := fallbackCompositeLitType(node)
			if goType != "" {
				pos := fset.Position(node.Pos())
				e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
					FromNodeID:    fromNodeID,
					ReferenceName: goType,
					ReferenceKind: model.EdgeInstantiates,
					Line:          pos.Line,
					Column:        pos.Column - 1,
				})
			}
		}
		return true
	})
}

// fallbackFuncSignature builds the signature string for a Go function type:
// "<params> <result>" — matching goSignature in walk_go.go which uses
// tree-sitter node.Text() for the parameters and result fields.
// We extract the substrings directly from the source bytes using file offsets.
func fallbackFuncSignature(ft *ast.FuncType, src []byte, fset *token.FileSet) string {
	if ft == nil || ft.Params == nil {
		return ""
	}
	paramStart := fset.Position(ft.Params.Pos()).Offset
	paramEnd := fset.Position(ft.Params.End()).Offset
	if paramStart < 0 || paramEnd > len(src) || paramEnd < paramStart {
		return ""
	}
	sig := string(src[paramStart:paramEnd])
	if ft.Results != nil {
		resStart := fset.Position(ft.Results.Pos()).Offset
		resEnd := fset.Position(ft.Results.End()).Offset
		if resStart >= 0 && resEnd <= len(src) && resEnd >= resStart {
			sig += " " + string(src[resStart:resEnd])
		}
	}
	return sig
}

// fallbackFuncReturnType extracts the normalized bare return type name for a
// Go function, mirroring goReturnType in walk_go.go.
func fallbackFuncReturnType(ft *ast.FuncType) string {
	if ft == nil || ft.Results == nil || len(ft.Results.List) == 0 {
		return ""
	}
	// Multi-return: take the first result's type.
	first := ft.Results.List[0]
	if first == nil || first.Type == nil {
		return ""
	}
	return fallbackGoBaseType(first.Type)
}

// fallbackGoBaseType extracts the bare return type name from a go/ast Expr.
// Mirrors goBaseTypeName / goReturnType in walk_go.go.
func fallbackGoBaseType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return fallbackGoBaseType(t.X)
	case *ast.SelectorExpr:
		// pkg.Type → Type
		return fallbackGoBaseType(t.Sel)
	case *ast.ArrayType:
		return fallbackGoBaseType(t.Elt)
	case *ast.IndexExpr:
		return fallbackGoBaseType(t.X) // Foo[T]
	case *ast.IndexListExpr:
		return fallbackGoBaseType(t.X) // Foo[T, U]
	}
	return ""
}

// fallbackCallName extracts the callee name from a go/ast CallExpr,
// mirroring the logic in extractGoCall (walk_go.go).
func fallbackCallName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		methodName := fn.Sel.Name
		switch recv := fn.X.(type) {
		case *ast.Ident:
			if recv.Name == "self" || recv.Name == "this" || recv.Name == "cls" || recv.Name == "super" {
				return methodName
			}
			return recv.Name + "." + methodName
		case *ast.CallExpr:
			// Chained: New().Method()
			if inner, ok := recv.Fun.(*ast.Ident); ok {
				return inner.Name + "()." + methodName
			}
			return methodName
		default:
			return methodName
		}
	default:
		return ""
	}
}

// fallbackCompositeLitType extracts the type name from a go/ast CompositeLit,
// mirroring extractGoCompositeInstantiation (walk_go.go). Returns "" for
// non-struct literals (slices, maps, anonymous structs).
func fallbackCompositeLitType(lit *ast.CompositeLit) string {
	if lit.Type == nil {
		return ""
	}
	switch t := lit.Type.(type) {
	case *ast.Ident:
		// Bare struct: SpecConfig{}
		name := t.Name
		// Strip generic args not present in go/ast Ident, but handle ArrayType etc.
		return name
	case *ast.SelectorExpr:
		// Qualified: projectdef.SpecConfig{}
		if pkg, ok := t.X.(*ast.Ident); ok {
			return pkg.Name + "." + t.Sel.Name
		}
	}
	return ""
}

// fallbackReceiverType extracts the receiver type name from a method's
// receiver list, stripping pointer and generic parameters.
func fallbackReceiverType(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	field := recv.List[0]
	if field == nil || field.Type == nil {
		return ""
	}
	return fallbackTypeName(field.Type)
}

// fallbackTypeName extracts the base type name from an AST type expression.
func fallbackTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return fallbackTypeName(t.X)
	case *ast.IndexExpr:
		return fallbackTypeName(t.X) // Foo[T]
	case *ast.IndexListExpr:
		return fallbackTypeName(t.X) // Foo[T, U]
	}
	return ""
}

// fallbackQualName returns qualName if non-empty, otherwise name.
func fallbackQualName(qualName, name string) string {
	if qualName != "" {
		return qualName
	}
	return name
}

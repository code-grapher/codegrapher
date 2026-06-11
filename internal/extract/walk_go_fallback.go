package extract

// walkGoFallback is called when gotreesitter produces a parse error for a Go
// file (root.Kind() == "ERROR" or root.HasError()). It uses the standard
// library go/parser — which correctly handles all valid Go including
// []struct{...} table-driven test patterns that trigger a gotreesitter bug —
// to extract top-level symbol nodes.
//
// Positioned so it runs only when the primary tree-sitter walk is known to be
// incomplete; for well-parsed files the tree-sitter walk is used exclusively
// (no change to golden outputs).
//
// Port note: this is a deliberate divergence (D-3) from upstream, which uses
// the real C tree-sitter via WASM and never hits this gotreesitter parsing
// limitation. Documented in KNOWN-BUGS.md.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/specscore/codegrapher/model"
)

// walkGoFallback extracts all top-level declarations from Go source using
// go/parser and merges any NEW nodes (by ID) into e.nodes/e.edges. Nodes
// already present (extracted by the partial tree-sitter walk) are not
// duplicated.
func (e *extractor) walkGoFallback(src []byte) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, e.filePath, src, parser.ParseComments)
	if err != nil {
		// go/parser may return a partial AST even on error — use it anyway.
		if f == nil {
			return
		}
	}

	// Build existing-ID set to avoid duplicating nodes the tree-sitter walk
	// already emitted correctly.
	existingIDs := make(map[string]bool, len(e.nodes))
	for _, n := range e.nodes {
		existingIDs[n.ID] = true
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			e.fallbackFunc(d, fset, existingIDs)
		case *ast.GenDecl:
			e.fallbackGenDecl(d, fset, existingIDs)
		}
	}
}

// fallbackFunc extracts a single top-level function or method declaration.
func (e *extractor) fallbackFunc(d *ast.FuncDecl, fset *token.FileSet, existing map[string]bool) {
	if d.Name == nil {
		return
	}
	name := d.Name.Name
	startLine := fset.Position(d.Pos()).Line

	kind := model.KindFunction
	var qualName string
	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = model.KindMethod
		recvType := fallbackReceiverType(d.Recv)
		if recvType != "" {
			qualName = recvType + "::" + name
		}
	}

	id := model.GenerateNodeID(e.filePath, kind, name, startLine)
	if existing[id] {
		return
	}

	isExported := isGoExported(name)
	endLine := fset.Position(d.End()).Line

	node := model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: fallbackQualName(qualName, name),
		FilePath:      e.filePath,
		Language:      e.lang,
		StartLine:     startLine,
		EndLine:       endLine,
		IsExported:    isExported,
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
	if kind == model.KindMethod && d.Recv != nil && len(d.Recv.List) > 0 {
		recvType := fallbackReceiverType(d.Recv)
		if recvType != "" {
			e.addReceiverContains(recvType, id)
		}
	}
}

// fallbackGenDecl extracts var/const/type/import declarations.
func (e *extractor) fallbackGenDecl(d *ast.GenDecl, fset *token.FileSet, existing map[string]bool) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			e.fallbackValueSpec(s, d, fset, existing)
		case *ast.TypeSpec:
			e.fallbackTypeSpec(s, fset, existing)
		case *ast.ImportSpec:
			e.fallbackImportSpec(s, fset, existing)
		}
	}
}

func (e *extractor) fallbackValueSpec(s *ast.ValueSpec, d *ast.GenDecl, fset *token.FileSet, existing map[string]bool) {
	isConst := d.Tok.String() == "const"
	kind := model.KindVariable
	if isConst {
		kind = model.KindConstant
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
		node := model.Node{
			ID:            id,
			Kind:          kind,
			Name:          name.Name,
			QualifiedName: name.Name,
			FilePath:      e.filePath,
			Language:      e.lang,
			StartLine:     startLine,
			EndLine:       fset.Position(s.End()).Line,
			IsExported:    isGoExported(name.Name),
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
	}
}

func (e *extractor) fallbackTypeSpec(s *ast.TypeSpec, fset *token.FileSet, existing map[string]bool) {
	if s.Name == nil {
		return
	}
	name := s.Name.Name
	startLine := fset.Position(s.Pos()).Line

	var kind model.NodeKind
	switch s.Type.(type) {
	case *ast.StructType:
		kind = model.KindStruct
	case *ast.InterfaceType:
		kind = model.KindInterface
	default:
		kind = model.KindTypeAlias
	}

	id := model.GenerateNodeID(e.filePath, kind, name, startLine)
	if existing[id] {
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
		IsExported:    isGoExported(name),
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
	node := model.Node{
		ID:            id,
		Kind:          model.KindImport,
		Name:          importPath,
		QualifiedName: importPath,
		FilePath:      e.filePath,
		Language:      e.lang,
		StartLine:     startLine,
		EndLine:       fset.Position(s.End()).Line,
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

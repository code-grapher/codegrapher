package extract

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// nodeExtra holds optional fields that callers may provide when creating a node.
type nodeExtra struct {
	docstring      string
	signature      string
	visibility     *string
	isExported     bool
	isAsync        bool
	isStatic       bool
	isAbstract     bool
	returnType     string
	qualifiedName  string // override if set
	decorators     []string
	typeParameters []string
}

// extractor is the per-file extraction state.
type extractor struct {
	filePath       string
	content        string
	lang           model.Language
	nodes          []model.Node
	edges          []model.Edge
	unresolvedRefs []model.UnresolvedReference
	nodeStack      []string // node IDs for qualified-name building
	errors         []model.ExtractionError

	// commentByEndLine maps (1-indexed) end-line of a comment → its text.
	// Built once from the tree before walking for symbols.
	commentByEndLine map[int]string

	// insideExport is true when we are descending through an export_statement.
	// Used by isTSExported to correctly mark declarations as exported.
	insideExport bool

	// seenNodeIDs tracks node IDs already emitted to prevent duplicates when
	// gotreesitter's ERROR root causes walkGo to visit the same AST node via
	// multiple paths (direct child + inside ERROR subtree).
	seenNodeIDs map[string]bool

	// dartPendingMember holds the node ID of the most recently emitted Dart
	// method/function whose body is a following sibling (function_body), so the
	// sibling's calls attribute to it.
	dartPendingMember string
}

// ExtractFile parses content as lang, extracts a file node (and, eventually,
// all symbols), and returns the result. The AST-walking logic for individual
// symbol kinds will be added in subsequent tasks; this skeleton always
// succeeds and returns at least the file node.
func ExtractFile(path string, content []byte, lang model.Language) (model.ExtractionResult, error) {
	start := time.Now()

	e := &extractor{
		filePath: path,
		content:  string(content),
		lang:     lang,
	}

	// Parse the file with tree-sitter for TS/JS; Go uses go/parser directly
	// (ADR-003: go/parser is the primary Go scanner; walkGo is the test oracle only).
	var tree *tsparse.Tree
	switch lang {
	case model.LangTypeScript, model.LangTSX, model.LangJavaScript, model.LangJSX:
		// JSX-bearing files (.tsx/.jsx) need the tsx grammar; plain TS/JS use
		// the typescript grammar (which handles `<T>` type assertions tsx can't).
		tsLang := tsparse.LangTypeScript
		if lang == model.LangTSX || lang == model.LangJSX {
			tsLang = tsparse.LangTSX
		}
		p, err := tsparse.NewParser(tsLang)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangPython:
		p, err := tsparse.NewParser(tsparse.LangPython)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangCSharp:
		p, err := tsparse.NewParser(tsparse.LangCSharp)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangJava:
		p, err := tsparse.NewParser(tsparse.LangJava)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangKotlin:
		p, err := tsparse.NewParser(tsparse.LangKotlin)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangRuby:
		p, err := tsparse.NewParser(tsparse.LangRuby)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangRust:
		p, err := tsparse.NewParser(tsparse.LangRust)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangPHP:
		p, err := tsparse.NewParser(tsparse.LangPHP)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangC:
		p, err := tsparse.NewParser(tsparse.LangC)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangScala:
		p, err := tsparse.NewParser(tsparse.LangScala)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangSwift:
		p, err := tsparse.NewParser(tsparse.LangSwift)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangCPP:
		p, err := tsparse.NewParser(tsparse.LangCPP)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangDart:
		p, err := tsparse.NewParser(tsparse.LangDart)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangLua:
		p, err := tsparse.NewParser(tsparse.LangLua)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangElixir:
		p, err := tsparse.NewParser(tsparse.LangElixir)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangHaskell:
		p, err := tsparse.NewParser(tsparse.LangHaskell)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangObjC:
		p, err := tsparse.NewParser(tsparse.LangObjC)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangPerl:
		p, err := tsparse.NewParser(tsparse.LangPerl)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangErlang:
		p, err := tsparse.NewParser(tsparse.LangErlang)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangJulia:
		p, err := tsparse.NewParser(tsparse.LangJulia)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	case model.LangFSharp:
		p, err := tsparse.NewParser(tsparse.LangFSharp)
		if err == nil {
			tree, err = p.Parse(content)
			if err != nil {
				e.errors = append(e.errors, model.ExtractionError{
					Message:  err.Error(),
					FilePath: path,
					Severity: "error",
					Code:     "parse_error",
				})
			}
		}
	}

	// Build the comment index so docstring lookup works during TS/JS symbol walking.
	if tree != nil {
		e.commentByEndLine = buildCommentIndex(tree.RootNode())
	}

	// File-level-only languages (yaml, …) are stored in the files table with
	// zero symbol nodes — matching isFileLevelOnlyLanguage() in grammars.ts.
	// Skip the file node emission entirely so NodeCount stays 0.
	if IsFileLevelOnly(lang) {
		return model.ExtractionResult{
			Nodes:  nil,
			Errors: e.errors,
		}, nil
	}

	// Always emit a file node as the root.
	e.emitFileNode(tree)

	// Walk the tree and extract symbol nodes.
	switch lang {
	case model.LangGo:
		// go/parser is the primary walk (ADR-003); walkGo (gotreesitter) is the test oracle only.
		e.walkGoFallback(content, true)
	case model.LangTypeScript, model.LangTSX, model.LangJavaScript, model.LangJSX:
		if tree != nil {
			e.walkTS(tree.RootNode())
		}
	case model.LangPython:
		if tree != nil {
			e.walkPython(tree.RootNode())
		}
	case model.LangCSharp:
		if tree != nil {
			e.walkCSharp(tree.RootNode())
		}
	case model.LangJava:
		if tree != nil {
			e.walkJava(tree.RootNode())
		}
	case model.LangKotlin:
		if tree != nil {
			e.walkKotlin(tree.RootNode())
		}
	case model.LangRuby:
		if tree != nil {
			e.walkRuby(tree.RootNode())
		}
	case model.LangRust:
		if tree != nil {
			e.walkRust(tree.RootNode())
		}
	case model.LangPHP:
		if tree != nil {
			e.walkPHP(tree.RootNode())
		}
	case model.LangC:
		if tree != nil {
			e.walkC(tree.RootNode())
		}
	case model.LangCPP:
		if tree != nil {
			e.walkCPP(tree.RootNode())
		}
	case model.LangScala:
		if tree != nil {
			e.walkScala(tree.RootNode())
		}
	case model.LangSwift:
		if tree != nil {
			e.walkSwift(tree.RootNode())
		}
	case model.LangDart:
		if tree != nil {
			e.walkDart(tree.RootNode())
		}
	case model.LangLua:
		if tree != nil {
			e.walkLua(tree.RootNode())
		}
	case model.LangElixir:
		if tree != nil {
			e.walkElixir(tree.RootNode())
		}
	case model.LangHaskell:
		if tree != nil {
			e.walkHaskell(tree.RootNode())
		}
	case model.LangObjC:
		if tree != nil {
			e.walkObjC(tree.RootNode())
		}
	case model.LangPerl:
		if tree != nil {
			e.walkPerl(tree.RootNode())
		}
	case model.LangErlang:
		if tree != nil {
			e.walkErlang(tree.RootNode())
		}
	case model.LangJulia:
		if tree != nil {
			e.walkJulia(tree.RootNode())
		}
	case model.LangFSharp:
		if tree != nil {
			e.walkFSharp(tree.RootNode())
		}
	case model.LangGoMod:
		e.extractGoMod(content)
	case model.LangPackageJSON:
		e.extractPackageJSON(content)
	}

	// For Go files, also run the framework route extractor.
	if lang == model.LangGo {
		fileNodeID := model.FileNodeID(path)
		routeNodes, routeRefs := ExtractGoRoutes(path, content, fileNodeID)
		// Route nodes are standalone — no contains edge to the file node.
		for _, rn := range routeNodes {
			rn.UpdatedAt = e.nodes[0].UpdatedAt // use same timestamp as file node
			e.nodes = append(e.nodes, rn)
		}
		e.unresolvedRefs = append(e.unresolvedRefs, routeRefs...)
	}

	return model.ExtractionResult{
		Nodes:                e.nodes,
		Edges:                e.edges,
		UnresolvedReferences: e.unresolvedRefs,
		Errors:               e.errors,
		DurationMs:           time.Since(start).Milliseconds(),
	}, nil
}

// emitFileNode creates the file node and pushes it onto the nodeStack.
func (e *extractor) emitFileNode(tree *tsparse.Tree) {
	id := model.FileNodeID(e.filePath)
	name := filepath.Base(e.filePath)

	var startLine, endLine int
	startLine = 1
	if tree != nil {
		endLine = int(tree.RootNode().EndPoint().Row) + 1
	} else {
		// Count lines from raw content.
		endLine = strings.Count(e.content, "\n") + 1
	}

	node := model.Node{
		ID:            id,
		Kind:          model.KindFile,
		Name:          name,
		QualifiedName: e.filePath,
		FilePath:      e.filePath,
		Language:      e.lang,
		StartLine:     startLine,
		EndLine:       endLine,
		StartColumn:   0,
		EndColumn:     0,
		UpdatedAt:     time.Now().UnixMilli(),
	}
	e.nodes = append(e.nodes, node)
	e.nodeStack = append(e.nodeStack, id)
}

// buildQualifiedName constructs a qualified name by joining the names of all
// non-file nodes currently on the stack, plus name itself, with "::".
func (e *extractor) buildQualifiedName(name string) string {
	var parts []string
	for _, id := range e.nodeStack {
		for _, n := range e.nodes {
			if n.ID == id && n.Kind != model.KindFile {
				parts = append(parts, n.Name)
				break
			}
		}
	}
	parts = append(parts, name)
	return strings.Join(parts, "::")
}

// isInsideClassLike reports whether the top of the node stack is a class-like
// kind (class, struct, interface, trait, enum, or module).
func (e *extractor) isInsideClassLike() bool {
	if len(e.nodeStack) == 0 {
		return false
	}
	topID := e.nodeStack[len(e.nodeStack)-1]
	for i := len(e.nodes) - 1; i >= 0; i-- {
		if e.nodes[i].ID == topID {
			switch e.nodes[i].Kind {
			case model.KindClass, model.KindStruct, model.KindInterface,
				model.KindTrait, model.KindEnum, model.KindModule:
				return true
			}
			return false
		}
	}
	return false
}

// createNode creates a model.Node for the given kind and name at the location
// of n, applies any extra fields, records a containment edge from the current
// stack top, and returns a pointer to the stored node. Returns nil if name is
// empty.
func (e *extractor) createNode(kind model.NodeKind, name string, n *tsparse.Node, extra nodeExtra) *model.Node {
	if name == "" {
		return nil
	}
	startLine := int(n.StartPoint().Row) + 1
	id := model.GenerateNodeID(e.filePath, kind, name, startLine)

	// Prevent duplicate nodes (and their contains edges) when gotreesitter's
	// ERROR root causes walkGo to visit the same declaration via multiple paths.
	if e.seenNodeIDs == nil {
		e.seenNodeIDs = make(map[string]bool)
	}
	if e.seenNodeIDs[id] {
		return nil
	}
	e.seenNodeIDs[id] = true

	qualName := e.buildQualifiedName(name)
	if extra.qualifiedName != "" {
		qualName = extra.qualifiedName
	}

	node := model.Node{
		ID:             id,
		Kind:           kind,
		Name:           name,
		QualifiedName:  qualName,
		FilePath:       e.filePath,
		Language:       e.lang,
		StartLine:      startLine,
		EndLine:        int(n.EndPoint().Row) + 1,
		StartColumn:    int(n.StartPoint().Column),
		EndColumn:      int(n.EndPoint().Column),
		Docstring:      extra.docstring,
		Signature:      extra.signature,
		Visibility:     extra.visibility,
		IsExported:     extra.isExported,
		IsAsync:        extra.isAsync,
		IsStatic:       extra.isStatic,
		IsAbstract:     extra.isAbstract,
		ReturnType:     extra.returnType,
		Decorators:     extra.decorators,
		TypeParameters: extra.typeParameters,
		UpdatedAt:      time.Now().UnixMilli(),
	}
	e.nodes = append(e.nodes, node)

	if len(e.nodeStack) > 0 {
		parentID := e.nodeStack[len(e.nodeStack)-1]
		e.edges = append(e.edges, model.Edge{
			Source: parentID,
			Target: id,
			Kind:   model.EdgeContains,
		})
	}
	return &e.nodes[len(e.nodes)-1]
}

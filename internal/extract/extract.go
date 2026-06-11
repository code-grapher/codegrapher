package extract

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// parseTimeoutDuration is the per-file parse deadline for gotreesitter. The
// standard library go/parser is used as fallback when it fires. Configurable
// via CODEGRAPH_PARSE_TIMEOUT_MS (0 = disabled). Default: 30 s.
var parseTimeoutDuration = func() time.Duration {
	if v := os.Getenv("CODEGRAPH_PARSE_TIMEOUT_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil {
			if ms == 0 {
				return 0
			}
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 30 * time.Second
}()

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

	// Parse the file with tree-sitter when we have a supported grammar.
	// For Go files the parse runs in a goroutine with a hard deadline: gotreesitter
	// has a known pathological blow-up (issue #110) on files with many
	// []struct{...} literals where SetTimeoutMicros does not fire in time.
	// On timeout the tree is nil and goTreeError is set, so walkGoFallback
	// (go/parser) handles the whole file — identical node output, no hang.
	var tree *tsparse.Tree
	switch lang {
	case model.LangGo:
		p, err := tsparse.NewParser(tsparse.LangGo)
		if err == nil {
			type parseResult struct {
				tree *tsparse.Tree
				err  error
			}
			if parseTimeoutDuration <= 0 {
				// Timeout disabled: parse synchronously (may hang on pathological files).
				tree, err = p.Parse(content)
				if err != nil {
					e.errors = append(e.errors, model.ExtractionError{
						Message:  err.Error(),
						FilePath: path,
						Severity: "error",
						Code:     "parse_error",
					})
				}
			} else {
				ch := make(chan parseResult, 1)
				go func() {
					t, parseErr := p.Parse(content)
					ch <- parseResult{t, parseErr}
				}()
				select {
				case r := <-ch:
					tree = r.tree
					if r.err != nil {
						e.errors = append(e.errors, model.ExtractionError{
							Message:  r.err.Error(),
							FilePath: path,
							Severity: "error",
							Code:     "parse_error",
						})
					}
				case <-time.After(parseTimeoutDuration):
					// Parse hung — goroutine leaks but extraction continues via go/parser.
					e.errors = append(e.errors, model.ExtractionError{
						Message:  "gotreesitter parse timeout; falling back to go/parser",
						FilePath: path,
						Severity: "warning",
						Code:     "parse_timeout",
					})
					// tree stays nil; goTreeError will be set below to trigger walkGoFallback.
				}
			}
		}
	case model.LangTypeScript, model.LangTSX, model.LangJavaScript, model.LangJSX:
		p, err := tsparse.NewParser(tsparse.LangTypeScript)
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

	// Build the comment index so docstring lookup works during symbol walking.
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
	// goTreeError is true when the tree-sitter parse produced an error tree or
	// timed out; in either case walkGoFallback (go/parser) fills in the gaps.
	goTreeError := lang == model.LangGo && tree == nil // timeout: tree is nil
	goFullFallback := goTreeError                      // D-2: no tree-sitter walk at all
	if tree != nil {
		root := tree.RootNode()
		switch lang {
		case model.LangGo:
			e.walkGo(root)
			// Detect gotreesitter parse failure: root kind is "ERROR" (not
			// "source_file") or the root has error nodes. When this happens the
			// partial tree-sitter walk misses declarations after the first
			// problematic construct. We supplement with the go/parser fallback.
			if root.Kind() == "ERROR" || root.HasError() {
				goTreeError = true
			}
		case model.LangTypeScript, model.LangTSX, model.LangJavaScript, model.LangJSX:
			e.walkTS(root)
		}
	}

	// go/parser supplemental pass: fills in any top-level declarations that
	// the gotreesitter walk missed due to its []struct{...} parsing bug or timeout.
	// goFullFallback=true for D-2 (timeout): all function bodies are walked by
	// go/parser since tree-sitter produced nothing. For D-3 (ERROR root, goFullFallback=false)
	// only newly-added function bodies are walked to avoid double-emitting refs.
	if goTreeError {
		e.walkGoFallback(content, goFullFallback)
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

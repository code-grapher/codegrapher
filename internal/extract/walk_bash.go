package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkBash walks a parsed Bash (tree-sitter `bash`) file root and extracts
// symbols. Bash is shell scripting and structurally thin: no classes, no
// methods, no namespaces. The graph is intentionally just functions, calls,
// and `source` edges (mirroring the Lua thin-dynamic template).
//
// Node type reference (tree-sitter-bash, probe-verified):
//
//	program (root)
//	function_definition (fields: name=word, body=compound_statement;
//	    covers both `foo() { }` and `function foo { }`)
//	command (fields: name=command_name, argument...; a bare call `foo`,
//	    `echo hi`, and `source "lib.sh"` / `. lib.sh` are all commands)
//	command_name → word
//	variable_assignment (fields: name=variable_name, value)
//	declaration_command (wraps an export/readonly/declare keyword, optional
//	    flag words like `-r`, then a variable_assignment or bare variable_name)
//	compound_statement, string, string_content, word, number
func (e *extractor) walkBash(root *tsparse.Node) {
	e.bashWalkChildren(root)
}

// bashWalkChildren visits every named child of node.
func (e *extractor) bashWalkChildren(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		e.visitNodeBash(child)
	}
}

// visitNodeBash dispatches a single statement node. Unknown kinds descend into
// their children so calls/definitions nested inside control flow are still seen.
func (e *extractor) visitNodeBash(node *tsparse.Node) {
	switch node.Kind() {
	case "function_definition":
		e.extractBashFunction(node)
	case "variable_assignment":
		e.extractBashAssignment(node, false, false)
	case "declaration_command":
		e.extractBashDeclaration(node)
	case "command":
		e.extractBashCommand(node)
	default:
		e.bashWalkChildren(node)
	}
}

// extractBashFunction handles a function_definition. The name is a `word`; both
// `foo()` and `function foo` forms are KindFunction. The body is walked under
// the function node so nested calls/assignments produce contains edges.
func (e *extractor) extractBashFunction(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := strings.TrimSpace(nameNode.Text())
	if name == "" {
		return
	}

	fn := e.createNode(model.KindFunction, name, node, nodeExtra{signature: name + "()"})
	if fn == nil {
		return
	}

	if body := node.ChildByFieldName("body"); body != nil {
		e.nodeStack = append(e.nodeStack, fn.ID)
		e.bashWalkChildren(body)
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// extractBashAssignment handles a variable_assignment (`VAR=value`). exported
// marks an `export` declaration; readonly marks a `readonly`/`declare -r`
// declaration. An ALL-CAPS name, or any readonly assignment, is a KindConstant;
// otherwise KindVariable. Only top-level (file-scope) assignments are emitted.
func (e *extractor) extractBashAssignment(node *tsparse.Node, exported, readonly bool) {
	if !e.bashAtFileScope() {
		return
	}
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := strings.TrimSpace(nameNode.Text())
	if name == "" {
		return
	}

	kind := model.KindVariable
	if readonly || bashIsConstantName(name) {
		kind = model.KindConstant
	}

	extra := nodeExtra{signature: bashAssignSignature(node)}
	if exported {
		extra.isExported = true
	}
	e.createNode(kind, name, node, extra)
}

// extractBashDeclaration handles a declaration_command (`export`/`readonly`/
// `declare ...`). It carries the export/readonly intent down into the wrapped
// variable_assignment, or emits a bare KindVariable for `export EX`.
func (e *extractor) extractBashDeclaration(node *tsparse.Node) {
	exported := false
	readonly := false
	for i := 0; i < node.ChildCount(); i++ {
		c := node.Child(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "export":
			exported = true
		case "readonly":
			readonly = true
		case "word":
			// declare flags, e.g. `-r` → readonly.
			if strings.Contains(c.Text(), "r") && strings.HasPrefix(c.Text(), "-") {
				readonly = true
			}
		}
	}

	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "variable_assignment":
			e.extractBashAssignment(c, exported, readonly)
		case "variable_name":
			// bare `export EX` — top-level only.
			if !e.bashAtFileScope() {
				continue
			}
			name := strings.TrimSpace(c.Text())
			if name == "" {
				continue
			}
			kind := model.KindVariable
			if readonly || bashIsConstantName(name) {
				kind = model.KindConstant
			}
			e.createNode(kind, name, c, nodeExtra{isExported: exported})
		}
	}
}

// extractBashCommand handles a command node. `source file` / `. file` emits an
// import; any other command emits an EdgeCalls reference from the current scope
// (the resolver filters it to in-repo functions, so shell builtins/externals
// produce no edge). Arguments are descended for nested calls.
func (e *extractor) extractBashCommand(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	cmd := ""
	if nameNode != nil {
		cmd = strings.TrimSpace(nameNode.Text())
	}

	if cmd == "source" || cmd == "." {
		e.extractBashSource(node)
		return
	}

	if len(e.nodeStack) > 0 && cmd != "" {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    callerID,
			ReferenceName: cmd,
			ReferenceKind: model.EdgeCalls,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}

	// Descend into arguments for nested calls (e.g. command substitution).
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil || c.Kind() == "command_name" {
			continue
		}
		e.bashWalkChildren(c)
	}
}

// extractBashSource handles `source file.sh` / `. file.sh`: emits a KindImport
// node per sourced file plus an EdgeImports reference from the current scope.
// The import name is the file's basename (path separators stripped) so it can
// resolve against the in-repo file of the same name (through-source).
func (e *extractor) extractBashSource(node *tsparse.Node) {
	sig := strings.TrimSpace(node.Text())
	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}

	arg := node.ChildByFieldName("argument")
	if arg == nil {
		return
	}
	path := bashArgText(arg)
	name := bashSourceName(path)
	if name == "" {
		return
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

// bashAtFileScope reports whether the current scope is the top-level file node
// (nothing but the file is on the node stack).
func (e *extractor) bashAtFileScope() bool {
	return len(e.nodeStack) == 1
}

// bashArgText returns the textual value of a source argument, stripping quotes
// from a string node.
func bashArgText(arg *tsparse.Node) string {
	if arg.Kind() == "string" {
		if c := arg.ChildByFieldName("content"); c != nil {
			return c.Text()
		}
		for i := 0; i < arg.NamedChildCount(); i++ {
			if c := arg.NamedChild(i); c != nil && c.Kind() == "string_content" {
				return c.Text()
			}
		}
		return strings.Trim(arg.Text(), `"'`)
	}
	return strings.TrimSpace(arg.Text())
}

// bashSourceName reduces a sourced path to its basename so it can match an
// in-repo file (e.g. "lib/util.sh" → "util.sh", "lib.sh" → "lib.sh"). A path
// containing a shell variable ($X) yields "" (cannot resolve statically).
func bashSourceName(path string) string {
	if path == "" || strings.Contains(path, "$") {
		return ""
	}
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		path = path[idx+1:]
	}
	return path
}

// bashAssignSignature renders a variable_assignment as a `name=value` signature.
func bashAssignSignature(node *tsparse.Node) string {
	val := strings.TrimSpace(node.Text())
	if len(val) > 100 {
		return val[:100] + "..."
	}
	return val
}

// bashIsConstantName reports whether a name is UPPER_SNAKE (only [A-Z0-9_], at
// least one letter).
func bashIsConstantName(name string) bool {
	if name == "" {
		return false
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

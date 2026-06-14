package extract

import (
	"strconv"
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkErlang walks a parsed Erlang (tree-sitter `erlang`) file root and extracts
// symbols. Erlang is functional BEAM — one file is one module, and the module
// namespaces its functions; there are no classes. The model mirrors Elixir
// (modules + functions, `mod:func` remote calls).
//
// Grammar shapes (confirmed by probe):
//
//	source_file → form*
//	module_attribute     { name: atom }                        → -module(foo).
//	export_attribute     { funs: fa* }                         → -export([bar/1]).
//	import_attribute     { module: atom, funs: fa* }           → -import(lists,[map/2]).
//	behaviour_attribute  { name: atom }                        → -behaviour(gen_server).
//	record_decl          { name: atom, fields: record_field* } → -record(st,{a,b}).
//	pp_define            { lhs: macro_lhs{name:var}, ... }     → -define(NAME, v).
//	pp_include           { file: string }                      → -include("x.hrl").
//	pp_include_lib       { file: string }                      → -include_lib("a/b.hrl").
//	fun_decl             { clause: function_clause }           → bar(X) -> ... .
//	  function_clause    { name: atom, args: expr_args, body: clause_body }
//	  call               { expr: atom | remote, args: expr_args }   (call site)
//	  remote             { module: remote_module{module:atom}, fun: atom }
//
// Multi-clause functions appear as SEPARATE top-level fun_decl nodes (the
// grammar does not group clauses), so functions are deduped by name/arity.
func (e *extractor) walkErlang(root *tsparse.Node) {
	// One file = one module. The module attribute is pushed first so every
	// function/record qualifies as `module::name`.
	moduleID := ""
	exported := e.erlangCollectExports(root)

	for i := 0; i < root.NamedChildCount(); i++ {
		form := root.NamedChild(i)
		if form == nil {
			continue
		}
		if form.Kind() == "module_attribute" {
			if mn := e.extractErlangModule(form); mn != nil {
				moduleID = mn.ID
				e.nodeStack = append(e.nodeStack, moduleID)
			}
			break
		}
	}

	// Per-scope dedup of multi-clause functions by name/arity.
	seenFn := map[string]bool{}
	for i := 0; i < root.NamedChildCount(); i++ {
		form := root.NamedChild(i)
		if form == nil {
			continue
		}
		switch form.Kind() {
		case "module_attribute":
			// already handled
		case "behaviour_attribute":
			e.extractErlangBehaviour(form)
		case "record_decl":
			e.extractErlangRecord(form)
		case "pp_define":
			e.extractErlangDefine(form)
		case "pp_include", "pp_include_lib":
			e.extractErlangInclude(form)
		case "import_attribute":
			e.extractErlangImport(form)
		case "fun_decl":
			e.extractErlangFunction(form, exported, seenFn)
		}
	}

	if moduleID != "" {
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// erlangCollectExports scans -export attributes and returns the set of exported
// "name/arity" keys.
func (e *extractor) erlangCollectExports(root *tsparse.Node) map[string]bool {
	exported := map[string]bool{}
	for i := 0; i < root.NamedChildCount(); i++ {
		form := root.NamedChild(i)
		if form == nil || form.Kind() != "export_attribute" {
			continue
		}
		for _, fa := range erlangChildrenOfKind(form, "fa") {
			name, arity := erlangFA(fa)
			if name != "" {
				exported[name+"/"+arity] = true
			}
		}
	}
	return exported
}

// extractErlangModule handles `-module(foo).` → KindModule named foo.
func (e *extractor) extractErlangModule(form *tsparse.Node) *model.Node {
	name := erlangAttrAtomName(form)
	if name == "" {
		return nil
	}
	return e.createNode(model.KindModule, name, form, nodeExtra{signature: "-module(" + name + ")"})
}

// extractErlangBehaviour handles `-behaviour(mod).`/`-behavior(mod).` → an
// `implements` edge from the enclosing module to the behaviour module.
func (e *extractor) extractErlangBehaviour(form *tsparse.Node) {
	name := erlangAttrAtomName(form)
	if name == "" || len(e.nodeStack) == 0 {
		return
	}
	moduleID := e.nodeStack[len(e.nodeStack)-1]
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    moduleID,
		ReferenceName: name,
		ReferenceKind: model.EdgeImplements,
		Line:          int(form.StartPoint().Row) + 1,
		Column:        int(form.StartPoint().Column),
		FilePath:      e.filePath,
		Language:      model.LangErlang,
	})
}

// extractErlangRecord handles `-record(name, {a, b}).` → KindStruct named name
// plus one KindField per record_field.
func (e *extractor) extractErlangRecord(form *tsparse.Node) {
	name := erlangFieldAtomText(form, "name")
	if name == "" {
		return
	}
	sn := e.createNode(model.KindStruct, name, form, nodeExtra{signature: "-record(" + name + ")"})
	if sn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, sn.ID)
	for _, rf := range erlangChildrenOfKind(form, "record_field") {
		fname := erlangFieldAtomText(rf, "name")
		if fname == "" {
			// record_field's name may be a plain atom child.
			if a := erlangFirstChildOfKind(rf, "atom"); a != nil {
				fname = a.Text()
			}
		}
		if fname != "" {
			e.createNode(model.KindField, fname, rf, nodeExtra{})
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractErlangDefine handles `-define(NAME, value).` → KindConstant named NAME.
func (e *extractor) extractErlangDefine(form *tsparse.Node) {
	lhs := form.ChildByFieldName("lhs")
	if lhs == nil {
		return
	}
	nameNode := lhs.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}
	e.createNode(model.KindConstant, name, form, nodeExtra{signature: "-define(" + name + ")"})
}

// extractErlangInclude handles `-include("x.hrl").` / `-include_lib(...)` →
// KindImport plus an EdgeImports ref to the included file (by base name).
func (e *extractor) extractErlangInclude(form *tsparse.Node) {
	fileNode := form.ChildByFieldName("file")
	if fileNode == nil {
		return
	}
	raw := strings.Trim(fileNode.Text(), "\"")
	if raw == "" {
		return
	}
	base := raw
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	e.createNode(model.KindImport, base, form, nodeExtra{
		signature:     "-include(\"" + raw + "\")",
		qualifiedName: raw,
	})
	if len(e.nodeStack) > 0 {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    e.nodeStack[len(e.nodeStack)-1],
			ReferenceName: base,
			ReferenceKind: model.EdgeImports,
			Line:          int(form.StartPoint().Row) + 1,
			Column:        int(form.StartPoint().Column),
			FilePath:      e.filePath,
			Language:      model.LangErlang,
		})
	}
}

// extractErlangImport handles `-import(mod, [f/1]).` → KindImport named mod plus
// an EdgeImports ref to the imported module.
func (e *extractor) extractErlangImport(form *tsparse.Node) {
	modNode := form.ChildByFieldName("module")
	if modNode == nil {
		return
	}
	mod := modNode.Text()
	if mod == "" {
		return
	}
	e.createNode(model.KindImport, mod, form, nodeExtra{
		signature:     "-import(" + mod + ")",
		qualifiedName: mod,
	})
	if len(e.nodeStack) > 0 {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    e.nodeStack[len(e.nodeStack)-1],
			ReferenceName: mod,
			ReferenceKind: model.EdgeImports,
			Line:          int(form.StartPoint().Row) + 1,
			Column:        int(form.StartPoint().Column),
			FilePath:      e.filePath,
			Language:      model.LangErlang,
		})
	}
}

// extractErlangFunction handles a fun_decl. Multiple clauses of the same
// function appear as separate fun_decls; they are deduped by name/arity (only
// the first clause emits a node, but every clause's body is walked for calls).
func (e *extractor) extractErlangFunction(form *tsparse.Node, exported map[string]bool, seen map[string]bool) {
	clause := form.ChildByFieldName("clause")
	if clause == nil {
		clause = erlangFirstChildOfKind(form, "function_clause")
	}
	if clause == nil {
		return
	}
	nameNode := clause.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Text()
	if name == "" {
		return
	}
	arity := erlangClauseArity(clause)
	key := name + "/" + arity

	isExp := exported[key]
	vis := "private"
	if isExp {
		vis = "public"
	}
	visPtr := vis

	var fnID string
	if !seen[key] {
		seen[key] = true
		fn := e.createNode(model.KindFunction, name, form, nodeExtra{
			signature:  name + "/" + arity,
			visibility: &visPtr,
			isExported: isExp,
		})
		if fn == nil {
			return
		}
		fnID = fn.ID
	} else {
		// Subsequent clause: attribute its body calls to the existing function.
		fnID = model.GenerateNodeID(e.filePath, model.KindFunction, name, e.erlangFirstClauseLine(name, arity))
	}

	// Walk the clause body for call sites under the function scope.
	body := clause.ChildByFieldName("body")
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, fnID)
	e.erlangWalkCalls(body)
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// erlangFirstClauseLine returns the start line of the already-emitted function
// node named name with the given arity, so later clauses attribute calls to it.
func (e *extractor) erlangFirstClauseLine(name, arity string) int {
	sig := name + "/" + arity
	for i := range e.nodes {
		if e.nodes[i].Kind == model.KindFunction && e.nodes[i].Name == name && e.nodes[i].Signature == sig {
			return e.nodes[i].StartLine
		}
	}
	return 0
}

// erlangWalkCalls descends a subtree emitting EdgeCalls refs for each `call`
// node. Remote calls (`mod:func`) keep the `mod:func` name; local calls keep the
// bare function name. BIFs/auto-imported names are skipped.
func (e *extractor) erlangWalkCalls(node *tsparse.Node) {
	if node == nil {
		return
	}
	if node.Kind() == "call" {
		if name := erlangCalleeName(node); name != "" && !erlangIsBIF(name) && len(e.nodeStack) > 0 {
			e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
				FromNodeID:    e.nodeStack[len(e.nodeStack)-1],
				ReferenceName: name,
				ReferenceKind: model.EdgeCalls,
				Line:          int(node.StartPoint().Row) + 1,
				Column:        int(node.StartPoint().Column),
				FilePath:      e.filePath,
				Language:      model.LangErlang,
			})
		}
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		e.erlangWalkCalls(node.NamedChild(i))
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// erlangCalleeName returns the callee name for a `call` node. A `remote` expr
// (`mod:func`) yields `mod:func`; a bare atom yields the function name.
func erlangCalleeName(node *tsparse.Node) string {
	expr := node.ChildByFieldName("expr")
	if expr == nil {
		return ""
	}
	switch expr.Kind() {
	case "atom":
		return expr.Text()
	case "remote":
		fun := expr.ChildByFieldName("fun")
		mod := expr.ChildByFieldName("module")
		if fun == nil {
			return ""
		}
		modName := ""
		if mod != nil {
			if a := mod.ChildByFieldName("module"); a != nil {
				modName = a.Text()
			} else {
				modName = strings.TrimSuffix(mod.Text(), ":")
			}
		}
		if modName == "" {
			return fun.Text()
		}
		return modName + ":" + fun.Text()
	}
	return ""
}

// erlangClauseArity returns the arity (as a string) of a function_clause, from
// the named-child count of its expr_args.
func erlangClauseArity(clause *tsparse.Node) string {
	args := clause.ChildByFieldName("args")
	if args == nil {
		args = erlangFirstChildOfKind(clause, "expr_args")
	}
	if args == nil {
		return "0"
	}
	return strconv.Itoa(args.NamedChildCount())
}

// erlangFA returns (name, arity) of an `fa` node (`name/arity` export spec).
func erlangFA(fa *tsparse.Node) (string, string) {
	fun := fa.ChildByFieldName("fun")
	name := ""
	if fun != nil {
		name = fun.Text()
	} else if a := erlangFirstChildOfKind(fa, "atom"); a != nil {
		name = a.Text()
	}
	arity := "0"
	if ar := fa.ChildByFieldName("arity"); ar != nil {
		if v := ar.ChildByFieldName("value"); v != nil {
			arity = v.Text()
		} else {
			arity = strings.TrimPrefix(ar.Text(), "/")
		}
	}
	return name, arity
}

// erlangAttrAtomName returns the `name` atom text of an attribute form
// (module/behaviour), falling back to the first atom child.
func erlangAttrAtomName(form *tsparse.Node) string {
	if n := form.ChildByFieldName("name"); n != nil {
		return n.Text()
	}
	if a := erlangFirstChildOfKind(form, "atom"); a != nil {
		return a.Text()
	}
	return ""
}

// erlangFieldAtomText returns the text of the named field if it is present.
func erlangFieldAtomText(node *tsparse.Node, field string) string {
	if n := node.ChildByFieldName(field); n != nil {
		return n.Text()
	}
	return ""
}

// erlangFirstChildOfKind returns the first direct named child of the given kind.
func erlangFirstChildOfKind(node *tsparse.Node, kind string) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// erlangChildrenOfKind returns all direct named children of the given kind.
func erlangChildrenOfKind(node *tsparse.Node, kind string) []*tsparse.Node {
	var out []*tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == kind {
			out = append(out, c)
		}
	}
	return out
}

// erlangBIFs is a small skip set of the most common auto-imported BIFs whose
// bare call sites would otherwise create noise.
var erlangBIFs = map[string]bool{
	"length": true, "element": true, "setelement": true, "tuple_size": true,
	"is_list": true, "is_atom": true, "is_tuple": true, "is_integer": true,
	"is_float": true, "is_number": true, "is_binary": true, "is_pid": true,
	"is_map": true, "is_function": true, "is_boolean": true, "is_record": true,
	"hd": true, "tl": true, "abs": true, "trunc": true, "round": true,
	"spawn": true, "self": true, "node": true, "exit": true, "throw": true,
	"size": true, "byte_size": true, "bit_size": true, "map_size": true,
	"list_to_atom": true, "atom_to_list": true, "integer_to_list": true,
	"list_to_integer": true, "list_to_tuple": true, "tuple_to_list": true,
	"apply": true, "make_ref": true, "now": true, "error": true,
}

// erlangIsBIF reports whether a bare callee name is an auto-imported BIF.
// Remote `mod:func` calls are never BIFs here.
func erlangIsBIF(name string) bool {
	if strings.Contains(name, ":") {
		return false
	}
	return erlangBIFs[name]
}

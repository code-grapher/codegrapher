package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkHaskell walks a parsed Haskell (tree-sitter `haskell`) file root and
// extracts symbols. Haskell is functional — a module namespaces top-level
// bindings; type classes are the nearest analog to interfaces and `instance`
// declarations are their implementations.
//
// Grammar shape (confirmed by probe — see
// docs/superpowers/specs/2026-06-14-haskell-extraction-design.md):
//
//	haskell { header, imports, declarations }
//	header     { module: module(module_id…) }            → module name
//	import     { module, qualified?, alias?, import_list? }
//	signature  { name: variable, type }                  → f :: …
//	function   { name: variable, patterns, match{expression} } → f x = …
//	data_type/newtype { name, constructors: data_constructors{ data_constructor{ prefix|record } } }
//	type_synomym { name, type }                          → type alias
//	class      { name, type_params, class_declarations{ signature… } }
//	instance   { name (class), type_patterns (type), instance_declarations{ function… } }
//	apply      { function: variable|qualified, argument }  → call site
//
// Multi-equation functions (`f 0 = …` / `f n = …`) and a signature + binding
// of the same name collapse onto ONE function node: the first occurrence in a
// scope creates the node; later equations/signatures merge (signature → its
// Signature) and attribute their RHS calls to it.
func (e *extractor) walkHaskell(root *tsparse.Node) {
	// The module header (if present) becomes the enclosing module scope so
	// members qualify Module::name.
	header := haskellChildOfKind(root, "header")
	moduleScope := ""
	if header != nil {
		if name := haskellModuleName(header.ChildByFieldName("module")); name != "" {
			mn := e.createNode(model.KindModule, name, header, nodeExtra{signature: "module " + name})
			if mn != nil {
				moduleScope = mn.ID
				e.nodeStack = append(e.nodeStack, mn.ID)
			}
		}
	}

	if imports := haskellChildOfKind(root, "imports"); imports != nil {
		for i := 0; i < imports.NamedChildCount(); i++ {
			if imp := imports.NamedChild(i); imp != nil && imp.Kind() == "import" {
				e.extractHaskellImport(imp)
			}
		}
	}

	if decls := haskellChildOfKind(root, "declarations"); decls != nil {
		// fnNodes tracks function name → node ID within this declarations scope so
		// multiple equations / a signature+binding collapse onto one node.
		fnNodes := map[string]string{}
		for i := 0; i < decls.NamedChildCount(); i++ {
			child := decls.NamedChild(i)
			if child == nil {
				continue
			}
			e.visitHaskellDecl(child, fnNodes)
		}
	}

	if moduleScope != "" {
		e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
	}
}

// visitHaskellDecl dispatches a single top-level declaration. fnNodes lets
// repeated function names (multi-equation / signature+binding) reuse one node.
func (e *extractor) visitHaskellDecl(node *tsparse.Node, fnNodes map[string]string) {
	switch node.Kind() {
	case "signature":
		e.extractHaskellSignature(node, fnNodes)
	case "function", "bind":
		e.extractHaskellFunction(node, fnNodes)
	case "data_type", "newtype":
		e.extractHaskellData(node)
	case "type_synomym":
		e.extractHaskellTypeAlias(node)
	case "class":
		e.extractHaskellClass(node)
	case "instance":
		e.extractHaskellInstance(node)
	}
}

// extractHaskellSignature handles a top-level type signature `f :: …`. If the
// function node already exists in this scope, the signature is merged into its
// Signature; otherwise a function node is created carrying the signature.
func (e *extractor) extractHaskellSignature(node *tsparse.Node, fnNodes map[string]string) {
	name := haskellSigName(node)
	if name == "" {
		return
	}
	if id, ok := fnNodes[name]; ok {
		// Merge into existing node's signature.
		for i := range e.nodes {
			if e.nodes[i].ID == id && e.nodes[i].Signature == "" {
				e.nodes[i].Signature = strings.TrimSpace(node.Text())
				break
			}
		}
		e.emitHaskellTypeRefs(node.ChildByFieldName("type"), id)
		return
	}
	fn := e.createNode(model.KindFunction, name, node, nodeExtra{
		signature:  strings.TrimSpace(node.Text()),
		isExported: true,
	})
	if fn == nil {
		return
	}
	fnNodes[name] = fn.ID
	e.emitHaskellTypeRefs(node.ChildByFieldName("type"), fn.ID)
}

// extractHaskellFunction handles a binding equation `f x = …` (or pattern
// `bind`). Multiple equations of the same name attribute their RHS calls to the
// single function node (created on first occurrence, or reused from a prior
// signature).
func (e *extractor) extractHaskellFunction(node *tsparse.Node, fnNodes map[string]string) {
	name := haskellSigName(node) // name field is a `variable`, same accessor
	if name == "" {
		return
	}
	id, ok := fnNodes[name]
	if !ok {
		fn := e.createNode(model.KindFunction, name, node, nodeExtra{
			signature:  strings.TrimSpace(haskellFunctionHeadText(node)),
			isExported: true,
		})
		if fn == nil {
			return
		}
		id = fn.ID
		fnNodes[name] = id
	}
	e.walkHaskellBody(node, id)
}

// walkHaskellBody descends a function/bind body (its `match` expression) under
// scopeID, recording call sites.
func (e *extractor) walkHaskellBody(node *tsparse.Node, scopeID string) {
	e.nodeStack = append(e.nodeStack, scopeID)
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "match" || child.Kind() == "function_body" {
			e.walkHaskellExprs(child, scopeID)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// walkHaskellExprs recursively scans an expression subtree for call sites
// (`apply` heads and `infix` operators) and records EdgeCalls refs from
// callerID.
func (e *extractor) walkHaskellExprs(node *tsparse.Node, callerID string) {
	switch node.Kind() {
	case "apply":
		if fn := node.ChildByFieldName("function"); fn != nil {
			if name := haskellCalleeName(fn); name != "" && !haskellIsPreludeBuiltin(name) {
				e.addHaskellCall(callerID, name, node)
			}
		}
	case "infix":
		if op := node.ChildByFieldName("operator"); op != nil {
			if name := strings.TrimSpace(op.Text()); name != "" && !haskellIsPreludeBuiltin(name) {
				e.addHaskellCall(callerID, name, op)
			}
		}
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.walkHaskellExprs(child, callerID)
		}
	}
}

// addHaskellCall appends an unresolved EdgeCalls reference.
func (e *extractor) addHaskellCall(callerID, name string, node *tsparse.Node) {
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    callerID,
		ReferenceName: name,
		ReferenceKind: model.EdgeCalls,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
		FilePath:      e.filePath,
		Language:      model.LangHaskell,
	})
}

// extractHaskellData handles `data`/`newtype T = …` → KindStruct. Positional
// constructors become KindEnumMember; record fields become KindField.
func (e *extractor) extractHaskellData(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := strings.TrimSpace(nameNode.Text())
	if name == "" {
		return
	}
	sn := e.createNode(model.KindStruct, name, node, nodeExtra{signature: strings.TrimSpace(firstLine(node.Text()))})
	if sn == nil {
		return
	}
	cons := node.ChildByFieldName("constructors")
	if cons == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, sn.ID)
	for i := 0; i < cons.NamedChildCount(); i++ {
		dc := cons.NamedChild(i)
		if dc == nil || dc.Kind() != "data_constructor" {
			continue
		}
		inner := dc.ChildByFieldName("constructor")
		if inner == nil {
			continue
		}
		switch inner.Kind() {
		case "record":
			if cn := inner.ChildByFieldName("name"); cn != nil {
				e.createNode(model.KindEnumMember, strings.TrimSpace(cn.Text()), inner, nodeExtra{})
			}
			if fields := inner.ChildByFieldName("fields"); fields != nil {
				e.extractHaskellRecordFields(fields)
			}
		case "prefix":
			if cn := inner.ChildByFieldName("name"); cn != nil {
				e.createNode(model.KindEnumMember, strings.TrimSpace(cn.Text()), inner, nodeExtra{})
			}
		default:
			// Other constructor shapes (infix, etc.) — name by full text head.
			e.createNode(model.KindEnumMember, firstWord(inner.Text()), inner, nodeExtra{})
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractHaskellRecordFields emits a KindField per record field.
func (e *extractor) extractHaskellRecordFields(fields *tsparse.Node) {
	for i := 0; i < fields.NamedChildCount(); i++ {
		f := fields.NamedChild(i)
		if f == nil || f.Kind() != "field" {
			continue
		}
		fn := f.ChildByFieldName("name")
		if fn == nil {
			continue
		}
		name := strings.TrimSpace(fn.Text())
		if name == "" {
			continue
		}
		e.createNode(model.KindField, name, f, nodeExtra{signature: strings.TrimSpace(f.Text())})
	}
}

// extractHaskellTypeAlias handles `type Alias = …` → KindTypeAlias.
func (e *extractor) extractHaskellTypeAlias(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := strings.TrimSpace(nameNode.Text())
	if name == "" {
		return
	}
	tn := e.createNode(model.KindTypeAlias, name, node, nodeExtra{signature: strings.TrimSpace(node.Text())})
	if tn != nil {
		e.emitHaskellTypeRefs(node.ChildByFieldName("type"), tn.ID)
	}
}

// extractHaskellClass handles `class C a where …` → KindInterface; its method
// signatures become KindMethod members.
func (e *extractor) extractHaskellClass(node *tsparse.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := strings.TrimSpace(nameNode.Text())
	if name == "" {
		return
	}
	cn := e.createNode(model.KindInterface, name, node, nodeExtra{signature: strings.TrimSpace(firstLine(node.Text()))})
	if cn == nil {
		return
	}
	decls := node.ChildByFieldName("declarations")
	if decls == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, cn.ID)
	for i := 0; i < decls.NamedChildCount(); i++ {
		d := decls.NamedChild(i)
		if d == nil {
			continue
		}
		// class_declarations wrap each as a `signature` (possibly via a
		// `declaration` field) — accept signatures at any depth-1.
		sig := d
		if sig.Kind() != "signature" {
			if inner := haskellChildOfKind(sig, "signature"); inner != nil {
				sig = inner
			}
		}
		if sig.Kind() == "signature" {
			if mname := haskellSigName(sig); mname != "" {
				mn := e.createNode(model.KindMethod, mname, sig, nodeExtra{signature: strings.TrimSpace(sig.Text())})
				if mn != nil {
					e.emitHaskellTypeRefs(sig.ChildByFieldName("type"), mn.ID)
				}
			}
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractHaskellInstance handles `instance C T where …`: emits an `implements`
// ref (T → C) and walks the instance's method bindings as KindMethod under a
// synthetic `C.T` module scope so their names qualify distinctly.
func (e *extractor) extractHaskellInstance(node *tsparse.Node) {
	className := ""
	if cn := node.ChildByFieldName("name"); cn != nil {
		className = strings.TrimSpace(cn.Text())
	}
	typeName := haskellInstanceType(node)
	if className == "" {
		return
	}

	implName := className
	if typeName != "" {
		implName = className + "." + typeName
	}
	sig := "instance " + className
	if typeName != "" {
		sig += " " + typeName
	}
	in := e.createNode(model.KindModule, implName, node, nodeExtra{signature: sig})
	if in == nil {
		return
	}

	// implements: T → C. Anchored on the instance module node; the resolver
	// re-anchors to the concrete type T when it resolves.
	refName := className
	if typeName != "" {
		refName = typeName + "@" + className
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    in.ID,
		ReferenceName: refName,
		ReferenceKind: model.EdgeImplements,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
		FilePath:      e.filePath,
		Language:      model.LangHaskell,
	})

	decls := node.ChildByFieldName("declarations")
	if decls == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, in.ID)
	fnNodes := map[string]string{}
	for i := 0; i < decls.NamedChildCount(); i++ {
		d := decls.NamedChild(i)
		if d == nil {
			continue
		}
		fn := d
		if fn.Kind() != "function" && fn.Kind() != "bind" {
			if inner := haskellChildOfKind(fn, "function"); inner != nil {
				fn = inner
			}
		}
		if fn.Kind() == "function" || fn.Kind() == "bind" {
			name := haskellSigName(fn)
			if name == "" {
				continue
			}
			id, ok := fnNodes[name]
			if !ok {
				mn := e.createNode(model.KindMethod, name, fn, nodeExtra{signature: strings.TrimSpace(haskellFunctionHeadText(fn))})
				if mn == nil {
					continue
				}
				id = mn.ID
				fnNodes[name] = id
			}
			e.walkHaskellBody(fn, id)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractHaskellImport handles `import Foo.Bar (…)` / `import qualified Foo.Bar
// as B` → KindImport + an EdgeImports ref to the named module. The local
// binding name is the alias (`as`), else the module's last segment; the ref
// name is the full dotted module for through-import resolution.
func (e *extractor) extractHaskellImport(node *tsparse.Node) {
	fullModule := haskellModuleName(node.ChildByFieldName("module"))
	if fullModule == "" {
		return
	}
	localName := haskellLastSegment(fullModule)
	if alias := haskellModuleName(node.ChildByFieldName("alias")); alias != "" {
		localName = alias
	}
	e.createNode(model.KindImport, localName, node, nodeExtra{
		signature:     strings.TrimSpace(firstLine(node.Text())),
		qualifiedName: fullModule,
	})

	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}
	if parentID != "" {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    parentID,
			ReferenceName: haskellLastSegment(fullModule),
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
			FilePath:      e.filePath,
			Language:      model.LangHaskell,
		})
	}
}

// emitHaskellTypeRefs records EdgeReferences from fromID to each capitalized
// type `name` node referenced in a type subtree. Best-effort type-usage edges.
func (e *extractor) emitHaskellTypeRefs(typ *tsparse.Node, fromID string) {
	if typ == nil {
		return
	}
	seen := map[string]bool{}
	var rec func(n *tsparse.Node)
	rec = func(n *tsparse.Node) {
		if n == nil {
			return
		}
		if n.Kind() == "name" {
			t := strings.TrimSpace(n.Text())
			if t != "" && !haskellIsPrimitiveType(t) && !seen[t] {
				seen[t] = true
				e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
					FromNodeID:    fromID,
					ReferenceName: t,
					ReferenceKind: model.EdgeReferences,
					Line:          int(n.StartPoint().Row) + 1,
					Column:        int(n.StartPoint().Column),
					FilePath:      e.filePath,
					Language:      model.LangHaskell,
				})
			}
		}
		for i := 0; i < n.NamedChildCount(); i++ {
			rec(n.NamedChild(i))
		}
	}
	rec(typ)
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// haskellChildOfKind returns the first direct child of node with the given kind.
func haskellChildOfKind(node *tsparse.Node, kind string) *tsparse.Node {
	if node == nil {
		return nil
	}
	for i := 0; i < node.ChildCount(); i++ {
		if c := node.Child(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// haskellModuleName returns the dotted text of a `module` node (Foo.Bar.Baz),
// joining its module_id segments.
func haskellModuleName(mod *tsparse.Node) string {
	if mod == nil {
		return ""
	}
	var parts []string
	for i := 0; i < mod.NamedChildCount(); i++ {
		if c := mod.NamedChild(i); c != nil && c.Kind() == "module_id" {
			parts = append(parts, strings.TrimSpace(c.Text()))
		}
	}
	if len(parts) == 0 {
		return strings.TrimSpace(mod.Text())
	}
	return strings.Join(parts, ".")
}

// haskellSigName returns the `name` field's text of a signature/function node
// (the bound `variable`).
func haskellSigName(node *tsparse.Node) string {
	n := node.ChildByFieldName("name")
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Text())
}

// haskellFunctionHeadText returns the head text of a function equation up to and
// including its patterns (drops the RHS), as a compact signature.
func haskellFunctionHeadText(node *tsparse.Node) string {
	name := haskellSigName(node)
	if p := node.ChildByFieldName("patterns"); p != nil {
		return name + " " + strings.TrimSpace(p.Text())
	}
	return name
}

// haskellCalleeName returns the callee name for an `apply` function child. A
// qualified callee (`Map.insert`) keeps its `Module.func` form; a bare variable
// returns its text.
func haskellCalleeName(node *tsparse.Node) string {
	switch node.Kind() {
	case "variable", "constructor":
		return strings.TrimSpace(node.Text())
	case "qualified":
		return strings.TrimSpace(node.Text())
	case "apply":
		// Curried application: head is the leftmost function.
		if f := node.ChildByFieldName("function"); f != nil {
			return haskellCalleeName(f)
		}
	}
	// Operators/parenthesized — skip.
	return ""
}

// haskellInstanceType returns the head type name of an instance's type_patterns
// (the T in `instance C T`).
func haskellInstanceType(node *tsparse.Node) string {
	tp := node.ChildByFieldName("patterns")
	if tp == nil {
		return ""
	}
	for i := 0; i < tp.NamedChildCount(); i++ {
		c := tp.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == "name" {
			return strings.TrimSpace(c.Text())
		}
		// `apply`/parenthesized type head — take its first name.
		if w := firstWord(c.Text()); w != "" {
			return w
		}
	}
	return ""
}

// haskellLastSegment returns the final dotted segment (A.B.C → C).
func haskellLastSegment(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// haskellPrimitiveTypes is a small skip set so common base types don't create
// noisy `references` edges to nonexistent nodes.
var haskellPrimitiveTypes = map[string]bool{
	"Int": true, "Integer": true, "Double": true, "Float": true, "Bool": true,
	"Char": true, "String": true, "Word": true, "Maybe": true, "Either": true,
	"IO": true, "Ordering": true, "Rational": true, "()": true,
}

func haskellIsPrimitiveType(name string) bool { return haskellPrimitiveTypes[name] }

// haskellPreludeBuiltins is a small skip set of the most common auto-imported
// Prelude functions/operators whose call sites would otherwise create noise.
var haskellPreludeBuiltins = map[string]bool{
	"map": true, "filter": true, "foldr": true, "foldl": true, "foldl'": true,
	"show": true, "read": true, "return": true, "pure": true, "print": true,
	"putStrLn": true, "putStr": true, "getLine": true, "fmap": true, "mapM": true,
	"mapM_": true, "forM": true, "forM_": true, "sequence": true, "sequence_": true,
	"length": true, "head": true, "tail": true, "init": true, "last": true,
	"reverse": true, "concat": true, "concatMap": true, "zip": true, "zipWith": true,
	"elem": true, "notElem": true, "lookup": true, "fst": true, "snd": true,
	"id": true, "const": true, "flip": true, "curry": true, "uncurry": true,
	"not": true, "otherwise": true, "error": true, "undefined": true, "fromIntegral": true,
	"realToFrac": true, "min": true, "max": true, "abs": true, "negate": true,
	"sum": true, "product": true, "maximum": true, "minimum": true, "all": true,
	"any": true, "and": true, "or": true, "take": true, "drop": true,
	// Common operators.
	"+": true, "-": true, "*": true, "/": true, "++": true, ".": true, "$": true,
	"==": true, "/=": true, "<": true, ">": true, "<=": true, ">=": true,
	"&&": true, "||": true, ">>=": true, ">>": true, "<$>": true, "<*>": true,
	"!!": true, ":": true,
}

// haskellIsPreludeBuiltin reports whether name is an auto-imported Prelude
// builtin (bare names/operators; qualified Module.func calls are never builtins).
func haskellIsPreludeBuiltin(name string) bool {
	if strings.Contains(name, ".") && !strings.HasPrefix(name, ".") {
		// A dotted name that isn't the `.` operator is qualified — keep it.
		return false
	}
	return haskellPreludeBuiltins[name]
}

package extract

import (
	"slices"
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkElixir walks a parsed Elixir (tree-sitter `elixir`) file root and extracts
// symbols. Elixir is functional — modules namespace functions; there are no
// classes. `defprotocol`/`defimpl` are the nearest analog to interfaces.
//
// Grammar reality: tree-sitter-elixir is LOW-LEVEL. Nearly every construct
// (`defmodule`, `def`, `defp`, `defmacro`, `defstruct`, `defprotocol`,
// `defimpl`, `import`, `alias`, `require`, `use`) is a `call` node — a `call`
// with a `target` identifier, an `arguments` child, and an optional `do_block`.
// The walker recognizes these by the call target's TEXT, not by node kind.
//
// Node shapes (confirmed by probe):
//
//	call { target: identifier|dot, arguments, do_block? }
//	defmodule Name do … end       → call(target=defmodule, args=[alias Name], do_block)
//	def name(p), do: x            → call(target=def, args=[call(name,(p)), keywords{do:}])
//	def name(p) do … end          → call(target=def, args=[call(name,(p))], do_block)
//	defstruct [:a, :b]            → call(target=defstruct, args=[list of atoms])
//	alias A.B.C, as: D            → call(target=alias, args=[alias A.B.C, keywords{as:}])
//	defimpl Proto, for: Type      → call(target=defimpl, args=[alias Proto, keywords{for:}])
//	Module.func(x)                → call(target=dot{left:alias, right:identifier}, args)
//	@attr value                   → unary_operator(@, operand=call(attr, value))
func (e *extractor) walkElixir(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		if child := root.NamedChild(i); child != nil {
			e.visitNodeElixir(child)
		}
	}
}

// visitNodeElixir dispatches a single node. `call` nodes dispatch on their
// target text; other node kinds descend into named children so nested calls and
// definitions are still seen.
func (e *extractor) visitNodeElixir(node *tsparse.Node) {
	switch node.Kind() {
	case "call":
		e.visitElixirCall(node)
	case "unary_operator":
		e.extractElixirAttribute(node)
	default:
		e.visitElixirChildren(node)
	}
}

// visitElixirChildren descends into a node's named children without emitting a
// node for the container itself.
func (e *extractor) visitElixirChildren(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		if child := node.NamedChild(i); child != nil {
			e.visitNodeElixir(child)
		}
	}
}

// visitElixirCall handles a `call` node, dispatching on the target identifier's
// text. A dotted target (`Module.func`) or a non-macro identifier is a function
// call site.
func (e *extractor) visitElixirCall(node *tsparse.Node) {
	target := node.ChildByFieldName("target")
	if target == nil {
		return
	}

	if target.Kind() == "identifier" {
		switch target.Text() {
		case "defmodule":
			e.extractElixirModule(node)
			return
		case "def", "defp", "defmacro", "defmacrop":
			e.extractElixirFunction(node, target.Text())
			return
		case "defstruct":
			e.extractElixirStruct(node)
			return
		case "defprotocol":
			e.extractElixirProtocol(node)
			return
		case "defimpl":
			e.extractElixirImpl(node)
			return
		case "import", "alias", "require", "use":
			e.extractElixirImport(node, target.Text())
			return
		}
	}

	// Anything else is a call site (bare `func(...)` or `Module.func(...)`).
	e.extractElixirCallSite(node)
}

// extractElixirModule handles `defmodule Name do … end` → KindModule. The module
// name is pushed onto the node stack so member functions qualify as `A.B.C::func`.
func (e *extractor) extractElixirModule(node *tsparse.Node) {
	name := elixirFirstAliasName(node)
	if name == "" {
		return
	}
	mn := e.createNode(model.KindModule, name, node, nodeExtra{signature: "defmodule " + name})
	if mn == nil {
		return
	}
	e.walkElixirBody(node, mn.ID)
}

// extractElixirProtocol handles `defprotocol Name do … end` → KindInterface; its
// `def` signatures become methods of the protocol.
func (e *extractor) extractElixirProtocol(node *tsparse.Node) {
	name := elixirFirstAliasName(node)
	if name == "" {
		return
	}
	pn := e.createNode(model.KindInterface, name, node, nodeExtra{signature: "defprotocol " + name})
	if pn == nil {
		return
	}
	e.walkElixirBody(node, pn.ID)
}

// extractElixirImpl handles `defimpl Proto, for: Type do … end`: emits an
// `implements` ref (Type → Proto) and walks the impl's `def`s as methods. The
// impl block is scoped under a synthetic module name "Proto.Type" so its
// functions qualify distinctly and resolve back to the protocol.
func (e *extractor) extractElixirImpl(node *tsparse.Node) {
	args := elixirArgs(node)
	if args == nil {
		return
	}
	proto := ""
	forType := ""
	for i := 0; i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg == nil {
			continue
		}
		switch arg.Kind() {
		case "alias":
			if proto == "" {
				proto = arg.Text()
			}
		case "keywords":
			if v := elixirKeywordValue(arg, "for"); v != "" {
				forType = v
			}
		}
	}
	if proto == "" {
		return
	}

	implName := proto
	if forType != "" {
		implName = proto + "." + forType
	}
	implSig := "defimpl " + proto
	if forType != "" {
		implSig += ", for: " + forType
	}
	in := e.createNode(model.KindModule, implName, node, nodeExtra{signature: implSig})
	if in == nil {
		return
	}

	// implements: Type → Proto. Anchored on the impl module node; if the
	// concrete Type resolves to a module, the resolver re-anchors it.
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    in.ID,
		ReferenceName: proto,
		ReferenceKind: model.EdgeImplements,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
		FilePath:      e.filePath,
		Language:      model.LangElixir,
	})

	e.walkElixirBody(node, in.ID)
}

// walkElixirBody pushes scopeID and visits the statements inside the call's
// do_block.
func (e *extractor) walkElixirBody(node *tsparse.Node, scopeID string) {
	body := elixirChildOfKind(node, "do_block")
	if body == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, scopeID)
	for i := 0; i < body.NamedChildCount(); i++ {
		if child := body.NamedChild(i); child != nil {
			e.visitNodeElixir(child)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractElixirFunction handles def/defp/defmacro/defmacrop. The function name
// and parameters live in the first argument, which is itself a `call`
// (target=name, arguments=params) or a bare identifier (zero-arg). The body is
// either a sibling `do_block` or a `do:` keyword pair inside arguments.
func (e *extractor) extractElixirFunction(node *tsparse.Node, macro string) {
	args := elixirArgs(node)
	if args == nil {
		return
	}
	head := args.NamedChild(0)
	if head == nil {
		return
	}

	name, params := elixirFunctionHead(head)
	if name == "" {
		return
	}

	private := macro == "defp" || macro == "defmacrop"
	vis := "public"
	if private {
		vis = "private"
	}
	visPtr := vis

	kind := model.KindFunction
	if e.isInsideClassLike() {
		// Inside a defprotocol/defimpl/defmodule the def is a method; but to keep
		// parity with the spec (modules namespace functions) we keep KindFunction
		// for modules and KindMethod for protocol/impl interface members.
		if e.topIsElixirInterface() {
			kind = model.KindMethod
		}
	}

	sig := macro + " " + name + params
	fn := e.createNode(kind, name, node, nodeExtra{
		signature:  sig,
		visibility: &visPtr,
		isExported: !private,
	})
	if fn == nil {
		return
	}

	// Walk the body for call sites. Body is a sibling do_block or a do: keyword.
	e.nodeStack = append(e.nodeStack, fn.ID)
	if db := elixirChildOfKind(node, "do_block"); db != nil {
		for i := 0; i < db.NamedChildCount(); i++ {
			if c := db.NamedChild(i); c != nil {
				e.visitNodeElixir(c)
			}
		}
	}
	// `, do: expr` keyword form: scan trailing keywords in arguments.
	for i := 1; i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg != nil && arg.Kind() == "keywords" {
			e.visitElixirKeywordValues(arg)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// visitElixirKeywordValues visits the value of each pair in a keywords node
// (used for `, do: expr` and similar inline bodies).
func (e *extractor) visitElixirKeywordValues(kw *tsparse.Node) {
	for i := 0; i < kw.NamedChildCount(); i++ {
		pair := kw.NamedChild(i)
		if pair == nil || pair.Kind() != "pair" {
			continue
		}
		if v := pair.ChildByFieldName("value"); v != nil {
			e.visitNodeElixir(v)
		}
	}
}

// extractElixirStruct handles `defstruct [...]` → KindStruct named after the
// enclosing module, plus one KindField per keyword/atom entry.
func (e *extractor) extractElixirStruct(node *tsparse.Node) {
	modName := e.nearestElixirModuleName()
	if modName == "" {
		modName = "Struct"
	}
	sn := e.createNode(model.KindStruct, modName, node, nodeExtra{signature: "defstruct"})
	if sn == nil {
		return
	}

	args := elixirArgs(node)
	if args == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, sn.ID)
	for i := 0; i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg == nil {
			continue
		}
		switch arg.Kind() {
		case "list":
			e.extractElixirStructFields(arg)
		case "keywords":
			e.extractElixirStructFields(arg)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractElixirStructFields emits a KindField per atom (`:name`) or keyword key
// (`name:`) inside a defstruct list/keywords node.
func (e *extractor) extractElixirStructFields(container *tsparse.Node) {
	for i := 0; i < container.NamedChildCount(); i++ {
		el := container.NamedChild(i)
		if el == nil {
			continue
		}
		switch el.Kind() {
		case "atom":
			name := strings.TrimPrefix(el.Text(), ":")
			e.createNode(model.KindField, name, el, nodeExtra{})
		case "pair":
			if k := el.ChildByFieldName("key"); k != nil {
				name := strings.TrimSuffix(strings.TrimSpace(k.Text()), ":")
				e.createNode(model.KindField, name, el, nodeExtra{})
			}
		}
	}
}

// extractElixirImport handles import/alias/require/use → KindImport plus an
// EdgeImports ref from the current scope to the named module. For `alias A.B.C`
// the imported name is the last segment (or the `as:` rename); the ref name is
// the full dotted module so the resolver can find the real definition.
func (e *extractor) extractElixirImport(node *tsparse.Node, macro string) {
	args := elixirArgs(node)
	if args == nil {
		return
	}
	first := args.NamedChild(0)
	if first == nil || first.Kind() != "alias" {
		return
	}
	fullModule := first.Text()
	if fullModule == "" {
		return
	}

	// Local binding name: last segment, or the `as:` rename for alias.
	localName := elixirLastSegment(fullModule)
	if macro == "alias" {
		if as := elixirKeywordValueFromArgs(args, "as"); as != "" {
			localName = elixirLastSegment(as)
		}
	}

	e.createNode(model.KindImport, localName, node, nodeExtra{
		signature:     macro + " " + fullModule,
		qualifiedName: fullModule,
	})

	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}
	if parentID != "" {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    parentID,
			ReferenceName: elixirLastSegment(fullModule),
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
			FilePath:      e.filePath,
			Language:      model.LangElixir,
		})
	}
}

// extractElixirCallSite handles a function call site. `Module.func(...)` keeps
// the dotted `Module.func` name; a bare `func(...)` is a local call. Kernel
// builtins are skipped. Arguments are descended for nested calls.
func (e *extractor) extractElixirCallSite(node *tsparse.Node) {
	name := elixirCalleeName(node)
	if name != "" && !elixirIsKernelBuiltin(name) && len(e.nodeStack) > 0 {
		callerID := e.nodeStack[len(e.nodeStack)-1]
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    callerID,
			ReferenceName: name,
			ReferenceKind: model.EdgeCalls,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
			FilePath:      e.filePath,
			Language:      model.LangElixir,
		})
	}
	if args := elixirArgs(node); args != nil {
		e.visitElixirChildren(args)
	}
}

// extractElixirAttribute handles a module attribute `@name value`. Doc
// attributes (@moduledoc/@doc/@typedoc/@spec/@type/@behaviour/@callback) are
// skipped; a constant-style `@x value` becomes a KindConstant.
func (e *extractor) extractElixirAttribute(node *tsparse.Node) {
	operand := node.ChildByFieldName("operand")
	if operand == nil {
		// Not an `@` attribute (could be unary minus etc.) — descend.
		e.visitElixirChildren(node)
		return
	}
	// `@name value` parses as @ + call(target=name, arguments=value).
	if operand.Kind() != "call" {
		return
	}
	target := operand.ChildByFieldName("target")
	if target == nil || target.Kind() != "identifier" {
		return
	}
	attrName := target.Text()
	switch attrName {
	case "moduledoc", "doc", "typedoc", "spec", "type", "typep", "opaque",
		"behaviour", "behavior", "callback", "macrocallback", "impl", "derive",
		"enforce_keys", "deprecated", "dialyzer", "compile":
		return
	}
	e.createNode(model.KindConstant, attrName, node, nodeExtra{signature: "@" + attrName})
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// elixirFirstAliasName returns the text of the first `alias` argument of a call
// (the module/protocol name in defmodule/defprotocol).
func elixirFirstAliasName(node *tsparse.Node) string {
	args := elixirArgs(node)
	if args == nil {
		return ""
	}
	for i := 0; i < args.NamedChildCount(); i++ {
		if a := args.NamedChild(i); a != nil && a.Kind() == "alias" {
			return a.Text()
		}
	}
	return ""
}

// elixirFunctionHead extracts (name, params) from a function head node. The head
// is a `call` (target=name, arguments=params) for `def f(x)`, or a bare
// identifier for `def f` (zero-arg).
func elixirFunctionHead(head *tsparse.Node) (string, string) {
	switch head.Kind() {
	case "call":
		t := head.ChildByFieldName("target")
		if t == nil {
			return "", ""
		}
		name := t.Text()
		// A dotted target on a def head (rare) — keep last segment.
		if t.Kind() == "dot" {
			if r := t.ChildByFieldName("right"); r != nil {
				name = r.Text()
			}
		}
		params := ""
		if a := elixirArgs(head); a != nil {
			params = a.Text()
		}
		return name, params
	case "identifier":
		return head.Text(), ""
	case "binary_operator":
		// `def f(x) when guard` — the left side is the real head.
		if l := head.ChildByFieldName("left"); l != nil {
			return elixirFunctionHead(l)
		}
	}
	return "", ""
}

// elixirCalleeName returns the callee name for a call site. A dotted target
// (`Module.func`) keeps the `Module.func` form; a bare identifier returns the
// identifier.
func elixirCalleeName(node *tsparse.Node) string {
	target := node.ChildByFieldName("target")
	if target == nil {
		return ""
	}
	switch target.Kind() {
	case "identifier":
		return target.Text()
	case "dot":
		left := target.ChildByFieldName("left")
		right := target.ChildByFieldName("right")
		if right == nil {
			return ""
		}
		if left == nil {
			return right.Text()
		}
		return left.Text() + "." + right.Text()
	}
	return ""
}

// elixirArgs returns the `arguments` child of a call node. In tree-sitter-elixir
// `arguments` is an unnamed-field child (a plain named child of kind
// "arguments"), so it must be located by kind rather than field name.
func elixirArgs(node *tsparse.Node) *tsparse.Node {
	return elixirChildOfKind(node, "arguments")
}

// elixirChildOfKind returns the first direct child of node with the given kind.
func elixirChildOfKind(node *tsparse.Node, kind string) *tsparse.Node {
	for i := 0; i < node.ChildCount(); i++ {
		if c := node.Child(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

// elixirKeywordValue returns the alias/identifier text of the value for keyword
// `key` (without trailing colon) inside a `keywords` node.
func elixirKeywordValue(kw *tsparse.Node, key string) string {
	for i := 0; i < kw.NamedChildCount(); i++ {
		pair := kw.NamedChild(i)
		if pair == nil || pair.Kind() != "pair" {
			continue
		}
		k := pair.ChildByFieldName("key")
		if k == nil {
			continue
		}
		if strings.TrimSuffix(strings.TrimSpace(k.Text()), ":") == key {
			if v := pair.ChildByFieldName("value"); v != nil {
				return v.Text()
			}
		}
	}
	return ""
}

// elixirKeywordValueFromArgs scans an arguments node for a keywords child and
// returns the value text for key.
func elixirKeywordValueFromArgs(args *tsparse.Node, key string) string {
	for i := 0; i < args.NamedChildCount(); i++ {
		if a := args.NamedChild(i); a != nil && a.Kind() == "keywords" {
			if v := elixirKeywordValue(a, key); v != "" {
				return v
			}
		}
	}
	return ""
}

// elixirLastSegment returns the final segment of a dotted module name
// (A.B.C → C).
func elixirLastSegment(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// nearestElixirModuleName returns the Name of the nearest KindModule on the node
// stack (the enclosing module for a defstruct).
func (e *extractor) nearestElixirModuleName() string {
	for _, id := range slices.Backward(e.nodeStack) {

		for j := range e.nodes {
			if e.nodes[j].ID == id && e.nodes[j].Kind == model.KindModule {
				return e.nodes[j].Name
			}
		}
	}
	return ""
}

// topIsElixirInterface reports whether the top of the node stack is a
// KindInterface (a defprotocol), so its `def`s become methods.
func (e *extractor) topIsElixirInterface() bool {
	if len(e.nodeStack) == 0 {
		return false
	}
	top := e.nodeStack[len(e.nodeStack)-1]
	for i := range e.nodes {
		if e.nodes[i].ID == top {
			return e.nodes[i].Kind == model.KindInterface
		}
	}
	return false
}

// elixirKernelBuiltins is a small skip set of the most common auto-imported
// Kernel / standard-library names whose call sites would otherwise create noise.
var elixirKernelBuiltins = map[string]bool{
	// Kernel control/value functions and guards.
	"is_nil": true, "is_atom": true, "is_binary": true, "is_list": true,
	"is_map": true, "is_tuple": true, "is_integer": true, "is_float": true,
	"is_number": true, "is_boolean": true, "is_function": true, "is_pid": true,
	"to_string": true, "to_charlist": true, "inspect": true, "raise": true,
	"throw": true, "send": true, "spawn": true, "self": true, "make_ref": true,
	"length": true, "hd": true, "tl": true, "elem": true, "tuple_size": true,
	"map_size": true, "byte_size": true, "abs": true, "max": true, "min": true,
	"round": true, "trunc": true, "div": true, "rem": true, "apply": true,
	"put_in": true, "get_in": true, "update_in": true, "pop_in": true,
	// Pseudo-keyword macros that surface as calls.
	"if": true, "unless": true, "cond": true, "case": true, "for": true,
	"with": true, "try": true, "receive": true, "quote": true, "unquote": true,
	"fn": true, "raise!": true,
}

// elixirIsKernelBuiltin reports whether a callee name is an auto-imported Kernel
// builtin (bare names only; dotted Module.func calls are never builtins here).
func elixirIsKernelBuiltin(name string) bool {
	if strings.Contains(name, ".") {
		return false
	}
	return elixirKernelBuiltins[name]
}

package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkPowerShell walks a parsed PowerShell (tree-sitter `powershell`) file root
// and extracts symbols. PowerShell is a scripting language with functions plus
// PS5+ `class`/`enum` declarations, so the graph mixes the thin scripting model
// (functions, command calls, dot-source/Import-Module imports — like Lua/R) with
// the static-class model (classes, methods, properties, extends/implements).
//
// Node-type reference (tree-sitter-powershell), confirmed by a probe test:
//
//	program > statement_list > statements
//	function_statement   (field function_name; params via param_block in the
//	                       script_block, or a function_parameter_declaration)
//	class_statement      (simple_name name; optional ": Base, IFoo" bases as
//	                       further simple_name children; class_property_definition
//	                       {attribute>type_literal, variable}; class_method_definition
//	                       {attribute return type, simple_name, params, script_block})
//	enum_statement       (simple_name; enum_member > simple_name)
//	command              (field command_name, field command_elements) — call;
//	                       dot-source = command with command_invokation_operator "."
//	                       and command_name_expr > command_name
//	invokation_expression — `[T]::new()` (type_literal, ::, member_name, argument_list)
//	                       or `$o.M()` (variable, ., member_name, argument_list)
//	assignment_expression (left_assignment_expression, field value) — `$Var = …`
func (e *extractor) walkPowerShell(root *tsparse.Node) {
	e.psWalkChildren(root)
}

// psWalkChildren visits every named child of node, dispatching statements and
// descending into containers/expressions so nested calls are still seen.
func (e *extractor) psWalkChildren(node *tsparse.Node) {
	for i := 0; i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child == nil {
			continue
		}
		e.visitNodePowerShell(child)
	}
}

// visitNodePowerShell dispatches a single PowerShell node.
func (e *extractor) visitNodePowerShell(node *tsparse.Node) {
	switch node.Kind() {
	case "function_statement":
		e.extractPSFunction(node)
	case "class_statement":
		e.extractPSClass(node)
	case "enum_statement":
		e.extractPSEnum(node)
	case "command":
		e.extractPSCommand(node)
	case "invokation_expression":
		e.extractPSInvokation(node)
	case "assignment_expression":
		e.extractPSAssignment(node)
	default:
		e.psWalkChildren(node)
	}
}

// extractPSFunction handles a function_statement → KindFunction. Its body is
// walked under the function scope so nested calls attribute to it.
func (e *extractor) extractPSFunction(node *tsparse.Node) {
	nameNode := psFirstChildOfKind(node, "function_name")
	if nameNode == nil {
		return
	}
	name := strings.TrimSpace(nameNode.Text())
	if name == "" {
		return
	}
	fn := e.createNode(model.KindFunction, name, node, nodeExtra{signature: psFunctionSignature(node, name)})
	if fn == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, fn.ID)
	e.psWalkChildren(node)
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractPSClass handles a class_statement → KindClass. The first simple_name is
// the class name; any further simple_name children (after a ":") are bases —
// the first is an extends reference, the rest implements (best-effort, matching
// the other static languages). Properties and methods are emitted under the class.
func (e *extractor) extractPSClass(node *tsparse.Node) {
	var names []*tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "simple_name" {
			names = append(names, c)
		}
	}
	if len(names) == 0 {
		return
	}
	className := strings.TrimSpace(names[0].Text())
	cls := e.createNode(model.KindClass, className, node, nodeExtra{signature: "class " + className})
	if cls == nil {
		return
	}

	// Bases: first → extends, rest → implements.
	for i := 1; i < len(names); i++ {
		base := strings.TrimSpace(names[i].Text())
		if base == "" {
			continue
		}
		kind := model.EdgeImplements
		if i == 1 {
			kind = model.EdgeExtends
		}
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    cls.ID,
			ReferenceName: base,
			ReferenceKind: kind,
			Line:          int(names[i].StartPoint().Row) + 1,
			Column:        int(names[i].StartPoint().Column),
		})
	}

	e.nodeStack = append(e.nodeStack, cls.ID)
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "class_property_definition":
			e.extractPSProperty(c)
		case "class_method_definition":
			e.extractPSMethod(c, className)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractPSProperty handles a class_property_definition → KindProperty.
func (e *extractor) extractPSProperty(node *tsparse.Node) {
	var v *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "variable" {
			v = c
			break
		}
	}
	if v == nil {
		return
	}
	name := psStripSigil(v.Text())
	if name == "" {
		return
	}
	e.createNode(model.KindProperty, name, node, nodeExtra{signature: strings.TrimSpace(node.Text())})
}

// extractPSMethod handles a class_method_definition → KindMethod (a method named
// like the class is the constructor, still a KindMethod). Its body is walked so
// method-internal calls attribute to it.
func (e *extractor) extractPSMethod(node *tsparse.Node, className string) {
	var nameNode *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "simple_name" {
			nameNode = c
			break
		}
	}
	if nameNode == nil {
		return
	}
	name := strings.TrimSpace(nameNode.Text())
	if name == "" {
		return
	}
	m := e.createNode(model.KindMethod, name, node, nodeExtra{signature: psMethodSignature(node, name)})
	if m == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, m.ID)
	if body := node.ChildByFieldName("script_block"); body != nil {
		e.psWalkChildren(body)
	} else {
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); c != nil && c.Kind() == "script_block" {
				e.psWalkChildren(c)
			}
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractPSEnum handles an enum_statement → KindEnum with KindEnumMember members.
func (e *extractor) extractPSEnum(node *tsparse.Node) {
	var nameNode *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "simple_name" {
			nameNode = c
			break
		}
	}
	if nameNode == nil {
		return
	}
	name := strings.TrimSpace(nameNode.Text())
	en := e.createNode(model.KindEnum, name, node, nodeExtra{signature: "enum " + name})
	if en == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, en.ID)
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil || c.Kind() != "enum_member" {
			continue
		}
		member := strings.TrimSpace(c.Text())
		if sn := psFirstChildOfKind(c, "simple_name"); sn != nil {
			member = strings.TrimSpace(sn.Text())
		}
		e.createNode(model.KindEnumMember, member, c, nodeExtra{})
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractPSCommand handles a command node: dot-source (`. ./lib.ps1`) and
// `Import-Module`/`using module` emit imports; `New-Object C` emits an
// instantiation; any other command whose name matches an in-repo function emits
// a call (the resolver drops names with no in-repo match, so built-in cmdlets
// like Write-Host produce no edge). Arguments are walked for nested calls.
func (e *extractor) extractPSCommand(node *tsparse.Node) {
	// Dot-source: command_invokation_operator "." + command_name_expr path.
	if psIsDotSource(node) {
		e.extractPSDotSource(node)
		return
	}

	nameNode := node.ChildByFieldName("command_name")
	cmdName := ""
	if nameNode != nil {
		cmdName = strings.TrimSpace(nameNode.Text())
	}

	switch {
	case strings.EqualFold(cmdName, "Import-Module"):
		e.extractPSImportModule(node)
		return
	case strings.EqualFold(cmdName, "using"):
		// `using module Foo` — second token is the module name.
		if mod := psCommandArg(node, 1); mod != "" {
			e.emitPSImport(mod, node)
		}
		return
	case strings.EqualFold(cmdName, "New-Object"):
		// `New-Object C` — first argument is the class to instantiate.
		if cls := psCommandArg(node, 0); cls != "" {
			e.emitPSRef(cls, model.EdgeInstantiates, node)
		}
		e.psWalkCommandElements(node)
		return
	}

	if cmdName != "" && !psIsCommonCmdlet(cmdName) {
		e.emitPSRef(cmdName, model.EdgeCalls, node)
	}
	e.psWalkCommandElements(node)
}

// psWalkCommandElements descends into a command's argument elements for nested
// calls/invocations.
func (e *extractor) psWalkCommandElements(node *tsparse.Node) {
	if el := node.ChildByFieldName("command_elements"); el != nil {
		e.psWalkChildren(el)
	}
}

// extractPSDotSource handles `. ./lib.ps1` → KindImport + through-source import
// (resolves to the in-repo file, like Lua's require).
func (e *extractor) extractPSDotSource(node *tsparse.Node) {
	var path string
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == "command_name_expr" || c.Kind() == "command_name" {
			path = strings.TrimSpace(c.Text())
			break
		}
	}
	if path == "" {
		return
	}
	name := psModuleBasename(path)
	if name == "" {
		return
	}
	e.emitPSImport(name, node)
}

// extractPSImportModule handles `Import-Module Foo` → KindImport.
func (e *extractor) extractPSImportModule(node *tsparse.Node) {
	mod := psCommandArg(node, 0)
	if mod == "" {
		return
	}
	e.emitPSImport(psModuleBasename(mod), node)
}

// emitPSImport creates a KindImport node named module and an EdgeImports ref from
// the current scope (resolved through to the in-repo file when one matches).
func (e *extractor) emitPSImport(module string, node *tsparse.Node) {
	module = strings.Trim(module, `"'`)
	if module == "" {
		return
	}
	var parentID string
	if len(e.nodeStack) > 0 {
		parentID = e.nodeStack[len(e.nodeStack)-1]
	}
	e.createNode(model.KindImport, module, node, nodeExtra{signature: strings.TrimSpace(node.Text())})
	if parentID != "" {
		e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
			FromNodeID:    parentID,
			ReferenceName: module,
			ReferenceKind: model.EdgeImports,
			Line:          int(node.StartPoint().Row) + 1,
			Column:        int(node.StartPoint().Column),
		})
	}
}

// extractPSInvokation handles an invokation_expression: `[C]::new(...)` →
// instantiates C; `$obj.Method(...)` → calls Method (receiver stripped). Other
// static member calls `[C]::Member(...)` emit a call to Member. Arguments walked.
func (e *extractor) extractPSInvokation(node *tsparse.Node) {
	memberName := ""
	if mn := psFirstChildOfKind(node, "member_name"); mn != nil {
		memberName = strings.TrimSpace(mn.Text())
	}

	if typeLit := psFirstChildOfKind(node, "type_literal"); typeLit != nil {
		// `[C]::member(...)`. `[C]::new` is construction of C.
		typeName := psTypeLiteralName(typeLit)
		if typeName != "" {
			if strings.EqualFold(memberName, "new") {
				e.emitPSRef(typeName, model.EdgeInstantiates, node)
			} else if memberName != "" {
				e.emitPSRef(memberName, model.EdgeCalls, node)
			}
		}
	} else if memberName != "" {
		// `$obj.Method(...)` — strip the receiver, call the bare method name.
		e.emitPSRef(memberName, model.EdgeCalls, node)
	}

	if args := psFirstChildOfKind(node, "argument_list"); args != nil {
		e.psWalkChildren(args)
	}
}

// extractPSAssignment handles a top-level `$Var = …` assignment: a single named
// variable target becomes a KindVariable/KindConstant. The right-hand value is
// walked for nested calls/invocations.
func (e *extractor) extractPSAssignment(node *tsparse.Node) {
	if e.psAtFileScope() {
		if lhs := psFirstChildOfKind(node, "left_assignment_expression"); lhs != nil {
			if v := psFirstChildOfKind(lhs, "variable"); v != nil {
				raw := strings.TrimSpace(v.Text())
				name := psStripSigil(raw)
				if name != "" {
					e.createNode(psVarKind(raw, name), name, node, nodeExtra{signature: psAssignSignature(node)})
				}
			}
		}
	}
	if val := node.ChildByFieldName("value"); val != nil {
		e.psWalkChildren(val)
	}
}

// emitPSRef appends an unresolved reference of the given kind from the current
// scope. Names are resolved against in-repo definitions only; external/unmatched
// names produce no edge (handled by the resolver).
func (e *extractor) emitPSRef(name string, kind model.EdgeKind, node *tsparse.Node) {
	if name == "" || len(e.nodeStack) == 0 {
		return
	}
	callerID := e.nodeStack[len(e.nodeStack)-1]
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    callerID,
		ReferenceName: name,
		ReferenceKind: kind,
		Line:          int(node.StartPoint().Row) + 1,
		Column:        int(node.StartPoint().Column),
	})
}

// psAtFileScope reports whether the current scope is the top-level file node.
func (e *extractor) psAtFileScope() bool {
	return len(e.nodeStack) == 1
}

// psIsDotSource reports whether a command node is a dot-source invocation.
func psIsDotSource(node *tsparse.Node) bool {
	for i := 0; i < node.ChildCount(); i++ {
		if c := node.Child(i); c != nil && c.Kind() == "command_invokation_operator" {
			return strings.TrimSpace(c.Text()) == "."
		}
	}
	return false
}

// psCommandArg returns the i-th non-separator argument text of a command's
// command_elements (0-indexed), trimmed, or "".
func psCommandArg(node *tsparse.Node, i int) string {
	el := node.ChildByFieldName("command_elements")
	if el == nil {
		return ""
	}
	idx := 0
	for j := 0; j < el.NamedChildCount(); j++ {
		c := el.NamedChild(j)
		if c == nil || c.Kind() == "command_argument_sep" {
			continue
		}
		if idx == i {
			return strings.TrimSpace(c.Text())
		}
		idx++
	}
	return ""
}

// psFirstChildOfKind returns the first named descendant-or-child of node with the
// given kind found by depth-first search, or nil.
func psFirstChildOfKind(node *tsparse.Node, kind string) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() == kind {
			return c
		}
		if found := psFirstChildOfKind(c, kind); found != nil {
			return found
		}
	}
	return nil
}

// psTypeLiteralName returns the type name of a `[Name]` type_literal.
func psTypeLiteralName(node *tsparse.Node) string {
	if tn := psFirstChildOfKind(node, "type_name"); tn != nil {
		return strings.TrimSpace(tn.Text())
	}
	return ""
}

// psStripSigil removes a leading `$` and any `scope:` prefix from a variable
// reference (`$global:GThing` → "GThing", `$Name` → "Name").
func psStripSigil(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "$")
	if idx := strings.LastIndex(s, ":"); idx >= 0 {
		s = s[idx+1:]
	}
	return s
}

// psVarKind classifies a top-level assignment target. A `$global:`/`$script:`
// scope or an ALL-CAPS name → KindConstant (best-effort); else KindVariable.
func psVarKind(raw, name string) model.NodeKind {
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "$global:") || strings.HasPrefix(lower, "$script:") {
		return model.KindConstant
	}
	if psIsConstantName(name) {
		return model.KindConstant
	}
	return model.KindVariable
}

// psIsConstantName reports whether name is ALL-CAPS (only [A-Z0-9_], at least one
// letter).
func psIsConstantName(name string) bool {
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

// psModuleBasename reduces a module/path argument to a bare module name for
// matching a defining file (e.g. "./lib.ps1" → "lib", "Foo" → "Foo").
func psModuleBasename(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	path = strings.ReplaceAll(path, "\\", "/")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		path = path[idx+1:]
	}
	for _, ext := range []string{".ps1", ".psm1", ".psd1"} {
		if strings.HasSuffix(strings.ToLower(path), ext) {
			path = path[:len(path)-len(ext)]
			break
		}
	}
	return path
}

// psFunctionSignature renders a function's header.
func psFunctionSignature(node *tsparse.Node, name string) string {
	sig := "function " + name
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == "function_parameter_declaration" {
			sig += c.Text()
			break
		}
	}
	return sig
}

// psMethodSignature renders a method's header (return type + name + params).
func psMethodSignature(node *tsparse.Node, name string) string {
	sig := name
	if attr := psFirstChildOfKind(node, "attribute"); attr != nil {
		sig = strings.TrimSpace(attr.Text()) + " " + name
	}
	return sig + "()"
}

// psAssignSignature renders the right-hand side of an assignment as a signature.
func psAssignSignature(node *tsparse.Node) string {
	val := node.ChildByFieldName("value")
	if val == nil {
		return ""
	}
	v := strings.TrimSpace(val.Text())
	if v == "" {
		return ""
	}
	if len(v) > 100 {
		return "= " + v[:100] + "..."
	}
	return "= " + v
}

// psCommonCmdlets is a small skip set of ubiquitous built-in cmdlets whose calls
// never produce edges. The resolver also drops any name with no in-repo match, so
// this set is just an early-out for the most common noise.
var psCommonCmdlets = map[string]bool{
	"write-host": true, "write-output": true, "write-error": true,
	"write-warning": true, "write-verbose": true, "write-debug": true,
	"get-childitem": true, "get-content": true, "set-content": true,
	"get-item": true, "set-item": true, "where-object": true,
	"foreach-object": true, "select-object": true, "sort-object": true,
	"new-object": true, "import-module": true, "return": true,
}

// psIsCommonCmdlet reports whether name is a ubiquitous built-in cmdlet.
func psIsCommonCmdlet(name string) bool {
	return psCommonCmdlets[strings.ToLower(name)]
}

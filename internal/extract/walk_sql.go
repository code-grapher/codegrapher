package extract

import (
	"strings"

	"github.com/specscore/codegrapher/internal/tsparse"
	"github.com/specscore/codegrapher/model"
)

// walkSql walks a parsed SQL (tree-sitter `sql`) file root and extracts a thin
// schema + query-reference graph optimized for one goal: detect CREATE TABLE /
// CREATE VIEW and link SELECT (and DML) to the tables/views they reference.
//
// Symbol model (reuses existing kinds — no new node/edge kinds):
//   - CREATE TABLE name (...)  → KindStruct named `name`; each column → KindField
//     (column type into Signature). Schema-qualified `schema.name` keeps the bare
//     table name as Name and `schema.name` as QualifiedName.
//   - CREATE VIEW name AS SELECT … → KindStruct named `name` (a view is a virtual
//     table). The view node is the container for its SELECT's table references.
//   - CREATE [OR REPLACE] FUNCTION/PROCEDURE name → KindFunction.
//
// Edges (all `references` / EdgeReferences, resolved by table name cross-file):
//   - A SELECT/INSERT/UPDATE/DELETE references each table/view in its FROM/JOIN
//     clause (and DML target). Inside a CREATE VIEW the refs are emitted from the
//     view node; a standalone top-level statement emits them from the file node.
//
// Node-type reference (confirmed via the throwaway probe — parses cleanly for
// both upper- and lower-case keywords, no ERROR nodes):
//
//	source_file (root)
//	create_table_statement   (name identifier|dotted_name; table_parameters → table_column[name,type])
//	create_view_statement    (name identifier|dotted_name; view_body → select_statement)
//	create_function_statement(name identifier; create_function_parameters)
//	select_statement         (select_clause, from_clause, where_clause, …)
//	from_clause              (identifier|dotted_name target, or join_clause)
//	join_clause              (identifier/alias/join_condition children)
//	insert_statement (identifier after INTO), update_statement (identifier),
//	delete_statement (from_clause), dotted_name (identifier . identifier)
func (e *extractor) walkSql(root *tsparse.Node) {
	for i := 0; i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		if child == nil {
			continue
		}
		e.visitSqlStatement(child)
	}
}

// visitSqlStatement dispatches a top-level statement. Query statements at the
// top level emit their references from the current stack top (the file node).
func (e *extractor) visitSqlStatement(node *tsparse.Node) {
	switch node.Kind() {
	case "create_table_statement":
		e.extractSqlTable(node)
	case "create_view_statement":
		e.extractSqlView(node)
	case "create_function_statement", "create_procedure_statement":
		e.extractSqlFunction(node)
	case "select_statement", "insert_statement", "update_statement", "delete_statement":
		e.emitSqlTableRefs(node)
	default:
		// Unknown / wrapper statement: descend so nested queries are still seen.
		for i := 0; i < node.NamedChildCount(); i++ {
			if c := node.NamedChild(i); c != nil {
				e.visitSqlStatement(c)
			}
		}
	}
}

// extractSqlTable handles CREATE TABLE: a KindStruct named after the table, with
// each column emitted as a KindField (column type text → Signature).
func (e *extractor) extractSqlTable(node *tsparse.Node) {
	nameNode := sqlTableName(node)
	if nameNode == nil {
		return
	}
	name, qualified := sqlNameParts(nameNode)
	if name == "" {
		return
	}
	extra := nodeExtra{signature: "CREATE TABLE " + qualified}
	if qualified != name {
		extra.qualifiedName = qualified
	}
	tbl := e.createNode(model.KindStruct, name, node, extra)
	if tbl == nil {
		return
	}

	params := node.ChildByFieldName("parameters")
	if params == nil {
		params = sqlChildByKind(node, "table_parameters")
	}
	if params == nil {
		return
	}
	e.nodeStack = append(e.nodeStack, tbl.ID)
	for i := 0; i < params.NamedChildCount(); i++ {
		col := params.NamedChild(i)
		if col == nil || col.Kind() != "table_column" {
			continue
		}
		colName := sqlFieldText(col, "name")
		if colName == "" {
			continue
		}
		e.createNode(model.KindField, colName, col, nodeExtra{signature: sqlColumnType(col)})
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractSqlView handles CREATE VIEW: a KindStruct named after the view; its
// SELECT body's table references are emitted FROM the view node.
func (e *extractor) extractSqlView(node *tsparse.Node) {
	nameNode := sqlTableName(node)
	if nameNode == nil {
		return
	}
	name, qualified := sqlNameParts(nameNode)
	if name == "" {
		return
	}
	extra := nodeExtra{signature: "CREATE VIEW " + qualified}
	if qualified != name {
		extra.qualifiedName = qualified
	}
	view := e.createNode(model.KindStruct, name, node, extra)
	if view == nil {
		return
	}

	body := sqlChildByKind(node, "view_body")
	if body == nil {
		body = node
	}
	// Push the view node so its SELECT's FROM/JOIN references attribute to it.
	e.nodeStack = append(e.nodeStack, view.ID)
	for i := 0; i < body.NamedChildCount(); i++ {
		if sel := body.NamedChild(i); sel != nil && sel.Kind() == "select_statement" {
			e.emitSqlTableRefs(sel)
		}
	}
	e.nodeStack = e.nodeStack[:len(e.nodeStack)-1]
}

// extractSqlFunction handles CREATE [OR REPLACE] FUNCTION/PROCEDURE → KindFunction.
func (e *extractor) extractSqlFunction(node *tsparse.Node) {
	var nameNode *tsparse.Node
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && (c.Kind() == "identifier" || c.Kind() == "dotted_name") {
			nameNode = c
			break
		}
	}
	if nameNode == nil {
		return
	}
	name, qualified := sqlNameParts(nameNode)
	if name == "" {
		return
	}
	extra := nodeExtra{signature: qualified}
	if qualified != name {
		extra.qualifiedName = qualified
	}
	e.createNode(model.KindFunction, name, node, extra)
}

// emitSqlTableRefs walks a query statement and emits a `references` ref to each
// table/view named in its FROM/JOIN clauses and DML targets, from the current
// stack top (view node inside a CREATE VIEW, else the file node). Nested
// subquery select_statements are walked too.
func (e *extractor) emitSqlTableRefs(node *tsparse.Node) {
	if len(e.nodeStack) == 0 {
		return
	}
	from := e.nodeStack[len(e.nodeStack)-1]

	switch node.Kind() {
	case "insert_statement", "update_statement":
		// First identifier/dotted_name child is the target table.
		if t := sqlFirstTableName(node); t != nil {
			e.addSqlRef(from, t)
		}
	}

	tsparse.Walk(node, func(n *tsparse.Node) {
		switch n.Kind() {
		case "from_clause":
			e.collectSqlFromTables(from, n)
		}
	})
}

// collectSqlFromTables emits refs for every table identifier in a from_clause,
// including those inside a join_clause. Aliases and join conditions are skipped.
func (e *extractor) collectSqlFromTables(from string, fromClause *tsparse.Node) {
	for i := 0; i < fromClause.NamedChildCount(); i++ {
		c := fromClause.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "identifier", "dotted_name":
			e.addSqlRef(from, c)
		case "join_clause":
			for j := 0; j < c.NamedChildCount(); j++ {
				jc := c.NamedChild(j)
				if jc == nil {
					continue
				}
				if jc.Kind() == "identifier" || jc.Kind() == "dotted_name" {
					e.addSqlRef(from, jc)
				}
				// alias / join_condition children are skipped.
			}
		}
	}
}

// addSqlRef appends a `references` ref to the table named by nameNode (the bare
// table name; schema qualifier dropped for name-based resolution).
func (e *extractor) addSqlRef(from string, nameNode *tsparse.Node) {
	name, _ := sqlNameParts(nameNode)
	if name == "" {
		return
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    from,
		ReferenceName: name,
		ReferenceKind: model.EdgeReferences,
		Line:          int(nameNode.StartPoint().Row) + 1,
		Column:        int(nameNode.StartPoint().Column),
	})
}

// sqlTableName returns the name node (identifier or dotted_name) of a
// create_table/create_view statement — the first such child after the keywords.
func sqlTableName(node *tsparse.Node) *tsparse.Node {
	if n := node.ChildByFieldName("name"); n != nil {
		return n
	}
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && (c.Kind() == "identifier" || c.Kind() == "dotted_name") {
			return c
		}
	}
	return nil
}

// sqlFirstTableName returns the first identifier/dotted_name child of node (the
// target table of an INSERT/UPDATE statement).
func sqlFirstTableName(node *tsparse.Node) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		c := node.NamedChild(i)
		if c != nil && (c.Kind() == "identifier" || c.Kind() == "dotted_name") {
			return c
		}
	}
	return nil
}

// sqlNameParts splits a name node into (bareName, qualifiedName). For a plain
// identifier both are equal; for a dotted_name `schema.tbl` the bare name is the
// last segment and the qualified name is the full text.
func sqlNameParts(nameNode *tsparse.Node) (name, qualified string) {
	if nameNode == nil {
		return "", ""
	}
	if nameNode.Kind() == "dotted_name" {
		qualified = strings.TrimSpace(nameNode.Text())
		// Last identifier child is the bare object name.
		for i := nameNode.NamedChildCount() - 1; i >= 0; i-- {
			c := nameNode.NamedChild(i)
			if c != nil && c.Kind() == "identifier" {
				return c.Text(), qualified
			}
		}
		if idx := strings.LastIndex(qualified, "."); idx >= 0 {
			return qualified[idx+1:], qualified
		}
		return qualified, qualified
	}
	name = strings.TrimSpace(nameNode.Text())
	return name, name
}

// sqlFieldText returns the text of node's field child, or "".
func sqlFieldText(node *tsparse.Node, field string) string {
	if c := node.ChildByFieldName(field); c != nil {
		return c.Text()
	}
	return ""
}

// sqlColumnType returns the column's type text (the `type` field), used as the
// field signature, e.g. "INT" / "TEXT".
func sqlColumnType(col *tsparse.Node) string {
	if t := col.ChildByFieldName("type"); t != nil {
		return strings.TrimSpace(t.Text())
	}
	return ""
}

// sqlChildByKind returns the first named child of node with the given kind.
func sqlChildByKind(node *tsparse.Node, kind string) *tsparse.Node {
	for i := 0; i < node.NamedChildCount(); i++ {
		if c := node.NamedChild(i); c != nil && c.Kind() == kind {
			return c
		}
	}
	return nil
}

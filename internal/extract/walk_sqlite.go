package extract

import (
	"database/sql"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/specscore/codegrapher/model"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (CGO-free), registered as "sqlite"
)

// extractSQLite introspects a binary SQLite database file and emits its schema
// catalog into the graph: tables/views (KindStruct), columns (KindField),
// indexes (KindIndex), triggers (KindTrigger), and PK/FK/UNIQUE/CHECK
// constraints (KindConstraint), joined by `contains` and `references` edges.
//
// Integration note: a binary .db cannot be driven from a []byte, so this opens
// the database by FILE PATH (read-only/immutable). When e.filePath points at a
// real SQLite file on disk (the indexer) it is opened directly; otherwise the
// provided content bytes are written to a temp file and opened (so extraction
// stays a pure function of (path, content) — e.g. the parity harness passes a
// repo-relative path plus the bytes). The file node has already been emitted by
// the caller and sits at e.nodes[0]; DB-level metadata is attached to it.
func (e *extractor) extractSQLite() {
	openPath, cleanup, err := e.sqliteOpenPath()
	if err != nil {
		e.sqliteWarn("sqlite_open_error", err)
		return
	}
	defer cleanup()

	db, err := sql.Open("sqlite", "file:"+openPath+"?mode=ro&immutable=1")
	if err != nil {
		e.sqliteWarn("sqlite_open_error", err)
		return
	}
	defer db.Close()

	e.attachDBMetadata(db)

	objects, err := e.sqliteObjects(db)
	if err != nil {
		e.sqliteWarn("sqlite_schema_error", err)
		return
	}

	fileID := model.FileNodeID(e.filePath)
	now := time.Now().UnixMilli()

	// Virtual tables (FTS5, R*Tree, …) create internal "shadow" tables named
	// "<vtable>_<suffix>"; skip those so only the virtual table itself appears.
	shadowPrefixes := sqliteShadowPrefixes(objects)

	for _, o := range objects {
		switch o.typ {
		case "table":
			if isSQLiteShadowTable(o.name, shadowPrefixes) {
				continue
			}
			e.extractSQLiteTable(db, o, fileID, now)
		case "view":
			e.extractSQLiteView(o, fileID, now)
		case "trigger":
			e.extractSQLiteTrigger(o, fileID, now)
		}
		// Standalone indexes (type='index', user-created CREATE INDEX with non-null
		// sql) are emitted per-table inside extractSQLiteTable via PRAGMA index_list,
		// so they are not handled here.
	}
}

// sqliteObject is one row of sqlite_master we care about.
type sqliteObject struct {
	typ     string // table | view | index | trigger
	name    string
	tblName string
	sql     string
}

// sqliteObjects reads the user schema objects, skipping internal sqlite_* names.
func (e *extractor) sqliteObjects(db *sql.DB) ([]sqliteObject, error) {
	rows, err := db.Query(`SELECT type, name, tbl_name, COALESCE(sql, '')
		FROM sqlite_master
		WHERE type IN ('table','view','index','trigger')
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sqliteObject
	for rows.Next() {
		var o sqliteObject
		if err := rows.Scan(&o.typ, &o.name, &o.tblName, &o.sql); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// attachDBMetadata records database-level pragmas on the file node.
func (e *extractor) attachDBMetadata(db *sql.DB) {
	if len(e.nodes) == 0 {
		return
	}
	meta := map[string]any{}
	var userVersion, appID int64
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err == nil && userVersion != 0 {
		meta["userVersion"] = userVersion
	}
	if err := db.QueryRow(`PRAGMA application_id`).Scan(&appID); err == nil && appID != 0 {
		meta["applicationId"] = appID
	}
	var enc string
	if err := db.QueryRow(`PRAGMA encoding`).Scan(&enc); err == nil && enc != "" {
		meta["textEncoding"] = enc
	}
	if len(meta) > 0 {
		e.nodes[0].Metadata = meta
	}
}

var (
	reVirtualUsing = regexp.MustCompile(`(?is)CREATE\s+VIRTUAL\s+TABLE\s+.*?\s+USING\s+([A-Za-z0-9_]+)`)
	reWithoutRowid = regexp.MustCompile(`(?is)\)\s*WITHOUT\s+ROWID`)
	reStrict       = regexp.MustCompile(`(?is)\)\s*[^;]*\bSTRICT\b`)
	reTriggerWhen  = regexp.MustCompile(`(?is)CREATE\s+(?:TEMP\s+|TEMPORARY\s+)?TRIGGER\s+\S+\s+(BEFORE|AFTER|INSTEAD\s+OF)\s+(DELETE|INSERT|UPDATE)`)
	reCheck        = regexp.MustCompile(`(?is)\bCHECK\s*\(`)
)

// extractSQLiteTable emits a KindStruct table node plus its columns, constraints,
// and explicit indexes.
func (e *extractor) extractSQLiteTable(db *sql.DB, o sqliteObject, fileID string, now int64) {
	meta := map[string]any{"objectType": "table"}
	if m := reVirtualUsing.FindStringSubmatch(o.sql); m != nil {
		meta["objectType"] = "virtual"
		meta["module"] = strings.ToLower(m[1])
	}
	if reWithoutRowid.MatchString(o.sql) {
		meta["withoutRowid"] = true
	}
	if reStrict.MatchString(o.sql) {
		meta["strict"] = true
	}
	if n, ok := e.sqliteRowCount(db, o.name); ok {
		meta["rowCount"] = n
	}

	tblID := e.addSQLiteNode(model.KindStruct, o.name, o.name, "CREATE TABLE "+o.name, meta, fileID, now)

	cols := e.sqliteColumns(db, o.name)
	for _, c := range cols {
		// Skip auxiliary hidden columns (e.g. FTS5's table-named and `rank`
		// columns). Generated columns (hidden 2/3) are kept.
		if c.hidden == 1 {
			continue
		}
		e.addSQLiteColumn(o.name, c, tblID, now)
	}

	e.extractSQLitePrimaryKey(o.name, cols, tblID, now)
	e.extractSQLiteForeignKeys(db, o.name, tblID, now)
	e.extractSQLiteIndexesAndUnique(db, o.name, tblID, now)
	e.extractSQLiteChecks(o, tblID, now)
}

// extractSQLiteView emits a KindStruct view node, its columns, and `references`
// to the tables/views named in its SELECT body (reusing the tree-sitter SQL ref
// logic on the stored DDL).
func (e *extractor) extractSQLiteView(o sqliteObject, fileID string, now int64) {
	viewID := e.addSQLiteNode(model.KindStruct, o.name, o.name, "CREATE VIEW "+o.name,
		map[string]any{"objectType": "view"}, fileID, now)
	e.sqliteViewRefs(o.sql, viewID)
}

// extractSQLiteTrigger emits a KindTrigger node contained by its table, with a
// reference to that table.
func (e *extractor) extractSQLiteTrigger(o sqliteObject, fileID string, now int64) {
	meta := map[string]any{}
	if m := reTriggerWhen.FindStringSubmatch(o.sql); m != nil {
		meta["timing"] = strings.ToUpper(strings.Join(strings.Fields(m[1]), " "))
		meta["event"] = strings.ToUpper(m[2])
	}
	// Parent is the trigger's table when present in the graph, else the file.
	parent := fileID
	if tblID, ok := e.sqliteNodeID[sqliteKey(model.KindStruct, o.tblName)]; ok && o.tblName != "" {
		parent = tblID
	}
	trigID := e.addSQLiteNode(model.KindTrigger, o.name, o.name, "CREATE TRIGGER "+o.name, meta, parent, now)
	if o.tblName != "" {
		e.addSQLiteRef(trigID, o.tblName)
	}
}

// --- columns ---------------------------------------------------------------

type sqliteColumn struct {
	name       string
	declType   string
	notNull    bool
	defaultVal string
	hasDefault bool
	pkPos      int
	hidden     int // 0 normal, 1 hidden, 2 virtual generated, 3 stored generated
}

func (e *extractor) sqliteColumns(db *sql.DB, table string) []sqliteColumn {
	rows, err := db.Query(`PRAGMA table_xinfo(` + quoteIdent(table) + `)`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []sqliteColumn
	for rows.Next() {
		var (
			cid, notnull, pk, hidden int
			name, typ                string
			dflt                     sql.NullString
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk, &hidden); err != nil {
			return out
		}
		out = append(out, sqliteColumn{
			name: name, declType: typ, notNull: notnull == 1,
			defaultVal: dflt.String, hasDefault: dflt.Valid, pkPos: pk, hidden: hidden,
		})
	}
	return out
}

func (e *extractor) addSQLiteColumn(table string, c sqliteColumn, tblID string, now int64) {
	meta := map[string]any{"notNull": c.notNull}
	if aff := sqliteAffinity(c.declType); aff != "" {
		meta["typeAffinity"] = aff
	}
	if c.hasDefault {
		meta["default"] = c.defaultVal
	}
	if c.pkPos > 0 {
		meta["pkPosition"] = c.pkPos
	}
	switch c.hidden {
	case 1:
		meta["hidden"] = true
	case 2, 3:
		meta["generated"] = true
	}
	e.addSQLiteNode(model.KindField, c.name, table+"::"+c.name, c.declType, meta, tblID, now)
}

// --- constraints -----------------------------------------------------------

func (e *extractor) extractSQLitePrimaryKey(table string, cols []sqliteColumn, tblID string, now int64) {
	var pk []sqliteColumn
	for _, c := range cols {
		if c.pkPos > 0 {
			pk = append(pk, c)
		}
	}
	if len(pk) == 0 {
		return
	}
	// Order by pk position.
	for i := 0; i < len(pk); i++ {
		for j := i + 1; j < len(pk); j++ {
			if pk[j].pkPos < pk[i].pkPos {
				pk[i], pk[j] = pk[j], pk[i]
			}
		}
	}
	names := make([]string, len(pk))
	for i, c := range pk {
		names[i] = c.name
	}
	meta := map[string]any{"subtype": "primaryKey", "columns": names}
	e.addSQLiteNode(model.KindConstraint, "pk", table+"::pk", "PRIMARY KEY", meta, tblID, now)
}

func (e *extractor) extractSQLiteForeignKeys(db *sql.DB, table, tblID string, now int64) {
	rows, err := db.Query(`PRAGMA foreign_key_list(` + quoteIdent(table) + `)`)
	if err != nil {
		return
	}
	defer rows.Close()
	type fkRow struct {
		id           int
		from, to     string
		refTable     string
		onUpd, onDel string
	}
	var fks []fkRow
	for rows.Next() {
		var (
			id, seq             int
			refTable, from      string
			to                  sql.NullString
			onUpd, onDel, match string
		)
		if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpd, &onDel, &match); err != nil {
			break
		}
		fks = append(fks, fkRow{id: id, from: from, to: to.String, refTable: refTable, onUpd: onUpd, onDel: onDel})
	}
	rows.Close()
	if len(fks) == 0 {
		return
	}
	// Group composite FKs by id (rows are returned id-major, seq-minor).
	groups := map[int][]fkRow{}
	var order []int
	for _, r := range fks {
		if _, ok := groups[r.id]; !ok {
			order = append(order, r.id)
		}
		groups[r.id] = append(groups[r.id], r)
	}
	for _, id := range order {
		g := groups[id]
		from := make([]string, len(g))
		to := make([]string, len(g))
		for i, r := range g {
			from[i] = r.from
			to[i] = r.to
		}
		meta := map[string]any{
			"subtype":    "foreignKey",
			"columns":    from,
			"refTable":   g[0].refTable,
			"refColumns": to,
			"onDelete":   g[0].onDel,
			"onUpdate":   g[0].onUpd,
		}
		name := "fk_" + strings.Join(from, "_")
		fkID := e.addSQLiteNode(model.KindConstraint, name, table+"::"+name, "FOREIGN KEY", meta, tblID, now)
		// references: the FK targets the referenced table (and its columns by name).
		e.addSQLiteRef(fkID, g[0].refTable)
		for _, col := range to {
			if col != "" {
				e.addSQLiteRef(fkID, g[0].refTable+"."+col)
			}
		}
	}
}

// extractSQLiteIndexesAndUnique emits explicit (CREATE INDEX) indexes as
// KindIndex nodes and UNIQUE constraints (origin 'u') as KindConstraint nodes.
// Auto-created PK indexes (origin 'pk') are already represented by the primary
// key constraint and are skipped.
func (e *extractor) extractSQLiteIndexesAndUnique(db *sql.DB, table, tblID string, now int64) {
	type idxRow struct {
		name    string
		unique  bool
		origin  string
		partial bool
	}
	rows, err := db.Query(`PRAGMA index_list(` + quoteIdent(table) + `)`)
	if err != nil {
		return
	}
	var idxs []idxRow
	for rows.Next() {
		var (
			seq, uniq, partial int
			name, origin       string
		)
		if err := rows.Scan(&seq, &name, &uniq, &origin, &partial); err != nil {
			break
		}
		idxs = append(idxs, idxRow{name: name, unique: uniq == 1, origin: origin, partial: partial == 1})
	}
	rows.Close()

	for _, ix := range idxs {
		cols := e.sqliteIndexColumns(db, ix.name)
		switch ix.origin {
		case "c": // explicit CREATE INDEX → KindIndex node
			meta := map[string]any{"unique": ix.unique, "partial": ix.partial, "origin": "c"}
			idxID := e.addSQLiteNode(model.KindIndex, ix.name, table+"::"+ix.name, "CREATE INDEX "+ix.name, meta, tblID, now)
			for _, col := range cols {
				if col != "" {
					e.addSQLiteRef(idxID, table+"."+col)
				}
			}
		case "u": // UNIQUE constraint (backed by an auto index)
			meta := map[string]any{"subtype": "unique", "columns": cols, "backingIndex": ix.name}
			name := "unique_" + strings.Join(cols, "_")
			e.addSQLiteNode(model.KindConstraint, name, table+"::"+name, "UNIQUE", meta, tblID, now)
		}
		// origin "pk": represented by the primary key constraint; skip.
	}
}

func (e *extractor) sqliteIndexColumns(db *sql.DB, index string) []string {
	rows, err := db.Query(`PRAGMA index_info(` + quoteIdent(index) + `)`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var (
			seqno, cid int
			name       sql.NullString
		)
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return cols
		}
		cols = append(cols, name.String) // NULL → "" for expression indexes
	}
	return cols
}

// extractSQLiteChecks recovers CHECK constraints from the table DDL (no PRAGMA
// exposes them). Each `CHECK( … )` expression becomes a KindConstraint node.
func (e *extractor) extractSQLiteChecks(o sqliteObject, tblID string, now int64) {
	exprs := extractCheckExpressions(o.sql)
	for i, expr := range exprs {
		meta := map[string]any{"subtype": "check", "expression": expr}
		name := "check_" + strconv.Itoa(i+1)
		e.addSQLiteNode(model.KindConstraint, name, o.name+"::"+name, "CHECK", meta, tblID, now)
	}
}

// extractCheckExpressions returns the parenthesized body of each top-level
// CHECK(...) in a CREATE TABLE statement, via balanced-paren scanning.
func extractCheckExpressions(ddl string) []string {
	var out []string
	for _, loc := range reCheck.FindAllStringIndex(ddl, -1) {
		open := loc[1] - 1 // index of the '(' captured by the regex
		depth := 0
		for i := open; i < len(ddl); i++ {
			switch ddl[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					out = append(out, strings.TrimSpace(ddl[open+1:i]))
					i = len(ddl)
				}
			}
		}
	}
	return out
}

// --- view references (reuse tree-sitter SQL ref extraction) -----------------

// sqliteViewRefs parses the view's stored SELECT DDL with the tree-sitter SQL
// extractor and re-emits its table references from the view node.
func (e *extractor) sqliteViewRefs(ddl, viewID string) {
	res, err := ExtractFile(e.filePath, []byte(ddl), model.LangSql)
	if err != nil {
		return
	}
	for _, ref := range res.UnresolvedReferences {
		if ref.ReferenceKind != model.EdgeReferences {
			continue
		}
		e.addSQLiteRef(viewID, ref.ReferenceName)
	}
}

// --- node/edge helpers ------------------------------------------------------

// addSQLiteNode appends a node (with a deterministic, qualified-name-based ID),
// records a `contains` edge from parentID, indexes it for intra-file lookup, and
// returns the node ID. Position is line 1 (a binary DB has no source lines).
func (e *extractor) addSQLiteNode(kind model.NodeKind, name, qualified, signature string, meta map[string]any, parentID string, now int64) string {
	id := model.GenerateNodeID(e.filePath, kind, qualified, 1)
	e.nodes = append(e.nodes, model.Node{
		ID:            id,
		Kind:          kind,
		Name:          name,
		QualifiedName: qualified,
		FilePath:      e.filePath,
		Language:      e.lang,
		StartLine:     1,
		EndLine:       1,
		Signature:     signature,
		Metadata:      meta,
		UpdatedAt:     now,
	})
	if parentID != "" {
		e.edges = append(e.edges, model.Edge{Source: parentID, Target: id, Kind: model.EdgeContains})
	}
	if e.sqliteNodeID == nil {
		e.sqliteNodeID = map[string]string{}
	}
	e.sqliteNodeID[sqliteKey(kind, name)] = id
	return id
}

// addSQLiteRef records a `references` ref by name, resolved later by resolveSqliteRef.
func (e *extractor) addSQLiteRef(from, name string) {
	if name == "" {
		return
	}
	// Drop a schema/table qualifier for column refs like "t.col" → resolve by "t".
	refName := name
	if i := strings.LastIndex(refName, "."); i >= 0 {
		refName = refName[:i]
	}
	e.unresolvedRefs = append(e.unresolvedRefs, model.UnresolvedReference{
		FromNodeID:    from,
		ReferenceName: refName,
		ReferenceKind: model.EdgeReferences,
	})
}

func (e *extractor) sqliteRowCount(db *sql.DB, table string) (int64, bool) {
	var n int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + quoteIdent(table)).Scan(&n); err != nil {
		return 0, false
	}
	return n, true
}

// sqliteOpenPath returns a filesystem path to open as the SQLite database, plus
// a cleanup func. If e.filePath is a readable SQLite file it is used directly;
// otherwise the in-memory content bytes are spilled to a temp file. Errors when
// neither source yields a SQLite database.
func (e *extractor) sqliteOpenPath() (string, func(), error) {
	noop := func() {}
	if data, err := os.ReadFile(e.filePath); err == nil && isSQLiteFile(data) {
		return e.filePath, noop, nil
	}
	if isSQLiteFile([]byte(e.content)) {
		f, err := os.CreateTemp("", "codegrapher-*.db")
		if err != nil {
			return "", noop, err
		}
		if _, err := f.WriteString(e.content); err != nil {
			f.Close()
			os.Remove(f.Name())
			return "", noop, err
		}
		f.Close()
		return f.Name(), func() { os.Remove(f.Name()) }, nil
	}
	return "", noop, errors.New("not a SQLite database file")
}

func (e *extractor) sqliteWarn(code string, err error) {
	e.errors = append(e.errors, model.ExtractionError{
		Message:  err.Error(),
		FilePath: e.filePath,
		Severity: "warning",
		Code:     code,
	})
}

func sqliteKey(kind model.NodeKind, name string) string { return string(kind) + "\x00" + name }

// sqliteShadowPrefixes returns "<name>_" for every virtual table, used to skip
// the shadow tables those virtual tables create.
func sqliteShadowPrefixes(objects []sqliteObject) []string {
	var prefixes []string
	for _, o := range objects {
		if o.typ == "table" && reVirtualUsing.MatchString(o.sql) {
			prefixes = append(prefixes, o.name+"_")
		}
	}
	return prefixes
}

// isSQLiteShadowTable reports whether name is a shadow table of some virtual
// table (matches one of the "<vtable>_" prefixes).
func isSQLiteShadowTable(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// quoteIdent double-quotes a SQLite identifier for safe interpolation.
func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// sqliteAffinity computes a column's type affinity from its declared type per
// SQLite's rules (https://sqlite.org/datatype3.html#determination_of_column_affinity).
func sqliteAffinity(declType string) string {
	t := strings.ToUpper(declType)
	switch {
	case t == "":
		return "BLOB"
	case strings.Contains(t, "INT"):
		return "INTEGER"
	case strings.Contains(t, "CHAR"), strings.Contains(t, "CLOB"), strings.Contains(t, "TEXT"):
		return "TEXT"
	case strings.Contains(t, "BLOB"):
		return "BLOB"
	case strings.Contains(t, "REAL"), strings.Contains(t, "FLOA"), strings.Contains(t, "DOUB"):
		return "REAL"
	default:
		return "NUMERIC"
	}
}

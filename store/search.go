// Package store — FTS5 search methods.
//
// These methods implement the search pipeline from src/db/queries.ts's
// QueryBuilder.searchNodes (FTS5 BM25 → LIKE fallback → fuzzy Levenshtein,
// plus an exact-name supplement). They belong in store because the SQL lives
// here; the scoring/rescoring logic lives in the query package.
package store

import (
	"database/sql"
	"strings"

	"github.com/specscore/codegrapher/model"
)

// SearchFTS runs an FTS5 prefix-match query against nodes_fts and returns
// (node, raw-bm25-score) pairs. The BM25 column weights mirror the original:
// id=0, name=20, qualified_name=5, docstring=1, signature=2.
// Returns up to limit*5 rows (over-fetch for post-hoc rescoring).
// Returns nil on FTS parse error (mirrors the original's try-catch → []).
func (s *Store) SearchFTS(
	text string,
	kinds []model.NodeKind,
	langs []model.Language,
	limit, offset int,
) ([]model.SearchResult, error) {
	ftsQuery := buildFTSQuery(text)
	if ftsQuery == "" {
		return nil, nil
	}
	fetchLimit := max(limit*5, 100)

	// Qualify all column references with "nodes." to avoid ambiguity: the JOIN
	// of nodes_fts (content table) and nodes exposes both nodes_fts.id and
	// nodes.id — SQLite treats unqualified "id" as ambiguous and errors out.
	sql := `
		SELECT nodes.id, nodes.kind, nodes.name, nodes.qualified_name, nodes.file_path, nodes.language,
		       nodes.start_line, nodes.end_line, nodes.start_column, nodes.end_column,
		       nodes.docstring, nodes.signature, nodes.visibility,
		       nodes.is_exported, nodes.is_async, nodes.is_static, nodes.is_abstract,
		       nodes.decorators, nodes.type_parameters, nodes.return_type, nodes.updated_at,
		       bm25(nodes_fts, 0, 20, 5, 1, 2) as score
		FROM nodes_fts
		JOIN nodes ON nodes_fts.rowid = nodes.rowid
		WHERE nodes_fts MATCH ?`
	args := []any{ftsQuery}
	if len(kinds) > 0 {
		sql += ` AND nodes.kind IN (` + placeholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, string(k))
		}
	}
	if len(langs) > 0 {
		sql += ` AND nodes.language IN (` + placeholders(len(langs)) + `)`
		for _, l := range langs {
			args = append(args, string(l))
		}
	}
	sql += ` ORDER BY score LIMIT ? OFFSET ?`
	args = append(args, fetchLimit, offset)

	rows, err := s.db.Query(sql, args...)
	if err != nil {
		// FTS query failed (syntax error, etc.) — return empty like the original.
		return nil, nil //nolint:nilerr
	}
	defer rows.Close()
	var out []model.SearchResult
	for rows.Next() {
		n, score, err := scanNodeWithScoreRow(rows)
		if err != nil {
			return nil, err
		}
		// bm25 returns negative scores; take absolute value.
		if score < 0 {
			score = -score
		}
		out = append(out, model.SearchResult{Node: n, Score: score})
	}
	return out, rows.Err()
}

// SearchLike runs a LIKE-based substring search (fallback when FTS returns
// nothing).
func (s *Store) SearchLike(
	text string,
	kinds []model.NodeKind,
	langs []model.Language,
	limit, offset int,
) ([]model.SearchResult, error) {
	exact := text
	startsWith := text + "%"
	contains := "%" + text + "%"

	q := `
		SELECT ` + nodeColumns + `,
			CASE
				WHEN name = ?           THEN 1.0
				WHEN name LIKE ?        THEN 0.9
				WHEN name LIKE ?        THEN 0.8
				WHEN qualified_name LIKE ? THEN 0.7
				ELSE 0.5
			END as score
		FROM nodes
		WHERE (name LIKE ? OR qualified_name LIKE ? OR name LIKE ?)`
	args := []any{
		exact, startsWith, contains, contains,
		contains, contains, startsWith,
	}
	if len(kinds) > 0 {
		q += ` AND kind IN (` + placeholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, string(k))
		}
	}
	if len(langs) > 0 {
		q += ` AND language IN (` + placeholders(len(langs)) + `)`
		for _, l := range langs {
			args = append(args, string(l))
		}
	}
	q += ` ORDER BY score DESC, length(name) ASC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SearchResult
	for rows.Next() {
		n, score, err := scanNodeWithScoreRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, model.SearchResult{Node: n, Score: score})
	}
	return out, rows.Err()
}

// fuzzyCandidate pairs a name with its edit distance.
type fuzzyCandidate struct {
	name string
	dist int
}

// SearchFuzzy runs an edit-distance sweep over all distinct symbol names.
// Only fires when text length ≥ 3.
func (s *Store) SearchFuzzy(
	text string,
	kinds []model.NodeKind,
	langs []model.Language,
	limit int,
	editDistFn func(a, b string, max int) int,
) ([]model.SearchResult, error) {
	lowered := strings.ToLower(text)
	maxDist := 2
	if len(lowered) <= 4 {
		maxDist = 1
	}

	// Pull distinct names.
	rows, err := s.db.Query(`SELECT DISTINCT name FROM nodes`)
	if err != nil {
		return nil, err
	}
	var allNames []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		allNames = append(allNames, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var cands []fuzzyCandidate
	for _, name := range allNames {
		d := editDistFn(strings.ToLower(name), lowered, maxDist)
		if d <= maxDist {
			cands = append(cands, fuzzyCandidate{name, d})
		}
	}
	// Sort by dist ascending (stable).
	sortCandidates(cands)

	capSize := limit * 2
	if capSize < 50 {
		capSize = 50
	}
	if len(cands) > capSize {
		cands = cands[:capSize]
	}

	var out []model.SearchResult
	seen := make(map[string]struct{})
	for _, c := range cands {
		if len(out) >= limit {
			break
		}
		q := `SELECT ` + nodeColumns + ` FROM nodes WHERE name = ?`
		args := []any{c.name}
		if len(kinds) > 0 {
			q += ` AND kind IN (` + placeholders(len(kinds)) + `)`
			for _, k := range kinds {
				args = append(args, string(k))
			}
		}
		if len(langs) > 0 {
			q += ` AND language IN (` + placeholders(len(langs)) + `)`
			for _, l := range langs {
				args = append(args, string(l))
			}
		}
		q += ` LIMIT 5`
		nodeRows, err := s.db.Query(q, args...)
		if err != nil {
			return nil, err
		}
		ns, err := scanNodes(nodeRows)
		if err != nil {
			return nil, err
		}
		score := 1.0 / (1.0 + float64(c.dist))
		for _, n := range ns {
			if _, ok := seen[n.ID]; ok {
				continue
			}
			seen[n.ID] = struct{}{}
			out = append(out, model.SearchResult{Node: n, Score: score})
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// SearchAllByFilters returns up to limit nodes matching kind/lang filters
// with a uniform score of 1. Used when no text is given.
func (s *Store) SearchAllByFilters(
	kinds []model.NodeKind,
	langs []model.Language,
	limit int,
) ([]model.SearchResult, error) {
	q := `SELECT ` + nodeColumns + ` FROM nodes WHERE 1=1`
	var args []any
	if len(kinds) > 0 {
		q += ` AND kind IN (` + placeholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, string(k))
		}
	}
	if len(langs) > 0 {
		q += ` AND language IN (` + placeholders(len(langs)) + `)`
		for _, l := range langs {
			args = append(args, string(l))
		}
	}
	q += ` ORDER BY name LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	ns, err := scanNodes(rows)
	if err != nil {
		return nil, err
	}
	out := make([]model.SearchResult, len(ns))
	for i, n := range ns {
		out[i] = model.SearchResult{Node: n, Score: 1.0}
	}
	return out, nil
}

// ExactNameCaseInsensitive finds nodes whose name matches term (case-insensitive).
func (s *Store) ExactNameCaseInsensitive(
	term string,
	kinds []model.NodeKind,
	langs []model.Language,
	limit int,
) ([]model.Node, error) {
	q := `SELECT ` + nodeColumns + ` FROM nodes WHERE name = ? COLLATE NOCASE`
	args := []any{term}
	if len(kinds) > 0 {
		q += ` AND kind IN (` + placeholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, string(k))
		}
	}
	if len(langs) > 0 {
		q += ` AND language IN (` + placeholders(len(langs)) + `)`
		for _, l := range langs {
			args = append(args, string(l))
		}
	}
	q += ` LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanNodes(rows)
}

// GetDependencyFilePaths returns file paths of files depended on by filePath
// via the resolved symbol edge graph (calls/references/etc. cross-file edges).
func (s *Store) GetDependencyFilePaths(filePath string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT m.file_path
		FROM nodes n
		JOIN edges e ON e.source = n.id
		JOIN nodes m ON e.target = m.id
		WHERE n.file_path = ?
		  AND m.file_path != ?
		  AND e.kind NOT IN ('contains','imports')`,
		filePath, filePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetDependentFilePaths returns file paths of files that depend on filePath.
func (s *Store) GetDependentFilePaths(filePath string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT n.file_path
		FROM nodes n
		JOIN edges e ON e.source = n.id
		JOIN nodes m ON e.target = m.id
		WHERE m.file_path = ?
		  AND n.file_path != ?
		  AND e.kind NOT IN ('contains','imports')`,
		filePath, filePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// -----------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------

// buildFTSQuery converts a plain text query to an FTS5 MATCH expression.
// Mirrors searchNodesFTS in the original.
func buildFTSQuery(text string) string {
	// Replace :: qualifier separator with space before stripping.
	s := strings.ReplaceAll(text, "::", " ")
	// Remove FTS5 special chars.
	s = ftsSpecialCharsReplaced(s)
	var terms []string
	for _, t := range strings.Fields(s) {
		if t == "" {
			continue
		}
		// Strip FTS5 boolean operators.
		upper := strings.ToUpper(t)
		if upper == "AND" || upper == "OR" || upper == "NOT" || upper == "NEAR" {
			continue
		}
		terms = append(terms, `"`+t+`"*`)
	}
	return strings.Join(terms, " OR ")
}

func ftsSpecialCharsReplaced(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\'', '"', '*', '(', ')', ':', '^':
			// drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// scanNodeWithScoreRow scans a row that has nodeColumns plus a trailing score column.
func scanNodeWithScoreRow(rows *sql.Rows) (model.Node, float64, error) {
	var (
		n                                           model.Node
		kind, lang                                  string
		docstring, signature, visibility            sql.NullString
		decoratorsRaw, typeParamsRaw, returnTypeRaw sql.NullString
		isExported, isAsync, isStatic, isAbstract   int
		score                                       float64
	)
	err := rows.Scan(
		&n.ID, &kind, &n.Name, &n.QualifiedName, &n.FilePath, &lang,
		&n.StartLine, &n.EndLine, &n.StartColumn, &n.EndColumn,
		&docstring, &signature, &visibility,
		&isExported, &isAsync, &isStatic, &isAbstract,
		&decoratorsRaw, &typeParamsRaw, &returnTypeRaw, &n.UpdatedAt,
		&score,
	)
	if err != nil {
		return n, 0, err
	}
	n.Kind = model.NodeKind(kind)
	n.Language = model.Language(lang)
	n.Docstring = docstring.String
	n.Signature = signature.String
	if visibility.Valid {
		v := visibility.String
		n.Visibility = &v
	}
	n.IsExported = isExported == 1
	n.IsAsync = isAsync == 1
	n.IsStatic = isStatic == 1
	n.IsAbstract = isAbstract == 1
	n.Decorators = parseJSONArray(decoratorsRaw)
	n.TypeParameters = parseJSONArray(typeParamsRaw)
	n.ReturnType = returnTypeRaw.String
	return n, score, nil
}

// sortCandidates sorts candidates ascending by dist (stable).
func sortCandidates(cands []fuzzyCandidate) {
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0 && cands[j].dist < cands[j-1].dist; j-- {
			cands[j], cands[j-1] = cands[j-1], cands[j]
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

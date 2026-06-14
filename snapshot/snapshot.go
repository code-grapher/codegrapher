// Package snapshot exports and imports the codegrapher index as INGR files.
//
// Layout inside the output directory (default: codegraph/):
//
//	.ingitdb.yaml                     root inGitDB config
//	README.md                         generated summary
//	nodes/nodes.ingr                  node records
//	edges/edges.ingr                  edge records
//	files/files.ingr                  file records
//	project_metadata/project_metadata.ingr  metadata records
//
// The directory is a valid inGitDB database (`ingitdb validate` exits 0).
// Records are sorted by primary key for byte-determinism.
// Volatile fields (updated_at, indexed_at, modified_at) are excluded.
// Two exports of the same code tree are byte-identical.
//
// Uses github.com/ingr-io/ingr-go (official MIT library, pure Go).
package snapshot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ingr-io/ingr-go/ingr"
	"github.com/specscore/codegrapher/store"
)

// DefaultSnapshotDir is the default output/input directory for snapshots,
// relative to the project root (the path passed to export/import).
const DefaultSnapshotDir = "codegraph"

// Export writes INGR snapshot files for the index to outDir, structured as a
// valid inGitDB database. projectRoot is used to detect the git remote for the
// README; pass "" to fall back to the outDir parent name.
// outDir is created if it does not exist.
func Export(dbPath, outDir, projectRoot string) error {
	s, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("snapshot: open store: %w", err)
	}
	defer s.Close()

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("snapshot: mkdir %s: %w", outDir, err)
	}

	if err := writeIngitdbConfig(outDir); err != nil {
		return fmt.Errorf("snapshot: ingitdb config: %w", err)
	}
	if err := exportNodes(s, outDir); err != nil {
		return fmt.Errorf("snapshot: nodes: %w", err)
	}
	if err := exportEdges(s, outDir); err != nil {
		return fmt.Errorf("snapshot: edges: %w", err)
	}
	if err := exportFiles(s, outDir); err != nil {
		return fmt.Errorf("snapshot: files: %w", err)
	}
	if err := exportMetadata(s, outDir); err != nil {
		return fmt.Errorf("snapshot: metadata: %w", err)
	}
	if err := exportCoverage(s, outDir); err != nil {
		return fmt.Errorf("snapshot: coverage: %w", err)
	}
	if err := exportNodeCoverage(s, outDir); err != nil {
		return fmt.Errorf("snapshot: node_coverage: %w", err)
	}
	if err := writeREADME(outDir, projectRoot); err != nil {
		return fmt.Errorf("snapshot: readme: %w", err)
	}
	return nil
}

// Import reads INGR snapshot files from inDir and initializes the store at
// dbPath, replacing any existing data. Run sync afterward to reconcile drift.
func Import(dbPath, inDir string) error {
	// Remove existing DB so Initialize creates a fresh one.
	_ = os.Remove(dbPath)
	s, err := store.Initialize(dbPath)
	if err != nil {
		return fmt.Errorf("snapshot: init store: %w", err)
	}
	defer s.Close()

	if err := importNodes(s, ingrPath(inDir, "nodes")); err != nil {
		return fmt.Errorf("snapshot: nodes: %w", err)
	}
	if err := importEdges(s, ingrPath(inDir, "edges")); err != nil {
		return fmt.Errorf("snapshot: edges: %w", err)
	}
	if err := importFiles(s, ingrPath(inDir, "files")); err != nil {
		return fmt.Errorf("snapshot: files: %w", err)
	}
	if err := importMetadata(s, ingrPath(inDir, "project_metadata")); err != nil {
		return fmt.Errorf("snapshot: metadata: %w", err)
	}
	// Coverage recordsets are optional: older snapshots omit them.
	if err := importCoverage(s, ingrPath(inDir, "coverage")); err != nil {
		return fmt.Errorf("snapshot: coverage: %w", err)
	}
	if err := importNodeCoverage(s, ingrPath(inDir, "node_coverage")); err != nil {
		return fmt.Errorf("snapshot: node_coverage: %w", err)
	}
	return nil
}

// ingrPath returns the path to a collection's INGR file inside outDir.
// Layout: outDir/<name>/<name>.ingr
func ingrPath(outDir, name string) string {
	return filepath.Join(outDir, name, name+".ingr")
}

// ---------------------------------------------------------------------------
// nodes.ingr
// ---------------------------------------------------------------------------

// Excluded: updated_at (volatile)
var nodeCols = []ingr.ColDef{
	{Name: "$ID"}, {Name: "kind"}, {Name: "name"}, {Name: "qualified_name"},
	{Name: "file_path"}, {Name: "language"},
	{Name: "start_line", Type: "int"}, {Name: "end_line", Type: "int"},
	{Name: "start_column", Type: "int"}, {Name: "end_column", Type: "int"},
	{Name: "docstring"}, {Name: "signature"}, {Name: "visibility"},
	{Name: "is_exported", Type: "bool"}, {Name: "is_async", Type: "bool"},
	{Name: "is_static", Type: "bool"}, {Name: "is_abstract", Type: "bool"},
	{Name: "decorators"}, {Name: "type_parameters"}, {Name: "return_type"},
}

func exportNodes(s *store.Store, outDir string) error {
	// Use AllNodes which returns nodes sorted by id.
	nodes, err := s.AllNodes()
	if err != nil {
		return err
	}
	// Ensure sorted by id for determinism.
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	rows := make([]recordRow, 0, len(nodes))
	for _, n := range nodes {
		row := recordRow{
			id: n.ID,
			data: map[string]any{
				"kind":            string(n.Kind),
				"name":            n.Name,
				"qualified_name":  n.QualifiedName,
				"file_path":       n.FilePath,
				"language":        string(n.Language),
				"start_line":      n.StartLine,
				"end_line":        n.EndLine,
				"start_column":    n.StartColumn,
				"end_column":      n.EndColumn,
				"docstring":       n.Docstring,
				"signature":       n.Signature,
				"visibility":      visibilityVal(n.Visibility),
				"is_exported":     n.IsExported,
				"is_async":        n.IsAsync,
				"is_static":       n.IsStatic,
				"is_abstract":     n.IsAbstract,
				"decorators":      n.Decorators,
				"type_parameters": n.TypeParameters,
				"return_type":     n.ReturnType,
			},
		}
		rows = append(rows, row)
	}
	dir := filepath.Join(outDir, "nodes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeCollectionDef(dir, nodeCollectionDef); err != nil {
		return err
	}
	return writeINGR(filepath.Join(dir, "nodes.ingr"), "nodes", nodeCols, rows)
}

func importNodes(s *store.Store, path string) error {
	maps, err := readINGR(path)
	if err != nil {
		return err
	}
	return s.Transaction(func(tx *sql.Tx) error {
		for _, row := range maps {
			_, err := tx.Exec(`
				INSERT OR REPLACE INTO nodes
				(id, kind, name, qualified_name, file_path, language,
				 start_line, end_line, start_column, end_column,
				 docstring, signature, visibility,
				 is_exported, is_async, is_static, is_abstract,
				 decorators, type_parameters, return_type, updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`,
				strVal(row["$ID"]), strVal(row["kind"]), strVal(row["name"]),
				strVal(row["qualified_name"]), strVal(row["file_path"]), strVal(row["language"]),
				intVal(row["start_line"]), intVal(row["end_line"]),
				intVal(row["start_column"]), intVal(row["end_column"]),
				nullStr(strVal(row["docstring"])), nullStr(strVal(row["signature"])),
				nullStr(strVal(row["visibility"])),
				boolToInt(row["is_exported"]), boolToInt(row["is_async"]),
				boolToInt(row["is_static"]), boolToInt(row["is_abstract"]),
				jsonArrStr(row["decorators"]), jsonArrStr(row["type_parameters"]),
				nullStr(strVal(row["return_type"])),
			)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// edges.ingr
// ---------------------------------------------------------------------------

// $ID synthesized as "source|target|kind|line|col". Excluded: none.
var edgeCols = []ingr.ColDef{
	{Name: "$ID"}, {Name: "source"}, {Name: "target"}, {Name: "kind"},
	{Name: "metadata"}, {Name: "line", Type: "int"}, {Name: "col", Type: "int"},
	{Name: "provenance"},
}

func exportEdges(s *store.Store, outDir string) error {
	edges, err := s.AllEdges()
	if err != nil {
		return err
	}
	// Sort by synthesized PK for determinism.
	sort.Slice(edges, func(i, j int) bool {
		ei, ej := edges[i], edges[j]
		ki := edgeKey(ei.Source, ei.Target, string(ei.Kind), ei.Line, ei.Column)
		kj := edgeKey(ej.Source, ej.Target, string(ej.Kind), ej.Line, ej.Column)
		return ki < kj
	})

	rows := make([]recordRow, 0, len(edges))
	for _, e := range edges {
		id := edgeKey(e.Source, e.Target, string(e.Kind), e.Line, e.Column)
		var metaVal any
		if len(e.Metadata) > 0 {
			b, _ := json.Marshal(e.Metadata)
			metaVal = string(b)
		}
		rows = append(rows, recordRow{
			id: id,
			data: map[string]any{
				"source":     e.Source,
				"target":     e.Target,
				"kind":       string(e.Kind),
				"metadata":   metaVal,
				"line":       e.Line,
				"col":        e.Column,
				"provenance": e.Provenance,
			},
		})
	}
	dir := filepath.Join(outDir, "edges")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeCollectionDef(dir, edgeCollectionDef); err != nil {
		return err
	}
	return writeINGR(filepath.Join(dir, "edges.ingr"), "edges", edgeCols, rows)
}

func edgeKey(source, target, kind string, line, col int) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d", source, target, kind, line, col)
}

func importEdges(s *store.Store, path string) error {
	maps, err := readINGR(path)
	if err != nil {
		return err
	}
	return s.Transaction(func(tx *sql.Tx) error {
		for _, row := range maps {
			line := intVal(row["line"])
			col := intVal(row["col"])
			var lineArg, colArg any
			if line != 0 {
				lineArg = line
			}
			if col != 0 {
				colArg = col
			}
			meta := strVal(row["metadata"])
			var metaArg any
			if meta != "" {
				metaArg = meta
			}
			prov := strVal(row["provenance"])
			var provArg any
			if prov != "" {
				provArg = prov
			}
			_, err := tx.Exec(`
				INSERT OR IGNORE INTO edges (source, target, kind, metadata, line, col, provenance)
				VALUES (?,?,?,?,?,?,?)`,
				strVal(row["source"]), strVal(row["target"]), strVal(row["kind"]),
				metaArg, lineArg, colArg, provArg,
			)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// files.ingr
// ---------------------------------------------------------------------------

// Excluded: modified_at, indexed_at (volatile)
var fileCols = []ingr.ColDef{
	{Name: "$ID"}, {Name: "content_hash"}, {Name: "language"},
	{Name: "size", Type: "int"}, {Name: "node_count", Type: "int"}, {Name: "errors"},
}

func exportFiles(s *store.Store, outDir string) error {
	files, err := s.GetAllFiles()
	if err != nil {
		return err
	}
	// Already sorted by path from GetAllFiles, but enforce explicitly.
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	rows := make([]recordRow, 0, len(files))
	for _, f := range files {
		var errsVal any
		if len(f.Errors) > 0 {
			b, _ := json.Marshal(f.Errors)
			errsVal = string(b)
		}
		rows = append(rows, recordRow{
			id: f.Path,
			data: map[string]any{
				"content_hash": f.ContentHash,
				"language":     string(f.Language),
				"size":         f.Size,
				"node_count":   f.NodeCount,
				"errors":       errsVal,
			},
		})
	}
	dir := filepath.Join(outDir, "files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeCollectionDef(dir, fileCollectionDef); err != nil {
		return err
	}
	return writeINGR(filepath.Join(dir, "files.ingr"), "files", fileCols, rows)
}

func importFiles(s *store.Store, path string) error {
	maps, err := readINGR(path)
	if err != nil {
		return err
	}
	return s.Transaction(func(tx *sql.Tx) error {
		for _, row := range maps {
			errsVal := strVal(row["errors"])
			var errsArg any
			if errsVal != "" {
				errsArg = errsVal
			}
			_, err := tx.Exec(`
				INSERT OR REPLACE INTO files
				(path, content_hash, language, size, modified_at, indexed_at, node_count, errors)
				VALUES (?,?,?,?,0,0,?,?)`,
				strVal(row["$ID"]), strVal(row["content_hash"]), strVal(row["language"]),
				int64Val(row["size"]), intVal(row["node_count"]), errsArg,
			)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// project_metadata.ingr
// ---------------------------------------------------------------------------

// Excluded: updated_at (volatile)
var metaCols = []ingr.ColDef{
	{Name: "$ID"}, {Name: "value"},
}

func exportMetadata(s *store.Store, outDir string) error {
	meta, err := s.GetAllMetadata()
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rows := make([]recordRow, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, recordRow{
			id:   k,
			data: map[string]any{"value": meta[k]},
		})
	}
	dir := filepath.Join(outDir, "project_metadata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeCollectionDef(dir, metadataCollectionDef); err != nil {
		return err
	}
	return writeINGR(filepath.Join(dir, "project_metadata.ingr"), "project_metadata", metaCols, rows)
}

func importMetadata(s *store.Store, path string) error {
	maps, err := readINGR(path)
	if err != nil {
		return err
	}
	return s.Transaction(func(tx *sql.Tx) error {
		for _, row := range maps {
			_, err := tx.Exec(`
				INSERT OR REPLACE INTO project_metadata (key, value, updated_at)
				VALUES (?,?,0)`,
				strVal(row["$ID"]), strVal(row["value"]),
			)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// INGR I/O helpers
// ---------------------------------------------------------------------------

type recordRow struct {
	id   string
	data map[string]any
}

func writeINGR(path, title string, cols []ingr.ColDef, rows []recordRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := ingr.NewRecordsWriter(f)
	if _, err := w.WriteHeader(title, cols); err != nil {
		return err
	}

	records := make([]ingr.Record, 0, len(rows))
	for _, row := range rows {
		data := make(map[string]any, len(row.data)+1)
		for k, v := range row.data {
			data[k] = v
		}
		data["$ID"] = row.id
		records = append(records, ingr.NewMapRecordEntry(row.id, data))
	}

	if len(records) > 0 {
		if _, err := w.WriteRecords(0, records...); err != nil {
			return err
		}
	}
	return w.Close()
}

func readINGR(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	dec := ingr.NewDecoder(f)
	var rows []map[string]any
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return rows, nil
}

// ---------------------------------------------------------------------------
// Value coercion helpers (for reading back from ingr maps)
// ---------------------------------------------------------------------------

func strVal(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}

func intVal(v any) int {
	if v == nil {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	default:
		// ingr.Number is just a string alias, handle as string
		s := strVal(v)
		if s == "" {
			return 0
		}
		var n int64
		fmt.Sscanf(s, "%d", &n)
		return int(n)
	}
}

func int64Val(v any) int64 {
	if v == nil {
		return 0
	}
	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	default:
		return int64(intVal(v))
	}
}

func boolToInt(v any) int {
	if v == nil {
		return 0
	}
	switch t := v.(type) {
	case bool:
		if t {
			return 1
		}
		return 0
	case float64:
		if t != 0 {
			return 1
		}
		return 0
	default:
		if intVal(v) != 0 {
			return 1
		}
		return 0
	}
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func visibilityVal(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

// jsonArrStr returns the JSON string for decorators/typeParameters columns,
// which may come back from ingr as []any or as a pre-encoded JSON string.
func jsonArrStr(v any) any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" || s == "null" {
			return nil
		}
		return s
	case []any:
		if len(t) == 0 {
			return nil
		}
		b, err := json.Marshal(t)
		if err != nil {
			return nil
		}
		return string(b)
	default:
		return nil
	}
}

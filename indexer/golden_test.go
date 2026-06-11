package indexer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// --- golden row shapes (the SQLite JSON dump format of the original CLI) ----

type goldenNode struct {
	ID             string  `json:"id"`
	Kind           string  `json:"kind"`
	Name           string  `json:"name"`
	QualifiedName  string  `json:"qualified_name"`
	FilePath       string  `json:"file_path"`
	Language       string  `json:"language"`
	StartLine      int     `json:"start_line"`
	EndLine        int     `json:"end_line"`
	StartColumn    int     `json:"start_column"`
	EndColumn      int     `json:"end_column"`
	Docstring      *string `json:"docstring"`
	Signature      *string `json:"signature"`
	Visibility     *string `json:"visibility"`
	IsExported     int     `json:"is_exported"`
	IsAsync        int     `json:"is_async"`
	IsStatic       int     `json:"is_static"`
	IsAbstract     int     `json:"is_abstract"`
	Decorators     *string `json:"decorators"`
	TypeParameters *string `json:"type_parameters"`
	ReturnType     *string `json:"return_type"`
}

type goldenContains struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

type goldenResEdge struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Kind       string  `json:"kind"`
	Provenance *string `json:"provenance"`
	Line       *int    `json:"line"`
	Col        *int    `json:"col"`
}

type goldenFile struct {
	Path      string `json:"path"`
	Language  string `json:"language"`
	NodeCount int    `json:"nodeCount"`
	Size      int64  `json:"size"`
}

// --- dump helpers ------------------------------------------------------------

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func jsonArr(v []string) *string {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	s := string(b)
	return &s
}

func toGoldenNode(n model.Node) goldenNode {
	return goldenNode{
		ID:             n.ID,
		Kind:           string(n.Kind),
		Name:           n.Name,
		QualifiedName:  n.QualifiedName,
		FilePath:       n.FilePath,
		Language:       string(n.Language),
		StartLine:      n.StartLine,
		EndLine:        n.EndLine,
		StartColumn:    n.StartColumn,
		EndColumn:      n.EndColumn,
		Docstring:      nullable(n.Docstring),
		Signature:      nullable(n.Signature),
		Visibility:     n.Visibility,
		IsExported:     b2i(n.IsExported),
		IsAsync:        b2i(n.IsAsync),
		IsStatic:       b2i(n.IsStatic),
		IsAbstract:     b2i(n.IsAbstract),
		Decorators:     jsonArr(n.Decorators),
		TypeParameters: jsonArr(n.TypeParameters),
		ReturnType:     nullable(n.ReturnType),
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// dumpDB reads every node and edge from the store, via the file records.
func dumpDB(t *testing.T, s *store.Store) (nodes []model.Node, edges []model.Edge) {
	t.Helper()
	files, err := s.GetAllFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		ns, err := s.GetNodesByFile(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, ns...)
	}
	for _, n := range nodes {
		es, err := s.GetOutgoingEdges(n.ID, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		edges = append(edges, es...)
	}
	return nodes, edges
}

func resEdgeKey(e goldenResEdge) string {
	prov := ""
	if e.Provenance != nil {
		prov = *e.Provenance
	}
	line, col := 0, 0
	if e.Line != nil {
		line = *e.Line
	}
	if e.Col != nil {
		col = *e.Col
	}
	return fmt.Sprintf("%s|%s|%s|%s|%d|%d", e.Source, e.Target, e.Kind, prov, line, col)
}

func loadJSON[T any](t *testing.T, path string) []T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

// copyFixture copies a fixture tree into a fresh temp dir.
func copyFixture(t *testing.T, fixtureDir string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(fixtureDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(fixtureDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, src)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// assertGoldenState dumps the store and compares nodes (minus updated_at),
// contains edges, non-contains edges, and file records against the goldens.
//
// Deviation note: golden rows with provenance "heuristic" (2 in go-small) are
// produced by upstream's conformance synthesizer, which the resolve package
// deliberately did not port — its own parity tests exclude them. They are
// excluded from the exact-equality comparison here for the same reason.
func assertGoldenState(t *testing.T, s *store.Store, goldenDir, fixtureCopy string) {
	t.Helper()

	gotNodes, gotEdges := dumpDB(t, s)

	// --- nodes ---
	wantNodes := loadJSON[goldenNode](t, filepath.Join(goldenDir, "extraction-nodes.json"))
	gotGolden := make([]goldenNode, len(gotNodes))
	for i, n := range gotNodes {
		gotGolden[i] = toGoldenNode(n)
	}
	sortByJSON(gotGolden)
	sortByJSON(wantNodes)
	if !reflect.DeepEqual(gotGolden, wantNodes) {
		reportSliceDiff(t, "nodes", gotGolden, wantNodes, func(n goldenNode) string { return n.ID })
	}

	// --- contains edges ---
	wantContains := loadJSON[goldenContains](t, filepath.Join(goldenDir, "extraction-contains.json"))
	var gotContains []goldenContains
	for _, e := range gotEdges {
		if e.Kind == model.EdgeContains {
			gotContains = append(gotContains, goldenContains{Source: e.Source, Target: e.Target, Kind: string(e.Kind)})
		}
	}
	sortByJSON(gotContains)
	sortByJSON(wantContains)
	if !reflect.DeepEqual(gotContains, wantContains) {
		reportSliceDiff(t, "contains edges", gotContains, wantContains,
			func(e goldenContains) string { return e.Source + "->" + e.Target })
	}

	// --- non-contains edges (extraction + resolution) ---
	allWant := loadJSON[goldenResEdge](t, filepath.Join(goldenDir, "resolution-edges.json"))
	var wantRes []goldenResEdge
	for _, e := range allWant {
		if e.Provenance != nil && *e.Provenance == "heuristic" {
			continue // see deviation note above
		}
		wantRes = append(wantRes, e)
	}
	var gotRes []goldenResEdge
	for _, e := range gotEdges {
		if e.Kind == model.EdgeContains {
			continue
		}
		line, col := e.Line, e.Column
		gotRes = append(gotRes, goldenResEdge{
			Source:     e.Source,
			Target:     e.Target,
			Kind:       string(e.Kind),
			Provenance: nullable(e.Provenance),
			Line:       &line,
			Col:        &col,
		})
	}
	// The store reads NULL line/col back as 0, so normalize golden nulls to 0
	// for a total comparison on the same footing.
	//
	// Exact-duplicate rows are collapsed on both sides: ts-small's golden
	// holds two byte-identical copies of two `references` edges (upstream
	// recorded the same unresolved reference twice and resolved both; the
	// edges table has no unique index). The Go extractor records each
	// reference once — the resulting graph is identical.
	normRes := func(es []goldenResEdge) []string {
		seen := map[string]bool{}
		var keys []string
		for _, e := range es {
			k := resEdgeKey(e)
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		return keys
	}
	gk, wk := normRes(gotRes), normRes(wantRes)
	if !reflect.DeepEqual(gk, wk) {
		t.Errorf("non-contains edges mismatch:\n got: %v\nwant: %v", gk, wk)
	}

	// --- unresolved refs must be empty ---
	n, err := s.GetUnresolvedReferencesCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("unresolved_refs count = %d, want 0", n)
	}

	// --- files ---
	// nodeCount in files.json was captured from an older upstream build that
	// did not yet extract Go interface methods (go-small's Reader::Get), so
	// it disagrees with its sibling golden extraction-nodes.json by exactly
	// that node. The nodes dump is the internally consistent spec (and what
	// internal/extract was ported against), so expected nodeCount is the
	// per-file count of golden nodes; path/language/size still come from
	// files.json.
	nodesPerFile := map[string]int{}
	for _, n := range wantNodes {
		nodesPerFile[n.FilePath]++
	}
	wantFiles := loadJSON[goldenFile](t, filepath.Join(goldenDir, "files.json"))
	for i := range wantFiles {
		wantFiles[i].NodeCount = nodesPerFile[wantFiles[i].Path]
	}
	gotFileRecs, err := s.GetAllFiles()
	if err != nil {
		t.Fatal(err)
	}
	var gotFiles []goldenFile
	for _, f := range gotFileRecs {
		gotFiles = append(gotFiles, goldenFile{
			Path: f.Path, Language: string(f.Language), NodeCount: f.NodeCount, Size: f.Size,
		})
	}
	sortByJSON(gotFiles)
	sortByJSON(wantFiles)
	if !reflect.DeepEqual(gotFiles, wantFiles) {
		t.Errorf("files mismatch:\n got: %+v\nwant: %+v", gotFiles, wantFiles)
	}

	// Hashes recomputed from the fixture copy must match the DB.
	for _, f := range gotFileRecs {
		content, err := os.ReadFile(filepath.Join(fixtureCopy, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Errorf("read %s: %v", f.Path, err)
			continue
		}
		if want := HashContent(content); f.ContentHash != want {
			t.Errorf("file %s: contentHash = %s, want %s", f.Path, f.ContentHash, want)
		}
	}
}

func sortByJSON[T any](items []T) {
	sort.Slice(items, func(i, j int) bool {
		a, _ := json.Marshal(items[i])
		b, _ := json.Marshal(items[j])
		return string(a) < string(b)
	})
}

func reportSliceDiff[T any](t *testing.T, label string, got, want []T, key func(T) string) {
	t.Helper()
	gotByKey := map[string]T{}
	for _, g := range got {
		gotByKey[key(g)] = g
	}
	wantByKey := map[string]T{}
	for _, w := range want {
		wantByKey[key(w)] = w
	}
	for k, w := range wantByKey {
		g, ok := gotByKey[k]
		if !ok {
			t.Errorf("%s: missing %s", label, k)
			continue
		}
		if !reflect.DeepEqual(g, w) {
			gj, _ := json.Marshal(g)
			wj, _ := json.Marshal(w)
			t.Errorf("%s: %s differs\n got: %s\nwant: %s", label, k, gj, wj)
		}
	}
	for k := range gotByKey {
		if _, ok := wantByKey[k]; !ok {
			t.Errorf("%s: unexpected %s", label, k)
		}
	}
}

// --- the gates ---------------------------------------------------------------

func TestGoldenInit(t *testing.T) {
	for _, fixture := range []string{"go-small", "ts-small"} {
		t.Run(fixture, func(t *testing.T) {
			root := repoRootDir(t)
			fixtureCopy := copyFixture(t, filepath.Join(root, "testdata", "fixtures", fixture))
			goldenDir := filepath.Join(root, "testdata", "golden", fixture)

			idx, res, err := Init(fixtureCopy, Options{})
			if err != nil {
				t.Fatalf("Init: %v", err)
			}
			defer idx.Close()
			if !res.Success {
				t.Fatalf("Init result not successful: %+v", res)
			}

			assertGoldenState(t, idx.Store(), goldenDir, fixtureCopy)
		})
	}
}

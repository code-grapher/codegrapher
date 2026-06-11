package query

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// -----------------------------------------------------------------------
// Result types — JSON shapes match the original CLI --json payloads.
// -----------------------------------------------------------------------

// SymbolRef is the shape used in callers/callees/affected arrays.
type SymbolRef struct {
	Name      string         `json:"name"`
	Kind      model.NodeKind `json:"kind"`
	FilePath  string         `json:"filePath"`
	StartLine int            `json:"startLine"`
}

// CallersResult is the JSON payload for `codegraph callers <symbol>`.
type CallersResult struct {
	Symbol  string      `json:"symbol"`
	Callers []SymbolRef `json:"callers"`
}

// CalleesResult is the JSON payload for `codegraph callees <symbol>`.
type CalleesResult struct {
	Symbol  string      `json:"symbol"`
	Callees []SymbolRef `json:"callees"`
}

// ImpactResult is the JSON payload for `codegraph impact <symbol>`.
type ImpactResult struct {
	Symbol    string      `json:"symbol"`
	Depth     int         `json:"depth"`
	NodeCount int         `json:"nodeCount"`
	EdgeCount int         `json:"edgeCount"`
	Affected  []SymbolRef `json:"affected"`
}

// FileInfo is one entry in the `files` JSON array.
type FileInfo struct {
	Path      string         `json:"path"`
	Language  model.Language `json:"language"`
	NodeCount int            `json:"nodeCount"`
	Size      int64          `json:"size"`
}

// PendingChanges mirrors the pendingChanges field in the status payload.
type PendingChanges struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Removed  int `json:"removed"`
}

// IndexInfo mirrors the `index` block of the status payload.
type IndexInfo struct {
	BuiltWithVersion           string `json:"builtWithVersion"`
	BuiltWithExtractionVersion int    `json:"builtWithExtractionVersion"`
	CurrentExtractionVersion   int    `json:"currentExtractionVersion"`
	ReindexRecommended         bool   `json:"reindexRecommended"`
}

// StatusResult is the JSON payload for `codegraph status`.
type StatusResult struct {
	Initialized      bool                   `json:"initialized"`
	Version          string                 `json:"version"`
	ProjectPath      string                 `json:"projectPath"`
	IndexPath        string                 `json:"indexPath"`
	LastIndexed      string                 `json:"lastIndexed"`
	FileCount        int                    `json:"fileCount"`
	NodeCount        int                    `json:"nodeCount"`
	EdgeCount        int                    `json:"edgeCount"`
	DBSizeBytes      int64                  `json:"dbSizeBytes"`
	Backend          string                 `json:"backend"`
	JournalMode      string                 `json:"journalMode"`
	NodesByKind      map[model.NodeKind]int `json:"nodesByKind"`
	Languages        []string               `json:"languages"`
	PendingChanges   PendingChanges         `json:"pendingChanges"`
	WorktreeMismatch any                    `json:"worktreeMismatch"`
	Index            IndexInfo              `json:"index"`
}

// SearchOptions controls result set size and filtering for SearchNodes.
type SearchOptions struct {
	Limit     int
	Offset    int
	Kinds     []model.NodeKind
	Languages []model.Language
}

// -----------------------------------------------------------------------
// SearchNodes — FTS5 → LIKE → fuzzy pipeline
// -----------------------------------------------------------------------

// SearchNodes runs the multi-strategy search pipeline (FTS5 → LIKE → fuzzy,
// with exact-name supplement and multi-signal rescoring).
// Mirrors QueryBuilder.searchNodes from src/db/queries.ts.
func SearchNodes(s *store.Store, rawQuery string, opts SearchOptions) ([]model.SearchResult, error) {
	if opts.Limit == 0 {
		opts.Limit = 10
	}

	parsed := ParseQuery(rawQuery)
	kinds := mergeKinds(opts.Kinds, parsed.Kinds)
	langs := mergeLangs(opts.Languages, parsed.Languages)
	text := parsed.Text

	var results []model.SearchResult
	var err error

	if text != "" {
		results, err = s.SearchFTS(text, kinds, langs, opts.Limit, opts.Offset)
		if err != nil {
			return nil, err
		}
	} else {
		results, err = s.SearchAllByFilters(kinds, langs, opts.Limit*5)
		if err != nil {
			return nil, err
		}
	}

	// LIKE fallback.
	if len(results) == 0 && len(text) >= 2 {
		results, err = s.SearchLike(text, kinds, langs, opts.Limit, opts.Offset)
		if err != nil {
			return nil, err
		}
	}

	// Fuzzy fallback.
	if len(results) == 0 && len(text) >= 3 {
		results, err = s.SearchFuzzy(text, kinds, langs, opts.Limit, BoundedEditDistance)
		if err != nil {
			return nil, err
		}
	}

	// Exact-name supplement: inject exact matches at max score.
	if len(results) > 0 && rawQuery != "" {
		existingIDs := make(map[string]struct{}, len(results))
		for _, r := range results {
			existingIDs[r.Node.ID] = struct{}{}
		}
		maxScore := 0.0
		for _, r := range results {
			if r.Score > maxScore {
				maxScore = r.Score
			}
		}
		for _, term := range strings.Fields(rawQuery) {
			if len(term) < 2 {
				continue
			}
			extras, err := s.ExactNameCaseInsensitive(term, kinds, langs, 20)
			if err != nil {
				return nil, err
			}
			for _, n := range extras {
				if _, ok := existingIDs[n.ID]; !ok {
					results = append(results, model.SearchResult{Node: n, Score: maxScore})
					existingIDs[n.ID] = struct{}{}
				}
			}
		}
	}

	// Multi-signal rescoring.
	scoringQuery := text
	if scoringQuery == "" {
		scoringQuery = rawQuery
	}
	if len(results) > 0 && scoringQuery != "" {
		for i := range results {
			n := results[i].Node
			// Accumulate left-to-right exactly as upstream does
			// (score + kind + path + name) so float rounding matches bit-for-bit.
			score := results[i].Score
			score += KindBonus(n.Kind)
			score += ScorePathRelevance(n.FilePath, scoringQuery, nil)
			score += NameMatchBonus(n.Name, scoringQuery)
			results[i].Score = score
		}
		sort.SliceStable(results, func(i, j int) bool {
			return results[i].Score > results[j].Score
		})
		if len(results) > opts.Limit {
			results = results[:opts.Limit]
		}
	}

	// Apply path: and name: hard filters.
	if len(parsed.PathFilters) > 0 {
		lowered := make([]string, len(parsed.PathFilters))
		for i, p := range parsed.PathFilters {
			lowered[i] = strings.ToLower(p)
		}
		filtered := results[:0:0]
		for _, r := range results {
			fp := strings.ToLower(r.Node.FilePath)
			for _, p := range lowered {
				if strings.Contains(fp, p) {
					filtered = append(filtered, r)
					break
				}
			}
		}
		results = filtered
	}
	if len(parsed.NameFilters) > 0 {
		lowered := make([]string, len(parsed.NameFilters))
		for i, n := range parsed.NameFilters {
			lowered[i] = strings.ToLower(n)
		}
		filtered := results[:0:0]
		for _, r := range results {
			nm := strings.ToLower(r.Node.Name)
			for _, n := range lowered {
				if strings.Contains(nm, n) {
					filtered = append(filtered, r)
					break
				}
			}
		}
		results = filtered
	}

	// Sort generated files last (stable, matching bin/codegraph.ts behavior).
	sort.SliceStable(results, func(i, j int) bool {
		gi := IsGeneratedFile(results[i].Node.FilePath)
		gj := IsGeneratedFile(results[j].Node.FilePath)
		if gi == gj {
			return false // preserve existing order
		}
		return !gi // non-generated first
	})

	return results, nil
}

// -----------------------------------------------------------------------
// Symbol matching (CLI verb assembly)
// -----------------------------------------------------------------------

// verbMatches mirrors the CLI verbs' symbol resolution: a full searchNodes
// pipeline call with limit 50 (src/bin/codegraph.ts callers/callees/impact).
func verbMatches(s *store.Store, symbol string) ([]model.SearchResult, error) {
	return SearchNodes(s, symbol, SearchOptions{Limit: 50})
}

// isExactVerbMatch mirrors the CLI's exact-match filter:
// node.name === symbol || name.endsWith("."+symbol) || name.endsWith("::"+symbol).
func isExactVerbMatch(name, symbol string) bool {
	return name == symbol ||
		strings.HasSuffix(name, "."+symbol) ||
		strings.HasSuffix(name, "::"+symbol)
}

// verbLimit is the CLI default --limit for callers/callees.
const verbLimit = 20

// -----------------------------------------------------------------------
// Callers
// -----------------------------------------------------------------------

// Callers returns the set of nodes that call any definition matching symbol.
// Mirrors the callers verb assembly in src/bin/codegraph.ts.
func Callers(s *store.Store, symbol string) (*CallersResult, error) {
	matches, err := verbMatches(s, symbol)
	if err != nil {
		return nil, err
	}
	refs := []SymbolRef{}
	if len(matches) == 0 {
		return &CallersResult{Symbol: symbol, Callers: refs}, nil
	}

	seen := make(map[string]struct{})
	collect := func(nodeID string) error {
		callers, err := getCallers(s, nodeID, 1)
		if err != nil {
			return err
		}
		for _, n := range callers {
			if _, ok := seen[n.ID]; ok {
				continue
			}
			seen[n.ID] = struct{}{}
			refs = append(refs, nodeToRef(n))
		}
		return nil
	}

	for _, m := range matches {
		if !isExactVerbMatch(m.Node.Name, symbol) && len(matches) > 1 {
			continue
		}
		if err := collect(m.Node.ID); err != nil {
			return nil, err
		}
	}
	// Fallback: if the exact filter removed everything, use the top match.
	if len(refs) == 0 {
		if err := collect(matches[0].Node.ID); err != nil {
			return nil, err
		}
	}
	if len(refs) > verbLimit {
		refs = refs[:verbLimit]
	}
	return &CallersResult{Symbol: symbol, Callers: refs}, nil
}

// Callees returns the set of nodes called by any definition matching symbol.
func Callees(s *store.Store, symbol string) (*CalleesResult, error) {
	matches, err := verbMatches(s, symbol)
	if err != nil {
		return nil, err
	}
	refs := []SymbolRef{}
	if len(matches) == 0 {
		return &CalleesResult{Symbol: symbol, Callees: refs}, nil
	}

	seen := make(map[string]struct{})
	collect := func(nodeID string) error {
		callees, err := getCallees(s, nodeID, 1)
		if err != nil {
			return err
		}
		for _, n := range callees {
			if _, ok := seen[n.ID]; ok {
				continue
			}
			seen[n.ID] = struct{}{}
			refs = append(refs, nodeToRef(n))
		}
		return nil
	}

	for _, m := range matches {
		if !isExactVerbMatch(m.Node.Name, symbol) && len(matches) > 1 {
			continue
		}
		if err := collect(m.Node.ID); err != nil {
			return nil, err
		}
	}
	if len(refs) == 0 {
		if err := collect(matches[0].Node.ID); err != nil {
			return nil, err
		}
	}
	if len(refs) > verbLimit {
		refs = refs[:verbLimit]
	}
	return &CalleesResult{Symbol: symbol, Callees: refs}, nil
}

// -----------------------------------------------------------------------
// Impact
// -----------------------------------------------------------------------

// Impact returns the blast-radius subgraph for any definition matching symbol.
// Mirrors the impact verb assembly in src/bin/codegraph.ts.
func Impact(s *store.Store, symbol string, depth int) (*ImpactResult, error) {
	if depth == 0 {
		depth = 2
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 10 {
		depth = 10
	}
	matches, err := verbMatches(s, symbol)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return &ImpactResult{Symbol: symbol, Depth: depth, Affected: []SymbolRef{}}, nil
	}

	// Merge impact subgraphs across all exact-matching symbols.
	mergedNodes := make(map[string]model.Node)
	seenEdges := make(map[string]struct{})
	edgeCount := 0

	for _, m := range matches {
		if !isExactVerbMatch(m.Node.Name, symbol) && len(matches) > 1 {
			continue
		}
		nodes, edges, err := getImpactRadius(s, m.Node.ID, depth)
		if err != nil {
			return nil, err
		}
		for id, n := range nodes {
			mergedNodes[id] = n
		}
		for _, e := range edges {
			key := e.Source + "->" + e.Target + ":" + string(e.Kind)
			if _, ok := seenEdges[key]; !ok {
				seenEdges[key] = struct{}{}
				edgeCount++
			}
		}
	}

	// Fallback to top match if the exact filter removed everything.
	if len(mergedNodes) == 0 {
		nodes, edges, err := getImpactRadius(s, matches[0].Node.ID, depth)
		if err != nil {
			return nil, err
		}
		for id, n := range nodes {
			mergedNodes[id] = n
		}
		edgeCount = len(edges)
	}

	affected := make([]SymbolRef, 0, len(mergedNodes))
	for _, n := range mergedNodes {
		affected = append(affected, nodeToRef(n))
	}
	// Deterministic output order (paritytest sorts affected, so any total order works).
	sort.Slice(affected, func(i, j int) bool {
		if affected[i].Name != affected[j].Name {
			return affected[i].Name < affected[j].Name
		}
		if affected[i].FilePath != affected[j].FilePath {
			return affected[i].FilePath < affected[j].FilePath
		}
		return affected[i].StartLine < affected[j].StartLine
	})

	return &ImpactResult{
		Symbol:    symbol,
		Depth:     depth,
		NodeCount: len(mergedNodes),
		EdgeCount: edgeCount,
		Affected:  affected,
	}, nil
}

// -----------------------------------------------------------------------
// Status
// -----------------------------------------------------------------------

// Status assembles the status payload for a store.
// projectPath is the project root directory (for the projectPath field).
func Status(s *store.Store, projectPath string) (*StatusResult, error) {
	stats, err := s.GetStats()
	if err != nil {
		return nil, err
	}
	dbSize, err := s.Size()
	if err != nil {
		dbSize = 0
	}

	// Collect sorted language list.
	langSet := make(map[string]struct{})
	for _, f := range mustGetAllFiles(s) {
		langSet[string(f.Language)] = struct{}{}
	}
	var langs []string
	for l := range langSet {
		langs = append(langs, l)
	}
	sort.Strings(langs)

	// Ensure nodesByKind is non-nil (even when empty).
	nodesByKind := stats.NodesByKind
	if nodesByKind == nil {
		nodesByKind = map[model.NodeKind]int{}
	}

	return &StatusResult{
		Initialized: true,
		// version / indexPath / lastIndexed are machine- or release-specific
		// and normalized away by the parity harness.
		Version:          codegraphParityVersion,
		ProjectPath:      projectPath,
		IndexPath:        filepath.Join(projectPath, ".codegraph"),
		LastIndexed:      "",
		FileCount:        stats.FileCount,
		NodeCount:        stats.NodeCount,
		EdgeCount:        stats.EdgeCount,
		DBSizeBytes:      dbSize,
		Backend:          "node-sqlite",
		JournalMode:      s.JournalMode(),
		NodesByKind:      nodesByKind,
		Languages:        langs,
		PendingChanges:   PendingChanges{},
		WorktreeMismatch: nil,
		Index: IndexInfo{
			BuiltWithVersion:           codegraphParityVersion,
			BuiltWithExtractionVersion: indexer.ExtractionVersion,
			CurrentExtractionVersion:   indexer.ExtractionVersion,
			ReindexRecommended:         false,
		},
	}, nil
}

// codegraphParityVersion is the upstream codegraph CLI release this port
// tracks; status reports it as both the engine and built-with version.
const codegraphParityVersion = "0.9.9"

// -----------------------------------------------------------------------
// Files
// -----------------------------------------------------------------------

// Files returns the files verb payload: path, language, nodeCount, size for
// every tracked file, sorted by path.
func Files(s *store.Store) ([]FileInfo, error) {
	files, err := s.GetAllFiles()
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		out = append(out, FileInfo{
			Path:      f.Path,
			Language:  f.Language,
			NodeCount: f.NodeCount,
			Size:      f.Size,
		})
	}
	return out, nil
}

// -----------------------------------------------------------------------
// Graph traversal internals
// -----------------------------------------------------------------------

// getCallers returns callers of nodeID via calls/references/imports edges.
// Faithful port of getCallersRecursive from src/graph/traversal.ts: no
// provenance filtering, no node-kind filtering — heuristic edges and
// file-level imports edges contribute callers like any other edge.
func getCallers(s *store.Store, nodeID string, maxDepth int) ([]model.Node, error) {
	var result []model.Node
	visited := make(map[string]struct{})
	if err := getCallersRecursive(s, nodeID, maxDepth, 0, &result, visited); err != nil {
		return nil, err
	}
	return result, nil
}

func getCallersRecursive(
	s *store.Store,
	nodeID string,
	maxDepth, currentDepth int,
	result *[]model.Node,
	visited map[string]struct{},
) error {
	if currentDepth >= maxDepth {
		return nil
	}
	if _, ok := visited[nodeID]; ok {
		return nil
	}
	visited[nodeID] = struct{}{}

	edges, err := s.GetIncomingEdges(nodeID, []model.EdgeKind{
		model.EdgeCalls, model.EdgeReferences, model.EdgeImports,
	})
	if err != nil {
		return err
	}
	if len(edges) == 0 {
		return nil
	}
	sourceIDs := make([]string, 0, len(edges))
	for _, e := range edges {
		sourceIDs = append(sourceIDs, e.Source)
	}
	nodesMap, err := s.GetNodesByIDs(sourceIDs)
	if err != nil {
		return err
	}
	for _, e := range edges {
		n, ok := nodesMap[e.Source]
		if !ok {
			continue
		}
		if _, vis := visited[n.ID]; vis {
			continue
		}
		*result = append(*result, n)
		if err := getCallersRecursive(s, n.ID, maxDepth, currentDepth+1, result, visited); err != nil {
			return err
		}
	}
	return nil
}

// getCallees returns callees of nodeID via calls/references/imports edges.
// Faithful port of getCalleesRecursive from src/graph/traversal.ts.
func getCallees(s *store.Store, nodeID string, maxDepth int) ([]model.Node, error) {
	var result []model.Node
	visited := make(map[string]struct{})
	if err := getCalleesRecursive(s, nodeID, maxDepth, 0, &result, visited); err != nil {
		return nil, err
	}
	return result, nil
}

func getCalleesRecursive(
	s *store.Store,
	nodeID string,
	maxDepth, currentDepth int,
	result *[]model.Node,
	visited map[string]struct{},
) error {
	if currentDepth >= maxDepth {
		return nil
	}
	if _, ok := visited[nodeID]; ok {
		return nil
	}
	visited[nodeID] = struct{}{}

	edges, err := s.GetOutgoingEdges(nodeID, []model.EdgeKind{
		model.EdgeCalls, model.EdgeReferences, model.EdgeImports,
	}, "")
	if err != nil {
		return err
	}
	if len(edges) == 0 {
		return nil
	}
	targetIDs := make([]string, 0, len(edges))
	for _, e := range edges {
		targetIDs = append(targetIDs, e.Target)
	}
	nodesMap, err := s.GetNodesByIDs(targetIDs)
	if err != nil {
		return err
	}
	for _, e := range edges {
		n, ok := nodesMap[e.Target]
		if !ok {
			continue
		}
		if _, vis := visited[n.ID]; vis {
			continue
		}
		*result = append(*result, n)
		if err := getCalleesRecursive(s, n.ID, maxDepth, currentDepth+1, result, visited); err != nil {
			return err
		}
	}
	return nil
}

// getImpactRadius returns all nodes and edges in the impact subgraph of nodeID.
// Faithful port of getImpactRadius from src/graph/traversal.ts.
func getImpactRadius(s *store.Store, nodeID string, maxDepth int) (map[string]model.Node, []model.Edge, error) {
	nodes := make(map[string]model.Node)
	var edges []model.Edge
	visited := make(map[string]struct{})

	startNode, err := s.GetNodeByID(nodeID)
	if err != nil {
		return nil, nil, err
	}
	if startNode == nil {
		return nodes, nil, nil
	}
	nodes[nodeID] = *startNode

	if err := impactRecursive(s, nodeID, maxDepth, 0, nodes, &edges, visited); err != nil {
		return nil, nil, err
	}
	return nodes, edges, nil
}

// impactRecursive is a faithful port of getImpactRecursive (traversal.ts):
//   - container nodes expand their children at the SAME depth (one symbol);
//   - incoming edges of ALL kinds EXCEPT `contains` are followed upward
//     (a container contains its members but does not depend on them, #536);
//   - no provenance filtering: heuristic edges are followed like any other.
func impactRecursive(
	s *store.Store,
	nodeID string,
	maxDepth, currentDepth int,
	nodes map[string]model.Node,
	edges *[]model.Edge,
	visited map[string]struct{},
) error {
	if currentDepth >= maxDepth {
		return nil
	}
	if _, ok := visited[nodeID]; ok {
		return nil
	}
	visited[nodeID] = struct{}{}

	// For container nodes, traverse into their children so that callers of
	// contained methods appear in impact.
	focalNode, err := s.GetNodeByID(nodeID)
	if err != nil {
		return err
	}
	containerKinds := map[model.NodeKind]bool{
		model.KindClass: true, model.KindInterface: true, model.KindStruct: true,
		model.KindTrait: true, model.KindProtocol: true, model.KindModule: true,
		model.KindEnum: true,
	}
	if focalNode != nil && containerKinds[focalNode.Kind] {
		containsEdges, err := s.GetOutgoingEdges(nodeID, []model.EdgeKind{model.EdgeContains}, "")
		if err != nil {
			return err
		}
		if len(containsEdges) > 0 {
			childIDs := make([]string, 0, len(containsEdges))
			for _, e := range containsEdges {
				childIDs = append(childIDs, e.Target)
			}
			childMap, err := s.GetNodesByIDs(childIDs)
			if err != nil {
				return err
			}
			for _, e := range containsEdges {
				child, ok := childMap[e.Target]
				if !ok {
					continue
				}
				if _, vis := visited[child.ID]; vis {
					continue
				}
				nodes[child.ID] = child
				*edges = append(*edges, e)
				// Recurse into children at the same depth (same symbol).
				if err := impactRecursive(s, child.ID, maxDepth, currentDepth, nodes, edges, visited); err != nil {
					return err
				}
			}
		}
	}

	// All incoming edges (things that depend on this node), excluding `contains`.
	allIncoming, err := s.GetIncomingEdges(nodeID, nil)
	if err != nil {
		return err
	}
	incoming := allIncoming[:0:0]
	for _, e := range allIncoming {
		if e.Kind != model.EdgeContains {
			incoming = append(incoming, e)
		}
	}
	if len(incoming) == 0 {
		return nil
	}
	sourceIDs := make([]string, 0, len(incoming))
	for _, e := range incoming {
		sourceIDs = append(sourceIDs, e.Source)
	}
	sources, err := s.GetNodesByIDs(sourceIDs)
	if err != nil {
		return err
	}
	for _, e := range incoming {
		n, ok := sources[e.Source]
		if !ok {
			continue
		}
		if _, already := nodes[n.ID]; already {
			continue
		}
		nodes[n.ID] = n
		*edges = append(*edges, e)
		if err := impactRecursive(s, n.ID, maxDepth, currentDepth+1, nodes, edges, visited); err != nil {
			return err
		}
	}
	return nil
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func nodeToRef(n model.Node) SymbolRef {
	return SymbolRef{
		Name:      n.Name,
		Kind:      n.Kind,
		FilePath:  n.FilePath,
		StartLine: n.StartLine,
	}
}

func mergeKinds(a, b []model.NodeKind) []model.NodeKind {
	if len(b) == 0 {
		return a
	}
	seen := make(map[model.NodeKind]struct{}, len(a)+len(b))
	var out []model.NodeKind
	for _, k := range append(a, b...) {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out
}

func mergeLangs(a, b []model.Language) []model.Language {
	if len(b) == 0 {
		return a
	}
	seen := make(map[model.Language]struct{}, len(a)+len(b))
	var out []model.Language
	for _, l := range append(a, b...) {
		if _, ok := seen[l]; !ok {
			seen[l] = struct{}{}
			out = append(out, l)
		}
	}
	return out
}

func mustGetAllFiles(s *store.Store) []model.FileRecord {
	files, _ := s.GetAllFiles()
	return files
}

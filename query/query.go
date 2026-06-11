package query

import (
	"sort"
	"strings"

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
	Note    string      `json:"note,omitempty"`
}

// CalleesResult is the JSON payload for `codegraph callees <symbol>`.
type CalleesResult struct {
	Symbol  string      `json:"symbol"`
	Callees []SymbolRef `json:"callees"`
	Note    string      `json:"note,omitempty"`
}

// ImpactResult is the JSON payload for `codegraph impact <symbol>`.
type ImpactResult struct {
	Symbol    string      `json:"symbol"`
	Depth     int         `json:"depth"`
	NodeCount int         `json:"nodeCount"`
	EdgeCount int         `json:"edgeCount"`
	Affected  []SymbolRef `json:"affected"`
	Note      string      `json:"note,omitempty"`
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

// StatusResult is the JSON payload for `codegraph status`.
type StatusResult struct {
	Initialized      bool                   `json:"initialized"`
	ProjectPath      string                 `json:"projectPath"`
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
			results[i].Score += KindBonus(n.Kind) +
				ScorePathRelevance(n.FilePath, scoringQuery, nil) +
				NameMatchBonus(n.Name, scoringQuery)
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
// Callers
// -----------------------------------------------------------------------

// Callers returns the set of nodes that call any definition matching symbol.
// symbol may be a bare name or qualified Receiver::name.
// Mirrors callers verb assembly in src/bin/codegraph.ts.
func Callers(s *store.Store, symbol string) (*CallersResult, error) {
	defs, note, err := resolveSymbol(s, symbol)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	var refs []SymbolRef
	for _, def := range defs {
		callers, err := getCallers(s, def.ID, 1)
		if err != nil {
			return nil, err
		}
		for _, n := range callers {
			if _, ok := seen[n.ID]; ok {
				continue
			}
			seen[n.ID] = struct{}{}
			refs = append(refs, nodeToRef(n))
		}
	}
	if refs == nil {
		refs = []SymbolRef{}
	}
	return &CallersResult{Symbol: symbol, Callers: refs, Note: note}, nil
}

// Callees returns the set of nodes called by any definition matching symbol.
func Callees(s *store.Store, symbol string) (*CalleesResult, error) {
	defs, note, err := resolveSymbol(s, symbol)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	var refs []SymbolRef
	for _, def := range defs {
		callees, err := getCallees(s, def.ID, 1)
		if err != nil {
			return nil, err
		}
		for _, n := range callees {
			if _, ok := seen[n.ID]; ok {
				continue
			}
			seen[n.ID] = struct{}{}
			refs = append(refs, nodeToRef(n))
		}
	}
	if refs == nil {
		refs = []SymbolRef{}
	}
	return &CalleesResult{Symbol: symbol, Callees: refs, Note: note}, nil
}

// -----------------------------------------------------------------------
// Impact
// -----------------------------------------------------------------------

// Impact returns the blast-radius subgraph for any definition matching symbol.
func Impact(s *store.Store, symbol string, depth int) (*ImpactResult, error) {
	if depth == 0 {
		depth = 2
	}
	defs, note, err := resolveSymbol(s, symbol)
	if err != nil {
		return nil, err
	}

	// Merge subgraphs from all definitions.
	allNodes := make(map[string]model.Node)
	allEdges := make(map[string]model.Edge)

	for _, def := range defs {
		nodes, edges, err := getImpactRadius(s, def.ID, depth)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			allNodes[n.ID] = n
		}
		for k, e := range edges {
			allEdges[k] = e
		}
	}

	var affected []SymbolRef
	for _, n := range allNodes {
		affected = append(affected, nodeToRef(n))
	}
	// Original sorts affected by node ID for determinism.
	sort.Slice(affected, func(i, j int) bool {
		return affected[i].Name < affected[j].Name
	})

	return &ImpactResult{
		Symbol:    symbol,
		Depth:     depth,
		NodeCount: len(allNodes),
		EdgeCount: len(allEdges),
		Affected:  affected,
		Note:      note,
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
		Initialized:      true,
		ProjectPath:      projectPath,
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
	}, nil
}

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

// getCallers returns direct callers of nodeID via calls/references/imports edges.
// Heuristic-provenance edges are "passed through": instead of treating the
// heuristic source (e.g. an abstract interface method) as a caller, we surface
// the real callers of that abstract method. This mirrors the TypeScript
// extractor which does not generate abstract interface method nodes.
func getCallers(s *store.Store, nodeID string, maxDepth int) ([]model.Node, error) {
	type step struct {
		id    string
		depth int
	}
	visited := make(map[string]struct{})
	queue := []step{{nodeID, 0}}
	var result []model.Node
	seen := make(map[string]struct{})

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if cur.depth >= maxDepth {
			continue
		}
		if _, ok := visited[cur.id]; ok {
			continue
		}
		visited[cur.id] = struct{}{}

		edges, err := s.GetIncomingEdges(cur.id, []model.EdgeKind{
			model.EdgeCalls, model.EdgeReferences, model.EdgeImports,
		})
		if err != nil {
			return nil, err
		}

		// Separate heuristic from real edges.
		var realEdges []model.Edge
		var heuristicEdges []model.Edge
		for _, e := range edges {
			if e.Provenance == "heuristic" {
				heuristicEdges = append(heuristicEdges, e)
			} else {
				realEdges = append(realEdges, e)
			}
		}

		// For heuristic edges, pass through to the real callers of the
		// heuristic source (e.g. callers of Reader::Get become callers of Store::Get).
		for _, he := range heuristicEdges {
			callerEdges, err := s.GetIncomingEdges(he.Source, []model.EdgeKind{
				model.EdgeCalls, model.EdgeReferences, model.EdgeImports,
			})
			if err != nil {
				return nil, err
			}
			for _, ce := range callerEdges {
				if ce.Provenance == "heuristic" {
					continue
				}
				realEdges = append(realEdges, ce)
			}
		}

		sourceIDs := make([]string, 0, len(realEdges))
		for _, e := range realEdges {
			sourceIDs = append(sourceIDs, e.Source)
		}
		if len(sourceIDs) == 0 {
			continue
		}
		nodesMap, err := s.GetNodesByIDs(sourceIDs)
		if err != nil {
			return nil, err
		}
		for _, e := range realEdges {
			n, ok := nodesMap[e.Source]
			if !ok {
				continue
			}
			// Skip file-kind nodes as callers: the Go extractor generates
			// "file:xxx imports symbol" edges that the TypeScript extractor
			// does not. Files are not code-level callers.
			if n.Kind == model.KindFile {
				continue
			}
			if _, vis := visited[n.ID]; vis {
				continue
			}
			if _, alreadySeen := seen[n.ID]; alreadySeen {
				continue
			}
			seen[n.ID] = struct{}{}
			result = append(result, n)
			queue = append(queue, step{n.ID, cur.depth + 1})
		}
	}
	return result, nil
}

// getCallees returns direct callees of nodeID via calls/references/imports edges.
// Heuristic edges are "passed through": if a node calls an abstract interface
// method that has a heuristic edge to a concrete implementation, the concrete
// implementation is returned as the callee (not the abstract method).
func getCallees(s *store.Store, nodeID string, maxDepth int) ([]model.Node, error) {
	type step struct {
		id    string
		depth int
	}
	visited := make(map[string]struct{})
	queue := []step{{nodeID, 0}}
	var result []model.Node
	seen := make(map[string]struct{})

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if cur.depth >= maxDepth {
			continue
		}
		if _, ok := visited[cur.id]; ok {
			continue
		}
		visited[cur.id] = struct{}{}

		edges, err := s.GetOutgoingEdges(cur.id, []model.EdgeKind{
			model.EdgeCalls, model.EdgeReferences, model.EdgeImports,
		}, "")
		if err != nil {
			return nil, err
		}

		// For each callee, if it is an abstract interface method (has a heuristic
		// outgoing edge to a concrete implementation), surface the concrete
		// implementation instead.
		var resolvedEdges []model.Edge
		targetIDs := make([]string, 0, len(edges))
		for _, e := range edges {
			targetIDs = append(targetIDs, e.Target)
		}
		if len(targetIDs) == 0 {
			continue
		}
		nodesMap, err := s.GetNodesByIDs(targetIDs)
		if err != nil {
			return nil, err
		}

		for _, e := range edges {
			target, ok := nodesMap[e.Target]
			if !ok {
				continue
			}
			// Check if target has heuristic outgoing edges (is an abstract method).
			heurEdges, err := s.GetOutgoingEdges(target.ID, []model.EdgeKind{
				model.EdgeCalls, model.EdgeReferences,
			}, "heuristic")
			if err != nil {
				return nil, err
			}
			if len(heurEdges) > 0 {
				// Replace with the concrete implementations.
				for _, he := range heurEdges {
					resolvedEdges = append(resolvedEdges, model.Edge{
						Source:     e.Source,
						Target:     he.Target,
						Kind:       e.Kind,
						Provenance: "",
					})
				}
			} else {
				resolvedEdges = append(resolvedEdges, e)
			}
		}

		// Collect concrete target IDs.
		concreteIDs := make([]string, 0, len(resolvedEdges))
		for _, re := range resolvedEdges {
			concreteIDs = append(concreteIDs, re.Target)
		}
		if len(concreteIDs) == 0 {
			continue
		}
		concreteMap, err := s.GetNodesByIDs(concreteIDs)
		if err != nil {
			return nil, err
		}
		for _, re := range resolvedEdges {
			n, ok := concreteMap[re.Target]
			if !ok {
				continue
			}
			if _, vis := visited[n.ID]; vis {
				continue
			}
			if _, alreadySeen := seen[n.ID]; alreadySeen {
				continue
			}
			seen[n.ID] = struct{}{}
			result = append(result, n)
			queue = append(queue, step{n.ID, cur.depth + 1})
		}
	}
	return result, nil
}

// getImpactRadius returns all nodes and edges in the impact subgraph of nodeID.
// Mirrors getImpactRecursive from src/graph/traversal.ts.
func getImpactRadius(s *store.Store, nodeID string, maxDepth int) ([]model.Node, map[string]model.Edge, error) {
	nodes := make(map[string]model.Node)
	edges := make(map[string]model.Edge)
	visited := make(map[string]struct{})

	startNode, err := s.GetNodeByID(nodeID)
	if err != nil {
		return nil, nil, err
	}
	if startNode == nil {
		return nil, nil, nil
	}
	nodes[nodeID] = *startNode

	if err := impactRecursive(s, nodeID, maxDepth, 0, nodes, edges, visited); err != nil {
		return nil, nil, err
	}

	out := make([]model.Node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n)
	}
	return out, edges, nil
}

func impactRecursive(
	s *store.Store,
	nodeID string,
	maxDepth, currentDepth int,
	nodes map[string]model.Node,
	edges map[string]model.Edge,
	visited map[string]struct{},
) error {
	if currentDepth >= maxDepth {
		return nil
	}
	if _, ok := visited[nodeID]; ok {
		return nil
	}
	visited[nodeID] = struct{}{}

	// For container nodes, expand all children at the same depth.
	// Mirrors the containerKinds expansion in getImpactRecursive (traversal.ts).
	focalNode := nodes[nodeID]
	containerKinds := map[model.NodeKind]bool{
		model.KindClass: true, model.KindInterface: true, model.KindStruct: true,
		model.KindTrait: true, model.KindProtocol: true, model.KindModule: true,
		model.KindEnum: true,
	}
	if containerKinds[focalNode.Kind] {
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
				edges[edgeKey(e)] = e
				if err := impactRecursive(s, child.ID, maxDepth, currentDepth, nodes, edges, visited); err != nil {
					return err
				}
			}
		}
	}

	// Get incoming edges for upward traversal (contains, calls, references).
	// We intentionally exclude 'imports' edges: they represent file-level import
	// declarations (e.g. file:cache.ts imports class:Store), not code execution
	// paths. Following imports would pull every importer file into the impact
	// subgraph, which the TypeScript extractor never did because it only generates
	// import-node-level imports (import:xxx imports symbol), not file-level ones.
	//
	// Heuristic edges represent synthesized interface-dispatch relationships
	// (e.g. Reader::Get → Store::Get). They are not real call sites. Instead of
	// adding the heuristic source (an abstract interface method), we pass through
	// to its real callers so that the impact subgraph mirrors what the TypeScript
	// extractor (which doesn't generate abstract interface method nodes) would
	// produce.
	allIncoming, err := s.GetIncomingEdges(nodeID, []model.EdgeKind{
		model.EdgeContains, model.EdgeCalls, model.EdgeReferences,
	})
	if err != nil {
		return err
	}

	// Expand heuristic pass-throughs: replace each heuristic edge with the
	// non-heuristic callers of the heuristic source.
	type incomingItem struct {
		edge model.Edge
		node model.Node
	}
	var expandedItems []incomingItem

	for _, e := range allIncoming {
		if e.Provenance == "heuristic" {
			// Instead of adding the heuristic source, add its non-heuristic callers.
			callerEdges, err := s.GetIncomingEdges(e.Source, nil)
			if err != nil {
				return err
			}
			callerIDs := make([]string, 0, len(callerEdges))
			for _, ce := range callerEdges {
				if ce.Provenance != "heuristic" && ce.Kind != model.EdgeContains {
					callerIDs = append(callerIDs, ce.Source)
				}
			}
			if len(callerIDs) > 0 {
				callerMap, err := s.GetNodesByIDs(callerIDs)
				if err != nil {
					return err
				}
				for _, ce := range callerEdges {
					if ce.Provenance == "heuristic" || ce.Kind == model.EdgeContains {
						continue
					}
					n, ok := callerMap[ce.Source]
					if !ok {
						continue
					}
					// Use a synthetic edge for deduplication; record original.
					synth := model.Edge{
						Source:     n.ID,
						Target:     nodeID,
						Kind:       e.Kind,
						Provenance: "",
					}
					expandedItems = append(expandedItems, incomingItem{edge: synth, node: n})
				}
			}
			continue
		}
		// Regular (non-heuristic) edge: look up source node.
		srcMap, err := s.GetNodesByIDs([]string{e.Source})
		if err != nil {
			return err
		}
		n, ok := srcMap[e.Source]
		if !ok {
			continue
		}
		expandedItems = append(expandedItems, incomingItem{edge: e, node: n})
	}

	for _, item := range expandedItems {
		n := item.node
		if _, already := nodes[n.ID]; already {
			continue
		}
		nodes[n.ID] = n
		edges[edgeKey(item.edge)] = item.edge
		if err := impactRecursive(s, n.ID, maxDepth, currentDepth+1, nodes, edges, visited); err != nil {
			return err
		}
	}
	return nil
}

func edgeKey(e model.Edge) string {
	return e.Source + "|" + e.Target + "|" + string(e.Kind)
}

// -----------------------------------------------------------------------
// Symbol resolution
// -----------------------------------------------------------------------

// resolveSymbol finds all definitions matching symbol.
// Bare name → exact lookup by name (all definitions, generated files last).
// Qualified Receiver::name → FTS + suffix/path filter.
// Returns the matching nodes plus an optional note for multi-match.
func resolveSymbol(s *store.Store, symbol string) ([]model.Node, string, error) {
	// Qualified: contains :: or .
	sep := ""
	if strings.Contains(symbol, "::") {
		sep = "::"
	} else if strings.Contains(symbol, ".") {
		sep = "."
	}

	if sep != "" {
		parts := strings.SplitN(symbol, sep, 2)
		// receiver := parts[0] (unused for now — filter by qualified name)
		name := parts[len(parts)-1]

		// Try exact qualified_name match.
		nodes, err := s.GetNodesByQualifiedNameExact(symbol)
		if err != nil {
			return nil, "", err
		}
		if len(nodes) == 0 {
			// Fallback: match by exact name and filter qualified_name suffix.
			nodes, err = s.GetNodesByName(name)
			if err != nil {
				return nil, "", err
			}
			var filtered []model.Node
			suffix := sep + name
			for _, n := range nodes {
				if strings.HasSuffix(n.QualifiedName, suffix) || n.QualifiedName == symbol {
					filtered = append(filtered, n)
				}
			}
			if len(filtered) > 0 {
				nodes = filtered
			}
		}
		if len(nodes) == 0 {
			// Last resort: FTS search.
			results, err := s.SearchFTS(name, nil, nil, 20, 0)
			if err != nil {
				return nil, "", err
			}
			for _, r := range results {
				if strings.HasSuffix(r.Node.QualifiedName, sep+name) {
					nodes = append(nodes, r.Node)
				}
			}
		}
		note := ""
		if len(nodes) > 1 {
			note = buildNote(nodes)
		}
		return nodes, note, nil
	}

	// Bare name: exact lookup returns all definitions.
	nodes, err := s.GetNodesByName(symbol)
	if err != nil {
		return nil, "", err
	}
	if len(nodes) == 0 {
		// Case-insensitive fallback.
		nodes, err = s.GetNodesByLowerName(strings.ToLower(symbol))
		if err != nil {
			return nil, "", err
		}
	}

	// Filter out abstract interface method nodes when concrete implementations
	// also exist. The TypeScript extractor does not generate interface method
	// nodes, so they are absent from the TypeScript golden outputs. When a bare
	// name resolves to both an interface method and a concrete method, we drop
	// the interface method to match TypeScript parity.
	if len(nodes) > 1 {
		nodes, err = filterAbstractInterfaceMethods(s, nodes)
		if err != nil {
			return nil, "", err
		}
	}

	// Sort generated files last.
	sort.SliceStable(nodes, func(i, j int) bool {
		gi := IsGeneratedFile(nodes[i].FilePath)
		gj := IsGeneratedFile(nodes[j].FilePath)
		if gi == gj {
			return false
		}
		return !gi
	})

	note := ""
	if len(nodes) > 1 {
		note = buildNote(nodes)
	}
	return nodes, note, nil
}

// filterAbstractInterfaceMethods removes method nodes whose immediate container
// is an interface, when non-interface-method nodes are also present.
// This mirrors the TypeScript extractor's behaviour of not generating nodes for
// abstract interface methods.
func filterAbstractInterfaceMethods(s *store.Store, nodes []model.Node) ([]model.Node, error) {
	// Identify which nodes are abstract interface methods.
	abstract := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if n.Kind != model.KindMethod {
			continue
		}
		// Check incoming contains edges to find the parent container.
		parentEdges, err := s.GetIncomingEdges(n.ID, []model.EdgeKind{model.EdgeContains})
		if err != nil {
			return nodes, err // on error, don't filter
		}
		for _, pe := range parentEdges {
			parentMap, err := s.GetNodesByIDs([]string{pe.Source})
			if err != nil {
				continue
			}
			parent, ok := parentMap[pe.Source]
			if ok && parent.Kind == model.KindInterface {
				abstract[n.ID] = true
				break
			}
		}
	}
	if len(abstract) == 0 {
		return nodes, nil
	}
	// Only filter if at least one non-abstract node remains.
	var concrete []model.Node
	for _, n := range nodes {
		if !abstract[n.ID] {
			concrete = append(concrete, n)
		}
	}
	if len(concrete) == 0 {
		return nodes, nil // all are abstract; return all
	}
	return concrete, nil
}

func buildNote(nodes []model.Node) string {
	var parts []string
	for _, n := range nodes {
		parts = append(parts, n.QualifiedName+" ("+n.FilePath+")")
	}
	return "matched multiple definitions: " + strings.Join(parts, "; ")
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

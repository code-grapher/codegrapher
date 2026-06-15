package mcp

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/query"
	"github.com/specscore/codegrapher/store"
)

// StoreBackend implements GraphBackend over a *store.Store, delegating the
// search pipeline to the query package (the same thin-adapter pattern as
// internal/cli/store_querier.go) and porting the upstream GraphTraverser
// methods the MCP tools need with their exact iteration order.
type StoreBackend struct {
	st          *store.Store
	projectRoot string

	projectNameTokens map[string]struct{}
}

// NewStoreBackend creates a StoreBackend. projectRoot is the absolute path to
// the project root (the directory containing .codegraph/).
func NewStoreBackend(st *store.Store, projectRoot string) *StoreBackend {
	return &StoreBackend{st: st, projectRoot: projectRoot}
}

func (b *StoreBackend) GetProjectRoot() string { return b.projectRoot }

func (b *StoreBackend) GetStats() (GraphStats, error) {
	s, err := b.st.GetStats()
	if err != nil {
		return GraphStats{}, err
	}
	size, _ := b.st.Size()

	filesByLanguage := make(map[model.Language]int)
	files, err := b.st.GetAllFiles()
	if err != nil {
		return GraphStats{}, err
	}
	for _, f := range files {
		filesByLanguage[f.Language]++
	}

	return GraphStats{
		FileCount:       s.FileCount,
		NodeCount:       s.NodeCount,
		EdgeCount:       s.EdgeCount,
		NodesByKind:     s.NodesByKind,
		FilesByLanguage: filesByLanguage,
		DBSizeBytes:     size,
		JournalMode:     b.st.JournalMode(),
	}, nil
}

func (b *StoreBackend) GetFiles() ([]FileInfo, error) {
	files, err := b.st.GetAllFiles()
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, len(files))
	for i, f := range files {
		out[i] = FileInfo{Path: f.Path, Language: f.Language, NodeCount: f.NodeCount}
	}
	return out, nil
}

// SearchNodes delegates to the query package's full search pipeline
// (FTS5 → LIKE → fuzzy with exact-name supplement and multi-signal rescoring).
func (b *StoreBackend) SearchNodes(rawQuery string, kinds []model.NodeKind, limit int) ([]model.SearchResult, error) {
	return query.SearchNodes(b.st, rawQuery, query.SearchOptions{Limit: limit, Kinds: kinds})
}

func (b *StoreBackend) GetNodesByName(name string) ([]model.Node, error) {
	return b.st.GetNodesByName(name)
}

func (b *StoreBackend) GetNodeByID(id string) (*model.Node, error) {
	return b.st.GetNodeByID(id)
}

func (b *StoreBackend) GetNodesInFile(filePath string) ([]model.Node, error) {
	return b.st.GetNodesByFile(filePath)
}

func (b *StoreBackend) GetFileDependents(filePath string) ([]string, error) {
	return b.st.GetDependentFilePaths(filePath)
}

// callerCalleeEdgeKinds mirrors traversal.ts getCallers/getCallees.
var callerCalleeEdgeKinds = []model.EdgeKind{
	model.EdgeCalls, model.EdgeReferences, model.EdgeImports,
}

// GetCallers mirrors GraphTraverser.getCallers (depth 1, with edges).
func (b *StoreBackend) GetCallers(nodeID string) ([]NodeEdge, error) {
	edges, err := b.st.GetIncomingEdges(nodeID, callerCalleeEdgeKinds)
	if err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(edges))
	for _, e := range edges {
		ids = append(ids, e.Source)
	}
	nodesMap, err := b.st.GetNodesByIDs(ids)
	if err != nil {
		return nil, err
	}
	var out []NodeEdge
	seen := map[string]struct{}{nodeID: {}}
	for _, e := range edges {
		n, ok := nodesMap[e.Source]
		if !ok {
			continue
		}
		if _, dup := seen[n.ID]; dup {
			continue
		}
		seen[n.ID] = struct{}{}
		out = append(out, NodeEdge{Node: n, Edge: e})
	}
	return out, nil
}

// GetCallees mirrors GraphTraverser.getCallees (depth 1, with edges).
func (b *StoreBackend) GetCallees(nodeID string) ([]NodeEdge, error) {
	edges, err := b.st.GetOutgoingEdges(nodeID, callerCalleeEdgeKinds, "")
	if err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(edges))
	for _, e := range edges {
		ids = append(ids, e.Target)
	}
	nodesMap, err := b.st.GetNodesByIDs(ids)
	if err != nil {
		return nil, err
	}
	var out []NodeEdge
	seen := map[string]struct{}{nodeID: {}}
	for _, e := range edges {
		n, ok := nodesMap[e.Target]
		if !ok {
			continue
		}
		if _, dup := seen[n.ID]; dup {
			continue
		}
		seen[n.ID] = struct{}{}
		out = append(out, NodeEdge{Node: n, Edge: e})
	}
	return out, nil
}

// containerKinds mirrors the container set in traversal.ts getImpactRecursive.
var containerKinds = map[model.NodeKind]bool{
	model.KindClass: true, model.KindInterface: true, model.KindStruct: true,
	model.KindTrait: true, model.KindProtocol: true, model.KindModule: true,
	model.KindEnum: true,
}

// GetImpactRadius is a faithful, insertion-ordered port of
// GraphTraverser.getImpactRadius: container nodes expand their children at
// the same depth; incoming edges of all kinds except `contains` are followed
// upward; no provenance filtering. The same semantics as query.Impact's
// traversal, kept here because the MCP formatter depends on JS Map insertion
// order, which the query package's sorted output discards.
func (b *StoreBackend) GetImpactRadius(nodeID string, depth int) (*Subgraph, error) {
	sg := NewSubgraph()
	startNode, err := b.st.GetNodeByID(nodeID)
	if err != nil {
		return nil, err
	}
	if startNode == nil {
		return sg, nil
	}
	sg.Set(*startNode)
	sg.Roots = []string{nodeID}
	visited := make(map[string]struct{})
	if err := b.impactRecursive(nodeID, depth, 0, sg, visited); err != nil {
		return nil, err
	}
	return sg, nil
}

func (b *StoreBackend) impactRecursive(nodeID string, maxDepth, currentDepth int, sg *Subgraph, visited map[string]struct{}) error {
	if currentDepth >= maxDepth {
		return nil
	}
	if _, ok := visited[nodeID]; ok {
		return nil
	}
	visited[nodeID] = struct{}{}

	focal, err := b.st.GetNodeByID(nodeID)
	if err != nil {
		return err
	}
	if focal != nil && containerKinds[focal.Kind] {
		containsEdges, err := b.st.GetOutgoingEdges(nodeID, []model.EdgeKind{model.EdgeContains}, "")
		if err != nil {
			return err
		}
		if len(containsEdges) > 0 {
			childIDs := make([]string, 0, len(containsEdges))
			for _, e := range containsEdges {
				childIDs = append(childIDs, e.Target)
			}
			childMap, err := b.st.GetNodesByIDs(childIDs)
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
				sg.Set(child)
				sg.Edges = append(sg.Edges, e)
				if err := b.impactRecursive(child.ID, maxDepth, currentDepth, sg, visited); err != nil {
					return err
				}
			}
		}
	}

	allIncoming, err := b.st.GetIncomingEdges(nodeID, nil)
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
	sources, err := b.st.GetNodesByIDs(sourceIDs)
	if err != nil {
		return err
	}
	for _, e := range incoming {
		n, ok := sources[e.Source]
		if !ok {
			continue
		}
		if sg.Has(n.ID) {
			continue
		}
		sg.Set(n)
		sg.Edges = append(sg.Edges, e)
		if err := b.impactRecursive(n.ID, maxDepth, currentDepth+1, sg, visited); err != nil {
			return err
		}
	}
	return nil
}

// GetChildren mirrors GraphTraverser.getChildren: contains-children of nodeID.
func (b *StoreBackend) GetChildren(nodeID string) ([]model.Node, error) {
	edges, err := b.st.GetOutgoingEdges(nodeID, []model.EdgeKind{model.EdgeContains}, "")
	if err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(edges))
	for _, e := range edges {
		ids = append(ids, e.Target)
	}
	nodesMap, err := b.st.GetNodesByIDs(ids)
	if err != nil {
		return nil, err
	}
	var out []model.Node
	for _, e := range edges {
		if n, ok := nodesMap[e.Target]; ok {
			out = append(out, n)
		}
	}
	return out, nil
}

func (b *StoreBackend) GetOutgoingEdges(nodeID string, kinds []model.EdgeKind) ([]model.Edge, error) {
	return b.st.GetOutgoingEdges(nodeID, kinds, "")
}

func (b *StoreBackend) GetIncomingEdges(nodeID string, kinds []model.EdgeKind) ([]model.Edge, error) {
	return b.st.GetIncomingEdges(nodeID, kinds)
}

var hierarchyEdgeKinds = []model.EdgeKind{model.EdgeExtends, model.EdgeImplements}

// GetTypeHierarchy mirrors GraphTraverser.getTypeHierarchy.
func (b *StoreBackend) GetTypeHierarchy(nodeID string) (*Subgraph, error) {
	sg := NewSubgraph()
	focal, err := b.st.GetNodeByID(nodeID)
	if err != nil {
		return nil, err
	}
	if focal == nil {
		return sg, nil
	}
	sg.Set(*focal)
	sg.Roots = []string{nodeID}

	visitedUp := make(map[string]struct{})
	if err := b.typeAncestors(nodeID, sg, visitedUp); err != nil {
		return nil, err
	}
	visitedDown := make(map[string]struct{})
	if err := b.typeDescendants(nodeID, sg, visitedDown); err != nil {
		return nil, err
	}
	return sg, nil
}

func (b *StoreBackend) typeAncestors(nodeID string, sg *Subgraph, visited map[string]struct{}) error {
	if _, ok := visited[nodeID]; ok {
		return nil
	}
	visited[nodeID] = struct{}{}
	edges, err := b.st.GetOutgoingEdges(nodeID, hierarchyEdgeKinds, "")
	if err != nil {
		return err
	}
	if len(edges) == 0 {
		return nil
	}
	ids := make([]string, 0, len(edges))
	for _, e := range edges {
		ids = append(ids, e.Target)
	}
	parents, err := b.st.GetNodesByIDs(ids)
	if err != nil {
		return err
	}
	for _, e := range edges {
		parent, ok := parents[e.Target]
		if !ok || sg.Has(parent.ID) {
			continue
		}
		sg.Set(parent)
		sg.Edges = append(sg.Edges, e)
		if err := b.typeAncestors(parent.ID, sg, visited); err != nil {
			return err
		}
	}
	return nil
}

func (b *StoreBackend) typeDescendants(nodeID string, sg *Subgraph, visited map[string]struct{}) error {
	if _, ok := visited[nodeID]; ok {
		return nil
	}
	visited[nodeID] = struct{}{}
	edges, err := b.st.GetIncomingEdges(nodeID, hierarchyEdgeKinds)
	if err != nil {
		return err
	}
	if len(edges) == 0 {
		return nil
	}
	ids := make([]string, 0, len(edges))
	for _, e := range edges {
		ids = append(ids, e.Source)
	}
	children, err := b.st.GetNodesByIDs(ids)
	if err != nil {
		return err
	}
	for _, e := range edges {
		child, ok := children[e.Source]
		if !ok || sg.Has(child.ID) {
			continue
		}
		sg.Set(child)
		sg.Edges = append(sg.Edges, e)
		if err := b.typeDescendants(child.ID, sg, visited); err != nil {
			return err
		}
	}
	return nil
}

// TraverseBFS mirrors GraphTraverser.traverseBFS, including the structural-
// edge prioritisation (contains, then calls, then everything else).
func (b *StoreBackend) TraverseBFS(startID string, opts TraversalOptions) (*Subgraph, error) {
	sg := NewSubgraph()
	startNode, err := b.st.GetNodeByID(startID)
	if err != nil {
		return nil, err
	}
	if startNode == nil {
		return sg, nil
	}

	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = int(^uint(0) >> 1) // Infinity
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}
	direction := opts.Direction
	if direction == "" {
		direction = "outgoing"
	}
	nodeKinds := make(map[model.NodeKind]bool, len(opts.NodeKinds))
	for _, k := range opts.NodeKinds {
		nodeKinds[k] = true
	}

	type step struct {
		node  model.Node
		edge  *model.Edge
		depth int
	}

	visited := make(map[string]struct{})
	queue := []step{{node: *startNode, depth: 0}}
	sg.Set(*startNode)
	sg.Roots = []string{startID}

	for len(queue) > 0 && sg.Len() < limit {
		s := queue[0]
		queue = queue[1:]

		if _, ok := visited[s.node.ID]; ok {
			continue
		}
		visited[s.node.ID] = struct{}{}

		if s.edge != nil {
			sg.Edges = append(sg.Edges, *s.edge)
		}
		if s.depth >= maxDepth {
			continue
		}

		adjacent, err := b.adjacentEdges(s.node.ID, direction, opts.EdgeKinds)
		if err != nil {
			return nil, err
		}
		// Stable sort: contains first, then calls, then the rest.
		prio := func(e model.Edge) int {
			switch e.Kind {
			case model.EdgeContains:
				return 0
			case model.EdgeCalls:
				return 1
			default:
				return 2
			}
		}
		stableSortEdges(adjacent, prio)

		wantIDs := make([]string, 0, len(adjacent))
		for _, e := range adjacent {
			next := e.Target
			if e.Source != s.node.ID {
				next = e.Source
			}
			if _, ok := visited[next]; !ok {
				wantIDs = append(wantIDs, next)
			}
		}
		var neighbors map[string]model.Node
		if len(wantIDs) > 0 {
			neighbors, err = b.st.GetNodesByIDs(wantIDs)
			if err != nil {
				return nil, err
			}
		}

		for _, e := range adjacent {
			nextID := e.Target
			if e.Source != s.node.ID {
				nextID = e.Source
			}
			if _, ok := visited[nextID]; ok {
				continue
			}
			next, ok := neighbors[nextID]
			if !ok {
				continue
			}
			if len(opts.NodeKinds) > 0 && !nodeKinds[next.Kind] {
				continue
			}
			sg.Set(next)
			edge := e
			queue = append(queue, step{node: next, edge: &edge, depth: s.depth + 1})
		}
	}

	return sg, nil
}

func (b *StoreBackend) adjacentEdges(nodeID, direction string, kinds []model.EdgeKind) ([]model.Edge, error) {
	switch direction {
	case "outgoing":
		return b.st.GetOutgoingEdges(nodeID, kinds, "")
	case "incoming":
		return b.st.GetIncomingEdges(nodeID, kinds)
	default:
		out, err := b.st.GetOutgoingEdges(nodeID, kinds, "")
		if err != nil {
			return nil, err
		}
		in, err := b.st.GetIncomingEdges(nodeID, kinds)
		if err != nil {
			return nil, err
		}
		return append(out, in...), nil
	}
}

// stableSortEdges sorts edges by priority, preserving input order within a
// priority tier (JS Array.sort is stable).
func stableSortEdges(edges []model.Edge, prio func(model.Edge) int) {
	// Insertion sort keeps it dependency-free and stable; adjacency lists
	// are small.
	for i := 1; i < len(edges); i++ {
		for j := i; j > 0 && prio(edges[j]) < prio(edges[j-1]); j-- {
			edges[j], edges[j-1] = edges[j-1], edges[j]
		}
	}
}

// FindNodesByExactName mirrors QueryBuilder.findNodesByExactName: a two-pass
// case-insensitive exact-name lookup with co-location boosting.
func (b *StoreBackend) FindNodesByExactName(names []string, kinds []model.NodeKind, limit int) ([]model.SearchResult, error) {
	if len(names) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	// Pass 1: which files contain each queried name; distinctive names are
	// those with fewer than 10 file matches.
	distinctiveFiles := make(map[string]struct{})
	for _, name := range names {
		rows, err := b.st.ExactNameCaseInsensitive(name, kinds, nil, 100)
		if err != nil {
			return nil, err
		}
		files := make(map[string]struct{})
		for _, n := range rows {
			files[n.FilePath] = struct{}{}
		}
		if len(files) > 0 && len(files) < 10 {
			for f := range files {
				distinctiveFiles[f] = struct{}{}
			}
		}
	}

	// Pass 2: per-name query with co-location scoring.
	perNameLimit := max(
		// ceil
		(limit+len(names)-1)/len(names), 8)
	fetch := max(perNameLimit*3, 50)

	var allResults []model.SearchResult
	seenIDs := make(map[string]struct{})
	for _, name := range names {
		rows, err := b.st.ExactNameCaseInsensitive(name, kinds, nil, fetch)
		if err != nil {
			return nil, err
		}
		var nameResults []model.SearchResult
		for _, n := range rows {
			if _, dup := seenIDs[n.ID]; dup {
				continue
			}
			score := 1.0
			if _, ok := distinctiveFiles[n.FilePath]; ok {
				score += 20
			}
			nameResults = append(nameResults, model.SearchResult{Node: n, Score: score})
		}
		stableSortByScoreDesc(nameResults)
		if len(nameResults) > perNameLimit {
			nameResults = nameResults[:perNameLimit]
		}
		for _, r := range nameResults {
			seenIDs[r.Node.ID] = struct{}{}
			allResults = append(allResults, r)
		}
	}

	stableSortByScoreDesc(allResults)
	if len(allResults) > limit {
		allResults = allResults[:limit]
	}
	return allResults, nil
}

// FindNodesByNameSubstring mirrors QueryBuilder.findNodesByNameSubstring:
// plain `name LIKE %sub%` ordered by name length. Implemented with a raw
// read-only query through Store.Transaction (the store's public API exposes
// no equivalent; SearchLike scores and matches qualified names too).
func (b *StoreBackend) FindNodesByNameSubstring(substring string, kinds []model.NodeKind, limit int, excludePrefix bool) ([]model.SearchResult, error) {
	if limit <= 0 {
		limit = 30
	}
	q := `SELECT id FROM nodes WHERE name LIKE ?`
	args := []any{"%" + substring + "%"}
	if excludePrefix {
		q += ` AND name NOT LIKE ?`
		args = append(args, substring+"%")
	}
	if len(kinds) > 0 {
		q += ` AND kind IN (` + sqlPlaceholders(len(kinds)) + `)`
		for _, k := range kinds {
			args = append(args, string(k))
		}
	}
	q += ` ORDER BY length(name) ASC LIMIT ?`
	args = append(args, limit)

	var ids []string
	err := b.st.Transaction(func(tx *sql.Tx) error {
		rows, err := tx.Query(q, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	nodesMap, err := b.st.GetNodesByIDs(ids)
	if err != nil {
		return nil, err
	}
	out := make([]model.SearchResult, 0, len(ids))
	for _, id := range ids {
		if n, ok := nodesMap[id]; ok {
			out = append(out, model.SearchResult{Node: n, Score: 1.0})
		}
	}
	return out, nil
}

// GetDominantFile mirrors QueryBuilder.getDominantFile: the file holding the
// densest concentration of in-file edges, excluding test/generated files.
func (b *StoreBackend) GetDominantFile() (*DominantFile, error) {
	type row struct {
		filePath  string
		edgeCount int
	}
	var rows []row
	err := b.st.Transaction(func(tx *sql.Tx) error {
		r, err := tx.Query(`
			SELECT n.file_path AS file_path, COUNT(*) AS edge_count
			FROM edges e
			JOIN nodes n ON e.source = n.id
			JOIN nodes m ON e.target = m.id
			WHERE n.file_path = m.file_path
			GROUP BY n.file_path
			ORDER BY edge_count DESC
			LIMIT 20
		`)
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		for r.Next() {
			var x row
			if err := r.Scan(&x.filePath, &x.edgeCount); err != nil {
				return err
			}
			rows = append(rows, x)
		}
		return r.Err()
	})
	if err != nil {
		return nil, err
	}
	filtered := rows[:0:0]
	for _, r := range rows {
		if !isLowValueDBFile(r.filePath, query.IsGeneratedFile) {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 || filtered[0].edgeCount < 20 {
		return nil, nil
	}
	next := 0
	if len(filtered) > 1 {
		next = filtered[1].edgeCount
	}
	return &DominantFile{
		FilePath:      filtered[0].filePath,
		EdgeCount:     filtered[0].edgeCount,
		NextEdgeCount: next,
	}, nil
}

func (b *StoreBackend) FindEdgesBetweenNodes(nodeIDs []string, kinds []model.EdgeKind) ([]model.Edge, error) {
	return b.st.FindEdgesBetweenNodes(nodeIDs, kinds)
}

// GetProjectNameTokens mirrors CodeGraph.getProjectNameTokens (memoized).
func (b *StoreBackend) GetProjectNameTokens() map[string]struct{} {
	if b.projectNameTokens == nil {
		b.projectNameTokens = deriveProjectNameTokens(b.projectRoot)
	}
	return b.projectNameTokens
}

// GetCode mirrors ContextBuilder.getCode/extractNodeCode: the node's source
// lines [startLine, endLine] read from disk. Config-leaf nodes return their
// key only (#383). Returns "" when the node or file is unavailable.
func (b *StoreBackend) GetCode(nodeID string) (string, error) {
	n, err := b.st.GetNodeByID(nodeID)
	if err != nil || n == nil {
		return "", err
	}
	if isConfigLeafNode(*n) {
		if n.Signature != "" {
			return n.Signature, nil
		}
		if n.QualifiedName != "" {
			return n.QualifiedName, nil
		}
		return n.Name, nil
	}
	abs := validatePathWithinRoot(b.projectRoot, n.FilePath)
	if abs == "" {
		return "", nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", nil
	}
	lines := strings.Split(string(data), "\n")
	startIdx := max(n.StartLine-1, 0)
	endIdx := min(n.EndLine, len(lines))
	if endIdx <= startIdx {
		return "", nil
	}
	return strings.Join(lines[startIdx:endIdx], "\n"), nil
}

// configLeafLanguages mirrors CONFIG_LEAF_LANGUAGES in src/utils.ts.
var configLeafLanguages = map[model.Language]bool{"yaml": true, "properties": true}

// isConfigLeafNode mirrors isConfigLeafNode in src/utils.ts.
func isConfigLeafNode(n model.Node) bool {
	return n.Kind == model.KindConstant && configLeafLanguages[n.Language]
}

// validatePathWithinRoot resolves relPath under root and rejects traversal
// outside it (the Go analogue of validatePathWithinRoot in src/utils.ts).
// Returns "" when the path escapes the root.
func validatePathWithinRoot(root, relPath string) string {
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	cleanRoot := filepath.Clean(root)
	if abs != cleanRoot && !strings.HasPrefix(abs, cleanRoot+string(filepath.Separator)) {
		return ""
	}
	return abs
}

func sqlPlaceholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func stableSortByScoreDesc(results []model.SearchResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

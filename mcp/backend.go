package mcp

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// StoreBackend implements GraphBackend over a *store.Store.
type StoreBackend struct {
	st          *store.Store
	projectRoot string
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
	return GraphStats{
		FileCount:   s.FileCount,
		NodeCount:   s.NodeCount,
		EdgeCount:   s.EdgeCount,
		NodesByKind: s.NodesByKind,
		DBSizeBytes: size,
		JournalMode: b.st.JournalMode(),
	}, nil
}

func (b *StoreBackend) GetFiles() ([]FileInfo, error) {
	files, err := b.st.GetAllFiles()
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, len(files))
	for i, f := range files {
		out[i] = FileInfo{
			Path:      f.Path,
			Language:  f.Language,
			NodeCount: f.NodeCount,
		}
	}
	return out, nil
}

func (b *StoreBackend) SearchNodes(query string, kinds []string, limit int) ([]SearchResult, error) {
	var nodeKinds []model.NodeKind
	for _, k := range kinds {
		nodeKinds = append(nodeKinds, model.NodeKind(k))
	}

	// Try FTS first.
	results, err := b.st.SearchFTS(query, nodeKinds, nil, limit, 0)
	if err != nil {
		return nil, err
	}

	// Fallback to LIKE if FTS returned nothing.
	if len(results) == 0 {
		results, err = b.st.SearchLike(query, nodeKinds, nil, limit, 0)
		if err != nil {
			return nil, err
		}
	}

	// Cap to limit.
	if len(results) > limit {
		results = results[:limit]
	}

	out := make([]SearchResult, len(results))
	for i, r := range results {
		out[i] = SearchResult{Node: nodeFromModel(r.Node), Score: r.Score}
	}
	return out, nil
}

func (b *StoreBackend) GetNodesByName(name string) ([]NodeInfo, error) {
	nodes, err := b.st.GetNodesByName(name)
	if err != nil {
		return nil, err
	}
	// Also try case-insensitive if exact match returns nothing.
	if len(nodes) == 0 {
		nodes, err = b.st.GetNodesByLowerName(lowercase(name))
		if err != nil {
			return nil, err
		}
	}
	out := make([]NodeInfo, len(nodes))
	for i, n := range nodes {
		out[i] = nodeFromModel(n)
	}
	return out, nil
}

func (b *StoreBackend) GetCallers(nodeID string) ([]NodeInfo, error) {
	edges, err := b.st.GetIncomingEdges(nodeID, []model.EdgeKind{model.EdgeCalls})
	if err != nil {
		return nil, err
	}
	var out []NodeInfo
	seen := make(map[string]struct{})
	for _, e := range edges {
		if _, ok := seen[e.Source]; ok {
			continue
		}
		seen[e.Source] = struct{}{}
		n, err := b.st.GetNodeByID(e.Source)
		if err != nil || n == nil {
			continue
		}
		out = append(out, nodeFromModel(*n))
	}
	return out, nil
}

func (b *StoreBackend) GetCallees(nodeID string) ([]NodeInfo, error) {
	edges, err := b.st.GetOutgoingEdges(nodeID, []model.EdgeKind{model.EdgeCalls}, "")
	if err != nil {
		return nil, err
	}
	var out []NodeInfo
	seen := make(map[string]struct{})
	for _, e := range edges {
		if _, ok := seen[e.Target]; ok {
			continue
		}
		seen[e.Target] = struct{}{}
		n, err := b.st.GetNodeByID(e.Target)
		if err != nil || n == nil {
			continue
		}
		out = append(out, nodeFromModel(*n))
	}
	return out, nil
}

func (b *StoreBackend) GetImpact(nodeID string, depth int) ([]ImpactEntry, error) {
	// BFS over callers up to depth.
	var out []ImpactEntry
	visited := map[string]struct{}{nodeID: {}}
	frontier := []string{nodeID}

	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		for _, id := range frontier {
			edges, err := b.st.GetIncomingEdges(id, []model.EdgeKind{model.EdgeCalls})
			if err != nil {
				continue
			}
			for _, e := range edges {
				if _, ok := visited[e.Source]; ok {
					continue
				}
				visited[e.Source] = struct{}{}
				n, err := b.st.GetNodeByID(e.Source)
				if err != nil || n == nil {
					continue
				}
				out = append(out, ImpactEntry{Node: nodeFromModel(*n), Depth: d})
				next = append(next, e.Source)
			}
		}
		frontier = next
	}
	return out, nil
}

func (b *StoreBackend) GetNodeByID(id string) (*NodeInfo, error) {
	n, err := b.st.GetNodeByID(id)
	if err != nil || n == nil {
		return nil, err
	}
	info := nodeFromModel(*n)
	return &info, nil
}

func (b *StoreBackend) GetNodesByFile(filePath string) ([]NodeInfo, error) {
	nodes, err := b.st.GetNodesByFile(filePath)
	if err != nil {
		return nil, err
	}
	out := make([]NodeInfo, len(nodes))
	for i, n := range nodes {
		out[i] = nodeFromModel(n)
	}
	return out, nil
}

func (b *StoreBackend) GetFileDependents(filePath string) ([]string, error) {
	return b.st.GetDependentFilePaths(filePath)
}

func (b *StoreBackend) ReadFile(relPath string) (string, error) {
	abs := filepath.Join(b.projectRoot, relPath)
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("mcp: read file %s: %w", relPath, err)
	}
	return string(data), nil
}

func (b *StoreBackend) FindRelevantContext(query string, maxNodes int) (*Subgraph, error) {
	// Use FTS search to find nodes relevant to the query.
	results, err := b.st.SearchFTS(query, nil, nil, maxNodes, 0)
	if err != nil || len(results) == 0 {
		// Fallback to LIKE
		results, err = b.st.SearchLike(query, nil, nil, maxNodes, 0)
		if err != nil {
			return nil, err
		}
	}

	sg := &Subgraph{
		FileSource: make(map[string]string),
		FileNodes:  make(map[string][]NodeInfo),
	}

	seen := make(map[string]struct{})
	for _, r := range results {
		n := nodeFromModel(r.Node)
		if _, ok := seen[n.ID]; ok {
			continue
		}
		seen[n.ID] = struct{}{}
		sg.Nodes = append(sg.Nodes, n)
		sg.FileNodes[n.FilePath] = append(sg.FileNodes[n.FilePath], n)
	}

	// Also expand callers/callees of top results to get more context.
	topLimit := 5
	if len(results) < topLimit {
		topLimit = len(results)
	}
	for _, r := range results[:topLimit] {
		for _, dir := range []string{"callers", "callees"} {
			var edges []model.Edge
			var err error
			if dir == "callers" {
				edges, err = b.st.GetIncomingEdges(r.Node.ID, []model.EdgeKind{model.EdgeCalls})
			} else {
				edges, err = b.st.GetOutgoingEdges(r.Node.ID, []model.EdgeKind{model.EdgeCalls}, "")
			}
			if err != nil {
				continue
			}
			for _, e := range edges {
				targetID := e.Source
				if dir == "callees" {
					targetID = e.Target
				}
				if _, ok := seen[targetID]; ok {
					continue
				}
				seen[targetID] = struct{}{}
				n, err := b.st.GetNodeByID(targetID)
				if err != nil || n == nil {
					continue
				}
				info := nodeFromModel(*n)
				sg.Nodes = append(sg.Nodes, info)
				sg.FileNodes[info.FilePath] = append(sg.FileNodes[info.FilePath], info)
				if len(sg.Nodes) >= maxNodes {
					goto done
				}
			}
		}
	}
done:

	// Load source for each file.
	for fp := range sg.FileNodes {
		src, err := b.ReadFile(fp)
		if err == nil {
			sg.FileSource[fp] = src
		}
	}

	return sg, nil
}

func lowercase(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

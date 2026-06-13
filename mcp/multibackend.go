package mcp

import (
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
)

// MultiBackend implements GraphBackend by fanning out each operation across a
// slice of per-scope StoreBackends and merging the results. The data model is
// one SQLite DB per (language, version) scope; every live read is single-scope
// (no cross-DB JOINs), so whole-repo answers come from running each query per
// scope and merging in Go.
//
// Merge strategy by method category:
//   - Search/list (SearchNodes, FindNodesByExactName, FindNodesByNameSubstring,
//     GetNodesByName, GetFiles): run on each backend, concatenate, de-duplicate
//     by stable node identity (node ID; file path for files), then re-apply the
//     single-store sort/limit so the merged output matches single-store shape.
//   - Lookup-by-id (GetNodeByID, GetCode): return the first backend that
//     resolves the id (node IDs are file-path-derived, so unique to one scope).
//   - Traversal rooted at an id (GetCallers, GetCallees, GetChildren,
//     GetImpactRadius, GetTypeHierarchy, TraverseBFS, GetOutgoingEdges,
//     GetIncomingEdges, GetNodesInFile, GetFileDependents, FindEdgesBetweenNodes):
//     each scope is self-contained for its own nodes/edges, so non-owning scopes
//     return empty and the owning scope's result is what surfaces. Subgraph
//     methods return the owning scope's subgraph directly to preserve the
//     insertion order the explore formatter depends on.
//   - status/stats (GetStats): sum counts and union the language/kind maps.
//   - GetDominantFile: the densest file across all scopes.
//   - GetProjectNameTokens / GetProjectRoot: scope-independent (derived from the
//     shared project root); taken from the first backend.
//
// For a single backend, every method delegates to behavior identical to today.
type MultiBackend struct {
	backends    []*StoreBackend
	projectRoot string
}

// NewMultiBackend wraps each store in a StoreBackend and fans out across them.
// Compile-time check: MultiBackend must satisfy the same seam StoreBackend does.
var _ GraphBackend = (*MultiBackend)(nil)

func NewMultiBackend(stores []*store.Store, projectRoot string) *MultiBackend {
	backends := make([]*StoreBackend, len(stores))
	for i, s := range stores {
		backends[i] = NewStoreBackend(s, projectRoot)
	}
	return &MultiBackend{backends: backends, projectRoot: projectRoot}
}

func (m *MultiBackend) GetProjectRoot() string { return m.projectRoot }

func (m *MultiBackend) GetStats() (GraphStats, error) {
	var out GraphStats
	out.NodesByKind = map[model.NodeKind]int{}
	out.FilesByLanguage = map[model.Language]int{}
	for _, b := range m.backends {
		s, err := b.GetStats()
		if err != nil {
			return GraphStats{}, err
		}
		out.FileCount += s.FileCount
		out.NodeCount += s.NodeCount
		out.EdgeCount += s.EdgeCount
		out.DBSizeBytes += s.DBSizeBytes
		for k, v := range s.NodesByKind {
			out.NodesByKind[k] += v
		}
		for l, v := range s.FilesByLanguage {
			out.FilesByLanguage[l] += v
		}
		// Journal mode is uniform across scope DBs; surface the first.
		if out.JournalMode == "" {
			out.JournalMode = s.JournalMode
		}
	}
	return out, nil
}

func (m *MultiBackend) GetFiles() ([]FileInfo, error) {
	var out []FileInfo
	seen := make(map[string]struct{})
	for _, b := range m.backends {
		files, err := b.GetFiles()
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if _, dup := seen[f.Path]; dup {
				continue
			}
			seen[f.Path] = struct{}{}
			out = append(out, f)
		}
	}
	return out, nil
}

func (m *MultiBackend) SearchNodes(query string, kinds []model.NodeKind, limit int) ([]model.SearchResult, error) {
	var all []model.SearchResult
	for _, b := range m.backends {
		res, err := b.SearchNodes(query, kinds, limit)
		if err != nil {
			return nil, err
		}
		all = append(all, res...)
	}
	return mergeSearchResults(all, limit), nil
}

func (m *MultiBackend) GetNodesByName(name string) ([]model.Node, error) {
	var all []model.Node
	seen := make(map[string]struct{})
	for _, b := range m.backends {
		nodes, err := b.GetNodesByName(name)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if _, dup := seen[n.ID]; dup {
				continue
			}
			seen[n.ID] = struct{}{}
			all = append(all, n)
		}
	}
	return all, nil
}

func (m *MultiBackend) GetNodeByID(id string) (*model.Node, error) {
	for _, b := range m.backends {
		n, err := b.GetNodeByID(id)
		if err != nil {
			return nil, err
		}
		if n != nil {
			return n, nil
		}
	}
	return nil, nil
}

func (m *MultiBackend) GetNodesInFile(filePath string) ([]model.Node, error) {
	var all []model.Node
	seen := make(map[string]struct{})
	for _, b := range m.backends {
		nodes, err := b.GetNodesInFile(filePath)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if _, dup := seen[n.ID]; dup {
				continue
			}
			seen[n.ID] = struct{}{}
			all = append(all, n)
		}
	}
	return all, nil
}

func (m *MultiBackend) GetFileDependents(filePath string) ([]string, error) {
	var all []string
	seen := make(map[string]struct{})
	for _, b := range m.backends {
		deps, err := b.GetFileDependents(filePath)
		if err != nil {
			return nil, err
		}
		for _, d := range deps {
			if _, dup := seen[d]; dup {
				continue
			}
			seen[d] = struct{}{}
			all = append(all, d)
		}
	}
	return all, nil
}

func (m *MultiBackend) GetCallers(nodeID string) ([]NodeEdge, error) {
	return m.mergeNodeEdges(nodeID, (*StoreBackend).GetCallers)
}

func (m *MultiBackend) GetCallees(nodeID string) ([]NodeEdge, error) {
	return m.mergeNodeEdges(nodeID, (*StoreBackend).GetCallees)
}

func (m *MultiBackend) mergeNodeEdges(nodeID string, fn func(*StoreBackend, string) ([]NodeEdge, error)) ([]NodeEdge, error) {
	var all []NodeEdge
	seen := make(map[string]struct{})
	for _, b := range m.backends {
		res, err := fn(b, nodeID)
		if err != nil {
			return nil, err
		}
		for _, ne := range res {
			if _, dup := seen[ne.Node.ID]; dup {
				continue
			}
			seen[ne.Node.ID] = struct{}{}
			all = append(all, ne)
		}
	}
	return all, nil
}

func (m *MultiBackend) GetImpactRadius(nodeID string, depth int) (*Subgraph, error) {
	return m.owningSubgraph(nodeID, func(b *StoreBackend) (*Subgraph, error) {
		return b.GetImpactRadius(nodeID, depth)
	})
}

func (m *MultiBackend) GetTypeHierarchy(nodeID string) (*Subgraph, error) {
	return m.owningSubgraph(nodeID, func(b *StoreBackend) (*Subgraph, error) {
		return b.GetTypeHierarchy(nodeID)
	})
}

func (m *MultiBackend) TraverseBFS(startID string, opts TraversalOptions) (*Subgraph, error) {
	return m.owningSubgraph(startID, func(b *StoreBackend) (*Subgraph, error) {
		return b.TraverseBFS(startID, opts)
	})
}

// owningSubgraph returns the subgraph from the scope that owns startID,
// preserving that scope's exact insertion order. A node ID is file-path-derived
// and therefore lives in exactly one scope; non-owning scopes would only return
// a degenerate (empty or start-node-only) subgraph. When no scope owns it, the
// first backend's (empty) result is returned to match single-store behavior.
func (m *MultiBackend) owningSubgraph(startID string, fn func(*StoreBackend) (*Subgraph, error)) (*Subgraph, error) {
	for _, b := range m.backends {
		n, err := b.GetNodeByID(startID)
		if err != nil {
			return nil, err
		}
		if n != nil {
			return fn(b)
		}
	}
	if len(m.backends) > 0 {
		return fn(m.backends[0])
	}
	return NewSubgraph(), nil
}

func (m *MultiBackend) GetChildren(nodeID string) ([]model.Node, error) {
	var all []model.Node
	seen := make(map[string]struct{})
	for _, b := range m.backends {
		nodes, err := b.GetChildren(nodeID)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if _, dup := seen[n.ID]; dup {
				continue
			}
			seen[n.ID] = struct{}{}
			all = append(all, n)
		}
	}
	return all, nil
}

func (m *MultiBackend) GetOutgoingEdges(nodeID string, kinds []model.EdgeKind) ([]model.Edge, error) {
	return m.mergeEdges(kinds, func(b *StoreBackend, k []model.EdgeKind) ([]model.Edge, error) {
		return b.GetOutgoingEdges(nodeID, k)
	})
}

func (m *MultiBackend) GetIncomingEdges(nodeID string, kinds []model.EdgeKind) ([]model.Edge, error) {
	return m.mergeEdges(kinds, func(b *StoreBackend, k []model.EdgeKind) ([]model.Edge, error) {
		return b.GetIncomingEdges(nodeID, k)
	})
}

func (m *MultiBackend) FindEdgesBetweenNodes(nodeIDs []string, kinds []model.EdgeKind) ([]model.Edge, error) {
	return m.mergeEdges(kinds, func(b *StoreBackend, k []model.EdgeKind) ([]model.Edge, error) {
		return b.FindEdgesBetweenNodes(nodeIDs, k)
	})
}

func (m *MultiBackend) mergeEdges(kinds []model.EdgeKind, fn func(*StoreBackend, []model.EdgeKind) ([]model.Edge, error)) ([]model.Edge, error) {
	var all []model.Edge
	seen := make(map[string]struct{})
	for _, b := range m.backends {
		edges, err := fn(b, kinds)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			k := edgeKey(e)
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			all = append(all, e)
		}
	}
	return all, nil
}

func (m *MultiBackend) FindNodesByExactName(names []string, kinds []model.NodeKind, limit int) ([]model.SearchResult, error) {
	var all []model.SearchResult
	for _, b := range m.backends {
		res, err := b.FindNodesByExactName(names, kinds, limit)
		if err != nil {
			return nil, err
		}
		all = append(all, res...)
	}
	return mergeSearchResults(all, limit), nil
}

func (m *MultiBackend) FindNodesByNameSubstring(substring string, kinds []model.NodeKind, limit int, excludePrefix bool) ([]model.SearchResult, error) {
	var all []model.SearchResult
	for _, b := range m.backends {
		res, err := b.FindNodesByNameSubstring(substring, kinds, limit, excludePrefix)
		if err != nil {
			return nil, err
		}
		all = append(all, res...)
	}
	return mergeSearchResults(all, limit), nil
}

func (m *MultiBackend) GetDominantFile() (*DominantFile, error) {
	var best *DominantFile
	for _, b := range m.backends {
		df, err := b.GetDominantFile()
		if err != nil {
			return nil, err
		}
		if df == nil {
			continue
		}
		if best == nil || df.EdgeCount > best.EdgeCount {
			best = df
		}
	}
	return best, nil
}

func (m *MultiBackend) GetProjectNameTokens() map[string]struct{} {
	if len(m.backends) > 0 {
		return m.backends[0].GetProjectNameTokens()
	}
	return deriveProjectNameTokens(m.projectRoot)
}

func (m *MultiBackend) GetCode(nodeID string) (string, error) {
	for _, b := range m.backends {
		n, err := b.GetNodeByID(nodeID)
		if err != nil {
			return "", err
		}
		if n != nil {
			return b.GetCode(nodeID)
		}
	}
	return "", nil
}

// mergeSearchResults de-duplicates by node ID (first occurrence wins), re-sorts
// by score descending (stable, mirroring the single-store stableSortByScoreDesc),
// and re-applies the limit so the merged shape matches a single-store result.
func mergeSearchResults(results []model.SearchResult, limit int) []model.SearchResult {
	seen := make(map[string]struct{}, len(results))
	deduped := results[:0:0]
	for _, r := range results {
		if _, dup := seen[r.Node.ID]; dup {
			continue
		}
		seen[r.Node.ID] = struct{}{}
		deduped = append(deduped, r)
	}
	stableSortByScoreDesc(deduped)
	if limit > 0 && len(deduped) > limit {
		deduped = deduped[:limit]
	}
	return deduped
}

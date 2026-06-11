package mcp

import (
	"fmt"

	"github.com/specscore/codegrapher/model"
)

// mockBackend is a simple in-memory GraphBackend for tests.
type mockBackend struct {
	projectRoot string
	stats       GraphStats
	files       []FileInfo
	nodesByName map[string][]NodeInfo
	nodesByID   map[string]NodeInfo
	callers     map[string][]NodeInfo
	callees     map[string][]NodeInfo
	sources     map[string]string // filePath -> source
}

func newMockBackend() *mockBackend {
	return &mockBackend{
		projectRoot: "/tmp/test-project",
		stats: GraphStats{
			FileCount:   3,
			NodeCount:   10,
			EdgeCount:   15,
			NodesByKind: map[model.NodeKind]int{model.KindFunction: 5, model.KindStruct: 2},
			DBSizeBytes: 1024 * 100,
			JournalMode: "wal",
		},
		files: []FileInfo{
			{Path: "main.go", Language: "go", NodeCount: 3},
			{Path: "store/store.go", Language: "go", NodeCount: 5},
			{Path: "store/cache.go", Language: "go", NodeCount: 2},
		},
		nodesByName: map[string][]NodeInfo{
			"Get": {
				{ID: "get1", Kind: model.KindMethod, Name: "Get", QualifiedName: "Store.Get",
					FilePath: "store/store.go", Language: "go", StartLine: 10, EndLine: 15,
					Signature: "(key string) (string, error)"},
			},
			"Set": {
				{ID: "set1", Kind: model.KindMethod, Name: "Set", QualifiedName: "Store.Set",
					FilePath: "store/store.go", Language: "go", StartLine: 17, EndLine: 22},
			},
		},
		nodesByID: map[string]NodeInfo{
			"get1": {ID: "get1", Kind: model.KindMethod, Name: "Get", QualifiedName: "Store.Get",
				FilePath: "store/store.go", Language: "go", StartLine: 10, EndLine: 15},
			"caller1": {ID: "caller1", Kind: model.KindMethod, Name: "Lookup", QualifiedName: "Cache.Lookup",
				FilePath: "store/cache.go", Language: "go", StartLine: 5, EndLine: 10},
			"set1": {ID: "set1", Kind: model.KindMethod, Name: "Set", QualifiedName: "Store.Set",
				FilePath: "store/store.go", Language: "go", StartLine: 17, EndLine: 22},
		},
		callers: map[string][]NodeInfo{
			"get1": {{ID: "caller1", Kind: model.KindMethod, Name: "Lookup",
				FilePath: "store/cache.go", Language: "go", StartLine: 5}},
		},
		callees: map[string][]NodeInfo{},
		sources: map[string]string{
			"store/store.go": `package store

type Store struct {
	items map[string]string
}

// New creates a new Store.
func New() *Store {
	return &Store{items: make(map[string]string)}
}

// Get returns value for key.
func (s *Store) Get(key string) (string, error) {
	v, ok := s.items[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set stores value.
func (s *Store) Set(key, value string) {
	s.items[key] = value
}
`,
		},
	}
}

func (m *mockBackend) GetProjectRoot() string { return m.projectRoot }
func (m *mockBackend) GetStats() (GraphStats, error) {
	return m.stats, nil
}
func (m *mockBackend) GetFiles() ([]FileInfo, error) { return m.files, nil }

func (m *mockBackend) SearchNodes(query string, kinds []string, limit int) ([]SearchResult, error) {
	var out []SearchResult
	for name, nodes := range m.nodesByName {
		if contains(name, query) {
			for _, n := range nodes {
				out = append(out, SearchResult{Node: n, Score: 1.0})
				if len(out) >= limit {
					return out, nil
				}
			}
		}
	}
	return out, nil
}

func (m *mockBackend) GetNodesByName(name string) ([]NodeInfo, error) {
	return m.nodesByName[name], nil
}

func (m *mockBackend) GetCallers(nodeID string) ([]NodeInfo, error) {
	return m.callers[nodeID], nil
}

func (m *mockBackend) GetCallees(nodeID string) ([]NodeInfo, error) {
	return m.callees[nodeID], nil
}

func (m *mockBackend) GetImpact(nodeID string, depth int) ([]ImpactEntry, error) {
	callers := m.callers[nodeID]
	var out []ImpactEntry
	for _, c := range callers {
		out = append(out, ImpactEntry{Node: c, Depth: 1})
	}
	return out, nil
}

func (m *mockBackend) GetNodeByID(id string) (*NodeInfo, error) {
	n, ok := m.nodesByID[id]
	if !ok {
		return nil, nil
	}
	return &n, nil
}

func (m *mockBackend) GetNodesByFile(filePath string) ([]NodeInfo, error) {
	var out []NodeInfo
	for _, n := range m.nodesByID {
		if n.FilePath == filePath {
			out = append(out, n)
		}
	}
	return out, nil
}

func (m *mockBackend) GetFileDependents(filePath string) ([]string, error) {
	return nil, nil
}

func (m *mockBackend) ReadFile(relPath string) (string, error) {
	src, ok := m.sources[relPath]
	if !ok {
		return "", fmt.Errorf("file not found: %s", relPath)
	}
	return src, nil
}

func (m *mockBackend) FindRelevantContext(query string, maxNodes int) (*Subgraph, error) {
	sg := &Subgraph{
		FileSource: make(map[string]string),
		FileNodes:  make(map[string][]NodeInfo),
	}
	// Return nodes matching the query.
	for name, nodes := range m.nodesByName {
		if contains(name, query) || contains(query, name) {
			for _, n := range nodes {
				sg.Nodes = append(sg.Nodes, n)
				sg.FileNodes[n.FilePath] = append(sg.FileNodes[n.FilePath], n)
			}
		}
	}
	// Load sources.
	for fp := range sg.FileNodes {
		if src, ok := m.sources[fp]; ok {
			sg.FileSource[fp] = src
		}
	}
	return sg, nil
}

func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) &&
		(s == substr || len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			containsAt(s, substr)))
}

func containsAt(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

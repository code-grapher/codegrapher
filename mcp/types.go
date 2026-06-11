// Package mcp implements the MCP (Model Context Protocol) server for codegrapher.
// It exposes up to 8 tools for code intelligence over an indexed knowledge graph.
package mcp

import "github.com/specscore/codegrapher/model"

// GraphBackend is the narrow seam the tool handlers use. Implemented by
// StoreBackend and mocked in tests.
type GraphBackend interface {
	// GetStats returns index statistics.
	GetStats() (GraphStats, error)
	// GetProjectRoot returns the absolute project root path.
	GetProjectRoot() string
	// GetFiles returns all indexed file records.
	GetFiles() ([]FileInfo, error)
	// SearchNodes performs FTS/name search.
	SearchNodes(query string, kinds []string, limit int) ([]SearchResult, error)
	// GetNodesByName returns all nodes with the given name.
	GetNodesByName(name string) ([]NodeInfo, error)
	// GetCallers returns caller node IDs for a given node ID.
	GetCallers(nodeID string) ([]NodeInfo, error)
	// GetCallees returns callee node IDs for a given node ID.
	GetCallees(nodeID string) ([]NodeInfo, error)
	// GetImpact returns nodes affected by changing the given node (BFS callers to depth).
	GetImpact(nodeID string, depth int) ([]ImpactEntry, error)
	// GetNodeByID returns one node by ID.
	GetNodeByID(id string) (*NodeInfo, error)
	// GetNodesByFile returns nodes in a file ordered by start line.
	GetNodesByFile(filePath string) ([]NodeInfo, error)
	// GetFileDependents returns files that import/reference filePath.
	GetFileDependents(filePath string) ([]string, error)
	// ReadFile reads file content from disk (path relative to project root).
	ReadFile(relPath string) (string, error)
	// FindRelevantContext builds a subgraph for explore queries.
	FindRelevantContext(query string, maxNodes int) (*Subgraph, error)
}

// GraphStats summarizes the index.
type GraphStats struct {
	FileCount   int                    `json:"fileCount"`
	NodeCount   int                    `json:"nodeCount"`
	EdgeCount   int                    `json:"edgeCount"`
	NodesByKind map[model.NodeKind]int `json:"nodesByKind"`
	DBSizeBytes int64                  `json:"dbSizeBytes"`
	JournalMode string                 `json:"journalMode"`
}

// FileInfo describes one indexed file.
type FileInfo struct {
	Path      string         `json:"path"`
	Language  model.Language `json:"language"`
	NodeCount int            `json:"nodeCount"`
}

// SearchResult is a node with a relevance score.
type SearchResult struct {
	Node  NodeInfo `json:"node"`
	Score float64  `json:"score"`
}

// NodeInfo is a simplified view of a graph node for MCP tool output.
type NodeInfo struct {
	ID            string         `json:"id"`
	Kind          model.NodeKind `json:"kind"`
	Name          string         `json:"name"`
	QualifiedName string         `json:"qualifiedName"`
	FilePath      string         `json:"filePath"`
	Language      model.Language `json:"language"`
	StartLine     int            `json:"startLine"`
	EndLine       int            `json:"endLine"`
	Signature     string         `json:"signature,omitempty"`
	Docstring     string         `json:"docstring,omitempty"`
}

// ImpactEntry is one node in an impact analysis, with its depth.
type ImpactEntry struct {
	Node  NodeInfo `json:"node"`
	Depth int      `json:"depth"`
}

// Subgraph is a set of nodes and their source code, used by the explore tool.
type Subgraph struct {
	// Nodes in relevance order.
	Nodes []NodeInfo `json:"nodes"`
	// FileSource maps relative file path to full source text.
	FileSource map[string]string `json:"fileSource"`
	// FileNodes maps relative file path to the nodes within it (ordered by line).
	FileNodes map[string][]NodeInfo `json:"fileNodes"`
}

// nodeFromModel converts a model.Node to a NodeInfo.
func nodeFromModel(n model.Node) NodeInfo {
	return NodeInfo{
		ID:            n.ID,
		Kind:          n.Kind,
		Name:          n.Name,
		QualifiedName: n.QualifiedName,
		FilePath:      n.FilePath,
		Language:      n.Language,
		StartLine:     n.StartLine,
		EndLine:       n.EndLine,
		Signature:     n.Signature,
		Docstring:     n.Docstring,
	}
}

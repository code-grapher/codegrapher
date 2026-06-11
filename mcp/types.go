// Package mcp implements the MCP (Model Context Protocol) server for
// codegrapher — a behavior-parity port of upstream codegraph's MCP surface
// (src/mcp/tools.ts + src/context/index.ts), served over stdio.
package mcp

import "github.com/specscore/codegrapher/model"

// GraphStats summarizes the index. Mirrors GraphStats in upstream types.
type GraphStats struct {
	FileCount       int
	NodeCount       int
	EdgeCount       int
	NodesByKind     map[model.NodeKind]int
	FilesByLanguage map[model.Language]int
	DBSizeBytes     int64
	JournalMode     string
}

// FileInfo describes one indexed file.
type FileInfo struct {
	Path      string
	Language  model.Language
	NodeCount int
}

// NodeEdge pairs a neighbor node with the edge that reached it — the shape
// upstream GraphTraverser.getCallers/getCallees return.
type NodeEdge struct {
	Node model.Node
	Edge model.Edge
}

// Subgraph is an insertion-ordered node set plus edges and roots, mirroring
// upstream's Subgraph { nodes: Map, edges, roots }. JS Map iteration order is
// insertion order, and the explore formatter depends on it, so the Go port
// tracks order explicitly.
type Subgraph struct {
	order []string
	nodes map[string]model.Node

	Edges []model.Edge
	Roots []string

	// Confidence is "high" or "low" (findRelevantContext's honest-handoff
	// signal). Unused by explore formatting but kept for fidelity.
	Confidence string
}

// NewSubgraph returns an empty subgraph.
func NewSubgraph() *Subgraph {
	return &Subgraph{nodes: make(map[string]model.Node)}
}

// Set inserts or updates a node, preserving first-insertion order.
func (g *Subgraph) Set(n model.Node) {
	if _, ok := g.nodes[n.ID]; !ok {
		g.order = append(g.order, n.ID)
	}
	g.nodes[n.ID] = n
}

// Get returns the node with id, if present.
func (g *Subgraph) Get(id string) (model.Node, bool) {
	n, ok := g.nodes[id]
	return n, ok
}

// Has reports whether id is in the subgraph.
func (g *Subgraph) Has(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

// Delete removes a node (its order slot is skipped on iteration).
func (g *Subgraph) Delete(id string) {
	delete(g.nodes, id)
}

// Len returns the number of nodes.
func (g *Subgraph) Len() int { return len(g.nodes) }

// IDs returns node IDs in insertion order.
func (g *Subgraph) IDs() []string {
	out := make([]string, 0, len(g.nodes))
	for _, id := range g.order {
		if _, ok := g.nodes[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// Values returns nodes in insertion order.
func (g *Subgraph) Values() []model.Node {
	out := make([]model.Node, 0, len(g.nodes))
	for _, id := range g.order {
		if n, ok := g.nodes[id]; ok {
			out = append(out, n)
		}
	}
	return out
}

// FindOptions mirrors FindRelevantContextOptions in upstream types.
type FindOptions struct {
	SearchLimit    int
	TraversalDepth int
	MaxNodes       int
	MinScore       float64
	EdgeKinds      []model.EdgeKind
	NodeKinds      []model.NodeKind
}

// TraversalOptions mirrors upstream TraversalOptions for traverseBFS.
type TraversalOptions struct {
	MaxDepth  int // <=0 means unlimited
	EdgeKinds []model.EdgeKind
	NodeKinds []model.NodeKind
	Direction string // "outgoing" | "incoming" | "both"
	Limit     int
}

// GraphBackend is the engine seam the tool handlers use — the subset of
// upstream's CodeGraph facade the MCP tools call. Implemented by StoreBackend.
type GraphBackend interface {
	GetProjectRoot() string
	GetStats() (GraphStats, error)
	GetFiles() ([]FileInfo, error)

	// SearchNodes runs the full multi-strategy search pipeline
	// (QueryBuilder.searchNodes upstream → query.SearchNodes here).
	SearchNodes(query string, kinds []model.NodeKind, limit int) ([]model.SearchResult, error)

	GetNodesByName(name string) ([]model.Node, error)
	GetNodeByID(id string) (*model.Node, error)
	GetNodesInFile(filePath string) ([]model.Node, error)
	GetFileDependents(filePath string) ([]string, error)

	// GetCallers / GetCallees are depth-1 traversals over
	// calls/references/imports edges, with the reaching edge attached.
	GetCallers(nodeID string) ([]NodeEdge, error)
	GetCallees(nodeID string) ([]NodeEdge, error)

	// GetImpactRadius mirrors GraphTraverser.getImpactRadius: insertion-
	// ordered blast radius including the start node.
	GetImpactRadius(nodeID string, depth int) (*Subgraph, error)

	// GetChildren returns contains-children of a container node.
	GetChildren(nodeID string) ([]model.Node, error)

	GetOutgoingEdges(nodeID string, kinds []model.EdgeKind) ([]model.Edge, error)
	GetIncomingEdges(nodeID string, kinds []model.EdgeKind) ([]model.Edge, error)

	// GetTypeHierarchy walks extends/implements ancestors + descendants.
	GetTypeHierarchy(nodeID string) (*Subgraph, error)

	// TraverseBFS mirrors GraphTraverser.traverseBFS.
	TraverseBFS(startID string, opts TraversalOptions) (*Subgraph, error)

	// FindNodesByExactName mirrors QueryBuilder.findNodesByExactName
	// (case-insensitive exact name with co-location boosting).
	FindNodesByExactName(names []string, kinds []model.NodeKind, limit int) ([]model.SearchResult, error)

	// FindNodesByNameSubstring mirrors QueryBuilder.findNodesByNameSubstring
	// (LIKE %sub%, ordered by name length).
	FindNodesByNameSubstring(substring string, kinds []model.NodeKind, limit int, excludePrefix bool) ([]model.SearchResult, error)

	// GetDominantFile mirrors QueryBuilder.getDominantFile.
	GetDominantFile() (*DominantFile, error)

	// FindEdgesBetweenNodes mirrors QueryBuilder.findEdgesBetweenNodes.
	FindEdgesBetweenNodes(nodeIDs []string, kinds []model.EdgeKind) ([]model.Edge, error)

	// GetProjectNameTokens mirrors CodeGraph.getProjectNameTokens.
	GetProjectNameTokens() map[string]struct{}

	// GetCode reads a node's source slice from disk (ContextBuilder.getCode).
	// Returns "" (no error) when the node or file is unavailable.
	GetCode(nodeID string) (string, error)
}

// DominantFile is getDominantFile's result.
type DominantFile struct {
	FilePath      string
	EdgeCount     int
	NextEdgeCount int
}

package browserapi

import (
	"net/http"
	"sort"

	"github.com/specscore/codegrapher/model"
)

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.validRevision(w, r)
	if !ok {
		return
	}
	rootID := r.PathValue("symbolId")
	root, err := s.findNode(rootID)
	if err != nil {
		s.internalError(w)
		return
	}
	if root == nil {
		s.writeError(w, http.StatusNotFound, "symbol_not_found", "Indexed symbol was not found")
		return
	}
	direction := r.URL.Query().Get("direction")
	if direction == "" {
		direction = "both"
	}
	if direction != "incoming" && direction != "outgoing" && direction != "both" {
		s.writeError(w, http.StatusUnprocessableEntity, "invalid_direction", "Graph direction must be incoming, outgoing, or both")
		return
	}
	depth, valid := boundedInt(r.URL.Query().Get("depth"), 1, s.limits.MaxGraphDepth)
	if !valid {
		s.writeError(w, http.StatusUnprocessableEntity, "invalid_depth", "Graph depth must be a positive integer")
		return
	}
	maxNodes, valid := boundedInt(r.URL.Query().Get("maxNodes"), s.limits.MaxGraphNodes, s.limits.MaxGraphNodes)
	if !valid {
		s.writeError(w, http.StatusUnprocessableEntity, "invalid_limit", "Graph node limit must be a positive integer")
		return
	}
	maxEdges, valid := boundedInt(r.URL.Query().Get("maxEdges"), s.limits.MaxGraphEdges, s.limits.MaxGraphEdges)
	if !valid {
		s.writeError(w, http.StatusUnprocessableEntity, "invalid_limit", "Graph edge limit must be a positive integer")
		return
	}

	nodes := map[string]model.Node{rootID: *root}
	edgeMap := map[string]model.Edge{}
	frontier := []string{rootID}
	truncated := false
	for level := 0; level < depth && len(frontier) > 0; level++ {
		next := []string{}
		for _, current := range frontier {
			for _, store := range s.idx.Stores() {
				var edges []model.Edge
				if direction != "incoming" {
					outgoing, queryErr := store.GetOutgoingEdgesLimited(current, maxEdges+1)
					if queryErr != nil {
						s.internalError(w)
						return
					}
					edges = append(edges, outgoing...)
				}
				if direction != "outgoing" {
					incoming, queryErr := store.GetIncomingEdgesLimited(current, maxEdges+1)
					if queryErr != nil {
						s.internalError(w)
						return
					}
					edges = append(edges, incoming...)
				}
				for _, edge := range edges {
					key := string(edge.Kind) + "\x00" + edge.Source + "\x00" + edge.Target
					if _, exists := edgeMap[key]; exists {
						continue
					}
					if len(edgeMap) >= maxEdges {
						truncated = true
						continue
					}
					candidateNodes := map[string]model.Node{}
					complete := true
					for _, id := range []string{edge.Source, edge.Target} {
						if _, exists := nodes[id]; exists {
							continue
						}
						if _, exists := candidateNodes[id]; exists {
							continue
						}
						node, queryErr := s.findNode(id)
						if queryErr != nil {
							s.internalError(w)
							return
						}
						if node == nil {
							complete = false
							break
						}
						candidateNodes[id] = *node
					}
					if !complete {
						continue
					}
					if len(nodes)+len(candidateNodes) > maxNodes {
						truncated = true
						continue
					}
					edgeMap[key] = edge
					for id, node := range candidateNodes {
						nodes[id] = node
						next = append(next, id)
					}
				}
			}
		}
		frontier = uniqueStrings(next)
	}
	publicNodes := make([]Symbol, 0, len(nodes))
	for _, node := range nodes {
		publicNodes = append(publicNodes, publicSymbol(node))
	}
	sort.Slice(publicNodes, func(i, j int) bool { return publicNodes[i].ID < publicNodes[j].ID })
	publicEdges := make([]GraphEdge, 0, len(edgeMap))
	for _, edge := range edgeMap {
		publicEdges = append(publicEdges, GraphEdge{SourceID: edge.Source, TargetID: edge.Target, Kind: string(edge.Kind), Line: edge.Line, Column: edge.Column})
	}
	sort.Slice(publicEdges, func(i, j int) bool {
		if publicEdges[i].Kind != publicEdges[j].Kind {
			return publicEdges[i].Kind < publicEdges[j].Kind
		}
		if publicEdges[i].SourceID != publicEdges[j].SourceID {
			return publicEdges[i].SourceID < publicEdges[j].SourceID
		}
		return publicEdges[i].TargetID < publicEdges[j].TargetID
	})
	if !s.ensureCurrentRevision(w, snapshot.revision) {
		return
	}
	s.writeJSON(w, http.StatusOK, GraphResponse{RepositoryID: s.repositoryID, Revision: snapshot.revision, RootSymbolID: rootID, Direction: direction, Depth: depth, MaxNodes: maxNodes, MaxEdges: maxEdges, Nodes: publicNodes, Edges: publicEdges, Truncated: truncated, Freshness: publicFreshness(s.freshness(), snapshot.indexedAt)})
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := values[:0]
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

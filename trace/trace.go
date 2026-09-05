// Package trace builds and queries the CodeGrapher projection of SpecScore
// trace records. The projection is deliberately separate from language scope
// stores because a source link commonly crosses from Go to a SpecScore node.
package trace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
	"github.com/specscore/specscore-cli/pkg/sourceref"
	ssTrace "github.com/specscore/specscore-cli/pkg/trace"
)

const (
	contractVersion = ssTrace.ContractVersion
	codeNodePrefix  = "code:"
	unknownPrefix   = "unknown:"
)

// ContractVersion is the provider contract consumed by this package.
const ContractVersion = contractVersion

// Build scans normalized SpecScore records and source symbols into a complete
// deterministic projection. It does not mutate any store.
func Build(root string, sourceStores []*store.Store) ([]store.TraceNode, []store.TraceEdge, error) {
	features, err := ssTrace.Discover(root)
	if err != nil {
		return nil, nil, fmt.Errorf("trace: discover features: %w", err)
	}

	nodes := make([]store.TraceNode, 0)
	targetIDs := make(map[string]string)
	for _, feature := range features {
		featureRef := normalizeRef(feature.ID)
		featureID := specNodeID("feature", featureRef)
		addTraceNode(&nodes, targetIDs, store.TraceNode{
			ID: featureID, Kind: string(model.KindFeature), Reference: featureRef,
			Title: feature.Title, Path: relPath(root, feature.Source.Path),
			StartLine: feature.Source.StartLine, StartColumn: feature.Source.StartColumn,
			EndLine: feature.Source.EndLine, EndColumn: feature.Source.EndColumn,
			Status: feature.Status,
		})
		for _, req := range feature.Requirements {
			addSpecChild(root, &nodes, targetIDs, featureRef, req.ID, string(model.KindRequirement), req.Title, req.Source)
		}
		for _, ac := range feature.AcceptanceCriteria {
			addSpecChild(root, &nodes, targetIDs, featureRef, ac.ID, string(model.KindAcceptanceCriterion), ac.Title, ac.Source)
		}
		for _, scenario := range feature.Scenarios {
			addSpecChild(root, &nodes, targetIDs, featureRef, scenario.ID, "scenario", scenario.Title, scenario.Source)
		}
	}

	// Add every extracted source symbol to the projection. This gives an edge a
	// stable, inspectable source node while leaving normal graph nodes untouched.
	symbols := make(map[string]model.Node)
	for _, st := range sourceStores {
		all, err := st.AllNodes()
		if err != nil {
			return nil, nil, fmt.Errorf("trace: read symbols: %w", err)
		}
		for _, symbol := range all {
			if !durableSymbol(symbol) {
				continue
			}
			symbols[symbol.ID] = symbol
			id := codeNodePrefix + symbol.ID
			nodes = append(nodes, store.TraceNode{
				ID: id, Kind: string(symbol.Kind), Reference: codeReference(symbol.ID),
				Title: symbol.Name, Path: relPath(root, symbol.FilePath),
				StartLine: symbol.StartLine, StartColumn: symbol.StartColumn,
				EndLine: symbol.EndLine, EndColumn: symbol.EndColumn,
			})
		}
	}

	var edges []store.TraceEdge
	paths := make([]string, 0)
	for _, st := range sourceStores {
		files, err := st.GetAllFiles()
		if err != nil {
			return nil, nil, fmt.Errorf("trace: read files: %w", err)
		}
		for _, f := range files {
			if _, ok := symbolsForPath(symbols, f.Path); ok {
				paths = append(paths, f.Path)
			}
		}
	}
	sort.Strings(paths)
	paths = unique(paths)
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			continue
		}
		candidates := symbolsAtPath(symbols, path)
		for lineNo, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			directive := sourceref.ScanDirective(line)
			if directive == nil {
				continue
			}
			directive.Line = lineNo + 1
			symbol := nearestFollowing(candidates, directive.Line)
			if symbol.ID == "" {
				// A source annotation without a following durable symbol has no
				// attachable edge and is intentionally omitted from the accepted
				// projection.
				continue
			}
			targetRef := canonicalTarget(directive.Target)
			targetID := targetIDs[targetRef]
			if targetID == "" {
				targetID = unknownNodeID(targetRef)
				targetIDs[targetRef] = targetID
				nodes = append(nodes, store.TraceNode{ID: targetID, Kind: "unresolved", Reference: targetRef})
			}
			accepted := validDirective(directive, symbol)
			if err := sourceref.ValidateDirective(directive); err != nil {
				accepted = false
			}
			edges = append(edges, store.TraceEdge{
				SourceID: codeNodePrefix + symbol.ID, TargetID: targetID,
				Relation: string(directive.Relation), Accepted: accepted,
				SourcePath: path, SourceLine: directive.Line, SourceColumn: 1,
				TargetReference: targetRef,
			})
		}
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].TargetID != edges[j].TargetID {
			return edges[i].TargetID < edges[j].TargetID
		}
		if edges[i].Relation != edges[j].Relation {
			return edges[i].Relation < edges[j].Relation
		}
		return edges[i].SourceID < edges[j].SourceID
	})
	return nodes, edges, nil
}

// Index builds and persists the complete projection.
func Index(root string, sourceStores []*store.Store, projection *store.Store) error {
	nodes, edges, err := Build(root, sourceStores)
	if err != nil {
		return err
	}
	revision := indexedRevision(root)
	return projection.ReplaceTrace(nodes, edges, revision)
}

// SourceLocation is the user-facing location of an attached source symbol or
// directive.
type SourceLocation struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
}

type Coverage struct {
	Available      bool    `json:"available"`
	ContentHash    string  `json:"content_hash,omitempty"`
	LinesCovered   int     `json:"lines_covered,omitempty"`
	LinesUncovered int     `json:"lines_uncovered,omitempty"`
	PctCovered     float64 `json:"pct_covered,omitempty"`
	RunAt          int64   `json:"run_at,omitempty"`
}

type LinkedSymbol struct {
	Symbol    store.TraceNode `json:"symbol"`
	Location  SourceLocation  `json:"location"`
	Directive SourceLocation  `json:"directive"`
	Coverage  *Coverage       `json:"coverage,omitempty"`
}

// QueryResult is the stable JSON response for `codegrapher trace`.
type QueryResult struct {
	Version           string          `json:"version"`
	Reference         string          `json:"reference"`
	IndexedRevision   string          `json:"indexed_revision"`
	Target            store.TraceNode `json:"target"`
	Implements        []LinkedSymbol  `json:"implements"`
	Verifies          []LinkedSymbol  `json:"verifies"`
	References        []LinkedSymbol  `json:"references"`
	AvailableCoverage bool            `json:"available_coverage"`
}

// Query returns accepted links for a feature, REQ, AC, or scenario reference.
func Query(reference string, projection *store.Store, sourceStores []*store.Store, root string) (*QueryResult, error) {
	normalized, err := normalizeQueryReference(reference)
	if err != nil {
		return nil, err
	}
	nodes, err := projection.GetTraceNodes()
	if err != nil {
		return nil, fmt.Errorf("trace: read nodes: %w", err)
	}
	byRef := make(map[string]store.TraceNode, len(nodes))
	byID := make(map[string]store.TraceNode, len(nodes))
	for _, n := range nodes {
		byRef[n.Reference] = n
		byID[n.ID] = n
	}
	target, ok := byRef[normalized]
	if !ok {
		return nil, fmt.Errorf("trace: reference %q is not indexed", reference)
	}
	edges, err := projection.GetTraceEdgesByTarget(target.ID)
	if err != nil {
		return nil, fmt.Errorf("trace: read links: %w", err)
	}
	coverage := coverageByNode(sourceStores)
	result := &QueryResult{Version: ContractVersion, Reference: normalized, Target: target, Implements: []LinkedSymbol{}, Verifies: []LinkedSymbol{}, References: []LinkedSymbol{}}
	result.IndexedRevision, _ = projection.GetMetadata("trace_indexed_revision")
	for _, edge := range edges {
		symbol, ok := byID[edge.SourceID]
		if !ok || !strings.HasPrefix(symbol.ID, codeNodePrefix) {
			continue
		}
		link := LinkedSymbol{
			Symbol:    symbol,
			Location:  SourceLocation{Path: symbol.Path, StartLine: symbol.StartLine, StartColumn: symbol.StartColumn, EndLine: symbol.EndLine, EndColumn: symbol.EndColumn},
			Directive: SourceLocation{Path: edge.SourcePath, StartLine: edge.SourceLine, StartColumn: edge.SourceColumn, EndLine: edge.SourceLine, EndColumn: edge.SourceColumn + 1},
		}
		if cov, ok := coverage[strings.TrimPrefix(symbol.ID, codeNodePrefix)]; ok {
			link.Coverage = &cov
			result.AvailableCoverage = true
		}
		switch edge.Relation {
		case string(sourceref.RelationImplements):
			result.Implements = append(result.Implements, link)
		case string(sourceref.RelationVerifies):
			result.Verifies = append(result.Verifies, link)
		case string(sourceref.RelationReferences):
			result.References = append(result.References, link)
		}
	}
	return result, nil
}

func addSpecChild(root string, nodes *[]store.TraceNode, refs map[string]string, featureRef, id, kind, title string, source ssTrace.SourceRange) {
	ref := normalizeRef(id)
	if !strings.Contains(ref, "#") {
		ref = featureRef + "#" + ref
	}
	addTraceNode(nodes, refs, store.TraceNode{ID: specNodeID(kind, ref), Kind: kind, Reference: ref, Title: title, Path: relPath(root, source.Path), StartLine: source.StartLine, StartColumn: source.StartColumn, EndLine: source.EndLine, EndColumn: source.EndColumn})
}

func addTraceNode(nodes *[]store.TraceNode, refs map[string]string, n store.TraceNode) {
	if _, exists := refs[n.Reference]; exists {
		return
	}
	refs[n.Reference] = n.ID
	*nodes = append(*nodes, n)
}

func durableSymbol(n model.Node) bool {
	if n.FilePath == "" || n.Kind == model.KindFile || n.Kind == model.KindModule {
		return false
	}
	switch n.Kind {
	case model.KindFeature, model.KindIdea, model.KindPlan, model.KindRequirement,
		model.KindAcceptanceCriterion, model.KindTask:
		return false
	default:
		return true
	}
}

func symbolsForPath(symbols map[string]model.Node, path string) (model.Node, bool) {
	for _, n := range symbols {
		if n.FilePath == path {
			return n, true
		}
	}
	return model.Node{}, false
}

func symbolsAtPath(symbols map[string]model.Node, path string) []model.Node {
	var result []model.Node
	for _, n := range symbols {
		if n.FilePath == path {
			result = append(result, n)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartLine != result[j].StartLine {
			return result[i].StartLine < result[j].StartLine
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func nearestFollowing(symbols []model.Node, line int) model.Node {
	for _, symbol := range symbols {
		if symbol.StartLine > line {
			return symbol
		}
	}
	return model.Node{}
}

func validDirective(d *sourceref.Directive, symbol model.Node) bool {
	if symbol.ID == "" {
		return false
	}
	if d.Relation != sourceref.RelationVerifies {
		return true
	}
	return strings.HasSuffix(symbol.FilePath, "_test.go") && (strings.HasPrefix(symbol.Name, "Test") || strings.HasPrefix(symbol.Name, "Benchmark") || strings.HasPrefix(symbol.Name, "Fuzz"))
}

func normalizeQueryReference(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("trace: empty reference")
	}
	if !strings.HasPrefix(raw, "specscore:") && !strings.HasPrefix(raw, "https://specscore.org/") {
		raw = "specscore:" + raw
	}
	ref, err := sourceref.ParseReference(raw)
	if err != nil {
		return "", fmt.Errorf("trace: parse reference: %w", err)
	}
	return canonicalTarget(ref), nil
}

func canonicalTarget(ref *sourceref.Reference) string {
	if ref == nil {
		return ""
	}
	value := ref.ResolvedPath
	if ref.Fragment != "" {
		value += "#" + strings.ToLower(ref.Fragment[:min(len(ref.Fragment), 4)]) + ref.Fragment[min(len(ref.Fragment), 4):]
	}
	return normalizeRef(value)
}

func normalizeRef(ref string) string {
	ref = strings.TrimPrefix(ref, "specscore:")
	if i := strings.Index(ref, "#"); i >= 0 {
		return strings.ToLower(ref[:i]) + "#" + strings.ToLower(ref[i+1:])
	}
	return strings.ToLower(ref)
}

func specNodeID(kind, ref string) string { return "spec:" + kind + ":" + ref }
func unknownNodeID(ref string) string    { return unknownPrefix + ref }
func codeReference(id string) string     { return codeNodePrefix + id }

func relPath(root, path string) string {
	if path == "" {
		return ""
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}

func indexedRevision(root string) string {
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func coverageByNode(stores []*store.Store) map[string]Coverage {
	out := make(map[string]Coverage)
	for _, st := range stores {
		rows, err := st.GetAllNodeCoverage()
		if err != nil {
			continue
		}
		for _, row := range rows {
			out[row.NodeID] = Coverage{Available: true, ContentHash: row.ContentHash, LinesCovered: row.LinesCovered, LinesUncovered: row.LinesUncovered, PctCovered: row.PctCovered, RunAt: row.RunAt}
		}
	}
	return out
}

func unique(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
	"github.com/spf13/cobra"
)

// PathResult is one bounded, directed call path. A path is static graph
// evidence: it describes one possible route, not an observed execution.
type PathResult struct {
	Start            string        `json:"start"`
	Target           string        `json:"target"`
	Status           string        `json:"status"`
	Hint             string        `json:"hint,omitempty"`
	Freshness        NodeFreshness `json:"freshness"`
	MaxHops          int           `json:"maxHops"`
	MaxNodes         int           `json:"maxNodes"`
	MaxEdges         int           `json:"maxEdges"`
	VisitedNodes     int           `json:"visitedNodes"`
	VisitedEdges     int           `json:"visitedEdges"`
	Truncated        bool          `json:"truncated,omitempty"`
	Steps            []PathStep    `json:"steps,omitempty"`
	StartCandidates  []BriefSymbol `json:"startCandidates,omitempty"`
	TargetCandidates []BriefSymbol `json:"targetCandidates,omitempty"`
}

const (
	defaultPathMaxNodes = 1_000
	defaultPathMaxEdges = 5_000
)

type pathLimits struct{ maxNodes, maxEdges int }

// PathStep is a symbol on a path. Edge describes the transition from the
// preceding step; it is omitted for the starting symbol.
type PathStep struct {
	Symbol BriefSymbol `json:"symbol"`
	Edge   *PathEdge   `json:"edge,omitempty"`
	Source string      `json:"source,omitempty"`
}

type PathEdge struct {
	Kind       model.EdgeKind `json:"kind"`
	Line       int            `json:"line,omitempty"`
	Column     int            `json:"column,omitempty"`
	Provenance string         `json:"provenance,omitempty"`
}

func newPathCmd() *cobra.Command {
	var jsonOut bool
	var format, sourceMode, pathFlag, scope string
	var maxHops, maxNodes, maxEdges int
	cmd := &cobra.Command{
		Use:   "path <start-symbol-or-id> <target-symbol-or-id>",
		Short: "Find one bounded directed call path between two symbols",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sourceMode != "" && sourceMode != "inline" && sourceMode != "footer" {
				return fmt.Errorf("--source must be inline or footer")
			}
			if sourceMode == "footer" && wantsJSON(format, jsonOut) {
				return errors.New("--source=footer is text-only; use --source=inline with --format json")
			}
			root, err := nodeProjectPath(pathFlag)
			if err != nil {
				return err
			}
			idx, err := indexer.Open(root, indexer.Options{})
			if err != nil {
				return fmt.Errorf("open index: %w", err)
			}
			defer func() { _ = idx.Close() }()
			fresh, err := refreshNodeIndex(idx)
			if err != nil {
				return err
			}
			result, err := findCallPathWithLimits(idx, splitCSV(scope), args[0], args[1], maxHops, pathLimits{maxNodes: maxNodes, maxEdges: maxEdges}, sourceMode != "", fresh)
			if err != nil {
				return err
			}
			if wantsJSON(format, jsonOut) {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
					return err
				}
			} else if sourceMode == "footer" {
				if err := printPathFooter(cmd.OutOrStdout(), result); err != nil {
					return err
				}
			} else if err := printPathMarkdown(cmd.OutOrStdout(), result, sourceMode == "inline"); err != nil {
				return err
			}
			if result.Status != "ok" {
				return errors.New("no unambiguous call path found")
			}
			return nil
		},
	}
	addJSONOutputFlags(cmd, &format, &jsonOut)
	cmd.Flags().StringVar(&sourceMode, "source", "", "Include path source: footer (default when set) or inline")
	cmd.Flags().Lookup("source").NoOptDefVal = "footer"
	cmd.Flags().IntVar(&maxHops, "max-hops", 8, "Maximum directed call edges to traverse")
	cmd.Flags().IntVar(&maxNodes, "max-nodes", defaultPathMaxNodes, "Maximum graph nodes to visit")
	cmd.Flags().IntVar(&maxEdges, "max-edges", defaultPathMaxEdges, "Maximum graph edges to inspect")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().StringVar(&scope, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	return cmd
}

func nodeProjectPath(pathFlag string) (string, error) {
	if pathFlag != "" {
		pathFlag = resolveArg([]string{pathFlag})
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		pathFlag = findNearestOrReturn(cwd)
	}
	if !indexer.IsInitialized(pathFlag) {
		return "", fmt.Errorf("CodeGraph not initialized in %s", pathFlag)
	}
	return pathFlag, nil
}

func findCallPath(idx *indexer.Indexer, scopes []string, startQuery, targetQuery string, maxHops int, wantSource bool, freshness NodeFreshness) (PathResult, error) {
	return findCallPathWithLimits(idx, scopes, startQuery, targetQuery, maxHops, pathLimits{maxNodes: defaultPathMaxNodes, maxEdges: defaultPathMaxEdges}, wantSource, freshness)
}

func findCallPathWithLimits(idx *indexer.Indexer, scopes []string, startQuery, targetQuery string, maxHops int, limits pathLimits, wantSource bool, freshness NodeFreshness) (PathResult, error) {
	result := PathResult{Start: startQuery, Target: targetQuery, Freshness: freshness, MaxHops: maxHops, MaxNodes: limits.maxNodes, MaxEdges: limits.maxEdges, Steps: []PathStep{}}
	if maxHops < 0 || limits.maxNodes < 1 || limits.maxEdges < 1 {
		if maxHops < 0 {
			return result, errors.New("--max-hops must not be negative")
		}
		return result, errors.New("--max-nodes and --max-edges must be positive")
	}
	stores := idx.StoresFiltered(scopes)
	starts, err := findNodeMatches(stores, startQuery)
	if err != nil {
		return result, err
	}
	targets, err := findNodeMatches(stores, targetQuery)
	if err != nil {
		return result, err
	}
	if len(starts) != 1 || len(targets) != 1 {
		result.Status = "ambiguous"
		if len(starts) == 0 || len(targets) == 0 {
			result.Status = "not_found"
		}
		result.StartCandidates, result.TargetCandidates = briefMatches(starts), briefMatches(targets)
		result.Hint = "Use query --brief and retry path with exact candidate ids."
		return result, nil
	}
	start, target := starts[0], targets[0]
	if start.store != target.store {
		result.Status, result.Hint = "not_found", "The selected symbols are in separate index scopes; no persisted directed path joins them."
		return result, nil
	}
	if start.node.ID == target.node.ID {
		result.Status = "ok"
		result.Steps = []PathStep{{Symbol: briefNode(start.node)}}
		return addPathSources(idx, scopes, result, wantSource)
	}

	type seen struct {
		previous string
		edge     model.Edge
		hops     int
	}
	seenByID := map[string]seen{start.node.ID: {hops: 0}}
	result.VisitedNodes = 1
	queue := []string{start.node.ID}
	found := false
	for len(queue) > 0 && !found {
		current := queue[0]
		queue = queue[1:]
		entry := seenByID[current]
		if entry.hops >= maxHops {
			edges, more, err := boundedCallEdges(start.store, current, limits.maxEdges-result.VisitedEdges)
			if err != nil {
				return result, err
			}
			result.VisitedEdges += len(edges)
			result.Truncated = result.Truncated || more
			for _, edge := range edges {
				if _, alreadySeen := seenByID[edge.Target]; !alreadySeen {
					result.Truncated = true
					break
				}
			}
			continue
		}
		edges, more, err := boundedCallEdges(start.store, current, limits.maxEdges-result.VisitedEdges)
		if err != nil {
			return result, err
		}
		result.VisitedEdges += len(edges)
		result.Truncated = result.Truncated || more
		for _, edge := range edges {
			if _, exists := seenByID[edge.Target]; exists {
				continue
			}
			if result.VisitedNodes >= limits.maxNodes {
				result.Truncated = true
				continue
			}
			seenByID[edge.Target] = seen{previous: current, edge: edge, hops: entry.hops + 1}
			result.VisitedNodes++
			if edge.Target == target.node.ID {
				found = true
				break
			}
			queue = append(queue, edge.Target)
		}
	}
	if !found {
		result.Status = "not_found"
		if result.Truncated {
			result.Hint = "No path was found within the configured hop/node/edge bounds; increase the relevant limit if a wider static search is acceptable."
		} else {
			result.Hint = "No directed calls edge connects the selected symbols."
		}
		return result, nil
	}
	ids := []string{target.node.ID}
	for ids[len(ids)-1] != start.node.ID {
		ids = append(ids, seenByID[ids[len(ids)-1]].previous)
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}
	nodes, err := start.store.GetNodesByIDs(ids)
	if err != nil {
		return result, err
	}
	for i, id := range ids {
		n, ok := nodes[id]
		if !ok {
			return result, fmt.Errorf("path node %s disappeared", id)
		}
		step := PathStep{Symbol: briefNode(n)}
		if i > 0 {
			edge := seenByID[id].edge
			step.Edge = &PathEdge{Kind: edge.Kind, Line: edge.Line, Column: edge.Column, Provenance: edge.Provenance}
		}
		result.Steps = append(result.Steps, step)
	}
	result.Status = "ok"
	return addPathSources(idx, scopes, result, wantSource)
}

// boundedCallEdges asks for one sentinel row beyond the remaining allowance,
// so the caller can state truncation without loading an unbounded adjacency.
func boundedCallEdges(s *store.Store, sourceID string, remaining int) ([]model.Edge, bool, error) {
	if remaining < 1 {
		return nil, true, nil
	}
	edges, err := s.GetOutgoingEdgesByKindLimited(sourceID, []model.EdgeKind{model.EdgeCalls}, remaining+1)
	if err != nil {
		return nil, false, err
	}
	if len(edges) > remaining {
		return edges[:remaining], true, nil
	}
	return edges, false, nil
}

func addPathSources(idx *indexer.Indexer, scopes []string, result PathResult, want bool) (PathResult, error) {
	if !want {
		return result, nil
	}
	for i := range result.Steps {
		matches, err := findNodeMatches(idx.StoresFiltered(scopes), result.Steps[i].Symbol.ID)
		if err != nil {
			return result, err
		}
		if len(matches) != 1 {
			return result, fmt.Errorf("path symbol %s changed during source retrieval", result.Steps[i].Symbol.ID)
		}
		source, err := readVerifiedIndexedNodeSource(idx.Root(), matches[0])
		if err != nil {
			return result, err
		}
		result.Steps[i].Source = source
	}
	result.Freshness.Verified = true
	return result, nil
}

func printPathMarkdown(w io.Writer, result PathResult, inline bool) error {
	if result.Status != "ok" {
		if _, err := fmt.Fprintf(w, "## %s path `%s` → `%s`\n\n%s\n", result.Status, result.Start, result.Target, result.Hint); err != nil {
			return err
		}
		for _, c := range append(result.StartCandidates, result.TargetCandidates...) {
			if _, err := fmt.Fprintf(w, "- `%s` — `%s` (%s) — %s:%d\n", c.ID, c.QualifiedName, c.Kind, c.FilePath, c.StartLine); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err := fmt.Fprintf(w, "## Static call path: `%s` → `%s`\n", result.Start, result.Target); err != nil {
		return err
	}
	for i, step := range result.Steps {
		if i > 0 {
			e := step.Edge
			if _, err := fmt.Fprintf(w, "\n  ↓ `%s` @%d:%d [%s]\n", e.Kind, e.Line, e.Column, e.Provenance); err != nil {
				return err
			}
		}
		s := step.Symbol
		if _, err := fmt.Fprintf(w, "- `%s` (%s) — %s:%d", s.QualifiedName, s.Kind, s.FilePath, s.StartLine); err != nil {
			return err
		}
		if s.Signature != "" {
			if _, err := fmt.Fprintf(w, " — `%s`", s.Signature); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if inline {
			if err := printPathStepSource(w, step); err != nil {
				return err
			}
		}
	}
	return nil
}

func printPathStepSource(w io.Writer, step PathStep) error {
	if step.Source == "" {
		return nil
	}
	fence := codeFence(step.Source)
	_, err := fmt.Fprintf(w, "\n%s%s\n%s\n%s\n", fence, step.Symbol.Language, step.Source, fence)
	return err
}

func printPathFooter(w io.Writer, result PathResult) error {
	metadata := result
	metadata.Steps = append([]PathStep(nil), result.Steps...)
	for i := range metadata.Steps {
		metadata.Steps[i].Source = ""
	}
	b, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "```json\n%s\n```\n", b); err != nil {
		return err
	}
	if result.Status != "ok" {
		return nil
	}
	if _, err := fmt.Fprintln(w, "\n## Sources"); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, step := range result.Steps {
		if !seen[step.Symbol.ID] {
			seen[step.Symbol.ID] = true
			if err := printPathStepSource(w, step); err != nil {
				return err
			}
		}
	}
	return nil
}

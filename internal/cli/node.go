package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/store"
	"github.com/spf13/cobra"
)

// NodeResult is a symbol-oriented view of an indexed node. Source is omitted
// from JSON by default; text output renders it as a raw fenced code block.
type NodeResult struct {
	Requested   string         `json:"requested"`
	Status      string         `json:"status"`
	Hint        string         `json:"hint,omitempty"`
	Symbol      *BriefSymbol   `json:"symbol,omitempty"`
	Source      string         `json:"source,omitempty"`
	SourceRange string         `json:"sourceRange,omitempty"`
	Freshness   NodeFreshness  `json:"freshness"`
	Relations   []NodeRelation `json:"relations,omitempty"`
	Candidates  []BriefSymbol  `json:"candidates,omitempty"`
}

type NodeFreshness struct {
	Refreshed bool `json:"refreshed"`
	Verified  bool `json:"verified"`
}

type NodeRelation struct {
	Direction  string         `json:"direction"`
	Kind       model.EdgeKind `json:"kind"`
	Symbol     BriefSymbol    `json:"symbol"`
	Provenance string         `json:"provenance,omitempty"`
	Line       int            `json:"line,omitempty"`
	Column     int            `json:"column,omitempty"`
}

type matchedNode struct {
	node  model.Node
	store *store.Store
}

func newNodeCmd() *cobra.Command {
	var jsonOut, relations bool
	var format, fileHint, scope, sourceMode string
	var line, limit int
	var pathFlag string

	cmd := &cobra.Command{
		Use:   "node <symbol> [symbol...]",
		Short: "Show a symbol's metadata, source, and immediate relationships",
		Long:  "Show indexed symbol metadata. --source renders line-bounded source as a Markdown code block; use --format json only when a machine-readable payload is needed.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sourceMode != "" && sourceMode != "inline" && sourceMode != "footer" {
				return fmt.Errorf("--source must be inline or footer")
			}
			if sourceMode == "footer" && wantsJSON(format, jsonOut) {
				return errors.New("--source=footer is text-only; use --source=inline with --format json")
			}
			projectPath := ""
			if pathFlag != "" {
				projectPath = resolveArg([]string{pathFlag})
			} else {
				cwd, _ := os.Getwd()
				projectPath = findNearestOrReturn(cwd)
			}
			if !indexer.IsInitialized(projectPath) {
				return fmt.Errorf("CodeGraph not initialized in %s", projectPath)
			}
			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				return fmt.Errorf("open index: %w", err)
			}
			defer func() { _ = idx.Close() }()

			fresh, err := refreshNodeIndex(idx)
			if err != nil {
				return err
			}
			var results []NodeResult
			incomplete := false
			for _, symbol := range args {
				result, err := resolveNode(idx, splitCSV(scope), symbol, fileHint, line, sourceMode != "", relations, limit, fresh)
				if err != nil {
					return err
				}
				results = append(results, result)
				incomplete = incomplete || result.Status != "ok"
			}
			out := cmd.OutOrStdout()
			if wantsJSON(format, jsonOut) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				// Always an array: batches and one-symbol calls share one stable
				// machine contract.
				if err := enc.Encode(results); err != nil {
					return err
				}
				if incomplete {
					return errors.New("one or more requested symbols are ambiguous or not found")
				}
				return nil
			}
			if sourceMode == "footer" {
				if err := printNodeFooter(out, results); err != nil {
					return err
				}
			} else {
				for i, result := range results {
					if i > 0 {
						if _, err := fmt.Fprintln(out, "\n---"); err != nil {
							return err
						}
					}
					if err := printNodeMarkdown(out, result, sourceMode == "inline"); err != nil {
						return err
					}
				}
			}
			if incomplete {
				return errors.New("one or more requested symbols are ambiguous or not found")
			}
			return nil
		},
	}
	addJSONOutputFlags(cmd, &format, &jsonOut)
	cmd.Flags().StringVar(&sourceMode, "source", "", "Include line-bounded source: footer (default when set) or inline")
	cmd.Flags().Lookup("source").NoOptDefVal = "footer"
	cmd.Flags().BoolVar(&relations, "relations", false, "Include bounded immediate incoming and outgoing relationships")
	cmd.Flags().StringVar(&fileHint, "file", "", "Disambiguate by indexed file path")
	cmd.Flags().IntVar(&line, "line", 0, "Disambiguate by source line")
	cmd.Flags().IntVarP(&limit, "limit", "l", 12, "Maximum relationships in each direction")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().StringVar(&scope, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	return cmd
}

func refreshNodeIndex(idx *indexer.Indexer) (NodeFreshness, error) {
	res, err := idx.RefreshForRead(indexer.Options{})
	if err != nil {
		return NodeFreshness{}, err
	}
	return NodeFreshness{Refreshed: res.FilesAdded > 0 || res.FilesModified > 0 || res.FilesRemoved > 0 || res.FullReindex}, nil
}

func resolveNode(idx *indexer.Indexer, scopes []string, symbol, fileHint string, line int, wantSource, wantRelations bool, limit int, freshness NodeFreshness) (NodeResult, error) {
	matches, err := findNodeMatches(idx.StoresFiltered(scopes), symbol)
	if err != nil {
		return NodeResult{}, err
	}
	matches = narrowNodeMatches(matches, fileHint, line)
	if len(matches) == 0 {
		return NodeResult{Requested: symbol, Status: "not_found", Hint: "Use query --brief to discover indexed symbols.", Candidates: []BriefSymbol{}, Freshness: freshness}, nil
	}
	if len(matches) > 1 {
		return NodeResult{Requested: symbol, Status: "ambiguous", Hint: "Retry node with an exact candidate id, --file, or --line.", Candidates: briefMatches(matches), Freshness: freshness}, nil
	}

	match := matches[0]
	symbolBrief := briefNode(match.node)
	result := NodeResult{Requested: symbol, Status: "ok", Symbol: &symbolBrief, Freshness: freshness}
	if wantSource {
		content, rec, err := readIndexedNodeSource(idx.Root(), match)
		if err != nil {
			return NodeResult{}, err
		}
		if indexer.HashContent(content.all) != rec.ContentHash {
			res := idx.SyncFiles([]string{match.node.FilePath}, indexer.Options{})
			if res.LockUnavailable {
				return NodeResult{}, errors.New("index is locked; cannot safely refresh changed source")
			}
			if len(res.Errors) > 0 {
				return NodeResult{}, fmt.Errorf("refresh %s: %s", match.node.FilePath, res.Errors[0].Message)
			}
			freshness.Refreshed = true
			matches, err = findNodeMatches(idx.StoresFiltered(scopes), symbol)
			if err != nil {
				return NodeResult{}, err
			}
			matches = narrowNodeMatches(matches, fileHint, line)
			if len(matches) != 1 {
				return NodeResult{}, fmt.Errorf("symbol %q changed while refreshing; run node again", symbol)
			}
			match = matches[0]
			symbolBrief = briefNode(match.node)
			result.Symbol = &symbolBrief
			content, rec, err = readIndexedNodeSource(idx.Root(), match)
			if err != nil || indexer.HashContent(content.all) != rec.ContentHash {
				return NodeResult{}, fmt.Errorf("source %s changed during retrieval; no stale source returned", match.node.FilePath)
			}
		}
		result.Source = string(content.node)
		result.SourceRange = "line-bounded"
		result.Freshness = freshness
		result.Freshness.Verified = true
	}
	if wantRelations {
		rels, err := nodeRelations(match, limit)
		if err != nil {
			return NodeResult{}, err
		}
		result.Relations = rels
	}
	return result, nil
}

type nodeSource struct{ all, node []byte }

func readIndexedNodeSource(root string, match matchedNode) (nodeSource, *model.FileRecord, error) {
	abs := filepath.Join(root, filepath.FromSlash(match.node.FilePath))
	cleanRoot := filepath.Clean(root)
	if abs != cleanRoot && !strings.HasPrefix(filepath.Clean(abs), cleanRoot+string(filepath.Separator)) {
		return nodeSource{}, nil, errors.New("indexed path escapes project root")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nodeSource{}, nil, fmt.Errorf("read %s: %w", match.node.FilePath, err)
	}
	rec, err := match.store.GetFileByPath(match.node.FilePath)
	if err != nil || rec == nil {
		return nodeSource{}, nil, fmt.Errorf("no indexed file record for %s", match.node.FilePath)
	}
	return nodeSource{all: data, node: lineBoundedSource(data, match.node.StartLine, match.node.EndLine)}, rec, nil
}

func lineBoundedSource(data []byte, start, end int) []byte {
	if start < 1 || end < start {
		return nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if start > len(lines) {
		return nil
	}
	if end > len(lines) {
		end = len(lines)
	}
	return []byte(strings.Join(lines[start-1:end], ""))
}

func findNodeMatches(stores []*store.Store, symbol string) ([]matchedNode, error) {
	var out []matchedNode
	// A brief query returns IDs, and an ID is the unambiguous, lossless way to
	// select one overload for source retrieval.
	for _, s := range stores {
		node, err := s.GetNodeByID(symbol)
		if err != nil {
			return nil, err
		}
		if node != nil {
			out = append(out, matchedNode{node: *node, store: s})
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	qualified := strings.Contains(symbol, ".") || strings.Contains(symbol, "::")
	for _, s := range stores {
		var nodes []model.Node
		var err error
		if !qualified {
			nodes, err = s.GetNodesByName(symbol)
		} else {
			nodes, err = s.GetNodesByQualifiedNameExact(symbol)
			if len(nodes) == 0 && strings.Contains(symbol, ".") {
				qualified := strings.ReplaceAll(symbol, ".", "::")
				nodes, err = s.GetNodesByQualifiedNameSuffix(qualified)
			}
		}
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			out = append(out, matchedNode{node: n, store: s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].node.FilePath < out[j].node.FilePath || (out[i].node.FilePath == out[j].node.FilePath && out[i].node.StartLine < out[j].node.StartLine)
	})
	return out, nil
}

func narrowNodeMatches(matches []matchedNode, fileHint string, line int) []matchedNode {
	if fileHint != "" {
		var narrowed []matchedNode
		want := strings.ToLower(strings.ReplaceAll(fileHint, "\\", "/"))
		for _, m := range matches {
			path := strings.ToLower(m.node.FilePath)
			if strings.HasSuffix(path, want) || strings.Contains(path, want) {
				narrowed = append(narrowed, m)
			}
		}
		matches = narrowed
	}
	if line > 0 {
		var narrowed []matchedNode
		for _, m := range matches {
			if m.node.StartLine <= line && m.node.EndLine >= line {
				narrowed = append(narrowed, m)
			}
		}
		matches = narrowed
	}
	return matches
}

func nodeRelations(match matchedNode, limit int) ([]NodeRelation, error) {
	if limit < 1 {
		return []NodeRelation{}, nil
	}
	collect := func(edges []model.Edge, direction string) ([]NodeRelation, error) {
		ids := make([]string, 0, len(edges))
		for _, e := range edges {
			if direction == "outgoing" {
				ids = append(ids, e.Target)
			} else {
				ids = append(ids, e.Source)
			}
		}
		nodes, err := match.store.GetNodesByIDs(ids)
		if err != nil {
			return nil, err
		}
		out := make([]NodeRelation, 0, len(edges))
		for _, e := range edges {
			id := e.Target
			if direction == "incoming" {
				id = e.Source
			}
			if n, ok := nodes[id]; ok {
				out = append(out, NodeRelation{Direction: direction, Kind: e.Kind, Symbol: briefNode(n), Provenance: e.Provenance, Line: e.Line, Column: e.Column})
			}
		}
		return out, nil
	}
	outgoing, err := match.store.GetOutgoingEdgesLimited(match.node.ID, limit)
	if err != nil {
		return nil, err
	}
	incoming, err := match.store.GetIncomingEdgesLimited(match.node.ID, limit)
	if err != nil {
		return nil, err
	}
	out, err := collect(outgoing, "outgoing")
	if err != nil {
		return nil, err
	}
	in, err := collect(incoming, "incoming")
	if err != nil {
		return nil, err
	}
	return append(out, in...), nil
}

func briefNode(n model.Node) BriefSymbol {
	return BriefSymbol{ID: n.ID, Kind: n.Kind, Name: n.Name, QualifiedName: n.QualifiedName, FilePath: n.FilePath, Language: n.Language, StartLine: n.StartLine, EndLine: n.EndLine, StartColumn: n.StartColumn, EndColumn: n.EndColumn, Signature: n.Signature}
}

func briefMatches(matches []matchedNode) []BriefSymbol {
	out := make([]BriefSymbol, 0, len(matches))
	for _, match := range matches {
		out = append(out, briefNode(match.node))
	}
	return out
}

func printNodeMarkdown(w io.Writer, result NodeResult, requestedSource bool) error {
	if len(result.Candidates) > 0 {
		if _, err := fmt.Fprintf(w, "## %s symbol `%s`\n", result.Status, result.Requested); err != nil {
			return err
		}
		for _, candidate := range result.Candidates {
			sig := ""
			if candidate.Signature != "" {
				sig = " — `" + candidate.Signature + "`"
			}
			if _, err := fmt.Fprintf(w, "- `%s` — `%s` (%s) — %s:%d-%d%s\n", candidate.ID, candidate.QualifiedName, candidate.Kind, candidate.FilePath, candidate.StartLine, candidate.EndLine, sig); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(w, "\nChoose a candidate, then run `codegrapher node \"<exact id>\" --source`. %s\n", result.Hint)
		return err
	}
	if result.Symbol == nil {
		if _, err := fmt.Fprintf(w, "## not_found symbol `%s`\n", result.Requested); err != nil {
			return err
		}
		if result.Hint != "" {
			if _, err := fmt.Fprintf(w, "\n%s\n", result.Hint); err != nil {
				return err
			}
		}
		return nil
	}
	s := *result.Symbol
	if _, err := fmt.Fprintf(w, "## %s (%s)\n\n", s.QualifiedName, s.Kind); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "- File: `%s:%d-%d`\n", s.FilePath, s.StartLine, s.EndLine); err != nil {
		return err
	}
	if s.Signature != "" {
		if _, err := fmt.Fprintf(w, "- Signature: `%s`\n", s.Signature); err != nil {
			return err
		}
	}
	if result.Freshness.Refreshed {
		if _, err := fmt.Fprintln(w, "- Freshness: incrementally refreshed"); err != nil {
			return err
		}
	}
	if requestedSource {
		if err := printNodeSourceMarkdown(w, result); err != nil {
			return err
		}
	}
	if len(result.Relations) > 0 {
		if _, err := fmt.Fprintln(w, "\n### Immediate relationships"); err != nil {
			return err
		}
		for _, r := range result.Relations {
			prov := ""
			if r.Provenance != "" {
				prov = " [" + r.Provenance + "]"
			}
			if _, err := fmt.Fprintf(w, "- %s `%s` → `%s` (%s)%s\n", r.Direction, r.Kind, r.Symbol.QualifiedName, r.Symbol.FilePath, prov); err != nil {
				return err
			}
		}
	}
	return nil
}

func printNodeSourceMarkdown(w io.Writer, result NodeResult) error {
	if result.Source == "" || result.Symbol == nil {
		return nil
	}
	s := *result.Symbol
	if _, err := fmt.Fprintf(w, "\n### %s — `%s:%d-%d`\n\n", s.QualifiedName, s.FilePath, s.StartLine, s.EndLine); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "> Source range is %s; it was verified against the indexed file hash.\n\n", result.SourceRange); err != nil {
		return err
	}
	fence := codeFence(result.Source)
	_, err := fmt.Fprintf(w, "%s%s\n%s\n%s\n", fence, s.Language, result.Source, fence)
	return err
}

// printNodeFooter keeps source outside structured metadata. This is the
// token-friendly default for shell/agent use: an agent can inspect one compact
// JSON-shaped header, then read raw (not JSON-escaped) code blocks.
func printNodeFooter(w io.Writer, results []NodeResult) error {
	metadata := make([]NodeResult, len(results))
	copy(metadata, results)
	for i := range metadata {
		metadata[i].Source = ""
		metadata[i].SourceRange = ""
	}
	var data []byte
	var err error
	if len(metadata) == 1 {
		data, err = json.MarshalIndent(metadata[0], "", "  ")
	} else {
		data, err = json.MarshalIndent(metadata, "", "  ")
	}
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "```json\n%s\n```\n", data); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\n## Sources"); err != nil {
		return err
	}
	for _, result := range results {
		if err := printNodeSourceMarkdown(w, result); err != nil {
			return err
		}
	}
	return nil
}

func codeFence(source string) string {
	longest := 0
	for _, line := range strings.Split(source, "\n") {
		for i := 0; i < len(line); {
			if line[i] != '`' {
				i++
				continue
			}
			j := i
			for j < len(line) && line[j] == '`' {
				j++
			}
			if j-i > longest {
				longest = j - i
			}
			i = j
		}
	}
	return strings.Repeat("`", max(longest+1, 3))
}

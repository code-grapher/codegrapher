package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/spf13/cobra"
)

func newQueryCmd() *cobra.Command {
	var jsonOut bool
	var format string
	var brief bool
	var limit int
	var kind string
	var pathFlag string
	var scope string

	cmd := &cobra.Command{
		Use:   "query <search>",
		Short: "Search for symbols in the codebase",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			search := args[0]
			var projectPath string
			if pathFlag != "" {
				projectPath = resolveArg([]string{pathFlag})
			} else {
				cwd, _ := os.Getwd()
				projectPath = findNearestOrReturn(cwd)
			}

			if !indexer.IsInitialized(projectPath) {
				printError(fmt.Sprintf("CodeGraph not initialized in %s", projectPath))
				os.Exit(1)
			}

			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				printError(fmt.Sprintf("Failed to open index: %s", err))
				os.Exit(1)
			}
			defer func() { _ = idx.Close() }()

			q := NewStoreQuerier(idx.StoresFiltered(splitCSV(scope))...)
			opts := SearchOptions{Limit: limit}
			if kind != "" {
				opts.Kinds = []model.NodeKind{model.NodeKind(kind)}
			}

			results, err := q.SearchNodes(search, opts)
			if err != nil {
				printError(fmt.Sprintf("Search failed: %s", err))
				os.Exit(1)
			}

			if wantsJSON(format, jsonOut) {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if brief {
					return enc.Encode(briefSearchResults(results))
				}
				if results == nil {
					results = []model.SearchResult{}
				}
				return enc.Encode(results)
			}

			if len(results) == 0 {
				printInfo(fmt.Sprintf("No results found for %q", search))
				return nil
			}

			fmt.Println(bold(fmt.Sprintf("\nSearch Results for %q:\n", search)))
			for _, r := range results {
				n := r.Node
				loc := fmt.Sprintf("%s:%d", n.FilePath, n.StartLine)
				score := dim(fmt.Sprintf("(%d%%)", int(r.Score*100/100)))
				fmt.Printf("%s %s %s\n", cyan(padRight(string(n.Kind))), n.Name, score)
				fmt.Println(dim("  " + loc))
				if n.Signature != "" {
					fmt.Println(dim("  " + n.Signature))
				}
				fmt.Println()
			}
			return nil
		},
	}

	addJSONOutputFlags(cmd, &format, &jsonOut)
	cmd.Flags().BoolVar(&brief, "brief", false, "Return compact symbol metadata (use with --format json)")
	cmd.Flags().IntVarP(&limit, "limit", "l", 10, "Maximum results")
	cmd.Flags().StringVarP(&kind, "kind", "k", "", "Filter by node kind")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().StringVar(&scope, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	return cmd
}

// BriefSymbol is the compact, stable discovery shape for automation. It
// intentionally excludes documentation, decorators, and result scores so an
// agent can choose a symbol before requesting its source with `node`.
type BriefSymbol struct {
	ID            string         `json:"id"`
	Kind          model.NodeKind `json:"kind"`
	Name          string         `json:"name"`
	QualifiedName string         `json:"qualifiedName"`
	FilePath      string         `json:"filePath"`
	Language      model.Language `json:"language"`
	StartLine     int            `json:"startLine"`
	EndLine       int            `json:"endLine"`
	StartColumn   int            `json:"startColumn"`
	EndColumn     int            `json:"endColumn"`
	Signature     string         `json:"signature,omitempty"`
}

func briefSearchResults(results []model.SearchResult) []BriefSymbol {
	if len(results) == 0 {
		return []BriefSymbol{}
	}
	out := make([]BriefSymbol, 0, len(results))
	for _, r := range results {
		n := r.Node
		out = append(out, BriefSymbol{
			ID: n.ID, Kind: n.Kind, Name: n.Name, QualifiedName: n.QualifiedName,
			FilePath: n.FilePath, Language: n.Language, StartLine: n.StartLine, EndLine: n.EndLine,
			StartColumn: n.StartColumn, EndColumn: n.EndColumn, Signature: n.Signature,
		})
	}
	return out
}

func padRight(s string) string {
	for len(s) < 12 {
		s += " "
	}
	return s
}

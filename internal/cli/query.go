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
	var limit int
	var kind string
	var pathFlag string

	cmd := &cobra.Command{
		Use:   "query <search>",
		Short: "Search for symbols in the codebase",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			search := args[0]
			var projectPath string
			if pathFlag != "" {
				projectPath = resolveArg([]string{pathFlag}, 0)
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
			defer idx.Close()

			q := NewStoreQuerier(idx.Store())
			opts := SearchOptions{Limit: limit}
			if kind != "" {
				opts.Kinds = []model.NodeKind{model.NodeKind(kind)}
			}

			results, err := q.SearchNodes(search, opts)
			if err != nil {
				printError(fmt.Sprintf("Search failed: %s", err))
				os.Exit(1)
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
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
				fmt.Printf("%s %s %s\n", cyan(padRight(string(n.Kind), 12)), n.Name, score)
				fmt.Println(dim("  " + loc))
				if n.Signature != "" {
					fmt.Println(dim("  " + n.Signature))
				}
				fmt.Println()
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "Output as JSON")
	cmd.Flags().IntVarP(&limit, "limit", "l", 10, "Maximum results")
	cmd.Flags().StringVarP(&kind, "kind", "k", "", "Filter by node kind")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	return cmd
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

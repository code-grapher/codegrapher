package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/specscore/codegrapher/indexer"
	"github.com/spf13/cobra"
)

func newCalleesCmd() *cobra.Command {
	var jsonOut bool
	var limit int
	var pathFlag string
	var scope string

	cmd := &cobra.Command{
		Use:   "callees <symbol>",
		Short: "Find all functions/methods that a specific symbol calls",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbol := args[0]
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
			defer func() { _ = idx.Close() }()

			q := NewStoreQuerier(idx.StoresFiltered(splitCSV(scope))...)
			result, err := q.Callees(symbol)
			if err != nil {
				printError(fmt.Sprintf("callees failed: %s", err))
				os.Exit(1)
			}

			if limit > 0 && len(result.Callees) > limit {
				result.Callees = result.Callees[:limit]
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			if len(result.Callees) == 0 {
				printInfo(fmt.Sprintf("No callees found for %q", symbol))
				return nil
			}
			fmt.Println(bold(fmt.Sprintf("\nCallees of %q (%d):\n", symbol, len(result.Callees))))
			for _, n := range result.Callees {
				fmt.Printf("%s %s\n", cyan(padRight(string(n.Kind), 12)), n.Name)
				fmt.Println(dim(fmt.Sprintf("  %s:%d", n.FilePath, n.StartLine)))
				fmt.Println()
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "Output as JSON")
	cmd.Flags().IntVarP(&limit, "limit", "l", 20, "Maximum results")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().StringVar(&scope, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	return cmd
}

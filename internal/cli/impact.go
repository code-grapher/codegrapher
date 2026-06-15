package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/specscore/codegrapher/indexer"
	"github.com/spf13/cobra"
)

func newImpactCmd() *cobra.Command {
	var jsonOut bool
	var depth int
	var pathFlag string
	var scope string

	cmd := &cobra.Command{
		Use:   "impact <symbol>",
		Short: "Analyze what code is affected by changing a symbol",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			symbol := args[0]
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
			result, err := q.Impact(symbol, depth)
			if err != nil {
				printError(fmt.Sprintf("impact failed: %s", err))
				os.Exit(1)
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			if result.NodeCount == 0 {
				printInfo(fmt.Sprintf("No affected symbols found for %q", symbol))
				return nil
			}

			fmt.Println(bold(fmt.Sprintf("\nImpact of changing %q — %d affected symbols:\n", symbol, result.NodeCount)))

			// Group by file.
			byFile := map[string][]SymbolRef{}
			fileOrder := []string{}
			seen := map[string]bool{}
			for _, n := range result.Affected {
				if !seen[n.FilePath] {
					seen[n.FilePath] = true
					fileOrder = append(fileOrder, n.FilePath)
				}
				byFile[n.FilePath] = append(byFile[n.FilePath], n)
			}
			for _, file := range fileOrder {
				fmt.Println(cyan(file))
				for _, n := range byFile[file] {
					fmt.Printf("  %s%s:%d\n", dim(padRight(string(n.Kind))), n.Name, n.StartLine)
				}
				fmt.Println()
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "Output as JSON")
	cmd.Flags().IntVarP(&depth, "depth", "d", 2, "Traversal depth")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().StringVar(&scope, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	return cmd
}

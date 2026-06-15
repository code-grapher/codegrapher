package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/specscore/codegrapher/indexer"
	"github.com/spf13/cobra"
)

func newUninitCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "uninit [path]",
		Short: "Remove CodeGraph from a project (deletes .codegraph/ directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := resolveArg(args)

			if !indexer.IsInitialized(projectPath) {
				printWarn(fmt.Sprintf("CodeGraph is not initialized in %s", projectPath))
				return nil
			}

			if !force {
				fmt.Print("This will permanently delete all CodeGraph data. Continue? (y/N) ")
				reader := bufio.NewReader(os.Stdin)
				answer, _ := reader.ReadString('\n')
				answer = strings.TrimSpace(answer)
				if strings.ToLower(answer) != "y" {
					printInfo("Cancelled")
					return nil
				}
			}

			if err := indexer.Uninit(projectPath); err != nil {
				printError(fmt.Sprintf("Failed to uninitialize: %s", err))
				os.Exit(1)
			}
			printSuccess(fmt.Sprintf("Removed CodeGraph from %s", projectPath))
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation prompt")
	return cmd
}

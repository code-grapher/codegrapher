package cli

import (
	"fmt"
	"os"

	"github.com/specscore/codegrapher/indexer"
	"github.com/spf13/cobra"
)

func newUnlockCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unlock [path]",
		Short: "Remove a stale lock file that is blocking indexing",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := resolveArg(args, 0)

			if !indexer.IsInitialized(projectPath) {
				printError(fmt.Sprintf("CodeGraph not initialized in %s", projectPath))
				return nil
			}

			lockPath := indexer.GetCodeGraphDir(projectPath) + "/codegraph.lock"
			if _, err := os.Stat(lockPath); os.IsNotExist(err) {
				printInfo("No lock file found — nothing to do")
				return nil
			}

			if err := os.Remove(lockPath); err != nil {
				printError(fmt.Sprintf("Failed to remove lock: %s", err))
				os.Exit(1)
			}
			printSuccess("Removed lock file. You can now run indexing again.")
			return nil
		},
	}
}

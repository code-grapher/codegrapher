package cli

import (
	"fmt"
	"os"

	"github.com/specscore/codegrapher/indexer"
	"github.com/spf13/cobra"
)

func newIndexCmd() *cobra.Command {
	var force, quiet, verbose bool

	cmd := &cobra.Command{
		Use:   "index [path]",
		Short: "Index all files in the project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := resolveArg(args, 0)

			if !indexer.IsInitialized(projectPath) {
				printError(fmt.Sprintf("CodeGraph not initialized in %s", projectPath))
				printInfo("Run \"codegraph init\" first")
				os.Exit(1)
			}

			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				printError(fmt.Sprintf("Failed to open index: %s", err))
				os.Exit(1)
			}
			defer func() { _ = idx.Close() }()

			if force {
				if err := idx.Store().Clear(); err != nil {
					printError(fmt.Sprintf("Failed to clear index: %s", err))
					os.Exit(1)
				}
				if !quiet {
					printInfo("Cleared existing index")
				}
			}

			opts := indexer.Options{}
			if quiet {
				result := idx.IndexAll(opts)
				if !result.Success {
					os.Exit(1)
				}
				return nil
			}

			if verbose {
				opts.OnProgress = verboseProgress()
			} else if isTTY() {
				sp := newSpinner()
				sp.start("Indexing…")
				opts.OnProgress = func(p indexer.IndexProgress) {
					sp.update(progressLabel(p))
				}
				defer sp.stop()
			}

			result := idx.IndexAll(opts)
			printIndexResult(result, projectPath)
			if !result.Success {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force full re-index")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress progress output")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed progress")
	return cmd
}

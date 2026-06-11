package cli

import (
	"fmt"
	"os"

	"github.com/specscore/codegrapher/indexer"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var quiet bool

	cmd := &cobra.Command{
		Use:   "sync [path]",
		Short: "Sync changes since last index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := resolveArg(args, 0)

			if !indexer.IsInitialized(projectPath) {
				if !quiet {
					printError(fmt.Sprintf("CodeGraph not initialized in %s", projectPath))
				}
				os.Exit(1)
			}

			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				if !quiet {
					printError(fmt.Sprintf("Failed to open index: %s", err))
				}
				os.Exit(1)
			}
			defer idx.Close()

			if quiet {
				idx.Sync(indexer.Options{})
				return nil
			}

			opts := indexer.Options{}
			if isTTY() {
				sp := newSpinner()
				sp.start("Syncing…")
				opts.OnProgress = func(p indexer.IndexProgress) {
					sp.update(progressLabel(p))
				}
				defer sp.stop()
			}

			result := idx.Sync(opts)
			totalChanges := result.FilesAdded + result.FilesModified + result.FilesRemoved
			if totalChanges == 0 {
				printInfo("Already up to date")
			} else {
				printSuccess(fmt.Sprintf("Synced %s changed files", formatNumber(totalChanges)))
				var details []string
				if result.FilesAdded > 0 {
					details = append(details, fmt.Sprintf("Added: %d", result.FilesAdded))
				}
				if result.FilesModified > 0 {
					details = append(details, fmt.Sprintf("Modified: %d", result.FilesModified))
				}
				if result.FilesRemoved > 0 {
					details = append(details, fmt.Sprintf("Removed: %d", result.FilesRemoved))
				}
				printInfo(fmt.Sprintf("%s — %s nodes in %s",
					joinStrings(details, ", "),
					formatNumber(result.NodesUpdated),
					formatDuration(result.DurationMs)))
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress output (for git hooks)")
	return cmd
}

func joinStrings(ss []string, sep string) string {
	s := ""
	for i, v := range ss {
		if i > 0 {
			s += sep
		}
		s += v
	}
	return s
}

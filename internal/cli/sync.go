package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/specscore/codegrapher/indexer"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	return newSyncCmdWithRunner(func(idx *indexer.Indexer, opts indexer.Options) indexer.SyncResult {
		return idx.Sync(opts)
	})
}

type syncRunner func(*indexer.Indexer, indexer.Options) indexer.SyncResult

func newSyncCmdWithRunner(runSync syncRunner) *cobra.Command {
	var quiet bool
	var initialize bool

	cmd := &cobra.Command{
		Use:   "sync [path]",
		Short: "Sync changes since last index",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := resolveArg(args)

			if !indexer.IsInitialized(projectPath) {
				if !initialize {
					if !quiet {
						printError(fmt.Sprintf("CodeGraph not initialized in %s", projectPath))
					}
					os.Exit(1)
				}
				opts := indexer.Options{}
				if isTTY() && !quiet {
					sp := newSpinner()
					sp.start("Indexing…")
					opts.OnProgress = func(p indexer.IndexProgress) {
						sp.update(progressLabel(p))
					}
					defer sp.stop()
				}
				idx, result, err := indexer.Init(projectPath, opts)
				if err != nil {
					if !quiet {
						printError(fmt.Sprintf("Failed to initialize index: %s", err))
					}
					os.Exit(1)
				}
				defer func() { _ = idx.Close() }()
				if !quiet {
					printIndexResult(result, projectPath)
				}
				if !result.Success {
					os.Exit(1)
				}
				return nil
			}

			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				if !quiet {
					printError(fmt.Sprintf("Failed to open index: %s", err))
				}
				os.Exit(1)
			}
			defer func() { _ = idx.Close() }()

			opts := indexer.Options{}
			if isTTY() && !quiet {
				sp := newSpinner()
				sp.start("Syncing…")
				opts.OnProgress = func(p indexer.IndexProgress) {
					sp.update(progressLabel(p))
				}
				defer sp.stop()
			}

			result := runSync(idx, opts)
			if result.LockUnavailable {
				return fmt.Errorf("sync did not run: writer lock is unavailable")
			}
			if len(result.Errors) > 0 {
				if !quiet {
					printErrorBreakdown(result.Errors)
					writeErrorLog(projectPath, result.Errors)
					printInfo("See .codegraph/errors.log for details")
				}
				return fmt.Errorf("sync failed for %d file(s): %s", len(result.Errors), result.Errors[0].Message)
			}
			if quiet {
				return nil
			}

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
	cmd.Flags().BoolVar(&initialize, "init", false, "Initialize and build the index when the repository is not initialized")
	return cmd
}

func joinStrings(ss []string, sep string) string {
	var s strings.Builder
	for i, v := range ss {
		if i > 0 {
			s.WriteString(sep)
		}
		s.WriteString(v)
	}
	return s.String()
}

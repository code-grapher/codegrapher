package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/model"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var jsonOut bool
	var format string
	var pathFlag string
	var scope string
	var noPending bool

	var refresh bool

	cmd := &cobra.Command{
		Use:   "status [path]",
		Short: "Show index status and statistics",
		Long: `Show index statistics and pending changes. Opens the index read-only: it never
migrates, converts or modifies it and leaves no -wal/-shm files.

By default status scans for pending changes (the one place staleness is
reported); --no-pending skips that scan and reports stats only, with JSON
pendingChanges null ("unknown", not zeros). An index that needs an upgrade (old
schema, or WAL from an earlier release) still prints what it can plus the
notice, and exits with code 3; run "codegrapher sync" or pass --refresh.
It also warns when .codegraph/ is not Git-ignored or is tracked.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var projectPath string
			if pathFlag != "" {
				projectPath = resolveArg([]string{pathFlag})
			} else {
				projectPath = resolveArg(args)
			}
			startPath := projectPath // for worktree mismatch detection

			if !indexer.IsInitialized(projectPath) {
				if wantsJSON(format, jsonOut) {
					out := map[string]any{
						"initialized": false,
						"projectPath": projectPath,
					}
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					_ = enc.Encode(out)
					return nil
				}
				printBold("\nCodeGraph Status\n")
				printInfo(fmt.Sprintf("Project: %s", projectPath))
				printWarn("Not initialized")
				printInfo("Run \"codegrapher init\" to initialize")
				return nil
			}

			if refresh {
				if _, err := refreshIndex(projectPath); err != nil {
					printError(fmt.Sprintf("Failed to refresh index: %s", err))
					return &ExitError{Code: 1}
				}
			}
			// Read-only open that tolerates an index needing upgrade, so status can
			// still report what it can alongside the upgrade notice.
			idx, err := indexer.OpenReadOnly(projectPath, indexer.Options{AllowStale: true})
			if err != nil {
				printError(fmt.Sprintf("Failed to open index: %s", err))
				return &ExitError{Code: ExitCodeFor(err)}
			}
			defer func() { _ = idx.Close() }()
			upgrade := idx.UpgradeNeeded()

			q := NewStoreQuerier(idx.StoresFiltered(splitCSV(scope))...)
			status, err := q.Status(projectPath)
			if err != nil {
				if upgrade == nil {
					printError(fmt.Sprintf("Failed to get status: %s", err))
					return &ExitError{Code: 1}
				}
				// An old schema may lack tables status reads: report the notice only.
				status = &StatusResult{NodesByKind: map[model.NodeKind]int{}}
			}
			status.Initialized = true
			status.ProjectPath = projectPath
			status.PendingChanges = nil
			status.Warnings = indexer.CheckDataDirIgnored(projectPath)
			if upgrade != nil {
				status.UpgradeNeeded = upgrade.Error()
			} else if !noPending {
				changed := idx.GetChangedFiles()
				status.PendingChanges = &PendingChanges{
					Added:    len(changed.Added),
					Modified: len(changed.Modified),
					Removed:  len(changed.Removed),
				}
			}

			// Worktree mismatch detection.
			mismatch := indexer.DetectWorktreeIndexMismatch(startPath, projectPath)
			if mismatch != nil {
				status.WorktreeMismatch = map[string]string{
					"worktreeRoot": mismatch.WorktreeRoot,
					"indexRoot":    mismatch.IndexRoot,
				}
			}

			if wantsJSON(format, jsonOut) {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(status); err != nil {
					return err
				}
				return statusExit(upgrade)
			}

			printBold("\nCodeGraph Status\n")
			fmt.Println(cyan("Project:"), projectPath)
			if mismatch != nil {
				printWarn(indexer.WorktreeMismatchWarning(*mismatch))
			}
			fmt.Println()

			printBold("Index Statistics:")
			fmt.Printf("  Files:     %s\n", formatNumber(status.FileCount))
			fmt.Printf("  Nodes:     %s\n", formatNumber(status.NodeCount))
			fmt.Printf("  Edges:     %s\n", formatNumber(status.EdgeCount))
			fmt.Printf("  DB Size:   %.2f MB\n", float64(status.DBSizeBytes)/1024/1024)
			fmt.Printf("  Backend:   %s\n", status.Backend)
			fmt.Printf("  Journal:   %s\n", status.JournalMode)
			fmt.Println()

			printBold("Nodes by Kind:")
			type kv struct {
				k string
				v int
			}
			var kvs []kv
			for k, v := range status.NodesByKind {
				kvs = append(kvs, kv{string(k), v})
			}
			sort.Slice(kvs, func(i, j int) bool { return kvs[i].v > kvs[j].v })
			for _, kv := range kvs {
				if kv.v > 0 {
					fmt.Printf("  %-15s %s\n", kv.k, formatNumber(kv.v))
				}
			}
			fmt.Println()

			printBold("Files by Language:")
			for _, lang := range status.Languages {
				fmt.Printf("  %s\n", lang)
			}
			fmt.Println()

			printStatusFooter(status)
			return statusExit(upgrade)
		},
	}

	addJSONOutputFlags(cmd, &format, &jsonOut)
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	cmd.Flags().StringVar(&scope, "scope", "", "Comma-separated scope keys to query (default: all scopes)")
	cmd.Flags().BoolVar(&noPending, "no-pending", false, "Skip the pending-change scan and report stats only (JSON pendingChanges is null)")
	addRefreshFlag(cmd, &refresh)
	return cmd
}

// statusExit turns an upgrade notice into the distinct exit code.
func statusExit(upgrade error) error {
	if upgrade != nil {
		return &ExitError{Code: ExitNeedsUpgrade}
	}
	return nil
}

// printStatusFooter prints the upgrade notice, pending changes and warnings.
func printStatusFooter(status *StatusResult) {
	if status.UpgradeNeeded != "" {
		printWarn(status.UpgradeNeeded)
		printInfo("Run \"codegrapher sync\" (or pass --refresh to any read command) to upgrade the index")
		fmt.Println()
	} else if status.PendingChanges == nil {
		printInfo("Pending changes not checked (--no-pending)")
		fmt.Println()
	} else if total := status.PendingChanges.Added + status.PendingChanges.Modified + status.PendingChanges.Removed; total > 0 {
		printBold("Pending Changes:")
		if status.PendingChanges.Added > 0 {
			fmt.Printf("  Added:     %d files\n", status.PendingChanges.Added)
		}
		if status.PendingChanges.Modified > 0 {
			fmt.Printf("  Modified:  %d files\n", status.PendingChanges.Modified)
		}
		if status.PendingChanges.Removed > 0 {
			fmt.Printf("  Removed:   %d files\n", status.PendingChanges.Removed)
		}
		printInfo("Run \"codegrapher sync\" to update the index")
		fmt.Println()
	} else {
		printSuccess("Index is up to date")
		fmt.Println()
	}
	printWarnings(status.Warnings)
}

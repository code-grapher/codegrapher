package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/specscore/codegrapher/indexer"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var jsonOut bool
	var pathFlag string

	cmd := &cobra.Command{
		Use:   "status [path]",
		Short: "Show index status and statistics",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var projectPath string
			if pathFlag != "" {
				projectPath = resolveArg([]string{pathFlag}, 0)
			} else {
				projectPath = resolveArg(args, 0)
			}
			startPath := projectPath // for worktree mismatch detection

			if !indexer.IsInitialized(projectPath) {
				if jsonOut {
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
				printInfo("Run \"codegraph init\" to initialize")
				return nil
			}

			idx, err := indexer.Open(projectPath, indexer.Options{})
			if err != nil {
				printError(fmt.Sprintf("Failed to open index: %s", err))
				os.Exit(1)
			}
			defer idx.Close()

			q := NewStoreQuerier(idx.Store())
			status, err := q.Status(projectPath)
			if err != nil {
				printError(fmt.Sprintf("Failed to get status: %s", err))
				os.Exit(1)
			}

			// Populate pending changes from indexer.
			changed := idx.GetChangedFiles()
			status.PendingChanges = PendingChanges{
				Added:    len(changed.Added),
				Modified: len(changed.Modified),
				Removed:  len(changed.Removed),
			}

			// Worktree mismatch detection.
			mismatch := indexer.DetectWorktreeIndexMismatch(startPath, projectPath)
			if mismatch != nil {
				status.WorktreeMismatch = map[string]string{
					"worktreeRoot": mismatch.WorktreeRoot,
					"indexRoot":    mismatch.IndexRoot,
				}
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(status); err != nil {
					return err
				}
				return nil
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

			totalChanges := status.PendingChanges.Added + status.PendingChanges.Modified + status.PendingChanges.Removed
			if totalChanges > 0 {
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
				printInfo("Run \"codegraph sync\" to update the index")
			} else {
				printSuccess("Index is up to date")
			}
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "Output as JSON")
	cmd.Flags().StringVarP(&pathFlag, "path", "p", "", "Project path")
	return cmd
}

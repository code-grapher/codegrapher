package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/scope"
	"github.com/specscore/codegrapher/store"
	"github.com/specscore/codegrapher/trace"
	"github.com/spf13/cobra"
)

func newTraceCmd() *cobra.Command {
	var root string
	var format string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "trace <spec-reference>",
		Short: "Show accepted SpecScore source links for a feature, REQ, AC, or scenario",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := root
			if projectPath == "" {
				projectPath = resolveArg(nil)
			} else {
				projectPath = resolveArg([]string{projectPath})
			}
			if !indexer.IsInitialized(projectPath) {
				return fmt.Errorf("no codegraph index found at %s — run 'codegrapher init' first", projectPath)
			}
			reg, err := indexer.OpenRegistry(projectPath)
			if err != nil {
				return fmt.Errorf("trace: open index: %w", err)
			}
			defer func() { _ = reg.Close() }()
			projection, err := reg.Store(scope.Scope{Language: "trace", Version: "1"})
			if err != nil {
				return fmt.Errorf("trace: open projection: %w", err)
			}
			stores := reg.Stores()
			var sourceStores = make([]*store.Store, 0, len(stores))
			for sc, st := range stores {
				if sc.Language != "trace" {
					sourceStores = append(sourceStores, st)
				}
			}
			if revision, _ := projection.GetMetadata("trace_indexed_revision"); revision == "" {
				if err := trace.Index(projectPath, sourceStores, projection); err != nil {
					return err
				}
			}
			result, err := trace.Query(args[0], projection, sourceStores, projectPath)
			if err != nil {
				return err
			}
			if wantsJSON(format, jsonOut) {
				return json.NewEncoder(os.Stdout).Encode(result)
			}
			fmt.Printf("%s (%s)\n", result.Reference, result.IndexedRevision)
			fmt.Printf("implements: %d\nverifies: %d\nreferences: %d\n", len(result.Implements), len(result.Verifies), len(result.References))
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Repository root (default: nearest initialized project)")
	addJSONOutputFlags(cmd, &format, &jsonOut)
	return cmd
}

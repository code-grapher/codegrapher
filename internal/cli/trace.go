package cli

import (
	"encoding/json"
	"errors"
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

	var refresh bool

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
			idx, err := openIndexForRead(projectPath, refresh)
			if err != nil {
				return fmt.Errorf("trace: open index: %w", err)
			}
			defer func() { _ = idx.Close() }()
			projection, sourceStores, err := splitTraceStores(idx.Registry().Stores())
			if err != nil {
				return err
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
	addRefreshFlag(cmd, &refresh)
	return cmd
}

// splitTraceStores separates the SpecScore trace projection from the source
// stores. The command is read-only, so a missing or never-built projection is
// reported instead of built on demand.
func splitTraceStores(stores map[scope.Scope]*store.Store) (*store.Store, []*store.Store, error) {
	var projection *store.Store
	sourceStores := make([]*store.Store, 0, len(stores))
	for sc, st := range stores {
		if sc.Language == "trace" {
			projection = st
			continue
		}
		sourceStores = append(sourceStores, st)
	}
	// indexTrace stamps indexed_with_version once the projection is complete;
	// trace_indexed_revision is empty for a built projection of a non-Git tree.
	if projection != nil {
		if built, _ := projection.GetMetadata("indexed_with_version"); built != "" {
			return projection, sourceStores, nil
		}
	}
	return nil, nil, errors.New("trace projection is not built; run `codegrapher sync` or pass --refresh")
}

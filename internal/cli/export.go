package cli

import (
	"fmt"
	"path/filepath"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/snapshot"
	"github.com/spf13/cobra"
)

func newExportCmd() *cobra.Command {
	var outDir string

	cmd := &cobra.Command{
		Use:   "export [path]",
		Short: "Export the index as INGR snapshot files",
		Long: `Export the codegrapher index to a directory of INGR snapshot files.

Writes: nodes.ingr, edges.ingr, files.ingr, project_metadata.ingr

The files are byte-deterministic: two exports of the same code tree
produce identical output regardless of when or where they ran.

Default output directory: <project-root>/codegraph/
Use --out to override.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := resolveArg(args, 0)

			if !indexer.IsInitialized(projectPath) {
				return fmt.Errorf("no codegraph index found at %s — run 'codegrapher init' first", projectPath)
			}

			dbPath := indexer.DatabasePath(projectPath)

			out := outDir
			if out == "" {
				out = filepath.Join(projectPath, snapshot.DefaultSnapshotDir)
			}

			printInfo(fmt.Sprintf("Exporting index from %s → %s", dbPath, out))

			if err := snapshot.Export(dbPath, out, projectPath); err != nil {
				return fmt.Errorf("export failed: %w", err)
			}

			printSuccess(fmt.Sprintf("Snapshot written to %s", out))
			printInfo("nodes/nodes.ingr, edges/edges.ingr, files/files.ingr, project_metadata/project_metadata.ingr")
			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "", "Output directory (default: <project-root>/"+snapshot.DefaultSnapshotDir+")")
	return cmd
}

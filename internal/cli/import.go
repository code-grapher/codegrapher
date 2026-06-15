package cli

import (
	"fmt"
	"path/filepath"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/snapshot"
	"github.com/spf13/cobra"
)

func newImportCmd() *cobra.Command {
	var inDir string

	cmd := &cobra.Command{
		Use:   "import [path]",
		Short: "Import an INGR snapshot into the local index store",
		Long: `Import INGR snapshot files into the codegrapher index store.

Reads: nodes.ingr, edges.ingr, files.ingr, project_metadata.ingr

The existing database is replaced. Run 'codegrapher sync' afterward
to reconcile any drift between the snapshot and the working tree.

Default input directory: <project-root>/codegraph/
Use --in to override.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := resolveArg(args)

			in := inDir
			if in == "" {
				in = filepath.Join(projectPath, snapshot.DefaultSnapshotDir)
			}

			dbPath := indexer.DatabasePath(projectPath)

			printInfo(fmt.Sprintf("Importing snapshot from %s → %s", in, dbPath))

			if err := snapshot.Import(dbPath, in); err != nil {
				return fmt.Errorf("import failed: %w", err)
			}

			printSuccess("Snapshot imported")
			printInfo("Run 'codegrapher sync' to reconcile with the working tree")
			return nil
		},
	}

	cmd.Flags().StringVar(&inDir, "in", "", "Input directory (default: <project-root>/"+snapshot.DefaultSnapshotDir+")")
	return cmd
}

package cli

import (
	"github.com/spf13/cobra"
)

// Version is the CLI version, stamped at build time via -ldflags.
var Version = "0.1.0"

// NewRootCmd builds and returns the root Cobra command with all sub-commands
// attached. It does NOT call Execute() — the caller does.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "codegrapher",
		Short: "Code intelligence and knowledge graph for any codebase",
		Long: `codegrapher builds and queries a SQLite knowledge graph of every symbol,
edge, and file in a codebase. Use it to search for symbols, trace call
chains, analyse blast radius, and keep the index in sync.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newInitCmd(),
		newUninitCmd(),
		newIndexCmd(),
		newSyncCmd(),
		newStatusCmd(),
		newQueryCmd(),
		newFilesCmd(),
		newCallersCmd(),
		newCalleesCmd(),
		newImpactCmd(),
		newUnlockCmd(),
		newVersionCmd(),
		newAffectedCmd(),
		newServeCmd(),
		newExportCmd(),
		newImportCmd(),
	)

	return root
}

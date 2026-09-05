package cli

import (
	"github.com/spf13/cobra"
	"github.com/strongo/buildinfo"
	"github.com/strongo/buildinfo/cobracmd"
)

// NewRootCmd builds and returns the root Cobra command with all sub-commands
// attached. It does NOT call Execute() — the caller does.
func NewRootCmd() *cobra.Command {
	info := buildinfo.Get("codegrapher")

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
		newAffectedCmd(),
		newServeCmd(),
		newExportCmd(),
		newImportCmd(),
		newCoverageCmd(),
		newTraceCmd(),
	)
	cobracmd.WireCobra(root, info)

	return root
}

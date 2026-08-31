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
		Version:       info.Short(),
	}
	// Cobra's default --version template decorates the value (e.g.
	// "codegrapher version 0.1.4\n"). Override it so `codegrapher --version`
	// prints exactly the bare semver, matching buildinfo.Info.Short().
	root.SetVersionTemplate("{{.Version}}\n")

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
		cobracmd.VersionCommand(info),
		newAffectedCmd(),
		newServeCmd(),
		newExportCmd(),
		newImportCmd(),
		newCoverageCmd(),
	)

	return root
}

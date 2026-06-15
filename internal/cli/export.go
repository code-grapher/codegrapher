package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/snapshot"
	"github.com/spf13/cobra"
)

func newExportCmd() *cobra.Command {
	var outDir string
	var ref string
	var report bool

	cmd := &cobra.Command{
		Use:   "export [path]",
		Short: "Export the index as per-scope, compressed INGR snapshot files",
		Long: `Export the codegrapher index to per-(language,version) INGR snapshots.

For each scope writes, under <out>/{language}/{version}/{name}/:
  {name}.ingr.zst and {name}.ingr.gz   (name ∈ nodes,edges,files,project_metadata)
plus <out>/manifest.json describing the available scopes.

Files are compressed-only and byte-deterministic: two exports of the same
code tree produce identical output regardless of when or where they ran.

Default output directory: <project-root>/codegraph/
Use --out to override and --ref to set the git ref segment (default: the
current branch, falling back to HEAD).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectPath := resolveArg(args)

			if !indexer.IsInitialized(projectPath) {
				return fmt.Errorf("no codegraph index found at %s — run 'codegrapher init' first", projectPath)
			}

			out := outDir
			if out == "" {
				out = filepath.Join(projectPath, snapshot.DefaultSnapshotDir)
			}
			if ref == "" {
				ref = detectGitRef(projectPath)
			}

			reg, err := indexer.OpenRegistry(projectPath)
			if err != nil {
				return fmt.Errorf("export failed: %w", err)
			}
			defer func() { _ = reg.Close() }()

			printInfo(fmt.Sprintf("Exporting index → %s (ref %s)", out, ref))

			manifest, sizes, err := snapshot.ExportScoped(reg.Stores(), out, ref)
			if err != nil {
				return fmt.Errorf("export failed: %w", err)
			}

			printSuccess(fmt.Sprintf("Snapshot written to %s", out))
			for _, sc := range manifest.Scopes {
				printInfo(fmt.Sprintf("  %s — %d nodes, %d files, %d edges",
					sc.Key, sc.Counts.Nodes, sc.Counts.Files, sc.Counts.Edges))
			}
			if report {
				printSizeReport(sizes)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "", "Output directory (default: <project-root>/"+snapshot.DefaultSnapshotDir+")")
	cmd.Flags().StringVar(&ref, "ref", "", "Git ref segment (default: current branch, else HEAD)")
	cmd.Flags().BoolVar(&report, "report", false, "Print an original/zstd/gzip size table per recordset")
	return cmd
}

// printSizeReport prints a per-recordset compression table plus totals.
func printSizeReport(sizes []snapshot.RecordsetSize) {
	if len(sizes) == 0 {
		return
	}
	fmt.Printf("\n%-26s %-18s %10s %10s %8s %10s %8s\n",
		"scope", "recordset", "original", "zstd", "z-ratio", "gzip", "g-ratio")
	var totO, totZ, totG int
	for _, s := range sizes {
		totO += s.Original
		totZ += s.Zstd
		totG += s.Gzip
		fmt.Printf("%-26s %-18s %10d %10d %7.1f%% %10d %7.1f%%\n",
			s.Scope, s.Name, s.Original, s.Zstd, pct(s.Zstd, s.Original), s.Gzip, pct(s.Gzip, s.Original))
	}
	fmt.Printf("%-26s %-18s %10d %10d %7.1f%% %10d %7.1f%%\n",
		"TOTAL", "", totO, totZ, pct(totZ, totO), totG, pct(totG, totO))
}

func pct(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

// detectGitRef returns the current branch name, or "HEAD" when it cannot be
// determined (detached HEAD, non-git directory, or git unavailable).
func detectGitRef(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	branch := strings.TrimSpace(string(out))
	if err != nil || branch == "" || branch == "HEAD" {
		return "HEAD"
	}
	return branch
}

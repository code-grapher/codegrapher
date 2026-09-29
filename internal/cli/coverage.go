package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/specscore/codegrapher/coverage"
	"github.com/specscore/codegrapher/indexer"
	"github.com/specscore/codegrapher/scope"
	"github.com/specscore/codegrapher/store"
	"github.com/spf13/cobra"
)

func newCoverageCmd() *cobra.Command {
	var ref string
	var root string
	var outDir string
	var merge bool

	cmd := &cobra.Command{
		Use:   "coverage <profile.out> [profile.out...]",
		Short: "Ingest a Go coverage profile into the index",
		Long: `Ingest one or more Go coverprofiles from the indexed checkout. Hits
compose by exact block; modes and layouts must agree. Go NumStmt determines
the authoritative statement percentage. --merge accumulates a later focused
batch only when stored source hash, mode, layout, and ref agree.

Resolves each profile (module) path to the repo-relative file stored in the
graph, computes per-file line coverage and innermost per-function counts,
and stamps each record with the file's current content_hash. Profiles older
than their source files are refused; regenerate after source changes. Files
with no matching indexed file are reported and skipped (non-fatal).

With --out, also writes coverage.ingr and node_coverage.ingr to that
directory — the recordsets the CLI uploads to the server.`,
		Args: cobra.MinimumNArgs(1),
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
			if ref == "" {
				ref = detectGitRef(projectPath)
			}

			reg, err := indexer.OpenRegistry(projectPath)
			if err != nil {
				return fmt.Errorf("coverage failed: %w", err)
			}
			defer func() { _ = reg.Close() }()

			opts := coverage.Options{Ref: ref, Root: projectPath, Merge: merge}
			for _, profilePath := range args {
				info, err := os.Stat(profilePath)
				if err != nil {
					return fmt.Errorf("coverage: stat profile %s: %w", profilePath, err)
				}
				stamp := info.ModTime().UnixMilli()
				if opts.ProfileModifiedAt == 0 || stamp < opts.ProfileModifiedAt {
					opts.ProfileModifiedAt = stamp
				}
			}
			ing := coverage.NewIngestor()
			ingestStore := func(s *store.Store, runOpts coverage.Options) (coverage.Summary, error) {
				var readers []io.Reader
				var opened []*os.File
				for _, profilePath := range args {
					f, err := os.Open(profilePath)
					if err != nil {
						for _, o := range opened {
							_ = o.Close()
						}
						return coverage.Summary{}, fmt.Errorf("coverage: open profile %s: %w", profilePath, err)
					}
					opened = append(opened, f)
					readers = append(readers, f)
				}
				sum, err := ing.IngestMany(context.Background(), s, readers, runOpts)
				for _, o := range opened {
					_ = o.Close()
				}
				return sum, err
			}
			preflight := opts
			preflight.ValidateOnly = true
			for _, s := range reg.Stores() {
				if _, err := ingestStore(s, preflight); err != nil {
					return fmt.Errorf("coverage: preflight: %w", err)
				}
			}
			var matched, skipped, linesCov, linesUnc int
			var stmtCov, stmtUnc int
			for _, s := range reg.Stores() {
				sum, err := ingestStore(s, opts)
				if err != nil {
					return fmt.Errorf("coverage: ingest: %w", err)
				}
				matched += sum.FilesMatched
				skipped += sum.FilesSkipped
				linesCov += sum.LinesCovered
				linesUnc += sum.LinesUncovered
				stmtCov += sum.StatementsCovered
				stmtUnc += sum.StatementsUncovered
			}

			if matched == 0 {
				printWarn("No profile files matched indexed files — nothing ingested")
			}
			printSuccess(fmt.Sprintf("Coverage ingested (ref %s)", ref))
			printInfo(fmt.Sprintf("  %d files matched, %d unmatched (skipped)", matched, skipped))
			printInfo(fmt.Sprintf("  %d/%d statements covered (%.1f%%); %d lines hit, %d missed",
				stmtCov, stmtCov+stmtUnc, coverage.Pct(stmtCov, stmtUnc), linesCov, linesUnc))

			if outDir != "" {
				if err := writeCoverageRecordsets(reg.Stores(), outDir); err != nil {
					return fmt.Errorf("coverage: write recordsets: %w", err)
				}
				printInfo(fmt.Sprintf("  Wrote coverage.ingr + node_coverage.ingr → %s", outDir))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ref, "ref", "", "Git ref segment (default: current branch, else HEAD)")
	cmd.Flags().StringVar(&root, "root", "", "Repository root for module-path resolution (default: project path)")
	cmd.Flags().StringVar(&outDir, "out", "", "Also write coverage.ingr + node_coverage.ingr to this directory")
	cmd.Flags().BoolVar(&merge, "merge", false, "Accumulate exact block hits from prior coverage of the same indexed source, mode, layout, and ref")
	cmd.AddCommand(newCoverageTargetsCmd())
	return cmd
}

// writeCoverageRecordsets aggregates coverage from every scope store and writes
// coverage.ingr + node_coverage.ingr to outDir. Aggregation across stores is
// safe: file paths and node ids are globally unique. Encode* sorts records, so
// the merged output stays deterministic.
func writeCoverageRecordsets(stores map[scope.Scope]*store.Store, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Stable iteration order over scopes (cosmetic; Encode* re-sorts anyway).
	scopes := make([]scope.Scope, 0, len(stores))
	for sc := range stores {
		scopes = append(scopes, sc)
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].Key() < scopes[j].Key() })

	var fileRecs []coverage.FileCoverage
	var nodeRecs []coverage.NodeCoverage
	for _, sc := range scopes {
		fc, err := coverage.FileCoverageFromStore(stores[sc])
		if err != nil {
			return err
		}
		nc, err := coverage.NodeCoverageFromStore(stores[sc])
		if err != nil {
			return err
		}
		fileRecs = append(fileRecs, fc...)
		nodeRecs = append(nodeRecs, nc...)
	}

	fcFile, err := os.Create(filepath.Join(outDir, coverage.RecordsetCoverage+".ingr"))
	if err != nil {
		return err
	}
	defer func() { _ = fcFile.Close() }()
	if err := coverage.EncodeFileCoverage(fcFile, fileRecs); err != nil {
		return err
	}

	ncFile, err := os.Create(filepath.Join(outDir, coverage.RecordsetNodeCoverage+".ingr"))
	if err != nil {
		return err
	}
	defer func() { _ = ncFile.Close() }()
	return coverage.EncodeNodeCoverage(ncFile, nodeRecs)
}

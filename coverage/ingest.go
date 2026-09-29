package coverage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/specscore/codegrapher/gomod"
	"github.com/specscore/codegrapher/store"
)

// marshalRanges encodes the RLE ranges as the JSON stored in coverage.ranges.
func marshalRanges(ranges []Range) (string, error) {
	if ranges == nil {
		ranges = []Range{}
	}
	b, err := json.Marshal(ranges)
	if err != nil {
		return "", fmt.Errorf("coverage: marshal ranges: %w", err)
	}
	return string(b), nil
}

// ingestor is the real Ingestor: it parses a Go coverprofile, resolves each
// profile (module) path to the repo-relative path stored in the graph, computes
// per-file line coverage and innermost per-function counts, and writes them to
// the store stamped with each file's current content_hash and the run clock.
type ingestor struct{}

// Ingest implements Ingestor. Profile files that resolve to no indexed file are
// skipped (counted in Summary.FilesSkipped); the caller decides whether to warn.
func (ingestor) Ingest(ctx context.Context, st *store.Store, profile io.Reader, opts Options) (Summary, error) {
	return ingestor{}.IngestMany(ctx, st, []io.Reader{profile}, opts)
}

func (ingestor) IngestMany(ctx context.Context, st *store.Store, profiles []io.Reader, opts Options) (Summary, error) {
	files, _, err := mergeProfiles(profiles...)
	if err != nil {
		return Summary{}, err
	}

	modulePath, err := modulePath(opts.Root)
	if err != nil {
		return Summary{}, err
	}

	now := opts.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	runAt := now()

	var (
		fileRows     []store.CoverageRow
		nodeRows     []store.NodeCoverageRow
		summary      Summary
		totalCov     int
		totalUnc     int
		totalStmtCov int
		totalStmtUnc int
	)

	for _, pf := range files {
		if err := ctx.Err(); err != nil {
			return Summary{}, err
		}
		repoPath := resolveRepoPath(pf.Name, modulePath)
		fr, err := st.GetFileByPath(repoPath)
		if err != nil {
			return Summary{}, fmt.Errorf("coverage: lookup file %s: %w", repoPath, err)
		}
		if fr == nil {
			summary.FilesSkipped++
			continue
		}
		if opts.Root != "" {
			sourcePath := filepath.Join(opts.Root, filepath.FromSlash(repoPath))
			data, readErr := os.ReadFile(sourcePath)
			if readErr == nil {
				digest := sha256.Sum256(data)
				if hex.EncodeToString(digest[:]) != fr.ContentHash {
					return Summary{}, fmt.Errorf("coverage: %s: indexed source differs from checkout; run codegrapher sync before ingest", repoPath)
				}
				if opts.ProfileModifiedAt > 0 {
					info, statErr := os.Stat(sourcePath)
					if statErr != nil {
						return Summary{}, statErr
					}
					if info.ModTime().UnixMilli() > opts.ProfileModifiedAt {
						return Summary{}, fmt.Errorf("coverage: %s: source is newer than a supplied profile; regenerate profiles from this checkout", repoPath)
					}
				}
			} else if !os.IsNotExist(readErr) || opts.ProfileModifiedAt > 0 {
				return Summary{}, fmt.Errorf("coverage: read indexed source %s: %w", repoPath, readErr)
			}
		}
		if opts.Merge {
			old, err := st.GetCoverageByFile(repoPath)
			if err != nil {
				return Summary{}, err
			}
			if old != nil {
				if old.ContentHash != fr.ContentHash {
					return Summary{}, fmt.Errorf("coverage: %s: stored coverage is stale after source changed; re-ingest without --merge", repoPath)
				}
				if old.Mode != pf.Mode {
					return Summary{}, fmt.Errorf("coverage: %s: stored mode %q differs from incoming %q; re-ingest without --merge", repoPath, old.Mode, pf.Mode)
				}
				if old.Ref != opts.Ref {
					return Summary{}, fmt.Errorf("coverage: %s: stored ref %q differs from incoming %q; re-ingest without --merge", repoPath, old.Ref, opts.Ref)
				}
				var blocks []Block
				if err := json.Unmarshal([]byte(old.Blocks), &blocks); err != nil {
					return Summary{}, fmt.Errorf("coverage: %s: stored blocks: %w", repoPath, err)
				}
				if len(blocks) == 0 {
					return Summary{}, fmt.Errorf("coverage: %s: stored coverage has no exact blocks; re-ingest without --merge", repoPath)
				}
				if err := mergeBlocks(pf.Blocks, blocks); err != nil {
					return Summary{}, fmt.Errorf("coverage: %s: %w", repoPath, err)
				}
				pf.Covered, pf.Uncovered = lineSets(pf.Blocks)
			}
		}
		summary.FilesMatched++

		cov := len(pf.Covered)
		unc := len(pf.Uncovered)
		totalCov += cov
		totalUnc += unc
		stmtCov, stmtUnc := statementTotals(pf.Blocks)
		totalStmtCov += stmtCov
		totalStmtUnc += stmtUnc
		blocksJSON, err := json.Marshal(pf.Blocks)
		if err != nil {
			return Summary{}, err
		}

		ranges := rangesFromStates(LineStates(pf.Blocks))
		rangesJSON, err := marshalRanges(ranges)
		if err != nil {
			return Summary{}, err
		}
		fileRows = append(fileRows, store.CoverageRow{
			FilePath:    repoPath,
			ContentHash: fr.ContentHash,
			Mode:        pf.Mode,
			Ranges:      rangesJSON,
			Blocks:      string(blocksJSON), StatementsCovered: stmtCov, StatementsUncovered: stmtUnc, Ref: opts.Ref,
			LinesCovered:   cov,
			LinesUncovered: unc,
			PctCovered:     Pct(stmtCov, stmtUnc),
			RunAt:          runAt,
		})

		nodes, err := st.GetNodesByFile(repoPath)
		if err != nil {
			return Summary{}, fmt.Errorf("coverage: nodes for %s: %w", repoPath, err)
		}
		lineCounts := attributeLines(nodes, pf.Covered, pf.Uncovered)
		stmtCounts := attributeBlocks(nodes, pf.Blocks)
		lineByID := map[string]nodeLineCount{}
		for _, c := range lineCounts {
			lineByID[c.NodeID] = c
		}
		stmtByID := map[string]nodeLineCount{}
		for _, c := range stmtCounts {
			stmtByID[c.NodeID] = c
		}
		allIDs := map[string]bool{}
		for id := range lineByID {
			allIDs[id] = true
		}
		for id := range stmtByID {
			allIDs[id] = true
		}
		for id := range allIDs {
			nc, sc := lineByID[id], stmtByID[id]
			nodeRows = append(nodeRows, store.NodeCoverageRow{
				NodeID:            id,
				ContentHash:       fr.ContentHash,
				LinesCovered:      nc.Covered,
				LinesUncovered:    nc.Uncovered,
				StatementsCovered: sc.Covered, StatementsUncovered: sc.Uncovered, Ref: opts.Ref,
				PctCovered: Pct(sc.Covered, sc.Uncovered),
				RunAt:      runAt,
			})
		}
	}

	if !opts.ValidateOnly {
		if err := st.ReplaceCoverage(fileRows, nodeRows); err != nil {
			return Summary{}, fmt.Errorf("coverage: write coverage: %w", err)
		}
	}

	summary.LinesCovered = totalCov
	summary.LinesUncovered = totalUnc
	summary.StatementsCovered = totalStmtCov
	summary.StatementsUncovered = totalStmtUnc
	summary.PctCovered = Pct(totalStmtCov, totalStmtUnc)
	return summary, nil
}

func statementTotals(blocks []Block) (covered, uncovered int) {
	for _, b := range blocks {
		if b.Hit {
			covered += b.NumStmt
		} else {
			uncovered += b.NumStmt
		}
	}
	return
}

// FileCoverageFromStore reads every per-file coverage row from st and converts
// it to []FileCoverage (decoding the stored RLE JSON). Used by the CLI and the
// snapshot exporter to emit the "coverage" recordset.
func FileCoverageFromStore(st *store.Store) ([]FileCoverage, error) {
	rows, err := st.GetAllCoverage()
	if err != nil {
		return nil, err
	}
	out := make([]FileCoverage, 0, len(rows))
	for _, r := range rows {
		var ranges []Range
		if r.Ranges != "" {
			if err := json.Unmarshal([]byte(r.Ranges), &ranges); err != nil {
				return nil, fmt.Errorf("coverage: unmarshal ranges for %s: %w", r.FilePath, err)
			}
		}
		out = append(out, FileCoverage{
			FilePath:          r.FilePath,
			ContentHash:       r.ContentHash,
			Mode:              r.Mode,
			Ranges:            ranges,
			StatementsCovered: r.StatementsCovered, StatementsUncovered: r.StatementsUncovered, Ref: r.Ref,
			LinesCovered:   r.LinesCovered,
			LinesUncovered: r.LinesUncovered,
			PctCovered:     coveragePercent(r.StatementsCovered, r.StatementsUncovered, r.LinesCovered, r.LinesUncovered),
			RunAt:          r.RunAt,
		})
		if r.Blocks != "" {
			if err := json.Unmarshal([]byte(r.Blocks), &out[len(out)-1].Blocks); err != nil {
				return nil, fmt.Errorf("coverage: unmarshal blocks for %s: %w", r.FilePath, err)
			}
		}
	}
	return out, nil
}

// NodeCoverageFromStore reads every per-node coverage row from st and converts
// it to []NodeCoverage. Used by the CLI and snapshot exporter to emit the
// "node_coverage" recordset.
func NodeCoverageFromStore(st *store.Store) ([]NodeCoverage, error) {
	rows, err := st.GetAllNodeCoverage()
	if err != nil {
		return nil, err
	}
	out := make([]NodeCoverage, 0, len(rows))
	for _, r := range rows {
		out = append(out, NodeCoverage{
			NodeID:            r.NodeID,
			ContentHash:       r.ContentHash,
			LinesCovered:      r.LinesCovered,
			LinesUncovered:    r.LinesUncovered,
			StatementsCovered: r.StatementsCovered, StatementsUncovered: r.StatementsUncovered, Ref: r.Ref,
			PctCovered: coveragePercent(r.StatementsCovered, r.StatementsUncovered, r.LinesCovered, r.LinesUncovered),
			RunAt:      r.RunAt,
		})
	}
	return out, nil
}

// modulePath reads the module path from root/go.mod. A missing or unparseable
// go.mod is not fatal — paths then resolve by best-effort suffix matching only
// (modulePath == "").
func modulePath(root string) (string, error) {
	if root == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("coverage: read go.mod: %w", err)
	}
	mf, err := gomod.Parse("go.mod", data)
	if err != nil {
		return "", nil
	}
	return mf.Module, nil
}

// resolveRepoPath converts a profile file name (a package import path ending in
// the file, e.g. "example.com/m/pkg/f.go") to the repo-relative path stored in
// the graph (e.g. "pkg/f.go") by stripping the module path prefix. When the
// module path is unknown or does not prefix the name, the name is returned
// unchanged (already repo-relative, or resolved by the store lookup failing).
func resolveRepoPath(profileName, modulePath string) string {
	name := path.Clean(profileName)
	if modulePath != "" {
		if name == modulePath {
			return name
		}
		if rel, ok := strings.CutPrefix(name, modulePath+"/"); ok {
			return rel
		}
	}
	return name
}

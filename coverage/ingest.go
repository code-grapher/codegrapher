package coverage

import (
	"context"
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
	files, _, err := parseProfiles(profile)
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
		fileRows []store.CoverageRow
		nodeRows []store.NodeCoverageRow
		summary  Summary
		totalCov int
		totalUnc int
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
		summary.FilesMatched++

		cov := len(pf.Covered)
		unc := len(pf.Uncovered)
		totalCov += cov
		totalUnc += unc

		ranges := encodeRanges(pf.Covered, pf.Uncovered)
		rangesJSON, err := marshalRanges(ranges)
		if err != nil {
			return Summary{}, err
		}
		fileRows = append(fileRows, store.CoverageRow{
			FilePath:       repoPath,
			ContentHash:    fr.ContentHash,
			Mode:           pf.Mode,
			Ranges:         rangesJSON,
			LinesCovered:   cov,
			LinesUncovered: unc,
			PctCovered:     Pct(cov, unc),
			RunAt:          runAt,
		})

		nodes, err := st.GetNodesByFile(repoPath)
		if err != nil {
			return Summary{}, fmt.Errorf("coverage: nodes for %s: %w", repoPath, err)
		}
		for _, nc := range attributeLines(nodes, pf.Covered, pf.Uncovered) {
			nodeRows = append(nodeRows, store.NodeCoverageRow{
				NodeID:         nc.NodeID,
				ContentHash:    fr.ContentHash,
				LinesCovered:   nc.Covered,
				LinesUncovered: nc.Uncovered,
				PctCovered:     Pct(nc.Covered, nc.Uncovered),
				RunAt:          runAt,
			})
		}
	}

	if err := st.PutCoverage(fileRows); err != nil {
		return Summary{}, fmt.Errorf("coverage: write coverage: %w", err)
	}
	if err := st.PutNodeCoverage(nodeRows); err != nil {
		return Summary{}, fmt.Errorf("coverage: write node_coverage: %w", err)
	}

	summary.LinesCovered = totalCov
	summary.LinesUncovered = totalUnc
	summary.PctCovered = Pct(totalCov, totalUnc)
	return summary, nil
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
			FilePath:       r.FilePath,
			ContentHash:    r.ContentHash,
			Mode:           r.Mode,
			Ranges:         ranges,
			LinesCovered:   r.LinesCovered,
			LinesUncovered: r.LinesUncovered,
			PctCovered:     Pct(r.LinesCovered, r.LinesUncovered),
			RunAt:          r.RunAt,
		})
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
			NodeID:         r.NodeID,
			ContentHash:    r.ContentHash,
			LinesCovered:   r.LinesCovered,
			LinesUncovered: r.LinesUncovered,
			PctCovered:     Pct(r.LinesCovered, r.LinesUncovered),
			RunAt:          r.RunAt,
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
		if rel := strings.TrimPrefix(name, modulePath+"/"); rel != name {
			return rel
		}
	}
	return name
}

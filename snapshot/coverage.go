package snapshot

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/specscore/codegrapher/coverage"
	"github.com/specscore/codegrapher/store"
)

// Coverage recordsets reuse the coverage package as the single source of truth
// for the INGR byte layout: exportCoverage/exportNodeCoverage call
// coverage.Encode*, import calls coverage.Decode*.
//
// Unlike updated_at/indexed_at (which the store re-stamps and snapshot omits),
// run_at IS part of the frozen coverage recordset — the server needs it to show
// when coverage was produced — so it is preserved verbatim through
// export→import→export, keeping the round-trip byte-identical. For the
// determinism gate, run_at is treated as volatile: comparisons normalize it
// (see parity tests) so a snapshot regenerated under a different clock still
// matches.

func exportCoverage(s *store.Store, outDir string) error {
	recs, err := coverage.FileCoverageFromStore(s)
	if err != nil {
		return err
	}
	dir := filepath.Join(outDir, coverage.RecordsetCoverage)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeCollectionDef(dir, coverageCollectionDef); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, coverage.RecordsetCoverage+".ingr"))
	if err != nil {
		return err
	}
	defer f.Close()
	return coverage.EncodeFileCoverage(f, recs)
}

func exportNodeCoverage(s *store.Store, outDir string) error {
	recs, err := coverage.NodeCoverageFromStore(s)
	if err != nil {
		return err
	}
	dir := filepath.Join(outDir, coverage.RecordsetNodeCoverage)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeCollectionDef(dir, nodeCoverageCollectionDef); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, coverage.RecordsetNodeCoverage+".ingr"))
	if err != nil {
		return err
	}
	defer f.Close()
	return coverage.EncodeNodeCoverage(f, recs)
}

// importCoverage reads coverage.ingr (if present) and writes the rows to the
// store. Absent recordset = no coverage; not an error (older snapshots).
func importCoverage(s *store.Store, path string) error {
	data, ok, err := readFileOptional(path)
	if err != nil || !ok {
		return err
	}
	recs, err := coverage.DecodeFileCoverage(bytes.NewReader(data))
	if err != nil {
		return err
	}
	rows := make([]store.CoverageRow, 0, len(recs))
	for _, r := range recs {
		rangesJSON, err := json.Marshal(r.Ranges)
		if err != nil {
			return err
		}
		rows = append(rows, store.CoverageRow{
			FilePath:       r.FilePath,
			ContentHash:    r.ContentHash,
			Mode:           r.Mode,
			Ranges:         string(rangesJSON),
			LinesCovered:   r.LinesCovered,
			LinesUncovered: r.LinesUncovered,
			PctCovered:     r.PctCovered,
			RunAt:          r.RunAt,
		})
	}
	return s.PutCoverage(rows)
}

// importNodeCoverage reads node_coverage.ingr (if present) and writes the rows.
// Rows whose node is absent are skipped by the store (FK). Absent = no error.
func importNodeCoverage(s *store.Store, path string) error {
	data, ok, err := readFileOptional(path)
	if err != nil || !ok {
		return err
	}
	recs, err := coverage.DecodeNodeCoverage(bytes.NewReader(data))
	if err != nil {
		return err
	}
	rows := make([]store.NodeCoverageRow, 0, len(recs))
	for _, r := range recs {
		rows = append(rows, store.NodeCoverageRow{
			NodeID:         r.NodeID,
			ContentHash:    r.ContentHash,
			LinesCovered:   r.LinesCovered,
			LinesUncovered: r.LinesUncovered,
			PctCovered:     r.PctCovered,
			RunAt:          r.RunAt,
		})
	}
	return s.PutNodeCoverage(rows)
}

// readFileOptional reads path; a missing file yields ok=false, nil error.
func readFileOptional(path string) (data []byte, ok bool, err error) {
	data, err = os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

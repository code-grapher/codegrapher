package coverage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/ingr-io/ingr-go/ingr"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// INGR recordset layout. Both recordsets mirror the snapshot package's
// conventions (PK is $ID, ints typed, derived/volatile-derivable fields kept
// out). PctCovered is intentionally NOT a column: it is recomputed from the
// line counts on decode, so the format stays integer-exact and deterministic.
//
// coverage      ($ID = file_path)
// node_coverage ($ID = node_id)

var fileCoverageCols = []ingr.ColDef{
	{Name: "$ID"},                          // file_path
	{Name: "content_hash"},                 //
	{Name: "mode"},                         // set | count | atomic
	{Name: "ranges"},                       // JSON array of Range
	{Name: "lines_covered", Type: "int"},   //
	{Name: "lines_uncovered", Type: "int"}, //
	{Name: "run_at", Type: "int"},          // unix ms
}

var nodeCoverageCols = []ingr.ColDef{
	{Name: "$ID"},                          // node_id
	{Name: "content_hash"},                 //
	{Name: "lines_covered", Type: "int"},   //
	{Name: "lines_uncovered", Type: "int"}, //
	{Name: "run_at", Type: "int"},          // unix ms
}

// EncodeFileCoverage writes recs as the "coverage" INGR recordset, sorted by
// file path for byte-determinism.
func EncodeFileCoverage(w io.Writer, recs []FileCoverage) error {
	sorted := append([]FileCoverage(nil), recs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].FilePath < sorted[j].FilePath })

	rw := ingr.NewRecordsWriter(w)
	if _, err := rw.WriteHeader(RecordsetCoverage, fileCoverageCols); err != nil {
		return err
	}
	records := make([]ingr.Record, 0, len(sorted))
	for _, r := range sorted {
		rangesJSON, err := json.Marshal(r.Ranges)
		if err != nil {
			return fmt.Errorf("coverage: marshal ranges for %s: %w", r.FilePath, err)
		}
		records = append(records, ingr.NewMapRecordEntry(r.FilePath, map[string]any{
			"$ID":             r.FilePath,
			"content_hash":    r.ContentHash,
			"mode":            r.Mode,
			"ranges":          string(rangesJSON),
			"lines_covered":   r.LinesCovered,
			"lines_uncovered": r.LinesUncovered,
			"run_at":          r.RunAt,
		}))
	}
	if len(records) > 0 {
		if _, err := rw.WriteRecords(0, records...); err != nil {
			return err
		}
	}
	return rw.Close()
}

// EncodeNodeCoverage writes recs as the "node_coverage" INGR recordset, sorted
// by node id for byte-determinism.
func EncodeNodeCoverage(w io.Writer, recs []NodeCoverage) error {
	sorted := append([]NodeCoverage(nil), recs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].NodeID < sorted[j].NodeID })

	rw := ingr.NewRecordsWriter(w)
	if _, err := rw.WriteHeader(RecordsetNodeCoverage, nodeCoverageCols); err != nil {
		return err
	}
	records := make([]ingr.Record, 0, len(sorted))
	for _, r := range sorted {
		records = append(records, ingr.NewMapRecordEntry(r.NodeID, map[string]any{
			"$ID":             r.NodeID,
			"content_hash":    r.ContentHash,
			"lines_covered":   r.LinesCovered,
			"lines_uncovered": r.LinesUncovered,
			"run_at":          r.RunAt,
		}))
	}
	if len(records) > 0 {
		if _, err := rw.WriteRecords(0, records...); err != nil {
			return err
		}
	}
	return rw.Close()
}

// DecodeFileCoverage reads a "coverage" INGR recordset. PctCovered is recomputed
// from the line counts.
func DecodeFileCoverage(r io.Reader) ([]FileCoverage, error) {
	rows, err := decodeRows(r)
	if err != nil {
		return nil, err
	}
	out := make([]FileCoverage, 0, len(rows))
	for _, row := range rows {
		var ranges []Range
		if s := str(row["ranges"]); s != "" {
			if err := json.Unmarshal([]byte(s), &ranges); err != nil {
				return nil, fmt.Errorf("coverage: unmarshal ranges for %q: %w", str(row["$ID"]), err)
			}
		}
		cov, unc := toInt(row["lines_covered"]), toInt(row["lines_uncovered"])
		out = append(out, FileCoverage{
			FilePath:       str(row["$ID"]),
			ContentHash:    str(row["content_hash"]),
			Mode:           str(row["mode"]),
			Ranges:         ranges,
			LinesCovered:   cov,
			LinesUncovered: unc,
			PctCovered:     Pct(cov, unc),
			RunAt:          int64(toInt(row["run_at"])),
		})
	}
	return out, nil
}

// DecodeNodeCoverage reads a "node_coverage" INGR recordset. PctCovered is
// recomputed from the line counts.
func DecodeNodeCoverage(r io.Reader) ([]NodeCoverage, error) {
	rows, err := decodeRows(r)
	if err != nil {
		return nil, err
	}
	out := make([]NodeCoverage, 0, len(rows))
	for _, row := range rows {
		cov, unc := toInt(row["lines_covered"]), toInt(row["lines_uncovered"])
		out = append(out, NodeCoverage{
			NodeID:         str(row["$ID"]),
			ContentHash:    str(row["content_hash"]),
			LinesCovered:   cov,
			LinesUncovered: unc,
			PctCovered:     Pct(cov, unc),
			RunAt:          int64(toInt(row["run_at"])),
		})
	}
	return out, nil
}

// Validate checks that data is a well-formed INGR recordset for the named
// collection ("coverage" or "node_coverage"): decodable, every record has a
// non-empty $ID and content_hash, non-negative counts, and (for coverage)
// parseable ranges with valid kinds. The server collect endpoint calls this on
// uploaded bytes before storing them.
func Validate(recordset string, data []byte) error {
	switch recordset {
	case RecordsetCoverage:
		recs, err := DecodeFileCoverage(bytesReader(data))
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.FilePath == "" {
				return fmt.Errorf("coverage: record with empty file path")
			}
			if r.ContentHash == "" {
				return fmt.Errorf("coverage: %s: empty content_hash", r.FilePath)
			}
			if r.LinesCovered < 0 || r.LinesUncovered < 0 {
				return fmt.Errorf("coverage: %s: negative line count", r.FilePath)
			}
			for _, rg := range r.Ranges {
				if rg.Kind != KindHit && rg.Kind != KindMiss {
					return fmt.Errorf("coverage: %s: bad range kind %q", r.FilePath, rg.Kind)
				}
				if rg.Start <= 0 || rg.End < rg.Start {
					return fmt.Errorf("coverage: %s: bad range [%d,%d]", r.FilePath, rg.Start, rg.End)
				}
			}
		}
		return nil
	case RecordsetNodeCoverage:
		recs, err := DecodeNodeCoverage(bytesReader(data))
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.NodeID == "" {
				return fmt.Errorf("node_coverage: record with empty node id")
			}
			if r.ContentHash == "" {
				return fmt.Errorf("node_coverage: %s: empty content_hash", r.NodeID)
			}
			if r.LinesCovered < 0 || r.LinesUncovered < 0 {
				return fmt.Errorf("node_coverage: %s: negative line count", r.NodeID)
			}
		}
		return nil
	default:
		return fmt.Errorf("coverage: unknown recordset %q", recordset)
	}
}

func decodeRows(r io.Reader) ([]map[string]any, error) {
	dec := ingr.NewDecoder(r)
	var rows []map[string]any
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("coverage: decode recordset: %w", err)
	}
	return rows, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func toInt(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		var n int64
		fmt.Sscanf(str(v), "%d", &n)
		return int(n)
	}
}

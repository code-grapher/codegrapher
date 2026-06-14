// Package coverage is the single home for Go line-coverage attribution in
// codegrapher: it turns a `go test -coverprofile` profile into per-file and
// per-function coverage records, and defines the INGR recordset format those
// records are exported/uploaded/served in.
//
// One library, several callers:
//   - the `codegrapher coverage` CLI runs Ingest locally on the same checkout
//     the tests ran on (guaranteed matching commit) and emits the recordsets;
//   - the server validates uploaded recordsets via Validate and stores them
//     next to graph data;
//   - any other consumer may call Ingest directly against an open *store.Store.
//
// Scope (see SPEC.md): Go only, line coverage only. Per-function counts are
// attributed to the INNERMOST enclosing node (non-overlapping); inclusive
// roll-ups (a function plus its nested closures) are computed by consumers via
// the graph's `contains` edges, not stored here.
package coverage

import (
	"context"
	"io"

	"github.com/specscore/codegrapher/store"
)

// Recordset names. These are the INGR collection identifiers used on disk
// ({name}.ingr), in the snapshot manifest counts, and on the wire when the CLI
// uploads to the server's collect endpoint.
const (
	RecordsetCoverage     = "coverage"
	RecordsetNodeCoverage = "node_coverage"
)

// Range is a run-length-encoded span of consecutive lines sharing one coverage
// state. Start and End are 1-indexed and inclusive. Kind is "hit" or "miss".
type Range struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Kind  string `json:"kind"`
}

const (
	KindHit  = "hit"
	KindMiss = "miss"
)

// FileCoverage is per-file line coverage from one ingest run. ContentHash is
// the file's hash at ingest time; a later mismatch against the live file marks
// this record stale (consumers keep + flag it, never drop). PctCovered is
// derived from the line counts, not stored in the recordset.
type FileCoverage struct {
	FilePath       string  `json:"filePath"`
	ContentHash    string  `json:"contentHash"`
	Mode           string  `json:"mode"` // go profile mode: set | count | atomic
	Ranges         []Range `json:"ranges"`
	LinesCovered   int     `json:"linesCovered"`
	LinesUncovered int     `json:"linesUncovered"`
	PctCovered     float64 `json:"pctCovered"`
	RunAt          int64   `json:"runAt"` // unix ms
}

// NodeCoverage is innermost-attributed line counts for one function/method
// node. Lines inside a nested closure count toward that closure, not its
// parent (non-overlapping). PctCovered is derived, not stored in the recordset.
type NodeCoverage struct {
	NodeID         string  `json:"nodeId"`
	ContentHash    string  `json:"contentHash"`
	LinesCovered   int     `json:"linesCovered"`
	LinesUncovered int     `json:"linesUncovered"`
	PctCovered     float64 `json:"pctCovered"`
	RunAt          int64   `json:"runAt"` // unix ms
}

// Options configure an Ingest run.
type Options struct {
	// Ref is the git ref/branch the profile was produced on (recorded for the
	// snapshot manifest; informational here).
	Ref string
	// Root is the repository root used to resolve profile (module) paths to the
	// repo-relative file paths stored in the graph.
	Root string
	// Now overrides the RunAt clock (unix ms). nil uses the real clock.
	Now func() int64
}

// Summary reports the outcome of an Ingest run.
type Summary struct {
	FilesMatched   int
	FilesSkipped   int // profile files with no matching indexed file
	LinesCovered   int
	LinesUncovered int
	PctCovered     float64
}

// Ingestor parses a Go coverage profile and writes coverage + node_coverage
// records into st, attributing lines to the innermost enclosing node.
//
// The real implementation lands in Track A; NewIngestor currently returns a
// stub so the CLI and server can be built against this seam in parallel.
type Ingestor interface {
	Ingest(ctx context.Context, st *store.Store, profile io.Reader, opts Options) (Summary, error)
}

// NewIngestor returns the default Ingestor. Track A replaces the stub returned
// here with the real attribution implementation.
func NewIngestor() Ingestor { return stubIngestor{} }

// Pct returns the covered percentage for the given line counts, or 0 when there
// are no measured lines.
func Pct(covered, uncovered int) float64 {
	total := covered + uncovered
	if total == 0 {
		return 0
	}
	return float64(covered) / float64(total) * 100
}

package coverage

import (
	"context"
	"io"

	"github.com/specscore/codegrapher/store"
)

// stubIngestor is a no-op placeholder so the CLI and server compile and can
// exercise their plumbing before Track A lands real attribution. It reads and
// discards the profile and writes nothing.
//
// Replaced in Track A (A4) by the real Ingestor.
type stubIngestor struct{}

func (stubIngestor) Ingest(_ context.Context, _ *store.Store, profile io.Reader, _ Options) (Summary, error) {
	if profile != nil {
		_, _ = io.Copy(io.Discard, profile)
	}
	return Summary{}, nil
}

package cli

import (
	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/query"
	"github.com/specscore/codegrapher/store"
)

// StoreQuerier implements GraphQuerier by delegating to the query package.
// It is the production implementation used by the CLI verbs.
// Tests use mockQuerier instead.
type StoreQuerier struct {
	s *store.Store
}

// NewStoreQuerier wraps a *store.Store as a GraphQuerier.
func NewStoreQuerier(s *store.Store) GraphQuerier {
	return &StoreQuerier{s: s}
}

func (q *StoreQuerier) SearchNodes(rawQuery string, opts SearchOptions) ([]model.SearchResult, error) {
	return query.SearchNodes(q.s, rawQuery, query.SearchOptions{
		Limit:     opts.Limit,
		Offset:    opts.Offset,
		Kinds:     opts.Kinds,
		Languages: opts.Languages,
	})
}

func (q *StoreQuerier) Callers(symbol string) (*CallersResult, error) {
	r, err := query.Callers(q.s, symbol)
	if err != nil {
		return nil, err
	}
	return &CallersResult{
		Symbol:  r.Symbol,
		Callers: toSymbolRefs(r.Callers),
	}, nil
}

func (q *StoreQuerier) Callees(symbol string) (*CalleesResult, error) {
	r, err := query.Callees(q.s, symbol)
	if err != nil {
		return nil, err
	}
	return &CalleesResult{
		Symbol:  r.Symbol,
		Callees: toSymbolRefs(r.Callees),
	}, nil
}

func (q *StoreQuerier) Impact(symbol string, depth int) (*ImpactResult, error) {
	r, err := query.Impact(q.s, symbol, depth)
	if err != nil {
		return nil, err
	}
	return &ImpactResult{
		Symbol:    r.Symbol,
		Depth:     r.Depth,
		NodeCount: r.NodeCount,
		EdgeCount: r.EdgeCount,
		Affected:  toSymbolRefs(r.Affected),
	}, nil
}

func (q *StoreQuerier) Status(projectPath string) (*StatusResult, error) {
	r, err := query.Status(q.s, projectPath)
	if err != nil {
		return nil, err
	}
	return &StatusResult{
		Initialized: r.Initialized,
		Version:     r.Version,
		ProjectPath: r.ProjectPath,
		IndexPath:   r.IndexPath,
		LastIndexed: r.LastIndexed,
		FileCount:   r.FileCount,
		NodeCount:   r.NodeCount,
		EdgeCount:   r.EdgeCount,
		DBSizeBytes: r.DBSizeBytes,
		Backend:     r.Backend,
		JournalMode: r.JournalMode,
		NodesByKind: r.NodesByKind,
		Languages:   r.Languages,
		PendingChanges: PendingChanges{
			Added:    r.PendingChanges.Added,
			Modified: r.PendingChanges.Modified,
			Removed:  r.PendingChanges.Removed,
		},
		WorktreeMismatch: r.WorktreeMismatch,
		Index: IndexInfo{
			BuiltWithVersion:           r.Index.BuiltWithVersion,
			BuiltWithExtractionVersion: r.Index.BuiltWithExtractionVersion,
			CurrentExtractionVersion:   r.Index.CurrentExtractionVersion,
			ReindexRecommended:         r.Index.ReindexRecommended,
		},
	}, nil
}

func (q *StoreQuerier) Files() ([]FileInfo, error) {
	r, err := query.Files(q.s)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, len(r))
	for i, f := range r {
		out[i] = FileInfo{
			Path:      f.Path,
			Language:  f.Language,
			NodeCount: f.NodeCount,
			Size:      f.Size,
		}
	}
	return out, nil
}

// toSymbolRefs converts a slice of query.SymbolRef to cli.SymbolRef.
func toSymbolRefs(refs []query.SymbolRef) []SymbolRef {
	out := make([]SymbolRef, len(refs))
	for i, r := range refs {
		out[i] = SymbolRef{
			Name:      r.Name,
			Kind:      r.Kind,
			FilePath:  r.FilePath,
			StartLine: r.StartLine,
		}
	}
	return out
}

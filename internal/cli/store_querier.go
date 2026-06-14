package cli

import (
	"sort"

	"github.com/specscore/codegrapher/model"
	"github.com/specscore/codegrapher/query"
	"github.com/specscore/codegrapher/store"
)

// StoreQuerier implements GraphQuerier by fanning each operation out across a
// slice of per-scope stores and merging the results in Go. The data model is
// one SQLite DB per (language, version) scope; every underlying query func is
// single-scope, so whole-repo answers come from running each query per store
// and merging — mirroring mcp.MultiBackend.
//
// For a single store, every method delegates to behavior byte-identical to the
// previous single-store StoreQuerier.
//
// Tests use mockQuerier instead.
type StoreQuerier struct {
	stores []*store.Store
}

// NewStoreQuerier wraps one or more *store.Store as a GraphQuerier. With a
// single store it behaves identically to the original single-store wrapper.
func NewStoreQuerier(stores ...*store.Store) GraphQuerier {
	return &StoreQuerier{stores: stores}
}

func (q *StoreQuerier) SearchNodes(rawQuery string, opts SearchOptions) ([]model.SearchResult, error) {
	qopts := query.SearchOptions{
		Limit:     opts.Limit,
		Offset:    opts.Offset,
		Kinds:     opts.Kinds,
		Languages: opts.Languages,
	}

	// Single store: delegate directly so output is byte-identical to the
	// previous single-store path (including query.SearchNodes' final
	// generated-files-last ordering).
	if len(q.stores) == 1 {
		return query.SearchNodes(q.stores[0], rawQuery, qopts)
	}

	// Multi-store: fetch the first Limit+Offset from each store (Offset=0), then
	// merge, rank by score desc (stable), and apply Offset/Limit in Go.
	perStore := qopts
	perStore.Offset = 0
	if opts.Limit > 0 {
		perStore.Limit = opts.Limit + opts.Offset
	}

	var all []model.SearchResult
	for _, s := range q.stores {
		res, err := query.SearchNodes(s, rawQuery, perStore)
		if err != nil {
			return nil, err
		}
		all = append(all, res...)
	}

	// Dedup by node ID (first wins), then stable-sort by score descending to
	// mirror query/MCP ranking.
	seen := make(map[string]struct{}, len(all))
	deduped := all[:0:0]
	for _, r := range all {
		if _, dup := seen[r.Node.ID]; dup {
			continue
		}
		seen[r.Node.ID] = struct{}{}
		deduped = append(deduped, r)
	}
	sort.SliceStable(deduped, func(i, j int) bool {
		return deduped[i].Score > deduped[j].Score
	})

	// Apply offset/limit slice.
	if opts.Offset > 0 {
		if opts.Offset >= len(deduped) {
			return []model.SearchResult{}, nil
		}
		deduped = deduped[opts.Offset:]
	}
	if opts.Limit > 0 && len(deduped) > opts.Limit {
		deduped = deduped[:opts.Limit]
	}
	return deduped, nil
}

// storesForSymbol narrows the callers/callees/impact fan-out to the scopes that
// contain symbol as an EXACT match, when any do. query.{Callers,Callees,Impact}
// match symbols fuzzily (substring) and only suppress fuzzy hits when a single
// store has more than one match; the per-scope fan-out can otherwise leak a
// fuzzy hit from a scope where the symbol appears only as a substring (e.g.
// "get" matching "Widget"). Preferring exact-match scopes mirrors single-merged-
// store behavior. When no scope has an exact match, every scope is kept so the
// fuzzy single-match fallback still works (e.g. for partial-name lookups).
func (q *StoreQuerier) storesForSymbol(symbol string) ([]*store.Store, error) {
	if len(q.stores) <= 1 {
		return q.stores, nil
	}
	var exact []*store.Store
	for _, s := range q.stores {
		ok, err := query.HasExactMatch(s, symbol)
		if err != nil {
			return nil, err
		}
		if ok {
			exact = append(exact, s)
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	return q.stores, nil
}

func (q *StoreQuerier) Callers(symbol string) (*CallersResult, error) {
	out := &CallersResult{Symbol: symbol, Callers: []SymbolRef{}}
	seen := make(map[symbolRefKey]struct{})
	stores, err := q.storesForSymbol(symbol)
	if err != nil {
		return nil, err
	}
	for _, s := range stores {
		r, err := query.Callers(s, symbol)
		if err != nil {
			return nil, err
		}
		if out.Symbol == "" {
			out.Symbol = r.Symbol
		}
		for _, ref := range toSymbolRefs(r.Callers) {
			k := refKey(ref)
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out.Callers = append(out.Callers, ref)
		}
	}
	return out, nil
}

func (q *StoreQuerier) Callees(symbol string) (*CalleesResult, error) {
	out := &CalleesResult{Symbol: symbol, Callees: []SymbolRef{}}
	seen := make(map[symbolRefKey]struct{})
	stores, err := q.storesForSymbol(symbol)
	if err != nil {
		return nil, err
	}
	for _, s := range stores {
		r, err := query.Callees(s, symbol)
		if err != nil {
			return nil, err
		}
		if out.Symbol == "" {
			out.Symbol = r.Symbol
		}
		for _, ref := range toSymbolRefs(r.Callees) {
			k := refKey(ref)
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out.Callees = append(out.Callees, ref)
		}
	}
	return out, nil
}

func (q *StoreQuerier) Impact(symbol string, depth int) (*ImpactResult, error) {
	out := &ImpactResult{Symbol: symbol, Affected: []SymbolRef{}}
	seen := make(map[symbolRefKey]struct{})
	stores, err := q.storesForSymbol(symbol)
	if err != nil {
		return nil, err
	}
	for _, s := range stores {
		r, err := query.Impact(s, symbol, depth)
		if err != nil {
			return nil, err
		}
		// Depth is normalized identically by every store; take the first.
		if out.Depth == 0 {
			out.Depth = r.Depth
		}
		out.NodeCount += r.NodeCount
		out.EdgeCount += r.EdgeCount
		for _, ref := range toSymbolRefs(r.Affected) {
			k := refKey(ref)
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out.Affected = append(out.Affected, ref)
		}
	}
	return out, nil
}

func (q *StoreQuerier) Status(projectPath string) (*StatusResult, error) {
	out := &StatusResult{
		ProjectPath: projectPath,
		NodesByKind: map[model.NodeKind]int{},
	}
	langSet := map[string]struct{}{}
	first := true
	for _, s := range q.stores {
		r, err := query.Status(s, projectPath)
		if err != nil {
			return nil, err
		}
		if first {
			out.Initialized = r.Initialized
			out.Version = r.Version
			out.ProjectPath = r.ProjectPath
			out.IndexPath = r.IndexPath
			out.LastIndexed = r.LastIndexed
			out.Backend = r.Backend
			out.JournalMode = r.JournalMode
			out.PendingChanges = PendingChanges{
				Added:    r.PendingChanges.Added,
				Modified: r.PendingChanges.Modified,
				Removed:  r.PendingChanges.Removed,
			}
			out.WorktreeMismatch = r.WorktreeMismatch
			out.Index = IndexInfo{
				BuiltWithVersion:           r.Index.BuiltWithVersion,
				BuiltWithExtractionVersion: r.Index.BuiltWithExtractionVersion,
				CurrentExtractionVersion:   r.Index.CurrentExtractionVersion,
				ReindexRecommended:         r.Index.ReindexRecommended,
			}
			first = false
		}
		out.FileCount += r.FileCount
		out.NodeCount += r.NodeCount
		out.EdgeCount += r.EdgeCount
		out.DBSizeBytes += r.DBSizeBytes
		for k, v := range r.NodesByKind {
			out.NodesByKind[k] += v
		}
		for _, l := range r.Languages {
			langSet[l] = struct{}{}
		}
	}
	langs := make([]string, 0, len(langSet))
	for l := range langSet {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	out.Languages = langs
	return out, nil
}

func (q *StoreQuerier) Files() ([]FileInfo, error) {
	var out []FileInfo
	seen := make(map[string]struct{})
	for _, s := range q.stores {
		r, err := query.Files(s)
		if err != nil {
			return nil, err
		}
		for _, f := range r {
			if _, dup := seen[f.Path]; dup {
				continue
			}
			seen[f.Path] = struct{}{}
			out = append(out, FileInfo{
				Path:      f.Path,
				Language:  f.Language,
				NodeCount: f.NodeCount,
				Size:      f.Size,
			})
		}
	}
	return out, nil
}

// symbolRefKey identifies a SymbolRef for de-duplication across scopes.
type symbolRefKey struct {
	Name      string
	Kind      model.NodeKind
	FilePath  string
	StartLine int
}

func refKey(r SymbolRef) symbolRefKey {
	return symbolRefKey{Name: r.Name, Kind: r.Kind, FilePath: r.FilePath, StartLine: r.StartLine}
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

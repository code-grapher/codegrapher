package cli

import (
	"github.com/specscore/codegrapher/model"
)

// mockQuerier implements GraphQuerier with injected fixture data for testing
// JSON output shape assertions.
type mockQuerier struct {
	searchFn  func(q string, opts SearchOptions) ([]model.SearchResult, error)
	callersFn func(sym string) (*CallersResult, error)
	calleesFn func(sym string) (*CalleesResult, error)
	impactFn  func(sym string, depth int) (*ImpactResult, error)
	statusFn  func(path string) (*StatusResult, error)
	filesFn   func() ([]FileInfo, error)
}

func (m *mockQuerier) SearchNodes(q string, opts SearchOptions) ([]model.SearchResult, error) {
	if m.searchFn != nil {
		return m.searchFn(q, opts)
	}
	return nil, nil
}

func (m *mockQuerier) Callers(sym string) (*CallersResult, error) {
	if m.callersFn != nil {
		return m.callersFn(sym)
	}
	return &CallersResult{Symbol: sym, Callers: []SymbolRef{}}, nil
}

func (m *mockQuerier) Callees(sym string) (*CalleesResult, error) {
	if m.calleesFn != nil {
		return m.calleesFn(sym)
	}
	return &CalleesResult{Symbol: sym, Callees: []SymbolRef{}}, nil
}

func (m *mockQuerier) Impact(sym string, depth int) (*ImpactResult, error) {
	if m.impactFn != nil {
		return m.impactFn(sym, depth)
	}
	return &ImpactResult{Symbol: sym, Depth: depth, Affected: []SymbolRef{}}, nil
}

func (m *mockQuerier) Status(path string) (*StatusResult, error) {
	if m.statusFn != nil {
		return m.statusFn(path)
	}
	return &StatusResult{
		Initialized: true,
		ProjectPath: path,
		NodesByKind: map[model.NodeKind]int{},
		Languages:   []string{},
	}, nil
}

func (m *mockQuerier) Files() ([]FileInfo, error) {
	if m.filesFn != nil {
		return m.filesFn()
	}
	return []FileInfo{}, nil
}

package cli

import (
	"github.com/specscore/codegrapher/model"
)

// -----------------------------------------------------------------------
// GraphQuerier — the narrow interface the CLI needs from the query layer.
//
// Defined here so the CLI compiles and is fully testable before the real
// query package exists. The real query package (or any adapter) must
// satisfy this interface to be wired in.
// -----------------------------------------------------------------------

// SymbolRef is the shape used in callers/callees/affected arrays.
// Matches the `callers`/`callees` array element shape in the golden JSON.
type SymbolRef struct {
	Name      string         `json:"name"`
	Kind      model.NodeKind `json:"kind"`
	FilePath  string         `json:"filePath"`
	StartLine int            `json:"startLine"`
}

// CallersResult is the JSON payload for `codegraph callers <symbol>`.
type CallersResult struct {
	Symbol  string      `json:"symbol"`
	Callers []SymbolRef `json:"callers"`
	Note    string      `json:"note,omitempty"`
}

// CalleesResult is the JSON payload for `codegraph callees <symbol>`.
type CalleesResult struct {
	Symbol  string      `json:"symbol"`
	Callees []SymbolRef `json:"callees"`
	Note    string      `json:"note,omitempty"`
}

// ImpactResult is the JSON payload for `codegraph impact <symbol>`.
type ImpactResult struct {
	Symbol    string      `json:"symbol"`
	Depth     int         `json:"depth"`
	NodeCount int         `json:"nodeCount"`
	EdgeCount int         `json:"edgeCount"`
	Affected  []SymbolRef `json:"affected"`
	Note      string      `json:"note,omitempty"`
}

// FileInfo is one entry in the `files` JSON array.
type FileInfo struct {
	Path      string         `json:"path"`
	Language  model.Language `json:"language"`
	NodeCount int            `json:"nodeCount"`
	Size      int64          `json:"size"`
}

// PendingChanges mirrors the pendingChanges field in the status payload.
type PendingChanges struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Removed  int `json:"removed"`
}

// IndexInfo mirrors the `index` block of the status payload.
type IndexInfo struct {
	BuiltWithVersion           string `json:"builtWithVersion"`
	BuiltWithExtractionVersion int    `json:"builtWithExtractionVersion"`
	CurrentExtractionVersion   int    `json:"currentExtractionVersion"`
	ReindexRecommended         bool   `json:"reindexRecommended"`
}

// StatusResult is the JSON payload for `codegraph status`.
type StatusResult struct {
	Initialized      bool                   `json:"initialized"`
	Version          string                 `json:"version"`
	ProjectPath      string                 `json:"projectPath"`
	IndexPath        string                 `json:"indexPath"`
	LastIndexed      string                 `json:"lastIndexed"`
	FileCount        int                    `json:"fileCount"`
	NodeCount        int                    `json:"nodeCount"`
	EdgeCount        int                    `json:"edgeCount"`
	DBSizeBytes      int64                  `json:"dbSizeBytes"`
	Backend          string                 `json:"backend"`
	JournalMode      string                 `json:"journalMode"`
	NodesByKind      map[model.NodeKind]int `json:"nodesByKind"`
	Languages        []string               `json:"languages"`
	PendingChanges   PendingChanges         `json:"pendingChanges"`
	WorktreeMismatch any                    `json:"worktreeMismatch"`
	Index            IndexInfo              `json:"index"`
}

// SearchOptions controls result set size and filtering.
type SearchOptions struct {
	Limit     int
	Offset    int
	Kinds     []model.NodeKind
	Languages []model.Language
}

// GraphQuerier is the narrow read-only interface the CLI needs from the query
// layer. Implement it against the real query package; use mockQuerier in tests.
type GraphQuerier interface {
	// SearchNodes runs the symbol-search pipeline and returns scored results.
	SearchNodes(rawQuery string, opts SearchOptions) ([]model.SearchResult, error)

	// Callers returns the set of symbols that call the given symbol name.
	Callers(symbol string) (*CallersResult, error)

	// Callees returns the set of symbols that the given symbol name calls.
	Callees(symbol string) (*CalleesResult, error)

	// Impact returns the blast-radius for the given symbol name.
	Impact(symbol string, depth int) (*ImpactResult, error)

	// Status returns index statistics.
	Status(projectPath string) (*StatusResult, error)

	// Files returns the list of indexed files.
	Files() ([]FileInfo, error)
}

// Package browserapi exposes one initialized CodeGrapher repository through a
// versioned, authenticated, read-only HTTP API for browser consumers.
package browserapi

import "time"

const BasePath = "/codegrapher/v1"

type Limits struct {
	MaxTreeEntries   int `json:"maxTreeEntries"`
	MaxFileBytes     int `json:"maxFileBytes"`
	MaxSearchResults int `json:"maxSearchResults"`
	MaxGraphDepth    int `json:"maxGraphDepth"`
	MaxGraphNodes    int `json:"maxGraphNodes"`
	MaxGraphEdges    int `json:"maxGraphEdges"`
}

func DefaultLimits() Limits {
	return Limits{MaxTreeEntries: 1000, MaxFileBytes: 1 << 20, MaxSearchResults: 100, MaxGraphDepth: 3, MaxGraphNodes: 250, MaxGraphEdges: 500}
}

type Freshness struct {
	State              string     `json:"state"`
	IndexedAt          *time.Time `json:"indexedAt,omitempty"`
	LastSuccessfulSync *time.Time `json:"lastSuccessfulSync,omitempty"`
	LastError          string     `json:"lastError,omitempty"`
}

type StatusResponse struct {
	APIVersion      string    `json:"apiVersion"`
	ServerVersion   string    `json:"serverVersion"`
	Capabilities    []string  `json:"capabilities"`
	RepositoryCount int       `json:"repositoryCount"`
	Freshness       Freshness `json:"freshness"`
	Limits          Limits    `json:"limits"`
}

type Repository struct {
	ID          string    `json:"id"`
	Label       string    `json:"label"`
	Remote      string    `json:"remote,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	HeadCommit  string    `json:"headCommit,omitempty"`
	Revision    string    `json:"revision,omitempty"`
	Freshness   Freshness `json:"freshness"`
	FileCount   int       `json:"fileCount"`
	SymbolCount int       `json:"symbolCount"`
	EdgeCount   int       `json:"edgeCount"`
}

type TreeEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size,omitempty"`
	Language    string `json:"language,omitempty"`
	ContentHash string `json:"contentHash,omitempty"`
}

type TreeResponse struct {
	RepositoryID string      `json:"repositoryId"`
	Revision     string      `json:"revision"`
	Path         string      `json:"path"`
	Entries      []TreeEntry `json:"entries"`
	Limit        int         `json:"limit"`
	Truncated    bool        `json:"truncated"`
	Freshness    Freshness   `json:"freshness"`
}

type FileResponse struct {
	RepositoryID string    `json:"repositoryId"`
	Revision     string    `json:"revision"`
	Path         string    `json:"path"`
	Language     string    `json:"language"`
	Size         int64     `json:"size"`
	LineCount    int       `json:"lineCount"`
	ContentHash  string    `json:"contentHash"`
	Content      string    `json:"content"`
	Freshness    Freshness `json:"freshness"`
}

type SourceRange struct {
	StartLine   int `json:"startLine"`
	EndLine     int `json:"endLine"`
	StartColumn int `json:"startColumn"`
	EndColumn   int `json:"endColumn"`
}

type Symbol struct {
	ID            string      `json:"id"`
	Kind          string      `json:"kind"`
	Name          string      `json:"name"`
	QualifiedName string      `json:"qualifiedName"`
	FilePath      string      `json:"filePath"`
	Language      string      `json:"language"`
	Range         SourceRange `json:"range"`
	Signature     string      `json:"signature,omitempty"`
	Docstring     string      `json:"docstring,omitempty"`
	Visibility    *string     `json:"visibility,omitempty"`
	Exported      bool        `json:"exported"`
}

type SymbolResponse struct {
	RepositoryID string    `json:"repositoryId"`
	Revision     string    `json:"revision"`
	Symbol       Symbol    `json:"symbol"`
	Freshness    Freshness `json:"freshness"`
}

type SearchResult struct {
	Score  float64 `json:"score"`
	Symbol Symbol  `json:"symbol"`
}

type SearchResponse struct {
	RepositoryID string         `json:"repositoryId"`
	Revision     string         `json:"revision"`
	Query        string         `json:"query"`
	Results      []SearchResult `json:"results"`
	Limit        int            `json:"limit"`
	Truncated    bool           `json:"truncated"`
	Freshness    Freshness      `json:"freshness"`
}

type GraphEdge struct {
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
	Kind     string `json:"kind"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}

type GraphResponse struct {
	RepositoryID string      `json:"repositoryId"`
	Revision     string      `json:"revision"`
	RootSymbolID string      `json:"rootSymbolId"`
	Direction    string      `json:"direction"`
	Depth        int         `json:"depth"`
	MaxNodes     int         `json:"maxNodes"`
	MaxEdges     int         `json:"maxEdges"`
	Nodes        []Symbol    `json:"nodes"`
	Edges        []GraphEdge `json:"edges"`
	Truncated    bool        `json:"truncated"`
	Freshness    Freshness   `json:"freshness"`
}

type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

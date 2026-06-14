// Package model defines the core knowledge-graph types shared by every
// codegrapher package: nodes, edges, file records, and their kind enums.
//
// Ported from src/types.ts of github.com/colbymchenry/codegraph (MIT).
// JSON field names and presence semantics deliberately mirror the original's
// output (verified against testdata/golden): optional string/slice fields are
// omitted when empty, while Visibility is always emitted (null when absent).
package model

// NodeKind is the type of a code symbol in the graph.
type NodeKind string

// All node kinds, mirroring NODE_KINDS in the original. The port only ever
// *produces* the kinds emitted by Go/TypeScript/JavaScript extraction plus
// route nodes, but the full set is retained so indexes built by the original
// remain readable and the search query parser validates identically.
const (
	KindFile       NodeKind = "file"
	KindModule     NodeKind = "module"
	KindClass      NodeKind = "class"
	KindStruct     NodeKind = "struct"
	KindInterface  NodeKind = "interface"
	KindTrait      NodeKind = "trait"
	KindProtocol   NodeKind = "protocol"
	KindFunction   NodeKind = "function"
	KindMethod     NodeKind = "method"
	KindProperty   NodeKind = "property"
	KindField      NodeKind = "field"
	KindVariable   NodeKind = "variable"
	KindConstant   NodeKind = "constant"
	KindEnum       NodeKind = "enum"
	KindEnumMember NodeKind = "enum_member"
	KindTypeAlias  NodeKind = "type_alias"
	KindNamespace  NodeKind = "namespace"
	KindParameter  NodeKind = "parameter"
	KindImport     NodeKind = "import"
	KindExport     NodeKind = "export"
	KindRoute      NodeKind = "route"
	KindComponent  NodeKind = "component"
)

// NodeKinds lists every valid NodeKind (runtime-iterable, like NODE_KINDS).
var NodeKinds = []NodeKind{
	KindFile, KindModule, KindClass, KindStruct, KindInterface, KindTrait,
	KindProtocol, KindFunction, KindMethod, KindProperty, KindField,
	KindVariable, KindConstant, KindEnum, KindEnumMember, KindTypeAlias,
	KindNamespace, KindParameter, KindImport, KindExport, KindRoute,
	KindComponent,
}

// EdgeKind is the type of a relationship between two nodes.
type EdgeKind string

// All edge kinds, mirroring EdgeKind in the original.
const (
	EdgeContains     EdgeKind = "contains"
	EdgeCalls        EdgeKind = "calls"
	EdgeImports      EdgeKind = "imports"
	EdgeExports      EdgeKind = "exports"
	EdgeExtends      EdgeKind = "extends"
	EdgeImplements   EdgeKind = "implements"
	EdgeReferences   EdgeKind = "references"
	EdgeTypeOf       EdgeKind = "type_of"
	EdgeReturns      EdgeKind = "returns"
	EdgeInstantiates EdgeKind = "instantiates"
	EdgeOverrides    EdgeKind = "overrides"
	EdgeDecorates    EdgeKind = "decorates"
	EdgeRequires     EdgeKind = "requires"
	EdgeReplaces     EdgeKind = "replaces"
	EdgeExcludes     EdgeKind = "excludes"
)

// Language identifies the programming language of a file or symbol.
// The port indexes go/typescript/javascript/tsx/jsx; the full upstream set is
// retained for index compatibility and language detection of skipped files.
type Language string

const (
	LangTypeScript  Language = "typescript"
	LangJavaScript  Language = "javascript"
	LangTSX         Language = "tsx"
	LangJSX         Language = "jsx"
	LangGo          Language = "go"
	LangPython      Language = "python"
	LangCSharp      Language = "csharp"
	LangJava        Language = "java"
	LangKotlin      Language = "kotlin"
	LangRuby        Language = "ruby"
	LangRust        Language = "rust"
	LangPHP         Language = "php"
	LangC           Language = "c"
	LangCPP         Language = "cpp"
	LangScala       Language = "scala"
	LangSwift       Language = "swift"
	LangDart        Language = "dart"
	LangLua         Language = "lua"
	LangElixir      Language = "elixir"
	LangHaskell     Language = "haskell"
	LangObjC        Language = "objc"
	LangPerl        Language = "perl"
	LangErlang      Language = "erlang"
	LangFSharp      Language = "fsharp"
	LangGoMod       Language = "go.mod"
	LangPackageJSON Language = "package.json"
	LangNode        Language = "node"
	LangYAML        Language = "yaml"
	LangUnknown     Language = "unknown"
)

// Node is a code symbol in the knowledge graph.
type Node struct {
	ID            string   `json:"id"`
	Kind          NodeKind `json:"kind"`
	Name          string   `json:"name"`
	QualifiedName string   `json:"qualifiedName"`
	FilePath      string   `json:"filePath"`
	Language      Language `json:"language"`
	StartLine     int      `json:"startLine"`   // 1-indexed
	EndLine       int      `json:"endLine"`     // 1-indexed
	StartColumn   int      `json:"startColumn"` // 0-indexed
	EndColumn     int      `json:"endColumn"`   // 0-indexed

	Docstring      string   `json:"docstring,omitempty"`
	Signature      string   `json:"signature,omitempty"`
	Visibility     *string  `json:"visibility"` // always emitted; null when absent (matches original)
	IsExported     bool     `json:"isExported"`
	IsAsync        bool     `json:"isAsync"`
	IsStatic       bool     `json:"isStatic"`
	IsAbstract     bool     `json:"isAbstract"`
	Decorators     []string `json:"decorators,omitempty"`
	TypeParameters []string `json:"typeParameters,omitempty"`
	ReturnType     string   `json:"returnType,omitempty"`

	// UpdatedAt is a Unix timestamp in milliseconds (Date.now() in the original).
	UpdatedAt int64 `json:"updatedAt"`
}

// Edge is a relationship between two nodes.
type Edge struct {
	Source     string         `json:"source"`
	Target     string         `json:"target"`
	Kind       EdgeKind       `json:"kind"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Line       int            `json:"line,omitempty"`
	Column     int            `json:"column,omitempty"`
	Provenance string         `json:"provenance,omitempty"` // "tree-sitter" | "scip" | "heuristic"
}

// FileRecord is metadata about a tracked source file.
type FileRecord struct {
	Path        string            `json:"path"`
	ContentHash string            `json:"contentHash"`
	Language    Language          `json:"language"`
	Size        int64             `json:"size"`
	ModifiedAt  int64             `json:"modifiedAt"` // ms
	IndexedAt   int64             `json:"indexedAt"`  // ms
	NodeCount   int               `json:"nodeCount"`
	Errors      []ExtractionError `json:"errors,omitempty"`
}

// ExtractionError is an error or warning produced while extracting a file.
type ExtractionError struct {
	Message  string `json:"message"`
	FilePath string `json:"filePath,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity"` // "error" | "warning"
	Code     string `json:"code,omitempty"`
}

// UnresolvedReference is a reference recorded during extraction that the
// resolution pipeline later turns into an edge (or discards).
type UnresolvedReference struct {
	FromNodeID    string   `json:"fromNodeId"`
	ReferenceName string   `json:"referenceName"`
	ReferenceKind EdgeKind `json:"referenceKind"`
	Line          int      `json:"line"`
	Column        int      `json:"column"`
	FilePath      string   `json:"filePath,omitempty"`
	Language      Language `json:"language,omitempty"`
	Candidates    []string `json:"candidates,omitempty"`
}

// ExtractionResult is the outcome of parsing one source file.
type ExtractionResult struct {
	Nodes                []Node
	Edges                []Edge
	UnresolvedReferences []UnresolvedReference
	Errors               []ExtractionError
	DurationMs           int64
}

// SearchResult pairs a node with its relevance score.
type SearchResult struct {
	Node  Node    `json:"node"`
	Score float64 `json:"score"`
}

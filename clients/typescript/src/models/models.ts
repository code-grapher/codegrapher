/**
 * A sequence of textual characters.
 */
export type String = string;
export interface ApiStatus {
  apiVersion: "v1";
  serverVersion: string;
  capabilities: Array<string>;
  repositoryCount: number;
  freshness: Freshness;
  limits: ApiLimits;
}
/**
 * A 32-bit integer. (`-2,147,483,648` to `2,147,483,647`)
 */
export type Int32 = number;
/**
 * A 64-bit integer. (`-9,223,372,036,854,775,808` to `9,223,372,036,854,775,807`)
 */
export type Int64 = bigint;
/**
 * A whole number. This represent any `integer` value possible.
 * It is commonly represented as `BigInteger` in some languages.
 */
export type Integer = number;
/**
 * A numeric type
 */
export type Numeric = number;
export interface Freshness {
  state: FreshnessState;
  indexedAt?: Date;
  lastSuccessfulSync?: Date;
  lastError?: string;
}
export enum FreshnessState {
  Ready = "ready",
  Updating = "updating",
  Stale = "stale"
}
/**
 * An instant in coordinated universal time (UTC)"
 */
export type UtcDateTime = Date;
export interface ApiLimits {
  maxTreeEntries: number;
  maxFileBytes: number;
  maxSearchResults: number;
  maxGraphDepth: number;
  maxGraphNodes: number;
  maxGraphEdges: number;
}
export interface RepositoryList {
  repositories: Array<RepositorySummary>;
}
export interface RepositorySummary {
  id: string;
  label: string;
  remote?: string;
  branch?: string;
  headCommit?: string;
  revision?: string;
  freshness: Freshness;
  fileCount: number;
  symbolCount: number;
  edgeCount: number;
}
export interface TreeResponse {
  repositoryId: string;
  revision: string;
  path: string;
  entries: Array<TreeEntry>;
  limit: number;
  truncated: boolean;
  freshness: Freshness;
}
export interface TreeEntry {
  name: string;
  path: string;
  kind: TreeEntryKind;
  size?: bigint;
  language?: string;
  contentHash?: string;
}
export enum TreeEntryKind {
  File = "file",
  Directory = "directory",
  Symlink = "symlink"
}
/**
 * Boolean with `true` and `false` values.
 */
export type Boolean = boolean;
export interface FileResponse {
  repositoryId: string;
  revision: string;
  path: string;
  language: string;
  size: bigint;
  lineCount: number;
  contentHash: string;
  content: string;
  freshness: Freshness;
}
export interface SymbolResponse {
  repositoryId: string;
  revision: string;
  symbol: Symbol;
  freshness: Freshness;
}
export interface Symbol {
  id: string;
  kind: string;
  name: string;
  qualifiedName: string;
  filePath: string;
  language: string;
  range: SourceRange;
  signature?: string;
  docstring?: string;
  visibility?: string;
  exported: boolean;
}
export interface SourceRange {
  startLine: number;
  endLine: number;
  startColumn: number;
  endColumn: number;
}
export interface SearchResponse {
  repositoryId: string;
  revision: string;
  query: string;
  results: Array<SearchResult>;
  limit: number;
  truncated: boolean;
  freshness: Freshness;
}
export interface SearchResult {
  score: number;
  symbol: Symbol;
}
/**
 * A 64 bit floating point number. (`±5.0 × 10^−324` to `±1.7 × 10^308`)
 */
export type Float64 = number;
/**
 * A number with decimal value
 */
export type Float = number;
export enum GraphDirection {
  Incoming = "incoming",
  Outgoing = "outgoing",
  Both = "both"
}
export interface GraphResponse {
  repositoryId: string;
  revision: string;
  rootSymbolId: string;
  direction: GraphDirection;
  depth: number;
  maxNodes: number;
  maxEdges: number;
  nodes: Array<Symbol>;
  edges: Array<GraphEdge>;
  truncated: boolean;
  freshness: Freshness;
}
export interface GraphEdge {
  sourceId: string;
  targetId: string;
  kind: string;
  line?: number;
  column?: number;
}

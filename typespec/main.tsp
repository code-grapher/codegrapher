import "@typespec/http";
import "@typespec/openapi";
import "@typespec/openapi3";

using Http;
using OpenAPI;

@service(#{ title: "CodeGrapher Browser API" })
@info(#{ version: "1.0.0" })
@server("http://127.0.0.1:7331/codegrapher/v1", "CodeGrapher daemon browser API")
@useAuth(BearerAuth)
namespace CodeGrapher.Browser.V1;

alias RepositoryId = string;
alias Revision = string;
alias SymbolId = string;

enum FreshnessState {
  ready,
  updating,
  stale,
}

enum TreeEntryKind {
  file,
  directory,
  symlink,
}

enum GraphDirection {
  incoming,
  outgoing,
  both,
}

model ApiLimits {
  maxTreeEntries: int32;
  maxFileBytes: int32;
  maxSearchResults: int32;
  maxGraphDepth: int32;
  maxGraphNodes: int32;
  maxGraphEdges: int32;
}

model Freshness {
  state: FreshnessState;
  indexedAt?: utcDateTime;
  lastSuccessfulSync?: utcDateTime;
  lastError?: string;
}

model ApiStatus {
  apiVersion: "v1";
  serverVersion: string;
  capabilities: string[];
  repositoryCount: int32;
  freshness: Freshness;
  limits: ApiLimits;
}

model RepositorySummary {
  id: RepositoryId;
  label: string;
  remote?: string;
  branch?: string;
  headCommit?: string;
  revision?: Revision;
  freshness: Freshness;
  fileCount: int32;
  symbolCount: int32;
  edgeCount: int32;
}

model RepositoryList {
  repositories: RepositorySummary[];
}

model TreeEntry {
  name: string;
  path: string;
  kind: TreeEntryKind;
  size?: int64;
  language?: string;
  contentHash?: string;
}

model TreeResponse {
  repositoryId: RepositoryId;
  revision: Revision;
  path: string;
  entries: TreeEntry[];
  limit: int32;
  truncated: boolean;
  freshness: Freshness;
}

model FileResponse {
  repositoryId: RepositoryId;
  revision: Revision;
  path: string;
  language: string;
  size: int64;
  lineCount: int32;
  contentHash: string;
  content: string;
  freshness: Freshness;
}

model SourceRange {
  startLine: int32;
  endLine: int32;
  startColumn: int32;
  endColumn: int32;
}

model Symbol {
  id: SymbolId;
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

model SymbolResponse {
  repositoryId: RepositoryId;
  revision: Revision;
  symbol: Symbol;
  freshness: Freshness;
}

model SearchResult {
  score: float64;
  symbol: Symbol;
}

model SearchResponse {
  repositoryId: RepositoryId;
  revision: Revision;
  query: string;
  results: SearchResult[];
  limit: int32;
  truncated: boolean;
  freshness: Freshness;
}

model GraphEdge {
  sourceId: SymbolId;
  targetId: SymbolId;
  kind: string;
  line?: int32;
  column?: int32;
}

model GraphResponse {
  repositoryId: RepositoryId;
  revision: Revision;
  rootSymbolId: SymbolId;
  direction: GraphDirection;
  depth: int32;
  maxNodes: int32;
  maxEdges: int32;
  nodes: Symbol[];
  edges: GraphEdge[];
  truncated: boolean;
  freshness: Freshness;
}

model ApiError {
  code: string;
  message: string;
  requestId: string;
}

@error
model ApiErrorResponse {
  @statusCode statusCode: 400 | 401 | 403 | 404 | 409 | 413 | 422 | 500 | 503;
  @body error: ApiError;
}

@get
@route("/status")
@operationId("getStatus")
op getStatus(): ApiStatus | ApiErrorResponse;

@get
@route("/repositories")
@operationId("listRepositories")
op listRepositories(): RepositoryList | ApiErrorResponse;

@get
@route("/repositories/{repositoryId}")
@operationId("getRepository")
op getRepository(@path repositoryId: RepositoryId): RepositorySummary | ApiErrorResponse;

@get
@route("/repositories/{repositoryId}/revisions/{revision}/tree")
@operationId("getTree")
op getTree(
  @path repositoryId: RepositoryId,
  @path revision: Revision,
  @query path?: string,
): TreeResponse | ApiErrorResponse;

@get
@route("/repositories/{repositoryId}/revisions/{revision}/files")
@operationId("getFile")
op getFile(
  @path repositoryId: RepositoryId,
  @path revision: Revision,
  @query path: string,
): FileResponse | ApiErrorResponse;

@get
@route("/repositories/{repositoryId}/revisions/{revision}/symbols/{symbolId}")
@operationId("getSymbol")
op getSymbol(
  @path repositoryId: RepositoryId,
  @path revision: Revision,
  @path symbolId: SymbolId,
): SymbolResponse | ApiErrorResponse;

@get
@route("/repositories/{repositoryId}/revisions/{revision}/search")
@operationId("searchSymbols")
op searchSymbols(
  @path repositoryId: RepositoryId,
  @path revision: Revision,
  @query query: string,
  @query limit?: int32,
): SearchResponse | ApiErrorResponse;

@get
@route("/repositories/{repositoryId}/revisions/{revision}/symbols/{symbolId}/graph")
@operationId("getSymbolGraph")
op getSymbolGraph(
  @path repositoryId: RepositoryId,
  @path revision: Revision,
  @path symbolId: SymbolId,
  @query direction?: GraphDirection,
  @query depth?: int32,
  @query maxNodes?: int32,
  @query maxEdges?: int32,
): GraphResponse | ApiErrorResponse;

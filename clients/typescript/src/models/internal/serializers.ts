import type {
  ApiLimits,
  ApiStatus,
  FileResponse,
  Freshness,
  GraphEdge,
  GraphResponse,
  RepositoryList,
  RepositorySummary,
  SearchResponse,
  SearchResult,
  SourceRange,
  Symbol,
  SymbolResponse,
  TreeEntry,
  TreeResponse,
} from "../models.js";

export function decodeBase64(value: string): Uint8Array | undefined {
  if(!value) {
    return value as any;
  }
  // Normalize Base64URL to Base64
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
    .padEnd(value.length + (4 - (value.length % 4)) % 4, '=');

  return new Uint8Array(Buffer.from(base64, 'base64'));
}export function encodeUint8Array(
  value: Uint8Array | undefined | null,
  encoding: BufferEncoding,
): string | undefined {
  if (!value) {
    return value as any;
  }
  return Buffer.from(value).toString(encoding);
}export function dateDeserializer(date?: string | null): Date {
  if (!date) {
    return date as any;
  }

  return new Date(date);
}export function dateRfc7231Deserializer(date?: string | null): Date {
  if (!date) {
    return date as any;
  }

  return new Date(date);
}export function dateRfc3339Serializer(date?: Date | null): string {
  if (!date) {
    return date as any
  }

  return date.toISOString();
}export function dateRfc7231Serializer(date?: Date | null): string {
  if (!date) {
    return date as any;
  }

  return date.toUTCString();
}export function dateUnixTimestampSerializer(date?: Date | null): number {
  if (!date) {
    return date as any;
  }

  return Math.floor(date.getTime() / 1000);
}export function dateUnixTimestampDeserializer(date?: number | null): Date {
  if (!date) {
    return date as any;
  }

  return new Date(date * 1000);
}export function jsonApiStatusToTransportTransform(
  input_?: ApiStatus | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    apiVersion: input_.apiVersion,serverVersion: input_.serverVersion,capabilities: jsonArrayStringToTransportTransform(input_.capabilities),repositoryCount: input_.repositoryCount,freshness: jsonFreshnessToTransportTransform(input_.freshness),limits: jsonApiLimitsToTransportTransform(input_.limits)
  }!;
}export function jsonApiStatusToApplicationTransform(input_?: any): ApiStatus {
  if(!input_) {
    return input_ as any;
  }
    return {
    apiVersion: input_.apiVersion,serverVersion: input_.serverVersion,capabilities: jsonArrayStringToApplicationTransform(input_.capabilities),repositoryCount: input_.repositoryCount,freshness: jsonFreshnessToApplicationTransform(input_.freshness),limits: jsonApiLimitsToApplicationTransform(input_.limits)
  }!;
}export function jsonArrayStringToTransportTransform(
  items_?: Array<string> | null,
): any {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = item as any;
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonArrayStringToApplicationTransform(
  items_?: any,
): Array<string> {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = item as any;
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonFreshnessToTransportTransform(
  input_?: Freshness | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    state: input_.state,indexedAt: dateRfc3339Serializer(input_.indexedAt),lastSuccessfulSync: dateRfc3339Serializer(input_.lastSuccessfulSync),lastError: input_.lastError
  }!;
}export function jsonFreshnessToApplicationTransform(input_?: any): Freshness {
  if(!input_) {
    return input_ as any;
  }
    return {
    state: input_.state,indexedAt: dateDeserializer(input_.indexedAt)!,lastSuccessfulSync: dateDeserializer(input_.lastSuccessfulSync)!,lastError: input_.lastError
  }!;
}export function jsonApiLimitsToTransportTransform(
  input_?: ApiLimits | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    maxTreeEntries: input_.maxTreeEntries,maxFileBytes: input_.maxFileBytes,maxSearchResults: input_.maxSearchResults,maxGraphDepth: input_.maxGraphDepth,maxGraphNodes: input_.maxGraphNodes,maxGraphEdges: input_.maxGraphEdges
  }!;
}export function jsonApiLimitsToApplicationTransform(input_?: any): ApiLimits {
  if(!input_) {
    return input_ as any;
  }
    return {
    maxTreeEntries: input_.maxTreeEntries,maxFileBytes: input_.maxFileBytes,maxSearchResults: input_.maxSearchResults,maxGraphDepth: input_.maxGraphDepth,maxGraphNodes: input_.maxGraphNodes,maxGraphEdges: input_.maxGraphEdges
  }!;
}export function jsonRepositoryListToTransportTransform(
  input_?: RepositoryList | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositories: jsonArrayRepositorySummaryToTransportTransform(input_.repositories)
  }!;
}export function jsonRepositoryListToApplicationTransform(
  input_?: any,
): RepositoryList {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositories: jsonArrayRepositorySummaryToApplicationTransform(input_.repositories)
  }!;
}export function jsonArrayRepositorySummaryToTransportTransform(
  items_?: Array<RepositorySummary> | null,
): any {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = jsonRepositorySummaryToTransportTransform(item as any);
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonArrayRepositorySummaryToApplicationTransform(
  items_?: any,
): Array<RepositorySummary> {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = jsonRepositorySummaryToApplicationTransform(item as any);
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonRepositorySummaryToTransportTransform(
  input_?: RepositorySummary | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    id: input_.id,label: input_.label,remote: input_.remote,branch: input_.branch,headCommit: input_.headCommit,revision: input_.revision,freshness: jsonFreshnessToTransportTransform(input_.freshness),fileCount: input_.fileCount,symbolCount: input_.symbolCount,edgeCount: input_.edgeCount
  }!;
}export function jsonRepositorySummaryToApplicationTransform(
  input_?: any,
): RepositorySummary {
  if(!input_) {
    return input_ as any;
  }
    return {
    id: input_.id,label: input_.label,remote: input_.remote,branch: input_.branch,headCommit: input_.headCommit,revision: input_.revision,freshness: jsonFreshnessToApplicationTransform(input_.freshness),fileCount: input_.fileCount,symbolCount: input_.symbolCount,edgeCount: input_.edgeCount
  }!;
}export function jsonTreeResponseToTransportTransform(
  input_?: TreeResponse | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositoryId: input_.repositoryId,revision: input_.revision,path: input_.path,entries: jsonArrayTreeEntryToTransportTransform(input_.entries),limit: input_.limit,truncated: input_.truncated,freshness: jsonFreshnessToTransportTransform(input_.freshness)
  }!;
}export function jsonTreeResponseToApplicationTransform(
  input_?: any,
): TreeResponse {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositoryId: input_.repositoryId,revision: input_.revision,path: input_.path,entries: jsonArrayTreeEntryToApplicationTransform(input_.entries),limit: input_.limit,truncated: input_.truncated,freshness: jsonFreshnessToApplicationTransform(input_.freshness)
  }!;
}export function jsonArrayTreeEntryToTransportTransform(
  items_?: Array<TreeEntry> | null,
): any {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = jsonTreeEntryToTransportTransform(item as any);
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonArrayTreeEntryToApplicationTransform(
  items_?: any,
): Array<TreeEntry> {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = jsonTreeEntryToApplicationTransform(item as any);
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonTreeEntryToTransportTransform(
  input_?: TreeEntry | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    name: input_.name,path: input_.path,kind: input_.kind,size: input_.size,language: input_.language,contentHash: input_.contentHash
  }!;
}export function jsonTreeEntryToApplicationTransform(input_?: any): TreeEntry {
  if(!input_) {
    return input_ as any;
  }
    return {
    name: input_.name,path: input_.path,kind: input_.kind,size: input_.size,language: input_.language,contentHash: input_.contentHash
  }!;
}export function jsonFileResponseToTransportTransform(
  input_?: FileResponse | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositoryId: input_.repositoryId,revision: input_.revision,path: input_.path,language: input_.language,size: input_.size,lineCount: input_.lineCount,contentHash: input_.contentHash,content: input_.content,freshness: jsonFreshnessToTransportTransform(input_.freshness)
  }!;
}export function jsonFileResponseToApplicationTransform(
  input_?: any,
): FileResponse {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositoryId: input_.repositoryId,revision: input_.revision,path: input_.path,language: input_.language,size: input_.size,lineCount: input_.lineCount,contentHash: input_.contentHash,content: input_.content,freshness: jsonFreshnessToApplicationTransform(input_.freshness)
  }!;
}export function jsonSymbolResponseToTransportTransform(
  input_?: SymbolResponse | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositoryId: input_.repositoryId,revision: input_.revision,symbol: jsonSymbolToTransportTransform(input_.symbol),freshness: jsonFreshnessToTransportTransform(input_.freshness)
  }!;
}export function jsonSymbolResponseToApplicationTransform(
  input_?: any,
): SymbolResponse {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositoryId: input_.repositoryId,revision: input_.revision,symbol: jsonSymbolToApplicationTransform(input_.symbol),freshness: jsonFreshnessToApplicationTransform(input_.freshness)
  }!;
}export function jsonSymbolToTransportTransform(input_?: Symbol | null): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    id: input_.id,kind: input_.kind,name: input_.name,qualifiedName: input_.qualifiedName,filePath: input_.filePath,language: input_.language,range: jsonSourceRangeToTransportTransform(input_.range),signature: input_.signature,docstring: input_.docstring,visibility: input_.visibility,exported: input_.exported
  }!;
}export function jsonSymbolToApplicationTransform(input_?: any): Symbol {
  if(!input_) {
    return input_ as any;
  }
    return {
    id: input_.id,kind: input_.kind,name: input_.name,qualifiedName: input_.qualifiedName,filePath: input_.filePath,language: input_.language,range: jsonSourceRangeToApplicationTransform(input_.range),signature: input_.signature,docstring: input_.docstring,visibility: input_.visibility,exported: input_.exported
  }!;
}export function jsonSourceRangeToTransportTransform(
  input_?: SourceRange | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    startLine: input_.startLine,endLine: input_.endLine,startColumn: input_.startColumn,endColumn: input_.endColumn
  }!;
}export function jsonSourceRangeToApplicationTransform(
  input_?: any,
): SourceRange {
  if(!input_) {
    return input_ as any;
  }
    return {
    startLine: input_.startLine,endLine: input_.endLine,startColumn: input_.startColumn,endColumn: input_.endColumn
  }!;
}export function jsonSearchResponseToTransportTransform(
  input_?: SearchResponse | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositoryId: input_.repositoryId,revision: input_.revision,query: input_.query,results: jsonArraySearchResultToTransportTransform(input_.results),limit: input_.limit,truncated: input_.truncated,freshness: jsonFreshnessToTransportTransform(input_.freshness)
  }!;
}export function jsonSearchResponseToApplicationTransform(
  input_?: any,
): SearchResponse {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositoryId: input_.repositoryId,revision: input_.revision,query: input_.query,results: jsonArraySearchResultToApplicationTransform(input_.results),limit: input_.limit,truncated: input_.truncated,freshness: jsonFreshnessToApplicationTransform(input_.freshness)
  }!;
}export function jsonArraySearchResultToTransportTransform(
  items_?: Array<SearchResult> | null,
): any {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = jsonSearchResultToTransportTransform(item as any);
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonArraySearchResultToApplicationTransform(
  items_?: any,
): Array<SearchResult> {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = jsonSearchResultToApplicationTransform(item as any);
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonSearchResultToTransportTransform(
  input_?: SearchResult | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    score: input_.score,symbol: jsonSymbolToTransportTransform(input_.symbol)
  }!;
}export function jsonSearchResultToApplicationTransform(
  input_?: any,
): SearchResult {
  if(!input_) {
    return input_ as any;
  }
    return {
    score: input_.score,symbol: jsonSymbolToApplicationTransform(input_.symbol)
  }!;
}export function jsonGraphResponseToTransportTransform(
  input_?: GraphResponse | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositoryId: input_.repositoryId,revision: input_.revision,rootSymbolId: input_.rootSymbolId,direction: input_.direction,depth: input_.depth,maxNodes: input_.maxNodes,maxEdges: input_.maxEdges,nodes: jsonArraySymbolToTransportTransform(input_.nodes),edges: jsonArrayGraphEdgeToTransportTransform(input_.edges),truncated: input_.truncated,freshness: jsonFreshnessToTransportTransform(input_.freshness)
  }!;
}export function jsonGraphResponseToApplicationTransform(
  input_?: any,
): GraphResponse {
  if(!input_) {
    return input_ as any;
  }
    return {
    repositoryId: input_.repositoryId,revision: input_.revision,rootSymbolId: input_.rootSymbolId,direction: input_.direction,depth: input_.depth,maxNodes: input_.maxNodes,maxEdges: input_.maxEdges,nodes: jsonArraySymbolToApplicationTransform(input_.nodes),edges: jsonArrayGraphEdgeToApplicationTransform(input_.edges),truncated: input_.truncated,freshness: jsonFreshnessToApplicationTransform(input_.freshness)
  }!;
}export function jsonArraySymbolToTransportTransform(
  items_?: Array<Symbol> | null,
): any {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = jsonSymbolToTransportTransform(item as any);
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonArraySymbolToApplicationTransform(
  items_?: any,
): Array<Symbol> {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = jsonSymbolToApplicationTransform(item as any);
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonArrayGraphEdgeToTransportTransform(
  items_?: Array<GraphEdge> | null,
): any {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = jsonGraphEdgeToTransportTransform(item as any);
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonArrayGraphEdgeToApplicationTransform(
  items_?: any,
): Array<GraphEdge> {
  if(!items_) {
    return items_ as any;
  }
  const _transformedArray = [];

  for (const item of items_ ?? []) {
    const transformedItem = jsonGraphEdgeToApplicationTransform(item as any);
    _transformedArray.push(transformedItem);
  }

  return _transformedArray as any;
}export function jsonGraphEdgeToTransportTransform(
  input_?: GraphEdge | null,
): any {
  if(!input_) {
    return input_ as any;
  }
    return {
    sourceId: input_.sourceId,targetId: input_.targetId,kind: input_.kind,line: input_.line,column: input_.column
  }!;
}export function jsonGraphEdgeToApplicationTransform(input_?: any): GraphEdge {
  if(!input_) {
    return input_ as any;
  }
    return {
    sourceId: input_.sourceId,targetId: input_.targetId,kind: input_.kind,line: input_.line,column: input_.column
  }!;
}

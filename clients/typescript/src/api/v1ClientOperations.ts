import { parse } from "uri-template";
import type { V1ClientContext } from "./v1ClientContext.js";
import { createRestError } from "../helpers/error.js";
import type { OperationOptions } from "../helpers/interfaces.js";
import {
  jsonApiStatusToApplicationTransform,
  jsonFileResponseToApplicationTransform,
  jsonGraphResponseToApplicationTransform,
  jsonRepositoryListToApplicationTransform,
  jsonRepositorySummaryToApplicationTransform,
  jsonSearchResponseToApplicationTransform,
  jsonSymbolResponseToApplicationTransform,
  jsonTreeResponseToApplicationTransform,
} from "../models/internal/serializers.js";
import type {
  ApiStatus,
  FileResponse,
  GraphDirection,
  GraphResponse,
  RepositoryList,
  RepositorySummary,
  SearchResponse,
  SymbolResponse,
  TreeResponse,
} from "../models/models.js";

export interface GetStatusOptions extends OperationOptions {}
export async function getStatus(
  client: V1ClientContext,
  options?: GetStatusOptions,
): Promise<ApiStatus> {
  const path = parse("/status").expand({});
  const httpRequestOptions = {
    headers: {},
  };
  const response = await client.pathUnchecked(path).get(httpRequestOptions);


  if (typeof options?.operationOptions?.onResponse === "function") {
    options?.operationOptions?.onResponse(response);
  }
  if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
    return jsonApiStatusToApplicationTransform(response.body)!;
  }
  throw createRestError(response);
}
;
export interface ListRepositoriesOptions extends OperationOptions {}
export async function listRepositories(
  client: V1ClientContext,
  options?: ListRepositoriesOptions,
): Promise<RepositoryList> {
  const path = parse("/repositories").expand({});
  const httpRequestOptions = {
    headers: {},
  };
  const response = await client.pathUnchecked(path).get(httpRequestOptions);


  if (typeof options?.operationOptions?.onResponse === "function") {
    options?.operationOptions?.onResponse(response);
  }
  if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
    return jsonRepositoryListToApplicationTransform(response.body)!;
  }
  throw createRestError(response);
}
;
export interface GetRepositoryOptions extends OperationOptions {}
export async function getRepository(
  client: V1ClientContext,
  repositoryId: string,
  options?: GetRepositoryOptions,
): Promise<RepositorySummary> {
  const path = parse("/repositories/{repositoryId}").expand({
    repositoryId: repositoryId
  });
  const httpRequestOptions = {
    headers: {},
  };
  const response = await client.pathUnchecked(path).get(httpRequestOptions);


  if (typeof options?.operationOptions?.onResponse === "function") {
    options?.operationOptions?.onResponse(response);
  }
  if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
    return jsonRepositorySummaryToApplicationTransform(response.body)!;
  }
  throw createRestError(response);
}
;
export interface GetTreeOptions extends OperationOptions {
  path?: string
}
export async function getTree(
  client: V1ClientContext,
  repositoryId: string,
  revision: string,
  options?: GetTreeOptions,
): Promise<TreeResponse> {
  const path = parse("/repositories/{repositoryId}/revisions/{revision}/tree{?path}").expand({
    repositoryId: repositoryId,
    revision: revision,
    ...(options?.path && {path: options.path})
  });
  const httpRequestOptions = {
    headers: {},
  };
  const response = await client.pathUnchecked(path).get(httpRequestOptions);


  if (typeof options?.operationOptions?.onResponse === "function") {
    options?.operationOptions?.onResponse(response);
  }
  if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
    return jsonTreeResponseToApplicationTransform(response.body)!;
  }
  throw createRestError(response);
}
;
export interface GetFileOptions extends OperationOptions {}
export async function getFile(
  client: V1ClientContext,
  repositoryId: string,
  revision: string,
  path: string,
  options?: GetFileOptions,
): Promise<FileResponse> {
  const path_2 = parse("/repositories/{repositoryId}/revisions/{revision}/files{?path}").expand({
    repositoryId: repositoryId,
    revision: revision,
    path: path
  });
  const httpRequestOptions = {
    headers: {},
  };
  const response = await client.pathUnchecked(path_2).get(httpRequestOptions);


  if (typeof options?.operationOptions?.onResponse === "function") {
    options?.operationOptions?.onResponse(response);
  }
  if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
    return jsonFileResponseToApplicationTransform(response.body)!;
  }
  throw createRestError(response);
}
;
export interface GetSymbolOptions extends OperationOptions {}
export async function getSymbol(
  client: V1ClientContext,
  repositoryId: string,
  revision: string,
  symbolId: string,
  options?: GetSymbolOptions,
): Promise<SymbolResponse> {
  const path = parse("/repositories/{repositoryId}/revisions/{revision}/symbols/{symbolId}").expand({
    repositoryId: repositoryId,
    revision: revision,
    symbolId: symbolId
  });
  const httpRequestOptions = {
    headers: {},
  };
  const response = await client.pathUnchecked(path).get(httpRequestOptions);


  if (typeof options?.operationOptions?.onResponse === "function") {
    options?.operationOptions?.onResponse(response);
  }
  if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
    return jsonSymbolResponseToApplicationTransform(response.body)!;
  }
  throw createRestError(response);
}
;
export interface SearchSymbolsOptions extends OperationOptions {
  limit?: number
}
export async function searchSymbols(
  client: V1ClientContext,
  repositoryId: string,
  revision: string,
  query: string,
  options?: SearchSymbolsOptions,
): Promise<SearchResponse> {
  const path = parse("/repositories/{repositoryId}/revisions/{revision}/search{?query,limit}").expand({
    repositoryId: repositoryId,
    revision: revision,
    query: query,
    ...(options?.limit !== undefined && {limit: options.limit})
  });
  const httpRequestOptions = {
    headers: {},
  };
  const response = await client.pathUnchecked(path).get(httpRequestOptions);


  if (typeof options?.operationOptions?.onResponse === "function") {
    options?.operationOptions?.onResponse(response);
  }
  if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
    return jsonSearchResponseToApplicationTransform(response.body)!;
  }
  throw createRestError(response);
}
;
export interface GetSymbolGraphOptions extends OperationOptions {
  direction?: GraphDirection
  depth?: number
  maxNodes?: number
  maxEdges?: number
}
export async function getSymbolGraph(
  client: V1ClientContext,
  repositoryId: string,
  revision: string,
  symbolId: string,
  options?: GetSymbolGraphOptions,
): Promise<GraphResponse> {
  const path = parse("/repositories/{repositoryId}/revisions/{revision}/symbols/{symbolId}/graph{?direction,depth,maxNodes,maxEdges}").expand({
    repositoryId: repositoryId,
    revision: revision,
    symbolId: symbolId,
    ...(options?.direction && {direction: options.direction}),
    ...(options?.depth !== undefined && {depth: options.depth}),
    ...(options?.maxNodes !== undefined && {maxNodes: options.maxNodes}),
    ...(options?.maxEdges !== undefined && {maxEdges: options.maxEdges})
  });
  const httpRequestOptions = {
    headers: {},
  };
  const response = await client.pathUnchecked(path).get(httpRequestOptions);


  if (typeof options?.operationOptions?.onResponse === "function") {
    options?.operationOptions?.onResponse(response);
  }
  if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
    return jsonGraphResponseToApplicationTransform(response.body)!;
  }
  throw createRestError(response);
}
;

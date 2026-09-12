import type { V1ClientContext } from "./v1ClientContext.js";
import type { OperationOptions } from "../helpers/interfaces.js";
import type { ApiStatus, FileResponse, GraphDirection, GraphResponse, RepositoryList, RepositorySummary, SearchResponse, SymbolResponse, TreeResponse } from "../models/models.js";
export interface GetStatusOptions extends OperationOptions {
}
export declare function getStatus(client: V1ClientContext, options?: GetStatusOptions): Promise<ApiStatus>;
export interface ListRepositoriesOptions extends OperationOptions {
}
export declare function listRepositories(client: V1ClientContext, options?: ListRepositoriesOptions): Promise<RepositoryList>;
export interface GetRepositoryOptions extends OperationOptions {
}
export declare function getRepository(client: V1ClientContext, repositoryId: string, options?: GetRepositoryOptions): Promise<RepositorySummary>;
export interface GetTreeOptions extends OperationOptions {
    path?: string;
}
export declare function getTree(client: V1ClientContext, repositoryId: string, revision: string, options?: GetTreeOptions): Promise<TreeResponse>;
export interface GetFileOptions extends OperationOptions {
}
export declare function getFile(client: V1ClientContext, repositoryId: string, revision: string, path: string, options?: GetFileOptions): Promise<FileResponse>;
export interface GetSymbolOptions extends OperationOptions {
}
export declare function getSymbol(client: V1ClientContext, repositoryId: string, revision: string, symbolId: string, options?: GetSymbolOptions): Promise<SymbolResponse>;
export interface SearchSymbolsOptions extends OperationOptions {
    limit?: number;
}
export declare function searchSymbols(client: V1ClientContext, repositoryId: string, revision: string, query: string, options?: SearchSymbolsOptions): Promise<SearchResponse>;
export interface GetSymbolGraphOptions extends OperationOptions {
    direction?: GraphDirection;
    depth?: number;
    maxNodes?: number;
    maxEdges?: number;
}
export declare function getSymbolGraph(client: V1ClientContext, repositoryId: string, revision: string, symbolId: string, options?: GetSymbolGraphOptions): Promise<GraphResponse>;
//# sourceMappingURL=v1ClientOperations.d.ts.map
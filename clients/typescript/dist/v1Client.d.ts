import type { BearerTokenCredential } from "@typespec/ts-http-runtime";
import { type V1ClientOptions } from "./api/v1ClientContext.js";
import { type GetFileOptions, type GetRepositoryOptions, type GetStatusOptions, type GetSymbolGraphOptions, type GetSymbolOptions, type GetTreeOptions, type ListRepositoriesOptions, type SearchSymbolsOptions } from "./api/v1ClientOperations.js";
export declare class V1Client {
    #private;
    constructor(credential: BearerTokenCredential, options?: V1ClientOptions);
    getStatus(options?: GetStatusOptions): Promise<import("./index.js").ApiStatus>;
    listRepositories(options?: ListRepositoriesOptions): Promise<import("./index.js").RepositoryList>;
    getRepository(repositoryId: string, options?: GetRepositoryOptions): Promise<import("./index.js").RepositorySummary>;
    getTree(repositoryId: string, revision: string, options?: GetTreeOptions): Promise<import("./index.js").TreeResponse>;
    getFile(repositoryId: string, revision: string, path: string, options?: GetFileOptions): Promise<import("./index.js").FileResponse>;
    getSymbol(repositoryId: string, revision: string, symbolId: string, options?: GetSymbolOptions): Promise<import("./index.js").SymbolResponse>;
    searchSymbols(repositoryId: string, revision: string, query: string, options?: SearchSymbolsOptions): Promise<import("./index.js").SearchResponse>;
    getSymbolGraph(repositoryId: string, revision: string, symbolId: string, options?: GetSymbolGraphOptions): Promise<import("./index.js").GraphResponse>;
}
//# sourceMappingURL=v1Client.d.ts.map
// TypeSpec runtime 0.2.1 probes process while warning about explicit HTTP loopback.
if (!('process' in globalThis)) Object.defineProperty(globalThis, 'process', { value: {}, configurable: true });
import type { BearerTokenCredential } from "@typespec/ts-http-runtime";
import {
  createV1ClientContext,
  type V1ClientContext,
  type V1ClientOptions,
} from "./api/v1ClientContext.js";
import {
  getFile,
  type GetFileOptions,
  getRepository,
  type GetRepositoryOptions,
  getStatus,
  type GetStatusOptions,
  getSymbol,
  getSymbolGraph,
  type GetSymbolGraphOptions,
  type GetSymbolOptions,
  getTree,
  type GetTreeOptions,
  listRepositories,
  type ListRepositoriesOptions,
  searchSymbols,
  type SearchSymbolsOptions,
} from "./api/v1ClientOperations.js";

export class V1Client {
  #context: V1ClientContext
  constructor(credential: BearerTokenCredential, options?: V1ClientOptions) {
    this.#context = createV1ClientContext(credential, options);

  }
  async getStatus(options?: GetStatusOptions) {
    return getStatus(this.#context, options);
  };
  async listRepositories(options?: ListRepositoriesOptions) {
    return listRepositories(this.#context, options);
  };
  async getRepository(repositoryId: string, options?: GetRepositoryOptions) {
    return getRepository(this.#context, repositoryId, options);
  };
  async getTree(
    repositoryId: string,
    revision: string,
    options?: GetTreeOptions,
  ) {
    return getTree(this.#context, repositoryId, revision, options);
  };
  async getFile(
    repositoryId: string,
    revision: string,
    path: string,
    options?: GetFileOptions,
  ) {
    return getFile(this.#context, repositoryId, revision, path, options);
  };
  async getSymbol(
    repositoryId: string,
    revision: string,
    symbolId: string,
    options?: GetSymbolOptions,
  ) {
    return getSymbol(this.#context, repositoryId, revision, symbolId, options);
  };
  async searchSymbols(
    repositoryId: string,
    revision: string,
    query: string,
    options?: SearchSymbolsOptions,
  ) {
    return searchSymbols(this.#context, repositoryId, revision, query, options);
  };
  async getSymbolGraph(
    repositoryId: string,
    revision: string,
    symbolId: string,
    options?: GetSymbolGraphOptions,
  ) {
    return getSymbolGraph(
      this.#context,
      repositoryId,
      revision,
      symbolId,
      options
    );
  }
}

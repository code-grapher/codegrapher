// TypeSpec runtime 0.2.1 probes process while warning about explicit HTTP loopback.
if (!('process' in globalThis))
    Object.defineProperty(globalThis, 'process', { value: {}, configurable: true });
import { createV1ClientContext, } from "./api/v1ClientContext.js";
import { getFile, getRepository, getStatus, getSymbol, getSymbolGraph, getTree, listRepositories, searchSymbols, } from "./api/v1ClientOperations.js";
export class V1Client {
    #context;
    constructor(credential, options) {
        this.#context = createV1ClientContext(credential, options);
    }
    async getStatus(options) {
        return getStatus(this.#context, options);
    }
    ;
    async listRepositories(options) {
        return listRepositories(this.#context, options);
    }
    ;
    async getRepository(repositoryId, options) {
        return getRepository(this.#context, repositoryId, options);
    }
    ;
    async getTree(repositoryId, revision, options) {
        return getTree(this.#context, repositoryId, revision, options);
    }
    ;
    async getFile(repositoryId, revision, path, options) {
        return getFile(this.#context, repositoryId, revision, path, options);
    }
    ;
    async getSymbol(repositoryId, revision, symbolId, options) {
        return getSymbol(this.#context, repositoryId, revision, symbolId, options);
    }
    ;
    async searchSymbols(repositoryId, revision, query, options) {
        return searchSymbols(this.#context, repositoryId, revision, query, options);
    }
    ;
    async getSymbolGraph(repositoryId, revision, symbolId, options) {
        return getSymbolGraph(this.#context, repositoryId, revision, symbolId, options);
    }
}
//# sourceMappingURL=v1Client.js.map
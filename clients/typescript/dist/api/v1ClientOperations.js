import { parse } from "uri-template";
import { createRestError } from "../helpers/error.js";
import { jsonApiStatusToApplicationTransform, jsonFileResponseToApplicationTransform, jsonGraphResponseToApplicationTransform, jsonRepositoryListToApplicationTransform, jsonRepositorySummaryToApplicationTransform, jsonSearchResponseToApplicationTransform, jsonSymbolResponseToApplicationTransform, jsonTreeResponseToApplicationTransform, } from "../models/internal/serializers.js";
export async function getStatus(client, options) {
    const path = parse("/status").expand({});
    const httpRequestOptions = {
        headers: {},
    };
    const response = await client.pathUnchecked(path).get(httpRequestOptions);
    if (typeof options?.operationOptions?.onResponse === "function") {
        options?.operationOptions?.onResponse(response);
    }
    if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
        return jsonApiStatusToApplicationTransform(response.body);
    }
    throw createRestError(response);
}
;
export async function listRepositories(client, options) {
    const path = parse("/repositories").expand({});
    const httpRequestOptions = {
        headers: {},
    };
    const response = await client.pathUnchecked(path).get(httpRequestOptions);
    if (typeof options?.operationOptions?.onResponse === "function") {
        options?.operationOptions?.onResponse(response);
    }
    if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
        return jsonRepositoryListToApplicationTransform(response.body);
    }
    throw createRestError(response);
}
;
export async function getRepository(client, repositoryId, options) {
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
        return jsonRepositorySummaryToApplicationTransform(response.body);
    }
    throw createRestError(response);
}
;
export async function getTree(client, repositoryId, revision, options) {
    const path = parse("/repositories/{repositoryId}/revisions/{revision}/tree{?path}").expand({
        repositoryId: repositoryId,
        revision: revision,
        ...(options?.path && { path: options.path })
    });
    const httpRequestOptions = {
        headers: {},
    };
    const response = await client.pathUnchecked(path).get(httpRequestOptions);
    if (typeof options?.operationOptions?.onResponse === "function") {
        options?.operationOptions?.onResponse(response);
    }
    if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
        return jsonTreeResponseToApplicationTransform(response.body);
    }
    throw createRestError(response);
}
;
export async function getFile(client, repositoryId, revision, path, options) {
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
        return jsonFileResponseToApplicationTransform(response.body);
    }
    throw createRestError(response);
}
;
export async function getSymbol(client, repositoryId, revision, symbolId, options) {
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
        return jsonSymbolResponseToApplicationTransform(response.body);
    }
    throw createRestError(response);
}
;
export async function searchSymbols(client, repositoryId, revision, query, options) {
    const path = parse("/repositories/{repositoryId}/revisions/{revision}/search{?query,limit}").expand({
        repositoryId: repositoryId,
        revision: revision,
        query: query,
        ...(options?.limit !== undefined && { limit: options.limit })
    });
    const httpRequestOptions = {
        headers: {},
    };
    const response = await client.pathUnchecked(path).get(httpRequestOptions);
    if (typeof options?.operationOptions?.onResponse === "function") {
        options?.operationOptions?.onResponse(response);
    }
    if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
        return jsonSearchResponseToApplicationTransform(response.body);
    }
    throw createRestError(response);
}
;
export async function getSymbolGraph(client, repositoryId, revision, symbolId, options) {
    const path = parse("/repositories/{repositoryId}/revisions/{revision}/symbols/{symbolId}/graph{?direction,depth,maxNodes,maxEdges}").expand({
        repositoryId: repositoryId,
        revision: revision,
        symbolId: symbolId,
        ...(options?.direction && { direction: options.direction }),
        ...(options?.depth !== undefined && { depth: options.depth }),
        ...(options?.maxNodes !== undefined && { maxNodes: options.maxNodes }),
        ...(options?.maxEdges !== undefined && { maxEdges: options.maxEdges })
    });
    const httpRequestOptions = {
        headers: {},
    };
    const response = await client.pathUnchecked(path).get(httpRequestOptions);
    if (typeof options?.operationOptions?.onResponse === "function") {
        options?.operationOptions?.onResponse(response);
    }
    if (+response.status === 200 && response.headers["content-type"]?.includes("application/json")) {
        return jsonGraphResponseToApplicationTransform(response.body);
    }
    throw createRestError(response);
}
;
//# sourceMappingURL=v1ClientOperations.js.map
export function decodeBase64(value) {
    if (!value) {
        return value;
    }
    // Normalize Base64URL to Base64
    const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
        .padEnd(value.length + (4 - (value.length % 4)) % 4, '=');
    return new Uint8Array(Buffer.from(base64, 'base64'));
}
export function encodeUint8Array(value, encoding) {
    if (!value) {
        return value;
    }
    return Buffer.from(value).toString(encoding);
}
export function dateDeserializer(date) {
    if (!date) {
        return date;
    }
    return new Date(date);
}
export function dateRfc7231Deserializer(date) {
    if (!date) {
        return date;
    }
    return new Date(date);
}
export function dateRfc3339Serializer(date) {
    if (!date) {
        return date;
    }
    return date.toISOString();
}
export function dateRfc7231Serializer(date) {
    if (!date) {
        return date;
    }
    return date.toUTCString();
}
export function dateUnixTimestampSerializer(date) {
    if (!date) {
        return date;
    }
    return Math.floor(date.getTime() / 1000);
}
export function dateUnixTimestampDeserializer(date) {
    if (!date) {
        return date;
    }
    return new Date(date * 1000);
}
export function jsonApiStatusToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        apiVersion: input_.apiVersion, serverVersion: input_.serverVersion, capabilities: jsonArrayStringToTransportTransform(input_.capabilities), repositoryCount: input_.repositoryCount, freshness: jsonFreshnessToTransportTransform(input_.freshness), limits: jsonApiLimitsToTransportTransform(input_.limits)
    };
}
export function jsonApiStatusToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        apiVersion: input_.apiVersion, serverVersion: input_.serverVersion, capabilities: jsonArrayStringToApplicationTransform(input_.capabilities), repositoryCount: input_.repositoryCount, freshness: jsonFreshnessToApplicationTransform(input_.freshness), limits: jsonApiLimitsToApplicationTransform(input_.limits)
    };
}
export function jsonArrayStringToTransportTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = item;
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonArrayStringToApplicationTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = item;
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonFreshnessToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        state: input_.state, indexedAt: dateRfc3339Serializer(input_.indexedAt), lastSuccessfulSync: dateRfc3339Serializer(input_.lastSuccessfulSync), lastError: input_.lastError
    };
}
export function jsonFreshnessToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        state: input_.state, indexedAt: dateDeserializer(input_.indexedAt), lastSuccessfulSync: dateDeserializer(input_.lastSuccessfulSync), lastError: input_.lastError
    };
}
export function jsonApiLimitsToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        maxTreeEntries: input_.maxTreeEntries, maxFileBytes: input_.maxFileBytes, maxSearchResults: input_.maxSearchResults, maxGraphDepth: input_.maxGraphDepth, maxGraphNodes: input_.maxGraphNodes, maxGraphEdges: input_.maxGraphEdges
    };
}
export function jsonApiLimitsToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        maxTreeEntries: input_.maxTreeEntries, maxFileBytes: input_.maxFileBytes, maxSearchResults: input_.maxSearchResults, maxGraphDepth: input_.maxGraphDepth, maxGraphNodes: input_.maxGraphNodes, maxGraphEdges: input_.maxGraphEdges
    };
}
export function jsonRepositoryListToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositories: jsonArrayRepositorySummaryToTransportTransform(input_.repositories)
    };
}
export function jsonRepositoryListToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositories: jsonArrayRepositorySummaryToApplicationTransform(input_.repositories)
    };
}
export function jsonArrayRepositorySummaryToTransportTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = jsonRepositorySummaryToTransportTransform(item);
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonArrayRepositorySummaryToApplicationTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = jsonRepositorySummaryToApplicationTransform(item);
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonRepositorySummaryToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        id: input_.id, label: input_.label, remote: input_.remote, branch: input_.branch, headCommit: input_.headCommit, revision: input_.revision, freshness: jsonFreshnessToTransportTransform(input_.freshness), fileCount: input_.fileCount, symbolCount: input_.symbolCount, edgeCount: input_.edgeCount
    };
}
export function jsonRepositorySummaryToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        id: input_.id, label: input_.label, remote: input_.remote, branch: input_.branch, headCommit: input_.headCommit, revision: input_.revision, freshness: jsonFreshnessToApplicationTransform(input_.freshness), fileCount: input_.fileCount, symbolCount: input_.symbolCount, edgeCount: input_.edgeCount
    };
}
export function jsonTreeResponseToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositoryId: input_.repositoryId, revision: input_.revision, path: input_.path, entries: jsonArrayTreeEntryToTransportTransform(input_.entries), limit: input_.limit, truncated: input_.truncated, freshness: jsonFreshnessToTransportTransform(input_.freshness)
    };
}
export function jsonTreeResponseToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositoryId: input_.repositoryId, revision: input_.revision, path: input_.path, entries: jsonArrayTreeEntryToApplicationTransform(input_.entries), limit: input_.limit, truncated: input_.truncated, freshness: jsonFreshnessToApplicationTransform(input_.freshness)
    };
}
export function jsonArrayTreeEntryToTransportTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = jsonTreeEntryToTransportTransform(item);
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonArrayTreeEntryToApplicationTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = jsonTreeEntryToApplicationTransform(item);
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonTreeEntryToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        name: input_.name, path: input_.path, kind: input_.kind, size: input_.size, language: input_.language, contentHash: input_.contentHash
    };
}
export function jsonTreeEntryToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        name: input_.name, path: input_.path, kind: input_.kind, size: input_.size, language: input_.language, contentHash: input_.contentHash
    };
}
export function jsonFileResponseToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositoryId: input_.repositoryId, revision: input_.revision, path: input_.path, language: input_.language, size: input_.size, lineCount: input_.lineCount, contentHash: input_.contentHash, content: input_.content, freshness: jsonFreshnessToTransportTransform(input_.freshness)
    };
}
export function jsonFileResponseToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositoryId: input_.repositoryId, revision: input_.revision, path: input_.path, language: input_.language, size: input_.size, lineCount: input_.lineCount, contentHash: input_.contentHash, content: input_.content, freshness: jsonFreshnessToApplicationTransform(input_.freshness)
    };
}
export function jsonSymbolResponseToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositoryId: input_.repositoryId, revision: input_.revision, symbol: jsonSymbolToTransportTransform(input_.symbol), freshness: jsonFreshnessToTransportTransform(input_.freshness)
    };
}
export function jsonSymbolResponseToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositoryId: input_.repositoryId, revision: input_.revision, symbol: jsonSymbolToApplicationTransform(input_.symbol), freshness: jsonFreshnessToApplicationTransform(input_.freshness)
    };
}
export function jsonSymbolToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        id: input_.id, kind: input_.kind, name: input_.name, qualifiedName: input_.qualifiedName, filePath: input_.filePath, language: input_.language, range: jsonSourceRangeToTransportTransform(input_.range), signature: input_.signature, docstring: input_.docstring, visibility: input_.visibility, exported: input_.exported
    };
}
export function jsonSymbolToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        id: input_.id, kind: input_.kind, name: input_.name, qualifiedName: input_.qualifiedName, filePath: input_.filePath, language: input_.language, range: jsonSourceRangeToApplicationTransform(input_.range), signature: input_.signature, docstring: input_.docstring, visibility: input_.visibility, exported: input_.exported
    };
}
export function jsonSourceRangeToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        startLine: input_.startLine, endLine: input_.endLine, startColumn: input_.startColumn, endColumn: input_.endColumn
    };
}
export function jsonSourceRangeToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        startLine: input_.startLine, endLine: input_.endLine, startColumn: input_.startColumn, endColumn: input_.endColumn
    };
}
export function jsonSearchResponseToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositoryId: input_.repositoryId, revision: input_.revision, query: input_.query, results: jsonArraySearchResultToTransportTransform(input_.results), limit: input_.limit, truncated: input_.truncated, freshness: jsonFreshnessToTransportTransform(input_.freshness)
    };
}
export function jsonSearchResponseToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositoryId: input_.repositoryId, revision: input_.revision, query: input_.query, results: jsonArraySearchResultToApplicationTransform(input_.results), limit: input_.limit, truncated: input_.truncated, freshness: jsonFreshnessToApplicationTransform(input_.freshness)
    };
}
export function jsonArraySearchResultToTransportTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = jsonSearchResultToTransportTransform(item);
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonArraySearchResultToApplicationTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = jsonSearchResultToApplicationTransform(item);
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonSearchResultToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        score: input_.score, symbol: jsonSymbolToTransportTransform(input_.symbol)
    };
}
export function jsonSearchResultToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        score: input_.score, symbol: jsonSymbolToApplicationTransform(input_.symbol)
    };
}
export function jsonGraphResponseToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositoryId: input_.repositoryId, revision: input_.revision, rootSymbolId: input_.rootSymbolId, direction: input_.direction, depth: input_.depth, maxNodes: input_.maxNodes, maxEdges: input_.maxEdges, nodes: jsonArraySymbolToTransportTransform(input_.nodes), edges: jsonArrayGraphEdgeToTransportTransform(input_.edges), truncated: input_.truncated, freshness: jsonFreshnessToTransportTransform(input_.freshness)
    };
}
export function jsonGraphResponseToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        repositoryId: input_.repositoryId, revision: input_.revision, rootSymbolId: input_.rootSymbolId, direction: input_.direction, depth: input_.depth, maxNodes: input_.maxNodes, maxEdges: input_.maxEdges, nodes: jsonArraySymbolToApplicationTransform(input_.nodes), edges: jsonArrayGraphEdgeToApplicationTransform(input_.edges), truncated: input_.truncated, freshness: jsonFreshnessToApplicationTransform(input_.freshness)
    };
}
export function jsonArraySymbolToTransportTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = jsonSymbolToTransportTransform(item);
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonArraySymbolToApplicationTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = jsonSymbolToApplicationTransform(item);
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonArrayGraphEdgeToTransportTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = jsonGraphEdgeToTransportTransform(item);
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonArrayGraphEdgeToApplicationTransform(items_) {
    if (!items_) {
        return items_;
    }
    const _transformedArray = [];
    for (const item of items_ ?? []) {
        const transformedItem = jsonGraphEdgeToApplicationTransform(item);
        _transformedArray.push(transformedItem);
    }
    return _transformedArray;
}
export function jsonGraphEdgeToTransportTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        sourceId: input_.sourceId, targetId: input_.targetId, kind: input_.kind, line: input_.line, column: input_.column
    };
}
export function jsonGraphEdgeToApplicationTransform(input_) {
    if (!input_) {
        return input_;
    }
    return {
        sourceId: input_.sourceId, targetId: input_.targetId, kind: input_.kind, line: input_.line, column: input_.column
    };
}
//# sourceMappingURL=serializers.js.map
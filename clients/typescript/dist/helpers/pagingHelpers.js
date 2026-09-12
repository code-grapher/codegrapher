export function buildPagedAsyncIterator(options) {
    const pagedResult = {
        getElements: options.getElements,
        getPage: options.getPagedResponse,
        byPage: (setting) => {
            return getPageAsyncIterator(pagedResult, { setting });
        },
    };
    const iter = getItemAsyncIterator(pagedResult);
    return {
        next() {
            return iter.next();
        },
        [Symbol.asyncIterator]() {
            return this;
        },
        byPage: pagedResult.byPage,
    };
}
async function* getItemAsyncIterator(pagedResult) {
    const pages = getPageAsyncIterator(pagedResult);
    for await (const page of pages) {
        const results = pagedResult.getElements(page);
        yield* results;
    }
}
async function* getPageAsyncIterator(pagedResult, options = {}) {
    let response = await pagedResult.getPage(undefined, options.setting);
    let results = response?.pagedResponse;
    let nextToken = response?.nextToken;
    if (!results) {
        return;
    }
    yield results;
    while (nextToken) {
        response = await pagedResult.getPage(nextToken, options.setting);
        if (!response) {
            return;
        }
        results = response.pagedResponse;
        nextToken = response.nextToken;
        yield results;
    }
}
//# sourceMappingURL=pagingHelpers.js.map
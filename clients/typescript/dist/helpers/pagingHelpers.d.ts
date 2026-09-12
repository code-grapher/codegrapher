/**
* An interface that allows async iterable iteration both to completion and by page.
*/
export interface PagedAsyncIterableIterator<TElement, TPageResponse, TPageSettings> {
    /**
      * The next method, part of the iteration protocol
      */
    next(): Promise<IteratorResult<TElement>>;
    /**
      * The connection to the async iterator, part of the iteration protocol
      */
    [Symbol.asyncIterator](): PagedAsyncIterableIterator<TElement, TPageResponse, TPageSettings>;
    /**
      * Return an AsyncIterableIterator that works a page at a time
      */
    byPage: (settings?: TPageSettings) => AsyncIterableIterator<TPageResponse>;
} /**
* An interface that describes how to communicate with the service.
*/
export interface BuildPagedAsyncIteratorOptions<TElement, TPageResponse, TPageSettings> {
    getElements: (response: TPageResponse) => TElement[];
    getPagedResponse: (nextToken?: string, settings?: TPageSettings) => Promise<{
        pagedResponse: TPageResponse;
        nextToken?: string;
    } | undefined>;
} /**
* Helper to paginate results in a generic way and return a PagedAsyncIterableIterator
*/
export declare function buildPagedAsyncIterator<TElement, TPageResponse, TPageSettings>(options: BuildPagedAsyncIteratorOptions<TElement, TPageResponse, TPageSettings>): PagedAsyncIterableIterator<TElement, TPageResponse, TPageSettings>;
//# sourceMappingURL=pagingHelpers.d.ts.map
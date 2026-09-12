import { type BearerTokenCredential, type Client, type ClientOptions } from "@typespec/ts-http-runtime";
export interface V1ClientContext extends Client {
}
export interface V1ClientOptions extends ClientOptions {
    endpoint?: string;
}
export declare function createV1ClientContext(credential: BearerTokenCredential, options?: V1ClientOptions): V1ClientContext;
//# sourceMappingURL=v1ClientContext.d.ts.map
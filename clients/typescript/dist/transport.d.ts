import { V1Client } from "./v1Client.js";
/** The confinement boundary already provided by the CodeGrapher browser UI. */
export interface V1ClientTransport {
    baseUrl: string;
    fetch: typeof globalThis.fetch;
}
/** Creates the generated client without taking ownership of the browser secret. */
export declare function createV1ClientFromTransport(transport: V1ClientTransport): V1Client;
//# sourceMappingURL=transport.d.ts.map
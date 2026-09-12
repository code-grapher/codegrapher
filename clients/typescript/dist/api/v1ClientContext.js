import { getClient, } from "@typespec/ts-http-runtime";
export function createV1ClientContext(credential, options) {
    const params = {};
    const resolvedEndpoint = options?.endpoint ?? "http://127.0.0.1:7331/codegrapher/v1";
    ;
    return getClient(resolvedEndpoint, {
        ...options, credential, authSchemes: [{
                kind: "http",
                scheme: "bearer"
            }]
    });
}
//# sourceMappingURL=v1ClientContext.js.map
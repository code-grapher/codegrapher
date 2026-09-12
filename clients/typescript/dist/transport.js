import { createHttpHeaders, } from "@typespec/ts-http-runtime";
import { V1Client } from "./v1Client.js";
/** Creates the generated client without taking ownership of the browser secret. */
export function createV1ClientFromTransport(transport) {
    const endpoint = transport.baseUrl.replace(/\/$/, "");
    const parsed = new URL(endpoint);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
        throw new Error("CodeGrapher transport baseUrl must use HTTP or HTTPS.");
    }
    return new V1Client(undefined, {
        endpoint,
        allowInsecureConnection: parsed.protocol === "http:",
        httpClient: fetchAdapter(transport.fetch),
    });
}
function fetchAdapter(fetchImpl) {
    return {
        async sendRequest(request) {
            if (request.body != null) {
                throw new Error("CodeGrapher browser API transport is read-only.");
            }
            const headers = new Headers();
            for (const [name, value] of request.headers)
                headers.append(name, value);
            const response = await fetchImpl(request.url, {
                method: request.method,
                headers,
                signal: request.abortSignal,
                credentials: request.withCredentials ? "include" : "omit",
                redirect: "error",
            });
            const responseHeaders = createHttpHeaders();
            response.headers.forEach((value, name) => responseHeaders.set(name, value));
            return {
                request,
                status: response.status,
                headers: responseHeaders,
                bodyAsText: await response.text(),
            };
        },
    };
}
//# sourceMappingURL=transport.js.map
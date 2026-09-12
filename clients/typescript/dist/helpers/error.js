export class RestError extends Error {
    request;
    response;
    status;
    body;
    headers;
    constructor(message, response) {
        // Create an error message that includes relevant details.
        super(`${message} - HTTP ${response.status} received for ${response.request.method} ${response.request.url}`);
        this.name = 'RestError';
        this.request = response.request;
        this.response = response;
        this.status = response.status;
        this.headers = response.headers;
        this.body = response.body;
        // Set the prototype explicitly.
        Object.setPrototypeOf(this, RestError.prototype);
    }
    static fromHttpResponse(response) {
        const defaultMessage = `Unexpected HTTP status code: ${response.status}`;
        return new RestError(defaultMessage, response);
    }
}
export function createRestError(response) {
    return RestError.fromHttpResponse(response);
}
//# sourceMappingURL=error.js.map
import {
  type BearerTokenCredential,
  type Client,
  type ClientOptions,
  getClient,
} from "@typespec/ts-http-runtime";

export interface V1ClientContext extends Client {

}export interface V1ClientOptions extends ClientOptions {
  endpoint?: string;
}export function createV1ClientContext(
  credential: BearerTokenCredential,
  options?: V1ClientOptions,
): V1ClientContext {
  const params: Record<string, any> = {};
  const resolvedEndpoint = options?.endpoint ?? "http://127.0.0.1:7331/codegrapher/v1";;return getClient(resolvedEndpoint,{
    ...options,credential,authSchemes: [{
      kind: "http",
      scheme: "bearer"
    }]
  })
}

import { readFile, writeFile } from 'node:fs/promises'

const path = 'clients/typescript/src/api/v1ClientContext.ts'
const source = await readFile(path, 'utf8')
const from = '  const resolvedEndpoint = "http://127.0.0.1:7331/codegrapher/v1".replace(/{([^}]+)}/g, (_, key) =>\n' +
  '    key in params ? String(params[key]) : (() => { throw new Error(`Missing parameter: ${key}`); })()\n' +
  '  );'
const to = '  const resolvedEndpoint = options?.endpoint ?? "http://127.0.0.1:7331/codegrapher/v1";'
if (!source.includes(from)) {
  throw new Error('TypeSpec client emitter output changed; review endpoint override correction')
}
await writeFile(path, source.replace(from, to), 'utf8')

const operationsPath = 'clients/typescript/src/api/v1ClientOperations.ts'
let operations = await readFile(operationsPath, 'utf8')
for (const option of ['limit', 'depth', 'maxNodes', 'maxEdges']) {
  const fromOption = `...(options?.${option} && {${option}: options.${option}})`
  const toOption = `...(options?.${option} !== undefined && {${option}: options.${option}})`
  if (!operations.includes(fromOption)) {
    throw new Error(`TypeSpec client emitter output changed; review zero-preserving ${option} correction`)
  }
  operations = operations.replaceAll(fromOption, toOption)
}
await writeFile(operationsPath, operations, 'utf8')

const clientPath = 'clients/typescript/src/v1Client.ts'
const client = await readFile(clientPath, 'utf8')
const shim = "// TypeSpec runtime 0.2.1 probes process while warning about explicit HTTP loopback.\nif (!('process' in globalThis)) Object.defineProperty(globalThis, 'process', { value: {}, configurable: true });\n"
if (!client.startsWith(shim)) {
  await writeFile(clientPath, shim + client, 'utf8')
}

const indexPath = 'clients/typescript/src/index.ts'
const index = await readFile(indexPath, 'utf8')
const transportExport = 'export * from "./transport.js";\n'
if (!index.includes(transportExport)) {
  await writeFile(indexPath, index + transportExport, 'utf8')
}

const transport = `import {
  createHttpHeaders,
  type HttpClient,
  type PipelineRequest,
  type PipelineResponse,
} from "@typespec/ts-http-runtime";
import { V1Client } from "./v1Client.js";

/** The confinement boundary already provided by the CodeGrapher browser UI. */
export interface V1ClientTransport {
  baseUrl: string;
  fetch: typeof globalThis.fetch;
}

/** Creates the generated client without taking ownership of the browser secret. */
export function createV1ClientFromTransport(transport: V1ClientTransport): V1Client {
  const endpoint = transport.baseUrl.replace(/\\/$/, "");
  const parsed = new URL(endpoint);
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error("CodeGrapher transport baseUrl must use HTTP or HTTPS.");
  }
  return new V1Client(undefined as never, {
    endpoint,
    allowInsecureConnection: parsed.protocol === "http:",
    httpClient: fetchAdapter(transport.fetch),
  });
}

function fetchAdapter(fetchImpl: typeof globalThis.fetch): HttpClient {
  return {
    async sendRequest(request: PipelineRequest): Promise<PipelineResponse> {
      if (request.body != null) {
        throw new Error("CodeGrapher browser API transport is read-only.");
      }
      const headers = new Headers();
      for (const [name, value] of request.headers) headers.append(name, value);
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
`
await writeFile('clients/typescript/src/transport.ts', transport, 'utf8')

const packagePath = 'clients/typescript/package.json'
const packageJson = JSON.parse(await readFile(packagePath, 'utf8'))
packageJson.files = ['dist']
packageJson.types = './dist/index.d.ts'
packageJson.scripts = {
  ...packageJson.scripts,
  prepare: 'pnpm run build',
}
packageJson.exports = {
  '.': {
    types: './dist/index.d.ts',
    import: './dist/index.js',
    default: './dist/index.js',
  },
  './models': {
    types: './dist/models/index.d.ts',
    import: './dist/models/index.js',
    default: './dist/models/index.js',
  },
}
await writeFile(packagePath, `${JSON.stringify(packageJson, null, 2)}\n`, 'utf8')

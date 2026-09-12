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

const clientPath = 'clients/typescript/src/v1Client.ts'
const client = await readFile(clientPath, 'utf8')
const shim = "// TypeSpec runtime 0.2.1 probes process while warning about explicit HTTP loopback.\nif (!('process' in globalThis)) Object.defineProperty(globalThis, 'process', { value: {}, configurable: true });\n"
if (client.startsWith(shim)) {
  process.exit(0)
}
await writeFile(clientPath, shim + client, 'utf8')

import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'

const generatedStatus = () => execFileSync(
  'git',
  [
    'status',
    '--porcelain',
    '--untracked-files=all',
    '--',
    'api/openapi/v1',
    'clients/typescript',
  ],
  { encoding: 'utf8' },
).trim()

const statusBeforeGeneration = generatedStatus()
execFileSync('pnpm', ['generate:contract'], { stdio: 'inherit' })
execFileSync('pnpm', ['build:client'], { stdio: 'inherit' })
const context = readFileSync('clients/typescript/src/api/v1ClientContext.ts', 'utf8')
if (!context.includes('options?.endpoint ?? "http://127.0.0.1:7331/codegrapher/v1"')) {
  throw new Error('generated client does not honor its endpoint option')
}
const client = readFileSync('clients/typescript/src/v1Client.ts', 'utf8')
if (!client.includes("Object.defineProperty(globalThis, 'process'")) {
  throw new Error('generated client is missing the browser-safe insecure-loopback shim')
}
const transport = readFileSync('clients/typescript/src/transport.ts', 'utf8')
if (!transport.includes('createV1ClientFromTransport')) {
  throw new Error('generated client is missing the browser transport adapter')
}
const operations = readFileSync('clients/typescript/src/api/v1ClientOperations.ts', 'utf8')
for (const option of ['limit', 'depth', 'maxNodes', 'maxEdges']) {
  if (!operations.includes(`options?.${option} !== undefined`)) {
    throw new Error(`generated client drops an explicit zero ${option}`)
  }
}
const statusAfterGeneration = generatedStatus()

if (statusBeforeGeneration || statusAfterGeneration) {
	process.stderr.write(
		`Generated browser contract is stale. Run pnpm generate:contract and commit:\n${statusBeforeGeneration || statusAfterGeneration}\n`,
	)
	process.exit(1)
}

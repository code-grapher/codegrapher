import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'

execFileSync('pnpm', ['generate:contract'], { stdio: 'inherit' })
const context = readFileSync('clients/typescript/src/api/v1ClientContext.ts', 'utf8')
if (!context.includes('options?.endpoint ?? "http://127.0.0.1:7331/codegrapher/v1"')) {
  throw new Error('generated client does not honor its endpoint option')
}
const client = readFileSync('clients/typescript/src/v1Client.ts', 'utf8')
if (!client.includes("Object.defineProperty(globalThis, 'process'")) {
  throw new Error('generated client is missing the browser-safe insecure-loopback shim')
}
const status = execFileSync(
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

if (status) {
  process.stderr.write(
    `Generated browser contract is stale. Run pnpm generate:contract and commit:\n${status}\n`,
  )
  process.exit(1)
}

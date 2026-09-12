import { spawnSync } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'

const generatedPath = 'api/openapi/v1/openapi.yaml'
const original = readFileSync(generatedPath, 'utf8')
try {
  writeFileSync(generatedPath, original + '\n# deliberate drift probe\n', 'utf8')
  const result = spawnSync('pnpm', ['check:contract'], { encoding: 'utf8' })
  if (result.status === 0) {
    throw new Error('contract drift check accepted a modified generated artifact')
  }
  if (!`${result.stdout}\n${result.stderr}`.includes('Generated browser contract is stale')) {
    throw new Error(`contract drift check failed for the wrong reason:\n${result.stdout}\n${result.stderr}`)
  }
} finally {
  writeFileSync(generatedPath, original, 'utf8')
}

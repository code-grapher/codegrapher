import { chromium } from 'playwright-core'
import { existsSync } from 'node:fs'

const [pageUrl, apiEndpoint, token] = process.argv.slice(2)
const candidates = [
  process.env.CHROME_BIN,
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
].filter(Boolean)
const executablePath = candidates.find(existsSync)
if (!executablePath) throw new Error('Chrome executable not found; set CHROME_BIN')

const browser = await chromium.launch({ executablePath, headless: true })
try {
  const page = await browser.newPage()
  page.on('requestfailed', (request) => process.stderr.write(`request failed ${request.method()} ${request.url()}: ${request.failure()?.errorText}\n`))
  page.on('response', (response) => {
    if (!response.ok()) process.stderr.write(`response ${response.status()} ${response.request().method()} ${response.url()}\n`)
  })
  await page.goto(pageUrl)
  const result = await page.evaluate(
    async ({ endpoint, credential }) => globalThis.runCodeGrapherJourney(endpoint, credential),
    { endpoint: apiEndpoint, credential: token },
  )
  process.stdout.write(JSON.stringify(result))
} finally {
  await browser.close()
}

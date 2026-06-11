/**
 * Minimal INGR parser — inline fallback.
 *
 * TODO: swap this for the official @ingr/codec ESM build once
 * github.com/ingr-io/ingr-js ships a dist/ or npm release. The library
 * uses vite to produce both ESM (index.js) and CJS (index.cjs) but the
 * dist/ is not committed and no npm release exists as of 2026-06-11.
 * When it does ship, vendor the built ESM file here and update the import
 * in app.js.
 *
 * This implementation is derived from the ingr-js source (MIT licence).
 * Source: https://github.com/ingr-io/ingr-js  MIT © ingr-io
 */

/**
 * @param {string} text
 * @returns {{ recordsetName: string, columns: string[], records: Record<string, unknown>[] }}
 */
export function parseIngr(text) {
  const rawLines = text.split('\n');
  const lines = rawLines[rawLines.length - 1] === '' ? rawLines.slice(0, -1) : rawLines;

  if (lines.length === 0) throw new Error('Empty INGR file');

  const headerMatch = lines[0].match(/^#\s+INGR\.io\s+\|\s+(.+?):\s+(.+)$/);
  if (!headerMatch) throw new Error('Invalid INGR header: ' + lines[0]);

  const recordsetName = headerMatch[1].trim();
  // Column names may have :type annotations — strip them for the key name
  const rawCols = headerMatch[2].split(',').map(c => c.trim());
  const columns = rawCols.map(c => c.replace(/:.*$/, '').trim());
  const n = columns.length;

  // Find footer: last line matching "# N record(s)"
  let footerStart = lines.length;
  for (let i = lines.length - 1; i >= 1; i--) {
    if (/^#\s+\d+\s+records?$/.test(lines[i].trim())) {
      footerStart = i;
      break;
    }
  }

  // Collect data lines (skip delimiter lines: #, #-, #--, ...)
  const dataLines = [];
  for (let i = 1; i < footerStart; i++) {
    if (/^#-*$/.test(lines[i])) continue;
    dataLines.push(lines[i]);
  }

  if (dataLines.length % n !== 0) {
    throw new Error(
      `INGR data line count (${dataLines.length}) not divisible by column count (${n})`
    );
  }

  const records = [];
  for (let i = 0; i < dataLines.length; i += n) {
    const record = {};
    for (let j = 0; j < n; j++) {
      const colName = columns[j].replace(/^\$/, '');
      record[colName] = JSON.parse(dataLines[i + j].trim());
    }
    records.push(record);
  }

  return { recordsetName, columns, records };
}

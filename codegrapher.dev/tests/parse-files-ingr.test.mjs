/**
 * Smoke test: parse files.ingr and verify tree-building logic.
 * Run with: node --test tests/parse-files-ingr.test.mjs
 *
 * Dependency-free (node:test + node:assert + node:fs).
 * The fixture at testdata/go-small-files.ingr was generated from
 * testdata/fixtures/go-small via:
 *   CGO_ENABLED=0 go build -o /tmp/cgr ./cmd/codegrapher
 *   cp -r testdata/fixtures/go-small /tmp/go-small-fixture
 *   cd /tmp/go-small-fixture && /tmp/cgr init && /tmp/cgr export
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const __dir = dirname(fileURLToPath(import.meta.url));
const fixturePath = join(__dir, '..', 'testdata', 'go-small-files.ingr');

// ─── Inline parser (mirrors public/vendor/ingr-codec.js) ────────────────────

function parseIngr(text) {
  const rawLines = text.split('\n');
  const lines = rawLines[rawLines.length - 1] === '' ? rawLines.slice(0, -1) : rawLines;

  if (lines.length === 0) throw new Error('Empty INGR file');

  const headerMatch = lines[0].match(/^#\s+INGR\.io\s+\|\s+(.+?):\s+(.+)$/);
  if (!headerMatch) throw new Error('Invalid INGR header: ' + lines[0]);

  const recordsetName = headerMatch[1].trim();
  const rawCols = headerMatch[2].split(',').map(c => c.trim());
  const columns = rawCols.map(c => c.replace(/:.*$/, '').trim());
  const n = columns.length;

  let footerStart = lines.length;
  for (let i = lines.length - 1; i >= 1; i--) {
    if (/^#\s+\d+\s+records?$/.test(lines[i].trim())) {
      footerStart = i;
      break;
    }
  }

  const dataLines = [];
  for (let i = 1; i < footerStart; i++) {
    if (/^#-*$/.test(lines[i])) continue;
    dataLines.push(lines[i]);
  }

  if (dataLines.length % n !== 0) {
    throw new Error(`INGR data line count (${dataLines.length}) not divisible by column count (${n})`);
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

// ─── Tree builder (mirrors public/app.js) ───────────────────────────────────

function buildTree(paths) {
  const root = { dirs: {}, files: [] };
  for (const p of paths) {
    const parts = p.split('/');
    let node = root;
    for (let i = 0; i < parts.length - 1; i++) {
      const dir = parts[i];
      if (!node.dirs[dir]) node.dirs[dir] = { dirs: {}, files: [] };
      node = node.dirs[dir];
    }
    node.files.push(parts[parts.length - 1]);
  }
  return root;
}

// ─── Filter (mirrors public/app.js) ─────────────────────────────────────────

function escapeRegExp(s) {
  return s.replace(/[.+?^${}()|[\]\\]/g, '\\$&');
}

function filterFiles(files, pattern) {
  if (!pattern) return files;
  const lp = pattern.toLowerCase();
  if (lp.includes('*')) {
    const re = new RegExp(lp.split('*').map(escapeRegExp).join('.*'));
    return files.filter(f => re.test(f.toLowerCase()));
  }
  return files.filter(f => f.toLowerCase().includes(lp));
}

// ─── Tests ───────────────────────────────────────────────────────────────────

test('parseIngr: parses go-small files.ingr correctly', () => {
  const text = readFileSync(fixturePath, 'utf8');
  const result = parseIngr(text);

  assert.equal(result.recordsetName, 'files');
  assert.ok(result.columns.includes('$ID'), 'columns should include $ID');
  assert.equal(result.records.length, 3, 'go-small has 3 indexed files');
});

test('parseIngr: records have expected file paths', () => {
  const text = readFileSync(fixturePath, 'utf8');
  const { records } = parseIngr(text);
  const ids = records.map(r => r['ID']);

  assert.ok(ids.includes('cmd/app/main.go'), 'should include cmd/app/main.go');
  assert.ok(ids.includes('internal/store/cache.go'), 'should include internal/store/cache.go');
  assert.ok(ids.includes('internal/store/store.go'), 'should include internal/store/store.go');
});

test('parseIngr: records have language field set to go', () => {
  const text = readFileSync(fixturePath, 'utf8');
  const { records } = parseIngr(text);
  for (const r of records) {
    assert.equal(r['language'], 'go', `expected language=go for ${r['ID']}`);
  }
});

test('buildTree: constructs correct directory structure', () => {
  const text = readFileSync(fixturePath, 'utf8');
  const { records } = parseIngr(text);
  const files = records.map(r => String(r['ID']));
  const tree = buildTree(files);

  assert.ok('cmd' in tree.dirs, 'root should have cmd dir');
  assert.ok('internal' in tree.dirs, 'root should have internal dir');
  assert.ok('app' in tree.dirs['cmd'].dirs, 'cmd should have app subdir');
  assert.ok(tree.dirs['cmd'].dirs['app'].files.includes('main.go'));

  assert.ok('store' in tree.dirs['internal'].dirs, 'internal should have store subdir');
  const storeFiles = tree.dirs['internal'].dirs['store'].files;
  assert.ok(storeFiles.includes('cache.go'));
  assert.ok(storeFiles.includes('store.go'));
});

test('buildTree: root has no direct files (all files are nested)', () => {
  const text = readFileSync(fixturePath, 'utf8');
  const { records } = parseIngr(text);
  const files = records.map(r => String(r['ID']));
  const tree = buildTree(files);

  assert.deepEqual(tree.files, [], 'root should have no direct files');
});

test('filterFiles: substring filter works case-insensitively', () => {
  const files = ['cmd/app/main.go', 'internal/store/cache.go', 'internal/store/store.go'];

  assert.deepEqual(filterFiles(files, 'store'), [
    'internal/store/cache.go',
    'internal/store/store.go',
  ]);

  assert.deepEqual(filterFiles(files, 'MAIN'), ['cmd/app/main.go']);
  assert.deepEqual(filterFiles(files, 'xyz'), []);
});

test('filterFiles: glob wildcard (*) works', () => {
  const files = ['cmd/app/main.go', 'internal/store/cache.go', 'internal/store/store.go'];

  assert.deepEqual(filterFiles(files, '*.go'), files);
  assert.deepEqual(filterFiles(files, 'internal/*.go'), [
    'internal/store/cache.go',
    'internal/store/store.go',
  ]);
  assert.deepEqual(filterFiles(files, '*cache*'), ['internal/store/cache.go']);
});

test('filterFiles: empty pattern returns all files', () => {
  const files = ['a.go', 'b.go'];
  assert.deepEqual(filterFiles(files, ''), files);
  assert.deepEqual(filterFiles(files, null), files);
});

test('parseIngr: throws on invalid header', () => {
  assert.throws(() => parseIngr('not a valid header\nsome data'), /Invalid INGR header/);
});

test('parseIngr: handles empty file', () => {
  assert.throws(() => parseIngr(''), /Empty INGR file/);
});

# SPEC: Go Line Coverage in Codegraph

Status: Draft — awaiting approval
Scope: cross-repo (`codegrapher` Go CLI + `codegrapher-dev` Angular UI)

## 1. Objective

Let users see, in the `codegrapher-dev` viewer, which source lines are covered by
tests and which are not — both as a per-line gutter on the source view and as
per-function line-coverage numbers.

Coverage is **ingested** from an existing Go coverage profile
(`go test -coverprofile`); codegrapher does not run tests itself. Coverage data
is persisted next to the graph (tagged by ref + content hash), exported as a new
INGR recordset, and rendered by the UI.

**Target users:** developers browsing a repo in the codegrapher viewer who want to
know what is and isn't tested, at file and function granularity.

### In scope
- Go only.
- **Line** coverage only (Go's statement-level profile mapped to lines).
- File-level covered / uncovered lines.
- Per-function/method: `lines_covered`, `lines_uncovered`, `pct_covered`.
- New `coverage` + `node_coverage` storage, export/import, and UI rendering.

### Out of scope (explicitly)
- Branch coverage (Go has none natively).
- TS/JS/any non-Go language.
- Running tests / orchestrating `go test`.
- Coverage trends/history beyond the latest ingested run per ref.

## 2. Key decisions (locked)

1. **Coverage lives next to the graph INGR data**, tagged by ref/branch/tag and
   file `content_hash`. Rationale: graph is cheap to rescan, coverage is
   expensive to produce — so coverage is persisted, not regenerated.
2. **Per-function attribution:** stored/exported counts are **innermost-only**
   (each line attributed to the innermost enclosing function; parents exclude
   nested closure bodies → non-overlapping atomic counts). The **UI computes
   inclusive rollups** by summing a function's own counts plus all its
   descendants via existing `contains` edges.
3. **Stale handling:** when a file's current `content_hash` ≠ the hash recorded at
   ingest time, coverage is **kept and flagged stale**; the UI greys it out and
   shows a stale indicator.
4. Coverage is volatile and therefore stored in **separate tables** keyed by
   file path / node id — never as columns on the deterministic `nodes` table.

## 3. Commands

New CLI verb in `internal/cli`:

```
codegrapher coverage <profile.out> [--ref <ref>] [--root <path>]
```
- Parses a Go coverage profile (`mode: set|count|atomic` + `file:startLine.col,endLine.col stmts count` blocks).
- Resolves profile file paths to indexed file paths (module-path → repo-relative).
- Computes covered/uncovered line sets per file and innermost per-function counts.
- Writes `coverage` (per-file) and `node_coverage` (per-function) rows, stamped
  with each file's current `content_hash` and the run timestamp.
- Reports a summary (files matched, files skipped/unmatched, overall %).

Existing verbs extended:
- `export` — emits `coverage.ingr.{zst,gz}` and `node_coverage.ingr.{zst,gz}`
  per scope, and adds their counts to `manifest.json` scope entries.
- `import` — loads the two new recordsets when present (absent = no coverage).

## 4. Project structure / data model

### 4.1 Go — storage (`store/`)
Schema migration **v5 → v6** in `store/schema.sql` adding:

```sql
-- Per-file line coverage (latest ingested run for this file+ref)
CREATE TABLE coverage (
    file_path     TEXT NOT NULL,
    content_hash  TEXT NOT NULL,        -- hash at ingest time; mismatch => stale
    mode          TEXT NOT NULL,        -- go profile mode: set|count|atomic
    ranges        TEXT NOT NULL,        -- RLE JSON: [[start,end,"hit"|"miss"], ...]
    lines_covered   INTEGER NOT NULL,
    lines_uncovered INTEGER NOT NULL,
    pct_covered     REAL NOT NULL,
    run_at        INTEGER NOT NULL,     -- unix ms
    PRIMARY KEY (file_path)
);

-- Per-function innermost-attributed line counts
CREATE TABLE node_coverage (
    node_id         TEXT NOT NULL,
    content_hash    TEXT NOT NULL,
    lines_covered   INTEGER NOT NULL,
    lines_uncovered INTEGER NOT NULL,
    pct_covered     REAL NOT NULL,
    run_at          INTEGER NOT NULL,
    PRIMARY KEY (node_id),
    FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
);
CREATE INDEX idx_node_coverage_hash ON node_coverage(content_hash);
```

New Go types in `model/` (e.g. `model.FileCoverage`, `model.NodeCoverage`) and a
`store/coverage.go` with read/write/delete helpers mirroring `store/nodes.go`.

### 4.2 Go — ingest (`internal/coverage/` new package)
- `profile.go` — parse Go coverprofile via `golang.org/x/tools/cover`
  (`cover.ParseProfiles`); convert each profile block's statement spans to a
  covered/uncovered line set (a line is covered if any covering block has
  count > 0).
- `attribute.go` — map profile blocks → per-file line sets; assign each line to
  the innermost enclosing node (query `nodes` by file ordered by range, pick the
  tightest `[start_line,end_line]` containing the line).
- `rle.go` — encode covered/uncovered lines as run-length ranges.

### 4.3 Go — export (`snapshot/`)
Extend `scoped.go` recordset list + `manifest.json` `Counts` with `coverage` and
`node_coverage`. Volatile `run_at` excluded from determinism the same way
`updated_at`/`indexed_at` already are.

### 4.4 UI (`codegrapher-dev`, `libs/codegrapher/ui/src/lib/`)
- `data/graph.models.ts` — add:
  ```ts
  interface FileCoverage { filePath: string; contentHash: string;
    ranges: [number, number, 'hit' | 'miss'][]; linesCovered: number;
    linesUncovered: number; pctCovered: number; }
  interface NodeCoverage { nodeId: string; contentHash: string;
    linesCovered: number; linesUncovered: number; pctCovered: number; }
  ```
  Add `coverageByFile` / `nodeCoverageById` maps to `RepoGraph`.
- `data/graph-store.service.ts` + data sources — load the two new recordsets
  (optional; gracefully absent).
- `viewer/viewer-file-content.component.*` — add a coverage layer: per-line
  `hit`/`miss`/`stale` class on the gutter (`<td class="ln">`) or a thin gutter
  stripe. Stale = `coverageByFile.contentHash !== file.contentHash`.
- `viewer/viewer-symbol-list` + `viewer-file-structure` — per-function badge
  showing **inclusive** % (own `NodeCoverage` + descendants via `contains` edges)
  and covered/uncovered counts. Show "—" when no coverage; greyed when stale.

## 5. Code style
- Match existing repo conventions in each codebase (Go: existing `store/` and
  `snapshot/` patterns; UI: existing Angular standalone-component + signal style).
- No new heavy deps. Go profile parsing uses `golang.org/x/tools/cover`
  (`cover.ParseProfiles`) — the canonical Go profile parser.
- Coverage is additive — touch `nodes`/`edges` schema only via the v6 migration,
  never rewrite existing tables.

## 6. Testing strategy
- **Go unit:** profile parser (set/count/atomic modes, multi-block lines),
  RLE encode/decode round-trip, innermost-attribution against fixture nodes
  (including nested closures → parent excludes child lines).
- **Go integration:** ingest a fixture profile into a temp DB, assert
  `coverage`/`node_coverage` rows and percentages; export → import round-trip
  parity (extend `cmd/codegrapher/parity_test.go` patterns).
- **Stale path:** ingest, mutate file content/hash, assert rows flagged stale not
  dropped.
- **UI:** unit-test inclusive rollup (own + descendants) and stale detection;
  component test that gutter classes render for hit/miss/stale.
- Verification gate: `go test ./...` green in `codegrapher`; UI lib tests green.

## 7. Boundaries
**Always:** keep coverage in separate tables; stamp `content_hash` + `run_at`;
make recordsets optional so existing graphs without coverage still load.

**Ask first:** any change to `nodes`/`edges` schema; adding a runtime dependency;
changing the INGR manifest shape beyond additive counts.

**Never:** run `go test` from codegrapher; store branch coverage; block graph
load when coverage is missing or stale; double-count nested functions in
stored/exported numbers.

## 8. Effort
~3–4 days for the Go core (schema, ingest, per-function rollup, export/import,
tests) + ~1–1.5 days UI (gutter + per-function badges). Branch coverage and
TS/JS deferred.

# Per-language / per-version graph storage — design

**Date:** 2026-06-13
**Status:** Approved
**Repos:** `codegrapher` (Go core), `server` (Go serving), `codegrapher-dev` (Angular viewer)

## Goal

Support multi-language repositories by storing graph data **per language + toolchain
version** instead of one flat graph. The viewer requests each scope's graph data
**lazily**, showing a progress bar while a not-yet-loaded scope downloads.

This is a **greenfield** change: no backward compatibility, no migration of existing
single-DB indexes, old flat routes are removed.

## Decisions (locked)

| Decision | Choice |
|---|---|
| What "version" means | Detected **toolchain version** (Go via `go.mod`, TS/JS via `package.json`) |
| Language scope | **Existing extractors only** — Go, TS/JS/TSX/JSX, YAML. No new Python extractor. |
| Storage model | **Multiple SQLite DBs**, one per `(language, version)` |
| Operation scope | Every live operation is scoped to a single `(language, version)` — **no cross-DB JOINs** |
| Cross-language edges | Stored in the **source node's** DB; target id is an unresolved (harmless) ref |
| Version-string format | **Raw detected value** (`1.22`, `5.4`), path/segment-sanitized; `v0` fallback |
| Live query default | **Fan out across all scopes and merge**; CLI `--scope` CSV narrows |
| Discovery | A **manifest**, exposed both embedded in `/status` and at a dedicated endpoint |
| Progress UI | **Real %** when `Content-Length` is known, **indeterminate** otherwise |

## Scope vocabulary

A **scope** is a `(language, version)` pair. `language` is an existing `model.Language`
value (`go`, `typescript`, `javascript`, `tsx`, `jsx`, `yaml`). `version` is the detected
toolchain version string or `v0`. The scope key used in DB filenames, manifest entries,
URLs, and CLI `--scope` is `"{language}-{version}"`, e.g. `go-1.22`, `typescript-5.4`,
`yaml-v0`.

## Component 1 — Toolchain-version detection (codegrapher, index time)

While indexing, each file is mapped to a scope:

- **Go** — `go` directive of the **nearest ancestor `go.mod`** → e.g. `1.22`.
- **TS/JS/TSX/JSX** — **nearest `package.json`**: `typescript` from deps/devDeps if present,
  else `engines.node`, else `v0`.
- **YAML / any file with no governing manifest** — `v0`.

"Nearest ancestor" = walk up the directory tree from the file until the manifest is found,
stopping at the project root. The detected version string is sanitized to
`[A-Za-z0-9._-]`; anything else collapses to `v0`.

Detection is pure and unit-testable: input = file path + project file tree, output = scope.

## Component 2 — Multiple-DB storage + registry (codegrapher)

- DB path: `.codegraph/codegraph-{lang}-{version}.db` (e.g. `codegraph-go-1.22.db`).
- A new **scope registry** sits above `store.Store`:
  - resolves/creates the `*Store` for a scope on demand,
  - enumerates existing scopes (by globbing DB filenames),
  - closes all stores.
- The single-DB open sites are the change surface: `indexer/dir.go` (`DatabasePath`),
  `indexer/indexer.go` (`store.Open`), `indexer/init.go` (`store.Initialize`),
  `internal/cli/{export,import}.go`. These move from "the DB" to "the DB for scope S",
  routed through the registry.
- The watcher routes each changed file's nodes/edges/files to its scope's DB; a DB is
  created lazily on first write for a scope.
- Cross-language edges remain in the source node's DB. Their target id won't resolve in
  that DB — acceptable because operations never JOIN across scopes.

## Component 3 — Live queries (MCP / CLI)

- **Default:** fan out across all scopes, run the existing single-DB query against each,
  concatenate/merge results in Go. No JOIN crosses a DB boundary.
- **CLI `--scope` CSV** narrows to specific scopes, e.g. `--scope go-1.22,typescript-5.4`.
- MCP tools keep current behavior (whole-repo = all scopes) via the same fan-out.

## Component 4 — Export + manifest (codegrapher)

- Each scope DB exports to `codegraph/{lang}/{version}/{name}/{name}.ingr`
  (`name` ∈ {`files`, `nodes`, `edges`, `project_metadata`}), preserving the existing
  nested-`name` directory layout.
- `codegraph/manifest.json` is written at export time:

  ```json
  {
    "scopes": [
      {
        "language": "go",
        "version": "1.22",
        "key": "go-1.22",
        "counts": { "nodes": 1234, "files": 90, "edges": 4567 },
        "indexed_at": "2026-06-13T10:00:00Z"
      }
    ]
  }
  ```

  `counts` feed tree labels; `counts`/file sizes inform progress sizing.

## Component 5 — Server

- **New** `GET /graph/{git_host}/{org}/{repo}/{lang}/{v}/{name}.ingr`
  → `{baseDir}/codegraphs/{repoID}/{lang}/{v}/{name}.ingr`.
  Validates `lang` and `v` (`[A-Za-z0-9._-]`), reuses the `validIngrNames` whitelist.
- **New** `GET /graph/{git_host}/{org}/{repo}/manifest.json` → serves the stored manifest.
- **Extended** `GET /status/{git_host}/{org}/{repo}` → response embeds the manifest
  (`manifest` field) alongside existing index status.
- The old flat `GET /graph/.../{name}.ingr` route is **removed**.

## Component 6 — Viewer (codegrapher-dev)

- Selection state gains `(language, version)`. `RepoRef`-derived cache key and
  `GraphStoreService.loadGraph` are scoped by it.
- Both data sources gain the `{lang}/{v}` path segment:
  - DB server: `…/graph/{forge}/{org}/{repo}/{lang}/{v}/{name}.ingr`
  - GitHub raw: `…/codegraph/{lang}/{v}/{name}/{name}.ingr`; manifest at
    `…/codegraph/manifest.json`.
- **Flow:** fetch manifest first → build **tree roots per scope** (label e.g. `Go (1.22)`).
  Selecting a not-yet-loaded scope triggers a lazy fetch.
- **Progress:** read the response body as a stream; when all 4 recordsets report
  `Content-Length`, show a **real aggregate %**; otherwise show an **indeterminate** bar.
- Scope graphs are cached independently in IndexedDB (cache key includes `lang`+`version`),
  stale-while-revalidate as today.

## Component 7 — Testing / refactor (toward 100% coverage)

- Main refactor: extract the **scope registry** so the single-DB assumption is replaced in
  one localized place rather than scattered.
- New/updated tests:
  - version detection: nearest-manifest resolution, sanitization, `v0` fallback (Go + TS/JS);
  - export partitioning + cross-language edge assignment;
  - registry routing / lazy creation / enumeration;
  - manifest generation (counts, scopes);
  - server: scoped routing, `lang`/`v` validation, manifest endpoint, `/status` embed;
  - CLI `--scope` CSV parsing + fan-out/merge;
  - viewer: manifest parse, scoped cache key, lazy load, progress reader
    (`Content-Length` present and absent), tree roots per scope.

## Out of scope

- New language extractors (e.g. Python).
- Cross-language traversal / JOINs.
- Migrating existing single-DB indexes (greenfield).

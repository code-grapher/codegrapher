# ADR-002: Git-persisted index snapshots in INGR format

Status: ACCEPTED (owner decision, 2026-06-11) — post-parity feature, not yet built
Owner rationale: CI jobs and teammates should reuse an index from the repo
instead of re-indexing from scratch.

## Decision

- The **runtime store stays SQLite** (`.codegraph/codegraph.db`). It is the
  drop-in parity contract with the original codegraph (identical schema,
  FTS5 search) and is not the performance bottleneck. No graph DB.
- Add **`codegraph export` / `codegraph import`**: a snapshot of the index in
  **INGR** (https://ingr.io — compact, deterministic, git-friendly record
  format; one JSON-encoded field per line, self-describing header,
  record-count/digest footer; v1.0.0-RC).
- Workflow: snapshot is committed to the repo (default dir: `ingitdb/codegrapher/`).
  CI/teammates run `import` to seed the store, then the existing
  content-hash `sync` reconciles any drift — turning cold-start indexing
  into "import + sync seconds".

## Snapshot design constraints

- One `.ingr` file per table (`nodes`, `edges`, `files`, `unresolved_refs`,
  `project_metadata`), records sorted by primary key for byte-determinism.
- Volatile fields (`updated_at`, `indexed_at`, db size, absolute paths) are
  EXCLUDED or normalized — two exports of the same code tree must be
  byte-identical regardless of when/where they ran.
- Expect churn proportional to code change: node IDs hash line numbers, so
  edits rewrite the affected file's records. INGR keeps those diffs
  line-surgical; the volume is inherent to the data model and acceptable
  for the seed+sync use case.
- Implementation: use the official Go library
  **github.com/ingr-io/ingr-go** (MIT, pure Go, zero dependencies —
  CGO_ENABLED=0 preserved). No in-house writer/parser.

## Extension: in-browser code browsing (owner idea, 2026-06-11)

A static, serverless web viewer that loads the repo's committed `.ingr`
snapshot files directly in the browser (fetched from the repo checkout, raw
GitHub, or any static host) and offers quick symbol search/browse with
client-side filtering:

- Parsing via the official **github.com/ingr-io/ingr-js** library; a nodes table at
  specscore-cli scale (~8K records) filters instantly client-side; even
  ~100K-node repos are a few MB gzipped (highly repetitive lines).
- The edges table enables callers/callees navigation in the viewer.
- Scope guard: this is a browse/filter UX (substring/prefix, maybe tiny
  fuzzy) — it does NOT promise parity with the CLI's FTS5+BM25 `query`
  scoring.
- DECIDED (owner, 2026-06-11) — this is the END GOAL of the current effort,
  first target repo: specscore-cli (Go focus):
  - Viewer lives at codegrapher.dev with D-0001-style canonical routes:
    `https://codegrapher.dev/{git_host}/{org}/{repo}[/{path}][?q=pattern]`
    (first-segment dispatch: contains "." → forge route; literal → site
    page; forge-host allow-list, github.com first). See
    specscore-studio-app/spec/features/studio-url-scheme for the pattern.
  - Directory/file tree derives from the committed files.ingr (split paths
    client-side) — NO separate go-tree.yaml (single source of truth), NO
    GitHub API for the tree. nodes.ingr extends the tree into symbols and
    powers symbol search. Scope: indexed files only (it's a code browser).
  - File CONTENT fetched on demand from raw.githubusercontent.com (public
    repos; API/token only if private repos arrive later).
  - Entry point: a link in the target repo's root README →
    codegrapher.dev/github.com/{org}/{repo}.

## Alternatives considered

- **Graph DB** (Kùzu/Neo4j/...): rejected — workload is 1–3 hop traversals
  over indexed columns (sub-ms in SQLite); embedded graph engines are cgo
  or immature; would lose FTS5 and break index-format compatibility.
- **Committing the SQLite file** (or via LFS/CI cache): works but binary —
  no meaningful diffs/merges; INGR gives reviewable, mergeable snapshots.
- **ingitdb**: same family of motivation; owner selected INGR
  (deterministic fixed-line records) as the concrete format.

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
- Workflow: snapshot is committed to the repo (e.g. `.codegraph-snapshot/`).
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
- Implementation: small in-house pure-Go INGR writer/reader against the
  published spec (the format is deliberately trivial); no new heavy deps,
  CGO_ENABLED=0 preserved.

## Extension: in-browser code browsing (owner idea, 2026-06-11)

A static, serverless web viewer that loads the repo's committed `.ingr`
snapshot files directly in the browser (fetched from the repo checkout, raw
GitHub, or any static host) and offers quick symbol search/browse with
client-side filtering:

- INGR's fixed-line format parses in a few lines of JS; a nodes table at
  specscore-cli scale (~8K records) filters instantly client-side; even
  ~100K-node repos are a few MB gzipped (highly repetitive lines).
- The edges table enables callers/callees navigation in the viewer.
- Scope guard: this is a browse/filter UX (substring/prefix, maybe tiny
  fuzzy) — it does NOT promise parity with the CLI's FTS5+BM25 `query`
  scoring.
- Open: snapshot directory name/layout (shared concern with the ingitdb
  ecosystem direction), where the viewer lives (codegrapher.dev tool page
  vs. a static index.html shipped next to the snapshot).

## Alternatives considered

- **Graph DB** (Kùzu/Neo4j/...): rejected — workload is 1–3 hop traversals
  over indexed columns (sub-ms in SQLite); embedded graph engines are cgo
  or immature; would lose FTS5 and break index-format compatibility.
- **Committing the SQLite file** (or via LFS/CI cache): works but binary —
  no meaningful diffs/merges; INGR gives reviewable, mergeable snapshots.
- **ingitdb**: same family of motivation; owner selected INGR
  (deterministic fixed-line records) as the concrete format.

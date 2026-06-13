# Design: index `package.json` (dedicated `node` scope + client merge)

**Date:** 2026-06-13
**Status:** Approved (pending spec review)

## Context

The `go.mod` indexing feature (see `2026-06-13-gomod-indexing-design.md`)
established a pattern for indexing a dependency manifest as graph content. This
design applies the same pattern to `package.json`, the npm/node manifest, as the
first step of unparking TypeScript/JavaScript support.

`package.json` is JSON, not source — so neither tree-sitter nor a TypeScript
parser is involved; it is parsed with `encoding/json`, exactly as `go.mod` is
parsed with `x/mod/modfile`. It is read today by two hand-rolled call sites
(`scope.detectNodeVersion`, `mcp/queryutils`) and is otherwise not indexed.

Owner decisions feeding this design:
- **TypeScript unpark uses the existing tree-sitter path** (`gotreesitter`,
  pure-Go, CGO-free); `microsoft/typescript-go` adoption remains a separate
  future bet, not part of this work.
- **`package.json` first**, then TS *source* unpark as a later pass.
- Goldens and backward-compat are NOT constraints (carried over from the go.mod
  mandate); goldens are regenerated freely via the re-baseline script.

## Goals

- Parse `package.json` through one robust `encoding/json`-based package, shared
  by the existing readers.
- Index `package.json` as first-class, traversable graph content: a main module
  node + one node per dependency, across all four dependency categories.
- Store it in a **dedicated `node-vN` scope**, and have the query client
  **auto-merge** that scope into the JS/TS-family scopes so scoped queries see
  node dependencies.

## Non-goals

- TypeScript *source* parsing changes (separate later pass).
- npm `overrides`/`resolutions` (no `replace`/`exclude` analog is modeled —
  YAGNI; npm has no first-class equivalent worth a schema addition).
- Per-dependency line numbers (`encoding/json` does not expose them; see Part C).
- Linking source-level JS/TS `import` references to the `package.json` dependency
  that satisfies them (possible future work; node-per-dependency leaves it open).

---

## Part A — shared `pkgjson` parser

New package `pkgjson` wrapping `encoding/json`:

```go
type File struct {
    Name                 string
    Version              string
    Engines              map[string]string
    Dependencies         map[string]string
    DevDependencies      map[string]string
    PeerDependencies     map[string]string
    OptionalDependencies map[string]string
}

func Parse(data []byte) (*File, error)
```

Rewire the two existing readers to it, **preserving behavior**:
- `scope.detectNodeVersion` → continues to return the `typescript` devDep/dep
  version, else `engines.node`, for version **bucketing** (unchanged behavior).
- `mcp/queryutils` package-name token read → `f.Name`.

`encoding/json` is already robust; the win is consolidating two ad-hoc structs
into one typed parser (DRY), mirroring the gomod swap.

---

## Part B — schema additions (`model`)

- **Detection language:** `LangPackageJSON = "package.json"` — extractor
  dispatch and node provenance (`Node.Language`).
- **Scope-partition language:** `LangNode = "node"` — used ONLY as a storage
  partition (scope key `node-vN`); never emitted as any file's detection
  language. (Analogous to how `go.mod` mapped its detection language to a scope
  language, but here the target partition is a new, dedicated one.)
- **Edge kinds:** none new — reuse `EdgeRequires`. Dependency category lives in
  edge metadata: `{version, category}`, `category` ∈ {`prod`, `dev`, `peer`,
  `optional`}.
- **Node kinds:** none new — reuse `KindModule`.

---

## Part C — extractor (`extractPackageJSON`)

For one `package.json`:
- `KindFile` row (`FileNodeID("package.json")`, `Language=LangPackageJSON`);
  `EdgeContains` → main module node.
- **Main module node** → `KindModule`, name = package `name` (or the file's
  directory name when `name` is absent — npm allows nameless private packages),
  Signature encodes `version` + `engines`, e.g. `"v1.2.3; engines: node>=18"`
  (omit absent parts; empty Signature when neither present).
- **Each dependency** (across `dependencies`, `devDependencies`,
  `peerDependencies`, `optionalDependencies`) → its own `KindModule` node,
  deduped by package name, version in Signature. `EdgeRequires` (main→dep) with
  metadata `{version, category}`.
  - If the same package appears in multiple categories, the dep node is created
    once; one `EdgeRequires` edge is emitted per (name, category) occurrence so
    the category set is preserved.

### Node IDs (approved: all at line 1, no line-finding pass)
`encoding/json` does not expose per-key line numbers, and line numbers are not
load-bearing for `package.json`. Dep node IDs use
`GenerateNodeID(filePath, KindModule, name, 1)` — unique per dependency name
because dedup is by name. The main module node also uses line 1. No
line-discovery pass is added.

### Determinism
`encoding/json` decodes maps in unspecified order, so the extractor MUST sort
dependency names within each category before emitting nodes/edges, so node and
edge output order (and thus golden output) is deterministic across runs.

---

## Part D — storage: dedicated `node-vN` scope

- Pipeline: `DetectLanguage` matches basename `package.json` → `LangPackageJSON`
  (before the extension switch). `indexer/scan.go` then treats it as indexable.
- `ExtractFile` dispatches `LangPackageJSON` → `extractPackageJSON` (uses the
  Part A parser).
- **Scope:** `scope.DetectVersion` handles `LangPackageJSON` via
  `detectNodeVersion` (a `package.json` reading its own version/typescript dep).
  The `scopeLanguage` helper (added for go.mod) is extended to map
  `LangPackageJSON` → `LangNode`, so storage lands in `node-vN`
  (`codegraph-node-v1.db`). Node provenance (`Node.Language`) stays
  `LangPackageJSON`.

The version `N` is whatever `detectNodeVersion` yields, which is the SAME value
a sibling `.ts`/`.js` file in the project gets — so `node-vN` shares its major
version with the project's `typescript-vN`/`javascript-vN` scopes. This is what
makes the version-matched client merge (Part E) well-defined.

---

## Part E — client merge into the JS/TS family

The query side already fans out across ALL scope stores by default and merges,
so an unfiltered query includes `node-vN` for free. The merge behavior is only
needed for **scoped** queries.

**Rule:** when a `--scope` request includes a key `{lang}-vN` for `lang` in
{`typescript`, `javascript`, `tsx`, `jsx`}, the scope-filter expansion ALSO
includes `node-vN` (version-matched on the same `vN`) when that store exists.

- Implemented ONCE, in the scope-key expansion that feeds `StoresFiltered`
  (every CLI verb and the MCP layer route their `--scope`/scope selection
  through this single path). No per-verb changes.
- Explicit `--scope node-v1` still selects the node store alone.
- Default (no `--scope`) is unchanged (already all-stores).
- Expansion is additive and idempotent: requesting `typescript-v5,node-v5` is
  the same as requesting `typescript-v5`.

This is the "store server-side in a dedicated scope, merge client-side into
TS/JS/tsx/jsx" behavior: storage stays cleanly separated; association is a
query-time concern.

---

## Error handling

- Malformed `package.json` → `encoding/json` error recorded as a `warning`
  `ExtractionError` (code `packagejson_parse_error`); the file row is stored with
  zero symbol nodes (mirrors the go.mod parse-error path). No crash.
- Absent `package.json` → no-op.
- `node_modules/` is already excluded by the scanner's default ignores, so nested
  dependency `package.json` files are not indexed (verify during implementation;
  if not excluded, that is a separate concern to flag, not silently index
  thousands of them).

## Testing & goldens

- Unit: `pkgjson` parser table tests — all four dependency categories, `engines`,
  nameless package, malformed JSON.
- Unit: `extractPackageJSON` node/edge expectations (dedup across categories,
  category metadata, deterministic ordering).
- Scope/merge: a test asserting a `package.json` lands in `node-vN`, AND that a
  scoped query for `typescript-vN` auto-includes the `node-vN` store (the Part E
  merge); plus `--scope node-vN` alone works.
- Part A regression: `scope.detectNodeVersion` and `mcp/queryutils` return
  identical values on existing fixtures.
- Goldens: add a fixture containing a `package.json` (new `node-small` or extend
  `ts-small`) and re-baseline via `tools/parity/rebaseline-golden.sh` — never
  hand-edit `testdata/golden/**`. The rebaseline script may need a
  `node-small`/extended-fixture entry; that is a tooling edit, not a golden edit.

## Gates

`gofmt`, `go vet ./...`, `CGO_ENABLED=0 go build ./...`,
`CGO_ENABLED=0 go test -count=1 ./...` — all green, fixture goldens and the
binary parity test included.

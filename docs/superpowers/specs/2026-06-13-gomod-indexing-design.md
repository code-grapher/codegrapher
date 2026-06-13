# Design: index `go.mod` + robust shared parser

**Date:** 2026-06-13
**Status:** Approved (pending spec review)

## Problem

`go.mod` is not indexed as graph content. `DetectLanguage` maps only `.go`,
the TS/JS family, and `.yml/.yaml`; a `go.mod` falls through to `LangUnknown`,
so the file walker (`indexer/scan.go`, gated on `DetectLanguage != LangUnknown`)
skips it — no file row, no nodes, no edges.

`go.mod` is read today, but only for two scalar fields, by three separate
hand-rolled parsers:

| Site | Field | How |
|------|-------|-----|
| `resolve/resolve.go` `loadGoModulePath` | `module` path | `bufio.Scanner`, `HasPrefix("module ")` |
| `scope/scope.go` `detectGoVersion` | `go` directive | regex `^\s*go\s+(\d+\.\d+…)` |
| `mcp/queryutils.go` (~L324) | last segment of `module` path | regex `^\s*module\s+(\S+)` |

`golang.org/x/mod v0.33.0` is already present in `go.sum` transitively, but is
not a direct import.

## Goals

- **A.** Replace the three hand-rolled `go.mod` readers with one robust parser
  built on `golang.org/x/mod/modfile`.
- **B.** Index `go.mod` as first-class, traversable graph content with full
  directive fidelity (module, go, toolchain, require, replace, exclude,
  retract).

## Non-goals

- Changing scope/version **bucketing** semantics. Bucketing stays on the `go`
  directive, not `toolchain` (switching would re-key every scope DB and break
  goldens).
- Linking source-level Go `import` references to the `require` that satisfies
  them. The node-per-dependency model leaves this open as future work, but it
  is out of scope here.

---

## Part A — Shared `gomod` parser

New package `gomod` wrapping `golang.org/x/mod/modfile` (promoted from
transitive to **direct** dependency). One function parses a `go.mod` byte slice
into a structured value:

```go
type File struct {
    Module    string     // module path
    Go        string     // "1.26.4"
    Toolchain string     // "go1.26.4" or ""
    Requires  []Require  // {Path, Version, Indirect, Line}
    Replaces  []Replace  // {OldPath, OldVersion, NewPath, NewVersion, Line}
    Excludes  []Exclude  // {Path, Version, Line}
    Retracts  []Retract  // {Low, High, Rationale, Line}
}
```

Three call sites rewire to it, **preserving current behavior**:

- `resolve.loadGoModulePath` → `f.Module`
- `scope.detectGoVersion` → `f.Go` (keeps bucketing on the `go` directive, not
  `toolchain`)
- `mcp/queryutils` module-token read → `f.Module`

The existing walk-up-to-nearest-`go.mod` logic in `scope`/`resolve` is
unchanged; only the parse step swaps. `modfile.Parse` on a malformed file
returns an error — callers in A treat that as "no value" (empty string),
matching today's silent-failure behavior.

---

## Part B — Index `go.mod` as graph content (node per dependency)

### Schema additions (`model`)

- **Language:** `LangGoMod = "go.mod"` — used for extractor dispatch and node
  provenance (`Node.Language`).
- **Edge kinds (new):** `EdgeRequires`, `EdgeReplaces`, `EdgeExcludes`. Added to
  the `EdgeKinds` slice and any edge-kind validators / explore-traversal
  allowlists.
- **Node kinds:** none new — reuse the existing-but-unused `KindModule`.

### Nodes & edges produced from one `go.mod`

- `KindFile` row (`FileNodeID("go.mod")`, `Language=LangGoMod`); `EdgeContains`
  → main module node.
- **Main module node** → `KindModule`, name = module path, line = `module`
  directive line. The `go`, `toolchain`, and `retract` directives are encoded
  on this node's `Signature` string (see below).
- **Each require / replace-target / exclude** → its own `KindModule` node, name
  = dependency module path, version in node `Signature`, line = directive line.
  ID via the existing `GenerateNodeID(filePath, KindModule, name, line)`
  formula — deterministic and collision-free (module paths are unique; lines
  disambiguate regardless).
- Edges from the main module node:
  - `EdgeRequires` → dep node, metadata `{version, indirect}`.
  - `EdgeReplaces` → source = the replaced dep node, target = the replacement
    node; metadata flags local-path replacements (e.g. `./fork`).
  - `EdgeExcludes` → excluded module node, metadata `{version}`.

### `go` / `toolchain` / `retract` encoding (decided: Signature-encoding)

`model.Node` has no generic metadata map (only `model.Edge` does). Rather than
add a `Metadata` field to `Node` (which would widen the node JSON shape, the
SQLite nodes schema, and the golden surface), these directives are encoded on
the main module node's `Signature` string, e.g.:

```
go 1.26.4; toolchain go1.26.4; retract [v1.0.0, v1.0.1]
```

This keeps the schema/store/golden blast radius limited to the new nodes and
edges.

### Pipeline wiring

1. `extract.DetectLanguage` — match **basename** `go.mod` → `LangGoMod` (it is a
   filename, not an extension; handled before the `filepath.Ext` switch).
2. `indexer/scan.go` — `go.mod` becomes indexable automatically once
   `DetectLanguage` no longer returns `LangUnknown` for it.
3. `extract.ExtractFile` — dispatch `LangGoMod` → new `extractGoMod`, which uses
   the Part A parser to build nodes/edges.
4. **Scope (confirmed):** `go.mod` folds into the **`go-vN`** partition — the
   same per-scope store as the Go source it governs — via a `LangGoMod→LangGo`
   mapping at store-selection time plus a `LangGoMod` case in
   `scope.DetectVersion` (which reads its own `go` directive). Rationale:
   co-locates the module/dependency nodes with the source in one store so they
   are traversable together and a future import↔require link stays within a
   single store. Node provenance (`Node.Language`) remains `LangGoMod`; only the
   storage partition is `go-vN`.

### Error handling

- Malformed `go.mod` → `modfile.Parse` error recorded as a `warning`
  `ExtractionError`; the file row is stored with zero nodes (mirrors the
  existing parse-error path). No crash.
- Absent `go.mod` → no-op (file simply isn't in the scan set).

---

## Testing & goldens

- **Unit:** `gomod` parser table tests — require blocks, `// indirect`, local +
  remote `replace`, `exclude`, `retract` (single + range), `toolchain`.
- **Unit:** `extractGoMod` node/edge expectations for a representative `go.mod`.
- **A-regression:** `resolve`/`scope`/`queryutils` return identical module path
  and version on existing fixtures (behavior parity for Part A).
- **Goldens (will change):** any fixture repo containing a `go.mod` now emits
  extra nodes/edges. The 46-golden binary parity set and fixture goldens must be
  **re-baselined via the re-baseline scripts** — never hand-edited, per project
  standing rules. A dedicated `go.mod` fixture is added.

## Gates

`gofmt`, `go vet ./...`, `CGO_ENABLED=0 go build ./...`,
`CGO_ENABLED=0 go test -count=1 ./...` — all green, fixture goldens and the
46-golden binary parity test included.

# KNOWN-BUGS

codegrapher is a behavior-parity port of [codegraph](https://github.com/colbymchenry/codegraph)
(MIT). Parity is **bug-for-bug**: where the original misbehaves, we deliberately
reproduce it, because the golden outputs captured from the original are the spec
and downstream consumers may depend on the observed behavior.

This file tracks three categories:

- **A. Upstream bugs we reproduce on purpose.** Each has a `TODO(upstream-bug N)`
  marker at the code site. Fix these only as a deliberate policy change
  (diverging from upstream), together with a golden re-baseline.
- **B. Port & harness bugs found during development** — fixed; recorded so the
  failure modes aren't re-introduced.
- **C. Known gaps** — functionality not yet at parity.

---

## A. Upstream bugs deliberately reproduced

### UB-1: Go type declarations lose their doc comments

Doc comments on Go `struct` / `interface` / `type` alias declarations are never
extracted (functions and methods are fine).

- **Upstream cause:** `getPrecedingDocstring(type_spec)` uses
  `previousNamedSibling`, but a `type_spec` has no named siblings inside its
  `type_declaration` wrapper — the comment is a sibling of the wrapper at
  `source_file` level (`src/extraction/tree-sitter.ts`).
- **Our site:** `internal/extract/walk_go.go` → `extractGoStruct` /
  `extractGoInterface` / `extractGoTypeAlias` (docstring hard-coded empty).
- **Symptom:** `query --json` shows no `docstring` for Go types.

### UB-2: Exported TS/JS declarations lose their doc comments

Doc comments on any TS/JS declaration wrapped in `export` (i.e. most public
API) are never extracted; unexported declarations keep theirs.

- **Upstream cause:** same `previousNamedSibling` lookup — the declaration node
  is a child of `export_statement`, so the preceding comment is a sibling of
  the `export_statement`, not of the declaration
  (`src/extraction/tree-sitter.ts`).
- **Our site:** `internal/extract/walk_ts.go` — every `if !e.insideExport`
  docstring guard (class/interface/enum/type-alias/function/variable
  extractors).
- **Symptom:** exported TS symbols have no `docstring` in any output.

### UB-3: TS return-type references are emitted twice

Every TS/JS return-type annotation produces **two** identical `references`
rows (same source, target, kind, line, col), inflating `status` edge counts
and duplicating rows in raw edge listings.

- **Upstream cause:** `extractTypeAnnotations` walks the `return_type` field,
  then separately walks the first `type_annotation` named child of the same
  declaration — in the TS grammar these are the *same AST node*
  (`src/extraction/tree-sitter.ts` ~3674 and ~3697).
- **Our site:** `internal/extract/walk_ts.go` → `extractTSTypeAnnotationRefs`
  (the deliberate second walk).
- **Symptom:** e.g. ts-small: `edges` table has 41 rows, 39 unique;
  `status --json` reports `edgeCount: 41`. See the two duplicate rows in
  `testdata/golden/ts-small/resolution-edges.json`.

---

## B. Port & harness bugs found during development (fixed)

| # | Where | Bug | Fixed in |
|---|---|---|---|
| 1 | `internal/paritytest` | `sortArray` indexed a detached key slice while `sort.SliceStable` moved items — canonicalization depended on input order, so two permutations of the same set could compare unequal. Regression test added. | `13d2dcd` |
| 2 | golden capture process | All day-one CLI goldens were captured running the original under **Node 26 + `CODEGRAPH_ALLOW_UNSAFE_NODE=1`**, which degrades upstream's WASM parsing (e.g. ts-small edgeCount 37 instead of the true 41; wrong impact sets). Verified the original is deterministic under Node 22 (3 runs, identical DB hashes) and re-captured everything. **Rule: capture goldens only under Node 22** (`fnm exec --using 22`). | `e1ee40a` |
| 3 | `query` scoring | Three porting bugs vs upstream: path-relevance bonuses don't stack the filename bonus with dir/path; prefix bonus is `Math.round(10+30*ratio)`; final score must sum in upstream's association order `((bm25+kind)+path)+name` to match float results bit-for-bit. | `6bc3676` |
| 4 | `query` traversal | Impact/callers edge-kind filtering had been *fitted to the corrupted goldens* (category B-2) and was exactly inverted: upstream impact follows incoming edges of all kinds **except `contains`** (imports included); callers/callees apply **no** file-kind or provenance filtering. | `6bc3676` |
| 5 | `internal/extract` | TS type-annotation references weren't emitted at all (then later: emitted once instead of upstream's twice — see UB-3). | `64bd571`, `1eabfb4` |
| 6 | `resolve` | Dotted references (`cache.warm`) were looked up verbatim and never matched; upstream resolves the member name with proximity ranking. | `64bd571` |
| 7 | `resolve` | Heuristic synthesis passes (Go struct→interface `implements` conformance; interface-method→implementation `calls`) were silently missing; parity tests had quietly excluded `provenance="heuristic"` edges. Exclusions removed, passes ported, and edge identity in parity tests tightened to include provenance + line. | `03592f3`, `26e6c61` |

Process lesson encoded in the harness: **goldens are immutable**. Every
guideline-violating "fix the golden to match the port" so far has been masking
a real bug (B-3, B-4) — the two legitimate golden changes (B-2 re-capture,
adding new capture dimensions) are full re-captures from the original via
`tools/parity/capture-*.sh`, never hand edits.

---

## C. Known gaps (not yet at parity)

| Gap | State |
|---|---|
| MCP daemon/proxy modes (`daemon.sock`, `daemon.pid`, ppid watchdog) | Not implemented; direct (stdio) mode only. Pending decision at integration. |
| MCP `codegraph_explore` relevance selection | Simplified (FTS + 1-hop expansion) vs upstream's full `findRelevantContext` pipeline; output *format* matches. Pending real `query`-package wiring. |
| Windows `lock` alive-probe | Stale-lock detection on Windows uses the 2-minute mtime timeout only (no PID liveness probe) to stay pure-Go. Conservative: locks are never reclaimed early. |

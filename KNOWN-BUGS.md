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
| 8 | `internal/extract` | Grouped `var (...)` declarations produced zero nodes: `extractGoVarConst` iterated direct children looking for `var_spec`, but gotreesitter wraps grouped var blocks in an intermediate `var_spec_list` node. Fixed by unwrapping `var_spec_list` before collecting specs. | current |
| 9 | `internal/extract` | 30 Go files containing `[]struct{...}` table-driven test patterns caused gotreesitter to return a root with `Kind()=="ERROR"` instead of `"source_file"`, losing all top-level declarations after the first problematic construct. Fixed by detecting an ERROR/HasError root and supplementing with a `walkGoFallback` pass using the standard library `go/parser` (which parses all valid Go correctly). Documented as D-3. | current |
| 10 | `indexer/scan.go` | `.yml`/`.yaml` files returned `LangUnknown` from `DetectLanguage`, so they were never scanned. Fixed by adding `LangYAML` detection and an `IsFileLevelOnly` guard in `ExtractFile` that returns zero nodes (matching upstream's `isFileLevelOnlyLanguage`). | current |

Process lesson encoded in the harness: **goldens are immutable**. Every
guideline-violating "fix the golden to match the port" so far has been masking
a real bug (B-3, B-4) — the two legitimate golden changes (B-2 re-capture,
adding new capture dimensions) are full re-captures from the original via
`tools/parity/capture-*.sh`, never hand edits.

---

## C. Known gaps (not yet at parity)

| Gap | State |
|---|---|
| MCP daemon/proxy modes (`daemon.sock`, `daemon.pid`, ppid watchdog) | Not implemented; direct (stdio) mode only. `codegraph serve --mcp` serves direct stdio; `CODEGRAPH_DAEMON_INTERNAL` is rejected with a clear error and the daemon-default transport falls back to direct mode with a stderr notice. |
| Windows `lock` alive-probe | Stale-lock detection on Windows uses the 2-minute mtime timeout only (no PID liveness probe) to stay pure-Go. Conservative: locks are never reclaimed early. |

---

## D. Deliberate divergences from upstream

### D-2: gotreesitter parse timeout falls back to go/parser

gotreesitter has a known pathological GLR blow-up (upstream issue #110) on
certain literal-heavy Go files (notably `pkg/lint/coverage_test.go` at 211 KB).
The `SetTimeoutMicros` mechanism doesn't fire because the per-iteration check
runs too infrequently relative to the iteration count for such files.

Divergence (`internal/extract/extract.go`): the gotreesitter `Parse` call runs
in a goroutine with a 30-second hard deadline (configurable via
`CODEGRAPH_PARSE_TIMEOUT_MS`). On timeout the goroutine is abandoned (Go has no
cancellation) and `walkGoFallback` (go/parser) extracts the file. The upstream
uses the C tree-sitter via WASM and never hits this limitation; for us go/parser
produces identical node IDs and line numbers (same `token.FileSet` numbering
matches gotreesitter's row+1 convention). The leaked goroutine eventually
completes on its own and is GC'd; it holds no database handles.

### D-3: go/parser supplemental pass for gotreesitter ERROR roots

Files where gotreesitter produces `root.Kind()=="ERROR"` (the `[]struct{...}`
anonymous struct slice pattern in function bodies) get a supplemental extraction
pass via `walkGoFallback` (`internal/extract/walk_go_fallback.go`). go/parser
correctly parses all valid Go and produces identical node IDs. New nodes only
(by ID) are merged — nodes the partial tree-sitter walk already emitted
correctly are never duplicated.

### D-1: FileLock no longer reclaims young locks with invalid content

Upstream's `FileLock.acquire` treats a lock file whose content is empty or
unparsable as stale **regardless of age** and unlinks it. But every acquirer
necessarily passes through that state: the `O_EXCL` create and the PID write
are separate operations, so the file is momentarily empty. A concurrent
acquirer hitting that window deletes the half-born lock and acquires too —
**two winners**, and two writers can corrupt the SQLite index. Our
`TestRaceAcquire` (20 concurrent acquirers) reproduced this intermittently.

Divergence (`lock/lock.go` → `handleExistingLock`): a lock with invalid
content is reclaimed only once it is older than `StaleTimeout` (2 min);
young-but-invalid is treated as held. Locks naming a valid dead PID are still
reclaimed immediately (crashed owner), and the age rule alone still reclaims
anything older than the timeout — both exactly as upstream. Only the
pathological microsecond interleaving changes. A sub-microsecond TOCTOU
between the freshness stat and the unlink remains (closing it fully needs
flock-style OS primitives); it is narrower than upstream's by ~5 orders of
magnitude.

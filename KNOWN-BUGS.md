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
| 0 | `internal/extract/walk_go.go`, `walk_go_fallback.go` | UB-1: Go type declarations (`struct`/`interface`/`type` alias) lost their doc comments. The tree-sitter walk now passes the `type_declaration` node as anchor to `extractGoTypeSpec` and calls `e.lookupDoc(anchor)`; the go/parser walk uses `ast.TypeSpec.Doc` with fallback to `ast.GenDecl.Doc`, matching go/ast CommentMap semantics. Both walks now extract type declaration doc comments correctly. | current |
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

### D-2: go/parser is the primary Go scanner; gotreesitter Go walk is the test oracle

go/parser is used directly for all Go files (ADR-003, completed 2026-06-11).
The gotreesitter-based `walkGo` is retained in `internal/extract/walk_go.go`
as a test oracle; a differential test runs both walks over fixtures and asserts
identical emission. The old D-2 timeout goroutine and D-3 ERROR-root supplemental
pass have been removed from the production path.

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

---

## E. Differences vs codegraph, kept deliberately

Analysis of the full codegrapher corpus shows a net edge difference of −62
non-`contains` edges (orig: 14,985 / port: 14,923). All 223 missing edges and
all 182 extra edges have `line=0`, indicating they are unresolved-reference or
heuristic edges, not structurally extracted ones. They cluster into two patterns.

| ID | Description | Net edge delta | Verdict |
|---|---|---|---|
| E-1 | D-2 fallback missing `imports` edges | −12 | Acceptable: fallback walk omits unresolved import refs; `contains` edges still link the import nodes |
| E-2 | Heuristic resolution attribution differences | ~−50 net | Acceptable: JS vs Go proximity ranking differ in tie-breaking; both are heuristics |

### E-1: D-2 fallback import edges (12 missing)

Files affected: `pkg/lint/coverage_test.go` (211 KB, hits D-2 timeout),
`pkg/feature/coverage_test.go`. The `fallbackImportSpec` function in
`internal/extract/walk_go_fallback.go` creates import nodes (they appear in
`contains`) but does not emit unresolved `imports` reference edges from the file
node, unlike the normal tree-sitter walk's `extractSpec`. The upstream's C
tree-sitter / WASM parses these files without timing out and emits the edge. Our
go/parser fallback correctly identifies the import nodes — the `contains` edges
still connect them — but the file→import `imports` resolution edge is absent.
Because the resolution pass reconnects imports via `contains` regardless, this
has no practical effect on query results.

### E-2: Heuristic resolution attribution differences (net ~50 edges)

Most affected files show equal counts of missing and extra edges (net=0 per
file), meaning the same resolved target is attributed to a different source node
— e.g. orig emits a `calls` edge from `function:TestFoo` while the port emits it
from `file:foo_test.go`, or vice versa. For files parsed by the go/parser
fallback (D-2 / D-3) the proximity algorithm picks a different "nearest
enclosing" node because go/parser and tree-sitter produce different intermediate
AST shapes. A smaller subset of files have only extra (10 files, 15 edges) or
only missing (19 files, ~47 edges) edges, reflecting minor differences in
tie-breaking when multiple candidate functions are equidistant. Both the JS and
Go resolvers are heuristics with no authoritative ground truth; we keep ours.

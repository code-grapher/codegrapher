# TEST-COVERAGE.md

Per-package coverage from `CGO_ENABLED=0 go test -count=1 -cover ./...`
(all 14 packages green, run 2026-06-11).

---

## Coverage table

| Package | Coverage | Test approach |
|---|---:|---|
| `model` | **100.0%** | Unit: ID scheme, node/edge kind constants, hash formula |
| `indexer` | **80.7%** | Golden parity (extraction + resolution DB dumps vs 46 JSON goldens); unit for dir scanning, git hooks, worktree detection, sync, init |
| `internal/paritytest` | **86.0%** | Unit: canonicalization helpers (sort, normalize, compare); regression for the detached-key slice bug (B-1) |
| `lock` | **77.3%** | Unit: acquire/release, stale-PID reclaim, young-invalid-content hold (D-1), race test (20 concurrent acquirers) |
| `watch` | **78.4%** | Unit: debounce, pending-file map, lock-busy reschedule, env-var override |
| `internal/extract` | **73.2%** | Parity (Go + TS fixture extraction vs DB-dump goldens); differential oracle (go/parser vs gotreesitter walk over go-small) |
| `query` | **70.9%** | Unit: all five query verbs over in-memory store; scoring formula; ambiguous-symbol aggregation; Levenshtein fallback |
| `resolve` | **70.7%** | Parity (resolution-edges.json goldens for go-small + ts-small); unit for dotted-ref proximity ranking, heuristic synthesis passes |
| `store` | **71.2%** | Unit: CRUD, FTS5 search, BM25 rescoring, migration runner, content-hash change detection |
| `snapshot` | **68.4%** | Round-trip: export → import → re-export asserts byte-identical output; field normalization for volatile fields |
| `internal/tsparse` | **64.4%** | Unit: parser round-trip over Go and TS fixtures; node kind/position assertions; 4 test functions, 10 sub-tests |
| `mcp` | **57.9%** | Golden replay: byte-for-byte MCP JSON-RPC session replayed against 12 goldens per fixture (go-small + ts-small; 24 total) |
| `internal/cli` | **2.1%** | Unit: JSON output shapes for all verbs (json_output_test.go); CLI wiring not covered |
| `cmd/codegrapher` | **0.0%** | Binary parity: `TestParityGoldens` builds the binary and runs it end-to-end against both fixtures; coverage instrumentation is unavailable for a subprocess binary |

---

## Harness architecture

**Self-baselined goldens** (`tools/parity/rebaseline-golden.sh`): after the UB-1
docstring fix and the ADR-003 go/parser flip, all goldens were regenerated from
our own binary. The original `capture-*.sh` scripts (requiring Node 22 + upstream
CLI) are retained for upstream comparison but are no longer the live spec source.
Goldens are immutable — every "fix the golden to match the port" attempt so far
masked a real bug (see KNOWN-BUGS.md B-3, B-4).

**46-golden binary parity suite** (`cmd/codegrapher/parity_test.go`): builds the
`codegrapher` binary with `CGO_ENABLED=0` then runs `init`, `status --json`,
`files --json`, `query --json`, `callers/callees/impact --json` against both
fixtures (go-small + ts-small), comparing each output against a golden file via
`internal/paritytest` (normalize + sort + exact compare). This is the highest-level
regression guard; it catches any change in CLI output, scoring, or ID format.

**MCP golden replay** (`mcp/parity_test.go`): replays captured JSON-RPC sessions
(the same requests `tools/parity/capture-mcp-golden.sh` sent to the original TS
server) against the Go MCP server in-process, comparing responses against 24
golden files. Covers all 8 MCP tools across both fixtures.

**Differential oracle** (`internal/extract/oracle_test.go`): runs both the
`go/parser` walk (primary, ADR-003) and the gotreesitter Go walk (test oracle)
over every `.go` file in the go-small fixture and asserts identical node IDs,
kinds, names, start lines, contains edges, and unresolved-ref sets. Files where
gotreesitter returns an ERROR root are skipped with a note. Enforces that the
two Go parsers stay in sync as the codebase evolves.

---

## Known coverage gaps

| Package | Gap | Why hard to cover |
|---|---|---|
| `cmd/codegrapher` | 0% line coverage (subprocess binary) | `go test -cover` cannot instrument a binary it spawns; would require `-coverprofile` injection into the binary build |
| `internal/cli` | CLI wiring (flag parsing, Cobra plumbing, Bubble Tea path) | Requires a full Cobra `Execute()` invocation; the existing unit tests only cover `--json` output helpers |
| `mcp` | Daemon/proxy modes (C-gap: not implemented) | Not applicable until daemon is ported |
| `mcp` | Error paths in `codegraph_explore` (e.g. DB read failures mid-stream) | Requires injecting a fault store; the current tests use a real in-memory store |
| `watch` | Linux inotify per-dir registration cap (50 K dirs) | Requires a large fixture or a mock FS; cost outweighs value for v1 |
| `lock` | Windows PID-liveness path (`alive_windows.go`) | Needs a Windows runner; macOS/Linux CI covers the Unix path |
| `snapshot` | Import error paths (corrupt INGR files, version mismatch) | Missing negative-test fixtures |
| `store` | WAL checkpoint + busy_timeout retry loops | Requires concurrent writers under controlled timing |
| `resolve` | Remaining ~20 heuristic resolution patterns (dotted chains > 2 levels, cross-package interface conformance) | Needs richer fixture repos with those patterns |

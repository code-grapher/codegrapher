# CODEGRAPH.md — Inventory of the Original (Pre-Port Analysis)

Analysis of [colbymchenry/codegraph](https://github.com/colbymchenry/codegraph) v0.9.9
(local: `../codegraph`), the TypeScript original being ported to Go as
`github.com/specscore/codegrapher`. First deliverable per AGENT-BRIEF.md.

Date: 2026-06-10 · Analyst: Claude (Fable 5) · Method: cloc, self-indexing with the
installed CLI (`codegraph init` on `../codegraph`), vitest coverage run, module exploration.

---

## 1. Size

| Metric | Value |
|---|---|
| Source | 124 TS files + 1 SQL, **31,232 LOC** code (+12,845 comment, +4,310 blank) |
| Tests | 69 TS files, **15,220 LOC** (64 test files + `evaluation/` + `integration/`) |
| Self-index | **216 files, 3,510 nodes, 10,081 edges**, 8.16 MB DB, indexed in 1.4 s |
| Node kinds (self) | function 943, import 879, method 749, constant 500, file 214, interface 107, class 50, variable 42, type_alias 24, property 2 |
| Runtime deps | commander, web-tree-sitter, tree-sitter-wasms, ignore, picomatch, jsonc-parser, @clack/prompts, + 3 ANSI helpers |

### LOC by module

| Module | LOC (raw) | Port scope |
|---|---:|---|
| `resolution/` | 14,756 | **partial** — core + `frameworks/go.ts` only (20 other framework resolvers skipped) |
| `extraction/` | 11,498 | **partial** — tree-sitter core, wazero loading, `languages/{go,typescript,javascript}.ts`; 16 other languages + vue/svelte/razor/mybatis/dfm/liquid standalone extractors skipped |
| `mcp/` | 6,301 | **full** — all 8 tools, daemon/proxy/direct modes, ppid watchdog |
| `installer/` | 3,263 | **SKIPPED** (user decision 2026-06-10) — agent-config editors, orthogonal to code intelligence |
| root files (`index.ts`, `types.ts`, `utils.ts`, …) | 3,049 | full |
| `db/` | 2,392 | full |
| `bin/` | 1,837 | full minus install/uninstall/upgrade commands |
| `context/` | 1,681 | full (used by explore + context CLI verb) |
| `sync/` | 1,106 | full |
| `graph/` | 1,068 | full |
| `search/` | 625 | full |
| `upgrade/` | 515 | **SKIPPED** — npm self-update logic, meaningless for a static Go binary |
| `ui/` | 296 | replaced — Bubble Tea/Lip Gloss instead of shimmer worker (visual parity not required) |

**Effective port surface: ~18–20K LOC TS → est. ~15–18K LOC Go + ported tests.**

---

## 2. CLI surface (observed v0.9.9 — observed behavior is the spec)

```
init [path]        initialize .codegraph/ + build index   (-v verbose)
uninit [path]      delete .codegraph/
index [path]       full re-index
sync [path]        incremental re-index since last index
status [path]      index stats                            (--json)
query <search>     symbol search                          (-l limit, -k kind, --json)
files              indexed file tree                      (--json)
callers <symbol>   who calls symbol                       (--json)
callees <symbol>   what symbol calls                      (--json)
impact <symbol>    blast radius                           (--json, depth)
affected [files…]  test files affected by changed sources (--json)
context <task>     AI context assembly                    (from bin/, wraps ContextBuilder)
serve              MCP server (stdio; daemon/proxy modes)
unlock [path]      remove stale lock file
install/uninstall  agent-config installers                ← OUT OF SCOPE
upgrade            self-update                            ← OUT OF SCOPE
```

Notes vs AGENT-BRIEF.md: there is **no `explore` CLI verb** — explore is an MCP tool.
`--json` already exists on the query verbs (no deviation needed; parity must match it).

### Exact JSON output shapes (parity contracts)

`query --json` → array of `{node: {…full node…}, score}`:

```json
[{"node": {"id": "method:8a7521b6e44b...", "kind": "method", "name": "extract",
  "qualifiedName": "DfmExtractor::extract", "filePath": "src/extraction/dfm-extractor.ts",
  "language": "typescript", "startLine": 32, "endLine": 53, "startColumn": 2, "endColumn": 3,
  "docstring": "…", "signature": "(): ExtractionResult", "visibility": null,
  "isExported": false, "isAsync": false, "isStatic": false, "isAbstract": false,
  "updatedAt": 1781126486850}, "score": 109.96}]
```

`callers --json` → `{"symbol", "callers": [{"name","kind","filePath","startLine"}…]}`
`callees --json` → `{"symbol", "callees": [same shape]}`
`impact --json`  → `{"symbol", "depth", "nodeCount", "edgeCount", "affected": [same shape]}`
`files --json`   → `[{"path","language","nodeCount","size"}…]`
`status --json`  → `{"initialized","projectPath","fileCount","nodeCount","edgeCount",
                    "dbSizeBytes","backend","journalMode","nodesByKind":{…},
                    "languages":[…],"pendingChanges":{added,modified,removed},
                    "worktreeMismatch"}` (+ CI fields: version, indexPath, lastIndexed)

Text outputs use color/glyph chrome (`CODEGRAPH_ASCII`/`CODEGRAPH_UNICODE` toggles) —
data parity required, visual parity not.

### Key behavioral contracts

- **Node IDs**: `${kind}:${sha256(filePath + ":" + kind + ":" + name + ":" + line).hex[:32]}`;
  file nodes are literal `file:${path}`; route nodes `route:${path}:${line}:${method}:${routePath}`.
- **qualifiedName**: ancestor names (excluding file node) joined with `::`;
  Go methods use `${receiverType}::${name}`.
- **Ambiguous symbols**: bare name → exact indexed lookup returns ALL definitions
  (generated files sorted last); qualified (`A.b`, `a::b`) → FTS + suffix/path filter,
  retry with bare last part. callers/callees aggregate across all matches, dedupe by
  node ID, append a note listing matched definitions when >1. impact merges subgraphs.
- **Search scoring**: FTS5 BM25 → fallback LIKE → fallback bounded Levenshtein (≥3 chars);
  exact-name supplements injected at max score; rescore = bm25 + kindBonus(fn/method +10,
  interface +9, class +8) + pathRelevance(filename +10, dir +5, path +3, test files −15)
  + nameMatchBonus(exact +80, exact-token +60, prefix +10–40, all-terms +15, substring +10).
- **Change detection**: SHA-256 content hash vs `files.content_hash` (mtime stored, not used).
- **Locking**: `.codegraph/codegraph.lock` PID file via O_EXCL; stale if mtime >2 min AND
  pid dead; in-process async Mutex on top.
- **Watcher**: built-in fs.watch (recursive on macOS/Win; per-dir inotify on Linux capped
  at 50K dirs), **debounce 2000 ms default** (`CODEGRAPH_WATCH_DEBOUNCE_MS`) — this is the
  "sync lag contract"; pendingFiles map feeds MCP staleness banners; lock-busy reschedules
  without dropping pending. Go port: fsnotify with same debounce semantics.
- **Worker pool**: parse workers recycled every 250 files (WASM heap), 10 s per-file
  parse timeout. Go port: errgroup pool; recycling may be unnecessary (wazero), document.

---

## 3. MCP server

Tools (exact names): `codegraph_search`, `codegraph_callers`, `codegraph_callees`,
`codegraph_impact`, `codegraph_node`, `codegraph_explore`, `codegraph_status`,
`codegraph_files`. Allowlist via `CODEGRAPH_MCP_TOOLS`.

Architecture: three modes — **direct** (stdio session), **daemon** (detached per-project
process serving Unix socket/named pipe at `.codegraph/daemon.sock`, pid JSON at
`daemon.pid`, idle timeout 300 s, max-idle 30 min, peer sweep 30 s), **proxy**
(stdio↔socket bridge carrying ppid watchdog, version-matched handshake).

`codegraph_explore` is the single most complex function (~1000 lines in
`mcp/tools.ts::handleExplore`): FTS + exact-name supplement → PascalCase type bias →
density/relevance scoring → source slice reads → line-gap clustering → adaptive sibling
skeletonization → formatted output with relationships section + staleness banner.
Output budgets tiered by project file count. **Risk #1: pin this format early.**

---

## 4. Index store

SQLite (original uses `node:sqlite`, WAL). Schema v1 + migrations (current version
tracked in `db/migrations.ts`): tables `schema_versions`, `nodes`, `edges`, `files`,
`unresolved_refs`, `project_metadata`; **FTS5** virtual table `nodes_fts(id, name,
qualified_name, docstring, signature)` with sync triggers; composite edge indexes
(`(source,kind)`, `(target,kind)` — narrow ones deliberately dropped in migration v4).
PRAGMAs: busy_timeout 5000, WAL, synchronous=NORMAL, 64 MB cache, 256 MB mmap.

Go port: `modernc.org/sqlite` (pure Go, FTS5 supported). Schema is ours to own
(no consumer reads it directly) but porting it verbatim minimizes behavior drift.

`.codegraph/` layout: `.gitignore`, `codegraph.db{,-wal,-shm}`, `codegraph.lock`,
`daemon.pid`, `daemon.sock`. No config file — all config via `CODEGRAPH_*` env vars
(~22 of them; see module map). `project_metadata` table stores version/extraction-version.

---

## 5. Test inventory & coverage

64 test files (15.2K LOC) + `evaluation/` (LLM eval harness — out of port scope) +
`integration/`. Suite result on Node v26.1.0, after `npm run build`:
**1347 passed / 1 flaky / 2 skipped of 1350** (flake: a 5 s timeout in
`sync.test.ts` under coverage-instrumentation load; a ppid-watchdog timing test
also flaked once — both pass in normal runs).

Port-relevant clusters (mandatory-minimum port set), by seam:

- **store/db**: sqlite-backend, node-sqlite-backend, db-perf, concurrent-locking, graph
- **extraction**: extraction, foundation, object-literal-methods, strip-comments,
  generated-detection, is-test-file, glyphs(ui)
- **resolution**: resolution, symbol-lookup, closure-collection-synthesizer,
  gin-middleware-chain (Go framework), context-ranking
- **search**: search-query-parser, iterate-nodes-by-kind
- **sync/watch**: sync, git-hooks, ppid-watchdog, installer-targets(skip)
- **mcp**: mcp-initialize, mcp-daemon, mcp-roots, mcp-tool-allowlist, mcp-staleness-banner,
  mcp-debounce-env, mcp-catchup-gate, mcp-ppid-watchdog, mcp-files-path-normalization,
  daemon-attach-log, daemon-client-liveness, explore-blast-radius, explore-output-budget,
  adaptive-explore-sizing, node-file-view, fabric-view(skip), status-json
- **context**: context, context-ranking
- **out of scope**: drupal, expo-modules, react-native-bridge, rn-event-channel,
  swift-objc-bridge(+resolver), frameworks(partial — keep go cases), npm-sdk, npm-shim,
  installer, prepare-release, node-version-check, upgrade-related

### 5.1 Coverage of original

Measured with `vitest --coverage` (v8 provider) over 51 of 64 test files — the 13
excluded files drive `dist/bin/codegraph.js` through subprocesses (their coverage
is invisible to in-process v8, and one of them aborts the coverage reporter), so
true behavioral coverage is somewhat higher than measured:

| Metric | % | Counted |
|---|---:|---|
| Statements | **66.56%** | 21,109 / 31,713 |
| Branches | **80.11%** | 7,097 / 8,859 |
| Functions | **78.24%** | 1,187 / 1,517 |
| Lines | **66.56%** | 21,109 / 31,713 |

Implication for the port: the mandatory-minimum ported suite inherits roughly this
coverage profile; the cover100 run later starts from a strong (not green-field) base.

---

## 6. Scope decisions (user-confirmed 2026-06-10)

1. **CLI**: all data verbs + serve; **skip** install/uninstall/upgrade.
2. **Languages**: Go + TypeScript/JavaScript extraction only; 16 tree-sitter languages
   + 6 standalone extractors skipped.
3. **Frameworks**: `frameworks/go.ts` only; 20 TS/other framework resolvers skipped.
   Angular: upstream has no Angular resolver — Angular projects get core-TS indexing,
   identical to upstream behavior (parity preserved).
4. **Parser (ADR-001)**: **wazero + upstream's exact tree-sitter WASM grammars**
   (pinned to tree-sitter-wasms versions upstream uses) → bit-identical parse trees,
   pure-Go CGO_ENABLED=0 binary, clean specscore-cli embedding. cgo is the documented
   fallback if the benchmark gate (vs original on ../specscore-cli) shows unacceptable
   parse speed.

---

## 7. Effort estimate

| Phase | Scope | Est. |
|---|---|---|
| Foundation | go.mod, types, store (schema+queries+FTS scoring), parity harness | 1 session (this one) |
| Indexer | wazero runtime + grammar loading + tree-sitter core walk + go/ts/js extractors + orchestrator/worker pool | largest seam; tree-sitter core walk is the deepest logic |
| Resolution | 5-phase pipeline (core) + go framework resolver + import/name matching | second largest |
| Query/graph/search/context | traversal, graph queries, search scoring, context builder | moderate |
| CLI | Cobra verbs + Bubble Tea progress | small |
| Sync | orchestrator sync + fsnotify watcher + locks | moderate |
| MCP | 8 tools (explore = hardest single function), daemon/proxy/watchdog | large |
| Reports | MIGRATION.md, TEST-COVERAGE.md per package | small |

Parallelization: indexer ∥ sync after foundation; query after indexer; CLI after query;
MCP last. Sonnet subagents per seam, adversarial parity verifier before each merge.
Benchmark gate (STOP, ask user) once a real repo indexes end-to-end.

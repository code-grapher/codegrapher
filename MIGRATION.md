# MIGRATION.md — Port Record: codegraph (TypeScript) → codegrapher (Go)

This document records what was ported, what was deliberately changed, what was
skipped, how behavior differs from the original, and the benchmark results.
See AGENT-BRIEF.md for the original task definition and KNOWN-BUGS.md for the
full bug/divergence catalogue.

---

## 1. Scope ported

### model

Core data types (`Node`, `Edge`, `File`, `UnresolvedRef`), node/edge/kind
constants, and the deterministic ID scheme (`kind:sha256(filePath:kind:name:line)[:32]`).
Ported from `types.ts` and the node-ID logic scattered across `db/` and `extraction/`.
This package has no dependencies within the repo — it is the shared vocabulary for
all other packages.

### store

SQLite persistence layer using `modernc.org/sqlite` (pure Go, FTS5 supported).
Schema ported verbatim from `db/migrations.ts`: tables `schema_versions`, `nodes`,
`edges`, `files`, `unresolved_refs`, `project_metadata`; FTS5 virtual table
`nodes_fts` with sync triggers; migration runner. All PRAGMAs preserved
(WAL, synchronous=NORMAL, 64 MB cache, 256 MB mmap, busy_timeout 5000 ms).
Node/edge CRUD, stats queries, FTS5 BM25 search, language aggregation, file hash
tracking, and pending-change detection are all ported. Sources: `db/sqlite-backend.ts`,
`db/migrations.ts`, `graph/index.ts`, `search/index.ts`.

### internal/extract

File-level extraction pipeline (from `extraction/`): language detection, Go AST
walk, TypeScript/JavaScript tree-sitter walk, YAML file-node registration, and the
docstring-extraction helpers. The Go walk uses `go/parser` + `go/ast` as the primary
scanner (ADR-003); the gotreesitter-based Go walk is retained as a test oracle.
TypeScript and JavaScript are parsed via gotreesitter (ADR-001 superseded). Sources:
`extraction/tree-sitter.ts`, `extraction/languages/go.ts`, `extraction/languages/typescript.ts`.

### internal/tsparse

Thin wrapper around `github.com/trakhimenok/gotreesitter` (a patched fork of
`github.com/odvcencio/gotreesitter`) exposing `Parser`, `Tree`, and `Node` types
matching the node-traversal API used by the TS/JS extraction walk. No CGO; no WASM.
Grammar blobs for Go, TypeScript, and JavaScript are embedded in the gotreesitter
binary. The package boundary fully encapsulates parser selection from the rest of
the codebase.

### resolve

Five-phase resolution pipeline ported from `resolution/index.ts`: (1) import-path
resolution, (2) name-qualified lookup, (3) cross-file symbol binding, (4) Go
struct→interface conformance heuristic synthesis, (5) interface-method→implementation
`calls` heuristic synthesis. The Go framework resolver (`resolution/frameworks/go.ts`)
is ported and handles `net/http` route registrations, producing `route` nodes and
`calls` edges. The dotted-reference (`cache.warm`) proximity-ranked member lookup
is ported.

### indexer

Orchestrator that ties extract + resolve into a full index build. Parallel worker
pool using `golang.org/x/sync/errgroup` with a bounded goroutine count; store writes
serialized. Directory scanning with `.gitignore` / `codegraph.gitignore` respect
(ported from `utils.ts` ignore logic). Git hooks installation/removal. Worktree
detection. Sources: `bin/init.ts`, `bin/index.ts`, and the orchestration layer in
`sync/index.ts`.

### query

All five query verbs — `query` (FTS5 BM25 + LIKE + Levenshtein fallback, rescoring
with kind/path/name bonuses), `callers`, `callees`, `impact` (blast-radius subgraph
merge), `affected` (test-file reachability), and `files` listing. Exact rescore
formula ported: `((bm25 + kindBonus) + pathRelevance) + nameMatchBonus`. Ambiguous
symbol aggregation (all definitions, deduplicated by node ID, with a `note` appended
when more than one definition matched) is ported. Sources: `graph/index.ts`,
`search/index.ts`, `bin/query*.ts`.

### mcp

MCP server in stdio direct mode (ADR-001 / C-gap: daemon/proxy modes not implemented).
All eight tools ported: `codegraph_search`, `codegraph_callers`, `codegraph_callees`,
`codegraph_impact`, `codegraph_node`, `codegraph_explore`, `codegraph_status`,
`codegraph_files`. Tool allowlist via `CODEGRAPH_MCP_TOOLS`. `codegraph_explore`
implements the full adaptive algorithm: FTS + exact-name supplement → PascalCase type
bias → density/relevance scoring → source slice reads → line-gap clustering → adaptive
sibling skeletonization → relationships section + staleness banner, with output budgets
tiered by project file count. Staleness banners and the catch-up gate are ported.
Sources: `mcp/tools.ts`, `mcp/server.ts`, `mcp/context.ts`.

### lock

PID-file lock over `.codegraph/codegraph.lock` via O_EXCL create. Stale-lock
detection: PID dead (Unix) or mtime > 2 min (Windows / fallback). In-process async
mutex layered on top. Sources: `utils.ts` FileLock class.

### watch

File-system watcher using `github.com/fsnotify/fsnotify`, implementing the same
debounce semantics as upstream (`CODEGRAPH_WATCH_DEBOUNCE_MS`, default 2000 ms).
Pending-file map for MCP staleness banners; lock-busy reschedule without dropping
pending changes. Sources: `sync/watcher.ts`.

### snapshot

INGR-format export/import (`codegraph export` / `codegraph import` verbs). One
`.ingr` file per table (`nodes`, `edges`, `files`, `unresolved_refs`,
`project_metadata`), records sorted by primary key for byte-determinism. Volatile
fields (`updated_at`, `indexed_at`, absolute paths) excluded or normalized so two
exports of the same code tree are byte-identical. Uses `github.com/ingr-io/ingr-go`
(MIT, pure Go). This feature is absent in the original; see ADR-002.

### internal/tsparse

(Described above under that heading.)

### internal/cli/cmd

Cobra-based CLI with all ported verbs: `init`, `uninit`, `index`, `sync`, `status`,
`query`, `files`, `callers`, `callees`, `impact`, `affected`, `serve`, `unlock`,
`export`, `import`, `version`. All `--json` flags preserved. Terminal output uses
Cobra's built-in help and plain text; no Bubble Tea interactive progress UI was
required for the current feature set. Sources: `bin/` commands.

---

## 2. Deliberately skipped upstream modules

**installer (~3.3K LOC):** `src/installer/` — agent-config editors for Claude
Desktop, Cursor, VS Code, Windsurf, Zed, and related `install`/`uninstall` CLI
commands. Orthogonal to code intelligence; the install story for a static Go binary
is `go install` or a release download, not an IDE config patcher.

**upgrade:** `src/upgrade/` (~515 LOC) — npm self-update logic. Meaningless for a
static Go binary; version management is handled by the module system and release
pipeline.

**16 additional tree-sitter languages:** Python, Ruby, Rust, C/C++, C#, Java, PHP,
Swift, Kotlin, Dart, Scala, Elixir, Haskell, Lua, Bash, SQL. Per scope decision
(2026-06-10): Go and TypeScript/JavaScript only for v1.

**6 standalone extractors:** DFM, Vue, Svelte, Razor, MyBatis XML, Liquid —
format-specific extractors outside the tree-sitter core. Skipped with the language
scope decision.

**~19 framework resolvers:** Angular, React, Next.js, NestJS, Express, Fastify,
Django, Rails, Laravel, Spring, Gin (non-route parts), Echo, Fiber, and others in
`resolution/frameworks/`. Only the Go net/http framework resolver (`go.ts`) is
ported. Angular projects get core-TS extraction identical to upstream behavior
(no angular-specific resolver exists upstream either — parity preserved).

**MCP daemon and proxy modes:** Detached per-project daemon process
(`.codegraph/daemon.sock`, `daemon.pid`, idle timeout, peer sweep) and the
stdio↔socket proxy with ppid watchdog and version-matched handshake. Direct stdio
mode is implemented; `CODEGRAPH_DAEMON_INTERNAL` is rejected with a clear error and
daemon-default transport falls back to direct mode with a stderr notice. See
KNOWN-BUGS.md C.

---

## 3. Behavior differences vs the original

### Parser strategy (ADR-003)

Go files are parsed by `go/parser` + `go/ast` (the standard library). The original
uses a C tree-sitter grammar compiled to WASM, executed under Node's WebAssembly
runtime. `go/parser` parses all valid Go correctly and several orders of magnitude
faster; the pathological file that costs the WASM grammar 330 s / 7.7 GB of RSS
parses in under a millisecond. The gotreesitter Go walk is retained in
`internal/extract/walk_go.go` as a test oracle; a differential test asserts
identical emission between both walks over the fixtures and a real corpus.
TypeScript/JavaScript continue to use gotreesitter (same grammar sources as
upstream, different compilation path).

### Correctness-over-compatibility mandate

Where the original contains undefined behavior that would corrupt the index,
codegrapher diverges deliberately. The full list is in KNOWN-BUGS.md sections D
and E. Short summary:

- **D-1 (lock TOCTOU fix):** Upstream treats a lock file with empty/unparsable
  content as stale regardless of age. Because the O_EXCL create and PID write are
  separate operations, that window allows two concurrent acquirers to both win and
  corrupt the SQLite index. codegrapher treats young-but-invalid locks as held;
  only locks naming a dead PID or older than `StaleTimeout` (2 min) are reclaimed.
  A `TestRaceAcquire` (20 concurrent acquirers) reproduced the upstream race
  intermittently; it is consistently clean under the fix.

- **Go type docstrings now extracted:** Upstream's WASM Go grammar walk missed doc
  comments on `struct`/`interface`/`type` alias declarations (KNOWN-BUGS.md B-0).
  `go/parser` and the corrected gotreesitter walk both extract them correctly.
  Goldens were re-baselined from our binary after the fix; the original's incorrect
  output is no longer the reference.

- **Blank-identifier skip:** `_` declarations are never emitted as nodes; the
  original silently indexes them.

### Deliberately reproduced upstream bugs

- **UB-2:** Exported TS/JS declarations lose their doc comments (export wrapper
  shifts the `previousNamedSibling` lookup off the comment node).
- **UB-3:** TS return-type annotations produce two identical `references` rows,
  inflating edge counts.

Both are reproduced exactly, with `TODO(upstream-bug N)` markers at the code sites.

### Net edge difference on specscore-cli corpus

−62 non-`contains` edges vs the original (orig: 14,985 / port: 14,923). All
differences are on edges with `line=0` (unresolved-reference or heuristic edges).
Two accepted patterns: E-1 (12 missing `imports` reference edges for files that
hit the gotreesitter parse-timeout and were supplemented by the go/parser fallback
— no practical query impact) and E-2 (~50 net heuristic attribution differences
from proximity tie-breaking between JS and Go resolvers). See KNOWN-BUGS.md E.

### New features absent upstream

- **`export` / `import` verbs** (ADR-002): INGR snapshot format for git-committed
  index snapshots. CI/teammates import a snapshot then sync, turning cold-start
  indexing into seconds.
- **codegrapher.dev viewer** (ADR-002): a static, serverless browser viewer that
  loads committed `.ingr` files and provides symbol search/browse with client-side
  filtering and callers/callees navigation. First target: specscore-cli.

---

## 4. Performance (cold index + sync on specscore-cli)

Measured on Apple M2, `CGO_ENABLED=0`, `CODEGRAPH_NO_WATCH=1`:

| Metric | original codegraph (Node 22) | codegrapher (Go) |
|---|---:|---:|
| Indexing CPU time | 13.6 s | 3.8 s |
| Indexing wall time | 20.1 s | 4.5 s |
| Peak RSS | 223 MB | 138 MB |
| Incremental sync | 0.50 s | 0.09 s |

The Go port is approximately 3.5× faster at indexing and 5.5× faster at sync, with
38% lower peak memory. The primary contributors are: (1) `go/parser` replacing
WASM tree-sitter for Go files (the largest file class in specscore-cli), (2) the
parallel worker pool with no per-file restart overhead, and (3) the absence of the
Node/V8 JIT warm-up cost.

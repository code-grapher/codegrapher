# Task: Port codegraph (TypeScript) → Go as `codegrapher`

Convert https://github.com/colbymchenry/codegraph (local source: `../codegraph/src`,
MIT) from TypeScript into Go in this repo — github.com/specscore/codegrapher (local `.`).

## Context you'd otherwise lack

- codegraph is the hard code-intelligence dependency of **cover100**
  (github.com/specscore/cover100), an agent workflow whose researcher/engineer
  agents call `codegraph query|callers|callees` as their primary symbol lookup —
  index initialized in a git worktree (`codegraph init`), re-synced after every
  merge (`codegraph sync`). Your port must remain a **drop-in** for that usage:
  same verbs, same semantics.
- **codegrapher has THREE consumers, in two shapes:**
  1. **As a Go library** embedded in `specscore-cli` (`../specscore-cli`) — the
     spec↔code linking features (`trace`, `feature --where`, drift detection)
     will import your packages directly, no subprocess.
  2. **As a CLI** for external/agent consumers — cover100's agents shell out to
     it; that surface stays frozen and machine-readable.
  3. **Indirectly via specstudio-skills** (`~/projects/specscore/specstudio-skills`) —
     the SpecStudio skills (ideate/specify/plan/implement across Claude Code,
     Gemini, Codex) drive `specscore` CLI commands from their SKILL.md
     instructions; when specscore-cli gains trace/--where verbs powered by your
     library, every skill inherits spec↔code awareness. You don't integrate with
     the skills directly — you just need to not break the chain that reaches them.
- **Architecture requirement that follows: LIBRARY-FIRST.** Design clean public
  Go packages (suggested seams: `store`, `indexer`, `query`, `watch`) with the
  CLI as a thin consumer of the library — never logic in the CLI layer that the
  library doesn't expose. specscore-cli must be able to do everything the CLI
  can do via imports alone.
- Strategic reasons for the port: (1) a **single static Go binary** — upstream
  bundles a ~115MB Node runtime; our install story improves dramatically;
  (2) the spec↔code linking foundation above.
- **Read these two spec documents before designing anything** — they record
  decisions already made about spec↔code linking:
  - `../specscore/spec/ideas/spec-code-linking-via-codegraph.md` — concept:
    REQ→implementing-symbols/verifying-tests bindings, orphans report, drift
    detection, one-call agent orientation (`specscore feature <slug> --where`
    returns spec summary + current file:line entry points + drift flags).
  - `../specscore-cli/spec/ideas/codegraph-integration.md` — coupling decision:
    specscore-cli EMBEDS codegrapher as a Go library; shell-out to the CLI is the
    contract for non-Go consumers; reading the index SQLite schema directly is
    forbidden for everyone. Link markers like `// implements: <feature>#req:<slug>`
    are scanned by specscore-cli; codegrapher resolves symbols and keeps
    file:line current.
- Implication: every query verb needs a stable **machine-readable JSON output
  mode** carrying symbol kind + current file:line — and the library API must
  return the same data as typed Go structs. If the original lacks `--json`,
  add it (the one permitted deviation from as-is).

## Scope

- Migrate **as is** — feature parity in BEHAVIOR, no redesign: `init`, `uninit`,
  `index`, `sync`, `query`, `callers`, `callees`, `explore`, plus the MCP server.
- **Terminal UI does NOT need to be 1:1** — it must be functional and preferably
  nice; use Bubble Tea (and Lip Gloss) for interactive/progress UI rather than
  replicating the original's exact rendering. Parity applies to data and
  machine-readable output, not to visual chrome.
- Language support: **Go and TypeScript only** for now. Upstream parses via
  web-tree-sitter + tree-sitter-wasms; choose cgo tree-sitter bindings with
  vendored grammars vs WASM-via-wazero — pick one and justify it in a short ADR
  (decision drivers: CGO_ENABLED=0 cross-compilation — which library embedding
  in specscore-cli makes MORE important — vs parse speed). See the pure-Go
  preference below: parsing is the one component that legitimately can't be
  pure Go; everything else should be.
- Keep `.codegraph/` directory + init/sync semantics conceptually compatible;
  the internal schema is yours (no consumer reads it directly).
- MIT license with attribution to the original.

## Quality & engineering guidelines

- **Write code that is easy to test.** We will be driving this repo to 100%
  test coverage later (with cover100); design for it now — small functions,
  injected dependencies (clock, fs, process boundaries), no hidden globals.
  100% is NOT mandatory in this task; porting `../codegraph/__tests__` is the
  MANDATORY MINIMUM.
- **Utilize concurrency where it pays**: indexing is embarrassingly parallel
  per file — use goroutines + errgroup with a bounded worker pool; keep the
  store writes serialized or batched. Make concurrency testable (pass worker
  counts, use deterministic ordering in outputs).
- **Libraries:** use Cobra for the CLI, Bubble Tea (+Lip Gloss) for terminal UI,
  and other well-known open-source Go libraries where they beat hand-rolling
  (e.g. errgroup, fsnotify, modernc.org/sqlite or mattn/go-sqlite3 per the ADR).
  Don't hand-roll what the ecosystem solved.
- **Prefer pure, safe Go.** If a component is hard to implement in pure Go and
  would require e.g. embedding a WASM runtime or other heavy unsafe machinery —
  and it is not core to parsing — SKIP it and report the skip with rationale in
  the final MIGRATION.md report. Scope reduction with a written reason beats a
  fragile dependency.
- **Benchmark gate:** when the port can index a real repo, STOP and ask me —
  we will benchmark original codegraph vs codegrapher indexing the
  `../specscore-cli` repo (cold index + sync) and compare wall-clock, memory,
  and result counts before continuing to polish.

## Method

1. **First deliverable — `CODEGRAPH.md`**: measure the original's test coverage,
   inventory its behavior (module map, CLI surface incl. exact output formats,
   index schema summary, coverage %, test inventory from `../codegraph/__tests__`),
   and your size/effort estimate.
2. **Golden parity is your verification harness**: run the original CLI and your
   port against the same fixture repos (one small Go repo, one small TS repo);
   diff `query/callers/callees` outputs. Every difference must be deliberate and
   documented — never accidental.
   **Ordering note:** Go maps do not guarantee iteration order — if the
   original's output order came from JS object/Map ordering, your serialized
   order may differ (e.g. JSON object keys). That is OK wherever order is not
   functionally meaningful; make the parity comparison order-insensitive for
   those cases (canonicalize/sort both sides before diffing) instead of chasing
   1:1 ordering. Within the port itself, still prefer DETERMINISTIC output
   (sort map keys before serializing) so repeated runs diff cleanly — just
   don't contort the code to mimic the original's incidental ordering.
3. Port `../codegraph/__tests__` as the base suite; prefer Go table tests. The
   library packages get their own unit tests independent of the CLI.
4. Use `../specscore-cli` as the example for Go CLI scaffolding and conventions —
   and as the reality check that your library API is importable and ergonomic
   from its codebase.
5. Dogfood: the original `codegraph` CLI is installed — use it to explore
   `../codegraph` itself (callers/callees/query) before reading files at random.
6. **Commit each package/command as soon as it is green** — never batch commits
   to the end; interrupted work must be resumable.
7. Where the original's source and its observed behavior disagree, the
   **observed behavior of the installed CLI is the spec**.

## Deliverables (reports)

1. `CODEGRAPH.md` — the original's inventory + coverage + your estimate
   (first deliverable, before porting).
2. `MIGRATION.md` — the final migration report: what was ported, what was
   deliberately changed (UI, JSON mode), what was SKIPPED with rationale
   (see pure-Go rule), parity-diff summary, benchmark results, known gaps.
3. `TEST-COVERAGE.md` — **one per Go package/dir**: current coverage, an honest
   estimate of how hard reaching 100% would be (what needs fakes/seams, what is
   genuinely hard to cover), and a concrete test-coverage improvement plan.
   These become the work orders for the later cover100 run on this repo.

## Questions to answer BEFORE you start porting (in your first reply)

- Use the installed codegraph CLI + cloc on `../codegraph/src` to estimate the
  task (symbols, files, LOC); put the estimate in `CODEGRAPH.md`.
- Will you use subagents and/or agent teams, and how will you split the work?
- What's ambiguous or risky? Ask now; I'm stepping away after this.

### Here is my thinking (feel free to change to better):

- **Estimating:** `codegraph init ../codegraph && codegraph stats` (or index +
  query counts) for symbol/file counts, plus `cloc ../codegraph/src` for LOC.
  The dist bundle is ~9.8MB of JS but source is much smaller; expect the parser
  bindings and the MCP server to dominate effort, not the store/query logic.
- **Work split:** single context first for the foundation — core types, the
  `.codegraph` store, and the golden-parity fixture harness. Only THEN fan out
  subagents per seam (indexer / query / CLI / MCP), each verified against the
  parity fixtures. Add one adversarial verifier pass that re-runs parity diffs
  and tries to refute "done" claims before anything merges. A full agent team
  is overkill; a worker-pool of subagents with one verifier is the right size.
- **Sequencing:** store → indexer (Go grammar first, TS grammar second) →
  query verbs → CLI → MCP server last (it wraps the same library calls).
- **ADR lean:** parsing is the ONE sanctioned exception to the pure-Go rule —
  tree-sitter requires cgo or a WASM runtime either way. Start with cgo
  bindings + vendored grammars for parse speed and ecosystem maturity, but
  PROVE the wazero/WASM fallback path in a spike branch if cgo blocks
  CGO_ENABLED=0 builds — single-binary cross-compilation is a launch
  requirement, not a nice-to-have. Whatever you pick, isolate it behind the
  `indexer` package boundary so the rest of the codebase stays pure Go and
  fully testable without the parser.
- **Risk watchlist:** (1) parity on `explore` output (likely the fuzziest verb —
  pin its format early); (2) tree-sitter grammar version drift between upstream's
  WASM grammars and Go bindings producing different node shapes — pin grammar
  versions and encode them in the parity fixtures; (3) the file watcher (`sync`
  semantics) — replicate the ~1s lag contract, don't redesign it.

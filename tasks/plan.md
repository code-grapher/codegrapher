# Plan: Go Line Coverage in Codegraph

Source spec: `SPEC.md`. Scope: Go only, line coverage, innermost per-function
counts (UI rolls up inclusive), coverage stored next to graph INGR data, stale =
keep+flag.

Cross-repo, three tracks: `codegrapher` (library + CLI), `server` (collect +
serve endpoints), `codegrapher-dev` (UI).

## Architecture decision (locked)

- The coverage math (profile → per-file + per-function records) lives in **one
  library package** in `codegrapher` (`coverage/`) with a clean public API.
- `codegrapher coverage <profile>` is a **thin CLI wrapper** over that library.
  Runs anywhere (laptop/CI) on the same checkout the tests ran on → produces
  `coverage.ingr` + `node_coverage.ingr` → uploads to the server.
- The **server** is mostly dumb storage: a collect endpoint validates the
  uploaded recordsets (via the library's format contract) and writes them next
  to graph files; the serve path lists them like existing recordsets.
- Library is exposed so the server (or any consumer) **can** call ingest
  directly later — no rework needed.

## Parallelization strategy

```
        ┌─────────────────────────────────────────────┐
        │ T0  coverage library API + STUB + format     │  (gating, sequential)
        │     in codegrapher — what all 3 repos build   │
        │     against                                   │
        └───────────────┬─────────────────────────────┘
                        │  [CP0: review the seam]
        ┌───────────────┼────────────────────────────────────────┐
        ▼               ▼                                          ▼
  TRACK A (codegrapher)   TRACK B (server)            TRACK C (codegrapher-dev) Phase 1
  real impl behind stub   collect+serve endpoints     prep+test against FIXTURES
  A1 schema v6 + store     B1 POST /coverage collect   C1 data models + load
  A2 profile parser        B2 GET serve recordsets     C2 per-line gutter
  A3 attribution + RLE     (built against stub lib)    C3 per-function badges
  A4 implement Ingest +CLI                             (no live server yet)
  A5 export/import recsets
        │                       │                                 │
        └───────────┬───────────┴─────────────────────────────────┘
                    ▼  [CP1: real lib + endpoints + UI-on-fixtures all green]
            TRACK C Phase 2: connect UI to live server (C4) + end-to-end
                    ▼  [CP2: full path ingest→upload→serve→render]
```

After **T0** the three tracks run as **3 parallel sub-agents, each in a git
worktree on a branch named `feat/coverage`** in its own repo.

## Worktrees

Same branch name in all three repos: **`feat/coverage`**.
- `codegrapher`  → worktree on `feat/coverage`
- `server`       → worktree on `feat/coverage`
- `codegrapher-dev` → worktree on `feat/coverage`
(`server`'s `replace ../codegrapher` must point at the codegrapher worktree for
the parallel build — see CP0 note.)

---

## T0 — Coverage library API + stub + recordset format  ⟶ GATING
**Repo:** `codegrapher`  ·  **Depends on:** none
**Files:** `coverage/` new package — `coverage.go` (public API + types),
`coverage_stub.go` (deterministic placeholder impl), `format.go` (INGR recordset
schema for `coverage` + `node_coverage` + a `Validate([]byte)` helper),
`coverage_test.go`.

**Public API (indicative — finalize here):**
```go
package coverage

type Options struct { Ref, Root string }
type Summary struct { FilesMatched, FilesSkipped int; PctCovered float64 }

// Ingest parses a Go coverprofile and writes coverage + node_coverage records
// into store, attributing lines to the innermost enclosing node.
type Ingestor interface {
    Ingest(ctx context.Context, st *store.Store, profile io.Reader, opts Options) (Summary, error)
}

// Recordset format the CLI emits and the server validates.
func Validate(recordset string, data []byte) error   // "coverage" | "node_coverage"
```

**Acceptance criteria**
- Package compiles and is importable by both the CLI and `server`.
- `Ingestor` stub returns a deterministic Summary + writes a tiny fixed sample
  (or no-ops) so downstream callers compile and exercise plumbing.
- `Validate` enforces the recordset shape (used by the server collect endpoint).
- Recordset field layout for `coverage` / `node_coverage` documented here and in
  `SPEC.md §4.1/§4.4` (already aligned).

**Verify**
- `go build ./...` in codegrapher; `go test ./coverage/...` green (stub-level).
- Sanity: a throwaway importer (or the server worktree) compiles against it.

### ▶ CHECKPOINT 0 — Review the seam
Human review of the public API + recordset format **before** fan-out. Point
`server`'s `replace` at the codegrapher worktree. **Then launch 3 parallel agents.**

---

## TRACK A — codegrapher real implementation
Replaces the T0 stub with the real thing. (Was T1–T5.)

### A1 — Schema v6 + coverage store layer
Files: `store/schema.sql` (v5→v6), migration, `model/model.go`, `store/coverage.go`.
- AC: `coverage` + `node_coverage` tables per SPEC §4.1; clean v5 migration;
  read/write helpers; `node_coverage.node_id`→`nodes.id` `ON DELETE CASCADE`.
- Verify: `go test ./store/...` incl. migration-from-v5 + round-trip.

### A2 — Coverprofile parser (`coverage/profile.go`)
Files: `coverage/profile.go`; add `golang.org/x/tools/cover` to `go.mod`.
- AC: parse set/count/atomic via `cover.ParseProfiles`; blocks → per-file
  covered/uncovered line set (covered if any covering block count>0).
- Verify: table tests (set/count/multi-block/zero-count).

### A3 — Innermost attribution + RLE (`coverage/attribute.go`, `rle.go`)
- AC: each line → innermost enclosing node; non-overlapping per-node counts;
  file-level lines (no enclosing fn) counted in `coverage` only; RLE round-trip.
- Verify: nested-closure fixture (parent excludes child lines); pct math.

### A4 — Real `Ingestor` + `codegrapher coverage` CLI
Files: real impl in `coverage/`, `internal/cli/coverage.go`, register in root.
- AC: replaces stub; CLI parses profile → resolves module path → repo-relative,
  writes records stamped with `content_hash`+`run_at`; `--out <dir>` emits
  `coverage.ingr`+`node_coverage.ingr`; prints matched/skipped/% summary;
  unmatched files skipped (warn, non-fatal).
- Verify: integration test ingest fixture profile → assert rows + percentages.

### A5 — Export/import recordsets + manifest
Files: `snapshot/scoped.go`, `snapshot/snapshot.go`, manifest `Counts`.
- AC: export emits `coverage.ingr.{zst,gz}` + `node_coverage.ingr.{zst,gz}`;
  manifest counts include both; import optional (absent = no error); `run_at`
  excluded from byte-determinism.
- Verify: extend `cmd/codegrapher/parity_test.go` round-trip + determinism.

---

## TRACK B — server collect + serve endpoints
Built against the T0 stub library; works with real lib once A4/A5 land.

### B1 — `POST /coverage/...` collect endpoint
Files: `internal/handler/coverage_handler.go` (new), register in
`cmd/server/main.go`, reuse `auth.Middleware`.
- Route (model on `/graph/` path validation):
  `POST /coverage/{git_host}/{org}/{repo}/{ref}/{lang}/{version}`
- AC: accepts uploaded `coverage.ingr` + `node_coverage.ingr` (multipart or two
  PUTs — decide in task); validates via `coverage.Validate`; writes to
  `{baseDir}/codegraphs/{repoID}/{ref}/{lang}/{version}/` pre-compressed
  (zst+gz) exactly like graph recordsets; auth required (X-API-Key/Origin);
  returns 204/200; bad format → 400.
- Verify: handler test — valid upload writes files; malformed → 400; unauth → 401/403.

### B2 — Serve coverage recordsets (GET)
Files: `internal/handler/graph_handler.go` (extend valid recordset names).
- AC: `coverage`, `node_coverage` added to the valid `{name}.ingr` set served by
  `ServeGraph`; content negotiation (zstd/gzip) unchanged; missing → 410 Gone
  (same as today).
- Verify: GET returns stored bytes with correct `Content-Encoding`; unknown name
  still rejected.

(Status/manifest already surface scope counts; include coverage counts if the
manifest is regenerated server-side — otherwise served as-is from export.)

---

## TRACK C — codegrapher-dev UI

### Phase 1 — prep + test against FIXTURES (parallel, no live server)
**C1 data models + load** — `data/graph.models.ts`, `data/graph-store.service.ts`,
data sources: add `FileCoverage`/`NodeCoverage` types + `coverageByFile`/
`nodeCoverageById` maps; load 2 optional recordsets from a **local fixture**;
graph loads fine when absent; cache in IndexedDB.
- Verify: lib unit test parses fixture → maps; absent path → empty maps, no error.

**C2 per-line gutter** — `viewer/viewer-file-content.component.*`: hit/miss/none
gutter state layered with Shiki + symbol links (no regression); stale
(`coverageByFile.contentHash !== file.contentHash`) → greyed + indicator.
- Verify: component test renders hit/miss/stale classes from fixtures.

**C3 per-function badges** — `viewer/viewer-symbol-list*`, `viewer-file-structure*`:
inclusive %+counts (own `NodeCoverage` + descendants via `contains` edges);
"—" when absent; greyed when stale.
- Verify: unit test of inclusive rollup against nested fixture.

### Phase 2 — connect to live server
**C4** — point `DbServerGraphDataSource` at the real `coverage`/`node_coverage`
recordsets from the server; remove fixture shim; manual + e2e against a server
worktree instance.
- Verify: load a repo with coverage from the live endpoint; gutter + badges
  render; absent-coverage repo still loads.

---

## ADDENDUM (owner, later request): server-side `/cover/` trigger + UI button

Decision: a UI **"Update test coverage"** button (Go scopes only, for now) hits a
server **trigger** endpoint; an **isolated runner** — NOT the server process —
does the heavy lifting and uploads via the existing `/coverage/` collect
endpoint (B1). The server stays dumb storage + dispatcher.

```
UI button (Go only)
   └─ POST /cover/{git_host}/{org}/{repo}/{ref}        ← trigger (like /index/)
        server: enqueue job, return 202; expose SSE progress
          └─ Runner (decoupled, isolated):
               clone @ref → go test ./... -coverprofile
               → codegrapher coverage (library Ingest) → recordsets
               → POST to /coverage/{...}/{lang}/{version}   (B1, existing)
   └─ on done: UI reloads coverage recordsets (C4 path)
```

Why a runner, not in-server exec: running a repo's tests = executing untrusted
code. Owner chose isolation. The runner is a **pluggable interface**; the server
only dispatches + reports status.

### D1 — server `/cover/` trigger endpoint + job + Runner interface
**Repo:** `server`  ·  **Depends on:** A4 (library Ingest), B1 (collect)
- `POST /cover/{git_host}/{org}/{repo}/{ref}` under `auth.Middleware`; model on
  the `/index/` job pattern (GetOrCreate job, 202 Accepted, SSE at
  `/cover/{repoID}/{ref}/events`). Reject non-Go scope requests (owner: Go only).
- Define `Runner` interface: `Run(ctx, repoID, ref) error` (runner clones, tests,
  ingests, and uploads to `/coverage/` itself — server just invokes + tracks).
- Ship ONE concrete runner as the default (see D-FORK) behind the interface.

### D2 — UI "Update test coverage" button
**Repo:** `codegrapher-dev`  ·  **Depends on:** D1, C1–C4
- Button visible only for **Go** scopes. Click → `POST /cover/...`; subscribe to
  SSE progress; on completion reload coverage recordsets (reuse C4's load path).
- States: idle / running (progress) / done (refreshed) / error. Disabled for
  non-Go.

### D-FORK RESOLVED (owner): Docker sandbox, reuse synchestra-vm
Isolation = **Docker container**, and we **unify/reuse code with synchestra-vm**
(`github.com/synchestra-io/synchestra-vm`) rather than build Docker logic twice.

What synchestra-vm gives us (via `pkg/host`): `NewDockerRunner()`,
`ContainerRunner{CreateAndStart, Stop}`, `ContainerConfig{Image, Env, Mounts,
Entrypoint,...}`, and battle-tested isolation presets (read-only rootfs,
CAP_DROP=ALL, non-root 65532, no-new-privileges, seccomp=default, network
isolation, CPU/mem/PID limits) in `internal/host/runner/spawner.go:buildSpec()`.

Gap to close (the actual unification work): its runner targets LONG-LIVED,
agent-in-container sandboxes (gRPC register-back). We need a **one-shot**
"run this command in a locked-down container, wait for exit, collect an
artifact file" capability. That logic should live in **synchestra-vm `pkg/`**
(promoted/added), consumed by BOTH projects.

#### D0 — Promote a one-shot sandboxed-exec API into synchestra-vm  *(repo: synchestra-vm)*
- New `pkg/sandbox` (or extend `pkg/host`): a reusable API roughly
  ```go
  type Job struct { Image string; Repo RepoSpec; Cmd []string; Collect []string; Limits Limits }
  type Result struct { ExitCode int; Logs []byte; Artifacts map[string][]byte }
  func RunOnce(ctx context.Context, j Job) (Result, error)
  ```
  built on the existing DockerRunner + the `buildSpec()` isolation preset
  (promote the preset out of `internal/host/runner` so it's reusable). Adds the
  missing wait-for-exit + copy-artifact-out (docker CopyFromContainer) steps.
- Keep synchestra's existing provisioning working (additive; refactor preset
  into a shared helper, no behavior change). Its gates stay green.

#### D1 (revised v3) — server `/cover/` trigger + Docker test runner (egress on, CLONE ONCE)
Owner constraints: egress is fine; the hard rule is **clone the repo exactly
once**. The server already clones @ref for indexing — the runner MOUNTS that
clone, it does NOT clone again inside the container. Container does the one
untrusted step (run tests); server (already imports codegrapher) does ingest.

- `POST /cover/{git_host}/{org}/{repo}/{ref}` trigger + job + SSE (as above).
- Server prep: ensure the repo is cloned @ref ONCE (reuse the existing indexing
  clone path / clone cache — single source) AND indexed (graph needed for
  attribution). No second clone anywhere.
- Concrete `Runner` = thin adapter over synchestra-vm `pkg/sandbox.RunOnce`:
  - **ISOLATION RULE: never bind-mount untrusted host source (esp. read-write).**
    Malicious test code must not be able to touch host files / corrupt the clone.
  - **Reuse the single clone, no second `git clone`, no host mount.** From the one
    indexing clone, `git archive --format=tar {ref}` (tree only, no `.git`, no
    network) → **inject** the tar INTO the container (`CopyToContainer`) onto a
    writable tmpfs workdir. Container works on its own ephemeral copy; discarded
    on removal.
  - Writable tmpfs for the workdir, `GOCACHE`, `GOTMPDIR`, and `/out/cover.out`.
  - (Read-only bind of a host GOMODCACHE is acceptable later — ro can't corrupt —
    but the SOURCE is always injected, never mounted.)
  - **Network = egress allowed** (bridge) so `go test` resolves modules; optional
    later optimization: mount host GOMODCACHE to cut repeat downloads. `GOTOOLCHAIN=local`
    + recent-Go image to avoid surprise toolchain fetches.
  - Cmd = `go test ./... -coverprofile=/out/cover.out`; Collect = `/out/cover.out`.
- After RunOnce: server calls `coverage.Ingest(store, cover.out)` directly and
  stores recordsets where ServeGraph reads them — **no CLI-in-image, no
  upload-to-self** (B1 `/coverage/` upload stays for the external CLI/CI path).
- server `go.mod` gains a require on `github.com/synchestra-io/synchestra-vm`
  (local `replace` to the worktree until released).

#### Open sub-decisions (will confirm at D-build time, not now)
- Exact home/name of the shared API in synchestra-vm (`pkg/sandbox` vs extend
  `pkg/host`) and whether owner wants it shaped for general reuse.
- The runner image (where the codegrapher CLI binary comes from / how baked).
- Whether the runner uploads via HTTP `/coverage/` or writes to a shared volume.

Not blocking the base feature: CLI upload + rendering + B1/B2 storage all work
without D0/D1/D2.

## Track E — extract the shared sandbox library (owner decision)
Decision: YES, extract — **after D1 lands** (let D1 prove the full chain first; the
move is then mechanical, import-path + go.mod only). Home: a **neutral standalone
repo**, owned by neither product. Both synchestra-vm and codegrapher depend on it.

Why: synchestra-vm is a product, not a library (product-imports-product smell);
and consuming `synchestra-vm/pkg/sandbox` drags its whole go.mod (firestore,
secretmanager, grpc, otel…) into consumers' module graph. A dedicated repo has a
**minimal go.mod (~just `docker/docker`)**.

### E1 — create the neutral sandbox repo  *(after D1)*
- Home: **`strongo`** org (basic open-source Go libs — most neutral fit; chosen
  over the `sneat-co` brand umbrella). Proposed module path
  **`github.com/strongo/sandbox`** (pkgs: `sandbox` = RunOnce/Job/Result;
  `isolation` = hardened preset). Confirm exact name at E1.
- License **Apache-2.0**; copyright holder **Sneat.co** (NOTICE: `Copyright 2026
  Sneat.co`). Hosted in `strongo` org; copyright Sneat.co.
- New repo, minimal `go.mod` (~just `docker/docker`). Move
  `internal/host/isolation` (preset) + `pkg/sandbox` (RunOnce + types) out of
  synchestra-vm into it, unchanged API.
- Unit tests move with them (Docker-free + daemon-guarded integration test).
### E2 — synchestra-vm consumes the shared repo
- synchestra-vm imports `isolation` + `sandbox` from the new repo; its existing
  runner (already routed through the shared preset) swaps to the new import.
  Remove the now-extracted packages. Gates stay green (behavior identical).
### E3 — codegrapher-server consumes the shared repo
- Swap `synchestra-vm/pkg/sandbox` → the new repo's import; drop the
  synchestra-vm require/replace. `go mod tidy`; gates green.
- OPEN (confirm at E1): exact neutral org + module path (e.g.
  `github.com/<neutral-org>/sandbox`).

## Checkpoints
- **CP0** — review library API + recordset format; wire `replace`; then fan out.
- **CP1** — real lib (A1–A5) + server endpoints (B1–B2) + UI-on-fixtures (C1–C3)
  all green independently.
- **CP2** — Phase 2 wired: full path CLI ingest → upload → serve → UI render.

## Verification gates (every task)
- Go repos: `go test ./...` green, `go vet ./...` clean.
- UI: affected lib unit tests green, lint clean.
- No `nodes`/`edges` schema change beyond the additive v6 migration.

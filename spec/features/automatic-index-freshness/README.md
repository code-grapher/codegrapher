---
format: https://specscore.md/feature-specification
status: Implementing
---

# Feature: Automatic index freshness

> [SpecScore.**Studio**](https://specscore.studio): | [Explore](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness?op=explore) | [Edit](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness?op=edit) | [Ask question](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness?op=ask) | [Request change](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/automatic-index-freshness?op=request-change) |
**Status:** Implementing
**Source Ideas:** automatic-index-freshness

## Summary

Keep CodeGrapher indexes current automatically through one observable incremental reconciliation engine, beginning with a foreground watch command.

## Problem

An initialized graph becomes stale as soon as source files change unless a
caller remembers to run `codegrapher sync`. AI agents, Git operations, and
editors can generate rapid, duplicated, renamed, or bursty filesystem events,
so treating each event as an authoritative mutation would be both expensive
and incorrect. The current repository has most of the right primitives but no
standalone foreground command and no diagnostic event/operation lifecycle.

### Current-state audit

- Graph data is local to a filesystem checkout under `.codegraph/`, partitioned
  into scoped SQLite stores. The current identity model is the worktree path;
  commit-addressed snapshots and cross-worktree structural sharing do not yet
  exist.
- `Indexer.Sync` is the canonical incremental reconciler. It scans current
  candidates, uses size and mtime as a cheap prefilter, confirms changes by
  content hash, removes missing files, re-extracts changed files, resolves
  cross-file edges, refreshes SpecScore trace data, and runs maintenance only
  after a material change. A scanner-version mismatch escalates to full reindex.
- `Indexer.SyncFiles` is the cheaper path-aware form used by Git hooks and now
  by the watcher. It always hashes the supplied candidates, handles additions,
  edits and removals, rebuilds when scope-affecting manifests change, preserves
  incoming edges while a callee is replaced, refreshes trace data, and returns
  per-file extraction errors. `Indexer.GetChangedFiles` is a read-side
  classifier for status/freshness reporting; it combines Git candidates with
  index-vs-filesystem verification but does not mutate the graph.
- Writes are serialized in process and by a PID-backed cross-process lock.
- Sync mutation is not one SQLite transaction spanning all scope stores.
  Individual store operations can succeed before a later extraction, edge, or
  trace operation fails. `SyncResult.Errors` is therefore part of the freshness
  contract: strict callers must retain dirty state and retry rather than report
  the batch current. Lock contention is now an explicit result signal rather
  than an ambiguous zero-duration result.
- `watch.FileWatcher` already performs recursive `fsnotify` watching,
  debounce, path deduplication, retry after lock contention, create-directory
  registration, pending-path tracking, graceful stop/restart, and a bounded
  real-filesystem test. It is not wired into a `watch` CLI command, native
  watcher errors are discarded, and it exposes no structured raw-event or
  operation timing stream.
- Opt-in Git hooks already exist for `post-commit`, `post-merge`, and
  `post-checkout`. They launch `codegrapher sync` in the background and preserve
  user hook content. They remain a fallback when native watching is unavailable,
  not a second mutation implementation and not the default freshness mechanism.
- The scanner obtains tracked and untracked visible files from Git when
  possible and otherwise performs a layered `.gitignore` walk. Git change
  classification covers staged, unstaged, untracked, deleted, clean committed
  renames, and embedded repositories. Current metadata records HEAD and
  invalidates commit-sensitive data when it moves. It does not yet model an
  upstream/base ref, merge-base ancestry, branch-switch deltas, or the shared
  common Git directory as a durable repository identity.
- Git worktree support currently detects accidental use of another worktree's
  index and advises local initialization. Repository identity, HEAD identity,
  base snapshots, and delta overlays remain future work.
- Daemon/proxy transport is explicitly unimplemented. There is no PID,
  registration, IPC, or multi-repository daemon lifecycle to reuse yet.
- A local full initialization of this worktree indexed 1,275 files and 5,628
  nodes in about 6.3 seconds. This is directional development evidence, not a
  committed benchmark.

### Baseline and measurement gaps

The repository currently has correctness tests and one directional full-index
timing, but no stable benchmark suite for warm open/query latency, one-file
reconciliation, idle watcher CPU/wakeups, duplicate storage across worktrees,
or daemon startup. Those numbers MUST be collected on fixed fixture repositories
before choosing daemon pooling or structural sharing. Phase 1 adds operation
timings and batch counts so real watcher workloads can be measured without
instrumentation-only rescans.

## Behavior

### One canonical reconciliation engine

#### REQ: canonical-incremental-reconciliation

Every automatic trigger MUST feed potentially dirty state into the existing
incremental reconciliation path. Filesystem, daemon, WB, agent, Git-hook, and
manual triggers MUST NOT implement separate graph mutation logic. Filesystem
events are hints; current filesystem and index state remain authoritative.

### Foreground watch journey

#### REQ: foreground-watch-command

`codegrapher watch [path]` MUST watch the current repository/worktree by
default, establish the watch set before startup reconciliation closes the
race window, invoke the canonical incremental sync after a debounce window,
remain near-idle without changes, and stop cleanly on cancellation or an
interrupt. An uninitialized path MUST fail with an actionable `codegrapher
init` instruction rather than silently creating policy-changing state.

#### REQ: concise-default-output

Normal watch output MUST report startup, material update results, failures,
and shutdown without printing every native event. No-op reconciliations MAY be
suppressed in normal mode.

### Verbose watcher diagnostics

#### REQ: verbose-observation-stream

`codegrapher watch --verbose` MUST print timestamped native events with their
operation and project-relative path, then clearly report each reconciliation
operation starting and completing or failing. Completion MUST include elapsed
time and useful batch statistics: accepted events, unique dirty paths,
coalesced events, checked files, added/modified/removed files, updated nodes,
and whether reconciliation escalated to a full reindex. Related lifecycle
messages MUST carry a monotonically increasing operation identifier.

The library MUST expose structured observations and leave terminal rendering
to the CLI. Verbose observation MUST not alter reconciliation behavior or add
repository scans of its own.

### Reliability boundary

#### REQ: watcher-failures-visible

Native watcher errors and reconciliation failures MUST be visible. Failed or
lock-blocked reconciliation MUST retain dirty paths and retry; the index MUST
not be reported current until reconciliation succeeds.

Rename remains a native event hint: authoritative sync determines whether it
is a deletion, creation, or both. Event bursts MUST be debounced and
deduplicated before one sync. Event-storm thresholds, overflow-triggered full
reconciliation, and periodic safety reconciliation are later robustness work,
not separate indexing paths.

### Target architecture

```text
filesystem events     manual sync       future WB/agent/Git hints
        \                  |                       /
         +-------- dirty-path / dirty-state hints-+
                              |
                    debounce + batch + observe
                              |
                  canonical Indexer reconciliation
                              |
                 verify filesystem / Git state
                              |
                     scoped graph/index update
```

The long-term storage model is repository/content based: immutable parsed
content keyed by blob/content identity, commit-addressed snapshots mapping
paths to content, and worktree-local deltas. Branch names are labels, not graph
identity. The foreground watcher intentionally works with today's per-worktree
store while keeping this boundary intact.

#### Component responsibilities

- **Hint producers** report possible dirty paths or a whole-worktree dirty
  signal. Native filesystem events, a manual command, a future lifecycle API,
  Git hooks, WB, and agents are peers; none writes graph storage directly.
- **Watcher/batcher** owns recursive watch coverage, ignore admission,
  debounce, deduplication, exact dirty-path batches, retry retention, and
  diagnostic observations. Native events are never treated as authoritative.
- **Reconciler** owns filesystem/Git verification, content hashing, scope
  rebuild decisions, extraction, edge restoration/resolution, trace refresh,
  maintenance, error propagation, and the writer lock. `SyncFiles` handles
  exact hints; `Sync` handles whole-worktree uncertainty and startup repair.
- **Store boundary today** is one worktree-local `.codegraph` directory with
  scoped SQLite databases. Only the reconciler mutates it. Readers use existing
  strict freshness checks where the command contract requires current data.
- **Future daemon** owns process lifetime, repository registrations, watch
  budgets, retry/backoff, and IPC. It calls the same watcher and reconciler and
  does not introduce a daemon-only graph format.
- **Future reusable storage** separates immutable content-addressed parse data,
  commit snapshots mapping repository-relative paths to content IDs, and
  worktree-local overlays. A materializer presents the existing query model so
  callers do not need to understand base/delta composition.

#### Lifecycle decisions

- A foreground watch is scoped to the explicitly selected checkout or the
  current Git worktree. It refuses an initialized ancestor belonging to another
  worktree and never initializes implicitly.
- Startup order is signal handling, index open, native watch establishment,
  whole-worktree reconciliation, then readiness. Events received during
  reconciliation remain queued for a path-aware follow-up, closing the
  scan-to-watch race.
- Shutdown stops admission and timers, closes native watches, waits for any
  active reconciliation and its observations, then closes the index.
- A future daemon registration is keyed first by canonical worktree root plus
  repository/common-Git-dir identity. Registration is explicit or demand-led;
  path disappearance moves to a grace period, reappearance resumes with full
  reconciliation, and expiry releases watches while preserving reusable graph
  data. Moves are treated as disappear-plus-register unless repository identity
  proves continuity. Daemon restart reloads registrations, validates every
  path/HEAD, then reconciles before reporting current.
- The future generic hint API accepts repository identity, worktree path,
  optional dirty paths, optional old/new HEADs, source, and monotonic request
  identity. WB may call it, but CodeGrapher MUST remain correct without WB.

#### Git and identity decisions

- Repository-relative paths are stable within a checkout; branch names are
  presentation labels only. A snapshot is identified by repository identity,
  commit/tree identity, index format/extractor version, and relevant config.
- Staged, unstaged, untracked, deleted, and renamed paths form a worktree delta
  over HEAD. A branch switch or changed HEAD is a whole-worktree uncertainty
  until a verified tree diff can safely narrow it.
- Upstream, base branch, merge-base, and ancestry are useful future inputs for
  reuse and impact analysis, but none is required for Phase 1 correctness.
- Linked worktrees share a common Git directory but keep independent working
  trees and dirty overlays. Same repository plus same HEAD permits reuse only
  after config/version equality is verified; dirty state never shares a mutable
  overlay.

#### Decision record

1. **One reconciler, multiple hints.** Rejected: watcher-specific graph writes,
   because they would drift from manual sync correctness.
2. **Foreground command first.** Rejected for Phase 1: daemon-first delivery,
   because process ownership/IPC adds lifecycle risk before event correctness is
   measured.
3. **Exact dirty paths after native events.** Rejected: full scan per debounce,
   because it wastes work and can miss same-size/same-mtime non-Git edits behind
   a stat prefilter.
4. **Fail closed on incomplete watch coverage.** Rejected: warn and continue
   after the directory cap, because “watching” would falsely imply freshness.
5. **Per-worktree storage now, content-addressed reuse later.** Rejected for the
   first slice: immediate storage redesign, because it is not needed to validate
   the freshness journey and lacks duplicate-storage measurements.
6. **Generic lifecycle hints before WB coupling.** Rejected: a WB-only protocol,
   because correctness and public CLI behavior must remain independently useful.
7. **Existing Git hooks stay opt-in fallback.** Rejected: mandatory hook
   installation, because hooks are shared machine state, can conflict with hook
   managers, and do not observe ordinary editor saves.

#### Risks and mitigations

- Native APIs may duplicate, reorder, rename, or overflow events. Debounce and
  authoritative reconciliation handle ordinary ambiguity; overflow/event-storm
  full reconciliation remains a Phase 2 gate before daemonization.
- Directory limits or permission failures can leave partial coverage. Startup
  fails actionably instead of reporting readiness; runtime errors are emitted
  visibly and retain dirty state when reconciliation is involved.
- Ignore rules can change while watching. The watcher uses scanner-aligned
  built-ins/layered `.gitignore` rules, admits ignore-file events, and discovers
  newly visible subtrees after an ignore change.
- Multi-store partial failure can expose a partially updated graph. Strict
  error propagation prevents “current” claims; a transactional journal or
  generation swap is a later atomicity improvement.
- Daemon crashes, stale registrations, moved/deleted worktrees, PID reuse, and
  version skew can leak resources or serve stale data. The daemon phase requires
  versioned IPC, liveness ownership, reconciliation-on-restart, grace/expiry,
  and explicit health before it can replace foreground ownership.
- Shared parse/snapshot data can corrupt isolation if config, extractor version,
  or dirty overlays are mixed. Reuse keys include all semantic inputs and shared
  layers remain immutable.

### Phased delivery

1. Foreground watcher, startup reconciliation, structured diagnostics, and a
   bounded real-filesystem journey test.
2. Explicit overflow/event-storm policy, reconciliation fallback, and
   correctness comparison with a clean full index.
3. One reusable daemon lifecycle using the same watcher/reconciler.
4. Multi-repository/worktree registration, stale-path cleanup, and resource
   controls.
5. Cheapest measured same-repository/same-HEAD graph reuse.
6. Optional generic lifecycle hints, with WB as one caller.
7. Optional agent and Git hooks only where measurements show added value.
8. Content-addressed parsed representation and snapshot/delta sharing only
   after simpler reuse has been measured.

## Acceptance Criteria

### AC: foreground-watch-reconciles-real-edit

**Requirements:** automatic-index-freshness#req:canonical-incremental-reconciliation, automatic-index-freshness#req:foreground-watch-command

**Given** an initialized temporary repository and a running foreground watcher

**When** an existing source file is changed without changing its size or mtime,
and another source file is created

**Then** path-aware debounced reconciliations update content hashes, symbols,
and call edges, and the watcher remains active until it is cancelled.

### AC: burst-is-coalesced

**Requirements:** automatic-index-freshness#req:canonical-incremental-reconciliation

**Given** repeated events for one path and another path inside one debounce window

**When** the debounce window closes

**Then** one operation reports all accepted events, two unique dirty paths, and
the correct coalesced-event count.

### AC: verbose-reports-event-and-operation-timing

**Requirements:** automatic-index-freshness#req:verbose-observation-stream

**Given** `codegrapher watch --verbose`

**When** a filesystem event triggers reconciliation

**Then** terminal output identifies the received event, the operation start,
the matching completion identifier, elapsed time, and batch/sync statistics
without requiring exact timing values.

### AC: default-output-is-not-event-level

**Requirements:** automatic-index-freshness#req:concise-default-output

**Given** `codegrapher watch` without `--verbose`

**When** filesystem events arrive

**Then** raw event lines are absent while material update results and failures
remain visible.

### AC: failure-remains-dirty-and-visible

**Requirements:** automatic-index-freshness#req:watcher-failures-visible

**Given** a dirty path whose reconciliation returns an indexer error

**When** the operation ends

**Then** the path remains pending, the failure is observable, and a later retry
can clear it only after success.

### AC: lock-contention-retries-without-clearing

**Requirements:** automatic-index-freshness#req:watcher-failures-visible

**Given** a dirty path while another writer owns the graph lock

**When** reconciliation receives the explicit lock-unavailable result

**Then** a retry observation is emitted, no failure callback is emitted, and
the dirty path is cleared only after a later successful reconciliation.

### AC: watch-coverage-is-complete-or-start-fails

**Requirements:** automatic-index-freshness#req:foreground-watch-command, automatic-index-freshness#req:watcher-failures-visible

**Given** ignored dependency/build directories or a repository exceeding the
configured native directory-watch budget

**When** foreground watching starts

**Then** scanner-aligned ignored trees consume no watches, while an uncovered
admitted tree causes actionable startup failure rather than false readiness.

### AC: worktree-index-is-local

**Requirements:** automatic-index-freshness#req:foreground-watch-command

**Given** an uninitialized Git worktree nested below a different initialized
checkout

**When** `codegrapher watch` resolves its default path

**Then** it refuses the other checkout's index and instructs the user to run
`codegrapher init` in the current worktree.

### AC: populated-directory-move-is-reconciled

**Requirements:** automatic-index-freshness#req:canonical-incremental-reconciliation

**Given** a running watcher, an indexed source directory, and a pre-populated
source directory outside the tree

**When** the pre-populated directory is moved in or the indexed directory is
moved out

**Then** moved-in admitted files form a dirty-path batch, a missing directory
hint removes all tracked descendants, and neither direction waits for a later
unrelated event.

### AC: graceful-cancellation

**Requirements:** automatic-index-freshness#req:foreground-watch-command

**Given** a running watcher

**When** its command context is cancelled during idle or an active reconciliation

**Then** native watches and timers are released, active graph writes finish
before index close, and the command exits cleanly.

## Open Questions

1. What measured event count and time window should switch Phase 2 from a
   path-oriented batch to an explicit whole-worktree reconciliation signal?
2. Should one future daemon own a shared immutable base store with worktree
   delta stores, or should it initially coordinate isolated stores behind one
   process lifecycle?

---
*This document follows the https://specscore.md/feature-specification*

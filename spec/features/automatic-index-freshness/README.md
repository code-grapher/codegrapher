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
- Writes are serialized in process and by a PID-backed cross-process lock.
- `watch.FileWatcher` already performs recursive `fsnotify` watching,
  debounce, path deduplication, retry after lock contention, create-directory
  registration, pending-path tracking, graceful stop/restart, and a bounded
  real-filesystem test. It is not wired into a `watch` CLI command, native
  watcher errors are discarded, and it exposes no structured raw-event or
  operation timing stream.
- Git worktree support currently detects accidental use of another worktree's
  index and advises local initialization. Repository identity, HEAD identity,
  base snapshots, and delta overlays remain future work.
- Daemon/proxy transport is explicitly unimplemented. There is no PID,
  registration, IPC, or multi-repository daemon lifecycle to reuse yet.
- A local full initialization of this worktree indexed 1,275 files and 5,628
  nodes in about 6.3 seconds. This is directional development evidence, not a
  committed benchmark.

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

**When** a real source file is created or changed

**Then** one debounced reconciliation updates the graph and the watcher remains
active until it is cancelled.

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

**Given** a dirty path whose reconciliation fails or cannot acquire the lock

**When** the operation ends

**Then** the path remains pending, a failure is observable when appropriate,
and a later retry can clear it only after success.

### AC: graceful-cancellation

**Requirements:** automatic-index-freshness#req:foreground-watch-command

**Given** a running watcher

**When** its command context is cancelled

**Then** native watches and timers are released and the command exits cleanly.

## Open Questions

1. What measured event count and time window should switch Phase 2 from a
   path-oriented batch to an explicit whole-worktree reconciliation signal?
2. Should one future daemon own a shared immutable base store with worktree
   delta stores, or should it initially coordinate isolated stores behind one
   process lifecycle?

---
*This document follows the https://specscore.md/feature-specification*

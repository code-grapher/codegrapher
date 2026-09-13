---
format: https://specscore.md/idea-specification
status: Implemented
---

# Idea: Automatic index freshness

**Status:** Implemented
**Date:** 2026-09-12
**Owner:** alex
**Promotes To:** automatic-index-freshness
**Supersedes:** —
**Related Ideas:** —

## Problem Statement

How might CodeGrapher keep each repository and worktree graph current automatically while staying independently correct, resource-efficient, observable, and reusable across foreground, daemon, WB, and agent workflows?

## Context

CodeGrapher already has an incremental filesystem reconciler and an `fsnotify`
watching package, but it does not expose a foreground `codegrapher watch`
command. The watcher also drops native watcher errors and has no structured
observation seam for explaining which events arrived, which paths were
coalesced, when reconciliation started, or how long it took.

The founder-provided implementation prompt is preserved verbatim in this Idea
under **Original Prompt**. This Idea is the durable source artifact for the
Feature and phased Plan rather than an ad-hoc implementation brief.

## Recommended Direction

Use one canonical incremental reconciliation engine. Feed filesystem events and optional lifecycle hints into it, ship a foreground watcher first, then add robustness, daemon lifecycle, multi-worktree reuse, and optional integrations only when measured.

## Alternatives Considered

- **Build a daemon first** — rejected because foreground lifecycle and
  reconciliation observability can be proven with substantially less process
  management and no loss of future compatibility.
- **Let the watcher update individual graph records directly** — rejected
  because filesystem events are lossy hints and the existing incremental
  reconciler already verifies current filesystem state.
- **Require WB or agent hooks to announce changes** — rejected because those
  integrations are not present in every repository and cannot be a correctness
  dependency.

## MVP Scope

Ship codegrapher watch for the current repository/worktree with startup reconciliation, debounced filesystem watching, concise default diagnostics, detailed --verbose event and operation timing, graceful shutdown, focused tests, and one real filesystem integration test.

## Not Doing (and Why)

- Making WB, agent hooks, Git hooks, or an IDE required for correctness — CodeGrapher must remain standalone
- Implementing content-addressed structural sharing in the first slice — measure simpler reuse first
- Building the multi-repository daemon in the foreground-watcher slice — specify it now and implement later

## Key Assumptions to Validate

| Tier | Assumption | How to validate |
|------|------------|-----------------|
| Must-be-true | Existing `Indexer.Sync` correctly reconciles create, update, delete, rename-as-delete-plus-create, and no-op writes. | Exercise it through the watcher and compare queried/indexed state after a real filesystem edit. |
| Must-be-true | A foreground watcher can remain idle without polling the repository. | Inspect the event loop and verify the end-to-end test blocks until an actual filesystem event. |
| Should-be-true | A structured observation callback can add detailed diagnostics without coupling the watch package to terminal formatting. | Unit-test emitted observation values independently from CLI rendering. |
| Might-be-true | Commit-addressed graph reuse will materially reduce worktree startup time. | Measure same-HEAD worktree initialization before selecting a reuse design. |


## SpecScore Integration

- **New Features this would create:** `automatic-index-freshness`
- **Existing Features affected:** `wb-fleet-integration` benefits from later
  registration and same-HEAD reuse but is not required by the foreground MVP.
- **Dependencies:** existing `watch.FileWatcher`, `indexer.Indexer.Sync`, scoped
  SQLite stores, and cross-process index locking.

## Original Prompt

The following is the founder-provided prompt preserved verbatim as the source
artifact for this Idea:

````text
You are an engineer working on CodeGrapher.

Your task is to audit the existing implementation, define the complete target architecture for keeping CodeGrapher graphs/indexes up to date automatically, produce a phased implementation plan from the simplest useful version to the ultimate design, and then implement the phases that are justified and reasonably achievable now.

The design should aim high, but implementation should proceed incrementally. Do not overengineer the first version.

## Primary goal

CodeGrapher should keep its graph/index current automatically as source files change.

It should work well:

- as a standalone tool,
- across normal Git repositories,
- across multiple Git worktrees,
- with many active repositories/worktrees,
- in AI-agent-heavy development workflows,
- and especially when used together with WB/Workbench, which may manage many repos and worktrees.

CodeGrapher must remain independently correct without WB, Codex, Claude, Git hooks, IDE integrations, or other external systems.

External integrations may improve discovery, latency, or efficiency, but they must not be required for correctness.

## Important principle

There should be one canonical incremental indexing/update engine.

Do not create separate indexing logic for:

- filesystem watcher,
- daemon,
- WB integration,
- Codex/Claude hooks,
- Git hooks,
- manual sync/update commands.

All of these should ultimately feed into the same existing or improved incremental update path.

Conceptually:

```text
change/event/hint
    ↓
determine potentially dirty paths/state
    ↓
existing incremental update/reconciliation logic
    ↓
verify actual current file/Git state
    ↓
update graph/index
```

The watcher detects when something may need updating.

The incremental indexer determines what actually changed.

## First: audit the current implementation

Before designing or changing anything, inspect the repository carefully.

Document what already exists.

In particular determine:

### Existing indexing model

- How is the graph/index currently stored?
- Is graph state associated with:
  - filesystem path,
  - repository,
  - branch,
  - Git commit,
  - content hash,
  - blob hash,
  - some other identity?
- Is there already a concept of repository state or snapshot?
- Is there already caching or reuse between indexing runs?

### Existing incremental update support

We believe incremental updates likely already exist.

Find the actual implementation and determine:

- commands/APIs involved,
- how changed files are identified,
- whether only changed files are reparsed,
- how removed/renamed files are handled,
- how dependent graph edges are recalculated,
- whether update operations are transactional/atomic,
- whether hashes/mtime/content are used to avoid unnecessary work,
- how reliable incremental reconciliation currently is.

Do not invent a separate `sync` subsystem if equivalent functionality already exists.

Reuse and improve the existing primitives.

### Git awareness

Determine whether CodeGrapher currently knows:

- repository root,
- `.git` location,
- branch,
- HEAD commit,
- worktree identity,
- repository identity across multiple worktrees,
- staged/unstaged changes,
- Git blob IDs,
- commit ancestry,
- merge bases.

### CLI

Inspect the existing CLI conventions and architecture.

Do not rename the CLI.

Use:

```bash
codegrapher
```

Do not use `cgam`; that is only a possible future naming idea.

Determine how new commands should fit existing CLI conventions.

### Process/background infrastructure

Check whether there is already:

- a daemon,
- process management,
- PID handling,
- sockets,
- local IPC,
- background service support,
- logs,
- lock files,
- singleton enforcement.

Reuse existing infrastructure where appropriate.

### Watching

Check whether there is already any filesystem watching support, directly or through dependencies.

### Performance

Where feasible, establish rough baselines for:

- full indexing,
- incremental indexing of one changed file,
- incremental indexing of many changed files,
- startup cost,
- graph/index copy/open cost,
- memory footprint.

Do not spend excessive effort benchmarking before implementation, but gather enough evidence to guide the design.

---

# Target user-facing behaviour

## Foreground watcher

Add/support:

```bash
codegrapher watch
```

Default behaviour:

- run in the foreground,
- operate on the current repository/worktree by default,
- watch source changes,
- feed changes into the existing incremental update mechanism,
- print useful diagnostics suitable for development/debugging.

Useful foreground output may include:

- detected changes,
- batching/debounce behaviour,
- files actually reindexed,
- ignored/no-op changes,
- duration,
- graph/index update result,
- watcher overflow/errors,
- reconciliation fallback.

Do not make logs excessively noisy by default.

Support:

```bash
codegrapher watch --verbose
```

Verbose mode is intended for development and troubleshooting. It should
clearly report:

- filesystem events received, including event type and affected path where
  available,
- debounce/coalescing decisions and batch size,
- operations queued or triggered,
- each operation starting, completing, or failing,
- elapsed time for each completed operation,
- files reconciled, reindexed, ignored, or determined to be unchanged,
- watcher overflow, event-storm fallback, and full-reconciliation decisions,
- useful per-batch statistics such as events received, paths deduplicated,
  files updated, no-op files, queue/debounce delay, reconciliation duration,
  and total duration.

Make it possible to distinguish:

```text
raw filesystem events
    ↓
coalesced dirty paths
    ↓
reconciliation operations
    ↓
files actually reindexed
```

Include timestamps and a batch or operation identifier when useful for
correlating related messages.

The normal `codegrapher watch` output should remain concise. Verbose
diagnostics may be noisy, but must not add substantial processing overhead or
alter watcher behaviour.

Add automated tests confirming that verbose mode reports received events,
operation lifecycle, completion duration, and useful batch statistics, while
normal mode does not emit event-level detail. Avoid tests that depend on exact
timing values.

## Background daemon

Add/support:

```bash
codegrapher daemon start
codegrapher daemon stop
codegrapher daemon restart
codegrapher daemon status
```

The daemon should use the same watcher/indexing engine as `codegrapher watch`.

The difference is process lifecycle, not indexing behaviour.

Do not duplicate watcher logic.

Target architecture should support one long-lived CodeGrapher daemon managing multiple registered repositories/worktrees, rather than one daemon process per repo.

Design the complete model even if the initial implementation supports a narrower subset.

---

# Filesystem watcher design

A filesystem watcher should be the primary automatic trigger for updates.

Treat filesystem events as hints, not as authoritative state.

Filesystem watchers may:

- emit duplicate events,
- emit multiple events for one save,
- see temp-file/rename save patterns,
- receive huge event bursts during Git operations,
- overflow or lose events,
- behave differently by OS.

Therefore the watcher should:

1. mark affected paths/state dirty,
2. debounce and batch changes,
3. invoke the existing incremental reconciliation/update logic,
4. verify the actual current filesystem/Git state before updating the graph.

Use a modest debounce/batching window appropriate to the implementation. Do not hard-code assumptions without reason.

Something in the order of hundreds of milliseconds is likely reasonable, but choose based on the ecosystem/library being used.

## Event storms

For operations such as:

- checkout,
- pull,
- merge,
- rebase,
- branch switch,
- generated-code refresh,
- dependency/source regeneration,

do not insist on processing thousands of filesystem events individually.

Define a threshold/fallback where CodeGrapher switches to efficient repository/worktree reconciliation.

The goal is:

```text
many noisy events
    ↓
mark worktree dirty
    ↓
reconcile current state
```

rather than:

```text
10,000 events
    ↓
10,000 independent update operations
```

## Reliability

Reconciliation should happen at appropriate safety points such as:

- watcher startup,
- daemon startup,
- watcher overflow/error,
- repo/worktree registration,
- after large/ambiguous change bursts.

A periodic reconciliation may be useful, but do not add an aggressive timer unless justified.

Prefer event-driven operation plus strategic reconciliation.

---

# Git repository and worktree awareness

The ultimate architecture should understand that graph state is fundamentally related to repository content/state, not merely filesystem directories.

A worktree should be treated as a view over repository state plus local differences.

Example:

```text
main @ commit A
    │
    ├── worktree feature-x @ A
    │       └── modifies foo.go
    │
    └── worktree feature-y @ A
            └── modifies bar.go
```

Conceptually:

```text
feature-x graph
    = graph(A)
    + delta(foo.go)
```

CodeGrapher should not unnecessarily parse the entire worktree again if it already knows the graph for the same underlying repository state.

## Branch names are not graph identity

Do not key graph identity primarily by branch name.

Two branches may point to the same commit and should be able to reuse the same underlying graph state.

Example:

```text
main ───────┐
            ├── commit abc123
feature ────┘
```

There should not be two completely independent copies solely because the branch names differ.

## Initial worktree optimisation

Implement the simplest practical reuse first.

For example:

1. detect repository identity,
2. detect worktree,
3. detect current HEAD,
4. determine whether CodeGrapher already has graph/index state corresponding to that repository/HEAD,
5. reuse or cheaply copy that state if supported by the current architecture,
6. run existing incremental reconciliation against the actual worktree,
7. start watching subsequent changes.

Do not build an elaborate persistent graph engine in phase one.

## Ultimate target

Specify a future-capable architecture where CodeGrapher could structurally reuse indexed content.

For example:

```text
Repository
│
├── indexed content / blobs
│     content/blob hash
│          ↓
│     parsed symbols + relationships
│
├── snapshot A
│     paths → blob/content identities
│
├── main
│     base snapshot A + working delta
│
└── feature-x
      base snapshot A + working delta
```

If the same file/blob exists unchanged across many worktrees, CodeGrapher should ideally avoid reparsing and duplicating its parsed representation.

Git blob IDs or equivalent content hashes may be useful.

However:

- design for this,
- document it,
- keep the current storage model compatible where practical,
- but do not implement sophisticated structural sharing now unless the existing architecture makes it cheap.

Measure first.

---

# WB / Workbench integration

WB may know useful lifecycle information that CodeGrapher otherwise has to discover:

- repository cloned,
- repository opened,
- worktree created,
- worktree removed,
- worktree activated,
- branch switched,
- large Git operation completed,
- repository moved.

Design an optional integration where WB can notify CodeGrapher of these events.

For example, CodeGrapher could receive enough information to quickly register a newly created worktree and reuse an already-known graph state.

However:

## Hard requirement

CodeGrapher correctness must never depend on WB.

If WB is absent, CodeGrapher should still be able to inspect Git and discover the necessary repository/worktree information itself.

WB is an optimisation and discovery source.

## Do not over-couple

Do not make:

```text
Claude/Codex → WB → CodeGrapher
```

the required architecture.

CodeGrapher should expose its own clean integration point.

WB may call that integration point where useful.

If WB itself should eventually have a generic event/plugin mechanism, document this as a recommendation, but do not expand this task into a major WB architecture implementation unless it is exceptionally cheap and clearly necessary.

---

# Codex / Claude agent hooks

Evaluate integration with AI-agent harness hooks.

Examples might include post-edit/post-tool-use notifications.

Potential benefit:

```text
agent edits foo.go
    ↓
agent hook tells CodeGrapher foo.go is probably dirty
    ↓
CodeGrapher prioritises reconciliation immediately
```

But do not assume these hooks are necessary.

If the filesystem watcher already gives near-immediate reliable updates, agent hooks may add little value.

Treat them as optional low-latency hints.

If implemented, the semantics should be:

> these paths may have changed; reconcile them

not:

> blindly rebuild these files.

The CodeGrapher indexing engine must remain authoritative.

Do not make agent-hook installation mandatory.

---

# Git hooks

Evaluate but do not automatically implement:

- pre-commit,
- pre-push.

Possible purpose:

```text
pre-commit
    → verify/reconcile staged or changed files

pre-push
    → cheap consistency verification
```

These should be safety barriers, not the normal indexing path.

Avoid expensive hooks that developers will disable.

If the watcher + reconciliation architecture makes these unnecessary, document that conclusion.

If hooks add meaningful protection cheaply, propose or implement them in a later phase.

---

# Registration and daemon scope

The ultimate daemon should support multiple repositories and worktrees.

Specify how they are registered/discovered.

Consider sources such as:

- explicit CodeGrapher registration,
- current repo/worktree used by `codegrapher`,
- previously indexed repositories,
- Git worktree discovery,
- optional WB notifications.

Do not assume WB is present.

Define lifecycle behaviour for:

- registering repo/worktree,
- unregistering/removing worktree,
- missing paths,
- deleted worktrees,
- moved repos,
- daemon restart,
- stale registrations.

Keep the initial implementation simple.

---

# Phased delivery

You must specify the whole ultimate architecture, but implement progressively.

Do not try to implement every optimisation at once.

Use phases similar to the following, but adjust based on what already exists.

## Phase 0 — Audit and design

Deliver:

- current-state analysis,
- architecture,
- key decisions,
- risks,
- phased implementation plan,
- explicit mapping from existing CodeGrapher components to the proposed design.

Do not redesign existing functionality unnecessarily.

## Phase 1 — Foreground watcher MVP

Implement the simplest useful version:

```bash
codegrapher watch
```

Requirements:

- current repo/worktree by default,
- foreground operation,
- filesystem watcher,
- debounce/batching,
- reuse existing incremental update logic,
- startup reconciliation,
- graceful shutdown,
- useful diagnostics,
- correct handling of create/update/delete/rename as supported by the existing updater.

This phase should produce immediate real-world value.

## Phase 2 — Robust watcher behaviour

Improve:

- event storms,
- watcher overflow/errors,
- checkout/rebase/merge behaviour,
- large-batch reconciliation,
- reliability tests,
- performance tests where useful.

## Phase 3 — Daemon

Implement:

```bash
codegrapher daemon start
codegrapher daemon stop
codegrapher daemon restart
codegrapher daemon status
```

Requirements:

- reuse the same watcher implementation,
- clean process lifecycle,
- logs,
- singleton/locking behaviour,
- clean status output.

Initial daemon scope may be limited if necessary, but architecture should support many repos/worktrees.

## Phase 4 — Multi-repo/worktree daemon

Add:

- repository/worktree registration,
- multiple watchers,
- lifecycle management,
- stale path cleanup,
- efficient resource usage.

## Phase 5 — Git/worktree-aware graph reuse

Implement the cheapest high-value reuse mechanism supported by the current storage model.

Likely first target:

- same repository,
- same HEAD,
- known graph/index state,
- reuse/copy existing graph state,
- reconcile only worktree differences.

Do not implement a large content-addressed storage subsystem merely to satisfy the theoretical design.

## Phase 6 — WB integration

Add optional WB lifecycle notifications if they materially improve:

- worktree discovery,
- registration,
- startup latency,
- graph reuse.

Keep the CodeGrapher API/integration generic enough that WB is not special-cased deep in the indexing engine.

## Phase 7 — Optional agent/Git hooks

Only add if evidence shows clear value.

Evaluate:

- Claude hooks,
- Codex hooks,
- pre-commit,
- pre-push.

Prefer fewer moving parts.

## Future phase — Structural sharing/content-addressed graph storage

Specify but only implement if justified.

Potential goals:

- content/blob hash identity,
- parsed representation reuse,
- snapshots,
- deltas,
- graph sharing across branches/worktrees,
- reduced memory/storage,
- near-instant initialisation of new worktrees.

This is the destination, not necessarily the current task's implementation scope.

---

# Testing requirements

Add meaningful automated tests.

At minimum test where applicable:

## Watcher

- edit existing file,
- create file,
- delete file,
- rename file,
- rapid repeated saves,
- multiple files in one burst,
- ignored files,
- changes producing no graph difference,
- watcher shutdown/restart.

## Git operations

Test representative scenarios:

- branch checkout,
- worktree creation,
- merge/rebase or equivalent large tree changes,
- uncommitted working-tree changes.

Do not rely only on unit tests if filesystem watcher behaviour requires integration tests.

## Worktrees

Test:

- two worktrees at same HEAD,
- one diverging file,
- different branches pointing to same commit,
- worktree deletion,
- daemon restart with existing worktrees.

## Correctness

After incremental/watch-driven updates, compare graph/index results against an authoritative clean/full index where feasible.

This is important.

A watcher implementation that is fast but occasionally stale is not acceptable.

## Performance

Add lightweight benchmarks or timings where useful.

Especially compare:

- full reindex,
- one-file incremental update,
- burst update,
- new worktree initialisation before/after reuse.

Do not prematurely optimise based only on theory.

---

# Resource usage

The daemon may eventually watch many repositories/worktrees.

Design for reasonable:

- CPU,
- memory,
- file descriptor/watch handle use,
- idle overhead.

Do not continuously rescan entire repositories when idle.

The system should be close to idle when nothing changes.

---

# Failure behaviour

Define what happens when:

- index update fails,
- a file is temporarily invalid during save,
- repository disappears,
- watcher fails,
- Git metadata cannot be read,
- existing graph state is incompatible/corrupt,
- daemon crashes/restarts.

Prefer self-healing reconciliation over requiring manual cleanup.

Do not silently leave an index permanently stale.

---

# Observability

Provide enough diagnostics to understand what CodeGrapher is doing.

Useful information may include:

- watched repo/worktree count,
- pending dirty paths,
- last successful update,
- last reconciliation,
- watcher errors,
- update duration,
- graph version/HEAD state,
- daemon PID/status,
- worktree registrations.

Do not build a large telemetry system as part of this task.

---

# Design constraints

Prioritise, in order:

1. correctness,
2. simplicity,
3. reuse of existing CodeGrapher architecture,
4. low idle overhead,
5. fast incremental response,
6. future compatibility with graph sharing,
7. elegant integrations.

Avoid speculative complexity.

Do not introduce a large distributed/event framework.

Do not redesign unrelated CodeGrapher subsystems.

Do not implement sophisticated graph structural sharing until the simpler reuse mechanisms have been measured.

---

# Architectural principle to preserve

The intended long-term model is:

> Graphs belong primarily to repository states/content, not filesystem directories. Worktrees are views over those graph states plus local deltas.

But the implementation path should be:

> simple correct watcher → robust watcher → daemon → multi-worktree awareness → cheap graph reuse → deeper structural sharing only when justified.

---

# Expected outputs

Produce/update appropriate project documentation containing:

## 1. Audit report

Explain:

- what exists,
- what can be reused,
- gaps,
- surprising findings,
- current limitations.

## 2. Target architecture

Describe the complete end-state architecture, including components, responsibilities, graph/worktree identity, watcher behaviour, daemon model, integrations, and future structural sharing.

Include diagrams where useful.

## 3. Decision record

For major decisions, explain rationale and alternatives considered.

Especially cover:

- watcher vs hooks,
- daemon model,
- Git/worktree identity,
- graph reuse,
- WB coupling,
- structural sharing timing.

## 4. Phased implementation plan

Make phases independently useful and shippable.

Clearly identify:

- what is implemented now,
- what is deferred,
- dependencies,
- risks,
- success criteria.

## 5. Implementation

Implement the justified current phases, starting from the simplest working watcher and proceeding only where the architecture and existing code make the next phases sensible.

Do not stop after writing a plan if the implementation is reasonably actionable.

## 6. Tests

Add and run relevant tests.

## 7. Final report

Summarise:

- files/components changed,
- commands added,
- architecture implemented,
- tests run/results,
- measured performance where available,
- what remains for later phases,
- any decisions that require human review.

---

# Working style

Work autonomously.

Inspect the existing code before making assumptions.

Prefer adapting existing abstractions over introducing parallel ones.

If the repository already solves part of this problem under different terminology, reuse it.

Do not stop for minor ambiguities. Make a reasonable decision, record it, and continue.

Only stop for a genuinely blocking question where proceeding risks a major incorrect architectural decision.

If one area is blocked, continue work on all non-blocked areas.

The quality of the architecture and correctness matter most.

Implementation time, maintenance complexity, runtime cost, and AI token usage also matter. Prefer solutions that achieve most of the value with substantially less complexity.

````

## Open Questions

None at this time.

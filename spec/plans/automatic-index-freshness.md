---
format: https://specscore.md/plan-specification
status: Approved
---

# Plan: Automatic index freshness

**Status:** Approved
**Source Feature:** automatic-index-freshness
**Date:** 2026-09-12
**Owner:** alex
**Supersedes:** —

## Summary

Deliver the first independently useful slice of automatic freshness: a
foreground `codegrapher watch` command built on the existing watcher and
incremental reconciler, with structured verbose diagnostics, focused coverage,
and one bounded real-filesystem end-to-end journey. Later daemon, multi-repo,
WB hinting, and graph-sharing phases remain explicitly deferred in the Feature.

## Approach

First make watcher behavior observable without terminal coupling, then add the
CLI lifecycle and render concise/verbose output, and finally prove the whole
journey through a real filesystem edit and cancellation. This ordering keeps
the canonical indexer authoritative and makes each failure diagnosable. The
plan does not implement daemon or graph-storage redesign because neither is
needed to validate the foreground contract.

## End-to-End User Journey

1. I initialize a repository once and run `codegrapher watch` from that
   repository or pass its path. I see startup reconciliation finish and a clear
   watching message.
2. I make no changes. CodeGrapher stays quiet and close to idle; there is no
   periodic full-tree polling.
3. I save, create, rename, or delete source files. CodeGrapher batches the
   event hints, verifies actual state through incremental sync, and reports a
   concise material result.
4. When I need to diagnose behavior, I restart with `--verbose`. I see received
   events, matching operation start/completion identifiers, elapsed time, and
   batch/index statistics.
5. I press Ctrl-C or cancel the command. The watcher releases resources and
   exits cleanly without leaving a background process.

## Tasks

### Task 1: Preserve the source prompt and specify the architecture

**Verifies:** automatic-index-freshness#ac:foreground-watch-reconciles-real-edit, automatic-index-freshness#ac:verbose-reports-event-and-operation-timing
**Depends-On:** —
**Status:** complete

Store the founder prompt as the source Idea, record the current implementation
audit and end-state architecture in the Feature, and keep the executable slice
and deferred phases explicit.

### Task 2: Add structured watcher observations

**Verifies:** automatic-index-freshness#ac:burst-is-coalesced, automatic-index-freshness#ac:failure-remains-dirty-and-visible
**Depends-On:** 1
**Status:** planning

Add a library-level observation callback for native events, operation start,
completion, retry/failure, and watcher errors. Track exact per-operation event,
unique-path, coalescing, duration, and sync-result statistics without changing
debounce or reconciliation semantics. Cover success, coalescing, failure, lock
retry, and non-verbose/no-observer behavior with deterministic unit tests.

### Task 3: Add the foreground CLI command

**Verifies:** automatic-index-freshness#ac:verbose-reports-event-and-operation-timing, automatic-index-freshness#ac:default-output-is-not-event-level, automatic-index-freshness#ac:graceful-cancellation
**Depends-On:** 2
**Status:** planning

Wire `codegrapher watch [path]` to the existing `Indexer.Sync` and watcher.
Establish watches before startup reconciliation, support `-v/--verbose`, use
command writers/context for testability, emit actionable initialization and
disabled-watcher errors, and release the index and watcher on cancellation.

### Task 4: Prove the bounded whole journey

**Verifies:** automatic-index-freshness#ac:foreground-watch-reconciles-real-edit, automatic-index-freshness#ac:graceful-cancellation
**Depends-On:** 3
**Status:** planning

Add one integration test using a real `fsnotify` watcher, a real initialized
temporary Go repository, a real file edit, an observed graph update, and
context cancellation. Keep it skipped under short mode and avoid exhaustive
platform/event permutations already covered by watcher unit tests.

### Task 5: Review, verify, and reconcile lifecycle state

**Verifies:** automatic-index-freshness#ac:foreground-watch-reconciles-real-edit, automatic-index-freshness#ac:burst-is-coalesced, automatic-index-freshness#ac:verbose-reports-event-and-operation-timing, automatic-index-freshness#ac:default-output-is-not-event-level, automatic-index-freshness#ac:failure-remains-dirty-and-visible, automatic-index-freshness#ac:graceful-cancellation
**Depends-On:** 4
**Status:** planning

Run focused tests during implementation, the repository's required Go gates
once, SpecScore lint, and an independent adversarial review of spec, plan,
implementation, and tests. Fix or explicitly record every finding, then use
SpecScore lifecycle commands to derive the implemented plan/feature state.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/plan-specification*

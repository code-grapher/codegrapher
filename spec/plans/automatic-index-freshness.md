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

First audit the full lifecycle and preserve its decisions, then make watcher
behavior observable without terminal coupling. Feed exact dirty-path batches
to the canonical `SyncFiles` path, harden coverage/error/shutdown boundaries,
add the CLI lifecycle and concise/verbose rendering, and finally prove the
whole journey through real filesystem edits and cancellation. The plan does
not implement daemon or graph-storage redesign because neither is needed to
validate the foreground contract; it does specify their ownership and gates.

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

**Verifies:** automatic-index-freshness#ac:burst-is-coalesced, automatic-index-freshness#ac:failure-remains-dirty-and-visible, automatic-index-freshness#ac:lock-contention-retries-without-clearing, automatic-index-freshness#ac:watch-coverage-is-complete-or-start-fails, automatic-index-freshness#ac:populated-directory-move-is-reconciled
**Depends-On:** 1
**Status:** complete

Add a library-level observation callback for native events, operation start,
completion, retry/failure, and watcher errors. Track exact per-operation event,
unique-path, coalescing, duration, and sync-result statistics without changing
debounce or reconciliation semantics. Cover success, coalescing, failure, lock
retry, and non-verbose/no-observer behavior with deterministic unit tests.

Pass sorted exact dirty paths to `Indexer.SyncFiles`, retain them across real
errors and lock contention, fail startup rather than accept partial native
watch coverage, align watcher admission with scanner ignore rules, discover
pre-populated/newly-unignored trees, and wait for active writes during stop.

### Task 3: Add the foreground CLI command

**Verifies:** automatic-index-freshness#ac:verbose-reports-event-and-operation-timing, automatic-index-freshness#ac:default-output-is-not-event-level, automatic-index-freshness#ac:graceful-cancellation, automatic-index-freshness#ac:worktree-index-is-local
**Depends-On:** 2
**Status:** complete

Wire `codegrapher watch [path]` to startup `Indexer.Sync`, path-aware
`Indexer.SyncFiles`, and the watcher.
Establish watches before startup reconciliation, support `-v/--verbose`, use
command writers/context for testability, emit actionable initialization and
disabled-watcher errors, and release the index and watcher on cancellation.
Reject an initialized ancestor belonging to another Git worktree and propagate
structured indexer failures rather than treating them as successful batches.

### Task 4: Prove the bounded whole journey

**Verifies:** automatic-index-freshness#ac:foreground-watch-reconciles-real-edit, automatic-index-freshness#ac:graceful-cancellation, automatic-index-freshness#ac:failure-remains-dirty-and-visible, automatic-index-freshness#ac:populated-directory-move-is-reconciled
**Depends-On:** 3
**Status:** complete

Add one command integration test using a real `fsnotify` watcher, a real
initialized temporary Go repository, a same-size/same-mtime edit, an added
file, in-tree file rename, directory rename-out, content-hash/symbol/call-edge
assertions, clean-rebuild graph parity, and context cancellation. Add focused
real-filesystem tests for moving in a populated directory and failing closed
when a runtime directory-watch cap is reached; keep them skipped under short
mode and leave platform/event permutations to unit tests.

### Task 5: Review, verify, and reconcile lifecycle state

**Verifies:** automatic-index-freshness#ac:foreground-watch-reconciles-real-edit, automatic-index-freshness#ac:burst-is-coalesced, automatic-index-freshness#ac:verbose-reports-event-and-operation-timing, automatic-index-freshness#ac:default-output-is-not-event-level, automatic-index-freshness#ac:failure-remains-dirty-and-visible, automatic-index-freshness#ac:lock-contention-retries-without-clearing, automatic-index-freshness#ac:watch-coverage-is-complete-or-start-fails, automatic-index-freshness#ac:worktree-index-is-local, automatic-index-freshness#ac:populated-directory-move-is-reconciled, automatic-index-freshness#ac:graceful-cancellation
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

---
format: https://specscore.md/plan-specification
status: Executing
---

# Plan: Robust watcher and local daemon

**Status:** Executing
**Source Feature:** automatic-index-freshness
**Date:** 2026-09-12
**Owner:** alex
**Supersedes:** —

## Summary

Deliver the next two independently useful freshness slices: an explicit
event-storm fallback and one background CodeGrapher daemon with reliable
start/stop/restart/status behavior. The daemon reuses the foreground watcher's
reconciliation engine, exposes authenticated loopback lifecycle control, and
leaves multi-worktree registration and the TypeSpec browser API to their
separate follow-up phases.

## Approach

First make the watcher safe under an intentionally oversized batch and record
the required Phase 2 correctness/performance evidence, then
extract a repository freshness owner shared by foreground and background
lifecycles. Build the daemon as a per-user singleton whose readiness follows
watch coverage, startup reconciliation, and a drained event-generation barrier,
not process creation. Serialize starters with an OS file lock; track a random
instance nonce through `starting`, `ready`/`degraded`, `stopping`, and terminal
states; and use authenticated loopback proof rather than PID-only signaling.
Persist state in a user-private directory, rotate logs at 5 MiB with one prior
file, and keep process spawning behind a small testable platform boundary. This
slice watches one explicit worktree; durable
multi-worktree registration, public browsing endpoints, TypeSpec/OpenAPI, and
shared graph storage remain later Feature phases. The public API is reserved
under `/codegrapher/` (with versioned resources such as `/codegrapher/v1/...`),
while this plan's private lifecycle control surface remains loopback-only and
separate with a credential that can never authorize browser access. The website
route is `/browse/<ip-address-or-domain>/...`; the older root-host form is not
part of the contract.

## End-to-End User Journey

1. I initialize a repository and run `codegrapher daemon start` there. The
   command returns only after the watcher is ready and startup reconciliation
   succeeded, and it tells me the PID, log path, watched worktree, and exact
   `codegrapher daemon stop` teardown command.
2. I close the starting shell and do nothing else to the daemon. The background
   owner stays close to idle while the repository is unchanged.
3. I edit a source file. Without another start or sync command, the daemon
   batches the event and updates the graph through the same reconciler used by
   `codegrapher watch`.
4. I run `codegrapher daemon status` or request JSON status. I see one live
   owner, one watched worktree, pending-path and reconciliation health, and the
   durable log location.
5. I accidentally run `start` again. I get the existing owner and path rather
   than a duplicate daemon. If I try to start a different worktree, the command
   explains that this initial release owns one worktree and tells me to stop or
   restart it.
6. I run `restart`. The old control listener closes, one replacement owner
   establishes watch coverage, and startup reconciliation completes before the
   command reports ready.
7. I run `stop`. The daemon joins active graph work, releases the index and
   control listener, removes live state, and status reports stopped.

## Tasks

### Task 1: Specify the safety and lifecycle contracts

**Verifies:** automatic-index-freshness#ac:event-storm-falls-back-to-full-reconciliation, automatic-index-freshness#ac:daemon-lifecycle-keeps-an-index-current, automatic-index-freshness#ac:duplicate-daemon-ownership-is-rejected, automatic-index-freshness#ac:daemon-control-rejects-unauthenticated-callers
**Depends-On:** —
**Status:** complete

Record the user journey, readiness definition, singleton/control ownership,
observability, TypeSpec boundary, failure behavior, and exact deferred scope in
the Feature and this Plan.

### Task 2: Add an explicit event-storm fallback

**Verifies:** automatic-index-freshness#ac:event-storm-falls-back-to-full-reconciliation
**Depends-On:** 1
**Status:** complete

Add a configurable accepted-event threshold to the existing debounced watcher.
Once crossed, replace the per-path set with a bounded whole-worktree marker,
advance its generation for later events, call the existing whole-worktree
reconcile path, emit an observable reason, and prove success/failure retry behavior with
deterministic storm and checkout/merge tests. Record fixed-fixture one-file and
burst benchmark baselines plus an idle no-sync assertion. Preserve the existing
fail-closed native-error contract so partial coverage is never called ready.

Baseline on Apple M5 Max/darwin-arm64: bounded synthetic storm admission
`164.1 ns/op`, `168 B/op`, 4 allocations; one real Go-file `SyncFiles` edit
`32.9 ms/op`, about `308 KB/op`, 3,035 allocations. These are directional
development baselines, not portable CI pass/fail thresholds.

### Task 3: Extract the shared repository freshness owner

**Verifies:** automatic-index-freshness#ac:daemon-lifecycle-keeps-an-index-current
**Depends-On:** 2
**Status:** in_progress

Move index open, watcher construction, startup reconciliation, observation
tracking, wait, and joined shutdown into a library owner used by both
`codegrapher watch` and the daemon. Add a generation barrier that captures and
drains every startup-era accepted event before daemon readiness. Preserve the
foreground command's text, error, and cancellation behavior with focused
regression tests, including an edit during blocked startup reconciliation.

### Task 4: Implement the singleton daemon service and private control API

**Verifies:** automatic-index-freshness#ac:daemon-lifecycle-keeps-an-index-current, automatic-index-freshness#ac:duplicate-daemon-ownership-is-rejected, automatic-index-freshness#ac:daemon-control-rejects-unauthenticated-callers
**Depends-On:** 3
**Status:** queued

Implement atomic user-private state, PID/liveness ownership, loopback-only
versioned status/stop endpoints with random bearer authentication, startup
and lifetime OS locks, per-instance nonce transitions, same-path idempotency,
different-path refusal, startup timeout cleanup, graceful cancellation,
authenticated stale-owner recovery, truthful degraded/failed health, and
5 MiB single-generation log rotation. Keep lifecycle coordination and the
control server injectable for unit tests. If authenticated control cannot be
reached, use a nonblocking lifetime-lock probe to distinguish reclaimable dead
state from an unreachable live owner that must be preserved.

### Task 5: Add the daemon CLI lifecycle

**Verifies:** automatic-index-freshness#ac:daemon-lifecycle-keeps-an-index-current, automatic-index-freshness#ac:duplicate-daemon-ownership-is-rejected
**Depends-On:** 4
**Status:** queued

Add `daemon start [path]`, `stop`, `restart [path]`, `status`, and an internal
foreground child entry point. Resolve worktree-local indexes safely, wait for
authenticated readiness, print the named teardown command, support stable JSON
status, and redirect background output to the durable log. Use platform-specific
detachment code: a new session/process group on Unix and a detached process
group on Windows; the authenticated stop endpoint owns graceful termination on
both. Add platform-specific launcher and private-state permission tests, plus
built-binary lifecycle jobs on supported Unix and Windows CI hosts; keep
cross-compilation as an additional compile guard rather than behavioral proof.

### Task 6: Prove the whole background journey

**Verifies:** automatic-index-freshness#ac:daemon-lifecycle-keeps-an-index-current, automatic-index-freshness#ac:duplicate-daemon-ownership-is-rejected, automatic-index-freshness#ac:daemon-control-rejects-unauthenticated-callers
**Depends-On:** 5
**Status:** queued

Add focused state/control/process tests plus one bounded built-binary E2E over a
real initialized Git repository: start, client exit, edit, graph freshness,
status, two concurrent starters, same-path idempotency, different-path refusal,
missing/invalid control tokens, deliberately blocked startup plus edit, failed
readiness cleanup, reused/unrelated PID state, restart/stop during active
reconciliation, unreachable endpoint with a held lifetime lock, and dead-state
recovery. Add failure → event-during-retry → old-generation-success →
follow-up-success coverage so currency cannot be restored early. Inject
reconciliation and native-watch failures to prove degraded recovery versus
failed exit. Verify the path set remains bounded after storm fallback and no
listener or process survives test cleanup.

### Task 7: Review, land, release, install, and verify

**Verifies:** automatic-index-freshness#ac:event-storm-falls-back-to-full-reconciliation, automatic-index-freshness#ac:daemon-lifecycle-keeps-an-index-current, automatic-index-freshness#ac:duplicate-daemon-ownership-is-rejected, automatic-index-freshness#ac:daemon-control-rejects-unauthenticated-callers, automatic-index-freshness#ac:daemon-failure-state-is-truthful
**Depends-On:** 6
**Status:** queued

Run focused tests and race checks, the repository's required gates once,
SpecScore lint, and an independent adversarial review of the exact diff. Fix or
explicitly decline every finding, land through WB, verify the release contains
the landed revision, upgrade the Homebrew cask, and run the installed binary's
daemon journey before starting the browser-API integration task.

## Deferred AC Coverage

The following foreground-watcher acceptance criteria were already implemented
and verified by [`automatic-index-freshness`](automatic-index-freshness.md).
This daemon plan preserves them through focused regressions in Task 3 but does
not duplicate their original acceptance suite:

- automatic-index-freshness#ac:foreground-watch-reconciles-real-edit — automatic-index-freshness Tasks 3–5
- automatic-index-freshness#ac:burst-is-coalesced — automatic-index-freshness Tasks 2 and 5
- automatic-index-freshness#ac:verbose-reports-event-and-operation-timing — automatic-index-freshness Tasks 2, 3 and 5
- automatic-index-freshness#ac:default-output-is-not-event-level — automatic-index-freshness Tasks 3 and 5
- automatic-index-freshness#ac:failure-remains-dirty-and-visible — automatic-index-freshness Tasks 2, 4 and 5
- automatic-index-freshness#ac:lock-contention-retries-without-clearing — automatic-index-freshness Tasks 2 and 5
- automatic-index-freshness#ac:watch-coverage-is-complete-or-start-fails — automatic-index-freshness Tasks 2 and 5
- automatic-index-freshness#ac:worktree-index-is-local — automatic-index-freshness Tasks 3 and 5
- automatic-index-freshness#ac:populated-directory-move-is-reconciled — automatic-index-freshness Tasks 2, 4 and 5
- automatic-index-freshness#ac:graceful-cancellation — automatic-index-freshness Tasks 3–5

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/plan-specification*

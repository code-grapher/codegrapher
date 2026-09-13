---
format: https://specscore.md/plan-specification
status: Approved
---

# Plan: Post-release hardening

**Status:** Approved
**Source Feature:** automatic-index-freshness
**Date:** 2026-09-13
**Owner:** alex
**Supersedes:** —

## Summary

Harden read-side freshness after the daemon release, close the known `node`
regressions, and reconcile shipped SpecScore lifecycle state against executable
evidence.

## Approach

Keep `RefreshForRead` strict for errors that can invalidate graph/source
identity while allowing warning-only policy skips to converge. Classify
directory candidates before hashing, add focused indexer and CLI regressions,
and stamp the revision only after newly populated descendants converge. Secure
remote API serving is decomposed by the sibling
`secure-remote-browser-api` plan. Shipped lifecycle state is audited separately
against an evidence matrix so this plan does not claim unrelated acceptance.

## End-to-End User Journey

1. An agent asks `codegrapher node` for a real symbol while unrelated repository
   entries have changed. The observable good result is verified symbol source;
   directories and warning-only files do not block it, and populated descendants
   converge before the repository revision is stamped.
2. A genuinely unsafe refresh candidate fails. The observable good result is no
   stale source, an unstamped repository revision, and an error naming the path.
3. A maintainer reviews the shipped Feature evidence. The observable good result
   is lifecycle metadata that matches executable acceptance rather than commit
   presence alone.

## Tasks

### Task 1: Add failing freshness regressions

**Verifies:** automatic-index-freshness#ac:unrelated-nonfatal-candidates-do-not-block-read, automatic-index-freshness#ac:read-command-refuses-foreign-worktree-index
**Status:** planning

Cover directory candidates, oversized SQLite warnings, malformed SpecScore
warnings, and path-bearing fatal diagnostics at the indexer boundary. Add one
bounded CLI integration test that retrieves a real symbol through the combined
candidate set, plus foreign-worktree refusal and exact initialization.

### Task 2: Classify read-refresh candidates

**Verifies:** automatic-index-freshness#ac:unrelated-nonfatal-candidates-do-not-block-read, automatic-index-freshness#ac:read-command-refuses-foreign-worktree-index
**Depends-On:** 1
**Status:** planning

Expand directory candidates through the canonical scanner, remove an exact
stale file record, persist warning-only file outcomes, reject only severe
refresh errors, and preserve revision-stamp and source-verification safety.

### Task 3: Review, verify, release, and close issues

**Verifies:** automatic-index-freshness#ac:unrelated-nonfatal-candidates-do-not-block-read, automatic-index-freshness#ac:read-command-refuses-foreign-worktree-index
**Depends-On:** 1, 2
**Status:** planning

Run focused tests, the repository gate, and an adversarial diff review. Land
through WB, release and install the resulting CodeGrapher version, reproduce the
original issue journeys with the installed binary, and close only issues whose
exact symptoms are proved fixed.

## Deferred AC Coverage

The following acceptance criteria remain covered by the already Implemented
`automatic-index-freshness` and `robust-watcher-local-daemon` plans and are not
changed by this focused hardening plan:

- automatic-index-freshness#ac:foreground-watch-reconciles-real-edit
- automatic-index-freshness#ac:burst-is-coalesced
- automatic-index-freshness#ac:verbose-reports-event-and-operation-timing
- automatic-index-freshness#ac:default-output-is-not-event-level
- automatic-index-freshness#ac:failure-remains-dirty-and-visible
- automatic-index-freshness#ac:lock-contention-retries-without-clearing
- automatic-index-freshness#ac:watch-coverage-is-complete-or-start-fails
- automatic-index-freshness#ac:worktree-index-is-local
- automatic-index-freshness#ac:populated-directory-move-is-reconciled
- automatic-index-freshness#ac:graceful-cancellation
- automatic-index-freshness#ac:event-storm-falls-back-to-full-reconciliation
- automatic-index-freshness#ac:daemon-lifecycle-keeps-an-index-current
- automatic-index-freshness#ac:duplicate-daemon-ownership-is-rejected
- automatic-index-freshness#ac:daemon-control-rejects-unauthenticated-callers
- automatic-index-freshness#ac:daemon-failure-state-is-truthful

## Lifecycle Evidence Audit

The matrix below is the status basis; each named test carries the corresponding
`specscore:verifies` directive and is run by `go test ./...`.

| Feature | Acceptance criteria | Executable evidence |
|---|---|---|
| Automatic index freshness | foreground edit; worktree locality; populated directory move; graceful cancellation | `TestWatchCommandReconcilesRealFilesystemEditAndCancels`, `TestWatchCommandRejectsNearestIndexFromDifferentGitWorktree` |
| Automatic index freshness | burst coalescing; verbose/default output; failure retry; lock contention | `TestObservationsDescribeCoalescedOperation`, `TestWatchOutputDefaultSuppressesRawEventsAndNoOpCompletion`, `TestWatcherRetriesAfterRealIndexerFailure`, `TestLockUnavailableReschedules` |
| Automatic index freshness | complete coverage; event-storm fallback | `TestStartFailsWhenDirectoryWatchCapWouldLeavePartialCoverage`, `TestEventStormFallsBackToBoundedWholeTreeMarkerAndRetries` |
| Automatic index freshness | daemon currency; duplicate ownership; authentication; truthful failure | `TestBuiltBinaryDaemonLifecycle`, `TestControlAPIRequiresTokenAndNonce`, `TestStatusStaysStaleUntilLatestAcceptedGenerationSucceeds`, `TestWaitStoppedReturnsFailureAndHonorsDeadline` |
| Automatic index freshness | unrelated nonfatal read candidates | `TestSyncFilesDirectoryCandidateReplacesStaleFileAndIndexesDescendants`, `TestRefreshForReadAcceptsWarningOnlyCandidates`, `TestNodeCommandReturnsSourceWithUnrelatedNonfatalCandidates` |
| Automatic index freshness | foreign-worktree read refusal and exact initialization | `TestNodeRefusesForeignWorktreeIndexAndInitCreatesLocalIndex` |
| Automatic index freshness | worktree-local read and initialization | `TestNodeRefusesForeignWorktreeIndexAndInitCreatesLocalIndex` |
| Sync initialization on demand | first update initializes/increments; default does not create policy | `TestSyncInitInitializesThenUsesIncrementalReconciliation`, `TestSyncWithoutInitStillRefusesUninitializedRepository` |
| SpecScore Source Traceability | canonical target; sync refresh; non-test rejection | `TestBuildAttachesTypedLinksAndRejectsNonExecutableVerification`, `TestSyncRefreshesCanonicalSpecScoreTrace` |

No acceptance criterion in these three Features remains without executable
evidence. Their lifecycle statuses can therefore be reconciled after the final
green suite, not merely from commit presence.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/plan-specification*

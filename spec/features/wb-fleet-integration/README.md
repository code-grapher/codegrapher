---
format: https://specscore.md/feature-specification
status: Draft
---

# Feature: WB Fleet Integration

> [SpecScore.**Studio**](https://specscore.studio): | [Explore](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/wb-fleet-integration?op=explore) | [Edit](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/wb-fleet-integration?op=edit) | [Ask question](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/wb-fleet-integration?op=ask) | [Request change](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/wb-fleet-integration?op=request-change) |
**Status:** Draft
**Source Ideas:** —

## Summary

Fleet-safe CodeGrapher behavior for WB-managed repositories and worktrees.

## Problem

WB manages one canonical checkout and many linked worktrees for the same
repository. A graph built in one checkout must be demonstrably fresh for the
exact commit being queried, must not confuse worktree-local edits with the
canonical base, and must give automation stable output and links. Test-impact
results also need measured confidence: same-package Go tests have no import
edge and therefore cannot be omitted silently.

## Behavior

CodeGrapher records the indexed base commit SHA and refuses or marks results
stale when the queried checkout does not match it. The target design is a
shared canonical-base graph plus a worktree-local delta overlay: reads combine
the immutable base at its exact SHA with only that worktree's changed, added,
and removed files. A worktree never writes its delta into another worktree or
the canonical base. Overlay creation, invalidation, and garbage collection are
separate follow-up work; this feature defines the boundary only.

Machine-readable command output uses `--format=json`; `--json` remains a
compatibility shortcut. Explorer results expose stable deep links that identify
the repository, exact base SHA, worktree delta identity when present, and the
selected symbol or file. The `affected` command includes co-located Go test
files and reports confidence evidence so WB can decide whether to run a narrow
test set or a broader gate.

Initialization keeps its local `.codegraph/` data directory out of Git status
by adding `.codegraph/` to the Git exclude file resolved by
`git rev-parse --git-path info/exclude`. It preserves existing user entries,
does not edit tracked `.gitignore`, and applies the same way in linked
worktrees.

## Acceptance Criteria

- Given a queried worktree, when its base SHA differs from the graph's recorded
  base SHA, then status exposes the mismatch and automation cannot treat the
  result as fresh.
- Given several worktrees from one canonical base, when an overlay is later
  implemented, then a query observes its own delta plus the shared exact-SHA
  base and no other worktree's edits.
- Given a changed Go production file with a same-package `_test.go` file, when
  `affected` runs, then that test appears; confidence measurement records the
  graph-derived and package-co-location candidates separately.
- Given an automation command with JSON output, when it passes `--format=json`,
  then it receives the documented JSON payload; `--json` produces the same
  payload.
- Given an explorer result, when it is shared as a deep link, then it resolves
  the exact repository revision and graph context.
- Given a Git worktree with user-maintained exclude entries, when CodeGrapher
  initializes, then `.codegraph/` is ignored without modifying tracked
  `.gitignore`, user entries remain intact, and repeated initialization does
  not duplicate the exclude entry.

## Open Questions

1. Should a stale base SHA be a hard command failure for all query verbs or a
   JSON freshness field that WB policy interprets?
2. What retention policy should garbage-collect worktree delta overlays after
   a merge, abandonment, or base reindex?

---
*This document follows the https://specscore.md/feature-specification*

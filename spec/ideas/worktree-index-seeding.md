---
format: https://specscore.md/idea-specification
status: Draft
---

# Idea: Worktree index seeding

**Status:** Draft
**Date:** 2026-11-21
**Owner:** alex
**Promotes To:** —
**Supersedes:** —
**Related Ideas:** extends:automatic-index-freshness

## Problem Statement

How might every agent and reviewer starting work in a fresh Git worktree get a current CodeGrapher index of that exact tree in about a second, without a manual init and without writing to the canonical clone?

## Context

Tracked as [code-grapher/codegrapher#67](https://github.com/code-grapher/codegrapher/issues/67);
related to [#40](https://github.com/code-grapher/codegrapher/issues/40)
(`status` and `sync` silently reuse the canonical index inside a nested
worktree and call it current).

The founder (2026-11-21) wants every agent and reviewer to have CodeGrapher
indexed on the exact code they work on: CodeGrapher should understand
worktrees, use the canonical clone's index, increment on top of it, and copy
it automatically when needed.

Measured on sneat-co/sneat-go (1,090 files, 16 changed vs main, Apple
Silicon, CodeGrapher 0.14.1): a fresh `init` in a new worktree takes 6.35 s;
a plain copy of the 58 MB canonical `.codegraph` takes 0.06 s, an APFS
copy-on-write clone about 0 s, and the incremental `index` on the seeded copy
1.25 s. The seeded index answered `callers`, `impact` and `query` identically
to a fresh one; the only difference was that the copied `.codegraph/.gitignore`
was indexed as a file.

Manual seeding is blocked in the fleet: the wb agent guard reads
`cp -cR <canonical>/.codegraph .` as a write into the canonical clone and
refuses it. The feature therefore has to live in CodeGrapher.

## Recommended Direction

On first use in a linked Git worktree that has no index of its own, resolve
the canonical clone with `git rev-parse --git-common-dir`, take a consistent
copy of its index and catch it up with the normal incremental refresh.

- **Consistent, copy-on-write copy.** Each scope database is opened read-only
  (`mode=ro`). For the default rollback journal, a read transaction holds
  SQLite's SHARED lock — no writer can commit — while the file is cloned
  (APFS `clonefile`, `FICLONE` reflink on btrfs/XFS), falling back to a plain
  copy. A WAL-mode file or one with a journal beside it goes through
  `VACUUM INTO`. Seeding waits (bounded, stat-only) for a live canonical
  writer so the scope databases agree with one another.
- **Identity stamp.** The copy records `seeded_from` and `worktree_root`; the
  path-based worktree mismatch guard accepts it because it now lives in the
  worktree, and still refuses an index borrowed from another working tree.
- **Base commit fast path.** Full builds and refreshes record the commit the
  index was built from (`indexed_git_head`) and whether that tree was dirty
  (`indexed_git_dirty`, dirty if dirty before or after the build). A copy of a
  clean, settled base keeps the revision, so the first refresh diffs
  `<base>..HEAD` plus working-tree changes; an unknown, dirty or mid-write
  base is dropped and the refresh hashes every file instead.
- **No canonical refresh in the seed path** (founder, 2026-11-21). Seed from
  whatever is there; the worktree's own incremental pass is correct whatever
  the base looks like. Refreshing the canonical index first would be a write
  to shared state from inside a task (the wb guard forbids it), concurrent
  seeds would race on it, and it would add latency to every seed for about
  the same total re-index work.
- **Fallbacks.** No canonical index → full init. Seed or catch-up failure →
  discard the copy and full init. `init` itself seeds in a worktree and keeps
  its full build as the fallback.
- **Data directory never indexed.** `.codegraph/` paths are excluded from the
  scan, the git candidate lists, sync admission and the watcher, whatever Git
  reports.
- **Opt-out.** `--no-worktree-seed` (global flag) or
  `CODEGRAPHER_WORKTREE_SEED=0`.
- **#40.** `status` compares the requested path, not the resolved index root,
  so a borrowing worktree is reported as a mismatch and never called current;
  `status --refresh`, `sync` and `index` refuse to write another working
  tree's index.

## Alternatives Considered

- **Shared read-only base plus a per-worktree overlay** — rejected: every
  query would merge two databases, while a copy-on-write clone costs near
  zero.
- **Refresh the canonical index before seeding** — rejected for the reasons
  under Recommended Direction.
- **Manual `cp -c` by agents or wb** — blocked by the wb guard and duplicated
  in every harness; CodeGrapher owns its index layout.

## MVP Scope

Automatic seeding for every command that resolves an index (queries, `sync`,
`index`, `serve`, `daemon`, `watch`, `init`), base-commit and dirtiness
metadata, copy-on-write on macOS and Linux with a plain-copy fallback, the
opt-out, the data-directory exclusion, the #40 status/sync guard, tests
through seams, and one real check on sneat-co/sneat-go.

## Not Doing (and Why)

- Refreshing the canonical index in the seed path — a write to shared state from inside a task; see Recommended Direction
- Keeping the canonical index fresh outside the seed path (on wb's canonical fast-forward, or via `codegrapher daemon` in the canonical clone) — follow-up, not this slice
- Seeding in `status`, `uninit`, `unlock` or `import` — status is read-only by contract and the others must not create an index
- Bare repositories — there is no canonical checkout to seed from

## Key Assumptions to Validate

| Tier | Assumption | How to validate |
|------|------------|-----------------|
| Must-be-true | A seeded and caught-up index answers exactly like a fresh one. | Compare node, edge and file signatures in tests; compare `callers`, `impact` and `query` on sneat-go. |
| Must-be-true | Seeding never writes to the canonical clone. | Fingerprint the canonical tree before and after in tests; opens are `mode=ro`. |
| Should-be-true | Holding a SHARED lock while cloning yields a consistent copy under the rollback journal. | SQLite locking semantics; writers need EXCLUSIVE to change the file. |
| Might-be-true | The git-diff fast path is worth the extra `git status` at stamp time. | Time the first query in a seeded worktree with and without a recorded clean base. |

## SpecScore Integration

- **New Features this would create:** `worktree-index-seeding`
- **Existing Features affected:** `automatic-index-freshness` (worktree reuse)
- **Dependencies:** `indexer.Indexer.RefreshForRead`, scoped SQLite stores,
  the cross-process index lock, the worktree mismatch guard.

## Open Questions

- Should the follow-up keep the canonical index fresh from wb's canonical fast-forward, or from a `codegrapher daemon` in the canonical clone?

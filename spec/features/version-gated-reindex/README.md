---
format: https://specscore.md/feature-specification
status: Draft
---

# Feature: Version-gated reindex

> [SpecScore.**Studio**](https://specscore.studio): | [Explore](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/version-gated-reindex?op=explore) | [Edit](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/version-gated-reindex?op=edit) | [Ask question](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/version-gated-reindex?op=ask) | [Request change](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/version-gated-reindex?op=request-change) |

**Status:** Draft
**Source Ideas:** —

## Summary

Gate codegraph sync on the scanner version stored in the index: same version performs an additive sync, a changed or missing version escalates to a full reindex.

## Problem

The CLI already embeds two version identifiers — the scanner release
(`indexer.PackageVersion`) and the extraction format (`indexer.ExtractionVersion`)
— and stamps both into index metadata on every index (`indexed_with_version`,
`indexed_with_extraction_version`). It already supports both an additive update
(`codegraph sync` → `Indexer.Sync`) and a full rebuild (`codegraph index --force`
→ `Store().Clear()` + full index).

What is missing is the decision that ties them together. `Sync` performs
incremental work without checking the stored version. When a newer scanner runs
`sync` against an index built by an older version, it merges new-format data into
stale-format data, producing a silently inconsistent graph. The additive-vs-full
choice is left entirely to the caller, with no safety net.

Separately, the `ReindexRecommended` status flag is hardcoded `false`
(`query.Status`), so neither the server nor the viewer can distinguish a
stale-version index from a current one.

## Behavior

On `Indexer.Sync`, before performing any incremental work, read
`indexed_with_version` and `indexed_with_extraction_version` from the primary
scope store via `Store.GetMetadata`:

- **Match** — stored scanner version equals `PackageVersion` **and** stored
  extraction version equals `ExtractionVersion` → perform the existing additive
  sync unchanged.
- **Mismatch or missing** — either stored value differs from its current
  constant, or either is absent (an index built before this feature) → escalate
  to a full reindex: clear every scope store, re-index from scratch, and re-stamp
  both metadata values. The result is reported via a new `FullReindex bool` field
  on `SyncResult`.

The escalation runs inside the lock `Sync` already holds, so it must call a
lock-free internal index core rather than the public `IndexAll` (which re-acquires
`idx.mu` and the cross-process lock) to avoid deadlock.

`query.Status` sets `ReindexRecommended = true` when the stored scanner or
extraction version differs from the current constant, otherwise `false`. The
parity-faked `BuiltWithVersion` / `BuiltWithExtractionVersion` /
`CurrentExtractionVersion` fields are left exactly as they are, so the checked-in
`testdata/golden/*/status.json` parity goldens stay unchanged.

## Acceptance Criteria

- **AC-1 (additive on match):** Given an index built by the current scanner,
  When `codegraph sync` runs after some source files change, Then only the
  changed files are re-extracted and `SyncResult.FullReindex` is `false`.
- **AC-2 (escalate on scanner-version change):** Given an index whose stored
  `indexed_with_version` differs from the current `PackageVersion`, When
  `codegraph sync` runs, Then the index is fully cleared and rebuilt,
  `SyncResult.FullReindex` is `true`, and the stored version metadata is updated
  to the current values.
- **AC-3 (escalate on extraction-version change):** Given an index whose stored
  `indexed_with_extraction_version` differs from the current `ExtractionVersion`,
  When `codegraph sync` runs, Then a full reindex is performed (as in AC-2).
- **AC-4 (escalate on missing metadata):** Given an index with no stored version
  metadata (built before this feature), When `codegraph sync` runs, Then a full
  reindex is performed.
- **AC-5 (status flag):** Given a stored version that matches the current scanner,
  When status is queried, Then `ReindexRecommended` is `false`; Given any
  mismatch, Then `ReindexRecommended` is `true`.
- **AC-6 (parity preserved):** Given a freshly indexed fixture, When the status
  parity goldens are regenerated, Then `testdata/golden/*/status.json` is
  unchanged.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/feature-specification*

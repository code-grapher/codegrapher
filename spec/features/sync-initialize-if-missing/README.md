---
format: https://specscore.md/feature-specification
status: Stable
---

# Feature: Sync initialization on demand

> [SpecScore.**Studio**](https://specscore.studio): | [Explore](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/sync-initialize-if-missing?op=explore) | [Edit](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/sync-initialize-if-missing?op=edit) | [Ask question](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/sync-initialize-if-missing?op=ask) | [Request change](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/sync-initialize-if-missing?op=request-change) |
**Status:** Stable
**Source Ideas:** —

## Summary

Let automation explicitly initialize an unindexed repository through the normal sync command, then use incremental reconciliation thereafter.

## Problem

Repository-update automation can call `codegrapher sync` only after somebody
has initialized that checkout. A newly cloned repository therefore turns the
first automatic refresh into a warning and remains unindexed. Making WB inspect
`.codegraph` or run a wrapper would duplicate CodeGrapher's initialization
policy in a consumer.

## Behavior

### REQ: explicit-initialize-if-missing

`codegrapher sync [path] --init` MUST test initialization through
CodeGrapher's existing index identity rules. When the target is uninitialized,
it MUST run the same full initialization and initial indexing used by
`codegrapher init`, then return that result without performing a redundant
incremental scan. Without `--init`, the existing uninitialized-path refusal
MUST remain unchanged.

### REQ: initialized-path-remains-incremental

When the target is already initialized, `--init` MUST NOT rebuild or recreate
the graph. The command MUST use the ordinary incremental `Indexer.Sync` path.

### REQ: automation-output-and-failure

`--quiet` MUST suppress normal initialization and incremental-sync output.
Initialization, open, or indexing failure MUST return a non-zero result and
MUST NOT claim the graph is current. An incremental sync that cannot acquire
the writer lock, or that reports any non-recoverable file update error, MUST
also return non-zero in quiet and interactive modes.

## Acceptance Criteria

### AC: first-update-initializes-then-next-update-syncs

**Requirements:** sync-initialize-if-missing#req:explicit-initialize-if-missing,
sync-initialize-if-missing#req:initialized-path-remains-incremental,
sync-initialize-if-missing#req:automation-output-and-failure

**Given** an uninitialized repository
**When** automation runs `codegrapher sync --init --quiet .`
**Then** CodeGrapher creates a usable initial index without output
**And when** a source file changes and the same command runs again
**Then** the ordinary incremental reconciler consumes the change and leaves no
pending changed file.

### AC: incomplete-incremental-sync-fails

**Requirements:** sync-initialize-if-missing#req:automation-output-and-failure

**Given** an initialized repository
**When** incremental reconciliation cannot acquire its writer lock or reports a
non-recoverable file update error
**Then** `codegrapher sync`, including `--quiet`, returns non-zero and does not
print an up-to-date or successful-sync result.

### AC: default-sync-does-not-create-policy

**Requirements:** sync-initialize-if-missing#req:explicit-initialize-if-missing

**Given** an uninitialized repository
**When** `codegrapher sync .` runs without `--init`
**Then** it refuses with the existing initialization instruction and creates no
CodeGraph directory.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/feature-specification*

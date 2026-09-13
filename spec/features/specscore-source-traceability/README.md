---
format: https://specscore.md/feature-specification
status: Stable
---

# Feature: SpecScore Source Traceability

> [SpecScore.**Studio**](https://specscore.studio): | [Explore](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/specscore-source-traceability?op=explore) | [Edit](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/specscore-source-traceability?op=edit) | [Ask question](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/specscore-source-traceability?op=ask) | [Request change](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/specscore-source-traceability?op=request-change) |
**Status:** Stable
**Source Ideas:** —

## Summary

Connect accepted SpecScore source directives to canonical Feature, REQ, AC, and scenario nodes.

## Problem

SpecScore source directives use canonical resource paths such as
`spec/features/checkout#req:totals`, while SpecScore's provider records use a
feature-local ID such as `checkout#req:totals`. If CodeGrapher stores those as
different identities, the same live REQ or AC appears twice: one indexed node
with no source links and one unresolved node carrying the links. Queries then
look successful while silently losing the specification target's title,
location, and status.

## Behavior

### Canonical target identity

#### REQ: canonical-feature-target-identity

CodeGrapher MUST normalize Feature, REQ, AC, and scenario provider records to
the same `spec/features/<feature-id>` identity produced by the shared SpecScore
source-reference parser. `feature/<id>`, `spec/features/<id>`, short provider
IDs, and canonical SpecScore URLs MUST converge on one target node. A live
target MUST NOT be represented by a second `unresolved` node.

### Incremental projection refresh

#### REQ: sync-refreshes-specscore-trace

`codegrapher sync` and explicit changed-file sync MUST rebuild the cross-scope
trace projection after a SpecScore artifact or source directive changes. The
projection MUST attach accepted `implements`, `verifies`, and `references`
edges to the canonical live target at the current working-tree content.

### Verification boundary

#### REQ: executable-test-verification

A `verifies` directive counts as accepted verification only when CodeGrapher
attaches it to an executable test symbol supported by that language provider.
Coverage data MAY enrich an accepted test link, but line coverage alone MUST
NOT create or accept a verification relationship.

## Acceptance Criteria

### AC: canonical-directive-target-resolves

**Requirements:** specscore-source-traceability#req:canonical-feature-target-identity

**Given** an indexed Feature containing a REQ and an AC and source directives
using canonical `spec/features/...#req:` and `#ac:` targets

**When** CodeGrapher builds or queries the trace projection

**Then** the accepted edges terminate at the indexed REQ and AC nodes, the
query returns their real path and source ranges, and no duplicate unresolved
target is created.

### AC: sync-refreshes-feature-and-source-links

**Requirements:** specscore-source-traceability#req:sync-refreshes-specscore-trace

**Given** an initialized graph whose working tree gains a new Feature REQ or AC
and a source directive for it

**When** `codegrapher sync` completes

**Then** a trace query returns the new live target and its accepted source
links from the same working-tree state without requiring a full reindex.

### AC: non-test-verification-is-not-accepted

**Requirements:** specscore-source-traceability#req:executable-test-verification

**Given** identical `verifies` directives above an executable test and a normal
helper symbol

**When** CodeGrapher builds the trace projection

**Then** only the executable test link is accepted, and coverage presence or
absence does not turn the helper into verification evidence.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/feature-specification*

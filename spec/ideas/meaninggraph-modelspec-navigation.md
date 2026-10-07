---
format: https://specscore.md/idea-specification
status: Approved
---

# Idea: MeaningGraph and ModelSpec indexing and navigation

**Status:** Approved
**Date:** 2026-10-07
**Owner:** alex
**Promotes To:** —
**Supersedes:** —
**Related Ideas:** —

## Problem Statement

How might we let developers and agents navigate from domain meaning through model structure to explicit implementation evidence?

## Context

Approved by the user on 2026-10-07: full CodeGrapher support plus MeaningGraph and ModelSpec website sections and metadata viewer links.

## Recommended Direction

Reuse the MeaningGraph and ModelSpec Go libraries, project typed semantic nodes and declared relationships into the existing CodeGrapher index, expose them in CLI/API/viewer, and publish pinned viewer links from both registries.

## Alternatives Considered

- File-only browsing is useful but cannot navigate concepts or bindings.
- A second semantic database would duplicate freshness and graph APIs.
- Name-based joins are ambiguous; only declared bindings and explicit source mappings become accepted edges.

## MVP Scope

End-to-end typed declaration indexing, semantic search, bidirectional bindings, model relationships, source navigation, explicit code mappings, diagnostics and incremental refresh, plus both websites.

## Not Doing (and Why)

- Automatic name-based implementation claims — mappings must be explicit
- Runtime completeness proofs — static relationships do not prove runtime impact

## Key Assumptions to Validate

| Tier | Assumption | How to validate |
|------|------------|-----------------|
| Must-be-true | Shared parsers preserve module and graph boundaries | Integration fixtures spanning modules, duplicate names, and HCL/JSON twins |
| Should-be-true | Existing graph transport can carry semantic nodes and metadata | Export/import and browser fixture tests |
| Might-be-true | Registry entries are indexed on the public provider | Real public viewer checks after deployment |


## SpecScore Integration

- **New Features this would create:** Typed MeaningGraph and ModelSpec navigation.
- **Existing Features affected:** Index freshness, repository browser, source traceability.
- **Dependencies:** Released MeaningGraph and ModelSpec parsing libraries, provider/viewer transport, registry website generators.

## Open Questions

None at this time.

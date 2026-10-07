---
format: https://specscore.md/plan-specification
status: Executing
---

# Plan: Deliver MeaningGraph and ModelSpec navigation and website links

**Status:** Executing
**Source:** idea:meaninggraph-modelspec-navigation
**Date:** 2026-10-07
**Owner:** alex
**Supersedes:** —

## Summary

Deliver typed MeaningGraph and ModelSpec indexing, semantic search, bidirectional binding and source navigation, explicit code mappings, and support sections with pinned viewer links on meaninggraph.io and modelspec.org, plus a CodeGrapher homepage support section linking both projects. The user authorized implementation on 2026-10-07.

## Approach

Use released parser libraries and the existing derived index, sync engine, API and snapshot transport. Keep semantic kinds distinct from code kinds. Set graph repository identity explicitly from verified repository metadata or configured graph roots; preserve separate directory scopes when an address is unavailable. Use ModelSpec Load module/group/twin rules rather than reconstructing grouping. Scope identities by graph/module rather than filenames, preserve HCL/JSON representations without duplicate semantic objects, and retain dependency revisions and unresolved diagnostics. External concept identities include the requested pin; a locally supplied dependency resolves only when its checkout revision/content is verified, otherwise expose an unresolved target. Add searchable semantic text without dropping metadata, and carry node/edge metadata through backward-compatible snapshot decoding. Bindings use the declared role. Code relationships require explicit source annotations, never matching names. The primary Codex agent owns landing all five repositories; implementation lanes use isolated worktrees on the local host. No new daemon or port is required for indexing. Website claims are published only when the viewer journey succeeds.

## User Journey

1. A developer indexes a repository containing ModelSpec and MeaningGraph declarations. Observable result: typed declarations, metadata and diagnostics are available through CLI/API.
2. They search a concept by label or synonym. Observable result: results identify kind, graph/module and source, with ambiguous identities preserved.
3. They follow its binding to a model property and back. Observable result: the actual declaration, role, type and source location remain visible.
4. They follow an explicit mapping into code and inspect related callers/tests. Observable result: evidence is distinguished from inferred candidates; no mapping means no accepted implementation link.
5. They edit, move or remove a declaration. Observable result after sync: dependent relationships refresh and stale nodes/edges disappear; incremental and full indexes agree.
6. They open a graph/model metadata card on either public website and click CodeGrapher. Observable result: the viewer opens the matching repository and pinned revision, and source links select the relevant file/line. Browser refresh retains the location. The provider publishes and resolves immutable commit snapshots independently of the default branch; a missing or mismatched snapshot is shown explicitly instead of falling back. Viewer routes use /github.com/<org>/<repo>/<file>?branch=<commit>#line=<line>, with segment encoding and existing line selection retained.

7. They visit CodeGrapher’s homepage and discover MeaningGraph and ModelSpec support. Observable result: the support section explains concept, binding, model/member and explicit code navigation, and links to both project websites.

## Acceptance Criteria

- Typed extraction covers meaningful declarations and members in both ModelSpec serializations and MeaningGraph YAML, including labels/synonyms, values, units and measures.
- Relations include containment, model references/composition, MeaningGraph inheritance/value/unit/measure relationships, binding roles, and explicit code mappings.
- Duplicate names across modules/graphs never merge. HCL/JSON twins are deduplicated with representation provenance and drift diagnostics.
- Invalid declarations and missing dependencies remain visible as diagnostics; externally pinned references retain identity and requested revision without silently using the local default branch. No implicit network fetch is added to local indexing.
- CLI/API/export/import and viewer preserve semantic kinds and metadata. Search supports labels and synonyms. Node/source navigation remains bounded and truthful about freshness.
- Incremental refresh handles changed targets, unchanged referrers, deletion and file moves.
- CodeGrapher’s homepage states MeaningGraph and ModelSpec support and links to meaninggraph.io and modelspec.org.
- Both registry websites have a CodeGrapher support section and viewer actions on registered metadata cards/pages, with escaped segment-encoded URLs and matching pinned revisions. Illustrative catalogue items have no fabricated source mappings.
- One real fixture exercises search, binding navigation, source/code mapping, edit/sync and website-to-viewer navigation. MeaningGraph cross-repository model binding restrictions remain enforced.

## Tasks

### Task 1: Index semantic declarations and relationships

**Verifies:** idea:meaninggraph-modelspec-navigation
**Status:** complete

Implement parser reuse, detection, typed nodes, scoped identities, representation deduplication, diagnostics, semantic search metadata, relationships and explicit code mapping syntax. Integrate full and incremental indexing and export/API transport. Verify with fixtures and incremental/full equivalence.

### Task 2: Render and navigate semantic objects

**Verifies:** idea:meaninggraph-modelspec-navigation
**Status:** complete

Expose Code, Models and Meaning views/search filters and object details with model constraints, concept metadata, binding roles, evidence, source actions and focused relationship navigation. Verify refreshable pinned routes and supported transport fixtures.

### Task 3: Make public provider indexing revision-aware

**Verifies:** idea:meaninggraph-modelspec-navigation
**Status:** in_progress

Extend code-grapher/server immutable-commit indexing and status/manifest lookup to retain and serve the requested snapshot; default-branch jobs must not overwrite a pinned graph. Viewer passes commit identity and checks returned snapshot commit. Verify coexistence of default and two pinned revisions, concurrent jobs, status mismatch/missing states, and real website pins. Update the server CodeGrapher dependency to the released implementation without local replacements.

### Task 4: Add reciprocal website support and pinned viewer actions

**Verifies:** idea:meaninggraph-modelspec-navigation
**Status:** in_progress

Update MeaningGraph and ModelSpec homepages and registered graph/model/concept/source metadata surfaces. Add a CodeGrapher homepage support section describing the semantic navigation and linking both project websites. Use the existing CodeGrapher route grammar, encode each path segment, retain pinned commits and lines where known, preserve accessibility and do not invent links for illustrative items. Verify generated pages and destination behavior.

### Task 5: Review, land and verify production journey

**Verifies:** idea:meaninggraph-modelspec-navigation
**Status:** in_progress

Run independent adversarial review, resolve every finding, run appropriate repository checks, land through WB, verify exact remote target and cleanup, verify release/provider/viewer deployment and both website deployments, index real public example repositories, and exercise the end-to-end browser journey. Record any externally blocked step without claiming production support.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/plan-specification*

## Independent Review

Review r1 identified one blocker and four major findings. All are accepted and addressed in this plan: immutable provider snapshots and mismatch states (Task 3); explicit graph identity and shared module/twin grouping (Task 1); search and metadata transport (Tasks 1–2); declared encoded revision/line routes (Tasks 2–4); and verified pin-qualified external resolution (Task 1). The line-selection finding was withdrawn after checking the existing hash-to-line scroll path. Review r2: blockers=0 majors=0 minors=0; approved for implementation. No findings declined. Independent report: [_reviews/README.md](_reviews/README.md).

Implementation reviews and pre-deployment verification: [_implementation_reviews/README.md](_implementation_reviews/README.md).

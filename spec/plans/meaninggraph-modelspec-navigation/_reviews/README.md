# Independent adversarial review: MeaningGraph and ModelSpec navigation

Reviewed 2026-10-07, read-only against the idea and plan in the CodeGrapher worktree and the pinned local source checkouts. This report covers the user's full intent: typed CodeGrapher indexing and navigation plus support sections and viewer links on both websites. No implementation or deployment was reviewed.

## Round 1 findings and round 2 disposition

| Initial rank | Finding | Disposition in revised plan |
| --- | --- | --- |
| Blocker | Public pinned viewer links could select the provider's default-branch graph. The viewer currently gets its manifest from ref-agnostic status (`codegrapher-dev/libs/codegrapher/ui/src/lib/data/db-server-graph-data-source.ts:110-128`), while server explicit-commit indexing currently writes to the default-branch slot (`server/internal/handler/handler.go:214-247`). | Addressed for planning. Plan lines 29 and 58-63 explicitly require immutable per-commit snapshots, coexistence with default and two pins, revision-aware lookup, returned-commit checks, and missing/mismatch states. Existing scoped `/graph/{repo}/{ref}/...` transport (`server/internal/handler/graph_handler.go:41-48,70-78`) is a usable seam. This remains an implementation and live-verification gate, not a plan blocker. |
| Major | Graph/module identity and HCL/JSON grouping were underspecified. MeaningGraph loaders do not set `Graph.Address` (`meaninggraph/cli/pkg/meaning/graph.go:17-23`); ModelSpec has assignment, layout, standalone, and twin rules (`modelspec/cli/pkg/modelspec/load.go:567-579,607-645`). | Addressed. Plan line 20 requires verified graph identity or separate directory scope and reuse of ModelSpec `Load` grouping/twin rules; lines 35 and 49 require duplicate/twin verification. |
| Major | Labels, synonyms, and roles would be lost or unsearchable through existing transport. Backend FTS indexes ordinary symbol fields (`codegrapher/store/search.go:52-85`); viewer search checks names (`codegrapher-dev/libs/codegrapher/ui/src/lib/viewer/search.ts:134-155`); snapshot decoder omits node/edge metadata (`codegrapher-dev/libs/codegrapher/ui/src/lib/data/graph.ts:68-95`). | Addressed. Plan lines 20, 37, 49, and 56 explicitly require searchable semantic text, backend/API/export transport, and backward-compatible node/edge metadata decoding. Exact schema and migration remain implementation decisions. |
| Major | Source-line route was presumed absent from `parseRoute`. | Withdrawn as a false positive. `parseRoute` does not parse a hash (`codegrapher-dev/libs/codegrapher/ui/src/lib/ui/ui.ts:1170-1199`), but the file load calls `scrollToHash`, which selects `#line=<n>` and legacy `#L<n>` (`ui.ts:1125-1131,1145-1157`). Plan line 29 now states the existing `?branch=<commit>#line=<line>` grammar and retains line selection. The remaining route work is commit selection and exact snapshot verification. |
| Major | A supplied external MeaningGraph dependency can be accepted under an unverified pin. `GraphResolver` explicitly cannot verify the supplied graph against a pin (`meaninggraph/cli/pkg/meaning/check.go:49-55,60-78`). | Addressed. Plan lines 20 and 36 require pin-qualified identity and verified checkout revision/content, with an unresolved target otherwise; line 49 assigns this to semantic indexing. |

## Round 2 residual review

No new plan blocker or major omission found. The idea's scope (lines 21, 25, 35) and the plan's journey and tasks (lines 24-40, 44-77) cover both requested websites, typed declaration navigation, semantic search, bidirectional bindings, explicit code evidence, incremental refresh, and a live public journey. The plan explicitly makes production copy contingent on a working viewer journey (line 20) and requires deployment and real repository verification (line 77).

Two implementation gates deserve close review, but the revised plan already names them: (1) the server currently retains one graph slot per repo (`server/internal/handler/handler.go:214-247`), so Task 3 must really isolate immutable commits and prevent default jobs from overwriting them; (2) viewer scope discovery currently resolves from the default status manifest (`codegrapher-dev/libs/codegrapher/ui/src/lib/data/graph-store.service.ts:100-120`), so a pinned route must request its own manifest and reject a mismatched snapshot. Cache isolation already includes `branch` (`graph-store.service.ts:62-72`). These are verification targets, not additional findings.

A proposed implementation detail was checked after the plan verdict: shadow copies of explicitly mapped code symbols in a semantic scope using the same node ID would violate the viewer merge assumption that IDs are scope-unique (`codegrapher-dev/libs/codegrapher/ui/src/lib/data/graph.ts:145-169`). The subsequent `nodesById` map overwrites duplicates by load order (`graph.ts:189-194`), and per-scope query fan-out may expose duplicate hits. Existing cross-scope trace links use a separate projection (`codegrapher/store/schema.sql:193-222`). This is an implementation review gate, not an unresolved plan finding: keep one canonical symbol owner or specify deterministic dedup and provenance, then test search/counts/navigation/export/import and incremental removal.

The plan is ready for implementation. It is not evidence that the feature, public provider, or websites are delivered. Claim production support only after Task 5's exact-revision browser checks pass.

VERDICT: blockers=0 majors=0 minors=0 land=yes

## Open Questions

None at this time.

---
format: https://specscore.md/idea-specification
status: Draft
---

# Idea: SpecScore artifact extraction (features, ideas, plans, …) into the codegrapher graph

**Status:** Draft
**Date:** 2026-06-15
**Owner:** trakhimenok
**Promotes To:** —
**Supersedes:** —
**Related Ideas:** —

## Problem Statement

How might we let codegrapher index a SpecScore spec tree so its artifacts (features, ideas, plans, decisions, tasks, scenarios) and their relationships become first-class, queryable nodes in the knowledge graph?

## Context

codegrapher uses a one-extractor-per-format architecture (internal/extract/walk_go.go, walk_sql.go, walk_sqlite.go, walk_gomod.go, walk_packagejson.go). SpecScore artifacts are richly structured and self-identifying: frontmatter 'format: https://specscore.md/<kind>-specification', an H1 ('# Feature:' / '# Idea:'), '**Status:**'/'**Grade:**' metadata, and cross-references (../plan/README.md, Promotes To, Related Ideas, Depends-On, REQ/AC IDs). The SQLite extractor set the precedent for adding domain-native NodeKinds (index/trigger/constraint) alongside a dedicated Language + walk_*.go. SpecScore itself has an open Idea (spec-code-linking-via-codegraph) about spec↔code traceability via a codegraph index, so teaching codegrapher to read SpecScore artifacts is a foundational building block for that. Owner answers (2026-06-15): cover all artifact types in the vision; deep intra-file structure; spec-graph-only for MVP (defer spec↔code binding); serve both MCP and CLI; detect by path (spec/**) confirmed by frontmatter.

## Recommended Direction

Add a native SpecScore extractor following the established walk_*.go pattern: a new Language "specscore", SpecScore-native NodeKinds (feature, idea, plan, requirement, acceptance_criterion, scenario, decision, task), reusing the existing 'contains' edge for intra-file containment and 'references' for cross-file links, plus a small set of new EdgeKinds (promotes_to, supersedes, depends_on) for SpecScore-specific relationships. Files are detected by path (spec/**) and confirmed by the 'format: https://specscore.md/<kind>-specification' frontmatter, so non-SpecScore markdown is never misclassified. Each artifact becomes a node carrying its slug, kind, status, and grade; deep parsing emits child nodes (REQs, ACs, scenarios) with containment edges, and cross-references resolve to edges between artifact nodes. Spec-graph-internal only for now — spec↔code binding (specscore: annotations, Verifies trailers) is explicitly deferred. The graph is then queryable from both the codegraph MCP server and the CLI, giving agents one-call orientation over a spec tree.

## Alternatives Considered

- **Generic frontmatter-markdown extractor** — model any schema'd markdown doc, with SpecScore as one configured schema. Lost: speculative configurability with a single known consumer; violates the project's Simplicity-First rule. The native extractor can be generalized later if a second schema ever appears.
- **Thin metadata-only index (file-level nodes, no intra-file parsing)** — one node per artifact plus cross-file edges, no REQ/AC/scenario children. Lost as the *vision* because it can't answer per-requirement queries, but it is precisely the right MVP slice and is folded into MVP Scope (deferring child-node depth for Plans/Decisions/Tasks).
- **Reuse existing code NodeKinds (map Feature→module, REQ→function)** — avoid adding new kinds. Lost: it overloads code-shaped kinds with spec semantics, making queries ambiguous and corrupting language/kind stats. The SQLite extractor's precedent (native `index`/`trigger`/`constraint` kinds) shows domain-native kinds are the sanctioned pattern.

## MVP Scope

A thin vertical slice proving the model end-to-end on the codegrapher repo's own spec/ tree (and the SpecScore repo as an external fixture): a walk_specscore.go that handles Features and Ideas only, detected by path+frontmatter, emitting Feature/Idea nodes (slug, kind, status, grade) plus deep child nodes for Requirements and Acceptance Criteria with 'contains' edges, and resolving cross-file references (including Promotes To / Related Ideas) to 'references'/'promotes_to' edges. Deterministic self-goldens for the fixtures; the new kinds/edges/language registered in model. No Plans/Decisions/Tasks/Scenarios yet, no spec↔code binding.

## Not Doing (and Why)

- Spec↔code binding (specscore: annotations, Verifies: trailers) — deferred; MVP is spec-graph-internal only
- A generic frontmatter-markdown extractor — speculative flexibility; SpecScore is the only schema asked for
- Plans, Decisions, Tasks, Scenarios in the first slice — proven on Features+Ideas first, then fanned out
- Editing SpecScore artifacts or writing back to them — codegrapher is read-only intelligence
- A new MCP verb or CLI subcommand dedicated to specs — reuse existing query/explore surfaces over the new nodes

## Key Assumptions to Validate

| Tier | Assumption | How to validate |
|------|------------|-----------------|
| Must-be-true | SpecScore artifacts are reliably identifiable without false positives — `format:` frontmatter under `spec/**` is a sufficient discriminator from ordinary repo markdown. | Walk the SpecScore repo and the codegrapher `spec/` tree; confirm every artifact is detected and no non-artifact markdown (README index files, docs/) is misclassified. |
| Must-be-true | The artifact structure is regular enough to parse deterministically (stable H1 `# <Kind>:` form, `**Status:**`/`**Grade:**` lines, predictable REQ/AC headings) across real artifacts, not just the spec examples. | Parse the full SpecScore `spec/features/**` + `spec/ideas/*` corpus; verify slug/kind/status/grade extraction and REQ/AC enumeration match a hand-checked sample. |
| Should-be-true | The existing `contains`/`references` edges plus three new edges (`promotes_to`, `supersedes`, `depends_on`) cover the relationships worth modeling in the MVP. | Inventory cross-reference forms across the corpus (relative links, Promotes To, Related Ideas, Depends-On) and confirm each maps to one of these edges. |
| Should-be-true | Markdown can be parsed with the project's existing toolchain (CGO_ENABLED=0, gotreesitter or a lightweight Go markdown pass) without a new heavy dependency. | Spike the parse path; confirm it builds with CGO disabled and adds no problematic dependency. |
| Might-be-true | Cross-file references resolve cleanly within a single repo without needing cross-repo resolution. | Check whether any MVP-scope references point outside the repo; if rare, defer cross-repo per Not Doing. |


## SpecScore Integration

- **New Features this would create:** a "SpecScore artifact extraction" Feature (detection + node/edge model), likely with sub-features for the model registration (new NodeKinds/EdgeKinds/Language) and the `walk_specscore.go` extractor itself.
- **Existing Features affected:** none directly; the new kinds/edges extend `model` and surface through existing query/MCP/explore paths without changing their contracts.
- **Dependencies:** the codegrapher `model` package (node/edge/language enums) and the `internal/extract` walk-dispatch + golden-parity harness. This Idea is also a foundational building block for SpecScore's own `spec-code-linking-via-codegraph` Idea, though that linkage is out of scope here.

## Open Questions

- Markdown parsing approach under CGO_ENABLED=0: reuse gotreesitter's markdown grammar (consistent with other extractors) vs. a small pure-Go heading/frontmatter pass — decide at design time.
- Whether REQ/AC node identity should use the artifact-local ID from the spec or a derived slug, for stable cross-references.
- How aggressively to fan out beyond Features+Ideas once the MVP slice lands (Plans next, given their Depends-On graph value, vs. Decisions/Tasks/Scenarios).

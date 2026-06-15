---
format: https://specscore.md/idea-specification
status: Specified
---

# Idea: Index all non-gitignored files as file-level nodes (even unknown extensions like *.txt), not just recognized source languages

**Status:** Specified
**Date:** 2026-06-15
**Owner:** specstudio:implement
**Promotes To:** whole-repo-file-nodes
**Supersedes:** —
**Related Ideas:** —

## Problem Statement

How might we make the code graph reflect the entire tracked repository — including files written in languages we don't parse — so that nothing git-visible is invisible and cross-file references have a concrete node to resolve against?

## Context

Today ScanDirectory filters candidates through IsSourceFile (extract.DetectLanguage != LangUnknown), so files with unrecognized extensions get no node at all. Idea: emit a file-level node for every git-visible (non-gitignored) file, regardless of language, so the graph reflects the whole repo. Design questions: graph/DB bloat, binary blobs, size caps, value of bare file nodes, and it would rebaseline every golden.

## Recommended Direction

Decouple "should this file be a node?" from "can we parse this file's language?". Today both decisions are fused in `IsSourceFile` (`indexer/scan.go`), which admits a path only when `extract.DetectLanguage != LangUnknown`. Split them: admit **every** non-gitignored file as a graph candidate, then branch — recognized languages get full symbol extraction (tree-sitter parse) as they do now, while unrecognized files get just a bare file-level node carrying path, size, mtime, content hash, and `Language = unknown`.

This is cheaper than the seed's "graph/DB bloat, binary blobs, size caps" framing implies, because file records already store **no content blob** — only a SHA-256 hash plus metadata (`indexer/store.go`). A bare file node is therefore O(path), not O(filesize). That is what makes the chosen scope — every non-gitignored file, binaries included, no size cap — storage-reasonable: the marginal cost of an extra node is a row, not a blob. The two real costs are (1) hashing I/O (we still read each file's bytes to compute its SHA-256) and (2) re-baselining essentially every self-golden, which we accept under the 2026-06-11 self-golden mandate.

This serves both stated goals at once: whole-repo completeness (config, docs, data, and unknown-extension files all appear) and reference-resolution targets (an `#include`, a doc link, or an import of an unparsed file now resolves to a real node instead of dangling).

## Alternatives Considered

- **Text-only, with a size cap.** Skip binaries and files over N bytes. Lost because the owner chose full completeness, and since nodes are metadata-only (no blob) the bloat rationale for a cap is weak. Retained as the natural fallback if hashing I/O on large binaries proves costly.
- **Just allowlist more extensions into `IsSourceFile`.** Lost because it perpetuates the "node iff parseable" coupling, turns coverage into endless extension whack-a-mole, and still leaves genuinely unknown extensions invisible.
- **Synthesize file nodes lazily, only when something references them.** Lost because the completeness goal wants every file present unconditionally; lazy materialization complicates resolution and incremental sync for no storage win (the node is already cheap).

## MVP Scope

A timeboxed change that splits candidate admission from language detection in `ScanDirectory` (and the parallel predicate on the watch path), emits a bare file-level node with `Language = unknown` for every non-gitignored file we can't parse, and re-baselines the affected self-goldens via the existing scripts. Done when indexing a fixture repo that contains a `.txt` file and a binary yields file nodes for both (zero symbol nodes), goldens are regenerated, and all gates are green (gofmt, vet, CGO_ENABLED=0 build/test, fixture + binary-parity goldens).

## Not Doing (and Why)

- Storing file content/blobs in the DB — only path + hash + metadata, exactly as today.
- Parsing or extracting symbols from unknown-language files — bare file node only.
- Size caps or binary exclusion in the MVP — full completeness was chosen; revisit only if hashing I/O proves costly.
- TypeScript-side changes — Go-first focus; TS work is parked per the 2026-06-11 owner decision.
- New edge types that point at the new file nodes — reference-resolution wiring is follow-on work, not this MVP.

## Key Assumptions to Validate

| Tier | Assumption | How to validate |
|------|------------|-----------------|
| Must-be-true | File records store no content blob (only hash + metadata), so admitting every file — binaries included — adds rows, not megabytes. | Confirmed by reading `indexer/store.go`; further measure DB-size delta when indexing a fixture repo with a large binary. |
| Should-be-true | Hashing every file (reading bytes to compute SHA-256, including big binaries) stays an acceptable indexing cost on real repos. | Time a full index of a repo with large binaries before vs. after the change. |
| Might-be-true | Bare file nodes with no symbols carry real downstream value for reference resolution. | Check whether any current resolver wants to point at unparsed files; defer until resolution wiring is designed. |


## SpecScore Integration

- **New Features this would create:** likely one — "Whole-repo file-node indexing" (Go).
- **Existing Features affected:** indexer scan/sync, the watch-path source-file predicate, and essentially all self-goldens.
- **Dependencies:** none hard; sequence after any in-flight scan/sync work to avoid golden churn collisions.

## Open Questions

- Should the watch-path predicate (`defaultIsSourceFile` / `IsSourceFileFunc`) change in lockstep with `ScanDirectory`, or is scan-only enough for the MVP?
- Do unknown-language file nodes need a distinct node `Kind`, or do we reuse the existing file-node `Kind` with `Language = unknown`?
- Is hashing I/O on large binaries costly enough that we should reintroduce an opt-out (size cap / binary skip) later?

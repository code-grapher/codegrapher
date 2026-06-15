---
format: https://specscore.md/feature-specification
status: Stable
---

# Feature: Whole-repo file-node indexing

> [SpecScore.**Studio**](https://specscore.studio): | [Explore](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/whole-repo-file-nodes?op=explore) | [Edit](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/whole-repo-file-nodes?op=edit) | [Ask question](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/whole-repo-file-nodes?op=ask) | [Request change](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/whole-repo-file-nodes?op=request-change) |
**Status:** Stable
**Source Ideas:** index-all-non-gitignored-files-as-file-level-nodes-even
**Grade:** A

## Summary

Emit a file-level node for every non-gitignored file, not only files in recognized source languages.

## Problem

`ScanDirectory` (`indexer/scan.go`) admits a path as an indexing candidate only
when `IsSourceFile` returns true — i.e. when `extract.DetectLanguage(path)` is not
`LangUnknown`. The watch path applies the same predicate (`defaultIsSourceFile` in
`watch/watcher.go`). As a result, any file with an unrecognized extension (a
`*.txt`, a config file, a binary, a docs file) gets **no node at all** and is
invisible to the graph. Two decisions are fused into one: "should this file be a
node?" and "can we parse this file's language?".

This blocks whole-repo completeness (the graph cannot reflect files we don't
parse) and leaves cross-file references to unparsed files with nowhere to resolve.

Note for implementation: admission today is actually `IsSourceFile(rel) ||
isSpecScoreArtifact(rootDir, rel)` at two call sites (`indexer/scan.go:251` and
`:438`); the broadening to all files must preserve the SpecScore-artifact path and
update both sites (plus the watch predicate) so nothing regresses.

## Behavior

### File-node admission

#### REQ: admit-all-tracked-files

`ScanDirectory`, and the watch-path predicate that mirrors it, MUST admit **every**
non-gitignored file as an indexing candidate, decoupling admission from language
detection. A file whose `DetectLanguage` is recognized continues to receive full
symbol extraction exactly as today; a file whose language is `LangUnknown` is
admitted and receives a bare file-level node (next REQ) instead of being dropped.
`.gitignore` exclusion is unchanged — ignored files are still not candidates.

#### REQ: bare-file-node-shape

For an admitted file whose `DetectLanguage` returns `LangUnknown`, the indexer MUST
emit exactly one file-level node carrying the file's path, size, mtime, and content
hash, with `Language = unknown`. It MUST NOT run a tree-sitter parse for that file
and MUST NOT emit symbol nodes, edges, or unresolved references from it. No file
content blob is stored — this matches the existing `FileRecord` shape
(`indexer/store.go`), so the node is path-and-metadata sized, not content sized.

#### REQ: no-size-or-binary-filter

The MVP MUST NOT apply a size cap or a binary-content exclusion. Every
non-gitignored file — binaries included, regardless of size — receives a file
node. (Reintroducing an opt-out is explicitly deferred; see Not Doing.)

### Regression net

#### REQ: goldens-rebaselined

The behavior change re-baselines essentially every self-golden. Affected goldens
MUST be regenerated via the existing re-baseline scripts (never hand-edited per the
standing rule), and the full gate suite — gofmt, `go vet ./...`,
`CGO_ENABLED=0 go build ./...`, `CGO_ENABLED=0 go test -count=1 ./...` including the
fixture and binary-parity golden tests — MUST pass.

## Acceptance Criteria

### AC: unknown-extension-gets-node (verifies REQ:admit-all-tracked-files, REQ:bare-file-node-shape)

Scenario: file with an unrecognized extension is indexed
Given a repo containing a non-gitignored file with an unrecognized extension (e.g. `notes.txt`)
When the repo is indexed
Then exactly one file-level node exists for that file with `Language = unknown` and zero symbol nodes are emitted for it

### AC: recognized-source-unchanged (verifies REQ:admit-all-tracked-files)

Scenario: recognized source file still gets full extraction
Given a repo containing a recognized source file (e.g. `main.go`)
When the repo is indexed
Then that file still receives full symbol extraction and its symbol nodes are present as before

### AC: gitignored-file-excluded (verifies REQ:admit-all-tracked-files)

Scenario: gitignored file produces no node
Given a non-gitignored repo file and a second file matched by `.gitignore`
When the repo is indexed
Then a file-level node exists for the tracked file and no node of any kind exists for the gitignored file

### AC: binary-file-gets-node (verifies REQ:no-size-or-binary-filter, REQ:bare-file-node-shape)

Scenario: binary file gets a bare node without parsing
Given a non-gitignored binary file (e.g. a small `.png`) of arbitrary size
When the repo is indexed
Then a single file-level node with `Language = unknown` is emitted for it and no tree-sitter parse or symbol extraction is attempted

### AC: gates-green-after-rebaseline (verifies REQ:goldens-rebaselined)

Scenario: gates pass with regenerated goldens
Given the implemented change
When the self-goldens are regenerated via the re-baseline scripts and the gate suite runs
Then gofmt, `go vet ./...`, `CGO_ENABLED=0 go build ./...`, and `CGO_ENABLED=0 go test -count=1 ./...` all pass with regenerated (not hand-edited) goldens

## Not Doing / Out of Scope

- Storing file content/blobs — only path + hash + metadata, as today.
- Symbol extraction or new edge types for unknown-language files — bare node only; reference-resolution wiring to these nodes is follow-on work.
- Size caps or binary exclusion — deferred; revisit only if hashing I/O proves costly.
- TypeScript-side changes — Go-first focus; TS work is parked.

## Rehearse Integration

The four behavioral ACs (`unknown-extension-gets-node`, `recognized-source-unchanged`,
`gitignored-file-excluded`, `binary-file-gets-node`) are testable via Go tests over a
fixture repo and are covered by the implementation's test suite; `gates-green-after-rebaseline`
is verified by the gate suite itself. No separate Rehearse stubs are scaffolded — the
existing Go fixture/golden test harness is the test surface.

## Open Questions

- Should unknown-language file nodes reuse the existing file-node `Kind` (with `Language = unknown`), or carry a distinct `Kind`? Resolved at implementation time toward reuse unless a consumer needs to distinguish them.

---
*This document follows the https://specscore.md/feature-specification*

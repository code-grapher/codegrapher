---
format: https://specscore.md/plan-specification
status: Approved
---
# Plan: Whole Repo File Nodes

**Status:** Approved
**Source Feature:** whole-repo-file-nodes
**Date:** 2026-06-15
**Owner:** alexandertrakhimenok
**Supersedes:** —

## Summary

Make codegrapher index every non-gitignored file as a file-level node, not only
files in recognized source languages. Touches the indexer scan/index path
(`indexer/scan.go`, the per-file index path), the watch predicate
(`watch/watcher.go`), the Go test suite, and the self-goldens.

## Approach

Three linear tasks. First split admission from language detection so every
non-gitignored file becomes an indexing candidate (preserving `.gitignore`
exclusion and the existing `isSpecScoreArtifact` OR-clause at both call sites and
the watch predicate). Then emit a bare file-level node for admitted files whose
language is `LangUnknown` — path/size/mtime/hash, `Language = unknown`, no
tree-sitter parse, no symbol nodes — while leaving the recognized-language path
untouched. Finally regenerate the self-goldens via the re-baseline scripts and
green the full gate suite. Tests are written inside each implementation task (TDD);
the golden rebaseline necessarily comes last because it captures the new behavior.

## Tasks

### Task 1: Decouple admission from language detection (scan + watch)

**Verifies:** whole-repo-file-nodes#ac:gitignored-file-excluded
**Depends-On:** —
**Status:** pending

Change the candidate-admission predicate so `ScanDirectory` admits every
non-gitignored file, decoupled from `DetectLanguage`. Update both call sites
(`indexer/scan.go:251` and `:438`) and the watch predicate
(`defaultIsSourceFile` in `watch/watcher.go`), preserving `.gitignore` exclusion
and the existing `isSpecScoreArtifact` OR-clause. Add a test asserting a
gitignored file still produces no node while a tracked file is admitted.

### Task 2: Emit a bare file-level node for unknown-language files

**Verifies:** whole-repo-file-nodes#ac:unknown-extension-gets-node, whole-repo-file-nodes#ac:binary-file-gets-node, whole-repo-file-nodes#ac:recognized-source-unchanged
**Depends-On:** 1
**Status:** pending

In the per-file index path, when `DetectLanguage` returns `LangUnknown`, emit
exactly one file-level node (path, size, mtime, content hash, `Language = unknown`)
with no tree-sitter parse and no symbol nodes, edges, or unresolved references;
apply no size cap or binary exclusion. Leave the recognized-language extraction
path unchanged. Add tests for an unknown-extension file (one node, zero symbols), a
binary file (one node, no parse), and a recognized source file (symbols still
present).

### Task 3: Regenerate self-goldens and green the gate suite

**Verifies:** whole-repo-file-nodes#ac:gates-green-after-rebaseline
**Depends-On:** 2
**Status:** pending

Regenerate the affected self-goldens via the existing re-baseline scripts (never
hand-edited) so they capture the new whole-repo file nodes, then confirm gofmt,
`go vet ./...`, `CGO_ENABLED=0 go build ./...`, and `CGO_ENABLED=0 go test -count=1 ./...`
all pass, including the fixture and binary-parity golden tests.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/plan-specification*

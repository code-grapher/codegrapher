---
format: https://specscore.md/plan-specification
status: Under Review
---
# Plan: Specscore Artifact Extraction

**Status:** Under Review
**Source:** idea:specscore-artifact-extraction
**Date:** 2026-06-15
**Owner:** alexandertrakhimenok
**Supersedes:** —

## Summary

Implements the MVP slice of the SpecScore artifact extractor: codegrapher learns to index a SpecScore spec tree (Features, Ideas, Plans) into the knowledge graph. Touches `model` (new Language/NodeKinds/EdgeKinds), `go.mod` + a thin adapter (strict reuse of the `specscore-cli` parsers — no porting), the detection layer, `internal/extract` (node emission), `resolve/` (cross-file edges), and the golden-parity test harness. Spec-graph-internal only; no spec↔code binding.

## Approach

Strictly linear, bottom-up: register the vocabulary first (Task 1) so every later layer compiles against it; wire in the `specscore-cli` parser as a dependency and adapter (Task 2) so extraction has structured input; teach detection to recognize artifacts by path+frontmatter (Task 3); emit nodes and intra-file `contains` edges in the extractor (Task 4); resolve cross-file references into edges in the dedicated `resolve/` package (Task 5, kept separate because that is its own architectural layer); lock behavior with deterministic self-goldens and the full gate suite (Task 6). Reuse is strict: if a needed `specscore-cli` parse function is unexported, it is exported upstream in `specscore-cli`, never reimplemented here.

## Tasks

### Task 1: Register the SpecScore vocabulary in `model`

**Source:** idea:specscore-artifact-extraction
**Depends-On:** —
**Status:** pending

Add `Language "specscore"`, the SpecScore-native `NodeKind`s (`feature`, `idea`, `plan`, `requirement`, `acceptance_criterion`, `task`) and `EdgeKind`s (`promotes_to`, `supersedes`, `depends_on`) to `model/model.go`, including their entries in the runtime-iterable `NodeKinds`/edge lists and any kind-validation paths. Foundation for every later task.

### Task 2: Wire in the `specscore-cli` parser (strict reuse)

**Source:** idea:specscore-artifact-extraction
**Depends-On:** 1
**Status:** pending

Add `github.com/specscore/specscore-cli` to `go.mod` and build a thin codegrapher-side adapter that calls its exported `pkg/feature` and `pkg/idea` parsers to turn an artifact's bytes into a structured doc (kind, slug, status, grade, child REQ/AC/Task headings, raw cross-references). No parsing logic is copied; any missing export is added in `specscore-cli`. Verify the dependency builds under `CGO_ENABLED=0`.

### Task 3: Detect SpecScore artifacts by path + frontmatter

**Source:** idea:specscore-artifact-extraction
**Depends-On:** 1
**Status:** pending

Extend the detection layer so a `.md`/`README.md` under `spec/**` whose frontmatter carries `format: https://specscore.md/<kind>-specification` is classified as `LangSpecScore` and dispatched to the new extractor, without misclassifying ordinary repository markdown (READMEs, docs/). This is new content+path-aware detection beyond the current extension-only `DetectLanguage`.

### Task 4: Emit artifact and child nodes in the extractor

**Source:** idea:specscore-artifact-extraction
**Depends-On:** 2, 3
**Status:** pending

Add `extractSpecScore` in `internal/extract` that, from the adapter's parsed doc, emits the artifact node (slug, kind, status, grade) plus deep child nodes — Requirements/Acceptance Criteria under Features, Tasks under Plans — joined by `contains` edges, and the `file`→artifact `contains` edge, mirroring the `extractGoMod` pattern. Cross-file references are recorded but not yet resolved.

### Task 5: Resolve cross-file references into edges

**Source:** idea:specscore-artifact-extraction
**Depends-On:** 4
**Status:** pending

In the `resolve/` package, link the recorded references between artifact nodes: relative-link and Related Ideas references → `references`, Promotes To → `promotes_to`, Supersedes → `supersedes`, and Plan Depends-On task ordering → `depends_on`. Single-repo resolution only.

### Task 6: Deterministic self-goldens and gates

**Source:** idea:specscore-artifact-extraction
**Depends-On:** 5
**Status:** pending

Add fixtures (codegrapher's own `spec/` tree plus a small SpecScore sample), chosen so the goldens exercise all three new edge kinds (`promotes_to`, `supersedes`, `depends_on`) and the child-node kinds; generate deterministic self-goldens via the re-baseline script, and wire them into the binary golden-parity test. All gates green: `gofmt`, `go vet ./...`, `CGO_ENABLED=0 go build ./...`, `CGO_ENABLED=0 go test -count=1 ./...`.

## Open Questions

- Strict reuse may require exporting currently-unexported `specscore-cli` parse helpers (e.g., section/status/dependency parsers); that export work lands in `specscore-cli`, scoped as a Task 2 dependency, not as porting here.
- Whether REQ/AC/Task node identity should use the artifact-local ID from the spec or a derived slug, for stable cross-references — settle during Task 4.
- Pulling the `specscore-cli` module brings its (cobra-based) dependency tree into codegrapher; confirm it stays `CGO_ENABLED=0`-clean and acceptably sized during Task 2.

---
*This document follows the https://specscore.md/plan-specification*

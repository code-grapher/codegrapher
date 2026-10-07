# Independent implementation reviews and verification

## Website implementation

# Independent website implementation review — r1

Reviewed the uncommitted MeaningGraph and ModelSpec website diffs against the full user request and the approved CodeGrapher URL contract. This was a read-only review of the website worktrees; I authored none of these changes.

## Findings

No blocking, major, or minor findings. The MeaningGraph homepage insertion and registry graph, model, file, and concept actions use the registry's validated GitHub repository and pinned commit (`scripts/lib/enhance.mjs:28-33`, `scripts/lib/render.mjs:70-77,97-100,138-141,200-203,317-320`). The concept action includes `#line`, and the graph-list card uses separate anchors rather than nested links (`scripts/lib/render.mjs:138-141`). The updated E2E selector checks the actual fixture repository and full commit (`test/e2e/registry.spec.mjs:117-129`).

ModelSpec's homepage and registry actions similarly derive viewer URLs from the registered GitHub repository, commit, and model file; unsupported forges receive no viewer action (`public/index.html:410-418`, `src/render.mjs:56-65,257-263,440-445,492-496`). Output URLs pass through the existing `safeUrl` boundary. Focused tests cover pin, path encoding, and non-GitHub handling (`test/render.test.mjs:44-56`).

The new ModelSpec link class names have no dedicated CSS rule. Existing site anchor styles remain legible, so this is an optional polish suggestion, not a finding or landing gate.

VERDICT: blockers=0 majors=0 minors=0 land=yes

## Provider implementation

# Independent provider implementation review — r1

Reviewed the server worktree's exact-commit index/status/events flow, manifest storage, clone checkout, and eviction against the approved pinned-snapshot contract. I authored none of these changes.

## Major findings

1. **Fresh pin-only repositories can be evicted immediately.** `runIndex` updates `LastIndexedAt` only for default-branch indexing, while a pin-only entry records time in `Snapshots[commit].IndexedAt` (`internal/handler/handler.go:254-272`). Eviction chooses the oldest repository solely by `LastIndexedAt` (`internal/eviction/eviction.go:47-59`). At a storage limit, a newly requested pinned repository with zero `LastIndexedAt` sorts older than all default-indexed repositories, so the just-built snapshot can be deleted by the same request (`internal/handler/handler.go:278-284`). Make eviction age consider the latest retained pin and cover pin-only capacity with a test.

2. **Snapshot map mutation can race with status/events reads.** `Manifest.Get` returns a shallow `RepoEntry` copy whose `Snapshots` map is shared (`internal/manifest/manifest.go:63-71`). `runIndex` writes that map outside the manifest mutex (`internal/handler/handler.go:254-272`), while status and event handlers read it (`internal/handler/status_handler.go:59-75`, `internal/handler/events_handler.go:91-103`). A status request concurrent with a completed index may race or panic. Clone the map before mutation or deep-copy map-bearing entries at the manifest boundary, and run a concurrent race test.

The new exact-commit status checks do validate stored manifest ref and provenance, and the per-repository lock serializes clone/index work. These findings block landing until fixed and independently rechecked.

VERDICT: blockers=0 majors=2 minors=0 land=no

## Round 2

Both r1 findings are fixed. `runIndex` now records `LastStoredAt` for every successful default or pin export (`internal/handler/handler.go:254-274`), and eviction compares that time with legacy default and pin timestamps (`internal/eviction/eviction.go:47-70`). The new capacity regression verifies that an older default repository is evicted before a fresh pin-only repository (`internal/eviction/eviction_test.go:49-79`).

`Manifest.Get`, `All`, and `Upsert` now clone the `Snapshots` map while holding the manifest lock (`internal/manifest/manifest.go:69-110`), removing the shared-map write/read path. A concurrent reader/writer regression was added (`internal/manifest/manifest_test.go:10-52`); the provider author reports focused `go test -race` passing across manifest, eviction, handler, gitops, graphmanifest, and indexjob. I independently inspected the fix and find no remaining provider finding.

The provider author also removed the prior asynchronous `git gc` call after index completion (`internal/handler/handler.go:278-295`). This avoids clone mutation after releasing the per-repository lock while another pinned request starts. I inspected that cutover; storage is still bounded by repository eviction.

VERDICT r2: blockers=0 majors=0 minors=0 land=yes

## Round 3 incremental eviction review

The provider now serializes eviction passes with `evictMu` and takes a candidate repository's lock with `TryLock` before deleting its clone and graph (`internal/handler/handler.go:300-316`, `internal/eviction/eviction.go:24-70`). The indexing repository is already locked and can be evicted only if its own export exceeds storage limits; the caller then reports failure. Another repository actively indexing is skipped. I checked the lock order: a run holds its own repo lock, then briefly `evictMu`; candidate locks are nonblocking, so two concurrent runs do not wait cyclically. The regression covers an active older repository and verifies it remains present (`internal/handler/desired_commit_test.go:20-67`). The provider author reports focused six-package race tests passing. No new finding.

VERDICT: blockers=0 majors=0 minors=0 land=yes

## Round 4 incremental provenance and eviction review

I rechecked the later provider diff against the backend ExtractionVersion 15 contract. `storedGraphCurrent` compares the linked backend's engine and extraction constants (`internal/handler/handler.go:306-307`, `internal/indexing/indexing.go:22-28`). Pin idempotence requires current provenance and matching manifest ref (`internal/handler/handler.go:139-149`); status and one-shot SSE withhold stale pin/default graph claims (`internal/handler/status_handler.go:62-83`, `internal/handler/events_handler.go:90-108`). `runIndex` forces a full rebuild in the affected default or pin slot when provenance is stale and stamps that slot only after export (`internal/handler/handler.go:247-283`). A completed retained job can be replaced for the refresh (`internal/indexjob/registry.go:163-182`). Stale default and pin regressions, including completed-job replacement, cover these paths (`internal/handler/status_handler_test.go:81-99`, `internal/handler/desired_commit_test.go:152-185`, `internal/indexjob/registry_test.go:109-128`).

The cross-repo eviction protection remains sound in this final diff: `evictMu` serializes passes and candidate `TryLock` prevents deleting another active clone (`internal/handler/handler.go:310-326`, `internal/eviction/eviction.go:31-73`). The regression holds both repository locks, verifies the active older repo survives, and verifies the current over-limit repo is removed (`internal/handler/desired_commit_test.go:20-67`). There is no `GCBackground` launch in `runIndex`. No new finding.

Provider author reports a seven-package focused race suite passing three times. My independent focused Go invocation could not run in this read-only worktree because the temporary local backend replacement currently requires a `go.mod` update; I did not mutate that consumer file. `git diff --check` passed. The release-tag dependency cutover remains the landing owner's separate gate.

VERDICT: blockers=0 majors=0 minors=0 land=yes

## Backend semantic projection

# Independent backend review: MeaningGraph / ModelSpec semantic graph

Reviewer: `/root/websites` (read-only review of the backend worktree)
Target: `/Users/alex/projects/.worktrees/codegrapher-site-links/github.com/code-grapher/codegrapher`
Scope: semantic source identity, ModelSpec twin ownership, declared binding behavior; uncommitted implementation against the approved plan.

## Round 1 findings

1. **Major: remote identity accepted unsupported local or malformed URLs.** `semantic/build.go` derived the public repository address by loosely stripping a remote prefix. A `file:///tmp/owner/repo` remote or a URL with extra path segments could become a public `host/owner/repo` identity, which then fed meaning node IDs and metadata. This could misidentify a local graph in externally linkable concepts. Reported to backend implementer and root.
2. **Major: same-name ModelSpec JSON twins could attach to the wrong HCL module.** For a layout twin with `TwinOf == nil`, `modelSpec()` selected the first matching module name from a Go map. Two separate models directories with the same module name could merge a JSON representation into the wrong scope, nondeterministically. Reported to backend implementer and root.

## Round 2 verification

The backend implementer replaced loose remote extraction with `parseRepoAddress`, accepting exact HTTPS, SSH `git@`, or scp-style `git@host:owner/repo` shapes with two repository path components. Local/file, traversal, query, and extra-segment remotes return an empty public address. `modelSpec()` now assigns a `TwinOf == nil` layout twin to the HCL group in its own models directory. New tests cover malformed remotes and two same-named HCL/JSON layouts over repeated builds.

Read back the revised `semantic/build.go` and test cases. Ran:

```text
GOCACHE=/tmp/codegrapher-backend-review-gocache GOSUMDB=off GOPROXY=off go test -mod=mod ./semantic -run 'Test(RepoAddressRejectsLocalAndMalformedRemotes|MultiFileTwinUsesOwningLayoutDirectory|BindingAliasUsesDeclaredModelSource|InvalidBindingRoleDoesNotProduceAcceptedEdge)' -count=5
ok github.com/specscore/codegrapher/semantic 0.616s
```

The two reported majors are closed. No remaining findings in the reviewed semantic paths. This review does not replace the backend's full Go gate or provider release verification; the landing owner will verify those separately.

## README section re-review

Reviewed the newly added `## MeaningGraph and ModelSpec` section against `semantic/build.go`, its tests, and the indexer semantic projection. The example comment prefixes and target forms match the parser; declared bindings, labels/synonyms, source locations, diagnostics, and unresolved external pins are represented as described. One nonblocking wording precision was sent to the landing owner: a preceding annotation is accepted within three lines of its symbol, so “immediately before (within three lines)” is more exact than unrestricted “before.” No unresolved pin or unsupported automatic mapping is promised by the section.

VERDICT: blockers=0 majors=0 minors=0 land=yes

## Backend API and migration

# Backend browser API and extraction-version review

Scope: independent read-only review of the backend worktree's browser API metadata/provenance transport, TypeSpec/OpenAPI/generated TypeScript client, and ExtractionVersion 15 migration/rebaseline. The parser/projection review is separate.

## Round 1 findings and disposition

1. **Major, fixed — distinct binding evidence collapsed in graph API.** `browserapi/graph.go` keyed edges only by kind/source/target, although `semantic/build.go:678` emits per-binding role, match, note and line, and `store/schema.sql:47-57` permits several edges between one pair. The API could show one arbitrary role for multiple bindings. The graph now deduplicates by complete serialized edge identity and emits every distinct row (`browserapi/graph.go:75-85,134-142`). A regression inserts a second same-endpoint binding with different role and note and asserts both survive (`browserapi/server_test.go:157-188`).
2. **Major, fixed — a failed semantic-only rebuild could be marked current.** In the zero-code-file branch, the extraction-version stamp followed a failed semantic projection. That could prevent a later Sync from retrying. Stamping is now conditional on successful projection (`indexer/init.go:166-180`), and a failure/retry regression verifies version 14 remains stale until a successful rebuild (`indexer/semantic_integration_test.go:179-210`).
3. **Minor, fixed — rebaseline rewrote unrelated golden provenance.** The first full capture modified machine paths, timestamps, sizes and ordering in many goldens even though `internal/paritytest/paritytest.go` canonicalizes those values. The author restored that churn. `tools/parity/rebaseline-extraction-version.py:13-34` derives the version from Go source and updates only the two version fields; 27 status goldens now contain only 14→15 changes.

## Final checks

- API Go DTOs, `typespec/main.tsp:128,159-160`, generated OpenAPI, and TypeScript source/dist models and serializers agree on optional symbol/edge metadata and edge provenance. `browserapi/server_test.go:119-195` exercises search metadata, binding role, explicit mapping provenance and canonical code bridge metadata.
- `indexer/indexer.go` raises extraction version to 15; `indexer/semantic_integration_test.go:140-177` verifies a version-14 index triggers full semantic rebuild. No unrelated golden query output remains changed.
- Independent focused run: `GOCACHE=/private/tmp/codegrapher-backend-api-review-gocache go test ./browserapi ./indexer ./internal/paritytest` passed. `git diff --check` passed.

VERDICT: blockers=0 majors=0 minors=0 land=yes

## Viewer implementation

# Independent viewer review — 2026-10-07

Scope: uncommitted `codegrapher-dev` viewer diff in the isolated `codegrapher-site-links` worktree. Reviewed metadata transport, knowledge views and search, binding/code evidence, pinned provider status and manifests, file/line navigation, and related tests. Reviewer: independent backend implementation lane.

## R1 finding — major, resolved

The new semantic source links encoded each file path segment, while `parseRoute` still consumed the encoded path verbatim. A URL such as `/github.com/org/repo/model/Caf%C3%A9%20%23%25.modelspec.hcl?branch=<40-char-commit>#line=17` could open via a click but fail to find the file after refresh. `viewerFileHref` and `routePath` also emitted raw paths. Evidence before repair: new source links in `libs/codegrapher/ui/src/lib/viewer/semantic-details.ts`, and prior raw-path parser/link builders in `libs/codegrapher/ui/src/lib/ui/ui.ts`.

R2 fix verified: `libs/codegrapher/ui/src/lib/viewer/path-url.ts:1-22` now provides shared segment encoding and safe decoding; `libs/codegrapher/ui/src/lib/ui/ui.ts:1215-1217,1341-1349` decodes incoming paths and encodes generated routes. The round-trip test at `libs/codegrapher/ui/src/lib/ui/ui.spec.ts:124-132` covers a pinned commit, Unicode, space, `#`, `%`, and line hash; adjacent cases reject encoded separators, traversal, and malformed escapes. The search and semantic source links use the shared helper. This resolves the original finding.

## R2 review

`graph.ts` decodes both node and edge metadata whether INGR returns JSON text or an object; absent/malformed metadata stays empty. `semantic-details.ts` follows declared edges, preserves binding roles and annotation evidence, and resolves `semantic_code` bridges through `canonicalCodeId` to the actual code symbol. Search includes labels/synonyms and excludes bridges as standalone results. Pinned manifest/status paths compare the requested immutable commit with both the indexed commit and manifest ref before loading; file links carry the same revision and line. No further blocker was found in the inspected diff.

The author-reported test, lint, and build gates and the final integrated pinned browser journey remain separate landing checks.

VERDICT: blockers=0 majors=0 minors=0 land=yes

## Root verification receipts

- MeaningGraph: 182 unit tests pass; production registry build succeeds; 54 browser cases passed plus both corrected graph-card desktop/mobile cases passing.
- ModelSpec: 189 unit tests pass; production registry build succeeds; 58 desktop/mobile browser cases pass.
- Viewer: root inspected WB logs confirming 258 UI tests, 3 app tests, UI/app lint, production build, and 72 browser cases passing with 6 expected opt-in skips.
- Backend: full version-15 Go suite, vet, build and generated contract checks pass. Root inspected the saved package results and source migration changes; exact PR/main CI remains a landing gate.
- Both website PRs have two passing checks at their exact reviewed heads.
- Root browser inspection confirms both support sections render legibly in their existing site styles. Temporary preview servers/tabs and E2E listeners were closed.
- These are pre-deployment receipts. Public support remains gated on the released dependency, provider/viewer deployment and exact-revision website journey.

## Open Questions

None at this time.

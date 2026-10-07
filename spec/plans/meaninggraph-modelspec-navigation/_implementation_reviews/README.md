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


## Forward CLI correction

# Independent forward CLI review

Reviewed read-only in `/Users/alex/projects/.worktrees/codegrapher-site-links/github.com/code-grapher/codegrapher` at HEAD `6594dd9ccd8e965635fe9bd9ac899a6c24191f78`. Scope is the uncommitted diff in `internal/cli/node.go`, `internal/cli/query.go`, `internal/cli/store_querier.go` plus new `internal/cli/semantic_navigation_test.go`; unrelated plan changes excluded. At review time, tracked CLI diff SHA-256 was `4233960d757218d71bdbe2fae10fa7bfb4b49d229b4fe0d280a0f7292753220f`, and test file SHA-256 was `3a0ec976c6ecb87e9aaa1da7bafc123ae909da5a38cdff26586308c77aa27e6d`.

## Findings

No remaining blocker, major, or minor finding. During review I identified that the first draft added `BriefSymbol.Metadata` but did not populate it for `query --brief`; the author fixed it before the ready signal (`internal/cli/query.go:119,122-135`). I also requested readable metadata and binding evidence in default text `node` output; the final diff includes both (`internal/cli/node.go:514-521,542-559`).

## Evidence

- Bridge lookup follows `canonicalCodeId` only into the selected stores and returns the owning code node (`internal/cli/node.go:57-77`); name and ID node matches are deduplicated by canonical ID (`internal/cli/node.go:310-356`). Ordinary multi-scope query aliases bridge rows to canonical code and keeps the strongest score; explicit bridge-kind queries retain the bridge (`internal/cli/store_querier.go:63-104`).
- `NodeRelation` now carries edge metadata and bridge evidence, and relation collection resolves bridge targets to canonical code (`internal/cli/node.go:39-50,383-459`). The reverse code journey reads incoming mapping edges from the semantic bridge, bounded by the requested per-direction limit (`internal/cli/node.go:435-459`). Semantic node metadata is present in JSON and readable text (`internal/cli/node.go:462-463,514-521`).
- The CLI regression exercises duplicate-free Total search, explicit bridge inspection, node aliasing, concept labels/synonyms, binding role/match/note, forward and reverse explicit code mapping, readable text, `query revenue --brief`, and an actual HCL edit plus `sync` removing stale binding/mapping (`internal/cli/semantic_navigation_test.go:14-178`). ExtractionVersion remains 15.
- Independent focused command passed: `GOCACHE=/private/tmp/codegrapher-cli-review-gocache go test ./internal/cli -run 'TestSemanticCLIContractAndHCLSync|TestBriefSearchResultsOmitVerboseNodeFields' -count=1`. `git diff --check` passed. An independent whole-package run could not pass in this sandbox because unrelated transport/upgrade tests bind local ports; the focused regression does not use ports. The author reports full Go suite and lint green, and root separately reran the actual standalone CLI fixture.

VERDICT: blockers=0 majors=0 minors=0 land=yes


Root verification: full Go suite passed with loopback test access (`/private/tmp/codegrapher-site-links-root-cli-test.log`); `go vet` and static CLI build passed. Standalone CLI search returned one canonical function without its internal bridge duplicate, and concept/code node output preserved labels, roles, annotation evidence and both mapping directions. Production provider/viewer/website verification remains pending.


## Released provider dependency cutover

# Independent provider consumer-cutover review

Reviewed read-only in `/Users/alex/projects/.worktrees/codegrapher-site-links/github.com/code-grapher/server`, branch `codegrapher-site-links`, against `origin/main` and worktree HEAD `51f317f18450bb5860dacb20725f013676f8ba28`. This is the final uncommitted provider diff; the landing owner will assemble the PR. The earlier implementation review and resolved r1–r4 findings are recorded in `/private/tmp/codegrapher-site-links-provider-review.md`. I authored none of the provider changes.

## Exact dependency identity

- `go.mod` declares Go `1.27.0` and `github.com/specscore/codegrapher v0.15.1`. It has no local CodeGrapher `replace`; its only replace is the published gotreesitter fork `github.com/trakhimenok/gotreesitter v0.20.3-0.20260611095614-14527fe8bf96`.
- GitHub tag `v0.15.1` directly points to backend merge commit `86704bdfa62e912b5fafc69be083f4bf48335e34` (PR #59). Its release was published at `2026-10-07T06:26:49Z`, with draft and prerelease both false. `go list -m -json` independently resolved this module with `GoVersion: 1.27.0` and module sum `h1:liKpDB2QfSayQScchEkeAzUk+9sLsOn2lhyAdTA2uGg=`.
- Final consumer file hashes: `go.mod` SHA-256 `cc2e85343e98b815f49589ea776b2b9a86589c07e65e4009a9307aae25bd49b7`; `go.sum` SHA-256 `f2d59115105089c663d01b3b041c062151eb829c8f29268c01a24c47db75b4ce`.

## Cutover findings

The CI diff removes the sibling `codegrapher` checkout and uses `server/go.mod`/`server/go.sum` with the `server` working directory for vet, CGO-disabled build, and tests. The deployment script removes its `../codegrapher` guard and still cross-builds a self-contained Linux/amd64 binary. Neither path depends on the former local module replacement.

README route and response copy matches the current handlers: full 40-character commit pins, default and pin-specific status, scoped graph manifests and recordsets, `409` for manifest-ref mismatch, `410` for absent graph data, and `406` when a recordset request accepts neither zstd nor gzip. During this review I found that the response table omitted the `202` index acknowledgement and `406` encoding response; the provider author corrected both and I rechecked the resulting diff. No finding remains.

Independent checks at the final module hashes passed: `go mod tidy -diff` produced no diff, `go mod verify` reported all modules verified, `git diff --check` and `bash -n scripts/deploy-server.sh` passed, and `CGO_ENABLED=0 go test -count=1 ./internal/handler ./internal/graphmanifest ./internal/manifest` passed. The provider author reports exit status zero for full vet, CGO-disabled build/test, Linux/amd64 build, and module checks on `v0.15.1`; I inspected the corresponding `/private/tmp/codegrapher-site-links-provider-v0151-{test,vet,build,modverify,linux-build,tidydiff,diffcheck}.log` outputs but did not duplicate those broader gates. The build logs contain a stat-cache write warning, which the author reports did not fail the builds. Merge, remote CI, and deployment remain the landing owner's subsequent verification steps.

VERDICT: blockers=0 majors=0 minors=0 land=yes


## Reciprocal website support

# Independent reciprocal-support review

Reviewer: `/root/websites` (read-only; another author owns these edits)

## Backend idea and plan delta

- Worktree: `/Users/alex/projects/.worktrees/codegrapher-site-links/github.com/code-grapher/codegrapher`
- HEAD: `2ec63a29918249fef31b273a6841c25a784c751a`
- Paths: `spec/ideas/meaninggraph-modelspec-navigation.md`, `spec/plans/meaninggraph-modelspec-navigation/README.md`
- Exact path-limited `git diff` SHA-256: `bbc06154f58b1485d5dc6b9a26df71c926229de91aabcb668b54d718df1a2136`

The addition records the user's expanded CodeGrapher homepage request in the idea context, scope, summary, user journey, acceptance criteria, and Task 4. It retains the requirement for typed semantics, explicit code mapping, correct pinned viewer routes on the two registries, and no fabricated links for illustrative items. The new homepage acceptance criterion names both external project sites. No plan inconsistency or unsupported automatic code-mapping promise found in these two paths.

## Viewer homepage delta

- Worktree: `/Users/alex/projects/.worktrees/codegrapher-site-links/github.com/code-grapher/codegrapher-dev`
- HEAD: `6799edbeebe0030a08a08ec0e2be4e7e1c7c29d3`
- Paths: `apps/codegrapher/src/app/landing-page.component.html`, `apps/codegrapher/src/styles.css`
- Exact path-limited `git diff` SHA-256: `e22e9a0f21d766a9eb6f105b61bac986026b3d4ed82c2326f659742576ee9e91`

The new section says CodeGrapher indexes MeaningGraph and ModelSpec declarations and lets visitors inspect typed Meaning/Models/Code views, accepted bindings, source locations, model relationships, and code symbols connected by explicit annotations. Those claims align with the reviewed semantic implementation and avoid claiming inferred code mappings or resolved external dependencies. The two links use the correct project homepages, have descriptive link text and safe new-tab attributes, and the section has a unique heading target for the added header/footer navigation. Existing global focus styles apply. The two-column card rule collapses to one column through the later `@media (max-width: 540px)` `.cards` rule; at intermediate widths it follows the site's existing two-column layout. The section adds no interactive state or fabricated source links. The author's lint/build and diff-check results were reported green; this review inspected the source and CSS without starting a preview server.

No findings in the reviewed reciprocal-support delta. The root landing owner must still verify the final browser journey before production deployment.

VERDICT: blockers=0 majors=0 minors=0 land=yes


Root receipts: CodeGrapher v0.15.1 is published at merge `86704bdfa62e912b5fafc69be083f4bf48335e34`; exact main Go CI and Release workflows succeeded. The provider Linux binary links released v0.15.1 and all consumer packages pass. The real Chinook registry revision `26e852cca00101f53a84ef8ee1f1ae389067f5cf` indexes 88 files, 900 nodes and 5,756 edges; invoice-total binds to the ModelSpec Total member with role value. Core external pins remain unresolved without verified exact revisions. The user explicitly authorized WB’s observed-check landing mode. Provider PR #8 merged at `50ae6124418775c26b09e3c02fc0d81e983fadb5`; viewer PR #18 merged at `8cfbdd9448d9d28e5ce2477eccdf9c51a55c2835`. WB verified exact candidate checks, target containment, remote branch deletion and canonical fast-forward; the five task worktrees were retained for delivery verification. The provider deployed from clean merged main; the remote binary SHA-256 matches the local binary (`40ae37735e6603822b72a6790e586297e6663a44680e23752d188e96cef62c10`). Its public status serves separate Chinook snapshots at pins `26e852cca00101f53a84ef8ee1f1ae389067f5cf` and `f11b1192ed9f48cdd4f788d1d4ffde0e972ee04b`, alongside default main `411cb7aead96a0b4314f3068c0a0455f869dfdee`. Both pins contain 100 semantic nodes and 167 edges; default contains 109 and 174. All advertise ExtractionVersion 15 and matching indexed commits. A missing all-zero pin stays unindexed and its graph manifest returns HTTP 410.

The viewer deployed as Cloudflare version `a473e80d-56ba-4836-bee2-d81fa8cd64c5`. Root verified the actual static homepage support section, real concept-label search and Invoice.Total binding details. The production browser journey exposed a source-navigation bug: a representation changed the pinned URL while Meaning search still covered raw source. The independently reviewed forward correction merged in PR #19 at `845cece9e6dea65f500a001e6ce3e57b27e93d37` and deployed as Cloudflare version `f763c57b-9a3d-47ea-9a79-2385db4db746`. Exact merged lint/build-test/e2e checks passed. Root repeated the real pinned Meaning → invoice-total → Invoice.Total → HCL journey: source renders immediately at line 378, the Structure panel lists semantic declarations, a Structure click retains the pin, and refresh retains source/path/line. The JSON twin renders and its canonical Total declaration opens line 312. MeaningGraph PR #18 merged at `e3bcf42b65161fb8583c5c47fb8fd81f97bffb73`; ModelSpec PR #14 merged at `a4ab94112536fd03813e21fa22e45244d2a4d69f`. Both exact merged Deploy workflows completed successfully, with Deploy and live smoke-check steps executed. Live build markers advertise those exact commits, production=true and fixture=false. Root verified both supported-by sections, card viewer links, MeaningGraph concept YAML source at line 343, and ModelSpec HCL source actions at registry pin `26e852cca00101f53a84ef8ee1f1ae389067f5cf`. The merged viewer’s lint/build-test/e2e checks passed, while its separate automatic Workers Builds check failed without exposing an error log. Manual deployment succeeded; The same automatic Workers Builds failure was verified on prior main `3cac2866610a992a529a2afc0fb8111cc9665fdd`, before this feature. Automatic-build diagnosis requires an authenticated Cloudflare dashboard session and is separate from the successful manual feature deployment. WB cleaned all five original implementation worktrees and local task branches after verifying exact fresh origin/main containment. The remote task branches had already been deleted during each WB landing. The cleanup receipt reports applied=true, worktree_gone=true and branch_deleted=true for every repository; remote_deleted=false records that there was no remaining remote branch to delete.


## Pinned source-navigation correction

# Independent viewer source-navigation review

Reviewer: `/root/websites` (read-only review of another author's change)
Worktree: `/Users/alex/projects/.worktrees/codegrapher-site-links/github.com/code-grapher/codegrapher-dev`
Base HEAD: `8cfbdd9448d9d28e5ce2477eccdf9c51a55c2835`
Candidate paths, in the order used for the digest below:

1. `apps/codegrapher-e2e/src/pinned-semantic-route.spec.ts`
2. `apps/codegrapher-e2e/src/semantic-source-navigation.spec.ts` (untracked new file)
3. `libs/codegrapher/ui/src/lib/ui/ui.ts`
4. `libs/codegrapher/ui/src/lib/viewer/file-structure.spec.ts`
5. `libs/codegrapher/ui/src/lib/viewer/file-structure.ts`

Exact SHA-256 of each listed path's UTF-8 name followed by NUL and its raw file bytes, concatenated in this order: `3c4bada727df773ffd380ff9711fe9ce0fe49a4fcdebde354c036bd042acd73d`. This identifies both tracked edits and the untracked regression file. `git diff --check` passed.

The reported bug is reproduced by the old render priority: Meaning/Models view synthesizes search results even after a source link changes the route to a file, and main content renders results before the file panel. The fix clears the query and category only for explicit file, directory, or symbol navigation. A semantic relationship click uses the details component's `selectNode` output, so it keeps the semantic details view. A representation/evidence link uses `navigate`, clearing the search view and revealing the file panel. The file content path still uses the existing request ID check to discard stale load responses and calls `scrollToHash` after loading.

The representation URL is segment encoded and includes the requested line. `onSymbolNavigate` strips its query but `navigateToPath` carries the current branch for paths under the same repository; it does not substitute the default branch or carry a branch across repositories. The new Chromium regression asserts the pinned URL and line after the representation click, visible HCL source, absence of the search-results panel, a Structure declaration click that retains the same pin, and the same rendered line after reload. Its relationship click first verifies that the target semantic details remain open. The preexisting pinned-route test now fails immediately on an unexpected unpinned status request.

File Structure now includes semantic declarations in the canonical file and secondary HCL/JSON representations. A secondary representation uses its own file and line in the link while retaining the canonical node ID. Unit cases cover canonical JSON to secondary HCL with a segment-encoded space, and canonical HCL to secondary JSON. `repoBase` is an unpinned `/<forge>/<org>/<repo>` path in the file panel; Structure emits a path/hash and the UI controller adds the current same-repo branch on click, as the Chromium regression verifies.

Receipts read: `/private/tmp/codegrapher-semantic-source-ui-test.log` (260/260 UI tests), `-ui-lint.log`, `-e2e-lint.log`, and `-e2e.log` (two pinned Chromium journeys). All report success. No separate preview server was started for this review.

VERDICT: blockers=0 majors=0 minors=0 land=yes


## Registry integration reviews

# MeaningGraph PR #18 integration review

Reviewer: `/root/websites` (read-only review; root owns landing)
Reviewed-Head: `23413be72b953a91751fd9a8e8192c0479a78515`
Current origin/main integrated: `6eda3e1f2360b21ea477921c815b8bc59b6a3805`
Previous reviewed feature commit: `7081d025ef365f4951aad436c2defc451d405eed`

The updated branch is a merge of main into the feature branch. Its six-file feature diff against current main remains the CodeGrapher support section, pinned registered graph/model/concept viewer links, styles, and tests. Compared directly with the previous reviewed feature commit, `scripts/lib/enhance.mjs`, `scripts/lib/render.mjs`, `site/registry.css`, `test/e2e/registry.spec.mjs`, and `test/unit/build.test.mjs` are byte-identical. The only change among those six files is a one-line expected output addition in `test/unit/site.test.mjs` for `registry-search.json`, which current main now generates. The new registry-search UI is mounted separately and does not select or rewrite the feature's graph cards or `data-codegrapher-link` actions. The feature still derives viewer routes only from registered repository/file/line identities with the pinned full commit; illustrative homepage entities have no invented mappings.

The worktree is clean. `git diff --check origin/main...HEAD` passed. `npm test` passed all 185 unit tests on this exact reviewed head, including the support-section patch and encoded pinned-link checks. The root landing owner is waiting for PR CI and will perform the final landing and production journey checks.

VERDICT: blockers=0 majors=0 minors=0 land=yes


# ModelSpec PR #14 integration review

Reviewer: `/root/websites` (read-only review; root owns landing)
Reviewed-Head: `f1f42788f33f86ccaba32e249c6713f77eb03f37`
Current origin/main integrated: `034c58e459a5ac75f28617b882cbfc2909f1ace3`
Previous reviewed feature commit: `cd90c442d71b7c1c253351ed496dea397f42d21f`

The updated branch merges main into the feature branch. Its three-file feature diff against current main remains the CodeGrapher homepage support section, registered model/card/file viewer links with a pinned commit and segment-encoded source path, and URL tests. Compared directly with the previous reviewed feature commit, `public/index.html` and `test/render.test.mjs` are byte-identical. The changes to `src/render.mjs` between those commits come from current main's native Collections section and its jump link; the feature's `codeGrapherUrl` and registered card/detail/model-file link hunks remain intact. Current main's gated registry-search UI is mounted separately and does not select or rewrite those CodeGrapher links. Non-GitHub repository links continue to be omitted.

The worktree is clean. `git diff --check origin/main...HEAD` passed. `npm test` passed all 192 unit tests on this exact reviewed head, including pinned and encoded viewer URL checks. The root landing owner will perform the approved observed-check landing and production journey checks.

VERDICT: blockers=0 majors=0 minors=0 land=yes


## Qualified semantic search correction

# Independent qualified semantic search review

Reviewer: `/root/websites` (read-only review of root-authored change)
Base HEAD: `845cece9e6dea65f500a001e6ce3e57b27e93d37`
Candidate paths, in digest order: `libs/codegrapher/ui/src/lib/viewer/search.spec.ts`, `libs/codegrapher/ui/src/lib/viewer/search.ts`
Exact SHA-256 of each path's UTF-8 name followed by NUL and raw file bytes, concatenated in that order: `3a3c1a0635e9f22bcf92e0e37ebcefffd502427d0b2035821a75f8fafd800e5d`

The search change adds `qualifiedName` as a matched term only for `model_` and `meaning_` nodes. Existing name matching still takes display precedence; labels, synonyms and docstrings remain fallbacks, so a qualified hit shows its full identity as `matchedText` without changing the symbol's display name. The existing `inView` gate runs before matching, preserving Code/Models/Meaning category separation and excluding `semantic_code`. File and directory matching is untouched. Search remains case-insensitive substring/glob matching, so `Invoice.Total` can find a qualified member such as `chinook.Invoice.Total` without making names exact-match identifiers.

The regression uses two distinct `Total` members and checks that `Invoice.Total` selects only the Invoice member, `sales.*.Total` selects the Order member, bare `Total` still finds both, a qualified Meaning concept is searchable in Meaning view, and an Invoice member does not appear in Meaning view. The preexisting search tests cover labels, synonyms, docstrings and Code filtering. `git diff --check` passed, and `/private/tmp/codegrapher-site-links-qualified-search-checks.log` reports successful UI test and lint targets with the cache skipped.

VERDICT: blockers=0 majors=0 minors=0 land=yes


Root follow-up receipt: qualified semantic search PR #20 merged at `c9c7c6038b23dbc37b88280bf7857b05471d3c61` after lint/build-test/e2e CI passed, then deployed from clean merged main as Cloudflare version `e84f1783-c085-4f25-8292-6b5b62cbee39`. Production Models search for `Invoice.Total` returns one typed Total member with matching identity `chinook.Invoice.Total`; details retain declared reverse MeaningGraph bindings and both HCL/JSON source representations. Root saved visual proof of the typed details, pinned source and all three support sections.


## Production delivery and cleanup

| Surface | Verified implementation commit | Production receipt |
| --- | --- | --- |
| CodeGrapher CLI/library | `86704bdfa62e912b5fafc69be083f4bf48335e34` | Published CodeGrapher v0.15.1; exact Go CI and release passed |
| Public provider | `50ae6124418775c26b09e3c02fc0d81e983fadb5` | Clean deployed binary links v0.15.1; remote/local SHA-256 match; default plus two immutable pins coexist |
| Viewer and homepage | `c9c7c6038b23dbc37b88280bf7857b05471d3c61` | Cloudflare version `e84f1783-c085-4f25-8292-6b5b62cbee39`; exact merged lint/build-test/e2e passed; real pinned navigation and qualified search passed |
| MeaningGraph website | `e3bcf42b65161fb8583c5c47fb8fd81f97bffb73` | [Deploy and smoke check succeeded](https://github.com/sneat-co/meaninggraph/actions/runs/37588257022); live production build marker matches |
| ModelSpec website | `a4ab94112536fd03813e21fa22e45244d2a4d69f` | [Deploy and smoke check succeeded](https://github.com/specscore/modelspec/actions/runs/37588486889); live production build marker matches |

All three live homepages describe support and link to their counterparts. Root clicked both registry card links and source-page actions. MeaningGraph’s invoice-total source action opens the pinned YAML at line 343; ModelSpec’s HCL action opens the pinned model. In the viewer, label search, qualified member search, binding details, source actions, HCL/JSON Structure declarations and refresh retain the registry commit. Explicit code annotations and actual edit/sync removal were verified with the standalone CLI fixture and regression; the Chinook source does not fabricate implementation mappings. Externally pinned core references remain unresolved without a verified exact dependency.

Local operational receipts: `/private/tmp/codegrapher-site-links-{server,viewer,viewer-source,qualified-search,meaninggraph,modelspec}-land.json`, `-cleanup.json`, both `-live-build.json` files, `-live-{pin-a,pin-b,default}-status.json`, and the missing-pin 410 response. Screenshots are saved in the calling task’s visualization directory. WB cleanup removed the five original worktrees and local branches and released the original task’s remote claim. No owned preview or browser-test listeners remain on ports 4211, 4207, 8764, 8765, 4178, 4179 or 8791–8793. Root also removed its temporary Chinook source clone, synthetic CLI fixture and standalone verification binary while retaining all receipts (`/private/tmp/codegrapher-site-links-temp-cleanup.json`). Temporary verification tabs were closed; the homepage, typed details and user-requested Cloudflare sign-in tab remain available.

The existing automatic Workers Builds failure is separate from the verified manual viewer deployment. It was present before these changes; dashboard log diagnosis awaits the user’s sign-in. Automatic approval review rejected direct reuse of a credential from Wrangler configuration, and that route was stopped. This is recorded as a remaining hosting-maintenance limitation, not a failure of the delivered semantic support.

---
format: https://specscore.md/plan-specification
status: Approved
---

# Plan: Authenticated browser API

**Status:** Approved
**Source Feature:** live-daemon-api
**Date:** 2026-09-12
**Owner:** alex
**Supersedes:** —

## Summary

Deliver the provider-owned browser contract, one-repository read-only API,
foreground capability composition, and daemon integration as one releasable
slice. The website repository stays read-only in this provider task; its landed
routing and transport at `d4b0d9f266db6753f22d92cb1d58e83bab538411`
define the consumer boundary, while its gateway remains honestly
`contract-pending` until a later consumer-wiring change.

## Approach

Start from the whole browser journey and freeze its wire contract in TypeSpec
before writing handlers. Keep HTTP DTOs and security in a new public-API package
that composes the existing index/query/freshness layers. Persist one random
opaque repository ID inside the worktree-local index, derive a revision digest
from indexed relative paths and content hashes, and reject mismatched revisions.
Run foreground capabilities under one cancellation group; extend the daemon's
private state only with a separately generated browser credential and public
listener metadata. Keep multi-repository registration, write operations,
relay/tunnel hosting, historical Git revisions, and UI wiring outside this
provider release.

## End-to-End User Journey

1. I initialize a repository and run `codegrapher daemon start`. **Observable good result:** the command returns only after freshness and the browser API are ready, and prints the current codegrapher.dev `/browse/localhost:<port>` contract link whose fragment carries a browser secret rather than the lifecycle token.
2. In the provider E2E, I open a real local-development browser page that uses the generated client and explicit insecure-loopback opt-in. The browser sends a bearer-authenticated preflight/status request directly to `/codegrapher/v1/`. **Observable good result:** the API identifies version 1, reports one repository, and never returns a local path. The hosted website gateway is not claimed wired by this provider release.
3. I do nothing while the browser loads repository metadata, the root tree, and one file using the advertised repository ID and revision. **Observable good result:** directory entries are deterministic, source is bounded and repository-relative, and the URL remains refresh-safe.
4. I open a symbol, search for another symbol, and expand a one-hop graph. **Observable good result:** every response carries effective limits/truncation metadata and links only symbols/files in the registered repository.
5. I try a traversal path and then reuse the private lifecycle token against the public API. **Observable good result:** both requests fail with distinct JSON errors and neither response leaks a path or credential.
6. I edit a source file and wait without issuing `sync`. **Observable good result:** freshness becomes updating and then ready; the previous revision gets `revision_changed`, repository metadata advertises the new revision, and the new file/source graph is visible.
7. I stop the daemon. **Observable good result:** private authenticated control performs a joined shutdown, the public listener closes, and daemon status is stopped.

## Tasks

### Task 1: Freeze and generate the provider contract

**Verifies:** live-daemon-api#ac:contract-generated-from-typespec, live-daemon-api#ac:status-negotiates-api-compatibility, live-daemon-api#ac:repository-identity-is-stable-and-path-free, live-daemon-api#ac:symbol-search-and-graph-are-bounded
**Depends-On:** —
**Status:** planning

Define the `/codegrapher/v1/` service, status compatibility handshake, auth
scheme, DTOs, exact paths, bounds, freshness enum, truncation envelope, and
error union in `typespec/main.tsp`. The repository-root `package.json`, pinned
`pnpm-lock.yaml`, and `typespec/tspconfig.yaml` own generation into
`api/openapi/v1/openapi.yaml` and the importable `clients/typescript` package.
Add one reproducible generation/drift-check command and document consumer
import, endpoint override, and authenticated fetch usage.

### Task 2: Implement the bounded repository API

**Verifies:** live-daemon-api#ac:public-api-authenticates-and-confines-origin, live-daemon-api#ac:status-negotiates-api-compatibility, live-daemon-api#ac:repository-identity-is-stable-and-path-free, live-daemon-api#ac:tree-and-file-access-is-traversal-safe-and-bounded, live-daemon-api#ac:symbol-search-and-graph-are-bounded, live-daemon-api#ac:freshness-and-revision-are-truthful
**Depends-On:** 1
**Status:** planning

Build a public-API package over existing index stores and freshness status.
Persist the opaque ID, compute the index revision, normalize and resolve paths
under the registered root, reject escaping symlinks, map internal models to
wire DTOs, scrub path-bearing freshness/internal errors, cap all reads/queries,
implement constant-time bearer auth and exact-origin CORS for both deployed
domains plus configured local development, and return stable request-correlated
JSON errors.

### Task 3: Compose foreground serve capabilities

**Verifies:** live-daemon-api#ac:serve-capabilities-compose
**Depends-On:** 2
**Status:** planning

Replace the placeholder `--api` branch with the real server and make the default
set API+watch+MCP. Preserve stdio exclusively for MCP; send human API/watch
diagnostics to stderr when MCP is selected. Run all selected capabilities under
one cancellation scope, propagate the first real failure, and join shutdown.

### Task 4: Serve the API from the background daemon

**Verifies:** live-daemon-api#ac:daemon-separates-browser-and-control-credentials, live-daemon-api#ac:freshness-and-revision-are-truthful
**Depends-On:** 2
**Status:** planning

Generate and persist a separate browser credential in the private daemon state,
bind the public listener, expose only its address in authenticated control
status, and let the local CLI render the browser link/secret. Prove token
non-interchangeability, state-file privacy, readiness ordering, failed-bind
cleanup, and joined stop/restart behavior.

### Task 5: Prove the whole real-server journey

**Verifies:** live-daemon-api#ac:real-server-browser-api-journey, live-daemon-api#ac:tree-and-file-access-is-traversal-safe-and-bounded, live-daemon-api#ac:symbol-search-and-graph-are-bounded, live-daemon-api#ac:daemon-separates-browser-and-control-credentials
**Depends-On:** 3, 4
**Status:** planning

Add focused handler/security/bounds tests and one real-browser journey over a
temporary initialized repository and real listener. Serve a page from a
configured local-development origin, use the generated client with explicit
HTTP-loopback opt-in, and make real HTTP requests, never fixture responses.
Assert process/listener cleanup, document that hosted non-loopback access needs
HTTPS and that the landed website gateway remains pending, and retain existing
watcher/daemon lifecycle regressions.

### Task 6: Review, land, release, install, and verify

**Verifies:** live-daemon-api#ac:contract-generated-from-typespec, live-daemon-api#ac:public-api-authenticates-and-confines-origin, live-daemon-api#ac:status-negotiates-api-compatibility, live-daemon-api#ac:repository-identity-is-stable-and-path-free, live-daemon-api#ac:tree-and-file-access-is-traversal-safe-and-bounded, live-daemon-api#ac:symbol-search-and-graph-are-bounded, live-daemon-api#ac:freshness-and-revision-are-truthful, live-daemon-api#ac:serve-capabilities-compose, live-daemon-api#ac:daemon-separates-browser-and-control-credentials, live-daemon-api#ac:real-server-browser-api-journey
**Depends-On:** 5
**Status:** planning

Run focused tests, the required Go/CGO gates once, contract drift verification,
SpecScore lint, and an independent adversarial review of the exact diff. Resolve
every finding, land via WB, verify the exact remote receipt and release revision,
upgrade the Homebrew-managed binary, and run the installed daemon/browser API
journey before reporting the consumer integration contract and still-pending
website wiring explicitly.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/plan-specification*

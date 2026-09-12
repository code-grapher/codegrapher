---
format: https://specscore.md/feature-specification
status: Amending
---

# Feature: Authenticated browser API

> [SpecScore.**Studio**](https://specscore.studio): | [Explore](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/live-daemon-api?op=explore) | [Edit](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/live-daemon-api?op=edit) | [Ask question](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/live-daemon-api?op=ask) | [Request change](https://specscore.studio/app/github.com/code-grapher/codegrapher/spec/features/live-daemon-api?op=request-change) |
**Status:** Amending
**Source Ideas:** live-daemon-repository-browser

## Summary

Expose the one repository owned by a foreground server or background daemon
through a bounded, authenticated, versioned HTTP API that the codegrapher.com
repository browser can consume without learning local filesystem paths.

## Problem

The released daemon keeps an index fresh but exposes only its private lifecycle
control endpoint. The landed browser has secure routing and credential transport
for `/browse/<authority>/repos/<repoId>/revisions/<revision>/...`, but no provider
contract or generated client. Connecting them without a provider-owned contract
would make wire shapes drift, while exposing raw index paths or filesystem reads
would turn a read-only browser into an arbitrary local-file service.

## Behavior

The public API is a transport adapter over the existing index/query/freshness
owners. It neither indexes independently nor grants access to unregistered
filesystem roots.

### REQ: contract-source-of-truth

`typespec/main.tsp` MUST be the source of truth for the public
  `/codegrapher/v1/` contract. Its checked-in OpenAPI document and TypeScript
client MUST be generated artifacts, and verification MUST fail when either
drifts.

### REQ: generated-client-distribution

The generated `@code-grapher/browser-api-client` package MUST be consumable
from an immutable CodeGrapher Git revision without a local filesystem link.
Installing its `clients/typescript` subdirectory MUST build the declared
JavaScript and declaration exports reproducibly from the checked-in generated
source.

### REQ: public-authentication-and-cors

Every public operation MUST require
`Authorization: Bearer <browser credential>`.
  The browser credential is never the private lifecycle-control credential and
the latter MUST never authorize a public request. CORS MUST deny every origin
except configured exact origins. Production `https://codegrapher.com` and the
currently deployed `https://codegrapher.dev` are enabled by default;
local-development origins are opt-in. Preflight and actual
responses MUST share the same decision and credentials MUST never be permitted
with wildcard origin.

### REQ: public-status-compatibility

Authenticated `GET /codegrapher/v1/status` MUST report API version, CodeGrapher
version, capabilities, limits, one-repository count, and aggregate freshness.
This response is the compatibility handshake used to distinguish unreachable,
unauthorized, unsupported API, updating, stale, and ready states. The CLI rejects
an uninitialized repository before opening this API.

### REQ: registered-repository-identity

The initial release MUST register exactly the initialized repository supplied to
  `serve` or owned by the daemon. Its opaque repository ID is persisted with the
index, remains stable across restarts and directory moves, and carries no path.
Repository responses MUST expose only display identity, branch/HEAD metadata,
  index revision, capabilities, and truthful freshness. Absolute paths, control
  endpoints, lifecycle credentials, and daemon state-file locations never cross
the public API.

### REQ: truthful-freshness-and-revision

A revision MUST be an opaque digest of the indexed snapshot. Revision-scoped
reads MUST reject a no-longer-current revision instead of silently returning a
  different snapshot. A stale index remains readable only with `freshness=stale`
  and the last synchronization error/time when available.

### REQ: public-dto-sanitization

Public status, freshness, metadata, and errors MUST be constructed as explicit
transport DTOs. They MUST omit or scrub repository roots, absolute paths, daemon
state/log paths, lifecycle endpoints and credentials, and raw internal errors.
Public `lastError` text MAY describe a stable category and safe repository-relative
path but MUST NOT copy an arbitrary internal error string.

### REQ: traversal-safe-bounded-source

Tree and file reads MUST accept normalized repository-relative paths only, reject
  traversal (including encoded separators, NUL, symlinks escaping the registered
  root, and platform-specific absolute forms), use deterministic ordering, and
  enforce response-size and item-count limits.

### REQ: bounded-query-and-graph

Symbol, search, and graph responses MUST use transport DTOs rather than raw store
objects. Search count, graph depth, nodes, and edges MUST be capped; truncation
and the effective limits MUST be explicit in successful responses.

### REQ: serve-capability-selection

Bare `codegrapher serve` MUST enable every capability compiled into that release.
  If any of `--api`, `--watch`, or `--mcp` is explicitly present, only the named
capabilities MUST run. Selected capabilities MUST share cancellation and joined
shutdown. MCP stdout MUST remain protocol-only.

### REQ: daemon-browser-service

The background daemon MUST run freshness plus the public API and report its
browser endpoint separately from private lifecycle control. The locally rendered
browser link MAY reveal the browser credential; public status and lifecycle
control responses MUST NOT.

### REQ: browser-protocol-boundary

The API listener MUST default to HTTP loopback. The generated client MUST allow
the caller to supply the daemon endpoint and authenticated fetch implementation.
Insecure loopback is supported only when the browser consumer explicitly enables
it; hosted browser use against a non-loopback daemon requires HTTPS termination.
The provider MUST document mixed-content/private-network constraints and MUST NOT
claim the landed website gateway is wired until the consumer replaces its
`contract-pending` adapter.

### REQ: explicit-public-errors

Errors MUST be JSON with a stable code, human message, and request ID. Authentication
  failure, forbidden origin, invalid path, repository/symbol/file not found,
revision change, stale indexed file, and bounds violations MUST be distinct.

## Dependencies

- automatic-index-freshness

## Acceptance Criteria

### AC: contract-generated-from-typespec

**Requirements:** live-daemon-api#req:contract-source-of-truth

**Given** the checked-in TypeSpec source, OpenAPI document, and TypeScript client

**When** contract generation and drift verification run

**Then** both artifacts reproduce byte-for-byte and a changed generated file makes the drift check fail.

### AC: generated-client-installs-from-git

**Requirements:** live-daemon-api#req:generated-client-distribution

**Given** an immutable CodeGrapher Git revision and its `clients/typescript` package

**When** a consumer installs that Git subdirectory and imports the package root

**Then** the declared JavaScript and TypeScript exports exist without a local workspace link or copied DTOs.

### AC: public-api-authenticates-and-confines-origin

**Requirements:** live-daemon-api#req:public-authentication-and-cors, live-daemon-api#req:explicit-public-errors

**Given** configured and unconfigured browser origins plus browser and lifecycle tokens

**When** preflight and public API requests are made

**Then** only the configured origin with the browser token succeeds, with no wildcard credentials or secret-bearing error.

### AC: status-negotiates-api-compatibility

**Requirements:** live-daemon-api#req:public-status-compatibility, live-daemon-api#req:public-dto-sanitization

**Given** ready, updating, and stale freshness plus internal status containing local paths and raw errors

**When** the authenticated public status endpoint is read

**Then** it reports version 1, server version, capabilities, limits, repository count, and exact freshness without exposing any private field or raw path-bearing error.

### AC: repository-identity-is-stable-and-path-free

**Requirements:** live-daemon-api#req:registered-repository-identity

**Given** one initialized repository with a persisted public identity

**When** its server restarts and the repository directory moves

**Then** the same opaque ID is advertised and no success or error body contains an absolute or private daemon path.

### AC: tree-and-file-access-is-traversal-safe-and-bounded

**Requirements:** live-daemon-api#req:traversal-safe-bounded-source, live-daemon-api#req:explicit-public-errors

**Given** indexed files, an escaping symlink, and traversal/absolute/oversized requests

**When** tree and file endpoints are called

**Then** valid entries are deterministic and repository-relative while every escape or bound violation fails with its stable JSON error.

### AC: symbol-search-and-graph-are-bounded

**Requirements:** live-daemon-api#req:bounded-query-and-graph

**Given** an indexed symbol graph larger than each configured response limit

**When** symbol, search, and graph operations run

**Then** their DTOs contain browser-required metadata, effective limits, and truthful truncation without raw internal objects.

### AC: freshness-and-revision-are-truthful

**Requirements:** live-daemon-api#req:truthful-freshness-and-revision, live-daemon-api#req:public-dto-sanitization

**Given** ready, updating, and stale states plus an advertised revision

**When** status/metadata are read and the indexed snapshot changes

**Then** state is reported exactly and the old revision is rejected before any newer content is returned.

### AC: serve-capabilities-compose

**Requirements:** live-daemon-api#req:serve-capability-selection

**Given** bare `serve` and every explicit combination of API, watch, and MCP flags

**When** capability selection and shutdown are exercised

**Then** bare selects all three, explicit flags select only themselves, first failure cancels siblings, all selected services join, and MCP stdout remains clean.

### AC: daemon-separates-browser-and-control-credentials

**Requirements:** live-daemon-api#req:daemon-browser-service, live-daemon-api#req:public-authentication-and-cors

**Given** a ready background daemon

**When** its local browser link, public API, lifecycle control, restart, and stop are exercised

**Then** public and control tokens are non-interchangeable, the public endpoint is ready before start returns, and both listeners close on joined shutdown.

### AC: real-server-browser-api-journey

**Requirements:** live-daemon-api#req:contract-source-of-truth, live-daemon-api#req:public-authentication-and-cors, live-daemon-api#req:public-status-compatibility, live-daemon-api#req:registered-repository-identity, live-daemon-api#req:traversal-safe-bounded-source, live-daemon-api#req:bounded-query-and-graph, live-daemon-api#req:truthful-freshness-and-revision, live-daemon-api#req:browser-protocol-boundary

**Given** a real listener over a temporary initialized repository and a real browser page on a configured local-development origin using the generated client

**When** the browser walks status negotiation, authentication, repository discovery, revision tree/file/symbol/search/graph reads, and a traversal attempt

**Then** every real HTTP response matches the generated contract and shutdown leaves no listener or process; this proves provider browser compatibility, not completed wiring of the landed website gateway.

## Open Questions

None at this time.

---
*This document follows the https://specscore.md/feature-specification*

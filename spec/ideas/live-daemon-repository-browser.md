---
format: https://specscore.md/idea-specification
status: Specified
---

# Idea: Live daemon repository browser

**Status:** Specified
**Date:** 2026-09-12
**Owner:** alex
**Promotes To:** live-daemon-api
**Supersedes:** —
**Related Ideas:** —

## Problem Statement

How might codegrapher.com browse authenticated repositories and worktrees served by a released CodeGrapher daemon while preserving the existing GitHub provider experience?

## Context

Founder follow-up supplied on 2026-09-12. This work starts only after the automatic-freshness daemon is implemented, released, and verified.

## Recommended Direction

Add a small authenticated, versioned, read-only daemon HTTP API and a shared frontend source-provider abstraction so GitHub and live daemon repositories reuse the same tree, source, symbol, graph, search, breadcrumb, and deep-link UX.

## Alternatives Considered

1. Build a separate remote CodeGrapher UI. Rejected because it would duplicate
   the existing repository browser and let GitHub/live experiences drift.
2. Put the bearer secret in a query string or persist it broadly. Rejected
   because fragments avoid the initial codegrapher.com request and the token
   should remain in the smallest practical client-side session scope.
3. Add a relay/tunnel service first. Rejected because direct browser-to-daemon
   HTTP/JSON is enough to validate the provider boundary and avoids operating a
   new security-sensitive service.

## MVP Scope

Audit daemon and browser architecture; expose authenticated status/repository/tree/file/symbol/graph/search endpoints for registered worktrees; add host and repo routing with fragment-only bearer-secret handling; preserve GitHub routes; cover security, CORS, connection states, and one bounded live-provider integration journey.

## Not Doing (and Why)

- Relay or tunnel service — direct browser-to-daemon connectivity is the target.
- Separate remote UI — the existing CodeGrapher browser remains shared.
- Arbitrary filesystem browsing — only registered repositories/worktrees are exposed.

## Key Assumptions to Validate

| Tier | Assumption | How to validate |
|------|------------|-----------------|
| Must-be-true | The daemon can expose only explicitly registered worktrees without arbitrary filesystem access. | Threat-model repo lookup and path traversal; add authenticated API tests. |
| Must-be-true | Existing browser components can consume a provider interface without breaking GitHub routes. | Audit routing/data access first, then run existing GitHub route tests beside live-provider tests. |
| Should-be-true | Bearer auth plus explicit CORS origins is sufficient for direct browser connectivity. | Exercise preflight, valid/invalid tokens, and document HTTPS/mixed-content constraints. |
| Might-be-true | Current symbol, graph, and search models can be serialized with only bounded transport DTOs. | Map existing query APIs to the browser's actual payload needs before defining endpoints. |


## SpecScore Integration

- **New Features this would create:** live-daemon-api, live-daemon-browser-provider
- **Existing Features affected:** automatic-index-freshness, GitHub repository browser
- **Dependencies:** the CodeGrapher daemon must be implemented, released, and
  verified before this Idea is promoted into an implementation task.

## Open Questions

None at this time.

## Original Prompt

````text
You are an engineer working on CodeGrapher.

Your task is to extend the existing CodeGrapher browser and daemon/server architecture so the web UI can browse repositories served by a running local/remote CodeGrapher daemon, while preserving the existing GitHub-backed browsing model.

The existing browser already supports URLs such as:

```text
https://codegrapher.dev/github.com/specscore/specscore-cli
```

We now want to support live CodeGrapher instances using URLs like:

```text
https://codegrapher.dev/<ip-address-or-domain>#secret=<SECRET>
```

and:

```text
https://codegrapher.dev/<ip-address-or-domain>/<repo>#secret=<SECRET>
```

The goal is to reuse the current CodeGrapher browser UX and graph/source abstractions as much as possible, not to create a separate application.

## First: audit the existing implementation

Before changing anything, inspect both backend/daemon and web UI.

Determine:

- how `github.com/<org>/<repo>` routes are parsed,
- how repositories are represented in the frontend,
- how source tree browsing works,
- how files are loaded,
- how graph data is loaded,
- how symbols/search/navigation are represented,
- whether there is already a provider abstraction,
- whether GitHub-specific logic is mixed directly into UI components,
- what HTTP/server infrastructure already exists in CodeGrapher,
- whether the daemon already exposes any API or status endpoints,
- what repository/worktree registry already exists,
- what IDs/paths are currently used internally for repositories and worktrees.

Reuse existing abstractions aggressively.

Do not duplicate graph/query logic unnecessarily.

## Target behaviour

When a user opens:

```text
https://codegrapher.dev/devbox.example.com#secret=<SECRET>
```

the existing CodeGrapher web app should interpret `devbox.example.com` as a live CodeGrapher source rather than GitHub.

It should connect to the CodeGrapher daemon running on that host and display the repositories/worktrees exposed by it.

When the user opens:

```text
https://codegrapher.dev/devbox.example.com/datatug#secret=<SECRET>
```

the app should open that repository/worktree directly using the same or very similar browser UI already used for GitHub repositories.

The daemon serves the current indexed state of that repository/worktree.

The watcher/indexer is responsible for keeping that graph current; the web UI should only query the daemon.

## Secret handling

The secret is supplied through the URL fragment:

```text
#secret=<SECRET>
```

This is intentional.

The fragment is read client-side and must not be sent to `codegrapher.dev` as part of the initial HTTP request.

The frontend should extract the secret and use it only when talking to the target daemon.

Use authenticated API calls, for example:

```http
Authorization: Bearer <SECRET>
```

Do not place the secret in query parameters.

Do not log the secret.

Do not persist it unnecessarily.

If the frontend currently has a suitable secure in-memory session abstraction, reuse it.

If persistence is required for navigation/reloads, evaluate carefully and prefer the minimum practical scope.

## API design

Implement a small, coherent daemon HTTP API.

Adapt exact endpoint naming to existing project conventions, but keep responsibilities close to the following.

### Status

```http
GET /api/status
```

Returns information such as:

- CodeGrapher version,
- daemon status,
- API version,
- server identity if needed,
- repository/worktree count,
- index readiness,
- capabilities.

This endpoint may be useful for connection verification and compatibility checks.

### Repositories

```http
GET /api/repos
```

Returns repositories/worktrees exposed by the daemon.

Each entry should contain enough information for the browser to display and open it, for example:

- stable repo/worktree ID,
- display name,
- repository identity,
- worktree identity if applicable,
- branch,
- HEAD commit,
- relative/display path,
- index status,
- last update time if already available.

Avoid exposing unnecessary absolute filesystem paths if they are not required by the UI.

### Repository metadata

```http
GET /api/repos/{repoId}
```

Returns metadata for one repo/worktree.

Include only useful browser-facing information.

### Tree browsing

```http
GET /api/repos/{repoId}/tree?path=<relative-path>
```

Returns directory entries for source browsing.

Support:

- files,
- directories,
- relevant metadata,
- deterministic ordering.

Reuse the same data shape the existing frontend already expects if possible.

### File source

```http
GET /api/repos/{repoId}/file?path=<relative-path>
```

Returns source content and useful metadata.

Potential metadata:

- relative path,
- language,
- size,
- content/version/hash,
- line count,
- current HEAD/worktree state if relevant.

Prevent path traversal.

All paths must remain inside the registered repository/worktree.

### Symbols

Support symbol listing/search and symbol lookup using the existing graph/index.

For example:

```http
GET /api/repos/{repoId}/symbols?q=<query>
```

and:

```http
GET /api/repos/{repoId}/symbols/{symbolId}
```

Use the current internal symbol IDs and models where suitable.

Do not create a second symbol model just for HTTP.

### Graph queries

Expose graph relationships needed by the current browser.

For example:

```http
GET /api/repos/{repoId}/graph?...
```

The exact shape should be based on what the existing frontend already needs.

Potential queries include:

- callers,
- callees,
- references,
- dependencies,
- related symbols,
- incoming/outgoing graph edges.

If the current backend/query layer already has suitable commands or functions, expose those rather than reimplementing them.

### Search

If CodeGrapher already supports repository-wide code/symbol search, expose it through the API.

For example:

```http
GET /api/repos/{repoId}/search?q=<query>
```

Do not build a new search engine as part of this task.

## API principles

The daemon API should be:

- read-only for this phase,
- authenticated,
- versionable,
- simple,
- aligned with existing internal graph abstractions,
- safe against path traversal,
- efficient for browser use.

Avoid exposing arbitrary filesystem access.

The daemon should only expose repositories/worktrees that are explicitly registered or already managed by CodeGrapher.

## Authentication

Implement bearer-token authentication for protected API endpoints.

The daemon should have a secret/token generated or configured through existing daemon/config infrastructure.

If no token infrastructure exists, implement the smallest secure solution.

Requirements:

- high-entropy secret,
- constant-time comparison where appropriate,
- no secret logging,
- clear unauthorised response,
- ability to rotate/regenerate later.

If token rotation does not fit this phase cleanly, document it as follow-up work.

## CORS and browser connectivity

The frontend is loaded from:

```text
https://codegrapher.dev
```

and may connect to another host.

Implement the minimum required CORS policy.

Do not use permissive `*` together with credentials unnecessarily.

Since authentication is bearer-token based rather than cookies, configure explicit allowed origins where practical.

At minimum support:

```text
https://codegrapher.dev
```

Development origins may be configurable for local development.

Document any browser limitations around:

- HTTP vs HTTPS,
- mixed-content blocking,
- localhost/private addresses,
- certificates,
- custom ports.

Do not expand this task into building a relay/tunnel service.

Direct browser-to-daemon connectivity is the target for this phase.

## Frontend routing

Preserve the existing GitHub URL model.

Existing:

```text
/github.com/specscore/specscore-cli
```

must continue to work unchanged.

Add live-host interpretation.

Conceptually:

```text
/github.com/<org>/<repo>
    → GitHub-backed provider

/<host>/<repo>
    → live CodeGrapher daemon provider
```

Do not break GitHub routing.

Prefer introducing or extending a source/provider abstraction if one already exists or can be added cheaply.

For example:

```text
CodeSource
├── GitHubSource
└── LiveCodeGrapherSource
```

The UI should operate on a common repository/source interface wherever practical.

Avoid scattering conditions such as:

```text
if github ...
else if live ...
```

throughout many components.

## Host-only route

For:

```text
https://codegrapher.dev/<host>#secret=...
```

show the list of repos/worktrees returned by:

```http
GET /api/repos
```

Allow the user to open one.

Reuse existing cards/list/tree UI if suitable.

## Repository route

For:

```text
https://codegrapher.dev/<host>/<repo>#secret=...
```

resolve the repo identifier and open it directly.

If repository names are not globally unique within a daemon, use a stable browser-safe ID or canonical slug.

Do not depend on absolute filesystem paths in public URLs unless the existing architecture strongly requires it.

Prefer:

```text
/<host>/datatug
```

over:

```text
/<host>/Users/alex/projects/datatug
```

Internally the daemon may map the repo ID to its worktree path.

## Worktrees

The daemon may expose multiple worktrees for the same repository.

Design for this now.

For example:

```text
datatug/main
datatug/feature-dashboard
```

or another clean stable scheme.

Do not assume one repository equals one filesystem checkout.

The existing daemon/worktree registry should remain authoritative.

## UI reuse

The live repository browser should reuse as much existing GitHub repository UI as possible:

- repository header,
- file tree,
- source viewer,
- symbol navigation,
- graph view,
- callers/callees/references,
- search,
- breadcrumbs,
- deep links.

The data source should change; the browser experience should not be unnecessarily duplicated.

## Loading and connection states

Add clear states for:

- connecting,
- unauthorised,
- daemon unreachable,
- unsupported API version,
- repo not found,
- repo not indexed yet,
- index updating,
- disconnected.

Do not expose raw networking errors directly if they are confusing.

Provide enough technical detail for developers to diagnose failures.

## Deep links

Preserve the possibility of stable deep links.

Design routing so later we can support URLs for:

```text
/<host>/<repo>/file/<path>
/<host>/<repo>/symbol/<id-or-name>
/<host>/<repo>/graph/<symbol>
```

Do not implement every deep-link form unless already easy, but do not design the router in a way that prevents them.

## Security requirements

Review the implementation specifically for:

- path traversal,
- arbitrary file reads,
- token leakage,
- unsafe CORS,
- XSS via source/path/repo names,
- overly broad repository exposure,
- unauthenticated metadata leaks.

The API must not provide arbitrary access to the machine filesystem.

Only registered CodeGrapher repositories/worktrees are addressable.

## Phased implementation

Plan the whole target, but implement progressively.

### Phase 1 — Audit and API contract

Document:

- current GitHub browser architecture,
- current daemon/server architecture,
- provider abstraction opportunities,
- API contract,
- auth approach,
- routing approach.

### Phase 2 — Minimal daemon API

Implement:

- auth,
- `/api/status`,
- `/api/repos`,
- `/api/repos/{repoId}`,
- tree,
- file.

Validate with tests/curl.

### Phase 3 — Existing graph capabilities over API

Expose:

- symbols,
- graph relationships,
- search,

using existing CodeGrapher internals.

### Phase 4 — Frontend live provider

Implement:

- host parsing,
- fragment secret parsing,
- daemon connection,
- repo listing,
- repository opening,
- file tree,
- source display.

Reuse existing browser components.

### Phase 5 — Full graph/browser parity

Connect:

- symbol navigation,
- graph views,
- references,
- callers/callees,
- search,
- breadcrumbs/deep linking,

to the live provider.

### Phase 6 — Polish and robustness

Add:

- connection/error states,
- compatibility/version handling,
- worktree UX,
- security review,
- performance cleanup,
- docs.

## Testing

Add meaningful automated tests.

Backend/API:

- valid token,
- missing token,
- invalid token,
- repo listing,
- repo lookup,
- tree lookup,
- file lookup,
- missing file,
- path traversal attempt,
- unknown repo,
- symbols/graph/search where implemented.

Frontend:

- existing GitHub route still works,
- live host route parsed correctly,
- secret extracted from fragment,
- secret included in API auth,
- host-only repo list,
- repo route,
- unauthorised state,
- unreachable daemon,
- file navigation,
- graph/symbol navigation where implemented.

Add at least one end-to-end or integration path that starts a test daemon and exercises the live browser/API flow if the current test stack makes that practical.

## Performance

The API should serve from the existing CodeGrapher graph/index rather than repeatedly analysing source files.

Avoid unnecessary large payloads.

Use pagination or bounded responses where existing data sets can become large.

Do not prematurely introduce complex streaming/protocol infrastructure unless current graph payloads require it.

HTTP/JSON is preferred for the initial implementation.

## Documentation

Document:

- how to start the daemon/API,
- default host/port,
- how the secret is obtained/configured,
- example browser URL,
- API endpoints,
- CORS/development setup,
- current limitations,
- security considerations.

Include examples such as:

```text
https://codegrapher.dev/devbox.example.com#secret=<SECRET>
```

and:

```text
https://codegrapher.dev/devbox.example.com/datatug#secret=<SECRET>
```

## Important architectural rule

Do not create a separate “remote CodeGrapher UI”.

The current `codegrapher.dev` application should become capable of browsing multiple source providers.

GitHub is one provider.

A live CodeGrapher daemon is another provider.

The repository browser, source viewer, graph browser, and symbol UX should remain shared.

## Working style

Work autonomously.

Inspect the implementation before making assumptions.

Reuse existing abstractions wherever possible.

Do not stop for minor ambiguity.

Make reasonable decisions, document them, and continue.

Only stop if there is a genuinely blocking security or architecture issue.

Implement the simplest secure end-to-end path first, then build toward feature parity with the existing GitHub-backed browser.
````

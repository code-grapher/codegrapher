# Browser API contract

`typespec/main.tsp` is the source of truth for the authenticated public API.
Run `pnpm generate:contract` from the repository root to regenerate:

- `api/openapi/v1/openapi.yaml`
- `clients/typescript/`

`pnpm check:contract` regenerates both outputs and fails when the checked-in
artifacts drift. `pnpm build:client` type-checks and builds the generated client.

The generated `@code-grapher/browser-api-client` accepts a bearer credential and
an `endpoint` option:

```ts
const credential = { getBearerToken: async () => fragmentSecret }
const client = new V1Client(credential, {
  endpoint: 'http://127.0.0.1:7331/codegrapher/v1',
  allowInsecureConnection: true,
})
```

The generation command applies two checked, deterministic compatibility fixes
to the preview TypeSpec JavaScript emitter/runtime: it makes the advertised
`endpoint` option effective and prevents the explicit HTTP-loopback warning
from probing an absent browser `process` global. The drift check fails if the
upstream emitted shape changes, forcing the compatibility layer to be reviewed.

The caller must confine the credential to the advertised daemon origin and API
path. The landed web transport at
`code-grapher/codegrapher-dev` commit
`d4b0d9f266db6753f22d92cb1d58e83bab538411` already provides that confinement;
its gateway remains `contract-pending` until the generated package is wired in a
separate consumer change.

The local API defaults to HTTP loopback. A browser must explicitly opt into that
mode. A non-loopback or hosted-browser connection requires HTTPS termination and
may also be subject to the browser's private-network-access policy.

Production origins `https://codegrapher.com` and `https://codegrapher.dev` are
allowed by default. Add exact local-development origins with repeatable
`serve --cors-origin` flags or the comma-separated
`CODEGRAPH_BROWSER_CORS_ORIGINS` environment variable (also used by the daemon).

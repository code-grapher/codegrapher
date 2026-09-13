# Browser API contract

`typespec/main.tsp` is the source of truth for the authenticated public API.
Run `pnpm generate:contract` from the repository root to regenerate:

- `api/openapi/v1/openapi.yaml`
- `clients/typescript/`

`pnpm check:contract` regenerates both outputs and fails when the checked-in
artifacts drift. `pnpm build:client` type-checks and builds the generated client.

The generated `@code-grapher/browser-api-client` plugs directly into the
confining `{ baseUrl, fetch }` transport already landed in the browser UI:

```ts
import { createV1ClientFromTransport } from '@code-grapher/browser-api-client'
import { createLiveDaemonTransport } from '@codegrapher/ui'

const transport = createLiveDaemonTransport(authority, fragmentSecret, {
  allowInsecureLoopback: true,
})
const client = createV1ClientFromTransport(transport)
```

The adapter never receives the browser secret. Authentication and origin/path
confinement remain owned by the caller-supplied `fetch`, while the generated
client owns paths, parameters, and wire DTOs. Low-level callers may instead
construct `V1Client` with a bearer credential plus `endpoint` and
`allowInsecureConnection` options.

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
mode. Clear-text serving is refused for wildcard and non-loopback binds.

For direct HTTPS, supply all three settings and make the advertised port match
the listen port (an authority without a port means 443):

```sh
codegrapher serve --watch --api \
  --api-listen 0.0.0.0:443 \
  --api-tls-cert /path/to/fullchain.pem \
  --api-tls-key /path/to/private-key.pem \
  --api-public-authority graph.example.com
```

Startup validates the key pair, certificate validity, and DNS/IP SAN before it
opens the listener. It never falls back to HTTP or prints a browser link after
validation failure.

For a trusted reverse proxy or tunnel, keep CodeGrapher on loopback and mark
external termination explicitly:

```sh
codegrapher serve --watch --api \
  --api-listen 127.0.0.1:7331 \
  --api-external-tls \
  --api-public-authority graph.example.com
```

The proxy/tunnel must forward `https://graph.example.com/codegrapher/` to
`http://127.0.0.1:7331/codegrapher/`, preserve the `Authorization` header, and
use a certificate trusted for `graph.example.com`. Stop both CodeGrapher and the
proxy/tunnel after use; stopping only one leaves machine state behind. Hosted
browser connections may also be subject to private-network-access policy.

Production origins `https://codegrapher.com` and `https://codegrapher.dev` are
allowed by default. Add exact local-development origins with repeatable
`serve --cors-origin` flags or the comma-separated
`CODEGRAPH_BROWSER_CORS_ORIGINS` environment variable (also used by the daemon).

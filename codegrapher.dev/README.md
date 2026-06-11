# codegrapher.dev

Static promo site + repo viewer for [CodeGrapher](https://github.com/specscore/codegrapher).
No build step: plain HTML/CSS/JS ES modules. Served as Cloudflare Workers static
assets (`not_found_handling: single-page-application` for SPA routing).

## Layout

```
codegrapher.dev/
├── public/                  # everything served to the browser
│   ├── index.html           # landing page (also SPA shell for viewer)
│   ├── style.css            # landing + viewer styles (shared variables)
│   ├── script.js            # copy-to-clipboard (landing only)
│   ├── app.js               # ES module: router + viewer logic
│   ├── favicon.svg
│   └── vendor/
│       └── ingr-codec.js    # inline INGR parser (see ingr-js note below)
├── tests/
│   └── parse-files-ingr.test.mjs   # smoke tests (node --test)
├── testdata/
│   └── go-small-files.ingr  # fixture for tests (from go-small fixture)
├── wrangler.jsonc           # Workers config
└── README.md
```

## Viewer routes

| URL pattern | Behaviour |
|---|---|
| `/` | Landing page (static) |
| `/github.com/{org}/{repo}` | Repo tree viewer — loads `codegrapher/files/files.ingr` |
| `/github.com/{org}/{repo}/{path…}` | File viewer — fetches raw file content |
| `?q={pattern}` | Filters tree (substring or `*` glob); synced to URL via `history.replaceState` |

**Forge allow-list:** only `github.com` is accepted. Paths with `..`, `//`, or
characters outside `[A-Za-z0-9._/-]` are rejected with a clean error.

**SPA fallback:** `wrangler.jsonc` sets `assets.not_found_handling = "single-page-application"`.
Unknown paths (e.g. `/github.com/org/repo`) are served as `index.html`; `app.js`
reads `location.pathname` on load and dispatches to viewer or landing.

## Data contract

The viewer fetches:
- **Tree:** `https://raw.githubusercontent.com/{org}/{repo}/HEAD/codegrapher/files/files.ingr`
  Parsed client-side with the inline INGR parser to build the directory tree.
- **File content:** `https://raw.githubusercontent.com/{org}/{repo}/HEAD/{path}`
  Fetched on demand and rendered in a dark code surface with line numbers.

If `files/files.ingr` returns 404, a friendly "no snapshot yet" message is shown with
the one-liner to create one: `codegraph init && codegraph export`.

## ingr-js availability note

The official `@ingr/codec` library (`github.com/ingr-io/ingr-js`, MIT) exists but
ships no `dist/` directory and has no npm release (as of 2026-06-11). It requires
a vite build step. To preserve the no-build-step rule, `public/vendor/ingr-codec.js`
contains a minimal inline parser derived from the ingr-js source, with a
`TODO: swap-to-ingr-js` comment. When an official ESM/UMD build ships, vendor
that file here instead.

## Running tests

```sh
node --test tests/parse-files-ingr.test.mjs
```

10 tests covering INGR parsing, tree building, and search/filter logic.

## Local preview

```sh
npx wrangler dev
```

Serves `public/` locally with the same asset handling Cloudflare uses in
production. Open the printed `http://localhost:8787` URL.

## Deploy

### Primary: Cloudflare Workers Builds (git-connected, auto-deploy on push)

Deployment is automatic. The repository is connected in the Cloudflare
dashboard under **Workers & Pages → (this Worker) → Settings → Builds**
(Workers Builds), with:

- **Root directory:** `codegrapher.dev/`
- **Deploy command:** `npx wrangler deploy`

On every push to `main`, Cloudflare's builder runs `npx wrangler deploy`
from the root directory, reads `wrangler.jsonc`, and publishes the contents
of `public/`. No manual step is needed.

### Fallback: manual deploy

If you need to publish outside the git pipeline:

```sh
npx wrangler deploy
```

Run it from this directory (`codegrapher.dev/`).

## Custom domain

`codegrapher.dev` is attached to the Worker through the Cloudflare
dashboard: **Workers & Pages → (this Worker) → Settings → Domains & Routes →
Custom Domains**. Cloudflare provisions the TLS certificate automatically
once the domain's DNS is on Cloudflare.

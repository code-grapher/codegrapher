# codegrapher.dev

Static promo site for [CodeGrapher](https://github.com/specscore/codegrapher).
Single page, no build step: plain `index.html` + `style.css` + a tiny
`script.js` (copy-to-clipboard only). Served as Cloudflare Workers static
assets (the new Workers static-assets setup, not legacy Pages).

## Layout

```
codegrapher.dev/
├── public/            # everything served to the browser
│   ├── index.html
│   ├── style.css
│   ├── script.js
│   └── favicon.svg
├── wrangler.jsonc     # Workers config (assets.directory = "public")
└── README.md
```

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

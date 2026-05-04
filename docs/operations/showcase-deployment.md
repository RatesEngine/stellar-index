---
title: Showcase site deployment (`ratesengine.net`)
last_verified: 2026-05-04
status: operator runbook
---

# Showcase site deployment

Operator runbook for shipping `web/showcase` to production. The
showcase is a static-export Next.js app — no server-side runtime
needed at the edge — so any static-asset host works. This doc
walks through the recommended Cloudflare Pages path; alternatives
live at the bottom.

## What the showcase is

`web/showcase/` is a Next.js 15 app configured with
`output: 'export'` in `next.config.mjs`. `npm run build` produces
a fully static `web/showcase/out/` directory: HTML pre-rendered for
every route (including the top-100 coin slugs from
`generateStaticParams` at build time), all assets fingerprinted,
no runtime dependency on Node.

The API is contacted **client-side** from the browser to
`https://api.ratesengine.net` — set via the
`NEXT_PUBLIC_API_BASE_URL` env var at build time. Same-origin
hosting is not required and not desirable: keeping the showcase
domain separate from `api.ratesengine.net` means the API can
serve when the showcase is down, and vice versa.

## Build locally

```sh
cd web/showcase
pnpm install
pnpm build
# → web/showcase/out/   (drop this on any static host)
```

Size: ~3–4 MB minified, 100 kB shared JS, ~120 kB per route.

To preview the production build locally:

```sh
pnpm exec http-server -p 8080 web/showcase/out
# open http://localhost:8080
```

## Recommended: Cloudflare Pages

Same vendor as the API CDN (per `cdn-setup.md`), free tier
sufficient, Git-driven deploys.

### One-time setup

1. **Connect the repo.** Cloudflare dashboard → Workers & Pages →
   Create application → Pages → Connect to Git → select the
   `RatesEngine/rates-engine` repository.

2. **Build configuration.**
   - Framework preset: `Next.js (Static HTML Export)`.
   - Build command: `cd web/showcase && pnpm install --frozen-lockfile && pnpm build`
   - Build output directory: `web/showcase/out`
   - Root directory: leave at repo root (CF Pages clones the whole
     repo; the build command CDs into the showcase directory).
   - Node version: 22 (matches `web/showcase/.nvmrc`).
   - Branch: `main` for production; PR previews flip on by default.

3. **Environment variables.**
   - `NEXT_PUBLIC_API_BASE_URL` = `https://api.ratesengine.net`
     (production). Override per-environment for previews if you
     want them pointing at a staging API.

4. **Bind the domain.** Custom domains → Add custom domain →
   `ratesengine.net` (and `www.ratesengine.net` as a redirect
   target). Cloudflare proxies the apex through their edge so a
   single A record (or `@` CNAME flattening) is enough; there's
   no separate origin to point at.

5. **Build settings → Compatibility flags.** No flags needed —
   the build doesn't use `nodejs_compat` because nothing runs at
   the edge.

### What you get

- Every PR gets a preview deployment at
  `<pr-hash>.ratesengine-showcase.pages.dev` — useful for
  reviewing UI changes before merge.
- Production deploys on every push to `main` after the existing
  CI gates pass (the `web/showcase` job in `.github/workflows/`
  validates the build before merge).
- Edge cache TTLs are CF defaults (1 year for fingerprinted assets,
  short for HTML). The `out/_next/static/*` paths are fingerprinted
  so the long TTL is safe.

### How updates flow

```
PR merge into main
  → GitHub Actions runs `web/showcase` job (build + lint)
  → Cloudflare Pages webhook fires, runs build
  → New version deploys to ratesengine.net within ~2 min
```

Rolling back is a single click in the CF dashboard — Pages keeps
every previous build available as a preview URL.

## Security headers + CSP

`web/showcase/public/_headers` ships a Cloudflare-Pages /
Netlify-format header file that's copied verbatim to the build
output. It applies on every response:

- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY` + `frame-ancestors 'none'` CSP
- `Referrer-Policy: strict-origin-when-cross-origin`
- `Permissions-Policy` denying camera / mic / geolocation / payment / USB / accelerometer
- `Content-Security-Policy` restricting `connect-src` to `self` +
  `https://api.ratesengine.net` (so fetch only reaches the API
  origin), with `'unsafe-inline'` allowed on `script-src` /
  `style-src` because Next.js static export emits inline
  bootstrap and Tailwind utility styles
- 1-year `Cache-Control: immutable` on `/_next/static/*` (CF Pages
  defaults to this; the explicit header doubles as documentation
  and is required for Netlify)

If you switch to Vercel, translate `_headers` into a
`vercel.json` headers block — same directives, different syntax.

## Alternative: Vercel / Netlify

Both work identically — same build command, same output directory,
same env var. Netlify reads `_headers` natively; Vercel needs
`vercel.json` per the note above. We picked Cloudflare for vendor
consolidation with the API CDN; Vercel would be marginally faster
on first-paint metrics in their case-study tests, but the
difference disappears with the API on Cloudflare.

## Alternative: rsync to the API host

Possible but not recommended. The showcase is small enough to
serve from nginx on r1, but doing so couples the showcase
availability to the API host's availability — exactly the
property a separate static host avoids. Use only for air-gapped
demo setups.

```sh
cd web/showcase && pnpm build
rsync -av --delete out/ root@r1.ratesengine.net:/var/www/showcase/
```

`/etc/nginx/sites-available/showcase`:

```nginx
server {
    listen 443 ssl http2;
    server_name ratesengine.net;
    root /var/www/showcase;
    index index.html;
    location / {
        try_files $uri $uri/ $uri.html =404;
    }
    location /_next/static/ {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }
}
```

## Verification after deploy

```sh
# Page renders
curl -sI https://ratesengine.net | head -3

# Sitemap is present
curl -sI https://ratesengine.net/sitemap.xml | head -3

# /coins/USDC pre-rendered (not a 404)
curl -sI https://ratesengine.net/coins/USDC/ | head -3

# Robots is correct
curl -s https://ratesengine.net/robots.txt
```

If the build environment couldn't reach the API at build time, the
coin detail pages will fall back to the seed-only set
(7 slugs vs ~100). Re-run the build with the API reachable and
re-deploy.

## Touchpoints

- API CDN setup: [cdn-setup.md](cdn-setup.md)
- Status page (separate site): [`deploy/status-page/README.md`](../../deploy/status-page/README.md)
- Public-flip checklist: [public-flip.md](public-flip.md)
- Launch-day checklist: [launch-day-checklist.md](launch-day-checklist.md)

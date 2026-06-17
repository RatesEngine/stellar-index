# Cloudflare Pages — bootstrap

Provisions the customer-facing surfaces on Cloudflare Pages, plus
DNS + the `api.stellarindex.io` proxy:

| Surface | Project | Domain |
|---|---|---|
| Explorer (incl. in-site `/account`) | `stellarindex-showcase` | `stellarindex.io`, `www.stellarindex.io` |
| Status page | `stellarindex-status` | `status.stellarindex.io` |
| API docs | `stellarindex-docs` | `docs.stellarindex.io` |
| Dashboard redirect (retired) | `stellarindex-app` | `app.stellarindex.io` → `301 /account` |
| API (proxied) | (n/a — Caddy on r1) | `api.stellarindex.io` → `136.243.90.96` |

> **The CF Pages projects were renamed `ratesengine-*` → `stellarindex-*`
> on 2026-06-17.** Because CF Pages project names are immutable, the
> "rename" was a recreate-move-over: new direct-upload `stellarindex-*`
> projects were created + deployed, the `stellarindex.io` custom domains
> detached from the old projects and re-attached to the new ones (DNS
> CNAMEs repointed), and the old `ratesengine-*` projects deleted. The
> deploy workflows (`explorer-deploy.yml`, `status-page.yml`) target the
> `stellarindex-*` names.

> **The new projects are DIRECT-UPLOAD, not git-connected.** Deploys
> happen only via the `workflow_dispatch` workflows (wrangler
> `pages deploy`), never auto-on-push. (The old projects were
> git-connected and auto-deployed on every push, which is why they
> accrued ~2,000 deployments each.) Creating a git-connected project via
> the API fails on this account — `8000011 internal issue with your
> Cloudflare Pages Git installation` — so direct-upload is the supported
> path here.

> **The standalone customer dashboard was retired (2026-06-17).** Login +
> the account/keys/usage/settings/admin surfaces now live *in-site* at
> `stellarindex.io/account/*` (explorer routes). `app.stellarindex.io`
> now 301-redirects to `/account` via the tiny `stellarindex-app` Pages
> project (a single `_redirects` file — `/* https://stellarindex.io/account 301`),
> used because a zone-level redirect rule needs a Rulesets-scoped token
> this account's deploy token lacks.

> **The legacy `ratesengine.net` brand domains were dropped (2026-06-17).**
> Every old project also carried `*.ratesengine.net` custom domains; they
> were detached and now return CF 522 (their zone CNAMEs still point at
> the now-deleted projects). To make them `NXDOMAIN`, delete the
> `ratesengine.net` zone DNS records (separate zone; not in the
> stellarindex.io-scoped deploy token).

## One-time prerequisite (already done)

Cloudflare's GitHub app must be authorised against the
`StellarIndex` org once. Visit
`https://dash.cloudflare.com/<account-id>/pages/new/connect` and
click through the OAuth grant. After that, every project this
script creates can wire its `git source` programmatically — no
further dashboard clicks.

## Run it

```sh
# 1. Create a scoped API token at:
#      https://dash.cloudflare.com/profile/api-tokens
#    Permissions:
#      Account → Cloudflare Pages → Edit
#      Account → Account Settings → Read
#      Zone    → Zone → Read
#      Zone    → DNS → Edit
#    Zone resources: Include → Specific zone → stellarindex.io
#    Account resources: Include → <your account>

export CLOUDFLARE_API_TOKEN=cf_pat_...
export CLOUDFLARE_ACCOUNT_ID=...

# 2. Dry-run (shows the JSON bodies, no changes):
DRY_RUN=1 bash scripts/ops/cf-pages-bootstrap.sh

# 3. For real:
bash scripts/ops/cf-pages-bootstrap.sh
```

The script is **idempotent** — re-runs just verify state and
patch any drift. Safe to run from CI on every change to
`scripts/ops/cf-pages-bootstrap.sh` itself.

## What it does

1. Verifies the API token + looks up the `stellarindex.io`
   zone (warns + skips DNS if the zone isn't on Cloudflare).
2. For each of the three Pages projects: creates it pointing
   at `StellarIndex/stellar-index` with the right
   `root_dir` / `build_command` / `output_dir` / env vars,
   or PATCHes the existing project to converge on those
   values.
3. Attaches the production custom domain (and `www.` for the
   showcase).
4. Upserts the four CNAME / A records (proxied) at the zone.

After the first successful run, every push to `main` triggers
an automatic deploy on each Pages project (the git integration
handles it; no GitHub Actions minutes consumed).

## Verify

```sh
# Pages dashboard
open "https://dash.cloudflare.com/${CLOUDFLARE_ACCOUNT_ID}/pages"

# DNS resolution (should return Cloudflare IPs once propagated)
dig +short stellarindex.io status.stellarindex.io
dig +short api.stellarindex.io   # → 136.243.90.96 behind orange-cloud

# Surface health
curl -sI https://stellarindex.io | head -3
curl -sI https://status.stellarindex.io | head -3
curl -s  https://api.stellarindex.io/v1/healthz
```

## Enabling in-site login (the `/account` auth flow)

The magic-link handlers (`POST /v1/auth/login` + callback + logout)
mount **only** when `cfg.API.Dashboard.BaseURL` is non-empty AND Postgres
is reachable (see `internal/api/v1/server.go` `s.dashboardAuth != nil`).
On r1 today `[api.dashboard]` is absent, so `/v1/auth/login` returns 404
and `/account` shows the signed-out shell. To turn login on:

1. **Set the dashboard + cross-origin config** in `/etc/stellarindex.toml`
   on r1. Note the section split — `base_url`/`cookie_*` are
   `[api.dashboard]` keys; `allow_credentials`/`allowed_origins` are
   `[api]` keys. Putting the cookie keys under `[api]` crash-loops the API
   ("config: unknown keys"):

   ```toml
   [api]
   allowed_origins   = ["https://stellarindex.io"]
   allow_credentials = true

   [api.dashboard]
   # In-site account lives on the apex now — NOT app.stellarindex.io.
   base_url           = "https://stellarindex.io/account"
   email_from         = "Stellar Index <hello@stellarindex.io>"
   resend_api_key_env = "STELLARINDEX_RESEND_API_KEY"
   cookie_secure      = true
   # Apex-issued cookie must be readable by the api. subdomain (the
   # browser calls api.stellarindex.io with credentials), so scope it
   # to the parent zone. Pairs with SameSite=None;Secure (rc.109+).
   cookie_domain      = ".stellarindex.io"
   ```

2. **Add the Resend API key** to `/etc/default/stellarindex`:

   ```sh
   STELLARINDEX_RESEND_API_KEY=re_...
   ```

   Then `systemctl restart stellarindex-api`. Cross-origin cookies
   (apex page → `api.` XHR) need the `SameSite=None` cookie helper added
   **after the rc.109 tag** (commit 9ab579c0) — so a **rc.110+ binary is
   required**; rc.109 (currently on r1) still sets `SameSite=Lax` and the
   browser drops the cookie on the cross-subdomain call. Cut + deploy
   rc.110 before flipping login on.

## Fallback paths

- **Per-project Wrangler CLI deploy** — the new projects are
  direct-upload, so this is the primary path (not a fallback):
  ```sh
  cd web/explorer && pnpm build && \
    wrangler pages deploy out --project-name stellarindex-showcase
  # docs is a static dir, no build:
  wrangler pages deploy docs/reference/api --project-name stellarindex-docs
  ```
- **GitHub Actions workflow** — `explorer-deploy.yml` (explorer) and
  `status-page.yml` (status) deploy via `workflow_dispatch`. Trigger via
  `gh workflow run explorer-deploy.yml --ref main`. Both target the
  `stellarindex-*` project names.

## Troubleshooting

- **`source.config.production_branch` rejected** — you're
  hitting the legacy Pages API; the script uses the v4 endpoint
  which understands the new shape. If you've hand-edited
  `cf-pages-bootstrap.sh` to use older field names, revert.
- **Token verification 403** — token scopes are missing one of
  `Pages:Edit` / `Zone:Read` / `DNS:Edit`. Mint a new token.
- **DNS records create but the site doesn't load** — the
  custom-domain attachment can take ~30 s after project create
  for CF to issue the cert. Check
  `https://dash.cloudflare.com/<account>/pages/view/<project>/domains`.

## Dashboard retirement (`app.stellarindex.io`) — DONE 2026-06-17

The standalone dashboard was consolidated into the explorer's
`/account/*` routes; `web/dashboard/` was removed from the repo (CI job +
Makefile targets too). `app.stellarindex.io` now **301-redirects to
`https://stellarindex.io/account`** via the `stellarindex-app` Pages
project, whose only content is a `_redirects` file:

```
/*  https://stellarindex.io/account  301
```

(A zone-level dynamic-redirect rule would be tidier but needs a
Rulesets-scoped token; the deploy token only has Pages/DNS/Zone-read.)

## The rename, as executed (2026-06-17)

`ratesengine-*` → `stellarindex-*`, recreate-move-over (CF Pages names
are immutable). The exact sequence, for the next time a Pages project
needs renaming:

1. **Pre-create** the new direct-upload project:
   `POST /accounts/{acct}/pages/projects {"name":"stellarindex-X","production_branch":"main"}`
   (omit `source` → direct-upload; git-connected create fails with
   `8000011` on this account).
2. **Deploy** content to it (`wrangler pages deploy <dir> --project-name
   stellarindex-X`, or the `workflow_dispatch` deploy workflow) and
   verify `stellarindex-X.pages.dev` serves.
3. **Move each custom domain** (a hostname can only be on one project):
   `DELETE …/projects/ratesengine-X/domains/<host>` →
   `POST …/projects/stellarindex-X/domains {"name":"<host>"}` →
   `PATCH /zones/{zid}/dns_records/{id} {"content":"stellarindex-X.pages.dev"}`.
   Activation + cert on the new project is ~30–60 s (expect transient
   `522` during the window). Do the lowest-traffic host first as a canary.
4. **Delete the old project.** Blocked by `8000076 too many deployments`
   until you purge them — git-connected projects accrue ~1 deployment per
   push (these had ~2,000 each). Loop `DELETE
   …/projects/X/deployments/{id}?force=true` over every page, then delete
   the project. (`/tmp/cf_purge.sh`-style; pace ~4 req/s for the API rate
   limit.)

`docs.stellarindex.io` was **dead** before this (no project claimed it;
the API's own RFC-9457 404 body points users there) — fixed by standing
up `stellarindex-docs` (serves `docs/reference/api/`) and attaching the
hostname + a fresh CNAME.

# Rates Engine — public status page

Public status page for the Rates Engine API. Lives at
`status.ratesengine.net` post-launch.

This is the shipped implementation of the status-page surface
chosen in
[`docs/architecture/status-page-hosting-comparison.md`](../../docs/architecture/status-page-hosting-comparison.md).
The original 2026-04-30 recommendation was Instatus; the project
ultimately built this self-hosted Cloudflare Pages app because
the customer-webhook fan-out (F-1249) made push the primary
notification channel and Markdown-as-source-of-truth gave us one
incident corpus driving both the public page and the webhook
payload.

## Stack

- [Next.js 15](https://nextjs.org) — app router, RSC default
- TypeScript (strict)
- [TailwindCSS](https://tailwindcss.com)
- [lucide-react](https://lucide.dev) — icons

**Static export only** (`output: 'export'` in `next.config.mjs`).
Deployed to Cloudflare Pages on every push to `main` via the
Pages git integration (no GitHub Actions minutes consumed). Setup
in [`docs/operations/status-page-setup.md`](../../docs/operations/status-page-setup.md).

## Where the incidents come from

The page renders from the same incident corpus the API binary
embeds at compile time. One source of truth, one workflow:

```
internal/incidents/data/<YYYY-MM-DD>-<slug>.md
                       │
                       ├─→ web/status/src/lib/incidents.ts
                       │   (build-time loader; static export)
                       │
                       └─→ internal/incidents/incidents.go
                           (//go:embed data/*.md; served at
                            /v1/incidents and pushed via the
                            customer-webhook fan-out at
                            incident.sev1 / incident.resolved)
```

`src/lib/incidents.ts` reads the Markdown files at build time,
parses the YAML frontmatter, and pre-renders every
`/incident/[slug]` page into the static export. The runtime API
at `/v1/incidents` serves the same corpus from the Go binary's
`go:embed` block.

The frontmatter contract is pinned in
`internal/incidents/data/_template.md` — keep both loaders in
sync if you change it.

## Authoring an incident

End-to-end procedure in
[`docs/operations/runbooks/sev-status-page-update.md`](../../docs/operations/runbooks/sev-status-page-update.md).
Quick version:

1. Copy `internal/incidents/data/_template.md` to
   `internal/incidents/data/<YYYY-MM-DD>-<slug>.md`.
2. Fill in frontmatter (`title`, `severity`, `status`,
   `started_at`, `affected_components`).
3. Commit + push to `main`. Cloudflare Pages picks up the change
   in 30-90 s; the API binary's next deploy embeds it.
4. To fan out via customer-webhooks, run
   `ratesengine-ops emit-incident --slug <slug>` (fires the
   `incident.sev1` / `incident.resolved` event family from
   F-1249).

## Local dev

```sh
cd web/status
pnpm install
pnpm dev    # http://localhost:3002
```

The dev server reads from `../../internal/incidents/data/` via
the same loader, so authoring is hot-reload-ish (next page render
picks up changes; no hot-replace because the loader caches per
process — restart `pnpm dev` after editing a Markdown file).

## Build

```sh
pnpm build      # static export to ./out
pnpm typecheck  # tsc --noEmit
pnpm lint       # next lint
```

`make verify` at the repo root runs all three across all three
web apps (explorer, dashboard, status) so the status page can't
regress without the canonical pre-push gate catching it.

## Deploy

Cloudflare Pages git integration. No GitHub Actions step. The
`name` and `pages_build_output_dir` in `wrangler.toml` are the
only deploy-side config; everything else is handled by Pages
project settings (set up once per environment per
`docs/operations/status-page-setup.md`).

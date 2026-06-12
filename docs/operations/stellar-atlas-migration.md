# Stellar Atlas migration — plan + runbook

**Decision (2026-06-12):** the product rebrands from **Rates Engine**
(ratesengine.net) to **Stellar Atlas** (stellaratlas.xyz). Positioning
changes with it: Stellar Atlas is a **protocol explorer for the Stellar
network** — deep, verified, per-protocol on-chain data (contracts,
events, prices) — with the pricing API as one of its products, evolving
toward a **comprehensive blockchain explorer** (native + Soroban).
Recorded as ADR-0036.

Decisions locked with the operator:

- Go module path: `github.com/StellarAtlas/stellar-atlas`
- Binaries: `stellaratlas-*` (indexer, aggregator, api, ops, migrate, sla-probe)
- `audit-fixes-tier0` merges to main FIRST; rebrand lands on the merged base
- Scope: full migration including the live r1 cutover

## Survey (what the rename touches)

~900 of 2,399 tracked files. The persisted-state surfaces that need a
deliberate decision (not blind sed):

| Surface | Current | Action |
|---|---|---|
| Go module path | `github.com/RatesEngine/rates-engine` | rename in go.mod + every import |
| Binaries / cmd dirs | `ratesengine-*` | rename dirs + Makefile + workflows + systemd |
| Prometheus metrics | `ratesengine_*` namespace | rename to `stellaratlas_*` + ALL rule files + runbooks (history discontinuity accepted — pre-launch, no consumers) |
| Env vars | `RATESENGINE_*` | rename to `STELLARATLAS_*` + r1 /etc/default files |
| Postgres role + db (r1) | `ratesengine`/`ratesengine` | rename during cutover (services stopped) |
| Redis keys | no brand prefix | no action |
| DB cursor/source names | no brand | no action |
| MinIO buckets | brand-free (galexie) | no action |
| ClickHouse db | `stellar` | no action |
| User-Agents | `ratesengine/1.0`, `rates-engine/...` | rename |
| Emails | security@ratesengine.net | security@stellaratlas.xyz (mailbox: operator) |
| Domains | ratesengine.net (Cloudflare) | stellaratlas.xyz; Caddy serves BOTH until DNS + Pages flip |
| GitHub | RatesEngine/rates-engine | repo rename now; org `StellarAtlas` creation + transfer = operator step (redirects persist) |

**Immutable archives are NOT rewritten**: `docs/adr/0001-0035`,
`docs/discovery/`, `docs/audit-*/`, `CHANGELOG.md` history, and dated
blog posts keep the old name as historical record (repo policy: ADRs are
immutable). Their READMEs get a one-line banner pointing at ADR-0036.
Everything *living* is renamed.

## Phases

1. **Merge** `audit-fixes-tier0` → main (verify-green, ~45 commits).
2. **Module path + imports** — mechanical, whole-repo; build+tests prove it.
3. **cmd/ renames + build plumbing** — Makefile, release/deploy workflows,
   scripts, version ldflags.
4. **Go-level brand strings** — metric namespace (+ every rule file +
   runbook + dashboards), env prefix, User-Agents, OpenAPI metadata,
   emails.
5. **configs/ + deploy/** — ansible roles/units, prometheus jobs+rules,
   alertmanager, Caddy (both domains), loki, healthchecks, docker-compose.
6. **web/** — explorer, status, dashboard: branding, domains, copy.
7. **Docs + repositioning** — README + CLAUDE.md rewritten around the
   protocol-explorer identity; SECURITY/CONTRIBUTING; docs/protocols pages;
   archive banners; ADR-0036; CHANGELOG.
8. **Verify** — full `make verify`; fix fallout (lint-imports module path,
   docs lint, golden tests).
9. **Git** — staged commits on main; `gh repo rename stellar-atlas`; push.
10. **r1 live cutover** (separate checklist below).
11. **Post** — operator follow-ups (DNS, Cloudflare Pages, GitHub org,
    mailbox), memory/docs updates.

## r1 cutover checklist (phase 10)

Pre-built `linux/amd64` binaries scp'd to r1 (no GH release needed for
the cutover; next tag ships under the new names).

1. Stop + disable `ratesengine-*` units (indexer, aggregator, api,
   sla-probe, smoke timer). Galexie/MinIO/Postgres/CH/Redis untouched.
2. Postgres: `ALTER ROLE ratesengine RENAME TO stellaratlas` (+password
   re-set), `ALTER DATABASE ratesengine RENAME TO stellaratlas`.
3. Apply migrations 0057–0061 (the audit-fix PK migrations + protocol_contracts).
4. `/etc/default/ratesengine-*` → `/etc/default/stellaratlas-*` with
   `RATESENGINE_*` → `STELLARATLAS_*` var renames; TOML DSN updates.
5. Install `stellaratlas-*.service` units + binaries to /usr/local/bin;
   `daemon-reload`; enable + start; remove old unit files.
6. Drop the sla-probe interim `-freshness-target 150s` flag (memory:
   incidents 2026-06-11).
7. Prometheus: r1 scrape config job renames + rules.r1 swap; restart.
   Alertmanager: apply.sh with renamed routes. Loki/promtail labels.
8. Caddy: add stellaratlas.xyz + api.stellaratlas.xyz alongside the old
   domains (DNS does not exist yet — Caddy retries issuance until the
   operator creates DNS; old domains keep serving meanwhile).
9. `stellaratlas-ops seed-protocol-contracts -source blend` (ADR-0035
   deploy precondition) + verify gated sources.
10. Smoke: `r1-smoke.sh` against localhost:3000; check metrics flowing
    under `stellaratlas_*`; check healthchecks pings still landing (ping
    URLs are UUID-based — display names renamed in the UI later).
11. Deferred (documented, not in cutover): historical TRUNCATE +
    re-derives for 0057–0060 tables + blend/soroswap pre-gate purge
    (long-running ch-rebuild jobs; run after cutover).

## Operator follow-ups (can't be done from here)

- **DNS**: create stellaratlas.xyz zone (Cloudflare suggested) — apex +
  `www` → Pages, `api` → r1 (136.243.90.96, proxied like today),
  `status` → Pages.
- **Cloudflare Pages**: attach stellaratlas.xyz to the explorer project,
  status.stellaratlas.xyz to the status project; keep old domains as
  redirects until consumers move.
- **GitHub**: create org `StellarAtlas`, transfer the renamed
  `stellar-atlas` repo into it (redirects persist; module path already
  matches).
- **Mailbox**: security@stellaratlas.xyz (SECURITY.md already updated).
- **Healthchecks.io**: rename check display names (slugs/ping UUIDs
  unchanged, so monitoring continuity is unaffected).
- **Local checkout**: `mv ~/code/ratesengine ~/code/stellaratlas` at your
  convenience (note: Claude's per-project memory is keyed by path).

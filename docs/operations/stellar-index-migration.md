# Stellar Index migration — plan + runbook

This doc records the deployment/config rename steps executed when the
project's living surfaces (module path, binaries, metrics, env vars, DB
role/db, domains) moved to the **Stellar Index** naming. It keeps the
operational fidelity — job names, DB names, exactly what changed — so the
mechanical rename is reproducible. Stellar Index is a **protocol explorer
for the Stellar network** — deep, verified, per-protocol on-chain data
(contracts, events, prices) — with the pricing API as one of its
products, evolving toward a **comprehensive blockchain explorer** (native
+ Soroban).

Decisions locked with the operator:

- Go module path: `github.com/StellarIndex/stellar-index`
- Binaries: `stellarindex-*` (indexer, aggregator, api, ops, migrate, sla-probe)
- `audit-fixes-tier0` merges to main FIRST; the rename lands on the merged base
- Scope: full migration including the live r1 cutover

## Survey (what the rename touches)

~900 of 2,399 tracked files. The persisted-state surfaces that need a
deliberate decision (not blind sed):

| Surface | Target | Action |
|---|---|---|
| Go module path | `github.com/StellarIndex/stellar-index` | rename in go.mod + every import |
| Binaries / cmd dirs | `stellarindex-*` | rename dirs + Makefile + workflows + systemd |
| Prometheus metrics | `stellarindex_*` namespace | rename + ALL rule files + runbooks (history discontinuity accepted — pre-launch, no consumers) |
| Env vars | `STELLARINDEX_*` | rename + r1 /etc/default files |
| Postgres role + db (r1) | `stellarindex`/`stellarindex` | rename during cutover (services stopped) |
| Redis keys | no brand prefix | no action |
| DB cursor/source names | no brand | no action |
| MinIO buckets | brand-free (galexie) | no action |
| ClickHouse db | `stellar` | no action |
| User-Agents | `stellarindex/1.0`, `stellar-index/...` | rename |
| Emails | security@stellarindex.io | mailbox: operator |
| Domains | stellarindex.io (Cloudflare) | Caddy serves both old + new until DNS + Pages flip |
| GitHub | StellarIndex/stellar-index | repo rename now; org `StellarIndex` creation + transfer = operator step (redirects persist) |

**Immutable archives are NOT rewritten**: `docs/adr/0001-0035`,
`CHANGELOG.md` history, and dated blog posts keep the old name as
historical record (repo policy: ADRs are immutable). Everything *living*
is renamed.

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
   CHANGELOG.
8. **Verify** — full `make verify`; fix fallout (lint-imports module path,
   docs lint, golden tests).
9. **Git** — staged commits on main; `gh repo rename stellar-index`; push.
10. **r1 live cutover** (separate checklist below).
11. **Post** — operator follow-ups (DNS, Cloudflare Pages, GitHub org,
    mailbox), memory/docs updates.

## r1 cutover checklist (phase 10)

Pre-built `linux/amd64` binaries scp'd to r1 (no GH release needed for
the cutover; next tag ships under the new names).

1. Stop + disable `stellarindex-*` units (indexer, aggregator, api,
   sla-probe, smoke timer). Galexie/MinIO/Postgres/CH/Redis untouched.
2. Postgres: `ALTER ROLE stellarindex RENAME TO stellarindex` (+password
   re-set), `ALTER DATABASE stellarindex RENAME TO stellarindex`.
3. Apply migrations 0057–0061 (the audit-fix PK migrations + protocol_contracts).
4. `/etc/default/stellarindex-*` → `/etc/default/stellarindex-*` with
   `STELLARINDEX_*` → `STELLARINDEX_*` var renames; TOML DSN updates.
5. Install `stellarindex-*.service` units + binaries to /usr/local/bin;
   `daemon-reload`; enable + start; remove old unit files.
6. Drop the sla-probe interim `-freshness-target 150s` flag (memory:
   incidents 2026-06-11).
7. Prometheus: r1 scrape config job renames + rules.r1 swap; restart.
   Alertmanager: apply.sh with renamed routes. Loki/promtail labels.
8. Caddy: add stellarindex.io + api.stellarindex.io alongside the old
   domains (DNS does not exist yet — Caddy retries issuance until the
   operator creates DNS; old domains keep serving meanwhile).
9. `stellarindex-ops seed-protocol-contracts -source blend` (ADR-0035
   deploy precondition) + verify gated sources.
10. Smoke: `r1-smoke.sh` against localhost:3000; check metrics flowing
    under `stellarindex_*`; check healthchecks pings still landing (ping
    URLs are UUID-based — display names renamed in the UI later).
11. Deferred (documented, not in cutover): historical TRUNCATE +
    re-derives for 0057–0060 tables + blend/soroswap pre-gate purge
    (long-running ch-rebuild jobs; run after cutover).

## Operator follow-ups (can't be done from here)

- **DNS**: create stellarindex.io zone (Cloudflare suggested) — apex +
  `www` → Pages, `api` → r1 (136.243.90.96, proxied like today),
  `status` → Pages.
- **Cloudflare Pages**: attach stellarindex.io to the explorer project,
  status.stellarindex.io to the status project; keep old domains as
  redirects until consumers move.
- **GitHub**: create org `StellarIndex`, transfer the renamed
  `stellar-index` repo into it (redirects persist; module path already
  matches).
- **Mailbox**: security@stellarindex.io (SECURITY.md already updated).
- **Healthchecks.io**: rename check display names (slugs/ping UUIDs
  unchanged, so monitoring continuity is unaffected).
- **Local checkout**: `mv ~/code/stellarindex ~/code/stellarindex` at your
  convenience (note: Claude's per-project memory is keyed by path).

## Post-cutover fix (2026-06-13): Prometheus rule-dir drift

The cutover copied the renamed alert rules to
`/etc/prometheus/rules.d/`, but r1's `prometheus.yml` loads
`rule_files: /etc/prometheus/rules.r1/*.yml` — a DIFFERENT directory.
So for ~18h post-cutover, Prometheus kept loading the stale `rules.r1`
copy whose alerts targeted the old (pre-rename) `*-api` job + legacy
metric names — which no longer exist after the metric-namespace rename.
**Every one of the 116 alert rules was silently inert** (an absent-job
`== 0` matches nothing, so `stellarindex_api_down` would never have fired
on a real outage). Fixed by syncing `rules.d` → `rules.r1` + SIGHUP.

**Lesson for future renames:** after swapping rule files, confirm the
LOADED set via `curl localhost:9090/api/v1/rules`, not just the on-disk
copy — and reload via `systemctl reload prometheus` (the HTTP
`/-/reload` lifecycle API is disabled on r1). Alertmanager routing was
unaffected (severity-based, brand-agnostic).

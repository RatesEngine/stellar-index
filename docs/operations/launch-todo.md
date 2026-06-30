---
title: Launch to-do — consolidated master list
last_verified: 2026-06-30
status: living document
---

# Launch to-do — consolidated master list

> **Compiled 2026-06-30** from a full audit: ADR sweep (41 deferred items),
> docs sweep (~55 items across 26 files), live r1 probing, and a code-annotation
> scan (0 `TODO`/`FIXME` in Go — the backlog lives in ADRs/docs, not the code).
>
> **Decisions baked in (operator, 2026-06-30):**
> - **Push to launch.** We've been dragging our heels; launch is the goal. The
>   pre-flip / launch-blocking items below are now top priority.
> - **Multi-region is committed.** We promised R2/R3 (ADR-0008 / ADR-0016 +
>   the 99.99% uptime claim, coverage-matrix S9.1), so Phase 3 stays on the
>   active path, not parked. (Active/active is still v2 per ADR-0008; R2/R3
>   serve + delegate-trust per ADR-0016.)
> - **CoinGecko → paid plan.** Restore the oracle feed (currently dead, see P0-3).
>
> This doc is the **prioritized cross-cut**. The L-numbered detail tracker is
> [`docs/architecture/launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md);
> ADR rationale is in [`docs/adr/`](../adr/). Status legend:
> 🔴 not started · 🟡 ready to start · 🟢 in flight · ⚠ shipped-with-caveat · ✅ done.
> `[OPS]` = operator-scale (heavy / touches prod data); `[code]` = ordinary change.

---

## Phase 0 — Operational fixes (low-hanging fruit, do first)

**Status 2026-06-30: COMPLETE.** P0-1/P0-2/P0-5/P0-6/P0-7 done; P0-4 was a false
alarm; P0-3 code done (operator purchase pending).

> ⏳ **PENDING OPERATOR ACTION:** buy a **CoinGecko Pro** plan (P0-3), then set
> `COINGECKO_API_KEY` on r1 + restart the indexer — the only Phase-0 residual.
> @ash to do later (noted 2026-06-30).

| # | Item | Type | Status |
|---|------|------|--------|
| P0-1 | **`sep1-refresh` systemd timer** — no timer existed; issuer `org_name`/`org_verified` re-froze without it. | [code] | ✅ **DONE** — Ansible templates added (daily 05:12 UTC); installed + enabled on r1; ran once. Completes #46. |
| P0-2 | **`compute-completeness` systemd timer** — no timer; ADR-0033 verdict frozen 17–21 days. | [code] | ✅ **DONE** — daily 05:30 UTC timer + `run-compute-completeness.sh` self-chunking per-source driver (25k windows so the SDEX reconcile never hits ClickHouse's 12 GiB limit; never regresses ahead sources). Installed + enabled; catch-up kicked off. |
| P0-3 | **CoinGecko paid plan** — oracle feed dead 11 days (10k free-tier limit → 429 loop). | [OPS+code] | 🟡 **CODE DONE** — poller now auto-switches to `pro-api.coingecko.com` when a Pro key is set (was a foot-gun: Pro keys 404 on the public host). ⏳ **Operator:** buy the Pro plan, set `COINGECKO_API_KEY` in `/etc/default/stellarindex`, restart indexer. |
| P0-4 | ~~Massive FX poller stalled~~ | [OPS] | ✅ **FALSE ALARM** — FX is healthy; the worker runs in the **API** binary (`fx_quotes persisted rows:797` hourly). The audit misread `observed_at` (data-publish time, not write time). No action. |
| P0-5 | **Prometheus off the 49G root** — 13G of root, chronic >90%. | [OPS] | ✅ **DONE** — relocated to a `data/prometheus` ZFS dataset (zstd 11.87×, 13G→1.31G); root **94%→60%**. Codified in the ZFS role defaults. Prometheus verified healthy. |
| P0-6 | **`nft-drop` syslog spam** — ~10k dropped-packet lines / 200k. | [code] | ✅ **DONE** — rsyslog routes `nft-drop` to a logrotated `/var/log/nft-drop.log` and stops (off syslog). Safe (no firewall edit). |
| P0-7 | **Source-catalogue: `massive` missing from `/v1/sources`** | [code] | ✅ **DONE** — bridged the active FX feed `massive` (the `internal/sources/forex` worker, `fx_quotes` path) into `external.Registry` as an external FX source. Now visible in `/v1/sources` + correctly `IsOnChain=false` (fixed a latent bug where it fell through to on-chain). `coinmarketcap`/`cryptocompare`/`polygon-forex`/`exchangeratesapi` confirmed as intentionally-present disabled **paid** connectors (honest catalogue). Needs an API deploy to show live. |

### Follow-ups surfaced during P0 (tracked, not blockers)
- **Completeness checker false-negative on blend** — the now-running
  `compute-completeness` reconcile flags `blend` `complete=false`
  (`blend_auctions`/`blend_positions`/`blend_emissions`: `expected=0` from the
  event-lake re-derive vs `served=13/1675/6`). These three are
  **state-snapshot-derived** (ADR-0039 contract-state reader), NOT
  `soroban_events`-projected — so the checker shouldn't reconcile them against
  the event lake. The served blend data is present + correct; this is a checker
  source-classification refinement, not a data gap. (Surfaced by P0-2.)
- **FX-path debt** — the X2.5 triangulation forex-snap (`FXQuoteAtOrBefore`) reads the **`trades`** table filtered by `FXSources()` (the disabled connector-path sources), so it *always* soft-falls-back (`AggregatorFXSnapFallbackTotal`). The active FX feed `massive` writes **`fx_quotes`**, a different table. Unify the two FX paths — point the snap at `fx_quotes`, or collapse the redundant `massive`↔`polygon-forex` (same upstream provider). Low impact today (only non-USD-fiat-quoted pairs hit an FX leg).
- **ZFS-dataset drift** — `data/{clickhouse,loki,pgbackrest}` exist on r1 but aren't in the Ansible `zfs_datasets` defaults — reconcile in a dedicated pass.

---

## Phase 1 — Data completeness & backfills (launch-quality data)

`[OPS]` heavy jobs. Each touches prod data and should run in chunks under the
root-<2G watchdog (per the 2026-06-11 CH-log root-fill incident). Sequence with
care; none are instant.

**Verified 2026-06-30 — three of the heaviest items are already DONE** (the
docs that listed them were point-in-time snapshots). Always verify state before
running a multi-hour backfill.

| # | Item | Status |
|---|------|--------|
| P1-1 | F-1265 1-year `prices_1m`/CAGG backfill | ✅ **DONE** — `prices_1m` + `prices_1d` go back to **2015-11-18** (full history, not just 1yr). Superseded by the migration-0031 retention-removal + CAGG recompute. |
| P1-2 | **`/v1/tx` `tx_hash_index`** — ordered lookup + MV + 10.2B-row backfill (perf-todo §4). | 🔴 **REAL pending.** Forward-fix (table+MV+reader) is code; the backfill is heavy. **Low launch priority** — the tx pages are noindex (UX latency only). |
| P1-3 | galexie-archive "frozen at 62.2M" | ✅ **DONE/stale-doc** — archive-completeness daemon verified the archive **current to 63,259,021** today (988,422/988,422 checkpoints), ~10k behind live. `galexie-archive-fill.timer` keeps it synced. |
| P1-4 | **CH Phase 4 — `ch-rebuild-projected`** re-derive projected sources from the lake. | ❓ **Verify post-catch-up** (closes rc.107 mis-keyed-forward data). CH-heavy. |
| P1-5 | **`ch-supply` partition dedup + re-run** — v1 ran `-final=false`; partitions 25/45/62 duplicated → some tokens supply-inflated. | 🔴 **REAL pending — launch-relevant** (we serve market-cap/supply). `OPTIMIZE … PARTITION FINAL` + re-run `ch-supply -write`. CH-heavy. |
| P1-6 | Broad CAGG recompute (after retention migrations) | ✅ **DONE** — the 2015-deep `prices_1m`/`prices_1d` materialization IS this recompute. |
| P1-7 | **SEP-41 historical counterparty re-derive** — CAP-67 topic-shape loss (commit 99d2c2b0) was forward-only; r1-lake-verified ~99.96% mints + 100% clawbacks lost since P23. | 🔴 **REAL pending — launch-relevant** (total_supply undercount). Dispatcher-based re-derive. |
| P1-8 | Data-truth / Phase-C contract-WASM backfill (`state-snapshot -write`). | 🟢 **In progress.** |
| P1-9 | Pre-P20 ClaimAtom + pre-P23 classic-movement coverage. | 🟡 Low-priority historical caveat. |
| P1-10 | **CH Phase 8 — decommission** — drop `soroban_events`/old tables, refactor projector to read CH. | ⏳ **Do LAST** (gated on P1-4 + Phases 5–7). |

**Net real pending P1:** P1-2 (low launch priority), P1-4 (verify), **P1-5 + P1-7
(supply correctness — launch-relevant)**, P1-8 (in progress). The supply items
are the highest-value for launch; both are CH-heavy → run after the
compute-completeness catch-up finishes to avoid stacking lake I/O.

---

## Phase 2 — Launch-blocking infra (pre-public-flip)

| # | Item | Ref | Status | Notes |
|---|------|-----|--------|-------|
| P2-1 | **Pre-launch hardening** — 9 steps before flipping DNS: loopback bind, CORS narrow, Cloudflare proxy, Stripe secret, Healthchecks URLs, FX keys, smoke, backup baseline. | `pre-launch-hardening.md` | 🔴 | Gate for public flip. |
| P2-2 | **Stripe webhook handler** — lifts per-key `RateLimitPerMin` on payment; not built. Email-verification enforcement not built. | r1-deploy-state | 🔴 [code] | Monetization path. |
| P2-3 | **External security review** | L5.6 | 🔴 | Before public. |
| P2-4 | **`/v1/price/tip?...fiat:USD` 404 + USDC/USD≈1.0 synthesis + `min_usd_volume=0` stop-gap** — aggregator/CEX follow-up gaps. | r1-deploy-state | 🟡 [code] | Pricing correctness polish. |
| P2-5 | **Launch-day checklist L6.4 cutover** — DNS flip, enable public rate-limit tier, public-flip, showcase + status go-live, 24h watch. | `launch-day-checklist.md`, L6.4 | 🔴 [OPS] | The flip itself. |
| P2-6 | **API-walkthrough demo (L6.6) + first 24h watch (L6.7)** | L6.6/6.7 | 🔴 | Launch ops. |

---

## Phase 3 — Multi-region (committed — required for the uptime promise)

| # | Item | Ref | Status |
|---|------|-----|--------|
| P3-1 | **R2 (AWS us-east-1) provisioning + bringup** — galexie reads `aws-public-blockchain` S3 direct; Patroni replica off R1; weekly Tier A+D; `api-r2` DNS. | L4.14, ADR-0016 | 🔴 [OPS] |
| P3-2 | **R3 (Vultr) provisioning + bringup** — galexie-archive on Vultr Object Storage hybrid; initial ~6–12h AWS→Vultr bucket fill. | L4.15, ADR-0016 | 🔴 [OPS] |
| P3-3 | **Cross-region DNS** (geo/failover routing) | L4.16 | 🔴 |
| P3-4 | **Cross-region Postgres replication** verify (Patroni standby R2/R3 ← R1) | L4.17 | 🔴 |
| P3-5 | **Region-failover chaos test** | L5.8 | 🔴 |
| P3-6 | **Multi-region cutover runbook execution** | `multi-region-cutover.md` | 🔴 [OPS] |
| P3-7 | **Redis Sentinel ansible sub-role** + fix ha-plan §3.4 Cluster/Sentinel contradiction | ADR-0024 / Task #72 | 🔴 [code] |

---

## Phase 4 — Feature / program backlog (ADR-driven)

Granular-coverage mission ("every event for every major Stellar protocol" — the
standing program goal). Defaults to yes; sequence vs launch.

| # | Item | Ref | Notes |
|---|------|-----|-------|
| P4-1 | **Decoder contract-gating** — Phoenix, DeFindex, Aquarius, Comet. Comet has no factory namespace (open question: pool allowlist or WASM-hash gate). Soroswap+Blend already gated. | ADR-0035 | Each needs seed-protocol-contracts + per-source lake re-derive. |
| P4-2 | **Supply observers** — ClassicSupplyReader production primitive (ADR-0022), SEP-41 per-contract event decoder (ADR-0023, currently a stub), AccountEntry reserve backfill (ADR-0021, interim static). Wire ADR-0011 SEP-1 `max_supply` overlay (dead code today). | ADR-0011/21/22/23 | Unblocks accurate circulating/total/max supply at scale. |
| P4-3 | **Explorer Phase C** — account-state surface `/v1/accounts/{g}` balances + entry-change history backfill. | ADR-0038 | + Phase B participant-index derive (operator backfill). |
| P4-4 | **Anomaly Phase 2/3** — cross-oracle confidence factor (blocks on `internal/divergence` maturity); write `anomaly-freeze-engaged.md` runbook. | ADR-0019 | Phase 1 shipped; Phase 3 is post-launch L7.3. |
| P4-5 | **ADR-0027 LCM cold-tier** — flip the production flag + run the first bulk trim (~3–4TB reclaim, one-shot operator), then the monthly `trim-galexie-archive.timer` (not yet shipped). | ADR-0027, `lcm-cache-tiering.md` | [OPS] |
| P4-6 | **i128 enforcement** — the claimed custom golangci analyzer + BIGINT/DOUBLE-refusing migration check don't exist; build them. | ADR-0003 | Closes a claimed-but-absent invariant guard. |
| P4-7 | **`canonical/strkey.go` SDK conversion** + remaining SCVal decoder stubs (Soroswap/Aquarius/Phoenix off stubs). | ADR-0013 | |
| P4-8 | **TWAP `/v1/chart?price_type=twap`** (currently 400s) · **`change_24h_pct`** on asset detail (L7.7) · **SEP-41 `usd_volume`** pure-Soroban-native shape (L7.6). | ADR-0020 / L7.6/7.7 | Mostly post-launch polish. |
| P4-9 | **Smaller ADR debts** — typed cache-key pkg (ADR-0007), AssetType switch-coverage lint (ADR-0010), CF-range firewall hardening (ADR-0025), DIA mainnet integration (L7.1). | various | Low priority. |

---

## Phase 5 — Explicitly post-launch (park until after flip)

ADR-0004 Tier-1 own-validators (12mo post-launch) + ADR-0012 quorum-set ADR
(placeholder, gated on validators) · L7.2 99.99% uptime measurement · L7.5 GraphQL ·
ADR-0006 Parquet/DuckDB tiered storage · ADR-0007 DragonflyDB/KeyDB revisit ·
ADR-0009 inline-cached JWT verify.

---

## Ongoing — doc hygiene (verify-and-close stale snapshots)

The audit found several point-in-time docs that read as "outstanding" but are
actually resolved — reconcile so the canonical docs stop lying:

- **pgBackRest "Postgres has no backups"** (r1-deploy-state L354) — the
  `pgbackrest-backup.timer` fired today (02:11) + logs exist → **backups ARE
  running**. Update the doc.
- **`sla-probe.timer` "DISABLED"** (r1-deploy-state L720) — a live
  `stellarindex-sla-probe.timer` fired 2m ago → likely resolved. Verify + update.
- **Blend `BackfillSafe=false`** (r1-deploy-state L239) — coverage-matrix S3.6 says
  audit complete 2026-05-02 / `BackfillSafe=true`. Stale snapshot.
- **"completeness cron timer not installed"** (coverage-matrix X1.5) — note this is
  the *archive*-completeness timer (installed); the *source*-completeness timer
  (P0-2) genuinely doesn't exist. Disambiguate both docs.
- General: many architecture docs are dated snapshots (e.g. `stellar-focus-refactor-plan`
  "Status: Proposed, no code yet" while units A–C shipped). Stamp current status.

---

## Quick reference — the 2 cheap timers to add first (P0-1, P0-2)

Both are existing `stellarindex-ops` subcommands with no Ansible timer template.
Add templates under `configs/ansible/roles/archival-node/templates/systemd/`
mirroring `supply-snapshot.timer.j2`, wire into `14-stellarindex-services.yml`,
deploy. This is the literal "cronjobs we never made."

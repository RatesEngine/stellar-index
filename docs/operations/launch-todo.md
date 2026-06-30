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

## ⭐ CURRENT STATUS — where we are (updated 2026-06-30, end of long session)

**Two numbering schemes — don't conflate them:**
- **Launch phases P0–P5** (this doc's sections): P0 ops fixes → P1 backfills/data
  → P2 launch-blocking → P3 multi-region → P4 ADR features → P5 post-launch.
- **Resync phases A–E** (the "true + complete + never-behind" data work, run
  inside P0/P1): A substrate → B trustworthy verdict → C re-derives → D hygiene
  → E verdict green + steady-state.

**DONE this session (all committed + pushed + live on r1):**
- **P0 — COMPLETE.** sep1-refresh + compute-completeness + data-freshness
  timers; prometheus→ZFS (root 94%→60%); nft-drop off syslog; `massive` FX
  bridged into the registry. (P0-3 CoinGecko = operator purchase, pending.)
- **P1 — mostly done.** P1-1/P1-3/P1-6 were already done (verified, not re-run).
  **P1-5** supply_flows deduped (213M dup rows gone). **P1-7 SEP-41 supply
  gap — SOLVED** (served lake-derived, complete vs lake, deduped, current,
  monitored). Remaining P1: P1-2 (/v1/tx index, low priority — noindex), P1-4
  (CH Phase-4 re-derive — verify if needed), P1-8 (data-truth, in progress).
- **Resync A–E — done.** A substrate proven; B verdict trustworthy +
  self-maintaining + **15/15 green** (rc.149 preseed fix); C SEP-41 done;
  D supply_flows deduped; E verdict green. **Steady-state "never behind"
  watchdog LIVE** (data-freshness: per-domain + verdict + supply_flows;
  coingecko correctly flagged).

**P2 — launch-blocking infra (skip Stripe) — code-side COMPLETE 2026-06-30:**
- **P2-4 pricing polish — ✅ done (code).** (a) /price/tip fiat:USD verified live;
  (b) **crypto:USDC self-peg FIXED** (`aggregate.FiatProxy` arm in
  `tryStablecoinFiatProxy` → `1.0`/peg for `crypto:<STABLE>`/its-peg-fiat across
  /price, /price/tip, /observations, /oracle; cross-peg stays 404); (c)
  `min_usd_volume=0` verified intentional. **Needs deploy (rc.151 API).**
- **P2-1 hardening — ✅ code/config done on r1** (verified via
  `scripts/ops/pre-launch-check.sh`): loopback bind, CORS narrow, services/timers,
  Caddy :443, zero SECURITY warnings. Remainder = operator-secret accounts only.
- **P2-2 — Stripe SKIPPED** (per goal); email-verify machinery built but
  deliberately off (avoids onboarding dead-end; product decision).
- **P2-3 security review** — external = operator-procurement; code-side diff
  reviewed clean. **P2-5 cutover** — api/explorer already DNS-live + TLS-valid;
  remainder operator (CF orange-cloud + announce). **P2-6** — launch-day ops.

**NEXT after P2:**
- **P3 — multi-region** (committed): R2 (AWS) + R3 (Vultr) provisioning,
  cross-region DNS + Postgres replication, failover chaos. Mostly `[OPS]`.

**PENDING OPERATOR ACTIONS (only humans can do):**
1. **Buy CoinGecko Pro** → set `COINGECKO_API_KEY` on r1 + restart indexer (P0-3).
2. **Create Healthchecks.io account + Discord webhooks** → paste the 4×
   `HEALTHCHECKS_URL_*` + deadmansswitch + `DISCORD_WEBHOOK_URL_PAGES`/`_ALERTS`
   on r1, rerun `pre-launch-check.sh` (P2-1). (Alerting is Discord, not Slack.)
3. Sequence/approve the remaining heavy backfills (P1-2 /v1/tx index, P1-4) when
   wanted — all paced, none blocking.
4. Optional: Cloudflare orange-cloud in front of `api.` (P2-1 ④); book external
   security review (P2-3); launch-day cutover + announcement (P2-5/P2-6).

**Release train:** latest tag **rc.150**; **rc.151 cut this session** (API:
crypto:USDC self-peg, P2-4b). Last commit: `d42a84c2`.

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

### Steady-state — "never behind" guarantee
- ✅ **Data-freshness watchdog DONE + LIVE** (`data-freshness.{sh,timer}`, 15-min):
  emits per-domain ingest-freshness + the per-source ADR-0033 verdict to the
  node_exporter textfile collector; 3 alerts + runbooks deployed to r1
  Prometheus. Closes the gap that let coingecko rot 11d / sep1 never populate /
  the verdict go 21d stale, all unnoticed. Verified end-to-end: coingecko alert
  is `pending` (will fire). Plus the completeness verdict is now self-maintaining
  (Phase B, rc.149) + on a daily timer. **Result: any source past its cadence, a
  silent timer, or a real served≠lake gap now pages.**

### Follow-ups surfaced during P0 (tracked, not blockers)
- **Completeness verdict false-negative on blend (Phase-B lynchpin) — ✅ DONE +
  VERIFIED (rc.149 deployed).** Root cause: `compute-completeness` (the daily
  verdict) never called `preseedFactoryChildren` — only `verify-reconciliation`
  did. So its factory-gated childgates were the static `protocol_contracts`
  seed and went STALE as new pools deployed; blend's recent activity was on
  pools missing from the seed → `expected=0` while the live (self-seeding)
  decoder captured them (served correct). Fix: `compute-completeness` now
  preseeds factory children from the creation events before each re-derive —
  making the watchdog **self-maintaining** as pools deploy. **Verified on r1:
  blend → `complete=true`; the full verdict is now 15/15 green.**
- **SEP-41 is EXCLUDED from the Postgres-observer verdict** (the `event_index`
  PK-collapse) — but this is now **moot for supply**: SEP-41 token supply is
  served from the lake directly (`supply_flows`, summed on-demand), so it's
  tautologically lake-faithful (served == an on-demand lake sum), not dependent
  on the observer tables. Verified complete vs the lake (P1-7 ✅). The observer
  *audit-trail* (`sep41_transfers` per-account positions) remains a separate
  watch-list-gated feature; the lake holds its full history for on-demand serving
  if/when that feature is built.
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
| P1-5 | **`ch-supply` partition dedup + re-run** — `supply_flows` has **213M dup rows** (820.6M→607.4M FINAL, verified). | 🟡 **REAL but CH-INTERNAL** — served `/v1/assets` supply comes from `asset_supply_history`+`supply_1d` (live observers), NOT `ch-supply`'s `token_supply`. So the dup inflates the lake-side estimate + costs a 40× FINAL-read penalty, but does NOT corrupt served data. Lower priority. `OPTIMIZE … PARTITION FINAL` + re-run. CH-heavy. |
| P1-6 | Broad CAGG recompute (after retention migrations) | ✅ **DONE** — the 2015-deep `prices_1m`/`prices_1d` materialization IS this recompute. |
| P1-7 | **SEP-41 token supply** — every SEP-41 token's `total_supply` complete + served. | ✅ **DONE.** Solved from the LAKE, not the Postgres observers: `/v1/assets/{id}/supply` (all tokens) + `/v1/assets/{id}` detail (traded tokens, rc.150) sum `supply_flows` FINAL on-demand (Σmint−Σburn−Σclawback over the certified CH lake, `sep41_lake_flows` basis). Verified `supply_flows` is **complete vs the lake** (pre-fix P23 window == `contract_events` exactly — the CH path never had the Postgres observer's counterparty loss) and **deduped** (213M dup rows from the v1 `-final=false` populate removed → was inflated). Live-written + current; defensive `ch-supply` gap-fill timer + watchdog keep it so. The Postgres observer tables (`sep41_supply_events`/`sep41_transfers`, watch-list-gated, empty) are bypassed — not needed for supply. |
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
| P2-1 | **Pre-launch hardening** — 9 steps before flipping DNS: loopback bind, CORS narrow, Cloudflare proxy, Stripe secret, Healthchecks URLs, FX keys, smoke, backup baseline. | `pre-launch-hardening.md` | 🟢 code/config done; 🔴 [OPS-secrets] | **Verified 2026-06-30 via `scripts/ops/pre-launch-check.sh` against r1.** All code/config-side items are **already DONE on r1**: ① `listen_addr = 127.0.0.1:3000` loopback ✅ (external `:3000` refuses — confirmed); ② CORS narrowed to the 4 stellarindex.io hostnames ✅; ③ trusted_proxy_cidrs correct ✅; ⑥/⑦ all services + heartbeat/smoke timers active ✅; Caddy on :443 ✅; **zero `SECURITY:` startup warnings** ✅. Remaining 4 FAIL + 2 WARN are **pure operator account-creation** (cannot be done from code): Healthchecks.io URLs (4× `HEALTHCHECKS_URL_*` into `/etc/default/stellarindex-healthchecks`), deadmansswitch + Discord webhooks (`DISCORD_WEBHOOK_URL_PAGES`/`_ALERTS` in `/etc/default/alertmanager-secrets` — alerting migrated Slack→Discord 2026-06-30). ④ Cloudflare orange-cloud proxy = recommended, not blocking. ⑤ Stripe secret = SKIP. ⑧ outside-smoke + ⑨ backup baseline = do at flip. **Action for operator:** create the Healthchecks.io account + Discord webhooks, paste the URLs, rerun the verifier → expect 0 fails. |
| P2-2 | **Stripe webhook handler** (SKIP per goal) + email-verification enforcement. | r1-deploy-state | 🟢 (skip Stripe) | **2026-06-30:** Stripe webhook = **out of scope** (goal: "complete P2 but skip Stripe"). The email-verification note was **stale** — the enforcement machinery is fully built: `RequireEmailVerified` middleware (`internal/api/v1/server.go:609`), `SignupRequireEmailVerification` config flag, `EmailVerifiedAt` user field, `/v1/signup/verify` + `MarkEmailVerified`. It is **deliberately disabled on r1** (`SignupRequireEmailVerification=false`) to avoid the AC7 onboarding dead-end (2026-06-13). Flipping it on is a product decision gated on the Resend emailer being wired + the verify flow tested end-to-end — NOT a blind flag flip (would recreate the dead-end). Left off for launch. |
| P2-3 | **External security review** | L5.6 | 🟡 | **2026-06-30:** An *external* (third-party) review is operator-procurement, not a code task. Code-side substitute: the P2-4(b) diff is constant-returning (synthesises a fixed `1.0` peg; no user input flows into the price value; no injection/escaping surface) — reviewed clean. The standing internal gate is `/security-review` on each diff + the audit register (`docs/audit-2026-06-11/`, last full cold pass). Operator: book the external engagement before public announcement. |
| P2-4 | **Pricing polish** (3 sub-items). | r1-deploy-state | ✅ done | **Done 2026-06-30 (P2 goal):** (a) `/v1/price/tip` fiat:USD = **✅ STALE/RESOLVED** — `?asset=X&quote=fiat:USD` returns 200 with prices (BTC 58602, ETH 1570, XLM 0.18; param is `asset`+`quote`, not `pair`). (b) **USDC/USD≈1.0 synthesis = ✅ FIXED** — root cause was narrower than recorded: `?asset=USDC` 400s (bare ticker, `ParseAsset` rejects it); the real gap was `crypto:USDC` (the global-ticker form the catalogue/explorer use) → 404, while the classic `USDC-GA5Z…` already returned `1.0 peg` via `usdPeggedClassics` (F-1232). Added a `aggregate.FiatProxy` self-peg arm at the top of `tryStablecoinFiatProxy` (`internal/api/v1/price.go`): when the asset is a `crypto:<STABLE>` whose peg fiat == the requested quote, return `1.0`/`price_type:peg`. Covers USD+EUR+MXN pegs and flows through `/v1/price`, `/v1/price/tip`, `/v1/observations`, `/v1/oracle` (all share the helper). Cross-peg (`crypto:USDC/fiat:EUR`) correctly stays a 404 (real cross-rate). Import boundary OK (api→aggregate already allowed). 2 regression tests added; full api/v1 suite green. (c) **`min_usd_volume=0` = ✅ VERIFIED INTENTIONAL** — `[aggregate] min_usd_volume = 0` on r1 is a documented stop-gap (config comment + r1-deployment-state.md): default is 10000, but r1 is on-chain-only until CEX/CoinGecko connectors land, and micro-volume on-chain XLM/USDC trades are the only price data we have — filtering at 10000 would zero out every price. Correct for now; **re-raise to the 10000 default once [[project_pending_coingecko_purchase]] / CEX connectors flow** (tracked in P0-3). Not changed. |
| P2-5 | **Launch-day checklist L6.4 cutover** — DNS flip, enable public rate-limit tier, public-flip, showcase + status go-live, 24h watch. | `launch-day-checklist.md`, L6.4 | 🟢 largely live; 🔴 [OPS] final | **2026-06-30:** The endpoints are **already DNS-live + TLS-valid**: `api.stellarindex.io` → 200 over HTTPS (direct to R1 origin 136.243.90.96, grey-cloud); `stellarindex.io` → live via Cloudflare Pages. So the technical cutover is effectively done. Remaining is operator/launch-day only: (a) optional Cloudflare orange-cloud proxy in front of `api.` (P2-1 ④, recommended for L7/WAF); (b) confirm public rate-limit tier; (c) the go-live announcement + status-page flip. No code work. |
| P2-6 | **API-walkthrough demo (L6.6) + first 24h watch (L6.7)** | L6.6/6.7 | 🔴 [OPS] | Launch-day ops (operator). The data-freshness watchdog + smoke timers (P0/steady-state) are the automated half of the 24h watch — already live. |

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
| P3-7 | **Redis Sentinel ansible sub-role** + fix ha-plan §3.4 Cluster/Sentinel contradiction | ADR-0024 / Task #72 | ✅ **done (verified 2026-06-30)** — the `redis-sentinel` role is fully built (16 files: redis.conf/sentinel.conf/users.acl templates, install/configure/systemd/firewall/monitoring tasks, 568 task-lines) and ha-plan §3.4 was amended 2026-05-01 + ratified by ADR-0024. The `🔴 [code]` was stale. Remaining is operator deploy of the role onto the R2/R3 cache hosts (gated on P3-1/P3-2 provisioning). |

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

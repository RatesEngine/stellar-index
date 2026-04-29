---
title: Launch readiness backlog
last_verified: 2026-04-29
status: living document
---

# Launch readiness backlog

The canonical list of outstanding implementation work between
today and launch. Sourced from:

- [`coverage-matrix.md`](coverage-matrix.md) — RFP × ADR × code
  traceability with status per requirement
- [`docs/discovery/delivery-plan.md`](../discovery/delivery-plan.md) —
  the original 10-week calendar
- ADRs 0017, 0018, 0019 — cross-cutting integrity invariants
  added post-Phase-1
- This session's design discussions (oracle manipulation,
  consistency surfaces, anomaly response)

**Operator decision 2026-04-28: every outstanding item is
launch-blocking.** Items explicitly marked `⏳ post-launch` are the
only deferrals (DIA mainnet, 99.99% uptime measurement, ADR-0019
Phase 3 cross-oracle factor). Everything else ships before
production cutover.

## How to read this doc

- **Phase** — when the item lands relative to the original delivery plan
- **Effort** — engineering days (calendar-time estimate, single owner)
- **Depends on** — prerequisite items by ID
- **Blocks** — what cannot ship without this
- **Owner** — Go package or deployment area
- **Status** — `🟢 in flight` | `🟡 designed, ready to start` | `🔴 not started` | `✅ shipped` | `⏳ post-launch`

Items are grouped by surface (ingest / aggregator / API / ops / validation / finalization).
Within each surface, ordered by dependency.

---

## Ingest layer

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L1.1 | CEX connectors: Binance, Coinbase, Kraken, Bitstamp | Wk 4 | ~5 days | — | L4.1 | `internal/sources/external/<venue>` | 🟢 |
| L1.2 | Chainlink HTTP cross-check connector | Wk 4 | half-day | — | L4.4 | `internal/divergence/chainlink` | 🔴 |
| L1.3 | Asset enumeration / discovery (auto-detect new SEP-41 tokens) — Sniffer + Recorder + `discovered_assets` table; dispatcher integration deferred to follow-up PR | Wk 4 | full day | — | L4.1 | `internal/canonical/discovery`, `internal/storage/timescale/discovery.go` | 🟢 |

## Aggregator layer

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L2.1 | VWAP/TWAP impl across venues + per-pair USD volume threshold | Wk 5 | ~5 days | L1.1 | L3.* | `internal/aggregate` | 🔴 |
| L2.2 | `usd_volume` column populated per trade + FX anchor multiplication | Wk 5 | half-day | L2.1 | L3.* | `internal/aggregate/triangulate` | 🔴 |
| L2.3 | Forex factor snap rule for chained-fiat closed-bucket consistency (ADR-0018) | Wk 5 | half-day | L2.2 | L3.* | `internal/aggregate/triangulate` | 🟡 |
| L2.4 | Phase 1 anomaly thresholds — per-class TOML defaults wired into orchestrator Tick + freeze writer (ADR-0019 stop-gap, see #199 / #226 / #235) | Wk 5 | half-day | L2.1 | L3.1 | `internal/aggregate/anomaly` + config | 🟢 |
| L2.5 | Phase 2 statistical baseline — MAD math + `volatility_baseline_1m` table + refresh worker + aggregator wire-up shipped across 4 PRs (ADR-0019). | Wk 6 | ~3 days | L2.4 | L3.1 | `internal/aggregate/baseline` + migration | 🟢 |
| L2.6 | Multi-factor confidence score on every published price | Wk 6 | ~2 days | L2.5 | L3.1 | `internal/aggregate/confidence` | 🟡 |
| L2.7 | Freeze policy (3-signal AND on closed-bucket only) | Wk 6 | full day | L2.6 | L3.1 | `internal/aggregate/freeze` | 🟡 |
| L2.8 | Multi-window safeguard against frog-boiling (1d/7d/30d MAD) — math primitive shipped; storage + orchestrator wire-up follow | Wk 6 | half-day | L2.5 | — | `internal/aggregate/baseline` | 🟠 |
| L2.9 | Bootstrap (warmup) policy for new assets | Wk 6 | half-day | L2.6 | — | `internal/aggregate/baseline` | 🟡 |
| L2.10 | `internal/divergence/` package — cross-reference vs CoinGecko / CMC / Reflector / Band / Redstone (#204) | Wk 5–6 | full day | — | L2.11, L3.5 | `internal/divergence` | 🟢 |
| L2.11 | Wire `flags.divergence_warning` firing logic (#205) | Wk 6 | half-day | L2.10 | L3.5 | `internal/api/v1/envelope.go` consumers | 🟢 |
| L2.12 | `internal/supply/` package — circulating supply per ADR-0011 (6 PRs landed: skeleton+XLM, classic, SEP-41, hypertable+store, SAC cross-check+alert, SEP-1 overlay) | Wk 6 | ~2 days | — | L3.* (F2 fields) | `internal/supply/`, `internal/storage/timescale/supply.go`, `migrations/0005_*` | 🟢 |

## API layer

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L3.1 | `/v1/price` populated end-to-end (CAGG actually being filled by aggregator) | Wk 7 | (above) | L2.1, L2.7 | launch | `internal/api/v1/price.go` (handler shipped) | 🔴 |
| L3.2 | `/v1/price/tip` rolling-window + last-good-price (ADR-0018) | Wk 7 | half-day | L2.1 | L3.6 | `internal/api/v1/price_tip.go` | 🟢 |
| L3.3 | `/v1/observations` per-source raw (ADR-0018) | Wk 7 | half-day | — | L3.6 | `internal/api/v1/observations.go` | 🟢 |
| L3.4 | F5.3 Batch / bulk-query endpoint | Wk 7 | half-day | L3.1 | — | `internal/api/v1/batch.go` | 🟡 |
| L3.5 | F2.* Market Cap / FDV / Circulating / Max Supply on asset detail (24h volume + change_24h_pct deferred — aggregator-driven) | Wk 7 | full day | L2.12, L2.10 | launch | `internal/api/v1/{assets,assets_f2}.go` | 🟢 |
| L3.6 | SSE streaming infrastructure — heartbeats, Last-Event-ID, multi-pair subscription | Wk 7 | half-day | — | L3.7, L3.8, L3.9, L3.10 | `internal/api/streaming` | 🟢 |
| L3.7 | `/v1/price/tip/stream` SSE | Wk 7 | half-day | L3.2, L3.6 | — | same | 🟢 |
| L3.8 | `/v1/observations/stream` SSE | Wk 7 | half-day | L3.3, L3.6 | — | same | 🟢 |
| L3.9 | `/v1/price/stream` SSE (closed-bucket events) — handler + Hub wiring shipped; aggregator-side Publish lands with L3.1 | Wk 7 | half-day | L3.1, L3.6 | — | same | 🟢 |
| L3.10 | `pkg/client/` Go SDK skeleton (#201) | Wk 7 | half-day | — | — | `pkg/client` | 🟢 |
| L3.11 | Generated API reference via Redocly + GitHub Pages workflow + CI drift guard | Wk 7 | half-day | — | — | `scripts/dev/docs-api.sh`, `.github/workflows/api-docs.yml` | 🟢 |
| L3.12 | SEP-10 protocol implementation (Web Auth) — Validator (Challenge / Verify / VerifyJWT) shipped; HTTP handler wire-up + main.go config-loader follow in a separate PR | Wk 7 | full day | — | — | `internal/auth/sep10/` | 🟢 |
| L3.13 | Envelope flag retrofit (`flags.frozen`, `flags.single_source`) — handler-side wired via `FrozenLooker`; aggregator populates the marker when L2.7 ships | Wk 7 | half-day | L2.7 | — | `internal/api/v1/{envelope,price,server}.go` | 🟢 |
| L3.14 | CDN caching for historical endpoints — origin-side `Cache-Control` middleware applied per ADR-0018 surface (CloudFront / equivalent config follows in deploy track) | Wk 7 | half-day | infra | — | `internal/api/v1/middleware/cachecontrol.go` | 🟢 |
| L3.15 | Self-service onboarding page ([`docs/getting-started.md`](../getting-started.md)) — Pages workflow already deploys it via L3.11 | Wk 7 | half-day | — | — | `docs/getting-started.md` | 🟢 |
| L3.16 | URL discipline OpenAPI lint — query params don't change consistency contract (ADR-0018) | Wk 7 | half-day | — | — | `scripts/ci/lint-openapi-urls/` | 🟢 |

## Operations / infrastructure

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L4.1 | Patroni-managed Postgres ansible role | Wk 8 | full day | — | launch | `roles/postgres-ha` | 🔴 |
| L4.2 | Redis cluster + Sentinel ansible role | Wk 8 | half-day | — | launch | `roles/redis-ha` | 🔴 |
| L4.3 | HAProxy + keepalived ansible role | Wk 8 | half-day | L4.1, L4.2 | launch | `roles/haproxy-keepalived` | 🔴 |
| L4.4 | Prometheus + Grafana + Alertmanager ansible role | Wk 8 | full day | — | launch | `roles/observability` | 🔴 |
| L4.5 | Loki log aggregation ansible role | Wk 8 | half-day | L4.4 | — | `roles/loki` | 🔴 |
| L4.6 | Archive-completeness daemon (PR A: `check`) (#200) | Wk 8 | half-day | — | L4.7 | `cmd/ratesengine-ops archive-completeness check` | 🟢 |
| L4.7 | Archive-completeness daemon (PR B: `fix` with multi-source fallback) (#202) | Wk 8 | half-day | L4.6 | L4.8 | `cmd/ratesengine-ops archive-completeness fix` | 🟢 |
| L4.8 | Archive-completeness daemon (PR C: `verify` mode + systemd timer + Prometheus alerts) (#203) | Wk 8 | half-day | L4.7 | L4.9 | systemd + alert rules | 🟢 |
| L4.9 | Verify-archive `-fail-on-missed` flag (post-bootstrap hardening) | Wk 8 | half-day | L4.8 | launch | `cmd/ratesengine-ops/main.go` | 🟢 |
| L4.10 | Per-region asymmetric trust model wiring (R1 leader, R2/R3 delegate) | Wk 8 | full day | L4.4 | launch | metrics federation + envelope flag | 🟡 |
| L4.11 | Public status page at `status.ratesengine.net` (Statuspage / cstate / equivalent) | Wk 9 | half-day | L4.4 | launch | infra config + status worker | 🟡 |

## SLA validation

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L5.1 | k6 `api_steady_state.js` — 1000 req/min × 100 keys × 30 min | Wk 9 | half-day | L3.* | L5.2 | `test/load` | 🔴 |
| L5.2 | k6 `api_ramp_to_saturation.js` — find the cliff | Wk 9 | half-day | L5.1 | — | same | 🔴 |
| L5.3 | k6 `api_spike.js` — 10× burst recovery < 60s | Wk 9 | half-day | L5.1 | — | same | 🔴 |
| L5.4 | k6 `ingest_peak_ledger.js` — 5× normal event rate × 1 h | Wk 9 | half-day | L1.* | — | same | 🔴 |
| L5.5 | Chaos suite — kill-each-component scenarios | Wk 9 | full day | L4.* | launch | `test/chaos` | 🔴 |
| L5.6 | Security review (external or community) on full stack | Wk 9 | (external) | L3.* | launch | external auditor | 🔴 |
| L5.7 | SEV-1 / SEV-2 dry-run (kill something, time the response) | Wk 9 | half-day | L4.4, L4.11 | launch | runbooks | 🔴 |

## Finalization

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L6.1 | CHANGELOG hygiene + SemVer policy ([`docs/architecture/semver-policy.md`](semver-policy.md)) | Wk 10 | half-day | — | L6.4 | release process | 🟢 |
| L6.2 | Release notes template + release-process runbook ([`.github/RELEASE_NOTES_TEMPLATE.md`](../../.github/RELEASE_NOTES_TEMPLATE.md), [`docs/operations/release-process.md`](../operations/release-process.md)) | Wk 10 | half-day | L6.1 | L6.4 | docs | 🟢 |
| L6.3 | Public-flip prep — strategy for migrating private repo content to new public repo ([`docs/operations/public-flip.md`](../operations/public-flip.md)) | Wk 10 | hour planning | — | L6.4 | repo strategy | 🟢 |
| L6.4 | Production cutover — DNS flip, enable public rate-limit tier | Wk 10 | hour | All above | — | infra | 🔴 |
| L6.5 | Documentation sweep — every runbook verified, every ADR accurate, every config option documented | Wk 10 | full day | All above | L6.4 | docs | 🔴 |
| L6.6 | Customer sign-off demo | Wk 10 | external | L3.*, L4.*, L5.* | L6.4 | — | 🔴 |
| L6.7 | First 24-h post-launch watch | Wk 10 | day | L6.4 | — | rotating shifts | 🔴 |

## Post-launch (explicitly deferred)

| ID | Item | Justification | Status |
|---|---|---|---|
| L7.1 | DIA mainnet integration | Conditional on DIA shipping mainnet | ⏳ |
| L7.2 | 99.99% uptime measurement | Needs ≥ 30 days production data; reported 90 days post-launch | ⏳ |
| L7.3 | ADR-0019 Phase 3 cross-oracle factor | Depends on L2.10 (`internal/divergence/`) being production-quality first | ⏳ |
| L7.4 | Tier-1 own-validator deployment (per ADR-0004) | Multi-week catchup; not required for V1 launch | ⏳ |
| L7.5 | GraphQL surface alongside REST | Optional per RFP; defer until customer-driven | ⏳ |

---

## Dependency graph (the critical path)

The shortest path through all blocking items:

```
L1.1 CEX connectors  ──┐
                       ├─→ L2.1 VWAP impl ──→ L2.4 Phase-1 thresholds ──┐
L1.3 Asset discovery ──┘                                                 │
                                                                          ├─→ L3.1 /v1/price populated ──┐
L2.2 USD volume ──→ L2.3 FX snap rule ─────────────────────────────────→ │                              │
                                                                          │                              │
L2.5 Stat baseline ──→ L2.6 Confidence ──→ L2.7 Freeze ─────────────────→│                              │
                                                                          │                              │
L2.10 Divergence ──→ L2.11 Flag firing ────────────────────────────────→│                              │
                                                                          │                              │
L2.12 Supply ────────────────────────────→ L3.5 V2 market data ─────────│                              │
                                                                          │                              ├─→ L4.* infra
L3.6 SSE infra ──→ L3.7/8/9 streams ────────────────────────────────────→                              │   ──→ L5.* validation
                                                                                                          │   ──→ L6.* finalization
L4.6 → L4.7 → L4.8 → L4.9 archive-completeness daemon ──────────────────────────────────────────────────→
```

**Critical-path long-pole:** L2.5 → L2.6 → L2.7 (statistical
baselines + confidence + freeze) is ~1.5 weeks of focused work
and gates L3.1 (the API actually serving rates).

**Parallelisable:** ingest connectors (L1.*), supply package (L2.12),
divergence package (L2.10), SDK (L3.10), ansible roles (L4.1–L4.5)
all run in parallel with the aggregator critical path.

---

## What "launch-blocking" actually means

Per operator decision 2026-04-28, all 🔴 / 🟡 items above ship
before production cutover. Two consequences:

1. **The 10-week original plan slips by ~1–2 weeks.** The pre-Phase-1
   estimate didn't account for ADR-0017/0018/0019's added scope or
   for the ~1.5 weeks of confidence-scoring work in L2.5–L2.7.
   Realistic launch window: late July 2026 (vs original June 30).
2. **No "soft launch" with stub responses.** Every endpoint serves
   real, anomaly-protected data on day 1. Customers who tested
   against staging see the same wire shape and behaviour at
   production cutover.

The deferrals in the post-launch table above are the only carve-outs;
each has a justification that the operator has explicitly accepted.

---

## Maintenance

This doc is the **canonical** backlog. Update protocol:

- When an item ships: change status to ✅ and add a one-line note
  with the PR number(s)
- When a new item emerges (from an incident, new ADR, or scope
  decision): add a row in the appropriate surface, with full
  dependency / effort / owner fields
- When phase boundaries shift: update the Phase column (don't
  delete the original assignment — track the slip explicitly)
- Review cadence: end of every week, alongside the Friday status
  update. Failure to update means the doc is stale; treat that as a
  CI failure for the next week's planning.

The matching change log lives at the bottom of
[`coverage-matrix.md`](coverage-matrix.md) (the requirements layer);
this doc tracks the implementation layer.

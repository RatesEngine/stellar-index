---
title: Launch readiness backlog
last_verified: 2026-05-02
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
- **Status** — `🟢 in flight` | `🟡 designed, ready to start` | `🔴 not started` | `✅ shipped` | `⚠ shipped with caveat` | `⏳ post-launch`

Items are grouped by surface (ingest / aggregator / API / ops / validation / finalization).
Within each surface, ordered by dependency.

---

## Ingest layer

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L1.1 | CEX connectors: Binance, Coinbase, Kraken, Bitstamp — all four packages under `internal/sources/external/<venue>/` with REST-poller + backfill + restbase adapter; registered in `internal/sources/external/registry.go`; indexer wires per-venue goroutines via `setSourceEnabled`. | Wk 4 | ~5 days | — | L4.1 | `internal/sources/external/<venue>` | ✅ |
| L1.2 | Chainlink HTTP cross-check connector | Wk 4 | half-day | — | L4.4 | `internal/divergence/chainlink` | ✅ |
| L1.3 | Asset enumeration / discovery (auto-detect new SEP-41 tokens) — Sniffer + `discovery.AsyncSink` + `RecordDiscovered` storage path + `discovered_assets` hypertable shipped; indexer constructs the sink at boot, calls `disp.SetDiscoverySink(sink)`, and the dispatcher pushes every SEP-41-shaped event into the async buffer for persistence. `ratesengine_discovery_dropped_hits_total` gauges async-sink backpressure. | Wk 4 | full day | — | L4.1 | `internal/canonical/discovery`, `internal/storage/timescale/discovery.go`, `cmd/ratesengine-indexer/main.go` | ✅ |

## Aggregator layer

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L2.1 | VWAP/TWAP impl across venues + per-pair USD volume threshold | Wk 5 | ~5 days | L1.1 | L3.* | `internal/aggregate` + `Config.MinUSDVolume` filter in `internal/aggregate/orchestrator/orchestrator.go::refreshPairWindow` | ✅ |
| L2.2 | `usd_volume` column populated per trade + FX anchor multiplication — `internal/storage/timescale/trades.go::tradeUSDVolume` covers BOTH off-chain (CEX/FX with fiat:USD or USD-pegged stablecoin quote) AND on-chain (DEX with operator-declared classic USD-pegs + their SAC wrappers via `[trades].usd_pegged_classic_assets` + `[supply.sac_wrappers]`). Today this means USDC, USDT, EURC, EURB, MXNe, PYUSD — every classic-form stablecoin currently traded on Stellar — populate `usd_volume` correctly on both Soroswap/Phoenix/Aquarius and SDEX. The X2.5 forex snap (`internal/aggregate/orchestrator/triangulate.go::legPrice`) handles chained-fiat closed-bucket consistency. The pure-SEP-41 (Soroban-native, no classic backer) stablecoin case is the only uncovered shape; that set is essentially empty on mainnet today and is post-launch scope (see L7.6). | Wk 5 | half-day | L2.1 | L3.* | `internal/storage/timescale/trades.go` + `internal/aggregate/orchestrator/triangulate.go` | ✅ |
| L2.3 | Forex factor snap rule for chained-fiat closed-bucket consistency (ADR-0018) | Wk 5 | half-day | L2.2 | L3.* | `internal/aggregate/orchestrator/triangulate.go::isFXLeg` + `legPrice` (X2.5 snap path); FX-source enumeration via `internal/sources/external.FXSources()`; storage primitive `FXSourceTradeAtOrBefore` selects the most recent FX-source quote at-or-before bucket-end with deterministic across-region tiebreak. | ✅ |
| L2.4 | Phase 1 anomaly thresholds — per-class TOML defaults wired into orchestrator Tick (`evaluateAndMaybeFreeze`) + freeze writer publishes markers (ADR-0019). API-side `flags.frozen` round-trip closed by #431. | Wk 5 | half-day | L2.1 | L3.1 | `internal/aggregate/anomaly` + config | ✅ |
| L2.5 | Phase 2 statistical baseline — MAD math + `volatility_baseline_1m` table + `baseline.Refresher` worker (hourly cadence per pair) + aggregator wire-up shipped across 4 PRs (ADR-0019). `ratesengine_aggregator_baseline_refresh_total` emits per-pair-per-cycle outcomes. | Wk 6 | ~3 days | L2.4 | L3.1 | `internal/aggregate/baseline` + migration | ✅ |
| L2.6 | Multi-factor confidence score on every published price — math + orchestrator `computeConfidence` + per-bucket `cacheConfidence` write + API-side `redisConfidenceLooker` read shipped across 4 PRs. `ratesengine_aggregator_confidence_compute_total` emits outcomes. | Wk 6 | ~2 days | L2.5 | L3.1 | `internal/aggregate/confidence` | ✅ |
| L2.7 | Freeze policy end-to-end — orchestrator's Phase 1 (`evaluateAndMaybeFreeze`) + Phase 2 (`markPhase2Freeze`, 3-signal AND) fire BEFORE the VWAP cache write so the `prevVWAP` comparator slot stays intact across freezes; both call `freeze.Writer.Mark` to publish `freeze:<asset>:<quote>` markers; API binary's `freeze.NewLooker(rdb)` reads those markers (#431); `flags.frozen` on `/v1/price` reflects the producer-side decision. | Wk 6 | full day | L2.6 | L3.1 | `internal/aggregate/freeze` + `cmd/ratesengine-api/main.go` (Freeze: option) | ✅ |
| L2.8 | Multi-window safeguard against frog-boiling (1d/7d/30d MAD) — `baseline.MultiBaseline` (1d / 7d / 30d sub-windows via `SplitByLookback`); `migrations/0008_add_multi_window_baseline` persists per-window MAD; `baseline.Refresher` populates all three; `baselineLookupAdapter.LatestBaseline` reads in the orchestrator; `confidence.Score` uses `MaxZScore` to evaluate against the broadest signal. | Wk 6 | half-day | L2.5 | — | `internal/aggregate/baseline` + migration 0008 | ✅ |
| L2.9 | Bootstrap (warmup) policy for new assets — `confidence.BootstrapConfidenceCap` hard-caps the score at 0.5 during the <30d window; the per-factor calculator ramps to 1.0 linearly across that window. Class-average baseline + auto-classify deferred to follow-up post-launch. | Wk 6 | half-day | L2.6 | — | `internal/aggregate/baseline` + `internal/aggregate/confidence` | ✅ |
| L2.10 | `internal/divergence/` package — CoinGecko + Chainlink HTTP references shipped; `divergence.Service` queries each per-pair, computes median, writes `div:<asset>` to Redis per ADR-0019. | Wk 5–6 | full day | — | L2.11, L3.5 | `internal/divergence` | ✅ |
| L2.11 | `flags.divergence_warning` end-to-end — handler-side `DivergenceLooker` reads `div:<asset>` cache; aggregator's orchestrator Tick drives `RefreshPair` per pair (#429), so the cache is actually populated. The flag now reflects real cross-source divergence. | Wk 6 | half-day | L2.10 | L3.5 | `internal/api/v1/envelope.go` + `internal/aggregate/orchestrator/divergence_refresh.go` | ✅ |
| L2.12 | `internal/supply/` package — circulating supply per ADR-0011 (6 PRs landed: skeleton+XLM, classic, SEP-41, hypertable+store, SAC cross-check+alert, SEP-1 overlay) | Wk 6 | ~2 days | — | L3.* (F2 fields) | `internal/supply/`, `internal/storage/timescale/supply.go`, `migrations/0005_*` | ✅ |
| L2.12a | All six LCM-based supply observers register with the indexer dispatcher per opt-in `[supply]` watched-sets — closes the wiring gap flagged in #410 (PRs #411 / #412 / #413). `pipeline.RegisterSupplyEntryDecoders` attaches accounts (XLM Algorithm 1) when `sdf_reserve_accounts` is non-empty; trustlines + claimable_balances + liquidity_pools when `watched_classic_assets` is non-empty; sac_balances when `[supply.sac_wrappers]` is non-empty. `pipeline.RegisterSupplyEventDecoders` attaches sep41_supply (Algorithm 3 mint/burn/clawback) when `watched_sep41_contracts` is non-empty. New `dispatcher.AddDecoder` API admits event-stream Decoders post-construction. F2 fields on `/v1/assets/{id}` now populate end-to-end for opted-in deployments. | Wk 7 | ~2 days | L2.12 | L3.5 | `cmd/ratesengine-indexer/main.go` + `internal/pipeline/dispatcher.go` | ✅ |

## API layer

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L3.1 | `/v1/price` populated end-to-end — handler `internal/api/v1/price.go` is wired; CAGGs auto-refresh per the `add_continuous_aggregate_policy` calls in `migrations/0002_create_price_aggregates.up.sql` (no manual fill code needed). The remaining work is operational, not code: bring the aggregator binary up against production data so the closed-bucket rows accumulate. Closes naturally when L6.4 cutover happens. | Wk 7 | (above) | L2.1, L2.7 | launch | `internal/api/v1/price.go` + `migrations/0002` | ✅ |
| L3.2 | `/v1/price/tip` rolling-window + last-good-price (ADR-0018) — handler shipped at `s.mux.HandleFunc("GET /v1/price/tip", s.handlePriceTip)`. The streaming companion (`/v1/price/tip/stream`) is independent — see L3.7. | Wk 7 | half-day | L2.1 | L3.6 | `internal/api/v1/price_tip.go` | ✅ |
| L3.3 | `/v1/observations` per-source raw (ADR-0018) — handler shipped at `s.mux.HandleFunc("GET /v1/observations", s.handleObservations)`. The streaming companion (`/v1/observations/stream`) is independent — see L3.8. | Wk 7 | half-day | — | L3.6 | `internal/api/v1/observations.go` | ✅ |
| L3.4 | F5.3 Batch / bulk-query endpoint | Wk 7 | half-day | L3.1 | — | `internal/api/v1/price.go::handlePriceBatch` (GET, ≤100 ids) + `handlePriceBatchPost` (POST, ≤1000 ids); see coverage-matrix.md F5.3 (already ✅ verified). | ✅ |
| L3.5 | F2.* Market Cap / FDV / Circulating / Max Supply / 24h volume on asset detail — `internal/api/v1/assets_f2.go::applyF2Fields` populates all six F2 fields end-to-end on `/v1/assets/{id}`: `total_supply`, `circulating_supply`, `max_supply`, `supply_basis`, `market_cap_usd` (computed when a USD price is available), `fdv_usd` (same), and `volume_24h_usd` (off `Store.Volume24hUSDForAsset`). Best-effort per ADR-0011 — every field stays null when its source value isn't defensible. `change_24h_pct` is deferred to L7.7 (post-launch); it needs aggregator-side closing-bucket pct delta math the v1 orchestrator doesn't expose. | Wk 7 | full day | L2.12, L2.10 | launch | `internal/api/v1/{assets,assets_f2}.go` | ✅ |
| L3.6 | SSE streaming infrastructure — `streaming.Hub` (per-topic ring buffer + fanout), `streaming.StreamFromChannel` (per-tick generator path), `Last-Event-ID` resume, 15s heartbeats, slow-subscriber drop. | Wk 7 | half-day | — | L3.7, L3.8, L3.9, L3.10 | `internal/api/streaming` | ✅ |
| L3.7 | `/v1/price/tip/stream` SSE — handler at `s.handlePriceTipStream`; per-tick generator drives off `PriceReader`; pre-flight 503 when reader nil; passes through `streaming.StreamFromChannel`. | Wk 7 | half-day | L3.2, L3.6 | — | same | ✅ |
| L3.8 | `/v1/observations/stream` SSE — handler at `s.handleObservationsStream`; per-tick generator drives off `HistoryReader`; pre-flight 503 when reader nil; same generator pattern as L3.7. | Wk 7 | half-day | L3.3, L3.6 | — | same | ✅ |
| L3.9 | `/v1/price/stream` SSE (closed-bucket events) — end-to-end fan-out shipped across two PRs. Aggregator side: `orchestrator.StreamPublisher` interface + Redis-pub/sub `redispub.Publisher` + `cmd/ratesengine-aggregator/main.go` wiring publishes one `ClosedBucketEvent` per successful (pair, window) VWAP cache write to channel `ratesengine:closed-bucket:v1`. API side: `redispub.Subscriber` runs as a goroutine in `cmd/ratesengine-api/main.go`, decodes each event, and republishes on the in-process `streaming.Hub` with topic `closed:<asset>/<quote>` — same key `internal/api/v1.PriceStreamTopic` produces. Best-effort throughout: producer + consumer errors log + increment `ratesengine_aggregator_stream_publish_total` / `ratesengine_api_stream_subscribe_total` counters but never block the orchestrator's tick or the API binary's serve loop. | Wk 7 | half-day | L3.1, L3.6 | — | `internal/api/streaming/redispub/` + `cmd/ratesengine-aggregator/main.go` + `cmd/ratesengine-api/main.go` | ✅ |
| L3.10 | `pkg/client/` Go SDK — published types (`Envelope[T]`, `AssetDetail` with all 15 wire fields per #426, `Flags`, `PriceSnapshot`, `HistorySeries`, `Account`, `UsageRow`, `KeyCreated`); 8 client methods (`Price`, `HistorySinceInception`, `Assets`, `Asset`, `AssetMetadata`, `Me`, `Usage`, `CreateKey`); SemVer-pinned from v1.0.0 per ADR-0005. | Wk 7 | half-day | — | — | `pkg/client` | ✅ |
| L3.11 | Generated API reference via Redocly + GitHub Pages workflow + CI drift guard — `scripts/dev/docs-api.sh` regenerates `docs/reference/api/index.html` via Redocly v2.30.1 (pinned for reproducibility); `.github/workflows/api-docs.yml` deploys to GitHub Pages on push (gated on `workflow_dispatch` until the public-flip per L7's status, then re-armed automatically); `ci.yml` includes the drift guard so the rendered output never falls behind the OpenAPI source. | Wk 7 | half-day | — | — | `scripts/dev/docs-api.sh`, `.github/workflows/api-docs.yml` | ✅ |
| L3.12 | SEP-10 protocol implementation (Web Auth) — `sep10.Validator` (Challenge / Verify / VerifyJWT) shipped; `internal/api/v1.handleSEP10Challenge` + `handleSEP10Token` mounted at `/v1/auth/sep10/{challenge,token}`; API-binary main.go constructs the validator from `[api.sep10]` config (signing-seed env, JWT secret env, challenge TTL) and falls back to `auth.NoopSEP10Validator` when config is absent so the endpoints return 503 cleanly. | Wk 7 | full day | — | — | `internal/auth/sep10/` + `cmd/ratesengine-api/main.go` (sep10Validator wiring) | ✅ |
| L3.13 | Envelope flag retrofit (`flags.frozen`, `flags.single_source`) — handler-side `FrozenLooker` interface, aggregator's `freeze.Writer` publishes markers (ADR-0019 Phase 1 + 2), API binary wires `freeze.NewLooker(rdb)` so `/v1/price` reads the markers and stamps `flags.frozen` end-to-end. | Wk 7 | half-day | L2.7 | — | `internal/api/v1/{envelope,price,server}.go` + `cmd/ratesengine-api/main.go` (Freeze: option) | ✅ |
| L3.14 | CDN caching for historical endpoints — origin-side `Cache-Control` middleware shipped (`internal/api/v1/middleware/cachecontrol.go` + tests; per-path policy per ADR-0018; `CacheControlWithCDN(false)` toggles for no-CDN deployments). The remaining work is **infra-side**: provisioning CloudFront / Vercel / Bunny in front of `api.ratesengine.net` and pointing it at the origin. Tracked in `docs/operations/cdn-setup.md` (operator runbook, separate from this backlog row's code completion). | Wk 7 | half-day | infra | — | `internal/api/v1/middleware/cachecontrol.go` | ✅ |
| L3.15 | Self-service onboarding page ([`docs/getting-started.md`](../getting-started.md)) — 205-line walkthrough covering API-key signup, the four core surfaces (`/v1/price`, `/v1/history`, `/v1/assets`, `/v1/oracle`), SDK install + first request, rate-limit budgets per tier. Pages workflow (L3.11) deploys it alongside the API reference; both share the `docs.ratesengine.net` host. | Wk 7 | half-day | — | — | `docs/getting-started.md` | ✅ |
| L3.16 | URL discipline OpenAPI lint — `scripts/ci/lint-openapi-urls/` rejects query parameters that select between consistency surfaces (forbidden names `freshness`/`consistency`/`surface`/`tier`; multi-value enums that name two surface keywords). Real-spec sentinel test asserts `openapi/rates-engine.v1.yaml` passes. Wired into `verify.sh`, the GitHub Actions `ci.yml` workflow, and `make lint-openapi-urls`. | Wk 7 | half-day | — | — | `scripts/ci/lint-openapi-urls/` | ✅ |
| L3.11 | Generated API reference via Redocly + GitHub Pages workflow + CI drift guard. `make docs-api` regenerates `docs/reference/api/index.html` from `openapi/rates-engine.v1.yaml`; `.github/workflows/api-docs.yml` deploys to GitHub Pages on every main push; `scripts/ci/lint-docs.sh` §"API routes vs OpenAPI" enforces drift-free at lint time. | Wk 7 | half-day | — | — | `scripts/dev/docs-api.sh`, `.github/workflows/api-docs.yml` | ✅ |
| L3.12 | SEP-10 protocol implementation (Web Auth) — `sep10.Validator` (Challenge / Verify / VerifyJWT) shipped; `internal/api/v1.handleSEP10Challenge` + `handleSEP10Token` mounted at `/v1/auth/sep10/{challenge,token}`; API-binary main.go constructs the validator from `[api.sep10]` config (signing-seed env, JWT secret env, challenge TTL) and falls back to `auth.NoopSEP10Validator` when config is absent so the endpoints return 503 cleanly. | Wk 7 | full day | — | — | `internal/auth/sep10/` + `cmd/ratesengine-api/main.go` (sep10Validator wiring) | ✅ |
| L3.13 | Envelope flag retrofit (`flags.frozen`, `flags.single_source`) — handler-side `FrozenLooker` interface, aggregator's `freeze.Writer` publishes markers (ADR-0019 Phase 1 + 2), API binary wires `freeze.NewLooker(rdb)` so `/v1/price` reads the markers and stamps `flags.frozen` end-to-end. | Wk 7 | half-day | L2.7 | — | `internal/api/v1/{envelope,price,server}.go` + `cmd/ratesengine-api/main.go` (Freeze: option) | ✅ |
| L3.14 | CDN caching for historical endpoints — origin-side `Cache-Control` middleware applied per ADR-0018 surface. `internal/api/v1/middleware/cachecontrol.go::policyForPath` covers every route with the right private/public/no-store policy; `s-maxage` (CDN tier) gated on `cfg.API.CDNEnabled`. CloudFront / equivalent provider config is the operator's own deploy-track work — repo side is complete. | Wk 7 | half-day | infra | — | `internal/api/v1/middleware/cachecontrol.go` | ✅ |
| L3.15 | Self-service onboarding page ([`docs/getting-started.md`](../getting-started.md)) — Pages workflow deploys it via L3.11 | Wk 7 | half-day | — | — | `docs/getting-started.md` | ✅ |
| L3.16 | URL discipline OpenAPI lint — query params don't change consistency contract (ADR-0018). `scripts/ci/lint-openapi-urls/` runs as part of the CI pipeline, fails the build on a query param that would shift a consistency contract. | Wk 7 | half-day | — | — | `scripts/ci/lint-openapi-urls/` | ✅ |

## Operations / infrastructure

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L4.1 | Patroni-managed Postgres ansible role | Wk 8 | full day | — | launch | `configs/ansible/roles/patroni` (#344) | ✅ |
| L4.2 | Redis cluster + Sentinel ansible role | Wk 8 | half-day | — | launch | `configs/ansible/roles/redis-sentinel` (#350) | ✅ |
| L4.3 | HAProxy + keepalived ansible role | Wk 8 | half-day | L4.1, L4.2 | launch | `configs/ansible/roles/haproxy` (#362) | ✅ |
| L4.4 | Prometheus + Grafana + Alertmanager ansible role | Wk 8 | full day | — | launch | `configs/ansible/roles/prometheus` (#363) — Grafana deferred to staging deploy | ✅ |
| L4.5 | Loki log aggregation ansible role | Wk 8 | half-day | L4.4 | — | `configs/ansible/roles/loki` (#364) | ✅ |
| L4.6 | Archive-completeness daemon (PR A: `check`) — `cmd/ratesengine-ops archive-completeness check` walks the local archive against history-archive references, reports missing checkpoints. | Wk 8 | half-day | — | L4.7 | `cmd/ratesengine-ops archive-completeness check` | ✅ |
| L4.7 | Archive-completeness daemon (PR B: `fix` with multi-source fallback) — `archive-completeness fix` repairs missing checkpoints by pulling from peer archives + cross-anchor backups. | Wk 8 | half-day | L4.6 | L4.8 | `cmd/ratesengine-ops archive-completeness fix` | ✅ |
| L4.8 | Archive-completeness daemon (PR C: `verify` mode + systemd timer + Prometheus alerts) — `archive-completeness verify` runs check→fix→re-check + Prometheus textfile emit; `deploy/systemd/archive-completeness.{service,timer}` drive the daily cron; `deploy/monitoring/rules/archive-completeness.yml` alerts on staleness + unit-failed. | Wk 8 | half-day | L4.7 | L4.9 | systemd + alert rules | ✅ |
| L4.9 | Verify-archive `-fail-on-missed` flag (post-bootstrap hardening) — `cmd/ratesengine-ops/main.go::verifyArchive` accepts `-fail-on-missed` per ADR-0017 X1.7; flips checkpoint-anchor failure from soft warning to hard failure once bootstrap is complete. | Wk 8 | half-day | L4.8 | launch | `cmd/ratesengine-ops/main.go` | ✅ |
| L4.10 | Per-region asymmetric trust model wiring (R1 leader, R2/R3 delegate) | Wk 8 | full day | L4.4 | launch | each region's `verify-archive -tier` selection per ADR-0016: R1 runs Tier A+B+D as integrity leader, R2/R3 run them periodically as defence-in-depth. See [coverage-matrix.md X1.6](coverage-matrix.md) (already ✅ verified) and [archive-completeness.md §Per-region behaviour](../operations/archive-completeness.md). | ✅ |
| L4.11 | Public status page at `status.ratesengine.net` — decision: **Upptime** on GitHub Pages (independent of our origin; auto-monitors every 5 min via GitHub Actions; auto-creates incident issues on probe failure; auto-resolves on recovery — removes the on-call "must remember to post" failure mode that a manual page would have). End-to-end operator runbook in [`docs/operations/status-page-setup.md`](../operations/status-page-setup.md): template fork, `.upptimerc.yml` config, GH_PAT secret, Cloudflare DNS, smoke-test (force a probe failure + confirm auto-issue), manual incident-posting via labelled GitHub issues for incidents Upptime can't see (correctness bugs, regional outages, maintenance windows). | Wk 9 | half-day | L4.4 | launch | infra config (operator runbook ready) | 🟡 |
| L4.12 | verify-archive systemd timer — nightly Tier A on R1 per ADR-0016, with Prometheus alerts on unit-failed + run-stale. `deploy/systemd/verify-archive-tier-a.{service,timer}` drive the cron; `deploy/monitoring/rules/verify-archive.yml` alerts on `node_systemd_unit_state{name="verify-archive-tier-a.service",state="failed"}=1` + run-staleness via the Prometheus textfile timestamp. | Wk 8 | half-day | — | launch | `deploy/systemd/verify-archive-tier-a.{timer,service}` | ✅ |
| L4.13 | systemd units for `ratesengine-{indexer,aggregator,api}` — long-running service files referenced by the bringup doc and the L4.1-4.3 ansible roles. All three units live under `deploy/systemd/`; the ansible roles consume them as templates. | Wk 8 | half-day | — | L4.1, L4.2, L4.3 | `deploy/systemd/ratesengine-{indexer,aggregator,api}.service` | ✅ |

## SLA validation

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L5.1 | k6 `api_steady_state.js` — 1000 req/min × 100 keys × 30 min | Wk 9 | half-day | L3.* | L5.2 | `test/load/scenarios/01-price-hot-path.js` + `06-mixed-realistic.js` | ✅ |
| L5.2 | k6 `api_ramp_to_saturation.js` — find the cliff | Wk 9 | half-day | L5.1 | — | `test/load/scenarios/99-spike.js` (10× spike absorbs the saturation-find role) | ✅ |
| L5.3 | k6 `api_spike.js` — 10× burst recovery < 60s | Wk 9 | half-day | L5.1 | — | `test/load/scenarios/99-spike.js` | ✅ |
| L5.4 | k6 `ingest_peak_ledger.js` — 5× normal event rate × 1 h | Wk 9 | half-day | L1.* | — | covered by indexer's existing soak via `test/load/scenarios/06-mixed-realistic.js` ingest-side metrics; dedicated indexer-only k6 is a post-launch nice-to-have | ⚠ |
| L5.5 | Chaos suite Wave 1 (dev-stack smoke; 3 scenarios) — code shipped under `test/chaos/{run.sh,scenarios/}`. Closing the row needs **execution + recording**: a clean run against `make dev` plus a committed `test/chaos/reports/<launch-cut-timestamp>/` directory + RETRO. End-to-end execution runbook in [`docs/operations/chaos-wave1-runbook.md`](../operations/chaos-wave1-runbook.md): pre-flight, runner usage, pass criteria per scenario, what to capture per run, what to do when something breaks. Wave 2 (HA-shaped scenarios on staging baremetal) deferred post-launch. | Wk 9 | full day | L4.* | launch | `test/chaos` (operator runbook ready) | 🟢 |
| L5.4 | k6 `ingest_peak_ledger.js` — 5× normal event rate × 1 h. **Acceptance documented**: covered by `test/load/scenarios/06-mixed-realistic.js`'s ingest-side metrics (the mixed-realistic scenario exercises the indexer alongside API load and exposes the same indexer counters a dedicated scenario would assert against). A dedicated indexer-only k6 — useful for finding the indexer's saturation point in isolation, away from API noise — is a post-launch nice-to-have, not launch-blocking. | Wk 9 | half-day | L1.* | — | `test/load/scenarios/06-mixed-realistic.js` | ✅ |
| L5.5 | Chaos suite Wave 1 (dev-stack smoke; 3 scenarios). Wave 2 (HA-shaped scenarios on staging baremetal) deferred post-launch. | Wk 9 | full day | L4.* | launch | `test/chaos` | 🟢 |
| L5.4 | k6 `ingest_peak_ledger.js` — 5× normal event rate × 1 h | Wk 9 | half-day | L1.* | — | covered by indexer's existing soak via `test/load/scenarios/06-mixed-realistic.js` ingest-side metrics; dedicated indexer-only k6 is a post-launch nice-to-have | ⚠ |
| L5.5 | Chaos suite Wave 1 (dev-stack smoke; 3 scenarios — Redis down, Timescale down, Redis network partition) shipped at `test/chaos/scenarios/{01,02,03}-*.sh` + `run.sh` driver. Wave 2 (HA-shaped scenarios on staging baremetal) deferred post-launch. | Wk 9 | full day | L4.* | launch | `test/chaos` | ✅ |
| L5.6 | Security review (external or community) on full stack | Wk 9 | (external) | L3.* | launch | external auditor | 🔴 |
| L5.7 | SEV-1 / SEV-2 dry-run records — solo tabletop dry-runs against `scenarios/sev1-timescale-primary-failover.md` + `scenarios/sev2-source-decoder-regression.md` landed under `docs/operations/drills/2026-04-*.md`; promoted two action items into runbook updates (`timescale-primary-down.md` lead-with-readyz, `decode-errors.md` divergence_warning correlation). 3-person tabletop queued for post-launch. | Wk 9 | half-day | L4.4, L4.11 | launch | runbooks + drill writeups | ✅ |

## Finalization

| ID | Item | Phase | Effort | Depends on | Blocks | Owner | Status |
|---|---|---|---|---|---|---|---|
| L6.1 | CHANGELOG hygiene + SemVer policy — `CHANGELOG.md` follows Keep-a-Changelog format with `## [Unreleased]` curated continuously and promoted at each release cut. [`docs/architecture/semver-policy.md`](semver-policy.md) ratifies the dual-clock convention (SemVer for `pkg/*`, CalVer `YYYY.MM.DD.N` for binaries) plus deprecation policy and pre-tag manual checks. | Wk 10 | half-day | — | L6.4 | release process | ✅ |
| L6.2 | Release notes template + release-process runbook — [`.github/RELEASE_NOTES_TEMPLATE.md`](../../.github/RELEASE_NOTES_TEMPLATE.md) bakes the four mandatory sections (Tested-against / pkg-versions / Migration / Added/Changed/Deprecated/Removed/Fixed/Security). [`docs/operations/release-process.md`](../operations/release-process.md) covers pre-flight, cut, post-flight, hotfix branching, and the rollback path. | Wk 10 | half-day | L6.1 | L6.4 | docs | ✅ |
| L6.3 | Public-flip prep — [`docs/operations/public-flip.md`](../operations/public-flip.md) ratifies the new-repo (not history-rewrite) decision, captures the 16-row pre-flip checklist (every row's evidence column verified 2026-04-30), and documents the cut-over mechanics + post-flip steps + two-repo coexistence rules. | Wk 10 | hour planning | — | L6.4 | repo strategy | ✅ |
| L6.1 | CHANGELOG hygiene + SemVer policy ([`docs/architecture/semver-policy.md`](semver-policy.md)). The policy doc lives + the `[Unreleased]` discipline is enforced inline with feature PRs; CalVer release tagging documented in `release-process.md`. | Wk 10 | half-day | — | L6.4 | release process | ✅ |
| L6.2 | Release notes template + release-process runbook ([`.github/RELEASE_NOTES_TEMPLATE.md`](../../.github/RELEASE_NOTES_TEMPLATE.md), [`docs/operations/release-process.md`](../operations/release-process.md)). Both shipped; the release-process doc covers the rollback path referenced from `runbooks/all-ingestion-down.md`. | Wk 10 | half-day | L6.1 | L6.4 | docs | ✅ |
| L6.3 | Public-flip prep — strategy for migrating private repo content to new public repo ([`docs/operations/public-flip.md`](../operations/public-flip.md)). Pre-flip checklist (16 rows) verified 2026-04-30 (gitleaks clean, CODEOWNERS scrubbed, SECURITY.md current); cut-over mechanics scripted; 24-hour pre-cutover dry-run added 2026-05-03. Execution gates on the v1.0 launch signal (L6.4). | Wk 10 | hour planning | — | L6.4 | repo strategy | ✅ |
| L6.4 | Production cutover — DNS flip, enable public rate-limit tier | Wk 10 | hour | All above | — | infra | 🔴 |
| L6.5 | Documentation sweep — every runbook verified, every ADR accurate, every config option documented | Wk 10 | full day | All above | L6.4 | docs | 🔴 |
| L6.6 | Customer sign-off demo — pre-flight + 9-stage walk-through covering every public surface (closed-bucket pricing → tip → observations → history → SSE → asset detail → SDK) plus expected-Q&A. End-to-end script ready in [`docs/operations/customer-demo-script.md`](../operations/customer-demo-script.md); the customer leaves able to make their first real request unaided. | Wk 10 | external | L3.*, L4.*, L5.* | L6.4 | — | 🔴 |
| L6.7 | First 24-h post-launch watch | Wk 10 | day | L6.4 | — | rotating shifts | 🔴 |

## Post-launch (explicitly deferred)

| ID | Item | Justification | Status |
|---|---|---|---|
| L7.1 | DIA mainnet integration | Conditional on DIA shipping mainnet | ⏳ |
| L7.2 | 99.99% uptime measurement | Needs ≥ 30 days production data; reported 90 days post-launch | ⏳ |
| L7.3 | ADR-0019 Phase 3 cross-oracle factor | Depends on L2.10 (`internal/divergence/`) being production-quality first | ⏳ |
| L7.4 | Tier-1 own-validator deployment (per ADR-0004) | Multi-week catchup; not required for V1 launch | ⏳ |
| L7.5 | GraphQL surface alongside REST | Optional per RFP; defer until customer-driven | ⏳ |
| L7.7 | `change_24h_pct` field on `/v1/assets/{id}` | Field is declared in OpenAPI as nullable; the other six F2 fields populate end-to-end (L3.5). Implementing it requires the aggregator to emit a closed-bucket pct delta the v1 orchestrator doesn't expose — either a new CAGG that captures the per-bucket open/close pair or a derived computation that joins two bucket-end VWAPs across the 24h window. Today the field is honestly null. (L7.6 reserved for SEP-41 stablecoin USD-volume per PR #517.) | ⏳ |
| L7.6 | `usd_volume` for pure-SEP-41 (Soroban-native, no classic backer) stablecoins | Mainnet set is essentially empty today (USDC/USDT/EURC/EURB/MXNe/PYUSD are classic + SAC; L2.2 covers them via the SAC-wrapper map). Add a `[trades].usd_pegged_sep41_contracts` config surface if/when a Soroban-native stablecoin gains traction. | ⏳ |

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

---
title: Aggregation plan — the policy chain from raw trade to served price
last_verified: 2026-04-25
status: binding
---

# Aggregation plan

**Every served price flows through one path:**

```text
Timescale `trades` hypertable    ← decoders write here (per ingest-pipeline.md)
    │   (TradesInRange per (pair, window))
    ▼
internal/aggregate/orchestrator/ ← tick loop, one (pair, window) per call
    │   1. fetchForTarget(target, window)
    │      ├─ direct TradesInRange(target, …)
    │      └─ optional: stablecoin-backer expansion (XLM/fiat:USD →
    │                  XLM/USDT, XLM/USDC, XLM/DAI, XLM/PYUSD,
    │                  XLM/USDP — each rewritten via ProxyPair onto
    │                  the target)
    │   2. class filter (default: drop non-ClassExchange rows)
    │   3. σ-threshold outlier filter (default 4σ)
    │   4. VWAP via internal/aggregate/vwap.go
    │
    ▼
Redis  ← key `vwap:<base>:<quote>:<window-seconds>`, TTL = window
    │
    ▼
internal/api/v1/  ← /v1/vwap, /v1/twap, /v1/price (cache-first)
    │
    ▼
HTTP consumer
```

`/v1/sources` is the read-only sibling: it surfaces the same
`external.Registry` the class filter consults so API consumers can
see which venues contribute to VWAP and which are visible-only.

---

## The policy chain

The orchestrator applies three filters between `TradesInRange`
and `aggregate.VWAP`. Each step is independent and falls back to
"input unchanged" when its config flag is off.

| Step | Default | Config flag | Purpose |
| --- | --- | --- | --- |
| 1. Stablecoin expansion | OFF | `aggregate.enable_stablecoin_fiat_proxy` | Expand fiat-quote targets to direct + stablecoin backers; rewrite via `aggregate.ProxyPair` |
| 2. Class filter | ON | `aggregate.disable_class_filter` (inverted — zero is filter ON) | Drop non-`ClassExchange` rows; aggregator / oracle / authority_sanity classes don't contribute to VWAP |
| 3. Outlier filter | ON (`σ=4.0`) | `aggregate.outlier_sigma_threshold` | Drop trades whose price differs from the window mean by > σ standard deviations |

Order matters: class filter runs before the outlier filter because
the σ arithmetic should run over a pair-homogeneous, exchange-only
sample. Stablecoin expansion runs before both — it re-stamps the
rewritten trades onto the target pair, and the class filter then
treats each row by its venue identity (binance, coinbase, …) not
by the original on-chain pair.

### Why each filter is here, not at ingest

- **Decoders never re-stamp pairs.** A USDT depeg event is news;
  rewriting `XLM/USDT → XLM/USD` at decode time would hide it.
- **Decoders never drop trades by class.** A CoinGecko poll is
  data we want to record + serve via `/v1/sources`; we just don't
  want to fold it into our own VWAP. Filtering at decode would
  strip information we need.
- **Decoders never drop outliers.** σ-deviance is a window-relative
  signal. A row that's 5σ from the per-pair window mean is noise
  on a single pair but might be perfectly normal across all pairs
  combined.

In short: ingest preserves truth; aggregation applies policy.

---

## Configuration surface

`[aggregate]` in TOML drives the orchestrator. Operator overrides
win; empty falls back to library defaults.

| TOML key | Library default | Effect |
| --- | --- | --- |
| `pairs` | `[]` → built-in (XLM/BTC/ETH × USD/EUR/GBP) | Operator-supplied coverage set as canonical pair strings |
| `windows` | `[]` → `[5m, 1h, 24h]` | Per-window cadences as Go `time.Duration` strings |
| `interval_seconds` | 30 | Tick cadence — gap between successive (pair, window) refreshes |
| `max_trades_per_window` | 10 000 | Per-(pair, window) row cap |
| `disable_class_filter` | false | Off ⇒ ClassExchange-only VWAP (default) |
| `enable_stablecoin_fiat_proxy` | false | On ⇒ fiat-target fan-out across stablecoin backers |
| `outlier_sigma_threshold` | 4.0 | σ-threshold (0 disables) |
| `vwap_window_seconds` | 300 | Legacy alias retained for backwards-compat |
| `twap_window_seconds` | 300 | TWAP-specific cadence (used by api/v1/twap.go) |
| `min_usd_volume` | 10 000 | Eligibility threshold |
| `triangulation_enabled` | true | Master switch for the post-refresh triangulation pass; false skips the tick regardless of `aggregate.triangulations` rows. Triangulated rows now serve via `/v1/price` (PR for F-0014) — the switch is the operator-side kill-switch when the feature itself needs to be paused. |

The full reference lives at
[`docs/reference/config/README.md`](../reference/config/README.md);
this table is the curated subset that drives aggregator
behaviour day-to-day.

---

## Observability

Three Prometheus rules (`deploy/monitoring/rules/aggregator.yml`)
consume four counters from `internal/obs/metrics.go`:

| Counter | Labels | Used by |
| --- | --- | --- |
| `ratesengine_aggregator_ticks_total` | `outcome` (ok/error) | `aggregator_silent` alert |
| `ratesengine_aggregator_vwap_writes_total` | — | `aggregator_silent` alert |
| `ratesengine_aggregator_empty_windows_total` | — | (Operator dashboards; see runbooks) |
| `ratesengine_aggregator_dropped_trades_total` | `reason` (class/outlier) | `aggregator_outlier_storm` + `aggregator_class_drop_spike` alerts |

Alert runbooks at:

- [`aggregator-silent.md`](../operations/runbooks/aggregator-silent.md) — P1
- [`aggregator-outlier-storm.md`](../operations/runbooks/aggregator-outlier-storm.md) — P3
- [`aggregator-class-drop-spike.md`](../operations/runbooks/aggregator-class-drop-spike.md) — P3

Baseline-comparator alerts use `offset 1h` to auto-tune to operator
traffic. Suppress for the first hour after deploy — the comparator
returns zero before there's an hour of history.

---

## API surface

| Endpoint | Backed by | Purpose |
| --- | --- | --- |
| `GET /v1/vwap?pair=…` | Redis cache (`vwap:<base>:<quote>:<window-seconds>`) | The aggregator's primary product |
| `GET /v1/twap?pair=…` | Redis cache | Time-weighted average; same orchestrator (TWAP-via-orchestrator path is TBD — see Deferred) |
| `GET /v1/price?pair=…` | Redis cache → trades fallback | Last-trade or VWAP depending on freshness |
| `GET /v1/sources` | `external.Registry` (static) | Class + IncludeInVWAP metadata for every known venue |
| `GET /v1/markets` | Timescale `DistinctPairs` | Trade-table coverage; orthogonal to the registry |

`/v1/sources` and the orchestrator's class filter agree by
construction — they consume the same `external.Registry`, so a
venue listed with `include_in_vwap=true` *will* contribute to
the cached VWAP, and one with `false` *will not*. Discrepancies
between the two surfaces are a bug to surface in PR review, not a
runtime concern.

### Closed-bucket-only serving (cross-region consistency)

Per [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md), the
API endpoints above (`/v1/price`, `/v1/vwap`, `/v1/twap`,
`/v1/ohlc`) NEVER expose the in-progress (currently-filling)
window — only the most recent **closed** bucket. The orchestrator
writes both the in-progress and closed-window CAGG rows to
Timescale; query handlers MUST filter `bucket_to_ts <= now()` so
clients only ever see immutable, content-addressed values.

This is what makes "all 3 regions serve exactly the same rate" a
real property rather than a hopeful one: closed-bucket rows are
deterministic given the same trade inputs, and (sub-second to
seconds-of-replication-lag aside) replicate to all regions
byte-identical. See ADR-0015 for the trade-off analysis and the
≤30 s freshness contract this places on the default `/v1/price`
window.

---

## Boundaries — what this layer does NOT do

- **No persistent state.** The orchestrator is stateless across
  ticks; everything it needs comes from Timescale or
  `external.Registry` at refresh time. Restart-friendly by design.
- **No cross-binary state coupling.** Aggregator → API
  communication is via Redis keys + the static registry. The API
  has no read path into the orchestrator's in-memory `Stats()`.
- **No write path back to Timescale.** VWAP results live in Redis
  with TTL; if Redis loses the world, the next tick rebuilds it
  from raw trades. Continuous-aggregate materialised views (when
  they ship under [migrations/](../../migrations/)) provide the
  long-tail historical answer; the orchestrator focuses on the
  hot, freshness-sensitive cache.
- **No per-pair Prometheus labels.** Cardinality stays bounded —
  pair-level lenses live in the Redis key namespace and on the API
  contract, not on `/metrics`.

---

## Deferred — natural follow-ups

Listed here so a future contributor can pick one up without
re-deriving the design space:

- **Triangulation.** `XLM/USD × USD/EUR = XLM/EUR` for fiat pairs
  with sparse direct trades. Will live as a separate worker
  running alongside the direct-pair loop, writing to its own Redis
  key namespace + flagging `triangulated=true` in the envelope once
  the public serving path consumes triangulated results. That final
  API exposure is not wired in this snapshot.
- **Divergence detection.** Aggregator-class sources (CoinGecko,
  CoinMarketCap, CryptoCompare) currently visible in `/v1/sources`
  but excluded from VWAP. A divergence worker compares our VWAP
  against those references and writes to `div:<pair>` Redis keys
  + drives the existing `ratesengine_price_divergence_*` alerts.
- **MAD-based outlier filter.** σ-mean is brittle on small
  windows with fat tails. Switch to median-absolute-deviation
  behind the same `outlier_sigma_threshold` flag once we have
  pubnet runtime data backing the change with an ADR.
- **Continuous-aggregate refresh driver.** Timescale's background
  job handles materialised-view refresh today. A custom driver
  with tighter freshness guarantees lands when API consumers
  start hitting historical CAGGs at fresh-data SLAs.
- **Per-source weighted VWAP.** Currently every contributing
  source weights at 100. The `Metadata.DefaultWeight` field is
  shaped to support per-source overrides via config; the math
  change to `aggregate.VWAP` lands when an operator actually
  needs it.

Each is a drop-in extension — no shape change to the existing
orchestrator's `Config` or to the surrounding contracts.

---

## References

- [`internal/aggregate/orchestrator/orchestrator.go`](../../internal/aggregate/orchestrator/orchestrator.go) — Tick loop + filter chain
- [`internal/aggregate/stablecoin.go`](../../internal/aggregate/stablecoin.go) — `FiatProxy` / `ProxyPair` / `ExpandTargetPair`
- [`internal/aggregate/outliers.go`](../../internal/aggregate/outliers.go) — σ-threshold filter
- [`internal/sources/external/registry.go`](../../internal/sources/external/registry.go) — Source-class registry (single source of truth)
- [`internal/obs/metrics.go`](../../internal/obs/metrics.go) — Aggregator counters
- [`deploy/monitoring/rules/aggregator.yml`](../../deploy/monitoring/rules/aggregator.yml) — Prometheus rules
- [`docs/reference/config/README.md`](../reference/config/README.md) — Full config reference
- [`docs/reference/metrics/README.md`](../reference/metrics/README.md) — Full metrics reference
- [`CHANGELOG.md`](../../CHANGELOG.md) — Per-PR narrative for the build-out

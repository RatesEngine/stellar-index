---
adr: 0020
title: Chart API contract — timeframe + granularity + price_type
status: Accepted
date: 2026-04-30
supersedes: []
superseded_by: null
---

# ADR-0020: Chart API contract — timeframe + granularity + price_type

## Context

The historical price chart use case requires a chart contract shaped
as `(timeframe, granularity, price_type) → points[]`:

| Timeframe      | Granularity (suggested) | Data Points | Price Type   |
|----------------|-------------------------|-------------|--------------|
| 1 hour         | 1 min                   | ~60         | TWAP or VWAP |
| 24 hours       | 15 min                  | ~96         | TWAP or VWAP |
| 1 week         | 1 hr                    | ~168        | TWAP or VWAP |
| 1 month        | 4 hr                    | ~180        | TWAP or VWAP |
| Since Inception| 1 day                   | Variable    | TWAP or VWAP |

The existing API surfaces some but not all of this:

- `/v1/history` — raw trade rows in `[from, to)` (not chart-shaped).
- `/v1/history/since-inception` — full CAGG-served series at one
  granularity from a pair's earliest closed bucket. Has no
  timeframe parameter.
- `/v1/ohlc`, `/v1/vwap`, `/v1/twap` — single-bar aggregates over a
  window (not series).

None map 1:1 to the chart shape. The OpenAPI spec already declares
the `timeframe` + `granularity` parameter components but neither is
referenced by an operation — they were placeholders pending this
decision.

## Decision

Add a new `GET /v1/chart` endpoint that matches the chart contract
exactly:

```
GET /v1/chart
  ?asset=<id>
  &quote=<id>          # default: USD
  &timeframe=<tf>      # 1h | 24h | 1w | 1mo | 1y | all   (default 24h)
  &granularity=<g>     # 1m | 15m | 1h | 4h | 1d | 1w | 1mo (default per timeframe)
  &price_type=<pt>     # vwap | twap                       (default vwap)
```

Response shape mirrors `/v1/history/since-inception`:

```json
{
  "data": {
    "asset_id": "...",
    "quote": "fiat:USD",
    "timeframe": "24h",
    "granularity": "15m",
    "price_type": "vwap",
    "points": [{ "t": "...", "p": "1.234", "v_usd": "..." }, ...]
  },
  "flags": { ... }
}
```

### Why a new endpoint, not extension of since-inception

`/v1/history/since-inception` has an unbounded window by name and
documented purpose. Adding a `timeframe` param would muddy that
contract. Customers who want the full series (regulators, CSV
export, audit) and customers who want a chart with a rolling window
(Freighter's UI) have different latency / cap profiles — keeping
them as separate endpoints lets them evolve independently.

### Default-granularity table (timeframe → granularity)

When `granularity` is omitted, the handler picks per the table above:

| Timeframe | Default granularity | Approx points |
|-----------|---------------------|---------------|
| `1h`      | `1m`                | 60            |
| `24h`     | `15m`               | 96            |
| `1w`      | `1h`                | 168           |
| `1mo`     | `4h`                | 180           |
| `1y`      | `1d`                | 365           |
| `all`     | `1d`                | variable      |

Operators can still override (e.g. `timeframe=24h&granularity=1m`
for a 1440-point chart) — the table is a default, not a constraint.

### price_type handling

`vwap` is served from the existing `prices_<gran>` CAGGs (live
today).

`twap` is NOT yet served — we do not maintain a TWAP CAGG at audit
time. Requests with `price_type=twap` return `400 Bad Request` with
problem+json explaining the parameter is reserved for forward
compatibility but not yet supported. This is preferred over silent
fallback-to-VWAP (which would mis-label the response) and over
on-the-fly TWAP from the 1m CAGG (which would compute differently
from a future TWAP CAGG and create a one-time consumer-visible
break when we ship the CAGG).

Tracked as L7.8 (post-launch) in
[`launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md);
the row carries the implementation sketch (TWAP CAGG migration +
aggregator tick + handler flip). Reopened when a customer asks
for TWAP-shaped multi-bar charts.

### Closed-bucket guard

Per ADR-0015, only CLOSED buckets are returned. The
`HistoryPointsInRange` storage primitive applies the same
`bucket + interval <= now()` filter as the existing
`HistoryPoints`. The in-progress bucket is intentionally absent;
clients seeking sub-bucket freshness use `/v1/price` (point-in-time)
or `/v1/oracle/latest` (per-source).

### Cap

`historyMaxPoints = 50_000` (same as since-inception). At `1m`
granularity this is ~35 days of data; well above the largest
standard timeframe (1mo @ 4h = 180 points). Operators
running an unusual `timeframe=1y&granularity=1m` request hit the
cap and receive `flags.truncated=true`.

## Consequences

- Adds one new endpoint, one new storage method
  (`HistoryPointsInRange`) on the existing `HistoryReader`
  interface, one OpenAPI operation. No CAGG / migration changes.
- The existing `/v1/history/since-inception` is unaffected. Clients
  using it continue working unchanged.
- TWAP support is explicitly deferred. The 400 response includes a
  pointer to this ADR so consumers know the parameter is honored
  on a future release.
- Coverage matrix rows F1.3 (Historical Price Chart) move from
  partial to served.

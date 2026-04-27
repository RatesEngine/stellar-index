---
adr: 0015
title: API rates served from last-closed bucket, never in-progress
status: Accepted
date: 2026-04-27
supersedes: []
superseded_by: null
---

# ADR-0015: API rates served from last-closed bucket, never in-progress

## Context

The Rates Engine deploys to three geographically-separated regions
(Frankfurt / US-East / Singapore per `infrastructure/multi-region-topology.md`).
Postgres replication is mixed: R1→R2 synchronous (~80 ms RTT
makes the commit-time cost tolerable for batched per-ledger
inserts), R1→R3 asynchronous (~165 ms RTT makes sync infeasible
on the hot path).

Even with R2 sync, the three regions are not byte-identical at
any given instant: R3 lags by hundreds of milliseconds to several
seconds depending on link health, and R2's apply-side replay
introduces a small delay between commit-on-R1 and visible-on-R2.
If `/v1/price?asset=XLM&quote=USD` is interpreted as "the rate
computed from all trades observed up to *right now*", three regions
returning answers in parallel can disagree because their local
"right now" views differ. Same code, different inputs → different
outputs.

Disagreement of that shape is unacceptable for a price API: a client
hammering DNS-rotated regions would see the price flicker between
versions.

The Freighter RFP allows ≤30 s data freshness. The schema
(migration 0002) already pre-computes VWAP/TWAP into 1-minute,
15-minute, 1-hour, 4-hour, 1-day, 1-week, 1-month continuous
aggregates. Once a CAGG row for `[12:00:00, 12:01:00]` has been
computed by the primary, that single computed value replicates
deterministically to all replicas — sync OR async, doesn't matter
to the value, only to the time-to-arrival. **Closed-window
aggregations have a fixed identity once the window closes** —
they never change.

## Decision

**API rate endpoints serve the most recent _closed_ aggregate
bucket only. They never serve the in-progress (currently-filling)
bucket.**

Concretely:

- `/v1/price?asset=X&quote=Y[&window=W]` returns the VWAP for the
  most recent fully-closed `W`-window. The response carries the
  bucket's `[from_ts, to_ts]` as the `as_of` field so clients see
  the exact window the rate covers.
- `/v1/vwap`, `/v1/twap`, `/v1/ohlc` follow the same rule: the
  most-recent-row returned for any window is always closed.
- The default window when no `window` param is given is **30 s**
  (matches the Freighter SLA freshness allowance).
- Aggregator orchestration (`internal/aggregate/orchestrator/`)
  is the only writer for these CAGG rows. Once it writes a row
  for window `[t, t+W)`, that row is immutable.

The in-progress bucket — the one the aggregator is currently
filling — is never exposed via the public API. It exists only
internally for the next refresh tick to read from when computing
the next closed row.

## Consequences

- **Positive — same rate, every region.** Three regions querying
  the same window after replication catches up return identical
  bytes. Sync-vs-async replication topology only affects how
  quickly each region converges to the new closed-bucket row;
  once converged, all three serve byte-equivalent JSON. Clients
  see rates that have stopped changing, so by definition they
  can't disagree across regions.

- **Positive — reproducible queries.** A client asking for a rate at
  a specific `as_of` timestamp gets the same answer days later. The
  rate is content-addressed by `(pair, window, from_ts)`.

- **Positive — DR semantics simplify.** A failed-over standby that
  was lagging by a few seconds rejoins as primary; its CAGG rows for
  closed buckets are already byte-identical to what the prior primary
  served. No "did this region ever serve a different value?"
  reconciliation needed.

- **Negative — rates are 0–W seconds old.** The "current price" at
  time `t` is actually the VWAP from `[t-W, t-(t mod W))` — the most
  recent boundary. With W=30 s, an API caller sees a rate up to 30 s
  stale. This is within the Freighter SLA, but worth documenting
  explicitly so consumers don't expect tick-by-tick live data.

- **Negative — high-volatility moments lag visibly.** If XLM
  drops 5 % in a 10-second period, the API still serves the prior
  30-second bucket's VWAP until the new bucket closes. Clients who
  need sub-second tick data should subscribe to the SSE
  `/v1/price/stream` (planned), which can stream individual trades
  rather than aggregated rates.

- **Operational impact — closed-bucket-only is enforced in the
  query layer**, not in storage. The CAGG tables hold both closed
  *and* in-progress buckets (the latter because TimescaleDB's
  refresh policy keeps them current for fast aggregation). The
  query handlers (`internal/api/v1/`) MUST filter out the
  in-progress row by checking that `bucket_to_ts <= now()`. A test
  per endpoint asserts this.

- **Downstream design impact — cross-region traffic routing is now
  trivial.** No need for "primary-region affinity" or "always route
  pricing queries to r1" — any region serves the same answer. DNS
  geo-routing, anycast, or naive round-robin all work.

## Alternatives considered

1. **Sync replication everywhere (R1→R3 sync as well as R1→R2).**
   Rejected: ~165 ms RTT to Singapore puts every commit through a
   trans-Pacific round-trip. The "everywhere-sync" goal of zero-
   data-loss-on-failover doesn't outweigh the cost — galexie data
   is the source of truth, so up-to-≤30 s of postgres rows lost on
   double-failover is recoverable by re-replaying galexie ledgers
   through the indexer, not by needing the data on a third region's
   commit synchronously. R3 stays async (per
   [multi-region-topology.md §5](../architecture/infrastructure/multi-region-topology.md)).

2. **Centralised aggregator that pushes computed rates to a
   shared Redis cluster.** Rejected: cross-region Redis is
   fragile; either you partition (per-region cluster, defeating
   the "shared truth" property) or you pay cross-region writes
   on every aggregator tick. Per-region Redis with CAGG-replicated
   inputs achieves the same property without that complexity.

3. **Serve in-progress bucket but mark it `provisional=true` in
   the response.** Rejected: clients that ignore the flag (or
   library code that strips it) would see flickering. Better to
   not expose the bucket at all; the sub-30 s freshness SLA gives
   us the budget to wait for the bucket to close.

4. **Per-region indexers with no postgres replication, each
   computing its own rate.** Rejected: this is the actively-
   different-rates-per-region failure mode we're avoiding.
   Considered for cost savings (no cross-region WAL bandwidth)
   but the client-experience cost is too high.

## References

- [ADR-0006](0006-timescaledb-for-price-time-series.md) —
  TimescaleDB CAGG-based pre-aggregation (the mechanism that
  makes this design feasible).
- [ADR-0007](0007-redis-cache-schema.md) — Redis as per-region
  cache; cache keys are deterministic on `(pair, window, from_ts)`
  so cross-region cache misses still hit the same value.
- [`docs/architecture/ha-plan.md`](../architecture/ha-plan.md) —
  the multi-region topology this ADR's consistency property is
  designed for.
- [`docs/architecture/aggregation-plan.md`](../architecture/aggregation-plan.md) —
  the orchestrator that writes the CAGG rows this ADR's API surface
  reads from.
- [`migrations/0002_create_price_aggregates.up.sql`](../../migrations/0002_create_price_aggregates.up.sql) —
  the closed-window aggregates this ADR depends on.

---
title: Operator procedure — broad historical CAGG recompute
last_verified: 2026-05-22
status: draft
---

# Broad historical CAGG recompute

A one-shot operator sweep that fills every continuous aggregate
over the full raw range that's preserved in the source hypertable.
You run it once after a retention change widens the raw window,
then never again — the CAGGs' refresh policies cover everything
forward.

## When do I run this

- After **migration 0031** (removed `trades` retention,
  2026-05-14). The trades CAGGs from migration 0002 only cover
  the policy refresh window; the 90+-day raw history that 0031
  preserved isn't in the CAGG until you refresh.
- After **migration 0040** (removed `oracle_updates` retention,
  2026-05-22, #14). Same shape — the 0034 oracle CAGGs cover their
  policy window, the recently-preserved raw history needs a
  one-shot recompute.
- After any **operator-side raw backfill** that lands rows older
  than the CAGG's current oldest bucket. The CAGG's continuous
  policy backfills forward, not backward.

## When NOT to run this

- During r1's peak ingest window (avoid the 14:00–22:00 UTC
  block where SDEX + Soroswap traffic is heaviest — postgres
  is already busy).
- While a heavy r1 job is running (`verify-archive` chain walk,
  bulk-trim, Soroban-era backfill). The recompute is CPU- and
  IO-intensive on its own; pairing it with another heavy job
  amplifies pool pressure.
- If `data` zpool is over 85% capacity. CAGG rows compress well
  but the working chunks during refresh take real space.
- For routine "this CAGG looks stale" complaints — that's the
  [cagg-stale](runbooks/cagg-stale.md) runbook, not this sweep.

## The procedure

`refresh_continuous_aggregate(name, start, end)` rebuilds the
specified CAGG over the `[start, end)` window. `NULL, NULL` means
"every chunk older than the CAGG's `end_offset` policy" — i.e.
the full preserved raw range minus the freshest few minutes.

Run from psql against the `ratesengine` DB as the `ratesengine`
role:

```sh
psql "$RATESENGINE_POSTGRES_DSN"
```

### Trades CAGGs (migration 0002)

```sql
-- Run per-grain rather than as a single transaction; each call
-- can take minutes to hours depending on raw volume.
CALL refresh_continuous_aggregate('prices_1m',  NULL, NULL);
CALL refresh_continuous_aggregate('prices_15m', NULL, NULL);
CALL refresh_continuous_aggregate('prices_1h',  NULL, NULL);
CALL refresh_continuous_aggregate('prices_4h',  NULL, NULL);
CALL refresh_continuous_aggregate('prices_1d',  NULL, NULL);
CALL refresh_continuous_aggregate('prices_1w',  NULL, NULL);
CALL refresh_continuous_aggregate('prices_1mo', NULL, NULL);
```

### Oracle CAGGs (migration 0034)

```sql
CALL refresh_continuous_aggregate('oracle_prices_1m',  NULL, NULL);
CALL refresh_continuous_aggregate('oracle_prices_15m', NULL, NULL);
CALL refresh_continuous_aggregate('oracle_prices_1h',  NULL, NULL);
CALL refresh_continuous_aggregate('oracle_prices_4h',  NULL, NULL);
CALL refresh_continuous_aggregate('oracle_prices_1d',  NULL, NULL);
CALL refresh_continuous_aggregate('oracle_prices_1w',  NULL, NULL);
CALL refresh_continuous_aggregate('oracle_prices_1mo', NULL, NULL);
```

### Pools-per-source CAGG (migration 0036)

```sql
CALL refresh_continuous_aggregate('pools_per_source_1h', NULL, NULL);
```

## Watching it run

Each `CALL` blocks until done. From a second psql session monitor
postgres:

```sql
SELECT pid, query_start, state,
       LEFT(query, 80) AS query
  FROM pg_stat_activity
 WHERE query LIKE '%refresh_continuous_aggregate%'
    OR query LIKE '%_timescaledb_internal%'
 ORDER BY query_start;
```

And the host-side knobs that matter:

```sh
ssh r1 'iostat -x 5 3; df -h /var/lib/postgresql; zpool list data'
```

If pool capacity climbs faster than expected, ABORT the in-flight
refresh and rerun grain-by-grain with smaller manual windows:

```sql
-- Walk the range in 1-week slices instead of a single NULL/NULL call.
CALL refresh_continuous_aggregate(
  'prices_1m', '2026-01-01'::timestamptz, '2026-01-08'::timestamptz);
```

## Estimated runtime

Order-of-magnitude on r1 (2026-05-22, 2.7B trades / sub-1M oracle
observations after the post-0031/0040 preserved window):

| CAGG | First-time refresh | Subsequent (post-sweep) |
| --- | --- | --- |
| `prices_1m`         | hours       | seconds (policy keeps it warm) |
| `prices_15m..1mo`   | minutes–hours each | seconds |
| `oracle_prices_*`   | minutes each (oracle volume is much smaller than trades) | seconds |
| `pools_per_source_1h` | minutes   | seconds |

Total wall-clock for the full sweep on r1: probably 4–8 hours.
Schedule overnight.

## Done — how do I tell

The CAGG's oldest bucket should match the raw hypertable's oldest
row:

```sql
SELECT
  (SELECT MIN(bucket) FROM prices_1m)              AS prices_1m_oldest,
  (SELECT MIN(ts)     FROM trades)                 AS trades_oldest,
  (SELECT MIN(bucket) FROM oracle_prices_1m)       AS oracle_1m_oldest,
  (SELECT MIN(ts)     FROM oracle_updates)         AS oracle_oldest;
```

If the CAGG's oldest bucket lags the raw oldest by more than one
chunk width, a slice of the range is still un-refreshed — run a
targeted `refresh_continuous_aggregate(name, start, end)` for that
window.

## Related

- ADR-0006 — TimescaleDB time-series design (the CAGG hierarchy).
- Migration 0031 header — retention removed on `trades`.
- Migration 0040 header — retention removed on `oracle_updates`.
- [cagg-stale runbook](runbooks/cagg-stale.md) — single-CAGG
  troubleshooting (different problem; that runbook fires when a
  policy refresh has fallen behind, not when raw history exists
  outside the CAGG's window).

---
title: "[SEV-3] Indexer dropping ~1% of trades — Postgres lock-table-full — 2026-05-06"
date: 2026-05-06
severity: SEV-3
status: resolved
started_at: 2026-05-06T15:00:00Z
resolved_at: 2026-05-06T22:39:00Z
affected_components:
  - indexer
  - storage
postmortem:
---

<!--
Customer-facing incident post. Tone, detail level, and cadence per
docs/operations/sev-playbook.md §5.
-->

# [SEV-3] Indexer dropping ~1% of trades — Postgres lock-table-full

## Identification

Some trades arriving on `coinbase`, `binance`, `kraken`, and `sdex`
were not landing in `prices_1m` or `trades` for a window earlier
today. The error rate was small (~1% of trades) and bursty —
clusters of `pq: out of shared memory (53200)` rejections from
the storage layer. No customer-visible API errors; price endpoints
remained available throughout.

## Cause

TimescaleDB's chunk-based storage takes one shared-memory lock per
chunk that an INSERT touches. Postgres's lock table is sized as
`max_locks_per_transaction × max_connections`. We were running on
the Postgres default `max_locks_per_transaction = 64`, with
`max_connections = 200` — a 12,800-entry lock table. Under
concurrent ingest from 11 exchange-class sources, the indexer's
INSERTs occasionally needed to take more locks than the table
could hold, and Postgres returned SQLSTATE 53200 ("out of shared
memory"). The affected INSERTs were dropped (logged + retried at
the source layer; some events past their freshness window were
abandoned).

## Resolution

Bumped `max_locks_per_transaction` from 64 → 256 in
`/etc/postgresql/15/main/postgresql.conf` and restarted Postgres.
The lock table is now 51,200 entries (4× headroom). Brief ~5s
downtime during the restart; indexer/aggregator/api all reconnected
on their own without intervention.

## Verification

Monitored for 5 minutes post-restart:

- 53200 errors: 0 (was ~10/hour)
- `prices_1m` write rate: 887 rows in 5 min — backfill flowing
- All three services back to `active` within 6 seconds of restart

## What customers need to do

Nothing. The window of dropped trades was small and the affected
data is already being re-ingested by the live + backfill paths;
prices_1m buckets converge as the rolling-window fills.

## What we changed

- `postgresql.conf`: `max_locks_per_transaction = 256`
- Backup of pre-change conf at
  `postgresql.conf.bak.20260507-003904` on r1 for rollback.

## What we'll do next

- Track lock-table headroom in Prometheus
  (`pg_locks_count / (max_locks_per_transaction * max_connections)`)
  and page when above 70% saturation.
- Apply the same `max_locks_per_transaction` change to R2 + R3
  ahead of their cutover so the same incident doesn't recur.

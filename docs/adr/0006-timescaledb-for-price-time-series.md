---
adr: 0006
title: TimescaleDB for price time-series storage
status: Accepted
date: 2026-04-22
supersedes: []
superseded_by: null
---

# ADR-0006: TimescaleDB for price time-series storage

> ⚠️ **Retention revised — see migration 0031 (2026-05-14).** The 90-day raw-trade retention and 30-day sub-hourly-aggregate retention described below were REMOVED. Raw trades and all price aggregates are now retained indefinitely. Hourly+ aggregates were always indefinite.

## Context

Stellar Index must retain:

- Raw trades from on-chain (SDEX, Soroswap, Aquarius, Phoenix, Comet,
  Blend), oracle feeds (Reflector, Redstone, Band), CEXes, and FX
  providers — ~150 events/sec network-wide as of 2026-04, growing.
- Derived aggregates at multiple grains (1m, 15m, 1h, 4h, 1d, 1w,
  1mo VWAP / TWAP / OHLC), with 1-hour-and-above retention
  **indefinitely**.
- Since-inception historical pricing backfilled from Galexie.

The storage layer needs to satisfy, simultaneously:

1. **High-throughput, ordered inserts.** Append-only, partitioned by
   time, monotonic within a partition.
2. **Point-in-time window queries.** "VWAP for XLM/USD from T-24h
   to now" must complete in ≤ 200 ms p95 on cached data.
3. **Precise amounts.** i128 Soroban quantities never truncate
   (ADR-0003). Numeric columns with arbitrary-precision storage.
4. **Retention policies.** Raw grains age out after 90 days; hourly+
   aggregates retained indefinitely. Must be declarative, not
   batch-deleted.
5. **Materialised aggregates.** Continuous/incremental materialisation
   so every `/v1/history` read doesn't re-scan raw trades.
6. **Operational maturity.** HA (streaming replication / failover),
   backup/restore, PITR, observability, community-known tuning
   patterns. We can't pioneer our storage layer.

The HA plan ([docs/architecture/ha-plan.md](../architecture/ha-plan.md))
already assumes Patroni-managed Postgres-compatible storage and the
coverage matrix + API design ([docs/reference/api-design.md](../reference/api-design.md))
depend on continuous aggregates at the exact grains the API serves.
We need an ADR to bind the choice rather than leaving "TimescaleDB
(planned)" in a half-dozen downstream docs.

## Decision

**TimescaleDB on PostgreSQL 15** is the primary store for raw trades,
oracle updates, and derived price aggregates.

Structure:

- Raw `trades` hypertable, partitioned by `ts` daily, chunk interval
  1 day, columnar-compressed after 7 days. `ts`+`pair` primary-key
  + source identity index (`source`, `ledger`, `tx_hash`, `op_index`,
  `ts`).
- `oracle_updates` hypertable, same shape as `trades` with an
  `oracle_source` column.
- `events_raw` hypertable for Soroban event blobs, compressed after
  7 days.
- Seven **continuous aggregates** (`prices_1m`, `prices_15m`,
  `prices_1h`, `prices_4h`, `prices_1d`, `prices_1w`, `prices_1mo`)
  built via `CREATE MATERIALIZED VIEW ... WITH (timescaledb.continuous)`
  and kept fresh by `add_continuous_aggregate_policy`.
- Retention policies via `add_retention_policy` — raw retained 90
  days uncompressed, compressed indefinitely; sub-hourly aggregates
  30 days; hourly+ aggregates indefinite.
- Numeric amounts as `NUMERIC` columns (arbitrary-precision, matches
  [canonical.Amount](../../internal/canonical/amount.go) string wire
  form). Never `bigint`.

HA via Patroni (see [multi-region-topology.md](../architecture/infrastructure/multi-region-topology.md)):
one synchronous replica in the sibling region, one async in the
distant region. Backup via pgBackRest to MinIO with 5-min RPO.

## Consequences

**Positive**

- Continuous aggregates directly deliver the required
  timeframes + granularities without us maintaining a separate
  materialisation pipeline.
- Hypertable compression yields ~10× reduction on raw trades
  (documented by Timescale + validated in other pricing systems),
  so our sizing (~500 GB/year compressed per
  [archival-node-spec.md §3.3.1](../architecture/infrastructure/archival-node-spec.md))
  stays tractable.
- Everything Postgres-ecosystem works: Patroni, pgBackRest,
  pg_stat_statements, pgAudit, PgBouncer. Our operational knowledge
  transfers directly; existing tooling (pgadmin, psql, etc.) just
  works.
- The `NUMERIC` type is genuinely arbitrary-precision, not a
  floating-point workaround. i128 amounts round-trip without
  truncation — validated in
  [amount_test.go](../../internal/canonical/amount_test.go) against
  the KALIEN-incident fixture.
- Monorepo discipline (ADR-0005) isn't threatened — Timescale is
  an operational dependency, not a Go module dependency.

**Negative**

- **Licensing asymmetry.** TimescaleDB Community Edition is
  Apache-2.0 on the core hypertable engine but **TSL** (Timescale
  License — source-available, not OSI-approved) on continuous
  aggregates, compression, and data-tiering. We use TSL features.
  This is not a blocker per our Apache-2.0 repo license (we ship
  code, not Timescale binaries), but operators self-hosting must
  accept the TSL on the Timescale binary. Documented in
  [deploy/docker-compose/README.md](../../deploy/docker-compose/README.md).
- **Single-primary write bottleneck.** Patroni gives us HA, not
  horizontal scale. If write throughput exceeds one Postgres
  primary we have to shard, which Timescale supports (distributed
  hypertables are deprecated; the recommended path is app-layer
  sharding). Not a near-term concern at the network's volume.
- **Continuous aggregate refresh adds background CPU.** A badly-
  tuned refresh policy can starve foreground queries. Requires
  operator discipline.
- **Operating Postgres + Timescale is more work than managed
  alternatives** (AWS RDS/Aurora, Timescale Cloud). We pay this
  cost deliberately — see "Alternatives considered" below.

**Operational impact**

- Backups: pgBackRest with WAL streaming, ~5-min RPO. Monthly
  automated restore drill, documented in
  [docs/architecture/ha-plan.md §8](../architecture/ha-plan.md#8-backup--restore).
- Upgrades: Timescale major-version upgrades require `ALTER
  EXTENSION timescaledb UPDATE` + sometimes downtime. Tested in
  staging before production, never skipped.
- Observability: built-in `timescaledb_information` views +
  `pg_stat_statements` → Prometheus exporters. No custom metric
  pipeline required.

**Downstream design impact**

- Aggregation layer (`internal/aggregate`) queries continuous
  aggregates by default, falls back to raw trades only when the
  requested window is inside the uncompressed-raw horizon.
- Retention decisions are declarative SQL in `migrations/` — easy
  to reason about, reviewable, version-controlled.
- Redis cache layer ([ha-plan §3.4](../architecture/ha-plan.md))
  hydrates from Timescale on miss; cache keys map 1:1 to continuous-
  aggregate rows for the "last closed N-period" case.
- API `/v1/history` and `/v1/ohlc` ([api-design.md](../reference/api-design.md))
  queries hit the continuous aggregate matching the requested
  granularity — no on-the-fly rollup.

## Alternatives considered

1. **Plain PostgreSQL (no TimescaleDB extension).** Rejected: we'd
   have to hand-roll chunk management + materialised view refresh
   policies + compression. That's several months of work Timescale
   gives us for free. PostgreSQL declarative partitioning exists
   but is not as operator-friendly for time-series workloads at our
   grain count.

2. **ClickHouse.** Seriously considered. Genuinely faster for
   columnar analytical reads, single-server scales further than
   Timescale. Rejected for three reasons:
   - Ecosystem mismatch — we're a Postgres-native shop, the rest of
     our tooling (ORMs, migrations, Patroni, pgBackRest) doesn't
     apply. Doubling the operational surface costs us more than
     Timescale's per-query speed saves.
   - Mutable data semantics are weaker (CollapsingMergeTree,
     ReplacingMergeTree). Our ingest has real duplicate-handling
     needs (region de-dup, backfill re-runs); Timescale's upserts
     on a hypertable primary key are more natural.
   - Numeric precision: ClickHouse has `Decimal256` but its JSON
     I/O path in several drivers historically truncates. Our i128
     invariant (ADR-0003) demands we be certain about precision at
     every boundary.

3. **InfluxDB (OSS or Cloud).** Rejected. Flux query language is
   its own learning curve; the OSS 2.x line had stability and
   retention-policy usability issues for people at our scale;
   InfluxDB's pricing story has moved several times in the past
   three years (3.x architecture pivot). Risky to build a customer-
   facing pricing SLA on.

4. **Apache Cassandra / ScyllaDB.** Rejected. Write throughput is
   great; read pattern for our use case (time-windowed aggregations
   across many pairs) is an anti-pattern for Cassandra's data
   model. We'd end up building a secondary OLAP layer anyway.

5. **Amazon Timestream.** Rejected. AWS-locked; the multi-region
   story ([multi-region-topology.md](../architecture/infrastructure/multi-region-topology.md))
   requires colo / bare-metal. Open-source self-hosting is a
   hard project constraint. Timestream violates it.

6. **Timescale Cloud (managed).** Rejected for the same self-host
   constraint. Revisit once we have a managed-offering tier of the
   product.

7. **Parquet files in MinIO + DuckDB / Trino at query time.**
   Considered for historical backfill-only storage. Rejected as
   the primary store because query latency won't hit p95 ≤ 200 ms
   for arbitrary windows. **Retained as a future tiered-storage
   option** — aged-out Timescale chunks can be exported to Parquet
   in MinIO, queried via DuckDB for analytical research workloads.
   Not in the v1 scope.

## References

- Related ADRs:
  - ADR-0003 (i128 no-truncation) — `NUMERIC` columns satisfy this.
  - ADR-0002 (MinIO/S3-compat) — MinIO is the backup + tiered-storage
    target.
  - ADR-0005 (monorepo) — Timescale is an operational dependency,
    not a Go-module dep.
- HA + topology:
  - [ha-plan.md §3.3](../architecture/ha-plan.md) — Patroni topology.
  - [multi-region-topology.md §5](../architecture/infrastructure/multi-region-topology.md)
    — sync + async cross-region replication shape.
- External:
  - TimescaleDB docs — <https://docs.timescale.com/>
  - Continuous aggregates — `CREATE MATERIALIZED VIEW ... WITH (timescaledb.continuous)`
  - Compression — Timescale columnar compression design docs.
  - Timescale License (TSL) — <https://github.com/timescale/timescaledb/blob/main/tsl/LICENSE-TIMESCALE>

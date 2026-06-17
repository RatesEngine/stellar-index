---
title: Ecosystem review — Tillman (withObsrvr) and orbitlens (StellarExpert) feedback
last_verified: 2026-05-03
status: living doc
---

# Ecosystem review, 2026-04-23

On 2026-04-23 a Stellar infra operator solicited advice from the
three largest ecosystem indexers / explorers about **ingestion
strategy, DB/storage for full history, and partitioning**. This
doc captures their replies and how each point maps onto our
current architecture.

## Sources

- **Tillman (withObsrvr)** — indexing operator; author of
  `stellar-extract`, `nebu`, `flowctl`.
- **orbitlens (StellarExpert)** — author of
  [StellarExpert](https://stellar.expert), one of the three largest
  explorers.

## Tillman's summary

> *Start with Galexie as the archive. Full history < 4 TB. From there,
> either use RPC or the archive directly for ingestion using the
> Ingest SDK + ledgerbackend. To build internally, medallion
> architecture ~ streaming data lakehouse — hot data in a DB, flushed
> to cold object storage after a period. Do this for each medallion
> layer (Bronze, Silver).*

### How it maps to us

| Point | Our status |
| --- | --- |
| Galexie as the archive | ✅ Live on r1 (since 2026-04-23). Single captive-core on the box produces `LedgerCloseMeta` to MinIO. |
| Full history < 4 TB | 🧪 Our r1 box has 13 TB usable — ~3× overhead. Comfortable even with 3× growth + experimental envs. Worth confirming by completing the stellar-archivist mirror currently in progress. |
| Ingest SDK + ledgerbackend | ✅ Direction locked in [ADR-0013](../adr/0013-go-stellar-sdk-xdr-for-scval.md). The `github.com/stellar/go-stellar-sdk/ingest` package is our planned consumer. |
| Medallion (Bronze → Silver → Gold → Cold) | 🧪 **Not formalized yet.** Bronze (raw LCM in MinIO) exists; Silver (typed decoded rows in TimescaleDB) + Gold (aggregates) are planned; cold-storage flush boundary is undecided. **Open: ADR needed** — candidate ADR-0014. |

## orbitlens's summary (seven points)

### 1. Decide what to keep + granularity first

> *Some data can be recreated on the fly from raw XDRs. People are
> generally ok if historical stats for assets/accounts (transfers,
> trades, balances) display at hourly or even daily granularity.*

**Our status:** ✅ Partially addressed. Our history
grain set (1m / 15m / 1h / 4h / 1d / 1w / 1mo) drives our planned
CAGGs in `migrations/0002_create_price_aggregates.up.sql`. Retention
policy is documented (sub-hourly for 30 days, hourly+ indefinitely).
We keep raw trades in the hot DB + recreatable-from-MinIO XDRs for
anything dropped — which matches orbitlens' framing. What's *not*
yet decided is when to age raw trades to cold storage (see Tillman
point above).

### 2. ⚠️ Do NOT use sequential numeric IDs

> *It seems like a good idea for foreign keys and joins, but in reality
> such normalization prevents parallel ledger ingestion and sometimes
> can have nasty consequences if something goes wrong with the
> ingestion pipeline.*

**Our status:** ✅ **Addressed by design.** Audit on 2026-04-23
(task #166) across the entire repo found **zero** `SERIAL /
BIGSERIAL / GENERATED AS IDENTITY / AUTO_INCREMENT / nextval`
references in either migrations or Go storage code. All primary
keys are natural composites:

- `trades`: `PRIMARY KEY (source, ledger, tx_hash, op_index, ts)`
- `oracle_updates`: `PRIMARY KEY (source, ledger, tx_hash, op_index, ts)`
- `ingestion_cursors`: `PRIMARY KEY (source, sub_source)`
- `price_aggregates`: continuous aggregates (no PK, Timescale-managed)

This supports orbitlens' parallel-ingest pattern directly — workers
can insert rows from disjoint ledger ranges without coordinating on
an ID counter.

### 3. Galexie is probably the best tool for initial ingestion

> *We have our own custom implementation, but that's because we
> developed it before SDF presented this tool.*

**Our status:** ✅ Galexie is our producer. Already running on r1.

### 4. Look for databases with native i128 / i256 (ClickHouse noted)

> *SEP-41 token balances are stored as i128. ClickHouse has native
> i128 support. Without it, balance sorting and aggregation gets
> tricky.*

**Our status:** ⚠️ **Worth documenting as an alternative.** We picked
TimescaleDB for price-time-series in [ADR-0006](../adr/0006-timescaledb-for-price-time-series.md).
Postgres `NUMERIC` is unlimited precision (compatible with ADR-0003
i128-no-truncation) but slower than native i128. Aggregate
performance at scale is a known concern. **Follow-up:** addendum
to ADR-0006 capturing ClickHouse as the documented alternative
lever if TimescaleDB aggregate perf becomes a bottleneck, with a
migration ramp described.

### 5. Partition by year for historical data

> *We partition historical data by year (tx history, trades, etc).
> Not ideal but it keeps indexes small and improves cache. >94% of
> requests focus on history within the last year.*

**Our status:** 🧪 **Needs verification.** TimescaleDB defaults to
7-day chunks on hypertables — ideal for hot data, sub-optimal for
multi-year archive queries. At our planned scale (~millions of
trades/day × N years), multi-year queries across many chunks will
slow down. **Follow-up:** document our chunk-interval strategy,
possibly with a tail-chunk coalescing policy or a separate
year-partitioned cold table driven by the medallion flush.

### 6. Cache is king

> *Impossible to handle real API traffic without a proper caching
> strategy. Each request category requires individual cache
> granularity.*

**Our status:** ✅ Cache layer specified in [ADR-0007](../adr/0007-redis-cache-schema.md).
Cache-key conventions live in `internal/cachekeys/`. Per-grain TTLs
are planned in the query plane (sub-minute cache for `/v1/price`,
longer for coarser `/v1/history` grains).

### 7. At least 2 servers: prod + experimental

> *Most significant architectural changes require complete ledger
> history reingestion. We run 6 servers: 3 + 3 clusters, one
> stand-by / experimental.*

**Our status:** ⚠️ **Our multi-region plan has r1/r2/r3 but no
explicit "experimental" slot.** [ADR-0004](../adr/0004-tier1-validator-aspiration.md)
locks the geographic distribution for Phase 3 validators, not
experimental workloads. **Follow-up:** designate one of the boxes
(or add an "r-exp" slot) for schema-migration testing before
promoting to prod. Cheaper to do before we pin the layout.

## Summary — where this points us

| Area | Action |
| --- | --- |
| **ADR-0014 Medallion architecture** | WRITE — captures hot/cold boundary, Bronze-Silver-Gold layout, retention-to-cold policy. Highest-value durable artifact from this review. |
| ADR-0006 addendum — ClickHouse alternative | Document as a lever. Not a switch. |
| Chunk-strategy note on trades hypertable | 7-day hot chunks + year-boundary archive strategy. Write against the migrations dir or ADR-0006. |
| r-exp slot in multi-region plan | Update [ADR-0004](../adr/0004-tier1-validator-aspiration.md) (or successor) with experimental/stand-by designation. |
| Sequential-ID audit | ✅ Done — task #166. Clean. |

## Provenance

Verbatim conversation archived in chat history
2026-04-23. Full-conversation snippets available from
@ash on request; this doc summarizes under fair-use for
internal planning only.

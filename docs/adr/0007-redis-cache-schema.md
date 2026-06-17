---
adr: 0007
title: Redis as hot-path cache + rate-limit + ephemeral state
status: Accepted
date: 2026-04-22
supersedes: []
superseded_by: null
---

# ADR-0007: Redis as hot-path cache + rate-limit + ephemeral state

> **Amendment (2026-06-12, F-1353 / D2-07).** Where this ADR describes
> Redis HA as **Cluster** mode, the topology was later decided as
> **Sentinel, not Cluster** — see ADR-0024 (Redis HA via Sentinel).
> The cache-schema decision below stands as written; only the HA
> mechanism differs. The decision below is preserved as the original
> record.

## Context

The API p95 ≤ 200 ms latency SLA is only
achievable if the hot path is memory-cached. Round-tripping every
`/v1/price` query through a Postgres continuous aggregate would
cost 5–20 ms on the DB alone; under 2 000 rps burst that saturates
the primary.

Separately, the API needs a per-key rate-limit counter that
expires cleanly, a short-lived SEP-1 metadata cache (15 min TTL),
a short-lived asset-metadata cache (5 min TTL), and a per-channel
SSE subscriber registry.

These are four distinct workloads that all fit Redis cleanly —
key-value with TTL, atomic counters, Pub/Sub. Using one Redis
cluster for all of them is cheaper than introducing another
cache/broker (memcached, NATS) alongside.

HA plan §3.4 already sketches the Redis topology (3 masters + 3
replicas, Redis Cluster mode, Sentinel). This ADR locks the
**key schema + TTL + persistence posture** that API handlers,
the aggregator, and the rate-limiter all depend on.

## Decision

**Redis is the single hot-path cache + rate-limit store +
ephemeral-state store for Stellar Index.** One cluster serves every
workload listed below; no secondary Redis/Memcached/KV for any
component.

Key schema:

| Key pattern | Purpose | Value type | TTL | Writer | Reader |
| --- | --- | --- | --- | --- | --- |
| `price:<asset_id>` | Latest aggregated price (the `/v1/price` hot path) | JSON string, ~300 B | **60 s** | aggregator | api |
| `vwap:<base>:<quote>:<window>` | Pre-computed VWAP for a specific window | JSON string, ~200 B | matches window (60s / 300s / 900s / …) | aggregator | api |
| `ohlc:<base>:<quote>:<granularity>:<bucket>` | One OHLC candle (closed candle = immutable) | JSON string, ~250 B | 1 hr for open candles; no TTL for closed candles (CDN pinned) | aggregator | api |
| `rl:<api_key>:<min>` | Rate-limit counter for a key in a minute | INCR integer | 120 s | api | api |
| `rl:<ip>:<min>` | Rate-limit counter for an IP in a minute (anonymous tier) | INCR integer | 120 s | api | api |
| `toml:<domain>` | Cached `<home>/.well-known/stellar.toml` parse | JSON string | **15 min** | api (lazy, on miss) | api |
| `meta:<asset_id>` | Asset metadata (code, issuer, decimals, …) | JSON string | **5 min** | api (lazy) / indexer (eager invalidate) | api |
| `sub:<channel>:<subscriber_id>` | SSE subscriber registry (is-alive flag) | `1` | 60 s (heartbeat-renewed) | api | api |
| `div:<asset_id>` | Latest divergence-detection result per asset | JSON | 5 min | divergence worker | api |
| `health:<source>` | Per-source freshness gauge | JSON | 60 s | indexer | api, /metrics |

Hash-tag `{stellarindex}` is NOT used — we accept cluster slot
distribution as the natural load spread. Re-evaluate if a future
"must be on one node" workload appears.

Persistence:

- **AOF every-second**. Matches our tolerance for ≤ 1 s of data
  loss in the cache — everything is re-derivable from Timescale
  anyway.
- **RDB nightly** at 03:00 UTC for a secondary backup target.
  Shipped to MinIO.
- Max-memory policy: **`allkeys-lru`**. Under memory pressure,
  least-recently-used keys evict first; the aggregator re-warms
  on next read.

Failure modes:

- **Cache-miss on hot key** → handler falls back to Timescale
  query, populates Redis on the way back. Cold-cache latency ≤
  50 ms p95.
- **Full Redis outage** → handlers continue reading Timescale
  directly; `stale_flag=true` on every response until Redis
  recovers (because the aggregator can't write fresh hot prices).
- **Wiped Redis** (after a failover) → the aggregator re-warms
  the top-N assets from Timescale within ~2 min of startup. Rate-
  limit counters reset; users get a free minute (acceptable).
- **Redis cluster split** (one master unreachable) → Sentinel
  failover within 15–30 s. Keys on the affected slot return
  timeout errors during the window; handlers fall back to
  Timescale + mark `stale_flag=true`.

## Consequences

**Positive**

- p95 ≤ 200 ms on the primary endpoints (`/v1/price`, `/v1/ohlc`
  closed candles) is achievable with this cache + CDN on top.
- One operational surface — one Redis to monitor, backup, upgrade.
- Rate-limiting is atomic + sharded (cluster mode distributes
  `rl:<api_key>:<min>` naturally across masters).
- SEP-1 and asset-metadata caches deduplicate upstream traffic
  (home domains would otherwise be hammered on every asset lookup).
- Closed-candle keys with no TTL work with CDN pinning — the same
  value serves browser requests forever without cache churn.

**Negative**

- Cache + source-of-truth divergence is possible during Redis
  failover (handlers serve stale until Sentinel promotes).
  Mitigated by the 60 s TTLs on non-immutable keys.
- Rate-limit reset on Redis wipe is a real (small) abuse window.
- Cluster-mode Redis is operationally more complex than single-
  master Redis + Sentinel. The HA plan picked cluster mode; this
  ADR inherits.
- We're locked into Redis 7+ for cluster-mode features
  (streams aren't used today but `XADD` is a natural fit for the
  SSE subscriber side if we outgrow the current polled registry).

**Operational impact**

- Memory sizing: baseline ~1 GB (hot prices + metadata caches);
  peak ~4 GB under high subscriber load. Cluster at 512 MB×3 masters
  = 1.5 GB usable (replication doubles raw) is adequate for
  launch.
- Backup window: ~100 MB RDB nightly; fits trivially in MinIO
  backups bucket.
- Upgrade cadence: Redis minor versions via rolling replica-first;
  major versions via planned window.

**Downstream design impact**

- `internal/cache/redis/` package (future) owns the key grammar —
  every caller constructs keys through typed helpers, never raw
  strings. Prevents "someone forgot the `price:` prefix" bugs.
- `internal/ratelimit/` uses `rl:*` keys via Lua script (atomic
  INCR+EXPIRE).
- `internal/api/sse/` uses the `sub:*` heartbeat pattern.
- `internal/aggregate/` writes `price:*`, `vwap:*`, `ohlc:*` on
  every computation cycle.

## Alternatives considered

1. **In-process LRU caches (one per API pod).** Rejected: each
   pod sees stale data until the LRU expires; cache hit rate
   drops 3× with a 3-pod fleet; aggregator has no way to
   invalidate without a pub/sub that we'd have to build.

2. **Memcached.** Rejected for missing features — no atomic
   counters (rate limit needs INCR-with-expire), no TTL-per-key
   on SET (all keys share server-default), no cluster mode as
   mature as Redis Cluster.

3. **PostgreSQL UNLOGGED tables as cache.** Rejected: Postgres is
   already the write-heavy primary. Adding cache reads to its
   hot path defeats the purpose of a separate tier.

4. **DragonflyDB / KeyDB.** Both Redis-protocol-compatible,
   performance-competitive. Interesting but neither has the
   operational ecosystem maturity (Sentinel + pgBackRest-equiv
   tooling + managed-offering fallback) we want for launch. File
   as "revisit if measured Redis becomes a constraint" in the
   post-launch roadmap.

5. **No cache; serve everything from Timescale + CDN.** Rejected:
   CDN absorbs the immutable-closed-candle workload but not the
   1–5 s TTL hot-price workload. Origin at 2 000 rps on
   Timescale CAGGs breaks the SLA.

## References

- Related ADRs:
  - ADR-0006 (TimescaleDB) — the source-of-truth Redis caches in
    front of.
- Design docs:
  - [HA plan §3.4](../architecture/ha-plan.md#34-redis-cluster)
    — topology (3+3 Redis Cluster + Sentinel).
  - [API design §8.2](../reference/api-design.md#82-etag--conditional-get)
    — HTTP cache headers that sit in front of Redis.
  - [Repo hygiene plan §19](../architecture/repo-hygiene-plan.md#19-the-hygiene-bill--the-sum-of-all-ci-checks)
    — cadence commitment for key-grammar reviews.
- External:
  - Redis Cluster spec — <https://redis.io/docs/management/scaling/>
  - Sentinel — <https://redis.io/docs/management/sentinel/>
  - AOF + RDB — <https://redis.io/docs/management/persistence/>

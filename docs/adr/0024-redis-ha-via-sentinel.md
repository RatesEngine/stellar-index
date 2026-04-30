---
adr: 0024
title: Redis HA via Sentinel (not Cluster)
status: Accepted
date: 2026-04-30
supersedes: []
superseded_by: null
---

# ADR-0024: Redis HA via Sentinel (not Cluster)

## Context

[`docs/architecture/ha-plan.md §3.4`](../architecture/ha-plan.md)
describes the Redis topology as:

> "3 masters + 3 replicas, **Redis-Cluster mode (hash slots)**.
> Replicas on separate hosts from their masters. **3 sentinels
> on independent hosts for failover vote.**"

This is internally inconsistent. Redis Cluster and Redis
Sentinel are two different HA modes:

| Mode | Sharding | Failover | Sentinel processes? |
|---|---|---|---|
| **Redis Cluster** | yes (hash slots; data spread across N masters) | internal — cluster nodes vote among themselves | no — Cluster has no Sentinel |
| **Redis Sentinel** | no — single primary + replicas | external — N (≥3) Sentinel processes monitor the primary, elect a new one on failure | yes |

The ha-plan's "Cluster mode... 3 sentinels for failover vote"
combines the two — likely the original author wrote "Cluster
mode" colloquially meaning "clustered deployment" and the ADR
process never tightened the term.

This ADR ratifies the **Sentinel** choice and asks ha-plan §3.4
to be amended for terminological consistency the next time it
ships.

## Decision

**Redis HA via Sentinel.** Concretely:

- 1 primary + 2 replicas across 3 hosts (`cache-01` / `02` / `03`).
- 3 Sentinels co-located on the same 3 cache hosts (one per
  host), `quorum=2` for promotion votes.
- No sharding. The keyspace lives entirely on the primary; each
  replica holds a full async copy.
- Persistence: AOF `everysec` + RDB nightly (per ha-plan §3.4
  unchanged).
- Failover RTO: 15-30 s (per ha-plan §3.4 unchanged).
- Client connect via `go-redis/v9 FailoverClient`, which
  consults Sentinel for the current primary; no HAProxy or
  keepalived VIP needed in front of Redis.

## Why Sentinel rather than Cluster

1. **Hot-data set is small.** ha-plan §3.4 enumerates the
   hot-data categories (price cache + VWAP precompute +
   rate-limit + SEP-1 + asset-metadata + SSE registry). The
   total fits comfortably in single-primary RAM at expected
   launch scale. Sharding adds complexity that solves no
   current capacity problem.

2. **Operational simplicity.** Sentinel has fewer moving parts
   to debug under SEV-1 stress (3 Sentinels + 3 redis-server
   processes vs Cluster's per-node gossip + slot-migration
   bookkeeping).

3. **Migration path stays open.** If we outgrow Sentinel's
   capacity ceiling later, migrating to Cluster is a one-time
   cost, not an ongoing tax. Premature sharding would tax every
   feature that touches Redis (keyspace partitioning, SLOT-aware
   pipelining, etc.).

4. **Client-library support is uniform.** `go-redis/v9` exposes
   `FailoverClient` for Sentinel and `ClusterClient` for
   Cluster. We already use the simpler `Client` shape; the
   `Client` → `FailoverClient` change is one constructor call.
   `ClusterClient` requires more invasive changes
   (slot-aware hashing of key prefixes for multi-key ops, etc.).

## Consequences

- **Positive — ops surface stays small.** Three Sentinels +
  three redis-servers, no extra processes, no HAProxy in front.
- **Positive — `internal/cachekeys` change is minimal.** Switch
  the constructor from `redis.NewClient` to
  `redis.NewFailoverClient(redis.FailoverOptions{ MasterName,
  SentinelAddrs, ... })`. Cache-key surface unchanged; existing
  call sites untouched.
- **Negative — capacity ceiling.** All hot data through one
  primary's RAM. Mitigation: monitoring at 75 % `maxmemory`
  (warn) + 90 % (page); migration plan to Cluster documented
  if/when we approach the ceiling.
- **Negative — single-primary write path.** Reads can be served
  from replicas via `ReadOnlyReplicas` mode if needed; writes
  only the primary. Acceptable since writes are dominated by
  the aggregator's bulk refresh which is throughput-tolerant.
- **Negative — Sentinel itself can split-brain.** With `quorum=2`
  on 3 Sentinels, a 2-1 partition lets the larger side promote;
  the partitioned-1 side won't. Correct behaviour, documented
  in the runbook.

## Alternatives considered

- **Redis Cluster** (the as-written ha-plan §3.4 wording) —
  rejected per "Why Sentinel" above. Reconsider only if hot-
  data approaches the single-primary RAM ceiling.
- **Single-host Redis with replication-only** (no Sentinel) —
  rejected because failover would be manual; the SEV-1
  drill-scenario timeline (`docs/operations/drills/scenarios/`)
  assumes automatic failover.
- **Managed Redis SaaS (Elasticache / Upstash / Redis Labs)** —
  rejected because we self-host everything else by ADR-0008;
  introducing a SaaS dependency for HA-cache only is asymmetric.
- **KeyDB** (multi-master Redis fork) — rejected because the
  fork's bus factor is uncertain and we'd be the first
  Stellar-side team running it; not the right risk profile for
  the cache layer.

## Implementation notes

The Patroni ansible role design note (Task #72 sub-role) and
the Redis Sentinel ansible role design note (companion) cover
the implementation shape. ha-plan §3.4 should be amended in the
same PR that ships the Redis Sentinel ansible role to remove
the Cluster/Sentinel terminology contradiction.

## References

- [`docs/architecture/ha-plan.md §3.4`](../architecture/ha-plan.md) — current (contradictory) topology description; amended when Task #72 Redis Sentinel sub-role lands.
- [Redis Sentinel docs](https://redis.io/docs/latest/operate/oss_and_stack/management/sentinel/) — upstream reference.
- [`go-redis FailoverClient`](https://pkg.go.dev/github.com/redis/go-redis/v9#NewFailoverClient) — client-side API.
- [`docs/operations/runbooks/redis-master-down.md`](../operations/runbooks/redis-master-down.md) — runbook this ADR's choice makes work the "happy path".
- [`docs/operations/drills/scenarios/`](../operations/drills/) — drill scenarios that depend on automated failover.

---
title: Runbook — redis-memory
last_verified: 2026-05-03
status: draft
severity: P2
---

# Runbook — `ratesengine_redis_memory_saturated` / `_evictions_high`

## At a glance

| Field | Value |
| ----- | ----- |
| Alerts | `ratesengine_redis_memory_saturated` (> 90 % memory), `ratesengine_redis_evictions_high` (> 100/s) |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/cache.yml` |
| Typical MTTR | 30 min (scale-up) – hours (cleanup / policy change) |
| Impact | `allkeys-lru` starts evicting. Hot keys may get knocked out; cache hit-rate drops; API falls back to Timescale more often → elevated p95/p99 latency. Rate-limit counters can get evicted early → some clients get fresh quotas. |

## Symptoms

- `redis_memory_used_bytes / redis_memory_max_bytes > 0.90` for ≥ 5 min, **or**
- `rate(redis_evicted_keys_total[5m]) > 100` for ≥ 5 min.
- API panel: `ratesengine_sep1_cache_ops_total{result="miss"}`
  rate climbs; hit-rate drops.
- Latency alert may follow if the miss storm hits a popular asset.

## Quick diagnosis (≤ 5 min)

```sh
# Current memory usage + maxmemory policy
redis-cli info memory | grep -E 'used_memory_human|maxmemory_human|maxmemory_policy'

# What's filling it up? `--bigkeys` samples one key per type.
redis-cli --bigkeys

# Top eviction-rate shard (if sharded)
for shard in redis-0 redis-1 redis-2; do
  echo "=== $shard ==="
  redis-cli -h $shard info stats | grep -E 'evicted_keys|keyspace_misses|keyspace_hits'
done

# Is it one oversized key or many small ones?
redis-cli --memkeys   # requires redis-cli 6.0+
```

## Typical root causes

1. **Legitimate growth — more assets cached than the box is sized
   for.** Symptom: steady climb over days/weeks, eviction rate
   grew alongside.
   - Mitigation: scale up `maxmemory` (if host has headroom) or
     scale out (shard). `maxmemory` is set in
     `configs/ansible/roles/redis-sentinel/templates/redis.conf.j2`
     — bump and re-apply the role.

2. **Key-explosion bug.** A handler writes per-request cache keys
   without TTL, or with overly long TTLs, and the key-space
   balloons.
   - Signal: `info keyspace` shows a `dbN:keys=...` count way
     larger than distinct assets × 2 (the rough theoretical
     ceiling: price per pair + sep1 resolver per issuer + rate-
     limit counter per API key).
   - Mitigation: identify the bad writer (probably a recent PR),
     add a TTL + cap, deploy, then `FLUSHDB` or let LRU handle it.

3. **Someone used it as a queue / list.** Redis's `LPUSH` /
   streams patterns without bounds can grow unbounded.
   - Signal: `memkeys` / `--bigkeys` shows a single key (stream /
     list) dominating memory.
   - Mitigation: cap with `MAXLEN ~` on streams, or truncate the
     list manually, or move that workload elsewhere. Redis isn't
     our queue.

4. **Very large rate-limit window counters** (sliding-window log
   impl that stores one element per request per key). If someone
   deployed a sliding-log limiter instead of the intended
   token-bucket, memory scales with traffic.
   - Signal: keys matching the rate-limit prefix are ~KB each.
   - Mitigation: rollback to the token-bucket Lua in
     `internal/ratelimit/`.

## Mitigation

- [ ] Step 1 — figure out which pattern is growing (diagnosis above).
- [ ] Step 2 — if key-explosion: rollback / fix the writer. Don't
      just `FLUSHDB` to "reset" — the bug will refill it. Fix the
      source first.
- [ ] Step 3 — if legitimate growth: scale up. `maxmemory` bumps
      are zero-downtime if Redis has host headroom
      (`CONFIG SET maxmemory <new>` + persist to the ConfigMap).
- [ ] Step 4 — if a single big key: delete it or cap it
      (`UNLINK <key>` is non-blocking; `DEL` blocks).
- [ ] Verification: memory drops under 80 %, eviction rate back to
      baseline (roughly 0 during normal ops for a right-sized cache).

## Root cause analysis

- Keyspace growth curve over 30 days (dashboard).
- Which key prefix(es) grew — `--bigkeys` is a snapshot; ideally
  run `SCAN 0 MATCH <prefix>:*` across prefixes to count per
  namespace.
- Who shipped the growth pattern (git blame on the writer).
- Is the alert threshold (90 %) still right or is the box simply
  underprovisioned?

## Known false-positive patterns

- **Just after a deploy** that adds a new cache namespace: brief
  spike as the cache warms; subsides once LRU prunes cold entries.
  Should not cross the 5-min `for:` threshold.
- **Cleanup job side-effect**: large `SCAN + DEL` sweeps create
  brief memory spikes (the returned keys list). Bounded — ignore.

## Related

- `redis-master-down.md` — OOM-kill is a common cause of this
  escalating into a full master outage.
- `api-latency.md` — downstream effect when eviction hits popular
  keys.
- ADR-0007 (key schema + TTL conventions).

## Changelog

- 2026-04-23 — initial draft.

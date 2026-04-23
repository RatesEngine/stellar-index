---
title: Runbook — redis-master-down
last_verified: 2026-04-23
status: draft
severity: P1
---

# Runbook — `ratesengine_redis_master_down`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_redis_master_down` |
| Severity | P1 (page — SEV-1) |
| Detected by | `deploy/monitoring/rules/cache.yml` |
| Typical MTTR | 1–15 min (Sentinel-driven failover: < 1 min; manual: longer) |
| Impact | Hot-path cache for `/v1/price` gone. Rate-limiter fails open (no throttling). Clients still get served via Timescale fallback with `stale=true` and increased latency — so not an outage, but a **degraded SLA** + **no rate-limiting** (fail-open abuse window). |

## Symptoms

- `redis_up{role="master"} == 0` for ≥ 30 s on some shard.
- API latency rises (cache miss → Timescale path).
- `ratesengine_ratelimit_fail_open_total` counter jumps — this
  metric is the deliberate "Redis outage" signal (fail-open by
  design per HA plan §3.4).
- API logs: "redis get: connection refused" or
  "cache miss: pool exhausted".

## Quick diagnosis (≤ 5 min)

```sh
# Is it a single instance or the whole shard?
kubectl -n ratesengine get pods -l app=redis -o wide
redis-cli -h redis-0 ping
redis-cli -h redis-1 ping
redis-cli -h redis-2 ping

# Check Sentinel's view of the world
redis-cli -h redis-sentinel -p 26379 sentinel masters

# Is the host up but redis process dead?
kubectl -n ratesengine logs redis-0 --tail=100
kubectl -n ratesengine describe pod redis-0 | tail -30
```

## Typical root causes

1. **Sentinel mid-failover.** Redis Sentinel promotes a replica on
   master failure. Detection is fast (< 30 s) but the alert `for:
   30s` means we sometimes page right as Sentinel is resolving it.
   Wait one poll interval; if Sentinel's `sentinel masters` shows
   a new master, the alert will clear.

2. **OOMKilled on the master host.** Redis's `maxmemory` setting
   is independent of the kernel's view — if the host memory is
   under pressure (noisy neighbor, something else leaking), the
   kernel OOM-killer takes Redis.

3. **Persistence write stalled the primary.** AOF rewrite or an
   RDB save on a large dataset blocks `fork()` — seems to clients
   like the master is down because responses stall.
   - Signal: `redis-cli info persistence` shows `rdb_last_bgsave_status:err`
     or a running `aof_rewrite` that's been going for > 60 s.

4. **Network partition** between the API pods and the master. If
   the master is alive but unreachable from the API, Sentinel sees
   it and fails over. `up{role="master"}` from Prometheus's POV
   is 0 if Prometheus is in the same partitioned zone as the API.

## Mitigation

- [ ] Step 1 — check Sentinel's view first. If failover is in
      progress, hold — it should complete in seconds.
- [ ] Step 2 — if Sentinel's stuck: `redis-cli sentinel failover
      <mastername>` forces a promotion. Do this with a clear head;
      forcing failover on a transient network blip can split-brain
      if clients see the old master on recovery.
- [ ] Step 3 — if the master host is the problem: `kubectl delete
      pod redis-X` (StatefulSet recreates it as a replica; if it
      was the primary, Sentinel had already promoted a successor).
- [ ] Step 4 — verify the promoted replica is caught up:
      `redis-cli info replication` on the new master should show
      `master_repl_offset` equal across all followers.
- [ ] Verification: `up{role="master"} == 1`; API logs show
      `redis: reconnected`; `ratelimit_fail_open_total` rate drops
      to zero (it's cumulative, so watch the rate).

## Data loss considerations

- `/v1/price` hot-cache entries are re-derivable from Timescale on
  next request. Zero data loss risk there.
- Rate-limit counters are stored in Redis with ~1 min TTL. A
  failover resets them to zero; clients who were throttled get a
  fresh quota. Acceptable.
- API keys / SEP-10 session tokens (when they land) must not live
  only in Redis — always back by Timescale. See `internal/auth/`
  when implemented.

## Root cause analysis

- Sentinel log — ordered events: `+sdown`, `+odown`, `+new-epoch`,
  `+switch-master`.
- Redis log from both old and new masters around the event.
- Host-level: OOM log (`dmesg | grep -i oom`), load avg, network.
- Was a rolling restart in progress? If so, was the rollout policy
  respecting the Sentinel quorum?

## Known false-positive patterns

- **Rolling restart of the Redis StatefulSet**: our PodDisruptionBudget
  should prevent a quorum-loss, but rolling the master always
  trips this alert for ~30 s while Sentinel promotes. Muting the
  alert for the duration of a planned maintenance is acceptable.
- **Prometheus-exporter crash (not Redis crash)**: `redis_up`
  comes from `redis_exporter`. If the exporter sidecar died but
  Redis is fine, we page on a phantom outage. Check the exporter's
  own health before acting.

## Related

- `redis-memory.md` — OOM / eviction issues.
- `redis-replication.md` — replicas not following.
- HA plan §3.4: `docs/architecture/ha-plan.md` (Redis topology,
  fail-open rationale).
- ADR-0007 (key schema) — `docs/adr/0007-redis-key-schema.md`.

## Changelog

- 2026-04-23 — initial draft, called out the fail-open behaviour
  as a deliberate design choice (not a bug to fix).

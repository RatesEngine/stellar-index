---
title: Runbook — redis-master-down
last_verified: 2026-05-02
status: ratified (Sentinel-driven failover is the default after `redis-sentinel` ansible role landed)
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

Redis runs as `redis-server.service` + `redis-sentinel.service` on
three bare-metal hosts `cache-01..03` (per the `redis-sentinel`
ansible role; ADR-0008 §3.4). Per-host primary role is in the
inventory's `redis_role` var; current role at runtime comes from
Sentinel.

```sh
# Is it a single instance or the whole shard?
for h in cache-01 cache-02 cache-03; do
  echo -n "$h: "
  redis-cli -h $h -a "$REDIS_PASSWORD" ping
done

# Sentinel's view of the world (any one cache host serves)
redis-cli -h cache-01 -p 26379 -a "$REDIS_PASSWORD" \
  SENTINEL masters

# Is the host up but redis-server process dead?
ssh root@cache-01 "systemctl status redis-server --no-pager | head -15"
ssh root@cache-01 "journalctl -u redis-server -n 100 --no-pager"

# Sentinel itself running?
ssh root@cache-01 "systemctl status redis-sentinel --no-pager | head -10"
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

### A. Automatic Sentinel failover — the happy path

After the `redis-sentinel` ansible role landed, this is the
**default** path. Sentinel's `down-after-milliseconds=5000` +
`failover-timeout=60000` mean a primary failure typically
recovers in 15–30 s without operator intervention. The
`go-redis/v9` `FailoverClient` clients re-discover the new
primary automatically — no app restart required.

- [ ] Step 1 — check Sentinel's view first. If failover is in
      progress, hold — it should complete in 15–30 s.
      `redis-cli -h cache-01 -p 26379 -a "$REDIS_PASSWORD" \
       SENTINEL get-master-addr-by-name ratesengine-r1-cache`
      returns the current primary; `ratesengine_redis_sentinel_primary`
      gauge sums to 1 across hosts when steady-state.
- [ ] Step 2 — verify clients reconnected. API + aggregator logs
      should show `redis configured mode=sentinel` at startup;
      after failover, look for "redis: reconnected" or absence
      of "connection refused". `ratelimit_fail_open_total` rate
      drops back to zero.

### B. Stuck or split-brain — manual failover

Resort to this **only** if Sentinel's automatic failover hasn't
completed within 60 s OR `SENTINEL ckquorum` reports < 2 alive
sentinels.

- [ ] Step 1 — confirm the stuck state:
      `redis-cli -p 26379 -a "$REDIS_PASSWORD" SENTINEL ckquorum ratesengine-r1-cache`
      and `SENTINEL master ratesengine-r1-cache` (look for
      `last-ok-ping-reply` > 10000 ms or `flags` containing
      `s_down,o_down`).
- [ ] Step 2 — force a promotion:
      `redis-cli -p 26379 -a "$REDIS_PASSWORD" SENTINEL failover ratesengine-r1-cache`.
      Do this with a clear head; forcing failover on a transient
      network blip can split-brain if Sentinels rejoin and
      disagree on who's primary.
- [ ] Step 3 — if the primary host itself is gone: nothing to do
      from the cache cluster's side (Sentinel already promoted).
      Hand off to host-bringup runbook to restore the failed node
      as a fresh replica via `ansible-playbook --tags redis --limit cache-X`.
- [ ] Step 4 — verify the promoted replica is caught up:
      `redis-cli info replication` on the new primary should
      show every follower's `lag` column at 0–1.
- [ ] Verification: `redis_up{role="master"} == 1` (single
      instance), `ratesengine_redis_sentinel_primary` sums to 1
      across hosts, API logs show "redis: reconnected",
      `ratelimit_fail_open_total` rate drops to zero (it's
      cumulative — watch the rate, not the gauge).

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

- **Rolling restart of redis-server across the cluster** (e.g.
  apt upgrade rolled out via the `redis-sentinel` ansible role
  with `--limit`): rolling the current primary always trips this
  alert for ~30 s while Sentinel promotes a replica. Muting the
  alert for the duration of a planned maintenance is acceptable;
  the role's README documents the safe one-host-at-a-time apply
  pattern.
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
- 2026-05-02 — diagnosis converted from kubectl/StatefulSet
  commands to the `cache-01..03` bare-metal hosts +
  `redis-server.service` / `redis-sentinel.service` shape that
  the `redis-sentinel` ansible role actually deploys (ADR-0008
  §3.4).

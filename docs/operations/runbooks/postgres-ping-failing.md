---
title: Runbook — postgres_ping_failing
last_verified: 2026-05-27
status: ratified
severity: P1
---

# Runbook — `stellarindex_postgres_ping_failing`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `stellarindex_postgres_ping_failing` |
| Severity | P1 (page) |
| Detected by | Prometheus rule in `deploy/monitoring/rules/storage.yml` (and `configs/prometheus/rules.r1/storage.yml` R1 overlay) |
| Typical MTTR | 5 min |
| Impact | Indexer ingest is stalled or about to stall; live ledger writes failing every 60 s probe. |

## Why this exists

During the disk-full SEV cascade of 2026-05-26-27, a postgres
outage brought
`postgresql@15-main.service` down for ~10 h. Postgres recovered
when disk was freed, but the indexer's `*sql.DB` connection pool
held stale conns and silently failed writes for an additional ~3 h
until a manual `systemctl restart stellarindex-indexer`. Total
ledger gap: ~14 h.

The code fix shipped alongside this runbook:

1. `internal/storage/timescale/store.go` now sets
   `SetConnMaxLifetime(30 min)` + `SetConnMaxIdleTime(5 min)` —
   automatic pool refresh, bounds the cascade-gap to the lifetime
   interval.
2. `cmd/stellarindex-indexer/main.go::watchPostgresPing` probes the
   pool every 60 s and emits
   `stellarindex_postgres_ping_total{outcome="ok"|"error"}` plus the
   live streak gauge `stellarindex_postgres_ping_failure_streak`.

This alert fires when the error rate stays above 0.5/s for 2 min
— the live signal that the safety-net hasn't refreshed yet AND
something past the conn layer is broken.

## Symptoms

- `rate(stellarindex_postgres_ping_total{outcome="error"}[5m]) > 0.5` for 2+ min.
- `stellarindex_postgres_ping_failure_streak` climbing past 3.
- Indexer journal: `pool may be wedged` log line.
- Downstream: `stellarindex_trade_inserts_total{outcome="error"}` climbing.

## Quick diagnosis (≤ 5 min)

```sh
# 1. Is postgres itself up? (F-0154 — use the CLUSTER name, not the umbrella.)
ssh root@r1 'systemctl status postgresql@15-main.service'

# 2. Can a fresh client connect at all?
ssh root@r1 'sudo -u postgres pg_isready -t 5'

# 3. What does the indexer journal say about the ping streak?
ssh root@r1 'journalctl -u stellarindex-indexer.service --since "10 min ago" | grep -E "postgres.ping|pool may be wedged"'

# 4. Live metric on r1's prometheus:
curl -s http://r1:9090/api/v1/query?query=stellarindex_postgres_ping_failure_streak | jq .
```

## Mitigation (≤ 15 min)

If postgres@15-main is DOWN:

- [ ] Fix postgres first (see `db-disk-full.md` / `timescale-primary-down.md` depending on cause).
- [ ] The pool will refresh automatically within
      `PoolConnMaxLifetime` (30 min) once postgres is reachable,
      but you can force it now with a restart:
      `ssh root@r1 'systemctl restart stellarindex-indexer.service'`
- [ ] Verification: ping streak resets to 0 + `outcome="ok"` rate climbs back.

If postgres is UP but ping still fails:

- [ ] Likely a network blip, firewall reset, or auth misconfig.
- [ ] `ssh root@r1 'cat /etc/default/stellarindex-indexer | grep DSN'` — verify DSN env.
- [ ] If pool wedged but DB healthy, restart the indexer to drain.

## Root cause analysis

- The 14 h cascade gap on 2026-05-26-27 root caused this whole
  resilience seam. Future post-mortems should record:
  - the streak length before the alert fired,
  - whether the lifetime safety-net or the manual restart refreshed
    the pool first.

## Known false-positive patterns

- A postgres restart will briefly fail pings — but
  `for: 2m` should ride through any clean restart. If you see this
  alert on every routine restart, the restart is taking too long
  (investigate separately).
- A network partition between indexer + DB will look identical to
  a pool problem from this alert's perspective; correlate with
  `up{job="postgres_exporter"}` once F-0152 lands.

## Related

- `internal/storage/timescale/store.go` — `configurePool` +
  `PingContext` (the implementation).
- `cmd/stellarindex-indexer/main.go::watchPostgresPing` —
  the probe goroutine.
- `db-disk-full.md` — the most common upstream cause.
- `timescale-primary-down.md` — adjacent alert; this one fires
  when the DB is reachable from prometheus but not from the
  indexer.

## Changelog

- 2026-05-27 — initial draft alongside the F-0151 resilience fix.

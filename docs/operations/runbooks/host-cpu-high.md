---
title: Runbook — host-cpu-high
last_verified: 2026-05-03
status: draft
severity: P3
---

# Runbook — `ratesengine_host_cpu_high`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_host_cpu_high` |
| Severity | P3 (informational) |
| Detected by | `deploy/monitoring/rules/infra.yml` |
| Typical MTTR | 30 min – days (depends on whether it's fixable code vs scale-up) |
| Impact | Not directly customer-visible. High CPU usually precedes latency degradation — if `api-latency.md` hasn't fired yet, you have lead time. |

## Symptoms

- `100 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100 > 90`
  sustained 10 min.
- Load avg on the host exceeds CPU count.
- The same host's `iowait` / `softirq` may also be elevated
  (useful for root-causing).

## Quick diagnosis (≤ 5 min)

```sh
# Which process is eating CPU?
ssh <host> 'top -b -n1 -o %CPU | head -20'

# Per-service breakdown (systemd cgroup view)
ssh <host> 'systemd-cgtop --order=cpu --iterations=2 -n 20'

# Is it user-CPU, system-CPU, iowait, or softirq?
ssh <host> 'mpstat 1 5'
```

## Typical root causes

1. **A single process is pegged.** Usually means a hot code path
   we didn't anticipate — regex in a loop, unbounded concurrency
   firing off goroutines, bad SQL plan forcing a full scan
   client-side.

2. **captive-core catchup** on a galexie (or, in Phase-3
   deployments, stellar-rpc / stellar-core) host. Replay is
   CPU-bound; expected during boot + periodic maintenance.
   On r1 today, only galexie embeds a captive-core
   ([r1-deployment-state.md](../r1-deployment-state.md)); the
   stellar-rpc / stellar-core daemons were removed 2026-04-23
   and `core-lag.md` / `rpc-lag.md` are inert there. Galexie's
   own captive-core does not expose a `/info` endpoint, so the
   end-state signal is "fresh objects in `galexie-live` after
   catchup completes" rather than the stellar-core ledger-age
   metric.

3. **Postgres bad plan** — an ANALYZE drifted, now Postgres is
   picking a seq-scan over an index. `pg_stat_statements` shows
   a specific statement with high `mean_exec_time`.

4. **Compression / backup window** on a Postgres host.
   `pg_repack`, `pgBackRest --process-max=4`, or TimescaleDB
   compression jobs are CPU-intensive on purpose.

5. **Noisy neighbor** (colo / shared infra only) — another tenant
   saturated the physical cores; we're getting CPU steal.
   - Signal: `mpstat 1` shows `%steal` > 0.

## Mitigation

- [ ] Step 1 — identify the consumer (above).
- [ ] Step 2 — if one process: is it legitimate work? Then we're
      under-provisioned — schedule a vertical scale-up. If it's
      a bug (runaway goroutine, infinite loop), file an incident.
- [ ] Step 3 — if captive-core catchup: wait. Usually resolves in
      30–120 min.
- [ ] Step 4 — if Postgres plan regression: `ANALYZE` the affected
      tables, or `pg_hint_plan` the offending statement.
- [ ] Step 5 — if compression/backup: verify it completes; if it's
      running for hours, tune `--process-max` down.
- [ ] Verification: CPU drops back under 70 % sustained; alert
      clears.

## Known false-positive patterns

- **Multi-core hosts on average**: `avg(rate(...idle...))` = idle
  percentage averaged across cores. A single-core spike (pinned
  goroutine) doesn't cross 90 % on a 16-core host — so the alert
  is tuned to catch real saturation, not per-core pegs. If you
  need the per-core version, use the `max by (cpu)` variant.
- **Burst workloads**: some of our cron'd maintenance (hourly
  aggregator rollup) can burn CPU for a few minutes. The `for:
  10m` should absorb it.

## Related

- `api-latency.md` — downstream effect when CPU saturation slows
  request handlers.
- `pg-conns-saturated.md` — a common CPU-saturating scenario for
  Postgres hosts.
- `all-ingestion-down.md` — when galexie's captive-core stalls
  hard enough to halt fresh-object production.
- `core-lag.md`, `rpc-lag.md` — captive-core variants for
  Phase-3 deployments running stellar-core / stellar-rpc; inert
  on r1 today.

## Changelog

- 2026-04-23 — initial draft.
- 2026-04-30 — captive-core root-cause refers to galexie (the only
  on-host captive on r1 since 2026-04-23) rather than the removed
  stellar-rpc / stellar-core daemons. Related section flags those
  as Phase-3-only.

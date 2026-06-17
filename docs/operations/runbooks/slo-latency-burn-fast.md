---
title: Runbook — slo-latency-burn-fast
last_verified: 2026-05-12
status: draft
severity: P1
---

# Runbook — `stellarindex_slo_latency_burn_fast`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `stellarindex_slo_latency_burn_fast` |
| Severity | **P1** (page) |
| Detected by | `deploy/monitoring/rules/slo.yml` |
| Typical MTTR | 15–30 min |
| Impact | We're burning the latency error budget at the **fast** rate (we'll spend a month's budget in ~1 hour at the current trajectory). The visible signal is that p95 latency on `/v1/price` and adjacent surfaces has stepped above the latency SLA target of ≤ 200 ms for an extended-enough window that SLO compliance is at risk this quarter. |

## Symptoms

- Multi-window burn-rate detection: 5-min p95 burn AND 1-hour p95 burn both ≥ 14.4× the budget. (Per Google SRE workbook ch. 5; this is the "fast burn" archetype.)
- `/v1/price` p95 > 0.5 s for ≥ 2 min (the underlying `stellarindex_api_latency_p95_high` alert fires alongside).
- Dashboards show synthetic-probe latency above SLA budget.

## Quick diagnosis (≤ 5 min)

```sh
# Identify the slow route(s)
curl -s 'http://localhost:9090/api/v1/query?query=histogram_quantile(0.95,%20sum%20by%20(le,path)%20(rate(http_request_duration_seconds_bucket%5B5m%5D)))' \
  | jq '.data.result[] | {path: .metric.path, p95: .value[1]}' | head -20

# Top-N slow requests in the last 5 min from the API log
journalctl -u stellarindex-api --since '5 min ago' --no-pager \
  | jq -r 'select(.latency_ms > 200) | [.path, .latency_ms] | @tsv' | sort -k2 -n -r | head -20

# Is it a database query slowing us down?
sudo -u postgres psql -d stellarindex -c "SELECT query, calls, mean_exec_time, max_exec_time FROM pg_stat_statements ORDER BY max_exec_time DESC LIMIT 10;"
```

Key signals:
- **Single slow route** → probably a code-path regression; check recent deploys (rc.* tags in `git log`).
- **All routes slow** → upstream resource saturation (CPU / memory / postgres connections / Redis latency).
- **Specific user behaviour** → check rate-limit headers on the slow requests; a paid tier may be hammering one endpoint.

## Mitigation (≤ 15 min)

- [ ] Step 1 — if the slowness coincides with a recent deploy: roll back via `gh workflow run deploy.yml -f region=r1 -f version=<previous-rc>` (per `deploy-workflow.md`).
- [ ] Step 2 — if no recent deploy and a single route is dominant: capture a CPU profile via the metrics endpoint `curl http://localhost:3000/debug/pprof/profile?seconds=30 > /tmp/profile.pprof` and `go tool pprof` analyse out-of-band.
- [ ] Step 3 — if all routes slow + postgres connections saturated → jump to `pg-conns-saturated.md`.
- [ ] Step 4 — if all routes slow + Redis latency high → check Redis health (RDB BGSAVE blocked? memory saturated?); jump to `redis-master-down.md` family.
- [ ] Verification: `histogram_quantile(0.95, ...) < 0.20` (200 ms) for ≥ 5 min sustained.

## Root cause analysis

Capture for postmortem:
- The exact 5-min window where the burn started + the dominant slow path's `pg_stat_statements` snapshot.
- Recent deploy timestamps relative to the burn onset.
- Per-route p95/p99 trend graph for the prior 24 h.

## Known false-positive patterns

- **First 5 min after deploy** — connection pool warm-up + cache cold-start can push p95 transiently. The `for: 2m` window catches this; if it persists past 5 min it's real.
- **Synthetic SLA-probe load test windows** — operator-triggered weekly k6 runs (`k6-weekly.yml`) burn budget intentionally. Cross-reference against the `k6_weekly_running` heartbeat.

## Related

- `slo-latency-burn-medium.md` — same family at the slower burn rate.
- `slo-latency-burn-slow.md` — same family at the slowest burn rate.
- `api-latency.md` — when p95/p99 spike on a single endpoint without burning budget yet.
- `pg-conns-saturated.md`, `cache-miss-rate-high.md` — common upstream causes.
- ADR-0009 — API latency budget allocation.
- Note: r1 currently runs at p95 = 246 ms structurally; multi-region cutover is the long-term fix.

## Changelog

- 2026-05-12 — initial draft (audit-2026-05-12 F-1237 closure).

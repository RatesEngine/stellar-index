---
title: Runbook — slo-availability-burn-fast
last_verified: 2026-05-12
status: draft
severity: P1
---

# Runbook — `stellarindex_slo_availability_burn_fast`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `stellarindex_slo_availability_burn_fast` |
| Severity | **P1** (page) |
| Detected by | `deploy/monitoring/rules/slo.yml` |
| Typical MTTR | 15–30 min |
| Impact | We're burning the availability error budget at the **fast** rate. 5xx rate has spiked above the SLO budget for a 5-min/1-hour window combination — at this trajectory a month's budget is gone in ~1 hour. Consumers see request failures; the service SLA commits ≥ 99.9 % availability. |

## Symptoms

- Multi-window detection: 5-min 5xx burn AND 1-hour 5xx burn both ≥ 14.4× the budget.
- `stellarindex_api_error_rate_high` (P3) and likely `stellarindex_api_error_rate_critical` (P1) firing alongside.
- Reports of intermittent 5xx.

## Quick diagnosis (≤ 5 min)

```sh
# Which routes are 5xx-ing
journalctl -u stellarindex-api --since '5 min ago' --no-pager \
  | jq -r 'select(.status >= 500) | .path' | sort | uniq -c | sort -rn | head -10

# Sample one error to get the actual problem
journalctl -u stellarindex-api --since '5 min ago' --no-pager \
  | jq -r 'select(.status >= 500) | [.path, .request_id, .err] | @tsv' | head -10

# Is upstream the issue?
systemctl status stellarindex-aggregator stellarindex-indexer postgres redis caddy --no-pager | head -30
```

Key signals:
- **`/v1/price` 5xx → upstream Postgres or Redis failed**; jump to `timescale-primary-down.md` or `redis-master-down.md`.
- **All routes 5xx → API process itself is sick**; check OOM (`dmesg | grep -i kill`), goroutine leak (`curl /debug/pprof/goroutine?debug=1 | head -30`).
- **Recent deploy → roll back via `gh workflow run deploy.yml -f region=r1 -f version=<previous-rc>`**.

## Mitigation (≤ 15 min)

- [ ] Step 1 — if recent deploy correlates with the burn onset: roll back. Per `deploy-workflow.md`'s "automatic rollback on health-probe failure" semantics this should already have happened; if it didn't, investigate the deploy's health-probe path.
- [ ] Step 2 — if upstream resource saturated: jump to the appropriate runbook.
- [ ] Step 3 — if the API process needs a kick: `systemctl restart stellarindex-api` (the systemd unit has `Restart=on-failure`; manual restart is the same effect).
- [ ] Verification: 5xx rate < 0.5 % sustained for 5 min.

## Root cause analysis

For postmortem:
- The full request log over the burn window, filtered to 5xx.
- Goroutine + heap profiles captured during the burn (`/debug/pprof/goroutine`, `/heap`).
- Kernel `dmesg` over the burn window (OOM-kill markers).

## Known false-positive patterns

- **Brief upstream blips** — Cloudflare → R1 has periodic single-region network interruptions; if the burn was < 60 s and recovered without intervention, it's the network, not us. The `for: 2m` window catches most cases.
- **Synthetic load test** — k6 weekly intentionally triggers some 5xx on overload paths. Cross-reference the firing time.

## Related

- `slo-availability-burn-medium.md` / `slo-availability-burn-slow.md` — same family, slower burn.
- `api-down.md` — when scrape `up{job="stellarindex-api"} == 0`.
- `api-5xx.md` / `api-latency.md` — adjacent route-level alerts.
- ADR-0008 — HA topology + availability target.
- ADR-0009 — latency budget (separate from availability budget).

## Changelog

- 2026-05-12 — initial draft (audit-2026-05-12 F-1237 closure).

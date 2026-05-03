---
title: Runbook — scrape-failing
last_verified: 2026-05-02
status: draft
severity: P3
---

# Runbook — `ratesengine_prometheus_scrape_failing`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_prometheus_scrape_failing` |
| Severity | P3 (informational) |
| Detected by | `deploy/monitoring/rules/meta.yml` |
| Typical MTTR | 5–30 min |
| Impact | We've lost visibility into some subsystem. Doesn't mean the subsystem is unhealthy — often the exporter is the problem, and the service it monitors is fine. But we can't *tell* which is true until we investigate. |

## Symptoms

- `up{job=<J>, instance=<I>} == 0` for ≥ 2 min.
- Gap in the service's metric graphs from 2 min ago to now.
- The service's own user-visible health may be fine.

## Quick diagnosis (≤ 5 min)

Prometheus runs as `prometheus.service` on `mon-01` / `mon-02`
(per the `prometheus` ansible role; ADR-0008 §3 monitoring tier).

```sh
# What's Prometheus's view of the failing target?
ssh root@mon-01 "curl -s http://localhost:9090/api/v1/targets?state=active" | \
  jq '.data.activeTargets[] | select(.health != "up") |
        {job: .labels.job, instance: .labels.instance, lastError: .lastError}'

# Is the /metrics endpoint on the target reachable from the prom host?
ssh root@mon-01 "curl -s http://<target-host>:<port>/metrics | head"

# Is the exporter unit alive on the target host? (redis_exporter,
# postgres_exporter, node_exporter all ship as systemd units via
# their respective ansible roles.)
ssh root@<target-host> "systemctl status '*_exporter' --no-pager"
```

The `lastError` field from the Prometheus API tells you exactly
why scrape failed: connection refused, TLS, 404 on the /metrics
path, parse error, etc.

## Typical root causes

1. **Target host rebooted or the unit restarted**. Common during
   ansible-driven upgrades; Prometheus's static-config discovery
   re-resolves on each scrape so target reappearance is
   bounded by the scrape interval. The alert `for: 2m` absorbs
   this — if it fires, the host or unit is staying down.

2. **Exporter crash**. `redis_exporter`, `postgres_exporter`,
   `node_exporter` each have their own failure modes.
   - Mitigation: `ssh root@<host> "systemctl restart <exporter>"`.

3. **Static-config drift**. A new host added without an
   accompanying entry in the prometheus role's `prometheus.yml.j2`
   inventory. Target was added to inventory but role hasn't been
   re-applied; or the inverse — host removed from inventory but
   prometheus config still scrapes it.

4. **Auth drift**. Some exporters require a basic-auth or bearer
   token; if the credentials in vault got rotated without
   re-applying the prometheus role to refresh the scrape config,
   prometheus gets 401 on every attempt.

5. **Firewall / iptables rule blocking Prometheus.** A new rule
   on the target host (or the colo perimeter) doesn't allow
   `mon-01` / `mon-02` in to the metrics port.

## Mitigation

- [ ] Step 1 — look at `lastError` — that usually points at the
      exact cause.
- [ ] Step 2 — fix per cause:
      - Exporter crash: `systemctl restart <exporter>` on the host.
      - Static-config drift: re-apply the `prometheus` ansible
        role (rolls `/etc/prometheus/prometheus.yml` and SIGHUPs
        the unit).
      - Auth: rotate vault entry, re-apply role.
      - Firewall: open ingress from `mon-01` / `mon-02` to the
        target metrics port.
- [ ] Step 3 — if it's genuinely the *target service* down, not
      the scrape, cross-reference with that service's own alerts.
- [ ] Verification: `up` returns to 1; metrics resume flowing.

## Known false-positive patterns

- **Prometheus reload during a config change** drops all
  targets briefly. `for: 2m` is chosen to avoid paging on this.
- **Cold-start scrape window after a unit restart** — between
  `systemctl start` and the first /metrics serve, Prometheus
  sees `up==0`. Resolves on the next scrape interval.

## Related

- `alertmanager-bad-config.md` — AlertManager-specific reload
  issues.
- `deadmansswitch.md` — the failover when Prometheus itself is down.
- Per-service runbooks if the service is actually down, not just
  unscrapeable.

## Changelog

- 2026-04-23 — initial draft.
- 2026-05-02 — diagnosis converted from kubectl `prometheus-0`
  pod-exec / pod-list to the `mon-01` / `mon-02` hosts running
  `prometheus.service` per the `prometheus` ansible role
  (ADR-0008). Service-discovery section rewritten to talk about
  static-config drift instead of ServiceMonitor / PodMonitor.

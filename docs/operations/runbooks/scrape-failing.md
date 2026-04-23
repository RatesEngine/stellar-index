---
title: Runbook — scrape-failing
last_verified: 2026-04-23
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

```sh
# What's Prometheus's view of the failing target?
curl -s http://prometheus:9090/api/v1/targets?state=active | \
  jq '.data.activeTargets[] | select(.health != "up") | {job: .labels.job, instance: .labels.instance, lastError: .lastError}'

# Is the /metrics endpoint on the target reachable manually?
kubectl exec -it prometheus-0 -- wget -q -O- http://<service>:<port>/metrics | head

# Is the exporter process alive (node_exporter, redis_exporter, etc.)?
kubectl get pods -A | grep exporter
```

The `lastError` field from the Prometheus API tells you exactly
why scrape failed: connection refused, TLS, 404 on the /metrics
path, parse error, etc.

## Typical root causes

1. **Target pod got rescheduled**. Common during rolling restarts;
   Prometheus's service-discovery updates on the next
   discovery-interval (default 30 s). The alert `for: 2m` absorbs
   this — if it fires, the rollout is unusually slow.

2. **Exporter crash**. `redis_exporter`, `postgres_exporter`,
   `node_exporter` each have their own failure modes.
   - Mitigation: restart the exporter.

3. **Service discovery misconfig**. A new service deployed without
   the correct Prometheus-scrape annotations / ServiceMonitor /
   PodMonitor. Target appears then disappears.

4. **Auth drift**. Some exporters require a basic-auth or bearer
   token; if the credentials secret got rotated without updating
   the scrape config, Prometheus gets 401 on every attempt.

5. **Network policy blocking Prometheus.** A new NetworkPolicy
   was applied that doesn't allow the monitoring namespace in.

## Mitigation

- [ ] Step 1 — look at `lastError` — that usually points at the
      exact cause.
- [ ] Step 2 — fix per cause:
      - Exporter crash: restart.
      - SD misconfig: fix annotations / ServiceMonitor.
      - Auth: sync secret.
      - NetworkPolicy: allow monitoring namespace.
- [ ] Step 3 — if it's genuinely the *target service* down, not
      the scrape, cross-reference with that service's own alerts.
- [ ] Verification: `up` returns to 1; metrics resume flowing.

## Known false-positive patterns

- **Prometheus reload during a config change** will drop all
  targets briefly. `for: 2m` is chosen to avoid paging on this.
- **Cold pod during scale-out** — the pod is not yet Ready but
  Prometheus has already discovered it. Expected; resolves when
  the pod becomes Ready.

## Related

- `alertmanager-bad-config.md` — AlertManager-specific reload
  issues.
- `deadmansswitch.md` — the failover when Prometheus itself is down.
- Per-service runbooks if the service is actually down, not just
  unscrapeable.

## Changelog

- 2026-04-23 — initial draft.

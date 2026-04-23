---
title: Runbook — api-down
last_verified: 2026-04-23
status: draft
severity: P1
---

# Runbook — `ratesengine_api_down`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_api_down` |
| Severity | P1 (page — SEV-1) |
| Detected by | `deploy/monitoring/rules/api.yml` |
| Typical MTTR | 2–15 min |
| Impact | Complete public API outage — `/v1/price`, `/v1/history`, everything. Every customer (Freighter, Stellar.expert, the lot) sees connection errors or the reverse-proxy's 502 page. |

## Symptoms

- `sum(up{job="api"}) == 0` — every API pod down for ≥ 60 s.
- `/v1/healthz` and `/v1/readyz` probes fail from the LB.
- Reverse proxy (nginx / Cloudflare) surfaces 502/503 to clients.
- The pager fires on the `severity=page` label.

## Quick diagnosis (≤ 5 min)

```sh
# Can we reach any API pod at all?
kubectl -n ratesengine get pods -l app=api
kubectl -n ratesengine describe pods -l app=api | tail -60

# Recent events — OOMKilled / CrashLoopBackOff / ImagePullErr?
kubectl -n ratesengine get events --sort-by='.lastTimestamp' | tail -30

# If pods are Running but not Ready: /v1/readyz probes what?
kubectl -n ratesengine port-forward svc/api 8080 &
curl -s http://localhost:8080/v1/readyz | jq
```

Readyz gates on Timescale + Redis health (see `internal/api/v1/healthz.go`).
If *it* is red:

- `timescale` red → jump to `timescale-primary-down.md`.
- `redis` red → the API serves fail-open for rate limiting and degraded-envelope for price; this should *not* take the pod out of Ready. If it is, that's a bug — file it.

## Typical root causes

1. **Config push broke the binary startup**. We ship declarative
   config; a bad TOML gets rejected in `config.Validate()`
   (`internal/config/validate.go`) and the process exits with a
   non-zero code before serving. `kubectl logs` shows the specific
   validation error.

2. **Image push without schema migration**. If the new binary
   expects a column that's not there, the first query fails at
   serve-time. Readyz may still pass (ping is shallow).

3. **Secret rotation without re-rollout**. API reads the DSN from
   a mounted secret at boot — rotating Redis/Postgres credentials
   without rolling the API produces `authentication failed` log
   spam and no healthy pods.

4. **Node evacuation / pod eviction cascade**. If an entire node
   goes down and scheduler can't place the replacements (taints,
   resource pressure), the count drops to zero.

5. **LB / ingress breakage**. `up{job="api"}` is scraped via
   service-discovery; if service-endpoints are empty (stale
   selector, deleted service) we'd alarm even with healthy pods.

## Mitigation (≤ 15 min)

- [ ] Step 1 — **declare SEV-1** in whatever incident channel you use.
      Downtime is customer-visible and a breach of our Freighter SLA.

- [ ] Step 2 — find the root cause via the diagnosis above. Do NOT
      blindly `kubectl rollout restart` — if the last push is
      broken, restarting restages the broken version.

- [ ] Step 3 — if the latest rollout is the cause:
      ```sh
      kubectl -n ratesengine rollout undo deploy/api
      kubectl -n ratesengine rollout status deploy/api
      ```

- [ ] Step 4 — if config/secret is the cause: fix the source
      (GitOps commit or secret rotation), then apply. Don't edit
      the live ConfigMap unless you're sure the repo state matches
      afterwards.

- [ ] Step 5 — if node-level: scale up the node pool or cordon the
      bad node and let replicas reschedule.

- [ ] Verification: `up{job="api"}` returns to 1 per pod; `/v1/healthz`
      returns 200 from outside the cluster; error-rate alert clears
      within 5 min.

## Root cause analysis

Gather for the postmortem:

- The kubectl events window (`--since=30m`) showing the transition.
- Last 500 log lines from all API pods (liveness/readiness probe
  reasons often land here).
- The diff between the last-known-good rollout and the current one
  (`kubectl rollout history deploy/api`).
- Prometheus screenshots: `up`, `http_requests_total`, restart count.
- Whether this alert alone fired or it was a symptom of an upstream
  (Timescale / Redis / node) issue.

## Known false-positive patterns

- **Single-replica test environments** — `for: 60s` is aggressive
  for a 1-pod deploy during a normal rollout. Production runs
  ≥ 3 replicas so a rolling update never lands at 0; if you see
  this in staging during a deploy, it's expected.
- **Scrape-path breakage at the same time as a real outage.** If
  the Prometheus server lost DNS to the API service, you get
  `up == 0` identical to a real outage. Cross-check against LB
  access logs.

## Related

- `api-5xx.md` — handlers returning errors but pods healthy.
- `api-latency.md` — slow but alive.
- `timescale-primary-down.md`, `redis-master-down.md` — upstream
  failures that can cascade into readyz red.
- HA plan §9 degradation envelope: `docs/architecture/ha-plan.md`.

## Changelog

- 2026-04-23 — initial draft. Lint-docs required a runbook for
  the page-severity alert.

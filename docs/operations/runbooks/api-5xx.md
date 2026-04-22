---
title: Runbook — API 5xx rate elevated
last_verified: 2026-04-23
status: ratified
severity: P1 at >5% / P2 at >1%
---

# Runbook — `ratesengine_api_error_rate_{high,critical}`

## At a glance

| Field | Value |
| ----- | ----- |
| Alerts | `ratesengine_api_error_rate_high` (>1 % for 2 min) → P2<br>`ratesengine_api_error_rate_critical` (>5 % for 2 min) → P1 |
| Severity | **P1 at critical**, **P2 at high** |
| Detected by | Prometheus rule on `http_requests_total{status=~"5.."}` rate |
| Typical MTTR | 5–15 min for a bad-deploy revert; 30–60 min for a latent-bug forward fix |
| Impact | Clients seeing request failures. Affects both the V1 Freighter API SLA ("responsiveness ≥ 99.9 %") and the RFP p95/p99 latency targets — every 5xx adds timeout retries that inflate queue time. |

## Symptoms

- Pager fires on `ratesengine_api_error_rate_{high|critical}`.
- Grafana "Golden Signals" dashboard shows an error-rate cliff-edge
  or sawtooth pattern.
- Concurrent alerts likely: `ratesengine_api_latency_p95_high`
  (timeouts inflate latency), possibly `ratesengine_api_price_stale`
  if Timescale is the root cause.

## Quick diagnosis (≤ 5 min)

Three signals in order. The first that flags non-zero wins; skip
the rest.

### 1. What's actually failing?

```sh
# Top status/route combinations by count in the last 5 min
promql 'topk(5, sum by (route, status) (rate(http_requests_total{job="api",status=~"5.."}[5m])))'
# (or click the Grafana link in the alert annotation)
```

Expect a single dominant `{route="…", status="500"}` or `"503"`
pair. If errors spread across every route, the root cause is
shared infrastructure (DB, Redis, upstream RPC) — skip to §4.

### 2. Is it a recent deploy?

```sh
# Last 3 deploys + when the error rate lifted
kubectl -n ratesengine rollout history deployment/ratesengine-api | tail -5
# or:
systemctl status ratesengine-api | grep -i "main process"
```

Correlate the deploy timestamp against the error-rate lift. If a
deploy within the last ~1 h precedes the rise → revert is the
mitigation. Jump to §A (Mitigation).

### 3. Is a dependency the root cause?

Deps exposed via /v1/readyz. Does it report degraded?

```sh
curl -sSf https://api.ratesengine.net/v1/readyz | jq '.data'
# Expected:
# { "status": "ok" | "degraded",
#   "checks": [ { "name": "postgres", "ok": true }, ... ] }
```

- `postgres.ok == false` → [timescale-primary-down](timescale-primary-down.md).
- `redis.ok == false` → [redis-master-down](redis-master-down.md).
- All OK but 5xx still elevated → handler-level bug, §B Mitigation.

### 4. Is there a visible pattern in the logs?

```sh
# Pull the last N 5xx log lines and group by the panic line (if any)
journalctl -u ratesengine-api --since "15 min ago" \
  | jq -r 'select(.level=="ERROR") | "\(.method) \(.path) \(.status) \(.err)"' \
  | sort | uniq -c | sort -rn | head
```

Patterns to look for:
- Panic stack trace → handler bug; go to §B.
- `dial tcp … connection refused` → upstream issue (DB/Redis/RPC);
  return to §3.
- `context deadline exceeded` → slow dependency; check dependency
  latency dashboards.
- Handler-specific error like `ErrPriceNotFound` at a higher than
  normal rate → data issue, not a production incident; suppress
  alert if sustained.

## Mitigation

Pick by diagnosis; don't work through sequentially.

### A. Recent deploy is the cause — **revert**

Fastest path. Ship-and-revert is cheaper than production-debug.

```sh
# Kubernetes
kubectl -n ratesengine rollout undo deployment/ratesengine-api
# Watch metrics; error rate should fall within 60s of replica cut-over.

# Baremetal
systemctl stop ratesengine-api
# Deploy the previous binary via your usual artifact path:
/usr/local/bin/ratesengine-api-prev -config /etc/ratesengine/api.toml &
```

Verification:
- [ ] `ratesengine_api_error_rate_critical` clears within 3 min.
- [ ] /v1/healthz returns 200.
- [ ] /v1/readyz returns `status=ok` on at least 3 consecutive polls.

Only after the incident is contained: file a postmortem action
item to explain why CI + the rolling deploy didn't catch it.

### B. Handler bug (no recent deploy) — **gate + fix forward**

Panics in a handler usually indicate a nil-dereference on an
unexpected input shape. Recoverer catches them (returns 500
problem+json), so we don't crash — but the 5xx rate climbs.

If the bug is **isolated to one endpoint**:

```sh
# Temporarily disable the endpoint at the load balancer / Istio
# VirtualService — let users see 404 rather than 500.
# Example for the /v1/history endpoint:
kubectl -n istio-system patch virtualservice ratesengine-public \
  --type=json \
  -p='[{"op":"add","path":"/spec/http/0/match","value":[{"uri":{"prefix":"/v1/history"}}]},
       {"op":"add","path":"/spec/http/0/directResponse","value":{"status":404,"body":{"string":"endpoint temporarily disabled"}}}]'
```

Then fix, test, deploy. Remove the block after deploy.

If the bug affects **every handler** (e.g. middleware panic): can't
gate around it at LB level; deploy a hotfix that disables the
offending middleware via config, then follow up with a proper fix.

### C. Dependency failure — chase the real alert

If /v1/readyz points at a dep being down, the dependency's runbook
is the one to follow:
- [timescale-primary-down](timescale-primary-down.md)
- [redis-master-down](redis-master-down.md) (TODO(#0))
- [all-ingestion-down](all-ingestion-down.md)

This alert will auto-resolve once the dep recovers.

### D. Load-induced — **scale up**

Rare but possible (e.g. viral traffic, DDoS), characterised by:
- Error rate climbs WITHOUT a deploy, a dep failure, or a log
  pattern.
- `ratesengine_api_latency_p99_high` fires in tandem.
- `http_requests_total` rate is sharply higher than baseline.

Mitigation:

```sh
# Scale the api deployment (keep db + redis at current)
kubectl -n ratesengine scale deployment/ratesengine-api --replicas=6
# OR baremetal: start another api instance behind HAProxy.
```

If load doesn't subside + scale-up doesn't clear the alert in
10 min: rate-limit harder at the edge (Cloudflare WAF → short-TTL
per-IP rate limit) + declare SEV-1 to conserve DB capacity.

## Root cause analysis

For the postmortem (§6 of sev-playbook.md):

- `kubectl -n ratesengine logs deployment/ratesengine-api --since 1h` → full log dump.
- Grafana screenshot of the 1 h window around the alert.
- `git log -n 20 main` — was there a deploy-time trigger?
- `kubectl -n ratesengine describe pod -l app=ratesengine-api` —
  any OOMKills, restarts, CrashLoopBackoff?
- If Recoverer caught panics: the stack traces + request_ids
  needed to build fixtures.
- If Timescale was involved: slow-query log around the incident
  window.

Common root-cause patterns:
1. **Nil-pointer in a handler on a new input shape** — Recoverer
   catches it → 500. Fix: validate input earlier, add a test for
   the pathological shape.
2. **Timescale primary down** — every /v1/price call that falls
   through to LatestTradesForPair returns 500. Fix: dependency's
   runbook; handler-side, consider a short-term Redis-only
   fallback with `reduced_redundancy=true` in the envelope.
3. **Out-of-memory on a batch endpoint** — a client sent
   `asset_ids=<1000 assets>` and the in-memory result triggered
   OOM. Fix: hard cap batch size in the handler (api-design.md
   §5.3 says 100 for GET, 1000 for POST — verify enforcement).
4. **Context-deadline exceeded on slow CAGG query** — first
   request of the day hits a cold CAGG partition. Fix: keep-
   warm job that queries each CAGG every few minutes.

## Known false-positive patterns

- **Synthetic monitoring sends 4xx to unknown assets** — not
  5xx, doesn't trigger this alert. Safe to ignore.
- **Minute-zero after deploy** — rolling restart briefly serves
  503 from pods that haven't loaded config yet. Alert window is
  2 min so this usually self-resolves. If it fires during a
  planned rolling deploy, the deploy runbook should silence this
  alert for the window.

## Related

- [api-latency](api-latency.md) — runs in parallel when the 5xx
  is from timeouts.
- [timescale-primary-down](timescale-primary-down.md) — likely
  cause when 5xx is global + readyz shows postgres down.
- [sev-playbook](../sev-playbook.md) §3 — detection channels;
  §4 — response flow; §5 — public-comms templates.
- [alerts-catalog](../alerts-catalog.md) — the rules this
  runbook serves.
- [ha-plan.md](../../architecture/ha-plan.md) §9 — degradation
  flags (`stale`, `reduced_redundancy`) the handler returns
  during partial outages.

## Changelog

- 2026-04-23 — initial draft. @ash.

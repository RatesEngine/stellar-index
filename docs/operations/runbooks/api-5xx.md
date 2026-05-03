---
title: Runbook — API 5xx rate elevated
last_verified: 2026-05-03
status: ratified
severity: P1 at >5% / P2 at >1% / P1 at SLO burn-rate fast
---

# Runbook — `ratesengine_api_error_rate_{high,critical}` (+ SLO availability burn-rate variants)

## At a glance

| Field | Value |
| ----- | ----- |
| Direct-threshold alerts | `ratesengine_api_error_rate_high` (>1 % for 2 min) → P2<br>`ratesengine_api_error_rate_critical` (>5 % for 2 min) → P1 |
| SLO burn-rate alerts | `ratesengine_slo_availability_burn_{fast,medium,slow}` (per ADR-0009 multi-window pattern) — `deploy/monitoring/rules/slo.yml` |
| Severity | **P1 at critical**, **P2 at high**; **P1** for fast/medium burn, P3 for slow burn |
| Detected by | Prometheus rule on `http_requests_total{status=~"5.."}` rate |
| Typical MTTR | 5–15 min for a bad-deploy revert; 30–60 min for a latent-bug forward fix |
| Impact | Clients seeing request failures. Affects both the V1 Freighter API SLA ("responsiveness ≥ 99.9 %") and the RFP p95/p99 latency targets — every 5xx adds timeout retries that inflate queue time. |

## Burn-rate vs direct-threshold pages

This runbook handles two alert families with different semantics:

- **Direct-threshold** (`api_error_rate_{high,critical}`):
  "instantaneous 5xx rate just crossed 1 % / 5 %." Trips on
  any sustained spike, including a brief deploy hiccup.
- **SLO burn-rate** (`slo_availability_burn_{fast,medium,slow}`):
  "we're consuming SLO error budget too quickly." The 99.99 %
  availability target gives a small monthly budget; the
  multi-window pattern (per Google SRE workbook) requires a short
  AND a long window to both agree before firing, so brief blips
  don't trip it. Fast tier (5m AND 1h, 14.4× burn) means budget
  will be gone in hours; medium (30m AND 6h, 6×) gives days; slow
  (6h AND 24h, 1×) gives ~weeks.

A `_burn_fast` page is a real availability emergency even if the
direct-threshold P1 (`error_rate_critical`, > 5 %) hasn't fired:
the 99.99 % budget is so small that 1.5 % errors sustained for 1h
is enough. Use the diagnosis below as usual; treat the urgency as
budget-exhaustion, not transient.

## Symptoms

- Pager fires on `ratesengine_api_error_rate_{high|critical}`.
- Grafana "Golden Signals" dashboard shows an error-rate cliff-edge
  or sawtooth pattern.
- Concurrent alerts likely: `ratesengine_api_latency_p95_high`
  (timeouts inflate latency), possibly `ratesengine_api_price_stale`
  if Timescale is the root cause.

## Quick diagnosis (≤ 5 min)

The API tier runs as `ratesengine-api.service` on three hosts
(`api-01..03`) behind two HAProxy hosts sharing a keepalived VIP
(per [ADR-0008](../../adr/0008-ha-topology.md) §1, no Kubernetes).
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
shared infrastructure (DB, Redis, upstream RPC) — skip to §3.

### 2. Is it a recent deploy?

```sh
# Running version on each api host
for h in api-01 api-02 api-03; do
  echo -n "$h: "
  ssh root@$h "curl -sf http://127.0.0.1:3000/v1/version | jq -r '.data.version'"
done

# When did each unit last (re)start?
for h in api-01 api-02 api-03; do
  echo -n "$h: "
  ssh root@$h "systemctl show ratesengine-api -p ActiveEnterTimestamp --value"
done

# Or: `r1-deployment-state.md` records the running tag.
git log --oneline -1 docs/operations/r1-deployment-state.md
```

Correlate the last unit-restart timestamp against the error-rate
lift. If a release within the last ~1 h precedes the rise →
revert per §A (Mitigation).

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
# Pull the last N error log lines and group by the panic line (if any)
ssh root@api-01 "journalctl -u ratesengine-api --since '15 min ago' \
  | jq -r 'select(.level==\"ERROR\") | \"\(.method) \(.path) \(.status) \(.err)\"' \
  | sort | uniq -c | sort -rn | head"
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
Follow the rolling rollback procedure in
[`release-process.md`](../release-process.md) → Rollback: drain
one host out of HAProxy via the admin socket
(`disable server api_pool/api-01`), swap that host's binary back
to the previous tag under `/opt/ratesengine/release-<tag>/`,
restart the unit, re-enable in HAProxy, repeat for `api-02` and
`api-03`. The two undrained hosts carry traffic during each swap.

Verification:
- [ ] `ratesengine_api_error_rate_critical` clears within 3 min.
- [ ] /v1/healthz returns 200.
- [ ] /v1/readyz returns `status=ok` on at least 3 consecutive polls.
- [ ] `/v1/version` reports the previous tag on every backend.

Only after the incident is contained: file a postmortem action
item to explain why CI + the rolling deploy didn't catch it.

### B. Handler bug (no recent deploy) — **gate + fix forward**

Panics in a handler usually indicate a nil-dereference on an
unexpected input shape. Recoverer catches them (returns 500
problem+json), so we don't crash — but the 5xx rate climbs.

If the bug is **isolated to one endpoint**, two options:

1. **HAProxy path block** (fastest, ≤ 2 min):
   ```haproxy
   # Add to backend api_pool in haproxy.cfg, then `haproxy -c`
   # to validate and `systemctl reload haproxy`. Reload is
   # connection-graceful, no in-flight request loss.
   http-request return status 503 content-type "application/problem+json" \
       string "{\"type\":\"about:blank\",\"title\":\"endpoint temporarily disabled\",\"status\":503}" \
       if { path_beg /v1/history }
   ```
   Both `lb-01` and `lb-02` need the change (push via the
   `haproxy` ansible role, or copy `/etc/haproxy/haproxy.cfg`
   manually under time pressure).

2. **Feature-flag deny in the binary** (if a flag exists for the
   endpoint): edit `/etc/ratesengine.toml`, ansible-push, then
   `systemctl restart ratesengine-api` host-by-host.

Then fix, test, deploy. Remove the block after deploy.

If the bug affects **every handler** (e.g. middleware panic):
treat as §A even if the deploy isn't recent — roll back to the
last-known-good binary. You can't path-gate around middleware.

### C. Dependency failure — chase the real alert

If /v1/readyz points at a dep being down, the dependency's runbook
is the one to follow:
- [timescale-primary-down](timescale-primary-down.md)
- [redis-master-down](redis-master-down.md)
- [all-ingestion-down](all-ingestion-down.md)

This alert will auto-resolve once the dep recovers.

### D. Load-induced — **shed + rate-limit at the edge**

Rare but possible (e.g. viral traffic, DDoS), characterised by:
- Error rate climbs WITHOUT a deploy, a dep failure, or a log
  pattern.
- `ratesengine_api_latency_p99_high` fires in tandem.
- `http_requests_total` rate is sharply higher than baseline.

Bare metal does not auto-scale. The three API hosts are fixed
capacity (per [ADR-0008](../../adr/0008-ha-topology.md) §4), so
the answer is **shed load**, not "add hosts":

1. **Tighten edge rate-limits.** Cloudflare WAF → short-TTL
   per-IP rate-limit rule. Drops the abusive traffic before it
   hits HAProxy at all.
2. **Drop the heaviest non-essential paths.** SSE clients
   (`/v1/price/stream`) and batch reads are higher-cost per
   request than tip lookups. Temporary HAProxy `http-request
   return status 503` block on those paths buys headroom for the
   serving-tier endpoints; same procedure as §B option 1.
3. **Promote AWS DR if the colo is genuinely capacity-saturated.**
   This is a SEV-1 escalation — the cloud DR pool exists for
   exactly this case but flipping DNS to it is heavyweight; only
   do it if §1 + §2 don't clear within 10 min. The DR-activation
   procedure is tracked in `dr-activation.md` (TODO(#0) — runbook
   in flight); meanwhile, follow the AWS-side warm-standby
   bring-up notes in [`ha-plan.md`](../../architecture/ha-plan.md)
   §2.2 ("DR — cloud — AWS primary").

## Root cause analysis

For the postmortem (§6 of sev-playbook.md):

- `ssh root@api-XX "journalctl -u ratesengine-api --since '1h ago'"`
  on each of the three hosts → full log dump.
- Grafana screenshot of the 1 h window around the alert.
- `git log -n 20 main` — was there a deploy-time trigger?
- `systemctl status ratesengine-api --no-pager` on each host —
  recent restarts, OOM-killer activity (also `dmesg | grep -i
  oom`).
- HAProxy access log (`/var/log/haproxy.log`) for the same window
  — backend transitions, retries, 5xx attribution per backend.
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
- **Minute-zero after release** — rolling restart briefly serves
  503 from a host that just (re)started before its `/v1/readyz`
  flips green. The 10s `slowstart` in HAProxy + the readyz check
  bound this to a few seconds per host; alert window is 2 min
  so a normal rolling release won't trip it. If you see it
  during a planned rollout, the deploy script should silence
  this alert for the window.

## Related

- [api-down](api-down.md) — every backend Down rather than just
  errored.
- [api-latency](api-latency.md) — runs in parallel when the 5xx
  is from timeouts.
- [timescale-primary-down](timescale-primary-down.md) — likely
  cause when 5xx is global + readyz shows postgres down.
- [release-process.md](../release-process.md) → Rollback — the
  binary-swap procedure cited in §A.
- [sev-playbook](../sev-playbook.md) §3 — detection channels;
  §4 — response flow; §5 — public-comms templates.
- [alerts-catalog](../alerts-catalog.md) — the rules this
  runbook serves.
- [ha-plan.md](../../architecture/ha-plan.md) §9 — degradation
  flags (`stale`, `reduced_redundancy`) the handler returns
  during partial outages.

## Changelog

- 2026-04-23 — initial draft. @ash.
- 2026-04-30 — runbook now also covers the SLO multi-window
  availability burn-rate alerts shipped in #313 (per ADR-0009),
  which route here.
- 2026-05-02 — converted from kubectl/Istio commands to
  systemd / journalctl / HAProxy admin socket, reflecting the
  bare-metal deployment ratified in ADR-0008. §D scale guidance
  rewritten — bare metal doesn't autoscale; shed load + edge
  rate-limit is the real mitigation.

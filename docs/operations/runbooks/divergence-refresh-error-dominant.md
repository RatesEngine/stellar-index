---
title: Runbook — divergence-refresh-error-dominant
last_verified: 2026-05-02
status: living
severity: P3
---

# Runbook — `ratesengine_divergence_refresh_error_dominant`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_divergence_refresh_error_dominant` |
| Severity | P3 (ticket) |
| Detected by | `deploy/monitoring/rules/divergence.yml` |
| Typical MTTR | 5–60 min (usually upstream-reference recovery) |
| Impact | The API's `flags.divergence_warning` stops updating. After the 5-min cache TTL elapses, consumers see no warning even when prices DO diverge — false-negative window. Aggregate price endpoints continue serving; only the divergence flag is degraded. |

## Symptoms

- `rate(ratesengine_divergence_refresh_total{outcome="refresh_error"}[5m]) > rate(ratesengine_divergence_refresh_total{outcome="ok"}[5m])` sustained 30+ min.
- Aggregator log lines repeat `divergence refresh failed` with the underlying error (CoinGecko 429, Chainlink RPC timeout, Redis cache write failure).
- After 5 min sustained, `/v1/price` consumers reading `flags.divergence_warning` see whatever the last successful refresh wrote — eventually nothing once entries TTL out.

## Background — why this fires

The aggregator's orchestrator calls `divergence.Service.RefreshPair`
once per configured pair per Tick (default 30s). Each call queries
all configured external references concurrently (CoinGecko +
optionally Chainlink), computes the median, and writes
`div:<asset>` in Redis with a 5-min TTL.

`refresh_error` outcomes mean the call reached the refresher but
something downstream broke. The four common patterns:

1. **CoinGecko rate-limit** — free tier is 30 calls/min. With ~15
   pairs × 2 Hz = 30 calls/min, you're at the edge. Operators with
   more pairs need either a longer `interval_seconds` or a paid
   CoinGecko key.
2. **CoinGecko 5xx** — their free-tier endpoint occasionally
   degrades. Self-recovers in 5–30 min.
3. **Chainlink RPC unreachable** — the configured
   `[divergence].chainlink.rpc_url` is down or the operator's
   chosen RPC provider is rate-limiting. Check
   `cloudflare-eth.com` first (the default).
4. **Redis cache write failed** — happens during a Sentinel
   failover. Self-recovers; if not, page on `redis-conns-saturated`
   instead.

## Quick diagnosis (≤ 5 min)

```sh
# 1) Confirm the alert is real and which outcome dominates.
curl -fs http://localhost:9464/metrics \
  | grep '^ratesengine_divergence_refresh_total'

# 2) Look at recent aggregator logs for the underlying error.
journalctl -u ratesengine-aggregator -n 100 \
  | grep 'divergence refresh failed'

# 3) Probe each configured reference manually:
#    CoinGecko (the default)
curl -fs 'https://api.coingecko.com/api/v3/simple/price?ids=stellar&vs_currencies=usd'
#    Chainlink (when configured)
#    grep 'rpc_url' /etc/ratesengine.toml — then curl that endpoint
#    with a JSON-RPC eth_chainId payload.
```

## Decision tree

| Underlying error | Likely cause | Mitigation |
| ---------------- | ------------ | ---------- |
| HTTP 429 from CoinGecko | Rate-limit | Increase `[aggregate].interval_seconds`; reduce watched pairs; or get a paid CoinGecko key |
| HTTP 5xx from CoinGecko | Upstream degraded | Wait for upstream recovery (typically ≤30 min); the alert auto-resolves |
| Chainlink RPC timeout | RPC provider issue | Switch `[divergence].chainlink.rpc_url` to a different provider OR disable Chainlink temporarily |
| Redis cache write failed | Sentinel failover or Redis OOM | Check `redis-conns-saturated`; otherwise wait for failover to complete |
| All-references failed | Network egress is blocked | Check egress firewall + DNS for the aggregator host |

## Mitigation (≤ 60 min)

- [ ] **Identify the reference** failing via aggregator logs.
- [ ] **Probe the reference manually** (commands above) to confirm
      it's the upstream and not us.
- [ ] **Disable the failing reference** if recovery is taking
      longer than your tolerance window. Edit `[divergence]` in
      operator config, set the relevant `enabled = false`, restart
      the aggregator. The remaining references continue feeding
      the divergence comparison.
- [ ] **Verify** `rate(ratesengine_divergence_refresh_total{outcome="ok"}[5m])`
      recovers above the `refresh_error` rate; the alert
      auto-resolves after 30 min sustained.

## Root cause analysis

Capture for the postmortem:

- The reference that was failing (CoinGecko / Chainlink).
- The aggregator log line with the error class (HTTP status,
  network error, etc.).
- Duration of the outage (alert FIRING → RESOLVED).
- Whether `flags.divergence_warning` actually went stale on
  consumer-facing endpoints during the window.

## Known false-positive patterns

- **Cold start**: aggregator boots, queries fire, Redis isn't
  ready yet. The `for: 30m` clause masks this; if you keep
  hitting it on every restart, lengthen the API's `readyz`
  Redis-check timeout instead of widening this alert.
- **Operator config change**: editing `[divergence].coingecko`
  while the aggregator is running causes one tick of
  refresh_error before the new config takes effect (the
  reference closes its HTTP client). Self-recovers; ignore
  short blips.

## Related

- [`docs/architecture/aggregation-plan.md`](../../architecture/aggregation-plan.md)
  — divergence service architecture.
- [ADR-0019](../../adr/0019-anomaly-response-and-confidence-scoring.md)
  — divergence's role in the confidence score.
- `internal/divergence/` package code.
- Sibling alerts: `price-divergence` (the actual divergence-percent
  signal); `oracle-stale` (a different staleness shape).

## Changelog

- 2026-05-02 — initial draft alongside the divergence-refresh
  wiring (#429).

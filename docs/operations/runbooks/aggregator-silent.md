---
title: Runbook — aggregator-silent
last_verified: 2026-04-25
status: draft
severity: P1
---

# Runbook — `ratesengine_aggregator_silent`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_aggregator_silent` |
| Severity | **P1** (page) |
| Detected by | `deploy/monitoring/rules/aggregator.yml` |
| Typical MTTR | 5–20 min |
| Impact | Zero VWAP cache writes for 5+ min. `/v1/vwap` falls back to on-query raw aggregation (slow + ADR-0007 cache-miss path). Freshness signal API consumers depend on goes quiet within 60 s. |

## Symptoms

- `sum(rate(ratesengine_aggregator_vwap_writes_total[5m])) == 0` for ≥ 5 min.
- `/metrics` on the aggregator binary shows the counter not advancing
  between scrapes.
- `/v1/vwap?pair=...` responses carry an `observed_at` lagging the
  raw trade tip by minutes (when normally it tracks within tens of
  seconds).

## Quick diagnosis (≤ 5 min)

```sh
# 1) Is the binary alive at all?
systemctl status ratesengine-aggregator
journalctl -u ratesengine-aggregator -n 50 --no-pager

# 2) What does the orchestrator say about its last tick?
curl -fs http://localhost:9464/metrics | grep -E '^ratesengine_aggregator_(ticks_total|empty_windows|dropped_trades|vwap_writes)'

# 3) Is Redis reachable and accepting writes?
redis-cli -h <redis_host> -a "$REDIS_PASSWORD" PING
redis-cli -h <redis_host> -a "$REDIS_PASSWORD" SET _aggregator_probe "$(date -Iseconds)" EX 60

# 4) Is Timescale serving trades for the configured pair set?
psql -d ratesengine -c "SELECT pair, COUNT(*) FROM trades WHERE timestamp > now() - interval '5 minutes' GROUP BY pair ORDER BY 2 DESC LIMIT 10;"
```

Three signal-pairs to read off the metrics dump:

| What you see | What it means | Where to look next |
| ------------ | ------------- | ------------------ |
| `ticks_total{outcome="ok"}` advancing, `vwap_writes_total` flat | Ticks running but every (pair, window) lands in the empty-window branch | `empty_windows_total` should match the (pairs × windows) rate; if so → check Timescale for trade volume |
| `ticks_total{outcome="error"}` only advancing | Refresh-loop failures. Check journalctl for `refresh failed` lines | Likely Timescale or Redis side; section "Mitigation" below |
| `ticks_total` flat, no advance at all | Orchestrator tick loop not running | Process is alive but stuck — `pprof` goroutines, or restart |

## Mitigation (≤ 15 min)

- [ ] **If Redis is the proximate cause** (PING fails / writes error): fail
      Redis over to its standby node before touching the aggregator. The
      aggregator retries on the next tick, no restart needed.
- [ ] **If the tick loop is wedged**: `systemctl restart ratesengine-aggregator`.
      The orchestrator's first-tick-immediate behaviour means VWAP writes
      resume within `interval_seconds` (default 30 s) of restart.
- [ ] **If empty windows are the cause** (Timescale has trades but every
      configured pair returns zero rows): check whether
      `enable_stablecoin_fiat_proxy` is needed — a fiat-quote pair
      (`XLM/fiat:USD`) without expansion only matches direct FX-feed
      trades, which may be sparse if FX connectors aren't enabled.
- [ ] **Verification**: `vwap_writes_total` resumes advancing within
      one tick interval; alert clears within `for: 5m` of recovery.

## Root cause analysis

Capture for the postmortem:

- `journalctl -u ratesengine-aggregator --since='15 minutes ago'`
- A `/metrics` snapshot before and after recovery (paste both
  `ticks_total` and `dropped_trades_total` series).
- Any concurrent Timescale or Redis incidents from the same window.
- The current `cfg.Aggregate.*` state and the configured `Pairs` /
  `Windows` set — if expansion is off but the operator wanted it on,
  that's a config drift item.

## Known false-positive patterns

- **Aggregator binary not deployed in this environment** — dev /
  staging stacks where only the indexer + API are running. Either
  silence the rule on those targets via AlertManager routing, or
  set the rule's group to use `up{job="aggregator"} == 1` as a
  required precondition.
- **First 60 s after a fresh aggregator boot** — the immediate
  initial tick lands inside the alert's 5 min window, so a clean
  start should never trigger. If it does, suspect a config or
  storage bring-up issue rather than the orchestrator itself.

## Related

- ADR-0007 (cache strategy) — explains why VWAP is pre-computed
  rather than on-query.
- `ratesengine_aggregator_outlier_storm` — often co-fires when a
  market event simultaneously drives VWAP writes to zero (every
  trade σ-rejected) and floods the outlier counter.
- `aggregator-outlier-storm.md` — sister runbook; check its
  symptoms before assuming this is a pure orchestrator failure.

## Changelog

- 2026-04-25 — initial draft alongside the aggregator metrics
  PR #26 wire-up.

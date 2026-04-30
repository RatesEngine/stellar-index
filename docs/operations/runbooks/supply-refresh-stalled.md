---
title: Runbook — supply-refresh-stalled
last_verified: 2026-04-30
status: ratified
severity: P2
---

# Runbook — `ratesengine_aggregator_supply_refresh_stalled`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_aggregator_supply_refresh_stalled` |
| Severity | P2 (page) |
| Detected by | `deploy/monitoring/rules/supply-refresh.yml` |
| Typical MTTR | 15–30 min |
| Impact | `/v1/assets/{id}` F2 fields go increasingly stale across all watched assets. Customer-visible after the first stale snapshot lands a few minutes after the alert. |

## Symptoms

- `time() - max(timestamp(ratesengine_aggregator_supply_refresh_total{outcome="ok"})) > 30 * 60`
  for ≥ 5 min.
- Aggregator's logger emits no `supply refresh ok` lines for the
  same window.

## Quick diagnosis (≤ 5 min)

```sh
# 1. Aggregator process up?
sudo systemctl status ratesengine-aggregator

# 2. Are any goroutines progressing? (Other counters should
#    be incrementing if the orchestrator is alive.)
curl -s http://aggregator:9464/metrics | grep ratesengine_aggregator_ticks_total

# 3. What's the most-recent supply refresh outcome label?
#    The metric is keyed by (asset_key, outcome) — if every asset_key
#    has stopped incrementing the page is fleet-wide; if only one
#    asset_key has stalled while others tick, the failure is
#    per-asset and `error_dominant` should ALSO be firing.
curl -s http://aggregator:9464/metrics | \
  awk '/^ratesengine_aggregator_supply_refresh_total\{/' | sort

# 4. Recent supply-refresh logs.
sudo journalctl -u ratesengine-aggregator --since "1 hour ago" -n 200 | \
  grep -E "supply refresh|supply-refresh"
```

## Typical root causes

1. **Aggregator process down.** `systemctl status` shows
   inactive / failed.
   - Mitigation: investigate the crash via journald; restart.

2. **Orchestrator wedged.** Process is running but no goroutine
   is making progress. The orchestrator's own tick counter
   (`ratesengine_aggregator_ticks_total`) is also stalled.
   - Mitigation: restart the binary. File a P2 bug for the wedge.

3. **Every tick failing.** Goroutine is alive but every
   per-asset Tick produces a non-ok outcome.
   `_error_dominant` should ALSO be firing in this case — route
   to that runbook.

4. **Aggregator is up but supply refresher is disabled.**
   Operator set `[supply] aggregator_refresh_enabled = false` (or
   left it default) and the systemd-timer path is the active
   producer instead. The alert is correct as configured but maps
   to "wrong-environment" rather than "broken."
   - Mitigation: silence the alert with a label selector, or flip
     `aggregator_refresh_enabled = true` if the goroutine path is
     the intended deployment.

## Mitigation

- [ ] Step 1 — Check process health (Quick diagnosis #1).
- [ ] Step 2 — If process is up but stalled: check
      `ratesengine_aggregator_ticks_total` — if THAT also stalled,
      the orchestrator is wedged; restart.
- [ ] Step 3 — If process is up + orchestrator is ticking but
      supply isn't: confirm `aggregator_refresh_enabled = true` in
      the config; check logs for repeated outcome labels.
- [ ] Step 4 — Force a restart as the safe mitigation;
      investigate the underlying cause from journald +
      pprof goroutine dump if available.
- [ ] Verification: `outcome="ok"` increments resume within
      `aggregator_refresh_cadence` (default 5 min). The alert
      clears once `time() - max(timestamp(...)) <= 30*60`.

## Known false-positive patterns

- **Aggregator restart in progress.** The first few minutes
  after a restart have no observations yet. `for: 5m` absorbs
  ~one cadence; longer restarts still trip it.
- **Refresher disabled by config.** Operator-intentional. Use a
  silence rather than disabling the alert.

## Related

- `supply-refresh-error-dominant.md` — when the refresher is
  alive but every tick fails.
- `supply-snapshot-stale.md` — the systemd-timer-path equivalent
  (different metric, different expectation).
- `aggregator-silent.md` — when the orchestrator's tick counter
  itself is stalled.

## Changelog

- 2026-04-30 — initial draft alongside #313 (supply-refresh
  alerts).
- 2026-04-30 — quick-diagnosis #3 now references the
  `asset_key` label (added in #314) so operators can confirm
  whether the stall is fleet-wide or scoped to one asset.

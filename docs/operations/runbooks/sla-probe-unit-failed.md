---
title: Runbook — sla-probe-unit-failed
last_verified: 2026-04-30
status: ratified
severity: P3
---

# Runbook — `ratesengine_sla_probe_unit_failed_alert`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_sla_probe_unit_failed_alert` |
| Severity | P3 (ticket) |
| Detected by | `deploy/monitoring/rules/sla-probe.yml` |
| Typical MTTR | depends on the underlying breach |
| Impact | Umbrella signal — at least one SLA is breached. The more-specific p95 / freshness / availability alerts will name which one if they fire faster. |

## Symptoms

- `ratesengine_sla_probe_unit_failed > 0` for ≥ 30 min.
- Most-recent JSON report in journald carries a non-empty
  `failed_reasons` array.
- Often (not always) accompanied by one of the per-breach alerts
  in this family — those alerts are the "go fix THIS specific
  thing" page; this is the "one of them is firing somewhere"
  ticket.

## Quick diagnosis (≤ 5 min)

```sh
# Pull the most-recent failed-reasons array — it names the breach.
sudo journalctl -u ratesengine-sla-probe.service -n 1 --output=cat | jq -r '.failed_reasons[]'
```

Output is one of:

```
price: p95=215.3ms > target 200.0ms
price: p99=523.1ms > target 500.0ms
healthz: availability=98.50% < target 99.90%
price: freshness=42.1s > target 30.0s
```

## Mitigation

- [ ] Step 1 — Read the `failed_reasons` array to identify the
      breach kind (latency / availability / freshness).
- [ ] Step 2 — Route to the corresponding per-breach runbook:
  - p95 / p99 → `sla-probe-p95-breach.md`
  - freshness → `sla-probe-freshness-breach.md`
  - availability → `api-5xx.md` (the probe's "availability" maps
    to 2xx-success rate, which is the same signal as the API
    error-rate alert).
- [ ] Verification: `unit_failed` drops to 0 for ≥ 30 min.

## Why this exists alongside the per-breach alerts

The per-breach alerts (`_p95_breach`, `_freshness_breach`) are the
loud signals — they page on a specific known-bad condition. This
umbrella alert catches edge cases:

1. A breach combination not yet covered by a per-breach rule
   (e.g. p99 breaches alone).
2. A new endpoint added to the probe whose specific thresholds
   haven't been wired into the per-breach rules yet.
3. A future pair-specific breach (e.g. `availability` per pair
   not yet promoted to its own rule).

Treat it as a TODO indicator — if this fires without a per-breach
companion, that's a signal to add the per-breach rule.

## Known false-positive patterns

- None expected — the probe's verdict is deterministic from the
  measured values. If this fires without a real breach, that's a
  bug in `cmd/ratesengine-sla-probe/main.go::computeVerdict`.

## Related

- `sla-probe-p95-breach.md` — specific p95 breach.
- `sla-probe-freshness-breach.md` — specific freshness breach.
- `api-5xx.md` — the availability path.

## Changelog

- 2026-04-30 — initial draft alongside #294 (alert rules).

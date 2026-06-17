---
title: Runbook — aggregator-class-drop-spike
last_verified: 2026-04-25
status: draft
severity: P3
---

# Runbook — `stellarindex_aggregator_class_drop_spike`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `stellarindex_aggregator_class_drop_spike` |
| Severity | P3 (ticket) |
| Detected by | `deploy/monitoring/rules/aggregator.yml` |
| Typical MTTR | 30 min |
| Impact | The class filter is dropping trades at >10× baseline. Most often this means a new venue is emitting trades that aren't yet registered in `external.Registry`, so they hit the fail-closed `IncludeInVWAP=false` fallback and don't contribute to VWAP. Ingest still records the rows; only aggregation is affected. |

## Symptoms

- `sum(rate(stellarindex_aggregator_dropped_trades_total{reason="class"}[10m]))` > 10× baseline.
- New entries in `stellarindex_source_events_total` for source labels
  the registry doesn't list.

## Quick diagnosis (≤ 5 min)

```sh
# 1) Which source is producing the unregistered traffic?
curl -fs http://localhost:9464/metrics \
  | grep '^stellarindex_source_events_total{' | sort -t'"' -k2

# 2) Compare against what the registry knows.
grep -E '"[a-z][a-z0-9_-]+":\s*\{' \
  internal/sources/external/registry.go

# 3) Anything in the trades table from a never-before-seen source?
psql -d stellarindex -c \
  "SELECT source, COUNT(*) AS rows, MIN(timestamp) AS first_seen
   FROM trades
   WHERE timestamp > now() - interval '1 hour'
   GROUP BY source
   ORDER BY first_seen DESC
   LIMIT 20;"
```

The rows-vs-registry diff is the smoking gun: any source name in
the trades table that doesn't appear in `internal/sources/external/registry.go`
is being treated as `Class=Exchange + IncludeInVWAP=false` (the
fail-closed fallback) and silently dropped from VWAP.

## Mitigation (≤ 15 min)

- [ ] **Net-new venue intentionally being added** (someone is
      onboarding a connector): add a one-line entry to
      `external.Registry` under the appropriate `Class*` constant.
      A venue with a classifying ADR open and trade flow already in
      Timescale should be `ClassExchange + IncludeInVWAP=true`. A
      paid-tier aggregator/oracle stays `IncludeInVWAP=false`.
- [ ] **Unintentional source name** (typo, renamed connector, dev
      build leaking into prod): identify the rogue process and roll
      back. The trades it inserted are valid data — leave them in
      Timescale; they'll show up in `/v1/sources` once the source
      is registered.
- [ ] **Existing aggregator/oracle source spiked in volume**: this
      is expected — aggregator-class sources are *meant* to be
      dropped from VWAP. If the spike has a known cause (CoinGecko
      added new pairs, a Reflector contract upgraded), document
      it in the on-call channel and silence the alert for the
      relevant interval. No code change needed.
- [ ] **Verification**: `dropped_trades_total{reason="class"}` rate
      returns to baseline (or, after a registry update, the same
      rate but with the source now contributing as `IncludeInVWAP=true`
      → `vwap_writes_total` should also tick up).

## Root cause analysis

Capture for the postmortem:

- The source-label list before/after the change.
- The PR / commit that added or renamed the source.
- Confirmation that the source's class assignment is intentional.

## Known false-positive patterns

- **First hour after the alert rule lands**: as with
  `aggregator-outlier-storm`, the `offset 1h` comparator misfires
  during the first hour after deploy. Suppress the rule on first
  rollout.
- **Test fixtures leaking into prod metrics namespace**: a
  development binary scraped by prod Prometheus produces this
  exact symptom. Check `stellarindex_source_enabled` for sources
  that should be off in this region.

## Related

- `aggregator-outlier-storm.md` — sibling per-reason drop alert.
- `internal/sources/external/registry.go` — single-source-of-truth
  for class assignments. Adding a venue is a one-line amendment.
- ADR (TBD) — operator-facing per-source weighting once the
  registry outgrows a hand-curated map.

## Changelog

- 2026-04-25 — initial draft alongside the aggregator metrics
  PR #26 wire-up.

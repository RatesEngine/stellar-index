---
title: Runbook — aggregator-outlier-storm
last_verified: 2026-04-25
status: draft
severity: P3
---

# Runbook — `ratesengine_aggregator_outlier_storm`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_aggregator_outlier_storm` |
| Severity | P3 (ticket) |
| Detected by | `deploy/monitoring/rules/aggregator.yml` |
| Typical MTTR | 30 min – several hours |
| Impact | The σ-threshold filter is ejecting trades 5× faster than baseline. VWAP is computed on a smaller, possibly-biased sample; downstream divergence alerts likely follow if the cause is real market dislocation. |

## Symptoms

- `sum(rate(ratesengine_aggregator_dropped_trades_total{reason="outlier"}[10m])) > 5×` baseline.
- `/v1/vwap` may show a smaller `samples` count (when that field
  lands in PR-191) per pair than the same hour the previous day.
- Often co-occurs with `ratesengine_price_divergence_warning` if
  the underlying market actually moved fast.

## Quick diagnosis (≤ 5 min)

```sh
# 1) Confirm the spike is real, not just a baseline calibration issue.
curl -fs http://localhost:9464/metrics \
  | grep '^ratesengine_aggregator_dropped_trades_total{reason="outlier"}'

# 2) Is the upstream-trade rate also elevated? (real volume → real outliers)
psql -d ratesengine -c \
  "SELECT pair, COUNT(*) AS rows
   FROM trades
   WHERE timestamp > now() - interval '15 minutes'
   GROUP BY pair ORDER BY 2 DESC LIMIT 10;"

# 3) Sanity-check by pair: is the storm one pair or many?
# (planned: when per-pair labels exist; for now infer from
#  ratesengine_source_events_total per source)
curl -fs http://localhost:9464/metrics \
  | grep '^ratesengine_source_events_total' | sort
```

Decision tree:

| Trade-rate elevated? | Multiple pairs? | Probable cause | Mitigation |
| -------------------- | --------------- | -------------- | ---------- |
| Yes | Many | Real market-wide event (BTC flash-move, fiat-pair news) | Wait it out; verify divergence-warning fires + clears alongside |
| Yes | One pair | Pair-specific dislocation, possibly a depeg | Check the pair's primary venue; consider pausing stablecoin proxy for that quote if it was a peg break |
| No | Many | Connector regression — every venue producing weird amounts | Check `ratesengine_source_decode_errors_total`; likely a recent decoder change |
| No | One pair, one source | Single connector misbehaving (amount-decimal regression) | Disable that source via config; open ticket against the connector |

## Mitigation (≤ 15 min)

- [ ] **Real market event**: leave the filter doing its job. Annotate
      the on-call channel with the incident timestamp + the pair(s)
      affected so the postmortem-window divergence numbers have context.
- [ ] **Connector regression**: identify the offending source via
      `ratesengine_source_events_total` × `ratesengine_source_decode_errors_total`
      ratio + recent deploy diff. Disable that source in TOML
      (`[external.<venue>] enabled = false`) and reload — the
      orchestrator picks up the change at next tick.
- [ ] **Filter mis-calibration**: if neither of the above holds and
      the storm is sustained > 1 h, lower
      `aggregate.outlier_sigma_threshold` from 4.0 → 5.0 / 6.0 to
      let more rows through while RCA continues — a wide filter
      with weak signal beats a narrow filter dropping legitimate
      data.
- [ ] **Verification**: `dropped_trades_total{reason="outlier"}`
      rate returns within 5× of baseline.

## Root cause analysis

Capture for the postmortem:

- A 1-hour metric range showing the spike + recovery.
- Trade-table samples around the spike — what *did* the wild rows
  look like? (Source, pair, base/quote amounts, timestamp.)
- If it was a connector regression: the most recent commit touching
  the offending source's `parse.go` / `decode.go`.
- If it was a real market event: external context (CoinGecko /
  CoinMarketCap headlines, Tradingview screenshots) so the postmortem
  doesn't read as a pure ratesengine failure when it's the world.

## Known false-positive patterns

- **First hour after the alert rule lands**: the comparator
  (`offset 1h`) returns zero before there's an hour of history,
  which produces a divide-by-zero spike then a flatline. Suppress
  the alert during the first hour after deploy.
- **Aggregator restart**: ticks bunching up post-restart can briefly
  inflate the per-10m rate; usually clears in ≤ 5 min.
- **First tick of a new pair**: a freshly-added pair with sparse
  trades has σ ≈ 0 → every row drops. Mitigation: the filter's
  fewer-than-3-prices guard should kick in (verify
  `aggregate.FilterOutliers` returned input unchanged); if it
  didn't, that's a real bug.

## Related

- `aggregator-silent.md` — frequently co-fires when the storm
  filters out *every* row.
- ADR (TBD) σ-vs-MAD outlier filter — long-term migration plan if
  σ-threshold turns out to be too brittle on small windows. Until
  the ADR lands the σ default lives at
  `aggregate.outlier_sigma_threshold = 4.0` in TOML.
- `internal/aggregate/outliers.go` — filter implementation. Any
  algorithm change must update this runbook.

## Changelog

- 2026-04-25 — initial draft alongside the aggregator metrics
  PR #26 wire-up.

---
title: SLA proof report — TEMPLATE
last_verified: 2026-05-01
status: template (copy to sla-proof-YYYY-MM-DD.md per run)
related:
  - docs/architecture/k6-load-tests-design-note.md
  - test/load/scenarios/06-mixed-realistic.js
  - deploy/monitoring/rules/slo.yml
---

# SLA proof report — TEMPLATE

**This is the template.** Copy to
`docs/operations/sla-proof-YYYY-MM-DD.md` after each canonical
mixed-realistic run; fill in the `<<…>>` placeholders; commit
alongside the CHANGELOG entry.

The most recent passing report is the public proof-of-SLA the
service's headline claim ("p95 ≤ 200 ms; 99.9 % availability")
refers to.

## Run window

- **Start:** `<<UTC timestamp>>`
- **End:** `<<UTC timestamp>>`
- **Scenario:** `test/load/scenarios/06-mixed-realistic.js`
- **Target:** `<<staging hostname>>`
- **k6 version:** `<<output of `k6 version`>>`
- **Commit:** `<<git SHA tested against>>`

## Result

| Metric | Threshold | Actual | Verdict |
|---|---|---|---|
| `http_req_duration` p95 (mix) | < 200 ms | `<<value>>` | `<<PASS / FAIL>>` |
| `http_req_duration` p99 (mix) | < 500 ms | `<<value>>` | `<<PASS / FAIL>>` |
| `http_req_failed` rate | < 0.1 % | `<<value>>` | `<<PASS / FAIL>>` |
| `since-inception` p95 | < 1000 ms | `<<value>>` | `<<PASS / FAIL>>` |
| `batch` p95 | < 500 ms | `<<value>>` | `<<PASS / FAIL>>` |

**Overall:** `<<PASS / FAIL>>`.

## Per-endpoint breakdown

| Endpoint | Share | p50 | p95 | p99 | Errors |
|---|---|---|---|---|---|
| `/v1/price` | 60 % | `<<>>` | `<<>>` | `<<>>` | `<<>>` |
| `/v1/price/batch` | 15 % | `<<>>` | `<<>>` | `<<>>` | `<<>>` |
| `/v1/price/tip` | 10 % | `<<>>` | `<<>>` | `<<>>` | `<<>>` |
| `/v1/vwap` | 6 % | `<<>>` | `<<>>` | `<<>>` | `<<>>` |
| `/v1/history` | 4 % | `<<>>` | `<<>>` | `<<>>` | `<<>>` |
| `/v1/twap` | 3 % | `<<>>` | `<<>>` | `<<>>` | `<<>>` |
| `/v1/observations/stream` | 1 % | `<<>>` | `<<>>` | `<<>>` | `<<>>` |
| `/v1/oracle/lastprice` | 1 % | `<<>>` | `<<>>` | `<<>>` | `<<>>` |

## Concurrent ingest activity

(Proof we weren't load-testing an idle stack — a quiet ingest
during the run window invalidates the soak claim.)

- **Ledgers ingested during run:** `<<count>>`
- **Trades stored during run:** `<<count>>`
- **Aggregator refresh ticks during run:** `<<count>>`

Dashboard snapshot: `<<grafana snapshot URL>>`

## Grafana snapshots

- Per-endpoint p95 chart (run window): `<<grafana snapshot URL>>`
- Error-rate chart (run window): `<<grafana snapshot URL>>`
- Ingest activity chart (run window): `<<grafana snapshot URL>>`

## Run command

```sh
export K6_TARGET=https://api.staging.stellarindex.io/v1
export STELLARINDEX_LOAD_API_KEY="<from vault>"
make test-load-mixed
```

## Notes / caveats

`<<Free text. Document anything unusual: a flake retried, a
pre-existing alert that fired during the window, a regional
latency spike. Empty for clean runs.>>`

## Sign-off

- **Operator:** `<<name>>`
- **Reviewer:** `<<name>>`
- **Promoted to release notes:** `<<yes / no — link to release if yes>>`

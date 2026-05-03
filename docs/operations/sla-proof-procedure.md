---
title: SLA proof procedure (Task #77)
last_verified: 2026-05-03
status: ratified
related:
  - test/load/scenarios/06-mixed-realistic.js
  - docs/architecture/k6-load-tests-design-note.md §"How the proof report (#77) is generated"
  - docs/architecture/launch-readiness-backlog.md L5.* / L6.*
---

# SLA proof procedure

Step-by-step recipe for producing the **monthly + pre-launch SLA
proof report** that Task #77 in the launch-readiness backlog
demands. The output of this procedure is a checked-in markdown
file at `docs/operations/sla-proof-<YYYY-MM-DD>.md` with PASS /
FAIL against each SLO threshold from
[ADR-0009 `multi-window SLO`](../adr/0009-latency-budget.md).

The k6 scenarios themselves are
[Task #74](../architecture/launch-readiness-backlog.md) and live at
[`test/load/scenarios/`](../../test/load/scenarios/). The design
note backing the scenarios is
[`docs/architecture/k6-load-tests-design-note.md`](../architecture/k6-load-tests-design-note.md).

## What we're proving

Per ADR-0009 and [Freighter RFP §SLA](../freighter-rfp.md):

| Metric | Target | Source of truth |
| --- | --- | --- |
| `/v1/price` p95 | ≤ 200 ms | `06-mixed-realistic.js` `http_req_duration{endpoint="price"}` |
| `/v1/price` p99 | ≤ 500 ms | same, p99 |
| Error rate (5xx + non-2xx) | < 0.1 % over 10 min | k6 `http_req_failed` rate |
| Sustained load | 300 rps for 10 min uninterrupted | scenario soak window |
| Concurrent ingest active | yes | grafana panel "indexer ledgers/min" non-zero |

A run that fails any of these is **not** a valid SLA proof; the
operator either reruns after fixing the regression or files the
gap into the launch-readiness backlog before relaunching the
proof.

## Pre-flight checklist

Before kicking off the run:

- [ ] Staging stack is the **same configuration shape as
      production** — same Patroni / HAProxy / Redis-Sentinel
      ansible roles applied (`make ansible-staging-apply` if
      drift suspected).
- [ ] Indexer is actively ingesting (`/v1/readyz` shows
      `indexer.lag_seconds < 60`).
- [ ] Aggregator is publishing into the live serving cache —
      `/v1/price?asset=native&quote=fiat:USD` returns a fresh
      timestamp (within last 60 s).
- [ ] Status page shows operational; no active incident.
- [ ] No coincident chaos drill, deploy, or maintenance window
      scheduled in the run window (the proof is meaningless if
      the stack is being perturbed).

## Run

```sh
# Required env vars:
export K6_TARGET=https://api.staging.ratesengine.net/v1
export RATESENGINE_LOAD_API_KEY="<paste from vault — load-test key, not customer key>"

# Optional — override the Prometheus output target if you want
# the run isolated from the regular metrics stack:
# export PROM_OUT=experimental-prometheus-rw

# The canonical proof run (~13 min total: 30s ramp + 2m ramp +
# 10m soak + 30s drain):
make test-load-mixed
```

Note the **start UTC** + **end UTC** of the run. The Grafana
panel queries below use this window.

## Capture the artefacts

### 1. Grafana snapshot

Open the load-proof dashboard:
`https://grafana.staging.ratesengine.net/d/load-proof/k6-load-proof?from=<start>&to=<end>`.

Click **Share → Snapshot → External (Internet)** to mint a
public snapshot URL. **Mint with no expiry**; the link goes into
the markdown report and needs to outlive the dashboard.

Verify the snapshot includes (at minimum):

- Per-endpoint p95 chart for the soak window.
- Aggregate p95 / p99 chart.
- Error-rate chart.
- Concurrent indexer activity (proves we weren't load-testing
  an idle stack).

### 2. Promql baseline reads

Capture the numeric quantiles over the **soak window only** (not
the ramp), filtered to the k6 traffic only via the `tag` label:

```promql
# /v1/price p95 over soak window
histogram_quantile(0.95, sum by (le) (
  rate(k6_http_req_duration_seconds_bucket{
    endpoint="price", scenario="06-mixed-realistic"
  }[10m])
))

# Aggregate p99
histogram_quantile(0.99, sum by (le) (
  rate(k6_http_req_duration_seconds_bucket{
    scenario="06-mixed-realistic"
  }[10m])
))

# Error rate
sum(rate(k6_http_req_failed_total{scenario="06-mixed-realistic"}[10m]))
  /
sum(rate(k6_http_reqs_total{scenario="06-mixed-realistic"}[10m]))
```

Each query against the soak window. Numbers go into the report.

## Write the report

Clone the template at
[`sla-proof-template.md`](sla-proof-template.md) to
`docs/operations/sla-proof-<YYYY-MM-DD>.md`. Fill in:

1. The run window (start / end UTC).
2. The per-endpoint p95 / p99 / error-rate numbers from the
   Promql captures above.
3. The PASS / FAIL marker for each SLA row.
4. The Grafana snapshot link.
5. A one-line note on concurrent ingest activity ("indexer at
   12 ledgers/min; aggregator publishing every 5s").
6. Anything anomalous worth surfacing (a single 502 burst, a GC
   pause, a momentary p99 spike) — better to over-report than
   under-report.

Open the PR. The report file is the deliverable.

## Cadence

| Trigger | Required |
| --- | --- |
| Pre-launch (Task #77 closure) | yes — first proof; bumps L5.* / L6.* status |
| Monthly | yes — drift detection per design note §"Where this runs" |
| After any major release (semver minor or major) | yes |
| After any infra change touching API / storage / cache layer | yes |
| Post-incident | yes if the incident root cause was capacity-shaped |

A failing run **does not** invalidate the previous month's
proof; the previous proof remains the most-recent passing one.
But a failing run **does** require either a fix or an explicit
"why we're shipping anyway" annotation in the launch-readiness
backlog.

## What if staging isn't available?

If staging access is delayed (vendor / DNS / paperwork), the
launch-readiness backlog L5.4 already documents the
"documented-acceptance" path: the mixed-realistic scenario
covers the ingest-side soak via its own metrics, and a
synthetic proof run against the local docker-compose dev stack
produces directional numbers (lower fidelity, useful as a
sanity check). Document the fallback path in the proof report's
"Caveats" section so the reader knows the run wasn't against
production-shape infra.

## References

- Scenario:
  [`test/load/scenarios/06-mixed-realistic.js`](../../test/load/scenarios/06-mixed-realistic.js)
- Design note:
  [`docs/architecture/k6-load-tests-design-note.md`](../architecture/k6-load-tests-design-note.md)
- Report template:
  [`sla-proof-template.md`](sla-proof-template.md)
- ADR-0009 multi-window SLO:
  [`../adr/0009-latency-budget.md`](../adr/0009-latency-budget.md)
- Reports directory README:
  [`../../test/load/reports/README.md`](../../test/load/reports/README.md)

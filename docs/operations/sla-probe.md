---
title: SLA probe — periodic per-endpoint evidence trail
last_verified: 2026-05-03
status: living procedure
---

# SLA probe — periodic per-endpoint evidence trail

Operational companion to the executable SLA-evidence CLI shipped in
#283 (`cmd/ratesengine-sla-probe`). This doc covers:

- What the probe is + why it runs continuously
- Daily cron via `deploy/systemd/sla-probe.{service,timer}`
- The RFP-stated SLA targets the probe verifies against
- Textfile-collector integration + the four shipped alerts

## Purpose

The Freighter RFP and the awarded ctx-proposal both bind the API
to four SLA targets:

| Metric                   | Target           | Source                     |
| ------------------------ | ---------------- | -------------------------- |
| p95 latency              | ≤ 200 ms         | Freighter RFP §SLA         |
| p99 latency              | ≤ 500 ms         | Freighter RFP §SLA         |
| Availability             | ≥ 99.9 %         | Freighter RFP §SLA         |
| Price freshness          | ≤ 30 s staleness | Freighter RFP V1 § Pricing |

The SLA probe drives synthetic load against the deployed API,
measures per-endpoint p50/p95/p99 latency, parses `observed_at`
on the price endpoint to compute freshness, and tallies 2xx vs
non-2xx for availability. Each run emits a JSON report and exits
with code 0 (pass) or 1 (any SLA violated).

The systemd timer runs the probe every 15 minutes — tight enough
to pinpoint a SEV-2 latency-spike window (the SEV-2 detection
requirement is ≤ 30 min after the incident begins) but loose
enough that the probe itself doesn't dominate the anonymous-tier
rate budget.

## Operator wiring

```sh
sudo cp deploy/systemd/sla-probe.{service,timer} /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now sla-probe.timer
```

Override defaults via `/etc/default/sla-probe`:

```sh
BASE_URL=https://api.ratesengine.net/v1     # default
DURATION=30s                                 # default
CONCURRENCY=4                                # default
PAIRS="-pair native,fiat:USD -pair USDC-G…,fiat:USD"
REPORT_FORMAT=json                           # default; text also valid
RATESENGINE_PROBE_API_KEY=rek_…              # vault-minted key; required (see below)
EXTRA_FLAGS=""                               # default
```

### Why an API key is required

Without `RATESENGINE_PROBE_API_KEY` set, the probe hits the
anonymous-tier rate limit (60 req/min per IP). At the documented 4
workers × 30 s window that's ~1000 requests/sec/worker — every
non-`/healthz` endpoint reads as `availability < 0.1 %` and the
verdict comes back `fail` for reasons unrelated to actual SLA
compliance. Mint a load-test API key from the operator vault (same
class as `RATESENGINE_LOAD_API_KEY` for the k6 weekly) and set it
in `/etc/default/sla-probe` before enabling the timer. The probe
sends it as `Authorization: Bearer <key>` on every request — the
key never appears on the systemd unit's command line.

The defaults exercise XLM/USD as the smoke-test pair. Add `-pair`
entries to track additional asset/quote combinations the operator
cares about — each repeats the per-endpoint probe across the
chart, price, and oracle-latest surfaces for that pair.

## Reading the output

Each run logs its JSON report to the systemd journal:

```sh
sudo journalctl -u sla-probe.service -n 100 --output=cat | jq .
```

Key fields:

```json
{
  "base_url": "https://api.ratesengine.net/v1",
  "started_at": "2026-04-30T12:00:00Z",
  "duration_sec": 30.0,
  "concurrency": 4,
  "sla": {
    "p95_ms": 200,
    "p99_ms": 500,
    "freshness_sec": 30,
    "availability_pct": 99.9
  },
  "per_endpoint": [
    {
      "endpoint": "price",
      "path": "/price",
      "samples": 120,
      "successes": 120,
      "availability_pct": 100.0,
      "latency_ms": {
        "p50": 12.0, "p95": 45.0, "p99": 78.0,
        "max": 102.0, "mean": 18.0
      },
      "observed_at_fresh_sec": 1.5
    }
    // … one entry per endpoint
  ],
  "verdict": "pass",
  "failed_reasons": []
}
```

A `verdict` of `fail` carries the reasons in `failed_reasons` —
e.g. `["price: p95=215.3ms > target 200.0ms"]`. The unit also
exits non-zero, so `systemctl is-failed sla-probe.service`
reports the breach.

## Pre-flight: spot-check from the operator's laptop

Before enabling the timer, run a single probe directly:

```sh
ratesengine-sla-probe \
  -base-url https://api.ratesengine.net/v1 \
  -duration 10s \
  -concurrency 2 \
  -report-format text
```

The text-format output is easier to scan during ad-hoc triage.
A clean dry-run with `verdict: pass` confirms the endpoint set,
the rate-limit headroom, and the freshness path all work end-to-
end before the cron starts hitting them.

## Textfile-collector integration

`-textfile-output PATH` writes a Prometheus textfile after each
run so node_exporter can scrape per-endpoint p50/p95/p99 latency,
availability, freshness, and a pass/fail gauge. Operator wiring:

```sh
# /etc/default/sla-probe
TEXTFILE_OUTPUT=/var/lib/node_exporter/textfile_collector/sla_probe.prom
```

The systemd service writes to that path via the
`<path>.tmp`-then-rename atomic protocol; node_exporter skips
files whose name ends in `.tmp` so a partial write never appears
in a scrape.

### Metric set

```
ratesengine_sla_probe_latency_ms{endpoint=,quantile=}      gauge   ms
ratesengine_sla_probe_availability_pct{endpoint=}          gauge   percent
ratesengine_sla_probe_freshness_sec{endpoint=}             gauge   seconds (only when present)
ratesengine_sla_probe_samples{endpoint=}                   gauge   count
ratesengine_sla_probe_run_duration_seconds                 gauge   seconds
ratesengine_sla_probe_unit_failed                          gauge   1 on fail, 0 on pass
ratesengine_sla_probe_last_pass_timestamp                  gauge   unix; only on pass
```

### Alerts

Four alerts in `deploy/monitoring/rules/sla-probe.yml`, each with a
runbook under `docs/operations/runbooks/sla-probe-*.md`:

| Alert | Condition | Severity |
|-------|-----------|----------|
| `ratesengine_sla_probe_p95_breach` | per-endpoint p95 > 200 ms sustained 30 min | **P2** page |
| `ratesengine_sla_probe_freshness_breach` | per-endpoint freshness > 30 s sustained 30 min | **P2** page |
| `ratesengine_sla_probe_unit_failed_alert` | overall verdict gauge = 1 sustained 30 min | P3 ticket |
| `ratesengine_sla_probe_stale` | `last_pass_timestamp` older than 90 min (6× 15-min cadence) | **P2** page |

## SLA targets in code

The probe's `slaTargets` struct mirrors the table at the top of
this doc. Defaults are baked in
(`cmd/ratesengine-sla-probe/main.go::default*Target`); operators
can tune them via flags if their deployment carries a different
contract (e.g. an internal staging environment with looser bars).

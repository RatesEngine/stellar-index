---
title: Runbook — sla-probe-p95-breach
last_verified: 2026-04-30
status: ratified
severity: P2
---

# Runbook — `ratesengine_sla_probe_p95_breach`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_sla_probe_p95_breach` |
| Severity | P2 (page) |
| Detected by | `deploy/monitoring/rules/sla-probe.yml` |
| Typical MTTR | 15–60 min |
| Impact | The Freighter detail page shows degraded responsiveness on the named endpoint. Direct customer-visible SLA breach (RFP target: p95 ≤ 200 ms). |

## Symptoms

- `ratesengine_sla_probe_latency_ms{endpoint=…,quantile="0.95"} > 200`
  for ≥ 30 min (2 timer firings).
- The probe's most-recent JSON report in journald carries
  `failed_reasons: ["<endpoint>: p95=<N>ms > target 200.0ms"]`.
- Direct-API alert `ratesengine_api_latency_p95_high` may also be
  firing — they're complementary signals (probe = synthetic;
  histogram = real traffic).

## Quick diagnosis (≤ 5 min)

```sh
# 1. Get the most-recent probe report.
sudo journalctl -u sla-probe.service -n 1 --output=cat | jq .

# 2. Confirm direct-traffic histograms agree (rules out probe-only artefacts).
curl -s http://prometheus:9090/api/v1/query --data-urlencode \
  'query=histogram_quantile(0.95, sum by (route, le) (rate(http_request_duration_seconds_bucket{job=~"ratesengine[_-]api"}[5m])))' | \
  jq -r '.data.result[] | "\(.metric.route): \(.value[1])s"' | sort -k2 -rn | head

# 3. Run a one-off probe locally to see if it's regional / network.
ratesengine-sla-probe -base-url https://api.ratesengine.net/v1 \
  -duration 10s -concurrency 2 -report-format text
```

## Typical root causes (roughly in frequency order)

Same as `api-latency.md` — the probe and the direct-traffic
histograms see the same backend. The probe just adds:

1. **Probe-host network path issue.** If only the probe is slow but
   the histogram is fine, the issue is between the probe runner and
   the API edge. Confirm by running the probe from a different
   network.
2. **Endpoint-specific slowness** — if `/v1/price` is fine but
   `/v1/oracle/latest` breaches, the cause is on that endpoint's
   path (e.g. SEP-40 contract read latency).
3. Everything from `api-latency.md` ("Typical root causes").

## Mitigation

- [ ] Step 1 — Confirm the breach is real (not probe-host-only) per
      Quick diagnosis #2.
- [ ] Step 2 — If probe-only: investigate the probe host's network
      path; this is a probe issue, not a customer-impact alert.
- [ ] Step 3 — If real: route to `api-latency.md` for the full
      latency-triage flow.
- [ ] Verification: probe p95 returns under 200 ms for 30 min
      (2 consecutive passes — the alert clears with same `for`
      threshold).

## Known false-positive patterns

- **First scrape after a deploy** — fresh process, cold Redis +
  Timescale buffers. The 30-min `for` window absorbs this.
- **Probe host pinned at 100% CPU** during another job. Confirm
  via the probe host's own metrics.

## Related

- `api-latency.md` — the underlying latency-triage flow.
- `sla-probe-stale.md` — when the probe stops running entirely.
- Freighter SLA spec: `docs/freighter-rfp.md` §SLA.

## Changelog

- 2026-04-30 — initial draft alongside #294 (alert rules).

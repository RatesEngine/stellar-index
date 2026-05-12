# W14 — Observability, metrics, alerts, SLA

## Scope

Every metric emitted by runtime code, every Prometheus rule,
every Alertmanager route, every runbook, every Healthchecks.io
ping path, every Loki shipping path, every log field used by
operators.

In scope:
- `internal/obs/*` (log, metrics, http_middleware,
  middleware_unit)
- `deploy/monitoring/rules/*.yml` (18 files)
- `deploy/monitoring/README.md`
- `configs/prometheus/{prometheus.r1.yml,rules.r1/}`
- `configs/alertmanager/{alertmanager.r1.yml,apply.sh}`
- `configs/healthchecks/*` (heartbeat, smoke, sla-probe units +
  scripts)
- `configs/loki/*`
- `docs/operations/alerts-catalog.md`
- every runbook under `docs/operations/runbooks/` (70 files)
- per-binary metric registration (`internal/obs/metrics.go`)

## Inputs

- `inventory/alert-rule-inventory.md`
- `inventory/runbook-inventory.md`
- `inventory/metric-name-inventory.md`
- `evidence/log.md` EV-1207 (smoke script absent)

## Per-alert-rule audit (per `02-protocol.md` §10)

For every alert in the rule files, run the six-check loop:

| Rule file | Alert name | Metric exists | Threshold defensible | Severity routes correctly | Runbook URL valid | Runbook content useful | Has fired in R1? | Status |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| _populate from inventory/alert-rule-inventory.md_ | | | | | | | | |

## Per-runbook content audit

For every runbook under `docs/operations/runbooks/`:

| Runbook | Matches alert? | Diagnostic steps | Dashboard linked | Escalation defined | Postmortem template | Stale? | Status |
| --- | --- | --- | --- | --- | --- | --- | --- |
| _populate from inventory/runbook-inventory.md_ | | | | | | | |

## Metric ↔ rule reconciliation

For every alert expression PromQL, extract metric names; verify
each is registered by some binary:

```sh
grep -RhE 'expr:' deploy/monitoring/rules/*.yml | grep -oE '[a-z][a-z0-9_]+(_total|_seconds|_bytes|_count|_sum)?' | sort -u
```

Then for each, grep `internal/obs/` and per-binary metric
constructors. Missing metric = silent alert = finding.

## Alertmanager routing

Walk `configs/alertmanager/alertmanager.r1.yml`:
- severity labels (page / ticket / informational / deadmansswitch)
- receivers (email, webhook, status-page, etc.)
- deadmansswitch heartbeat (verify a counterpart sender exists)
- silences API enabled?
- secret hygiene (no PagerDuty key in plain text)

## Healthchecks.io path

- `configs/healthchecks/heartbeat.sh` — invocation pattern
- `configs/healthchecks/ratesengine-heartbeat@.{service,timer}` —
  templated unit; instance args
- `configs/healthchecks/smoke.sh` vs `scripts/dev/r1-smoke.sh`
- live R1 reports `/opt/ratesengine/scripts/dev/r1-smoke.sh`
  absent yet `ratesengine-smoke.timer` is firing — find which
  script the .service file actually invokes (EV-1207)
- `hc-ping.com` URL leak grep: zero in repo (verify)

## Loki + Promtail audit

- `configs/loki/*` — chunk store, retention, query timeout
- Promtail process bound on R1 (`ports 9080+38563`) — verify
  scrape targets + labels match operator queries
- log-field hygiene: structured log (`internal/obs/log.go`)
  always emits `binary`, `component`, `request_id`, never
  emits API keys

## SLA probe wiring

- `cmd/ratesengine-sla-probe` (W13) writes textfile →
- node_exporter `textfile_collector` exposes →
- Prometheus scrapes →
- `deploy/monitoring/rules/sla-probe.yml` fires alerts:
  - sla-probe-freshness-breach
  - sla-probe-p95-breach
  - sla-probe-stale
  - sla-probe-unit-failed

For each: verify metric name match probe output.

## Adversarial vectors

- C5.1..C5.5 observability blind spots
- C5.4 alertmanager misconfigured → alerts go to /dev/null
- B6.2 prewarm/handler drift produces cache-miss-rate-high alert

## Cross-workstream dependencies

- W13 owns SLA probe + verify-archive tool side
- W18 owns systemd units + ansible roles for monitoring
- W21 R1 live probe of alert state + textfile output

## Closure criteria

- Every alert row complete
- Every runbook row complete
- Metric ↔ rule reconciliation table complete (no missing metrics)
- Alertmanager routing + deadmansswitch verified
- Healthchecks.io smoke-script mystery resolved (EV-1207)
- SLA probe wiring verified end-to-end

---
title: Runbook — galexie-archive tip-lag
last_verified: 2026-05-20
status: ratified
severity: P1 | P3
---

# Runbook — `ratesengine_galexie_archive_tip_lag_*`

## At a glance

| Field | Value |
| ----- | ----- |
| Alerts | `ratesengine_galexie_archive_tip_lag_high` (P3, warn) · `ratesengine_galexie_archive_tip_lag_severe` (P1, page) · `ratesengine_galexie_archive_tip_lag_metric_stale` (P3) |
| Severity | P3 → P1 escalation |
| Detected by | Prometheus rules in `deploy/monitoring/rules/galexie-archive.yml` + `configs/prometheus/rules.r1/galexie-archive.yml` |
| Metric source | `node_exporter` textfile_collector reads `/var/lib/node_exporter/textfile_collector/galexie_archive_tip_lag.prom`, refreshed every 5 min by `galexie-archive-tip-lag.timer` → `/usr/local/bin/galexie-archive-tip-lag` |
| Steady-state | <12,000 ledgers lag (≈ one hourly fill cycle at mainnet ~3.3 s/ledger) |
| Customer impact | None while alerting — live ingest unaffected; durable upstream `aws-public-blockchain` backstops the data. The R1 full-mirror integrity-leader role degrades. |
| Companions | [archive-files-missing](archive-files-missing.md), [bootstrap-archival-node](bootstrap-archival-node.md) |

## Why this exists

The original `#26` incident was a **23-day silent stall** of
`galexie-archive` (the ADR-0016 R1 durable full-mirror). The
`galexie-archive-fill` catch-up script existed but only ran
manually — when its manual invocation drifted into oblivion,
nothing surfaced for weeks. The post-`#26` standing fix is the
hourly `galexie-archive-fill.timer`. **This alert is the safety
net under that:** if the timer itself (or its `mc` aliases, the
aws-public IAM, or a MinIO mtime-poison deadlock per the "mc mirror
gotcha") breaks silently, the lag will start growing and Prometheus
pages within hours — instead of the 23-day blindness `#26` was.

## Quick diagnosis

```sh
# 1. Is the catch-up timer actively running on its hourly cadence?
ssh r1 'systemctl list-timers galexie-archive-fill.timer galexie-archive-tip-lag.timer'
ssh r1 'systemctl status galexie-archive-fill.service'           # last run, exit code
ssh r1 'systemctl show galexie-archive-fill.service -p Result'

# 2. Did the most recent fill find no missing partitions (the
#    expected steady state)?
ssh r1 'tail -30 /var/log/galexie-mirror.log'                    # "needs work (missing): 0" = healthy

# 3. Is the textfile metric itself stale? (metric_stale alert variant)
ssh r1 'ls -la /var/lib/node_exporter/textfile_collector/galexie_archive_tip_lag.prom'
ssh r1 'cat /var/lib/node_exporter/textfile_collector/galexie_archive_tip_lag.prom'

# 4. Manually compute the gap (in case the script itself misreports):
ssh r1 'mc ls local/galexie-live | head -1; mc ls local/galexie-archive | head -1'
```

## Triage tree

- **`metric_stale` only** (lag value missing/old): the updater
  script broke. Read `journalctl -u galexie-archive-tip-lag.service -n 50` — usually mc-alias misconfig, permissions, or MinIO down. The metric file is the canary; restore it first so the other alerts can fire honestly.
- **`tip_lag_high` (>5,000)**: the hourly fill is running but
  slowly catching up. Check `mc admin info local` for MinIO load,
  and AWS public-blockchain bucket latency. Tolerate 1-2 cycles
  before escalating.
- **`tip_lag_severe` (>50,000 ≈ 3.5+ days)**: the fill timer has
  clearly broken — same failure class as #26. Run
  `/usr/local/bin/galexie-archive-fill` manually to see the live
  error, then root-cause the systemd timer/service. Page severity:
  R1's full-mirror integrity guarantee is broken; backfills /
  WASM-walks past the gap will fail.

## Remediation

```sh
# Manual catch-up (idempotent, the same script the timer runs):
ssh r1 'sudo /usr/local/bin/galexie-archive-fill'

# Force the tip-lag updater to re-read after manual remediation:
ssh r1 'sudo systemctl start galexie-archive-tip-lag.service'

# Re-enable the recurring timer if it was disabled:
ssh r1 'sudo systemctl enable --now galexie-archive-fill.timer galexie-archive-tip-lag.timer'
```

If `galexie-archive-fill` itself fails, follow its log to
`/var/log/galexie-mirror.log` — common causes: bucket permission
denied (rotate the `mc alias` creds), aws-public listing 503s
(retry in an hour), MinIO local out of space (check `zpool free`).

## Related

- ADR-0016 — per-region storage strategies (defines R1 = full mirror).
- `#26` — the originating 23-day silent-stall incident.
- `#7` — LCM-cache tiering (longer-term capacity strategy).
- [archive-files-missing](archive-files-missing.md) — sibling Tier-A/B archive integrity alert.
- `galexie-archive-fill.{service,timer}` (`deploy/systemd/`).

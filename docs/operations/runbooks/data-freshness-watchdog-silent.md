---
title: Runbook — data-freshness watchdog silent
last_verified: 2026-06-30
status: ratified
severity: P3
---

# Runbook — `stellarindex_data_freshness_watchdog_silent`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `stellarindex_data_freshness_watchdog_silent` |
| Severity | **P3** (ticket) |
| Detected by | `absent_over_time(stellarindex_data_freshness_stale[45m])` for > 15m |
| Emitted by | the absence of `data-freshness.sh`'s textfile output |
| Typical MTTR | 5–15 min |
| Impact | The watchdog that backstops every other source's staleness (coingecko, sep1, the completeness verdict) is itself blind — drift could now go unnoticed. Meta-alert. |

## Symptoms

The `stellarindex_data_freshness_*` series stop updating / go absent. The timer
runs every 15 min; absence > 45 min means it failed to fire or the script errors.

## Quick diagnosis (≤ 5 min)

```sh
systemctl status data-freshness.service data-freshness.timer
journalctl -u data-freshness.service --since '1 hour ago' | tail -30
ls -la --time-style=+%H:%M /var/lib/node_exporter/textfile_collector/data_freshness.prom
# Run it by hand to see the error:
/usr/local/sbin/data-freshness.sh
```

## Mitigation (≤ 15 min)

- **Timer disabled/not loaded:** `systemctl enable --now data-freshness.timer`.
- **Script errors (psql/DSN):** the script sources `/etc/default/stellarindex`
  for `STELLARINDEX_POSTGRES_DSN`; a Postgres outage or DSN drift breaks it —
  fix the DSN/DB, re-run `systemctl start data-freshness.service`.
- **Textfile unreadable (0600):** node_exporter is unprivileged; the script
  chmods the file 0644 before the atomic swap — if a stale 0600 file lingers,
  `chmod 0644` it.

## Root cause analysis

A oneshot timer/script that stopped: timer not enabled after a rebuild, a DB
outage failing the query under `set -e`, or a permissions regression on the
textfile.

## Known false-positive patterns

- A node_exporter restart briefly drops textfile metrics until the next scrape —
  the `for: 15m` absorbs that.

## Related

- `stellarindex_data_source_stale`, `stellarindex_completeness_incomplete` — the alerts this watchdog feeds.
- `stellarindex_ingest_gap_detector_silent` — the analogous meta-alert for the gap detector.

## Changelog

- 2026-06-30: created with the data-freshness watchdog.

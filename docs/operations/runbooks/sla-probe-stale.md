---
title: Runbook — sla-probe-stale
last_verified: 2026-04-30
status: ratified
severity: P2
---

# Runbook — `ratesengine_sla_probe_stale`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_sla_probe_stale` |
| Severity | P2 (page) |
| Detected by | `deploy/monitoring/rules/sla-probe.yml` |
| Typical MTTR | 15 min |
| Impact | We've lost the SLA-evidence trail required by the Freighter RFP. The API itself may be fine — this alert says we can't *prove* it. |

## Symptoms

- `(time() - ratesengine_sla_probe_last_pass_timestamp) > 90 * 60`
  for ≥ 5 min.
- Either the systemd timer isn't running, or every recent run has
  failed (which would also fire `_unit_failed_alert`).

## Quick diagnosis (≤ 5 min)

```sh
# 1. Is the timer scheduled?
sudo systemctl status sla-probe.timer
sudo systemctl list-timers sla-probe.timer

# 2. When did the unit last run?
sudo journalctl -u sla-probe.service --since "2 hours ago" -n 50

# 3. Is the textfile being written?
ls -la /var/lib/node_exporter/textfile_collector/sla_probe.prom

# 4. Force a one-off run.
sudo systemctl start sla-probe.service
sudo journalctl -u sla-probe.service -n 1 --output=cat | jq .
```

## Typical root causes

1. **Timer disabled** — operator ran `systemctl stop sla-probe.timer`
   for maintenance and forgot to re-enable. `systemctl status` shows
   `inactive`.
   - Mitigation: `sudo systemctl enable --now sla-probe.timer`.

2. **Service unit failing every run** — fires alongside this alert
   in journald.
   - Mitigation: check the journald entries; route to the
     `_unit_failed_alert` runbook.

3. **node_exporter not scraping the textfile_collector dir** — the
   probe runs and writes the file, but Prometheus never sees the
   metric, so the gauge stays at "no last_pass_timestamp ever set."
   - Signal: the file exists with a fresh mtime, but
     `last_pass_timestamp` gauge has no samples.
   - Mitigation: confirm node_exporter's
     `--collector.textfile.directory` flag points at the right path.

4. **TEXTFILE_OUTPUT environment variable unset** — operators who
   skipped the `/etc/default/sla-probe` config don't write the
   textfile, so node_exporter never sees the metric.
   - Signal: file doesn't exist at all.
   - Mitigation: add `TEXTFILE_OUTPUT=/var/lib/node_exporter/...`
     to `/etc/default/sla-probe`; reload the service.

## Mitigation

- [ ] Step 1 — Walk the diagnostic commands; identify which stage
      is silent.
- [ ] Step 2 — Apply the matching fix from "Typical root causes."
- [ ] Step 3 — Force a probe run via
      `sudo systemctl start sla-probe.service` and confirm
      `last_pass_timestamp` updates.
- [ ] Verification: alert clears within 5 min after a successful
      probe run lands in node_exporter.

## Known false-positive patterns

- **Fresh deploy** of the probe — the gauge has never been set, so
  `time() - 0` is a huge number. Filter the alert with `for: 5m`
  (already in place) plus `unless on(instance) ratesengine_sla_probe_unit_failed`
  if this becomes a recurring deploy-time false positive.

## Related

- `sla-probe-unit-failed.md` — when the probe is running but
  failing.
- `sla-probe-p95-breach.md` / `sla-probe-freshness-breach.md` —
  specific failure modes.

## Changelog

- 2026-04-30 — initial draft alongside #294 (alert rules).

---
title: Runbook — supply-snapshot-stale
last_verified: 2026-04-30
status: ratified
severity: P3
---

# Runbook — `ratesengine_supply_snapshot_stale` / `_critical_stale`

## At a glance

| Field | Value |
| ----- | ----- |
| Alerts | `ratesengine_supply_snapshot_stale` (P3, > 36 h), `ratesengine_supply_snapshot_critical_stale` (P2, > 72 h) |
| Detected by | `deploy/monitoring/rules/supply-snapshot.yml` |
| Typical MTTR | 15 min |
| Impact | `/v1/assets/{id}` F2 fields visibly old. After ≥ 36 h the displayed `observed_at` is more than a day behind chain state. |

## Symptoms

- `(time() - ratesengine_supply_snapshot_last_success_timestamp{asset_key=…}) > 36*3600`
  for ≥ 5 min.
- Two-tier: 36 h is the standard heartbeat budget (24 h cron + 12 h
  cushion); 72 h is the page-the-on-call line.

## Quick diagnosis (≤ 5 min)

```sh
# 1. Is the timer scheduled?
sudo systemctl status supply-snapshot.timer
sudo systemctl list-timers supply-snapshot.timer

# 2. When did the unit last run?
sudo journalctl -u supply-snapshot.service --since "3 days ago" -n 50

# 3. Is the textfile being written?
ls -la /var/lib/node_exporter/textfile_collector/supply_snapshot.prom

# 4. Force a one-off run.
sudo systemctl start supply-snapshot.service
```

## Typical root causes

1. **Timer disabled.** Operator ran `systemctl stop supply-snapshot.timer`
   for maintenance and forgot to re-enable.
   - Mitigation: `sudo systemctl enable --now supply-snapshot.timer`.

2. **Service unit failing every run.** Fires alongside the
   `_unit_failed_alert`.
   - Mitigation: route to `supply-snapshot-unit-failed.md`.

3. **`TEXTFILE_OUTPUT` env-var unset.** Operator never enabled the
   textfile path, so `last_success_timestamp` has no value to track.
   The runbook for `_unit_failed` has the mitigation; the staleness
   alert here is a downstream symptom.

4. **Clock skew.** If the host clock jumped backwards, a recently-
   passed run has a timestamp that looks ancient relative to `time()`.
   - Signal: `node_time_seconds` deviates from real time.
   - Mitigation: investigate ntp; clear by running the writer once
     with a fresh clock.

## Mitigation

- [ ] Step 1 — Identify which stage is silent (timer / unit / textfile).
- [ ] Step 2 — Apply the matching root-cause fix.
- [ ] Step 3 — Force a run: `sudo systemctl start supply-snapshot.service`.
- [ ] Verification: `last_success_timestamp` updates within 60 s
      after a successful run lands in node_exporter.

## Known false-positive patterns

- **Fresh deploy** of the supply-snapshot writer — the gauge has
  never been set. The `for: 5m` window absorbs this only if a
  successful run has happened recently. On a brand-new box, the
  alert is correct (no successful run exists).

## Related

- `supply-snapshot-unit-failed.md` — when runs are failing.
- `archive-completeness-stale.md` — same shape on the archive side.

## Changelog

- 2026-04-30 — initial draft alongside #295 (textfile + alerts).

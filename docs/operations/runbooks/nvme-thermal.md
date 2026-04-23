---
title: Runbook — nvme-thermal
last_verified: 2026-04-23
status: draft
severity: P2
---

# Runbook — `ratesengine_nvme_thermal_throttle`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_nvme_thermal_throttle` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/infra.yml` |
| Typical MTTR | 15 min – days (airflow fix vs waiting for cooler weather) |
| Impact | NVMe IO throttles to ~50 % of rated when over thermal limit. Postgres WAL flush slows, captive-core catchup slows, aggregator write throughput drops. Sustained thermal stress also accelerates drive wear — not immediate but not free. |

## Symptoms

- `node_nvme_temperature_celsius > 70` sustained 5 min on some device.
- Write IOPS / throughput drop visibly when temperature crosses
  threshold (compare IO panels with temperature panel).
- Latency alerts may follow (`api-latency.md`,
  `replica-lag.md`) if the throttle is heavy.

## Quick diagnosis (≤ 5 min)

```sh
# Per-drive temps
ssh <host> 'for d in /dev/nvme?n1; do
  echo -n "$d: "; smartctl -A "$d" | grep -i temperature
done'

# Is it all drives (chassis issue) or one (drive issue)?
# If all, check ambient temperature + fans.
ssh <host> 'sensors | grep -E "Composite|fan"'

# IPMI fan speeds
ssh <host> 'ipmitool sdr list | grep -iE "fan|temp"'
```

## Typical root causes

1. **Chassis airflow issue.** Clogged intake filter, failed fan,
   misrouted cables blocking flow. Usually affects multiple drives
   simultaneously.

2. **Ambient datacentre temperature.** Summer, HVAC issue, door
   propped open. All drives on affected hosts climb together.
   Check neighbouring hosts' temperatures.

3. **Single drive issue.** Heatsink came loose, drive in a
   poorly-ventilated slot. Only one drive on a host climbs while
   neighbours stay cool.

4. **Sustained heavy workload** — scrub, resilver, backup, mass
   compaction. Expected during these operations; the throttle
   does its job.

## Mitigation

- [ ] Step 1 — is it all drives on a host, all hosts in a rack,
      or one drive? That identifies the scale.
- [ ] Step 2 — immediate: reduce the workload. Pause pgBackRest,
      cancel any running scrubs, let the drive cool.
- [ ] Step 3 — medium-term: remote-hands to check fans / airflow;
      replace failed fans; clean dust filters.
- [ ] Step 4 — if ambient is the cause: escalate to colo's NOC;
      nothing you can do at 3 AM to change a DC's cooling.
- [ ] Verification: temperature drops below 65 °C sustained for
      15 min; write throughput returns to baseline.

## Known false-positive patterns

- **Brief spikes during scrub / resilver** — expected, should
  stay under threshold if the chassis is cooled correctly.
- **Sensor reporting glitches** — a single drive showing 85 °C
  while its neighbours are 45 °C can be a sensor read error.
  `smartctl -x` shows historical temperatures; cross-check.

## Related

- `nvme-smart.md` — thermal stress accelerates the SMART wear
  indicators.
- `replica-lag.md`, `compression-lag.md` — downstream effects
  when thermal throttle slows writes.

## Changelog

- 2026-04-23 — initial draft.

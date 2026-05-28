---
title: Runbook — ratesengine_ingest_gap_detector_silent
last_verified: 2026-05-28
status: ratified
severity: P2
---

# Runbook — `ratesengine_ingest_gap_detector_silent`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ingest_gap_detector_silent` |
| Severity | P2 (ticket) |
| Detected by | `rate(ratesengine_ingest_gap_detector_runs_total{outcome="ok"}[15m]) == 0` OR series absent for 15 min |
| Typical MTTR | 15 min (restart) — 1 h (deeper Postgres issue) |
| Impact | The data-gap detector goroutine is wedged. `ratesengine_ingest_gap_max_size_ledgers` gauges read stale value; the paging `ingest_gap_detected` alert can't fire even if a real gap forms. The system has lost its data-derived ingest-health signal. |

## Symptoms

- `ratesengine_ingest_gap_detector_runs_total{outcome="ok"}` rate is zero (or series absent).
- Operators reading the dashboard see the gap-size gauge frozen on its last-known value.
- May coincide with `ratesengine_aggregator_silent` (aggregator binary is down) or `ratesengine_postgres_exporter_down` (Postgres is unreachable).

## Triage — 5 minutes

1. **Aggregator service healthy?**

   ```sh
   ssh root@<region-host> 'systemctl status ratesengine-aggregator | head'
   ```

   If inactive or crash-looping, that's the root cause — fix the aggregator first (`journalctl -u ratesengine-aggregator -n 200`).

2. **Postgres reachable?**

   ```sh
   ssh root@<region-host> 'sudo -iu postgres pg_isready'
   ```

   If not, the detector's 60s scan timeout is firing every cycle and incrementing `outcome=error` instead. Cross-check `ratesengine_postgres_exporter_down`.

3. **Connection pool saturated?**

   ```sh
   ssh root@<region-host> "sudo -iu postgres psql -d ratesengine -c 'SELECT count(*), state FROM pg_stat_activity GROUP BY state;'"
   ```

   `active` count near `max_connections` means the detector can't get a connection. Likely caused by concurrent fill walks per F-0020; see `docs/operations/backfill-with-live-ingest.md` for the recommended posture.

## Remediation

### Aggregator down

```sh
ssh root@<region-host> 'systemctl restart ratesengine-aggregator'
ssh root@<region-host> 'journalctl -u ratesengine-aggregator -f'
```

The detector starts immediately on aggregator boot (first scan ~3 s post-startup), so the gauge refreshes and the alert clears within ~5 min.

### Postgres degraded

Defer the detector restart until Postgres is healthy. Once `pg_isready` returns clean, the detector recovers on its own cycle (no aggregator restart needed unless the goroutine has fully exited — check the aggregator log for `gap-detector` warnings).

### Pool saturation

Reduce concurrent walk parallelism per `docs/operations/backfill-with-live-ingest.md`:

```sh
ssh root@<region-host> 'systemctl stop soroban-events-fill.service'
# Wait for connection-count to drop, then resume at -parallel 4 instead of -parallel 12.
```

## Known false-positive patterns

- **Fresh deploy bootstrap.** Detector first scan takes 3-5 s. The first 15-min window after restart MAY tick `outcome=ok` only 2-3 times before alert evaluation; the alert won't fire because >0 ticks satisfy the rate expr. If it does fire transiently during a slow boot, the alert clears within one more cycle.
- **Operator-triggered aggregator restart.** Same shape as fresh deploy. Treat as routine if the restart was intentional.

## Related

- `ingest-gap-detected.md` — the paging alert this meta-alert protects from going silent.
- `aggregator-silent.md` — sibling meta-alert for the aggregator binary itself.
- `docs/operations/backfill-with-live-ingest.md` — F-0020 posture for managing Postgres pool pressure.

## Changelog

- 2026-05-28 — initial draft alongside the gap detector worker ship.

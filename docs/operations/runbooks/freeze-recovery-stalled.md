---
title: Runbook — freeze-recovery-stalled
last_verified: 2026-05-12
status: draft
severity: P3
---

# Runbook — `ratesengine_anomaly_freeze_recovery_stalled`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `FreezeRecoveryStalled` (P3) |
| Detected by | Prometheus rule in `deploy/monitoring/rules/anomaly.yml` (engaged > recovered + open-window for ≥ 2 h) |
| Typical MTTR | 5–15 min once the cause is found |
| Impact | Resolved freezes still appear "firing" in `freeze_events`; the explorer `/anomalies` timeline misrepresents resolved incidents as ongoing. The API itself is unaffected — `flags.frozen` is driven by the Redis marker, not the durable mirror. |

## What "stalled" means

The aggregator runs two sides of the freeze pipeline:

1. **Writer side** (`internal/aggregate/freeze.Writer.Mark`) — fires
   on every `ActionFreeze` decision. Writes a Redis marker (TTL =
   `cachekeys.FreezeTTL`, currently a few minutes) and INSERTs a
   row into `freeze_events` with `recovered_at = NULL`.
2. **Recovery side** (`internal/aggregate/freeze.Recovery`) — every
   60 s, lists every open `freeze_events` row, checks whether the
   Redis marker for that pair still exists, and stamps
   `recovered_at = now()` when the marker is gone.

The recovery worker is what closes durable rows once the underlying
anomaly clears (the orchestrator stops refreshing the marker, the
TTL elapses, the recovery worker notices). When recovery stalls,
`ratesengine_anomaly_freeze_engaged_total` keeps incrementing but
`ratesengine_anomaly_freeze_recovered_total` flatlines, and the
backlog of open rows in `freeze_events` grows.

## Symptoms

- `rate(ratesengine_anomaly_freeze_engaged_total[1h])` >
  `rate(ratesengine_anomaly_freeze_recovered_total[1h])` for ≥ 2 h
- `count by () (max_over_time(ratesengine_anomaly_freeze_recovery_sweeps_total{outcome="error"}[15m]))`
  is non-zero for the same window
- `SELECT count(*) FROM freeze_events WHERE recovered_at IS NULL`
  on r1 postgres returns a large number (steady-state should be
  ≤ the count of currently-frozen pairs visible in Redis)
- Aggregator logs include `recovery: ...` warnings every 60 s

## Quick diagnosis (≤ 5 min)

```sh
# 1) Is the recovery worker even running?
ssh r1 'journalctl -u ratesengine-aggregator --since "30 min ago" \
  | grep -i "freeze-recovery"'
# Expected: periodic Debug "recovery sweep complete" lines.
# Absent: the goroutine never started — restart aggregator.

# 2) Is it failing on the lister side (postgres) or the cache side (Redis)?
ssh r1 'curl -s http://localhost:9091/metrics \
  | grep "ratesengine_anomaly_freeze_recovery_sweeps_total"'
# outcome="error" → lister path or all-Redis is failing
# outcome="partial" → MarkRecovered postgres write is failing per-row

# 3) Confirm the open-row backlog directly:
ssh r1 'sudo -u postgres psql ratesengine -c \
  "SELECT count(*), min(frozen_at) FROM freeze_events WHERE recovered_at IS NULL;"'
```

Decision tree:

| `outcome="error"` rising | `outcome="partial"` rising | Probable cause | Action |
|---|---|---|---|
| Yes | No | Postgres lister query failing OR Redis transport broken | Check postgres logs + Redis health |
| No | Yes | `MarkRecovered` UPDATE failing for some rows | Check postgres logs for `freeze_events` UPDATE errors |
| No | No, but backlog still growing | The Redis marker is being refreshed by the orchestrator (legitimate — anomaly genuinely persists) | Verify with `redis-cli --scan --pattern 'freeze:*'` — count should equal open-row count |
| No | No, backlog flat near 0 | Alert is a false positive — the recovery worker is healthy | Tune the alert threshold |

## Mitigation (≤ 15 min)

- [ ] **Step 1 — Confirm whether the underlying freezes are real.**
  If the open rows match live Redis markers, the worker is doing
  the right thing and the anomalies legitimately persist. Switch to
  the [anomaly-freeze-engaged](anomaly-freeze-engaged.md) +
  [anomaly-freeze-sustained](anomaly-freeze-sustained.md) runbooks
  and investigate the underlying market events.

- [ ] **Step 2 — If postgres-side error:** check
  `journalctl -u postgresql --since "30 min ago"`. The most common
  causes are connection-pool exhaustion (recovery worker uses the
  shared `*sql.DB` from the aggregator's store) or a long-running
  ANALYZE/VACUUM blocking the UPDATE. Restart the aggregator if
  the pool is wedged.

- [ ] **Step 3 — If Redis-side error:** check
  `redis-cli ping` and the `ratesengine-redis` systemd unit. If
  Redis is up but the recovery sweep still fails its GETs, suspect
  ACL changes (the recovery worker uses the same client as the
  freeze writer — if one works the other should too).

- [ ] **Step 4 — One-shot manual sweep** if the backlog needs to
  clear immediately and the recovery worker remains broken:

  ```sh
  ssh r1 'sudo -u postgres psql ratesengine'
  -- Close every open row whose Redis marker is gone:
  -- (Adjust the cutoff to "2× FreezeTTL" — anything older than
  -- this can't possibly still have a marker.)
  UPDATE freeze_events
     SET recovered_at = now(),
         recovered_at_ledger = 0
   WHERE recovered_at IS NULL
     AND frozen_at < now() - interval '15 minutes';
  ```

  **Caveat:** this assumes `2 × FreezeTTL` exceeds any TTL refresh
  the orchestrator might apply. Verify the current `FreezeTTL`
  before running.

- [ ] **Verification:** `ratesengine_anomaly_freeze_recovered_total`
  resumes climbing on the next sweep tick (within 60 s), and the
  open-row count in postgres trends back down toward the count of
  live Redis markers.

## Root cause analysis

For the postmortem, capture:

- The duration of the stall and the maximum open-row backlog
- Whether the underlying transport was postgres or Redis (or
  whether the goroutine itself wasn't running)
- If the goroutine wasn't running: did the aggregator restart and
  miss wiring it up? (`freezeRecovery` block in
  `cmd/ratesengine-aggregator/main.go`.)
- Did the explorer `/anomalies` timeline visibly diverge from
  reality during the stall? Customer impact?

## Related

- [anomaly-freeze-engaged](anomaly-freeze-engaged.md) — the
  upstream runbook for the freeze itself.
- [anomaly-freeze-sustained](anomaly-freeze-sustained.md) — when a
  legitimate freeze persists past its expected window.
- [ADR-0019](../../adr/0019-anomaly-response-and-confidence-scoring.md) —
  the policy this runbook serves.

## Changelog

- 2026-05-12 — initial draft alongside the recovery worker (F-1229).

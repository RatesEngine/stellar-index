---
title: Runbook — ratesengine_ingest_gap_detected
last_verified: 2026-05-28
status: ratified
severity: P1
---

# Runbook — `ratesengine_ingest_gap_detected`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ingest_gap_detected` |
| Severity | P1 (page) |
| Detected by | `ratesengine_ingest_gap_max_size_ledgers{source} > 1000` for 15 min |
| Typical MTTR | 30 min — 4 h (depending on gap size and bucket reachability) |
| Impact | A contiguous block of soroban_events ingest is missing. Per-decoder coverage for every Soroban source is incomplete across the gap window; price-history queries spanning the gap return holes. |

## Symptoms

- Prometheus alert `ratesengine_ingest_gap_detected{source="soroban-events"}` is firing.
- `ratesengine_ingest_gap_max_size_ledgers{source="soroban-events"}` reports the size of the largest contiguous gap.
- The status page's per-source density may still show 100% — cursor-derived density measures process state, this alert measures data state.

## Triage — 5 minutes

1. **Get the exact gap list:**

   ```sh
   ssh root@<region-host>
   ratesengine-ops find-data-gaps --config /etc/ratesengine.toml --output text
   ```

   Output prints each `[from, to]` range + a ready-to-paste `ratesengine-ops backfill` command per gap.

2. **Classify the gap pattern:**
   - **One large contiguous gap (>50 K ledgers)** → cascade signature. Suspect ingest halt (Redis MISCONF, Postgres back-pressure, AsyncSink wedge). Cross-check F-0020-cluster alerts (`ratesengine_redis_writes_blocked`, `ratesengine_postgres_connections_high`).
   - **Many small gaps (each ~100-500 ledgers)** → flaky-write pattern. Suspect MinIO blip, transient ledgerstream reconnect, or a partial-batch sink failure.
   - **Single small gap at the trailing edge** → likely active backfill or a brief Postgres pause; re-check in 5 min before acting.

3. **Confirm the source is actively ingesting NOW:**

   ```sh
   ssh root@<region-host> 'sudo -iu postgres psql -d ratesengine -c "SELECT MAX(ledger_close_time), MAX(ledger) FROM soroban_events;"'
   ```

   If `MAX(ledger_close_time)` is fresh (within ~30 s) the writer is healthy and the gap is historic; if stale, the writer is still wedged and the gap is growing.

## Remediation

### Healthy writer + historic gap

Run the targeted backfill commands the diagnostic emitted:

```sh
ratesengine-ops backfill --config /etc/ratesengine.toml \
  --from <gap.start> --to <gap.end> --source soroban-events
```

One invocation per gap. Each ~92 K ledger gap takes 15-30 min on r1. Confirm the gauge drops by re-running `find-data-gaps` or watching `ratesengine_ingest_gap_max_size_ledgers` decay.

### Wedged writer + growing gap

This is the F-0020 cascade pattern. Pause heavy walks (any running `ratesengine-ops backfill -source soroban-events` invocation — `pkill -INT -f 'ratesengine-ops backfill'`; `verify-archive-tier-a.service`) per `docs/operations/backfill-with-live-ingest.md`, then:

1. Check Redis (`redis-cli info persistence` — `rdb_last_bgsave_status: ok`?).
2. Check Postgres (`SELECT count(*) FROM pg_stat_activity;` — saturated?).
3. Restart the indexer (`systemctl restart ratesengine-indexer`).
4. Watch the live cursor advance via `/v1/diagnostics/cursors`.
5. Once live ingest is recovered, schedule the historic-gap backfill above.

## Known false-positive patterns

- **First boot after rc.84+ deploy.** The detector runs immediately on startup so the gauge is non-empty before the first 5-min cycle; if a historic gap is preserved from before deploy the alert fires within 15 min. Resolve via the standard targeted-backfill path.
- **Genuinely-empty mainnet window.** Soroban activity dipped briefly below the `min-gap-size=1000` threshold (~1.5 h of zero contracts). Vanishingly rare on mainnet post-2024 but possible during testnet experiments — lower the threshold flag if your network is quieter.

## Related

- [projector-replay.md](projector-replay.md) — per-source projection-table repair via projector cursor rewind. Replaces the former `cascade-window-drain` orchestrator subcommand (ADR-0032 Phase 5).
- `docs/operations/backfill-with-live-ingest.md` — operational posture for running backfills alongside live ingest (F-0020 closure).
- F-0020 (audit-2026-05-26) — original cascade-window incident that motivated this detector.
- `ratesengine-ops find-data-gaps` — the operator-facing diagnostic this alert points at.
- `ingest-gap-detector-silent.md` — paired ticket-tier alert for when the detector itself wedges.

## Changelog

- 2026-05-28 — initial draft alongside the gap detector worker ship.

# Runbook: SDEX data-coverage gap detected

**Trigger:** `ratesengine_ingest_gap_max_size_ledgers{source="sdex"} > 1000` sustained 15 min.

This is the SDEX-specific surface of [ingest-gap-detected](ingest-gap-detected.md). SDEX is classic-DEX and does NOT flow through `soroban_events`; its rows land in the unified `trades` hypertable filtered by `source = 'sdex'`. Symmetric to the Soroban path, an SDEX-side cascade (Postgres back-pressure halting the SDEX writer goroutine while the rest of ingest stays healthy) used to be invisible at the data layer. This alert closes that gap.

## Triage (5 min)

1. **Confirm the signal.** Two quick checks:
   ```
   ssh r1
   curl -s localhost:9465/metrics | grep 'ratesengine_ingest_gap.*sdex'
   ratesengine-ops find-data-gaps --config /etc/ratesengine.toml --source sdex
   ```
   Both should agree on the gap inventory.

2. **Is the SDEX writer alive?**
   ```
   ssh r1 'journalctl -u ratesengine-indexer --since "30 min ago" | grep -E "sdex|source=sdex" | tail -50'
   ```
   Healthy = one batch-write log line every ~5s. Silent = goroutine wedged.

3. **Is the gap forming or static?**
   ```
   curl -s localhost:9465/metrics | grep 'ratesengine_ingest_gap_max_size_ledgers{source="sdex"}'
   sleep 60
   curl -s localhost:9465/metrics | grep 'ratesengine_ingest_gap_max_size_ledgers{source="sdex"}'
   ```
   Same value = static (incident already over; backfill needed). Growing = active outage (the writer goroutine is still down).

## Common shapes

- **Active writer halt (cascade-style).** The classic SDEX writer is paused; current tip's `last_insert_at` metric is stale. Investigate the cascade root cause first (Redis MISCONF, Postgres pool exhaustion, disk pressure). Restarting the indexer without addressing the root cause will deadlock again within minutes.
- **Historic legitimate-quiet window (false positive).** Pre-Soroban-era stretches of SDEX history have genuine low-activity periods. If the gap is in `[ledger < 50457424]` (pre-Soroban) and the gap size is <10K ledgers, consider raising the `min-gap-size` threshold for the SDEX target — file a follow-up to make this configurable per target.
- **Network outage during an upgrade.** Mainnet halts (e.g. a chain upgrade gone wrong) leave a real ledger gap but it's chain-wide, not SDEX-specific. The Soroban target should show a similarly-shaped gap. If only SDEX is short, it's an ingest-side issue.

## Remediation

Targeted SDEX backfill (re-walks MinIO via the dispatcher):

```
ratesengine-ops backfill \
  --config /etc/ratesengine.toml \
  --from $GAP_START --to $GAP_END \
  --source sdex \
  --parallel 8
```

Idempotent via the `trades` PK (`(source, ledger, tx_hash, op_index, ts)`). Re-runs over already-covered range are no-ops.

Verify the gauge decays on the next 30-min detector cycle:

```
curl -s localhost:9465/metrics | grep 'ratesengine_ingest_gap_max_size_ledgers{source="sdex"}'
# expect: 0 (or below your threshold if the historic legitimate-quiet caveat applies)
```

## Why no `sdex-backfill` subcommand?

Soroban-era sources have dedicated `*-backfill` subcommands because they can stream from the `soroban_events` landing zone (no MinIO re-walk needed). SDEX has no equivalent landing zone — the classic-DEX ingest path writes straight to `trades`. Repair requires re-decoding from MinIO via the binary `backfill --source sdex`, which is the existing tool.

## Related

- [ingest-gap-detected.md](ingest-gap-detected.md) — the parent alert (matches any `source=` label)
- [cascade-window-drain.md](cascade-window-drain.md) — Soroban equivalent for the seven per-source classifier tables
- ADR-0030 — per-source coverage invariant; SDEX target is the canonical example of a non-Soroban source registered in the same scheme

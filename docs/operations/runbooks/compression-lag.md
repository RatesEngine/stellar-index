---
title: Runbook — compression-lag
last_verified: 2026-04-23
status: draft
severity: P3
---

# Runbook — `ratesengine_timescale_compression_lag`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_timescale_compression_lag` |
| Severity | P3 (informational) |
| Detected by | `deploy/monitoring/rules/storage.yml` |
| Typical MTTR | 1 h – 1 day |
| Impact | Not customer-visible directly. But uncompressed chunks use 5–20× more disk than compressed, so sustained lag is a runway to `db-disk-full.md`. The alert's `for: 24h` threshold makes it a trending problem, not an incident. |

## Symptoms

- `ratesengine_uncompressed_chunks_older_than_7d > 0` sustained
  24 h.
- Disk usage growing faster than expected.
- `SELECT * FROM timescaledb_information.jobs WHERE proc_name =
  'policy_compression'` shows failures or skipped runs.

## Quick diagnosis (≤ 5 min)

```sh
# Which chunks are overdue?
psql -c "SELECT hypertable_name,
                chunk_name,
                range_start, range_end,
                is_compressed
         FROM timescaledb_information.chunks
         WHERE NOT is_compressed
           AND range_end < now() - interval '7 days'
         ORDER BY range_end
         LIMIT 20;"

# Why is the job failing?
psql -c "SELECT * FROM timescaledb_information.job_stats
         WHERE job_id IN (SELECT job_id FROM timescaledb_information.jobs
                          WHERE proc_name = 'policy_compression');"

# Manual compression — does it work?
psql -c "SELECT compress_chunk('<chunk_name>');"
```

## Typical root causes

1. **Compression job hitting a lock** with insert traffic. A
   hot chunk gets new rows while the job tries to compress it.
   - Mitigation: widen the `compress_after` interval so only
     truly-cold chunks are touched.

2. **Schema change conflicts** — a `ALTER TABLE` on the hypertable
   invalidates pending compression. TimescaleDB's compression is
   sensitive to schema.
   - Mitigation: complete the ALTER; may need to uncompress then
     recompress affected chunks.

3. **Disk IO saturated** — compression is IO-heavy. If the
   primary is close to IO limits, compression gets queued out.
   - Mitigation: scale IO (better disk, more parallelism caps).

4. **Job scheduler wedged** (same root cause as
   `cagg-stale.md`'s scheduler issue).

## Mitigation

- [ ] Step 1 — confirm which chunks are stuck + why (above).
- [ ] Step 2 — try manual compression on one chunk to reproduce
      the error in isolation.
- [ ] Step 3 — address the specific cause (lock, schema, IO).
- [ ] Step 4 — catch up the backlog. You can run compression
      in parallel carefully:
      ```sh
      psql -c "SELECT compress_chunk(c.chunk_schema || '.' || c.chunk_name)
               FROM timescaledb_information.chunks c
               WHERE NOT c.is_compressed
                 AND c.range_end < now() - interval '7 days'
               LIMIT 10;"
      ```
- [ ] Verification: `ratesengine_uncompressed_chunks_older_than_7d`
      drops to zero; disk usage trends back down.

## Known false-positive patterns

- **Recent schema migration** — the policy is disabled
  intentionally for a while until the migration completes.
  Silence during planned windows.
- **Historical backfill** adding new chunks for old data — those
  chunks are instantly > 7 days old and the compression policy
  needs a cycle or two to catch up. Expected; subsides.

## Related

- `db-disk-full.md` — where this ends up if unchecked.
- `cagg-stale.md` — related scheduler issues.

## Changelog

- 2026-04-23 — initial draft.

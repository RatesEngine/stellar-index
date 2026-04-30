---
title: Runbook — cursor-stuck
last_verified: 2026-04-30
status: draft
severity: P2
---

# Runbook — `ratesengine_ingestion_cursor_stuck`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ingestion_cursor_stuck` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/ingestion.yml` |
| Typical MTTR | 10–30 min |
| Impact | On indexer restart, the source re-scans from the last-persisted cursor. If the cursor froze hours ago, restart triggers a huge replay (slow + expensive). While the cursor is stuck, the source is either idle (nothing to advance) or advancing without persisting (data loss on restart). |

## Symptoms

- `increase(ratesengine_cursor_last_ledger{source=...}[5m]) == 0` AND `ratesengine_source_enabled == 1`.
- Dashboard: *Ingestion → Cursor progress* panel shows a flat line for the offending source.
- `ratesengine_source_events_total` may still rise (events are being persisted) — this now points more narrowly at cursor-update failure, not at a separate legacy persister goroutine.

## Quick diagnosis (≤ 5 min)

```sh
# Which source + how far back is the cursor?
ratesengine-ops list-cursors -config /etc/ratesengine/config.toml

# How does that compare to the network tip?
ratesengine-ops detect-gaps -config /etc/ratesengine/config.toml -threshold 100

# If detect-gaps says "ok" but the alert fires: the source isn't
# lagging, it's just not seeing events. Check SourceEventsTotal
# rate in Grafana. If it's zero, this may actually be
# "source-stopped" rolled up incorrectly — check that alert too.
```

Key signals:
- **Cursor flat + events > 0** → cursor upserts are failing or the live pipeline is rejecting ledgers before commit. Inspect indexer logs for `cursor upsert` warnings or dispatcher rejection/panic logs.
- **Cursor flat + events == 0** → no events to advance on. Source may be legitimately quiet. Check `source-stopped` before treating this as a persistence-only issue.
- **Cursor flat + repeated indexer errors** → treat as a live ingest fault, not a harmless replay delay.

## Mitigation (≤ 15 min)

- [ ] Step 1 — if upstream is unhealthy: fix that first. The
      indexer reads ledger metadata from Galexie's MinIO output
      (`galexie-live` bucket) — confirm Galexie is producing
      fresh objects (`mc ls minio/galexie-live | tail`) and that
      the indexer can reach MinIO. If MinIO/Galexie itself is the
      problem, jump to [all-ingestion-down](all-ingestion-down.md).
      The cursor will advance once ledgers start flowing again.
      *(Pre-2026-04-23 deployments routed via stellar-rpc; that
      path was removed from r1 and isn't the upstream today.)*
- [ ] Step 2 — if events are flowing but cursor is flat: restart the indexer pod after capturing recent logs. The current live path updates the cursor inline after successful ledger processing, so a flat cursor usually means repeated ledger failure or DB upsert trouble.
  ```sh
  kubectl rollout restart deploy/ratesengine-indexer
  ```
- [ ] Step 3 — if the cursor has regressed (persisted value < events observed): this should not happen (advance-only guard) and indicates a real bug. Capture the cursor table before restart: `psql -c "SELECT * FROM ingestion_cursors"` and attach to the postmortem.
- [ ] Verification: `ratesengine_cursor_last_ledger{source=...}` starts climbing again after the indexer resumes successful ledger commits.

## Root cause analysis

For the postmortem, gather:
- Indexer logs around when the cursor stopped moving. Search for `cursor upsert`, `dispatcher rejected ledger`, and `dispatcher panicked`.
- The cursor table snapshot before + after restart.
- `ratesengine_source_events_total` vs `ratesengine_cursor_last_ledger` over the incident window.
- If the issue happened post-deploy: diff the live `ledgerstream -> dispatcher -> UpsertCursor` path rather than the retired orchestrator code.

## Known false-positive patterns

- **Quiet sources during low-volume windows**. If a source emits no events, its cursor does not advance either. Cross-check `source-stopped` and the raw event rate before treating a flat cursor as a persistence failure.
- **Container just started** — give the indexer time to process and commit at least one successful ledger before treating the flat line as actionable.

## Related

- `source-stopped.md` — adjacent alert when events stop flowing entirely.
- `all-ingestion-down.md` — where to route when Galexie / MinIO
  (the actual upstream) is the problem.
- `rpc-lag.md` — only relevant if your deployment routes through
  stellar-rpc (r1 doesn't).
- Current live path: `cmd/ratesengine-indexer/main.go` `processAndPersistCursor`.

## Changelog

- 2026-04-23 — initial draft after the cursor-advancement fix + detect-gaps tooling landed.
- 2026-04-30 — Mitigation step 1 rewritten around Galexie + MinIO
  (the actual upstream); the prior "fix stellar-rpc" instruction
  pointed at a service r1 stopped running 2026-04-23. Related
  section now distinguishes the active (`all-ingestion-down`) and
  legacy (`rpc-lag`) upstream-failure paths.

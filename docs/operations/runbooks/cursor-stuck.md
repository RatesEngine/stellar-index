---
title: Runbook — cursor-stuck
last_verified: 2026-04-23
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
- `ratesengine_source_events_total` may still rise (events are being pulled) — it's the PERSIST path that's stuck, not the fetch path.

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
- **Cursor flat + events > 0** → cursor-persister goroutine is wedged (see RCA). Restart the indexer pod as mitigation.
- **Cursor flat + events == 0** → no events to advance on. Source may be legitimately quiet (rare on SDEX, normal on Phoenix). Check upstream — is the DEX contract itself active?
- **Cursor flat + health.Connected false** → the source is failing to reach RPC. Jump to `rpc-lag.md`.

## Mitigation (≤ 15 min)

- [ ] Step 1 — if Connected=false: fix the upstream (stellar-rpc) first. Cursor will advance once events start flowing again.
- [ ] Step 2 — if Connected=true and events are flowing but cursor is flat: restart the indexer pod. The orchestrator spawns a fresh cursor-persister goroutine on each `runOne` cycle; a full restart re-reads the cursor table and re-seeds cleanly.
  ```sh
  kubectl rollout restart deploy/ratesengine-indexer
  ```
- [ ] Step 3 — if the cursor has regressed (persisted value < events observed): this should not happen (advance-only guard) and indicates a real bug. Capture the cursor table before restart: `psql -c "SELECT * FROM ingestion_cursors"` and attach to the postmortem.
- [ ] Verification: `ratesengine_cursor_last_ledger{source=...}` starts climbing within a poll-interval of the restart (default `cursor_persist_every=30s`).

## Root cause analysis

For the postmortem, gather:
- Indexer logs around when the cursor stopped moving. Specifically search for `persist cursor failed` warnings — indicates UpsertCursor errored silently.
- The cursor table snapshot before + after restart.
- `ratesengine_source_events_total` vs `ratesengine_cursor_last_ledger` over the incident window — divergence is the signature of a wedged persister.
- If the issue happened post-deploy: diff `internal/consumer/orchestrator.go` between revisions, specifically `cursorPersister` logic.

## Known false-positive patterns

- **Quiet sources during low-volume windows**. Phoenix can have multi-minute stretches with zero swaps; the cursor doesn't advance because there's nothing new to observe. The alert's `and on (source) ratesengine_source_enabled == 1` predicate can't distinguish "idle" from "stuck" without a ledger-tip cross-check. A future alert revision will `AND` against `(tip - cursor) > 100` for this.
- **Container just started** — cursor persists every 30s by default, so the first 30s post-boot look stuck. Wait one full `cursor_persist_every` cycle before paging.

## Related

- `source-stopped.md` — adjacent alert when events stop flowing entirely.
- `rpc-lag.md` — root cause when the upstream is the problem.
- ADR — consumer/orchestrator.go `cursorPersister` is where advancement happens.

## Changelog

- 2026-04-23 — initial draft after the cursor-advancement fix + detect-gaps tooling landed.

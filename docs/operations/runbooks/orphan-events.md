---
title: Runbook — orphan-events
last_verified: 2026-05-03
status: draft
severity: P3
---

# Runbook — `ratesengine_ingestion_orphan_events`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ingestion_orphan_events` |
| Severity | P3 (informational) |
| Detected by | `deploy/monitoring/rules/ingestion.yml` |
| Typical MTTR | hours-to-days (investigation) |
| Impact | Losing individual swap / oracle updates. Not urgent unless the rate spikes to double-digit events/sec. |

## Symptoms

- `sum by (source) (rate(ratesengine_source_orphan_events_total[10m])) > 10/60` sustained 15 min.
- Per-source breakdown in the alert label shows WHICH source is dropping events.
- `ratesengine_source_events_total` for the same source may still rise — orphans are a subset of pulled events that couldn't be completed.

## Context — what counts as an orphan?

Depends on the source:

- **soroswap** — a `swap` event without its matching `sync` (or vice versa), correlated by `(ledger, tx_hash, op_index)`. Both arrive in the same Soroban transaction so they should nearly always come in the same RPC page.
- **phoenix** — one swap emits 8 separate events (one per field). An orphan is an incomplete N-of-8 set that aged past the buffer's `defaultOrphanMaxAge` (5 min) without the missing fields arriving.
- **aquarius, reflector** — N/A. These sources are 1-event-per-observation and can't produce orphans.

## Quick diagnosis (≤ 10 min)

```sh
# Which source is orphaning? (alert label also tells you this)
curl -s http://api:9464/metrics | grep ratesengine_source_orphan_events_total

# Look at the indexer's logs for the affected source — orphans get
# logged at debug level with the group key.
ssh root@indexer-01 "journalctl -u ratesengine-indexer -n 1000 --no-pager" \
  | grep -E "orphan|evicted" | tail -20

# Is the upstream RPC dropping events? Compare to the decode-error
# rate and the event-rate over the same window:
#   rate(ratesengine_source_events_total[10m])
#   rate(ratesengine_source_decode_errors_total[10m])
#   rate(ratesengine_source_orphan_events_total[10m])
# Orphan-rate rising while event-rate is flat → RPC drops or reorders.
# Event-rate + orphan-rate both rising → source volume spike with
# ordering happening to fall outside our buffer window.
```

## Mitigation (≤ 15 min)

**No live-fix path** — this is an informational alert. Don't
restart or roll back on the basis of orphan-events alone; orphans
are a subset of events that couldn't be correlated, not a blocking
failure. The conventional mitigation step is "investigate upstream"
— see the next section.

If the rate is genuinely catastrophic (`> 100/sec`, see "When to
escalate" below), promote to a `source-stopped` response: the
source is effectively not working, not just dropping a few rows.

## Investigation

This alert is informational; there's no live-fix path. Instead, gather:

- [ ] Sample a few orphan group-keys from the logs. Query a public
      stellar-rpc directly for their tx_hash (r1 doesn't run its
      own stellar-rpc — removed 2026-04-23, see
      [r1-deployment-state.md](../r1-deployment-state.md)):
  ```sh
  curl -X POST https://mainnet.sorobanrpc.com \
    -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"getTransaction","params":{"hash":"<tx>"}}'
  ```
  If the tx is retention-window-NOT_FOUND, RPC already dropped it.
- [ ] Check the contract ID's event stream on stellar.expert or via `getEvents` with the specific filter. A contract that changed its event shape would show up as phoenix decode_errors AND soroswap orphans simultaneously.
- [ ] If the phoenix orphan rate > soroswap's: the 5-min buffer `defaultOrphanMaxAge` may be too short. Phoenix's 8-event emission can span multiple transactions in pathological cases.

## When to escalate

- `> 100/sec` sustained — the source is effectively not working. Treat as `source-stopped`, not `orphan-events`.
- Orphan-rate matches event-rate — the correlation logic is broken (every event gets orphaned). Revert the most recent source-package change.

## Related

- `source-stopped.md` — adjacent alert for the "no events at all" case.
- `decode-errors.md` — different failure mode (events arrive but don't parse).
- `internal/sources/soroswap/consumer.go` — correlation buffer + age eviction.
- `internal/sources/phoenix/consumer.go` — same, for the 8-field fan-in.

## Changelog

- 2026-04-23 — initial draft alongside the orphan-events metric wiring.
- 2026-04-30 — getTransaction probe URL points at a public
  stellar-rpc; r1 doesn't run its own (removed 2026-04-23).

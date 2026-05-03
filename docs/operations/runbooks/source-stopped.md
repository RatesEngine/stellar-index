---
title: Runbook — source-stopped
last_verified: 2026-05-03
status: draft
severity: P2
---

# Runbook — `ratesengine_ingestion_source_stopped`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ingestion_source_stopped` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/ingestion.yml` |
| Typical MTTR | 15–60 min |
| Impact | One configured source has stopped producing events for 5+ minutes. API clients querying that pair see price staleness creep up. If multiple sources stop, escalate to `all-ingestion-down.md` (P1). |

## Symptoms

- `sum by (source) (rate(ratesengine_source_events_total[5m])) == 0` AND `ratesengine_source_enabled == 1` sustained 5 min.
- Dashboard: *Ingestion → Events per source* panel shows a flat line for the offending source while other sources are still producing.

## Quick diagnosis (≤ 5 min)

```sh
# Confirm which source: the alert label tells you, but dashboards
# sometimes drop the label on flat-line queries.
curl -s http://api:9464/metrics | \
  grep -E "ratesengine_source_(events_total|enabled|last_event_unix)"

# Health snapshot for every source's connection state:
ratesengine-ops list-cursors -config /etc/ratesengine/config.toml

# Is upstream the issue? r1 doesn't run its own stellar-rpc (removed
# 2026-04-23, see docs/operations/r1-deployment-state.md); point the
# probe at a public endpoint to confirm the network is closing
# ledgers and the source contract is still emitting events.
ratesengine-ops rpc-probe https://mainnet.sorobanrpc.com
```

Key signals:
- **Shared upstream failure**: on-chain and external sources both flatten at once. Jump to `all-ingestion-down.md`.
- **On-chain-only flattening**: inspect ledgerstream/indexer logs and current cursor movement for a dispatcher-path issue.
- **Per-source-only issue (others fine)**: the source's filter is rejecting everything, the source is legitimately idle, or a protocol change broke its decoder. Check `decode-errors` alert for correlation.

## Mitigation

- [ ] Step 1 — restart the indexer if this is isolated to one or a few sources and the broader host/process is healthy. The indexer runs as `ratesengine-indexer.service` on the indexer hosts (per the `archival-node` ansible role; ADR-0008).
  ```sh
  ssh root@indexer-01 "systemctl restart ratesengine-indexer && \
    systemctl status ratesengine-indexer --no-pager | head -10"
  ```
- [ ] Step 2 — if events flow for 1-2 min post-restart then stop again: the source is probably legitimately idle, misconfigured, or affected by upstream schema drift. Compare its recent on-chain/off-chain activity to expectations before treating it as a dead connector.
- [ ] Step 3 — if decode-errors is also firing: the contract's event shape changed. Follow `decode-errors.md` Step 3 (update decoder + backfill).
- [ ] Verification: `rate(ratesengine_source_events_total{source=...}[5m]) > 0` within 2 min of mitigation.

## Known false-positive patterns

- **Low-volume sources during quiet windows**. Phoenix in particular can genuinely see zero swaps for 5+ minute stretches during off-peak hours. The alert cannot distinguish “idle” from “stuck” on its own; once the source emits any event the alert clears. If this fires repeatedly during known-quiet windows, extend the `for:` window or suppress per-source paging for that venue.
- **Immediately post-deploy**. A restart briefly shows zero events while the source boots. The alert's 5 min window gives enough headroom, but very slow bootstraps (stellar-core catchup) can trip it.

## Related

- `all-ingestion-down.md` — P1 escalation when multiple sources stop.
- `rpc-lag.md` — upstream root cause.
- `decode-errors.md` — adjacent failure mode that can masquerade as source-stopped if every event is being rejected.
- `cursor-stuck.md` — persistence-layer sibling (events flowing but cursor not advancing).

## Changelog

- 2026-04-23 — initial draft.
- 2026-04-30 — rpc-probe URL points at a public stellar-rpc; r1
  doesn't run its own (removed 2026-04-23).

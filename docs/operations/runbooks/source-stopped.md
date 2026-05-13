---
title: Runbook — source-stopped
last_verified: 2026-05-13
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
| Impact | One configured source has stopped producing events for **30+ minutes**, sustained 15 min. API clients querying that pair see price staleness creep up. If multiple sources stop, escalate to `all-ingestion-down.md` (P1). |

## Symptoms

- `sum by (source) (rate(ratesengine_source_events_total[30m])) == 0` AND `ratesengine_source_enabled == 1` sustained 15 min.
- Dashboard: *Ingestion → Events per source* panel shows a flat line for the offending source while other sources are still producing.

The alert window is intentionally wide (30m rate × 15m sustain) to
suppress false positives on low-volume Soroban / FX sources
(blend auctions, ECB FX dailies, Band oracle pushes, Comet pool
swaps, Phoenix off-peak windows) — the natural emission cadence
on these sources can exceed 5 minutes during quiet hours. Total-
outage coverage is the separate
`ratesengine_ingestion_all_sources_stopped` (3-min window, P1) —
that one stays tight because if no source at all is emitting,
something is broken across the whole indexer/upstream surface.

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

## Per-source cadence reference

Use this matrix BEFORE assuming a source is genuinely stopped —
several sources have natural cadences that span the alert's
30-minute rate window. The "expected idle cap" column is the
upper bound on a *normal* silent stretch; sustained idleness
beyond it warrants investigation.

| Source | Expected cadence (active hours) | Expected idle cap (normal silence) | First-look-when-stopped |
| ------ | ------------------------------- | ---------------------------------- | ----------------------- |
| `sdex` (classic) | Continuous during US/EU trading; sparse off-hours | 30 min off-hours | Hubble cross-check via `ratesengine-ops hubble-check`; if Hubble shows trades we missed, decoder regression. |
| `soroswap` | Continuous during US/EU trading hours | 30 min off-hours | Soroban-RPC `getEvents` for the contract; if events flowing on-chain but not into us, decoder regression or cursor stuck. |
| `phoenix` | Low cadence; off-peak windows are common | 45 min off-peak | Same as soroswap. Phoenix's 8-event-per-swap shape (CLAUDE.md surprise) means partial decode-error storms can mimic source-stopped. |
| `comet` | Pool-activity-driven; sparse | 45 min | Verify the contract address matches the operator-watched pool; Comet's shared `("POOL", <event>)` topic can attract events from unrelated Balancer-v1 deploys. |
| `aquarius` | Tied to AMM pool activity; sparse | 30 min | Soroban-RPC `getEvents`. |
| `blend` | Auction-driven; very sparse outside active markets | 90 min | Auctions don't run continuously — verify there's an active auction window before treating silence as a stop. |
| `band` | Operator-controlled `relay()` cadence; typically every 5-15 min when configured | 20 min | Band emits **zero events** (CLAUDE.md surprise) — observed via `InvokeContract` op args through the dispatcher's `ContractCallDecoder`. Verify the `ContractCallDecoder` is wired and the contract is still being relayed-to upstream. |
| `redstone` | Batch pushes every ~1 min during active periods | 10 min active / 30 min off-peak | Redstone's adapter event topic is `"REDSTONE"`; the body has no `feed_id` (lives in OpArgs). Verify the OpArgs plumbing is intact. |
| `reflector` (×3 contracts: DEX/CEX/FX) | Continuous on the active feed | 15 min DEX/CEX, 60 min FX (FX feed is much slower) | Reflector is **three separate contracts** — confirm WHICH one is silent. The DEX/CEX contracts are the most-watched; the FX contract's slower cadence makes it falsely-page-prone. |
| `binance`, `kraken`, `bitstamp`, `coinbase` (CEX WS streamers) | Continuous (sub-second) when open | 60 s gap = anomalous; 5 min = certainly broken | These are WebSocket streamers, not pollers — silence usually means the WS connection dropped silently. Check streamer-error metrics + reconnect logs. |
| `coingecko` (poller) | Default 60s interval | 5 min (one missed cycle plus cooldown headroom) | CG-specific cooldown semantics — see [`external-poller-error-rate-high.md` § Vendor-specific 429 patterns](external-poller-error-rate-high.md#vendor-specific-429-patterns). |
| `ecb` (FX dailies) | Once per business day ~16:00 CET | 24 hours weekdays / 72 hours weekends-and-holidays | ECB doesn't publish on EU bank holidays; cross-reference the silence date against the published TARGET2 closing days before treating as a stop. |

When the silent source is one of the off-peak-prone ones (Phoenix,
Comet, Aquarius, Blend, ECB) AND the silence is within the
expected idle cap, this is almost always a false positive —
extending the alert window for that specific source is the right
fix, not restarting the indexer. The current alert's 30m × 15m
window was tuned in 2026-05-12 (F-1212b) to suppress most of these
but doesn't catch every off-peak case for the longer-cadence sources.

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

- **Low-volume sources during quiet windows**. Phoenix, Blend, Comet, Band, and ECB can each go genuinely silent for stretches of 10–25 minutes. The 30m-rate × 15m-sustain window is calibrated to wait past those quiet stretches before paging. Repeat false positives now mean either the venue's natural cadence has slowed beyond 30m (consider extending further) OR the source is genuinely stuck — investigate.
- **Immediately post-deploy**. A restart briefly shows zero events while the source boots. The 15-min sustain gives ample headroom for normal restarts including stellar-core catchup.

## Related

- `all-ingestion-down.md` — P1 escalation when multiple sources stop.
- `rpc-lag.md` — upstream root cause.
- `decode-errors.md` — adjacent failure mode that can masquerade as source-stopped if every event is being rejected.
- `cursor-stuck.md` — persistence-layer sibling (events flowing but cursor not advancing).

## Changelog

- 2026-04-23 — initial draft.
- 2026-04-30 — rpc-probe URL points at a public stellar-rpc; r1
  doesn't run its own (removed 2026-04-23).
- 2026-05-12 — alert window widened to 30m rate × 15m sustain to
  suppress false positives on low-volume sources (blend, band,
  ecb, comet, phoenix). F-1212b.

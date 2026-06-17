---
title: Runbook — oracle-stale
last_verified: 2026-05-03
status: draft
severity: P2
---

# Runbook — `stellarindex_oracle_stale`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `stellarindex_oracle_stale` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/divergence.yml` |
| Typical MTTR | 15–60 min |
| Impact | An oracle source stopped publishing updates for > 10× its declared resolution (e.g. > 50 min for Reflector's 5-min cadence). `/v1/oracle/latest` responses for that source's assets become increasingly out of date; any downstream consumer using the oracle prices (triangulation, divergence) gets poisoned inputs. |

## Symptoms

- `(time() - stellarindex_oracle_last_update_unix) > 10 * stellarindex_oracle_resolution_seconds` sustained 2 min.
- Alert label `source` names the specific variant — one of `reflector-dex`, `reflector-cex`, `reflector-fx`, `redstone`, `band`. (Chainlink-HTTP is a divergence reference in `internal/divergence/`, not an oracle source — it doesn't emit `stellarindex_oracle_*` metrics and won't appear here.)
- `stellarindex_source_events_total{source=reflector-...}` rate drops to zero at the same time (or has been zero throughout).

## Quick diagnosis (≤ 5 min)

```sh
# How long since last observation, per source?
curl -s http://localhost:9464/metrics |
  grep -E "stellarindex_oracle_last_update_unix|stellarindex_oracle_resolution_seconds"

# Is the CONTRACT itself still emitting? The oracle source
# subscribes to (ContractIDs=[contract], topics=[REFLECTOR,update]).
# A Reflector contract goes 5 min between updates in normal ops;
# > 50 min is real stall. r1 doesn't run its own stellar-rpc
# (removed 2026-04-23, see docs/operations/r1-deployment-state.md);
# point the probe at a public endpoint to confirm the network is
# closing ledgers and the oracle contract has been invoked recently.
stellarindex-ops rpc-probe https://mainnet.sorobanrpc.com

# Check stellar.expert for the contract's recent tx activity:
#   https://stellar.expert/explorer/public/contract/<contract-id>
#
# Fresh updates on stellar.expert but zero in our metrics →
# our subscription is broken. Zero updates on-chain → the
# relayer/publisher is down.
```

Key signals:
- **On-chain activity continues, we see zero** → filter or subscription issue on our side. Restart the indexer; if the issue persists, the contract's event shape may have changed.
- **On-chain activity paused** → the oracle's off-chain publisher (Reflector relayer, Redstone DataService, Band's chain-write bot) is down. Nothing we can do except switch providers or fail over.
- **We see SOME events but they're not decoding** → `stellarindex_source_decode_errors_total` for that source is also elevated. Jump to `decode-errors.md`.

## Mitigation

- [ ] Step 1 — identify whether the stall is upstream (publisher / contract) or downstream (our ingestion) via the probes above.
- [ ] Step 2 — if our-side: restart the indexer pod. The reflector source seeds from tip on boot and re-subscribes; this resolves stuck subscriptions.
- [ ] Step 3 — if publisher-side: check the provider's status page (Reflector: app.reflector.world, Redstone: app.redstone.finance, Band: data.bandprotocol.com). Open an incident tracking the upstream ETA. Our API will flag affected asset prices with `stale=true` in the response envelope — communicate that SLA departure to consumers.
- [ ] Step 4 — if a specific asset stops but others from the same source keep flowing: the contract de-listed that asset. Update the fallback aggregation config to drop the oracle for that asset.
- [ ] Verification: `stellarindex_oracle_last_update_unix` for the affected source starts incrementing again.

## Severity note

Originally flagged P2 (ticket) because the impact is bounded — the API keeps serving stale oracle prices instead of failing. If multiple oracle sources go stale at once, escalate to P1: every triangulation path that relies on an oracle is now broken. A single oracle going stale is usually handled by falling back to the other two Reflector variants.

## Related

- `decode-errors.md` — when we're receiving events but failing to parse them.
- `source-stopped.md` — same root-cause class, different alert (trade sources vs oracle sources).
- ADR / internal/sources/reflector — where the oracle contract IDs are configured.

## Changelog

- 2026-04-23 — initial draft. Replaces the 404 in the divergence alert rules' runbook_url.
- 2026-04-30 — rpc-probe URL points at a public stellar-rpc; r1
  doesn't run its own (removed 2026-04-23).

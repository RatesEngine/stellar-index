---
title: Runbook — decode-errors
last_verified: 2026-05-13
status: draft
severity: P3
---

# Runbook — `ratesengine_ingestion_decode_error`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ingestion_decode_error` |
| Severity | P3 (informational) |
| Detected by | `deploy/monitoring/rules/ingestion.yml` |
| Typical MTTR | hours-to-days (investigation) |
| Impact | Per-event parse failures. One failure = one lost observation. At sustained >1/sec, a non-trivial fraction of the source's signal is being dropped. |

## Symptoms

- `rate(ratesengine_source_decode_errors_total{source=...}[5m]) > 1` sustained 5 min.
- Dashboard: *Ingestion → Decode errors* panel non-zero for the offending source.
- Decode-error rate sometimes tracks a specific asset or contract — check the indexer's debug logs for patterns in rejected events.

## Context — what counts as a decode error?

- The SCVal / XDR bytes didn't match the expected shape for the source's event schema.
- Amount values parsed as out-of-range (zero / negative) where the canonical.Trade invariants require positive.
- Asset codes or strkeys failed content validation (e.g. non-alphanumeric classic code, malformed issuer).

Distinct from `orphan-events` (events were well-formed but their correlation partner never arrived) and `insert-errors` (events decoded fine but persistence failed).

## Quick diagnosis (≤ 10 min)

```sh
# Which source is erroring? (alert label tells you this)
curl -s http://api:9464/metrics | grep ratesengine_source_decode_errors_total

# Peek the indexer's stderr for the most recent rejection reasons.
# Source logs at debug when an event is dropped — enable temporarily
# if the default level is info.
ssh root@indexer-01 "journalctl -u ratesengine-indexer -n 500 --no-pager" \
  | grep -iE "decode|parse|malformed" | tail -30

# Cross-check: is the contract the source points at the right one?
# A protocol upgrade often changes event shape for a specific
# contract address — rpc-probe confirms the source contract still
# emits recent events, and what topic shape they have today.
# Note: r1 doesn't run its own stellar-rpc (removed 2026-04-23, see
# docs/operations/r1-deployment-state.md); point the probe at a
# public endpoint such as SDF's mainnet RPC.
ratesengine-ops rpc-probe https://mainnet.sorobanrpc.com
```

## Typical root causes

In decreasing order of frequency:

1. **Contract upgraded its event shape.** The most common trigger. A DEX redeploys a pair contract with tweaked event fields; our decoder's field arity check fails. Usually announced in the DEX's release notes; check there first.

2. **Stellar protocol version bump.** CAP-67 (P23) changed how classic asset events look — similar breaking changes happen at most protocol upgrades. `rpc-probe`'s `protocolVersion` line confirms whether the node's running a new protocol we haven't accounted for.

3. **Decoder regression in our repo.** After a deploy of the indexer, an ingest-path commit may have broken a specific event path. `git log --oneline internal/sources/<source>/` scoped to the post-deploy window identifies candidates. Revert is the fastest mitigation.

4. **Orchestrator hitting an off-schedule test/admin tx.** A contract's admin method (pause, upgrade) emits events that look like a swap but decode differently. These are rare and usually coincide with a DEX deploy. The fix is a decoder that ignores them; meanwhile, the error rate should revert to normal once the tx clears.

## Per-source quick reference

If the alert label points at one of these sources, the listed
surprise is the single most common cause of decode regressions —
worth checking BEFORE deeper diagnosis. The full long-form notes
live in `docs/discovery/dexes-amms/` and `docs/discovery/oracles/`;
this table is the operator-facing summary derived from them and
the CLAUDE.md "Things that will surprise you" list.

| Source | Most common decode-regression cause | First place to look |
| ------ | ----------------------------------- | ------------------- |
| `soroswap` | `SwapEvent` has no post-state reserves; reserves come in the immediately-following `SyncEvent`. A missing/orphaned `SyncEvent` produces a partner-less swap that decodes but has no usable reserve context. | `discovery/dexes-amms/soroswap.md`; check `orphan-events` rate. |
| `phoenix` | Phoenix emits **8 events per swap** (one per field with a 2-tuple topic `("swap", "<field>")`). A swap reconstruction that's short of all 8 looks malformed. | `discovery/dexes-amms/phoenix.md`; group by `(ledger, tx_hash, op_index)`. |
| `comet` | Comet uses a shared `("POOL", <event>)` topic across **every** pool contract — the decoder matches by topic bytes, not pool contract ID. A new pubnet contract deploying Balancer-v1 Comet code will look identical on the wire. | `discovery/dexes-amms/comet.md`; filter downstream by `Trade.Source = "comet"` + contract-address context. |
| `reflector` | Reflector is **three separate contracts** (DEX / CEX / FX) and has NO on-chain `twap` or `x_*` methods (proposal said it does; it doesn't). | `discovery/oracles/reflector.md`; check which Reflector contract is decoding. |
| `band` | Band's Soroban contract emits **zero events** — observed via `relay()` / `force_relay()` `InvokeContract` op args through the dispatcher's `ContractCallDecoder` interface (PR 168). Pair rates are at E18 scale; relayed single-asset rates are at E9. | `discovery/oracles/band.md`; verify the `ContractCallDecoder` is wired. |
| `redstone` | Adapter emits topic `"REDSTONE"` events but the body carries NO `feed_id`. Feed IDs live in the tx's `write_prices(updater, feed_ids, payload)` op args — plumbed through `events.Event.OpArgs` (PR 166). Length-mismatch between feed_ids and updated_feeds returns `ErrFeedIDCountMismatch`. | `discovery/oracles/redstone.md`; check the OpArgs plumbing. |
| `sdex` (classic) | Post-P23 (mainnet 2025-09-03) every classic asset movement emits a unified transfer/mint/burn event with a 4th `sep0011_asset` topic. Pre-P23 you parse operations+effects. The decoder handles both — but a protocol bump can introduce a third event shape. | `discovery/notes/cap-67-unified-events.md`; check `protocolVersion` from `rpc-probe`. |
| Any SEP-41 token | `transfer` event data can be EITHER a simple `i128` OR a map containing `amount` + `to_muxed_id`. Type-test before `MustI128()`. | `discovery/notes/sep-41-token-events.md`. |

When a Soroban DEX/oracle source decode-errors immediately after a
Stellar protocol bump or a known DEX redeploy: the source's WASM
likely changed event/topic shape. **Backfill is unsafe across the
upgrade boundary** until the WASM-hash audit re-runs (see
[`docs/operations/wasm-audits/`](../wasm-audits/) and
[`architecture/contract-schema-evolution.md`](../../architecture/contract-schema-evolution.md))
— flip `BackfillSafe = false` for that source until the audit log
shows the new WASM hash decodes cleanly.

## Mitigation

This alert is P3 because there's no emergency runtime response — we can't un-drop events after the fact. The mitigation ladder is:

- [ ] Step 1 — identify the root cause from the table above.
- [ ] Step 2 — if the cause is transient (option 4): wait. Rate should decline on its own.
- [ ] Step 3 — if the cause is a contract upgrade (option 1 or 2): update the decoder in `internal/sources/<source>/decode.go`. Typical iteration is one PR plus a golden-file fixture reproduction. Backfill from the cursor start via the indexer on relaunch.
- [ ] Step 4 — if the cause is a regression (option 3): `git revert` the suspect commit and deploy. File an incident to retry the regressed change with a proper test.
- [ ] Verification: `rate(...decode_errors_total[5m])` drops back under the 1/sec threshold within 5 min of mitigation.

### Customer comms note when `class_drop_spike` co-fires

If `ratesengine_aggregator_class_drop_spike` fires alongside this
alert, the affected source has dropped out of the VWAP for one or
more pairs. The remaining sources continue to serve prices, but
the smaller consensus may produce elevated
`flags.divergence_warning` on the affected pairs. **Surface this
in customer comms** — it explains why a customer might see a
warning flag without a corresponding price disruption. Template:
"Affected pairs may show elevated `flags.divergence_warning`; price
is still served correctly from remaining sources." See
[drills/2026-04-sev2-soroswap-decode-regression.md](../drills/2026-04-sev2-soroswap-decode-regression.md)
for the canonical exercise of this pattern.

## Related

- `orphan-events.md` — adjacent failure mode (events well-formed but partnerless).
- `insert-errors.md` — downstream failure mode (events decoded OK but write-path broke).
- `source-stopped.md` — when the rate hits 100% of pulled events, effectively stopping the source.
- `internal/sources/*/decode.go` — per-source decoder.

## Changelog

- 2026-04-23 — initial draft alongside the SourceDecodeErrorsTotal wiring + orphan/decode split.
- 2026-04-30 — rpc-probe URL points at a public stellar-rpc; r1
  doesn't run its own (removed 2026-04-23).

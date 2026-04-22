# Aquarius connector

Ingests trade events from [Aquarius](https://aqua.network) —
a Soroban AMM with three pool shapes. Primary Phase-1 reference:
[`docs/discovery/dexes-amms/aquarius.md`](../../../docs/discovery/dexes-amms/aquarius.md).

## What this ingests

Aquarius has three distinct pool types and a central router that
tracks them all:

1. **Volatile** — Uniswap-v2-style constant-product pool (`x * y = k`).
   Most pairs on Aquarius.
2. **Stableswap** — Curve-style invariant for stablecoin bundles.
   N-asset pools (typically 2–4 assets).
3. **Concentrated** — Uniswap-v3-style concentrated liquidity
   (WIP at Phase-1 audit time; may still be on a feature branch).
   We flag it but don't decode trades through it yet.

All three emit events through a **unified event module** — not
per-pool like Soroswap. This means one subscription covers every
Aquarius pool, and we multiplex by contract ID + event kind.

Mainnet addresses (Phase-1-verified):

| Contract | Address |
| --- | --- |
| Router | `CBQDHNBFBZYE4MKPWBSJOPIYLW4SFSXAXUTSXJN76GNKYVYPCKWC6QUK` |
| Factory | derived from router reads |
| Plane | derived from router reads |
| XLM SAC (used by Aquarius docs) | `CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA` |

Factory / plane / calculator / locker-feed / fees-collector
addresses are fetched at startup via router reads + cached.
Residual TODO(#0): capture these statically in `events.go` once
we've confirmed they're stable across protocol upgrades.

## Events we care about

| Event | Topic 0 | Carries | Role |
| --- | --- | --- | --- |
| `trade` | `trade` | base_in, base_out, counter_in, counter_out, user | PRIMARY — emits a single row, unlike Soroswap's swap+sync pair |
| `deposit_liquidity` | `deposit_liquidity` | amounts per asset, LP tokens minted | LP state change (not a trade) |
| `withdraw_liquidity` | `withdraw_liquidity` | amounts per asset, LP tokens burned | LP state change |
| `update_reserves` | `update_reserves` | reserves post-operation | redundant signal with `trade` — use for invariant checking |
| `reserves_sync` | `reserves_sync` | reserves (alternative naming) | older pools; same data as update_reserves |
| `kill_*` | `kill_*` | admin pool-kill signals | diagnostic, not a trade |

**One event per trade** (unlike Soroswap's swap+sync pairing). We
decode `trade` directly into `canonical.Trade` — no correlation
buffer needed. This is the main structural difference from the
Soroswap consumer.

## Quirks

### Q1 — Variable-arity stableswap pools

Stableswap pools have N assets (typically 2–4, but the contract
allows larger bundles). A trade event on an N-asset pool carries
`amounts_in[N]` and `amounts_out[N]` arrays. Of those arrays, at
least one `in` slot is positive and at least one `out` slot is
positive; other slots are zero.

Our decoder normalises: `base` = asset corresponding to the
positive `in` slot, `quote` = asset corresponding to the positive
`out` slot. If more than one of either is positive we have a
complex multi-asset swap — we record all of it but emit a
canonical.Trade for each `(in, out)` pair that's non-trivially
sized. Design detail in `decode.go`.

### Q2 — Token identity requires a router read

Unlike Soroswap where the pair contract ID maps 1:1 to a token
pair, Aquarius pools are identified by a pool-id + token-list. The
indexer consults the router's `pool_tokens(pool_id)` function
(cached) to learn the asset list for each pool.

### Q3 — Trade events include both in AND out arrays

Soroswap emits `amount0_in / amount1_in / amount0_out / amount1_out`
as four scalars. Aquarius emits them as two arrays indexed by asset
position. This means our SCVal decoder needs Vec-of-i128 support,
not just scalar i128. (The real XDR decoder handles this when the
SDK dep lands; for now we stub it.)

### Q4 — Concentrated-liquidity pools are WIP

If we observe `concentrated` pool events at ingest time, we log +
drop them with a counter (TODO(#0)). Safer than emitting half-
decoded trades. Re-verify whether the feature branch has merged
before Week 3 — it may have shipped since Phase-1 audit.

## File layout

| File | Purpose |
| --- | --- |
| `README.md` | this file |
| `events.go` | event-name + topic-symbol constants, pool type enum, mainnet router address |
| `decode.go` | trade-event → `canonical.Trade` (single-event decode; variable-arity handled) |
| `consumer.go` | implements `consumer.Source`; maintains the pool→tokens map |
| `source_test.go` | unit tests with fake SCVal decoder hooks |

## Relationship to Soroswap connector

Both connectors implement `consumer.Source` and feed the same
orchestrator. Differences that shape the code:

| Aspect | Soroswap | Aquarius |
| --- | --- | --- |
| Event correlation | swap + sync, 2 events per trade | 1 event per trade |
| Assets per pool | always 2 | 2–4 (stableswap) |
| Pool identity | pair contract address | pool_id + router lookup |
| Event topics | per-pair contract | per-router contract |
| Correlation buffer | yes (`(ledger, tx_hash, op_index)`) | no |

The common plumbing (orchestrator, cursors, config, stellarrpc
client) is shared.

## Phase status

**Skeleton only.** Topic byte-match dispatching + single-event
decode + variable-arity normalisation are implemented as Go code,
but SCVal XDR decoding is behind the `decoderHooks` package-level
vars (stubs today; real implementations land with the SDK dep).
Unit tests exercise the orchestration without the XDR codec.

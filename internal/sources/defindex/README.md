# defindex source

Decoder for **DeFindex** vault contracts on Stellar mainnet.
DeFindex is a yield-aggregator vault system from
[paltalabs/defindex](https://github.com/paltalabs/defindex) — vaults
hold user-deposited capital and route it into underlying lending /
DEX protocols (currently Blend) via per-vault `Strategy` contracts.

## Why a separate decoder

DeFindex vaults emit Soroban contract events with topic[0] =
`ScvString("DeFindexVault")`. The events flow through the standard
event-based dispatcher (`Decoder` interface, like `soroswap` /
`aquarius`); no `ContractCallDecoder` needed for the vault events.
Each user-facing vault op (`deposit` / `withdraw`) emits one event —
no swap+sync correlation, so the wire shape is simpler than Soroswap.

The vaults DO **not** emit price-discovery trades — a deposit moves
LP shares (the vault's `df_token`) at the vault's NAV but doesn't
set a market price. We surface them as **flow attribution** rows
only:

- They feed `aggregator_exposures` (capital allocation) via a
  separate periodic worker (Phase B follow-up).
- Same-tx Blend / Soroswap legs spawned by the vault's strategy
  get tagged `trades.routed_via = "defindex-{vault_name}"` by the
  router-attribution observer (also Phase B).

The vault is registered with `Class: ClassRouter` (alongside the
Soroswap router) — same attribution-only treatment, never a VWAP
contributor.

## Files

```
events.go              — constants: source name, contract IDs,
                         topic prefix bytes, event-name symbols
decode.go              — RawVaultEvent → DepositEvent / WithdrawEvent
dispatcher_adapter.go  — implements dispatcher.Decoder (event-based)
consumer.go            — Sink for the pipeline-side log emit
README.md              — this file
```

## Phase A scope (what ships now)

- Decode `("DeFindexVault","deposit")` and `("DeFindexVault","withdraw")`
  events on the **3 known autocompound vaults** (USDC / EURC / XLM).
- Emit a structured log line per event with depositor / amounts /
  share-token delta.
- No persist — no `routed_via` tag yet; no `aggregator_exposures`
  rows yet.

## Phase B follow-ups

1. **Multi-vault discovery.** Watch `("DeFindexFactory","create")`
   events + read the InvokeContract op return value to capture
   newly-deployed vault addresses (factory event body lacks the
   address — confirmed against
   `apps/contracts/factory/src/lib.rs:205-231` at tag `1.0.0`).
2. **`trades.routed_via` tagging.** Hook the dispatcher's
   ContractCallDecoder for vault `deposit` / `withdraw` calls —
   when the same tx contains a Blend or Soroswap event, tag those
   trades with the vault name.
3. **Aggregator-exposure ticker.** Periodic worker that queries
   each vault's on-chain state (vault token holdings + per-strategy
   balances) and writes `aggregator_exposures` rows. Frequency:
   1 min (same as TVL ticker).
4. **Strategy events.** Optionally decode `("BlendStrategy","deposit")`
   / `("BlendStrategy","withdraw")` events emitted by the strategy
   contract — these correlate with the vault event in the same tx
   and give per-strategy granularity.
5. **Rebalance event multiplexing.** The `("DeFindexVault","rebalance")`
   topic is shared by 4 distinct body shapes (`unwind` / `invest` /
   `SwapEIn` / `SwapEOut`). Discriminate by the `rebalance_method`
   field inside the body.

## Sources

- Function signatures: `apps/contracts/vault/src/interface.rs`
  @ tag 1.0.0.
- Event shapes: `apps/contracts/vault/src/events.rs` @ tag 1.0.0.
- Mainnet hashes: `apps/contracts/public/mainnet.contracts.json`
  @ tag 1.0.0 — vault WASM
  `0f3073517cbfacbfd482bc166cff38a0e7abeab9b7ee77334abab45880fb8f3a`.
- WASM audit: `docs/operations/wasm-audits/defindex.md` (in_progress).

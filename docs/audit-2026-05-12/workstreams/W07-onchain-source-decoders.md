# W07 — On-chain source decoders + auxiliary readers

## Scope

Every Soroban + classic source under `internal/sources/` (excluding
`internal/sources/external/*` which is W08, and the `forex/` +
`frankfurter/` siblings which are also W08).

Run the per-decoder loop (`02-protocol.md` §6) for each.

In scope:
- `internal/sources/soroswap` — SwapEvent + paired SyncEvent
- `internal/sources/aquarius`
- `internal/sources/phoenix` — 8-event grouping invariant
- `internal/sources/comet` — shared `("POOL", *)` topic
- `internal/sources/blend` — auctions
- `internal/sources/reflector` — DEX/CEX/FX three contracts
- `internal/sources/redstone` — WritePrices + op_args feed_id
- `internal/sources/band` — relay() ContractCallDecoder
- `internal/sources/sdex` — classic ops + effects (post-CAP-67)
- `internal/sources/accounts` — ADR-0021 account-entry observer
- `internal/sources/trustlines`
- `internal/sources/claimable_balances`
- `internal/sources/sac_balances`
- `internal/sources/sep41_supply` — ADR-0023
- `internal/sources/liquidity_pools`

Out of scope (handled in W08):
- `internal/sources/external/*` adapters
- `internal/sources/forex/*` and `internal/sources/frankfurter/*`

## Inputs

- `inventory/source-decoder-inventory.md`
- CLAUDE.md "Things that will surprise you" (per-source caveat list)
- `docs/operations/wasm-audits/<source>.md`
- `internal/sources/external/registry.go` BackfillSafe column

## Per-decoder seven-check loop

For each source above, fill this table:

| Check | Result | Evidence |
| --- | --- | --- |
| 1. Claim surface (event topics / op kinds / contract IDs / methods) | | |
| 2. Decode entry function(s) traced from dispatcher | | |
| 3. Malformed-input handling (no panic, typed error, drop counter) | | |
| 4. Storage / consumer integration (sink + table) | | |
| 5. Fixture realism (real-ledger captures? capture script + mtime) | | |
| 6. Tests vs actual risk (happy-path-only? malformed-input test? WASM-dispatch test?) | | |
| 7. WASM audit status (`BackfillSafe` flag + audit doc) | | |
| 8. CLAUDE.md surprise compliance (per-source caveat asserted in test) | | |

## Per-source caveats from CLAUDE.md

| Source | Caveat | Test covering it |
| --- | --- | --- |
| soroswap | SwapEvent has no post-state reserves; correlate with following SyncEvent by `(ledger, tx_hash, op_index)` | |
| phoenix | 8 events per swap; group by `(ledger, tx_hash, op_index)` | |
| comet | Shared `("POOL", <event>)` topic across every pool contract; downstream filter on contract context | |
| reflector | No on-chain `twap` or `x_*`; we compute locally; three separate contracts (DEX/CEX/FX) | |
| band | E18 pair-rate scale, E9 single-asset rate; **emits zero events** — observe `relay()` / `force_relay()` via ContractCallDecoder | |
| redstone | `WritePrices` event topic `"REDSTONE"`; event body lacks feed_id; feed_ids in op args via `events.Event.OpArgs`; skip on `ErrFeedIDCountMismatch` | |
| sdex / classic | Post-CAP-67 unified events vs pre-Whisk operations+effects | |
| sep41 | `transfer` data can be EITHER `i128` OR map containing `amount` + `to_muxed_id`; type-test before MustI128 | |
| accounts (ADR-0021) | observer for AccountEntry mutations | |
| supply observers (ADR-0022/0023) | per-class hypertable; aggregator-resident refresh path | |

## Per-source file inventory + audit row

| Source | Files | Tests | Fixtures | Audit doc | BackfillSafe? | Notes |
| --- | --- | ---: | ---: | --- | --- | --- |
| `soroswap` | | | `test/fixtures/soroswap` | `docs/operations/wasm-audits/soroswap.md` | | |
| `aquarius` | | | `test/fixtures/aquarius` | `…/aquarius.md` | | |
| `phoenix` | | | `test/fixtures/phoenix` | `…/phoenix.md` | | |
| `comet` | | | _verify_ | `…/comet.md` | | |
| `blend` | | | _verify_ | `…/blend.md` | | |
| `reflector` | | | `test/fixtures/reflector` | `…/reflector.md` | | |
| `redstone` | | | _verify_ | `…/redstone.md` | | |
| `band` | | | _verify_ | `…/band.md` | | |
| `sdex` | | | _verify_ | _per CAP-67 doc_ | | |
| `accounts` | | | _verify_ | _per ADR-0021_ | | |
| `trustlines` | | | _verify_ | _per ADR-0022_ | | |
| `claimable_balances` | | | _verify_ | _per ADR-0022_ | | |
| `sac_balances` | | | _verify_ | _per ADR-0022_ | | |
| `sep41_supply` | | | _verify_ | _per ADR-0023_ | | |
| `liquidity_pools` | | | _verify_ | _per ADR-0022_ | | |

## Adversarial vectors

- Whole A1.* family of hostile XDR
- A1.4 specifically (Comet topic confused with non-Blend pool)
- A2.9 Reflector contract upgraded with new field at same key
- A1.9 WASM upgrade mid-backfill (W24)

## Cross-workstream dependencies

- W06 verifies dispatcher routing
- W09 verifies storage table writes
- W10 verifies aggregator consumption
- W14 verifies decoder-stats metric emission (table 0020)
- W24 verifies BackfillSafe flag rationale per source

## Closure criteria

- Every source has a complete seven-check loop
- Every CLAUDE.md surprise has a corresponding test reference
- Every BackfillSafe=true claim has WASM audit evidence
- Every source's fixture realism is verified (mtime + capture script reference)

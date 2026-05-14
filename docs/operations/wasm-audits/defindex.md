---
title: DeFindex WASM-history audit
last_verified: 2026-05-14
status: in_progress — decoder shipped, BackfillSafe still false
source: defindex
backfill_safe: false
---

# DeFindex WASM audit

Audit log for the `defindex` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

## Status

**In progress (2026-05-14).** Decoder package
`internal/sources/defindex/` shipped this session. Live decoding
starts from current ledger forward; `BackfillSafe` remains
`false` in `internal/sources/external/registry.go` pending the
per-WASM-hash walk below.

DeFindex is a yield-aggregator vault system from
[paltalabs/defindex](https://github.com/paltalabs/defindex).
Vaults hold user-deposited capital and route it into underlying
yield protocols (currently Blend) via per-vault `Strategy`
contracts. We capture vault `deposit` / `withdraw` events for
flow attribution; the vaults do **not** emit price-discovery
trades and never contribute to VWAP.

## Contracts under audit

Captured from `internal/sources/defindex/events.go` (cross-checked
against `paltalabs/defindex` tag `1.0.0` on 2026-05-14):

| role | contract / hash |
| --- | --- |
| Factory | `CDKFHFJIET3A73A2YN4KV7NSV32S6YGQMUFH3DNJXLBWL4SKEGVRNFKI` |
| USDC autocompound vault | `CDB2WMKQQNVZMEBY7Q7GZ5C7E7IAFSNMZ7GGVD6WKTCEWK7XOIAVZSAP` |
| EURC autocompound vault | `CC5CE6MWISDXT3MLNQ7R3FVILFVFEIH3COWGH45GJKL6BD2ZHF7F7JVI` |
| XLM autocompound vault | `CDPWNUW7UMCSVO36VAJSQHQECISPJLCVPDASKHRC5SEROAAZDUQ5DG2Z` |
| Vault WASM hash (current baseline) | `0f3073517cbfacbfd482bc166cff38a0e7abeab9b7ee77334abab45880fb8f3a` |
| BlendStrategy WASM hash (current baseline) | `65ee2e1b32ff39a6c8f8572dd0d6d2db7952be6d54c740bfb1d6eab6dd209dc0` |

The vault WASM hash is shared by all three Phase-A vaults (they're
the same template instantiated against different underlying
assets / Blend pools).

## Decoder expectations

Captured from `internal/sources/defindex/{events,decode}.go` at
HEAD as of 2026-05-14. Any divergence from these in a deployed
WASM hash is an audit finding.

### Topic structure

Vault events have a 2-element topic:

```text
topic[0] = ScvString("DeFindexVault")    — 13 chars, exceeds symbol_short!'s 9-char cap
topic[1] = ScvSymbol(event_name)
  — Phase-A decodes:
    "deposit"   → user-facing flow into the vault
    "withdraw"  → user-facing flow out of the vault
  — Phase-B follow-ups (not yet decoded):
    "rescue", "paused", "unpaused", "nreceiver",
    "nmanager", "nemanager", "rbmanager", "dfees",
    "rebalance" (multiplexed body — discriminate by
                 `rebalance_method` Symbol field inside body)
```

### Body shapes

Both `deposit` and `withdraw` bodies are `ScvMap` keyed by
field-name `Symbol` (decode-by-name per
docs/architecture/contract-schema-evolution.md). Phase-A pulls
only the user-facing dimensions:

| event | body fields decoded |
| --- | --- |
| `deposit` | `depositor: Address`, `amounts: Vec<i128>`, `df_tokens_minted: i128` |
| `withdraw` | `withdrawer: Address`, `amounts_withdrawn: Vec<i128>`, `df_tokens_burned: i128` |

The body also carries `total_supply_before` and
`total_managed_funds_before` (for accurate NAV reconstruction);
we ignore these at Phase A.

`amounts` is a vec because DeFindex supports multi-asset vaults.
The Phase-A trio (USDC / EURC / XLM autocompound) are all
single-asset, so the vec has length 1 in practice — but the
decoder doesn't hardcode that.

### Surprising gotchas (catalogued during the upstream research)

1. **Topic[0] is `ScvString`, not `ScvSymbol`.** Same encoding
   pattern as Soroswap (`"SoroswapPair"` / `"SoroswapFactory"`).
   Confirmed via the `internal/sources/defindex/events.go`
   `scval.MustEncodeString` call.
2. **Factory `create` event body lacks the new vault address.**
   Captured in `apps/contracts/factory/src/lib.rs:205-231` at tag
   1.0.0 — `create_vault_internal` returns the vault address but
   the event body only carries `roles / vault_fee / assets`.
   Phase-B follow-up: plumb the InvokeContract op return value via
   `events.Event.OpArgs` (same pattern Band / Redstone use).
3. **Four distinct rebalance event bodies share one topic.** All
   four (`unwind`, `invest`, `SwapExactIn`, `SwapExactOut`)
   publish on `("DeFindexVault","rebalance")`. Discriminate by
   the `rebalance_method` Symbol field inside the body. Not
   needed at Phase A but worth noting before any future
   rebalance-decode work.
4. **Strategy events fire from the strategy contract, not the
   vault.** The same tx that emits a vault `deposit` will also
   emit a `("BlendStrategy","deposit")` from the per-vault strategy
   contract — and from there a Blend `("Pool","supply")`. All
   three are correlated by `tx_hash` + `op_index`. Phase A only
   decodes the vault layer.
5. **`from` field on strategy events is the vault address**, not
   the end-user. End-user attribution requires correlating with
   the vault event in the same tx (Phase B).

## Pending work to flip BackfillSafe → true

1. Walk historical `update_contract_op` events on each of the 3
   vault contracts + factory across the post-Soroban window —
   confirm zero (or audit each prior hash).
2. Disassemble the vault WASM at hash `0f3073...8f3a` and confirm
   the `deposit` / `withdraw` event-body field names match the
   decoder's expectations.
3. Once Phase 1+2 land, flip `BackfillSafe: true` in
   `internal/sources/external/registry.go`.

Until then, `ratesengine-ops backfill --source=defindex` is
gated off via the registry's `BackfillSafe: false`.

# Aquarius fixtures

Real `stellar-rpc getEvents` captures of Aquarius AMM `trade`
events, used as regression corpus for `internal/sources/aquarius/`
decoders.

## Why pin per WASM hash

Aquarius pool contracts have a `UPGRADE_DELAY = 259200s` (3-day)
governance window and bypassable emergency mode. WASM hashes rotate
on every upgrade; event body schemas can rotate with them. Fixtures
live under `<wasm_hash>/` subdirectories so decoders can be pinned
to specific versions — see
[docs/architecture/contract-schema-evolution.md](../../../docs/architecture/contract-schema-evolution.md).

## Capture workflow

```sh
WASM_HASH=$(your-probe-for-wasm-hash) \
  scripts/dev/capture-aquarius-fixtures.sh \
    -e http://127.0.0.1:8000 \
    -n 10
```

## Event topology

```
topic[0] = Symbol("trade")
topic[1] = Address(token_in)   — sold_asset, Soroban SAC address
topic[2] = Address(token_out)  — bought_asset
topic[3] = Address(user)       — trader (usually Aquarius router
                                 contract, not a G-account)

body     = ScVec[i128, i128, i128]
         = (sold_amount, bought_amount, fee)
```

Body shape is a positional 3-tuple because the emitting Rust code
uses `e.events().publish(topics, (in_amount, out_amount, fee))` —
soroban-sdk serializes plain Rust tuples as `ScvVec`, unlike
`#[contracttype]` structs which produce `ScvMap` with field-name
keys (seen in Soroswap / Reflector).

## Fixture file shape

```json
{
  "contract_id":       "CAB6MICC2WKRT372U3FRPKGGVB5R3FDJSMWSLPF2UJNJPYMBZ76RQVYE",
  "wasm_hash":         "v2-2026-04-23",
  "ledger":            62150001,
  "tx_hash":           "78d86d0651d6...",
  "ledger_closed_at":  "2026-04-16T...",
  "topics":            ["AAAADw...", "AAAAEgA...", "AAAAEgA...", "AAAAEgA..."],
  "value":             "AAAAEA...",
  "event_name":        "trade"
}
```

## Known gaps

- **No deposit_liquidity / withdraw_liquidity / update_reserves
  fixtures.** Out of scope for PR 164c (the decoder currently emits
  only on `trade`). If we add those connectors later, capture them
  under the same `<wasm_hash>/` directory and extend the
  real_fixture_test dispatcher.
- **WASM hash not yet resolved.** Fixtures currently land under
  `v2-2026-04-23/` (semantic-tag + date). Relabel when the ops CLI
  lands a `resolve-wasm` subcommand.

# Phoenix fixtures

Real `stellar-rpc getEvents` captures of Phoenix pool swap events.
Used as regression corpus for `internal/sources/phoenix/` decoder
(which is unusual in that one swap requires assembling **8
separate events** — see below).

## Why the 8-event format

Phoenix's pool contract doesn't publish a structured swap body like
the other Soroban DEXes. Instead,
`contracts/pool/src/contract.rs:1172-1185` emits 8 separate
`publish((<action>, <field>), value)` calls per swap:

```
topic                                  body
("swap", "sender")                     Address(G… or C…)
("swap", "sell_token")                 Address(C… Soroban token)
("swap", "offer_amount")               i128
("swap", "actual received amount")     i128     ← note the spaces in the name
("swap", "buy_token")                  Address
("swap", "return_amount")              i128
("swap", "spread_amount")              i128
("swap", "referral_fee_amount")        i128
```

**Both topic positions are `ScvString`** (not Symbol) — because
the Rust source passes bare string literals, and soroban-sdk
serializes literals as String. `"actual received amount"` can't
be a Symbol anyway because Symbols are identifier-only (no
spaces). Verified 2026-04-23 against mainnet.

**Bodies are raw single-value SCVals** — no Map or Vec wrapper.
Different from:
- Soroswap / Reflector (Map with field-name Symbol keys —
  `#[contractevent]` / `#[contracttype]` structs)
- Aquarius (Vec of positional i128s — plain Rust tuple)

Those three shapes span all three Soroban event body conventions
our scval helper handles.

## Correlation

All 8 events for one swap share `(ledger, tx_hash, operation_index)`.
The decoder's `RawSwap` struct buffers until all 8 slots are
populated (or the TTL eviction kicks in).

## Capture workflow

```sh
WASM_HASH=$(your-probe-for-wasm-hash) \
  scripts/dev/capture-phoenix-fixtures.sh \
    -e http://127.0.0.1:8000 \
    -n 40
```

The script captures all swap-field events from the five known pool
contracts (XLM/USDC, PHO/USDC, XLM/PHO, XLM/EURC, USDC/VEUR),
groups them by (ledger, tx_hash, op_index), and emits one fixture
JSON per **complete** 8-event group. Incomplete groups (e.g.
truncated at the page boundary) are dropped.

## Fixture file shape

```json
{
  "wasm_hash":        "v1-2026-04-23",
  "ledger":           62152147,
  "tx_hash":          "e02dd755d908…",
  "op_index":         0,
  "contract_id":      "CBHCRSVX3ZZ7…",
  "ledger_closed_at": "2026-04-16T23:36:52Z",
  "events": [
    { "topics": ["AAAADg…", "AAAADg…"], "value": "AAAAEg…" },
    … 7 more …
  ]
}
```

`real_fixture_test.go` replays each fixture through `classify()`
+ `RawSwap.assign()` + `decodeSwap()` — the same code path the
runtime consumer uses.

## Known gaps

- **WASM hash not yet resolved.** Fixtures currently land under
  `v1-2026-04-23/` (tag + date). Re-label when the ops CLI lands
  a `resolve-wasm` subcommand.
- **No `pool_stable` swap fixtures.** The discovery doc noted pool-
  stable's swap event wasn't fully read during Phase 1. If mainnet
  shows stable-pool swap events with a different 8-field shape
  we'll need to add them here with their own `<wasm_hash>/`
  directory.
- **No `provide_liquidity` / `withdraw_liquidity` fixtures.** Out
  of scope for PR 164d (decoder only emits on swap). When those
  connectors land, capture under the same WASM subdirectory.

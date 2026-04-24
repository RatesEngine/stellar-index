# Soroswap fixtures

Real `stellar-rpc getEvents` captures of Soroswap `SoroswapPair.swap`,
`SoroswapPair.sync`, and `SoroswapFactory.new_pair` events. Used as
regression corpus for `internal/sources/soroswap/` decoders.

## Why pin per WASM hash

Soroswap's pair contract can `update_contract` without changing its
address. The `#[contracttype]` struct serialization already matters
(Map of field-name Symbols); any future field rename or type widen
breaks decoders compiled against an older schema. Fixtures sit under
`<wasm_hash>/` so regression tests can branch by version — see
[docs/architecture/contract-schema-evolution.md](../../../docs/architecture/contract-schema-evolution.md).

## Capture workflow

```sh
WASM_HASH=$(your-probe-for-wasm-hash) \
  scripts/dev/capture-soroswap-fixtures.sh \
    -e http://127.0.0.1:8000 \
    -n 5
```

The script captures three topics separately:

| Event       | topic[0]            | topic[1]    |
| ----------- | ------------------- | ----------- |
| swap        | `String` "SoroswapPair"    | `Symbol` "swap" |
| sync        | `String` "SoroswapPair"    | `Symbol` "sync" |
| new_pair    | `String` "SoroswapFactory" | `Symbol` "new_pair" |

Byte-encoded topic blobs are pre-computed via
`go run scripts/dev/encode-topics -type string SoroswapPair …`
and hardcoded in the capture script for portability. If
`internal/scval`'s encoder shifts, both the script constants and the
`TopicPrefix*` / `TopicSymbol*` package variables must be
regenerated — the byte-level drift guard in
`internal/sources/soroswap/decode_test.go` catches client-side
mismatches.

## Fixture file shape

```json
{
  "contract_id":       "CATUJXDUO7SSSTAKSUV5YU6RSTB4B5AVIHQDV26QTCXOB46T6SLMWNMY",
  "wasm_hash":         "v1-2026-04-23",
  "ledger":            62240138,
  "tx_hash":           "27ed2ab56d42...",
  "ledger_closed_at":  "2026-04-23T...",
  "topics":            ["AAAADg...", "AAAADw...e"],
  "value":             "AAAAEQ...",
  "event_name":        "swap"
}
```

`real_fixture_test.go` pairs `swap_*` with `sync_*` by `(ledger,
tx_hash, contract_id)` — the same correlation key the runtime
buffer uses — then runs both through `decodeSwap`.

## Known gaps

- **No `new_pair` fixtures yet.** Soroswap pair deployments are
  infrequent; our 100k-ledger mainnet probe found zero. Ops should
  re-run the capture script over a longer retention window
  (self-hosted stellar-rpc on r1 retains ~7 days by default). Until
  then, the `new_pair` decoder is covered by the SDK-encoded tests
  in `decode_test.go`.
- **WASM hash not yet resolved.** Fixtures currently land under
  `v1-2026-04-23/` (tag + date label). When `ratesengine-ops
  resolve-wasm` lands, re-label to the true WASM hash.

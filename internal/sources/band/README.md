# Band connector

Ingests on-chain price updates from
[Band Protocol](https://www.bandprotocol.com)'s Soroban
StandardReference contract. Primary Phase-1 reference:
[`docs/discovery/oracles/band.md`](../../../docs/discovery/oracles/band.md).

## What this ingests

Mainnet address (Phase-1 verified):

| Contract | Address |
| --- | --- |
| StandardReference | `CCQXWMZVM3KRTXTUPTN53YHL272QGKF32L7XEDNZ2S6OSUFK3NFBGG5M` |

Band publishes USD-denominated single-symbol rates on a relayer
cadence (deviation-driven, not a fixed heartbeat). Coverage
includes BTC / ETH / common majors plus a Band-specific symbol
set the relayer maintains off-chain.

## Quirks

### Q1 — **Band emits zero events**

Verified 2026-04-22 via grep across
`bandprotocol/band-std-reference-contracts-soroban`; confirmed
2026-04-24 against the pinned source. A conventional
`dispatcher.Decoder` running on emitted events would **never
fire** for Band.

Instead this package plugs into
`dispatcher.ContractCallDecoder` — it observes the
`InvokeContract` op itself and decodes the relayer's call args as
the authoritative payload. Any future Soroban source whose
contract updates storage without publishing events follows the
same hook: match by `(contract_id, function_name)`, decode from
op args.

### Q2 — Two function signatures, same output

Both produce one `canonical.OracleUpdate` per
`(Symbol, rate)` pair:

| Function | Args | Notes |
| --- | --- | --- |
| `relay` | `(from: Address, symbol_rates, resolve_time, request_id)` | Standard relayer path |
| `force_relay` | `(symbol_rates, resolve_time, request_id)` | Admin-only path; no `from` arg |

The decoder dispatches via
`dispatcher_adapter.Match(contractID, functionName)` — both
function names are in the match list. `force_relay`'s missing
`from` falls back to the op source / tx source so the row still
carries attribution rather than being anonymous.

### Q3 — Two scales: E9 single-symbol, E18 pair

- **Single-symbol rates** (the `symbol_rates` Vec we decode) are
  `u64` at `E9 = 10^9` scale. Per
  `band-soroban/src/constant.rs`. Decoder publishes
  `DefaultDecimals = 9`.
- **Pair rates** computed on-read via `get_reference_data` are
  `E18`. We don't emit those from relay calls because they're a
  function of storage state, not the wire input.

If a future API consumer wants pair rates, they'll be derived
downstream from the published single-symbol updates rather than
re-decoded from a different on-chain endpoint.

### Q4 — USD is special

USD is hardcoded to `1 @ E9` in Band's storage; the relayer
doesn't push USD updates. The decoder treats a `USD` symbol in
`symbol_rates` as expected-no-op (skips emission, does not log
a decode error).

### Q5 — Timestamps in seconds

`resolve_time` is `u64` UNIX seconds, per
`band-soroban/src/storage/ref_data.rs:56` (compared against
`env.ledger().timestamp()` which is seconds). The decoder converts
it with `time.Unix(resolveSeconds, 0)` and stores it on
`canonical.OracleUpdate.Timestamp` (the field is `Timestamp`, not
`PublishedAt`). Out-of-range values (0 / pre-epoch, or a sentinel
far-future u64) fall back to the ledger close time.

### Q6 — Synthetic OpIndex fan-out

A single `relay` call produces N updates (one per
`symbol_rates` entry). Each emitted `OracleUpdate` shares
`(ledger, tx_hash, op_source)` but gets a unique
`OpIndex = base + i*1024` so the `oracle_updates` table primary
key stays distinct without colliding across batches.

## Files

| File | Role |
| --- | --- |
| [`events.go`](events.go) | Function-name + symbol-set constants, error sentinels |
| [`decode.go`](decode.go) | Pure decode-from-OpArgs → `[]canonical.OracleUpdate` |
| [`decode_test.go`](decode_test.go) | Decoder unit tests with synthetic call args |
| [`consumer.go`](consumer.go) | Dispatcher-side adapter glue |
| [`dispatcher_adapter.go`](dispatcher_adapter.go) | `(contract_id, function_name)` match registration |

## Operational notes

- **Class**: Oracle (per `external.Registry`) —
  `IncludeInVWAP=false` by default. Surfaced via `/v1/sources`
  for transparency, excluded from VWAP.
- **No event-based metrics.** Because Band emits no events,
  `ratesengine_source_events_total{source="band"}` will read
  zero. Use op-args ingestion counters
  (`ratesengine_source_orphan_events_total{source="band"}`
  reflects unmatched calls) and the standard cursor advance
  metric to confirm the ContractCallDecoder is firing.
- **Backfill**: supported. ContractCall observation works the
  same way over historical ledgers; the dispatcher routes
  InvokeContract ops to Band's adapter regardless of whether
  the ledger is live or replayed from a galexie archive.
- **Symbol set evolution**: when the relayer adds a new symbol,
  the decoder emits `ErrUnknownSymbol` for entries it can't map
  to a `canonical.Asset`. Adding a new symbol is a one-line
  amendment to the canonical crypto allow-list (ADR-0014) plus
  optional symbol-specific handling in
  [`decode.go`](decode.go).

## Verdict

Adopting Band gives us a third oracle alongside Reflector +
Redstone, at the cost of plumbing through `OpArgs` rather than
events. The ContractCallDecoder hook is reusable — any
event-less Soroban contract that updates storage on a known
function call slots into the same pattern.

## References

- Discovery: [`docs/discovery/oracles/band.md`](../../../docs/discovery/oracles/band.md)
- Band Soroban contract:
  <https://github.com/bandprotocol/band-std-reference-contracts-soroban>
- ADR-0014 — crypto-ticker representation
- Related: [`reflector`](../reflector/README.md),
  [`redstone`](../redstone/README.md) (the other Soroban-native
  oracles on pubnet)

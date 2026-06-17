# Aquarius connector

Ingests Aquarius trade events from Soroban pool contracts. The live
decoder is topic-driven and stateless: token identities are carried in
the event topics, so the dispatcher adapter does not need router reads
or a pool-token cache. See the protocol verification page:
[`docs/protocols/aquarius.md`](../../../docs/protocols/aquarius.md).

## What this ingests

Aquarius has multiple pool shapes. The current runtime only cares
about the common `trade` event shape they share:

1. **Volatile** — constant-product pools.
2. **Stableswap** — stablecoin-oriented pools.
3. **Future variants** — any pool/event shape that does not match the
   current 4-topic `trade` contract is rejected until explicitly
   audited and added.

The decoder emits one `canonical.Trade` per accepted trade event.
Unlike Soroswap, there is no swap+sync correlation buffer.

## Events we care about

| Event | Topic 0 | Carries | Role |
| --- | --- | --- | --- |
| `trade` | `trade` | `token_in`, `token_out`, `user` in topics; sold/bought/fee amounts in body | PRIMARY — single event per trade |
| `deposit_liquidity` | `deposit_liquidity` | LP state change | ignored |
| `withdraw_liquidity` | `withdraw_liquidity` | LP state change | ignored |
| `update_reserves` | `update_reserves` | reserve sync state | ignored |

The live dispatcher matches only `trade`. Other Aquarius event types
are intentionally excluded from the ingest path.

## Quirks

### Q1 — Token identity comes from the event topics

Aquarius `trade` events carry `Address(token_in)`,
`Address(token_out)`, and `Address(user)` directly in topics 1..3.
The decoder derives the pair from those topic values; it does not
consult a router or maintain a pool metadata cache.

### Q2 — Trade bodies are tuple-shaped SCVals

The body is a three-element tuple encoded as `ScvVec`:

1. sold amount (`i128`)
2. bought amount (`i128`)
3. fee (`i128`)

This is why the decoder leans on `internal/scval` rather than ad hoc
XDR parsing.

### Q3 — One event per trade

Aquarius differs from Soroswap and Phoenix operationally:

- Soroswap needs swap+sync correlation.
- Phoenix needs 8-field event fan-in.
- Aquarius emits one complete trade event, so decode is a pure
  single-event function.

## File layout

| File | Purpose |
| --- | --- |
| `README.md` | this file |
| `events.go` | topic symbols and source constants |
| `decode.go` | Aquarius `trade` event -> `canonical.Trade` |
| `dispatcher_adapter.go` | stateless dispatcher-facing decoder |
| `consumer.go` | `consumer.Event` wrapper emitted after decode |
| `decode_test.go`, `adapter_test.go`, `source_test.go`, `topic_decoder_reject_test.go`, `real_fixture_test.go` | unit + reject-path + real-fixture coverage |

## Relationship to other connectors

| Aspect | Soroswap | Phoenix | Aquarius |
| --- | --- | --- | --- |
| Event correlation | swap + sync | 8-field fan-in | none |
| Pair identity | derived from contract/pool context | derived across event set | derived directly from topics |
| Dispatcher seam | stateful decoder | stateful decoder | stateless decoder |

## Status

Production. Topic byte-match dispatching, single-event decode, and
real SCVal parsing all run against real fixtures under
`test/fixtures/aquarius/`.

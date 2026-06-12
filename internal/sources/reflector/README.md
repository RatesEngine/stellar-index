# Reflector oracle connector

First non-DEX source. Reflector is a decentralised oracle network
native to Stellar/Soroban, SEP-40-compliant. Primary Phase-1
reference:
[`docs/discovery/oracles/reflector.md`](../../../docs/discovery/oracles/reflector.md).

## What this ingests

**Reflector is three separate contracts**, not one — a correction
flagged in Phase-1 against our proposal. Each contract's feed is
a different upstream data source:

| Contract | Feed | Asset shape |
| --- | --- | --- |
| Reflector **DEX** | On-chain Stellar DEX prices | `Asset::Stellar(Address)` |
| Reflector **CEX** | Aggregated CEX prices | `Asset::Other(Symbol)` (e.g. "BTC") |
| Reflector **FX** | Fiat + commodity FX pairs | `Asset::Other(Symbol)` (e.g. "EURUSD") |

Each contract emits the **same event shape** on every price
update. This connector handles all three via one event schema
but with per-contract SourceName attribution
(`reflector-dex` / `reflector-cex` / `reflector-fx`) so alerts,
cursors, and divergence checks can break them out.

## Event model — one event, N updates

Verified from `reflector-contract/oracle/src/events.rs` during
Phase 1.

```
topic:  ["REFLECTOR", "update", <timestamp: u64 ms>]   (Symbols + a u64)
body:   Map{ "update_data": Vec<(Val, i128)> }          // [(asset, price), ...]
```

The timestamp lives in **topic[2]** as a u64 in **milliseconds**
(the contract's internal scale — `oracle/src/price_oracle.rs`
divides by 1000 to expose seconds via `last_timestamp`), NOT in the
body. The body is a Map with a single `update_data` key holding the
(asset, price) vector. (Earlier revisions of this README described a
`body: Map{prices, timestamp}` shape — that was never the wire form;
see `decodeUpdateBody` + `decodeUpdateTimestamp` in `decode.go`.)

Decoding one event produces **one canonical.OracleUpdate per
(asset, price) pair** in the update_data vector. Typical event
carries 30–50 prices on the CEX contract, fewer on DEX + FX.

## Quirks

### Q1 — Price scale is contract-declared

Reflector prices are i128 at a scale set by the contract's
`decimals()` SEP-40 method (typically 14). We store the raw i128
in `canonical.OracleUpdate.Price` + the decimals in
`.Decimals` — never float. The display-layer scales on read.

### Q2 — No on-chain `twap` / `x_*` methods

Our proposal originally claimed Reflector exposes `twap`, `x_twap`,
`x_last_price` etc. on-chain. Phase-1 verified those methods do NOT
exist on Reflector v3. We compute TWAP + cross-pair locally from
`lastprice` / `prices` history. Not this package's job, but worth
knowing when integrating: `internal/aggregate` handles that math.

### Q3 — Resolution is uniform 5 min per contract

Every contract on mainnet updates on a 5-min cadence. Exception: a
contract can go silent if its upstream halts (CEX aggregator outage,
etc.). Our `oracle-stale` alert (alerts-catalog §Divergence) fires
at > 10× the declared resolution = 50 min without an update.

### Q4 — Relayer identity available

The `tx.source_account` of the update transaction is the relayer
that submitted this batch. Each Reflector contract has a known set
of ~3–5 relayers. We stash that in `canonical.OracleUpdate.Observer`
so divergence analysis can detect a single relayer compromise.

### Q5 — Addresses

Mainnet contract addresses (Reflector v3, public — see the
address tables in
[`docs/discovery/oracles/reflector.md`](../../../docs/discovery/oracles/reflector.md)):

| Contract | Mainnet address | Owner |
| --- | --- | --- |
| Reflector DEX | `CALI2BYU2JE6WVRUFYTS6MSBNEHGJ35P4AVCZYF3B6QOE3QKOB2PLE6M` | Reflector DAO |
| Reflector CEX | `CAFJZQWSED6YAWZU3GWRTOCNPPCGBN32L7QV43XX5LZLFTK6JLN34DLN` | Reflector DAO |
| Reflector FX | `CBKGPWGKSKZF52CFHMTRR23TBWTPMRDIYZ4O2P5VS65BMHYH4DXMCJZC` | Reflector DAO |

Verify via stellar.expert before pasting into config — the DAO
can rotate addresses on a v4 spawn, in which case the discovery
doc gets the update first.

Operators set each via `[oracle.reflector]` in TOML; each gets
its own Source instance via `NewDEX()` / `NewCEX()` / `NewFX()`
helpers, with independent backfill state and metric labels
(`reflector-dex` / `reflector-cex` / `reflector-fx`).

## File layout (five-file convention)

| File | Purpose |
| --- | --- |
| `README.md` | this file |
| `events.go` | Source-name constants (3 variants), topic-symbol placeholders, decimals defaults, errors |
| `decode.go` | one-event → []canonical.OracleUpdate (Vec iteration + identity synthesis) |
| `consumer.go` | exports `UpdateEvent` (the `consumer.Event` payload the dispatcher seam emits per oracle update). Historical name; does not implement the legacy `consumer.Source` orchestrator interface. |
| `dispatcher_adapter.go` | topic-match + decode registration with `internal/dispatcher` — the production seam |
| `source_test.go` | unit tests against fake SCVal decoder hooks |

## Relationship to DEX sources

Same `dispatcher.Decoder` interface, same dispatcher seam.
Differences that shape the code:

| Aspect | DEXes | Reflector |
| --- | --- | --- |
| Emits | canonical.Trade | canonical.OracleUpdate |
| Event → records | 1-1 (or N-1 with correlation) | 1 → N |
| Asset resolution | Token contract addresses / classic | Includes Asset::Other(Symbol) for off-chain |
| Contract count | 1 per pool (Soroswap) or 1 router (Aquarius) | 3 independent contracts |

The ingestion side (dispatcher route table, indexer event-sink)
gains a new case arm to persist `OracleUpdate` via
`store.InsertOracleUpdate`.

## Status

Production. Contract address seeding, event correlation, and
the Vec<(Asset, i128)> SCVal payload decoding (including the
`Asset::Stellar(Address)` vs `Asset::Other(Symbol)` discriminant
switch) all run via `internal/scval` (ADR-0013). Real-mainnet
event fixtures live in `test/fixtures/reflector/v6-2026-04-23/`
and run on every `go test` cycle.

Set the pattern for [**Redstone**](../redstone/) (per-feed
contracts + Adapter-only event surface) and [**Band**](../band/)
(native StandardReference contract; observed via
`ContractCallDecoder` because the contract emits zero events).

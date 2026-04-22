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
topic:  ["REFLECTOR", "update"]          (both Symbols, indexed)
body:   Map{
           "prices":   Vec<(Asset, i128)>   // [(asset, price), ...]
           "timestamp": u64
         }
```

Decoding one event produces **one canonical.OracleUpdate per
(asset, price) pair** in the prices vector. Typical event carries
30–50 prices on the CEX contract, fewer on DEX + FX.

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

Mainnet contract addresses (TBC via stellar.expert — Phase-1 flagged
as unverified at audit time; we fill these at operator-config time
rather than hard-coding):

| Contract | Mainnet address | Owner |
| --- | --- | --- |
| Reflector DEX | `TBD via config` | SDF + Reflector team |
| Reflector CEX | `TBD via config` | same |
| Reflector FX | `TBD via config` | same |

Operator supplies each via config + each gets its own Source
instance via `NewDEX()` / `NewCEX()` / `NewFX()` helpers.

## File layout (five-file convention)

| File | Purpose |
| --- | --- |
| `README.md` | this file |
| `events.go` | Source-name constants (3 variants), topic-symbol placeholders, decimals defaults, errors |
| `decode.go` | one-event → []canonical.OracleUpdate (Vec iteration + identity synthesis) |
| `consumer.go` | implements consumer.Source; emits UpdateEvent |
| `source_test.go` | unit tests against fake SCVal decoder hooks |

## Relationship to DEX sources

Same consumer.Source interface, same orchestrator integration.
Differences that shape the code:

| Aspect | DEXes | Reflector |
| --- | --- | --- |
| Emits | canonical.Trade | canonical.OracleUpdate |
| Event → records | 1-1 (or N-1 with correlation) | 1 → N |
| Asset resolution | Token contract addresses / classic | Includes Asset::Other(Symbol) for off-chain |
| Contract count | 1 per pool (Soroswap) or 1 router (Aquarius) | 3 independent contracts |

The ingestion side (orchestrator, indexer event-sink) gains a new
case arm to persist `OracleUpdate` via
`store.InsertOracleUpdate`.

## Phase status

**Skeleton.** Contract address seeding + event correlation work
is present; SCVal XDR decoding of the Vec<(Asset, i128)> payload
is stubbed behind `decoderHooks` pending the SDK-dep PR. The
address-type switch (Stellar / Other) within SCVal Asset decoding
is the tricky bit that needs real XDR; the rest is bookkeeping.

Sets the pattern for **Redstone** (per-feed contracts + adapter)
and **Band** (native StandardReference contract) connectors.

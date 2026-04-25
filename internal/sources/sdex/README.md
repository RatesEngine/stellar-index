# SDEX connector

Ingests classic-Stellar DEX trades from `LedgerCloseMeta`
operation results. Unlike the Soroban source packages,
SDEX **doesn't emit contract events** — trades are implicit in
the results of classic offer / path-payment ops, surfaced via
`OffersClaimed` slices on the operation result. ADR-0001 rules
out Horizon-style classic-DEX APIs, so we parse XDR directly.

## What this ingests

The dispatcher routes a five-strong set of classic op types to
this decoder. Each can produce zero or more trades (one per
matched offer) per op:

| Operation | Op type code | Trade source |
| --- | --- | --- |
| `ManageSellOffer` | `OperationTypeManageSellOffer` | `OffersClaimed` |
| `ManageBuyOffer` | `OperationTypeManageBuyOffer` | `OffersClaimed` |
| `CreatePassiveSellOffer` | `OperationTypeCreatePassiveSellOffer` | `OffersClaimed` (shared `ManageSellOfferResult` union arm) |
| `PathPaymentStrictReceive` | `OperationTypePathPaymentStrictReceive` | `OffersClaimed` |
| `PathPaymentStrictSend` | `OperationTypePathPaymentStrictSend` | `OffersClaimed` |

## Quirks

### Q1 — No events, just op-result XDR

Every other source decodes a Soroban `events.Event`. SDEX
decodes the raw `xdr.OperationResult` instead — the dispatcher
hands this package the `(op, result)` pair via its
`OpContext` seam. Yielding one `canonical.Trade` per
`ClaimAtom` mirrors the OrderBook + LiquidityPool semantics
exactly: a single op can sweep multiple resting offers and emit
multiple trades.

### Q2 — Three ClaimAtom variants

`xdr.ClaimAtom` is a union with three arms; the decoder handles
two and surfaces the third as a hard error (it shouldn't occur
on post-P18 ledgers but warrants surfacing if it does):

| Variant | Status | Notes |
| --- | --- | --- |
| `ClaimAtomTypeOrderBook` | Decoded | Standard SDEX order-book match |
| `ClaimAtomTypeLiquidityPool` | Decoded | AMM-side fill against a pool, post-P18 |
| `ClaimAtomTypeV0` | `ErrUnknownClaimAtomType` | Legacy pre-P18 shape — should not appear on a healthy modern stream; sustained-rate alert via `ratesengine_source_decode_errors_total{source="sdex"}` |

### Q3 — Op succeeded vs op succeeded with zero trades

`extractClaimAtoms` returns `nil` for both:

- The op didn't reach `OperationResultCodeOpInner` (op failed at
  the tx-level).
- The op succeeded but `OffersClaimed` is empty (e.g. a
  `ManageSellOffer` that placed an offer at the spread without
  immediately matching).

Both are "no trades to emit" — neither is a decode error. The
`canonical.Trade` count gauge naturally stays low for ops that
post resting orders without executing.

### Q4 — CAP-67 unified events (post-P23) — orthogonal

Post-P23 (Whisk, mainnet 2025-09-03) every classic asset
movement also emits a unified transfer/mint/burn event with a
4th `sep0011_asset` topic. **Those are routed to a different
decoder** — they carry transfer semantics, not match-against-
offer semantics. SDEX still owns the trade-extraction path
because the unified events don't include the cross-leg
denomination and quote that an order-book or LP match
naturally has.

See
[`docs/discovery/notes/cap-67-unified-events.md`](../../../docs/discovery/notes/cap-67-unified-events.md).

### Q5 — Reserve / volume normalisation is the ingest stamp

SDEX trade amounts arrive at native Stellar precision — XLM at
7 decimals, classic assets at issuer-declared decimals. The
decoder stamps `canonical.Trade.BaseAmount` / `QuoteAmount` at
those precisions. The aggregator (per ADR-0003 + the aggregator's
class filter) treats SDEX as `ClassExchange` — it contributes
to VWAP at full precision; no scale rewrite needed.

## Files

| File | Role |
| --- | --- |
| [`events.go`](events.go) | `SourceName` constant + decode error sentinels |
| [`decode.go`](decode.go) | `matchesTradeOp` / `extractClaimAtoms` / `claimAtomToTrade` |
| [`decode_test.go`](decode_test.go) | Decoder unit tests covering OrderBook + LiquidityPool variants |
| [`dispatcher_adapter.go`](dispatcher_adapter.go) | Op-type registration with the dispatcher's `OpDecoder` seam |

(No `consumer.go` because SDEX has no live-stream consumer of
its own — every trade arrives via the dispatcher's per-ledger
fanout. Live ingest, backfill, and replay use the same path.)

## Operational notes

- **Class**: Exchange (per `external.Registry`) —
  `IncludeInVWAP=true` by default. SDEX produces real executed
  trades; it's a primary VWAP contributor.
- **Backfill**: supported. The op-result decoder works the same
  way over historical ledgers as it does live. SDEX trades go
  back to genesis.
- **Decode-error budget**: `ErrUnknownClaimAtomType` should
  remain at zero on modern ledgers. A non-zero rate signals
  either a protocol bump we haven't audited or replay over very
  old (pre-P18) ledgers carrying the V0 atom shape.

## References

- Reference implementation hint:
  `go-stellar-sdk/stellar-extract/trades.go` covers OrderBook +
  V0; we add LiquidityPool on top.
- ADR-0001 — "Horizon is not in our architecture"; explains why
  this package decodes raw XDR rather than calling a classic-DEX
  HTTP API.
- [`docs/discovery/notes/cap-67-unified-events.md`](../../../docs/discovery/notes/cap-67-unified-events.md)
  — interaction with the post-P23 unified-event surface.
- Related source: [`soroswap`](../soroswap/README.md) (the
  Soroban-DEX equivalent). Soroswap and SDEX trade
  independently; both contribute to VWAP.

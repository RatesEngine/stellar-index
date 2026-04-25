# Comet connector

Ingests trade events from [Comet](https://github.com/CometDEX/comet-contracts-v1)
— a Balancer-v1-derived weighted AMM running on Soroban. Primary
Phase-1 reference:
[`docs/discovery/dexes-amms/comet.md`](../../../docs/discovery/dexes-amms/comet.md).

## What this ingests

Comet uses Balancer-v1's weighted-pool math: each token in the
pool has a configurable weight, and the spot price between any two
tokens is a function of their reserves AND their weights. Pools
are N-token (up to 8 in Balancer-v1; Stellar limit unconfirmed).

**The most visible Comet pool on pubnet is Blend's BLND/USDC
backstop** — a `comet.wasm` is vendored at the root of
`blend-contracts/`. Blend uses the Comet pool as its backstop LP,
so even a pubnet without a standalone Comet DEX gives us BLND
pricing for free once this decoder runs.

Whether there is a *separate* Comet DEX with public trading pools
beyond Blend's backstop is an open question — see Open items in
the discovery doc.

## Topic shape — shared, not per-protocol

**Comet uses `("POOL", <event_name>)` for every event** — note the
uppercase `POOL` symbol, not a per-protocol namespace. Every pubnet
contract that deploys Balancer-v1 Comet code looks identical on
the wire.

The decoder dispatches on topic-bytes match (`Topic[0] == "POOL" &&
Topic[1] == "swap"`), not on contract address. Operators who want
narrow coverage (e.g. only Blend's backstop) filter downstream by
`Trade.Source == "comet"` plus the contract address that came
through `events.Event.ContractID` — not at dispatch time.

## Events we care about

| Event | Topic | Carries | Role |
| --- | --- | --- | --- |
| `swap` | `("POOL", "swap")` | caller, token_in, token_out, token_amount_in, token_amount_out (all i128) | PRIMARY — emits one row per swap; decoded directly to `canonical.Trade` |
| `join_pool` | `("POOL", "join_pool")` | LP join — pool reserves grow | LP state (not a trade) |
| `exit_pool` | `("POOL", "exit_pool")` | LP exit — pool reserves shrink | LP state |
| `deposit` / `withdraw` | `("POOL", "deposit"/"withdraw")` | single-asset LP add/remove | LP state |

v1 of this decoder only handles `swap`. The other events are
recognised in the topic catalogue
([`events.go`](events.go)) so the dispatcher logs unhandled
varieties rather than no-ops.

## Quirks

### Q1 — Token identities live in the event body

Unlike Soroswap (where pair contracts emit swap events without
token identities and need a factory→tokens registry), Comet's
`SwapEvent` carries `token_in` and `token_out` as `Address` fields
in the body itself. The decoder needs no pool registry. Cold-start
backfill works the same way live ingest does — token identities
arrive every event.

### Q2 — Trade direction

The trader sold `token_in` (into the pool) and bought `token_out`
(out of the pool). So `base = token_in`, `quote = token_out` —
matches the Aquarius convention where the "sold" side is the base.

### Q3 — Spot price is weight-aware, but the swap event is not

The executed price per swap is `token_amount_out / token_amount_in`
— a simple ratio carried directly in the event body. We use this
verbatim for `canonical.Trade.QuoteAmount / BaseAmount`.

The reserve-implied **spot price** between two pool tokens, by
contrast, requires both reserves AND both weights:

```text
spot_price_out_per_in =
    (reserve_in  / weight_in) /
    (reserve_out / weight_out) * (1 + swap_fee)
```

Spot-price tracking would require a pool-state tracker capturing
weight alongside reserves — out of scope for the trade-event
decoder. v1 reports executed swap prices only; spot inference is
a follow-up if a Phase-2+ requirement emerges.

### Q4 — `i128` everywhere

Every amount in every Comet event is `i128`. We parse via
`internal/scval` to `*big.Int`, never `int64`. Standard
[ADR-0003 invariant](../../../docs/adr/) applies.

### Q5 — Unhandled-event budget

Joins / exits / deposits / withdraws aren't trades, so they're
expected to outnumber swaps in any window. The dispatcher counts
them under `ratesengine_source_orphan_events_total{source="comet"}`
— operators alert on a *spike* in that counter, not its absolute
rate.

## Files

| File | Role |
| --- | --- |
| [`events.go`](events.go) | Topic constants + `Event` wrapper kind for the dispatcher seam |
| [`decode.go`](decode.go) | Pure decode-from-event → `canonical.Trade` |
| [`decode_test.go`](decode_test.go) | Decoder unit tests with synthetic event bodies |
| [`consumer.go`](consumer.go) | Dispatcher-side adapter glue |
| [`dispatcher_adapter.go`](dispatcher_adapter.go) | Topic-match registration |

## Verdict

Low-priority compared to Soroswap / Aquarius / Phoenix on its own
— but Blend's backstop pool gives us BLND pricing at near-zero
additional cost. Decoder reuses the same i128 + scval-Map shape
as the other Soroban AMM sources, so wiring it up doesn't add a
new pattern.

## References

- Discovery: [`docs/discovery/dexes-amms/comet.md`](../../../docs/discovery/dexes-amms/comet.md)
- Comet contracts: <https://github.com/CometDEX/comet-contracts-v1>
- Balancer v1 whitepaper (for the weighted-AMM math):
  <https://balancer.fi/whitepaper.pdf>
- Related sources: [`soroswap`](../soroswap/README.md),
  [`aquarius`](../aquarius/README.md),
  [`phoenix`](../phoenix/README.md).

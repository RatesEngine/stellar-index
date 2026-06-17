# Comet connector

Ingests trade + liquidity events from [Comet](https://github.com/CometDEX/comet-contracts-v1)
— a Balancer-v1-derived weighted AMM running on Soroban.

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
beyond Blend's backstop is an open question.

## Topic shape — shared, not per-protocol

**Comet uses `("POOL", <event_name>)` for every event** — note the
uppercase `POOL` symbol, not a per-protocol namespace. Every pubnet
contract that deploys Balancer-v1 Comet code looks identical on
the wire.

The decoder dispatches on topic-bytes match (`Topic[0] == "POOL"`
plus topic[1] ∈ {swap, join_pool, exit_pool, deposit, withdraw}),
not on contract address. Operators who want narrow coverage (e.g.
only Blend's backstop) filter downstream by `Trade.Source == "comet"`
or `LiquidityEvent.ContractID`, not at dispatch time.

## Events we handle

All five events the Soroban port of Balancer-v1 emits. Verified
2026-05-26 against upstream `main`
(`contracts/src/c_pool/{event.rs, call_logic/pool.rs}`).

| Event | Topic | Body | Output |
| --- | --- | --- | --- |
| `swap` | `(POOL, swap)` | caller, token_in, token_out, token_amount_in, token_amount_out — all i128 | `canonical.Trade` → `trades` |
| `join_pool` | `(POOL, join_pool)` | caller, token_in, token_amount_in | `LiquidityEvent{kind=join_pool, direction=add}` → `comet_liquidity` |
| `exit_pool` | `(POOL, exit_pool)` | caller, token_out, token_amount_out | `LiquidityEvent{kind=exit_pool, direction=remove}` → `comet_liquidity` |
| `deposit` | `(POOL, deposit)` | caller, token_in, token_amount_in | `LiquidityEvent{kind=deposit, direction=add}` → `comet_liquidity` |
| `withdraw` | `(POOL, withdraw)` | caller, token_out, token_amount_out, pool_amount_in | `LiquidityEvent{kind=withdraw, direction=remove, pool_amount_in=…}` → `comet_liquidity` |

`join_pool` and `exit_pool` are loop-emitted: an N-token join
produces N events from the same `(ledger, tx_hash, op_index)`,
each carrying one token's amount. The `comet_liquidity` PK
includes `token` so the per-token rows don't collide.

## What this does NOT handle (and why)

The task brief listed several "classic Balancer-v1" events
(`bind`, `rebind`, `unbind`, `finalize`, `gulp`, `set_swap_fee`,
`set_controller`, `set_public_swap`, BPT `transfer`). Verifying
2026-05-26 against upstream confirms:

- **`bind` / `rebind` / `unbind` / `finalize`** — these functions
  **do not exist in the Soroban port**. Comet's pool is initialised
  in one shot via `init(controller, tokens, weights, balances,
  swap_fee)` and the token+weight set is fixed at deploy time. No
  events to claim.
- **`set_swap_fee` / `set_public_swap`** — likewise absent from
  the Soroban port.
- **`set_controller`** — the function *exists* but the Soroban port
  does **not** publish an event for it. A future contract upgrade
  that adds one would surface as a new `(POOL, set_controller)`
  topic and the dispatcher would route it to our `Decode`, which
  rejects with `ErrNotCometEvent` until a handler is added.
- **`gulp`** — the function exists (absorbs tokens sent directly to
  the contract) but does not publish an event.
- **BPT (Balancer Pool Token) `transfer`** — emitted via the
  **SEP-41 standard token-event surface**, not the `POOL`
  namespace. Already claimed by `internal/sources/sep41_supply`
  when the pool contract is in scope; re-decoding it here would
  double-count.

If a future Soroban Comet upgrade starts emitting any of these,
the dispatcher will route them to `Decoder.Decode` (because
`Matches` claims any `(POOL, *)` we recognise — and the unknown
kinds fall through `Matches → false`, contributing to the
`source_orphan_events_total{source="comet"}` counter). Operators
alert on a sustained spike of that counter, then a follow-up PR
extends `classify`.

## Quirks

### Q1 — Token identities live in the event body

Unlike Soroswap (where pair contracts emit swap events without
token identities and need a factory→tokens registry), Comet's
`SwapEvent` carries `token_in` and `token_out` as `Address` fields
in the body itself. The decoder needs no pool registry. Cold-start
backfill works the same way live ingest does — token identities
arrive every event. Same is true for every liquidity event: the
participating `token` is in the body.

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
a follow-up if the requirement emerges.

### Q4 — `i128` everywhere

Every amount in every Comet event is `i128`. We parse via
`internal/scval` to `*big.Int`, never `int64`. Standard
[ADR-0003 invariant](../../../docs/adr/) applies. The
`comet_liquidity.amount` and `pool_amount_in` columns are
NUMERIC; the integration test exercises a >`int64`-max value.

### Q5 — `pool_amount_in` is withdraw-only

Only `WithdrawEvent` carries `pool_amount_in` — the count of BPT
(pool-share) tokens burned in exchange for the underlying
withdrawn. The other three liquidity events do not have this
field; the `pool_amount_in` column in `comet_liquidity` is NULL
on those rows. The writer rejects a zero/missing `pool_amount_in`
on a withdraw row (the contract always burns BPT shares to fund
a withdraw — a zero burn would be a bug).

## Historical fill

Live ingest from the rc that adds 0042 onwards writes
`comet_liquidity` directly. The pre-rc back-window can be filled
from `soroban_events` (migration 0041) once an operator schedules
a per-source backfill — query shape:

```sql
INSERT INTO comet_liquidity (
    contract_id, ledger, ledger_close_time, tx_hash, op_index,
    event_kind, direction, caller, token, amount, pool_amount_in
)
SELECT
    contract_id, ledger, ledger_close_time, tx_hash, op_index,
    topic_0_sym,  -- (after substituting via the topic_1_sym mapping)
    CASE topic_0_sym
        WHEN 'join_pool' THEN 'add'  WHEN 'deposit'  THEN 'add'
        WHEN 'exit_pool' THEN 'remove' WHEN 'withdraw' THEN 'remove'
    END,
    -- … plus SCVal extraction of caller/token/amount/pool_amount_in
    ...
FROM soroban_events
WHERE topic_0_sym = 'POOL'
  AND ledger_close_time BETWEEN <pre-rc cut-off> AND now();
```

The SCVal-body extraction in SQL is more painful than re-using the
Go decoder; a `stellarindex-ops comet-backfill` subcommand that
walks `soroban_events` and calls `decodeLiquidityEvent` in-process
is the cleaner path. Tracked as a follow-up.

## Files

| File | Role |
| --- | --- |
| [`events.go`](events.go) | Topic constants + consumer.Event types (TradeEvent, LiquidityEvent) + LiquidityKind helpers |
| [`decode.go`](decode.go) | Pure decode-from-event → typed structs (Trade, LiquidityEvent) |
| [`decode_test.go`](decode_test.go) | Decoder unit tests with synthetic swap event bodies |
| [`decode_body_reject_test.go`](decode_body_reject_test.go) | Body-malformation reject paths for swap |
| [`decode_liquidity_test.go`](decode_liquidity_test.go) | Decoder unit tests for join_pool / exit_pool / deposit / withdraw |
| [`consumer.go`](consumer.go) | Dispatcher-side adapter glue + LiquidityEvent type |
| [`dispatcher_adapter.go`](dispatcher_adapter.go) | Topic-match registration; routes swap → TradeEvent, liquidity events → LiquidityEvent |
| [`adapter_test.go`](adapter_test.go) | Adapter routing tests (swap path) |
| [`adapter_liquidity_test.go`](adapter_liquidity_test.go) | Adapter routing tests (liquidity path) |

## Storage

| Event | Hypertable | Migration |
| --- | --- | --- |
| `swap` | `trades` | 0001 |
| `join_pool` / `exit_pool` / `deposit` / `withdraw` | `comet_liquidity` | 0042 |
| BPT transfer (SEP-41 standard event) | `sep41_supply_events` (when pool in scope) | 0015 |

## Verdict

Low-priority compared to Soroswap / Aquarius / Phoenix on its own
— but Blend's backstop pool gives us BLND pricing at near-zero
additional cost. Decoder reuses the same i128 + scval-Map shape
as the other Soroban AMM sources, so wiring it up doesn't add a
new pattern. The liquidity-event tables now give us per-pool LP
flow visibility (depth changes, LP-user behaviour) on top of the
already-captured swap volume.

## References

- WASM audit: [`docs/operations/wasm-audits/comet.md`](../../../docs/operations/wasm-audits/comet.md)
- Comet contracts: <https://github.com/CometDEX/comet-contracts-v1>
- Balancer v1 whitepaper (for the weighted-AMM math):
  <https://balancer.fi/whitepaper.pdf>
- Related sources: [`soroswap`](../soroswap/README.md),
  [`aquarius`](../aquarius/README.md),
  [`phoenix`](../phoenix/README.md).

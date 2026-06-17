# Phoenix DEX connector

Ingests trade events from [Phoenix](https://app.phoenix-hub.io) —
a Stellar-native DEX with x·y=k + stableswap pools. See the
protocol verification page:
[`docs/protocols/phoenix.md`](../../../docs/protocols/phoenix.md).

## What this ingests

Phoenix's event model is **high-cardinality and unusual** relative to
Soroswap / Aquarius / Blend / Comet. Verified from Phoenix's
`contracts/pool/src/contract.rs:1172-1185`:

A single `swap` on a Phoenix pool emits **8 separate Soroban
events**, each with a 2-tuple topic `("swap", "<field_name>")` and a
one-value body:

| Topic[0] | Topic[1] | Body type |
| -------- | -------- | --------- |
| `swap` | `sender` | Address |
| `swap` | `sell_token` | Address |
| `swap` | `offer_amount` | i128 |
| `swap` | `actual received amount` | i128 (note: spaces in key) |
| `swap` | `buy_token` | Address |
| `swap` | `return_amount` | i128 |
| `swap` | `spread_amount` | i128 |
| `swap` | `referral_fee_amount` | i128 |

The same N-events-per-action pattern services Phoenix's
liquidity-management events (Task #27, contracts/pool/src/contract.rs
lines 346-355 / 501-508) and the per-pool stake contract
(contracts/stake/src/contract.rs lines 165-167 / 196-198):

| Action | Topic[0] | Events | Required fields |
| ------ | -------- | ------ | --------------- |
| swap | `swap` | 8 | sender, sell_token, offer_amount, actual received amount, buy_token, return_amount, spread_amount, referral_fee_amount |
| provide_liquidity | `provide_liquidity` | 5 | sender, token_a, token_a-amount, token_b, token_b-amount |
| withdraw_liquidity | `withdraw_liquidity` | 4 (+1 optional) | sender, shares_amount, return_amount_a, return_amount_b (+ `auto unbonded` when the withdrawal also unbonds) |
| bond | `bond` | 3 | user, token, amount |
| unbond | `unbond` | 3 | user, token, amount |

`provide_liquidity` and `withdraw_liquidity` write to the
`phoenix_liquidity` hypertable (migration 0044). `bond` and `unbond`
write to `phoenix_stake_events` (also 0044). The `withdraw_liquidity`
optional 5th `auto unbonded` event is recognised so it doesn't fall
into `ErrUnknownField`, but is intentionally discarded — the stake
contract's `unbond` event carries the same data through a more
authoritative channel.

To reconstruct each action we **must group its N events** by
`(ledger, tx_hash, op_index)` and assemble them into a single
record. This is the third event-correlation shape our consumer fleet
handles:

| Shape | Example | Events per trade |
| ----- | ------- | ---------------- |
| 1-event | Aquarius | one `trade` event carries everything |
| 2-event | Soroswap | `swap` + `sync` (Q1) |
| 8-event | **Phoenix** | one event per field |

## Mainnet addresses (verified on-chain)

| Contract | Address |
| --- | --- |
| Factory | `CB4SVAWJA6TSRNOJZ7W2AWFW46D5VR4ZMFZKDIKXEINZCZEGZCJZCKMI` |
| Multihop | `CCLZRD4E72T7JCZCN3P7KNPYNXFYKQCL64ECLX7WP5GNVYPYJGU2IO2G` |
| XLM SAC (Phoenix-specific) | `CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC` |

Pools (assumed stable):

| Pair | Pool |
| --- | --- |
| PHO / USDC | `CD5XNKK3B6BEF2N7ULNHHGAMOKZ7P6456BFNIHRF4WNTEDKBRWAE7IAA` |
| XLM / PHO | `CBCZGGNOEUZG4CAAE7TGTQQHETZMKUT4OIPFHHPKEUX46U4KXBBZ3GLH` |
| XLM / USDC | `CBHCRSVX3ZZ7EGTSYMKPEFGZNWRVCSESQR3UABET4MIW52N4EVU6BIZX` |
| XLM / EURC | `CBISULYO5ZGS32WTNCBMEFCNKNSLFXCQ4Z3XHVDP4X4FLPSEALGSY3PS` |
| USDC / VEUR | `CDQLKNH3725BUP4HPKQKMM7OO62FDVXVTO7RCYPID527MZHJG2F3QBJW` |

VEUR (tokenised EUR) and EURC (Circle euro) both useful for FX
coverage. PHO is Phoenix's governance token.

## Quirks

### Q1 — 8-event correlation window

The decoder buffers by `(ledger, tx_hash, op_index)` and waits for
all 8 expected fields. Two natural edge cases:

- **Partial set at page boundary.** An RPC page ends between the
  5th and 6th event; the 6–8th events land in the next page. Our
  buffer persists across pages, so the trade reconstructs
  correctly.
- **Event emission order.** The contract emits events in a specific
  order but we do NOT rely on it — the decoder identifies each
  event by topic[1] (the field name) and populates the matching
  slot, regardless of arrival order.

Out-of-order arrival is unlikely on-chain but easy to synthesise in
tests, so we prove robustness there.

### Q2 — Key "actual received amount" has embedded spaces

Phoenix's field-name symbol literally contains spaces (`"actual
received amount"`). This is legal SCVal symbol syntax but unusual.
The decoder byte-matches the full SCVal blob — nothing special
required — but it's worth knowing when reading bug reports.

### Q3 — Return vs actual-received

Phoenix distinguishes:
- `return_amount` = computed swap output before fees.
- `actual received amount` = after-fee amount the buyer saw.

For our canonical.Trade.QuoteAmount we use
`actual received amount` — that's what actually changed hands.
`return_amount - actual_received` is the fee. **Not surfaced
today** — `canonical.Trade` has no `Fee` field; if we add one,
the decoder already has the value to populate it.

### Q4 — Multihop expands to N×8 events

A Phoenix multihop swap passes through N pools and emits 8 events
per pool — so 16 events for a 2-hop, 24 for a 3-hop. A router
multihop emits all N swaps within a **single** op_index (the router
op), so op_index alone does NOT separate them. The decode buffer
emits-and-clears each 8-field swap before the next, so each swap's
first-field `event_index` is distinct; we fan the per-swap trade
op_index out on that event_index (`canonical.FanoutOpIndex`) so the
N trades for one multihop don't collide on the trades PK
(ADR-0033, same as aquarius/comet/soroswap). See `RawSwap.EventIndex`
in `decode.go`.

### Q5 — Stableswap pool contract emits a different schema

`contracts/pool_stable/src/contract.rs` uses a similar but
distinct event shape. **Not decoded yet.** The volatile
(`contracts/pool/`) schema above is the only one we handle in this
first implementation. Stableswap pool support is deferred until
after the volatile path has soaked in production — the field set
needs enumeration from a stableswap-pool fixture capture, and we
don't want to ship a partial decoder.

## File layout (five-file convention)

| File | Purpose |
| --- | --- |
| `README.md` | this file |
| `events.go` | 8 field-name constants + their SCVal topic constants + mainnet addresses + errors |
| `decode.go` | 8-event correlation buffer + single-Trade emission, decoded via `internal/scval` |
| `consumer.go` | the in-memory 8-event correlation buffer + `Event` wrapper the dispatcher seam emits. (Historical name; does not implement the legacy `consumer.Source` orchestrator interface — production routing is via `dispatcher.Decoder`.) |
| `dispatcher_adapter.go` | topic-match + decode registration with `internal/dispatcher` — the production seam |
| `decode_test.go`, `source_test.go`, `real_fixture_test.go` | unit + happy-path-and-orphan + real-mainnet-fixture tests |

## Status

Production for volatile (constant-product) pools — swaps + liquidity
management + LP staking. The 8-event swap, 5-event
`provide_liquidity`, 4-event `withdraw_liquidity`, and 3-event
`bond`/`unbond` correlation buffers all decode against real
fixtures and write to `trades`, `phoenix_liquidity`, and
`phoenix_stake_events` respectively. The 5th `auto unbonded`
optional event on withdraws is recognised + discarded
(intentionally).

Stableswap pool support is **partially shipped** — Phoenix's stable
pool emits identical `provide_liquidity` / `withdraw_liquidity`
topic shapes (verified against `contracts/pool_stable/src/contract.rs`
lines 353-362 / 506-513), so the new liquidity decoders cover both
pool variants. Stable-pool **swap** still drops to the orphan
counter; the field set is different and remains unenumerated (Q5).

### Historical fill

Granular-coverage mission: once `soroban_events` (ADR-0029) has
been backfilled across the Soroban era, populate
`phoenix_liquidity` + `phoenix_stake_events` for the historical
range via `INSERT … SELECT FROM soroban_events WHERE topic_0_sym IN
('provide_liquidity','withdraw_liquidity','bond','unbond')`, fed
through the per-action correlation buffer the same way the live
ingest does. Pending the per-WASM-hash decoder audit log
([docs/operations/wasm-audits/phoenix.md](../../../docs/operations/wasm-audits/phoenix.md))
being extended to cover the liquidity + stake field sets — current
audit only enumerates the 8 swap-field strings; we need to confirm
both WASM hashes also include the literal `provide_liquidity` /
`withdraw_liquidity` / `bond` / `unbond` symbols.

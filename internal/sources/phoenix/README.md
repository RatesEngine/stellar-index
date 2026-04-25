# Phoenix DEX connector

Ingests trade events from [Phoenix](https://app.phoenix-hub.io) —
a Stellar-native DEX with x·y=k + stableswap pools. Primary Phase-1
reference:
[`docs/discovery/dexes-amms/phoenix.md`](../../../docs/discovery/dexes-amms/phoenix.md).

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

To reconstruct one swap we **must group 8 events** by
`(ledger, tx_hash, op_index)` and assemble them into a single
record. This is the third event-correlation shape our consumer fleet
handles:

| Shape | Example | Events per trade |
| ----- | ------- | ---------------- |
| 1-event | Aquarius | one `trade` event carries everything |
| 2-event | Soroswap | `swap` + `sync` (Q1) |
| 8-event | **Phoenix** | one event per field |

## Mainnet addresses (verified Phase-1)

| Contract | Address |
| --- | --- |
| Factory | `CB4SVAWJA6TSRNOJZ7W2AWFW46D5VR4ZMFZKDIKXEINZCZEGZCJZCKMI` |
| Multihop | `CCLZRD4E72T7JCZCN3P7KNPYNXFYKQCL64ECLX7WP5GNVYPYJGU2IO2G` |
| XLM SAC (Phoenix-specific) | `CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC` |

Pools (listed Phase-1, assumed stable):

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
`return_amount - actual_received` is the fee, captured as metadata
(TODO(#0) — expose as Trade.Fee once we add that field).

### Q4 — Multihop expands to N×8 events

A Phoenix multihop swap passes through N pools and emits 8 events
per pool — so 16 events for a 2-hop, 24 for a 3-hop. Each operation
in the transaction is a distinct op_index, so the correlation key
already separates them: we end up with N complete trades for one
multihop.

### Q5 — Stableswap pool contract emits a different schema

`contracts/pool_stable/src/contract.rs` uses a similar but
distinct event shape. **Not decoded yet.** The volatile
(`contracts/pool/`) schema above is the only one we handle in this
first implementation. Stableswap pool support is an explicit
TODO(#0) once the volatile path is validated in production.

## File layout (five-file convention)

| File | Purpose |
| --- | --- |
| `README.md` | this file |
| `events.go` | 8 field-name constants + their SCVal topic constants + mainnet addresses + errors |
| `decode.go` | 8-event correlation buffer + single-Trade emission, decoded via `internal/scval` |
| `consumer.go` | implements `consumer.Source` (the dispatcher seam) |
| `dispatcher_adapter.go` | topic-match registration with `internal/dispatcher` |
| `decode_test.go`, `source_test.go`, `real_fixture_test.go` | unit + happy-path-and-orphan + real-mainnet-fixture tests |

## Status

Production for volatile (constant-product) pools. The 8-event
correlation buffer, the SCVal decoding via `internal/scval`
(ADR-0013), and the topic-match dispatch all run against real
mainnet event fixtures captured under `test/fixtures/phoenix/`.

Stableswap pool support is TODO(#0) — Phoenix's stableswap
emits a different field set we haven't enumerated yet. The
volatile path is the dominant traffic and runs cleanly without
it; stableswap drops to the orphan-events counter rather than
mis-decoding.

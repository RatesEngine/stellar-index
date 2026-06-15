# `internal/sources/blend_backstop`

Decoder for the **Blend Backstop** contract on Stellar (Soroban) — the
protocol's insurance / shared-liquidity module.

This is a **separate event surface** from the Blend pool /
pool-factory decoder in [`internal/sources/blend`](../blend/). The two
share neither contract addresses nor event vocabulary; do **not** fold
this package into that one.

## Scope

Two backstop contracts (gate on **both** — a backfill range replays
either):

| Contract | Address |
|---|---|
| Backstop V2 (current) | `CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7` |
| Backstop V1 | `blend.MainnetBackstopV1` (re-exported) |

Ten event types (`topic[0]` = Symbol):

| Event | Topics | Body | Promoted |
|---|---|---|---|
| `deposit` | `[sym, pool, user]` | `Vec[i128 amount, i128 shares]` | pool, user, amount, amount2=shares |
| `claim` | `[sym, user]` | `i128 amount` | user, amount (no pool) |
| `donate` | `[sym, pool, from]` | `i128 amount` | pool, amount; `from`→attrs |
| `queue_withdrawal` | `[sym, pool, user]` | `Vec[i128 shares, u64 expiration]` | pool, user, amount=shares; `expiration`→attrs |
| `withdraw` | `[sym, pool, user]` | `Vec[i128 amount, i128 shares]` | pool, user, amount, amount2=shares |
| `distribute` | `[sym]` | `i128 amount` | amount only |
| `gulp_emissions` | `[sym, token]` | `Vec[i128, i128]` | amount=data[0], amount2=data[1]; `token`→attrs |
| `dequeue_withdrawal` | `[sym, pool, user]` | `i128 amount` | pool, user, amount |
| `draw` | `[sym, pool]` | `Vec[Address to, i128 amount]` | pool, amount=data[1]; `to`→attrs |
| `rw_zone_add` | `[sym]` | `Vec[Address pool, u32 index]` | pool=data[0]; `index`→attrs |

## Symbol overlap — why the contract gate is load-bearing

The backstop's `claim` / `withdraw` / `queue_withdrawal` /
`gulp_emissions` symbols **collide** with Blend **pool** event symbols.
`Matches()` therefore gates on `IsBackstopContract(contract_id)` AND
`Classify() != ""` — never on the symbol alone. This is the
ADR-0035 factory-anchored gating model + the "Comet uses a shared
topic" trap in CLAUDE.md: a look-alike symbol from a non-backstop
contract must never mint a backstop row.

## Robustness

Per CLAUDE.md "EVERY-event" + "decode by field, degrade gracefully":

- A **promoted** field whose SCVal shape doesn't match (e.g. a
  `donate` `from` topic that isn't an Address) is left empty and the
  raw note is stashed in `attributes` (`*_error` key) rather than
  erroring the whole row.
- A **genuinely malformed** event (wrong arity, body that is neither
  i128 nor the expected Vec, an un-parseable required amount) returns
  an error — the dispatcher counts + skips it.
- i128 amounts round-trip through `canonical.Amount` / `*big.Int` per
  ADR-0003 and are emitted as decimal strings — never `int64`.

## Provenance — LIVE-CAPTURE ONLY

The per-event field layouts in `decode.go` / `events.go` were
**reverse-engineered from real mainnet lake samples on 2026-06-15** and
validated against the golden frames in `decode_test.go`. They are
**not yet confirmed against the Blend team's published contract
source**.

Consequence: this source is **live-capture only**. Do **not** flip any
`BackfillSafe` flag and do **not** run a historical re-derive against
these schemas until the Blend team confirms them — a drift in the
reverse-engineering would silently mis-attribute every backfilled row.

## Wiring

- `dispatcher_adapter.go` — `Decoder`, a stateless topic Decoder gated
  on the two known backstop contracts.
- `consumer.go` — projects each decoded event into the
  `blend_backstop.Event` `consumer.Event`. One row per event.
- `internal/pipeline/sink.go` — `IsProjectedEvent` arm +
  `persistBlendBackstopEvent` writes via
  `Store.InsertBlendBackstopEvent`.
- `internal/projector/registry.go` — `buildSource` registers
  `blend_backstop.NewDecoder()` (projector is sole writer in Phase-4).
- Storage: `blend_backstop_events` hypertable, migration
  [`0063_create_blend_backstop_events`](../../../migrations/0063_create_blend_backstop_events.up.sql).
- `internal/storage/timescale/per_source_gaps.go` — gap target
  (`blend-backstop`, Genesis ≈ 56,627,571).
- `internal/storage/timescale/protocol_stats.go` — `blend_backstop`
  leg in the trailing-24h event census.

## Tests

- `decode_test.go` — golden frames built from real base64 lake samples
  (`deposit`, `claim`, `distribute`, `queue_withdrawal`) pin the decode
  of the promoted fields; plus `Classify` coverage and
  short-topic / malformed-body guards.
- `dispatcher_adapter_test.go` — `Matches` gating (backstop symbol from
  backstop vs non-backstop contract, non-backstop symbol from backstop
  contract) + end-to-end `Decode`.

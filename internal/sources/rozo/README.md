# `internal/sources/rozo`

Decoder for the **Rozo intent-bridge protocol** on Stellar.

## Scope (2026-05-20)

Currently scoped to **v1 Payment** — the only mainnet-live Rozo
contract at the time of writing. v2 Forwarder + IntentBridge and
the newer rozo-intents schema are pre-mainnet and documented for
follow-up implementation in
[`docs/architecture/rozo-stellar-coverage.md`](../../../docs/architecture/rozo-stellar-coverage.md).

| Variant | Status | Decoder support |
|---|---|---|
| v1 Payment (`CAC5SKP5…IYRL`) | mainnet | this package |
| v2 Forwarder | design-stage | follow-up |
| v2 IntentBridge | design-stage | follow-up |
| rozo-intents (v2.x?) | status unclear | follow-up |

## What this package emits

Two canonical Go types — `Payment` and `Flush` — corresponding
1:1 to the two `#[contractevent]` types in
[`v1/stellar/payment/src/lib.rs`](https://github.com/RozoAI/rozo-intents-contracts/blob/main/v1/stellar/payment/src/lib.rs):

- **`Payment`** — emitted on `pay(from, amount, memo)`.
  Topic `(symbol_short!("payment"), from: Address)`. Body
  `{ from, destination, amount, memo }`.
- **`Flush`** — emitted on `flush(token)` (admin sweep).
  Topic `(symbol_short!("flush"),)`. Body
  `{ token, destination, amount }`.

## Wiring

This package is **wired into the ingest pipeline** (#41), scoped to
v1 Payment:

- `dispatcher_adapter.go` — `Decoder`, a stateless topic Decoder
  gated on the three known v1 Payment contracts (`Matches` checks
  topic[0] **and** `IsRozoContract`).
- `consumer.go` — the `rozo.Event` `consumer.Event`, plus the
  projections from `DecodePayment` / `DecodeFlush` into the
  `rozo_events` row shape.
- `internal/pipeline/dispatcher.go` — `BuildDispatcher` registers
  `rozo.NewDecoder()` when `"rozo"` is in `ingestion.enabled_sources`.
- `internal/pipeline/sink.go` — `persistRozoEvent` writes each
  event via `Store.InsertRozoEvent` and bumps the entry counter.
- Storage: `rozo_events` hypertable, migration
  [`0039_create_rozo_events`](../../../migrations/0039_create_rozo_events.up.sql)
  — fully-typed (no jsonb blob; v1 Payment is simple enough).
- Registry: `internal/sources/external/registry.go` —
  `Class: ClassBridge, IncludeInVWAP: false, DefaultWeight: 0,
  BackfillAvailable: true, BackfillSafe: false`.

**Operator steps to turn it on:**

1. Apply migration 0039 (`ratesengine-migrate up` after the SCP).
2. Add `"rozo"` to `ingestion.enabled_sources` in the region TOML.
3. `BackfillSafe` stays `false` until a WASM-history audit lands
   at `docs/operations/wasm-audits/rozo.md`.

**v2 Forwarder / IntentBridge are NOT wired** — they are pre-mainnet
(not deployed). When they go live they get their own source entries
(`rozo-forwarder`, `rozo-intent-bridge`) and migrations rather than
widening this package; see the architecture doc §Decoder design.

## Tests

`decode_test.go` covers:

- `Classify` — payment / flush / unknown-symbol / empty-topic.
- `DecodePayment` — happy path with all four fields populated;
  ADR-0003 large-i128 round-trip (locks the *big.Int → string
  precision invariant against the int64-truncation bug-class);
  missing-field surfaces `ErrMalformedBody`; wrong top-level
  ScVal kind early-fails.
- `DecodeFlush` — happy path + missing-field.
- Topic-symbol encoding stability — re-encoded bytes match the
  package-init constants. Drift here means `scval.MustEncodeSymbol`
  changed and every `Classify` call would silently miss matches.

## References

- Architecture doc: [`docs/architecture/rozo-stellar-coverage.md`](../../../docs/architecture/rozo-stellar-coverage.md)
- Upstream source: https://github.com/RozoAI/rozo-intents-contracts
- v1 contract on StellarExpert:
  https://stellar.expert/explorer/public/contract/CAC5SKP5FJT2ZZ7YLV4UCOM6Z5SQCCVPZWHLLLVQNQG2RWWOOSP3IYRL
- Class taxonomy: `internal/sources/external/framework.go` `ClassBridge`
- Sister sources (event-based decoders): `internal/sources/soroswap/`,
  `internal/sources/aquarius/`, `internal/sources/phoenix/`

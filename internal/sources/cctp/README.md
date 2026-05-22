# `internal/sources/cctp`

Decoder for **Circle CCTP v2** on Stellar (Soroban).

## Scope

Three on-chain contracts (verified mainnet 2026-05-20):

| Contract | Address |
|---|---|
| TokenMessengerMinter (v2) | `CAE2G5Z77UP7GYPYGFOWFGW7C7J6I4YP2AFGSADRKQY62SYUFLPNFTXL` |
| MessageTransmitter (v2) | `CACMENFFJPJMSDAJQLX4R7K3SFZIW2LJSE3R2UMLGSWHFHS353FVXAZV` |
| CctpForwarder | `CBZL2IH7F6BIDAA3WBNXYKIXSATJGMSW7K5P5MJ6STX5RXN47TZJDF5T` |

Four canonical events:

- **`deposit_for_burn`** (TokenMessengerMinter) — outbound USDC
  transfer. Topics include `burn_token`, `depositor`,
  `min_finality_threshold`.
- **`mint_and_withdraw`** (TokenMessengerMinter) — inbound mint
  after attestation. Topics include `mint_recipient`,
  `mint_token`.
- **`message_sent`** (MessageTransmitter) — wire envelope; paired
  with `deposit_for_burn` in the same tx.
- **`message_received`** (MessageTransmitter) — wire envelope;
  paired with `mint_and_withdraw` in the same tx.

Stellar's CCTP domain ID is `27`. Other notable CCTP domains
(referenced by `destination_domain` / `source_domain` fields):
Ethereum=0, Avalanche=1, Arbitrum=3, Solana=7.

## What this package emits

Four canonical Go types — `DepositForBurn`, `MintAndWithdraw`,
`MessageSent`, `MessageReceived` — corresponding 1:1 to the
`#[contractevent]` types in
[`circlefin/stellar-cctp/contracts/{token-messenger-minter-v2,message-transmitter-v2}/src/lib.rs`](https://github.com/circlefin/stellar-cctp).

BytesN<32> fields (`mint_recipient`, `destination_token_messenger`,
`destination_caller`, `nonce`, `sender`, etc.) are emitted as
lowercase hex (no `0x` prefix). The decoder doesn't try to
re-format for the destination chain's address shape — that's a
downstream concern (EVM destinations would drop the leading 12
zero-bytes; Solana keeps the full 32).

i128 amounts (`amount`, `max_fee`, `fee_collected`) round-trip
through `*big.Int` per ADR-0003 and are emitted as decimal
strings.

## Pairing semantics

A single `deposit_for_burn` call emits **both** a `DepositForBurn`
event (TokenMessengerMinter) **and** a `MessageSent` event
(MessageTransmitter) in the same transaction. Same for inbound
(`MessageReceived` + `MintAndWithdraw`).

A future consumer can correlate the pair by `(ledger, tx_hash)`
and surface them as one logical "outbound USDC transfer" record.
The decoder doesn't do the pairing — that's a sink-layer
concern.

## Wiring

This package is **wired into the ingest pipeline** (#40):

- `dispatcher_adapter.go` — `Decoder`, a stateless topic Decoder
  gated on the three known CCTP contracts (`Matches` checks
  topic[0] **and** `IsCCTPContract`).
- `consumer.go` — the `cctp.Event` `consumer.Event`, plus the
  projections from each `Decode*` struct into the `cctp_events`
  row shape. The decoder does **not** pair `DepositForBurn` with
  `MessageSent`; each event is its own row, correlatable later by
  `(ledger, tx_hash)`.
- `internal/pipeline/dispatcher.go` — `BuildDispatcher` registers
  `cctp.NewDecoder()` when `"cctp"` is in `ingestion.enabled_sources`.
- `internal/pipeline/sink.go` — `persistCCTPEvent` writes each
  event via `Store.InsertCCTPEvent` and bumps the entry counter.
- Storage: `cctp_events` hypertable, migration
  [`0038_create_cctp_events`](../../../migrations/0038_create_cctp_events.up.sql).
- Registry: `internal/sources/external/registry.go` —
  `Class: ClassBridge, IncludeInVWAP: false, DefaultWeight: 0,
  BackfillAvailable: true, BackfillSafe: false`.

**Operator steps to turn it on:**

1. Apply migration 0038 (`ratesengine-migrate up` after the SCP —
   migrations are not auto-deployed).
2. Add `"cctp"` to `ingestion.enabled_sources` in the region TOML.
3. `BackfillSafe` stays `false` until a WASM-history audit lands
   at `docs/operations/wasm-audits/cctp.md`. The contracts are
   brand new (a single WASM hash is expected) but the audit is
   required program work before `ratesengine-ops backfill` will
   run CCTP against historical ranges. Live ingest works without
   it — per the user's direction CCTP needs little/no history.

## Tests

`decode_test.go` (16 parallel tests + subtests):

- `Classify` — all four event types + unknown-symbol + empty-topic.
- `DecodeDepositForBurn` — happy path (covers BytesN<32> roundtrip
  for `mint_recipient` / `destination_token_messenger` /
  `destination_caller` and `hook_data`); ADR-0003 large-i128
  guard (`> 2^99`); short-topic surfaces `ErrMalformedTopic`;
  missing body field surfaces `ErrMalformedBody`.
- `DecodeMintAndWithdraw` — happy path + short-topic.
- `DecodeMessageSent` — Map-body path (`MessageSent { message:
  Bytes }` ScMap form) AND raw-Bytes fallback (forward-compat
  guard if the Soroban macro shifts).
- `DecodeMessageReceived` — happy path + short-topic.
- Topic-symbol encoding stability — re-encoded bytes match
  package-init constants. Drift here would silently break
  `Classify`.
- `ErrUnknownEvent` sentinel availability for downstream consumers.

## References

- Architecture doc: [`docs/architecture/cctp-stellar-coverage.md`](../../../docs/architecture/cctp-stellar-coverage.md)
- Upstream source: https://github.com/circlefin/stellar-cctp
- Circle developer docs: https://developers.circle.com/cctp/references/stellar-contracts
- Class taxonomy: `internal/sources/external/framework.go` `ClassBridge`
- Sister bridge: `internal/sources/rozo/` (Rozo v1 Payment)

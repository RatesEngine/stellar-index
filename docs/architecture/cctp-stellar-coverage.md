# CCTP-Stellar coverage — architecture & decoder design

**Status:** Implemented (#40). Live ingest wired 2026-05-22.
**Last verified:** 2026-05-22

This document captures the contract identities, event schemas, and
the design decisions for integrating Circle's CCTP v2 on Stellar
into the Rates Engine's source fleet. The implementation lives in
`internal/sources/cctp/` (decoder + dispatcher adapter + consumer)
with persistence to the `cctp_events` hypertable (migration 0038).
The storage shape — a per-protocol table rather than a shared
`bridge_events` — was operator-confirmed 2026-05-22. Remaining
work: the WASM-history audit that flips `BackfillSafe` true, and
verification against real on-chain events once mainnet traffic
exists. The design narrative below is retained as the rationale
record.

## Why we track CCTP

USDC is the dominant stablecoin on Stellar and CCTP is the native
cross-chain mint/burn mechanism. Every `deposit_for_burn` is a
USDC supply *exit* from Stellar; every `mint_and_withdraw` is a
*entry*. Tracking these gives us:

- The bridge-flow side of the granular-coverage mission
  ([[project_full_indexing_future_scope]]).
- A real-time signal for USDC supply attribution beyond classic
  trustline mints/burns (which Circle uses as the local Stellar
  minter — but CCTP-driven mint/burn is the cross-chain channel).
- An attribution surface for "which chain pair drove this USDC
  movement?".

CCTP does **not** publish prices — it's a bridge, not a market.
So it doesn't fit `ClassExchange` (no trades), `ClassOracle`
(no price signal), or `ClassAggregator` (no derived aggregate).
The closest existing class is `ClassRouter` ("derivative actions
on top of other sources, not new price observations") but that
elides the cross-chain dimension. **Recommendation:** a new
`ClassBridge` (see §Open design questions).

## Contracts (mainnet, 2026-05-20)

Reference:
https://developers.circle.com/cctp/references/stellar-contracts
Source: https://github.com/circlefin/stellar-cctp

| Contract | Mainnet address |
|---|---|
| TokenMessengerMinter (v2) | `CAE2G5Z77UP7GYPYGFOWFGW7C7J6I4YP2AFGSADRKQY62SYUFLPNFTXL` |
| MessageTransmitter (v2) | `CACMENFFJPJMSDAJQLX4R7K3SFZIW2LJSE3R2UMLGSWHFHS353FVXAZV` |
| CctpForwarder | `CBZL2IH7F6BIDAA3WBNXYKIXSATJGMSW7K5P5MJ6STX5RXN47TZJDF5T` |

Stellar's CCTP domain ID is **27** (per
`message_transmitter::get_local_domain`).

No `TokenMinter` or `MessageTransmitterAdmin` contracts exist;
those responsibilities are consolidated into the v2 contracts.

## Canonical events

Extracted from
`circlefin/stellar-cctp/contracts/{token-messenger-minter-v2,message-transmitter-v2}/src/lib.rs`.
The Soroban `#[contractevent]` macro generates the topic + body
layout via the `#[topic]` field markers.

### `TokenMessengerMinter` — `DepositForBurn`

Emitted by `deposit_for_burn` and `deposit_for_burn_with_hook`.

- **Topics:** `["deposit_for_burn", burn_token: Address, depositor: Address, min_finality_threshold: u32]`
- **Body:**

| Field | Type | Notes |
|---|---|---|
| `amount` | `i128` | local-decimal amount burned |
| `mint_recipient` | `BytesN<32>` | recipient address on destination chain (0-padded for EVM 20-byte addrs) |
| `destination_domain` | `u32` | CCTP domain ID (e.g. 0 = Ethereum, 1 = Avalanche, 3 = Arbitrum, 7 = Solana, 27 = Stellar) |
| `destination_token_messenger` | `BytesN<32>` | counterpart TokenMessenger on destination chain |
| `destination_caller` | `BytesN<32>` | authorised caller (zero = any caller) |
| `max_fee` | `i128` | maximum fee payable on destination |
| `hook_data` | `Bytes` | optional post-mint hook payload |

### `TokenMessengerMinter` — `MintAndWithdraw`

Emitted by `handle_receive_finalized_message` and
`handle_receive_unfinalized_message` after attestation.

- **Topics:** `["mint_and_withdraw", mint_recipient: Address, mint_token: Address]`
- **Body:**

| Field | Type | Notes |
|---|---|---|
| `amount` | `i128` | local-decimal amount minted |
| `fee_collected` | `i128` | fee deducted from `amount` |

### `MessageTransmitter` — `MessageSent`

Emitted by `send_message`.

- **Topics:** `["message_sent"]`
- **Body:** `{ message: Bytes }` (the serialised cross-chain message envelope)

### `MessageTransmitter` — `MessageReceived`

Emitted by `receive_message`.

- **Topics:** `["message_received", caller: Address, nonce: BytesN<32>, finality_threshold_executed: u32]`
- **Body:**

| Field | Type | Notes |
|---|---|---|
| `source_domain` | `u32` | originating CCTP domain |
| `sender` | `BytesN<32>` | sender on source chain |
| `message_body` | `Bytes` | application-level payload (mirrors the corresponding `MessageSent.message` on the source chain) |

### `MessageTransmitter` — `MaxMessageBodySizeUpdated`

Admin event, low signal value for our use case but emitted by
`set_max_message_body_size`. Track for completeness.

- **Topics:** `["max_message_body_size_updated"]`
- **Body:** `{ new_max_message_body_size: u32 }`

### Pairing `MessageSent` ↔ `DepositForBurn`

A single `deposit_for_burn` call results in **both** a
`DepositForBurn` event (TokenMessengerMinter) and a `MessageSent`
event (MessageTransmitter) within the same transaction. The
decoder should correlate by `(ledger, tx_hash)` and treat them
as one logical "outbound transfer" event with both halves of
the data merged.

Similarly `receive_message` → `MessageReceived` +
`MintAndWithdraw` per transaction.

## Decoder design

Three options for slotting into the dispatcher:

### Option A — Topic-based `Decoder` interface (matches Soroswap)

`internal/dispatcher` has a topic-keyed dispatch table. Register
two decoders:

- one matching `topic[0] == "deposit_for_burn"` against
  contract_id `CAE2G5...`
- one matching `topic[0] == "mint_and_withdraw"` against
  contract_id `CAE2G5...`
- one matching `topic[0] == "message_sent"` against contract_id
  `CACMEN...`
- one matching `topic[0] == "message_received"` against
  contract_id `CACMEN...`

The dispatcher matches by topic[0] symbol bytes. Per-source
filtering by contract_id happens downstream (drop events from
contracts we don't track even if topic matches — Comet pattern,
per CLAUDE.md).

This option mirrors how Soroswap / Phoenix / Aquarius work and
needs the least new code.

### Option B — `ContractCallDecoder` (function-name dispatch)

For sources that don't emit events (Band, soroswap-router), the
dispatcher offers a `ContractCallDecoder` that matches by
`(contract_id, function_name)` and decodes from op args. CCTP
does emit events, so this option isn't needed — but if the
admin events ever need finer granularity, this is the fallback.

### Option C — Per-contract decoder bundle

Bundle all four CCTP decoders into a single `cctp.Decoder` that
implements multiple topic matches via a dispatch table in the
decoder itself. Cleaner namespace but breaks the dispatcher's
flat shape.

**Recommendation: Option A.** It matches the existing pattern,
preserves the dispatcher's debuggability, and lets operators
disable individual event types via config if needed.

## Storage shape

CCTP events don't naturally fit the `trades` hypertable. Three
shapes to consider:

1. **New `cctp_events` hypertable.** One row per event with
   columns `ledger, ts, tx_hash, op_index, contract_id,
   event_type (enum), burn_token / mint_token, amount,
   counterparty_domain, counterparty_address, fee, hook_data`.
   Migration `0037_create_cctp_events.up.sql`. The cleanest
   semantic match — bridge events have their own shape.

2. **Generic `soroban_events` hypertable.** A wider net that
   future bridges and lending sources also write into. Avoids
   per-protocol migration churn but requires careful
   JSON-blob-vs-typed-columns tradeoffs.

3. **Synthetic trades**. Pretend `DepositForBurn` is a trade
   from `USDC` to a synthetic `USDC@<destination_domain>` asset.
   Lets it ride existing infrastructure but pollutes the trades
   table with non-trade data. **Rejected.**

**Recommendation: (1) `cctp_events` hypertable.** Clean
semantics; the bridge-flow surface deserves its own table. If
Rozo (#41) and future bridges share a common shape we can
generalise later.

## Backfill strategy

Per the user's direction (2026-05-20): "CCTP shouldnt have any
history because it is brand new". Initial implementation:

- Live ingest from current ledger forward. No historical walk.
- `BackfillAvailable: true, BackfillSafe: true` once the
  decoder is unit-tested and matches against real on-chain
  events. The brand-new property means backfill range
  `[first-deploy, tip]` is small (likely <100k ledgers, ~1 week
  of history) and cheap.
- First-deploy ledger TBD — extract from a chain explorer or
  via `find earliest tx involving CAE2G5...` once decoder
  ships.

## Open design questions (operator-gated)

1. ~~**New `ClassBridge` source class** vs reuse `ClassRouter`?~~
   **Resolved 2026-05-20** — `ClassBridge` added to
   `internal/sources/external/framework.go`. Registry entries for
   CCTP + Rozo use `Class: ClassBridge, IncludeInVWAP: false,
   DefaultWeight: 0, Paid: false`. Test guard
   `TestClassBridge_Defined` locks the wire value.

2. **Cross-chain attribution data**: when we record
   `DepositForBurn` with `destination_domain = 0` (Ethereum),
   do we resolve `destination_token_messenger` to a known
   identity (Circle's mainnet contract) at ingest, or surface
   the raw `BytesN<32>` and let downstream resolve?

3. **Hook data**: `deposit_for_burn_with_hook` carries arbitrary
   bytes. Some hooks are calls to known protocols (Pendle,
   Aave). Decoding hook intent is a downstream concern; the
   decoder just preserves the bytes.

4. **Storage migration**: who reviews + signs off on the
   `cctp_events` table schema?

5. **Verified currency catalogue**: should the catalogue gain a
   `bridge_capable` flag so the explorer can surface "USDC moves
   via CCTP" on the asset page?

## Next steps (post-decision)

1. Operator confirms the design recommendations above (especially
   §Open #1 ClassBridge).
2. Implement `internal/sources/cctp/{README.md, events.go,
   decode.go, consumer.go, source_test.go}` following the
   five-file convention (CLAUDE.md "Add a new on-chain Soroban
   DEX").
3. Add migration `0037_create_cctp_events.up.sql` per §Storage.
4. Register in `internal/sources/external/registry.go` with the
   confirmed class.
5. Add an alert for "CCTP decoder silent for >24h" to catch
   contract-upgrade drift.
6. Per CLAUDE.md "Soroban DeFi contracts upgrade in place": run
   a WASM-history walk over the CCTP contracts before flipping
   `BackfillSafe: true`. The contracts are brand new so a single
   hash is expected, but the audit is required program work.

## References

- Circle Stellar contracts: https://developers.circle.com/cctp/references/stellar-contracts
- Source repo: https://github.com/circlefin/stellar-cctp
- Existing source playbook: CLAUDE.md "Add a new on-chain
  Soroban DEX" / `internal/sources/soroswap/` template
- Class taxonomy: `internal/sources/external/framework.go`
- WASM audit playbook: `docs/operations/wasm-audits/README.md`

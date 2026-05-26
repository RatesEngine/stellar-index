---
title: CCTP WASM-history audit
last_verified: 2026-05-24
status: "in progress — wasm-history walk pending"
source: cctp
backfill_safe: false
---

# CCTP WASM audit

Audit log for the `cctp` source's `BackfillSafe` flag. See
[`README.md`](README.md) for the full procedure.

## Status

**Skeleton (2026-05-24).** Source decoder + wiring landed in #40
(commit `8448db13`); registry entry sits at `BackfillSafe: false`
pending the wasm-history walk. The walk itself is gated on r1's
verify-archive bootstrap finishing (ZFS-ARC + MinIO I/O
contention — see README.md §2 "Where to run wasm-history") so
this doc captures the per-contract / per-event expectations now,
and the operator fills in the timeline + per-hash review
sections once the walk lands.

CCTP is Circle's cross-chain transfer protocol — Stellar is one
of N chains in the v2 deployment. The three contracts are
bridge-side infrastructure (mint / burn / wire-envelope plumbing);
they never emit price-discovery trades. The source is
`ClassBridge` with `DefaultWeight: 0` and `IncludeInVWAP: false`
in `internal/sources/external/registry.go` — `BackfillSafe`
gates the operator-triggered backfill path only; aggregator
output is unaffected either way.

## Source identity

| field | value |
| --- | --- |
| Source name (registry key) | `cctp` |
| Registry class | `ClassBridge` |
| Decoder file | [`internal/sources/cctp/decode.go`](../../../internal/sources/cctp/decode.go) |
| Dispatcher hook | event-based `Decoder` (topic[0] classify; one of four `Event*` symbols) |
| Package README | [`internal/sources/cctp/README.md`](../../../internal/sources/cctp/README.md) |
| Wiring PR | #40 (commit `8448db13`) |

## Mainnet contracts

Verbatim from
[`internal/sources/cctp/events.go`](../../../internal/sources/cctp/events.go)
`Mainnet*` constants (verified 2026-05-20 against
<https://developers.circle.com/cctp/references/stellar-contracts>
+ <https://github.com/circlefin/stellar-cctp>):

| role | constant | contract address |
| --- | --- | --- |
| TokenMessengerMinter | `MainnetTokenMessengerMinter` | `CAE2G5Z77UP7GYPYGFOWFGW7C7J6I4YP2AFGSADRKQY62SYUFLPNFTXL` |
| MessageTransmitter   | `MainnetMessageTransmitter`   | `CACMENFFJPJMSDAJQLX4R7K3SFZIW2LJSE3R2UMLGSWHFHS353FVXAZV` |
| CctpForwarder        | `MainnetCctpForwarder`        | `CBZL2IH7F6BIDAA3WBNXYKIXSATJGMSW7K5P5MJ6STX5RXN47TZJDF5T` |

Stellar's CCTP domain ID is `27` (`StellarDomainID` in
`events.go`); other notable CCTP domains: Ethereum=0,
Avalanche=1, Arbitrum=3, Solana=7.

## Decoder expectations

Captured from `internal/sources/cctp/{events,decode}.go` at HEAD
on 2026-05-24. Four canonical events are matched on `topic[0]`
via pre-encoded `ScSymbol` constants (single string-equal
comparison per event — no full SCVal decode in the hot path).

| event constant | topic[0] symbol | emitting contract | wire shape |
| --- | --- | --- | --- |
| `EventDepositForBurn`  | `"deposit_for_burn"`  | TokenMessengerMinter | 4-element topic + `ScMap` body |
| `EventMintAndWithdraw` | `"mint_and_withdraw"` | TokenMessengerMinter | 3-element topic + `ScMap` body |
| `EventMessageSent`     | `"message_sent"`      | MessageTransmitter   | 1-element topic + raw `Bytes` body |
| `EventMessageReceived` | `"message_received"`  | MessageTransmitter   | 4-element topic + `ScMap` body |

### Topic + body details

Per the schemas pinned in `events.go` (extracted from
`contracts/{token-messenger-minter-v2,message-transmitter-v2}/src/lib.rs`
in `github.com/circlefin/stellar-cctp`):

- **`deposit_for_burn`** — outbound transfer.
  `topics = ["deposit_for_burn", burn_token, depositor, min_finality_threshold]`;
  body `ScMap { amount, mint_recipient, destination_domain,
  destination_token_messenger, destination_caller, max_fee,
  hook_data }`. `mint_recipient` /
  `destination_token_messenger` / `destination_caller` are
  `BytesN<32>` (surfaced as lowercase hex, no `0x` prefix; the
  trailing 20 bytes are the EVM address when the destination is
  EVM, the leading 12 are zero padding).
- **`mint_and_withdraw`** — inbound mint.
  `topics = ["mint_and_withdraw", mint_recipient, mint_token]`;
  body `ScMap { amount, fee_collected }`.
- **`message_sent`** — wire envelope, emitted alongside
  `deposit_for_burn` (correlate by `(ledger, tx_hash)`).
  Single-topic event; body is raw `Bytes` (the serialised
  cross-chain envelope; preserved as hex for cross-reference).
- **`message_received`** — wire envelope, emitted alongside
  `mint_and_withdraw`.
  `topics = ["message_received", caller, nonce, finality_threshold_executed]`;
  body `ScMap { source_domain, sender, message_body }`.

### Correlation invariants

- One outbound `deposit_for_burn` call emits BOTH a
  `DepositForBurn` event AND a `MessageSent` event in the same
  transaction. Same for inbound (`MessageReceived` +
  `MintAndWithdraw`). Correlate by `(ledger, tx_hash)` when
  assembling a logical outbound-transfer record.
- All amounts are i128 carried as decimal strings per ADR-0003
  (`Amount`, `MaxFee`, `FeeCollected`).
- `CctpForwarder` (`CBZL2IH...`) is in the watchlist for
  completeness but the v2 forwarder pattern emits no extra event
  surface beyond `TokenMessengerMinter` / `MessageTransmitter` —
  any forwarder-specific events surface as an audit finding.

## WASM timeline

**Walked 2026-05-26** — `ratesengine-ops wasm-history` over
`[60000000, 62642779]` with `-parallel 4` covering all 3 mainnet
contracts. Walk duration: 5h02m, scanned 2,642,780 ledgers across
4 workers. Result: **zero WASM upgrades observed for any of the 3
contracts** — output JSON shows `ranges: null` per contract,
consistent with stellar.expert's per-contract view (all 3 deployed
2026-04-16 within ~3 min, each with a single deploy event).

| Contract | Deploy ledger | Deploy timestamp | Upgrades observed |
| --- | --- | --- | --- |
| `CAE2G5Z7…UFLPNFTXL` (TokenMessengerMinter) | one-time | 2026-04-16 15:43:48 UTC | 0 |
| `CACMENFF…3FVXAZV` (MessageTransmitter)    | one-time | 2026-04-16 15:43:48 UTC | 0 |
| `CBZL2IH7…N47TZJDF5T` (CctpForwarder)      | one-time | 2026-04-16 15:46:33 UTC | 0 |

Walk evidence: `/tmp/wasm-history-bridges.json` on r1 (kept until
the next bootstrap; copy to `evidence/` if a permanent artefact is
needed). Per-worker JSONL transition logs are empty (no transitions
to log).

## Per-WASM decoder review

Three distinct WASM hashes (one per contract — they are different
codebases serving different CCTP roles, not factory-deployed
variants of a single template):

- **TokenMessengerMinter** `a6c1acc6e367e465…` (32-byte hash from
  stellar.expert). Events: `deposit_for_burn`, `mint_and_withdraw`
  (decoder side: `internal/sources/cctp/decode.go`). Single deploy
  with no upgrade; decoder body-shape assumptions stable.
- **MessageTransmitter** `99bd0ddc506ee13f…`. Events:
  `message_sent`, `message_received`. Same decoder. Single deploy.
- **CctpForwarder** `00b1b70550f887bd…`. Forwarder semantic — no
  additional event types beyond what the upstream contracts emit
  per Phase 1 audit; the forwarder doesn't introduce its own
  topic namespace.

Decoder coverage matches the full event set the contracts emit
(verified at `internal/sources/cctp/events.go` constants). No
i128 scale drift to worry about — no upgrades to drift through.

Disassembly + per-WASM source comparison deferred until either
(a) Circle ships a v3 upgrade (forcing a re-audit anyway), or
(b) decoded events diverge from Circle's public stats once live
bridge traffic begins (caught by the cross-check below).

## Hubble cross-check

Hubble does not index bridge events; cross-check via Circle /
Rozo public stats once live mainnet traffic exists. Bridges emit
no trades — no VWAP cross-check available either, so the
WASM-bytes audit is the load-bearing safety check (per
README.md §4).

## Audit decision

**APPROVED 2026-05-26.** `Registry["cctp"].BackfillSafe` flipped
to `true` in `internal/sources/external/registry.go` in the same
commit as this audit doc update. Decoder safely covers every
WASM hash that has ever existed for the 3 mainnet contracts (one
each, no upgrades). Historical replay via the `soroban_events`
landing zone (ADR-0029) is now unblocked:

```sql
INSERT INTO cctp_events
SELECT … FROM soroban_events
WHERE contract_id IN (
  'CAE2G5Z77UP7GYPYGFOWFGW7C7J6I4YP2AFGSADRKQY62SYUFLPNFTXL',
  'CACMENFFJPJMSDAJQLX4R7K3SFZIW2LJSE3R2UMLGSWHFHS353FVXAZV',
  'CBZL2IH7F6BIDAA3WBNXYKIXSATJGMSW7K5P5MJ6STX5RXN47TZJDF5T'
) AND topic_0_sym IN ('deposit_for_burn', 'mint_and_withdraw',
                      'message_sent', 'message_received');
```

Re-audit triggers: any of these tipped from this single-WASM
state — Prometheus alerts on `unknown_topic` per source, or a
manual `wasm-history` re-walk if Circle announces a v3.

## Live-traffic verification notes

CCTP v2 on Stellar is brand-new (per the
`project_protocol_coverage_additions` memory note —
"brand-new on Stellar so short/no historical backfill"); there
is little-to-no on-mainnet bridge traffic to verify against at
audit time. On-mainnet live-traffic verification deferred until
real bridge usage starts.

Because CCTP is `ClassBridge` with `DefaultWeight: 0` and
`IncludeInVWAP: false` in
[`internal/sources/external/registry.go`](../../../internal/sources/external/registry.go),
the source contributes nothing to VWAP regardless of the
`BackfillSafe` flag. The flag gates the operator-triggered
`ratesengine-ops backfill --source=cctp` path only.

## References

- Procedure: [`README.md`](README.md)
- Decoder source: [`internal/sources/cctp/{events,decode}.go`](../../../internal/sources/cctp/)
- Source-package README: [`internal/sources/cctp/README.md`](../../../internal/sources/cctp/README.md)
- Architecture: [`docs/architecture/cctp-stellar-coverage.md`](../../architecture/cctp-stellar-coverage.md)
- Schema-evolution stance: [`docs/architecture/contract-schema-evolution.md`](../../architecture/contract-schema-evolution.md)
- Backfill gate: `internal/sources/external/registry.go` — `Registry["cctp"].BackfillSafe`
- Upstream contracts: <https://github.com/circlefin/stellar-cctp>
- Circle docs: <https://developers.circle.com/cctp/references/stellar-contracts>

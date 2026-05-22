# Rozo intents — architecture & decoder design

**Status:** v1 Payment implemented (#41). Live ingest wired
2026-05-22. v2 Forwarder / IntentBridge remain pre-mainnet.
**Last verified:** 2026-05-22

This document captures the contract identities, event schemas, and
deployment status for integrating Rozo's intent-based cross-chain
contracts on Stellar into the Rates Engine. The v1 Payment
implementation lives in `internal/sources/rozo/` (decoder +
dispatcher adapter + consumer) with persistence to the `rozo_events`
hypertable (migration 0039). The storage shape — a per-protocol
table rather than a shared `bridge_events` — was operator-confirmed
2026-05-22. v2 Forwarder / IntentBridge and the newer rozo-intents
schema are pre-mainnet; the design narrative below is retained for
when they deploy. Remaining v1 work: the WASM-history audit that
flips `BackfillSafe` true, and verification against real on-chain
events.

## What Rozo is

> Cross-chain intent-based token forwarding and bridging contracts
> for Stellar/Soroban.

Rozo unblocks two Stellar-specific cross-chain UX gaps:

1. **Address+memo routing** — Stellar Smart Wallets (C-Accounts)
   can't transfer/receive USDC with memo. Rozo's proxy pattern
   lets cross-chain services target a Stellar address+memo
   destination by going through a forwarder contract.
2. **Contract-invocation routing** — many cross-chain services
   can't `InvokeContract` on Stellar; Rozo's intent_bridge
   exposes a relayer-driven path.

Like CCTP (#40), Rozo is a **bridge** semantic — not a price
source. It doesn't fit `ClassExchange`. Shares CCTP's
`ClassBridge`-or-not design question.

## Contracts (mainnet vs design-stage, 2026-05-20)

### Soroban contracts (C-strkey)

| Variant | Status | Mainnet address |
|---|---|---|
| v1 Payment (original) | LIVE | `CAC5SKP5FJT2ZZ7YLV4UCOM6Z5SQCCVPZWHLLLVQNQG2RWWOOSP3IYRL` |
| v1 Payment (deploy 2) | LIVE | `CCRLTS3CMJHYHFD7MYRBJPNW6R3LCXNDO2B6TK6AS6FSXAHR6GBMGLRE` |
| v1 Payment (deploy 3) | LIVE | `CAQPKW5AUPEA4C7OERZRUCBWT5RZDSETO4PR5REVRC5MT4CF3PBSKXQC` |
| v2 Forwarder | design-stage | (not deployed) |
| v2 IntentBridge | design-stage | (not deployed) |

All three live C contracts emit the same `PaymentEvent` / `FlushEvent`
schemas — confirmed by RozoAI 2026-05-21. The decoder matches by
`topic[0] = symbol_short!("payment" | "flush")`, so contract-set
expansion is a watchlist concern (cross-validation + activity
scoping), not a decoder-shape change.

Per RozoAI: "Our C wallets are mostly used when users are bridging
out from C wallets (when memo is not supported)." Most volume flows
through the relayer G-wallets below.

### Relayer classic accounts (G-strkey)

| Account | Role |
|---|---|
| `GADDIYCVR2Z6H46YWZE53LICP56ZBNEUUT2QAG4QHSWVIYE44HS7W3XY` | Relayer wallet — USDC / EURC |
| `GB4CLV3UMXDPFP5OQJQKUCWPRJXPXPJSHTUKZEJLAIZFZR7UHYAQ6EB4` | Relayer wallet — USDC / EURC |

Per RozoAI 2026-05-21: "those 2 addresses should cover most of the
txs on usdc/eurc." Memo-bearing bridge flows route through these
classic accounts; non-memo flows route through the C contracts.

These accounts don't emit Soroban events — they show up as the
source / destination of classic `payment` operations. Tracking
pattern: add to the supply observer's watched-account set or to a
bridge-specific observer recording deltas into
`bridge_relayer_balances`. Useful for: reconciling bridge inflow
vs outflow, flagging stuck relayer balances, deriving USDC / EURC
bridge volume independent of on-Soroban contract events.

The v1 README explicitly notes v2 is gated on Circle's CCTP
launch on Stellar (which is now live — see #40). Rozo v2
mainnet deployment is expected but not yet executed.

Source repos:
- https://github.com/RozoAI/rozo-intents-contracts (all variants)
- v1 contract verified at
  https://stellar.expert/explorer/public/contract/CAC5SKP5FJT2ZZ7YLV4UCOM6Z5SQCCVPZWHLLLVQNQG2RWWOOSP3IYRL

The repo also has a **third event schema** at
`stellar/contracts/rozo-intents/src/events.rs` — appears to be a
v2.1 / v3 unifying design that supersedes the v2 forwarder + 
intent_bridge split. Status unclear; not deployed. Captured below
for completeness.

## Canonical events

### v1 Payment (LIVE)

Source: `v1/stellar/payment/src/lib.rs`.

**`PaymentEvent`** — emitted by `pay(from, amount, memo)`.

- **Topics:** `("payment", from: Address)`  (using `symbol_short!("payment")`)
- **Body:** `PaymentEvent { from: Address, destination: Address, amount: i128, memo: String }`

USDC is hardcoded — `pay` only transfers USDC. The destination is
fixed at contract-init time. So a v1 PaymentEvent is "user X paid
N USDC to the wired destination with attached memo".

**`FlushEvent`** — emitted by `flush(token)` (admin path; sweeps
non-USDC balances).

- **Topics:** `("flush",)`
- **Body:** `FlushEvent { token: Address, destination: Address, amount: i128 }`

### v2 Forwarder (design-stage)

Source: `v2/stellar/forwarder/src/lib.rs`. Pre-mainnet —
schema may shift before deployment; this is the snapshot at
2026-05-20.

**`forward`** — emitted on `forward(token, amount, to, memo)`.
Topics: `("forward", sender: Address)`. Body: TBD (the
`event` value passed to publish is not captured in the grep
slice; needs full-source read at implementation time).

**Admin events** — `memo_set`, `memo_rm`, `proxy_set`. Topics
single-symbol `("memo_set",)` etc. Lower signal value (operator
config changes).

### v2 IntentBridge (design-stage)

Source: `v2/stellar/intent_bridge/src/lib.rs`. Pre-mainnet.

Topic-only single-symbol events:
- `("created",)` — intent created.
- `("filled",)` — intent filled by relayer.
- `("refunded",)` — intent refunded (timeout / failure).

Bodies not captured in the grep slice; full-source pass needed at
implementation time.

### v2.x / Future — `stellar/contracts/rozo-intents` (design-stage)

Source: `stellar/contracts/rozo-intents/src/events.rs`. Status
unclear — possibly v2.1 or v3. Captured for completeness; do NOT
ship decoder support until status is confirmed.

**`intent_created`** — `pub fn emit_intent_created(env, intent_id: BytesN<32>, sender: Address, source_token: Address, source_amount: i128, destination_chain_id: u64, receiver: BytesN<32>, destination_amount: i128, deadline: u64, relayer: BytesN<32>)`.

- **Topics:** `(Symbol::new(env, "intent_created"), intent_id: BytesN<32>)`
- **Body:** `(sender, source_token, source_amount, destination_chain_id, receiver, destination_amount, deadline, relayer)`

**`intent_filled`**

- **Topics:** `("intent_filled", intent_id)`
- **Body:** `(relayer, repayment_address, amount_paid)`

**`intent_failed`**

- **Topics:** `("intent_failed", intent_id)`
- **Body:** `(expected_fill_hash, received_fill_hash)`

**`intent_refunded`**

- **Topics:** `("intent_refunded", intent_id)`
- **Body:** `(refund_address, amount)`

Note that this newer schema uses long-form `Symbol::new(env,
"intent_created")` (10 chars) instead of `symbol_short!` (which
caps at 9 chars). The decoder must match the long-form bytes.

## Decoder design

**Phase 1 (now): v1 Payment only.**

The only LIVE contract is v1 Payment at `CAC5SKP5...IYRL`. Ship
the decoder for `PaymentEvent` + `FlushEvent` topics matched
against that contract address.

**Phase 2 (when v2 mainnet deploys): v2 Forwarder + IntentBridge.**

Add per-contract dispatch as the addresses become known. Topic
shapes are documented above; bodies need a full-source pass at
implementation time.

**Phase 3 (when status of rozo-intents is clarified): v2.x.**

The longer-form intent events with `intent_id` topic. Don't ship
until the user / operator confirms which schema variant is the
live one.

## Storage shape

Same shape question as CCTP. **Recommendation:** if `ClassBridge`
+ `cctp_events` lands per
[[docs/architecture/cctp-stellar-coverage]], extend that pattern
— either a separate `rozo_events` table or a generalised
`bridge_events` shape that both CCTP and Rozo write into.

A common `bridge_events` table is more elegant for cross-bridge
flow attribution ("which bridge moved the most USDC out of
Stellar yesterday?") but requires careful column design — CCTP
has `destination_domain: u32` (Circle's domain IDs), Rozo has
`destination_chain_id: u64` (arbitrary). The discriminator
column is non-trivial.

**Suggested decision tree:**

- If `bridge_events` lands: Rozo + CCTP share the table; rows
  tagged by `bridge_protocol`.
- If `cctp_events` lands separately: Rozo gets its own
  `rozo_events` table mirroring the shape.

## Backfill strategy

v1 Payment is live; live ingest from current ledger forward
covers everything moving forward. For history, the contract is
relatively new (deployment ledger TBD; check StellarExpert).
Backfill `[deploy, tip]` is small and cheap once decoder ships.

v2 + later: live-only from mainnet-deploy ledger when those
contracts go live.

## Open design questions (operator-gated)

1. ~~**`ClassBridge` source class** — same as CCTP's open
   question.~~ **Resolved 2026-05-20** — `ClassBridge` landed in
   `internal/sources/external/framework.go`. Rozo registry entries
   use `Class: ClassBridge`, same as CCTP.
2. **Storage shape** — `bridge_events` shared with CCTP, or
   `rozo_events` separate? See §Storage above.
3. **Phase-1 scope** — ship just v1 Payment now, or wait until
   v2 mainnet deploys so we can ship a coordinated v1+v2
   decoder? Recommendation: ship v1 now (it's the actual live
   surface), follow-up v2 when deployed.
4. **`rozo-intents` schema variant status** — which schema is the
   intended-live variant going forward? The v2 forwarder +
   intent_bridge split vs the newer single-package
   intent_created/filled/failed/refunded pattern? Affects
   Phase 2/3 decoder shape.

## Next steps (post-decision)

1. Operator confirms (1) ClassBridge taxonomy, (2) storage
   shape, (3) scope: ship v1 Payment now vs hold for v2.
2. Implement `internal/sources/rozo/{README.md, events.go,
   decode.go, consumer.go, source_test.go}` — initially Phase 1
   (v1 Payment only).
3. Add migration for chosen storage shape.
4. Register in `internal/sources/external/registry.go` with
   confirmed class.
5. WASM-history audit on v1 contract (single hash expected —
   v1 hasn't upgraded since deployment per the README being
   "v1, simplified version focused on Stellar").
6. Per CLAUDE.md "Soroban DeFi contracts upgrade in place":
   verify v1's upgrade path before flipping
   `BackfillSafe: true`.

## References

- Rozo repo: https://github.com/RozoAI/rozo-intents-contracts
- v1 mainnet contract:
  https://stellar.expert/explorer/public/contract/CAC5SKP5FJT2ZZ7YLV4UCOM6Z5SQCCVPZWHLLLVQNQG2RWWOOSP3IYRL
- v2 design docs:
  https://github.com/RozoAI/rozo-intents-contracts/tree/main/v2/docs
- Companion design: [[docs/architecture/cctp-stellar-coverage]]
- Class taxonomy: `internal/sources/external/framework.go`
- Existing source playbook: CLAUDE.md "Add a new on-chain
  Soroban DEX"

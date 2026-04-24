---
title: Per-contract schema evolution across versions — handling strategy
last_verified: 2026-04-23
status: living doc
---

# Per-contract schema evolution across versions

Stellar's XDR protocol version is handled elsewhere
([protocol-versions.md](../discovery/protocol-versions.md) — SDK
dispatches LedgerCloseMeta V0/V1/V2 for us). This doc is about
something different and less well-handled: **individual DeFi contracts
(Soroswap, Phoenix, Aquarius, Reflector) ship their own versions, and
those versions change the event schema our decoders parse.**

If we bolt real decoders onto today's live event shapes and then
backfill three months, we risk silently dropping (or worse,
mis-decoding) every event emitted under a prior version.

## The concern, concretely

Every Soroban contract supports `update_contract` — the admin can swap
the WASM while preserving storage. Topic names may stay stable across
an upgrade; body schemas frequently do not. Ways this bites us:

1. **Field renamed / reordered.** Soroban Map keys are Symbols. Old
   WASM emits `amount_0_in`, new WASM emits `amount0In`. Our decoder
   keyed on the old name silently returns zero.
2. **Field added / removed.** Old WASM emits a 4-field body, new
   emits 5. If we decode by positional arity rather than by named
   lookup, the new field shifts everything by one slot.
3. **Type widened.** i64 → i128 is the usual direction (ADR-0003).
   A decoder that assumes i64 truncates everything above 2^63.
4. **Topic shape change.** CAP-67 (P23, 2025-09-03) added a 4th topic
   (`sep0011_asset`) to every classic asset movement event. Pre-P23
   events have three topics; post-P23 have four. We already handle
   this in the classic-asset path
   ([cap-67-unified-events.md](../discovery/notes/cap-67-unified-events.md)),
   but the same pattern will recur per-contract.
5. **Factory redeployment.** A Soroswap v2 could appear as a *new*
   factory contract. Old pairs keep emitting old events under the
   v1 factory for months before migrating.

Live ingest only sees the current version. **Backfill sees every
version that ever ran** for the ledger range being replayed. A
decoder that works on today's topic[0] may produce zero results for
an event stream that predates the current WASM.

## What we know per source (2026-04-23)

### Soroswap
- Uniswap-V2 clone on Soroban; current mainnet WASM hash
  `18051456816b66f12e773a56f77c5794fac1b1fb7ab6e22d4fad5a412770f73e`
  (per `internal/sources/soroswap/events.go:36`).
- No confirmed prior versions. **Unverified.** Factory at
  `CA4HEQTL2...` — enumerate historical `new_pair` event schemas
  before accepting backfill from before the factory's first
  deploy-block.
- **Audit findings (2026-04-23, PR 164b real-fixture validation
  against mainnet):**
  - Topic[0] is `ScvString` (not `ScvSymbol`). Rust source
    `e.events().publish(("SoroswapPair", symbol_short!("swap")),
    event)` — the first tuple element is a string literal, which
    soroban-sdk serializes as ScvString. Our Phase-1 stub
    (`TopicSymbolSwap`) assumed Symbol; a topic-filter built from
    `Symbol("SoroswapPair")` returns zero events on mainnet.
  - Topic[0] carries the contract-group prefix (`"SoroswapPair"` or
    `"SoroswapFactory"`); topic[1] carries the event name as a
    Symbol. Our old `classify()` looked only at topic[0] — never
    matched anything.
  - Body shape (confirmed): `#[contracttype] struct { … }` serializes
    as `ScvMap` with Symbol field-name keys. Same pattern as
    Reflector's `#[contractevent]` single-field wrapper.
  - Real-fixture replay in
    `test/fixtures/soroswap/v1-2026-04-23/` — 8 swap+sync pairs
    from mainnet decode end-to-end.
  - `new_pair` factory events are infrequent (zero in a 100k-ledger
    window); the decoder is SDK-encode tested. Real capture
    requires a longer retention window on r1.

### Phoenix
- 8-events-per-swap pattern
  ([phoenix.md](../discovery/dexes-amms/phoenix.md)).
- Discovery doc does not record any earlier event shapes.
  **Unverified.**
- `scripts/*.sh` in the phoenix-contracts repo mentions multiple
  deploy rounds; we haven't pinned the ledger cut-over for any
  schema change.
- **Audit findings (2026-04-23, PR 164d real-fixture validation
  against mainnet):**
  - Both topic slots are `ScvString`, NOT `ScvSymbol`. The pool
    contract publishes `(str_literal, str_literal)` tuples and
    soroban-sdk serializes string literals as String. Verified
    against 5 live swap captures (40 field events total).
  - `"actual received amount"` with embedded spaces cannot be
    a Symbol anyway (Symbols are identifier-only) — fits with
    the String choice.
  - **Body shape is a bare single-value SCVal**, NOT wrapped in
    a Vec (like Aquarius 3-tuple) or Map (like Reflector /
    Soroswap). `publish(topics, scalar)` emits the scalar
    directly. Bodies encountered: `ScvAddress` (sender,
    sell_token, buy_token) and `ScvI128` (five amount fields).
  - Correlation key `(ledger, tx_hash, op_index)` already
    matches: all 8 events reliably share this triple in every
    captured fixture. Buffer TTL eviction untested against live
    data but unchanged from stub.
  - 5 real mainnet swaps decode end-to-end
    (`test/fixtures/phoenix/v1-2026-04-23/`).

### Aquarius
- Contracts have a **`UPGRADE_DELAY = 259200s` (3 days)** governance
  window with emergency-mode bypass
  ([aquarius.md §Upgrade delay](../discovery/dexes-amms/aquarius.md)).
- Explicit Phase-1 guidance: *"always decode events by topic name,
  not by cached WASM hash."* WASM hash
  `8844a760cf16788117b2a5a91d736794b3869c302aee47f8fbbcd0cc1a1096fd`
  recorded for the 2024-07-25 deploy but will rotate.
- No on-chain event-schema changelog.
- **Audit findings (2026-04-23, PR 164c real-fixture validation
  against mainnet):**
  - Our Phase-1 stub assumed trades carried a `Vec<i128>` parallel
    array of per-pool-asset amounts, with an N×N in/out fanout,
    and that token identities came from an externally-populated
    pool-info cache. All three were wrong.
  - Real contract
    (`aquarius-amm/liquidity_pool_events/src/lib.rs:122-150`,
    soroban-sdk 25.0.2) emits: topics
    `[Symbol("trade"), Address(token_in), Address(token_out), Address(user)]`;
    body is a Rust tuple `(in_amount as i128, out_amount as i128,
    fee as i128)` → serialized as `ScvVec` of 3 i128s.
  - Token identities are IN the topics — no pool cache needed on
    the trade path. 6 real mainnet trades decode end-to-end
    (`test/fixtures/aquarius/v2-2026-04-23/`). Stripped the unused
    `poolCache` / `SeedPool` / `WithSeededPools` / `lookupPool` /
    `PoolInfo` surface.
  - Body shape is **positional tuple**, not Map-by-field-name.
    Means schema drift in the body (e.g. adding a 4th field) would
    break silently unless we guard on arity — our decoder does,
    via `scval.AsTupleN(body, 3)`.
  - "Taker" in the trade event is almost always the Aquarius
    router contract (C-strkey), not the end-user G-account — our
    `canonical.Trade.Taker` stores the strkey as-is; consumer
    code that wants the real user must unwrap the router call via
    stellar-rpc `getTransaction`. Out of scope for PR 164c.

### Reflector
- **Three mainnet contracts** (DEX / CEX / FX), each independently
  upgradeable via admin `update_contract`
  ([reflector.md §Oracle upgrade](../discovery/oracles/reflector.md)).
- Current source is v3 (Code4rena-audited 2025-10).
- `version(env) -> u32` SEP-40 method exposes the contract version
  at read time — we should record it alongside every price ingest
  so historical decodes can branch on it.
- **Audit findings (2026-04-23, PR 164a real-fixture validation
  against mainnet):**
  - The comment at `internal/sources/reflector/events.go:61` claimed
    event body was `Map{"prices": Vec<(Asset, i128)>, "timestamp": u64}`.
    Actual contract (`reflector-contract/oracle/src/events.rs:4-10`)
    declares `timestamp` as `#[topic]` → topic[2]. Corrected.
  - soroban-sdk's `#[contractevent]` wraps **even a single non-topic
    field** in a Map keyed by field name. The body on the wire is
    `Map{"update_data": Vec<(Val, i128)>}`, NOT the raw Vec. Caught
    because SDK-encoded synthetic fixtures used the wrong shape; 4
    real mainnet captures (`test/fixtures/reflector/v6-2026-04-23/`)
    rejected all 4 synthetic-shape payloads.
  - topic[2] timestamp is **u64 milliseconds**, not seconds. The
    discovery doc correctly noted SEP-40's `last_timestamp()` method
    converts to seconds for public reads, but the raw event carries
    the internal millisecond value
    (`price_oracle.rs:74` divides by 1000 to expose seconds).
  - `stellar-rpc getEvents` topic filter is **position-count aware** —
    a 2-slot filter pattern won't match a 3-topic event. Must use
    the `"*"` wildcard (WildCardExactOne) at positions you don't
    want to constrain. Verified against
    `go-stellar-sdk/protocols/rpc/get_events.go:21`.
  - `Asset::Other(Symbol)` is a bare ticker that can be either fiat
    ("EUR", "USD", "ARS") OR crypto ("BTC", "ETH", "USDT"). ADR-0010
    covers the fiat case; **ADR-0014 (PR 164e) adds `AssetCrypto`**
    as a sibling variant with its own allow-list. Decoder tries
    fiat first, then crypto, then skips. All 10 real mainnet
    fixtures (4 DEX + 3 CEX + 3 FX) decode end-to-end after 164e.
  - ADR-0010 fiat allow-list extended 2026-04-23 with 13 additional
    ISO-4217 codes observed in the FX oracle's live payload.
  - ADR-0014 crypto allow-list seeded with 22 tickers (CEX feed
    contents + major global-cap cryptos).

## Handling strategy

**Short version: topic-name dispatch, named-field map lookup, and a
version column on every ingested row. Never key on WASM hash; never
rely on positional arity.**

Concretely:

1. **Decode events by field name, not position.** SCVal Maps carry
   their keys; use them. Positional decoding breaks under field
   reorder or insertion.
2. **Dispatch by topic[0] string, not contract address.** Contract
   addresses are per-deployment; topic symbols are per-protocol and
   change only via explicit upgrade. Keeps connector logic
   reusable across redeploys.
3. **Record contract WASM hash per ingested ledger range.** Cheap
   (single row per (contract, wasm_hash, first_ledger, last_ledger));
   lets backfill know which decoder version applies to which
   historical range. Populated from `getLedgerEntries` against the
   contract's instance-storage entry.
4. **Gate backfill behind a version audit.** Before we backfill
   more than N ledgers past "now", a tool (`ratesengine-ops
   schema-audit <source>`) must walk the contract's WASM-hash history
   and confirm each observed hash has a decoder variant registered.
   Unknown hash = backfill refuses to proceed for that contract.
5. **Keep per-version decoder variants in one place per source.**
   Each `internal/sources/<venue>/decode.go` accepts a
   `decoderVariant` selector (symbol, not address). Adding support
   for an older version = adding one function and one variant, no
   source-wide refactor.
6. **Fixtures are per-version.** `test/fixtures/<venue>/<wasm_hash>/`
   — golden base64 SCVal captures from real RPC for each version we
   claim to support. A decoder PR that doesn't add the fixture it
   targets cannot be reviewed.

## What's NOT yet done

- [ ] Per-source audit: enumerate every historical WASM hash for
      each of the four Soroban sources. Blocked on live mainnet RPC
      access (r1 stack is up; query hasn't been written).
- [ ] `ratesengine-ops schema-audit` CLI. Not scoped in Phase 1
      delivery plan; add to backlog with a pointer back to this
      doc.
- [ ] Column `contract_wasm_hash` on the `trades` and
      `oracle_updates` hypertables. No migration written yet.
- [ ] Extend this doc with Comet, SDEX, Blend, Redstone, Band as
      those connectors come online. Same concerns apply.

## Why this is an architecture doc, not an ADR

No single reversible decision to ratify. This is a **systemic
constraint** on every decoder PR — it belongs next to `ha-plan.md`
and `coverage-matrix.md` as a living architectural concern, not as
a one-shot decision. If a specific handling mechanism (e.g. the
`schema-audit` CLI shape) warrants an ADR later, we add one then and
link from here.

## References

- [docs/discovery/protocol-versions.md](../discovery/protocol-versions.md)
  — Stellar *protocol*-version handling (separate concern, well-solved).
- [docs/adr/0013-go-stellar-sdk-xdr-for-scval.md](../adr/0013-go-stellar-sdk-xdr-for-scval.md)
  — SDK dep decision; migration plan §5 explicitly calls for
  per-event-shape fixtures.
- [docs/operations/r1-deployment-state.md §SCVal decoders are stubs](../operations/r1-deployment-state.md)
  — Task #164 context.
- Per-source Phase-1 discovery:
  [soroswap](../discovery/dexes-amms/soroswap.md),
  [phoenix](../discovery/dexes-amms/phoenix.md),
  [aquarius](../discovery/dexes-amms/aquarius.md),
  [reflector](../discovery/oracles/reflector.md).

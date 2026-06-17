---
title: Comet WASM-history audit
last_verified: 2026-05-03
status: ratified — v2 folded into Blend audit
source: comet
backfill_safe: true
v2_audit: blend.md
---

# Comet WASM audit

Audit log for the `comet` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

> **2026-05-03 update — Comet's v2 audit is folded into Blend's.**
> On mainnet today, **the only deployed Comet pool is Blend's
> backstop** (`CAS3FL6TLZKDGGSISDBWGGPXT3NRR4DYTZD7YOD3HMYO6LTJUVGRVEAM`).
> Comet (the protocol) is a Balancer-v1-style weighted-AMM
> implementation that Blend uses for its backstop module; it
> isn't actively maintained as a standalone DEX. The
> per-instance v2 walk for "every Comet pool" is therefore the
> v2 walk for "the Blend backstop pool", which is captured in
> [`blend.md` §"Phase 2 results"](blend.md). This file
> documents the decoder + the protocol-level relationship; the
> v2 hash inventory and disassembly evidence live in
> blend.md.
>
> **2026-05-01 update.** Hash citations in this file have been
> cross-checked against the 2026-04-30 r1 walk; see
> [r1-walk-2026-05-01.md](r1-walk-2026-05-01.md) for the
> consolidated cross-source picture and current contract+WASM
> inventory.

## Status

**Ratified 2026-04-29.** `BackfillSafe` flips `false` → `true` in
`internal/sources/external/registry.go` in the same PR as this
audit. The known mainnet Comet deployment — Blend's backstop pool
`CAS3FL6T...` — has WASM hash `8abc28913035c074...` (verified
against Blend's mainnet snapshot at L55,261,759 + current
`stellar contract fetch --id`). Decoder-compatibility confirmed
via interface inspection + binary-string verification.

## Contracts under audit

Comet has **no canonical mainnet factory** in our codebase — the
source is a Balancer-v1-style weighted-AMM implementation
deployable as a library. CLAUDE.md flags this:

> **Comet uses a shared `("POOL", <event>)` topic across every pool
> contract**, not a per-protocol namespace. The decoder matches by
> topic bytes, not pool contract ID — any pubnet contract that
> deploys Balancer-v1 Comet code will look identical on the wire.

This makes Comet's audit shape distinct from Soroswap / Aquarius /
Phoenix: there's no factory we can enumerate from. Instead, the
audit unit is the **WASM-hash + event-shape pair**: any pool that
emits `("POOL", "swap")` with the expected body shape decodes
correctly.

| role | contract |
| --- | --- |
| Blend backstop Comet pool | `CAS3FL6TLZKDGGSISDBWGGPXT3NRR4DYTZD7YOD3HMYO6LTJUVGRVEAM` |
| WASM hash | `8abc28913035c07411ed5d134e6bfeab4723d97ddd4d1a22a0605d35c94d1a36` |

The Blend backstop is the **only known Comet pool on mainnet** at
audit time. Sources:

- Whether there is a standalone Comet DEX with public trading
  pools is open — we found only the Blend backstop.
- The mainnet snapshot at L55,261,759 in
  `.discovery-repos/blend-contracts/test-suites/src/mainnet-55261759-snapshot.json`
  contains a single `Comet Pool Token` ledger entry, matching this
  contract.
- Current `stellar contract fetch --id <pool>` returns the same
  WASM hash → no upgrade since the snapshot.

## Decoder expectations

Captured from `internal/sources/comet/{events,decode}.go` at HEAD
as of 2026-04-27.

### Topic structure (all five POOL events)

Verified 2026-04-23 against the public contract source
(`comet-contracts/src/c_pool/event.rs` and `call_logic/pool.rs:21,184-191`);
re-verified 2026-05-26 for the join/exit/deposit/withdraw
additions (#26):

    topic[0] = ScvSymbol("POOL")
    topic[1] = ScvSymbol("<kind>")
              // "swap" | "join_pool" | "exit_pool" | "deposit" | "withdraw"

    swap body     = ScvMap {
        "caller": Address, "token_in": Address, "token_out": Address,
        "token_amount_in": i128, "token_amount_out": i128,
    }
    join_pool / deposit body = ScvMap {
        "caller": Address, "token_in": Address, "token_amount_in": i128,
    }
    exit_pool body = ScvMap {
        "caller": Address, "token_out": Address, "token_amount_out": i128,
    }
    withdraw body  = ScvMap {
        "caller": Address, "token_out": Address,
        "token_amount_out": i128, "pool_amount_in": i128,
    }

Classification is **byte-equal** against pre-encoded `ScvSymbol`
constants. Since #26 the decoder claims all five POOL kinds:
`swap` → `canonical.Trade` (table `trades`), the other four →
`LiquidityEvent` (table `comet_liquidity`, migration 0042). Any
other `(POOL, *)` topic (e.g. a hypothetical `set_controller`
event from a future contract upgrade) returns `ErrNotCometEvent`
and is counted as an orphan event so a sustained spike alerts
operators to add a handler.

The Soroban port emits **only** these five topics under `POOL`. The
EVM-Balancer-v1 admin events (`bind` / `rebind` / `unbind` /
`finalize` / `gulp` / `set_swap_fee` / `set_controller` /
`set_public_swap`) are NOT in the Stellar port — either the
function doesn't exist or it's storage-only with no event
publication (verified 2026-05-26 against
`contracts/src/c_pool/{comet.rs, token_utility.rs}` upstream).
BPT (pool-share) `transfer` events use the SEP-41 standard
token-event surface and are claimed by
`internal/sources/sep41_supply` when the pool is in scope.

### Body extraction

Decoders pull every field **by name** (Map-keyed). Same robust
pattern as Soroswap (vs Aquarius's positional Vec) — adding a new
field doesn't break extraction; renaming or removing a field does.

| event | field | extracted by | invariant |
| --- | --- | --- | --- |
| swap | `token_in` | `scval.AsAddressStrkey` | valid Soroban Address |
| swap | `token_out` | same | same |
| swap | `token_amount_in` | `scval.AsAmountFromI128` | i128, sign > 0 |
| swap | `token_amount_out` | same | same |
| swap | `caller` | `scval.AsAddressStrkey` | extracted but not used in trade today |
| join_pool / deposit | `token_in` | `scval.AsAddressStrkey` | valid Soroban Address |
| join_pool / deposit | `token_amount_in` | `scval.AsAmountFromI128` | i128, sign > 0 |
| join_pool / deposit | `caller` | `scval.AsAddressStrkey` | stored as the LP user |
| exit_pool | `token_out` | `scval.AsAddressStrkey` | valid Soroban Address |
| exit_pool | `token_amount_out` | `scval.AsAmountFromI128` | i128, sign > 0 |
| exit_pool | `caller` | `scval.AsAddressStrkey` | stored as the LP user |
| withdraw | `token_out` / `token_amount_out` / `caller` | as above | as above |
| withdraw | `pool_amount_in` | `scval.AsAmountFromI128` | i128, sign > 0 (BPT burned) |

Decoder rejects with `ErrNonPositiveAmounts` if any required
amount is zero / negative. For swaps the trade direction is
`(token_in, token_amount_in) → base`,
`(token_out, token_amount_out) → quote`. Liquidity events stamp
`direction = 'add'` (join_pool / deposit) or `'remove'`
(exit_pool / withdraw) onto the row.

## Failure modes specific to Comet

1. **Topic[0] symbol change** — `"POOL"` → other namespace
   (`"COMET_POOL"`?) silently drops every event.
2. **Topic[1] symbol change for swap** — `"swap"` → `"trade"`
   silently drops every trade. Same hazard applies to each of
   `join_pool` / `exit_pool` / `deposit` / `withdraw` since #26 —
   a rename of any of the five known kinds drops that kind from
   `comet_liquidity` until the decoder is updated. Brand-new
   variants (e.g. a future `(POOL, set_controller)`) return
   `ErrNotCometEvent` and bump the orphan-events counter rather
   than landing as a typed row.
3. **Body field rename** — `token_in` → `tokenIn`, `token_amount_in`
   → `amount_in`, etc. Decoder fails per event with field-not-found;
   every swap dropped under the renamed WASM.
4. **Body field removal** — same effect as rename.
5. **Body field type change** — i128 → u128, Address → bytes —
   strict extraction errors per event.
6. **Body shape change Map → Vec** — would error at the Map-cast
   step; every swap dropped.
7. **A new pool architecture deployed at the same WASM lineage that
   emits a different swap shape** — since Comet has no central
   factory, a "Comet v2" might emit `("POOL", "swap")` with a
   different body shape, and we'd only learn about it via Hubble
   cross-check (count diff) or a per-pool review of the new pool's
   WASM hash. **This is the unique-to-Comet failure mode** — for
   other Soroban DEXes a factory upgrade is the source of truth;
   for Comet, every pool is a potential variant.

## WASM timeline

Single pool, single hash, no upgrades observed since first
deployment:

- **Pool contract**: `CAS3FL6T...` (deployed by the Blend
  backstop-bootstrapper as part of Blend's mainnet rollout).
- **WASM hash**: `8abc28913035c074...` recorded in the L55,261,759
  Blend mainnet snapshot. Last modified ledger of the pool's
  contract-data entry in that snapshot is L51,499,546 (when the
  pool was first instantiated by Blend's deploy).
- **Current state (2026-04-29)**: `stellar contract fetch --id` of
  the pool returns the same hash → no `update_current_contract_wasm`
  events between L51,499,546 and r1's current tip.

The original wasm-history walk did not include this contract in
its watch list (Comet wasn't tracked in the original 13-contract
list because no factory was known). The contract has been
audited via `stellar contract fetch --id` instead — a one-shot
current-state read sufficient when there's only one pool.

## Per-hash review findings

| hash (first 16) | role | active range | reviewer | finding |
| --- | --- | --- | --- | --- |
| `8abc28913035c074` | Blend backstop pool (CAS3FL6T...) | L51,499,546 (first deploy) → r1 current tip (no upgrade) | ash@2026-04-29 | matches current decoder |

### `8abc28913035c074` — Blend backstop pool, single hash

**Disassembly evidence:**

1. **Contract interface** (via `stellar contract info interface`):
   exposes `swap_exact_amount_in`, `swap_exact_amount_out`,
   `join_pool`, `exit_pool`, plus the `ALLOWANCE` / `Balance` /
   `SwapFee` etc. SEP-41-token surface (Comet pool tokens are LP
   shares). Matches the `c_pool/call_logic/pool.rs` source the
   decoder was verified against in 2026-04-23.
2. **Binary strings** include all decoder-relevant constants:
   `SwapEvent`, `DepositEvent`, `WithdrawEvent`, plus the body
   field names `caller`, `token_in`, `token_out`,
   `token_amount_in`, `token_amount_out` (all visible in the
   data section as a concatenated literal). The decoder pulls
   exactly these 5 fields by name — every one is preserved in
   the binary.
3. **Topic encoding**: `POOL` is a 4-char Soroban small symbol
   (encoded as a u64 constant in the WASM); `swap` is similarly
   small. Both byte-encoded forms match the decoder's pre-computed
   `TopicSymbolPool` / `TopicSymbolSwap` constants
   (verified 2026-04-23 via fixture capture).

## Caveats

- **Pool-of-pools enumeration is structural, not exhaustive.** Any
  contract on mainnet emitting `("POOL", "swap")` events would be
  picked up by our topic-based decoder. We've identified the only
  known Comet deployment (Blend backstop). If a future Comet pool
  is deployed that uses the SAME canonical WASM (which is what the
  Comet contracts repo publishes — there's no contract factory),
  it will run the same decoder code path and produce decoder-
  compatible trades. If a fundamentally-different Balancer-v1
  port emerges that emits the same `POOL`/`swap` topic but with
  a different body shape, that's a "Comet v2" detection problem
  flagged in the Failure modes section above; the decoder fails
  loud (`ErrMalformedPayload` per event) rather than silent. **The
  topic-based design means BackfillSafe applies to any range up
  to the last audit verification, conditional on no
  topic-squatting deployment having happened in that range.**
- **No automated re-verification when new Comet pools deploy.** A
  new pool deployment doesn't trigger this audit. Operators
  should monitor the `comet`-source trade volume and
  `comet`-source distinct-contract-count metrics; a sudden surge
  could indicate a new pool that wasn't part of this audit.

## Decision

**`BackfillSafe: true`** — flipped in
`internal/sources/external/registry.go` in this PR.

Rationale:

- The only known Comet deployment on mainnet (Blend backstop
  pool `CAS3FL6T...`) runs WASM `8abc28913035c074...` which
  matches the current decoder.
- WASM bytes fetched + binary-verified inline; all 5 SwapEvent
  body field names preserved.
- No upgrade events from first deployment (L51,499,546) through
  r1's current tip — the WASM is stable.
- Topic-based decoder design is robust to any future Comet pool
  using the canonical Balancer-v1 WASM (the same audited bytes).
  A non-canonical contract emitting `("POOL", "swap")` with a
  different body shape would fail decoder extraction (fail-loud
  via `ErrMalformedPayload`) rather than silently mis-attribute.
- Live ingest health: 0 `ErrMalformedPayload` rate spikes against
  the comet source.

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/comet/{events,decode}.go`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["comet"].BackfillSafe`
- Upstream contract source: local checkout under
  `.discovery-repos/comet-contracts/`

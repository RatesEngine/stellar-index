---
title: Band WASM-history audit
last_verified: 2026-05-01
status: ratified
source: band
backfill_safe: true
---

# Band WASM audit

Audit log for the `band` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

> **2026-05-01 update.** Hash citations in this file have been
> cross-checked against the 2026-04-30 r1 walk; see
> [r1-walk-2026-05-01.md](r1-walk-2026-05-01.md) for the
> consolidated cross-source picture and current contract+WASM
> inventory.

## Status

**Ratified 2026-04-29.** `BackfillSafe` flips `false` → `true` in
`internal/sources/external/registry.go` in the same PR as this
audit. The StandardReference contract shows **one stable WASM hash**
across the entire post-deploy window. No `update_contract` events
observed. Per-hash review against the live decoder's positional
op-args reader confirms function signatures and Vec tuple order
match.

## Contracts under audit

| role | mainnet contract |
| --- | --- |
| StandardReference | `CCQXWMZVM3KRTXTUPTN53YHL272QGKF32L7XEDNZ2S6OSUFK3NFBGG5M` |

The address is configured via `cfg.Oracle.Band.StandardReferenceContract`
in `ratesengine.toml`; the value above is the published mainnet
contract per `docs/discovery/oracles/band.md`.

## Decoder expectations — Band is structurally unique

Captured from `internal/sources/band/{events,decode}.go` at HEAD as
of 2026-04-29. Re-verified 2026-04-24 against pinned source.

Per CLAUDE.md:

> **Band's Soroban contract emits zero events.** A conventional
> topic-match Decoder never fires on Band. We observe the
> `relay()` / `force_relay()` InvokeContract call instead via
> the dispatcher's `ContractCallDecoder` interface (PR 168). Any
> future Soroban source that updates storage without publishing
> events plugs into the same hook — match by (contract_id,
> function_name), decode from op args.

So Band's audit is **fundamentally different** from every other
on-chain source's:

- **There are no events to decode.** wasm-history's
  `LedgerEntryChange` walk still works for tracking the contract's
  WASM evolution, but there's no event-shape audit because there
  are no events.
- **The decoder operates on op args**, not event bodies. The
  audit reviews function signatures + arg shapes against each WASM
  hash, not topic + body.
- **Failure modes are op-args-shaped.**

### Watched function signatures

Verified against `band-soroban/src/contract.rs:23-35`:

    relay(
        from:         Address,
        symbol_rates: Vec<(Symbol, u64)>,
        resolve_time: u64,
        request_id:   u64,
    )

    force_relay(
        symbol_rates: Vec<(Symbol, u64)>,
        resolve_time: u64,
        request_id:   u64,
    )

`force_relay` drops the `from` arg — admin-only path, not gated by
the relayer check. Both produce the same logical output: one
`(Symbol, rate)` pair per entry written to Band's `ref_data`
storage.

### Decoder reads args by position

The decoder reads InvokeContract args **positionally** — there's no
named-arg shape to extract by name. A reorder of args silently
produces wrong attribution.

| function | arg index | arg shape | what we extract |
| --- | --- | --- | --- |
| `relay` | 0 | Address | (currently ignored — relayer identity) |
| `relay` | 1 | Vec<(Symbol, u64)> | (symbol, rate) pairs |
| `relay` | 2 | u64 | resolve_time (UNIX seconds) |
| `relay` | 3 | u64 | request_id (currently ignored) |
| `force_relay` | 0 | Vec<(Symbol, u64)> | (symbol, rate) pairs |
| `force_relay` | 1 | u64 | resolve_time |
| `force_relay` | 2 | u64 | request_id |

### Rate scale + denomination

- Rates are `u64` at **E9 = 10^9** scale (per
  `band-soroban/src/constant.rs`). Every relayed rate uses this
  scale.
- Single-symbol rates from `relay` calls are **USD-denominated** —
  `get_ref_data(XYZ)` returns XYZ priced in USD. Pair rates (
  `get_reference_data`) are computed on-read at E18 — we **don't
  emit those** because they're a function of storage state, not
  wire input.
- Timestamps: `resolve_time` is UNIX seconds (verified against
  `env.ledger().timestamp()` comparison in `ref_data.rs:56`).

### Symbol allow-lists

The decoder skips symbols not on its fiat-or-crypto allow-list with
`ErrUnknownSymbol` (per-entry). A new symbol relayed by Band that
we haven't allow-listed is silently dropped — list lives in the
discovery doc + the package's symbol_resolver.

## Failure modes specific to Band

1. **`relay` / `force_relay` function rename** — the decoder's
   `(contract_id, function_name)` match key is the entry-point.
   Either function renamed → no calls dispatch to us → silent drop
   of every Band update.
2. **Function signature reorder** — e.g. `relay(symbol_rates, from,
   resolve_time, request_id)`. Positional decoder reads index 0 as
   the symbol_rates Vec → fails on type mismatch (Address vs Vec
   tuple). Per-call error, every call dropped under affected WASM.
3. **New optional arg added** — Soroban contracts can extend
   signatures by adding args. If a future relay accepts an extra
   `signer: Address` at index 4, our decoder ignores trailing args
   so we still extract the first 4 correctly. **Probably safe**,
   pending verification.
4. **Args swapped without signature change** (e.g. `(symbol, u64)`
   → `(u64, symbol)` inside the inner Vec) — silently produces
   wrong attribution. **No automated detection** — every new WASM
   hash needs source review.
5. **Rate scale change E9 → E18** — silently mis-reports every
   price. Caught only by cross-source divergence vs Reflector /
   Redstone.
6. **`u64` → `u128` rate type** — strict extraction errors per
   entry; every entry dropped.
7. **Rate sign change `u64` → `i64`** — possible if Band ever
   needed negative rates (e.g. pricing-model deltas). Strict u64
   extraction errors per entry.
8. **`from` Address required for force_relay** (adding gating) —
   would break our positional read of `force_relay` (index 0 would
   be Address instead of Vec). Per-call error.
9. **`get_ref_data` / `get_reference_data` semantics change** —
   doesn't affect our decoder (we don't emit pair rates from
   relay), but downstream consumers reading the pair-rate API
   would see different values. Out of scope for this audit.

## WASM timeline

Output from `ratesengine-ops wasm-history` over the post-Soroban
window — full archive on r1, walked 2026-04-29:

```json
[
  {
    "contract": "CCQXWMZVM3KRTXTUPTN53YHL272QGKF32L7XEDNZ2S6OSUFK3NFBGG5M",
    "ranges": [
      { "wasm_hash": "6cdb9a3cdeec01a1...",
        "from_ledger": 50842736, "to_ledger": 51931461 }
    ]
  }
]
```

The single range is observed only in the first worker's chunk
(where the original `CreateContract` lives at L50,842,736,
2024-03-19). Later workers saw no `update_current_contract_wasm`
event for the contract, so produced no entries — consistent with
**one Band StandardReference WASM** active across the full
post-deploy window through to walk-end at L59,301,651. Live ingest
from walk-end through r1's current tip (L62,342,614 as of
2026-04-29) confirms no further upgrade: 0 `ErrFunctionMismatch`
or type-extraction failures.

Soroban activated at L50,457,424 (2024-02-20); Band's first deploy
at L50,842,736 (2024-03-19) is the published mainnet launch.
Pre-Soroban ledgers can't host the contract.

## Per-hash review findings

| hash (first 16) | role | active range | reviewer | finding |
| --- | --- | --- | --- | --- |
| `6cdb9a3cdeec01a1` | StandardReference | L50,842,736 → L59,301,651 (walk-end; still current per live ingest through r1 tip L62,342,614) | ash@2026-04-29 | matches current decoder |

### `6cdb9a3cdeec01a1` — StandardReference, single hash, no upgrade

- **Function signatures**: `relay(Address, Vec<(Symbol, u64)>, u64, u64)`
  and `force_relay(Vec<(Symbol, u64)>, u64, u64)` match the
  positional reader in `internal/sources/band/decode.go`. Phase-1
  source review at `docs/discovery/oracles/band.md` pins
  `band-soroban@<release>` as the source of truth; the deployed
  WASM hash `6cdb9a3c…` corresponds to that source release (no
  rebuild post-deploy).
- **Inner Vec tuple order**: `(Symbol, u64)` — verified against
  `band-soroban/src/contract.rs` and reproduced in
  `internal/sources/band/decode_test.go` golden fixtures captured
  from live mainnet calls.
- **Rate scale**: E9 confirmed against
  `band-soroban/src/constant.rs`; live decoder applies the same
  scale via `bandRateScale = 1e9` constant.
- **No `update_current_contract_wasm` events** in the entire
  post-deploy window rule out signature drift across this range.
- Live ingest health: 0 `ErrFunctionMismatch` / 0 type-extraction
  failures observed in production metrics since the
  ContractCallDecoder hook landed (PR #168, 2026-04 cutover).

## Decision

**`BackfillSafe: true`** — flipped in
`internal/sources/external/registry.go` in this PR.

Rationale:

- StandardReference contract has **one stable WASM hash** across
  the entire post-deploy window — no upgrade events to decode
  against.
- Decoder's positional op-args reader matches the deployed WASM's
  function signatures (verified via Phase-1 fixtures + ongoing
  production ingest health).
- Band's structural simplicity (no events, no per-pair contracts,
  no factory-template indirection) means there is no analog to
  Soroswap's pair-WASM caveat.

If a future Band upgrade lands, the audit gets a per-hash entry +
decoder verification and the flag stays at `true` (or flips to
`false` if the new WASM diverges and the decoder fix isn't shipped
yet).

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/band/{events,decode}.go`
- Discovery doc: `docs/discovery/oracles/band.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["band"].BackfillSafe`
- Upstream contract source: pinned in `VERSIONS.md`
- WASM-history walk JSON (full): `r1:/var/log/wasm-history-all.json`

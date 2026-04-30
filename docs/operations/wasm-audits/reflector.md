---
title: Reflector WASM-history audit
last_verified: 2026-04-29
status: partial — fx ratified; dex + cex pending v2-era WASM review
sources: reflector-dex, reflector-cex, reflector-fx
backfill_safe: fx=true; dex=false; cex=false
---

# Reflector WASM audit

Audit log for the three Reflector source variants —
`reflector-dex`, `reflector-cex`, `reflector-fx`. All three share
**one decoder** and **one event shape**; they differ only in which
on-chain contract emits the events. We audit them as a single unit
for the wire format but make per-variant `BackfillSafe` decisions
because each contract has its own deploy history.

See `README.md` for the full procedure.

## Status

**Partial 2026-04-29.** Two unique WASM hashes observed across the
three contracts; FX has only ever run the current production hash
and flips to `BackfillSafe: true` in this PR. DEX and CEX both
upgraded from an earlier v2-era hash to the current production hash
in late April 2024; flipping them to `true` requires verifying the
v2-era event shape against the current decoder, which needs the
v2 WASM bytes (out of scope for this PR — see "Caveats").

## Contracts under audit

Per CLAUDE.md "Reflector is three separate contracts (DEX / CEX /
FX), not one." Each variant maps to a distinct mainnet contract:

| variant | source name | mainnet contract |
| --- | --- | --- |
| DEX | `reflector-dex` | `CALI2BYU2JE6WVRUFYTS6MSBNEHGJ35P4AVCZYF3B6QOE3QKOB2PLE6M` |
| CEX | `reflector-cex` | `CAFJZQWSED6YAWZU3GWRTOCNPPCGBN32L7QV43XX5LZLFTK6JLN34DLN` |
| FX  | `reflector-fx`  | `CBKGPWGKSKZF52CFHMTRR23TBWTPMRDIYZ4O2P5VS65BMHYH4DXMCJZC` |

Three legacy / placeholder contract IDs from `docs/discovery/oracles/reflector.md`
(`CAVLP5DH…`, `CCYOZJCO…`, `CCSSOHTB…`) were also walked and
produced **NO_EVENTS** — they are inactive on mainnet and not in
the live decoder's contract list.

## Decoder expectations

Captured from `internal/sources/reflector/{events,decode}.go` at
HEAD as of 2026-04-29. Re-verified 2026-04-23 against the upstream
`#[contractevent]` macro expansion.

### Topic structure

    topic[0] = ScvSymbol("REFLECTOR")
    topic[1] = ScvSymbol("update")
    topic[2] = ScvU64(timestamp)        // unix milliseconds
    body     = ScvVec<(ScVal, ScI128)>  // per-entry tuple

The 3-element topic shape is unusual — `timestamp` is hoisted out
of the body and into a `#[topic]` slot via the `#[contractevent]`
macro. **Important historical correction in the source comments:**
the previous decoder comment claimed body was
`Map{"prices": Vec<(Asset, i128)>, "timestamp": u64}` — that's
WRONG; `#[contractevent]` expands tuple-shaped fields to ScvVec
with the fields in declaration order.

Classification is byte-equal against `TopicSymbolReflector` +
`TopicSymbolUpdate`. Any of those drifting silently drops every
event.

### Body extraction

Each tuple in the outer `Vec<(ScVal, I128)>` is one (asset, price)
pair. The first element identifies the asset — it can be:

- `ScvAddress` (Soroban contract address — for DEX/CEX variant)
- `ScvSymbol` (a fiat code like "USD" or asset symbol — for FX variant)

The decoder skips entries whose first element is neither
ScvAddress nor ScvSymbol (per `ErrUnknownAssetIdentifier`).

The second element is the price as `i128` at Reflector's documented
14-decimal scale.

The decoder fans **one event** out into **N OracleUpdate rows** —
one per (asset, price) tuple in the vector. To preserve the
unique-key constraint on `(source, ledger, tx_hash, op_index)`, the
fanout uses a per-entry op_index stride (matching the SDEX pattern).

### Asset identification

For `reflector-dex` / `reflector-cex` (Soroban Address tuples), the
asset is `canonical.NewSorobanAsset(strkey)`. For `reflector-fx`
(Symbol tuples), it's `canonical.NewFiatAsset(symbol_str)`.

A future contract upgrade that swapped DEX from Address to Symbol
(or vice versa) would still decode but produce wrong asset
classifications.

## Failure modes specific to Reflector

1. **Topic[0] / topic[1] symbol change** — `"REFLECTOR"` or
   `"update"` to anything else silently drops every event.
2. **Topic[2] type change** — `u64` → `i64` or `Symbol` for
   timestamp would error per event (`AsU64FromTopic` strict).
   Fail-loud, but every event in the range dropped.
3. **Body shape change Vec → Map** — the outer-Vec assumption
   breaks; every event errors at extraction.
4. **Per-entry tuple field reorder** — currently `(asset, price)`;
   a swap to `(price, asset)` would produce nonsense (the i128
   would be parsed as an Address). **Almost certainly fail-loud
   per entry**, but every event dropped under that WASM.
5. **Per-entry tuple length change** (e.g. adding a confidence
   score) — would error at the AsTupleN(2) check; every entry
   skipped.
6. **Asset identifier type mix-up across variants** — DEX/CEX
   start emitting Symbols (or FX starts emitting Addresses).
   Decoder still produces output but with wrong asset
   classification — silent. Per-WASM source review must verify
   each variant's tuple type matches its expected shape.
7. **Price scale change** — Reflector documents 14 decimals; if a
   contract upgrade switched to E18 or similar, the i128 still
   decodes but every recorded price is off by 10^N. **No automated
   detection** — caught only by cross-check against external
   oracle data sources.
8. **Vector overflow past OpIndex fanout stride** — if Reflector
   ever emits more than `opIndexFanoutStride` (1024) entries in a
   single event, our op_index synthesis collides. `ErrPriceVectorOverflow`
   surfaces this; would require a stride bump.

## WASM timeline

Output from `ratesengine-ops wasm-history` over the post-Soroban
window — full archive on r1, walked 2026-04-29:

```json
[
  {
    "contract": "CALI2BYU...",
    "ranges": [
      { "wasm_hash": "4a64c8c8502df326f4ce06d98998dc7d8a61575a11d6c0fbd4c60d10dfe28ffa",
        "from_ledger": 50644229, "to_ledger": 51656691 },
      { "wasm_hash": "df88820e231ad8f3027871e5dd3cf45491d7b7735e785731466bfc2946008608",
        "from_ledger": 51656692, "to_ledger": 59301651 }
    ]
  },
  {
    "contract": "CAFJZQWS...",
    "ranges": [
      { "wasm_hash": "4a64c8c8502df326f4ce06d98998dc7d8a61575a11d6c0fbd4c60d10dfe28ffa",
        "from_ledger": 50644239, "to_ledger": 51656688 },
      { "wasm_hash": "df88820e231ad8f3027871e5dd3cf45491d7b7735e785731466bfc2946008608",
        "from_ledger": 51656689, "to_ledger": 59301651 }
    ]
  },
  {
    "contract": "CBKGPWGK...",
    "ranges": [
      { "wasm_hash": "df88820e231ad8f3027871e5dd3cf45491d7b7735e785731466bfc2946008608",
        "from_ledger": 56733481, "to_ledger": 59301651 }
    ]
  }
]
```

Two unique hashes total across all three contracts:

- **`4a64c8c8…`** — DEX + CEX only. Active L50,644,229 →
  L51,656,691 (~1.0M ledgers, roughly 2024-02-19 → 2024-04-26 in
  wall time). Replaced at L51,656,689 (CEX) / L51,656,692 (DEX) —
  the 3-second offset between contracts indicates a coordinated
  upgrade pushed in the same operator session.
- **`df88820e…`** — current production hash on **all three**
  variants. DEX + CEX adopted at the v2→v3 upgrade (~2024-04-26);
  FX deployed fresh on this hash at L56,733,481 (~2025-06) and
  has never been on any other.

The DEX+CEX upgrade timing aligns with Reflector's documented
v2→v3 transition (per `docs/discovery/oracles/reflector.md`). The
v3-era binary is what every fixture in `internal/sources/reflector/`
was captured against.

Live ingest from walk-end (L59,301,651) through r1's current tip
(L62,342,614) confirms no further upgrade events for any of the
three contracts: `df88820e` is still production.

## Per-hash review findings

| variant | hash (first 16) | active range | reviewer | finding |
| --- | --- | --- | --- | --- |
| FX | `df88820e231ad8f3` | L56,733,481 → L59,301,651 (walk-end; current per live ingest) | ash@2026-04-29 | matches current decoder |
| DEX (post-v3) | `df88820e231ad8f3` | L51,656,692 → L59,301,651 (walk-end) | ash@2026-04-29 | matches current decoder |
| CEX (post-v3) | `df88820e231ad8f3` | L51,656,689 → L59,301,651 (walk-end) | ash@2026-04-29 | matches current decoder |
| DEX (pre-v3) | `4a64c8c8502df326` | L50,644,229 → L51,656,691 | (pending) | NOT YET REVIEWED — see Caveats |
| CEX (pre-v3) | `4a64c8c8502df326` | L50,644,239 → L51,656,688 | (pending) | NOT YET REVIEWED — see Caveats |

### `df88820e231ad8f3` — current production, all three variants

- Live decoder fixtures
  (`internal/sources/reflector/decode_test.go`,
  `real_fixture_test.go`) are captured from this WASM's emitted
  events. Topic shape `("REFLECTOR", "update", <u64 ms>)` and body
  `Vec<(asset, i128)>` match the by-vec-tuple extraction.
- All three variants (DEX/CEX/FX) emit the SAME wire format from
  this WASM (the decoder is variant-agnostic except for the
  ScvAddress vs ScvSymbol asset slot, which is handled by
  `ErrUnknownAssetIdentifier` skipping rather than by per-variant
  classification).
- 14-decimal price scale matches the constant in the decoder.
- Live ingest health: 0 `ErrMalformedPayload` /
  `ErrUnknownAssetIdentifier` rate spikes since FX support landed
  (PR #161, 2026-03 cutover).
- No `update_current_contract_wasm` events from
  L51,656,689 (DEX+CEX) / L56,733,481 (FX) through walk-end +
  ongoing live ingest = production hash is stable.

### `4a64c8c8502df326` — DEX + CEX pre-v3 hash (NOT YET REVIEWED)

This hash was active on DEX and CEX from L50,644,229 (DEX) /
L50,644,239 (CEX) — i.e., from each contract's first deploy in
February 2024 — through the v2→v3 upgrade at ~L51,656,690 in late
April 2024. ~1M ledgers / ~9 weeks of mainnet history under each
contract.

Reflector's documented v2→v3 transition involved replacing the
contract's internal price-storage layout and adding new view
methods (per Reflector team comms in 2024). Whether the **emitted
event shape** changed across the upgrade is the open question
this audit can't answer without the v2 WASM bytes. The decoder's
own historical-correction comment ("the previous decoder comment
claimed body was Map{...} — that's WRONG") was discovered
against post-v3 fixtures — we don't have evidence it applies to
v2.

## Caveats

- **v2-era (`4a64c8c8…`) WASM bytes not disassembled inline.**
  This audit's load-bearing safety claim is per-hash decoder
  compatibility; without the v2 bytes we can't claim the v2 event
  shape matches what the v3-tuned decoder expects. If a backfill
  replays L50,644,229 → L51,656,691 with a v3 decoder against v2
  events, two outcomes are possible:
  1. The shape was already `Vec<(asset, i128)>` in v2 (likely; the
     `#[contractevent]` macro is older than the v2 release per
     upstream Soroban SDK history) → backfill succeeds.
  2. The shape was different in v2 → decoder produces
     `ErrMalformedPayload` per event, and the backfill emits zero
     v2 trades. Not silently wrong, but silently incomplete.
- **v2 disassembly is the unblocker for DEX/CEX**. The follow-up
  needs to either: (a) use `stellar-core dump-wasm 4a64c8c8…` from
  any node that has the v2 bytes cached in its bucket dir, (b) pull
  via stellar-rpc `getLedgerEntry` against the WASM-storage key
  for that hash, or (c) hash-compare against
  `reflector-contract` git tags pre-2024-04-26 to find the matching
  release.

## Decision

| source | BackfillSafe | rationale |
| --- | --- | --- |
| `reflector-fx` | **`true`** (flipped in this PR) | Single WASM hash since first deploy; matches current decoder; live ingest healthy. |
| `reflector-dex` | `false` (unchanged) | Pending v2-era `4a64c8c8…` WASM disassembly. Production hash is verified, but the 1.0M-ledger pre-v3 window is not. |
| `reflector-cex` | `false` (unchanged) | Same as DEX — pre-v3 window unverified. |

A v2-disassembly follow-up PR will flip DEX + CEX once the v2
event shape is verified against the current decoder.

If the v2 shape diverges, that follow-up ships either: (a) a
decoder that handles both shapes (gated by the contract's WASM
hash at decode time), or (b) a contracted backfill cutoff that
refuses replay of pre-v3 ranges for these sources.

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/reflector/{events,decode}.go`
- Discovery doc: `docs/discovery/oracles/reflector.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["reflector-{dex,cex,fx}"].BackfillSafe` (three entries)
- Upstream contract source: `https://github.com/reflector-network/reflector-contract`
- WASM-history walk JSON (full): `r1:/var/log/wasm-history-all.json`

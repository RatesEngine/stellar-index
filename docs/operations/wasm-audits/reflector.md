---
title: Reflector WASM-history audit
last_verified: 2026-05-03
status: ratified — v2 walk confirms two-hash inventory
sources: reflector-dex, reflector-cex, reflector-fx
backfill_safe: true
---

# Reflector WASM audit

Audit log for the three Reflector source variants —
`reflector-dex`, `reflector-cex`, `reflector-fx`. All three share
**one decoder** and **one event shape**; they differ only in which
on-chain contract emits the events. We audit them as a single unit
for the wire format but make per-variant `BackfillSafe` decisions
because each contract has its own deploy history.

See `README.md` for the full procedure.

> **2026-05-03 update — v2 walk confirms two-hash inventory.**
> The 2026-04-30 wide-net r1 walk re-observed the v2 (`4a64c8c8…`)
> → v3 (`df88820e…`) transition on DEX (`CALI2BYU…`) + CEX
> (`CAFJZQWS…`) at L51,656,689-91, and confirmed the FX
> (`CBKGPWGK…`) contract has been on `df88820e…` since first
> deploy at L56,733,481. **No further upgrades observed** through
> the walk's upper bound (L62,249,727). All three contracts
> currently run `df88820e…`. Bytes preserved + SHA-256-verified
> for both hashes at `evidence/r1-walk-2026-05-01/wasm-bytes/`
> on r1.
>
> **2026-05-01 update.** Hash citations in this file have been
> cross-checked against the 2026-04-30 r1 walk; see
> [r1-walk-2026-05-01.md](r1-walk-2026-05-01.md) for the
> consolidated cross-source picture and current contract+WASM
> inventory.

## Status

**Ratified 2026-04-29.** All three Reflector variants (DEX, CEX,
FX) flip `BackfillSafe: false → true` in this PR. Two unique WASM
hashes observed across the three contracts; both fetched and
analyzed via `stellar contract fetch` against
mainnet.sorobanrpc.com — interface diff between v2 (`4a64c8c8…`)
and v3 (`df88820e…`) is **cosmetic** (one removed governance
function, struct definition reordering); event-emitting types and
SDK-family are identical, so the wire format is preserved.

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
| DEX (pre-v3) | `4a64c8c8502df326` | L50,644,229 → L51,656,691 | ash@2026-04-29 | matches current decoder (disassembly) |
| CEX (pre-v3) | `4a64c8c8502df326` | L50,644,239 → L51,656,688 | ash@2026-04-29 | matches current decoder (disassembly) |

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

### `4a64c8c8502df326` — DEX + CEX pre-v3 hash (disassembly-confirmed)

This hash was active on DEX and CEX from L50,644,229 (DEX) /
L50,644,239 (CEX) — i.e., from each contract's first deploy in
February 2024 — through the v2→v3 upgrade at ~L51,656,690 in late
April 2024. ~1M ledgers / ~9 weeks of mainnet history under each
contract.

**Disassembly evidence** (added 2026-04-29):

WASM bytes fetched via `stellar contract fetch --wasm-hash
4a64c8c8…` against mainnet.sorobanrpc.com. Compared against the
v3 production hash (`df88820e…`) using `stellar contract info
interface` + data-section string analysis:

1. **Contract interface diff is cosmetic.** The v2→v3 transition
   removed a single governance function (`bump(env, ledgers_to_live:
   u32)` for storage TTL extension) and reordered the `PriceData` /
   `ConfigData` struct definitions in the rendered output. **Every
   public method signature relevant to event emission is
   unchanged** — `set_price(env, updates: Vec<i128>, timestamp:
   u64)` is identical, as is the `Asset { Stellar(Address) |
   Other(Symbol) }` enum and `PriceData { price: i128, timestamp:
   u64 }` struct. None of the cosmetic changes affects the
   event-publish wire format.
2. **Data-section field names are identical.** Both v2 and v3
   binaries contain the same heavy strings used in `Symbol::new`
   construction: `price`, `prices`, `timestamp`, `last_timestamp`,
   `asset`, `assets`, `base_asset`, `quote_asset`, `decimals`,
   `period`, `resolution`, `update_contract`, `updates`, `records`,
   `lastprice`. (The "REFLECTOR" / "update" topic Symbols are short
   enough to be encoded as small-symbol u64 constants and don't
   appear as raw strings in either binary — verified via
   `strings <wasm>`.)
3. **SDK family is the same.** v2 was built against soroban-sdk
   20.2.0 (commit 6e198b79); v3 against 20.3.2 (1d7f9bd8). Both are
   in the SDK 20.x line where the `#[contractevent]` macro
   (introduced in early 20.x) produces stable wire formats — a
   tuple-shaped event field expands to `ScvVec` of
   declaration-order entries; topic strings encode to small-symbol
   `ScvSymbol`. No SDK-internal change between 20.2.0 and 20.3.2
   touches event encoding.
4. **Source code at the v3-era release** (the only release our
   `.discovery-repos/reflector-contract` checkout has) shows the
   `#[contractevent(topics = ["REFLECTOR", "update"])] struct
   UpdateEvent { #[topic] timestamp: u64, update_data: Vec<(Val,
   i128)> }` pattern that matches the decoder's expected
   `topic[0..2] = ("REFLECTOR", "update", <u64>)` + `body =
   Vec<(Val, i128)>`. With contract spec, data section, and SDK
   family all identical between v2 and v3, the event shape is
   preserved.

**Conclusion**: the v3-tuned decoder will correctly decode v2-era
events. Backfill replays of L50,644,229 → L51,656,691 are safe.

## Decision

| source | BackfillSafe | rationale |
| --- | --- | --- |
| `reflector-fx` | **`true`** (flipped in PR #266) | Single WASM hash since first deploy; matches current decoder; live ingest healthy. |
| `reflector-dex` | **`true`** (flipped in this PR) | v2 (`4a64c8c8…`) + v3 (`df88820e…`) hashes both verified. v3 from fixtures + production health; v2 from disassembly + interface diff (cosmetic) + SDK-family compat. |
| `reflector-cex` | **`true`** (flipped in this PR) | Same evidence as DEX — both contracts share the same two hashes and the same disassembly findings apply. |

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/reflector/{events,decode}.go`
- Discovery doc: `docs/discovery/oracles/reflector.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["reflector-{dex,cex,fx}"].BackfillSafe` (three entries)
- Upstream contract source: `https://github.com/reflector-network/reflector-contract`
- WASM-history walk JSON (full): `r1:/var/log/wasm-history-all.json`

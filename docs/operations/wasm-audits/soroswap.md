---
title: Soroswap WASM-history audit
last_verified: 2026-05-03
status: ratified — v2 per-instance walk complete
source: soroswap
backfill_safe: true
---

# Soroswap WASM audit

Audit log for the `soroswap` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

> **2026-05-03 update — v2 per-instance walk complete.** The
> 2026-04-30 wide-net r1 walk inventoried all **196 Soroswap
> contracts** on mainnet (1 factory + 1 router + 194 pair
> instances), each pinned to a single WASM hash with no
> mid-life upgrades observed in the walk window. Per-instance
> evidence is itemised in `Phase 2 results` below; full hash
> bytes + disassembly artifacts live under
> `evidence/r1-walk-2026-05-01/` on r1. The remaining storage-
> rotation gap (factory `set_pair_wasm` ledger-entry walk) is
> tracked as a v3 follow-up — it cannot make backfill unsafe at
> rest because all 194 deployed pairs already share the audited
> hash.
>
> **2026-05-01 update.** Hash citations in this file have been
> cross-checked against the 2026-04-30 r1 walk; see
> [r1-walk-2026-05-01.md](r1-walk-2026-05-01.md) for the
> consolidated cross-source picture and current contract+WASM
> inventory.

## Status

**Ratified 2026-04-29.** `BackfillSafe` flips `false` → `true` in
`internal/sources/external/registry.go` in the same PR as this
audit. The factory + router walk produced one stable hash apiece
across the full post-Soroban window (L50,746,266 → L59,301,651,
~2024-03 → today). Per-hash review against the live decoder shows
no schema divergence. Pair-template WASM stability is documented as
a known caveat (see "Caveats" below) — addressed in a v2 audit
follow-up.

## Contracts under audit

Captured from `internal/sources/soroswap/events.go` (verified
2026-04-23 against `soroswap-core/public/mainnet.contracts.json`):

| role | contract / hash |
| --- | --- |
| Factory | `CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2` |
| Router | `CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH` |
| Pair WASM hash (current) | `18051456816b66f12e773a56f77c5794fac1b1fb7ab6e22d4fad5a412770f73e` |

The pair contracts themselves are deployed by the factory at
runtime; their per-instance contract IDs are enumerable from the
factory's `new_pair` events. Per-instance pair WASM walks land in a
follow-up; see "Caveats".

## Decoder expectations

Captured from `internal/sources/soroswap/{events,decode}.go` at
HEAD as of 2026-04-29. Any divergence from these in a deployed
WASM hash is an audit finding.

### Topic structure

Every Soroswap pair / factory event has a 2-element topic:

    topic[0] = ScvString
      - "SoroswapPair"     (pair-instance events: swap, sync, deposit, withdraw, skim)
      - "SoroswapFactory"  (factory events: new_pair)
      - "SoroswapRouter"   (declared but currently unused by the decoder)
    topic[1] = ScvSymbol  (event name)
      - "swap"      → trade-bearing event
      - "sync"      → pair-reserve update; correlated with swap
      - "deposit"   → liquidity provider deposit (not a trade)
      - "withdraw"  → liquidity provider withdraw (not a trade)
      - "skim"      → skim of accumulated fees (not a trade; skipped)
      - "new_pair"  → factory event; populates pair→(token0, token1) cache

Classification is **byte-equal** against pre-encoded base64 SCVal
constants (`TopicPrefixPair`, `TopicSymbolSwap`, etc.). A topic[0]
prefix renamed `"SoroswapPair"` → `"SoroswapPairV2"` (or similar)
silently drops every event from the upgraded contract.

### SwapEvent body

Defined in `pair/src/event.rs` as:

    SwapEvent {
        to:           Address,
        amount_0_in:  i128,
        amount_1_in:  i128,
        amount_0_out: i128,
        amount_1_out: i128,
    }

On the wire this serialises to ScvMap with 5 entries. Decoder pulls
**by name** (per CLAUDE.md "decode by Map-field-name not position"):

| field | extracted by | invariant the decoder relies on |
| --- | --- | --- |
| `amount_0_in`  | `scval.AsAmountFromI128` | i128, sign ≥ 0 |
| `amount_1_in`  | same | same |
| `amount_0_out` | same | same |
| `amount_1_out` | same | same |
| `to`           | (not extracted — ignored) | — |

Trade direction is derived from which of the four amounts is
non-zero. A well-formed swap has exactly one in/out pair non-zero —
either `(amount_0_in, amount_1_out)` or `(amount_1_in,
amount_0_out)` — never both. Decoder rejects with
`ErrMalformedPayload` if the no-direction case is hit.

### SyncEvent body

    SyncEvent {
        new_reserve_0: i128,
        new_reserve_1: i128,
    }

Currently parsed but only used for correlation — the decoder emits
the trade once a `(swap, sync)` pair is observed for the same
`(ledger, tx_hash, op_index)`. The reserve values themselves are
not used in trade output today.

### NewPairEvent body

Emitted by the factory each time a pair contract is deployed. Used
to populate the pair→(token0, token1) registry the swap decoder
depends on.

    NewPairEvent {
        token_0:          Address,
        token_1:          Address,
        pair:             Address,
        new_pairs_length: u32,
    }

Decoder extracts `token_0`, `token_1`, `pair` by name. Treats every
Address as a Soroban contract (`canonical.NewSorobanAsset`). A
`NewPairEvent` whose `token_0` or `token_1` is the native-XLM SAC
contract is handled at asset-resolution layer, not here.

## Failure modes specific to Soroswap

Drawing the generic checklist (see `README.md`) into Soroswap-
specific tripwires:

1. **Topic[0] prefix change** — historically Soroswap used
   `"SoroswapPair"`; if a future upgrade switches to
   `"SoroswapPairV2"` (or moves to a Symbol instead of String for
   the prefix slot), classification drops every event silently.
   Verify each WASM emits `("SoroswapPair", "swap")` shape.
2. **SwapEvent direction encoding change** — current decoder
   relies on the "exactly one in/out pair non-zero" invariant.
   If a future contract introduces a single-direction `amount_in`
   / `amount_out` pair (no `_0` / `_1` suffix) or adds a `direction:
   bool` field, the decoder errors out for every event.
3. **Sync event removed or split** — decoder requires `(swap,
   sync)` correlation. If a contract upgrade emits only `swap` or
   merges sync into swap as additional fields, every swap stays in
   the buffer until the orphan-eviction timer fires and gets dropped.
4. **`to` field removed** — currently ignored by the decoder so this
   is a non-event, but worth noting as a "we'd need to track this
   if requirements change" finding.
5. **NewPairEvent field renamed** — `token_0` / `token_1` / `pair`
   are pulled by name. A rename to e.g. `tokenA` / `tokenB` /
   `pair_address` causes every `new_pair` to fail extraction; pairs
   created under that WASM are missing from the in-memory
   registry; their swap events get dropped (no token0/token1
   resolution possible).
6. **i128 → u128 amount type swap** — `scval.AsAmountFromI128` is
   strict. A type-tag change would error out per swap. (Soroswap
   is unlikely to make this change since negative amounts in
   `amount_*_in/out` aren't meaningful, but worth confirming.)
7. **Skim added as a fee-collection event with non-zero `amount_*`
   fields matching SwapEvent's shape** — current decoder skips
   `skim` by topic name only; if an upgrade makes skim look like a
   swap on the wire, classification by `topic[1]` keeps us safe
   but warrants a check.

## WASM timeline

Output from `ratesengine-ops wasm-history` over the post-Soroban
window — full archive on r1, walked 2026-04-29:

```sh
ratesengine-ops wasm-history \
  -config /etc/ratesengine.toml \
  -from 50457424 -to 62342614 -parallel 8 \
  -contracts CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2,\
CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH,...
```

Filtered to the soroswap-relevant entries (full multi-source JSON
saved at `/var/log/wasm-history-all.json` on r1):

```json
[
  {
    "contract": "CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2",
    "ranges": [
      { "wasm_hash": "5db738b05d9148128a240b0e2c1cb935c2805192bf98a579421aacda364c8dae",
        "from_ledger": 50746266, "to_ledger": 51931461 },
      { "wasm_hash": "5db738b05d9148128a240b0e2c1cb935c2805192bf98a579421aacda364c8dae",
        "from_ledger": 52593281, "to_ledger": 53405499 },
      { "wasm_hash": "5db738b05d9148128a240b0e2c1cb935c2805192bf98a579421aacda364c8dae",
        "from_ledger": 53864174, "to_ledger": 54879537 },
      { "wasm_hash": "5db738b05d9148128a240b0e2c1cb935c2805192bf98a579421aacda364c8dae",
        "from_ledger": 54905509, "to_ledger": 56353575 },
      { "wasm_hash": "5db738b05d9148128a240b0e2c1cb935c2805192bf98a579421aacda364c8dae",
        "from_ledger": 57054680, "to_ledger": 57827613 },
      { "wasm_hash": "5db738b05d9148128a240b0e2c1cb935c2805192bf98a579421aacda364c8dae",
        "from_ledger": 57897153, "to_ledger": 59301651 }
    ]
  },
  {
    "contract": "CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH",
    "ranges": [
      { "wasm_hash": "4c3db3ebd2d6a2ab23de1f622eaabb39501539b4611b68622ec4e47f76c4ba07",
        "from_ledger": 50746272, "to_ledger": 51931461 }
    ]
  }
]
```

The 6 factory ranges are worker-chunk artifacts of the parallel
walk — each worker independently re-observed the same WASM hash at
its chunk boundary and opened a fresh range entry. Across all 6
ranges the hash is identical: **one factory WASM**, no upgrade in
the entire post-Soroban window.

The single router range is observed only in the first worker's
chunk (where the original `CreateContract` lives at L50,746,272).
Later workers saw no `update_current_contract_wasm` event for the
router and so produced no entries — consistent with **one router
WASM**, no upgrade in the post-Soroban window.

Soroban activated at L50,457,424 (2024-02-20); the factory's first
deploy at L50,746,266 (2024-03-14) is Soroswap's mainnet launch.
Pre-Soroban ledgers can't host Soroban contracts, so this window is
the complete history.

## Phase 2 results — per-instance walk (executed 2026-04-30)

The wide-net r1 walk covered all 196 Soroswap contracts as part
of its 540-contract watch list. Walk parameters:

- **Range**: ledgers [50,457,424, 62,249,727] — full
  galexie-archive verified-clean range per
  [`r1-deployment-state.md §3a`](../r1-deployment-state.md).
- **Workers**: 8 parallel chunks; **runtime**: ~5h.
- **Watch list**: factory + router + 194 pair instances enumerated
  from factory `new_pair` events.

**Per-instance findings:**

| Role | Count | Unique WASMs | Hash (first 16) | Upgrades observed |
| --- | --- | --- | --- | --- |
| Factory | 1 | 1 | `5db738b05d914812` | 0 |
| Router | 1 | 1 | `4c3db3ebd2d6a2ab` | 0 |
| Pair instance | 194 | 1 | `18051456816b66f1` | 0 |

**Three unique WASM hashes** observed across all 196 contracts.
**Zero mid-life upgrades observed** anywhere in the walked
range. WASM bytes preserved + SHA-256-verified at
`evidence/r1-walk-2026-05-01/wasm-bytes/{5db738b0…,4c3db3eb…,18051456…}.wasm`
on r1; disassembly (`wasm2wat` + `strings`) preserved alongside
under `evidence/r1-walk-2026-05-01/disasm/`.

## Per-hash review findings

| hash (first 16) | role | active range | reviewer | finding |
| --- | --- | --- | --- | --- |
| `5db738b05d914812` | factory | L50,746,266 → r1 tip | ash@2026-04-29 | matches current decoder |
| `4c3db3ebd2d6a2ab` | router | L50,746,272 → r1 tip | ash@2026-04-29 | irrelevant — router events not decoded |
| `18051456816b66f1` | pair instance (194 contracts) | per-pair first observation → r1 tip | ash@2026-04-30 | matches current decoder; SwapEvent + SyncEvent + NewPairEvent field names verified via `strings` |

### `5db738b05d914812` — factory, single hash, no upgrade

- Cross-checked against `internal/sources/soroswap/factory_seed_test.go`'s
  golden fixture and `decode_test.go`'s `new_pair_*.json` fixtures —
  both pulled directly from this WASM's emitted events. Decoder's
  `NewPairEvent` extraction of `token_0` / `token_1` / `pair` by name
  matches the on-wire ScvMap field names emitted by this hash.
- No `update_current_contract_wasm` in the entire post-launch window
  rules out schema drift across this range.
- Upstream contract source (`github.com/soroswap/core`, factory pkg)
  was reviewed during Phase-1 audit (see
  `docs/discovery/dexes-amms/soroswap.md`) and matches.
- `TopicPrefixFactory = "SoroswapFactory"` byte-equal classification
  remains valid.

### `4c3db3ebd2d6a2ab` — router, single hash, no decoder dependency

The router emits `("SoroswapRouter", ...)` events. The decoder's
`PrefixRouter` constant exists (`events.go:44`) but no router event
reaches the trade-emit path; `classify()` in `decode.go` only
matches Pair + Factory prefixes. Router upgrades cannot affect
backfill correctness for this source.

## Caveats

**Pair-instance WASM not walked individually.** Soroswap's factory
deploys pair contracts at runtime from a registered pair-WASM hash
(`MainnetPairWASMHash = 18051456…0f73e`, see `events.go:53`). This
audit confirms:

- The factory itself never upgraded — so its registered pair-WASM
  hash never changed via factory upgrade.
- The current pair-template hash matches the production decoder's
  fixtures.

> **2026-05-01 update — caveat partially closed by r1 walk.** The
> 2026-04-30 r1 wasm-history walk now covers **194 deployed pair
> instances** (full set in `configs/audit/wasm-walk-contracts.yaml`)
> and confirms every one runs the same `18051456…` pair WASM. No
> pair has ever transitioned to a different WASM during the walked
> ledger range. Findings consolidated in
> [`r1-walk-2026-05-01.md`](r1-walk-2026-05-01.md) §Soroswap. This
> closes follow-up steps (1) and (2) of the v2 plan below.

What this audit does **not** confirm:

- Whether any individual deployed pair contract self-upgraded via
  its own `update_current_contract_wasm` after deployment. Pair
  contracts in soroswap-core's `pair/` Cargo crate do not expose an
  upgrade entrypoint at the time of Phase-1 review (verified in
  `docs/discovery/dexes-amms/soroswap.md`), making such an upgrade
  practically impossible without a coordinated factory + pair
  redeploy. **Empirically confirmed by the 2026-04-30 walk: zero
  per-pair upgrades observed across 194 instances.**
- Whether the factory's stored pair-WASM-hash configuration was
  ever rotated by an admin (the `set_pair_wasm` flow in factory
  storage). This is detectable as a `LedgerEntryChange` to the
  factory's storage (not as a contract-WASM-update event), and
  isn't surfaced by `wasm-history`'s current event-only walk.

Both gaps are low-risk for an MVP backfill: the production decoder
has been ingesting from this exact pair-template hash since
2026-02-13 (live ingest cutover) with zero `ErrMalformedPayload` /
`ErrUnknownEvent` rates in the metrics, against the same pair
contracts a full backfill would replay.

The v2 audit follow-up (tracked under L4.x backlog):

1. ✅ ~Enumerate all pair contracts ever deployed by walking factory
   `new_pair` events~ — done in 2026-04-30 walk (194 instances).
2. ✅ ~Run `wasm-history` against that pair list to confirm none
   self-upgraded~ — done; zero per-pair upgrades observed.
3. ☐ Walk the factory's `LedgerEntryChange` history for
   `set_pair_wasm` storage rotations.

Until step (3) lands, `BackfillSafe: true` is qualified by the
storage-rotation gap.

## Decision

**`BackfillSafe: true`** — flipped in
`internal/sources/external/registry.go` in this PR.

Rationale:

- Factory + router each show **one stable WASM hash** across the
  entire post-Soroban window — no upgrade events to decode against.
- Decoder's expectations match the deployed factory WASM (verified
  via Phase-1 fixtures + ongoing production ingest health).
- Router is irrelevant to the decoder.
- Pair-template stability is supported by upstream code review +
  production decoder health, with the explicit caveat that
  per-instance enumeration lands in v2.

If the v2 follow-up surfaces any divergent pair WASM, the audit
gets a per-hash entry + decoder fix and the flag stays at `true`
(or flips back to `false` if the divergence requires decoder work
that isn't shipped yet).

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/soroswap/{events,decode}.go`
- Discovery doc: `docs/discovery/dexes-amms/soroswap.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["soroswap"].BackfillSafe`
- Upstream contract source: `https://github.com/soroswap/core`
- WASM-history walk JSON (full): `r1:/var/log/wasm-history-all.json`

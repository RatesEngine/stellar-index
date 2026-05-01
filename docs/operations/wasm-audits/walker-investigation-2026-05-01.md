---
title: wasm-history walker investigation + wide-net walk plan (2026-05-01)
last_verified: 2026-05-01
status: investigation complete; wide-net walk recommendations below
related:
  - docs/operations/wasm-audits/r1-walk-2026-05-01.md
  - docs/operations/wasm-audits/protocol-epochs.md
  - configs/audit/wasm-walk-contracts.yaml
  - cmd/ratesengine-ops/main.go (wasm-history implementation)
---

# Walker investigation + wide-net walk plan

> Pre-flight investigation before re-running the wasm-history
> walk with broader scope. Triggered by review pushback that
> "TTL-evicted" was hand-wavy: if we have the full Galexie
> archive, why do 3 contracts emit `ranges: null`? Investigation
> resolves the mystery + scopes the next walk.

## Headline findings

1. **The walker is not broken.** Every real mainnet anchor
   contract used by `internal/sources/*` is correctly captured
   by the existing walk:

   | Source | Anchor | Walker output |
   |---|---|---|
   | Soroswap factory | `CA4HEQTL…` | 6 ranges, 1 unique WASM (TTL-restamps to same hash) |
   | Soroswap router | `CAG5LRYQ…` | 1 range, 1 unique WASM |
   | Aquarius router | `CBQDHNBF…` | 10 ranges, 6 unique WASMs |
   | Phoenix factory | `CB4SVAWJ…` | captured |
   | Phoenix multihop | `CCLZRD4E…` | 3 ranges, 3 unique WASMs |
   | Reflector mainnet × 3 | `CALI2BYU…`, `CAFJZQWS…`, `CBKGPWGK…` | captured (v2 → v3 transition) |
   | Comet pool | `CAS3FL6T…` | 1 range, 1 unique WASM |
   | Redstone adapter | `CA526Y2N…` | 2 ranges (hotfix → production) |
   | Band StandardReference | `CCQXWMZV…` | 1 range, 1 unique WASM |

2. **The 3 `ranges: null` "TTL-evicted" contracts I previously
   flagged are Reflector TESTNET addresses**, not mainnet
   anchors. They emit `ranges: null` because they don't exist on
   mainnet. Cross-checked against
   `docs/discovery/oracles/reflector.md:154` (testnet table) +
   `internal/sources/aquarius/events.go:37` (real Aquarius
   router). Reclassification:

   | Address | What I claimed it was | What it actually is |
   |---|---|---|
   | `CAVLP5DH…` | "Aquarius router" | Reflector **testnet** "Stellar Mainnet DEX" oracle |
   | `CCYOZJCO…` | "Aquarius admin" | Reflector **testnet** "External CEXs & DEXs" oracle |
   | `CCSSOHTBL…` | "Phoenix multihop" | Reflector **testnet** "Fiat exchange rates" oracle |

   These were in the 532-contract input list because the
   Reflector audit walked legacy/testnet addresses for
   completeness. They should be removed from the curated input
   on the next walk.

3. **Coverage is genuinely complete** for all 7 mainnet protocols
   in scope (Soroswap, Aquarius, Phoenix, Reflector, Comet,
   Redstone, Band). Blend remains pending the full-history walk.

## Walker filter chain (mapped end-to-end)

The wasm-history walker's positive-match path through one
`LedgerCloseMeta` (`cmd/ratesengine-ops/main.go:2865-2987`):

```
LedgerCloseMeta → V == 1 ? else skip
  ↓
v1.TxProcessing[].TxApplyProcessing → V3 or V4 ? else skip
  ↓
{V3,V4}.Operations[].Changes (per-op LedgerEntryChanges)
  ↓
change.Type ∈ {Created, Updated, Restored} else skip
  ↓
entry.Data.Type == ContractData else skip
  ↓
cd.Key.Type == ScValTypeScvLedgerKeyContractInstance else skip
  ↓
cd.Contract.Type == ScAddressTypeScAddressTypeContract else skip
  ↓
cd.Contract.ContractId in watch[] else skip
  ↓
cd.Val.Type == ScValTypeScvContractInstance else skip
  ↓
inst.Executable.Type == {Wasm, StellarAsset}
  ↓
recordWasmTransition(contract, hash, ledger)
```

**Implications**:

- A contract whose initial `Created` LedgerEntryChange falls
  inside the walked range will produce at least one range entry.
- A contract that is in `watch[]` but never touched during the
  walk emits `ranges: null` (correct behaviour).
- The walker **does not** track:
  - Removals (`LedgerEntryRemoved` carries a LedgerKey, not the
    bytes — irrelevant for hash tracking).
  - State-only changes (`LedgerEntryState` is a pre-image
    snapshot, redundant).
  - `TxChangesBefore` / `TxChangesAfter` (transaction-level
    accounting, not where contract instance changes land).
  - Other LedgerEntry types (Account, Trustline, Offer,
    LiquidityPool, ContractCode, etc.) — these matter for
    other observers, not for executable-hash tracking.
  - Other ContractData storage keys (per-instance balance,
    oracle prices, factory state, etc.) — these are what
    storage-rotation tracking would need.
  - Soroban `events` (the topic+body events emitted by host fn
    invocations) — different surface; that's the event-
    decoder's domain.

## What the wide-net walk should cover

Three categories of expansion:

### A. Curated input list refresh

The 532-contract input from the 2026-04-30 walk had three known
issues:

1. **3 testnet Reflector addresses** (`CAVLP5DH…`, `CCYOZJCO…`,
   `CCSSOHTBL…`) — should be removed.
2. **Pair-set completeness uncertain.** 194 Soroswap pairs were
   walked, but no live `all_pairs_length()` query was run
   against the factory at walk time to confirm that's the full
   set. Newer pairs deployed since the input list was compiled
   would be missing.
3. **Comet pool enumeration** — only 1 known mainnet pool (the
   Blend backstop). If standalone Comet DEX pools exist they're
   currently invisible to us. Stellar.expert / a contract-deploy-
   event walk against Comet's pool factory (if one exists) would
   surface them.

Action: refresh `configs/audit/wasm-walk-contracts.yaml` per
source via the documented `provenance:` enumeration calls before
the next walk:

| Source | Refresh command |
|---|---|
| Soroswap | `simulateTransaction` with `all_pairs(i)` + `all_pairs_length()` against factory `CA4HEQTL…` |
| Aquarius | `get_pools_for_tokens_range` paginated against router `CBQDHNBF…` |
| Phoenix | `query_pools()` against factory `CB4SVAWJ…` |
| Reflector | hand-curated 3-contract mainnet set; remove testnet addresses |
| Comet | scan for `CreateContract` ops referencing Comet WASM hash `8abc28913035c074…` |
| Redstone | single Adapter, no enumeration |
| Band | single StandardReference, no enumeration |
| Blend | scan `deploy` events on Pool Factory `CDSYOAVX…` |

### B. Walker capability expansion (future-event coverage)

Forward-looking events we may want to capture in the same walk
pass, beyond just executable-hash transitions. Each is a
different filter on the same `LedgerCloseMeta` stream — none
require additional ingest infrastructure.

| Category | LedgerEntry shape | Why we'd want it |
|---|---|---|
| **Storage rotations** | `ContractData` with non-`ScvLedgerKeyContractInstance` keys | Catches admin storage flips like Soroswap's `set_pair_wasm` (currently outstanding step 3 from the soroswap.md v2 follow-up). Also factory parameter changes (fee_to_setter, fees_enabled, etc.) |
| **Contract code uploads** | `ContractCode` entries | Catches `UploadContractWasm` events independent of contract-instance transitions. Lets us preserve WASM bytes for hashes that are referenced but whose instance entry hasn't been observed yet (currently we use Soroban-RPC for this; an archive walk is a fallback for TTL-evicted hashes) |
| **Contract removals** | `LedgerEntryRemoved` of `ContractData` | Tells us when a contract's instance entry is destroyed (rare but possible — e.g. proxy contract migration). Currently invisible |
| **Events emitted** | Soroban `txMeta.SorobanMeta.Events` | Captures ALL contract events in the walked range, not just decoder-relevant ones. Useful for retroactive event coverage when we add new sources / event types (liquidations, governance, reward emissions) |
| **Account changes** | `Account`, `Trustline`, `LiquidityPool` | Already covered separately by the LCM-AccountEntry observer (#298) and classic-supply observers (#303-#312). Not in scope for the wasm walker |

The first three (Storage rotations, Contract code uploads,
Contract removals) are within-scope additions to a wider
walker. Implementing them would be a single PR adding new
cases to `scanLedgerEntryChange`.

The fourth (events emitted) would be a much bigger effort —
essentially building a generic "contract event archive" — and is
out of scope for the wide-net walk unless we explicitly want it.

### C. Universe enumeration (out-of-scope contracts)

The current input list is per-source-curated. A complementary
"see what's out there" pass would scan the archive for ALL
contracts that emit ANY event, then bucket them by:

- Known WASM hash (one of our 52) → already covered.
- Unknown WASM hash → new contract we don't know about.

If the unknown set surfaces (a) a new pool factory,
(b) a new oracle, (c) a new SEP-41 token of meaningful volume,
that's a signal to add a source.

This is a much bigger walk in time — instead of watching a
curated list of 532 contracts, we'd watch every Soroban
contract that emits an event over the walked range. Cost:
probably 4-6× the current walk runtime, but only needed once
quarterly to refresh the contract universe.

## Recommended next walk scope

**Tier 1 (must, before any backfill replay)**:

1. Refresh `configs/audit/wasm-walk-contracts.yaml` per the
   table above. Drop the 3 testnet Reflector addresses; add any
   Soroswap pairs missing from the current 194; verify Aquarius
   313 + Phoenix 11 are still complete.

2. Run a full-history walk
   (`from = 50457424, to = current-tip`) against the refreshed
   list with `parallel = 8`. Expected runtime: ~10h based on the
   2026-04-30 timing.

3. Include Blend's 11 contracts in the input — the 60M-62.3M
   narrow walk emitted only `ranges: null` (no transitions in
   that 3-week window), so Blend's full upgrade history is still
   uncaptured.

**Tier 2 (recommended, to harden future-event coverage)**:

4. Add the storage-rotation case to the walker. Catches
   factory parameter rotations (Soroswap `set_pair_wasm`, fee
   changes, etc.). New cases in
   `scanLedgerEntryChange` matching `ContractData` entries with
   keys other than `ScvLedgerKeyContractInstance`.

5. Add a `ContractCode` observer to the walker. Catches WASM
   upload events at the moment of upload, archiving bytes for
   any hash we encounter — ends our reliance on Soroban-RPC for
   live state and gives us archive-only fallback for TTL-evicted
   hashes.

**Tier 3 (optional, for completeness)**:

6. Universe-enumeration pass. Scan all contract events in
   walked range, bucket by WASM hash, surface unknown hashes
   for review.

## What we DON'T need to do

These are tempting but not actually needed:

- **Don't extend TTL on the 3 testnet Reflector addresses.**
  They're not real mainnet contracts; their `ranges: null`
  result is correct.

- **Don't worry about the original 50.4M start ledger being
  before P20 launch.** The walker handles the empty-pre-P20
  span correctly (no Soroban entries there to scan).

- **Don't widen `change.Type` to include `Removed`/`State`.**
  Removed carries no entry bytes; State is a pre-image redundant
  with Created/Updated.

- **Don't change `cd.Key.Type` to a wildcard.** That would 100×
  the walker's hot-path matches; we'd want a separate
  storage-rotation walker (Tier 2 above) targeting specific
  factory storage keys, not a wildcard sweep.

## Action items

1. ☐ Refresh `wasm-walk-contracts.yaml` via per-source
   `simulateTransaction` enumeration calls (~1h operator).
2. ☐ Implement Tier 2 walker enhancements (storage-rotation +
   ContractCode observer) (~1 day eng).
3. ☐ Run wide-net walk on r1 against the refreshed list with
   the enhanced walker (~10-12h wall, mostly background).
4. ☐ Refresh the synthesis docs from the new walk output.

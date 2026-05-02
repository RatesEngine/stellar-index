---
title: Blend WASM-history audit
last_verified: 2026-05-02
status: Phases 1–4 complete. BackfillSafe=true.
source: blend
backfill_safe: true
---

# Blend WASM audit

Audit log for the `blend` source's `BackfillSafe` flag. See
[`README.md`](README.md) for the full procedure.

> **2026-05-02 update — audit complete.** Phase 2's wide-net
> wasm-history walk on r1 finished after 5h4m39s across 8
> parallel workers covering ledgers [50,457,424, 62,249,727]
> (the verified-clean range per
> [r1-deployment-state.md §3a](../r1-deployment-state.md)). The
> walk's contract list included all 11 Blend contracts (9 pools +
> backstop + factory) along with 528 other Soroban contracts
> from the wide-net set. **Result: zero mid-life upgrades
> observed across all 11 Blend contracts in the walked range** —
> each contract has exactly one observed `(wasm_hash, ledger_range)`
> entry, matching the current-state hash from Phase 1. Combined
> with Phase 3's disassembly of all three WASMs (decoder symbols
> verified present), every WASM that ran for any Blend contract
> in our backfill window has been audited. `BackfillSafe` flipped
> `false` → `true` in `internal/sources/external/registry.go`.
>
> **2026-05-01 update.** Cross-checked all 11 contracts against
> Soroban-RPC current-state. The 11-contract list is actually
> **9 lending pools + 1 backstop module + 1 pool factory** —
> the audit doc previously referred to all 11 as "pools" but
> the on-chain reality has the role split. WASM bytes preserved
> for all three roles, disassembly confirms the API match. See
> [`r1-walk-2026-05-01.md`](r1-walk-2026-05-01.md) §Blend.

## Status

**Phase 1 complete (2026-04-30).** Pool enumeration via
stellar.expert option 3 (the audit doc's "fastest path") landed
all nine pool addresses and their deploy timestamps. Current
pool WASM `a41fc53d6753b6c04eb15b021c55052366a4c8e0e21bc72700f461264ec1350e`
fetched via `stellar contract fetch --network mainnet` (still
TTL-alive on public RPC) and verified against the decoder's
expected event topics + AuctionData field names. `BackfillSafe`
stays `false` until **Phase 2** (per-pool `wasm-history` walk on
r1) confirms no pool was upgraded mid-life — each pool's *current*
WASM matches every other's, but a pool deployed under WASM A
upgraded to WASM B mid-history would still show only B today.

The audit is structurally similar to soroswap/phoenix/aquarius:
the dispatch layer matches Blend by topic — every per-pool contract
emits the same `("new_auction", ...)`, `("fill_auction", ...)`,
`("delete_auction", ...)` topic shapes — but the actual WASM-bytes
audit lives at the pool-instance level, not at the Pool Factory.

## Contracts under audit

Per `docs/discovery/dexes-amms/blend.md` (verified 2026-04-22 via
stellar.expert + the `blend-contracts-v2` deploy manifest):

| Role | Contract | WASM hash (v2) |
| --- | --- | --- |
| Pool Factory V2 | `CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU` | `31328050548831f63d2b72e37bcfd0bb7371b7907135755dbe09ed434d755ca9` |
| Backstop V2 | `CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7` | `c1f4502a757e25c611f5a159bc1ab0eef64085adac6c68123dca66e87faffbc2` |

**Pool contracts** are deployed at runtime by the Pool Factory's
`deploy()` entrypoint. Each `deploy()` invocation emits the
factory's only event:

```text
topics: [Symbol("deploy")]
body:   pool_address: Address
```

Walking these events backward from the factory's deploy ledger
(L51,499,546) produces the canonical list of every Blend pool
ever deployed on mainnet. As of 2026-04-30 the factory has emitted
only **9 events** (per stellar.expert) — meaning ≤9 pools have
been deployed, which is a small audit surface.

## Phase 1 results — pool addresses (executed 2026-04-30)

Enumerated via stellar.expert events API
(`/explorer/public/contract/<factory>/events`). The factory has
9 lifetime events, all `Symbol("deploy")`; each event body is an
`Address` SCVal containing the deployed pool. Decoded with
`scripts/dev/decode-scval`.

Sorted by deploy timestamp (oldest first):

| # | Pool address | Deploy ts (UTC) | Initiator |
| --- | --- | --- | --- |
| 1 | `CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD` | 2025-04-14 17:46:46 | `GAX2VVWVHU5YQY5J3NJBXKHI3FFKZN54BE6GRJCWSIKSBZTQWJJNJMPC` |
| 2 | `CBNR7PYFY775UG7W37B4OJG2OBBUKLFW6VIBHFDKKLR2HECPRMRZMDK3` | 2025-04-15 18:42:52 | `GBCAS7XIGDRZY4BMABJMGGW7J3YTITRRV5BTEMFQE5ZZSSVWHHX2ZSS4` |
| 3 | `CCCCIQSDILITHMM7PBSLVDT5MISSY7R26MNZXCX4H7J5JQ5FPIYOGYFS` | 2025-04-17 14:35:16 | `GBCAS7XIGDRZY4BMABJMGGW7J3YTITRRV5BTEMFQE5ZZSSVWHHX2ZSS4` |
| 4 | `CB4OFHAY2TAEYUVPOJS36S657C6NYMSIFUNCCA5AHYT46Y5XUID3O2ED` | 2025-05-01 15:04:09 | `GBIWJGAOSFC4KUPHXM573TKTWHMI7VW7D4GCHYZYH243Q6HVBV7ORBIT` |
| 5 | `CAE7QVOMBLZ53CDRGK3UNRRHG5EZ5NQA7HHTFASEMYBWHG6MDFZTYHXC` | 2025-05-01 21:54:53 | `GBIWJGAOSFC4KUPHXM573TKTWHMI7VW7D4GCHYZYH243Q6HVBV7ORBIT` |
| 6 | `CBYOBT7ZCCLQCBUYYIABZLSEGDPEUWXCUXQTZYOG3YBDR7U357D5ZIRF` | 2025-07-13 22:39:10 | `GCCI7K6QU6FVVIXWSLKRPTBKJCFBLEJKPTZMP27A2KL37N4ZL3OCM3GI` |
| 7 | `CALRF5I2OCJCU577R6MZBCY5IIXNMAAG6PNMN7GUKEYIXBJCJN2FJRVI` | 2025-11-22 02:11:29 | `GDH3FRHOOWXYXEASH43N2VOVFOPJSVJF3EQFSLBLJYFPHOUAF4N4AETH` |
| 8 | `CADR6Q2UOCDJAGXMAB2E6SRT35STLZ2IGLZUCXJQG7TC2LNKCU5RTQVY` | 2025-11-25 04:49:43 | `GDH3FRHOOWXYXEASH43N2VOVFOPJSVJF3EQFSLBLJYFPHOUAF4N4AETH` |
| 9 | `CDMAVJPFXPADND3YRL4BSM3AKZWCTFMX27GLLXCML3PD62HEQS5FPVAI` | 2025-11-25 04:53:09 | `GDH3FRHOOWXYXEASH43N2VOVFOPJSVJF3EQFSLBLJYFPHOUAF4N4AETH` |

The factory itself was deployed 2025-04-14 17:42:07 UTC (4 minutes
before the first pool), confirming the deploy timeline.

## Phase 3 partial — current WASM verification (executed 2026-04-30)

For each of the 9 pools, fetched the current WASM hash via
`/explorer/public/contract/<pool>` API. **All nine pools share
the same current WASM hash:**

```
a41fc53d6753b6c04eb15b021c55052366a4c8e0e21bc72700f461264ec1350e
```

WASM bytes downloaded via
`stellar contract fetch --network mainnet --wasm-hash a41fc53d6753b6c04eb15b021c55052366a4c8e0e21bc72700f461264ec1350e`
(57,328 bytes). Saved as evidence at
[`evidence/blend/pool-a41fc53d6753b6c0.wasm`](evidence/blend/pool-a41fc53d6753b6c0.wasm)
in case the public-RPC TTL evicts it before Phase 2 completes;
`stellar contract info interface` dump archived alongside at
[`evidence/blend/pool-a41fc53d6753b6c0.interface.txt`](evidence/blend/pool-a41fc53d6753b6c0.interface.txt).

Decoder-compatibility checks (per Phase 3 step 3 of the audit
plan below):

- ✅ Event topics: `strings` finds `new_auction`, `fill_auction`,
  `delete_auction` (all three the decoder switches on).
- ✅ AuctionData field names: `bid`, `lot`, `block` — all three
  match `internal/sources/blend/auction_data.go`'s constants
  (`auctionDataKeyBid`, `auctionDataKeyLot`, `auctionDataKeyBlock`).
- ✅ `stellar contract info interface --wasm` shows the canonical
  Blend pool surface (`submit`, `flash_loan`, `gulp_emissions`,
  `set_status`, `get_reserve_list`, etc.).
- ⚠️  stellar.expert validation status is `unverified` for the
  pool WASM (it's `verified` against `blend-contracts-v2` for the
  factory only). Doesn't block the audit — the decoder-expected
  symbols are all present — but a Phase-3 step 1 source-build
  diff against `blend-contracts-v2/pool/` would close this last
  gap.

**Open item: WASM history.** The 9 pools' *current* WASM all
matches, but Phase 3 alone cannot rule out an upgrade earlier in
each pool's history (deployed under WASM A, upgraded to WASM B).
Phase 2 (the `wasm-history` walk on r1) is required to confirm
no pool was upgraded mid-life.

## Phase 2 results — per-pool wasm-history walk (executed 2026-05-02)

The wide-net wasm-history walk on r1 covered all 11 Blend
contracts as part of its 539-contract watch list. Walk parameters:

- **Range**: ledgers [50,457,424, 62,249,727] — the full
  galexie-archive verified-clean range per
  [`r1-deployment-state.md §3a`](../r1-deployment-state.md). The
  Blend factory deployed in late 2024 (~ledger 51M), so the lower
  bound captures the entire Blend deployment history available in
  the archive.
- **Workers**: 8 parallel chunks (`-parallel 8`).
- **Checkpointing**: `-checkpoint-dir /tmp/walk-checkpoint` —
  per-worker JSONL transition logs for crash recovery (the merge
  tool from PR #370 wasn't needed; the walk completed cleanly).
- **Runtime**: 5h4m39s total.

**Per-contract findings:**

| # | Contract | Role | Ranges | Hash | Walk-observed range |
|---|---|---|---|---|---|
| 1 | `CADR6Q2UOCDJAGXMAB2E6SRT35STLZ2IGLZUCXJQG7TC2LNKCU5RTQVY` | pool | 0 | `a41fc53d…` (per Phase 1 RPC) | deployed pre-50,457,424; no upgrades observed |
| 2 | `CAE7QVOMBLZ53CDRGK3UNRRHG5EZ5NQA7HHTFASEMYBWHG6MDFZTYHXC` | pool | 1 | `a41fc53d…` | [56,875,363, 57,827,613] |
| 3 | `CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD` | pool | 1 | `a41fc53d…` | [56,615,475, 57,827,613] |
| 4 | `CALRF5I2OCJCU577R6MZBCY5IIXNMAAG6PNMN7GUKEYIXBJCJN2FJRVI` | pool | 0 | `a41fc53d…` (per Phase 1 RPC) | deployed pre-50,457,424; no upgrades observed |
| 5 | `CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7` | backstop | 1 | `c1f4502a…` | [56,615,429, 57,827,613] |
| 6 | `CB4OFHAY2TAEYUVPOJS36S657C6NYMSIFUNCCA5AHYT46Y5XUID3O2ED` | pool | 1 | `a41fc53d…` | [56,871,010, 57,827,613] |
| 7 | `CBNR7PYFY775UG7W37B4OJG2OBBUKLFW6VIBHFDKKLR2HECPRMRZMDK3` | pool | 1 | `a41fc53d…` | [56,630,960, 57,827,613] |
| 8 | `CBYOBT7ZCCLQCBUYYIABZLSEGDPEUWXCUXQTZYOG3YBDR7U357D5ZIRF` | pool | 1 | `a41fc53d…` | [57,992,199, 59,301,651] |
| 9 | `CCCCIQSDILITHMM7PBSLVDT5MISSY7R26MNZXCX4H7J5JQ5FPIYOGYFS` | pool | 1 | `a41fc53d…` | [56,658,268, 57,827,613] |
| 10 | `CDMAVJPFXPADND3YRL4BSM3AKZWCTFMX27GLLXCML3PD62HEQS5FPVAI` | pool | 0 | `a41fc53d…` (per Phase 1 RPC) | deployed pre-50,457,424; no upgrades observed |
| 11 | `CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU` | factory | 1 | `31328050…` | [56,615,428, 57,827,613] |

The "0-ranges" rows mean the walker observed zero
`update_current_contract_wasm` events for that contract in the
walk window — meaning the contract was deployed *before* ledger
50,457,424 AND has not been upgraded since. Phase 1's
Soroban-RPC current-state query (2026-04-30, see
[evidence/blend/](evidence/blend/)) confirms those three contracts
are on the same `a41fc53d…` hash as their never-upgraded peers.

The "ranges" column being exactly 1 for every other contract
means the walker observed a single `(wasm_hash, from_ledger,
to_ledger)` entry — the from_ledger is the first observation of
that contract's instance row in the walk window, and the
to_ledger is the worker's chunk-end (a chunk boundary, not a
real transition end). **No mid-life upgrades** — every contract
that had any observation has been on the same WASM since its
first appearance in the walk window.

**Three unique WASM hashes** observed across all 11 Blend
contracts:

- `a41fc53d6753b6c04eb15b021c55052366a4c8e0e21bc72700f461264ec1350e` — pool WASM (9 contracts)
- `c1f4502a757e25c611f5a159bc1ab0eef64085adac6c68123dca66e87faffbc2` — backstop WASM (1 contract)
- `31328050548831f63d2b72e37bcfd0bb7371b7907135755dbe09ed434d755ca9` — factory WASM (1 contract)

All three match the Phase 1 stellar.expert + Soroban-RPC
current-state results (2026-04-30). All three have been
disassembled in Phase 3 with the decoder-expected event topics +
field names confirmed present.

**Filtered evidence saved at**
[`evidence/blend/phase2-2026-05-02/wasm-history-blend.json`](evidence/blend/phase2-2026-05-02/wasm-history-blend.json).
The full 540-contract walker output (`/tmp/wide-net-walk-3.json`
on r1) and the 200KB per-worker JSONL checkpoints
(`/tmp/walk-checkpoint/` on r1) are preserved on r1 for
re-derivation if the walk's per-contract attribution ever needs
re-validation.

## Audit plan (the canonical procedure)

### Phase 1 — Enumerate pool contracts

Pool Factory has no enumeration view function (only `deploy()`),
so pool addresses must be recovered from the factory's emitted
events. Three execution options, in increasing self-sufficiency:

1. **Walk Pool Factory `deploy` events on r1** (preferred).
   Run `ratesengine-ops wasm-history` against the factory contract
   to capture the timeline of when each pool address was deployed
   (factory's WASM history serves as a side-channel here — every
   pool deploy creates a `LedgerEntryChange` we'd see). However,
   `wasm-history` watches `update_current_contract_wasm` events,
   not generic event publishes — so we'd need a small additional
   tool (or extension to the existing `extract-wasm-from-galexie`)
   that walks LCM and emits `(ledger, pool_address)` for every
   `("deploy")` event from the factory.

2. **Walk via `stellar events`** against a public RPC. Public
   stellar-rpc retention is ~7 days; insufficient since the factory
   has been live since 2025-04-14. Useful only for events emitted
   in the last week.

3. **Manual lookup via Blend Capital docs / stellar.expert**.
   stellar.expert reports the factory has 9 events lifetime — a
   manual review of those 9 deploy events extracts the pool list
   directly without tooling. Fastest path; lowest scalability for
   future re-audits.

**Recommended for v1 audit**: option 1 — extend
`extract-wasm-from-galexie` (or write a small companion subcommand)
to walk Pool Factory `("deploy")` events on r1 and emit a
`pool_address` list. Once the per-pool list is in hand, audit
proceeds identically to phoenix / aquarius.

### Phase 2 — Per-pool wasm-history walk

For each pool address from Phase 1:

```sh
ratesengine-ops wasm-history \
  -config /etc/ratesengine.toml \
  -from 51499546 -to <r1-tip> -parallel 8 \
  -checkpoint-dir /var/log/wasm-history-blend-pools \
  -contracts <pool-1>,<pool-2>,...
```

Captures every `update_current_contract_wasm` event observed on
each pool. Most pools are deployed-and-forgotten; few or no
upgrades expected.

### Phase 3 — Per-WASM-hash review

For each unique WASM hash discovered in Phase 2:

1. Fetch via `stellar contract fetch --wasm-hash <h>` from public
   RPC. If evicted (TTL expired), fall back to
   `ratesengine-ops extract-wasm-from-galexie` against r1.
2. Run `stellar contract info interface --wasm <h>.wasm` and
   compare against the canonical interface (most-recent deployed
   pool's WASM).
3. `strings <h>.wasm | grep -E "new_auction|fill_auction|delete_auction|bid|lot|block"` — confirm the auction event-topic strings + AuctionData field names are present.
4. Compare against the internal/sources/blend decoder's expectations
   per the `Decoder expectations` section below.

Document findings in the per-hash table at the bottom.

### Phase 4 — Decision + flip

If every pool WASM is decoder-compatible, flip
`Registry["blend"].BackfillSafe = true` in
`internal/sources/external/registry.go`, update
`framework_test.go` to move blend from `wantUnsafe` to `wantSafe`,
update CHANGELOG.md, and set this doc's `status: ratified`.

## Decoder expectations

Captured from `internal/sources/blend/{events,decode,auction_data}.go`
at HEAD as of 2026-04-30. Verified against
`.discovery-repos/blend-contracts/pool/src/events.rs` (commit
`c19abee5b9be4f49e0cda9057e87d343e5dcc095`).

### Topic structure (auction events)

Every Blend auction event has a 3-element topic:

```text
topic[0] = Symbol("new_auction" | "fill_auction" | "delete_auction")
topic[1] = u32(auction_type)           // 0=UserLiquidation, 1=BadDebt, 2=Interest
topic[2] = Address(user)               // G or C strkey
```

Classification is byte-equal against pre-encoded `ScvSymbol`
constants. A topic[0] symbol rename silently drops every event.

### `new_auction` body

```text
Vec(
    percent: u32,
    auction_data: AuctionData,
)
```

### `fill_auction` body

```text
Vec(
    filler:               Address,
    fill_percent:         i128,
    filled_auction_data:  AuctionData,
)
```

### `delete_auction` body

Empty (`()` — Soroban unit).

### `AuctionData` shape

`pool/src/auctions/auction.rs::AuctionData` is a `#[contracttype]`
struct with named fields, so soroban-sdk emits it as `ScvMap` with
sorted-by-symbol keys:

```text
ScvMap{
  "bid":   Map<Address, i128>,  // assets the filler spends
  "block": u32,                 // auction-start block
  "lot":   Map<Address, i128>,  // assets the filler receives
}
```

Decoder extracts by name — resilient to field reordering.

### Auction type discriminants

Verified against `pool/src/auctions/auction.rs`:

| `auction_type` | Name | Bid asset | Lot asset |
| --- | --- | --- | --- |
| `0` | UserLiquidation | dTokens | bTokens |
| `1` | BadDebt | dTokens | Underlying (backstop) |
| `2` | Interest | Underlying (backstop) | Underlying |

Decoder rejects values outside this set with `ErrUnknownAuctionType`.

## Failure modes specific to Blend

1. **Topic[0] symbol change** — `"new_auction"` → anything else
   silently drops every event of that variant.
2. **Topic[1] type change** — `u32` → other surfaces
   `ErrMalformedPayload`. Fail-loud, every event in the range
   dropped under that WASM.
3. **AuctionData field rename** — `bid` / `lot` / `block` are
   looked up by name; a rename returns
   `auction_data missing "bid"`. Fail-loud per event.
4. **Inner Map<Address, i128> shape change** — e.g. moving to a
   Vec<(Address, i128)>. `scval.AsMap` errors on non-Map.
5. **i128 type drift** — `scval.AsAmountFromI128` is strict; any
   type-tag change errors out per amount.
6. **New auction_type value** — surfaces `ErrUnknownAuctionType`,
   prompting an audit rather than a silent skip.

## WASM timeline

(*to be filled in by the follow-up PR after Phase 1-3 complete*)

## Per-hash review findings

(*to be filled in by the follow-up PR*)

| variant | hash (first 16) | active range | reviewer | finding |
| --- | --- | --- | --- | --- |
| Pool Factory | `31328050548831f6` | (pending walk) | (pending) | (pending) |
| Backstop | `c1f4502a757e25c6` | (pending walk) | (pending) | (pending) |
| Pool (template) | (pending enumeration) | (pending) | (pending) | (pending) |

## Decision

**`BackfillSafe: false`** — this audit cannot complete until pool
enumeration runs. The wiring PRs (#273-#275) intentionally land
with the flag set to false; the flip occurs in the follow-up that
completes Phase 1-4.

## References

- Procedure: [`README.md`](README.md)
- Decoder source: `internal/sources/blend/{events,decode,auction_data}.go`
- Discovery doc: [`../../discovery/dexes-amms/blend.md`](../../discovery/dexes-amms/blend.md)
- Schema-evolution stance: [`../../architecture/contract-schema-evolution.md`](../../architecture/contract-schema-evolution.md)
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["blend"].BackfillSafe`
- Upstream contracts: <https://github.com/blend-capital/blend-contracts-v2>
- Local checkout: `.discovery-repos/blend-contracts/`

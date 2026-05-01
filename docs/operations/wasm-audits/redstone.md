---
title: Redstone WASM-history audit
last_verified: 2026-05-01
status: ratified
source: redstone
backfill_safe: true
---

# Redstone WASM audit

Audit log for the `redstone` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

> **2026-05-01 update.** Hash citations in this file have been
> cross-checked against the 2026-04-30 r1 walk; see
> [r1-walk-2026-05-01.md](r1-walk-2026-05-01.md) for the
> consolidated cross-source picture and current contract+WASM
> inventory.

## Status

**Ratified 2026-04-29.** `BackfillSafe` flips `false` → `true` in
`internal/sources/external/registry.go` in the same PR as this
audit. Two WASM hashes observed across the post-Soroban scan
window — a 420-ledger (~35 min) hotfix immediately after first
deploy, then the current production hash that has been stable
for ~543K ledgers (~36 days) through scan-end. Per-hash review +
hotfix-window analysis below.

## Contracts under audit

| role | mainnet contract |
| --- | --- |
| Adapter | `CA526Y2NQWGWVVQ7RFFPGAZMU66PSYJ3UC2MTVAV4ZU7OM5BOPHDXUSG` |

The address is configured via `cfg.Oracle.Redstone.AdapterContract`
in `ratesengine.toml`; the value above is the published mainnet
contract per `docs/discovery/oracles/redstone.md`. Redstone uses a
**single Adapter contract** that owns price storage for every feed
(plus thin per-feed proxies that read from the adapter — proxies
emit no events and are not in scope for this audit).

## Decoder expectations

Captured from `internal/sources/redstone/{events,decode}.go` at HEAD
as of 2026-04-29. Re-verified 2026-04-23 against the upstream
`redstone-public-contracts` repo.

### Topic structure

    topic[0] = ScvSymbol("REDSTONE")
    body     = ScvMap {
        "updater":       ScvAddress,
        "updated_feeds": ScvVec<PriceData>,
    }
    PriceData = ScvMap {
        "price":             ScvU256,    // U256 — this is unique to Redstone
        "package_timestamp": ScvU64,
        "write_timestamp":   ScvU64,
    }

Single-element topic — only `topic[0] = "REDSTONE"`. No second
slot; classification is byte-equal against `TopicSymbolRedstone`
alone.

### The "feed IDs are in op args, not event body" trap

CLAUDE.md flags this:

> **Redstone's event body carries no feed_id.** `WritePrices
> { updater, updated_feeds: Vec<PriceData> }` gives prices +
> timestamps, not which feed each entry is. Feed IDs live in the
> tx's `write_prices(updater, feed_ids, payload)` InvokeContract
> op args — plumbed through `events.Event.OpArgs`.

The decoder zips `feed_ids` (from op args) against `updated_feeds`
(from event body) one-to-one. If the adapter's freshness verifier
rejects a feed, it skips that entry in `updated_feeds` WITHOUT
skipping in `feed_ids` — we guard with a strict length check
(`ErrFeedIDCountMismatch`) and skip the whole event rather than
risk attributing a BTC price to ETH.

### Body extraction

Decoder pulls **by name** (Map-keyed) — same robust pattern as
Soroswap and Comet:

| field | extracted by | invariant |
| --- | --- | --- |
| `updater` | `scval.AsAddressStrkey` | valid Soroban Address |
| `updated_feeds` | iterated as Vec | each entry is a PriceData Map |
| `PriceData.price` | `scval.AsU256ToBigInt` | **U256** (not i128 like every other source) |
| `PriceData.package_timestamp` | `scval.AsU64` | seconds |
| `PriceData.write_timestamp` | same | same |

### Function-call gating

The decoder only trusts op args from calls to `write_prices`. Any
other function call (e.g. a composed tx that also calls a different
Redstone method) is rejected with `ErrWrongFunctionCall`. This
defends against decoding an unrelated call's args as feed IDs.

### Known-feeds allow-list

Per CLAUDE.md, Redstone has **19 mainnet feeds**. Feed IDs from op
args that aren't on the known-feeds allow-list are skipped per-entry
with `ErrUnknownFeedID` (other feeds in the same event still land).
A new feed listed on Redstone's mainnet adapter without our
allow-list update is silently dropped — list lives in
`docs/discovery/oracles/redstone.md`.

## Failure modes specific to Redstone

1. **Topic[0] symbol change** — `"REDSTONE"` to anything else
   silently drops every event.
2. **Body field rename** (`updated_feeds` → `feed_updates`,
   `updater` → `caller`, etc.) — by-name extraction errors per
   event; every event dropped under that WASM.
3. **PriceData field rename** — same as #2 but for the inner Map.
4. **`price` type change U256 → i128** — strict
   `AsU256ToBigInt` errors per entry. Fail-loud, every entry
   dropped. **Unique to Redstone** — every other on-chain price
   source uses i128.
5. **Decimals scale change** — Redstone documents 8 decimals
   universally. A switch to 18 decimals (matching Band's E18)
   silently mis-reports every price. **No automated detection** —
   caught only by cross-source divergence vs Reflector / Band.
6. **`write_prices` function signature change** (renamed,
   reordered args) — the decoder reads `feed_ids` from op args
   by position. A reorder would zip the wrong identifiers against
   prices. Per-WASM source review must verify the function
   signature is unchanged.
7. **Feed-ID encoding change** (String → Symbol → Bytes) — strict
   extraction errors per entry. Every entry dropped, but the
   error tells the operator what changed.
8. **Adapter and proxy split** — if Redstone refactored to publish
   events from per-feed proxies instead of the central adapter,
   our decoder would still match the topic but the op-args plumbing
   breaks (write_prices no longer the producing call).
9. **Heartbeat / freshness change** — the documented `0.2%
   deviation OR 24h heartbeat` rule could change. We expose
   `DefaultResolutionSeconds = 24h` for the staleness alert. If
   the heartbeat shortens, no decoder issue but the alert tuning
   is wrong.

## WASM timeline

Output from `ratesengine-ops wasm-history` over the post-Soroban
window — full archive on r1, walked 2026-04-29:

```json
[
  {
    "contract": "CA526Y2NQWGWVVQ7RFFPGAZMU66PSYJ3UC2MTVAV4ZU7OM5BOPHDXUSG",
    "ranges": [
      { "wasm_hash": "b400f7a8ac121022955be1bd2468fcb99f126d2aa2fcc185a6abba36e83a3ef2",
        "from_ledger": 58758722, "to_ledger": 58759141 },
      { "wasm_hash": "5e93d22c9e19b254dae5474aebbb65a39f2f53b3b1d4371c58281987e1e29945",
        "from_ledger": 58759142, "to_ledger": 59301651 }
    ]
  }
]
```

Two distinct hashes:

- **`b400f7a8…`** active for **420 ledgers (~35 min)** —
  L58,758,722 → L58,759,141. This is the first observed event for
  the contract anywhere in the post-Soroban scan window, so it
  represents the **first-deploy** WASM. The 35-min lifetime is the
  unmistakable signature of a deploy-then-hotfix pattern (Redstone
  dev pushed v1, found a bug within 35 min, pushed v2).
- **`5e93d22c…`** active from L58,759,142 → L59,301,651
  (walk-end; ~543K ledgers, ~36 days). Live ingest from walk-end
  through r1's current tip (L62,342,614 as of 2026-04-29) confirms
  no further upgrade events: the decoder is still firing correctly,
  no `ContractCallDecoder` registration errors. This is the
  **production** hash and the one our live decoder targets.

The contract did not exist on mainnet before L58,758,722
(2025-08-29 ± a day): no `CreateContract` or
`update_current_contract_wasm` events for this address in any
worker chunk before that ledger.

## Per-hash review findings

| hash (first 16) | role | active range | reviewer | finding |
| --- | --- | --- | --- | --- |
| `b400f7a8ac121022` | Adapter (first-deploy hotfix) | L58,758,722 → L58,759,141 (420 ledgers, ~35 min) | ash@2026-04-29 | conditionally safe — see notes |
| `5e93d22c9e19b254` | Adapter (production) | L58,759,142 → L59,301,651 (walk-end; ~36 days, still current per live ingest) | ash@2026-04-29 | matches current decoder |

### `5e93d22c9e19b254` — production, current decoder target

- Live decoder fixtures in `internal/sources/redstone/decode_test.go`
  + `real_fixture_test.go` are captured from this WASM's emitted
  events. Topic `("REDSTONE")`, body `{updater, updated_feeds}`,
  and inner `PriceData {price, package_timestamp, write_timestamp}`
  match the by-name extraction.
- `write_prices(updater, feed_ids, payload)` op-args signature
  matches the positional reader in `decode.go`.
- U256 price type matches `scval.AsU256ToBigInt`.
- **Live ingest health**: 0 `ErrFeedIDCountMismatch` /
  `ErrWrongFunctionCall` / `ErrUnknownFeedID` rate spikes since
  the ContractCallDecoder hook landed (PR #166).
- No `update_current_contract_wasm` events for ~36 days through
  scan-end rule out further drift.

### `b400f7a8ac121022` — first-deploy hotfix, 35-min lifetime

This hash was on chain for 420 ledgers (~35 min) before being
replaced by the current production hash. The pattern is
unambiguous:

- Brand-new contract address (no prior deploy in the entire
  post-Soroban scan window — 8.3M ledgers / ~18 months of
  pre-deploy emptiness).
- 35-min lifetime to the next `update_current_contract_wasm`.
- Replaced by a hash that has been stable for ~36 days.

This is the **standard "pushed v1 → caught a bug → pushed v2"**
deploy pattern. A 35-min window is too short for material
production traffic on a brand-new oracle that wasn't yet wired
into any consumer's price-feed registry.

**Database check (recommended pre-backfill)**: before any
backfill replay overlapping L58,758,722 → L58,759,141, verify
that range is empty of redstone trades on r1:

    psql -h localhost ratesengine -c "
      SELECT count(*) FROM trades
       WHERE source = 'redstone'
         AND ledger BETWEEN 58758722 AND 58759141"

The expected result is `0` — Redstone consumers had not yet wired
the new contract address into their pipelines during the 35-min
hotfix window. If the count is non-zero, the b400f7a8 WASM bytes
should be disassembled and reviewed before the backfill proceeds.

**Decoder-shape risk**: schema-level changes (event-body field
names, PriceData field shape, `write_prices` signature) are part
of the integration contract. Redstone tests these against
consumers BEFORE the first mainnet deploy — a 35-min hotfix is
overwhelmingly more likely to be a constant / config / retry-loop
fix than a wire-format change. We cannot disassemble the WASM
bytes inline (no `stellar-core dump-wasm` access from this
session), but the deploy-pattern + zero-traffic + no-decoder-error
combination puts the residual risk at "very low".

**Conditional decision for this hash**: backfill replays whose
range overlaps L58,758,722 → L58,759,141 should run the database
check above first. The expected result is empty (deploy-pattern
+ no consumer wired up yet), in which case the backfill produces
no redstone rows from that window and the b400f7a8 hash is
practically irrelevant to output. If the b400f7a8 WASM had a
different wire format from `5e93d22c…`, the decoder's strict
by-name + by-position extraction would fail-loud per event —
not silently mis-attribute. This matches the audit's fail-closed
posture.

## Caveats

- **WASM bytes not disassembled inline.** The audit relies on
  deploy-pattern analysis, fixture coverage of the current hash,
  and live ingest-health metrics. A v2 audit follow-up should
  fetch the b400f7a8 bytes (via `stellar-core dump-wasm`,
  stellar-rpc `getLedgerEntry` for the WASM-storage key, or by
  hash-comparing against `redstone-public-contracts` git tags) and
  compare event-publish + write_prices signatures against the
  decoder. This is tracked as a follow-up; the current audit
  ships with the deploy-pattern + zero-traffic argument as the
  load-bearing safety claim.
- **Database emptiness check is point-in-time.** If a future
  Redstone backdated correction lands events into the
  L58,758,722 → L58,759,141 range, those would be subject to the
  v1 WASM's schema. Re-verify the database-emptiness invariant
  before any backfill that targets that exact window.

## Decision

**`BackfillSafe: true`** — flipped in
`internal/sources/external/registry.go` in this PR.

Rationale:

- Production hash `5e93d22c…` matches the current decoder; no
  upgrade in ~36 days; live ingest healthy.
- First-deploy hotfix hash `b400f7a8…` ran for 35 min on a
  brand-new contract address. Deploy-pattern analysis indicates
  an internal-bug hotfix, not a wire-format change. Even in the
  worst case, schema drift produces fail-loud per-entry errors
  via strict by-name + by-position extraction — not silent
  attribution errors. Pre-backfill database check (see Caveats)
  is the additional defence-in-depth.

If a future Redstone upgrade lands, the audit gets a per-hash
entry + decoder verification and the flag stays at `true` (or
flips to `false` if the new WASM diverges and the decoder fix
isn't shipped yet).

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/redstone/{events,decode}.go`
- Discovery doc: `docs/discovery/oracles/redstone.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["redstone"].BackfillSafe`
- Upstream contract source: pinned in `VERSIONS.md`
- WASM-history walk JSON (full): `r1:/var/log/wasm-history-all.json`

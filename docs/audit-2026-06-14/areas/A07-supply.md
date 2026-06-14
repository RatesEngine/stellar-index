# A07 — Supply observers + derivation (READ-ONLY audit)

**Date:** 2026-06-14
**Scope:** `internal/supply/` (all `.go` incl. tests) + the supply-
observer packages under `internal/sources/`
(`accounts`, `trustlines`, `claimable_balances`, `liquidity_pools`,
`sac_balances`, `sep41_supply`).
**Method:** read every `.go` (incl. `*_test.go`) in the in-scope
packages + the load-bearing primitives the derivation depends on
(`internal/storage/timescale/classic_supply_observations.go`,
`internal/storage/timescale/sep41_supply_events.go`,
`internal/scval/scval.go::AsAddressStrkey`,
`migrations/0005,0010-0015,0030,0057`), and the two authoritative
discovery docs the SEP-41 supply decoder is checked against
(`docs/discovery/notes/sep-41-token-events.md`,
`docs/discovery/notes/cap-67-unified-events.md`). No source edited,
no git run. Supply package tests run green.

## Audited against

- **D1 correctness** — the 3-domain split (Alg 1 XLM / Alg 2 classic /
  Alg 3 SEP-41); total/circulating/max derivation; the
  Storage{Classic,SEP41}SupplyReader aggregation.
- **D2 ADR invariants** — i128 never→int64 (ADR-0003; supply is the
  highest-risk surface); `*big.Int` + NUMERIC end-to-end; ADR-0011
  circulating-vs-max policy; dispatcher hook per observer
  (LedgerEntryChangeDecoder / OpDecoder / Decoder).
- **D5** — NUMERIC arithmetic correctness; watched-set scoping
  (observers only track watched accounts/assets).
- **D6** — per-class hypertables + column types.

**Files read:** ~46 (29 in `internal/supply` + 35 in the 6 observer
packages, of which the trivial `consumer.go`/`doc.go`/`events.go`
stubs were skimmed; + 4 supporting storage/scval files + 2 discovery
docs + 6 migrations).

---

## Severity counts

| Severity | Count |
|---|---|
| Critical | 0 |
| High | 1 |
| Medium | 2 |
| Low / Info | 4 |

**No i128 truncation found anywhere in scope** — the highest-risk
class for this area is clean (see CORRECT section). The High is a
spec/decoder topic-position mismatch that *drops* (not truncates)
spec/CAP-67-shaped mint+clawback events — same finding A05 raised
against the same package, corroborated here from the primary SEP-41
and CAP-67 docs.

---

## Findings

| Severity | file:line | dimension | issue | why it matters | fix | confidence |
|---|---|---|---|---|---|---|
| **High** | `sep41_supply/decode.go:75-97` (`decodeCounterparty`) + `sep41_supply/doc.go:27-42` + `internal/canonical/discovery/sniffer.go:19-32` | D1 correctness / X8 CAP-67 + SEP-41 dual-shape | The counterparty topic index assumes the **legacy SAC 3-topic shape with `admin` at topic[1]**: `mint→to@topic[2]`, `clawback→from@topic[2]`, `burn→from@topic[1]`. Both authoritative primary-source docs disagree for mint+clawback: `sep-41-token-events.md` (reading SEP-41 v0.4.1) gives `mint = ["mint", to]` (to@**topic[1]**) and `clawback = ["clawback", from]` (from@**topic[1]**); `cap-67-unified-events.md` gives `["mint", to, sep0011_asset]` (to@**topic[1]**) and `["clawback", from, sep0011_asset]` (from@**topic[1]**). Burn (`["burn", from]`, from@topic[1]) is the one the decoder gets right. On a real spec/CAP-67 mint, the decoder reads topic[2] = `sep0011_asset` (a String/Symbol, or absent for the bare 2-topic form) → `scval.AsAddressStrkey` returns `ErrScValType` (or `ErrShortTopic`) → `decodeCounterparty` errors → `Decode` returns the error → **the whole mint/clawback row is dropped**. The same wrong assumption is duplicated in the `discovery/sniffer.go` docstrings (harmless there — the sniffer only reads topic[0] — but it shows the misconception is repo-wide). | If mainnet SAC currently emits the spec/CAP-67 shape, **every mint and clawback is silently lost** → `total_supply` is under-counted (mint missed pulls total down) and clawback compliance volume is invisible. This is the same data-loss *class* as the latent-Critical sep41 projector loss (MEMORY) — a config/positional foot-gun, not truncation. The unit test gives false assurance: `TestDecoder_CAP67_FourTopic_BackCompat` builds its "post-P23 mint" by **appending** `sep0011` to the legacy `["mint", admin, to]` (→ `["mint", admin, to, sep0011]`), which is NOT the real P23 shape `["mint", to, sep0011_asset]`; the fixture encodes the wrong assumption so the test passes while the decoder is wrong. | One lake query on r1 settles which side is wrong: decode a recent watched-contract (USDC/EURC SAC) `mint` + `clawback` and read topic arity + topic[1] type. If mainnet SAC actually emits the legacy `["mint", admin, to]` form, document that explicitly + add a *real* P23-shape fixture. If it emits the spec/CAP-67 shape, switch to identity-based counterparty resolution (scan topics[1..] for the first decodable `ScvAddress`, or branch on shape) and read `to`/`from` from topic[1]; fix the back-compat fixture + the sniffer docstrings either way. | High (decoder vs two primary-source docs is a certain mismatch; whether mainnet SAC currently emits legacy or spec shape needs one lake query) |
| **Medium** | `claimable_balances/dispatcher_adapter.go:52-68` + `liquidity_pools/dispatcher_adapter.go:55-77` (+ both `doc.go` "Removed-variant"; pinned by `TestObserver_SkipsRemovedAtV1` / `TestObserver_SkipsRemoved`) | D1 derivation / D5 watched-set + over-count | Both observers **filter the `Removed` change variant at `Matches`** (the LedgerKey for a claimable balance / LP carries the id/pool but not the asset, so it can't be watched-set-filtered cheaply). Consequence: when a claimable balance is *claimed* or an LP is *withdrawn-to-empty* (the entry is Removed on-chain), the observer writes **no** `is_removal=true` row. The storage sum is `SELECT DISTINCT ON (id) … ORDER BY ledger DESC … WHERE NOT is_removal` — so it keeps the last surviving non-removal observation forever. → `Claimable` and `LPReserve` components of Algorithm-2 `total_supply` **monotonically over-count** as claimables are claimed / pools drained. (Contrast: `trustlines`, `sac_balances`, `accounts` DO emit `is_removal=true` and the `WHERE NOT is_removal` correctly drops them.) | Classic `total_supply` (and thus circulating, FDV, market-cap) drifts high for any watched asset with claimable-balance or LP churn. The crosscheck (`CrossCheck`, 1-stroop tolerance) would catch it only for SAC-wrapped assets where the Alg-3 side is also computed — most classic assets have no Alg-3 counterpart, so the drift is unguarded. Documented as "v1; writer-side lookup follow-up lands when the Sum overcount is measurable in production" — but under the "complete granular coverage" mission (MEMORY) it's a standing correctness gap, not a deferrable nicety. | Add the writer-side Removed path: on a Removed claimable/LP change, look up the prior observation's asset_key (by claimable_id / pool_id) and emit an `is_removal=true` row, OR carry the asset on the LedgerKey path where the SDK exposes it. Until then, document the known high-bias on the affected components. | High (the skip is explicit + test-pinned; the over-count is a direct consequence of the `WHERE NOT is_removal` sum semantics) |
| **Medium** | `supply/storage_sep41_reader.go:54-59,113-121` (`AdminBalance` always 0) | D1 circulating derivation / ADR-0011 Alg-3 | `StorageSEP41SupplyReader` hard-codes `AdminBalance: big.NewInt(0)` because v1 doesn't track `set_admin`. So Algorithm-3 circulating = total − 0 − locked-set, i.e. the documented `BasisAdminExclusion` "admin balance is always excluded" is **only honoured if the operator manually lists the admin in `[supply].PerAsset` LockedSet**. A SEP-41 token configured with no locked-set reports `circulating == total` and stamps `Basis = admin_exclusion` — the basis label asserts an exclusion that wasn't applied. | Over-states circulating for any watched SEP-41 token whose admin holds a non-trivial balance and whose operator hasn't hand-listed the admin. The wire `basis` is misleading (says admin-excluded; isn't). Low real-world blast radius today (single-digit watched SEP-41 set, MEMORY), but it's a silent correctness + provenance-label gap. | Either (a) track `set_admin` and resolve the live admin balance, or (b) when no admin/locked-set is configured, stamp `Basis = admin_exclusion` only if an admin was actually resolved — otherwise a basis that reflects "no exclusion applied". The reader docstring is honest about the simplification; the *basis label* should match. | Medium (the 0 is intentional + documented; the mismatch between the stamped basis and the applied policy is the finding) |
| **Low/Info** | `supply/refresher.go:279-335` (`applyStaleComponentGate`, F-1320 dormant path) | D4 concurrency / D1 | The per-asset `lastComponentLedger` map is read+written in `applyStaleComponentGate` with no mutex. Safe **today** because each `Refresher` is single-asset and driven by one goroutine's ticker (doc: "one Refresher per watched asset … single-keyed in practice"). But the type keys by AssetKey "for safety against a future shared-Refresher caller" — if that future caller ever drives `Tick` from >1 goroutine, the map is an unguarded data race. | No live impact (single-goroutine per Refresher). Recorded so the "future shared-Refresher" comment doesn't lull a contributor into sharing one across goroutines without adding a lock. | Add a `sync.Mutex` around `lastComponentLedger` access, or document "Tick is NOT safe for concurrent calls" on the method. | High |
| **Low/Info** | `supply/textfile.go:113-119` (`last_success_timestamp` uses `time.Now()`) | D9 / X10 determinism | The success-path textfile stamps `last_success_timestamp` with `time.Now().Unix()` rather than `snap.ObservedAt`. Correct for a "did the timer run recently" staleness alert, but it means the metric is wall-clock, not ledger-derived — a backfill/replay re-run would stamp "now" for a historical snapshot. | Cosmetic / monitoring-only (the textfile explicitly is "monitoring data, not source of truth"; `asset_supply_history` keeps the real `observed_at`). Noted for completeness. | None required; optionally document that the success timestamp is run-time not observation-time. | High |
| **Low/Info** | `supply/crosscheck_refresher.go:181` (`DivergenceStroops.Float64()` discards exactness flag) | D1 precision | The cross-check gauge emits `result.DivergenceStroops.Float64()` and discards the `_` exact-ness bool. For a divergence > 2^53 stroops the gauge would lose precision. | Harmless: the gauge is an alerting signal (alert fires on `> 1 stroop`, the actual stroop value is logged exactly via `.String()` on the Over path) — float precision only matters for the dashboard number, and a >2^53-stroop divergence is a catastrophic disagreement that pages regardless. | None required; the exact value is preserved in the WARN log + the result struct. | High |
| **Low/Info** | `supply/storage_classic_reader.go:126-131` (Min-ledger error swallowed to `_ = err`) | D1 observability | On `MinClassicComponentLedger` query error the code does `_ = err; minLedger = 0` with a comment "Log + carry on" but **doesn't actually log** — the operator gets no signal that the freshness gate silently dropped to permissive for this asset. The XLM + SEP-41 sibling readers swallow the same error the same way (also without a log line). | Minor observability gap: a recurring freshness-query failure would silently keep the stale-component gate permissive forever with no breadcrumb. The snapshot itself stays correct (freshness metadata is supplementary). | Add a `WARN` log on the swallowed `MinClassicComponentLedger` / `MinSEP41ComponentLedger` / `MinReserveAccountLedger` errors, matching the comment's stated intent. | High |

---

## CORRECT — verified against the audit dimensions

**D2 i128 NEVER truncates (ADR-0003 — the highest-risk surface):**
- `grep` for the truncation anti-pattern (`parts.Lo`, `int64(...Lo)`,
  `Int128Parts` mishandling) across all 7 in-scope packages is
  **clean** (zero hits).
- The two genuine i128 sources route correctly through full-width
  helpers: `sep41_supply/decode.go:53-57` (`scval.AsAmountFromI128` →
  `amt.BigInt()`) and `sac_balances/dispatcher_adapter.go:216-238`
  (i128 OR map-with-`amount`, both via `AsAmountFromI128` → `BigInt()`).
- The four `big.NewInt(int64(...))` casts are **legitimate**, NOT
  truncation: `trustlines` `tl.Balance`, `claimable_balances`
  `cb.Amount`, `liquidity_pools` `cp.ReserveA/B`, `accounts`
  `entry.Balance` are all `xdr.Int64` — classic Stellar balances are
  int64 stroops on the wire by protocol (max << 2^63). XLM total
  (~50.0018B XLM = 5.0001807e17 stroops) fits int64 with room. The
  `accounts/events.go:46-48` doc even calls this out ("XLM is i64 in
  XDR but we carry NUMERIC upstream").
- The pure-Go arithmetic in the three computers (`xlm.go`,
  `classic.go`, `sep41.go`) is `*big.Int` end-to-end: total/
  circulating built via `new(big.Int).Add/Sub/Set` — no intermediate
  int64, no float.

**D6 NUMERIC columns (ADR-0003 storage leg):**
- Every supply column is `NUMERIC`: `asset_supply_history`
  total/circulating/max (migration 0005, with `>= 0` / `IS NULL OR
  >= 0` CHECKs); all five observer `balance_stroops` (0010-0014);
  `sep41_supply_events.amount` (0015, `CHECK (amount >= 0)`).
- Storage reads cast `::text` and parse via `new(big.Int).SetString`
  (`scanSum`, `scanLatestBalance`, `parseSEP41Numeric`,
  `SEP41NetMintAtOrBefore`) — no float intermediate, no driver
  int64 coercion.

**D1 the 3-domain split + derivation:**
- **Alg 1 (XLM)** `xlm.go`: total = frozen constant
  (`50_001_806_812 × 10^7`, immutable, copy-constructed); max = total
  (hard-capped); circulating = total − Σ(SDF reserves). Nil-reader
  guard, nil-reserve guard, error-on-reader-failure (no
  publish-total-as-circulating fallback). Correct.
- **Alg 2 (classic)** `classic.go`: total = trustline + claimable +
  LP-reserve + SAC-wrapped (4 components); circulating = total −
  issuer − locked-accounts − locked-contracts; max = override else
  nil (no fabrication). `validateClassicComponents` rejects nil +
  negative components before arithmetic. Correct (modulo the
  claimable/LP Removed over-count — Medium above).
- **Alg 3 (SEP-41)** `sep41.go`: total = mint − burn − clawback, with
  `ErrNegativeTotalSupply` guard (physically-impossible reading
  refused, not published); circulating = total − admin − locked-set;
  `validateSEP41Components` nil+negative guard. Correct (modulo
  AdminBalance=0 basis-label gap — Medium above).
- **Aggregation readers**: `StorageClassicSupplyReader` does 6
  queries (4 sums + issuer trustline + per-entity locked lookups),
  any sub-query failure aborts the whole Components (no partial-sum
  publish — explicitly documented); `StorageSEP41SupplyReader`
  composes kind-totals + per-holder SAC lookups. Both validate before
  return.

**D1 SQL aggregation correctness:**
- The four `Sum*AtOrBefore` use `DISTINCT ON (<entity>) … ORDER BY
  <entity>, ledger DESC` then `WHERE NOT is_removal` — the canonical
  "latest-observation-per-entity, drop removed" idiom; per-entity sum
  over the most-recent state. `COALESCE(…, 0)` so an unobserved asset
  returns 0, not NULL.
- `SEP41KindTotalsAtOrBefore` uses `SUM(amount) FILTER (WHERE
  event_kind = …)` per kind — single round-trip, per-kind separation
  preserved (compliance dashboards see clawback distinct from burn).
- `MinClassicComponentLedger` uses `NULLIF(COALESCE(MAX(ledger),0),0)`
  per component + `MIN over WHERE l IS NOT NULL` so an asset observed
  in only one component isn't pinned to 0 by the empty sibling tables
  — correct "slowest *observed* producer" semantics.
- `scanLatestBalance` returns 0 on `sql.ErrNoRows` AND on
  `is_removal=true` — issuer/locked lookups for a removed holder
  correctly contribute 0.

**D5 watched-set scoping (observers only track watched entities):**
- Every observer is constructed from an operator watch-list and
  rejects an empty list (`ErrEmptyWatchSet` / `ErrEmptyWrapperMap`)
  + empty entries: `accounts` (G-strkey set), `trustlines` /
  `claimable_balances` / `liquidity_pools` (asset_key set),
  `sac_balances` (contract→asset map), `sep41_supply` (contract set).
- `Matches` does the cheap gate (type discriminator + asset/contract
  derivation + O(1) map lookup) before any balance extraction — no
  NUMERIC parse on the reject path. LP fires only if ≥1 side is
  watched; emits one Observation per watched side.
- `sep41_supply.Matches` gates on `(contract_id ∈ watched) AND
  (topic[0] ∈ {mint,burn,clawback})` and explicitly skips `transfer`
  (doesn't move total supply) — and the projector reuses this same
  watched-set decoder (F-1316 noted in-code) so dispatcher + projector
  paths can't diverge.

**D2 dispatcher hook per observer (matches CLAUDE.md supply recipe):**
- `accounts`, `trustlines`, `claimable_balances`, `liquidity_pools`,
  `sac_balances` all implement `dispatcher.LedgerEntryChangeDecoder`
  (LedgerEntry mutations) — compile-time-asserted `var _ … = (*Observer)(nil)`.
- `sep41_supply` implements the events-based `dispatcher.Decoder`
  (Soroban contract events) — correct per the recipe ("Decoder for
  SEP-41 mint/burn/clawback observer").
- All amounts carried as `*big.Int` on the Observation/Event structs;
  `EventIndex` added to the sep41 PK (migration 0057 / F-1324) so
  multiple supply events from one op don't collapse on ON CONFLICT.

**D1 removal handling (the ones that DO it right):**
- `accounts`, `trustlines`, `sac_balances` emit `is_removal=true` with
  Balance=0 on `LedgerEntryRemoved` (deriving identity from the
  LedgerKey), and the `WHERE NOT is_removal` sum correctly drops them.
  `accounts` removal nil-guards the entry body. (claimable/LP are the
  exceptions — Medium finding.)

**D1 ADR-0011 max-supply precedence + no-fabrication:**
- `overlay.go` applies the precedence chain operator-override →
  SEP-1 declaration → nil; refuses to apply a negative or
  unparseable SEP-1 `max_supply` (publishes nil rather than junk);
  never touches XLM's computer-set max. `Supply.MaxSupply` is nil
  (→ JSON null) when no defensible value exists. `Basis` correctly
  upgrades to `override` when an override/SEP-1/non-empty-locked-set
  policy actually fired.

**D1 cross-check (Alg-2 ↔ Alg-3 SAC reconciliation):**
- `CrossCheck` is pure (`|classicTotal − sacTotal|` vs 1-stroop
  tolerance), nil-guards both totals, defensively copies inputs into
  the result. `CrossCheckRefresher` sorts + dedups pairs at
  construction (stable emit order across restarts), emits one outcome
  per pair regardless of success (bounded cardinality), distinguishes
  `missing_snapshot` (bootstrap) from `read_error` (transient) via
  `errors.Is(ErrNoSnapshot)`.

**D1 freshness gate (F-1236 / F-0040 / F-1320):**
- The stale-component gate correctly distinguishes "producer stalled"
  (MinComponentLedger changed / first-seen-lagging → reject) from
  "asset dormant" (MinComponentLedger unchanged tick-over-tick → the
  last observation IS the current supply → accept + re-stamp) — the
  F-1320 fix for the permanent-freeze bug. `MinComponentLedger==0`
  (no signal) falls through to legacy-permissive unless strict mode is
  on. Per-asset thresholds layer over the global default.

**D1 config-reader / lcm-reader robustness:**
- `ConfigReserveBalanceReader` parses balances at construction
  (fail-fast on malformed/negative), errors on an unconfigured
  account (no silent zero → no over-stated circulating).
- `LCMReserveBalanceReader` returns `ErrNoObservation` if ANY account
  lacks an in-scope observation (chained-fallback drops to config for
  the WHOLE call — never mixes live+static per call); removed accounts
  contribute 0; nil-Balance guarded.

**D2 no stellar-rpc / no Horizon in scope:** none of the supply
package or observers import `stellarrpc` / Horizon / `GetEvents` —
all data comes via the dispatcher (LedgerEntryChange / events) or the
served-tier storage readers. Compliant.

---

## Bottom line

The supply derivation layer is in **strong shape on the dimension
that matters most for this area — i128 safety. There is zero
truncation: every Soroban i128 (SEP-41 events + SAC contract-data
balances) routes through the full-width `AsAmountFromI128` → `BigInt`
helpers, every column is NUMERIC, and the three computers do pure
`*big.Int` arithmetic.** No Critical.

The one **High** is the `sep41_supply` mint/clawback counterparty
topic-index: it assumes the legacy SAC `["mint", admin, to]` shape
while both primary-source docs say spec/CAP-67 emit `["mint", to,
…]` — if mainnet SAC emits the spec shape, every mint+clawback is
dropped and `total_supply` under-counts. (Same finding A05 raised on
the same package; confirmed here from the SEP-41 v0.4.1 + CAP-67
docs. One lake query settles which side is wrong.) Two **Medium**s:
the claimable/LP Removed-variant over-count (intentional "v1"
deferral, but a standing classic-`total_supply` correctness gap that
test-pins the skip), and the SEP-41 `AdminBalance=0` basis-label
mismatch. The rest is Info-grade hardening (an unguarded map behind a
"future shared-Refresher" comment, two swallowed-without-logging
freshness errors, monitoring-only timestamp/precision notes).

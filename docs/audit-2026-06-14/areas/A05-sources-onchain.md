# A05 — On-chain Soroban sources + gating (READ-ONLY audit)

**Date:** 2026-06-14
**Scope:** all decoder packages under `internal/sources/` EXCEPT
`internal/sources/external/` (separate audit). Focus: Soroban DEX /
oracle / bridge decoders + the factory-gating substrate.
**Method:** read every `.go` (incl. `*_test.go`) in the in-scope
packages + the load-bearing primitives they depend on
(`internal/scval`, `internal/canonical/amount.go`,
`internal/projector/registry.go`). No source edited, no git run.

## Subdir coverage (in-scope, fully read)

`soroswap`, `soroswap_router`, `phoenix`, `aquarius`, `comet`, `blend`,
`defindex`, `cctp`, `rozo`, `band`, `redstone`, `reflector`,
`sep41_supply`, `sep41_transfers`, `childgate`, `sorobanevents` (16).

Supporting (read for invariant checks, not full audit targets):
`internal/scval/scval.go`, `internal/canonical/amount.go`,
`internal/projector/registry.go`.

Out of A05 scope, NOT audited here (classic/SAC supply observers + SDEX
+ external): `accounts`, `trustlines`, `claimable_balances`,
`liquidity_pools`, `sac_balances`, `sdex`, `forex`, `frankfurter`,
`external/`. (Several of these directly import `xdr` — legitimate for
the classic-LedgerEntry observers; flagged below only where in-scope.)

**Files read:** ~58 (all in-scope package `.go` + `*_test.go` + the 3
supporting files).

---

## Severity counts

| Severity | Count |
|---|---|
| Critical | 0 |
| High | 1 |
| Medium | 3 |
| Low / Info | 5 |

The biggest open *architectural* items (Phoenix/Aquarius/Comet/DeFindex
not yet on ADR-0035 childgate) are **known + tracked** (MEMORY:
"phoenix/defindex/aquarius/comet gates pending"), so they are recorded
as Medium/Info "still-open" rather than new findings — except where the
gap is more than the tracker implies.

---

## Findings

| Severity | file:line | dimension | issue | why it matters | fix | confidence |
|---|---|---|---|---|---|---|
| **High** | `sep41_supply/decode.go:75-97` (`decodeCounterparty`) | D1 correctness / X8 CAP-67 | The counterparty topic index is hard-coded to the **legacy SAC** event shape: `mint→topic[2]`, `clawback→topic[2]`, `burn→topic[1]`. But both authoritative discovery docs say the canonical shapes are `mint = ["mint", to]` (SEP-41 spec → `to` at **topic[1]**) and `mint = ["mint", to, sep0011_asset]` (CAP-67 P23 → `to` at **topic[1]**, asset string at topic[2]); clawback = `["clawback", from, sep0011_asset]` (`from` at **topic[1]**). On a real P23/spec mint the decoder reads topic[2] (a String/Symbol `sep0011_asset`, or absent for the 2-topic spec form) as the counterparty → `scval.AsAddressStrkey` returns `ErrScValType` (or `ErrShortTopic`) → **the whole mint/clawback event errors out and is dropped**. | Supply is undercounted: every mint that arrives in the CAP-67/spec shape is silently lost (the amount comes from `Value` but the event never lands because counterparty decode fails first). This is the same data-loss *class* as the latent-Critical sep41 projector loss in MEMORY, but here it's the topic-position assumption rather than the watched-set. The unit test "passes" only because `TestDecoder_CAP67_FourTopic_BackCompat` builds its "post-P23 mint" fixture by **appending** `sep0011` to the legacy `["mint", admin, to]` form (→ `["mint", admin, to, sep0011]`), which does not match the real P23 shape `["mint", to, sep0011_asset]`. The fixture encodes the wrong assumption, so the test gives false assurance. | Verify the on-chain shape on r1 (decode a recent USDC/EURC SAC `mint` + `clawback` from the lake). If mainnet SAC currently emits the legacy `["mint", admin, to]` (some SAC builds historically did), document that explicitly and add a real P23-shape fixture. If it emits the spec/CAP-67 shape, switch to **identity-based** counterparty resolution: don't trust a fixed index — scan topics[1..] for the first `ScvAddress` that decodes, OR branch on `Type=="contract"` (SAC, contract-implicit asset) vs the classic-unified path, and read `to`/`from` from topic[1]. Correct the back-compat fixture either way. | High (decoder/spec mismatch is certain; whether mainnet SAC currently emits the legacy or the spec shape needs one lake query to confirm which side is wrong) |
| **Medium** | `phoenix/consumer.go:170-198` (`buffer.absorb`) + `decode.go:96-110` (`groupKey`) | D1 per-field grouping / X8 Phoenix-8-events | The swap correlation `groupKey` is `(ledger, tx_hash, op_index)` only — it does NOT include `event_index` or pool. Two `swap` actions of the **same kind** in ONE op (the package's own comment + `RawSwap.EventIndex` doc say "a router multi-hop emits several 8-field swaps in one op") share the same key in the same map. Correct reassembly relies on the unverified assumption that all 8 fields of swap-A arrive **contiguously** before any field of swap-B. If they interleave, swap-B's `sender`/`sell_token`/… overwrite swap-A's slots (`RawSwap.assign` is a plain slot write), producing one corrupted merged trade instead of two. | The output-side PK collision is already handled by `FanoutOpIndex(op, RawSwap.EventIndex)`, but that only helps if grouping produced two distinct correct RawSwaps in the first place. If interleaving happens, grouping itself silently merges/corrupts — wrong amounts + a lost trade. The existing multi-action test (`liquidity_decode_test.go:367` BondAndUnbond) only covers *different* actions (which use separate maps), not two same-action swaps in one op. | Either confirm Phoenix multi-hop emits contiguously (8-of-A then 8-of-B) and pin it with a same-action interleave test, OR include `event_index`/contiguity-segment in the swap `groupKey` (key each 8-field run separately). Same review applies to `RawProvideLiquidity`/`RawStake` same-action-twice-per-op. | Medium (real if interleaving occurs; depends on Phoenix's emit order, which isn't verified in-repo) |
| **Medium** | `phoenix/dispatcher_adapter.go:55-58` (`Matches`) | D2 ADR-0035 gating | Phoenix gates purely on topic[0] = String(`swap`/`provide_liquidity`/`withdraw_liquidity`/`bond`/`unbond`/`XYK Pool: `/`initialize`) with **no contract-identity gate** — no childgate, despite `MainnetFactory`/`MainnetMultihop` constants existing in `events.go`. Any foreign contract emitting a `("swap","sender")`-shaped String tuple is absorbed as a Phoenix trade. | ADR-0035 / CLAUDE.md require `Matches()` to gate on contract IDENTITY. Topic strings are not unique. This is the documented "phoenix gate pending" item (MEMORY) — recorded so it isn't lost; risk is somewhat lower than a bare-Symbol collision because the topic is a String literal, but still a mis-attribution vector. | Adopt childgate anchored on the Phoenix factory (`MainnetFactory`) + fan-out to deployed pools (the open Q in `docs/protocols/phoenix.md`); precondition = seed-protocol-contracts + lake re-derive. | High (gap is real + intentional-pending; severity is the judgement call) |
| **Medium** | `aquarius/dispatcher_adapter.go:29-31`; `comet/dispatcher_adapter.go:33`; `defindex/dispatcher_adapter.go:41-43` | D2 ADR-0035 gating | Same class as Phoenix: Aquarius gates on topic[0]=`trade` only; Comet on `(POOL,<event>)` topic bytes only; DeFindex on the `BlendStrategy`/`DeFindexVault`/`DeFindexFactory` String prefixes only. None consult childgate. DeFindex's own docstring names the factory (`CDKFHFJI…NFKI` emitting `create`) so a childgate is buildable but not built. Comet is the genuinely-hard case (no factory namespace — documented open case in CLAUDE.md + `comet/events.go`). | Foreign contracts emitting the same topic shapes are mis-attributed. All documented as pending (MEMORY). Aquarius/DeFindex are buildable (have factories); Comet needs the WASM-hash/allowlist gate per ADR-0035. | Aquarius + DeFindex: childgate on their factories. Comet: operator pool-allowlist or WASM-hash gate (the ADR-0035 carve-out). | High (gaps real + intentional-pending) |
| **Low/Info** | `soroswap/dispatcher_adapter.go:253-256` (`Decode`) | D1 robustness | In the completed-pairs loop, a `decodeSwap` error does `return nil, err` mid-loop, discarding any `TradeEvent`s already appended to `out` for OTHER completed pairs in the same call. `absorb` returns at most one completed pair per call today, so this is currently harmless, but it's a latent partial-loss if `absorb` ever returns >1. | Defensive only; no live impact while one-pair-per-absorb holds. | Accumulate per-pair errors and continue, or document the one-pair invariant at the loop. | High |
| **Low/Info** | `soroswap_router/decode.go:120-139` (`deadline`) | D1 / X8 deadline overflow | The router decoder parses `deadline:u64` → `time.Unix(int64(deadline),0)` with **no upper sanity clamp** (unlike band/reflector/redstone which clamp to `closedAt + 24h`). A garbage far-future u64 would produce an unrepresentable timestamp. | MEMORY confirms the actual fix (NULL an unrepresentable `deadline_ts`, commit 4d8b3c77) lives at the **sink**, not here, so the row isn't dropped. Noted so the decoder/sink split is explicit; consider mirroring the sibling decoders' in-decoder clamp for consistency. | Optional: add the same `sanityFutureWindow` clamp here as in band/reflector/redstone. | High |
| **Low/Info** | `cctp/decode.go` (struct `ClosedAt: e.LedgerClosedAt`) + `dispatcher_adapter.go:71` | D1 timestamp consistency | CCTP/Rozo compute `observedAt = ev.EventClosedAt()` and pass it to the `eventFromX` wrapper, but the inner decode structs also set `ClosedAt: e.LedgerClosedAt` (the raw string-parsed field) directly. Two timestamp paths for the same event; if `EventClosedAt()` and the raw field ever diverge (parse-format edge), the row + wrapper could disagree. | Cosmetic today (both derive from the same ledger close); minor consistency smell. | Use the single `observedAt` everywhere. | Medium |
| **Low/Info** | `phoenix/events.go:88` `EventActionAdmin = "XYK Pool: "` etc. | D2 EVERY-event / completeness | Phoenix `classifyAny` covers swap/liquidity/stake/admin/initialize but the admin/initialize literals are XYK-pool-specific strings with trailing spaces; stableswap (`pool_stable`) admin/init variants may differ. Classification-only (no decode), so a missed admin topic just becomes an unmatched-topic drop, not data loss. | Low impact (these don't produce rows), but the EVERY-event closed-set claim for BackfillSafe should confirm stableswap admin spellings too. | Verify stableswap pool admin/init topic literals before flipping Phoenix BackfillSafe. | Medium |
| **Low/Info** | `blend/events.go:96-108` (Backstop contracts) | D2 EVERY-event | Blend pool/factory events are fully + correctly covered (21 topics), but the **Backstop** event surface (`queue_withdrawal`/`deposit`/`claim`/`distribute`/`donate`/`gulp_emissions`/`rw_zone*`…) is explicitly NOT decoded and the backstop contracts are deliberately kept OUT of the pool gate. | Known-uncaptured per the events.go comment + MEMORY (EVERY-event backlog). Recorded for completeness; not a regression. | Add a backstop decoder + its own gate before claiming 100% Blend event coverage. | High |

---

## CORRECT — verified against the audit dimensions

**D1 amount decoding (ADR-0003 — i128 never truncates):**
- `internal/scval/scval.go` `AsAmountFromI128/U128/U256` compose via
  `canonical.FromInt128Parts`/`FromUInt128Parts`/`FromUInt256Parts`
  (`amount.go:58-93`) using `big.Int.Lsh`+`Add` — full-width, signed
  hi correct. No `int64(parts.Lo)` anywhere in `internal/sources/`
  (grep clean; the only `MustI128` token is a comment in
  `sep41_transfers/decode.go:44`).
- Every decoder routes amounts through these helpers: soroswap (4 swap
  amounts + skim), phoenix (offer/received/liquidity/stake), aquarius
  (3-tuple sold/bought/fee), comet (swap + liquidity + pool_amount_in),
  blend (position/emission/auction/min_collateral/ReserveConfig.supply_cap),
  defindex (strategy amount + vault Vec<i128> + df_tokens), cctp
  (amount/max_fee/fee_collected), rozo (amount), band (u64 rate→big.Int),
  redstone (U256 price), reflector (i128 price), sep41_* (i128 amount).
- ReserveConfig `supply_cap` i128 stored as decimal string
  (`blend/decode_money_market.go:849`) — precision preserved.

**D1 topic dispatch (topic[0] symbol, not contract address):**
- All `classify*` functions byte-compare topic[0] (and topic[1] where
  the protocol uses a 2-tuple namespace) against `scval.MustEncode*`
  constants computed at init — no per-event SCVal parse on the reject
  path. Soroswap/DeFindex correctly use `MustEncodeString` for their
  long (>9 char / spaced) String prefixes; Phoenix uses
  `MustEncodeString` for both topic slots (spaces in "actual received
  amount" force ScvString — correctly handled).

**D1 decode-by-name (Map field name, not position):**
- soroswap swap/new_pair/skim, comet swap+liquidity, blend
  money-market/admin/ReserveConfig/auction-data, defindex
  strategy+vault, cctp all four, rozo both, redstone body — all use
  `scval.MapField`/`MustMapField`. Aquarius + blend position/auction +
  reflector + claim correctly use **positional** `AsTupleN` *only*
  where the contract emits a Rust tuple (ScvVec, not a named struct) —
  the right call, with arity guards to catch upgrades.

**D1 per-field grouping:**
- Phoenix 8-events-per-swap reassembly groups all 8 by name into typed
  slots, only emits on `Complete()` (8/8); separate maps per action
  (swap/PL/WL/bond/unbond) so different actions don't collide; age-out
  eviction bounded by event `ClosedAt` (not wall-clock — correct for
  backfill). (Same-action-twice-per-op caveat → Medium finding above.)
- Soroswap swap+sync correlation by `(ledger,tx,op)`, completes on both
  present, age-out by event ClosedAt; skim is standalone (correctly NOT
  fed into the swap buffer).

**D2 factory-anchored gating (ADR-0035) — the ones that ARE gated:**
- `childgate` registry is clean: factory set (multi-factory) + child set
  as separate maps, `IsFactory` for creation events / `Has` for child
  events, hook fired without lock held, idempotent Seed, concurrency-safe.
- **Blend** correctly gated: `Matches` → `IsFactory` for `deploy`,
  `Has` for every other event; `Decode` seeds children from `deploy`
  with the factory as provenance. `MainnetPoolFactories` is the
  empirically-verified multi-factory set (V1+V2 — both, per ADR-0035).
- **Soroswap** correctly gated on contract IDENTITY: `new_pair` only from
  `MainnetFactories` (the full verified set incl. 3 launch-era
  factories); all pair events only from the registered pair set (seeded
  live + DB warm + genesis walk).
- **CCTP** + **Rozo** gate on a hard-coded contract set AND topic — both
  required in `Matches` and re-checked in `Decode`.
- **Reflector** (×3 DEX/CEX/FX) + **Redstone** + **Band** +
  **soroswap_router** all scope on the configured contract ID
  (and, for band/router, the function name).

**D2 EVERY-event (classify enumerates full topic set):**
- Aquarius (15 topics incl. kill/unkill circuit-breakers), Blend (21
  pool/factory topics), DeFindex (3 strategy + 11 vault + 2 factory),
  Comet (5 POOL events, with a precise docstring of what the Soroban
  port does NOT emit) all enumerate beyond just the rows they produce —
  satisfying the closed-set requirement. (Open EVERY-event items:
  Blend backstop, Phoenix stableswap admin literals — Info findings.)

**D2 one-writer (projected sources via projector only):**
- `internal/projector/registry.go::buildSource` has a case for every
  projected source and reuses the **same** decoder constructors as the
  dispatcher (incl. the sep41 watched-set decoders — the F-1316
  synthetic-watched-contract loss is fixed: real `watchedSEP41` passed,
  skip-when-empty). band/sdex/soroswap-router/external correctly fall to
  the `default` (non-projected, dispatcher-written) per ADR-0032.

**D2 no stellar-rpc in decoders:**
- No `stellarrpc`/`GetEvents`/`BackfillRange`/`StreamLive` on any
  decode/Matches path. The one `stellarrpc` import is
  `soroswap/factory_seed.go::SeedFromFactoryRPC` — operator/boot seed
  tooling called explicitly, NOT from the live path (CLAUDE.md-allowed
  exception; the package README states "No stellarrpc.GetEvents on the
  live path").

**X8 known traps — verified still handled:**
- Soroswap SwapEvent has no reserves → swap+sync correlated by
  `(ledger,tx,op)`; reserves come from the SyncEvent. ✓
- Phoenix 8-events-per-swap grouped (Complete() = 8/8). ✓
- Comet shared `(POOL,<event>)` topic → acknowledged gating limitation,
  documented; decoder reads token_in/out from the body (no pool
  registry needed). ✓ (gating still open — Medium)
- Reflector = 3 separate contracts (DEX/CEX/FX), each contract-scoped;
  per-variant quote (DEX→XLM, CEX/FX→USD); no on-chain twap/x_* — TWAP
  computed downstream. ✓
- Band stores E18 pair / E9 single + emits ZERO events → ContractCall
  `relay`/`force_relay` decoder, E9 `DefaultDecimals`, USD special-cased
  (skipped), pair-rate not emitted from relay (it's storage-derived). ✓
- Redstone body carries no feed_id → feed_ids pulled from `OpArgs`
  (`write_prices(updater, feed_ids, payload)`), strict
  `len(feedIDs)==len(prices)` guard (`ErrFeedIDCountMismatch`), per-feed
  quote (EUROC/EUR fix). ✓
- SEP-41 transfer data i128 OR map → `sep41_transfers/decode.go:64-96`
  type-tests `sv.Type` (I128 vs Map{amount,to_muxed_id}) before reading,
  per the discovery-doc pattern. ✓
- CAP-67 4-topic vs SEP-41 3-topic dual shape → `sep41_transfers` reads
  from/to at topic[1]/[2] and ignores trailing `sep0011_asset` (works
  for both arities). `sep41_supply` is where the dual-shape handling is
  WRONG for mint/clawback — see High finding. (transfers ✓; supply ✗)
- Contract schema evolution (WASM-version-aware) → decode-by-name +
  arity guards + per-field MustMapField errors give fail-loud on drift;
  defindex documents surviving its known WASM upgrade by field name. ✓
- Reflector/Band/Redstone oracle-timestamp sanity clamp (epoch floor +
  `closedAt + 24h` window) prevents the timestamptz-overflow class that
  bit soroswap-router's deadline_ts. ✓ (router decoder itself relies on
  the sink for this — Info finding.)
- Fan-out OpIndex to avoid trades-PK collision (ADR-0033): aquarius,
  comet, soroswap, phoenix use `FanoutOpIndex(op, event_index)`;
  reflector/redstone/band use `op*stride + i`. ✓
- ScAddress CAP-67 5-variant decode (Muxed/ClaimableBalance/LiquidityPool)
  handled in `scval.AsAddressStrkey` — the LP-destination silent-drop
  bug is fixed. ✓

---

## Bottom line

The decode layer is in strong shape: zero i128 truncation, consistent
decode-by-name, every known X8 trap handled — **except** the
`sep41_supply` mint/clawback counterparty topic-index, which assumes the
legacy SAC shape and will drop CAP-67/spec-shaped mints (High; needs one
lake query to confirm which side is wrong, then a small fix + a real
P23 fixture). The ADR-0035 childgate rollout is the main *architectural*
debt — Blend + Soroswap are done and gated; Phoenix/Aquarius/DeFindex/
Comet are still topic-only (known + tracked; Comet is the legitimately
hard no-factory case). One latent grouping risk in Phoenix
(same-action-twice-per-op relies on contiguous emit order) is worth
pinning with a test.

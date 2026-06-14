# X1 — Ingest dataflow (end-to-end seam audit)

Cross-cutting seam per `00-audit-plan.md §3`: trace the FULL pipeline across
package boundaries and verify type contracts + the one-writer invariant at
each hop.

```
LedgerCloseMeta (Galexie MinIO)
   │
   ▼ internal/ledgerstream (Stream / StreamArchiveThenLive)         [hop 1]
   │   one *xdr.LedgerCloseMeta per ledger → callback
   ▼ cmd/stellarindex-indexer main: processAndPersistCursor
   │
   ├─► internal/pipeline.ProcessLedger → internal/dispatcher.ProcessLedger   [hop 2]
   │      4 decoder seams: Decoder (events) / OpDecoder (classic) /
   │      ContractCallDecoder (event-less) / LedgerEntryChangeDecoder (entries)
   │      ├─ dispatchOne also fans every Soroban event to:
   │      │     • DiscoverySink (SEP-41 sniff → discovered_assets)
   │      │     • RawEventSink  (→ soroban_events landing zone, ADR-0029)
   │      └─ emits []consumer.Event  → events channel (cap 256)
   │            │
   │            ▼ internal/pipeline.PersistEvents (8 workers)               [hop 3a]
   │               type-switch (HandleEvent / tradeFromEvent batch path)
   │               → internal/storage/timescale (served tier)
   │               SinkMode gates the projected subset (one-writer)
   │
   ├─► internal/storage/clickhouse.ExtractLedger → LiveSink.PushLedger      [hop 3b]
   │      structural extract (decoder-independent) → CH Tier-1 lake
   │      (best-effort, NON-BLOCKING, bounded-drop)
   │
   └─► internal/dispatcher.CensusLedger → ledger_ingest_log (ADR-0033 substrate)

internal/projector (tails soroban_events OR CH contract_events feed-switch)   [hop 4]
   │   per-source cursor; resolveTip bounded by ledgerstream cursor (+ CH watermark)
   ▼ same per-source Decoder.Decode → pipeline.HandleEvent → timescale
   │   (Phase 3 parallel = both write; Phase 4 = projector sole writer)
   ▼
internal/api  (reads served tier; explorer reads CH lake — see X3)            [hop 5]
```

## Verdict (one-line)

**The end-to-end ingest dataflow is sound and the one-writer invariant
holds.** Every `consumer.Event` type a decoder can emit (32 concrete types)
has a `HandleEvent` arm — the type-switch trap is closed and test-pinned. The
projected/non-projected partition is consistent across the four sites that
must agree (`IsProjectedEvent`, `projector/registry.go::buildSource`,
`projected_test.go`, `pipeline.BuildDispatcher`). Type contracts are faithful
across the Postgres-soroban_events and CH-contract_events reconstruct paths
(both populate `EventIndex` + `OpArgs`). Cursor/watermark continuity is
correct: the projector clamps to the ledgerstream cursor and, in CH
feed-switch mode, to the lake's `ContiguousWatermark`, so a best-effort
dual-sink drop stalls rather than skips. No double-write or missed-write of
any event domain was found in the steady-state configuration. Findings are
all Medium/Low — primarily latent foot-guns (operator can re-arm an
already-closed silent-loss path), cross-walk drift risk between the two
entry-change extractors, and doc/lockstep gaps. The single previously-Critical
class (F-1316 SEP-41 projector total-loss under Phase-4 default) is **closed**
at the config layer (verified below) and re-armable only by explicit operator
override.

## Findings

| severity | location | issue | why it matters | fix | conf |
|---|---|---|---|---|---|
| Medium | `internal/storage/clickhouse/extract_entry_changes.go:37-52` + `internal/dispatcher/dispatcher.go:728-739` | **Two independent hand-walks of the same tx-meta entry-change stream that must stay byte-identical but are not factored together.** The lake's `extractEntryChanges` and the live supply-observer feed's `walkEntryChanges` both walk V3/V4 `TxChangesBefore` → op `Changes` → `TxChangesAfter` + `FeeChanges`, and BOTH omit `tx.PostTxApplyFeeChanges` (a P23/V4 field). They agree TODAY, but they are two copies maintained in lockstep by comment only (`extract_entry_changes.go:18` "Mirrors dispatcher.walkEntryChanges EXACTLY"). | The ADR-0034 promise is "re-derive the LedgerEntry supply observers from the lake." That re-derive is only correct if the lake's `ledger_entry_changes` rows are the SAME set the live `LedgerEntryChangeDecoder` hook saw. If one walk drifts (e.g. someone adds `PostTxApplyFeeChanges` to only one), the re-derived supply diverges from live silently — and there is no reconcile that compares the two entry-change producers (the ADR-0033 reconcile covers soroban_events + classic-trade counts, not entry-changes). | Extract the meta-walk into one shared function both call, OR add a parity fixture test that asserts the two producers emit identical (op_index, change_type, key) tuples for a V3 and a V4 ledger. Decide once on `PostTxApplyFeeChanges` and apply to both. | med |
| Medium | `internal/config/config.go:508-509,1019-1022` + `internal/config/load.go:34-35` | **The F-1316 SEP-41 silent-total-loss path is closed but re-armable by one explicit operator line.** Phase-4 sole-writer mode (`Projector.Enabled=true` + `PersistPerSource=false`) makes the dispatcher skip every projected event; the projector cannot serve a domain it has no registry entry for. `BuildRegistry` *silently skips* (`registry.go:46-53`, returns `ok=false`) any enabled source it can't build — including sep41_* when `watched_sep41_contracts` is empty, reflector/redstone if those build paths ever soft-skip, etc. If the dispatcher is ALSO skipping that subset, the domain is dropped with no error and no metric distinguishing "projector wrote 0 because silent-skipped" from "projector wrote 0 because no activity." | The default is now safe (`Default()` seeds `PersistPerSource=true`, `LoadReader` decodes over `Default()` so an OMITTED key keeps `true`, and `default_drift_test.go` enforces the tag↔Default lockstep). But an operator who writes `persist_per_source = false` to "finish Phase 4" while ANY enabled projected source lacks its config (sep41 watched set, an oracle contract) silently loses that whole domain. The projector's skip is the quiet half — the dispatcher's skip is the loud half, but neither logs "you just dropped source X." | At projector wiring (`main.go:468-499`) diff `EnabledSources ∩ projected-set` against the built `registry.Sources`; for any projected source enabled but NOT in the registry while `PersistPerSource=false`, refuse to start (or force `SinkModeAll` for the gap) with a named error. Promote the silent `ok=false` skip in `BuildRegistry` to a logged line. | med |
| Medium | `internal/storage/clickhouse/live_sink.go:127-133` + `cmd/stellarindex-indexer/main.go:536-568` | **The ledgerstream cursor advances per ledger regardless of whether the CH dual-sink accepted that ledger.** `processAndPersistCursor` upserts the cursor after `ProcessLedger` enqueues to Postgres; the CH `ExtractLedger`+`PushLedger` runs AFTER and is non-blocking bounded-drop. A dropped/errored ledger leaves a hole in the lake while the cursor sails past it. | This is BY DESIGN (CH is best-effort; the `ch-live-catchup` gap-scan timer + `ContiguousWatermark` clamp are the compensating controls, all documented). It is correct ONLY because (a) the catch-up timer gap-scans BELOW CH_max (not tip-only), and (b) the feed-switch projector reads only `≤ ContiguousWatermark`. The risk is operational: if the catch-up timer is disabled/wedged, the lake accrues permanent holes the cursor can never re-trigger, and the *Postgres-sourced* projector (feed-switch OFF) is unaffected and would mask the lake gap. | No code change required; this is the documented contract. Ensure the `ch-live-catchup` timer's liveness is itself alerted (a dead timer = silently-growing lake holes). Confirm the gap-scan is below-CH_max in its own area audit (A18/ops). | med |
| Low | `internal/sources/sorobanevents/reconstruct.go:69-84` (Postgres path) vs `internal/storage/clickhouse/event_reader.go:159-194` (CH path) | **Topic-count divergence between the two event-reconstruct paths.** The Postgres `soroban_events` schema stores only topics 0-3 and `reconstructTopics` caps at 4; the CH `contract_events.topics_xdr` is an unbounded `Array(String)` and `scanContractEvents` passes them through whole. An event with >4 topics reconstructs with 4 topics from Postgres but all N from CH. | No live impact: every decoder in the repo matches on `topic[0]` and reads ≤4 topics (SEP-41 CAP-67 is exactly 4). But the two projector feed sources (`soroban_events` vs CH feed-switch) are NOT byte-identical for a hypothetical >4-topic event, so a future decoder that reads `topic[4]` would behave differently depending on the feed-switch flag. | Either cap CH topics to 4 on read for parity, or document that the soroban_events path is lossy beyond 4 topics and the CH path is the authoritative one (favouring decommissioning soroban_events per ADR-0034 #10). | low |
| Low | `internal/pipeline/sink.go:269-290` (`IsProjectedEvent`) + `internal/pipeline/projected_test.go` | **`soroswap_router.Event` is the one non-projected type not pinned in the lockstep table.** It is handled correctly (falls to `default: return false`), but unlike sdex/external/band it has no `projected_test.go` case asserting `false`. (Confirmed independently in A01 finding row.) | If someone later moves soroswap-router under the projector (it currently re-derives from the lake via `StreamContractCallOps`), the missing pin means the projected/non-projected split could drift without a failing unit test. The import-lint (ADR-0030) is the only remaining backstop. | Add `{"soroswap_router.Event", soroswap_router.Event{}, false}` to the table. | high |
| Low | `internal/dispatcher/dispatcher.go:559,623,671` | **`accountIDToStrkey(MuxedAccount.ToAccountId())` can panic on an unknown muxed type, and the panic aborts the WHOLE ledger** (via `pipeline.ProcessLedger`'s recover) rather than skipping one tx, unlike every other per-tx error in the loop. (Same as A01 finding — restated here because it is the one spot in the dispatch hop that violates the "one bad tx never aborts the ledger" contract that holds everywhere else in the seam.) | Defensive-only (only Ed25519/MuxedEd25519 are valid envelope sources on a real ledger). If it ever fires, the cursor refuses to advance and the ledger is retried forever — a hard ingest stall, not a skip. | Guard the source-account strkey derivation as a per-tx skip (`txReadErrors++ + continue`), or check `.Type` before `ToAccountId()`. | low |
| Low | `internal/storage/clickhouse/sink.go:382-394` (`flushChanges` docstring) | **Stale doc claims the entry-change lake is always empty** ("`s.changes` is currently ALWAYS empty … the lake has NO substrate"), now FALSE since `extract.go:106` populates it. (Same as A02 finding; load-bearing for X1 because the entry-change → lake → future-read path (item (d)) is exactly the substrate this doc denies exists.) | An agent tracing the (d) entry-change read path would read `flushChanges` and conclude the lake has no entry-change substrate, contradicting `extract_entry_changes.go`. | Update the docstring + the line-393 "always taken" inline comment. | high |
| Info | `cmd/stellarindex-indexer/main.go:455` (`events` chan cap 256) vs `internal/pipeline/dispatcher.go` (ProcessLedger buffers a whole ledger before enqueue) | **No back-pressure from sink to dispatcher within a ledger.** `ProcessLedger` accumulates every emitted event for the ledger into one slice, returns it, then the caller pushes them onto the cap-256 channel one at a time. A pathological large ledger holds the whole set in memory; the 8-worker sink applies back-pressure only across the channel boundary. (Same as A01 low.) | Bounded by one ledger (not a leak), and the shutdown drain is correct (fresh-context flush + undrained-range ERROR log at `sink.go:339-388`). Noted for the seam record: the only place an event can be silently lost on the served-tier path is the 90s drain-deadline trip, which logs the exact re-derivable ledger range at ERROR (ADR-0034 recovery) rather than dropping silently. | None — acceptable. | n/a |

## (a) Type-switch trap — every consumer.Event has a handler

Exhaustive inventory: **32 concrete `consumer.Event` types** across the source
packages. All 32 have an arm in `pipeline/sink.go::HandleEvent`; the `default`
arm counts+logs `unhandled` (no silent drop). All use value receivers, so the
type-switch matches the values the decoders emit (decoders return
`[]consumer.Event{TradeEvent{...}}`, not pointers — verified e.g.
`defindex/dispatcher_adapter.go:58,66`). The batch fast-path
`tradeFromEvent` covers the 6 trade-shaped types and is documented as
"MUST stay in lockstep with HandleEvent"; every other type correctly falls to
the per-event `HandleEvent` slow path (correctness-equivalent). **No trap
gap.** The projector reuses the SAME `HandleEvent` as its sink
(`main.go:476-478`), so a new event type is handled identically on both write
paths.

## (b) One-writer invariant (ADR-0031/0032)

The partition must agree across four sites; verified consistent:

- `pipeline/sink.go::IsProjectedEvent` — 22 projected types listed.
- `projector/registry.go::buildSource` — builds the 22-type-emitting sources
  (soroswap{Trade,Skim}, aquarius, phoenix{Trade,Liquidity,Stake},
  comet{Trade,Liquidity}, blend×6, cctp, rozo, defindex{Event,VaultEvent},
  sep41_supply, sep41_transfers, reflector{dex,cex,fx}, redstone).
- `pipeline/projected_test.go` — pins 22 projected + 4 non-projected (gap:
  soroswap_router not pinned — Low finding above).
- `pipeline/BuildDispatcher` — registers the same source decoders for the
  dispatcher path.

Multi-event-per-decoder cases verified: the single soroswap decoder emits
Trade+Skim, single phoenix emits Trade+Liquidity+Stake, single defindex emits
Event+VaultEvent, single comet emits Trade+Liquidity — **all** their event
types are in the projected set, so no decoder straddles the projected/
non-projected line. SinkMode wiring (`main.go:450-453`): `SinkModeSkipProjected`
is selected ONLY when `Enabled && !PersistPerSource`; otherwise `SinkModeAll`.
In Phase-3 (`PersistPerSource=true`, the default) both the dispatcher
events-goroutine AND the projector write the projected subset — intentional
double-write absorbed by `ON CONFLICT DO NOTHING` on the per-source PKs. **No
unintended double-write; no missed-write** in the default config.

## (c) Type contracts across hops (no silent field drops)

- LCM → events.Event (`dispatcher.contractEventToEventsEvent`): sets `Type`,
  `Ledger`, `LedgerClosedAt` (RFC3339), `ContractID`, `OperationIndex`,
  `EventIndex` (load-bearing for the soroban_events PK — without it Phoenix's
  8 events/op collapse to 1), `TxHash`, `Topic` (base64 SCVal), `Value`,
  `OpArgs`. Byte-identical to stellar-rpc getEvents shape (decoders
  byte-equality-match topics).
- events.Event → soroban_events (Postgres) → `sorobanevents.Reconstruct`:
  round-trips `EventIndex` + `OpArgs` (Redstone needs OpArgs for feed_ids).
  Lossy only on topics >4 (Low finding).
- events.Event → contract_events (CH) → `scanContractEvents`: same field set,
  `EventIndex` + `OpArgs` preserved; `Value`/topics stored already-base64.
  `extract.go:56-58` docstring asserts byte-identical serialization vs the
  dispatcher's emit; verified the MarshalBinary+base64.Std encoding matches on
  both sides (`extract.go:264-291` vs `dispatcher.go:970-998`).
- consumer.Event → timescale rows (`HandleEvent` arms): i128 amounts are
  carried as `*big.Int`/`.String()` into NUMERIC columns throughout (ADR-0003
  spot-checked: soroswap-router AmountIn/Out `.String()`, defindex `.String()`,
  cctp/rozo `Amount` big.Int, oracle `Price.String()`). No int64 truncation in
  the sink hop.
- The classic-trade census (`extract.go::claimAtomCount`/`realTradeCount`) is
  documented + verified to mirror `sdex.extractClaimAtoms` +
  `dispatcher.census` EXACTLY (same op types, same both-zero-dust exclusion, same
  passive-offer dual-result-arm fallback) so the lake's
  `classic_trade_effect_count` equals `COUNT(trades)` for the ADR-0033
  reconcile.

## (d) Entry-change extract → lake → (future) read path

`extractEntryChanges` (added under ADR-0038 Phase C, closes G12-03) populates
`LedgerExtract.Changes` with one `LedgerEntryChangeRow` per
created/updated/state/removed entry change, base64-ing entry+key XDR, tagging
change/entry type, at op_index -1 for fee/tx-level and op-index for per-op
changes. `change_index` is a per-tx monotonic counter incremented only for
successfully-emitted rows — **deterministic across re-ingest** (pure function
of identical input XDR), which is load-bearing for ReplacingMergeTree dedup
(A02 Info finding confirms). The rows reach the lake via the LiveSink
(`flushChanges`). **There is no production READ path yet** — the explorer's
account-state re-derive (current balances/trustlines/offers/contract-data) is
the intended future consumer (ADR-0038 Phase C), and the ADR-0034 "re-derive
the LedgerEntry supply observers from the lake" promise depends on it. The
seam is write-complete, read-pending. The Medium drift-risk finding (a) is the
material concern for this item: the lake's entry-change rows must equal what
the live supply observers consumed, and that parity rests on two hand-walks
agreeing.

## (e) Cursor / watermark continuity

- **Indexer cursor** (`cursorSource`, sub_source ""): `resolveStartLedger`
  resumes at `cursor.LastLedger + 1`; `processAndPersistCursor` upserts after
  each ledger's events are ENQUEUED (NOT after they're durable — the comment is
  explicit; durability is proven by the ADR-0033 reconcile, not the cursor).
- **Archive→live seam** (`StreamArchiveThenLive`): bounded archive read
  [from, seam-1] then unbounded live read [seam, ∞). Restart mid-archive
  resumes <seam → replays the archive→live progression; restart between phases
  (cursor=seam-1) takes the live-only branch. Continuity is correct.
- **soroban_events RawEventSink**: applies back-pressure (PushEvent MAY block)
  so the producer's cursor "cannot outrun durable writes" — this is the fix
  for the 2026-05-26 0.43%-drop incident where non-blocking drop + an
  already-advanced cursor made `-resume` skip the dropped ledgers.
- **Projector tip** (`resolveTip`): base bound = the `ledgerstream` cursor's
  LastLedger (never gets ahead of durably-ingested ledgers). In CH feed-switch
  mode, additionally clamped to `ContiguousWatermark(from)` — the highest
  ledger with NO hole below it — because the best-effort dual-sink can leave
  holes. The projector advances its per-source cursor to `toLedger`
  unconditionally (to skip event-free stretches), so the watermark clamp is
  what prevents reading-past-a-hole from silently losing that ledger's events.
  The watermark is keyed off `stellar.ledgers`, written LAST in each flush, so
  "present in ledgers ⟹ complete in CH." **Continuity correct; the
  watermark clamp is the load-bearing control and is present.**
- **Per-source projector cursors** are independent (one stuck decoder doesn't
  block others), advance to `toLedger` not `lastSeenLedger`, and on
  read/decode/sink failure leave the cursor untouched for next-cycle retry
  (idempotent via ON CONFLICT). Decode failures soft-fail and DO advance
  (deterministically broken row).

## (f) Double-write / missed-write per event domain

- **Projected Soroban subset** (22 types): Phase-3 default = double-write
  (dispatcher + projector), resolved by ON CONFLICT DO NOTHING — no duplicate
  rows, writer-of-record is whichever wins the race. Phase-4 (operator opt-in)
  = projector sole writer; dispatcher skips. No missed-write in either mode in
  steady state. The ONLY missed-write risk is the re-armable Phase-4 config
  foot-gun (Medium finding b).
- **Non-projected dispatcher subset**: sdex (classic OpDecoder → trades),
  external CEX/FX (parallel goroutines → trades/oracle_updates, same `events`
  channel + sink), band (ContractCallDecoder → oracle_updates),
  soroswap_router (ContractCallDecoder → soroswap_router_swaps, log-only/
  excluded from IsProjectedEvent), supply observers (5 LedgerEntryChangeDecoder
  /event observers → per-class hypertables). Each has exactly one writer (the
  dispatcher events-goroutine). Re-derive for band + soroswap_router is from
  the lake via `StreamContractCallOps` (`ch-rebuild -contract-calls`), NOT a
  projector path — correct per ADR-0032 (ContractCall sources have no
  soroban_events landing zone). No double-write.
- **External path** shares the SAME `events` channel + `PersistEvents` sink as
  the dispatcher, so `IsProjectedEvent(external.*)=false` correctly keeps them
  flowing under `SinkModeSkipProjected` (external is not Soroban-derived). No
  drop.
- **Lake (dual-sink)**: a SECOND, decoder-independent write of every ledger's
  structure. Not a double-write of the SERVED tier (different store, different
  purpose); the lake is the authoritative raw substrate, Postgres is the
  served working set (ADR-0034). Best-effort drops are healed by the catch-up
  timer (Medium finding c covers the operational dependency).

## Event-type → writer matrix

Writer key: **DISP** = dispatcher events-goroutine (`PersistEvents`);
**PROJ** = projector (`pipeline.HandleEvent`); **DISP→served / LAKE** both
where the dual-sink also captures the substrate.

| consumer.Event type | source | seam | projected? | served-tier writer (steady state) | re-derive / catch-up |
|---|---|---|---|---|---|
| soroswap.TradeEvent | soroswap | Decoder | yes | PROJ (Ph4) / DISP+PROJ (Ph3) | projector-replay / CH feed |
| soroswap.SkimEvent | soroswap | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| aquarius.TradeEvent | aquarius | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| phoenix.TradeEvent | phoenix | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| phoenix.LiquidityEvent | phoenix | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| phoenix.StakeEvent | phoenix | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| comet.TradeEvent | comet | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| comet.LiquidityEvent | comet | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| blend.NewAuctionEvent | blend | Decoder (gated) | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| blend.FillAuctionEvent | blend | Decoder (gated) | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| blend.DeleteAuctionEvent | blend | Decoder (gated) | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| blend.PositionEvent | blend | Decoder (gated) | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| blend.EmissionEvent | blend | Decoder (gated) | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| blend.AdminEvent | blend | Decoder (gated) | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| cctp.Event | cctp | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| rozo.Event | rozo | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| defindex.Event | defindex | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| defindex.VaultEvent | defindex | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| reflector.UpdateEvent | reflector{dex,cex,fx} | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| redstone.UpdateEvent | redstone | Decoder | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| sep41_supply.Event | sep41_supply | Decoder (watched) | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| sep41_transfers.Event | sep41_transfers | Decoder (watched) | yes | PROJ / DISP+PROJ | projector-replay / CH feed |
| sdex.TradeEvent | sdex | OpDecoder | no | DISP | ch-rebuild -sdex-gaps (lake) |
| band.UpdateEvent | band | ContractCallDecoder | no | DISP | ch-rebuild -contract-calls (lake) |
| soroswap_router.Event | soroswap_router | ContractCallDecoder | no | DISP (log-only) | ch-rebuild -contract-calls (lake) |
| external.TradeEvent | external CEX | own goroutine | no | DISP (shared sink) | external Backfiller (vendor API) |
| external.UpdateEvent | external FX/agg | own goroutine | no | DISP (shared sink) | external Poller/Backfiller |
| accounts.Observation | accounts | LedgerEntryChangeDecoder | no | DISP | ch-rebuild entry-changes (future) |
| trustlines.Observation | trustlines | LedgerEntryChangeDecoder | no | DISP | ch-rebuild entry-changes (future) |
| claimable_balances.Observation | claimable_balances | LedgerEntryChangeDecoder | no | DISP | ch-rebuild entry-changes (future) |
| liquidity_pools.Observation | liquidity_pools | LedgerEntryChangeDecoder | no | DISP | ch-rebuild entry-changes (future) |
| sac_balances.Observation | sac_balances | LedgerEntryChangeDecoder | no | DISP | ch-rebuild entry-changes (future) |

32/32 types accounted for. Projected: 22. Non-projected (dispatcher-owned): 10.
Each row has exactly one steady-state served-tier writer (Phase-4) or one
writer-of-record under ON-CONFLICT (Phase-3). No domain has zero writers; no
domain has two uncoordinated writers.

## Cross-references to per-area audits

- A01 (ingest-core): `walkEntryChanges` PostTxApplyFeeChanges gap; muxed-account
  panic; ProcessLedger per-ledger buffering; IsProjectedEvent soroswap_router
  test gap. (X1 restates the seam-level consequences.)
- A02 (lake): `flushChanges` stale "always empty" docstring; explorer-reader
  duplicate-row reads; `change_index` determinism (load-bearing for (d)).
- A04 (projector/completeness): the ContiguousWatermark clamp + ADR-0033
  reconcile are the completeness controls this seam relies on.

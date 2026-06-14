# A01 — Ingest core (READ-ONLY audit)

Scope: `internal/ledgerstream/`, `internal/dispatcher/`, `internal/consumer/`,
`internal/pipeline/` — all `.go` files including `*_test.go`.

Auditor pass date: 2026-06-14. Method: full read of every in-scope file +
cross-check against the SDK (`go-stellar-sdk@v0.5.0`) for the meta-version /
event-indexing semantics the dispatcher relies on.

**Files read: 42 / 42** (14 dispatcher incl. statsflush, 7 ledgerstream,
3 consumer, 11 pipeline + their tests). Plus 2 SDK files
(`ingest/ledger_transaction.go`, `xdr/muxed_account.go`) consulted to verify
attribution invariants.

---

## Findings

| severity | file:line | dim | issue | why it matters | suggested fix | conf |
|---|---|---|---|---|---|---|
| medium | dispatcher.go:728-739 (`walkEntryChanges`) | D1/D5 | The entry-change walk reads `tx.UnsafeMeta.MustV3/MustV4` + `tx.FeeChanges` but never walks `tx.PostTxApplyFeeChanges` (a P23/V4 field). The SDK's own struct doc says to prefer `LedgerTransaction.GetChanges()` (which DOES include post-apply fee changes) over hand-walking `UnsafeMeta`. | A balance change to a watched AccountEntry that lands in the post-apply fee block is silently invisible to the supply observers (accounts / trustlines / etc.). Narrow surface (post-apply fee changes are rare) but it is a coverage gap against the "every event" principle and it is the kind of drift the manual walk invites. | Either also dispatch `tx.PostTxApplyFeeChanges` (opIdx -1) in `walkEntryChanges`, or switch the walk to the SDK's `tx.GetChanges()` higher-level accessor and map its `Change` back to `LedgerEntryChange`. Add a V4-with-post-apply-fee fixture test. | med |
| medium | ledgerstream.go:265-292 / 300-328 (`maybeTolerateTrailingMissing` / `parseTrailingMissingSeq`) | D5/robustness | The trailing-edge tolerance only fires for the SDK error string `ledger object containing sequence X is missing`. The sibling SDK failure `maximum retries exceeded for downloading object containing sequence X` is explicitly NOT matched (asserted in `trailing_edge_internal_test.go:58-64`). That second wrap is a real live-tip failure mode (SDK exhausts retries on a not-yet-written partition). | A bounded backfill/chain-walk that races the live tip can still hard-error with `TolerateTrailingMissing=true` set, when the SDK happens to surface the retry-exhausted wrap instead of the plain missing wrap — defeating the whole point of the flag and turning a benign tip race into a failed walk. | Extend `trailingMissingRE` (or add a second regex) to also capture the `maximum retries exceeded ... sequence (\d+)` wrap, gated by the same window check. Add a test case mirroring the existing negative one. | med |
| low | dispatcher.go:559,623,671 (`accountIDToStrkey(...ToAccountId())`) | D1/D5 | `MuxedAccount.ToAccountId()` (SDK `xdr/muxed_account.go:179`) **panics** on an unknown muxed-account type — it is called on the tx source + per-op source with no per-tx guard. The surrounding per-tx loop is otherwise carefully written so one bad tx never aborts the ledger; this call is the one spot that would. | Only Ed25519 / MuxedEd25519 are valid envelope source-account types on a real ledger, so this is defensive-only. But if it ever fires, it aborts the WHOLE ledger via `pipeline.ProcessLedger`'s recover (cursor refuses to advance, ledger retried forever) rather than skipping one tx — inconsistent with the per-tx error tolerance everywhere else. | Wrap the source-account strkey derivation so an unexpected muxed type is a per-tx skip (`txReadErrors++` + continue) rather than a recovered panic, OR check `m.Type` before `ToAccountId()`. | low |
| low | dispatcher.go:524,690 (`outputs` accumulation in `ProcessLedger`) | D5 | `ProcessLedger` accumulates EVERY emitted `consumer.Event` for the whole ledger into one slice, then returns it; `pipeline.ProcessLedger` then pushes them onto the channel one at a time. Per-ledger memory is unbounded in ledger size. | A pathological large ledger (many thousands of trades/events) holds the full set in memory before any is drained. Bounded by one ledger so not a leak, but it forfeits streaming back-pressure that pushing per-event would give. | If memory ever bites, thread the sink channel (or a callback) into `ProcessLedger` so events stream out as decoded instead of buffering a ledger's worth. Low priority — single-ledger bound is acceptable today. | low |
| low | sink.go:180-211 (`persistWorker` shutdown) | D4 | On `ctx.Done()` all 8 workers can still hit the `case ev,ok := <-in` arm (Go `select` picks randomly among ready cases), so a non-zero worker may consume from `in` concurrently with worker 0's `drainBufferedEvents`. The consume is still safe (channel receive is atomic; each event is handled once) and worker-0's drain just sees fewer items. | Not a correctness bug — no double-write, no drop. But the "only worker 0 drains" comment slightly overstates the guarantee; the real invariant is "every buffered event is handled exactly once across all workers," which holds. Worth a one-line comment correction so a future reader doesn't assume worker 0 sees the complete set. | Clarify the comment, or have non-zero workers `return` immediately on `ctx.Done()` without re-entering the channel arm (drop the `select` race) — cosmetic. | low |
| low | sink.go:269-290 (`IsProjectedEvent`) + projected_test.go | D2 | `soroswap_router.Event` is handled only by the `default: return false` arm and is NOT in the table-driven `projected_test.go` cases, unlike every other non-projected type (sdex/external/band ARE pinned). The lockstep contract with `projector/registry.go` is asserted by test only for the types explicitly listed. | If someone later adds `soroswap_router` to the projector, the missing test case means the projected/non-projected split could drift without a failing unit test (the ADR-0030 import lint is the only backstop). Pure test-coverage gap, no live bug. | Add `{"soroswap_router.Event", soroswap_router.Event{}, false}` to the projected_test table. | low |

---

## Verified CORRECT (provable coverage)

These were specifically checked and found sound — recording them so the next
pass doesn't re-litigate.

**D2 — ADR invariants (all hold):**
- No `stellarrpc` import and no `rpc.GetEvents` / `BackfillRange` / `StreamLive`
  call anywhere in the four in-scope production packages (grep clean; only the
  doc comment in `ledgerstream.go:10` mentions the lint rule). Ingest is purely
  `Galexie → ledgerstream → dispatcher → decoder`.
- The four decoder seams are present, symmetric, and each honours the
  first-match-wins + non-fatal-error contract: `Decoder`/`dispatchOne`,
  `OpDecoder`/`dispatchOp`, `ContractCallDecoder`/`dispatchContractCall`,
  `LedgerEntryChangeDecoder`/`dispatchEntryChange` (dispatcher.go:75-205,
  798-863). Each bumps `eventsSeen` pre-Decode and `decodeErrors` on failure.
- Sink type-switch trap covered: `HandleEvent` (sink.go:413-590) has a case for
  every `consumer.Event` type the registered sources emit; the `default` arm
  counts+logs `unhandled` rather than silently dropping. `tradeFromEvent`
  (sink.go:240-257) is kept in lockstep with the `persistTrade` cases.
  `IsProjectedEvent` is pinned by `projected_test.go` against the projector
  registry (one minor omission noted above).
- i128/ADR-0003: no `int64(...Lo)` truncation in scope. All amount fields the
  sink touches are stringified via `.String()` off `*big.Int`-backed
  `canonical.Amount` (e.g. sink.go:477-478, 507, 536).

**D1 — Correctness (the high-risk attribution paths):**
- **op_index / event_index attribution is correct across BOTH meta versions.**
  Verified against the SDK `GetTransactionEvents` (ledger_transaction.go:278-313):
  - V3: `OperationEvents` is always length-1 with events at index 0; the Stellar
    protocol guarantees a Soroban tx has exactly one operation at envelope index
    0 (SDK comment ledger_transaction.go:273). So the dispatcher's `opIdx=0`
    event-loop index, the stamped `OperationIndex=0`, and `invokeCalls[0]` all
    line up. **No off-by-one / misattribution.**
  - V4: `OperationEvents` is sized `len(Operations)` and indexed 1:1 with
    operations, so `opIdx` + `invokeCalls[opIdx]` + per-op source align (dispatcher.go:580-597).
  - `evIdx` is plumbed into `EventIndex` so the `(ledger, tx_hash, op_index,
    event_index)` PK is unique per multi-event op (dispatcher.go:586-587,
    948-1001) — matches ADR-0033.
- Census (census.go) mirrors the dispatch eligibility rules exactly
  (successful-tx gate, `captureEligible` == `contractEventToEventsEvent` gate,
  `claimAtomCount`/`realTradeCount` both-zero drop == `sdex.decodeClaimAtom`) so
  the completeness reconcile compares like-for-like; `TxReadErrors` /
  `TxEventReadErrors` correctly make the census decline an authoritative row
  when it couldn't fully read a tx (G15-06).
- `tx.Result.Successful()` gates both dispatch and census; failed txs are skipped
  before any decode (dispatcher.go:541, census.go:92).
- `ExtractContractCallTree` (contract_call_export.go) is byte-identical to the
  live `extractInvokeContractCallTrees` (re-uses it), so the off-line re-derive
  routes the same calls the live dispatcher does.
- The auth-tree walk (`walkAuthTree` / `extractInvokeContractCallTrees`) copies
  `path` defensively per node (dispatcher.go:1062-1066, 1092-1097) — no slice-aliasing
  bug across recursion; non-auth fallback to top-level matches the pre-#48 baseline.
- `Must*()` uses are all guarded: `MustContractId` only after a
  `ScAddressTypeScAddressTypeContract` switch arm; `MustV3/MustV4` only after a
  `switch tx.UnsafeMeta.V` (dispatcher.go:728); the claimAtom `Must*` calls in
  census are all behind a matching `Code == ...Success` check.
- `contractEventToEventsEvent` defensively returns `nil` (not panic) for every
  malformed shape (non-contract type, nil ContractId, body V!=0, marshal error,
  strkey error) — dispatcher.go:948-1001.

**D4 — Concurrency:**
- `statsMu` correctly guards all reads+writes of `eventsSeen` / `decodeErrors` /
  `unmatchedHits` / `txReadErrors` / `txEventReadErrors`; `Stats()` snapshots
  under lock then does the orphan-reporter walk outside it (decoders set once at
  startup) — dispatcher.go:445-485, 773-794. F-1317 regression guard
  (`stats_race_test.go`) exercises it under `-race`.
- `ProcessLedger` is documented + used as serialized-per-caller; the
  ledgerstream callback model serializes it. No concurrent-ProcessLedger path
  exists.
- statsflush uses snapshot-and-delta (not snapshot-and-clear), copies maps on
  every tick (flusher.go:184-193), does a final flush on ctx cancel.

**D5 — Resource / per-tx tolerance:**
- One bad tx never aborts the ledger: `reader.Read` error → `txReadErrors++` +
  continue (dispatcher.go:530-540); `GetTransactionEvents` error →
  `txEventReadErrors++` + continue with the rest of the tx (dispatcher.go:570-579).
- `PersistEvents` shutdown drains via a FRESH bounded context (F-1318,
  sink.go:171-178, 303-305) so the parent-cancel doesn't instakill the final
  flush; undrained remainder is logged at ERROR with the recoverable ledger range
  (sink.go:339-388) rather than dropped silently (G15-08). `drainTimeout`=90s.
- `RawEventSink.PushEvent` is intentionally blocking (back-pressure) and
  `DiscoverySink.Push` is contractually non-blocking — both documented and the
  dispatcher honours the split (dispatcher.go:902-916).
- ledgerstream cold-tier init failure degrades to hot-only with a WARN rather
  than aborting (ledgerstream.go:352-392); `TieredDataStore` fails-loud on
  transient hot errors and only falls through to cold on `IsNotFound`
  (tiered.go:120-150). Single-ledger bounded range handled via `streamHot`/
  `walkDataStore` since the SDK rejects `To()==From()` (ledgerstream.go:215-222,
  validate_range_internal_test.go).

**D9 — Dead seam (`consumer.Orchestrator` / `consumer.Source`):**
- **Confirmed dead and safe.** Zero production callers of `consumer.New`,
  `consumer.Orchestrator`, `.StreamLive`, `.BackfillRange`, or `consumer.Source`
  outside the package itself and its own `_test.go` (grep clean). The
  `BackfillRange`/`StreamLive` methods on the legacy `Source` interface — which
  CLAUDE.md rule 6 would otherwise flag as "wrong" — exist ONLY on this dead
  interface; no on-chain source implements it (sources register dispatcher seams
  instead). `doc.go` accurately documents it as pre-2026-04-23 legacy retained
  as a reference shape. The orchestrator itself is internally sound (per-source
  recover→backoff, cursor advance-only floor, final-flush on cancel via detached
  ctx). No action needed beyond leaving it dead; could be deleted but that is a
  cleanup, not a finding.

**`walkEntryChanges` correctness (the canonical pattern):** the fee-block→
TxChangesBefore→Operations→TxChangesAfter ordering is correct, opIdx is -1 for
fee/tx-level blocks and the real op index for operation changes, and there is no
double-walk of fee changes (FeeChanges and TxChanges* are disjoint SDK fields).
The ONE gap is the omitted `PostTxApplyFeeChanges` (medium finding above).

---

## Severity counts

- critical: 0
- high: 0
- medium: 2
- low: 4

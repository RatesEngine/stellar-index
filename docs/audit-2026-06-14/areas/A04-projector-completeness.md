# A04 — Projector + completeness (READ-ONLY audit)

**Date:** 2026-06-14
**Scope:** every `.go` (incl. `*_test.go`) under
`internal/projector/`, `internal/completeness/`,
`internal/archivecompleteness/`, `internal/hashdb/`.
**Method:** read all 19 in-scope files plus the load-bearing callers
and dependencies they bind to (`internal/pipeline/sink.go`
`IsProjectedEvent`, `cmd/stellarindex-ops/compute_completeness.go`,
`internal/sources/soroswap/{decode,dispatcher_adapter,factory_seed}.go`,
`internal/pipeline/soroswap_registry.go`,
`internal/storage/timescale/soroban_events.go`,
`internal/storage/clickhouse/{event_reader,completeness}.go`,
`internal/storage/clickhouse/tier1_schema.sql`,
`cmd/stellarindex-indexer/main.go` cursor write path). No source
edited, no git mutated.

**Files read (in-scope, fully):** 19
- projector: `projector.go`, `registry.go`, `projector_test.go` (3)
- completeness: `watermark.go`, `recognition.go`, `reconcile.go`,
  `watermark_test.go`, `recognition_test.go`, `reconcile_test.go` (6)
- archivecompleteness: `doc.go`, `cross_anchor.go`,
  `cross_anchor_fill.go`, `report.go`, `metrics.go`,
  `cross_anchor_test.go`, `cross_anchor_fill_test.go`,
  `metrics_test.go` (8)
- hashdb: `hashdb.go`, `hashdb_test.go` (2)

**Supporting files read (invariant / caller checks, not audit targets):** ~10

---

## Severity counts

| Severity | Count |
|---|---|
| Critical | 0 |
| High | 1 |
| Medium | 3 |
| Low / Info | 6 |

**High inline:** A04-H1 (projector off-by-one at the durable tip —
the highest durable ledger is never projected until a newer one
arrives; permanent loss of the final ledger if ingest halts).

No Critical findings. The headline ADR-0033 three-claim verdict
math, the one-writer registry↔sink sync, and the firehose-exclusion
losslessness all verified correct.

---

## Findings

| Sev | file:line | dim | issue | why it matters | fix | conf |
|---|---|---|---|---|---|---|
| **High** | projector/projector.go:253 | D1 | Idle guard `if tip <= fromLedger` skips the tip ledger. `fromLedger = cursor.LastLedger+1`; the ledgerstream cursor (`indexer/main.go:1354`) is upserted to `lcm.LedgerSequence()` *after* the ledger is durably applied, so `tip` is itself a fully-committed, processable ledger. When `tip == fromLedger` the cycle goes idle and never scans ledger `tip`. | In live steady state the projector is structurally ALWAYS exactly one ledger behind the durable tip (ledger N is only projected once tip advances to N+1). If ingest halts at ledger T, T's events are never projected — a permanent served-tier hole at the watermark, directly against ADR-0033's "100% to tip" headline. The `ProjectorLagLedgers` gauge (`tip-toLedger`) reads 0 next cycle and masks it. | Idle guard should be `if tip < fromLedger`; process `[fromLedger, tip]` inclusive whenever `tip >= fromLedger`. (The reconcile re-derive would catch the resulting served-tier shortfall, so this is High not Critical — it's caught downstream, just not prevented.) | High |
| **Med** | projector/projector.go:223,285,294-323 | D4 | Per-cycle `cycleCtx` (60s `PerSourceTimeout`) wraps the *whole* read-decode-sink loop, and the sink (`p.sink(cycleCtx, out)`) shares it. If one batch's decode+sink work persistently exceeds 60s, the stream returns ctx-cancelled, the `err != nil` branch (324) leaves the cursor un-advanced, and the next cycle retries the SAME window → livelock — the same failure CLASS as the prior aquarius firehose livelock (commit 98135ac2), now reachable via a slow downstream sink rather than a wide scan. | A source can wedge permanently with no progress and no distinct signal beyond `ProjectorRunsTotal{outcome="error"}` ticking. BatchLimit=10k bounds scan rows but not per-row sink latency, so a degraded Postgres/CH write path reintroduces the livelock. | Either shrink the batch on repeated timeout (adaptive backoff), or advance the cursor to `lastSeenLedger` on partial progress before the deadline, or split the read budget from the sink budget. At minimum add a "consecutive-timeout" counter + alert so a wedged source is visible. | Med |
| **Med** | projector/projector.go:334,330-340 | D1 | Cursor advances to `toLedger` (the scan UPPER BOUND) not `lastSeenLedger`, AND on the no-FINAL CH read (295) a partial stream that errors mid-way (e.g. ctx deadline at 324) leaves the cursor untouched — good — but a stream that returns `nil` after the sink soft-fails (sink is fire-and-forget `SinkFunc`, no error returned, line 84/285) advances the cursor PAST events whose downstream write failed. The godoc (78-83) claims "does not advance the cursor for that row, retries next cycle," but `SinkFunc` returns nothing, so the projector cannot know a write failed. | The documented retry-on-sink-failure safety property does not hold: a transient downstream write error silently drops the event and the cursor moves on. Idempotent `ON CONFLICT DO NOTHING` only saves the case where the write later succeeds on a re-read — but there is no re-read; the cursor passed it. Relies entirely on the completeness reconcile to detect the drop after the fact. | Make `SinkFunc` return `error`; on sink error, stop the cycle without advancing (mirror the stream-error branch). Or document honestly that sink failures are detected only by the offline reconcile, not retried. | Med |
| **Med** | archivecompleteness/cross_anchor.go:149-167 | D1 | `alignLastCheckpoint` has dead/confused control flow: the condition `rem >= 63 \|\| rem < 63` (line 154) is a tautology (always true for `% 64`), the trailing `return 0` (166) is unreachable, and the comment block (155-157) describes two "equivalent forms" without committing. The function is functionally correct for the tested cases (`to-rem-1` gives the prior checkpoint) but the logic is unauditable as written. | Not a live bug — tests pass and the arithmetic is right — but it is exactly the kind of "looks-like-a-one-liner" code that hides an off-by-one on a future edit, in a function whose whole job is checkpoint-boundary alignment for archive completeness. A misalignment here silently under/over-counts `Expected`, corrupting the missing-file verdict. | Collapse to a single clear expression: `if to < 63 { return 0 (sentinel) }; if to%64==63 { return to }; return to - to%64 - 1`. Remove the tautology and the dead `return 0`. Add a test at `to=63` exactly and `to=64`. | Med |
| Low | projector/projector.go:340 vs :256 | D1 | `ProjectorLagLedgers` is set to `tip-toLedger` on a successful cycle but `tip-toLedger` is the residual AFTER the batch cap, not true lag from the cursor. On a multi-batch catch-up the gauge reflects "rows left after this batch" which is correct-ish, but combined with H1's tip-skip the gauge reads 0 even though the source is permanently 1 ledger behind. | Operators reading `projector_lag_ledgers==0` will believe the source is caught up to tip when it is structurally one ledger behind (H1). | Fix H1; lag becomes honest. Optionally also emit a `cursor_last_ledger` gauge so lag is derivable from first principles. | Med |
| Low | completeness/watermark.go:32-41 | D1 | Degenerate `tip < genesis` returns `Watermark{Complete:false, CoveragePct:0}` and sets `Ledger=genesis-1`. An empty range (no ledgers to verify) is arguably "vacuously complete," but the function reports incomplete. | A source whose genesis is ahead of the current tip (brand-new protocol, e.g. CCTP/Rozo per memory `project_protocol_coverage_additions`) would show `complete=false coverage=0` purely because no ledgers exist yet — a false "not covered" signal. | Decide + document the empty-range convention; if "vacuously complete" is intended, return `Complete:true, CoveragePct:1` for `tip < genesis`. Add a test for the `tip < genesis` case (currently untested). | Med |
| Low | completeness/reconcile.go:67,105-150 | D2/D1 | The reconcile (`ReDeriveOutputCounts*`) passes `nil` for `excludeTopic0Syms` while the projector (registry.go) passes `firehoseExcludeSyms`. So the projector excludes the 6 CAP-67 symbols at SQL; the reconcile relies on `dec.Matches()` to filter the firehose instead. This is intentional and SAFE (Matches is a superset filter; an over-exclusion by the projector would surface as a reconcile mismatch), but the asymmetry is undocumented at the reconcile call site. | If a decoder's `Matches` ever started matching one of the 6 excluded symbols, the projector would silently drop it and only the reconcile would notice — a latent foot-gun if the exclude-list audit (registry.go:79-92) drifts from a decoder change. | Add a one-line comment on the reconcile streamers noting the deliberate asymmetry, and consider a unit test asserting no in-scope decoder's `Matches` returns true for any symbol in `firehoseExcludeSyms`. | Med |
| Low | archivecompleteness/cross_anchor_fill.go:223 | D4 | Each Fill worker seeds its own `math/rand` from `time.Now().UnixNano() + workerID`. Two workers starting in the same nanosecond (plausible under the goroutine launch loop) with adjacent IDs can produce correlated shuffles, slightly defeating the load-spread intent. | Cosmetic — load-spread across SDF/validator sources, not correctness or security (already `//nolint:gosec`). Worst case is mild upstream-source skew during a repair burst. | Use a single shared `rand.Rand` guarded by the existing `mu`, or `math/rand/v2`'s lock-free top-level funcs. Low priority. | High |
| Low | archivecompleteness/metrics.go:35-38,232-244 | D1 | `RepairAttempts`/`RepairFailures` are documented as "per-source" but `PopulateFromFillResult` records ALL failures under one synthetic `multi-source-exhausted` label (because `FillResult` carries no per-source failure breakdown). The metric's `source` label is therefore success-only-granular; failures are aggregated. | An operator charting `repair_failures_total{source=...}` to find which upstream is down gets no per-source signal — every failure is `multi-source-exhausted`. The alert rule keys on this label, so it works, but the diagnostic value is lower than the field name implies. | Either rename the label semantics in the godoc, or extend `FillFailure` to carry the per-source last-error map so failures attribute correctly. | Med |
| Info | hashdb/hashdb.go (whole pkg) | D9 | Confirmed ZERO production callers. `grep -rn internal/hashdb` across `cmd/` + `internal/` (excluding the package itself + tests) returns nothing; only docs reference it. ADR-0033 itself (line 14) calls it "an unwired library." The package is internally sound: fixed-size dense records, exclusive create, magic+version header guards, all-zero sentinel = ErrMissing, drift detection tested. The "ADR-0033 feeder" role is aspirational per CLAUDE.md. | Dead-but-safe seam. Not a bug, but a maintenance liability: a complete, tested, documented package with no caller invites accidental "wiring it up wrong" later, and it carries the same status as the noted `consumer.Orchestrator` dead seam. | No action required. If it stays unwired through v1, consider a `//go:build` exclusion or an explicit "UNWIRED" banner in `doc.go` matching CLAUDE.md's `hashdb` note so a future agent doesn't assume it's live. | High |
| Info | completeness/reconcile.go:105-150 (ReDerive...FromEvents) | D5 | The CH-backed reconcile streams the ENTIRE `[lo, hi]` range in one call and accumulates `map[string]map[uint32]int`. Memory is O(distinct ledgers-with-events × kinds), NOT O(events) — the inner map is a per-ledger COUNTER, so a 60M-ledger sweep holds at most ~tens of millions of small int entries, far under the prior 12 GiB substrate-scan OOM. The adjacent-duplicate skip (113-129) keeps the read no-FINAL + gentle. This is the windowing-by-aggregation defense and it holds. | Verifies the D5 OOM concern is addressed for the reconcile path (the substrate scan itself is delegated to storage `FindLedgerIngestGaps`/`ContiguousWatermark`, out of this package). | None — documented here as a positive verification. The caller (`compute_completeness.go`) also does incremental `[from, srW.Ledger]` windowing on top. | High |

---

## CORRECT (verified, no action)

1. **ADR-0033 three-claim watermark math is sound.** `ComputeWatermark`
   (watermark.go) correctly reduces problem-ledgers to "one below the
   earliest problem ≥ genesis," clamps coverage to [0,1], handles
   problem-at-genesis (→ coverage 0, Ledger=genesis-1 sentinel),
   ignores out-of-range problems, and is pure/deterministic. The test
   matrix covers no-problems / mid-range / earliest-of-several /
   out-of-range / at-genesis. (Gap: empty-range `tip<genesis` untested
   — see Low above.)

2. **One-writer invariant (D2) holds: registry↔sink are in sync.**
   Every source `buildSource` (registry.go) can register —
   soroswap, aquarius, phoenix, comet, blend, cctp, rozo, defindex,
   sep41_transfers, sep41_supply, reflector{DEX,CEX,FX}, redstone —
   has every event type it emits classified `true` in
   `IsProjectedEvent` (sink.go:269). The `projected_test.go`
   table-driven test pins this both directions (projected sources →
   true; sdex/external/band → false). No drift found.

3. **Recognition audit (Claim 2a) is correct.** `AuditRecognition`
   reports both unrecognized shapes AND unreconstructable samples as
   gaps (can't rebuild ⇒ can't claim to decode). Reconstruct-failure
   path tested via empty-ContractID row.

4. **Reconcile multi-table by-kind split (Claim 2b) is correct.**
   `ReDeriveOutputCountsByKind` + `SumKinds` correctly avoid the
   overcount where one decoder routes multiple kinds to different
   tables (soroswap trade+skim, blend's 5 kinds / 4 tables). Tested.
   `ReconcileCounts` catches both drops (expected>actual) AND phantoms
   (actual>expected), sorted. Tested.

5. **CH reconcile dedup is correct + necessary.** The adjacent-identity
   skip (reconcile.go:113-129) dedups un-merged ReplacingMergeTree
   parts on the no-FINAL read; the COUNTING reconcile separately uses
   `useFinal=true` via `StreamContractEventsFiltered` (event_reader.go:86-92)
   where exactness matters. The live projector correctly uses
   `useFinal=false` (idempotent writes absorb dups). The two
   consumers' FINAL choice is deliberate and matches the documented
   rationale.

6. **Firehose exclusion is provably lossless (D2).** `firehoseExcludeSyms`
   = {transfer, mint, burn, clawback, approve, set_authorized} —
   set_admin deliberately retained because blend dispatches on it.
   Verified exact-match `NOT IN` in both Postgres
   (soroban_events.go:175, with `topic_0_sym IS NULL OR` guard) and CH
   (event_reader.go:108; `topic_0_sym` is non-nullable `String` in
   tier1_schema.sql:110 so `'' NOT IN (…)` = keep — no NULL-drop
   asymmetry between the two read paths). Spot-checked cctp
   (`mint_and_withdraw`/`deposit_for_burn` ≠ bare `mint`/`burn`),
   soroswap (matches only `SoroswapPair`/`SoroswapFactory` topic[0]),
   aquarius/phoenix/comet/rozo/defindex — none consume any excluded
   symbol as topic[0]. The aquarius livelock (memory) is fixed by this
   exclusion.

7. **soroswap RPC-seed fragility (D1) — production path does NOT use
   RPC.** Both the dispatcher and the projector seed the soroswap
   pair registry from the DB (`SoroswapPersistenceOptions` →
   `LoadSoroswapPairRegistry`) plus the live `new_pair` hook;
   `SeedFromFactoryRPC` (factory_seed.go) is a one-time bootstrap
   operator tool only. CLAUDE.md invariant #6 (no RPC in ingest) is
   honored. The real dependency is registry COMPLETENESS, not RPC:
   `Decoder.Matches` gates pair events on registry membership
   (dispatcher_adapter.go:166) and an un-seeded real pair has its
   swaps dropped + counted via `SkippedUnknownPair` (line 249,272).
   This is a documented hard precondition (seed-soroswap-pairs genesis
   walk), and critically the SAME decoder + SAME registry drive the
   completeness reconcile, so a registry hole shows up as a
   recognition/projection signal rather than silent divergence.

8. **soroswap stateful-decoder buffer survives correctly across
   projector cycles + reconcile windows (D4).** The Decoder (and its
   swap+sync correlation `buffer`) is constructed ONCE at
   BuildRegistry time and reused for the projector's lifetime, so the
   buffer persists across 5s cycles. Correlation groups share a single
   (ledger, tx, op) and complete within one ledger (per the reconcile
   godoc), so batch/window boundaries at ledger granularity never split
   a swap from its sync. The reconcile likewise reuses one decoder over
   the whole range. No cross-window split bug.

9. **resolveTip CH watermark clamp is correct (D1).** In CH
   feed-switch mode the tip is clamped to `ContiguousWatermark(from)`
   so the source STALLS at the first lake hole rather than advancing
   the cursor past it (which would silently lose that ledger, since the
   cursor advances to `toLedger` unconditionally). `ContiguousWatermark`
   (clickhouse/completeness.go) correctly computes the first missing
   ledger ≥ from via `leadInFrame` and normalizes NULL/empty to 0.

10. **hashdb on-disk format is robust.** Magic+version header, exclusive
    create (no silent truncate), dense fixed-size records, all-zero =
    ErrMissing sentinel, ErrOutOfRange below startLedger, drift vs
    missing distinguished. Comprehensive test coverage
    (round-trip, verify-ok, drift, missing, sparse, out-of-range,
    bad-magic, bad-version, exclusive-fails).

11. **archivecompleteness fill path is safe.** Atomic `.new`→rename
    placement, gzip validation (zip-bomb bounded at 4 MiB decompressed
    + 16 MiB compressed), empty-body rejection, ctx-cancellation
    between checkpoints, MkdirAll-before-GET (the 2026-04-28
    bash-script bug). Metrics textfile uses the node_exporter atomic
    `.tmp`→rename protocol with stable sorted-key ordering. Well tested.

---

## Notes for the register

- **A04-H1 is the only finding that touches the ADR-0033 "100% to
  tip" guarantee directly.** It is a structural off-by-one, not an
  edge case: every source is permanently one durable-ledger behind
  tip in steady state, and the final ledger is lost forever if ingest
  ever stops. It is mitigated (not prevented) by the completeness
  reconcile catching the served-tier shortfall, which is why it is
  graded High rather than Critical.

- **A04-M (sink fire-and-forget)** undermines the projector's
  documented retry-on-failure property; pair it with H1 when scoping a
  projector-correctness remediation — both are about the cursor
  advancing past work that wasn't durably done.

- **hashdb + (noted) `consumer.Orchestrator`** are the two dead seams
  in this area; both verified safe-because-unwired. Recommend the same
  treatment (explicit UNWIRED banner) if they survive to v1.

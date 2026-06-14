# A20 — Test suites + harnesses (READ-ONLY audit)

Scope: `test/**` — `test/integration/` (Go, build tag `integration`,
testcontainers-go), `test/load/` (k6 JS scenarios + lib), `test/chaos/`
(bash failure-injection), `test/fixtures/` (golden frames). Plus a
meta-assessment of test COVERAGE across the repo: which critical paths
lack tests, and test ROT (tests asserting stale/wrong behaviour vs current
ADRs).

Auditor pass date: 2026-06-14. Primary dimension D8 (test-suite
correctness), secondary D1 (do the harnesses themselves have bugs).

**Files read: 49 / ~104.**
- 30 / 30 `test/integration/*.go` (all 29 `*_test.go` + `doc.go`).
- 9 / 9 k6 scenarios + 5 / 5 `scenarios/lib/*.js` + `README.md` + `doc.go`.
- 5 / 5 chaos files (`run.sh`, 4 scenarios) + `lib/common.sh` + `README.md`
  + `doc.go` + the launch-cut RETRO.
- Golden fixtures spot-read (soroswap swap, reflector update) + traced to
  their loaders.
- Cross-checked against source-package tests to locate coverage gaps:
  `internal/storage/clickhouse/*_test.go` (5), `internal/api/v1/explorer_*_test.go`
  (2), `internal/api/v1/changes*_test.go` (2), `internal/aggregate/*_test.go` (7),
  `internal/completeness/*_test.go` (3), `internal/projector/projector_test.go`,
  the source-package `real_fixture_test.go` set, and the alert-rule files
  (`configs/prometheus/rules.r1/api.yml`, `deploy/monitoring/rules/api.yml`).

---

## Findings

| severity | file:line | dim | issue | why it matters | suggested fix | conf |
|---|---|---|---|---|---|---|
| high | test/load/scenarios/lib/alertmanager.js:21 | D1 | The default silence matchers are `alertname=APIHighLatencyP95,alertname=APIHighErrorRate`. The actual deployed alert names are `stellarindex_api_latency_p95_high` and `stellarindex_api_error_rate_high`/`_critical` (confirmed in `configs/prometheus/rules.r1/api.yml:24` + `deploy/monitoring/rules/api.yml:36`). The matcher names match NO alert. | The whole point of the 99-spike silence (this lib's only caller) is to stop on-call being paged for the planned 10× burst. With wrong matcher names the silence is a no-op even when `ALERTMANAGER_URL` is set: the spike trips `stellarindex_api_latency_p95_high`, AlertManager has no matching silence, on-call pages. The scenario's own comment (99-spike.js:14-16) and this file's header both assert the silence works. | Change the default matchers to the real alertnames (`stellarindex_api_latency_p95_high`, `stellarindex_api_error_rate_high`). Better: a CI lint that diffs the lib's matcher set against the `alert:` names in the rules dir, like the runbook-presence lints. | high |
| medium | test/integration/ledgerstream_to_storage_test.go:532-536 (`runIngest`) | D1 | A `dispatcher.ProcessLedger` error is downgraded to `t.Logf(...); return nil` instead of `t.Errorf`/`t.Fatal`. The dispatcher ALSO swallows per-tx read errors internally (bumps `txReadErrors` + `continue`). So a fixture that stops matching after an SDK bump, or a per-tx decode regression, degrades to "got 0 events, want 1" with the real error only logged — the load-bearing end-to-end ingest test for Reflector/Redstone/Comet/Band silently hides the cause of a failure. Every subtest here inherits the blind spot. | This is the single most load-bearing harness weakness in the integration suite: the test that proves Galexie→ledgerstream→dispatcher→sink still works will report a symptom, not a cause, exactly when a real regression lands. | `t.Errorf`/`t.Fatalf` on the `ProcessLedger` error (keep the count assertions too). Consider failing on `txReadErrors > 0` in the test harness. | med |
| medium | test/integration/external_fleet_test.go:145-165 | D1 | The persist goroutine logs `InsertTrade`/`InsertOracleUpdate` failures via `t.Logf` + `continue` (lines 151, 157) and `default: t.Logf("unhandled event %T")` (162). Assertions are lower-bounds only (`< 2`, `< 5`; lines 183-191) and the comment expects 6 but accepts 5. A schema/NUMERIC-scale drift or one whole poller emitting nothing (e.g. ECB → 0) can still pass. Synchronisation is a hard-coded `time.Sleep(2s)` (line 170) — flaky on a loaded box. | The CEX/FX fleet (external sources bypass the dispatcher) is exercised end-to-end ONLY here; a silent insert regression or a dropped venue would not fail this test. The documented FX 10^6-vs-10^8 decimal-scale foot-gun is never value-asserted (only `Sign()>0`), so a scaling regression passes. | `t.Errorf` on insert failure; assert exact per-venue counts where deterministic; assert one scaled value to pin the 10^6/10^8 decimals; replace the 2s sleep with a count-reached poll. | med |
| medium | test/integration/api_test.go:76-84, 712-729 | D1/D8 | Dead scaffolding for a sub-test that no longer exists: the comment + the `pools_per_source_1h` refresh + the `apiMarketsAdapter.AllPools` method reference an "AllPools sub-test" that is not present (no `t.Run` hits `/v1/pools` or calls `AllPools`). | Test rot: either coverage was deleted and the scaffolding left, or it was never written. A reader believes `/v1/pools` / `AllPools` is covered when it is not. | Delete the dead refresh + comment, or restore the sub-test. | med |
| medium | test/integration/doc.go:2 | D8 | Package doc lists `stellar-rpc` as an external dependency that integration tests require ("real external dependencies (Postgres, Redis, MinIO, stellar-rpc, …)"). stellar-rpc was removed from the ingest path (CLAUDE.md invariant 6 / r1 2026-04-23) and no integration test uses it — they use Postgres/Redis/MinIO via testcontainers. | Doc rot in the entry-point doc for the whole suite; contradicts a binding ADR invariant. A new agent reads this and assumes stellar-rpc is part of the test substrate. | Drop `stellar-rpc` from the dependency list. | high (that it's wrong) |
| medium | test/load/scenarios/lib/env.js:18-22 | D1 | `PROD_HOSTS` lists `'api.stellarindex.io'` TWICE (lines 19-20) and a stale `'rates.stellar.org'` (line 21, pre-rebrand domain). | The production-safety guard works (any one entry catches it), but the duplicate + stale host is sloppy and the stale entry suggests the guard list was never reviewed post-rebrand. If a real new prod host is added it may be missed. | De-dup; replace `rates.stellar.org` with the actual current prod host(s). | high |
| medium | test/integration/platform_postgres_stores_test.go:364 + ~10 sites | D1 | `_ = tokens.CreateInvite(...)` discards the error on the 2nd invite; the downstream `RevokeInvite`/re-`AcceptInvite` → `ErrNotFound` assertions (372-378) then pass for the WRONG reason (row never created vs. row revoked). Pervasive `got, _ := store.Get(...)` error-discard pattern (~10×, e.g. :475,:487,:500) then dereferences fields — a `(zero, err)` return compares against zero values and can spuriously pass/fail with a misleading message. | The platform store CRUD suite can green-light a broken invite/revoke flow because the negative assertion can't distinguish its two failure modes. | Check the `CreateInvite` error; replace `got, _ :=` with `got, err :=` + `t.Fatal`. | med |
| low | test/load/scenarios/05-streaming.js:30,45-71 | D1/D8 | (1) Comment line 30 says executor is "constant-VUs not ramping-arrival-rate" but the code uses `ramping-vus`. (2) The file doc + thresholds claim coverage of `/v1/price/stream` but the scenario only ever hits `/v1/observations/stream`. (3) "First event" is proxied by `r.timings.waiting > 0` (TTFB) — for an SSE stream that's the first byte, which may be the keepalive comment / headers, not a real price event; the `sse_first_event_ms` SLA is therefore measuring something looser than its name. | Doc/code drift + a soft metric: the SSE first-event SLA proof is weaker than advertised, and `/v1/price/stream` has no load coverage despite being listed. | Fix the comment; either add a `/v1/price/stream` arm or correct the doc; assert on an actual `data:` line, not raw TTFB. | med |
| low | test/chaos/scenarios/03-redis-network-partition.sh:30 + lib/common.sh:198 | D1 | `partition_container` runs pumba with a hardcoded `pause --duration 60s`, but `PARTITION_DURATION_SEC` defaults to 30 and the sample loop runs only `PARTITION_DURATION_SEC`. With pumba present the pause outlives the window (heal kills the PID early, fine at default). But if an operator raises `PARTITION_DURATION_SEC` past 60, pumba auto-heals mid-window and the loop measures a HEALED Redis as "during partition" → false pass. README pass-criterion ("during 30s partition") is coupled to the hardcoded 60. | Latent harness bug keyed to an env var the README invites tuning. The two durations should be one knob. | Pass `--duration ${PARTITION_DURATION_SEC}s` to pumba (or `(PARTITION+buffer)s`). | med |
| low | test/load/scenarios/99-spike.js + README "Scenarios" row | D1 | The README + the scenario header both state the pass criterion "recovery to baseline p95 within 2 min of spike end", but the scenario's `thresholds` (`sla.spike`) assert ONLY `http_req_failed: rate<0.005`. Recovery latency is never machine-checked — it's left to an out-of-band runbook read. | A stated, contractual-sounding pass criterion is not actually enforced by the gate; a regression in post-spike recovery would not fail the run. | Either add a windowed recovery threshold (e.g. a tagged Trend over the 2m recovery stage) or reword the README to mark recovery as operator-verified, not gated. | low |
| low | test/integration/soroban_events_storage_test.go:40-99,127-128 | D1 | Count-only test: every synthetic row has a unique `ledger`, so (a) no row is ever read back to verify the topic/body/op_args BYTEA columns round-trip, and (b) the "idempotent re-insert" assertion never actually hits the ON CONFLICT path because no two rows share the PK. Plus a dead `var _ = hex.EncodeToString` import-keep (127-128). | The ADR-0029 landing-zone test proves cardinality and INSERT-on-distinct-PK, NOT column fidelity nor real conflict resolution — the two things most likely to regress. | Read back ≥1 row and assert columns; add a genuine PK-colliding pair (same ledger+tx, distinct event_index) to exercise ON CONFLICT. | med |
| low | test/integration/storage_test.go:326-333, 397 | D8 | `TestCursorFirstLedgerBackfillMigration` re-runs an INLINE COPY of migration 0046's UPDATE SQL, and `TestCursorFirstLedgerMigrationReversible` hand-runs `ALTER TABLE … DROP COLUMN` rather than invoking the checked-in 0046 up/down scripts. | The tests assert a transcription of the migration, not the migration. If the real `.up.sql`/`.down.sql` drifts (extra index/constraint, different WHERE), the test passes while production diverges. | Drive the actual migration via the migrator (`Steps(±1)`), not an inline copy. | med |
| low | test/integration/issuers_coins_storage_test.go:187 | D1 | Asserts `err != sql.ErrNoRows` (direct `!=`) where the comment says callers use `errors.Is`. Passes today only because `GetIssuer` returns the raw sentinel; breaks the moment the error is wrapped with `%w`. | Brittle assertion that contradicts its own comment and the package's error contract. | Use `!errors.Is(err, sql.ErrNoRows)`. | high |
| low | test/integration/fx_quote_at_or_before_test.go:147 | D8 | `want := []string{"exchangeratesapi","polygon-forex"}` pins `FXSources()` to exactly two. CLAUDE.md lists `ecb` as an FX poller too; if a third FX source is registered this golden breaks. | Latent rot tied to a hardcoded 2-element set that the architecture says can grow. | Assert membership/superset, not exact equality, or document the intentional pin. | med |
| low | test/integration/classic_supply_storage_test.go (~15 sites) | D1 | ~15 `got, _ =` / `balA, _ =` calls discard the error from `Sum*AtOrBefore`; only the first call per subtest checks it. | A SQL regression that errors (rather than returning a wrong value) on a later call slips through silently. | Capture + assert the error on each call. | low |
| low | test/integration/api_registry_cursors_test.go:154-159 | D1 | The `lag_seconds` loop asserts only `>= 0` — a near-tautology (any non-negative passes; the comment admits "not an exact value"). No upper/finite bound, so a broken lag computation returning a huge value passes. | The cursor-lag diagnostic is effectively unasserted. | Add a sane upper bound (e.g. < a day) or a finiteness check. | low |
| low | test/integration/assets_test.go:206 | D1 | `mustSorobanTest` is defined but called nowhere in the package (grep clean). Dead helper. | Dead test code; reads as if a Soroban-asset case exists when it doesn't. | Remove, or add the missing case it implies. | low |
| low | test/integration/decoders_to_storage_test.go:257-259, 260-273 | D1 | (1) `if got == nil { t.Fatal("row not found") }` is unreachable — `t.Fatalf` already fired on `err != nil`, and the reader returns `ErrNotFound` (not nil,nil). (2) `insertAndVerifyOracle` never checks the `Observer` column, yet a subtest sets `Observer: ""` to prove the DEX case — the observer round-trip is unasserted in every oracle subtest. | One assertion that can never fire; one column the schema-bridge test claims to cover but doesn't. | Drop the dead nil-check; assert `Observer` in the verify helper. | low |
| low | test/integration/migrations_test.go:430-433 | D1 | `assertInsertRejected` accepts the error on substring `"23514"` OR `"check constraint"` OR `"violates check"` — it can't tell WHICH constraint fired, so a row rejected by a NOT-NULL/type error (instead of the intended CHECK) still "passes". | The CHECK-constraint guards (negative amount, op_index, decimals>38, confidence>1) are proven only loosely. | Assert the typed `pq.Error.Code == "23514"`. | low |
| low | test/chaos/scenarios/02-timescale-down.sh:79 vs README | D1 | The empty-payload guard greps for `"data":\s*[]` but `http_status` already passed `200`; the actual JSON envelope key is not re-verified against the handler shape. The launch-cut RETRO (2026-05-03) recorded `/v1/markets` returning 500 — so the "200 with empty data" branch was never actually exercised on a real run. | The most important assertion in the scenario (no silent-empty 200) is unproven against a live stack; only the 5xx branch has been observed. | Add a fixture-seeded run that forces the cache-hit branch so the empty-payload guard is actually exercised. | low |
| info | test/chaos/reports/2026-05-03-launch-cut/RETRO.md | D8 | The only committed chaos run (2026-05-03) ran 3 scenarios; `04-redis-misconf.sh` (added later, the F-0039 cascade guard) has NEVER been recorded as run. RETRO target is `localhost:8080`; the suite default is now `localhost:3000` (common.sh:25). | The newest + highest-value chaos scenario has no evidence of ever passing; minor host-port staleness in the retro. | Run the full 4-scenario suite and commit a fresh report. | high |

---

## Verified CORRECT (provable coverage)

Recorded so the next pass doesn't re-litigate.

**Build hygiene + structure:**
- All 29 `test/integration/*_test.go` carry `//go:build integration` (line 1).
  CI compiles them without Docker via `make test-integration-build` (the
  verify gate), so an interface change can't silently break the suite (F-1334).
- `test/load/doc.go` + `test/chaos/doc.go` are deliberate Go placeholders
  (not imported); the real scenarios are JS / bash. Correct.
- `run.sh` production-safety guard is duplicated in `common.sh::chaos_target_check`
  (defence-in-depth) and refuses `*production*` / `api.stellarindex.io` / `prod.*`.
- `http_status` correctly swallows curl's non-zero exit with `|| true` (NOT
  `|| echo 000`, which would double curl's own "000" — the comment at
  common.sh:104-111 documents the foot-gun and avoids it).

**D8 — architecture rot is effectively ABSENT (the headline good news):**
- **No i128→int64 truncation rot anywhere.** Every amount test uses
  `*big.Int` / `canonical.NewAmount` / NUMERIC `::text` round-trips. The
  ADR-0003 guards are present and CORRECT and assert the opposite of rot:
  `decoders_to_storage_test.go:113-131` (2^96-magnitude via `big.Int.Cmp`),
  `blend_money_market_storage_test.go:140-171` (~1.2e29 via `token_amount::text`),
  `blend…:369,379` (i128-in-jsonb asserted as STRINGS), plus large-i128
  round-trips in phoenix/comet/soroswap-skim/sep41 tests.
- **No 90-day trades-retention rot.** `migrations_test.go:186-197` asserts
  the retention policy is ABSENT on `trades` + `oracle_updates`
  (`assertPolicyAbsent`), citing migration 0031/0040 + ADR-0034 invariant 8.
  This is the de-rotted form (F-1334 flipped it from assert-attached) — exactly
  aligned with "raw history kept forever."
- **No `ledger_entry_changes`-is-empty rot, and no false PG dependency.**
  `ledger_entry_changes` is a CLICKHOUSE table (`deploy/clickhouse/tier1_schema.sql`,
  written by `internal/storage/clickhouse/{sink,extract}.go`), not a Postgres
  hypertable — so there is correctly no PG `ledger_entry_changes` to assert on,
  and no integration test asserts it empty. (Coverage gap noted below, but no rot.)
- **No cursor-derived-coverage rot.** The cursor tests (`storage_test.go`,
  `api_registry_cursors_test.go`) test the `ingestion_cursors` TABLE
  (legitimate persistence + monotonic-advance guard + `first_ledger` capture,
  migration 0046) — NOT cursor-derived coverage signal. ADR-0031's
  authoritative coverage = `completeness_snapshots`, exercised in
  `ledger_ingest_log_storage_test.go` (substrate / hash-chain / census).
- **No deleted-`*-backfill` and no stellarrpc-ingest references** in any
  `*_test.go` (the only `stellar-rpc` mention is the stale `doc.go` prose
  finding above + a stale comment in `soroswap/real_fixture_test.go:30`).

**Golden fixtures are valid + actually consumed:**
- `test/fixtures/{soroswap,phoenix,reflector,aquarius}/<wasm_hash>/*.json`
  carry real mainnet `contract_id` / `tx_hash` / base64 XDR `topics` + `value`
  + `wasm_hash` dir-versioning (contract-schema-evolution discipline). They ARE
  loaded — by the source-package `real_fixture_test.go` (UNIT) tests
  (`internal/sources/{soroswap,phoenix,reflector,aquarius}/real_fixture_test.go`)
  via `os.ReadDir(../../../test/fixtures/...)`, and by `dispatcher/routing_test.go`.
  Not orphaned. (One stale comment: soroswap loader line 30 says "stellar-rpc
  returns events in-order" — cosmetic, the pairing logic is sound.)

**k6 pairs-fixture rot was already FIXED (the fixture-bug class is closed):**
- `lib/pairs.js` is the de-rotted post-G22-01 form: every `asset`/`quote` is a
  `canonical.ParseAsset`-accepted shape (`native`/`crypto:`/`fiat:`/`<CODE>-<G>`),
  the AQUA issuer is the valid vanity strkey (the old `…M67AB6V` CRC-invalid
  issuer that 400'd every AQUA request is gone), and the CEX-quote-only
  `USDT/USD`+`USDC/USD` pairs that 404 the served tier were dropped. The doc
  comment (lines 35-41) explicitly explains the "pairs that 404 poison the
  error rate" trap and asserts every pair returns 200 on all four endpoints.
  Batch body is the correct `{asset_ids,quote}` shape; `/v1/history` reads
  `granularity=`. The committed proof run
  (`test/load/reports/2026-06-13/00-acceptance.json`) shows p95≈54ms,
  30600/30600 checks pass — consistent with the fixture being clean.

**Other strong tests worth keeping:**
- `asset_registry_replay_test.go` — F-1243 RowsAffected dedupe guard, exact
  observation-count assertions, real teardown. `freeze_events_test.go`,
  `source_entry_counts_test.go` (SET-not-ADD reconcile), `trade_insert_outcome_test.go`
  (delta-based metric assertions, robust to global-counter pollution),
  `ledger_ingest_log_storage_test.go` (ADR-0033 substrate, exact census golden),
  and the two platform concurrency-cap tests (`platform_…:782-939`, exact
  `ok==cap` / `quotaErrs==N-cap`) are all well-constructed.

---

## Critical paths LACKING test coverage

Ordered by risk. "Unit-only" = covered by a fast `*_test.go` but with NO
live-dependency (integration/testcontainer) round-trip; "none" = no test at all.

1. **ClickHouse lake write/read round-trip — UNIT-ONLY against no live server.**
   `internal/storage/clickhouse/*_test.go` (extract / entry-change / completeness /
   supply-flows / sink-buffer-cap) are PURE: they build rows from XDR
   (`entryChangeRow`, etc.) but never `sql.Open`/`Dial` a real ClickHouse —
   no testcontainers. There is **zero** integration coverage of:
   the CH sink write path, the `tier1_schema.sql` DDL applying cleanly, the
   `galexie → ClickHouse → decoders → Postgres` decode path, and the
   `ch-rebuild` lake-replay write path (the `-contract-calls` path for
   band + soroswap-router landed only days ago, commit 1e71bcab). Given
   ADR-0034 makes ClickHouse the certified raw lake / source of truth, this is
   the single biggest substrate gap. `test/integration/` is 100% TimescaleDB.

2. **Explorer endpoints — handler unit-tested with STUBS; reader + e2e untested.**
   `/v1/ledgers*`, `/v1/operations`, `/v1/tx/{hash}`, `/v1/accounts/{g}/transactions|operations`,
   `/v1/contracts/{id}`, `/v1/search`, `/v1/network/stats`. Handlers
   `explorer_ledgers` + `explorer_search` HAVE unit tests, but via a
   `stubExplorerReader` (`internal/api/v1/explorer_ledgers_test.go:13`) — the
   actual ClickHouse SQL in `internal/storage/clickhouse/explorer_reader.go`
   has **no test at all** (no `explorer_reader_test.go`). `explorer_accounts`,
   `explorer_contracts`, `explorer_operations`, `explorer_tx` handlers have NO
   dedicated unit test. No `test/integration/` test stands up `v1.Server` for
   any explorer route. The k6 catalogue scenario (07) and the chaos suite skip
   them entirely.

3. **Entry-change extract (the participant index) — reader untested end-to-end.**
   `entryChangeRow` row-building is unit-tested
   (`extract_entry_changes_test.go`), but the participant-index read path
   (`explorer_reader.go`, served by `explorer_accounts.go` / `explorer_operations.go`
   / `sep41_transfers.go`) has no integration test against a populated CH.
   Note the doc-rot hypothesis ("a test expecting empty ledger_entry_changes")
   does NOT exist — there is simply no test exercising the populated table.

4. **`/v1/changes/{type}/{id}` — unit-tested, no integration.**
   The change-summary handler HAS unit tests (`changes_test.go`,
   `changes_internal_test.go`) — contrary to a first-pass read — but there is
   no `test/integration/` HTTP round-trip and no test of the underlying
   `GetChangeSummary` reader against a real store.

5. **Aggregator compute → continuous-aggregate refresh — no end-to-end.**
   `internal/aggregate/*_test.go` thoroughly unit-test VWAP / TWAP /
   triangulation / stablecoin-map / outliers. But no integration test drives
   the `stellarindex-aggregator` compute → CAGG-write → `/v1/vwap` read chain;
   `api_test.go` only reads a manually-seeded CAGG. The
   stablecoin→fiat-proxy mapping and class-filter (only `ClassExchange`
   contributes) are unit-only — a wiring regression at the binary level
   wouldn't be caught.

6. **Soroswap/Phoenix swap+sync correlation buffer — no STORAGE integration.**
   The swap→sync reserve-correlation (the documented Soroswap trap) is
   covered by source-package UNIT tests (`real_fixture_test.go`) but the
   `ledgerstream_to_storage_test.go` file-doc explicitly promises an
   end-to-end correlation subtest that is still unimplemented. The flagship
   DEX has only single-event decoders exercised in the full ingest path.

7. **SSE streaming hub — load-tested loosely, no functional/chaos test.**
   `05-streaming.js` measures connection-accept TTFB (not real first-event),
   only against `/v1/observations/stream` (not `/v1/price/stream`). No
   integration test of the hub's last-event-id resume contract or
   mid-stream-kill reconnect (the chaos suite lists "API pod mid-stream kill"
   as Wave-2 deferred).

8. **Chaos Wave-2 (HA) entirely deferred + Wave-1 scenario 04 never run.**
   Patroni promotion, Sentinel failover, HAProxy VIP flip, MinIO node loss,
   SSE reconnect — all deferred to staging baremetal (none exist). The only
   committed chaos report (2026-05-03) predates `04-redis-misconf.sh`, so the
   F-0039 cascade guard has no recorded passing run.

9. **Webhook delivery (`internal/customerwebhook`) + notify — store-only.**
   `platform_postgres_stores_test.go` covers the WebhookStore CRUD + quota cap,
   but the actual HMAC-sign + POST + backoff/retry drain loop has no integration
   test (no mock receiver round-trip in `test/integration/`).

### Coverage gaps WITHIN existing integration tests (lower risk)
- `migrations_test.go` round-trips only 4 tables + the 7 price CAGGs; the many
  projected per-source hypertables (`blend_*`, `phoenix_*`, `comet_*`,
  `soroswap_skim`, `cctp_events`, `rozo_events`, `defindex_*`, `sep41_*`,
  reflector/redstone oracle tables) are not asserted to be created/dropped — a
  down-migration that forgets one wouldn't fail. Only `prices_1m` is proven to
  materialise a row; the other 6 CAGGs are existence-checked only.
- `storage_test.go` cursor monotonic-advance guard is single-threaded; the
  concurrent two-writer race it cites as motivation is not exercised.
- `/v1/markets` participant views (`SourceMarkets`/`AssetMarkets`/`PairMarket`/
  `GetPairsVolumeHistory24hBatch`) are adapter methods defined but unexercised
  (`api_test.go:655-710`).

---

## Summary

- **D8 (test-suite correctness) is in good shape:** the architecture-rot
  classes the audit was hunting (i128 truncation, 90-day retention,
  empty-`ledger_entry_changes`, cursor-derived coverage, dead `*-backfill` /
  stellarrpc) are **absent** from the integration suite — several are present
  in their explicitly-de-rotted, correct form. The k6 fixture-bug class
  (pairs that 404 poison the error rate) was already fixed (post-G22-01).
- **D1 (harness bugs) is where the real findings are.** The one HIGH is the
  k6 AlertManager-silence matchers not matching any deployed alert name (the
  spike scenario pages on-call despite "silencing"). The dominant medium-risk
  pattern is **error-swallowing** (`t.Logf`+`continue` instead of `t.Fatal`)
  in the two most load-bearing end-to-end integration tests
  (`ledgerstream_to_storage`, `external_fleet`) and the platform store suite —
  they report symptoms, not causes, exactly when a regression lands.
- **The biggest COVERAGE gaps are below the served tier:** ClickHouse (the
  ADR-0034 source of truth) has only pure unit tests and zero live round-trip;
  the entire explorer endpoint + participant-index surface is stub-unit-tested
  at best with no integration coverage and an entirely untested CH reader; and
  the aggregator compute path is unit-only. Chaos Wave-2 (HA) is all deferred
  and the newest Wave-1 scenario has never been run.

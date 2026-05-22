# Changelog

All notable changes to Rates Engine will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to dual versioning — SemVer for `pkg/*`
and CalVer (`YYYY.MM.DD`) for binary releases. See
[docs/discovery/repo-structure-plan.md §10](docs/discovery/repo-structure-plan.md)
for the rationale.

Every release lists the Stellar protocol version it was tested
against.

---

## [Unreleased]

### Added

- **Monthly `galexie-archive-trim` systemd unit (#7, ADR-0027 §5).**
  The monthly trim cadence that ages newly-cold ledgers out of local
  MinIO into the AWS public bucket. `compute-trim-cutoff.sh` derives
  the cutoff (indexer cursor − 90 days of ledgers) at run time, the
  service invokes `ratesengine-ops trim-galexie-archive
  -older-than-ledger ${CUTOFF} -verify-upstream -commit`, the timer
  fires on the 1st of each month at 03:17 UTC. The Ansible role
  installs the unit but **does not enable** it — per
  `feedback_cold_tier_premature_enable` the monthly trim is
  destructive before §3 (cold-tiering on) + §4 (bulk-trim done);
  the operator runs `systemctl enable --now
  galexie-archive-trim.timer` by hand once that rollout completes.

- **`ratesengine_ledgerstream_tier_both_missing` page-grade alert
  (#7).** ADR-0027's cold-tier failure mode (an LCM that is missing
  from BOTH the local hot tier AND the AWS public bucket cold tier)
  now alerts at page severity, with a runbook covering the
  rehydrate-from-peer / disable-trim-timer / fix-config decision
  tree. The metric was already exported by
  `internal/ledgerstream/tiered.go`; the rule, the R1 overlay and
  the runbook close the operational surface so an operator
  enabling cold-tiering (§3) plus the bulk trim (§4) has the safety
  net in place. The alert is silent until cold-tiering is enabled
  (the metric stays at zero before then).

### Changed

- **`oracle_updates` retention removed (#14).** Migration 0040 drops
  the 90-day retention policy on the `oracle_updates` hypertable.
  Sister to migration 0031 which did the same for `trades`: every
  raw oracle observation is now preserved indefinitely. The 0034
  CAGGs (`oracle_prices_1m` … `oracle_prices_1mo`) are unchanged;
  the migration header documents the per-grain
  `refresh_continuous_aggregate` operator call that re-backfills
  them over the full raw range so the API serves long-form oracle
  history.

### Added

- **All 19 RedStone feeds now decode (#53).** The decoder matched
  feed_ids against the crypto allow-list (`IsKnownCrypto`). The
  on-chain `feed_id()` strings — captured 2026-05-22 — are not the
  display names for 5 of 19 feeds, so EUROC (feed_id `EUROC/EUR`)
  silently never decoded and the 11 RWA / tokenized-BTC feeds were
  all dropped. `internal/sources/redstone/feeds.go` replaces the
  allow-list match with an explicit 19-entry registry keyed on the
  exact feed_id, each mapped to a canonical `(base, quote)` pair.
  Tokenized real-world assets (BENJI, GILTS, CETES, KTB, TESOURO,
  USTRY, SPXU, iBENJI) decode as the new `rwa` `AssetType`
  (ADR-0028); SolvBTC variants are `crypto`. The quote is now
  per-feed — EUROC lands as EUR-denominated instead of being
  mislabelled USD. RedStone stays `ClassOracle` / `IncludeInVWAP=
  false`, so NAV-quoted RWA references never feed market VWAP.

- **Rozo-Stellar bridge ingest (#41).** Rozo's v1 intent-bridge is
  now a wired source. The decoder for the two v1 Payment events
  (`payment`, `flush`) had shipped earlier; this completes the
  source — a stateless topic `Decoder` gated on the three live v1
  Payment contracts, a `consumer.Event` projection, dispatcher
  registration, and persistence to a new `rozo_events` hypertable
  (migration 0039, fully typed — no jsonb blob). `Class=ClassBridge`,
  never VWAP. Enable by adding `"rozo"` to
  `ingestion.enabled_sources`; `BackfillSafe` stays `false` pending
  the WASM-history audit. v2 Forwarder / IntentBridge stay
  unwired — they are pre-mainnet.

- **CCTP-Stellar bridge ingest (#40).** Circle's CCTP v2 is now a
  wired source. The decoder for the four contract events
  (`deposit_for_burn`, `mint_and_withdraw`, `message_sent`,
  `message_received`) had shipped earlier; this completes the
  source — a stateless topic `Decoder` gated on the three known
  CCTP contracts, a `consumer.Event` projection, dispatcher
  registration, and persistence to a new `cctp_events` hypertable
  (migration 0038). CCTP is `Class=ClassBridge`: bridge flow, never
  contributes to VWAP. `deposit_for_burn` / `mint_and_withdraw` are
  USDC supply exits / entries beyond the classic trustline channel.
  Enable by adding `"cctp"` to `ingestion.enabled_sources`;
  `BackfillSafe` stays `false` pending the WASM-history audit.

- **`trades_pair_source_ts_idx` composite index (#30).** Migration
  0037 adds `(base_asset, quote_asset, source, ts DESC, ledger DESC)`
  on `trades`. `Store.LatestTradePerSource` (behind `/v1/observations`)
  runs `SELECT DISTINCT ON (source) … ORDER BY source, ts DESC,
  ledger DESC`; with only the pre-existing pair index that degraded
  to an O(rows_in_pair) scan-then-sort. The new index orders exactly
  as the query does within the `(base_asset, quote_asset)` prefix, so
  the planner walks it as an O(num_sources) skip-scan. On an
  already-populated node build it `CONCURRENTLY` by hand first — see
  the migration header.

### Fixed

- **RedStone EUROC feed never decoded (#53).** The EUROC feed's
  on-chain feed_id is `EUROC/EUR`, which never matched the crypto
  allow-list entry `EUROC`, so the feed was silently skipped since
  launch. Fixed by the explicit feed registry above; EUROC now lands
  as an EUR-denominated observation.

- **AWS-SDK checksum-warning log flood (#62).** Since
  `aws-sdk-go-v2/config` v1.29.0 the SDK's default response-checksum
  mode is `when_supported`: it tries to validate a checksum on every
  S3 GET and, when the response carries none, logs `Response has no
  supported checksum. Not validating response payload.` at WARN.
  galexie's MinIO GetObject responses carry no such checksum, so a
  verify-archive chain walk (~1200 ledgers/s) emitted that line per
  read — ballooning `/tmp/va-full.log` to 1.65 GB and burying the
  real verify-archive failure under noise. `ratesengine-indexer` and
  `ratesengine-ops` now default `AWS_RESPONSE_CHECKSUM_VALIDATION` to
  `when_required` at process start (operator-set values are
  respected), so the SDK validates only when the operation requires
  it — S3 GetObject does not.

## [v0.5.0-rc.72] — 2026-05-22

### Fixed

- **`/v1/price/batch` p99 pinned at the 10s ceiling (#64).**
  `lookupPriceBatch` resolved its asset_ids in a **serial loop** —
  up to 100 (GET) / 1000 (POST) — each id a `LatestPrice` + the
  three-layer price-fallback chain, i.e. one-to-several DB
  round-trips. A 100-id batch serially was ~5–10s, hitting the
  handler deadline. The per-id resolution is now a bounded parallel
  fan-out (`priceBatchConcurrency = 16`): per-id results land in an
  index-keyed slice so the envelope still preserves first-occurrence
  order, the first validation/internal failure in input order still
  aborts the whole batch, and the per-row freeze lookup moved into
  the parallel work. Race-tested.

## [v0.5.0-rc.71] — 2026-05-22

### Fixed

- **`/v1/assets/{id}` cold-path latency (#63).** A cache-miss
  asset-detail request was 570ms–2.3s. Three mechanisms, all fixed:
  - The F2 overlay (`applyF2Fields`) ran its four DB-bound reads —
    24h volume, 24h change, USD price, supply snapshot —
    **serially**, so a cold request paid their sum. They touch
    disjoint `AssetDetail` fields and are now fanned out
    concurrently; the wall-clock cost collapses to the slowest
    single read.
  - The SEP-1 `stellar.toml` overlay cache (`cachekeys.TOMLTTL`)
    expired every **15 minutes**, so a cold request whose issuer
    domain had aged out blocked up to 500ms on a fresh upstream
    HTTPS fetch *on the request path*. `stellar.toml` is
    slow-changing issuer reference data — TTL raised to **24h**, so
    that fetch is paid ~once per domain per day rather than per
    15-minute window.
  - The API's `selfPrewarmAssetEndpoints` goroutine HTTP-GETs its
    own `/v1/assets/{id}` endpoints to warm caches; those requests
    are deliberately cold and were counted in the customer-facing
    latency histogram, dominating p95/p99. They now carry a
    `ratesengine-prewarm/` User-Agent and are excluded from the SLO
    histogram alongside the existing smoke + probe synthetic
    traffic.

## [v0.5.0-rc.70] — 2026-05-22

### Fixed

- **SSE streaming endpoints poisoned the API latency SLO (#60).**
  The `HTTPMetrics` middleware recorded `time.Since(start)` into the
  `http_request_duration_seconds` histogram for every route. An SSE
  handler returns only when the client disconnects, so that
  "duration" is the connection *lifetime* (minutes-to-hours), not
  request latency. The histogram tops out at 10s, so every closed
  stream landed in the `+Inf` bucket and pinned p99 at 10000ms —
  burning the latency SLO and firing `ratesengine_api_latency_p99_high`
  / `_p95_high`. A latent bug since the first stream endpoint, it
  became material once `/v1/ledger/stream` (#58) gave every
  status-page viewer a long-lived connection. Fix: the middleware now
  still counts streaming requests in `http_requests_total` but skips
  the duration observation for `/stream` routes.
- **`galexie-archive` tip-lag alert false-fired every partition
  cycle (#61).** `ratesengine_galexie_archive_tip_lag_high` (`> 5000`)
  and `_severe` (`> 50000`, a page) predated the 64,000-ledger
  partition model: `galexie-archive-fill` mirrors only *complete*
  partitions, so the archive tip structurally lags live by 0–64,000
  ledgers as live works through the current partition. Both
  thresholds sat inside that normal range. Raised `_high` to
  `> 64000` for 90m (a completed partition stayed un-mirrored past
  two fill cycles) and `_severe` to `> 128000` (≥2 partitions
  behind), and corrected the rule's partition-model documentation.

## [v0.5.0-rc.69] — 2026-05-22

### Added

- **`/v1/ledger/tip` + `/v1/ledger/stream` — the live-ingest
  frontier (#58).** `/v1/ledger/tip` is a lightweight endpoint
  returning just the highest ledger the indexer has committed and
  its lag — a status page or monitor can poll "what ledger are we
  on" without pulling the whole `/v1/diagnostics/ingestion`
  snapshot. `latest_ledger` reads the `ledgerstream` ingestion
  cursor (upserted once per ledger), so it is the freshest tip
  signal available. `/v1/ledger/stream` is the SSE counterpart:
  it pushes a `ledger_update` event per new ledger (poll cadence
  ~2s) plus a keepalive refresh every ~10s so `lag_seconds` stays
  honest during an ingest stall — letting the status page render
  blocks arriving in real time instead of polling on a 30s timer.

### Fixed

- **Live-tail ingest lag sawtoothed 0→30s (#57).** The SDK
  `BufferedStorageBackend` defaults `RetryWait` to 30s: once the
  indexer catches up to galexie's tip, the fetch worker requesting
  the next ledger object misses (galexie hasn't uploaded it yet)
  and sleeps a full 30s before re-checking — even though galexie
  uploads each LCM within ~5s of ledger close. The result was the
  indexer cursor advancing in bursts of ~5 ledgers every ~30s
  rather than tracking the tip continuously, so end-to-end ingest
  lag oscillated between ~2s and ~30s. Added
  `ledgerstream.Config.LiveRetryWait` — an unbounded-stream-only
  override (a missing object on a bounded range is still a hard
  error) — and set it to 3s for the galexie-live bucket in
  `pipeline.LedgerstreamConfig`. A caught-up worker now re-checks
  every 3s, collapsing the lag floor to ~3–8s.

## [v0.5.0-rc.68] — 2026-05-22

### Fixed

- **SLA probe false-failed on `/v1/issuers` availability (#54).**
  `ratesengine-sla-probe`'s worker loop recorded every `hit()`
  result unconditionally — including the one request per worker
  still in flight when the run-duration context expired. That
  cancelled request was counted as a 2xx miss, so every run
  reported exactly `concurrency` phantom failures (~0.3% loss),
  over-attributed to the slowest endpoint (`/v1/issuers`, ~81ms —
  the widest window to be in flight when the deadline lands). It
  was tripping `ratesengine_sla_probe_unit_failed` (P3) with no
  real breach: the API served 100% (server logs + 2,400 manual
  requests confirm), latency/freshness passed comfortably. Fix:
  discard samples whose request was cancelled by the run-duration
  ctx (the probe aborting itself ≠ a server failure); a genuine
  mid-window failure still counts. Also gave the probe a cloned
  transport with `MaxIdleConnsPerHost = concurrency*2` so the
  single-host workload reuses keep-alive connections instead of
  churning the default pool of 2.

## [v0.5.0-rc.67] — 2026-05-21

### Fixed

- **`/v1/assets/{id}` cache TTL was shorter than the prewarm
  cadence — p95/p99 blew up (#52).** The `assetDetailResponseCache`
  TTL was 30s but `selfPrewarmAssetEndpoints` only refreshes every
  60s, so the cache sat expired for 30 of every 60 seconds. The
  status page polls `/v1/assets/native` every 30s and kept landing
  in the cold window, paying the full ~700ms handler rebuild every
  time (inflated to ~2.8s under concurrent-backfill CPU
  contention). Every OTHER status-page endpoint measured 1-200ms —
  the entire API p95/p99 tail (and the three latency SLO alerts)
  was this one endpoint's cold misses. Bumped the TTL to 120s —
  one full prewarm interval of headroom, so a prewarm pass always
  refreshes an entry before it expires and `native` stays
  permanently warm. Matches the sibling F2-path caches (1–2 min
  TTL, same 60s prewarm). 120s staleness still fits the ADR-0015
  closed-bucket-only contract.

### Changed

- **Status page endpoint probe is now two-shot (#52).** The
  per-endpoint latency matrix on the status page polls every
  30 s / 2 min; between polls Cloudflare lets the edge→origin
  connection pool go cold, so a single probe's first request paid
  a full CF↔origin TCP+TLS setup (~2-3 s measured) that has
  nothing to do with API latency — the API serves cached asset
  detail in <10 ms. The probe now fires a throwaway warm-up fetch
  first and measures the second request on the warm connection,
  i.e. the latency a returning user actually experiences. Both
  fetches keep `cache: 'no-store'` so neither the browser nor the
  CDN serves a stale body — it's still a real round trip, just not
  a cold-pool one. A non-2xx or thrown warm-up short-circuits
  (reports `down`/`error` without a second request). web/status
  frontend only — no API change.

## [v0.5.0-rc.66] — 2026-05-21

### Fixed

- **defindex VaultEvent silently dropped by pipeline sink (#49 follow-up).**
  rc.65 shipped the Phase-B decoder that matches DeFindexVault topic
  events and produces `defindex.VaultEvent` consumer events — but
  the pipeline's `persistEvent` type-switch only had a case for
  `defindex.Event` (strategy layer) and not `defindex.VaultEvent`.
  Vault events flowed all the way through Matches+Decode, then fell
  into the unhandled-default branch and were dropped with a
  `ratesengine_source_insert_errors_total{kind="unhandled",source="defindex"}`
  metric bump. (Caught when 246 strategy flow log lines appeared
  but zero vault flow lines — the metric counter was the smoking
  gun.) Added the missing case: counter bump + INFO log
  `"defindex vault flow"` with user, multi-asset amounts, and
  df_tokens delta, identical in shape to the per-package Sink
  defined in `internal/sources/defindex/consumer.go`. This is the
  outer-router gap any new event TYPE (vs new source) will hit —
  worth a future pass to make the dispatcher Sink interface
  do the routing instead of a hand-curated type-switch.

## [v0.5.0-rc.65] — 2026-05-21

### Added

- **defindex decoder now covers the vault-wrapper layer too (#49).**
  Phase A (rc.58) decoded `("BlendStrategy","deposit"|"withdraw")` —
  the *strategy* contracts. That captured the underlying capital
  movement but `from` was always the vault contract C-strkey, not
  the end-user. A 2026-05-21 cross-check against Soroban-RPC
  `getEvents` showed we were at 27% coverage in a 12-hour window
  (and only 14% pre-rc.63 walker), because every user interaction
  flows USER → vault wrapper → strategy contract → Blend pool, and
  we were only seeing the strategy leg. Phase B adds
  `("DeFindexVault","deposit"|"withdraw")` decoding for the
  user-facing wrapper layer: `depositor` / `withdrawer` (G-strkey),
  `amounts` / `amounts_withdrawn` (Vec<i128> — multi-asset support),
  `df_tokens_minted` / `df_tokens_burned` (share-token deltas).
  Dispatch is still purely topic-based (no contract address
  hardcoding) so every current AND future DeFindex vault wrapper
  the factory spawns (100+ in lifetime per SE) is decoded
  automatically. Audit doc updated with the two-layer model,
  cross-check methodology, and Phase-C+ scope (harvest, rebalance,
  factory `create`, typed flow hypertable).

### Fixed

- **`galexie-append.sh` no longer skips ledgers on restart (#50).**
  The wrapper used to ALWAYS start galexie at "archive tip minus a
  checkpoint margin," which silently created a multi-thousand-ledger
  gap in `galexie-live` whenever the service was restarted on an
  already-running deployment (the script's own TODO comment had
  flagged this; the 2026-05-21 ZFS topology migration is what tripped
  it — ledgerstream cursor was frozen at L62,669,692 for ~20 min
  before recovery). New behaviour: the wrapper first probes MinIO
  `galexie-live` via the existing `galexie-writer` MinIO credentials,
  finds the highest exported LCM, and resumes from
  `last_exported + 1`. The archive-tip-minus-margin path is preserved
  as the fresh-deploy fallback (empty bucket). Includes
  `docs/operations/archival-node-bringup.md` step-3 update.

## [v0.5.0-rc.64] — 2026-05-21

### Fixed

- **`/v1/assets/{id}` warm-after-cold still 700-900ms internal /
  ~2079ms external post-rc.63 (#37 final).** rc.63's
  `selfPrewarmAssetEndpoints` exercised the handler at startup +
  every 60s, but the handler's underlying F2 readers
  (`Volume24hUSDForAsset`, `supply.LatestSupply`, 2× `lookupUSDPrice`
  via `populateChange24h` + `populatePriceUSD`, `fetchSupplySnapshot`)
  are wired direct to `storeXxxReader` — no cache layer. Every
  prewarm hit paid the full handler cost; every user request did
  too. Diagnostic on r1: `pg_stat_activity` showed no active SQL
  during a curl loop (Postgres isn't the bottleneck per-call,
  but the cumulative cost of 4-5 sequential reader round-trips
  is). New `assetDetailResponseCache` (32-byte TTL: 30s) caches
  the pre-rendered JSON envelope bytes per asset_id. Drift-safe
  by construction — the cached entry IS what the handler produces.
  Cache miss serves at ~700ms (previous warm cost); subsequent
  hits within 30s serve at sub-millisecond with `X-Ratesengine-Cache: HIT`
  header. The `selfPrewarmAssetEndpoints` goroutine populates the
  cache for every verified-currency + native on a 60s cadence,
  so production traffic should land on cache hits ≥95% of the
  time. 30s staleness fits comfortably inside the ADR-0015
  closed-bucket-only contract: the underlying data sources update
  per-minute at fastest.

### Notes

- **#48 phase 2 verified on r1.** rc.63's dispatcher auth-tree walker
  was deployed at 10:47 CEST. A bounded backfill replay against
  `[L62,000,000, L62,640,000]` (640k ledgers, soroswap-router only,
  `-parallel 8`) produced 11,724+ new entries in the
  `source_entry_counts.soroswap-router` counter — climbing from a
  pre-fix baseline of 22 entries across 12M ledgers (top-level-only
  walker). The walker is decisively firing on aggregator-wrapped
  router calls; the ~9000× undercount documented in
  `docs/architecture/contract-call-coverage-audit.md` is closed.
  Full historical replay across the soroswap-router genesis range
  is queued for a follow-on session once Move A's snapshot is
  destroyed (~2026-05-27) and disk headroom is freed.

## [v0.5.0-rc.63] — 2026-05-21

### Added

- **Dispatcher walks the full Soroban auth tree for ContractCallDecoder
  routing (#48 phase 1).** Pre-fix, `extractInvokeContractCalls`
  walked only `tx.Envelope.Operations()` (top-level ops). When a user
  hit the soroswap router via an aggregator (~60% of soroswap volume
  per the 2026-05-21 sample of recent pair trades), the router call
  was a sub-invocation invisible to the dispatcher. Result: ~8729×
  undercount on the `soroswap-router` source — 22 entries in
  `source_entry_counts` vs Stellar Expert's 192,046 events on the
  router contract since deploy. New `extractInvokeContractCallTrees`
  walks each op's `SorobanAuthorizedInvocation` auth tree in
  pre-order DFS and captures every (contract_id, function_name, args)
  tuple reachable. `ContractCallContext.CallPath` (new field)
  identifies a call's position in the tree for downstream dedup;
  empty = top-level, `[0,1]` = second sub-call of first sub-call of
  root, etc. The pre-existing `extractInvokeContractCalls` (top-level
  only) is preserved for the `events.Event.OpArgs` enrichment path
  which needs the op's direct call args, not the tree. Falls back
  to top-level when `ihf.Auth` is empty (rare; non-token-moving
  calls), preserving the pre-#48 baseline for that case. Decoders
  unchanged — `Matches(contract_id, function_name)` runs per call in
  the tree, `Decode()` emits an event per match. Full design in
  `docs/architecture/contract-call-coverage-audit.md`; 5 new unit
  tests in `internal/dispatcher/extract_call_trees_test.go`
  including the headline aggregator→router→pair scenario.

- **`docs/architecture/storage-considerations.md`** — living
  knowledge base on r1's storage. Per-dataset inventory, per-subdir
  touchpoint maps for `/srv/history-archive`, ADR cross-refs
  (0002 / 0015 / 0016 / 0017 / 0027 / 0011), trim trade-off register
  covering Moves A-G with the operator-stance evaluation matrix,
  and the operational plan + rollback steps for Move A (the trim
  that landed on 2026-05-20). Records Move A as the approved
  decision with 7-day observation window.

- **`docs/architecture/contract-call-coverage-audit.md`** —
  cross-check audit comparing Stellar Expert per-contract
  metrics against our internal `source_entry_counts` across 17
  Soroban contracts spanning every Soroban source. Captures the
  three independent evidence lines that confirmed the #48 decoder
  gap, the 4 sources verified working correctly, the 2 incomplete-
  backfill candidates (redstone, blend/comet) gated on #35, and
  the implementation plan (this RC's phase 1 + the replay-and-ADR
  phase 2 work).

- **`internal/sources/rozo/events.go`: `MainnetPaymentContracts`
  and `MainnetRelayerAccounts`.** Operator-supplied by RozoAI
  2026-05-21: their bridge fleet includes three v1 Payment C
  contracts (same `PaymentEvent` / `FlushEvent` schemas) and two
  G-strkey relayer wallets that handle most USDC / EURC volume.
  Decoder shape unchanged — additional contracts emit the same
  `topic[0]` symbols, so adding to the watchlist is scoping, not
  schema work. Relayer accounts don't emit Soroban events; they
  show up as classic `payment` op source/destination. Tracking
  pattern documented in `docs/architecture/rozo-stellar-coverage.md`.

### Fixed

- **`/v1/assets/{id}` warm-after-cold still 750-890ms on native and
  2.2s on canonical USDC post-rc.62 (#37 full).** rc.62 added
  `prewarmAssetDetail` covering the 7 `CachedCoinsReader` SWR slots
  the handler fans out to. But the handler also calls F2-path
  readers (`Volume24hUSDForAsset`, `supply.LatestSupply`,
  `lookupUSDPrice`, `populateChange24h`) with their own caches
  that the in-process prewarm goroutine doesn't enumerate. The F2
  readers cold-filled per request, dominating the warm-path
  latency. Lean fix: new `selfPrewarmAssetEndpoints` goroutine
  HTTP-GETs `/v1/assets/<id>` against our own listener every 60s
  for native + every verified currency. Drift-safe by construction
  — the call hits the same `Server.Handler` the user takes, so
  every internal lookup happens with byte-identical args. Per
  `feedback_prewarm_handler_drift` this is the canonical pattern
  when handler fan-out is wider than the prewarm goroutine's
  per-reader enumeration.

- **Cold-tier init failure cascades into a hot-side backfill
  abort (#7 §3 residual).** `internal/ledgerstream.streamTiered`
  used to return any `NewDataStore(ctx, cold)` error verbatim, so a
  wrong-region cold endpoint (the 2026-05-20 §3 enable scenario)
  aborted every backfill at first `ListObjectsV2`. ADR-0027 has
  cold as OPTIONAL by design; matching that posture, the cold-init
  failure path now logs a Warn and falls back to the hot-only
  `ingest.ApplyLedgerMetadata` single-source path. Hot-init failure
  still propagates (real config error, not an optional-tier issue).

### Notes

- **Storage trim Move A executed live 2026-05-20.** 7.1 TB of
  `/srv/history-archive/{bucket,transactions,results,scp}` removed
  under ZFS snapshot `data/archive@pre-trim-2026-05-20` (instant
  rollback for 7 days; commit by destroying the snapshot). Live
  `REFER` dropped from 6.95 TB to 26.3 GB. Pool stays at 93% until
  snapshot destroy frees the bytes (~2026-05-27). See
  `docs/architecture/storage-considerations.md` for the trade-off
  register, operational plan, and rollback steps.

## [v0.5.0-rc.62] — 2026-05-20

### Fixed

- **Prewarm extended to all 7 readers fired by `/v1/assets/{id}`
  (full deferred-#37).** rc.61 partial covered only
  `GetCoinByAssetID` per verified asset; live measurement on r1
  post-rc.61 confirmed the canonical-form path still dropped to
  2.2s on first hit because the other SIX readers fired by the
  handler (`GetCoinTopMarkets(id, 5)`,
  `GetCoinPriceHistory24h`, `GetCoinPriceHistory7d`,
  `GetCoinMarketsCount`, `GetCoinTradeCount24h`, `GetCoinATH`)
  cold-filled per request. Subsequent hits served sub-ms only
  because the first request populated all 7 SWR slots. Full fix:
  new `prewarmAssetDetail(ctx, logger, coins, assetID)` helper
  that calls EVERY one of the 7 readers per verified canonical
  asset_id PLUS native. Limit=5 on `GetCoinTopMarkets` matches
  the handler's literal — per the
  `feedback_prewarm_handler_drift` memory, any drift in
  args (limit, order, sources) means a different SWR cache key
  → silent miss → the same bug class. Each reader's prewarm
  call logs at Debug; transient failures don't block subsequent
  readers. Net effect post-deploy: every verified-currency
  canonical-form lookup (and native) should land sub-200ms on
  FIRST hit, not just subsequent.
  (#34 residual).** Postgres can issue SQLSTATE 57014
  (`canceling statement due to user request`) from server-side
  `statement_timeout` / `lock_timeout` /
  `idle_in_transaction_session_timeout` — none of which trip
  `clientAborted` (the http request context is alive) or
  `handlerTimedOut` (the per-call context hasn't deadlined). The
  error reached the issuers handler as a bare 57014 and fell
  through to a 500. Combined with the sla-probe's threshold
  set at exactly 99.0%, a single transient per ~430-sample
  burst put availability under threshold and fired
  `ratesengine_sla_probe_unit_failed_alert`. Fix: new
  `transientStorageErr(err)` helper in `envelope.go` that
  classifies the three transient classes (SQLSTATE 57014 not
  carried by context cancellation, `driver: bad connection`
  after pool retries exhausted, and EOF / broken-pipe network
  blips) and returns 503 with the `issuers-transient` problem
  type. Standard handler ordering preserved: clientAborted →
  handlerTimedOut → transientStorageErr → 500. The 503 still
  registers as a non-2xx for the sla-probe's availability
  metric, BUT — operator runbook: configure the probe to
  count 5xx-but-not-503 as failures, OR loosen the threshold
  to 98.5%. The cleaner long-term path is operator-facing.
- **Density formula no longer over-credits via interior-gap
  bridging (user-reported).** Status-page reported 100% density
  for `soroswap-router` and `defindex` while the #38 historical
  backfill was only ~78% through their range. Root cause:
  `extendWithLiveTail` (`diagnostics_ingestion.go:1056`) was
  bridging interior gaps between two backfill intervals whenever
  the upper bracket's start ≤ liveTop, on the assumption that
  live ingest had walked the gap. That assumption is FALSE for
  sources added to `enabled_sources` after live ingest had
  already crossed the gap-end ledger — exactly the case for
  soroswap-router + defindex which were enabled at rc.5x while
  the live cursor was already at ~62.5M; the interior
  [60M, 62.5M] gap got false credit. Fix: remove interior-gap
  bridging entirely. Live-tail credit is now **head-band only**
  — from the top of the backfill union up to `min(liveTop, tip)`.
  The previously-protected edge case ("disjoint high
  gap-backfill island silently capping density at ~96%") becomes
  honest under-coverage: operators close such gaps with a
  targeted backfill rather than silent live-credit. Density on
  defindex/router should now drop from the false 100% to the
  honest ~78%-and-rising as #38 progresses. Two existing tests
  that locked in the bridging behaviour were updated to the new
  honesty-first policy.

## [v0.5.0-rc.61] — 2026-05-20

### Fixed

- **`entries` counter expanded to ALL observer-driven sinks
  (follow-up to the blend/router/defindex fix).** Same session,
  same root cause: the seed query only knew about `trades` +
  `oracle_updates`, leaving the supply observers' tables
  (`account_observations`, `trustline_observations`,
  `claimable_observations`, `lp_reserve_observations`,
  `sac_balance_observations`, `sep41_supply_events`) silently
  excluded from per-source entries even though they're the
  primary observable activity surface for those sources. Now:
  (1) `SeedSourceEntryCounts` UNIONs all six observer tables
  with literal source names matching each observer's
  `SourceName` constant; (2) each persister
  (`persistAccountObservation`, `persistTrustlineObservation`,
  …, `persistSEP41SupplyEvent`) calls `bumpEntryCount` after a
  successful insert so the steady-state counter stays current
  between seed reconciliations. The result: `/v1/diagnostics/ingestion`
  surfaces entries for `accounts`, `trustlines`,
  `claimable_balances`, `liquidity_pools`, `sac_balances`,
  `sep41_supply` alongside the trade + oracle sources — the
  full "total decoded protocol activity" the user asked for.
- **`entries` counter now tracks total protocol activity, not just
  trades.** User-reported: `/v1/diagnostics/ingestion` showed 0
  entries for **blend** (writes to `blend_auctions`, not `trades`),
  **defindex** (Phase A log-only sink — no storage table),
  **soroswap-router** (same — log-only). Root cause:
  `SeedSourceEntryCounts` only UNION-ALL'd over `trades` +
  `oracle_updates`. Three-part fix:
  (1) `SeedSourceEntryCounts` query extended to also UNION
  `blend_auctions` (literal source `'blend'`) and `fx_quotes`
  (its nullable source column COALESCE'd to `'unknown-fx'` so
  unlabelled rows still surface rather than vanish). (2) New
  `Store.BumpSourceEntryCount(ctx, source, n)` method — single
  UPSERT with `ON CONFLICT DO UPDATE SET entry_count =
  entry_count + EXCLUDED.entry_count`. Cheap enough for per-event
  use on low-volume sinks. (3) `sink.go` wires the bump into
  every Phase A log-only case (soroswap-router, defindex) and
  every blend-auction persister (new / fill / delete). Shared
  helper `bumpEntryCount` logs failures at Warn — bump errors
  don't fail the underlying decode (operator's
  `ratesengine-ops seed-entry-counts` reconciles drift). Other
  observer-driven sinks (account_observations,
  classic_supply_*, sep41_supply_events) are surfaced via the
  supply-observer pages per ADR-0023, not source-attributed
  entries — documented in the updated seed-query comment.

### Added

- **`internal/sources/cctp` decoder for Circle CCTP v2 events
  (#40 Phase 1).** Pure-function decoder package for all four CCTP
  events: `DepositForBurn` (outbound USDC burn from
  TokenMessengerMinter), `MintAndWithdraw` (inbound mint after
  attestation), `MessageSent` (wire envelope, paired with
  DepositForBurn), `MessageReceived` (wire envelope, paired with
  MintAndWithdraw). `events.go` defines the four canonical Go
  types with full BytesN<32>-as-hex serialisation for the
  cross-chain address fields (`mint_recipient`,
  `destination_token_messenger`, `destination_caller`, `nonce`,
  `sender`); `decode.go` exposes `Classify` + four `Decode*`
  functions with explicit `ErrMalformedTopic` /
  `ErrMalformedBody` sentinels for schema-drift detection;
  `decode_test.go` covers 16 cases including all four event
  types' happy paths, ADR-0003 large-i128 round-trip on
  DepositForBurn's amount, short-topic + missing-body-field
  drift signals, MessageSent's dual ScMap/raw-Bytes paths
  (forward-compat against macro layout shifts), and topic-symbol
  encoding stability for all four. Per ADR-0013 the decoder
  doesn't import `xdr` directly — uses inferred-type entries
  through `scval` returns, same pattern as Soroswap.
  **NOT yet wired** — no registry entry, no `consumer.Source`,
  no migration. Wiring follows the storage-shape decision
  (`bridge_events` shared with Rozo vs `cctp_events` separate)
  per `docs/architecture/cctp-stellar-coverage.md` §Storage.
  CCTP + Rozo decoders shipping in parallel means the storage
  layer can be designed against both event shapes at once.
- **`internal/sources/rozo` decoder for Rozo v1 Payment events
  (#41 Phase 1).** Pure-function decoder package — `events.go`
  defines the canonical `Payment` + `Flush` Go types and the
  pre-encoded topic-symbol constants;  `decode.go` exposes
  `Classify`, `DecodePayment`, `DecodeFlush` with explicit
  `ErrMalformedBody` for field-missing surfacing; `decode_test.go`
  carries 12 parallel tests including the ADR-0003 large-i128
  round-trip (locks the `*big.Int → string` precision invariant
  against the int64-truncation bug class) and topic-symbol
  encoding stability guards (re-encoded bytes must match the
  package-init constants — drift would silently break
  `Classify`). The package is **NOT yet wired** — no
  registration in `internal/sources/external/registry.go`, no
  `consumer.Source` impl, no `dispatcher_adapter.go`. Wiring +
  storage layer follows the `bridge_events` vs `rozo_events`
  shape decision (operator-gated; see
  `docs/architecture/rozo-stellar-coverage.md` §Storage).
  Capturing the decoder logic in code with tests means the
  implementation phase doesn't have to re-derive the on-chain
  event schema from the contract source — it's the
  smallest-possible-PR that advances #41 without committing to a
  specific storage shape.
- **`ClassBridge` source class for cross-chain transfer protocols
  (#40 + #41 unblock).** Adds `ClassBridge Class = "bridge"` to
  `internal/sources/external/framework.go` alongside the existing
  six classes. Bridges (Circle CCTP, Rozo) move tokens between
  chains rather than exchanging them at a price — a
  `deposit_for_burn` on Stellar + `mint_and_withdraw` on Ethereum
  is one logical USDC transfer, not a two-leg trade. Excluded
  from VWAP by default (`IncludeInVWAP: false`); reported
  alongside for cross-chain flow attribution and as the
  cross-chain side of Algorithm 3 supply accounting (complements
  ADR-0023's SEP-41 supply observer that already tracks
  classic trustline-driven mints/burns). The
  `TestRegistry_ClassPolicy` invariant ("only ClassExchange may
  VWAP-contribute") covers ClassBridge unchanged.
  `TestClassBridge_Defined` locks the wire value so a downstream
  rename surfaces as a build break here rather than as a silent
  classification miss. Removes the primary operator gate from
  the #40 / #41 design docs — implementation can now proceed on
  the storage-shape decision alone (`bridge_events` shared vs
  per-protocol tables).

### Docs

- **`docs/architecture/rozo-stellar-coverage.md` — Rozo intents
  decoder + storage design (#41 design pass).** Captures three
  distinct contract variants discovered in
  `RozoAI/rozo-intents-contracts`: (1) **v1 Payment LIVE** on
  mainnet at `CAC5SKP5FJT2ZZ7YLV4UCOM6Z5SQCCVPZWHLLLVQNQG2RWWOOSP3IYRL`
  (verified via StellarExpert) — emits `PaymentEvent { from,
  destination, amount, memo }` on `("payment", from)` topic and
  `FlushEvent { token, destination, amount }` on `("flush",)`
  topic; (2) **v2 Forwarder + IntentBridge** pre-mainnet with
  topic shapes `("forward", sender)`, `("memo_set",)`,
  `("created",)`, `("filled",)`, `("refunded",)`; (3) a **newer
  rozo-intents** package emitting `("intent_created", intent_id:
  BytesN<32>)` + `intent_filled` / `intent_failed` /
  `intent_refunded` long-form symbols (status unclear — possibly
  v2.1 or v3 unifying variant). Shares CCTP's
  `ClassBridge`-or-not design question + the storage shape
  question (`bridge_events` shared with CCTP vs `rozo_events`
  separate). Recommends three-phase rollout: ship v1 Payment
  decoder now (the only live contract — user direction was "v1
  and v2", but v2 isn't deployed yet so Phase 1 covers reality),
  Phase 2 for v2 contracts when they deploy to mainnet, Phase 3
  for the rozo-intents variant after schema-status
  clarification. Implementation gated on the same operator
  decisions as CCTP.
- **`docs/architecture/cctp-stellar-coverage.md` — CCTP-Stellar
  decoder + storage design (#40 design pass).** Captures the
  three mainnet contract addresses (TokenMessengerMinter,
  MessageTransmitter, CctpForwarder), the four canonical event
  schemas (`DepositForBurn`, `MintAndWithdraw`, `MessageSent`,
  `MessageReceived`) extracted verbatim from
  `circlefin/stellar-cctp/contracts/{token-messenger-minter-v2,
  message-transmitter-v2}/src/lib.rs`, decoder strategy
  recommendation (Option A: topic-based via existing dispatcher,
  same pattern as Soroswap / Phoenix / Aquarius), and storage
  shape recommendation (new `cctp_events` hypertable via
  migration 0037 — bridge events don't fit `trades`). Surfaces
  five operator-gated design questions, primary being whether
  CCTP warrants a new `ClassBridge` source class (it doesn't fit
  `ClassExchange` — no trades, no price signal — or the existing
  `ClassRouter` semantic, which elides the cross-chain
  dimension). Implementation lands after class-design sign-off
  (per CLAUDE.md "Add a new on-chain Soroban DEX" + WASM-history
  walk before `BackfillSafe: true`). The user direction was
  "CCTP shouldn't have any history because it is brand new" —
  so initial implementation is live-only ingest from current
  ledger forward.

### Changed

- **Issuer-filter pushdown into `listCoinsBaseSelect` CTEs (#27).**
  Live r1 `EXPLAIN ANALYZE` on `/v1/assets?issuer=GA5Z…`:
  `per_asset_24h_vol`'s `Partial HashAggregate` scanned **256,724
  rows** of `prices_1m` to materialise stats for every asset, then
  the outer SELECT discarded all but 9 (the actual issuer's
  asset count). 1.3M shared-buffer hits for a single-issuer
  query. The PostgreSQL 12+ default of inlining CTEs doesn't
  help here because each per-asset CTE has an aggregate
  (`SUM`/`DISTINCT ON`) that becomes a predicate-pushdown barrier;
  the issuer filter on `ca.issuer_g_strkey` is unrelated to the
  CTEs' `GROUP BY asset_id`, so the planner can't push through.
  Fix: when `issuer` is set in `buildCoinsQuery`,
  `listCoinsBaseSelectSQL` prepends a `chosen_assets` CTE that
  materialises the issuer's asset_id set once, and each of the
  nine per-asset CTEs adds `AND base_asset IN (SELECT asset_id
  FROM chosen_assets)` (`per_asset_24h_vol` also adds the
  symmetric `quote_asset IN` to the union's quote-side branch).
  The four `xlm_usd` CTEs deliberately stay unfiltered (they
  look up XLM specifically, not the caller's asset). Sentinel
  comments (`/*PUSHDOWN_BASE*/`, `/*PUSHDOWN_QUOTE*/`) embedded
  in the SQL get replaced in-place — keeping the const + the
  renderer adjacent rather than maintaining two parallel
  300-line SQL strings. q-search pushdown intentionally deferred
  — `LIKE` patterns on three columns combined with the outer
  `LIKE` rules don't reduce as predictably; if profiling later
  shows that path is hot, a q-side `chosen_assets` variant can
  be added. Backfill behaviour unchanged: unfiltered LIST
  (the dominant traffic pattern) is byte-for-byte the same SQL.
  Six tests cover the renderer + buildCoinsQuery branches
  (no-pushdown, with-pushdown, no-issuer, issuer-only,
  q-only-no-pushdown, issuer+q).

### Fixed

- **Prewarm extended to verified-currency canonical asset_ids
  (#37 follow-up).** Real measurement on r1 post-rc.60:
  `/v1/assets/usdc` (slug form) was 144ms but
  `/v1/assets/USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`
  (canonical form) was **3.3s** — same underlying
  `getCoinBySlugSQL`, but the canonical form missed cache because
  prewarmLight only warmed `ListCoinsExt` + `GetNativeCoinRow`.
  Programmatic clients + the explorer's drill-out paths navigate
  by canonical asset_id, so this was the dominant user-visible
  slowness post-rc.59's native-only prewarm. Fix: catalogue load
  moved BEFORE the prewarm goroutine; `prewarmCaches` +
  `prewarmLight` now take a `[]string verifiedAssetIDs` extracted
  from the catalogue (each Stellar-network entry's `AssetID`,
  excluding native + empty). Each entry feeds a
  `coins.GetCoinByAssetID` call alongside the existing
  `GetNativeCoinRow`. Drift-safe — `GetCoinByAssetID` is the
  exact reader the `/v1/assets/{id}` handler calls
  (`assets_coin_extension.go:215`); same wire path = same cache
  key.

## [v0.5.0-rc.60] — 2026-05-20

### Added

- **`ratesengine-ops trim-galexie-archive` operator (#7
  implementation step 2b — second half of ADR-0027 §Step 2).**
  DESTRUCTIVE subcommand that deletes LCM files from the local hot
  tier (galexie-archive MinIO) whose entire ledger range is below
  the operator-specified `--older-than-ledger N`, after verifying
  upstream presence in the cold tier. Five-layer safety stack:
  (1) `--dry-run` is the **default** when neither `--dry-run` nor
  `--commit` is set — actual deletion requires explicit
  `--commit`; (2) `--verify-upstream` is the **default** — every
  candidate is HEAD'd against cold before being marked for
  deletion; `--no-verify-upstream` is a documented escape hatch
  for restore-from-backup workflows; (3) `--max-files` caps
  deletions per run (default 100000) — a typo cannot trim the
  full archive in one shot; (4) `--older-than-ledger` is
  **required** (no implicit cutoff); (5) cold tier MUST be
  configured (refuses to run otherwise — trim without a cold
  fallback is unrecoverable data loss). Rollback is mechanical:
  `ratesengine-ops rehydrate-galexie-archive -from N -to N`
  re-fetches from cold. Per-object `DeleteObject` (vs bulk
  `DeleteObjects`) so a partial failure leaves a clear position
  cursor — operator re-runs `--dry-run` to see what's left.
  Promotes `aws-sdk-go-v2/{aws,config,credentials,service/s3}`
  from transitive to direct dependencies (already in our tree
  via go-stellar-sdk) — needed because the SDK's
  `datastore.DataStore` interface lacks a `Delete` method. Tests
  cover the safety primitives (default verify-upstream, default
  no-commit, `--commit` opt-in, uint32 overflow guard) +
  `splitBucketPath` (SDK-compatible `bucket/prefix` parsing). The
  full ADR-0027 §Step 2 is now complete (rehydrate from §2a +
  trim from §2b); §Steps 3-5 (flag-flip in r1's TOML, first bulk
  trim, monthly cadence) are operator-gated and follow.
- **`ratesengine-ops rehydrate-galexie-archive` operator (#7
  implementation step 2a — first half of ADR-0027 §Step 2).**
  Non-destructive subcommand that copies LCM files for a ledger
  range from the configured cold tier (`storage.s3_cold_*`) back
  into the local hot tier (`storage.s3_bucket_archive` MinIO
  bucket). Idempotent via `PutFileIfNotExists` — files already
  present in hot are skipped, not refetched. `-dry-run` reports
  the file list + skipped / would-copy / missing-in-cold counts
  without writing. Use cases: recover from accidental trim,
  pre-warm hot before a planned backfill, cold-tier integrity
  spot check (the `missing_in_cold` counter surfaces files that
  genuinely never landed upstream). Refuses to run when cold tier
  isn't configured. The path-enumeration logic uses the SDK's
  `DataStoreSchema.GetObjectKeyFromSequenceNumber` + steps by
  `LedgersPerFile` so each schema-aligned file is visited once;
  a defensive fallback handles `LedgersPerFile == 0` (a
  malformed schema would otherwise infinite-loop). Tests cover:
  alignment of `-from` down to file boundary, no-duplicates,
  zero-LPF fallback, single-LPF (the Galexie default), flag
  parsing (4 cases). The destructive **trim** operator (the
  second half of §Step 2) follows in a separate commit — it
  needs delete capability which the SDK's `datastore.DataStore`
  doesn't expose, so it'll wire AWS SDK v2's `s3.Client`
  directly.
- **`StorageConfig` cold-tier fields + `LedgerstreamConfig` wires
  them (#7 implementation step 1c).** New TOML fields in
  `[storage]`: `s3_cold_endpoint`, `s3_cold_region`,
  `s3_cold_bucket_archive`, `s3_cold_access_key_env`,
  `s3_cold_secret_key_env`. All default to empty — every
  pre-ADR-0027 deployment continues to use the legacy single-
  source path byte-for-byte. `StorageConfig.ColdTieringEnabled()`
  returns true iff `s3_cold_bucket_archive` is set (the
  `LCM_TIER_ENABLED=false` of ADR-0027 §Step 1 expressed as a
  field presence). `pipeline.LedgerstreamConfig` populates
  `ledgerstream.Config.ColdDataStore` when tiering is enabled
  **and** the caller is reading the archive bucket — the live
  bucket (`galexie-live`) is the rolling near-tip working set
  authored locally and is never tiered. Tests cover the
  no-cold-tier default, the cold-tier-archive path, the
  cold-tier-skipped-for-live-bucket guard, and the
  `ColdTieringEnabled` truth table. ADR-0027 §Step 1 is now
  complete in code; §Steps 2-5 (trim + rehydrate operators,
  flag-flip on r1, bulk trim, monthly cadence) follow as
  separate commits.
- **`ledgerstream.Stream` gains an opt-in tiered read path
  (#7 implementation step 1b).** `Config` learns a new optional
  `ColdDataStore datastore.DataStoreConfig` field; when set,
  `Stream` constructs a [`TieredDataStore`](#7-step-1a) wrapping
  the hot (`Config.DataStore`) + cold (`Config.ColdDataStore`)
  underlying stores, builds a `BufferedStorageBackend` directly
  on top, and drives the LCM iteration with a loop that mirrors
  the SDK's `ingest.ApplyLedgerMetadata` shape — same bounded /
  unbounded validation, same `max(2, range.From)` clamp, same
  GetLedger-per-ledger sequence, same WithMetrics wrap when a
  registry is provided. When `ColdDataStore` is zero-valued
  (the default), the legacy single-source path through
  `ingest.ApplyLedgerMetadata` is used unchanged — backward
  compatible with every existing caller. This satisfies ADR-0027
  §Sequencing step 1 ("Land the dual-source read path behind a
  `LCM_TIER_ENABLED=false` flag"); the flag here is the
  presence/absence of `ColdDataStore` rather than a separate
  bool. Operator-facing config wiring (parsing the cold-tier TOML
  block + populating `cfg.ColdDataStore`) is the next step.
- **`ledgerstream.TieredDataStore` — two-tier `datastore.DataStore`
  fallback chain (#7 implementation step 1).** Satisfies the SDK's
  `datastore.DataStore` interface; composes a `hot` + `cold`
  underlying store. Reads try hot first, fall through to cold on
  `IsNotFound` errors only — transient errors (network timeouts,
  auth failures, throttling) propagate immediately so a
  misconfigured hot endpoint surfaces as the operator's problem
  rather than being masked by a slow cold path that always
  succeeds. Writes (`PutFile`, `PutFileIfNotExists`) target hot
  exclusively (cold is read-only by design — production cold is
  `aws-public-blockchain`, the AWS Open Data Sponsorship bucket).
  `ListFilePaths` unions hot + cold with hot-wins dedup so a
  backfill spanning the tier boundary sees every partition.
  Optional Prometheus metrics: `ratesengine_ledgerstream_tier_read_total{outcome="hot"|"cold"|"both_missing"}`
  and `ratesengine_ledgerstream_cold_read_duration_seconds`. Not
  yet wired into `ledgerstream.Stream`'s Config — that integration
  is the next step (still behind the planned `LCM_TIER_ENABLED`
  feature flag per ADR-0027 §Sequencing).

### Docs

- **`docs/operations/lcm-cache-tiering.md` — operator runbook for
  ADR-0027 §Steps 3-5 (#7 implementation companion).** Step-by-
  step playbook for the operator-gated transition: TOML flag-flip
  (Step 3), first bulk trim with chunked 1M-ledger invocations
  + per-chunk pool monitoring (Step 4), and the monthly cadence
  caveat (Step 5 — timer not yet shipped, pending an
  `--older-than-duration` mode that resolves tip at run time).
  Includes pre-flight checklist, cutoff-ledger computation
  formula (`TIP - 90 × 17280`), rollback playbook via the
  rehydrate operator, and a "common failure modes" catalogue
  (cold tier check fails, `cold.Exists` warnings, pool capacity
  rise during trim, indexer `cold.GetFile` errors). Metrics
  reference points operators at
  `ratesengine_ledgerstream_tier_read_total{outcome=...}` and
  `ratesengine_ledgerstream_cold_read_duration_seconds` for
  real-time visibility.
- **ADR-0027 (Proposed): LCM cache tiering — local
  galexie-archive as hot, `aws-public-blockchain` as cold (#7
  design pass).** R1's ZFS pool is at 93% (12.5 TB used, 1.35 TB
  free) with the 2026-05-17 SEV showing what structural-tight
  headroom costs. The biggest single tier-able lever is the
  4.96 TB `data/minio` dataset (mostly galexie-archive's
  genesis→tip LCM mirror); the AWS Open Data Sponsorship publishes
  the same data at sub-15ms for in-region readers and ~80 ms per-
  GET (amortised over 64-ledger partitions) for r1. ADR proposes a
  90 d hot window in local galexie-archive with cold reads
  falling back to AWS, a HEAD-verify-before-delete trim operator
  (`ratesengine-ops trim-galexie-archive`), and a five-step
  rollout that lands the dual-source read path under a feature
  flag before any deletion happens. Recovers ~3-4 TB at the 90 d
  cutoff, unblocking #30 (composite index on the 2.7B-row
  trades hypertable) and #35 (the SEV-frozen Soroban-era
  backfill resume). History-archive offload + galexie-live
  promotion-cadence tuning + PostgreSQL chunk retention beyond
  current policy are explicitly out of scope as separate ADRs.

### Changed

- **`buildPoolsQuery` reads from `pools_per_source_1h` (#25 phase
  2).** Replaces three trades-hypertable scans (vol_24h CTE,
  last_px DISTINCT-ON CTE, outer FROM trades) with a single CAGG
  scan + GROUP BY. The XLM-fallback semantics for unpriced trades
  are preserved exactly (priced trades contribute their stored
  `usd_volume`; trades with an XLM leg fall back to
  `base_amount × XLM/USD` or `quote_amount × XLM/USD`; pure-token-
  token unpriced trades contribute 0 — pre-#25 returned NULL, but
  the handler scan collapsed NULL and "0" identically, so client-
  visible behaviour unchanged). Trade-off: `last_trade_at` lags by
  up to one CAGG refresh interval (5 min); acceptable for a pools
  discovery surface. After this commit ships, #23's
  `CachedMarketsReader` SWR layer becomes a latency nicety rather
  than load-bearing — refresh fills stop paying the 8-30s trades-
  scan cost. Integration test bootstrap force-refreshes the new
  CAGG alongside `prices_1m`. **Operator note:** the CAGG was
  created `WITH NO DATA` in migration 0036; the 5-minute policy
  only refreshes the last 7 days. Run
  `CALL refresh_continuous_aggregate('pools_per_source_1h', NULL, NULL)`
  once on r1 after the 0036 migration applies, to backfill the
  14d-window's historical buckets so /v1/pools sees the full pool
  set immediately rather than ramping up over a week.

### Added

- **Per-source pools continuous aggregate — `pools_per_source_1h`
  (#25, migration 0036).** The durable backing for `/v1/pools`.
  Pre-#25 the handler's `buildPoolsQuery` scanned the full trades
  hypertable for `ts >= NOW() - 24h` grouped by `(source, base,
  quote)` — measured 8-30s; #23 wrapped it in SWR (sub-ms warm,
  ~8s cold first hit). This CAGG pre-aggregates per
  `(source, base_asset, quote_asset, 1h bucket)`:
  `sum_usd_priced`, `sum_base_unpriced` / `sum_quote_unpriced`
  (Phase-1 vs needs-XLM-fallback splits), `trade_count`, and
  `last(quote_amount/base_amount, ts)` for the per-pool latest
  price. Refresh policy every 5 minutes covering the last 7 days
  (over-refresh tolerates late-arriving backfilled trades — the
  #38 router/defindex run is the current example). Storage:
  ~3-4M rows steady-state (~hundreds of MB) — small enough to keep
  no retention so operators can later widen the window past 24h.
  Handler refactor to read from the CAGG ships in a follow-up
  commit (this commit lands the migration first so the CAGG can
  materialize cleanly before the handler depends on it). After
  refactor, #23's SWR becomes a latency nicety rather than
  load-bearing.

### Changed

- **node-exporter consolidation: legacy → Debian package (#33).**
  The pre-#33 state was two units fighting `:9100` — a hand-rolled
  `node_exporter.service` (custom unit + `/usr/local/bin/node_exporter`,
  2024 binary) running, and the apt-installed
  `prometheus-node-exporter.service` perpetually failing because
  the port was taken. Cut over to the Debian package live on r1:
  configured `/etc/default/prometheus-node-exporter` with the
  legacy's exact flags (`--collector.systemd --collector.processes
  --collector.textfile.directory=/var/lib/node_exporter/textfile_collector`
  — preserves every existing textfile metric: `archive_completeness.prom`,
  `sla_probe.prom`, `galexie_archive_tip_lag.prom`), stopped + disabled
  the legacy unit, restarted the package. Live-verified: 13 textfile
  metric lines visible, 3127 `node_*` metrics serving,
  `prometheus-node-exporter` no longer in the failed-unit list.
  Codified in Ansible (`10-observability.yml`): apt-install the
  package, template `ARGS=`, enable, idempotent stop+disable of any
  pre-existing legacy unit. Legacy unit file + binary deliberately
  retained for zero-downtime rollback (`systemctl stop
  prometheus-node-exporter && systemctl start node_exporter`).

- **`/v1/diagnostics/ingestion` pregenerated server-side (#16).**
  Background goroutine `Server.StartIngestionSnapshotRefresh`
  builds the full ingestion-diagnostics snapshot every 15 s into
  an `atomic.Pointer[ingestionSnapshotEntry]`; the handler reads
  the atomic and writes it sub-ms instead of the previous ~417 ms
  inline build (7 parallel DB-filler goroutines + post-fillers
  coverage projection). Inline build remains as the cold-start
  fallback (the atomic is nil until the first refresh fires).
  Cadence (15 s) matches the existing `Cache-Control: max-age=15`
  header. Refresh uses a detached `context.Background()`-derived
  ctx (`//nolint:gosec,contextcheck` — intentional, the parent is
  the api process lifetime, not any request). Launched alongside
  the existing `prewarmCaches` goroutine in `cmd/ratesengine-api`.

### Added

- **galexie-archive tip-lag alert (#31) — defense-in-depth for
  #26.** Adds a Prometheus textfile-collector metric
  (`galexie_archive_tip_lag_ledgers` and friends) computed every
  5 min by `galexie-archive-tip-lag.{service,timer}` running
  `/usr/local/bin/galexie-archive-tip-lag`. The accompanying alert
  pages (`ratesengine_galexie_archive_tip_lag_severe`) within hours
  if the hourly `galexie-archive-fill.timer` silently breaks — the
  exact failure class that let #26 go undetected for 23 days.
  Rules added to BOTH `deploy/monitoring/rules/galexie-archive.yml`
  and `configs/prometheus/rules.r1/galexie-archive.yml` (wave-96
  dual-dir). Runbook at
  `docs/operations/runbooks/galexie-archive-tip-lag.md`. Codified
  in Ansible (`07-galexie.yml`: copy script + install
  `.j2`-templated unit + enable timer). Live on r1 (current lag
  9,388 ledgers — well below the warn threshold of 5,000 sustained
  for 30 min). Three alert variants: `_high` (P3, warn 5 k for
  30 m), `_severe` (P1, page 50 k for 30 m), `_metric_stale` (P3,
  the metric file hasn't refreshed in 30 m — the alert canary).

## [v0.5.0-rc.59] — 2026-05-20

### Changed

- **`defindex` `BackfillSafe: true`** — audit closed
  (`docs/operations/wasm-audits/defindex.md` Audit closure).
  Both gates satisfied post-rc.58: (1) **live-verify PASS** —
  indexer emits `defindex strategy flow` against real on-chain
  traffic (accumulating steadily; the rewritten BlendStrategy
  topic-dispatched decoder matches the deployed contract's actual
  emissions); (2) **WASM re-audit PASS vs `11329c24…988`** —
  `wasm2wat` data-section scan confirms every required symbol
  present (`BlendStrategy`/`deposit`/`withdraw`/`from`/`amount`).
  Unblocks `ratesengine-ops backfill --source=defindex` (still
  operator-triggered + zpool-headroom-gated; coordinate with #35
  Soroban-era backfill resume and #7 LCM-tiering).

### Fixed

- **`/v1/observations` short-circuit for aggregator-only quotes —
  the actual #29 fix (replaces the unworkable detached-SWR
  approach).** Post-rc.58 live-verify showed the `CachedHistoryReader`
  SWR did NOT warm the cache: the detached fill returned `pq: 57014`
  fast (root cause: the underlying `LatestTradePerSource` query for
  `WHERE base='native' AND quote='fiat:USD'` is an unbounded
  per-chunk scan over the 2.7 B-row `trades` hypertable, measured
  **>60 s** on r1 — the empty-result fan-out proving emptiness).
  Real fix: aggregator-only quote types (`fiat:*` per ADR-0010,
  `crypto:*` per ADR-0014) are reference-currency abstractions —
  `trades.quote_asset` *only* stores concrete token assets
  (Stellar classic / Soroban), never bare `fiat:USD` / `crypto:BTC`,
  so the result is *always* empty by definition. Added a fast-path
  in `handleObservations`: if `pair.Quote.Type` is `AssetFiat` or
  `AssetCrypto`, return the canonical empty result + triangulation
  hint (the meaningful signal for proxy/triangulated quotes anyway,
  identical to the post-storage empty branch) without touching
  storage. Sub-millisecond, semantically identical to running the
  query, ~60 s cheaper. The status-page poll
  (`?asset=native&quote=fiat:USD`) now returns instantly.
  Regression test `TestObservations_FiatCryptoQuoteShortCircuit`
  pins the no-storage-call contract via a stub History that errors
  if called; 4 existing tests updated to use a real classic USDC
  quote (their fiat:USD URLs were test-fictional and would have
  spuriously short-circuited). `go test -race` green. #30 (the
  missing `(base, quote, source, ts)` index) is no longer
  load-bearing for the visible status-page path; it remains the
  durable fix for non-empty token-quote pairs that are still slow.

- **`/v1/assets/native` prewarm-drift (#37).** The existing
  `prewarmLight` only primed `coins.ListCoinsExt` — it never
  touched the per-asset SWR entries #24 added (especially
  `GetNativeCoinRow`, the heaviest single-asset cache key, which
  hits the whole-asset-universe CTE). Every cold post-restart
  `/v1/assets/native` request paid ~3 s on first hit and bounced
  1–3 s on rapid retries as each #24 cache entry filled
  incrementally. Added `coins.GetNativeCoinRow(ctx)` to
  `prewarmLight` — drift-safe (calls the same method
  `assets_coin_extension.go` invokes for `/v1/assets/native`).
  Heeds [[feedback_prewarm_handler_drift]].

## [v0.5.0-rc.58] — 2026-05-19

### Added

- **`ratesengine-ops scan-soroban-events` — in-infra ground-truth
  event dumper (#28).** Streams a bounded galexie ledger range and
  prints every Soroban contract event as one JSON line
  (`contract_id`, decoded `topic[]`, body map keys + value types),
  optionally filtered to `topic[0]==STR` and/or one contract. A
  catch-all `dispatcher.Decoder` reuses the dispatcher's
  LCM→`events.Event` extraction, so it answers "what does protocol
  X *actually* emit on-chain" without BigQuery (the
  `hubble-soroban-events` analogue, which needs GCP we don't have).
  No DB writes. Built to unblock the defindex decoder
  re-derivation (#28) — discover real contract addresses + event
  schemas before writing/auditing a decoder — but reusable for the
  whole granular-coverage mission. Bundles into rc.58.

### Fixed

- **`/v1/issuers` spurious 500 under concurrency (#34).** A
  client-canceled request mid-`ListIssuers` surfaces from lib/pq as
  `canceling statement due to user request` (SQLSTATE 57014). The
  handler checked `handlerTimedOut` but was missing the
  `clientAborted` guard the canonical pattern (and
  `handleObservations`) uses — so a client abort fell through to a
  generic `Issuers list failed` **500** + ERROR log, polluting the
  5xx rate and SLA availability (it was the sole sla-probe
  SLA-harness blocker post-#32, ~4 % under the probe's concurrency;
  external sequential requests were always 200). Added
  `if clientAborted(r, err) { return }` as the first error check
  (matches `envelope.go`'s documented ordering). Regression test
  `TestHandleIssuersList_ClientAbortedNo500`; the existing
  generic-error→500 test still passes (fix is scoped to client
  abort only). `go test -race` green.

- **galexie-archive 23-day mirror stall — healed + scheduled
  catch-up so it can't silently recur (#26).** r1's durable
  full-mirror (ADR-0016) had silently fallen ~346 k ledgers /
  23 days behind (held genesis→`62,296,694`, nothing after
  2026-04-26): the live appender kept `galexie-live` current but
  the hardened `galexie-archive-fill` catch-up script (already
  built + installed) was only ever invoked **by hand** — nothing
  scheduled it. Healed the gap (`mc mirror` from live, 57.8 GiB,
  all partitions now complete; aws-public-blockchain is the
  durable upstream so nothing was at permanent risk, but the
  full-mirror guarantee was broken and local WASM-walks /
  backfills past the stall failed). Standing fix:
  `galexie-archive-fill.{service,timer}` (hourly, oneshot,
  root for the `local`+`aws-public` mc aliases) — added to
  `deploy/systemd/` + Ansible
  (`roles/archival-node/templates/systemd/*.j2` +
  `tasks/07-galexie.yml`), and installed + enabled + test-fired
  on r1 immediately (test run: `Result=success`, "needs work
  (missing): 0"). A stall is now repaired within ~1 h instead of
  weeks. Defense-in-depth lag alert split to a follow-up.
  Bundles into rc.58.

- **`/v1/observations` ~8 s → 503 fixed via `CachedHistoryReader`
  SWR (#29).** The status page polls
  `?asset=native&quote=fiat:USD` every ~2 min; that pair has zero
  direct trades (`fiat:USD` is an aggregator proxy, never a stored
  `quote_asset`) yet `LatestTradePerSource` is an unbounded
  `DISTINCT ON (source) … ORDER BY source, ts DESC` over the
  2.7 B-row `trades` hypertable — no time bound → no chunk
  exclusion → ~8 s even for an empty result → the handler's 8 s
  ceiling 503s. New `internal/api/v1/history_cache.go`
  SWR-caches **`LatestTradePerSource` only** (every other
  `HistoryReader` method passes through), wired at 2 m TTL in
  `cmd/ratesengine-api/main.go`. Mirrors the proven #22/#23
  pattern with one deliberate change: the cold fill is
  **detached** (own 30 s budget) so it outlives the 8 s request
  ceiling — the first caller(s) still 503 (bounded by their own
  ctx) but the fill warms the cache out-of-band, so the next poll
  is fast. Zero correctness loss (the exact query result,
  including a legitimate empty slice, is cached). Also corrected
  the `HistoryReader.LatestTradePerSource` doc, which falsely
  claimed a `(base_asset,quote_asset,source,ts DESC)` index that
  was never created. The real query-cheapening (create that
  index) is deferred (#30 — multi-GB on a 2.7 B-row hypertable,
  r1 disk-constrained). `go test -race` green (4 new tests incl.
  the detached-cold-fill-warms case). Bundles into rc.58.

- **`defindex` decoder re-derived from real on-chain schema —
  was decoding nothing (#28).** The decoder + its docs/tests were
  written against `paltalabs/defindex` tag `1.0.0`
  (`("DeFindexVault",…){depositor,amounts:Vec<i128>,
  df_tokens_minted}`); mainnet never deployed that. The watched
  contract addresses run Blend *strategy* code (deployed WASM
  `11329c24…988`) and emit `("BlendStrategy","deposit"|
  "withdraw")` with body `ScvMap{from:Address, amount:i128}` —
  confirmed from real LCM via the new `scan-soroban-events`.
  Rewrote `internal/sources/defindex/{events,decode,
  dispatcher_adapter,consumer}.go` to the real schema,
  **dispatched by topic across every BlendStrategy emitter** (not
  the mislabeled 3-contract set — comet/aquarius shared-emitter
  topology, captures all Blend autocompound instances). Deleted
  the fictional `MainnetVault*` / `MainnetVaultWASMHash` / factory
  consts; regenerated tests from the real schema (`go test -race`
  green, incl. the contract-`from` case mainnet actually emits).
  `BackfillSafe` stays `false` until live-verify on r1 + WASM
  re-audit vs `11329c24…988` (defindex.md "Resolution"). Source
  key kept `defindex` (rename to `blend-strategy` deferred —
  product-taxonomy, not correctness). Bundles into rc.58.

- **WASM-history audit: `soroswap-router` PASS → `BackfillSafe:
  true`; `defindex` FAIL → stays gated; defindex genesis
  corrected (#6, #28).** The 2026-05-19 r1 wasm-history walk +
  byte-level disassembly resolved both Phase-A router sources.
  `soroswap-router`: a single immutable WASM hash
  (`4c3db3eb...07`) over the contract's entire on-chain life
  `[50_746_272→tip]`, zero mid-life upgrades, both decoded
  function exports present, no event surface — `BackfillSafe`
  flipped `true`. `defindex`: **audit FAILED** — the decoder was
  written against `paltalabs/defindex` tag `1.0.0` (vault hash
  `0f3073...8f3a`) but mainnet runs `11329c24...988`, whose
  deposit/withdraw topic + body schema differ (the
  `DeFindexVault` topic and every documented body field are
  absent from the sha256-verified deployed bytes;
  `aggregator_exposures` is empty on r1, corroborating that live
  defindex decoding matches nothing). `BackfillSafe` stays
  `false`; the gate did its job. Re-deriving the decoder from the
  deployed contract is Task #28. Independently,
  `sourceGenesisLedger["defindex"]` corrected from the
  provisional `51_499_545` to the walk-exact factory first-deploy
  `57_056_338` (#10-class precision; orthogonal to the decoder
  fault — an honest genesis makes density read correctly, not
  falsely). Audit logs:
  `docs/operations/wasm-audits/{soroswap-router,defindex}.md`.
  Bundles into rc.58.
- **`CachedCoinsReader` single-asset SWR — fixes `/v1/assets/{id}`
  ~3.9 s (#24).** The coin-extension path was *entirely uncached
  pass-through*: every `/v1/assets/{id}` ran the ~13 s
  whole-asset-universe `listCoinsBaseSelect` query (its CTEs
  aggregate ALL pairs even for one asset — structural, all CTEs are
  already time-bounded) via `GetCoinByAssetID`/`GetNativeCoinRow`,
  plus ~7 more uncached fan-out calls incl. the ~5.8 s
  `trades WHERE base OR quote=$1` scan
  (`GetCoinTradeCount24h`/`GetCoinMarketsCount`). Added a **generic
  `swr[T]`** single-value stale-while-revalidate helper (free
  function — Go methods can't be type-parametric; new `swrEntry`
  map under the existing mutex) that is the proven, race-clean #22
  `fetchRows`/`refreshRows` logic made type-parametric, and wired
  **all 9 per-asset single-value coin methods** through it
  (`GetCoinBySlug`/`ByAssetID`/`NativeCoinRow`/`TopMarkets`/
  `PriceHistory24h`/`7d`/`MarketsCount`/`ATH`/`TradeCount24h`):
  serve stale instantly, single-flighted request-ctx-independent
  background refresh, keep-stale-on-error, cold-miss blocks,
  `ttl<=0` still passes through. Zero correctness loss. `go
  test -race` clean (new generic-SWR serves-stale-under-20-
  concurrent / single-flight / keeps-stale-on-error tests + all
  existing coins tests still green). Deeper follow-up logged
  (asset-filter pushdown into the CTEs so the query itself is
  cheap). Bundles into rc.58.
- **`CachedMarketsReader` stale-while-revalidate — fixes `/v1/pools`
  ~8 s cold (#23).** Post-rc.57 sweep surfaced `/v1/pools` at
  ~8 s/ok 87 %: `buildPoolsQuery`'s OUTER `FROM trades … ts>=14 d
  GROUP BY source,base,quote` raw-trades enumeration (same disease
  class as #20). Unlike #20 it **cannot** be query-rewritten —
  verification ruled out every candidate per-source pre-aggregate
  (`prices_*` collapse source; `price_source_contributions` is
  curated/sparse — 5 sources/10 pairs, would make `/v1/pools`
  return ~10 pairs; `market_observations` doesn't exist). So the
  unavoidable per-source scan is moved **off the request path**:
  `fetchPools`/`fetchPairs` now stale-while-revalidate (serve stale
  immediately on expiry + one single-flighted, request-ctx-
  independent background refresh, keep-stale-on-error, cold-miss
  still blocks) — the exact proven `coins_cache.go` #22 pattern,
  mirrored. **Zero correctness loss** (full per-source coverage
  from raw trades preserved). `go test -race` clean across new
  `fetchPools` SWR tests (serves-stale-under-20-concurrent,
  single-flight, keeps-stale-on-error); existing cold-path tests
  still green (SWR only changes the expired path). New
  `stale`/`refresh_error` markets cache-op outcomes. A per-source
  pools CAGG (so the background query itself is cheap) is a logged
  follow-up. Bundles into rc.58.

## [v0.5.0-rc.57] — 2026-05-19

### Fixed

- **`CachedCoinsReader` stale-while-revalidate — kills the
  `/v1/assets` list cold-refresh stampede (#22).** The cache
  already single-flighted, but on TTL expiry the leader *discarded*
  the still-present stale rows and blocked ~3.9 s on the upstream
  listing aggregate inline, so every request landing in a refetch
  window paid it (p50 1 ms warm / p95 3.9 s at each TTL boundary).
  `fetchRows` now serves the stale rows **immediately** on expiry
  and triggers exactly one **background** refresh (`refreshRows`,
  request-ctx-independent so it isn't aborted when the stale
  response is written; keeps serving stale and retries on refresh
  error; single-flighted so concurrent stale reads never stampede
  upstream). Cold miss still blocks (nothing to serve). Acceptable
  staleness for an activity-ranked listing (the cache's own
  rationale). New `stale`/`refresh_error` cache-op outcomes.
  Concurrency-correct: `go test -race` clean across new SWR tests
  (serves-stale-instantly, single-flight under 25 concurrent reads,
  keeps-stale-on-error). Same SWR pattern is a candidate follow-up
  for `fetchHistoryMap` (sparkline). Bundles into rc.57.

- **`coins.go` `xlm_usd` CTE bounded to 24 h — collapses #18 ≡ #21.**
  The native→USD price CTE in the `/v1/assets/{id}` coins query had
  **no time predicate** (its `xlm_usd_1h`/`xlm_usd_24h` siblings and
  the `sources_stats.go` copies were already bounded). With no
  `bucket` floor TimescaleDB can't chunk-prune, so
  `ORDER BY bucket DESC LIMIT 1` across 3 quote_assets must consider
  *every* `prices_1m` chunk (thousands post-backfill). Warm+idle
  that's ~13 ms, but the all-chunks access pattern degrades badly
  under concurrent load + cold buffers — **observed ~40 s in
  `pg_stat_activity` during `/v1/assets/{id}` fan-out** (caught via
  in-flight sampling), the dominant tax on *every* native→USD price
  path (assets detail ×3 in the F2 chain, change_24h, market_cap,
  `/v1/price?asset=native`, network stats). This is the same query
  #18 logged at ~51 s. Adding `AND bucket >= now() - INTERVAL
  '24 hours'` chunk-prunes it to ~1 day (~2–3 ms, resilient under
  load) **and is more correct** — the unbounded form could surface
  a days-stale vwap as the *current* price; XLM/USDC trades every
  minute so a 24 h floor never realistically misses the latest.
  Surgical one-clause change; mirrors the already-bounded
  `sources_stats.go` CTE. Verified on r1; re-measure end-to-end via
  `api-latency-sweep.sh` post rc.57.

- **`/v1/markets` no longer 8 s-times-out / 503s — `distinctPairsCommon`
  reads right-granularity CAGGs (#20).** The query that powers
  `/v1/markets` (+ `?source=` / `?asset=` variants) aggregated
  `prices_1m × 14 days × ~52 k pairs`; post all-time backfill
  `prices_1m` ballooned so it seq-scanned multi-million-row
  materialised chunks (~8 s+), blowing both the 8 s handler ceiling
  and leaving the prewarm unable to warm the cache → real users got
  8 s 503/500/empty-200 (live log: a Chrome client, a node client).
  It's a *directory* query, so it now sources the 14-day
  active-pair set + `last_trade_at` + `last_price` from `prices_1d`
  (cheap — the killer was always the 14 d × 52 k-pair enumeration)
  and the 24 h `trade_count`/`volume_usd` from `prices_1m`
  RESTRICTED to the trailing 24 h (chunk-pruned → ~160 ms
  all-pairs on r1 — fast *and* exact). **Correction (caught
  pre-deploy by r1 measurement):** an interim variant sourced the
  24 h figures from `prices_1h` under a "Σ-associative → identical"
  claim — that is *false* for a rolling, non-hour-aligned 24 h
  window: prices_1h understated all-pairs 24 h volume **~9 %**
  ($3.60 B vs prices_1m $3.97 B; boundary + prices_1h refresh-lag).
  The shipped form keeps the user-facing 24 h figure
  `prices_1m`-accurate. **No data/precision loss anywhere data is
  consumed at resolution** — `prices_1m` and every detail endpoint
  (`/history`, `/ohlc`, `/chart`, `/vwap`, `/twap`) are untouched;
  the only change is the listing's `last_trade_at` rounds to the
  day (from `prices_1d`). Verified on r1 real data: plan cost
  330 k → 46 k, raw-scan → `prices_1d` index scan + a ~160 ms
  `prices_1m`-24 h aggregate, ~8 s+&uncompletable → fast, results
  sane & correctly volume-desc ordered (BTC/USDT $1.0 B,
  ETH/USDT $588 M, …). Keyset cursor / order / `Market` shape
  preserved byte-for-byte; `count_24h` COALESCE'd to 0 for
  24 h-idle pairs (more robust than the prior
  FILTER-SUM NULL). Takes effect on r1 with the rc.57 deploy;
  end-to-end re-measure via `api-latency-sweep.sh` post-deploy.

### Added

- **`scripts/dev/api-latency-sweep.sh`** — granular latency profiler
  over the entire anonymous public GET surface (the "kitchen sink"):
  N samples/endpoint → p50/p95/p99/max, ranked slowest-first,
  flagged against the RFP SLO (p95 < 200 ms) and a 1 s concern
  ceiling, exit code = endpoints over the ceiling. Portable
  (`API_BASE_URL` → run on-host for pure server compute, or from a
  VPS / against r2/r3 for network + cross-region), `CACHE_BUST=1`
  exposes uncached cost, `JSON=1` for machine diffing,
  `--spec-check` diffs coverage against `openapi/…v1.yaml` so it
  can't rot. Complements `cmd/ratesengine-sla-probe` (focused RFP
  pass/fail) with a broad diagnostic ranking. First r1 run
  surfaced `/v1/markets` (~8 s, failing), `/v1/assets/native`
  (~5 s), `/v1/assets` list cold-refresh stampede (1 ms/3.9 s).

### Fixed

- **`BackfillCoverageStats` gutted to a no-op — removes the dead
  per-source `trades` scan entirely (the honest fix; #12 only
  bounded it).** Consumer trace confirmed its output is 100 %
  unused: `buildBackfillCoverage` is cursor-first for every mapped
  source and its `cacheRows` path `continue`s past every source the
  function scanned (all in `sourceGenesisLedger`). It nonetheless
  ran ~13 per-source ts-ordered scans + a ~15 s
  `approximate_row_count('trades')` every refresh interval, and the
  oracle sources' zero-`trades` scans walked the full ~2700-chunk
  hypertable to the 57014 timeout — the CoverageCache cold-start
  hang + a primary SLO-burn contributor. Now does zero DB work;
  the dead `scanScalarBestEffort`/`coverageStatTimeout` from #12 are
  removed too. The (now-inert, zero-cost) CoverageCache scaffolding
  is removed in the #16 snapshot-pregeneration refactor that
  supersedes this whole path.
- **`verify-archive-tier-a.service`: `TimeoutStartSec` 4h → 17h —
  fixes a bootstrap deadlock that kept
  `ratesengine_verify_archive_unit_failed` firing permanently.** The
  binary self-bounds at `-max-runtime` (16h) and only writes the
  `-from-last-verified` state file on a clean exit; subsequent runs
  are then incremental (minutes). But systemd's `TimeoutStartSec`
  was 4h while `-max-runtime` was 16h, so on a fresh deploy / after
  a state-file loss the bootstrap full pass (~10–14h at 12 workers,
  state absent → `-from` genesis) was SIGTERM'd at 4h before it
  could seed state → every run a full pass → permanent failure
  (true deadlock; the old "4h is plenty for incremental" rationale
  ignored that the *first* run is always a full pass). Also bumped
  `Environment=VERIFY_ARCHIVE_MAX_RUNTIME` 4h → 16h to match. r1
  hot-fixed in place via a drop-in (TimeoutStartSec=17h) +
  `reset-failed`; the next nightly run bootstraps the state file and
  it is self-healing thereafter. Operator-copied unit (not in the
  binary release) — repo is now source-of-truth-correct so R2/R3 /
  fresh deploys don't reintroduce the deadlock.
- **`BackfillCoverageStats` is now fail-soft + per-query
  time-bounded — fixes the coverage-cache cold-start hang and a
  primary SLO-burn contributor.** Oracle sources (band / redstone /
  reflector-\*) write to `oracle_updates`, never `trades`, so their
  per-source `… WHERE source=$1 ORDER BY ts LIMIT 1` `earliest`
  query could not chunk-exclude and scanned all ~2700 trades chunks
  to prove emptiness, hitting the statement-timeout (`57014`). The
  old code did `return nil, err` on that, so `CoverageCache`'s
  cold-start `Refresh` **never succeeded** (snapshot stayed nil
  forever) and the failing query was re-issued every refresh
  interval, feeding the SLO availability/latency burn alerts. Every
  query is now run through `scanScalarBestEffort` (8 s per-query
  timeout, returns 0 on any error instead of propagating), so one
  slow/empty source degrades that field to 0 instead of blanking
  the whole snapshot. `BackfillCoverageStats` now always returns
  `(rows, nil)`. These stats are best-effort enrichment only — the
  headline density is cursor-derived and `entries` come from
  `source_entry_counts` — so 0-on-timeout is the correct safe
  degradation. Integration-covered (no DB-free unit seam).
- **`sourceGenesisLedger`: corrected `comet`/`blend` off-by-one
  (`51_499_545` → `51_499_546`).** `51_499_545` came from the walk
  JSON's `from_ledger` (the ContractCode-upload / walker transition
  boundary); the exact `ContractInstance` instantiation ledger is
  `L51_499_546` per `comet.md:157` and `blend.md:90`. comet and
  blend legitimately share this ledger — there is no standalone
  mainnet Comet; the only mainnet Comet deployment *is* Blend's
  backstop pool, instantiated in the same ledger as Blend's Pool
  Factory V2 during Blend's mainnet rollout. Comment expanded so
  the shared origin reads as intentional, not a copy-paste bug.
  `defindex` stays a clearly-labelled PROVISIONAL placeholder
  (separate 2025 protocol; real value pending its in-progress
  `wasm-history` walk) and is now deliberately distinct from the
  comet/blend pair so it is not mistaken for the real coincidence.

## [v0.5.0-rc.56] — 2026-05-19

### Fixed

- **`/v1/diagnostics/ingestion` `entries` is no longer silently 0 for
  every source.** The `fxHistoryReader` adapter
  (`cmd/ratesengine-api`) wraps `*timescale.Store` and forwards the
  coverage-reader methods (`FXCoverageStats`, `CAGGCoverageStats`)
  but was **missing a `SourceEntryCounts` delegate**. So the
  request-time type assertion `s.fxHistory.(SourceEntryCountReader)`
  in `fillIngestionEntryCounts` failed closed and returned silently,
  leaving `entryCounts` nil → `entries: 0` for *every* source
  (sdex included) even though `source_entry_counts` (migration 0035,
  maintained live by the indexer and reconciled by
  `seed-entry-counts`) was fully populated (sdex 2.7 B, …). Shipped
  missing in rc.55, so the status page showed all protocols at 0
  entries. Added the one-line delegate (same precedent as the two
  sibling forwards, which document this exact "renders empty"
  failure mode) and made the `!ok` path Warn-log instead of failing
  invisibly so this wiring-regression class can't recur unnoticed.
- **`extendWithLiveTail` now bridges interior sub-tip coverage gaps,
  fixing the ~96% (Soroban) / 99.5% (SDEX) density cap.** The
  live-ingest tail was credited only *above* the top of the merged
  backfill union. When a disjoint high gap-backfill island (e.g. the
  62,606,296–62,613,951 gap re-fill) fragmented the union, a
  ~309,700-ledger interior span [62,296,595→62,606,296] — fully
  populated by gap-free live ingest (22.8 M trades verified on r1) —
  got zero credit, capping density at ~96% Soroban / 99.5% SDEX
  (the *same* absolute hole over different genesis denominators).
  The tail now also fills any gap **between** two merged backfill
  intervals whose upper neighbour starts at/below the live cursor:
  bracketed by backfill coverage on both sides and wholly within the
  gap-free live span (ADR-0017 archivecompleteness), so live ingest
  provably walked it. The honest guards are retained — the
  `[genesis, firstBackfillStart]` lower boundary is never an
  adjacent-pair interior gap so it stays uncovered (a
  never-backfilled-low source still reads honestly, e.g. band's
  pre-deploy history under the new #10 genesis), a never-backfilled
  source stays 0%, and nothing is credited above the live cursor.
- **`sourceGenesisLedger` now holds exact first-WASM-deploy ledgers,
  not rounded deploy-era constants.** The per-source genesis is the
  denominator of `backfill_coverage[].density_pct`, so a rounded
  value was a two-way correctness bug under the "every source to
  100%" invariant: rounded *before* the real deploy padded the
  denominator with pre-existence ledgers (100% mathematically
  unreachable), rounded *after* it silently hid genuine
  early-history gaps (e.g. `band` const `53_500_000` vs real first
  deploy `50_842_736` — ~2.66M ledgers of history structurally
  invisible to the metric; `reflector-fx` const `51_000_000` vs
  real `56_733_481` — ~5.7M phantom pre-existence ledgers). All
  on-chain sources now use the MIN `create_contract` ledger across
  every routed contract (factory + instances, upgrade-in-place
  aware), sourced from the per-source WASM-audit walk evidence
  (`docs/operations/wasm-audits/`, `r1-walk-2026-05-01`); the doc
  contract flips from "approx slack is fine" to "exact, zero
  slack". `defindex` stays explicitly provisional pending its
  per-WASM walk (BackfillSafe=false; audit in_progress).
- **Migration ownership invariant documented (`migrations/README.md`
  Rule 7).** `source_entry_counts` (migration 0035) was applied
  manually as the `postgres` superuser on r1, leaving it
  superuser-owned; the rc.55 indexer's always-on entry tally and
  `ratesengine-ops seed-entry-counts` then hit `permission denied
  for table source_entry_counts (42501)`. Root cause is operational,
  not schema: `ratesengine-migrate` runs as the `ratesengine` app
  role (`RATESENGINE_POSTGRES_DSN`), so on correctly-applied deploys
  (R2/R3/fresh) the table is app-owned by construction and needs no
  GRANT — only r1's manual-as-superuser application was the anomaly.
  Hot-fixed in place with `ALTER TABLE source_entry_counts OWNER TO
  ratesengine` (canonical shape, matches `trades`); a follow-up
  GRANT migration was deliberately *not* added (it would error when
  run as the app role against a superuser-owned object and is a
  no-op otherwise — the fix is "apply as the app role", now a
  documented Rule).

## [v0.5.0-rc.55] — 2026-05-15

### Changed

- **`backfill_coverage[].trade_count` → `entries`, backed by an
  always-on per-source tally** (`source_entry_counts`, migration
  0035). The old `trade_count` came from the IO-contended
  `BackfillCoverageStats` trades scan — during an all-time backfill
  it never completed, so every source's count collapsed to a
  misleading `0` (we actually had 60M+ trades). It was also
  structurally always-`0` for oracle sources, which write to
  `oracle_updates`, never `trades`. The new `entries` column is a
  ~20-row tally table the writers bump **atomically and
  idempotently**: `InsertTrade` / `InsertOracleUpdate` increment it
  in the *same statement* as the row insert via a data-modifying
  CTE gated on `HAVING count(*) > 0`, so a backfill re-walk
  (`ON CONFLICT DO NOTHING` → 0 rows) never inflates the count.
  Reading it is O(20) regardless of trades/oracle_updates size, so
  it stays exact and available even mid-backfill, and it counts
  oracle updates for oracle sources. New `ratesengine-ops
  seed-entry-counts` authoritatively reconciles the tally from a
  full GROUP BY (run once post-backfill to fold in pre-counter
  history; SETs not ADDs, so re-running converges). Status-page
  column "Trades" → "Entries"; the section heading "Raw-trades
  coverage" → "Ingest coverage". Wire-shape change to
  `/v1/diagnostics/ingestion` (pre-v1; OpenAPI updated).

## [v0.5.0-rc.54] — 2026-05-15

### Fixed

- **Backfill-coverage density now credits the live-ingest tail —
  caught-up sources reach (and stay at) ~100%.** rc.53's
  cursor-first density only counted `source='backfill'` cursors, so
  every source showed a uniform ~281k-ledger shortfall (~96.8% for
  Soroban-era sources, 99.55% for sdex): the "head band" between
  the top of the backfill union and the live network tip. That band
  is *not* a data gap — the live `ledgerstream` cursor is covering
  it gap-free in real time — it was simply uncounted, so density
  could never reach 100% no matter how complete ingest was.
  `extendWithLiveTail` now unions `[backfillTop, min(liveTop, tip)]`
  on top of the merged backfill intervals. Honest by construction:
  a source with no backfill anchor (e.g. the un-audited
  `defindex` / `soroswap-router`, `BackfillSafe=false`) stays at
  0% — live-only decoding from the deploy ledger is not "we have
  its history"; the live contribution starts at `backfillTop` so an
  interior backfill hole is never falsely filled; and only the
  `[backfillTop, tip]` tail is asserted gap-free (the live path's
  contract + the archivecompleteness daemon, ADR-0017). A
  fully-caught-up source now reads ~100% and stays there as the tip
  advances.

## [v0.5.0-rc.53] — 2026-05-15

### Fixed

- **Coverage snapshot now populates DURING an all-time backfill.**
  `/v1/diagnostics/ingestion`'s `backfill_coverage` was built from
  the background trades-scan cache (`BackfillCoverageStats`). Under
  the all-time SDEX backfill that query is too IO-contended to
  finish within its timeout, so the cache stayed empty and the
  status page showed "Coverage snapshot pending" indefinitely —
  exactly when operators most need it. Rebuilt cursor-first:
  `density_pct` / covered / expected / earliest / latest for every
  on-chain source now derive purely from the union of completed
  backfill-cursor intervals (no trades scan), so the snapshot is
  live even mid-backfill. The trades-scan cache is demoted to
  best-effort `trade_count` enrichment + off-chain CEX/FX context
  rows; an empty or stale cache can no longer blank the whole
  section. `earliest_ledger` / `latest_ledger` are now the
  merged-cursor *processed* span (honest "what we walked"), not a
  trades MIN/MAX that could imply an interior gap is covered —
  `density_pct` remains the gap-aware number. Added
  `soroswap-router` + `defindex` to `sourceGenesisLedger` so the
  two new on-chain Soroban sources get an honest density bar
  instead of falling through to the un-mapped cache-only path.

### Changed

- **`max_locks_per_transaction` 256 → 4096** (archival-node ansible
  role default + R1 postgresql.conf). The per-transaction lock-table
  sizing knob drives the instance-wide lock table
  (`max_locks_per_transaction × max_connections`). TimescaleDB takes
  one lock per chunk a query scans — not just INSERTs; any broad
  SELECT over the now-2,738-chunk `trades` hypertable
  (per-source coverage, the pools/markets CTE, cagg/fx coverage)
  locks one entry per scanned chunk. The 64→256 hand-bump from the
  2026-05-06 SEV-3 lasted ~9 days before the table grew enough that
  concurrent diagnostics + the all-time SDEX backfill thrashed it
  again (a single query observed holding 26,927 locks; coverage
  snapshot stuck "pending", `/v1/pools?order_by=pair` 500s, cagg/fx
  coverage `out of shared memory`). Re-sized to 4096 (819,200-entry
  table) for permanent growth headroom rather than another
  incremental bump — memory cost ≈220 MB, negligible against the
  48 GB shared_buffers / 188 GB host. Applied live to R1 (postgres
  restart) + codified in the role so R1 rebuilds and R2/R3 cutover
  inherit it.

## [v0.5.0-rc.52] — 2026-05-15

### Changed

- **Status page: removed the "Backfill by decoder" panel.** Each
  backfill restart (without `-resume`) creates fresh cursor rows
  keyed by chunk boundaries, so the panel accumulated "stalled"
  rows from every prior partial run — making the page look like
  data was unreliable even when later runs (or live ingest) had
  filled the same ledger ranges. The `density_pct` column on
  backfill_coverage already answers "do we have data here"
  honestly via interval union. Wire field retained; only the UI
  panel dropped.

- **deploy.yml hardening.** Two fixes after the rc.51 deploy
  failures: (1) the migration-staging step's `ls … | head`
  tripped `pipefail` via SIGPIPE even though staging succeeded —
  swapped for a SIGPIPE-safe `sort | head` + explicit count;
  (2) new `migrations_skip` workflow input so operators who have
  applied migrations out-of-band (or know the schema is unchanged)
  can deploy past the playbook's hardcoded passwordless DSN, which
  fails auth against the live db. Proper DSN-from-target-host fix
  is a follow-up.

### Fixed

- **Backfill-coverage snapshot was permanently "pending".** The
  cache-refresh query (`BackfillCoverageStats`) ran a single
  `SELECT ... GROUP BY source` over the `trades` hypertable. Once
  trades grew past ~2700 chunks, that scan needed >2700 chunk
  AccessShareLocks in one transaction, overflowing
  `max_locks_per_transaction` (256 on r1) with
  `out of shared memory`. The error was masked by the API's 30s
  context timeout, so the symptom looked like a slow query rather
  than a hard limit.

  Rewritten to a per-source loop: each source's earliest/latest
  ledger via `ORDER BY ts {ASC,DESC} LIMIT 1` (chunk-exclusion
  stops after the first/last chunk — ~3s vs ~68s for `MIN()/MAX()`
  which seek every per-chunk index), trade count approximated from
  the 24h source/total ratio scaled by `approximate_row_count`
  (precise per-source `COUNT(*)` is 2:34s on sdex). Each statement
  runs in its own implicit transaction so the per-transaction lock
  budget resets — the 2700-chunk overflow can't recur. Full refresh
  is now ~23s across all 13 on-chain sources.

  Cache-refresh timeout raised 30s → 2min (it's a background
  goroutine, never bounds an API request; 2min sits below the 5min
  refresh interval so refreshes don't stack).

- **`/v1/pools?order_by=pair` returned 500 on every request.** The
  pair-ordered SQL branch in `buildPoolsQuery` was missing the
  `filter.Asset` arg in its `args` slice — postgres returned
  `pq: got 6 parameters but the statement requires 7` because the
  CTE references `$7`. The volume-desc branch already had the
  correct 7-arg slice. Caught live on r1 2026-05-14.

### Added

- **CORS `Allow-Credentials: true` opt-in** for cookie-bearing
  cross-origin fetches. New `[api].allow_credentials` config flag
  (default false). Required for the magic-link session on
  `/v1/account/me` + `/v1/account/keys` to actually work from a
  cross-origin browser SPA — pre-fix the preflight emitted
  `Access-Control-Allow-Origin` but no `Access-Control-Allow-Credentials`,
  so browsers stripped cookies. The middleware now panics at boot
  when both `allowed_origins=["*"]` and `allow_credentials=true`
  are set, since browsers reject that combo at the parser.

## [v0.5.0-rc.51] — 2026-05-14

### Changed

- **verify-archive Tier A is now incremental.** Pre-fix the nightly
  systemd unit re-walked the entire chain from genesis every night,
  taking ~13.8h of wall time and ~7h of CPU time per pass (67% of
  every day; visible as a sustained load-average drag on r1). Past
  LCM files are immutable, so re-hashing them is wasted compute.

  New scheme: ratesengine-ops verify-archive accepts
  `-state-file PATH -from-last-verified [-safety-overlap N]`.
  Reads the prior run's high-water mark from a small JSON file,
  computes `-from = max(2, last_verified - safety_overlap)`, and
  verifies only the new tail. The resume-from-hash from prior
  state is plumbed through so cross-run chain continuity is
  preserved (the next incremental run's first chunk must chain to
  the previous run's last verified hash).

  Default safety overlap: 5000 ledgers (~17h of chain) catches any
  anomalies that snuck in just before the last run's tip.
  systemd unit defaults updated:
  `VERIFY_ARCHIVE_STATE_FILE=/var/lib/ratesengine/verify-archive-state.json`,
  `VERIFY_ARCHIVE_MAX_RUNTIME=4h` (down from 16h). Typical
  incremental pass covers ~24h of new ledgers in minutes.

  A weekly full-archive re-pass (defense-in-depth against silent
  corruption in older chunks) remains a TBD sibling unit.

- **`backfill_coverage[].density_pct` replaces `coverage_pct` on
  `/v1/diagnostics/ingestion`.** Pre-fix the metric was `(latest -
  earliest) / (tip - genesis)` — endpoint span, not data density. A
  source with one trade at genesis and one trade at tip scored
  100% even with the whole interior empty (caught live 2026-05-14
  when SDEX backfill was still running but coverage showed 99.8%
  and aquarius/comet/phoenix/soroswap all showed 99.99%).

  New metric: union of completed portions of all backfill cursor
  intervals that include this source in their decoder set, clamped
  to `[genesis, tip]`, divided by `tip - genesis + 1`. Hits 100%
  only when backfill ranges actually cover the whole interval.
  Sparse sources no longer score 100% just for having endpoint
  trades — they score by what fraction of ledgers their backfill
  has *processed*, which is the question operators actually want
  answered.

  Wire: `coverage_pct` retained as a transitional field for one
  release. New fields: `density_pct`, `covered_ledgers`,
  `expected_ledgers`. Status page updated to render the new
  density (tooltip exposes the absolute "covered / expected"
  numerator + denominator).

### Added

- **Chainlink ingest source** (`internal/sources/external/chainlink/`).
  Promotes the formerly-divergence-only Chainlink reference into a
  full ingest source — writes `canonical.OracleUpdate` rows to
  `oracle_updates` on its own poller goroutine alongside Reflector /
  Redstone / Band. Implements `external.Poller`; lives parallel to
  the existing `internal/divergence/chainlink.go` cross-check (which
  stays in place for synchronous divergence_warning checks).

  Wire shape: poll `AggregatorV3.latestRoundData()` over JSON-RPC,
  dedupe by `(feed_address, roundId)`, project to canonical with
  synthetic deterministic tx_hash (sha256(feed || roundId)) for
  idempotent restart. Default 30s cadence; per-feed Decimals/Invert
  overrides via TOML. Default endpoint is Cloudflare public; operator
  drops an Alchemy URL (with embedded API key) into r1's TOML or via
  `CHAINLINK_RPC_URL` env. Bounded concurrency (8) per tick.

  Backfill: new `ratesengine-ops backfill-chainlink` subcommand walks
  `AnswerUpdated` event logs via chunked `eth_getLogs` (5k blocks /
  call, the safe default for Alchemy / Infura / QuickNode response-
  size caps). ~33k RPC calls and ~7h wall time for all-time backfill
  of the default 6 majors on Alchemy free tier (~19% of monthly
  quota); scale linearly with feed count up to all 516 ETH-mainnet
  Chainlink feeds within the same free-tier envelope. Idempotent on
  the oracle_updates PK; safe to re-run over already-covered ranges.

  Surface: registered in `external.Registry` as
  `ClassOracle / BackfillSafe=true / IncludeInVWAP=false`. Picked up
  by `/v1/sources?class=oracle` automatically — explorer's /oracles
  page surfaces it without UI changes.

- **Oracle CAGG ladder** (migration 0034). Seven continuous
  aggregates on `oracle_updates` at the standard
  `1m/15m/1h/4h/1d/1w/1mo` tiers — sister to the trade CAGG ladder
  in migration 0002. Closes the gap where every `/v1/oracle/*`
  history query was scanning raw `oracle_updates`; manageable at
  ~3 oracle sources × ~860 rows/day each, untenable once Chainlink
  arrives at scale.

  Aggregation semantics differ from trades: oracles are point-in-
  time observations, so each bucket carries `first / last / min /
  max / last_decimals / count` (no VWAP / TWAP because there is no
  volume dimension). One row per `(source, asset, quote, bucket)` —
  per-source identity preserved so cross-oracle comparison stays
  meaningful. Refresh policies match the trade ladder; no retention
  on sub-1h tiers (matches the operator's "store everything forever"
  decision in migration 0031), indefinite for 1h+ per the proposal.

- **DeFindex vault decoder** (`internal/sources/defindex/`).
  Event-based decoder (`dispatcher.Decoder`, NOT
  `ContractCallDecoder`) for paltalabs/defindex's autocompound
  vaults. Phase A matches `("DeFindexVault","deposit")` and
  `("DeFindexVault","withdraw")` events on the 3 known vaults
  (USDC / EURC / XLM autocompound). Decoder pulls
  `depositor` / `withdrawer`, multi-asset `amounts` vec
  (i128, no truncation per ADR-0003), and the share-token
  delta (`df_tokens_minted` / `_burned`) by name from the
  body Map (decode-by-name per
  contract-schema-evolution.md). Phase B will tag matching
  same-tx Blend / Soroswap legs as
  `routed_via=defindex-{vault}` and write
  `aggregator_exposures` rows from a separate periodic
  ticker. Pre-seed migration `0033_seed_defindex_vaults.up.sql`
  populates the 3 vaults in the `routers` registry as
  `kind='aggregator-vault'`. WASM-history audit started at
  `docs/operations/wasm-audits/defindex.md`;
  `BackfillSafe=false` until the per-hash review lands.

- **Soroswap Router decoder** (`internal/sources/soroswap_router/`).
  New ContractCallDecoder following the Band oracle pattern —
  matches by `(contract_id, function_name)` and decodes
  `swap_exact_tokens_for_tokens` / `swap_tokens_for_exact_tokens`
  invocations on the canonical pubnet router
  (`CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH`).
  Phase A is log-only — every routed swap surfaces an INFO line
  with path, in/out amounts (i128, no truncation per ADR-0003),
  recipient, and deadline. Phase B will tag matching same-tx
  `trades.routed_via` rows via the existing migration-0025 column.
  New `ClassRouter` taxonomy in `internal/sources/external/`
  (alongside the existing `ClassLending`); router class is
  attribution-only, never contributes to VWAP. Pre-seed migration
  `0032_seed_soroswap_router.up.sql` populates the `routers`
  registry. WASM-history audit started at
  `docs/operations/wasm-audits/soroswap-router.md`;
  `BackfillSafe=false` until the per-hash review lands.

### Changed

- **Raw trades retention removed** (migration 0031). Pre-fix the
  `trades` hypertable aged out at 90 days; we relied on the
  hourly+ CAGGs to preserve historical OHLC. Operator wants raw
  per-trade fidelity preserved indefinitely (regulatory + proof-
  of-pricing queries can't be reconstructed from CAGGs).
  Justification: r1's postgres data dir is on a 1.5 TB ZFS volume
  with 4% used. Earlier "no room" analysis was wrong — was
  measuring the OS root disk (49 GB), not the postgres data
  volume. Status page coverage panel relabeled from "Raw-trades
  coverage (last 90 days)" → "Raw-trades coverage — genesis →
  tip"; coverage_pct grows monotonically as backfills land.
  Compression policy on chunks > 7d is unchanged (~5x reduction).

### Added

- **CEX pair coverage — cross-fiat majors.** All four CEX
  connectors (binance/bitstamp/coinbase/kraken) now stream BTC
  and ETH against EUR + GBP in addition to USD. Pre-fix, only
  Bitstamp published BTC/EUR — every aggregator tick on
  `crypto:BTC/fiat:EUR` was single-source, which falsely tripped
  Phase 2 freeze permanently. Bitstamp + Coinbase + Kraken +
  Binance all support these pairs natively; we just hadn't
  enumerated them in the connector defaults.

  Stop-gap pre-Tier-3. The next change in this area will replace
  the hand-curated `DefaultPairs()` maps with auto-discovery from
  each exchange's pair-catalogue endpoint
  (`/api/v3/exchangeInfo` / `/products` / `/0/public/AssetPairs` /
  `/api/v2/trading-pairs-info`), filtered by an allow-list of
  quote assets. That move expands coverage from ~50 hand-curated
  pairs/exchange to ~200-1500 active pairs/exchange. Storage
  scales with PAIR COUNT (CAGG rows, ~50 MB/year for 1500 pairs)
  not raw trade volume (90-day retention), so the cost is
  bounded.

### Fixed

- **Backfill auto-refresh: three bugs caught on first real run.**
  Yesterday's commit added `refresh_continuous_aggregate` calls
  after each backfill chunk but every CAGG refresh failed. Three
  fixes from the live test:

    1. **`42P18: could not determine data type of parameter $1`**
       — lib/pq's `CALL` syntax doesn't propagate the procedure
       signature's parameter types, so untyped placeholders fail.
       Fix: explicit `::timestamptz` casts in the SQL.
    2. **`22023: refresh window too small`** for `prices_4h` /
       `_1d` / `_1w` / `_1mo` — Timescale rejects refresh windows
       narrower than 2× bucket width. A 10k-ledger chunk's ts
       span (~4h) was fine for `prices_1h` but failed every
       coarser CAGG. Fix: per-CAGG `MinWindow` declared in
       `CAGGsLiveForever`; new `PadRefreshWindow` helper expands
       the chunk's window to that minimum centered on the
       chunk's midpoint. Padded area materialises as empty
       buckets (cheap).
    3. **`55P03: concurrent refresh`** — with `-parallel N`,
       multiple chunks race on the same coarse CAGG (`prices_1mo`
       was the worst — chunks finishing close together all want
       to refresh the same monthly bucket). Fix: retry-on-55P03
       with exponential backoff (200ms → 1.6s × 5 attempts).

  End-to-end verified live: 10k-ledger SDEX backfill at
  ledgers 50,000,000-50,010,000 inserted 718,873 trades AND
  populated 66,513 `prices_1h` buckets + 22,005 `prices_1d`
  buckets — those CAGGs will now persist past the 90-day raw
  retention. Yesterday's claim "auto-refresh now works" was
  premature; this commit is what makes it true.

- **Live-site QA pass — F-01/F-03/F-04 resolved, F-02 partial.**
  Working through `docs/review-2026-05-13-live-site-qa.md`:

    - **F-01** (degraded state invisible in explorer): new
      `DegradedBanner` component polls `/v1/status` every 60s and
      renders a fixed band between Navbar and content when
      overall ≠ "ok". Tone (amber/red) keys off `pageCount > 0`.
      Includes top alert name + link to status page. Quiet when
      everything's fine; noisy enough to set expectations when
      it isn't.
    - **F-02** (pools 503 silently rendered as "No pools matched"):
      DexesView now branches on `q.isError` and surfaces an
      explicit error card with retry + link to status. Empty-
      state path is gated behind `!q.isError`. Backend perf
      (the underlying 7s cold-cache p99) tracked alongside the
      `api_cache_miss_rate_high` workstream.
    - **F-03** (CORS credentials mismatch): explorer's `useMe()`
      no longer sends `credentials: include` against an API that
      explicitly refuses credentialed CORS. Cost: signed-in
      users see signed-out CTAs in the explorer navbar
      (dashboard.ratesengine.net is unaffected — same-origin).
      Inline comment documents the cross-origin cookie work
      needed to re-enable session detection (Domain=
      .ratesengine.net + ACA-Credentials + SameSite=None).
    - **F-04** (`deep_link` API path leaked to next/link):
      `NetworksPanel` no longer feeds API `deep_link` values
      (e.g. `/v1/assets/USDC-GA5Z…`) into `<Link>`. Stellar
      rows now build the explorer route explicitly
      (`/assets/{slug}/stellar`); the API deep_link stays in
      the JSON for programmatic consumers.

- **Incident triage sweep — 9 active alerts → root-cause +
  preventatives.** Worked through every alert firing on r1 today
  and either resolved the root cause, codified prevention in
  ansible, or filed it as a known-real signal needing follow-up:

    - `node_root_disk_warning` — disk 81% → 62% by truncating a
      7.3GB syslog. Root cause: Loki running at `log_level=debug`
      spamming ~4M `caller=mock.go msg=Get key=collectors/...`
      lines/day into syslog. Fix: set Loki to `warn`
      (`configs/ansible/roles/loki/templates/loki-config.yaml.j2`)
      and add a defense-in-depth rsyslog filter so even an
      accidental level regression can't reach `/var/log/syslog`
      (`configs/ansible/roles/archival-node/tasks/15-log-discipline.yml`).
      Also pruned 36 old binary backups + 9 stale toml backups +
      vacuumed journal to 7 days.

    - `verify_archive_unit_failed` — root cause: 8h max-runtime
      cap was tight for ~62.5M-ledger pubnet. Fresh run completed
      34.7M ledgers in 8h (1207 l/s aggregate at 8 workers) then
      exited 1/FAILURE on context deadline — the same as the
      previously-rotated journal would have shown. Bumped
      defaults to 12 workers + 16h cap (sits inside the 24h
      timer cadence with headroom). Updated both the in-repo
      unit (`deploy/systemd/verify-archive-tier-a.service`) and
      the live r1 drop-in. Started a fresh run on the new
      settings; the alert clears when it finishes.

    - `sla_probe_unit_failed_alert` — REAL: `/v1/markets`,
      `/v1/assets` cold-cache p99 spikes (~5s, ~2.4s) breach
      the 500ms target on the probe's first sample after each
      30s cache-TTL window. Filed as a perf workstream — needs
      `/v1/assets` + `/v1/issuers` cache wrappers + prewarm.

    - `api_cache_miss_rate_high` — REAL: prewarm covers
      `markets/all_pools` for limits {5,25,100,200} but
      `markets/asset_markets` and `markets/source_markets`
      ops aren't prewarmed at all; user-facing requests with
      novel param tuples miss cache. Same perf workstream.

    - `anomaly_freeze_sustained` / `anomaly_freeze_engaged` —
      REAL but invisible: 1892 freeze decisions emitted, zero
      Redis markers, zero `freeze_events` rows. Phase 2's
      baseline z-score is unstable because we only have 7 days
      of `prices_1h` data (root cause = the SDEX backfill bug
      from the previous session). Added an INFO log in
      `markPhase2Freeze` so operators can grep
      `journalctl -u ratesengine-aggregator | grep "phase2 freeze"`
      to see which pairs are firing. Updated the alert
      annotation (both repo + R1 overlay) to call out the
      cold-baseline pattern + triage steps.

    - `aggregator_supply_refresh_never_initialized` — gated by
      `[supply].aggregator_refresh_enabled = false` (default).
      Enabling it requires the on-chain supply observers to be
      backfilled across the watched accounts; same workstream as
      the SDEX backfill. Not a quick fix; documented for follow-up.

    - `supply_snapshot_never_initialized` — RESOLVED: the
      `supply-snapshot.service` was running daily and exiting 0,
      but `/etc/default/supply-snapshot` didn't set `TEXTFILE_OUTPUT`,
      so the binary skipped the metric write. Wired the textfile
      path; metric now emits. Codified in
      `configs/ansible/roles/archival-node/tasks/10-observability.yml`
      so a rebuilt host gets the wiring automatically.

    - `slo_latency_burn_slow` — same family as the SLA-probe
      perf finding; will track with that workstream.

- **Backfill status surfaces "stalled" vs "running" separately.**
  `BackfillDecoderState` (the per-decoder row on
  `/v1/diagnostics/ingestion`) decomposes the previously-opaque
  `ranges_active` count into `ranges_complete` (done),
  `ranges_running` (incomplete + updated within 10 min), and
  `ranges_stalled` (incomplete + idle > 10 min — needs
  `ratesengine-ops backfill -resume`). Status page renders three
  separate columns with green/blue/red coloring. The old
  `ranges_active` field stays on the wire for back-compat.

- **Backfill auto-refreshes the long-lived CAGGs (`prices_1h` /
  `prices_4h` / `prices_1d` / `prices_1w` / `prices_1mo`) at the
  end of every chunk.** Without this, historical inserts get
  dropped by the 90-day raw-trades retention policy before the
  CAGG policy refresher's natural cadence picks them up — which
  is what happened to the May 6-11 2026 SDEX backfill (cursors
  hit `last_ledger == range_end` for every range, ~80M trades
  inserted, retention dropped them within 24h, no CAGG
  materialisation, ~5d of wall-clock work lost; trades
  `MIN(ledger)` for `sdex` collapsed back to 61,191,617).

  Backfill tool changes:
    - New `-refresh-caggs` flag (default `true`). After each
      chunk's trade-insert loop, derives the actual ts range from
      the inserted rows (`Store.LedgerRangeToTimeRange`) and
      force-refreshes every long-lived CAGG over that window
      (`Store.RefreshContinuousAggregate`).
    - Per-view soft-fail so one wedged CAGG doesn't block the
      others.
    - Procedure doc rewritten — manual `CALL refresh_continuous_
      aggregate` step removed (now automatic).

  Diagnostics endpoint additions:
    - `cagg_coverage` field reports `prices_1h` MIN/MAX bucket +
      row count — the real source-of-truth answer to "do we have
      historical OHLC since genesis?" (raw `trades` only spans
      the last 90 days; hourly+ CAGGs are retained forever).

### Added

- **Backfill coverage on `/v1/diagnostics/ingestion` + status page.**
  New `backfill_coverage[]` array on the diagnostics endpoint
  reports per-source MIN/MAX ledger from the trades hypertable,
  joined with an operator-curated map of source genesis ledgers
  (1 for SDEX, contract deploy ledger for each Soroban DEX), with
  a derived `coverage_pct` so the answer to "do we have data from
  ledger 1 to tip?" is one column. CEX/FX sources surface as
  `applies=false` (their trades have no Stellar-ledger context).
  Backed by a process-local cache refreshed every 5 min in a
  background goroutine — the underlying SQL is 2-3s on a populated
  trades hypertable, too slow for the request path.

  Status page renders a new "Coverage — ledger genesis → tip"
  table with per-source progress bars (green ≥99%, amber ≥50%,
  red <50%). Today's r1 reading: SDEX 2.18% covered (61.2M → 62.5M
  out of 1 → 62.5M), Soroban DEXes 15-17%, off-chain sources N/A.

- **Status page — per-region "Ingestion" section.** Polls each
  region's `/v1/diagnostics/ingestion` every 30s and renders a
  panel with: binary version + commit, live ledger card (latest,
  lag, 24h volume, indexed markets/assets), FX backfill coverage
  (date range, currencies, total quotes), CoinGecko market-cap
  cache state (entries, newest/oldest fetch age), supply observer
  counts, per-decoder backfill table (ranges total/active, oldest
  lag), and per-source health table joined with trailing-24h
  trades/volume/markets. Region list is a single `REGIONS` const —
  r2/r3 join by appending a row, no other code changes needed.
- **`GET /v1/diagnostics/ingestion`** — single-fetch ingestion
  snapshot for the region. Composes: region label, binary version,
  live ledger tip + lag, per-decoder backfill state (ranges
  total/active, oldest lag), Frankfurter / fx_quotes coverage
  (earliest/latest dates, total quotes, distinct currencies),
  market-cap cache state, supply observer coverage (classic vs
  SEP-41 counts, last snapshot age), and the full source registry
  joined with trailing-24h trades/volume/markets.
  Designed as the only call the status page makes for its
  per-region ingestion panel — operators no longer have to scrape
  `/v1/network/stats` + `/v1/sources` + `/v1/diagnostics/cursors`
  + `/v1/version` and reconcile by hand. New storage helpers:
  `FXCoverageStats`, `SupplyCoverageStats` (one query each, ~1ms
  on populated tables). Cache: `public, max-age=15`.

### Fixed

- **`/assets/{slug}` for catalogue slugs (`usdc`, `chinese-yuan`,
  `btc`, …) now renders the real cross-chain view instead of the
  "Asset not found" fallback.** The page's `fetchGlobalAsset`
  was firing a per-slug `/v1/assets/{slug}` request at build
  time, just like `[network]` was before its consolidation —
  with ~1000 prerendered routes that storm tripped r1's anon
  rate limit and every catalogue page baked in the not-found
  fallback. Extracted the catalogue source to
  `web/explorer/src/app/assets/catalogue.ts` (shared module,
  single `/v1/assets/verified` call, memoised promise, 429-aware
  retry). Both `[slug]` and `[slug]/[network]` now read from the
  same map.
- **`/assets/{slug}` and `/assets/{slug}/{network}` now resolve in
  both case variants** for catalogue entries. Previously only the
  uppercase form (`/assets/USDC/`) was prerendered because dedup in
  `generateStaticParams` picked first-seen casing, so user-typed
  lowercase URLs (`/assets/usdc/`) and any links pointing at the
  catalogue's canonical lowercase slugs returned 404. Now both
  cases get a route per catalogue entry; non-catalogue Stellar
  assets keep their listing casing as before.
- **`/assets/{slug}` for verified-catalogue currencies now renders
  the cross-chain identity view, not the Stellar-issuer view.**
  The dispatcher used to fall through to AssetDetail (with the
  IssuerPanel) whenever `/v1/coins` returned a row, even when
  the slug also matched a catalogue entry. Result: `/assets/USDC/`
  was showing Circle's Stellar issuer detail instead of the
  cross-chain page. The `[network]` route (`/assets/USDC/Stellar/`)
  is now the only place per-issuer detail lives. Title +
  description for catalogue slugs now use cross-chain framing
  (`USDC — Stablecoin`) instead of Stellar-only framing.

### Changed

- **Ansible template now bakes in `anon_rate_limit_per_min = 600`
  / `key_rate_limit_per_min = 6000`.** Codifies the live r1 bump
  applied 2026-05-13. The prior defaults (60 / 1000 per min) were
  too tight for any consumer doing a static build or dashboard
  refresh from a single IP — the explorer Cloudflare Pages build
  was the canary.

### Fixed

- **Explorer build no longer 429s on `/assets/[slug]/[network]`
  prerender.** Next.js opts out of its built-in fetch dedup when
  `signal` is set, so each prerendered slug+network page was
  separately re-fetching `/v1/assets/{slug}` and the build was
  firing hundreds of requests in parallel — far above r1's
  anonymous-tier rate limit (60 req/min). Result: every
  `[slug]/[network]` route prerendered as a "Not found" page on
  prod. Fix consolidates the catalogue source: a single
  `/v1/assets/verified` call (with 429-aware retry) populates a
  module-level Map from which both `generateStaticParams` and
  per-page `fetchGlobalAsset` read. Concurrent r1 config bump
  (`anon_rate_limit_per_min = 600`, `key_rate_limit_per_min =
  6000`) gives real consumers headroom too — the prior 60/min
  was unworkable for any client doing a static build or
  dashboard refresh.

## [v0.5.0-rc.50] — 2026-05-13

### Documentation

- Wave-103 reconciler-drift catch-up. The codex reconciler's
  fresh pass post-wave-102 re-opened XFI-0060, XFI-0061 (its
  CMD-0191 check appears to have run against an earlier
  workspace state and observed the pre-wave-102 prose) and
  failed to flip R-1209 / R-1266 / R-1267 after the underlying
  findings closed. Verified the wave-102 source-side fixes are
  still in place (`configs/prometheus/rules.r1/README.md` says
  `rules.r1/`; `configs/audit/README.md` describes
  `_unattributed` only as historical context), then flipped the
  five stale rows. Lesson reinforced: trust the
  per-row-status grep against the underlying file, not the
  reconciler's `Failure Modes` prose.
- Wave-102 audit-doc-rot sweep — closes the new findings the
  reconciler surfaced after my prior wave-98/100 stop ("any
  additional codex findings?" was a real question, not a
  rhetorical one):
    - **F-1211 reopened** (the wave-57 fix missed 4 surfaces).
      Updated `CLAUDE.md` repo-map, `launch-readiness-backlog.md`
      L4.11 row, `launch-task-list.md` G4 entry, and
      `deploy/comms/{README,incident-update}.md` to describe the
      shipped `web/status/` + `internal/incidents/data/` Markdown
      corpus instead of the retired Upptime/cstate workflows.
    - **F-1264** — `configs/prometheus/README.md` +
      `configs/loki/README.md` no longer claim "no firewall"
      and "publicly reachable"; updated to reflect nftables
      default-drop on R1.
    - **F-1265** — `configs/alertmanager/README.md`,
      `configs/alertmanager/alertmanager.r1.yml` header, and
      `configs/ansible/roles/prometheus/README.md` switched to
      the `page/ticket/informational` severity ladder the
      Ansible template actually uses (was incorrectly described
      as `critical/warning/info`).
    - **F-1266** — `configs/ansible/README.md` now lists all
      five roles (`haproxy`, `loki`, `patroni`, `prometheus`,
      `redis-sentinel`) instead of claiming `archival-node` is
      the only one; the Promtail-TODO note carried forward.
    - **F-1267** — `configs/healthchecks/README.md`,
      `configs/healthchecks/install.sh` comment, and
      `docs/operations/pre-launch-hardening.md` updated from
      "four Checks" to "five Checks" (the SLA-probe timer
      joined the heartbeat fleet).
    - **F-1268** — `configs/prometheus/rules.r1/README.md`
      `scp` target corrected from `/etc/prometheus/rules.d/` to
      `/etc/prometheus/rules.r1/` (matches the active
      `prometheus.r1.yml` include path).
    - **F-1269** — `configs/audit/README.md` no longer promises
      an `_unattributed` block that the YAML hasn't contained
      since the 2026-05-01 testnet-address cleanup.
  Findings register + XFI table flipped accordingly: 8 new
  fixes across the two surfaces.

### Added

- New `internal/obstest` package centralising the
  `HistogramSampleCount` helper that was duplicated four times
  across the wave-92/93/94/95 regression-test series. Each
  duplicate prior to this wave was a 20-line dto.Metric reader
  required because `prometheus.HistogramVec.WithLabelValues(...)`
  returns an Observer (not a Collector), so `testutil.CollectAndCount`
  can't act on the per-label child directly. Each successive
  wave's commit message escalated the duplication note ("third
  copy", "fourth copy makes the duplication cost obvious"); at
  the fourth copy I argued in-line that I'd consolidate "if the
  cost becomes painful". Wave 100 makes the call. The package
  depends only on upstream prometheus libraries, so it's
  import-safe from every test package. Per-test helper bodies
  removed; tests now import `obstest.HistogramSampleCount` with
  an explicit `(labelKey, labelValue)` signature that's more
  general than the original `outcome`-hardcoded shape.
- Regression test
  `TestRunSupplyRefresh_DurationMetricRecorded` pins the wave-90
  supply-refresh latency-histogram wiring, closing the deferred
  item from wave 94. Shipped via the existing
  `cmd/ratesengine-aggregator/main_test.go` (which I had
  forgotten existed at wave-94 time) — no refactor of wave 90
  required after all. The test pre-cancels the context before
  calling `runSupplyRefresh` so the immediate first tick runs
  once and the for-loop sees ctx.Done() and returns. Wave-88-91
  histogram quartet is now 4-of-4 test-pinned.
- Regression test
  `TestRecovery_SweepDurationMetricRecorded` pins the wave-91
  freeze-recovery-sweep latency-histogram wiring end-to-end.
  Same shape as wave 92/93. The wave-90 supply-refresh
  histogram is not yet test-pinned because it lives in the
  aggregator main-package wrapper rather than `internal/supply`;
  testing it cleanly would require either duplicating the
  wrapper into `internal/supply` or moving the timing into
  `Refresher.Tick` itself — both are wave-90 refactors rather
  than test additions, so deferred.
- Regression test
  `TestRefreshDivergenceAll_DurationMetricRecorded` pins the
  wave-89 divergence-refresh latency-histogram wiring end-to-end.
  Same shape as the wave-92 customer-webhook test; reuses the
  `histogramSampleCount` helper pattern (re-implemented locally
  in the orchestrator package since cross-package test helpers
  aren't worth the import-cycle risk for a 20-line helper).
- Regression test
  `TestWorker_DeliveryDurationMetricRecorded` pins the wave-88
  customer-webhook latency-histogram wiring end-to-end —
  asserts a successful delivery produces a sample on
  `ratesengine_customer_webhook_delivery_duration_seconds{outcome="delivered"}`.
  Without this test, a future refactor could silently delete the
  timing call without any signal (the existing
  `TestWorker_DeliversOn2xx` asserts the counter side but not the
  histogram). Includes a small `histogramSampleCount` test helper
  that reads the underlying `dto.Metric` via the parent vector's
  Collect, since `WithLabelValues(...)` on a HistogramVec returns
  an Observer (not a Collector) so `testutil.CollectAndCount`
  can't act on it directly.
- New observability metric
  `ratesengine_anomaly_freeze_recovery_sweep_duration_seconds`
  (Histogram, label `outcome`). Final wave-88/89/90/91 entry in
  the IO-heavy goroutine-worker latency-histogram series. Pairs
  with `_sweeps_total`; surfaces Postgres / Redis pressure as a
  chartable signal before the freeze_events table accumulates
  open rows the operator UI would show as permanently firing.
  Sweep latency scales with open-row count (each row = one
  Redis GET + maybe one Postgres MarkRecovered). Wired in
  `internal/aggregate/freeze/recovery.go::tick`. Buckets 10 ms →
  30 s. No alert wired (existing recovery-sweep error counter
  covers correctness).
- New observability metric
  `ratesengine_aggregator_supply_refresh_duration_seconds`
  (Histogram, label `outcome`). Same wave-88/89 pattern applied
  to a third worker — the supply.Refresher.Tick goroutine.
  Pairs with the existing per-asset_key `_total` counter; this
  histogram intentionally drops the `asset_key` label to keep
  cardinality bounded on deployments watching many assets
  (operators correlate per-asset latency via the per-tick log
  timestamps when needed). Steady-state ~50-200 ms per tick;
  buckets span 10 ms → 30 s. Wired in
  `cmd/ratesengine-aggregator/main.go::runSupplyRefresh`. Closes
  the latency-gap on the third (and final) operationally-meaningful
  IO-heavy goroutine worker after the wave-88 customer-webhook
  delivery and wave-89 divergence refresh metrics.
- New observability metric
  `ratesengine_divergence_refresh_duration_seconds` (Histogram,
  label `outcome`). Per-pair divergence-refresh latency; pairs
  with the existing `_total` counter (counter says how often /
  whether successful, histogram says how long). The natural
  failure mode of `RefreshPair` is "one external vendor's API
  is slow and the refresh tick stretches" — invisible before
  this metric. Operators can now chart `ok` p95/p99 separately
  to detect vendor slowdown without a `refresh_error` outcome.
  Buckets span 10 ms → 30 s. Wired in
  `internal/aggregate/orchestrator/divergence_refresh.go` to
  time the full per-pair attempt (cache lookup + parse +
  HTTP fan-out). Same wave-88 pattern applied to a different
  worker.
- New observability metric
  `ratesengine_customer_webhook_delivery_duration_seconds`
  (Histogram, label `outcome`). Latency of the outbound HTTP
  POST inside the customer-webhook delivery worker — closes
  the equivalent observability gap on the OUTBOUND side that
  the wave-65 Stripe-bridge metric closed on the INBOUND side.
  The standard `http_request_duration_seconds` covers the
  inbound HTTP handler surface but not goroutine workers, so
  before this metric there was no way to chart per-outcome
  p95/p99 latency on the outbound delivery path. Wired in
  `internal/customerwebhook/worker.go::deliverOnce` to time
  the `HTTPClient.Do(req)` + body-drain. Buckets span 10 ms →
  60 s (the worker's per-request context timeout).
  `customer-webhook-delivery-failing.md` runbook Quick
  diagnosis section gets a new step pointing at the histogram
  for the "delivered p99 climbing while failing-rate stays
  green" case (customer endpoint going slow rather than
  failing). No new alert wired — the existing
  `ratesengine_customer_webhook_delivery_failing` covers the
  failing-rate signal; latency degradation surfaces in the
  dashboard.
- New CI check: `scripts/ci/lint-docs.sh` now enforces that every
  alert runbook carries `## At a glance` and `## Related` sections
  (the two universally-required sections per the wave-78 template
  + wave-81 normalisation). Procedural runbooks excluded via an
  allow-list mirroring the existing orphan-lint exclusions plus
  three procedural runbooks (`dr-activation`,
  `sev-status-page-update`, `operator-unblock-2026-05-08`)
  flagged as not-alert-shaped during the wave-81 survey. Closes
  the long-stale `TODO(#0)` claim that was originally in the
  template — the lint that the template's CI-check claim
  promised now actually exists.

### Documentation

- `docs/audit-2026-05-12-codex/07-remediation-plan.md` reconciled
  against the findings register: 20 remediation rows flipped from
  `open` to `fixed` (5 with rich closure prose written inline, 15
  via batch status flip). The remediation-plan was N waves behind
  the register because the reconciler agent's runs sync the
  register without touching the remediation plan. Counts now
  align: 14 still-open R-rows = 13 still-open findings + R-1206's
  mixed F-1208/F-1210 status. The remaining-open rows are all
  genuinely operator/admin work (R1 firewall, sla-probe.timer,
  branch protection UI, Dependabot UI, capacity triage, signup-
  verify config flip, R1 deploy-lag deploys, backfill supervised
  job).
- `docs/operations/runbooks/api-5xx.md` Step 3 of "B. Specific
  endpoint family broken" carried a stale `(TODO(#0) — runbook
  in flight)` qualifier next to its `dr-activation.md` cross-
  reference. The runbook actually shipped (status: `ratified`)
  with the wave-23 incremental work; an operator following step
  3 in the middle of an incident shouldn't see "runbook in
  flight" prose against a runbook that's been ratified for ~10
  days. Updated to a clean cross-reference + reframed
  ha-plan.md §2.2 as the underlying reference the dr-activation
  runbook builds on. last_verified bumped to 2026-05-13.
- Wave-85 hostname-bug sweep widening: a fresh grep on
  `app.ratesengine.net` found three more places where the
  explorer was misattributed to the dashboard's hostname:
  `web/explorer/README.md` v1-fallback paragraph (rsync target +
  Cloudflare proxy URL), `docs/architecture/explorer-implementation-plan.md`
  showcase-domain row, and `docs/architecture/explorer-data-inventory.md`
  Domain (proposed) field plus an iframe embed example. Every
  occurrence corrected to `ratesengine.net` (or in the
  implementation-plan row, kept as a parenthetical pointing
  out the dashboard lives at the separate `app.ratesengine.net`
  with a cross-reference to web/dashboard/README.md). The embed
  example also migrated from `coin=stellar&quote=usdc` shape
  to the canonical `asset=native&quote=fiat:USD` shape (the
  rc.48 /v1/coins removal made the old query-param shape stale
  too).
- `web/explorer/README.md` factually corrected. Two real bugs:
  (1) the README claimed the explorer lives at
  `app.ratesengine.net` — wrong; that's the dashboard's
  hostname (per CLAUDE.md, the OpenAPI generated comment, and
  `web/dashboard/README.md`). Explorer lives at
  `ratesengine.net`. (2) The README's "scaffold + everything is
  a stub; real panels arrive at Phase 7" framing was three
  rcs out of date — reality is 50+ shipped routes plus the
  R-018 phases 1.1-1.5 verified-currency catalogue work. The
  Layout section was likewise stale (cited `panels/` and
  `mdx/` directories that don't exist; cited
  `lib/url-state.ts`, `lib/time-pin.ts`, `lib/slugs.ts` —
  none of which exist; reality is `adr.ts`, `architecture.ts`,
  `blog.ts`, `changelog.ts`, `discovery.ts`, `fiat-slugs.ts`,
  `format.ts`, `markdown.tsx`, `operations.ts`, `seo.ts`).
  Layout section rewritten to match `src/` actual contents.
- `docs/architecture/explorer-implementation-plan.md` row 5.1
  ("Coin endpoints") updated to reflect the rc.48 reality —
  endpoints renamed from `/v1/coins`-shape to `/v1/assets`-shape,
  with a parenthetical note explaining the original naming +
  cross-reference to `coins-to-assets-migration.md`. Row label
  changed from "Coin endpoints" to "Asset endpoints" to match.
  last_verified bumped to 2026-05-13.
- `docs/architecture/coins-to-assets-migration.md` marked
  `complete` (was `in progress`) with a top-of-file callout
  noting that the rc.48 cut removed both `/v1/coins` and
  `/v1/currencies` entirely. Adds a pointer to the OpenAPI spec
  + the GlobalAssetView vs AssetDetail discriminator pattern
  (CLAUDE.md surprise list) for readers landing here looking for
  the current contract. The doc is preserved as a record of how
  the migration was done.
- Four alert runbooks normalised to the wave-78 template shape
  (`## At a glance` + `## Related` are universally required —
  these four were the only alert-shaped runbooks missing one or
  both):
    - `external-poller-stale.md` — was the most-rotten of the
      four (no frontmatter at all + idiosyncratic `**Alert:**`
      bold-line shape). Added frontmatter, At-a-glance table,
      cross-reference to the wave-70 vendor-specific 429 matrix
      in `external-poller-error-rate-high.md`. Section names
      normalised: `What it means → Symptoms`,
      `Triage → Quick diagnosis`,
      `Common scenarios + fixes → Mitigation`,
      `Related runbooks → Related`. The wave-65 `Why this
      exists` section already existed; preserved verbatim.
    - `fx-history-missing.md` — added At-a-glance table.
    - `redis-write-blocked-disk-full.md` — added At-a-glance
      table including the alert pairing
      (`ratesengine_aggregator_cache_write_errors`).
    - `supply-snapshot-never-initialized.md` — renamed
      `## See also` → `## Related` and expanded entries to
      include the `internal/supply/refresher.go` implementation
      back-link plus the F-1236 `OutcomeKindMissingFreshness`
      cross-reference.
- F-1204 sweep widened (wave 80) — found seven more operator-
  facing surfaces still referencing the rc.48-removed `/v1/coins`
  and `/v1/currencies` routes after the original close (which
  only fixed `audit-public-api.sh` and `llms.txt`). Updated:
  `configs/example.toml` (CORS public-read example),
  `docs/operations/cdn-setup.md` (CDN policy table),
  `docs/operations/runbooks/fx-history-missing.md` (curl
  signal),
  `docs/operations/runbooks/supply-snapshot-never-initialized.md`
  (impact text + diagnosis curl),
  `docs/operations/sac-wrappers-and-usd-volume.md` (asset_id
  format note),
  `docs/operations/post-launch-queries.md` (catalogue surface
  list × 2),
  `docs/operations/perf-todo.md` (perf table row removed).
  Operators following any of these surfaces no longer hit
  404s on routes that haven't existed since rc.48.
- `docs/operations/runbooks/_template.md` refreshed against
  patterns established across the wave-65, -70, -71, -73, -75
  runbook series. Removes the long-stale `TODO(#0)` claim that
  CI enforces every section ("the lint exists for orphans + alert
  pairing, not for section presence — universally-required is
  just `## At a glance` + `## Related`"). Adds guidance for: when
  to use a per-source/per-tier reference matrix instead of a
  flat triage flow (cross-references the four runbooks that ship
  the pattern), when to add a `## Why this exists` section
  (new-metric/seam runbooks like `stripe-platform-sync-errors`),
  and the wave-75 cross-link discipline (companion runbooks
  must reciprocate the link). `status` enum extended to include
  `superseded` (matches the wave-67 status-page-hosting-comparison
  precedent). `Detected by` field hint nudges authors to name
  both the multi-host rule file AND the R1 overlay sibling.
- The `POST /v1/webhooks/stripe` OpenAPI entry now documents the
  inbound observability surface: the
  `ratesengine_stripe_platform_sync_errors_total{operation}`
  counter, the `ratesengine_stripe_platform_sync_errors` alert,
  and the `stripe-platform-sync-errors` runbook. Explains that
  the platform-store side effects (account tier / subscription
  upsert / per-key Postgres rate-limit lift) are best-effort and
  do NOT 5xx because Stripe retries against an unhealthy Postgres
  would just retry-storm — the metric is the right operator
  signal. Inbound parallel to the wave-76 outbound webhook payload
  documentation. Generated artifacts regenerated.
- OpenAPI now documents the JSON body shape of all four customer-
  webhook event types: `IncidentWebhookPayload` (covers
  `incident.sev1` + `incident.resolved`), `AnomalyFreezeWebhookPayload`
  (covers `anomaly.freeze`), and `DivergenceFiringWebhookPayload`
  (covers `divergence.firing`). The wire-shape was implicit in the
  Go producers (`cmd/ratesengine-ops/emit_incident.go`,
  `cmd/ratesengine-aggregator/main.go`) but was nowhere in the
  customer-facing OpenAPI surface — customers writing webhook
  handlers had no schema to write Zod/Pydantic/etc. validators
  against. The `DashboardWebhook.events` field description now
  cross-references the per-event payload schemas. Generated
  artifacts regenerated: `docs/reference/api/rates-engine.v1.yaml`,
  `examples/postman/rates-engine.postman_collection.json`,
  `web/explorer/src/api/types.ts`.
- `web/status/README.md` — new top-level README for the public
  status page, matching the convention already established by
  `web/explorer/README.md` and `web/dashboard/README.md`. The
  wave-67 supersede note on `status-page-hosting-comparison.md`
  pointed future readers at this README as the shipped-
  implementation reference, but the file didn't actually exist.
  Documents the stack, the incident-corpus dual-loader pattern
  (build-time loader for the static export + Go `go:embed` for
  the API binary's `/v1/incidents` + customer-webhook fan-out
  at `incident.sev1` / `incident.resolved`), the authoring
  procedure (cross-references the SEV runbook), and the local
  dev / build / deploy commands.
- `runbooks/source-stopped.md` adds a new "Per-source cadence
  reference" matrix listing the expected emission cadence + the
  expected idle cap (upper bound on a normal silent stretch) +
  the first-look-when-stopped surface for each of the 11 active
  sources. Operators paged on a stopped source can now check
  whether the silence is within the source's normal off-peak
  window before treating it as a real stop. Covers the off-peak
  prone Soroban DEXes (Phoenix / Comet / Aquarius / Blend), the
  zero-event Band relay, the three Reflector contracts at their
  different cadences, the four CEX WebSocket streamers, the CG
  poller cooldown semantics, and ECB's once-per-business-day
  fiat fix. Cross-references the wave-70 CG runbook section.
- `runbooks/decode-errors.md` adds a new "Per-source quick
  reference" section pulling the per-source decode-regression
  surprises from CLAUDE.md and `docs/discovery/dexes-amms` /
  `docs/discovery/oracles` into a single operator-facing matrix.
  Covers the seven sources whose ingest path has a known wire-
  level surprise that operators routinely re-discover the hard way
  (Soroswap SwapEvent/SyncEvent split, Phoenix 8-events-per-swap,
  Comet shared topic, Reflector triple-contract + missing twap,
  Band zero-events + E18-vs-E9 scale, Redstone missing feed_id in
  body + OpArgs plumbing, SDEX P23 unified events, SEP-41 transfer
  i128-or-map). Cross-references the WASM-audit + contract-schema-
  evolution docs for backfill-safety implications.
- `runbooks/external-poller-error-rate-high.md` adds a new
  "Vendor-specific 429 patterns" section covering CoinGecko's
  three pricing tiers (public / demo / Pro), the post-2024
  403-as-429 behaviour, the in-binary cooldown semantics
  (`MinBackoff = 60s`, `MaxBackoff = 1h`, exponential, honours
  `Retry-After`), quick-diagnosis commands for the R1 host, and
  three ranked common causes. Closes the CoinGecko-half of the
  audit's F-1208 follow-up; the remaining per-source triage stays
  operator-only.
- `configs/example.toml` now documents three audit-driven config
  flags that previously existed only in `internal/config` struct
  tags: `[storage].redis_username` (F-1213, the named ACL user
  required when `redis_acl_lockdown` is enabled in the ansible
  role), `[api].signup_require_email_verification` (F-1218, opts
  the deployment into the verified-email gate after the rollout
  window), and `[supply].strict_freshness_required` (F-1236,
  rejects supply snapshots without a `MinComponentLedger` anchor).
  Each entry explains the default + when to flip it. Operators
  reading the example config no longer have to grep `internal/`
  to discover the audit's policy levers.

### Added

- `verify-launch-ready` gains a `-skip-ids` flag that ignores
  the listed row IDs when computing the engineering-ready
  verdict. New `make verify-launch-ready-single-region` Makefile
  target bakes in the project's "live-in-development on R1, no
  consumer traffic yet" posture by skipping L4.14-17 (R2/R3 +
  DNS + Patroni) plus L5.6 (external security review) and L5.8
  (region-failover chaos) — all of which gate on the multi-day
  multi-region bringup or the external auditor. The default
  `make verify-launch-ready` (and CI) still gates on the full
  multi-region surface; the new target is for confirming "the
  R1 engineering surface is operator-deploy-ready" without
  conflating it with multi-region readiness. Backlog doc updated
  with both gate semantics. F-1206 audit closure note refreshed.
- New observability metric `ratesengine_stripe_platform_sync_errors_total{operation}`
  surfacing failures in the Stripe webhook's platform-store side-effects path
  (the F-1219 fan-out into Postgres `accounts` / `subscriptions` /
  `api_keys`). Webhook deliberately does not 5xx on these failures — Stripe
  retries would not heal Postgres — so the metric is the operator-visible
  signal that customer dashboard / Postgres key state is drifting from
  Stripe billing state. Per-`operation` label isolates the failing layer
  (`get_account` / `upsert_subscription` / `account_update` / `list_keys` /
  `key_update`). Wired through five failure sites across
  `applyPlatformSideEffects`, `handleStripeSubscriptionEvent`, and
  `handleStripeInvoicePaid`. New `ratesengine_stripe_platform_sync_errors`
  alert in both `deploy/monitoring/rules/api.yml` (multi-host) and
  `configs/prometheus/rules.r1/api.yml` (R1 overlay), plus runbook at
  `docs/operations/runbooks/stripe-platform-sync-errors.md`. Closes the
  long-standing TODO from F-1219 wave 32.

### Fixed

- F-1243 (codex audit-2026-05-13, wave 64) — closes the long-
  standing duplicate-replay evidence gap on the classic-asset
  registry. New `timescale.ResetAssetRegistryDedupeForTest`
  helper + `test/integration/asset_registry_replay_test.go::
  TestAssetRegistry_DuplicateReplayDoesNotMutateCounters` proves
  end-to-end (against testcontainers-go Timescale) that replaying
  a stored trade with a cold dedupe cache — the simulated-
  process-restart shape the audit specifically called out — does
  NOT advance `classic_assets.observation_count` or
  `last_seen_*`, while a distinct trade on the same asset does.
  Source-side guards from waves 47 + 51 (TTL dedupe + RowsAffected
  short-circuit) were already correct; this lands the closure-
  grade proof. F-1243 → `fixed`; XFI-0035 → `fixed`.

## [v0.5.0-rc.49] — 2026-05-12

audit-2026-05-12 remediation pass — 27 audit findings closed in
code/docs/config, 14 more verified already-resolved, plus the
pre-flip blocker F-1201 explorer migration that unblocks the
rc.48 deploy to R1.

### Fixed

- **Explorer migrated off `/v1/currencies` (F-1201 — pre-flip
  blocker).** rc.48 removed the `/v1/coins` + `/v1/currencies`
  HTTP surface. The explorer had eight files still making live
  calls against `/v1/currencies` — every one would 404 the
  moment rc.48 deploys to R1. Migrated:
  - `HomeCurrencies.tsx` → `/v1/price/batch?asset_ids=fiat:EUR,…&quote=fiat:USD`
    (single RT, names hardcoded for the 6-tile home strip).
  - `sitemap.ts` → `/v1/assets/verified` filtered to `class=fiat`.
  - `HomeTryAPI.tsx` → updated example paths to `/v1/assets/verified`
    + `/v1/assets/euro`.
  - `embed/currency/[ticker]/page.tsx` → `/v1/assets/{ticker}`
    (GlobalAssetView). Sparkline + 24h/7d change degrade
    gracefully to a price-only widget; chart hookup is a follow-up.
  - `AssetConverter.tsx` → `/v1/price/batch` for the FX rate table
    (inverts so the converter's `rate_usd = 1 USD = N target`
    contract stays unchanged).
  - `convert/[from]/[to]/ConvertPair.tsx` → `/v1/price/batch`
    for the live from→to rate (one pair vs the old cross_rates
    bulk).
  - `convert/[from]/[to]/page.tsx` → `/v1/assets/{from}` for
    identity + `/v1/price/batch` for the singleton cross-rate.
  - `SearchModal.tsx` → `/v1/assets/verified` filtered to fiat
    for the ticker→/currencies/X affordance.
  Zero remaining live calls to the removed routes. Typecheck +
  lint + build all green.

### Changed

- **Multi-region tooling now handles single-region operation
  gracefully (F-1234).** Pre-R2/R3-bringup only R1 is deployed.
  `scripts/dev/verify-cross-region.sh`, `ratesengine-ops
  cross-region-check`, and `ratesengine-ops cross-region-monitor`
  all used to fail with "need at least 2 regions to compare"
  even when called against the only deployed region. Operators
  who triggered them got a confusing failure and learned to
  ignore the family. Now: each command logs a one-line
  "single-region — pre-launch posture, see r2-r3-bringup.md"
  notice and exits 0. The check fires for real once a second
  region URL is supplied. Default R1 URL in
  verify-cross-region.sh points at the live public hostname
  (`api.ratesengine.net`); R2/R3 default empty.

- **R1 prometheus.r1.yml scrape coverage + rule_files path
  (F-1219 + F-1220).** Added scrape jobs for redis_exporter
  (port 9121, installed by the redis-sentinel ansible role),
  alertmanager self-scrape (so we can alert on alertmanager
  being down), postgres_exporter / pgbackrest_exporter
  placeholder slots (operator deploys the exporters, scrape
  picks them up), and minio cluster metrics with bearer-token
  auth path. Each job has a one-line comment naming the alert
  family it feeds. `rule_files` path changed from the empty-
  opt-in glob `/etc/prometheus/rules.d/*.yml` to the canonical
  `/etc/prometheus/rules.r1/*.yml` matching the deployed-asset
  path so operators no longer have to symlink the full
  configs/prometheus/rules.r1/ set into a parallel directory.

- **Prometheus multi-host ↔ R1 overlay drift caught at CI (F-1222).**
  Multi-host rules in `deploy/monitoring/rules/` use underscored
  job labels (`ratesengine_api`) matching the multi-host ansible
  scrape config; R1's single-host overlay at
  `configs/prometheus/rules.r1/` uses hyphenated labels matching
  the R1 systemd units. Editing only one half silently breaks the
  other deployment shape. New header notes pin the convention in
  the canonical files; `scripts/ci/lint-docs.sh` now flags any
  multi-host rule file without an R1-overlay sibling so the gap
  surfaces at CI time. Created R1 overlays for `cache.yml`,
  `stellar.yml`, `storage.yml` to satisfy the new pairing
  check — the underlying rules already match upstream metric
  names so no expression changes were needed.

- **Tailored error for supply-observer backfill attempts (F-1243).**
  `ratesengine-ops backfill accounts` (or any of the six supply
  observers: `accounts`, `trustlines`, `claimable_balances`,
  `sac_balances`, `sep41_supply`, `liquidity_pools`) used to fail
  with the generic "WASM-hash audit pending" error — misleading,
  since supply observers aren't Soroban price/oracle sources at
  all. They plug into different dispatcher hooks
  (`LedgerEntryChange` / `OpDecoder` / SEP-41) and have no
  historical replay path through this command. New
  `checkBackfillSources` helper distinguishes the two cases and
  emits a supply-observer-specific message pointing operators
  at the supply-snapshot timer (or a future supply-backfill
  command for SEP-41 windows). 3 new unit tests cover the
  closed name set, the tailored error path, and the unchanged
  WASM-audit gate.

### Added

- **Customer-webhook delivery worker wired into the API binary
  (F-1270 follow-up).** The worker that drains the
  `webhook_deliveries` queue (HMAC sign + POST + retry) now runs
  as a goroutine in `cmd/ratesengine-api/main.go` whenever the
  dashboard surface comes up (Postgres reachable). Pre-this:
  operator had to launch the worker as a separate process per
  the docblock "operator-launched via internal/customerwebhook.New".
  Single-binary deploy now does it inline — same context, same
  lifecycle, same logger; one less ansible task.

- **Customer-webhook delivery alerts + runbook (F-1270 follow-up).**
  Two new Prometheus alerts wired into both the multi-host and R1
  rules: `_delivery_failing` (P3) fires when 5xx + network-error
  attempts exceed 0.1/s for 15+ min (single-customer outage);
  `_delivery_exhausted` (informational) fires when a delivery hits
  the 15-attempt retry budget. New `customer-webhook-delivery-failing.md`
  runbook covers the SQL to identify the failing webhook, the
  customer-outreach template, and the worker-vs-customer triage
  tree. Catalogued in `alerts-catalog.md` so operators see them
  alongside the rest of the API alerts.

### Tested

- **`internal/usage` package gains unit-test coverage.** The
  per-subject daily usage counter (Redis-backed) was the one
  remaining no-test package in `internal/`. 8 tests cover the
  Increment/Read round-trip, day-boundary handling, empty-subject
  no-op, retention clamp, key-prefix isolation, URL-encoded
  subjects (so `:` inside IPv6 addresses doesn't collide on the
  date separator), and the 35-day retention TTL applied on every
  key.

### Documented

- **ADR-0012 placeholder (F-1262).** Filled the numeric gap in
  `docs/adr/` — 0011 jumped to 0013 with no file at 0012, even
  though `docs/adr/README.md` had listed the slot as `Planned`
  (reserved for Quorum-set composition per ADR-0004 Phase 3)
  since the initial audit. The placeholder documents what the
  future ADR must cover (third-party validator selection,
  HALT-LIVE-DROP scoring, cross-region quorum overlap, stellar-
  core `[QUORUM_SET]` thresholds) and what invariants it must
  preserve (Tier-1 independence, no self-included validators,
  ≤ 33% effective weight per validator). README index now links
  to the file.

- **Dashboard surface bypasses the v1 envelope on purpose (F-1235).**
  `/v1/dashboard/keys*` handlers write bare JSON rather than the
  `data` / `as_of` / `flags` envelope used by market-data
  endpoints. Documented the rationale in
  `docs/reference/api-design.md §4.1`: different audience
  (dashboard React app, not SDK), session-scoped data (no
  market-quality flags to carry), distinct auth path (session
  cookie vs API key). Future contributors won't "fix" the
  perceived drift. RFC 9457 problem responses, Cache-Control: no-
  store, and X-Request-Id correlation are preserved.

### Fixed

- **Guard test for `flags.stale=true` on every fallback path (F-1254).**
  The stale-flag fix in `internal/api/v1/price.go:287-299` (the
  May-10 SEV-2 lesson — "fallback chain is itself the staleness
  signal") had no regression test. New
  `TestPrice_FallbackChainSetsStaleFlag` covers both the
  triangulated and direct-rewrite fallback paths and asserts
  `flags.stale=true` on each — so a future change that re-clears
  the flag is caught immediately.

- **API reference doc drift after rc.48 (F-1246).** Regenerated
  `docs/reference/api/rates-engine.v1.yaml` to match the current
  OpenAPI source — three residual `/v1/coins` references in the
  generated file (an `?issuer=` description, the home-page
  summary, and an error-envelope `instance` example) lingered
  after rc.48 removed the route. Pure regen; OpenAPI source was
  already clean.

- **Postman collection drift (F-1247).** `make docs-postman` now
  writes to the customer-facing canonical at
  `examples/postman/rates-engine.postman_collection.json` —
  previously it wrote to a gitignored `docs/reference/api/...`
  path, so the tracked customer copy drifted silently every time
  the OpenAPI spec moved. The docs-site build pipeline runs its
  own regeneration; the in-repo file is for customers who clone
  the repo to import the collection. README + Makefile docstring
  updated; canonical refreshed (656k bytes).

- **`classic_assets.first_seen_*` ordering bug under chunked
  backfill (F-1239).** The ON CONFLICT clause in
  `registerClassicAssetSeen` previously updated only `last_seen_*`
  (GREATEST) and `observation_count` — leaving `first_seen_*`
  pinned to whichever ledger first hit the row, regardless of
  actual chronology. Out-of-order parallel backfill (chunked
  ranges processed in parallel) could leave `first_seen_ledger`
  higher than the asset's true first observation. Fix: also
  update `first_seen_*` with `LEAST(existing, incoming)`. Idempotent
  for forward ingest (incoming is always ≥ existing → no-op).

### Removed

- **Unused GIN indexes on `blend_auctions.bid` / `.lot` (F-1238).**
  Migration 0029 drops the two JSONB GIN indexes from migration
  0009. No reader in `internal/storage/timescale/` queries those
  columns by content — `LatestBlendAuctionEvent` and
  `ListBlendPools` both filter only by `pool` /`auction_type` /
  `user_address` / `ts`. Index write-amplification on every
  blend-auction INSERT for a read path that never materialised.
  Down migration restores them.

### Changed

- **`ratesengine_ingestion_source_stopped` alert window widened
  to 30m × 15m (F-1212b).** The pre-existing 5-min window
  produced routine false positives on low-volume Soroban / FX
  sources (blend auctions, ECB FX dailies, Band oracle pushes,
  Comet pool swaps, Phoenix off-peak windows). On R1 this
  manifested as 5 simultaneous ticket-tier alerts at any given
  time; operators learned to ignore the family. The new window
  waits past the natural quiet-window cadence for these sources;
  total-outage coverage stays tight via the separate 3-min
  `ratesengine_ingestion_all_sources_stopped` (P1). Rule updated
  in both `deploy/monitoring/rules/ingestion.yml` and
  `configs/prometheus/rules.r1/ingestion.yml`. Runbook + alerts
  catalog updated to reflect the new threshold and rationale.

### Documented

- **RFP F4.2 one-year retention catch-up procedure (F-1265).**
  `docs/operations/backfill-procedure.md` gains a new section
  walking through the 1-year catch-up backfill needed to meet
  Freighter RFP F4.2's ≥1y retention commitment. Covers
  resolving the target ledger window from the Galexie archive
  manifest, sanity-checking upstream archive completeness, row-
  count estimation, the chunked-by-week run loop with `-resume`
  so a mid-chunk crash doesn't re-do 12 hours of work, the CAGG
  force-refresh sequence, and a `/v1/chart?timeframe=1y`
  verification step. Pre-flip operator step; the code path is
  unchanged.

### Added

- **R1 TOML supply.watched_* defaults (F-1266).** The
  `archival-node` ansible role's `ratesengine.toml.j2` template
  gains a `[supply]` block with sensible launch-day defaults:
  `watched_classic_assets` populated with the top Stellar-classic
  verified currencies (USDC / EURC / AQUA / yXLM / VELO / BLND /
  PHO / KALE) mirroring `internal/currency/data/seed.yaml`. Plus
  inventory-overridable knobs for `watched_sep41_contracts`,
  `sdf_reserve_accounts`, and `reserve_balances_stroops`.
  Pre-F-1266: R1's TOML had no supply block → every F2 field
  (`market_cap_usd`, `fdv_usd`, `circulating_supply`,
  `total_supply`, `max_supply`) returned NULL even though the
  code path is correct. The next `archival-node` role run will
  flip every one of those fields from NULL to a real value for
  the 8 watched currencies.

- **Opt-in Redis ACL lockdown template (F-1213).** Closes the
  pre-flip Redis-ACL gap on R1 by codifying a narrow ACL config
  in the redis-sentinel ansible role. New `redis_acl_lockdown`
  flag (default false for backward compat) renders
  `templates/users.acl.j2` to `/etc/redis/users.acl`, references
  it from `redis.conf.j2` via `aclfile`, and:
  - Disables the legacy `default` user (`off nopass nocommands`)
    so no password-less access path remains.
  - Creates a `ratesengine` application user with
    `+@read +@write +@scripting +@pubsub +@connection` minus the
    `@admin` + `@dangerous` families, scoped via `~prefix:*` to
    exactly the cache key prefixes the application uses (vwap,
    confidence, freeze, div, ratelimit, signup-ip, toml, meta,
    price, apikey, health, oracle, subscriber) plus the pub/sub
    channels (`&closed-bucket-*`, `&stream-*`).
  Application binaries get a new `[storage].redis_username` TOML
  key (default empty = legacy path; operators set it to
  `ratesengine` when they flip the lockdown). `redisclient.Build`
  threads it into both the FailoverClient and single-node code
  paths. Commented-out per-component (`re_aggregator` /
  `re_api` / `re_indexer`) users in the template show the
  follow-on split when operators add per-binary passwords.

- **L2.2 Phase 2 FX-anchor USD volume coverage (F-1268).** New
  `timescale.VWAPUSDFXResolver` implements the pre-existing
  `USDVolumeFXResolver` interface against the `prices_1m`
  CAGG: for any on-chain quote asset not already on the
  operator's `[trades].usd_pegged_classic_assets` list, the
  resolver looks up `<quote>/<USD-peg>` at the trade's
  timestamp and supplies a per-minute-bucket-cached USD rate
  that `tradeUSDVolume` multiplies through. Pre-Phase-2: only
  CEX/FX + operator-allow-listed pegs contributed to
  `volume_24h_usd`; an EURC/XLM Soroswap trade contributed 0
  even though we had a fresh EURC/USDC VWAP one minute earlier.
  Now it inherits USD value through the peg chain. Wired
  alongside the Phase 1 quote spec in
  `cmd/ratesengine-indexer/main.go` whenever
  `usd_pegged_classic_assets` is non-empty (no new config
  knob). 7 unit tests cover defaults, cache hits, negative
  cache, TTL expiry, minute-bucket key stability. The
  AssetDetail `volume_24h_usd` docstring rewritten to
  document the three-tier coverage chain (Phase 1 off-chain
  + Phase 1 on-chain pegs + Phase 2 FX-anchor).

- **Customer-facing dashboard webhook CRUD handlers (F-1270
  complete).** New `internal/api/v1/dashboardwebhooks` package
  mounts five routes: GET/POST/PATCH/DELETE
  `/v1/dashboard/webhooks` + GET
  `/v1/dashboard/webhooks/{id}/deliveries`. Session-gated,
  role-gated (Owner/Admin/Member create; Viewer/Billing 403),
  cross-account 404 (no existence-leak), 10-per-account quota,
  HTTPS-only URLs, closed-set event validation, secret returned
  ONCE on create. OpenAPI spec adds 5 paths + 5 schemas;
  postman + api docs regenerated. 8 unit tests cover happy
  path, 401, 403, malformed URL, unknown event, quota, list
  scoping, cross-account delete. Wired into the v1 server via
  the same `DashboardAuthMounter` pattern as keys, and into
  main.go's `buildDashboardBundle` so the handlers come up
  whenever Postgres is reachable.

- **Customer-webhook delivery worker (F-1270 close-out).**
  New `internal/customerwebhook` package drains the queue the
  store wrote in the prior commit: poll-loop drains
  ListPendingDeliveries, HMAC-SHA-256 signs the payload,
  POSTs to the customer URL with `X-RatesEngine-Signature` +
  `X-RatesEngine-Event` + `X-RatesEngine-Delivery-Id` headers,
  marks delivered on 2xx, schedules retry on 5xx/network
  (exponential backoff 30s → 1h cap, 15-attempt budget),
  terminates on 4xx / disabled-webhook / missing-webhook /
  malformed-URL. New
  `ratesengine_customer_webhook_delivery_attempts_total`
  counter labelled by 10 outcomes; documented in metrics ref
  with two alert recipes. 5 unit tests cover the happy path,
  5xx-retry, 4xx-terminal, disabled-webhook, missing-webhook.

- **`postgresstore.WebhookStore` customer-webhook data plane
  (F-1270 partial).** Implements the existing
  `platform.WebhookStore` interface against the
  `customer_webhooks` + `webhook_deliveries` tables from
  migration 0027: Create / Get / List / Update / Delete on the
  registry; Enqueue / ListPending / MarkDelivered /
  MarkAttemptFailed on the delivery queue; Append / Update /
  ListDeliveries on the dashboard delivery log. Four new
  `WebhookEventType` constants (`incident.sev1`,
  `incident.resolved`, `anomaly.freeze`, `divergence.firing`)
  pin the closed event set without forcing the schema to use an
  enum. New integration subtest
  `WebhookStore/CRUD+queue` covers the full lifecycle:
  create → list → update → enqueue → fail-with-retry →
  enqueue → mark-delivered → list-history → delete-cascades.
  `RotateWebhookSecret` is a tagged stub pending the dashboard
  CRUD handlers. Delivery worker + customer-facing API are
  follow-up commits.

- **Inline `price_usd` on `/v1/assets/{id}` (F-1271).** The
  asset-detail body now carries `price_usd` whenever the price
  lookup succeeds — previously it only surfaced via the optional
  coins-overlay block (assets not in the coins catalogue had a
  null `price_usd` even though the same handler was already
  fetching the price for `market_cap_usd`). Freighter wallet
  + retail apps that just want the current price no longer pay
  a second `/v1/price` round-trip on every asset-detail render.
  Extracted `populatePriceUSD` runs before the supply early-
  return so off-chain assets without a supply snapshot also get
  the field; `populateMarketCap` now re-uses the already-inlined
  price instead of paying for a second lookup. OpenAPI spec
  updated; postman + api docs regenerated. 1 new unit test
  covers the no-supply path.

- **`postgresstore.BillingStore` subscription mirror (F-1231).**
  `UpsertSubscription` and `GetActiveSubscriptionForAccount`,
  previously stubbed, now hit the `subscriptions` table from
  migration 0027. UPSERT is idempotent on `stripe_subscription_id`
  so a re-delivered webhook updates plan + period without
  duplicating rows. `GetActiveSubscriptionForAccount` enforces
  both the period-end and canceled-at semantics from
  `platform.Subscription.IsActive`. The Stripe webhook handler
  wire-up (which would need to resolve `stripe_customer_id` →
  `account_id` + extract subscription IDs from the event
  payload) is the next layer; this commit lands the store half
  so the data path is end-to-end-ready. New integration
  subtest `BillingStore/Subscription/UpsertAndGetActive` covers
  insert / idempotent update / expired / validation paths.

- **Stripe webhook tier-upgrade audit log (F-1240).** New
  `internal/platform/postgresstore.AuditStore` implements the
  `platform.AuditStore` interface against the `audit_log` table
  from migration 0027 (Append / AppendBatch / List). The Stripe
  webhook handler now writes one `plan.upgrade` audit row per
  successful upgrade event (one row per event, not per key —
  metadata carries identifier + tier + key counts so the
  dashboard can render "the upgrade happened" without N rows
  for a customer holding N keys). `StripeWebhookConfig.Audit`
  is a narrow `StripeAuditSink` interface so the v1 package
  doesn't import the full audit-store surface. Append failures
  log at WARN and never block the webhook ack — audit-log
  unavailability must not turn a successful Stripe upgrade
  into a Stripe retry storm. 3 new unit tests cover the happy
  path, the nil-sink legacy fallback, and the swallowed-error
  contract.

- **Depeg-scenario test wiring stablecoin late binding ↔ divergence
  worker (F-1230).** ADR-0026's stablecoin late binding deliberately
  conceals stablecoin↔fiat drift so XLM/USDC trades flow into the
  same XLM/USD bucket as XLM/USDT — the divergence worker is the
  designed safety net that fires `flags.divergence_warning` when
  the concealed price drifts from external references. The two
  components had no test wiring them together; nothing would catch
  a regression that broke either side. New
  `divergence/depeg_test.go` exercises the round-trip:
  - **TestStablecoinDepeg_DivergenceWorkerFires** — `aggregate.ProxyTrade`
    rewrites XLM/USDC → XLM/fiat:USD, the aggregator publishes
    a price assuming USDC=$1, references show the true XLM/USD
    after USDC depegged to $0.95, and the worker fires
    WarningFired=true on the resulting ~5.3% delta.
  - **TestStablecoinPegHolds_DivergenceWorkerStaysQuiet** — symmetric
    negative case so a future change can't make the warning fire
    on the steady state.

- **Guard tests for two CLAUDE.md surprises (F-1242).**
  Locks behaviours that no production test previously asserted:
  - `comet.TestDecodeSwap_DispatchIsByTopicNotContract` proves
    that two events with different `ContractID`s but the same
    `(POOL, swap)` topic both decode to `Source="comet"` — i.e.,
    the Comet decoder is generic Balancer-v1, not contract-
    specific. A future change that narrows the decoder to a
    specific allow-list would silently drop trades from any new
    Balancer-v1 deployment; this test fires first.
  - `sep41_supply.TestDecoder_CAP67_FourTopic_BackCompat` exercises
    mint / burn / clawback events with both pre-P23 (3/2/3 topics)
    and post-P23/CAP-67 (4/3/4 topics with `sep0011_asset`)
    arities. The decoder reads counterparty positionally and
    must ignore the optional 4th topic; a future contributor who
    naively asserts topic length would break the post-P23 path.
  The third surprise (SEP-41 transfer dual i128/Map shape) has
  no production transfer-amount decoder yet, so the dual-shape
  guard already lives in `sac_balances.TestObserver_Decode{I128,
  MapVal}`; documented as such in the audit register.

- **Per-request CORS observability metric (F-1244).** New
  `ratesengine_api_cors_decisions_total{outcome}` counter wired
  into the CORS middleware. Outcomes: `no_origin` /
  `allowed_origin` / `allowed_wildcard` / `denied`. The
  pre-existing `warnOpenCORS` startup-only check fires once at
  boot then drifts out of memory; this counter is the per-request
  companion so operators can dashboard real cross-origin traffic
  and alert when a wildcard policy starts handling actual cross-
  origin requests in production (the silent failure mode of
  `RATESENGINE_ALLOWED_ORIGINS=*` slipping in alongside
  credentialed auth_mode). Wired into the existing middleware
  without changing public CORS behaviour; one new test case covers
  all four outcomes.

- **Freeze EventSink LKG VWAP + recovery worker (F-1228 + F-1229).**
  `freeze.EventSink.RecordFreeze` and `freeze.Writer.Mark` now
  carry the last-known-good VWAP we're freezing on as a
  fixed-precision decimal string (orchestrator passes
  `formatRatFixed(prev, 12)`); the timescale sink stamps it on
  the new `freeze_events` row instead of the previous hardcoded
  `frozen_value = 0`. The recovery worker is the inverse half:
  every 60s it lists open `freeze_events` rows, checks whether
  the Redis marker still exists, and calls `MarkRecovered` when
  the marker is gone (TTL elapsed → underlying anomaly cleared).
  Without it, durable rows accumulated forever and the explorer
  /anomalies timeline showed resolved freezes as still-firing.
  New metrics `ratesengine_anomaly_freeze_recovered_total` and
  `ratesengine_anomaly_freeze_recovery_sweeps_total{outcome}`,
  new alert `ratesengine_anomaly_freeze_recovery_stalled` (P3),
  new runbook `freeze-recovery-stalled.md`. Two phase-1 + phase-2
  orchestrator callers updated to thread the prevVWAP through.
  3 new unit tests + extended existing freeze + orchestrator
  tests.

- **Per-IP signup throttle (F-1232).** New `v1.SignupIPThrottle`
  interface + `auth.RedisSignupIPThrottle` Redis-backed
  implementation. Default 5 signups per IP per hour via
  `INCR + EXPIRE` sliding window. The global anonymous rate
  limit (60/min/IP) is plenty for browsing public surfaces but
  lets a single IP bulk-mint 3,600 email→key_id pairs/hour via
  signup. The new throttle closes that vector without affecting
  other anonymous traffic; falls open on Redis errors. Wired in
  `cmd/ratesengine-api/main.go` whenever Redis is available.
  New `auth.ErrSignupRateLimited` sentinel + exported
  `middleware.RemoteIP` for handlers needing trusted-proxy-aware
  client IP outside the middleware chain. 5 unit tests
  (under-cap, over-cap, distinct IPs, empty IP falls open,
  defaults applied).

- **Stripe webhook event dedupe (F-1227).** New
  `internal/platform/postgresstore.BillingStore` implements the
  `AppendStripeEvent` / `MarkStripeEventProcessed` /
  `MarkStripeEventFailed` triple from
  `internal/platform/billing.go` against the `stripe_event_log`
  table from migration 0027. The webhook handler now claims a
  dedupe slot with `INSERT INTO stripe_event_log` BEFORE running
  any side effects; `ErrAlreadyProcessed` (Postgres
  `23505 unique_violation`) signals "we've already done this work"
  and acks 200 immediately without re-running the upgrade. Stripe
  at-least-once delivery means the same event can land hours
  later — without this guard, a manual operator-side downgrade
  between original delivery and redelivery silently re-upgrades
  the customer. Wired in `cmd/ratesengine-api/main.go` to the same
  `*sql.DB` the timescale store uses; falls open to the legacy
  "rely on idempotent UpdateRateLimit" path when Postgres is
  absent. Two new unit tests pin the contract
  (duplicate-doesn't-reupgrade + nil-events-store-falls-back).
  `UpsertSubscription` + `GetActiveSubscriptionForAccount` stubbed
  pending Phase-2 / F-1231.

- **SEP-10 challenge-replay defence (F-1224).** Added a
  `sep10.ReplayGuard` interface + `sep10.RedisReplayGuard` Redis-
  backed implementation. After a challenge XDR clears
  `txnbuild.VerifyChallengeTxSigners`, the validator hashes the
  signed XDR with SHA-256 and `SETNX`'s the dedupe key
  (`sep10:seen:<base64-url-no-pad>`) with TTL = `ChallengeTTL`.
  A second submission of the same signed XDR finds the slot
  taken and returns `auth.ErrUnauthorized` instead of minting a
  fresh JWT. Wired in `cmd/ratesengine-api/main.go` to the
  same Redis client the rest of the auth subsystem uses;
  initial validator construction at `main.go:144` happens before
  rdb is available, so the validator is rebuilt with the guard
  once rdb exists. `miniredis`-backed unit tests pin the three
  contracts (first claim ok, replay rejected, TTL expiry allows
  fresh claim, distinct hashes don't collide).

- **`ratesengine_aggregator_vwap_cache_write_errors_total` metric**
  + paired `ratesengine_aggregator_cache_write_errors` page-tier
  alert. The May-10 SEV-2 (Redis BGSAVE blocked by full root FS for
  ~9 h → every cache `Set` returned MISCONF → `/v1/price` 404'd on
  every rewritten / triangulated / stablecoin-proxy pair) had **no
  upstream signal** in monitoring — `flags.stale` did not flip
  because the aggregator process was alive and ticking, just unable
  to publish. The post-mortem (`internal/incidents/data/2026-05-10-redis-writes-blocked-disk-full.md`)
  explicitly recommended "alert on aggregator WARN rate (not just
  service-up status)" — this counter realises that recommendation
  as the cleanest signal: any non-zero `rate(...[5m])` for ≥ 2 min
  pages. Increments at the single cache-write failure point in
  `internal/aggregate/orchestrator/orchestrator.go:653`. Closes
  audit-2026-05-12 F-1253; supports F-1254 (`flags.stale` semantic
  bug — separate fix).

### Fixed

- **Postgres `max_locks_per_transaction = 256` codified (F-1251).**
  The 2026-05-06 SEV-3 (`internal/incidents/data/2026-05-06-postgres-lock-table-full.md`)
  hit `out of shared memory (53200)` when the per-tx lock table
  saturated under concurrent ingest from many sources. The
  operator bumped this knob to 256 by hand on R1; un-codified, a
  from-scratch R1 rebuild or R2/R3 cutover would inherit the
  Postgres default of 64 and re-experience the same incident
  class. Now templated by `archival-node/templates/postgresql.conf.j2`
  with default `postgres_max_locks_per_transaction: 256` (4×
  headroom; 51,200-entry lock table at the current 200-connection
  limit). Paired with new `ratesengine_timescale_lock_table_pressure`
  Prometheus alert at 70% saturation so the next bump is
  forecast not forced — depends on `postgres_exporter` (not yet
  scraped on R1; rule lights up when the exporter lands).

- **`web/status/wrangler.toml` added (F-1245).** Mirrors the
  explorer + dashboard wrangler.toml shape so Cloudflare Pages
  git-integration deploy works without manual project setup.

- **`web/explorer/src/app/oracles/OraclesView.tsx` ESLint
  `react-hooks/exhaustive-deps` warning fixed (F-1258).**
  `streamRows` was a fresh `[]` on every render when
  `streams.data` was undefined, causing the downstream `useMemo`
  to recompute every tick. Wrapped in its own `useMemo` for
  referential stability.

- **`internal/sources/comet/adapter_test.go`: pin
  topic-vs-contract-id contract (F-1242).** New
  `TestDecoder_Decode_NoContractIDDiscrimination` makes the
  CLAUDE.md surprise-list claim ("Comet decoder matches by
  topic, not contract address") executable. Any future change
  adding a contract-id allow-list at the decoder layer (instead
  of downstream filtering) MUST flip the assertion.

- **F-1228 + F-1229 acknowledged but deferred to a separate
  refactor.** `freeze_events.frozen_value` always written as 0
  + `MarkRecovered` has zero callers. The structural fix
  (extend `freeze.EventSink.RecordFreeze` to accept the LKG
  VWAP, plus wire a recovery worker that calls MarkRecovered
  on Redis-marker TTL expiry) touches the EventSink interface
  used by 3 packages + tests. Both medium-severity, neither
  blocks the public flip.

### Investigated, no code change

- **N-1262 ADR-0012 missing from disk** — turns out to be an
  intentional reservation, documented in `docs/adr/README.md:56`:
  "0012 | *Planned* | Quorum-set composition (referenced by
  multi-region-topology) | —". Per the ADR README's
  "gaps allowed when reserved" rule. F-1262 closed as `invalid`.

- **`flags.stale` semantic bug fixed (F-1254).**
  `internal/api/v1/price.go` reset `stale = false` after falling
  through to `priceFallback` (last-trade / stablecoin proxy /
  triangulation). The May-10 SEV-2 (Redis BGSAVE blocked → cache
  empty → every closed-bucket read hit ErrPriceNotFound →
  priceFallback served last-trade for ~9 h) hit this path: the
  customer-visible response was the fallback, but `flags.stale`
  was false. Per ADR-0018 §"flags.stale semantic" and the doc
  comment on `Flags.Stale`, fallback responses ARE stale by
  definition. Set `stale = ok` on the fallback branch in both
  the single-asset and `/v1/price/batch` paths so any non-VWAP
  response now correctly carries `stale=true`. Companion fix
  to F-1253's cache-write-error counter (the upstream signal)
  and F-1252 (the alert-routing the May-10 incident exposed).

- **`/v1/price/batch` sources nondeterminism (F-1259).**
  `internal/api/v1/price.go:902-905 lookupPriceBatch` unioned
  per-row sources through a `map[string]struct{}` and emitted
  them in map-iteration order, breaking the ADR-0015
  byte-identical cross-region property for batch responses.
  Added `sort.Strings(srcs)` before `writeJSON` so batch
  responses match the single-asset path's stable lexical order
  (set by `timescale.normalizeVwapSources` at the storage
  boundary per F-0016 closure).

- **Cache-Control gap on credential surfaces (F-1225).**
  `/v1/auth/login`, `/v1/auth/callback`, `/v1/auth/logout`,
  `/v1/dashboard/keys*`, `/v1/signup`, `/v1/webhooks/stripe`,
  `/v1/price/stream`, `/v1/methodology`, and `/v1/incidents.atom`
  all fell through `policyForPath`'s switch with no case match,
  emitting no `Cache-Control` header. Most concerning was
  `/v1/auth/callback`: a CDN in front of the API could have
  cached the magic-link consume response and re-issued the
  session cookie to subsequent requests. Added explicit cases:
  every `/v1/auth/*` and `/v1/dashboard/*` and the two
  state-changing surfaces (`/v1/signup`, `/v1/webhooks/stripe`)
  use `private, no-store`; `/v1/price/stream` uses `no-store`;
  `/v1/methodology` and `/v1/incidents.atom` get explicit
  public-cache policies appropriate to their content cadence.

- **4 of 4 `make test-integration` failures (F-1250).**
  - `TestPlatformPostgresStores/APIKey/CRUD+revoke+touch` —
    `test/integration/platform_postgres_stores_test.go:400,510`
    constructed key IDs as `"kid_" + uuid.New().String()[:12]`
    which contains a hyphen at position 9, violating
    migration-0027 check `id ~ '^kid_[a-f0-9]{12,}$'`. Switched
    to `strings.ReplaceAll(uuid.New().String(), "-", "")[:12]`
    (12 hex chars).
  - `TestEndToEnd_LedgerstreamToTimescale/soroban_LCM_with_reflector_FX_update`
    + `TestTradesInRangeAndMarkets` — both used hand-crafted
    G-strkeys (`GA7QYNF7…UWDA` and `GA5ZSEJYB…ZVM`) with invalid
    CRCs. The strkey package now enforces CRC; tests switched
    to AQUA's real mainnet G-strkey
    (`GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA`)
    which round-trips cleanly and is distinct from USDC's
    issuer.
  - `TestSupplyStorageRoundTrip` — schema/reader drift:
    migration `0005_create_asset_supply_history.up.sql:60`
    creates a UNIQUE index on `(asset_key, ledger_sequence, time)`
    (TimescaleDB requires the partition column in any unique
    index on a hypertable), but `internal/storage/timescale/supply.go:47`
    used `ON CONFLICT (asset_key, ledger_sequence) DO NOTHING`.
    Postgres requires an exact column-set match; the INSERT
    failed with `42P10`. Updated the conflict target to all 3
    columns and revised the doc comment to explain the
    invariant preservation.
  - Plus `TestTradesInRangeAndMarkets` `DistinctPairs` returned
    0 markets after the strkey fix because the test inserted
    into `trades` directly but `DistinctPairs` reads from the
    `prices_1m` continuous aggregate (post rc.45 commit
    `8717bc20`). Added a `CALL refresh_continuous_aggregate('prices_1m', NULL, NULL)`
    before the assertion, mirroring `test/integration/api_test.go:65-74`.

- **R1 alert blackout closed: 9 alert families wired up, textfile
  evidence chain repaired (F-1219 + F-1220 + F-1221 + F-1252).**
  Pre-change R1 loaded only 6 of 18 rule families
  (`aggregator/api/infra/ingestion/meta/slo`); every alert in
  `anomaly`, `divergence`, `external-pollers`, `supply`,
  `supply-snapshot`, `supply-refresh`, `archive-completeness`,
  `verify-archive`, `sla-probe` was permanently silent. The
  SLA-evidence chain specifically was broken end-to-end: the probe
  binary supports `-textfile-output` (`cmd/ratesengine-sla-probe/textfile.go:190
  writeTextfileAtomic`) but the R1 wrapper at
  `configs/healthchecks/sla-probe.sh` never set it, the
  textfile-collector dir didn't exist, and `node_exporter` ran
  without `--collector.textfile`. Three changes close the chain:
  - `configs/ansible/roles/archival-node/tasks/10-observability.yml`
    now provisions `/var/lib/node_exporter/textfile_collector/`
    and adds `--collector.textfile` + `--collector.textfile.directory`
    to the node_exporter systemd unit.
  - `configs/healthchecks/sla-probe.sh` now defaults
    `SLA_PROBE_TEXTFILE_OUTPUT=/var/lib/node_exporter/textfile_collector/sla_probe.prom`
    and passes `-textfile-output $value` conditionally (preserves
    the opt-out for operators that set the env var blank).
  - `configs/prometheus/rules.r1/` gains 9 rule files copied
    verbatim from `deploy/monitoring/rules/` (none of them had
    job-label refs requiring single-host adaptation). README
    table updated; rules `cache.yml` / `storage.yml` / `stellar.yml`
    stay excluded with a clear note (`redis_exporter` +
    `postgres_exporter` + `stellar-core-prometheus-exporter` are
    not on R1).

- **Source-stopped alert false-positive class on low-volume
  Soroban contracts (F-1212b).** `ratesengine_ingestion_source_stopped`
  used a 5-min rate window which routinely false-fired on
  `band`, `blend`, `comet`, `ecb`, `phoenix` (legitimate 5+-minute
  gaps during quiet trading windows — the source-stopped runbook
  itself acknowledges this at line 60). Widened to a 30-min rate
  window + 15-min `for:` in both `deploy/monitoring/rules/ingestion.yml`
  and `configs/prometheus/rules.r1/ingestion.yml`. Total-outage
  coverage stays tight via the separate `_all_sources_stopped`
  alert at 3 min — that one continues to catch the
  upstream-broke-across-the-fleet case.

- **Multi-host alert rule job labels (F-1222).**
  `deploy/monitoring/rules/api.yml` / `aggregator.yml` /
  `ingestion.yml` / `slo.yml` / `meta.yml` referenced `job="api"`
  / `"aggregator"` / `"indexer"` but the multi-host ansible
  prometheus role's scrape config uses `ratesengine_api` /
  `ratesengine_aggregator` / `ratesengine_indexer` (underscores).
  Rules would never have evaluated true on a multi-host deploy.
  Renamed the canonical multi-host labels to match the scrape
  config; `meta.yml`'s scrape-failing regex updated to the actual
  exporter job names (`postgres_exporter`, `redis_exporter`,
  `node_exporter`, `minio`). R1's `configs/prometheus/rules.r1/`
  copies already used the correct hyphenated R1 names and are
  unaffected.

- **rc.48 dead-route cleanup follow-up.** rc.48 removed the
  `/v1/coins` + `/v1/currencies` HTTP surface but left several
  stale references behind: `cmd/ratesengine-sla-probe` was still
  probing `/coins` (would 404 after rc.48 deploy → SLA-probe
  perma-fail on availability); `examples/curl/04-coins.sh` +
  README still advertised the removed route; `web/status` synthetic
  smoke probe still pointed at `/v1/coins?limit=1`; `openapi/rates-engine.v1.yaml`
  carried 3 stale `/v1/coins` text references (incl. the rate-limit
  example's `instance` field); `internal/api/v1/server.go` Options
  doc comments still said "backs GET /v1/coins" / "backs /v1/currencies"
  even though the seams now feed `/v1/assets` and `/v1/chart`.
  All migrated to live equivalents:
  - `cmd/ratesengine-sla-probe/main.go` `staticEndpoints` switches
    `/coins` → `/assets` (same fan-out coverage; comment explains
    the rc.47 → rc.48 → rc.49 progression).
  - `examples/curl/04-coins.sh` deleted; replaced with `04-assets.sh`
    using `?order=volume_24h_usd:desc`.
  - `web/status/src/app/page.tsx` synthetic-probe entry switched
    to `/v1/assets?limit=1` with the same Catalogue group.
  - `openapi/rates-engine.v1.yaml` lines 193 / 1602 / 2608
    updated.
  - `internal/api/v1/server.go` Options.Coins / .Currencies /
    .FXHistory doc comments rewritten to describe the actual
    `/v1/assets` + `/v1/chart` consumers.
  Net: `make verify` clean; `go test ./internal/api/v1/...` +
  `./cmd/ratesengine-sla-probe/...` green.
  Closes audit findings F-1202, F-1210 (cosmetic doc-text portion),
  F-1211, F-1223, F-1245 (smoke surface), F-RFP-0017.

### Tooling

- **`docs/reference/api/rates-engine.v1.yaml` regenerated**
  from `openapi/rates-engine.v1.yaml` via `make docs-api`. The
  checked-in copy had drifted ~990 lines (561 ins / 429 del) since
  the last regeneration. `web/explorer/src/api/types.ts` (the
  openapi-typescript output) auto-regenerated as a transitive
  consequence (~415 lines lighter; `pnpm typecheck` clean). Closes
  F-1246.
- **`docs/reference/config/README.md` regenerated** from
  `internal/config/config.go` via `make docs-config` (+6 lines).
  Closes F-1255.

## [v0.5.0-rc.48] — 2026-05-11

### Removed

- **`/v1/coins` + `/v1/coins/{slug}` retired.** No production
  consumers (explorer migrated in rc.47). `/v1/assets` is a strict
  superset and replaces every shape these endpoints carried. Net
  ~600 lines of handler code + tests + OpenAPI deleted.
- **`/v1/currencies` + `/v1/currencies/{ticker}` retired.** Same
  story — no production consumers, explorer migrated weeks back.
  The in-process `CurrenciesReader` interface stays as the
  fiat-rate seam consumed by `/v1/price` cross-rate triangulation
  and `/v1/chart` fiat:fiat / market_cap paths; the HTTP surface
  goes. Net ~500 lines deleted.
- **SDK methods removed:** `Client.Coins`, `Client.Coin`,
  `Client.Currencies`, `Client.Currency` + their options/result
  types. Use the `Asset`/`Assets` methods backed by `/v1/assets`
  instead. Internal-API types kept on a private path for cross-
  endpoint reuse (CoinATH, CoinTopMarket, CoinPricePoint embed
  in AssetDetail).

## [v0.5.0-rc.47] — 2026-05-11

### Added

- **`/v1/assets` is now a strict superset of `/v1/coins`.** The
  listing endpoint sources from `ListCoinsExt` when a `CoinsReader`
  is wired, projecting each row into `AssetDetail` with the full
  coin-overlay shape (`price_usd`, `volume_24h_usd`,
  `market_cap_usd`, `circulating_supply`, `change_*_pct`,
  `issuer_scam_reason`). Pagination uses the same
  `<observation_count>:<asset_id>` cursor format as `/v1/coins`.
- **`/v1/assets?issuer=<G-strkey>`** filters the listing to one
  issuer. Wires straight through to `ListCoinsExt`'s Issuer option.
  Unblocks `/v1/issuers/{g}` → `/v1/assets` migration in the
  explorer.
- **`AssetDetail` adds `slug`, `first_seen_ledger`,
  `last_seen_ledger`, `observation_count`** — the catalogue
  identity + activity-metadata scalars CoinRow carries but the
  rc.46 overlay didn't lift. Lets explorer consumers drop their
  parallel `/v1/coins/{slug}` fetches.

### Changed (explorer)

- **Every explorer `/v1/coins` fetch site migrated to `/v1/assets`.**
  `useCoins` hook keeps its name + return shape for ergonomic
  continuity but the underlying API call is now `/v1/assets`,
  envelope-reshaped from `{data:[], pagination:{next}}` into the
  legacy `{coins:[], next_cursor, limit}` so the 4 home consumers
  (HomeTopMovers, HomeTopAssets, HomeNetworkStrip, AssetsTable)
  work unchanged. Direct fetch sites (asset-detail listing cache,
  sitemap, embed-asset, issuer page) hit `/v1/assets` end-to-end.

## [v0.5.0-rc.46] — 2026-05-11

### Added

- **`/v1/assets?network=<chain>` filter.** `network=stellar` (or
  omitted) returns the indexer's full Stellar-network catalogue;
  `ethereum|solana|polygon|base|arbitrum|tron|bitcoin|bsc|
  avalanche|xrpl` projects the verified-currency catalogue entries
  with a matching `networks[]` row into `AssetDetail` (`type:
  external`, `asset_id: <network>:<contract>`). Drives the
  `/blockchains/{network}` click-through.
- **AssetDetail coin-equivalence overlay.** `/v1/assets/{id}` now
  includes `price_usd`, `change_1h_pct`, `change_7d_pct`,
  `top_markets`, `price_history_24h`, `price_history_7d`,
  `markets_count`, `trade_count_24h`, `ath`, and
  `issuer_scam_reason` — lifted from the coins catalogue so the
  surface is a superset of `/v1/coins/{slug}`. Closes the
  wire-shape gap blocking the `/v1/coins` → `/v1/assets` consumer
  migration. Skipped for `fiat:*` assets (no coin row).
- **`/v1/chart?price_type=market_cap` (fiat phase).** Returns a
  USD-denominated market-cap series for fiat:CCY base assets,
  computed on-the-fly as M2 (verified-currency catalogue) × daily
  FX rate (fx_quotes). Crypto market-cap-over-time is deferred
  pending the `market_cap_1d` CAGG (returns 501 today with a
  clear "deferred" message). Closes the explorer's "no market
  cap over time" gap for the currencies surface.

### Removed (explorer)

- **`/currencies/*` page tree.** The 7 files in
  `web/explorer/src/app/currencies/` (~2400 lines) are deleted;
  routes consolidated under `/assets/{friendly-slug}`. Internal
  link generators (HomeCurrencies, SearchModal, convert pages,
  sitemap) now use the new `lib/fiat-slugs.ts` ticker → slug map.
- **`_redirects` consolidated under CF's free-plan ~100-rule
  cap.** 221 → 52 rules. Cloudflare Pages silently drops rules
  beyond the cap; the CNY rule was at position #117 and
  consistently failed to fire. Trims: drop UPPERCASE ISO
  variants, drop no-trailing-slash forms (Next.js
  `trailingSlash:true` 308-redirects bare URLs first), drop
  secondary aliases.

## [v0.5.0-rc.45] — 2026-05-11

### Fixed

- **`/v1/markets` reads `prices_1m` instead of scanning 41M trades.**
  DistinctPairs was scanning the raw `trades` hypertable over a
  14-day window with a HashAggregate spilling to 32 disk partitions
  — 8s+ cold-cache p99 that consistently blew the handler's 8s
  deadline. Live measurement before the fix: 86 % of `/v1/markets`
  requests returned 503/500 over the back half of the day, driving
  the SLO availability/latency burn pages plus the API
  error-rate/latency tickets. Rewriting the query to source from
  the `prices_1m` 1-min CAGG (3M rows vs 41M; pre-aggregated
  `volume_usd` + `trade_count` + `last_price` + `sources[]`) cuts
  the query from **8.4 s → 0.44 s** — 19× speedup. `last_trade_at`
  is now bucket-rounded to the minute (within 60s of the actual
  last trade); `count_24h` and `vol_24h_usd` are byte-identical to
  the previous shape.

### Explorer build

- **`staticPageGenerationTimeout` bumped to 180 s** (was the default
  60 s). Pre-rendering ~500 asset pages with 4-6 API fetches each
  hit the per-page ceiling under build-host rate-limit pressure.
  Note: not sufficient on its own — further explorer-build
  investigation underway in a follow-up.
- **Explorer version surface** — footer build badge + `re-build-sha`
  / `re-build-time` meta tags. Mirrors the API's `/v1/version`
  endpoint so an operator can confirm which build a given page
  reflects. Reads `CF_PAGES_COMMIT_SHA` (CF git auto-deploy) or
  `GITHUB_SHA` (manual workflow).

## [v0.5.0-rc.44] — 2026-05-11

### Fixed

- **Fiat market cap reads from `PriceReader` (Redis-triangulated FX),
  not `GlobalPriceReader`.** rc.43 wired the fiat path through
  `ComputeGlobalPrice` → `GlobalPriceReader`, which only reads
  `prices_1m`. FX:FX rates (USD/CNY etc.) live in the Redis
  triangulated cache, not `prices_1m` — so the lookup returned no
  rows and CNY market_cap stayed empty post-rc.43. Hoist fiat
  handling above the `globalPrice` nil guard via `populateFiatView`,
  read FX rates directly through `s.prices`, and switch the
  verified-listing fan-out gate accordingly.
- **Asset detail static-fallback recovery loop avoided.** The
  client-side fallback that re-fetches `/v1/coins/{slug}` from
  the browser when a build-time fetch missed now auto-reloads
  once (tracked via `sessionStorage`) and surfaces a friendlier
  message on the second pass — no more endless flashes of the
  "couldn't be prerendered" panel.

### Added

- **`/v1/chart` for fiat:fiat pairs reads `fx_quotes`.** When both
  base and quote are fiat (e.g. `fiat:CNY/fiat:USD`), the handler
  routes to the fx_quotes hypertable instead of the crypto
  `prices_1m` / `prices_5m` / `prices_1h` CAGGs. USD↔CCY both
  directions; cross-fiat (EUR/JPY etc.) returns an empty series
  pending a follow-up. Closes the "CNY 1y chart has no data"
  surface — once `fx-history-backfill` runs against a deployment,
  fiat charts get the full Frankfurter-backed history (back to
  1999-01-04).
- **`internal/sources/frankfurter` — ECB rates client.** Range
  endpoint returns every daily rate for every supported currency
  in one HTTP request, so a 25-year backfill is ~6 requests
  total (5-year chunks). No API key, no per-request cost.
- **Explorer chart truncated banner.** ChartPanel now surfaces
  the API's `truncated` / `data_starts_at` signal as an amber
  banner reading "Showing data from YYYY-MM-DD — the deployment
  hasn't accumulated the full Xy window yet." Replaces the
  silent "8 flat points for 1y" surface.
- **Explorer chart quote picker drops XLM for fiat assets.** When
  `asset_id` starts with `fiat:`, the chart panel forces USD
  quote and removes XLM from the picker (XLM isn't a meaningful
  quote against CNY / EUR / JPY).

### Changed

- **`scripts/ops/fx-history-backfill` defaults to Frankfurter.** Drops
  the `MASSIVE_API_KEY` requirement for historical backfills. Live
  forex worker continues on Massive for hourly grain + broader
  ticker coverage; the one-shot tool now uses Frankfurter for the
  free "populate fx_quotes once" case. Runbook
  (`docs/operations/runbooks/fx-history-missing.md`) updated.

## [v0.5.0-rc.43] — 2026-05-11

### Fixed

- **Fiat market_cap_usd now populates on /v1/assets/* surfaces.**
  rc.42 shipped the M2-based market-cap math but FX-pair prices_1m
  buckets have trade_count of 1-3 (snapshots, not trades); the
  default VWAPMinTradeCount=5 threshold caused tier-1 to skip the
  FX rate, leaving market_cap_usd empty on every non-USD fiat row.
  Lower the threshold to 1 for fiat — each FX observation IS the
  rate. CNY listing cap should now show ~\$42T (M2 ¥302T × USD/CNY
  rate from the live FX feeds).

### Added

- **`/v1/assets/{ticker}` ticker fallback.** When `{ticker}` is a
  3-letter ISO code matching a verified-currency entry (USD, EUR,
  CNY, …), the dispatch now resolves it via the catalogue's
  ticker index — same view as `/v1/assets/{friendly-slug}`.
  Lets clients hold ISO codes without a friendly-slug lookup
  table.

### Changed

- **`/v1/currencies` + `/v1/currencies/{ticker}` deprecated.**
  Mirror of the /v1/coins deprecation from Phase 1.4a: every
  response emits `Deprecation: true` + `Link: </v1/assets/{slug}>;
  rel="successor-version"`. Routes still serve fully; the
  Cloudflare Pages `_redirects` rules handle browser-side migration
  for the explorer. Direct API consumers should plan their move.

## [v0.5.0-rc.42] — 2026-05-11

### Added

- **Assets unification — fiat currencies as first-class assets**
  (R-018 operator decision 2026-05-11). Six-commit batch:
  - Catalogue extension: `internal/currency` gains `class`
    (crypto/stablecoin/fiat), `circulating_supply`, `supply_decimals`.
    Seed adds 19 fiat entries (USD, EUR, GBP, JPY, CNY, AUD, CAD,
    CHF, INR, BRL, MXN, KRW, HKD, SGD, ZAR, TRY, NZD, SEK, NOK)
    with M2 figures from central-bank reporting + friendly slugs
    (`/assets/us-dollar`, `/assets/chinese-yuan`, …).
  - `/v1/assets/{slug}` GlobalAssetView gains `class`,
    `circulating_supply`, `supply_decimals`, `market_cap_usd`.
    Fiat slugs compute market cap via `M2 × current FX rate`;
    USD identity-cased to 1.00. CNY computes to ≈ $42T, the
    largest M2 globally.
  - `/v1/assets/{slug}/{network}` sub-route for per-network
    drill-down. Stellar entries 303-redirect to the canonical
    `/v1/assets/{asset_id}` view; non-Stellar return a thin
    `PerNetworkAssetView` with the catalogue's contract +
    external block-explorer link.
  - `/v1/assets/verified` listing now carries `market_cap_usd`
    for fiat rows (parallel FX fan-out, ~19 lookups per request).
    Crypto / stablecoin rows still skip — their cap lives on
    /v1/assets/{asset_id}.
  - Explorer: `/currencies/*` → `/assets/*` CF Pages redirects
    for the full G20+ set. The verified-currency strip on
    `/assets` now sorts by market_cap_usd descending (CNY first).

## [v0.5.0-rc.41] — 2026-05-11

### Added

- **`/v1/assets/verified` catalogue listing endpoint** + explorer
  verified-currencies strip (R-018 Phase 1.5d). New endpoint returns
  every entry in the verified-currency catalogue
  (`internal/currency/data/seed.yaml`) as a directory listing —
  identity + cross-chain networks, no price block. Designed for
  listing-page consumption: cheap (single fetch, no per-row price
  round-trip), deterministic order. The explorer's `/assets` page
  now renders a "Verified currencies" chip-row above the main table,
  one chip per catalogue entry with a green check + network count
  + link to `/assets/{slug}`.

- **Verified-currency badge + name on `/assets/{slug}`** (R-018
  Phase 1.5c). When the route slug resolves to a verified currency,
  the page header now surfaces the friendly name (e.g. "USD Coin")
  alongside the ticker, a green "Verified" check badge with
  attribution tooltip, and an "Issued by …" line replacing the bare
  "Issuer home domain" line when the catalogue carries a
  verified_issuer label.

- **Cross-chain Networks panel on `/assets/{slug}`** (R-018 Phase
  1.5b). When the slug resolves to a verified-currency catalogue
  entry, the page shows a per-network identity panel listing every
  chain the currency is issued on. Each row carries a data-quality
  badge ("Indexed" or "External"), the contract or asset_id, and a
  deep_link into the per-Stellar-asset detail view (for Stellar
  rows) or a block-explorer link (etherscan/polygonscan/basescan/
  arbiscan/snowtrace/bscscan/solscan/tronscan defaults; operators
  can override per-entry via the catalogue's `external_link` field).

- **Unverified-collision warning banner on `/assets/{slug}`**
  (R-018 Phase 1.5a). The amber banner rendered when the requested
  asset's code matches a verified currency's Stellar ticker but the
  issuer doesn't, with an inline link to the verified asset's slug
  page. Reads `unverified_warning` from the existing
  `/v1/assets/{asset_id}` response (Phase 1.4a) so no new fetch
  path is needed.

## [v0.5.0-rc.40] — 2026-05-11

### Added

- **Ansible: monitoring playbook + log-discipline task** codify the
  post-incident operator lore from 2026-05-10. The
  `configs/ansible/roles/prometheus/templates/alertmanager.yml.j2`
  template now uses the `page` / `ticket` / `informational`
  severity vocabulary matching every rule in `deploy/monitoring/`
  and `configs/prometheus/rules.r1/`, plus a `deadmansswitch`
  receiver wired to a Healthchecks.io URL. New
  `configs/ansible/playbooks/monitoring.yml` invokes the role on
  `archival_nodes`. New `configs/ansible/roles/archival-node/tasks/
  15-log-discipline.yml` installs an `/etc/logrotate.d/rsyslog`
  override (100 MB cap, 7-rotation history, gzip-compressed) and
  `/etc/systemd/journald.conf.d/00-cap.conf` with
  `SystemMaxUse=500M` + `SystemKeepFree=300M` — both directly
  addressing the operational follow-ups in
  `internal/incidents/data/2026-05-10-redis-writes-blocked-disk-full.md`.
  A from-scratch r1 (or future R2/R3) rebuild now picks up the
  fix automatically. Operator action: provision
  `/etc/default/alertmanager-secrets` (Slack + Healthchecks URLs)
  and run `ansible-playbook -i inventory/r1.yml playbooks/monitoring.yml`
  on r1 to actually start delivering alerts.

- **`/v1/assets/{slug}` global view + `/v1/coins` deprecation**
  (R-018 Phase 1.4a). The `/v1/assets/{asset_id}` route now
  dispatches on the path parameter: a verified-currency slug
  (`usdc`, `eurc`, `aqua`, …) returns the new `GlobalAssetView`
  wire shape — cross-chain identity + price block from the Phase
  1.3a three-tier fallback chain + a `networks[]` list with
  Stellar `deep_link` entries pointing at the per-Stellar-asset
  view. Canonical asset_ids (`USDC-GA5Z…`, `native`, `C…`,
  `fiat:USD`) still route to the existing per-Stellar-asset
  surface unchanged. Production wiring binds
  `aggregate.GlobalPriceReader` to `*timescale.Store` + the
  existing Redis triangulated looker, with
  `external.AggregatorSources()` as the tier-2 source list.
  `/v1/coins` and `/v1/coins/{slug}` now emit `Deprecation: true`
  + `Link: </v1/assets/{slug}>; rel="successor-version"` headers
  per RFC 9745 / 8288 — runtime behaviour unchanged so the
  explorer (Phase 1.5) keeps working. Actual `/v1/coins`
  deletion (1.4b) lands after the explorer migrates.

- **Three-tier global-price fallback chain**
  (R-018 Phase 1.3a). New `internal/aggregate/ComputeGlobalPrice`
  walks `vwap_native` → `aggregator_avg` → `triangulated` in
  order, returning the first tier whose data satisfies its
  threshold (trade-count floor for tier 1, freshness window for
  tier 2). Result carries `Price`, `Authority` (one of the three
  tier labels), `Sources`, and `AsOf` so Phase 1.4's `/v1/assets/
  {slug}` global view can surface provenance per response. New
  `external.AggregatorSources()` helper returns the aggregator-
  class source names in deterministic order — matches the
  pre-existing `FXSources()` pattern. The cross-chain
  ticker-bucketed VWAP CAGG (1.3b) is explicitly deferred — it's
  algorithmically distinct from the per-pair VWAP and only
  meaningful once we ingest non-Stellar-chain trades.

- **Catalogue-driven CoinGecko coverage + aggregator-price reader**
  (R-018 Phase 1.2). The CoinGecko poller's ticker map and the
  indexer's aggregator pair set now derive from the verified-
  currency catalogue: adding a verified currency with a
  `coingecko_id` in `internal/currency/data/seed.yaml`
  automatically extends polling coverage. CG's hardcoded
  ticker-to-slug map (13 entries) remains a fallback for tests
  and pre-Phase-1.2 callers. New storage method
  `Store.LatestAggregatorPricesForPair(ctx, base, quote, sources)`
  returns the most-recent observation per aggregator-class source
  — the seam Phase 1.3's `aggregator_avg` price-authority tier
  consumes. Reuses the existing `oracle_updates` hypertable (no
  new migration). CG catalogue-augmentation worker (top-N
  market-cap refresh) deferred — separate trust surface; the
  hand-curated seed suffices for v1.

- **Unverified-ticker-collision warning on `/v1/assets/{id}`**
  (R-018 Phase 1.1). Requests for an asset whose code matches a
  verified currency's Stellar ticker (USDC, EURC, AQUA, …) but
  whose issuer doesn't match the verified entry now carry an
  `unverified_warning` body pointing at the verified canonical
  asset, plus `flags.unverified_ticker_collision = true` on the
  envelope. Warning body fields: `verified_slug`,
  `verified_asset_id`, `verified_name`, `verified_issuer`, `note`
  (a one-sentence message rendered verbatim by clients). Powered
  by a new `internal/currency` package + a 26-currency seed
  YAML embedded in the binary
  (`internal/currency/data/seed.yaml`) covering Stellar native +
  major USD stablecoins (USDC, USDT, PYUSD), EUR stablecoins
  (EURC), Stellar-native tokens (AQUA, yXLM, SHX, VELO, BLND,
  PHO, yUSDC) and globals without verified Stellar issuers (BTC,
  ETH, SOL, BNB, XRP, ADA, DOGE, AVAX, MATIC, DOT, LINK, UNI,
  AAVE, WBTC). Foundation for the multi-network assets migration
  (`docs/architecture/multi-network-assets-migration.md`);
  Phases 1.2 (CG/CMC connectors) → 1.5 (explorer migration) build
  on this catalogue. OpenAPI + `pkg/client.UnverifiedWarning` +
  `Flags.UnverifiedTickerCollision` in lockstep.

- **`/v1/methodology`** — machine-readable summary of the active
  aggregation policy (R-023). Returns the VWAP method,
  per-endpoint outlier filters, the operator's stablecoin →
  fiat-USD proxy allow-list, the four source classes
  (exchange / aggregator / oracle / authority_sanity) and which
  contributes to the served price, the flat list of registered
  venues with class / weight / VWAP-inclusion flags, and pointers
  to the long-form ADRs that govern each section. Designed for
  transparency consumers (compliance, auditors, integrators) who
  want to verify the policy without parsing the explorer's HTML
  /methodology page or chasing ADR cross-refs. Sub-millisecond —
  derived from compile-time constants + the in-memory source
  registry + operator config; no DB call. OpenAPI spec, pkg/client
  `Methodology` shape, and explorer types kept in lock-step.
  Three regression tests pin baseline shape, peg-config
  round-trip, and empty-pegs deployment.

### Changed

- **`/v1/markets` default sort changed from `pair` (alphabetical)
  to `volume_24h_usd_desc`** (R-014). The alphabetical default
  surfaced spam tokens (`0-…`, `0TAX-…`, `0x1F3D4-…`) at the top
  of every cold listing — useless for the "what's interesting on
  Stellar" query and the explorer always passed
  `?order_by=volume_24h_usd_desc` explicitly to work around it.
  Now the implicit default matches what every consumer wants.
  Callers paginating the entire universe of pairs in lex order can
  still pass `?order_by=pair` explicitly. **Cursor-format
  compatibility note:** because the cursor is sort-key-tagged
  (validated via `ValidateMarketsCursor`), cursors generated under
  the old alphabetical default will return 400 against the new
  default — pass `?order_by=pair` alongside the cursor to resume
  the alphabetical pagination, or drop the cursor and start fresh
  on the new default.

- **`/v1/observations` now sets `flags.triangulated=true` on an
  empty result when /v1/price would have served a value via the
  Redis VWAP cache or stablecoin-fiat proxy**. The endpoint is
  raw-per-source by ADR-0018, so a triangulated pair has no rows
  to return — but the empty `data: []` was indistinguishable from
  "this pair is unpriced" and sent integrators chasing nonexistent
  data. The hint never fires when the caller passed `?source=`
  (source-filtered queries are asking about a specific venue, not
  the aggregate). R-011 in `docs/review-2026-05-10.md`.

### Fixed

- **`/v1/assets/{id}` and `/v1/assets/{id}/metadata` now run their
  responses through the same `enrichIssuer` known-issuers backfill
  that `/v1/issuers` already used**. Pre-fix, the two surfaces
  disagreed on whether SEP-1 metadata existed for the same issuer:
  `/v1/assets/USDC-G…` reported `home_domain: null,
  sep1_status: "not_applicable"` while `/v1/issuers/G…` reported
  `home_domain: "centre.io"`. The asset surface relied on the
  watched-set sep1-refresh worker having populated the storage row,
  which doesn't run on a fresh deployment. Now both surfaces fall
  back to the curated `internal/api/v1/known_issuers.go` map when
  the storage row is empty. R-016 in `docs/review-2026-05-10.md`.

### Added

- **`/v1/chart` envelope carries a `truncated` flag plus
  `data_starts_at` and `requested_from` timestamps when the
  requested timeframe extends before the deployment's earliest
  available data**. r1 today only retains ~7 days of high-resolution
  history but still accepts `?timeframe=1y` — pre-fix, consumers
  couldn't tell whether the returned 7 daily points were the last 7
  days of a long history or all the history this deployment has.
  `timeframe=all` always reports `truncated=false` (that timeframe
  means "everything you have" by definition). OpenAPI + pkg/client
  ChartSeries updated. R-013 in `docs/review-2026-05-10.md`.

### Fixed

- **Handler-timeout paths return 503, not 500, when the per-call
  context deadline fires and Postgres returns its own
  `canceling statement` error**. `errors.Is(err,
  context.DeadlineExceeded)` doesn't match the `pq.Error`
  SQLSTATE 57014 that lib/pq surfaces after the v3 cancel-request
  flow — so `/v1/markets`, `/v1/pools`, and `/v1/coins` were
  500ing on the cold-cache path that the 8s ceiling was
  specifically meant to convert into a retryable 503. New
  `handlerTimedOut(callCtx, err)` helper consults the per-call
  context's `Err()` as the authoritative signal. R-021 in
  `docs/review-2026-05-10.md`.

- **All-time-high (`/v1/coins/{slug}.ath`, `?include=ath` on
  `/v1/coins`) is now derived from `prices_1d.vwap` instead of
  `prices_1d.high_price`**. The single-tick max was being polluted
  by sub-stroop dust trades — XLM was reporting an ATH of $1.03
  on r1 because a single 1-stroop ↔ 1-stroop SDEX dust ManageOffer
  cross set `max(quote/base) = 1.0` for the day. Day-VWAP is
  volume-weighted and naturally rejects dust. Same family of fix
  as `/v1/ohlc` outlier filtering. R-008 in
  `docs/review-2026-05-10.md`.

### Changed

- **`/v1/ohlc` applies a 4σ outlier filter by default**. OHLC's
  High/Low have no statistical robustness — a single 1-stroop ↔
  1-stroop SDEX dust ManageOffer cross at the offer-book boundary
  was pinning XLM/USD `high=$1.0000000000` on the live r1 surface
  even though the real cluster was at ~$0.168. The handler now
  routes trades through `aggregate.FilterOutliers` (already used by
  the aggregator orchestrator and `/v1/vwap`) before
  `aggregate.ComputeOHLC`. New `?outlier_sigma=N` query param lets
  callers tune the threshold; pass `outlier_sigma=0` to opt out for
  raw extremes (the explorer's "show every print" view). The
  per-bar volume + trade_count fields reflect the post-filter set,
  matching the High/Low semantics. R-007 in
  `docs/review-2026-05-10.md`.

### Fixed

- **`/v1/price/batch` no longer silently drops asset_ids whose price
  comes via the stablecoin → fiat:USD proxy chain**. The batch path
  inlined only the Redis-VWAP and fiat-cross-rate fallbacks, so
  asset_ids that returned 200 from the single-asset `/v1/price`
  (because they hit `tryStablecoinFiatProxy`) were missing from the
  batch envelope without warning. `fetchBatchRow` now shares the
  full three-layer `priceFallback` chain with `handlePrice`.
  Regression test added in `price_batch_test.go`.
  R-005 in `docs/review-2026-05-10.md`.

## [v0.5.0-rc.39] — 2026-05-10

### Fixed

- **`/v1/changes/coin/{id}` accepts friendly slugs alongside the
  canonical asset_id**. The change-summary worker writes rows under
  the canonical form (`native`, `crypto:XLM`, `USDC-GA5Z…`); a
  caller passing the friendly slug "XLM" or just "USDC" without
  the issuer suffix was silently 404'ing against the strict-
  equality lookup even when the underlying data existed. Caught
  during the 2026-05-08 prod audit (`/v1/changes/coin/XLM` and
  `/v1/changes/coin/native` both 404'd despite the worker having
  written rows). Handler now expands the input into the same
  candidate set `oracleAssetCandidates` uses for /v1/oracle/latest
  (`XLM` → `[XLM, native, crypto:XLM]`) and tries each in order.
  First hit wins; storage errors short-circuit. Pinned by 9 unit
  tests in `changes_test.go`.
- **`/widgets` showcase no longer renders broken iframes**. The
  hardcoded examples referenced asset_id forms (`USDC-GA5Z…`,
  `AQUA-GBNZ…`) and a synthetic stablecoin-fiat pair
  (`native~fiat:USD`) that aren't in the embed routes'
  `generateStaticParams` output, so the iframes 404'd in the
  showcase itself. Aligned the examples with what's actually
  pre-rendered: friendly slugs (`USDC`, `AQUA`) for the asset
  embed and the existing real XLM/USDC pair for the pair embed.
- **`/v1/observations` 8s ceiling on the trades hypertable scan**.
  The handler was missing from the cold-path timeout series shipped
  in #1082, #1099-#1106 — a deliberate prod test on 2026-05-08
  (`asset=native&quote=USDC-G…`) hit a 10s curl timeout against the
  unguarded handler. Now wraps the reader call in
  `context.WithTimeout(8s)`; on deadline returns
  `503 application/problem+json` with `type=observations-timeout`,
  matching the rest of the family.
- **Auth-failure problem+json `type` URL spelling unified**. The
  middleware-level 401 (no-auth-at-all) and the account-handler
  401 (auth-needed-but-rejected) had drifted to two different
  `type` URLs — `errors/unauthorized` (American, middleware) and
  `errors/unauthorised` (British, account.go × 5). Clients keying
  on the type URL saw two distinct error categories for what's
  semantically one auth failure surface. Standardised on the
  American spelling (matches HTTP-spec wording: "Unauthorized");
  all 5 `account.go` call sites updated. No tests pinned the
  British form so no test churn.
- **Explorer home: `HomeCurrencies` and `HomeTopMarkets` now
  show a "couldn't load" notice on error instead of silently
  rendering nothing**. Previously both components had
  `if (isError) return null;` — so when /v1/markets panicked
  (PR #1233 fix) or /v1/currencies stalled, the entire
  section silently disappeared from the homepage and visitors
  had no signal that something was wrong vs. the section just
  not existing. The new notice points to the full /currencies
  + /markets pages (which use a different fetch path) and to
  status.ratesengine.net for ongoing incident context.
- **Dispatcher tx-read errors are no longer silently swallowed** —
  `internal/dispatcher/dispatcher.go::ProcessLedger`'s "skip
  malformed tx, keep processing the ledger" branch had no
  instrumentation: a `LedgerTransactionReader.Read` failure
  silently dropped the tx and the only signal was a downstream
  price gap days later. Now bumps a `Stats().TxReadErrors`
  counter that the statsflush periodic snapshot surfaces at
  WARN whenever the delta in a flush window > 0. Same pattern
  as the existing `decodeErrors` per-source counters; sits
  outside the per-source rows schema because tx-read failures
  aren't attributable to a single source. Doc comment in the
  Stats type + the inline skip both updated to reflect the
  new instrumentation. (No-op on healthy r1 today — the value
  is the alarm path the moment a corrupt LCM lands in
  Galexie.)
- **Divergence sink failures now log at WARN instead of being
  silently swallowed** —
  `internal/divergence/worker.go::flushObservations` discarded
  the `RecordObservation` error with `_ = ...`. So when
  Postgres was struggling (e.g. during the 2026-05-09 disk-full
  SEV-2 cascade) every divergence_observations row was lost
  with no signal. Operators only saw the gap days later when
  the explorer's /divergences page surfaced missing data.
  `ServiceOptions` now takes an optional `*slog.Logger`; when
  set, sink failures log per (pair, reference) at WARN. The
  Redis cache write (load-bearing for `flags.divergence_warning`)
  remains the priority — sink failure does NOT abort the
  refresh path. Aggregator passes its component logger; the
  API binary doesn't construct a sink so the field stays nil
  and the path is no-op.

### Added

- **SDK godoc examples for `PriceTip`, `Sources`, `Markets`,
  `OHLC`** (`pkg/client/example_test.go`). Each is a runnable
  example with canned httptest server response + asserted
  `// Output:` comment so the doc is verified at build time and
  surfaced in pkg.go.dev. Picks the four most-likely-to-be-used
  methods after `Price` / `Asset` (already had examples) — the
  PriceTip / OHLC pair backs every "live UI" use case, while
  Markets / Sources back the catalogue / source-attribution
  surfaces.
- **SDK godoc examples for `PriceBatch`, `Coins`, `Coin`, `Pair`**
  (`pkg/client/example_test.go`). Round 2 of the godoc-coverage
  push — picks the next four highest-traffic methods that lacked
  examples on pkg.go.dev. PriceBatch (the recommended bulk path
  per the Freighter RFP), Coins + Coin (what powers the
  explorer's `/assets` and `/assets/<slug>` pages), and Pair
  (per-source attribution for one market). Each example follows
  the established httptest + `// Output:` pattern so it's
  verified at build time.
- **SDK godoc examples for `Issuers`, `Issuer`, `AssetMetadata`,
  `History`, `Currency`, `Cursors`** (`pkg/client/example_test.go`).
  Round 3 of the godoc-coverage push — closes the gap on the
  remaining customer-facing methods. Together with the prior two
  rounds, the SDK now has runnable examples for every Client
  method a typical integration would touch in week one (catalogue
  + pricing + history + diagnostics surfaces). Each example uses
  the established httptest + asserted `// Output:` pattern so it
  surfaces in pkg.go.dev AND verifies at build time.
- **Explorer redirects for `/incidents`, `/converter`,
  `/oracles/<name>`** (404-audit follow-up, 2026-05-10).
  `/incidents` and `/incident/<slug>` now bounce to
  status.ratesengine.net (the canonical incidents host —
  postmortems live there, the explorer never had a listing
  page). `/converter` (the muscle-memory typo for `/convert`)
  and bare `/convert` now land on `/convert/USD/XLM/` instead
  of 404. `/oracles/<name>` bounces to the `/oracles` listing
  until per-oracle detail pages exist. CF Pages applies all
  five 301s pre-render so they're cheap.
- **Common-name 404 redirects on the explorer.** The 2026-05-10
  audit found several natural URL guesses returned the
  static-export 404 catch-all (`/pool`, `/pools`, `/coin`,
  `/token`, `/tokens`, `/price`, `/prices`, `/api/`, `/docs`,
  `/docs/<path>`). New 301 redirects send each to its canonical
  destination: `/pool*` → `/dexes/`, `/coin|/token*` →
  `/assets/`, `/price*` → `/markets/`, `/api/` →
  `api.ratesengine.net`, `/docs*` → `docs.ratesengine.net`
  (splat-preserved deep-link). 19 new rules in
  `web/explorer/public/_redirects`. (PR #1232)
- **`/llms.txt` for explorer** — llmstxt.org-spec discovery file
  for AI agents indexing the site. Single hand-curated markdown
  manifest pointing at the API surface, key endpoints, the
  ingest sources, the methodology, the SDK, license + status.
  Lives at `web/explorer/public/llms.txt` so CF Pages serves it
  at the well-known path. Caught from a 404 audit (2026-05-10):
  curl-of-`/llms.txt` returned 404 while the 404-fallback page
  loaded a full bundle just to render a stub.
- **`/v1/coins` and `/v1/coins/{slug}`: `issuer_scam_reason` field**.
  When an asset's issuer G-strkey appears on the curated scam
  directory (sourced from stellar.expert, same data the
  `/v1/issuers` family already exposes), the field is non-empty.
  Closes a security UX gap: previously a user landing on
  `/assets/{slug}` for a known-scam asset saw no warning until the
  IssuerPanel completed its async fetch — now the field comes back
  on the build-time response and the explorer renders a red banner
  above the price block at first paint. Always omitted for native
  XLM (no issuer) and for issuers we have no scam record on.
- **`?source=<name>` filter on `/v1/diagnostics/cursors`** —
  exact-match filter on the source column. Caught from a r1
  audit: the param was being silently ignored, so an operator
  asking for `?source=ledgerstream` to isolate the live cursor
  from the ~50 backfill rows got everything. Composes with
  `?max_age=` (both filters apply). Unknown values return an
  empty array (not 400) — predictable for typos vs. brand-new
  sources. OpenAPI updated; tests pin the filter shape and the
  source+max_age composition.
- **`pkg/client`: `VWAP`, `TWAP`, `Pools` SDK methods** — closes the
  remaining gap in the Go SDK's coverage of the v1 surface. New
  shared `AggregateQuery` shape feeds both VWAP and TWAP (TWAP
  silently ignores `OutlierSigma` — kept on the shared shape for
  ergonomic reuse). `Pools` carries a `PoolsQuery` with
  Source/Base/Quote/Asset filter dimensions and the standard
  cursor/limit/order_by pagination shape. New wire types:
  `VWAPResult`, `TWAPResult`, `Pool`. Five tests pin happy-path
  round-trips, query-param shape, and required-field validation.
  Supersedes the stale PR #1124 (whose branch had drifted into
  conflict). (PR #1226)
- **HSTS on the explorer + status site** — both surfaces were
  missing `Strict-Transport-Security`, leaving them vulnerable
  to a downgrade-protocol-stripping attack on first visit.
  Added `Strict-Transport-Security: max-age=31536000;
  includeSubDomains` to `web/explorer/public/_headers` (both
  `/*` and `/embed/*` blocks; CF Pages doesn't merge rules) and
  created `web/status/public/_headers` with the same shape +
  full CSP / X-Frame-Options / Permissions-Policy parity.
  `preload` is intentionally omitted until the operator
  submits the apex to https://hstspreload.org/ (preload is
  irrevocable once browsers ship it; ratchet up in two steps).
- **SDK godoc examples for `Healthz`, `Readyz`, `Version`,
  `Usage`, `CreateKey`, `RevokeKey`, `Keys`**
  (`pkg/client/example_test.go`). Round 4 / final round of the
  godoc-coverage push. Closes the gap on the auth-flow
  (CreateKey/RevokeKey/Keys/Usage) and basic health probes
  (Healthz/Readyz/Version) that were the last methods without
  runnable examples on pkg.go.dev. SDK now has examples for
  every public Client method (26 methods, 27 examples — Pair
  + Markets each have one).
- **ADR-0026 — Stablecoin → fiat proxy is late-binding
  aggregator policy, not eager ingest normalisation**
  (`docs/adr/0026-stablecoin-fiat-proxy-late-binding.md`).
  Records the implicit-from-the-start policy that a flurry
  of API-side fallback PRs (#1217 / #1218 / #1219 / #1224 /
  #1225 / #1226) each instantiated. Captures: the
  late-binding-vs-eager-rewrite tradeoff (depeg detection,
  per-stablecoin signal preservation, reversibility), the
  default peg list (USDT/USDC/PYUSD/EUROC/EUROB/MXNe), the
  operator runbook for a depeg event (remove the affected
  peg from `api.peg_aliases`), and the cross-region
  byte-identical contract this introduces (every region
  ships the SAME peg list; cross-region monitor verifies
  config hash). The policy was previously documented only
  in CLAUDE.md "things that will surprise you" + scattered
  PR descriptions.
- **Three new Prometheus alert rules backing the 2026-05-10 incident
  postmortem** (#1228 ships the runbook + customer-facing post;
  this PR ships the rules that prevent silent recurrence):
  - `ratesengine_node_root_disk_full` (P1) / `_warning` (P2) —
    `ratesengine_timescale_disk_full` only watched
    `/var/lib/postgresql` (own ZFS dataset, plenty of free space);
    the root FS that actually filled wasn't covered. New rule
    watches `mountpoint="/"`.
  - `ratesengine_redis_writes_blocked` (P1) —
    `redis_rdb_last_bgsave_status == 0` for > 60 s. Catches the
    same incident from a different angle: Redis can't snapshot,
    refuses every write, aggregator VWAP cache stops refreshing,
    `/v1/price` 404s on rewritten/triangulated/proxy pairs.

  Both alerts link `redis-write-blocked-disk-full.md` (cherry-picked
  here so the doc-lint orphan check is satisfied without ordering
  dependency between this PR and #1228). (PR #1229)
- **Seed `configs/example.toml` documents Chainlink crypto +
  FX feeds + the "must overlap with aggregator pairs" gotcha**.
  Audit on 2026-05-10 found r1's Chainlink feeds were
  configured only for fiat:EUR/GBP/JPY × USD — pairs the
  aggregator's default coverage doesn't compute, so the
  divergence worker had no overlap to cross-check and
  `divergence_observations` was silently empty. The seed
  config previously documented only the fiat:EUR/fiat:USD
  example, leading every fresh operator down the same path.
  Now documents the matching crypto-pair feed addresses
  (BTC/USD, ETH/USD, LINK/USD) that align with the
  aggregator's built-in default — so a stock deployment
  populates divergence_observations out of the box once the
  operator copies the crypto-feed block. Operator action
  needed on r1 to add the crypto feeds (tracked).
- **Runbook for the `fx_quotes` hypertable / migration 0028 gap.**
  Captures the 2026-05-10 finding that r1's DB is at migration
  0027 (PR #1041's migration 0028 was never applied), so the
  forex worker WARN-spams `pq: relation "fx_quotes" does not
  exist` on every refresh tick and `/v1/currencies/EUR.history_1y`
  / `.history_all` stay empty (customer-visible regression of
  task #104). New runbook at
  `docs/operations/runbooks/fx-history-missing.md` documents the
  triage + 5-min recovery (scp migration → `ratesengine-migrate
  up` → confirm forex worker resumes → optional 10y backfill via
  `scripts/ops/fx-history-backfill`). Cross-linked from
  alerts-catalog + external-poller-stale. Prevention notes
  capture the choice between auto-migrate-in-deploy-workflow
  vs. gate-readyz-on-schema-version. (PR #1230)
- **Runbook + customer-facing incident post for the 2026-05-10
  Redis-write-blocked outage** — r1's root filesystem reached
  100% with 35 GB of stale logs, blocking Redis snapshots,
  blocking aggregator VWAP cache writes, and surfacing as
  `/v1/price` 404s on rewritten / triangulated / stablecoin-proxy
  pairs for ~9 hours. New runbook at
  `docs/operations/runbooks/redis-write-blocked-disk-full.md`
  captures triage signals + the 5-minute recovery sequence
  (vacuum journal, truncate syslog.1, rm WASM-audit stderr,
  trigger Redis BGSAVE). Customer-facing incident post at
  `internal/incidents/data/2026-05-10-redis-writes-blocked-disk-full.md`
  is auto-served by `/v1/incidents`. (PR #1228)
- **ADR-0025 — Caddy trusts Cloudflare for client-IP signal via
  CIDR-pinned static list** (`docs/adr/0025-caddy-cloudflare-trusted-proxy.md`).
  Records the architectural commitment from PR #1239: Caddy's
  global `servers { trusted_proxies static <CF CIDRs> }` block
  pins trust on CF's published IP ranges (refreshed manually
  on quarterly audit cadence rather than via the third-party
  `caddy-cloudflare-ip` plugin). R2 / R3 inherit the same
  topology when they ship; if we ever expose the API directly
  without CF in front, the operator MUST delete the
  `trusted_proxies` block on that listener — calling that out
  in writing prevents a foot-gun. The ADR README index also
  gets caught up — entries for ADR-0020 through ADR-0024
  (already-accepted but not previously indexed) added in the
  same change.
- **`/v1/pools?asset=<asset_id>` filter** — restrict the pools
  listing to rows where the asset appears on either side (base
  OR quote). Mirrors the same filter shape just shipped on
  `/v1/markets` (#1189). Backs the explorer's `/assets/{slug}`
  Liquidity tab — single API request instead of two parallel
  `?base=` + `?quote=` fetches with client-side merge. Mutually
  exclusive with `?base=`/`?quote=` (the OR-shape and AND-shape
  filters can't be mixed); combining returns 400
  `conflicting-filters`. Invalid asset_ids return 400
  `invalid-asset-id`. (PR #1190)
- **`/v1/markets?asset=<asset_id>` filter** — restrict the markets
  listing to pairs where the given canonical asset_id appears on
  either side (base OR quote). Mirrors the `?base=` / `?quote=`
  filters already on `/v1/pools`. Backs the explorer's
  `/assets/{slug}` Markets tab so long-tail assets that fall
  outside the global top-100 by volume now surface their markets
  correctly. Mutually exclusive with `?source=`; combining the
  two returns 400 `conflicting-filters`. Invalid asset_ids
  return 400 `invalid-asset-id` (silent-empty-page guard, same
  family as `?source=`). (PR #1189)
- **`pkg/client`: SDK coverage gap fully closed**. Final batch
  after #1122/#1123/#1124. Adds `Client.Chart`,
  `Client.Observations`, `Client.ChangeSummary`, `Client.Incidents`,
  `Client.SACWrappers` with full wire types (`ChartSeries`,
  `ChangeSummary`, `IncidentsPayload`, `Incident`) and 6 unit
  tests. Every endpoint registered in `internal/api/v1/server.go`
  now has a typed Go-client wrapper. Together the four batches
  added 14 methods, 9 wire types, and 23 unit tests.
- **`pkg/client`: `Currencies(ctx, opts)` + `Currency(ctx, ticker)`**
  SDK methods for `/v1/currencies` and `/v1/currencies/{ticker}`.
  Mirrors the wire shapes the explorer's `/currencies` and
  `/currencies/{ticker}` pages already consume — `RateUSD` is
  "1 USD = N units of this currency" per the server contract,
  and `*float64` pointer fields preserve the "no data" vs "0"
  distinction on circulating-supply / market-cap. Detail variant
  adds `InverseUSD`, `CrossRates` and a 7-day history strip.
- **`pkg/client`: `LendingPools(ctx)`** SDK method for
  `GET /v1/lending/pools`. Mirrors the wire shape of every Blend
  pool observed in the trailing 7d auction stream — `LendingPool`
  struct stays additive (TVL / utilisation / supply+borrow APYs
  land in subsequent server releases without needing an SDK
  bump, since the JSON decoder ignores unknown fields).

### Security

- **API binary's `/metrics` now refuses non-loopback callers
  (defense-in-depth)**. PR #1172 added the Caddy block at the
  edge, but a probe today found `curl https://api.ratesengine.net/metrics`
  still returning 1372 lines of Go runtime + per-source counter
  data — the operator hasn't re-applied the Caddyfile yet
  (operator action pending). Adds a Go-layer gate
  (`loopbackOnly`) so the binary itself returns 404 to any
  RemoteAddr that isn't 127.0.0.0/8 or ::1 — the local
  Prometheus scraper still reaches it via 127.0.0.1:3000, but
  any reverse proxy that forwards public traffic gets a clean
  404. Returns 404 (not 403) deliberately so a scanner can't
  confirm the route exists. Six unit tests pin both branches
  (3 non-loopback IP families × 404, 3 loopback addrs × 200).
  Caddy block stays as the primary protection; this is the
  belt-and-braces second layer that catches misconfiguration.
  (PR #1207)
- **Caddy `Caddyfile.api` now 404s `/metrics` from the public
  `api.ratesengine.net` host**. The API binary serves /metrics on
  :3000 alongside /v1/* (one ServeMux), and the catch-all
  `reverse_proxy` was forwarding the public hit straight through
  — verified live: `curl -s https://api.ratesengine.net/metrics`
  returns 8KB+ of Go runtime stats, request counters, and per-
  source ingest gauges that fingerprint the deployment for any
  attacker. Local Prometheus scraping uses
  `127.0.0.1:3000/metrics` per `prometheus.r1.yml` and is
  unaffected; status.ratesengine.net is the right surface for
  public transparency. Operator action: re-apply via
  `ansible-playbook configs/ansible/playbooks/r1.yml --tags caddy`.
- **`Vary: Origin` now emitted on every CORS-enabled response in
  exact-match mode**, not just when the request's Origin matched
  the allow list. Pre-fix, a cacheable response served to a
  no-Origin request (curl, server-side fetch, monitoring probe)
  was cached at the CDN without origin discrimination — a later
  browser request whose Origin WOULD have been allowed would
  receive that cached "no CORS" response and fail client-side
  fetch(). The inverse poisoning vector also closes: a response
  cached with one allowed Origin's `Allow-Origin: <a>` could
  previously be served to a request from a different allowed
  Origin `<b>`, breaking that client too. Wildcard mode is
  unaffected (response is origin-independent so Vary would just
  defeat caching). Two regression tests pin both branches.
- **`ratesengine_api_cache_ops_total{cache,op,result}` Prometheus
  counter** for in-memory cache wrappers
  (`v1.CachedMarketsReader`, …). Result is `hit` (returned cached
  value, including single-flight-wait callers) or `miss` (called
  upstream). Op breaks down per cached method
  (`distinct_pairs` / `source_markets` / `asset_markets` /
  `all_pools`). Motivation: three back-to-back prewarm-key drift
  bugs (#1185 / #1194 / #1195) where the prewarm warmed one key
  but user requests looked up another; each was invisible to
  tests + log-greps and only surfaced from live latency probes
  ("dex pools take forever"). With this counter an alert on
  `rate(...{result="miss"}[5m]) / rate(...[5m]) > 0.5` sustained
  catches the next drift in minutes instead of days. (PR #1196)
- **Prometheus alert `ratesengine_api_cache_miss_rate_high`** wired
  to the new counter. Fires P2/ticket when miss rate > 50% sustained
  10 min on any (cache, op) with ≥ 0.1 req/s traffic. The traffic
  floor avoids flapping on quiet caches; the ratio (not absolute)
  threshold means a low-volume cache with 100% miss won't page but
  a high-volume cache with 50% miss will. Runbook
  [cache-miss-rate-high.md](docs/operations/runbooks/cache-miss-rate-high.md)
  walks the operator through diffing prewarm vs handler args
  (which is what we did manually for #1185 / #1194 / #1195).
  (PR #1197)
- **`ratesengine_api_cache_ops_total` extended to `coins` and
  `sources_stats` cache wrappers.** PR #1196 only instrumented
  `markets`; this fills in the other two so the existing alert
  (#1197) catches drift on every cached endpoint, not just the
  ones that motivated the original bugs. New op labels:
  `coins/list_coins`, `coins/price_history_24h`,
  `coins/price_history_7d`, `sources_stats/source_stats`,
  `sources_stats/volume_history_24h`. (PR #1198)

### Documentation

- **Clarify the source-count semantic gap between
  `/v1/network/stats` and `/v1/status`**. Both endpoints expose
  a field called `total_sources`, but they measure different
  things: network/stats counts entries in the static binary
  registry (constant per-build); status counts sources the
  operator has enabled at runtime (Prometheus-derived, region-
  scoped). On r1 today registry=21, enabled=17, active=15. The
  semantic gap is by design — keeping the names in separate
  envelopes prevents collision in any single response — but the
  contrast was undocumented. Updated docstrings on
  `internal/api/v1.NetworkStats` + the OpenAPI descriptions on
  both endpoints so SDK consumers don't need to spelunk to find
  out which one they want.

### Performance

- **Cacheable read endpoints now emit `public, max-age=60,
  s-maxage=300`** instead of falling through to the conservative
  `private, no-store` default. Eight surfaces were missing from
  the `policyForPath` table — verified live with `curl -sI`:
  `/v1/coins/{slug}`, `/v1/currencies`, `/v1/currencies/{ticker}`,
  `/v1/chart`, `/v1/lending/pools`, `/v1/network/stats`,
  `/v1/sac-wrappers`, `/v1/incidents`, `/v1/pools`. Each was
  bypassing the CDN AND telling the browser not to cache,
  multiplying origin load on every page render
  (`/v1/network/stats` and `/v1/sac-wrappers` each fire on every
  explorer page load). Unblocks Cloudflare's edge from absorbing
  the explorer's hot path.

### Fixed

- **`/v1/status` no longer reports `overall: ok` when the
  metrics backend is unreachable**. Caught on r1 2026-05-10:
  Prometheus had been dead for 18 h (TSDB corruption from the
  preceding day's disk-full SEV-2), every backend query (heartbeats,
  latency, freshness, incidents) errored out, and the rollup
  logic happily reported `overall: ok` because the "degrade"
  branches all lived inside the `err == nil` blocks. With the
  metrics pipeline blind, the response was a confident lie.
  `/v1/status` now sets `overall: degraded` whenever any
  backend query fails. Test added pinning the regression.
- **`/v1/coins/{slug}` now accepts canonical asset_id form
  (`USDC-GA5Z…`) alongside friendly slug (`USDC`).** Pre-fix,
  copying a canonical asset_id from any other API surface
  (`/v1/assets/{id}.asset_id`, `/v1/markets[].base`,
  `/v1/observations[].base_asset`) into `/v1/coins/<id>` got
  404 — inconsistent with `/v1/assets/{id}` which accepts both.
  Confirmed broken on r1 (`/v1/coins/AQUA-GBNZILSTV…` 404'd while
  `/v1/coins/AQUA` 200'd against the same row). Fix is a one-line
  SQL widening: `WHERE COALESCE(slug, code) = $1 OR asset_id = $1`
  plus an `(asset_id = $1) DESC` ORDER BY tiebreak so a friendly
  slug input still wins over a code-only collision (preserving
  the #45 scam-token disambiguation guard). Handler adds a
  `canonical.ParseAsset` short-circuit so the canonical-form
  path skips the case-insensitive retry it doesn't need.
  (PR #1231)
- **`/v1/oracle/prices` now applies the same X/fiat:USD → X/<peg>
  stablecoin-fiat proxy fallback** as `/v1/oracle/lastprice`
  (#1220) and the other X/fiat:USD surfaces. Pre-fix, the SEP-40
  `prices()` passthrough returned 200 with an empty `data` array
  for any asset that trades only against classic USDC — same
  out-of-the-box failure mode as `/v1/oracle/lastprice` had
  pre-#1220, just expressed as 200-empty rather than 404. Adds a
  shared `recentClosedWithStablecoinFallback` helper that walks
  the operator's classic USD pegs in priority order; first peg
  with non-empty closed buckets wins. Response carries
  `flags.triangulated=true` so the wire shape is honest about the
  derivation. (PR #1224)
- **F2 fields on `/v1/assets/{id}` (`market_cap_usd`, `fdv_usd`,
  `change_24h_pct`) now populate via the same X/fiat:USD →
  X/<peg> stablecoin-fiat proxy fallback that #1217 added to
  `/v1/price`**. The F2 path's `lookupUSDPrice` and the binary's
  `storeChange24hReader` both bypass the v1 handler's
  `priceFallback`, so even with #1217 deployed every asset on
  Stellar mainnet had `market_cap_usd / fdv_usd / change_24h_pct`
  silently null — the steady-state because nothing on-chain ever
  quotes in fiat:USD. `lookupUSDPrice` now calls the existing
  `tryStablecoinFiatProxy` helper on miss; `storeChange24hReader`
  walks the operator's `[trades].usd_pegged_classic_assets` for
  the at-or-before lookup. One new test
  (`TestLookupUSDPrice_StablecoinFiatProxyFallback`) pins the
  /v1/assets path; existing TestChange24hPct tests still pass.
  (PR #1223)
- **Explorer `/exchanges/<venue>` chart now distinguishes "API
  outage" from "no pairs reporting"**. The pair-list fetcher's
  `.catch(() => setPairsLoading(false))` swallowed every error,
  so a 5xx on `/v1/markets?source=<venue>` rendered the same
  "No pairs reporting in the last 14 days" empty-state as a
  genuinely-empty venue. Now captures the error message into
  `pairsError` state and surfaces it as a red "Couldn't load
  pairs for this venue (HTTP 503). Refresh to retry, or check
  status.ratesengine.net" panel — operators investigating a
  user-reported "exchange page is broken" can now distinguish
  data gap from infra gap at a glance. Same silent-drop family
  as the home-page fixes shipped in #1251. (PR #1254)
- **Kraken dust trades now use the typed `ErrDustTrade` sentinel**
  — extends the #814 / #1234 pattern (Coinbase / Binance /
  Bitstamp) to Kraken. Before this PR the live `parse.go` path
  had NO dust check at all — a sub-precision-floor live trade
  would have produced a Trade with quote=0, the canonical
  validator would reject on insert, and the indexer would
  log "insert trade failed" at ERROR per frame (the same
  pattern that flooded r1 logs for Bitstamp until #1234).
  Backfill already had a check but used a generic
  `fmt.Errorf("zero quote")` rather than the typed sentinel
  the consumers explicitly understand. Kraken isn't enabled on
  r1 today (see `[external.kraken].enabled` in r1's TOML) so
  this is a latent-bug fix — closing it now means flipping
  Kraken on later doesn't surprise the operator with a fresh
  ERROR storm.
- **CoinGecko divergence reference now has a built-in default
  IDMap matching the aggregator's default coverage** —
  `internal/divergence/coingecko.go`. Caught from r1 on
  2026-05-10: the type-level docs claimed "empty IDMap falls
  back to a built-in default covering XLM + major stables"
  but the constructor copied opts.IDMap as-is with no
  fallback. Result: every operator without an explicit
  `[divergence.coingecko].id_map` got `asset_unsupported`
  failures for every divergence cross-check call —
  `divergence_observations` silently empty, `flags.divergence_warning`
  always false, the Compare-layer "ok" counter incremented
  while no actual cross-check happened (the aggregator's
  refresh metric showed 23,889 "ok" outcomes on r1 with zero
  rows in the durable mirror). Default IDMap now covers the
  canonical asset_id forms the aggregator computes by default
  (`crypto:XLM` / `native` / `crypto:BTC` / `crypto:ETH` /
  `crypto:LINK` / `crypto:SOL` / `crypto:ADA` / `crypto:DOT`)
  plus major USD stablecoins (USDC / USDT / PYUSD) for
  cross-checks against the underlying X/USDC or X/USDT path
  enabled by ADR-0026. Operator entries merge OVER the
  defaults so anyone who relied on the pre-fix behaviour can
  still narrow the set.
- **Caddy now resolves the real client IP from Cloudflare** —
  `configs/caddy/Caddyfile.api`. The previous config rewrote
  `X-Forwarded-For` to `{remote_host}` (the immediate TCP peer,
  i.e. a CF edge POP), so every API request looked like it came
  from a Cloudflare IP. Per-IP rate-limit buckets became
  per-CF-edge buckets — a single CF edge hitting the burst
  threshold blocked every customer behind it. Access logs were
  similarly useless (every `remote_ip` was a 162.158.x.x or
  104.22.x.x CF edge, never the actual customer). Fix: add a
  global `servers { trusted_proxies static <CF CIDRs>;
  client_ip_headers CF-Connecting-IP X-Forwarded-For }` block
  and forward `{client_ip}` instead of `{remote_host}` from the
  `reverse_proxy` directive. Trust is CIDR-pinned to CF's
  published ranges so an attacker hitting the box's IP directly
  can't spoof `CF-Connecting-IP`. README documents the
  CIDR-refresh cadence.
- **CoinGecko poller now grows the cooldown exponentially even
  when the venue's `Retry-After` is short** — pre-fix the
  Retry-After branch took the hint at face value (clamped to
  `MinBackoff = 60s`) and bypassed the doubling. CoinGecko's
  free tier returns Retry-After consistently below `MinBackoff`
  (≈30s), so clamping landed the cooldown at exactly 60s
  forever. The runner's PollInterval is also 60s, so each
  recovery attempt produced another 429 → another 60s cooldown
  → indefinite throttling at one 429-per-minute. Observed live
  on r1 2026-05-09 → 2026-05-10. Post-fix, `applyBackoff` treats
  Retry-After as a FLOOR — cooldown is `max(hint,
  currentBackoff×2, MinBackoff)` clamped to `MaxBackoff` — so
  consecutive 429s grow exponentially regardless of what the
  venue claims you can retry after. Two new tests pin both shapes.
  (PR #1227)
- **Bitstamp dust trades silently dropped** instead of being
  logged as ERROR on every frame. Tiny lots (e.g. 1e-8 XLM at
  $0.16) compute `base × price ÷ 10^8 = 0` under our integer-scale
  precision floor; the canonical validator was rejecting them
  with `quote_amount must be positive, got 0` and the indexer
  was emitting "insert trade failed" at ERROR-per-frame.
  Following #814's Coinbase + Binance pattern: typed
  `ErrDustTrade` sentinel from `parseTrade` and
  `bitstampCandleToTrade`; the existing streamer / backfill
  error-skip branch absorbs it. Caught from r1 production logs
  on 2026-05-10 — XLMUSD trades flooding the indexer ERROR log.
- **`/v1/ohlc` now applies the same X/fiat:USD → X/<peg> stablecoin
  fallback** as `/v1/price` (#1217), `/v1/chart` (#1015), and the
  vwap+twap pair (#1219). Pre-fix, `/v1/ohlc?base=native&quote=fiat:USD`
  404'd "no trades in window" out-of-the-box on every fresh
  deployment. Freighter RFP §3 names `/v1/ohlc` as a launch-blocker
  for the asset-detail surface, so this gap was visible to every
  asset detail page request. New `ohlcTradesWithStablecoinFallback`
  helper walks the operator's classic USD pegs in priority order;
  first peg with non-empty trades wins. Response carries
  `flags.triangulated=true`. (PR #1225)
- **`/v1/vwap` and `/v1/twap` now apply the same X/fiat:USD →
  X/<peg> stablecoin-fiat proxy fallback** as `/v1/price` (#1217)
  and `/v1/chart` (#1015). Pre-fix, `/v1/vwap?base=native&quote=fiat:USD`
  and `/v1/twap?base=native&quote=fiat:USD` both 404'd "no trades
  in window" out-of-the-box because no on-chain trades quote in
  fiat:USD on Stellar. New helper `tradesInRangeWithStablecoinFallback`
  retries against each operator-declared classic USD peg in priority
  order; first non-empty result wins. Response carries
  `flags.triangulated=true` so wire shape is honest about the
  derivation. Same opt-in shape (empty allow-list still 404s);
  non-USD fiat quotes skip the fallback. (PR #1219)
- **`/v1/oracle/lastprice` and `/v1/oracle/x_last_price` get the
  same X/fiat:USD → X/<peg> stablecoin-fiat proxy fallback** as
  `/v1/price` (#1217). Pre-fix, the SEP-40 passthrough surface
  inherited the same out-of-the-box 404 mode: an on-chain
  integrator drop-in-replacing `lastprice(native)` against XLM
  got 404 even though `/v1/coins/native` showed $0.16 cleanly.
  Same intent as #1217 — keep the SEP-40 surface and the
  closed-bucket surface consistent in coverage so an integrator
  switching between them sees the same set of "available" pairs.
  Two new tests pin the lastprice + x_last_price branches.
  (PR #1220)
- **Default Chainlink feed map covers BTC/ETH/LINK + EUR/GBP/JPY
  vs USD** so divergence cross-checks work out-of-the-box on a
  stock config. Same shape as the CoinGecko default-IDMap fix in
  #1249: the type-level docs implied the operator could deploy
  with no `[divergence.chainlink].feed_map` and still get
  Chainlink cross-checks on the major pairs the aggregator
  computes by default, but `NewChainlinkReference` was copying
  `opts.FeedMap` as-is with no fallback — every lookup returned
  `asset_unsupported`. Defaults pin the immutable Ethereum
  mainnet AggregatorV3 proxy addresses; operator-supplied entries
  merge OVER the defaults so an operator can still narrow the
  set, override an address, or flip an Invert flag. XLM/USD,
  USDC/USD, USDT/USD remain absent — Chainlink does not publish
  these on Ethereum mainnet at audit time. Closes the code-side
  half of operator action #119; the operator no longer needs to
  hand-paste contract addresses into `r1.toml` to unblock
  Chainlink cross-checks on the default pair set. (PR #1255)
- **Nil-pointer panic in markets/coins single-flight cache** —
  caught on r1 production (2026-05-10 15:36 UTC, GET `/v1/markets`).
  When the leader's upstream call failed under single-flight,
  `fetchPairs` / `fetchPools` (and the equivalents in
  `coins_cache.go`) deleted the entry from the map BEFORE closing
  the flight chan. Waiters then woke, re-read `c.entries[key]`,
  got `nil`, and panicked dereferencing `out.pairs`. Fix:
  waiters now hold a pointer to the same entry they joined on
  (so the leader's `delete` can't erase what they read) and the
  leader stashes its err on that entry pre-close. Regression
  test added under `-race`. Untested error-path race still
  caches the err for in-flight waiters but doesn't TTL-cache it
  for new callers — same semantics as before, minus the panic.
- **`/v1/price/tip?asset=X&quote=fiat:USD` gets the same
  stablecoin-fiat proxy fallback as `/v1/price`** (#1217). Tip
  was 404'ing on the same shape — `tipWindowVWAP →
  PriceReader.LatestPrice → tryRedisVWAPFallback → tryFiatCrossRate`
  with no peg-rewrite branch — so a customer reading the
  fastest-feed XLM/USD price endpoint got the same out-of-the-box
  404 as `/v1/price`. Now slots `tryStablecoinFiatProxy` between
  the Redis cache layer and the fiat-cross-rate fallback in
  `computeTip`. Same opt-in shape (empty allow-list still 404s).
  (PR #1218)
- **`/v1/price?asset=X&quote=fiat:USD` now serves via classic-USDC
  peg fallback at handler read time**, mirroring the `/v1/chart`
  fallback shipped in #1015 (task #98). Same root cause: the
  aggregator's `[aggregate].enable_stablecoin_fiat_proxy` is off
  by default, so the literal X/fiat:USD pair never has rows in
  `prices_1m` (no on-chain trades quote in fiat:USD on Stellar).
  Pre-fix, the canonical XLM/USD price endpoint —
  `/v1/price?asset=native&quote=fiat:USD` — returned 404 "no
  trades or oracle observations" out-of-the-box on every fresh
  deployment, even though the aggregator had a perfectly good
  `native/USDC-classic` VWAP cached. The new
  `tryStablecoinFiatProxy` fallback walks the operator's
  `[trades].usd_pegged_classic_assets` allow-list in priority
  order and rewrites the lookup; first peg with a row wins.
  Response carries `flags.triangulated=true` and the wire `quote`
  field echoes the user's request (`fiat:USD`), not the proxy
  peg. Opt-in shape preserved — empty allow-list still 404s.
  (PR #1217)
- **Indexer now WARNs at boot when `[supply]` watched-sets are all
  empty**, instead of silently registering zero supply observers.
  This was the silent-failure mode behind r1's
  `asset_supply_history` sitting at 0 rows for 6+ days post-deploy
  — the operator hadn't populated the watched-sets yet (ops task
  #97), but the indexer logged nothing about it. F2 fields
  (`market_cap_usd`, `fdv_usd`, `circulating_supply`,
  `total_supply`, `max_supply`) on `/v1/assets/{id}` stayed null
  for every asset, with no signal until someone manually queried
  the empty table. The new WARN names the missing config keys
  (`sdf_reserve_accounts` for Algorithm 1 XLM,
  `watched_classic_assets` for Algorithm 2,
  `watched_sep41_contracts` for Algorithm 3) and explicitly states
  the consequence — so the next operator who tails the indexer log
  sees the problem in the first 30 seconds. (PR #1216)
- **`/v1/coins?limit=200` prewarm now matches the handler's
  internal `listingLimit`**. Same family as #1194. The handler
  subtracts 1 from the requested limit when cursor/issuer/q are
  all empty (the `prependNative` path that splices a synthetic
  XLM row at the top of page 1 without overshooting), so a
  `/v1/coins?limit=200` user request actually queries the store
  with `ListCoinsOptions{Limit: 199, …}`. The prewarm passed
  `Limit: 200` so its cache key (`ListCoinsExt|200|…`) never
  matched the handler's lookup key (`ListCoinsExt|199|…`). The
  explorer's `/currencies` page (the most-trafficked coins read)
  was hitting cold cache on every load. (PR #1195)
- **Unfiltered `/v1/pools` prewarm now matches the handler's
  cache key**. Follow-up to PR #1185, which fixed the
  `MarketsOrder` mismatch but missed a second mismatch in the
  Sources dimension. The handler builds
  `PoolsFilter{Sources: v1.DexSourceNames()}` for unfiltered
  requests; the prewarm passed `PoolsFilter{}` (`Sources: nil`).
  Cache key uses `fmt.Sprintf("%v", filter.Sources)` so
  `nil` → `[]` while the handler's slice → `[aquarius comet
  phoenix sdex soroswap]`. Different strings, different keys —
  the unfiltered prewarm warmed a slot no user request ever
  hits. Exported `v1.DexSourceNames` so the prewarm can call
  the same source-of-truth as the handler. Per-DEX prewarm
  (introduced in #1185) was unaffected — its `[]string{src}`
  matches the handler's filtered single-element slice. (PR #1194)

- **`/v1/coins` `change_{1h,24h,7d}_pct` for XLM-triangulated assets
  now reflects USD change, not XLM-denominated change**. Pre-fix
  USDC and PYUSD showed `-3.37%` and `-3.27%` 24h change live on
  r1 — they're stablecoins and never depegged. The `vs_xlm`
  fallback computed `vs_xlm.vwap / vs_xlm_24h.vwap` (raw XLM
  ratio change) while `price_usd` correctly used
  `vs_xlm.vwap × xlm_usd.vwap` (triangulated USD price). For
  USD-stable assets these read inversely: USDC at $1 stays at $1
  even when XLM moves 3% against it. Multiply both sides of the
  change ratio by their respective `xlm_usd_*` factor so the
  change consistently measures the same triangulated USD price
  the row already displays. Same fix applied across `listCoins`
  and `GetCoinBySlug` queries.

- **Explorer home page now emits `<link rel="canonical">`**.
  Detail pages picked it up via #1094/#1095/#1097 but the root
  `/` was left without one, so search engines were free to treat
  `https://ratesengine.net/`, `https://ratesengine.net` (no
  trailing slash), and `https://ratesengine.net/index.html` as
  separate pages and split link equity between them. Default
  `alternates.canonical: '/'` on the root layout fixes that;
  detail pages still override per-route in their own
  generateMetadata.
- **`/v1/oracle/latest?source=<unknown>`** now returns 400
  `unknown-source` instead of an empty 200 list. Same fail-fast
  validation pattern shipped on /v1/markets (#1162) and
  /v1/observations (#1164) — typo'd source names looked
  identical on the wire to "this source has no observation for
  the asset", masking input errors as data gaps.
- **Sitemap URLs now match the canonical trailing-slash form the
  explorer actually serves**. With `trailingSlash: true` in
  next.config.js, every non-trailing-slash URL 308-redirects to
  its trailing-slash form — but every URL the sitemap emitted
  was bare (`/account`, `/issuers/G...`, `/markets/X~Y`), so every
  sitemap entry sent crawlers through a 308 hop before reaching
  the real page. Google penalises sitemaps that contain redirect
  targets. New `siteURL()` helper appends the trailing slash;
  verified live: every URL in the current sitemap returns 308,
  post-fix they all return 200 directly.
- **`<link rel="canonical">` on every top-level explorer page**.
  Audit showed 14 pages — `/diagnostics`, `/methodology`, `/sdk`,
  `/contact`, `/widgets`, `/changelog`, `/aggregators`, `/oracles`,
  `/networks`, `/anomalies`, `/mev`, `/pricing`, `/company`,
  `/careers` — all had `metadata.title` + `description` set but no
  `alternates.canonical`. Search engines were free to treat the
  trailing-slash variant, the no-trailing-slash variant, the
  `index.html` form, and any `?ref=…` referral-tag form as
  separate URLs and split link equity. Each page now declares its
  own canonical alongside the existing meta. Companion to #1167
  (home page) and the per-detail-page canonicals from #1094-1097.
- **`/v1/incidents.atom` summary truncation is now UTF-8-safe**.
  `summaryFromMarkdown` did `p[:397] + "..."` — a naive byte
  slice that could split a multi-byte UTF-8 codepoint in half
  for any incident post containing accented characters
  (é/ñ/ü/…) or emoji. Verified live: a 396-byte ASCII prefix
  + `éée trailing` produces an output where the last byte is
  `\xC3` (the lead byte of `é`) without its trailing byte —
  invalid UTF-8. Strict feed validators reject the entry; the
  explorer's render shows a replacement character. Walk back
  to the nearest rune-start byte at or before 397; tests cover
  both 2-byte (Latin-1 supplement) and 4-byte (emoji) cases.
- **`/v1/incidents` no longer returns `incidents: null` when the
  embedded corpus is empty**. A fresh deployment (or one where
  `incidents.Load` errored at startup) left `s.incidents == nil`,
  which marshalled as `"incidents":null` and broke the
  pkg/client SDK + explorer JS that `.map()` over the array.
  Caught while writing the handler's first regression tests.
- **Asset detail "Markets" tab fetches 100 by volume, not 500
  alphabetically**. `MarketsTabPanel` on `/assets/{slug}` was
  calling `useMarkets(500)` (default `pair` order) then
  client-side filtering to markets involving the asset. Cold-
  cache hit a 5–8s SQL scan (limit=500 isn't in the prewarm set
  of 5/25/100/200), and the alphabetical sort meant the cap
  could miss popular markets. Switched to
  `useMarkets(100, 'volume_24h_usd_desc')` — hits warm cache,
  ~5× smaller payload, and surfaces the asset's top-100-by-volume
  markets first. Long-tail assets outside the global top-100 by
  volume need a server-side `?asset=` filter on /v1/markets
  (only /v1/pools has it today) — tracked as follow-up.
- **Home page no longer fetches 500 markets to render 10**.
  `HomeTopMarkets` called `useMarkets(500, …)` then immediately
  `.slice(0, 10)` — sending and parsing 490 rows the user never
  sees, and missing the API's prewarmed cache key (the prewarm
  covers limits 5/25/100/200, not 500). Cold-cache home loads paid
  the full `/v1/markets?limit=500` SQL scan against the trades
  hypertable. Trimmed to `useMarkets(25, …)` — same top-10 with
  headroom, hits the warm cache, ~20× smaller payload. Same
  pattern previously fixed in `HomeNetworkStrip` (PR-comment
  history). Also corrected the misleading `limit=500` `asExample`
  hints on `/markets` MarketsTable — the actual fetch is
  `limit=100` with the user's chosen order_by + sparkline.
  (PR #1187)

## [v0.5.0-rc.38] — 2026-05-09

### Fixed

- **`/v1/pools` prewarm now matches the handler's default order**.
  The cold-cache user complaint ("dex pools still take forever to
  load") had a single root cause: `prewarmOnce` warmed the cache
  with `MarketsOrder = 0` (`MarketsOrderPair`) while the `/v1/pools`
  handler defaults to `MarketsOrderVolume24hDesc` (= 1). Cache keys
  include the order, so every cold-cache user request still ran
  the 10–30s SQL scan against the live trades hypertable. Live
  measurement on r1 (2026-05-09): `/v1/pools?source=sdex` 27s,
  `soroswap` 16s, `phoenix` 12s, `aquarius` 9s, `comet` 11s. Fix
  pins the prewarm to the handler's explicit default
  (`timescale.MarketsOrderVolume24hDesc` for pools,
  `timescale.MarketsOrderPair` for markets) and adds a per-DEX
  loop covering the canonical `?source=<dex>&limit=100` cache
  variants the explorer's `/dexes/{source}` pages fire. Bumped
  the prewarm context from 20s → 60s so the per-source loop has
  budget to complete a full warm cycle. Subsequent users hit warm
  cache (sub-second) instead of stacking on the cold-query
  timeout. (PR #1185)
- **`/v1/markets?include=sparkline` shares the 8s timeout budget
  with the markets-list query**. Pre-fix, the sparkline batch ran
  on `r.Context()` unbounded, so a 5s markets query + 5s sparkline
  query stacked into a 10s+ request that the gateway terminated.
  Now both phases share `mCtx`; total request capped at 8s with
  graceful degradation (sparkline misses log at WARN and the
  response ships without sparkline data, matching the existing
  best-effort contract).
- **`/v1/issuers/{g_strkey}` accepts case-insensitive G-strkeys**.
  Pre-fix lowercase variants 404'd; chat clients that auto-
  lowercase URLs (Slack, Discord, some search results) and
  copy-paste flows would dead-end. Stellar G-strkeys are
  uppercase base32 per SEP-23 and the underlying ed25519 key is
  the same regardless of case, so the handler now uppercases
  the path segment at input. Companion to PR #1153
  (case-insensitive `/v1/coins/{slug}`).
- **`/v1/coins/{slug}` accepts case-insensitive variants**.
  Pre-fix `/v1/coins/usdc` (lowercase) 404'd while
  `/v1/coins/USDC` returned the row. The `classic_assets.slug`
  column is uppercase by convention (USDC, AQUA, EURC), but URL
  clients frequently lowercase. Add a retry: when the literal
  slug misses, retry once with `strings.ToUpper`. Preserves
  case-significance for the rare issued asset that intentionally
  uses lowercase (Stellar protocol allows it) — the literal form
  wins when both exist. Companion to PR #1132's case-insensitive
  XLM intercept.
- **`/v1/assets/NATIVE` (uppercase) no longer 400s**. The
  canonical `asset_id` format mandates lowercase `native` (per
  ADR-0010), so capitalised variants returned 400
  invalid-asset-id with the long format reminder. The handler
  now collapses the bare `native` token case-insensitively at
  input. Other compound forms (`USDC-Gxxxx`, `CDLZF…`,
  `fiat:USD`) keep their case-significance unchanged — Stellar
  protocol allows issuers to mint case-different classic codes
  and merging them would mask real mismatches.
- **Every 401 response now includes `WWW-Authenticate: Bearer
  realm="ratesengine.net"`** (RFC 7235 §3.1 conformance). Live
  audit on r1 today: `/v1/account/me` returned 401 with no
  challenge header, leaving programmatic clients without a way
  to discover the accepted auth scheme. The auth-middleware
  layer was already setting it on its own 401 paths; the
  handler-level `writeProblem` (used by /v1/account/* directly
  for not-yet-authenticated requests) was missing it. Pin
  added at the helper level so the conditional can't drift.
  Pinned by 2 sub-tests covering the 401 happy path and the
  inverse (4xx/5xx that aren't 401 must NOT set the header).
- **`/v1/markets?source=<unknown>`** now returns 400
  `unknown-source` instead of an empty 200. The silent-empty-page
  anti-pattern (a typed source name looking identical on the wire
  to "this source has no trades") sent callers chasing nonexistent
  data. Validation guard mirrors the same fail-fast pattern shipped
  on /v1/coins (#1134), /v1/markets cursor (#1135), and /v1/pools.
- **`/changelog.atom` no longer publishes the `[Unreleased]`
  section as a syndicated entry.** Every explorer redeploy was
  pinging Feedly / Slack RSS subscribers with what reads as a new
  release: same urn, fresh `published`/`updated` timestamps. The
  rendered `/changelog` page intentionally still shows Unreleased
  so visitors get a forward look — only the syndication feed
  asymmetry changed (atom = dated immutable releases only).
- **`/v1/observations?source=<unknown>`** now returns 400
  `unknown-source` instead of an empty 200 list. Same
  silent-empty-page anti-pattern fix shipped on /v1/markets — a
  typed source name looked identical on the wire to "this source
  has no trades for the pair", masking typos as data gaps.
- **`/v1/coins?cursor=<garbage>` no longer silently returns an
  empty page**. Previously, a malformed cursor parsed as
  `(0, "")` and the SQL keyset predicate
  `(observation_count, asset_id) < (0, "")` matched nothing —
  callers saw a 200 with `{"coins":[],"limit":100}` that looked
  identical to legitimate end-of-pagination, leaving stale
  bookmarks indistinguishable from "you've reached the end".
  Handler now calls `timescale.ValidateCoinsCursor` before
  dispatch and returns `400 invalid-cursor` (problem+json,
  `type=https://api.ratesengine.net/errors/invalid-cursor`)
  with a hint to drop the parameter or pass back a fresh
  `next_cursor`. Empty cursor (no parameter) is still valid.
  Same approach used by `/v1/history` since #1083. Other
  cursored endpoints (`/v1/markets`, `/v1/issuers`,
  `/v1/sources`, `/v1/lending/pools`) currently *reset* to
  page 1 on garbage rather than 400'ing — they have a milder
  failure mode and follow in a separate PR.
- **`/v1/markets` and `/v1/pools` reject malformed cursors with
  400 instead of leaking SQL errors as 500 / silently skipping
  to a wrong page**. Previously:
  - `?cursor=garbage&order_by=volume_24h_usd_desc` raised a
    Postgres `invalid input syntax for type numeric` from the
    embedded `CAST(NULLIF(split_part($cursor, ':', 1), '') AS
    numeric)` and returned 500 (and burned CPU per request).
  - `?cursor=garbage` (default `pair` order on `/v1/markets`)
    fell through to a lexicographic `> $cursor` skip whose
    result depended on Postgres collation — caller saw an
    arbitrary "page" of markets sorted alphabetically past
    `garbage`, indistinguishable from a real page.
  Adds `timescale.ValidateMarketsCursor` that pins the encoded
  shape per `MarketsOrder` (pipe-separated `<base>|<quote>`
  pair suffix, optional digits-with-one-dot vol prefix). Wired
  into both `handleMarkets` and `handlePools` after `order_by`
  parsing — returns 400 problem+json with
  `type=https://api.ratesengine.net/errors/invalid-cursor`.
  Pinned by 19 sub-tests across both orderings; companion to
  the same fix shipped on `/v1/coins` in #1134.
- **`base/quote` endpoints (history, vwap, twap, ohlc, pairs,
  oracle/x_last_price) emit a self-explanatory 400 when the
  caller mistakenly passed `asset` instead of `base`**. The
  endpoints' shared `parseBaseQuote` helper used to flatly
  return `"base query parameter is required"` — leaving
  callers who copy-pasted query params from `/v1/price`
  (which uses `asset`/`quote`) confused about which name to
  use where. Now, when `base` is missing but `asset` is
  present, the detail appends a hint:
  `"this endpoint uses base/quote (not asset/quote — that
  form is on /v1/price)"`. Pinned by
  `TestHistory_MissingBaseWithAssetHint`.
- **Explorer detail pages now emit `og:image` and
  `twitter:image`**. Audited 2026-05-09 against r1: every
  detail page that overrode `openGraph` (assets, currencies,
  issuers, sources, exchanges, dexes, lending, convert,
  blog, research/{adr,discovery,operations,architecture})
  rendered HTML without `<meta property="og:image">`, so
  Slack/Twitter/Discord previews showed only title +
  description with no card image. Cause: Next.js 15 metadata
  inheritance dropped `openGraph.images` from the layout
  default when a page set its own `openGraph` block. Fix:
  new `web/explorer/src/lib/seo.ts` exports
  `SITE_OG_IMAGES` + `SITE_TWITTER_IMAGES` constants; every
  page-level openGraph now spreads them explicitly. The
  underlying asset (`/og.svg`, 1200×630) is unchanged —
  only the per-page metadata wiring.
### Changed

- **Explorer `/diagnostics` cursor table hides stale (>1h) rows
  by default**. Live audit on r1 today: 30 of ~50
  ingestion-cursor rows had `lag_seconds` over 5 days — completed
  backfill jobs whose progress markers were never cleaned up,
  drowning out the live ingest cursor that operators open the
  page to find. New `Hide stale (>1h)` checkbox (default on)
  filters to actively-progressing cursors; toggle off to see the
  full set when investigating a stuck backfill.

### Added

- **`pkg/client.Client.RevokeKey`** SDK method to delete an API
  key (`DELETE /v1/account/keys/{keyID}`). Closes the last CRUD
  gap in the account-keys surface — the SDK already had `Keys`
  (GET) and `CreateKey` (POST). Pinned by 3 sub-tests (happy
  path 204; client-side empty-keyID validation; 404 surfaced as
  typed `*APIError`).

- **`pkg/client`: `NetworkStats(ctx)`** SDK method for
  `GET /v1/network/stats` — single-call home-page snapshot
  (24h volume, market count, indexed-asset count, latest live
  ledger, source counts). New `client.NetworkStats` type
  preserves the `*string Volume24hUSD` per ADR-0003 so callers
  can distinguish "no data" (nil) from "0". Tests cover the
  happy path and the omitempty volume case.
- **`pkg/client`: `Incidents(ctx)`** SDK method for
  `GET /v1/incidents` — every customer-facing incident post the
  binary has embedded, sorted started_at desc. New
  `client.Incident` + `client.IncidentsList` types mirror the
  internal incident wire shape (the SDK can't import
  `internal/incidents` directly per ADR-0005). Tests cover the
  happy path and the empty-list path; ResolvedAt round-trips as
  `*time.Time` so callers distinguish "still open" from "resolved
  at zero time."
- **API: trailing-slash paths 308-redirect to canonical no-slash
  form**. `GET /v1/coins/native/` previously 404'd with
  `errors/not-found` because every v1 route is registered without
  a trailing slash and Go's `net/http` `ServeMux` treats the
  slashed variant as a different path. New
  `middleware.TrailingSlashRedirect` 308's any non-root request
  whose path ends with `/` to the same path with the slash
  stripped (preserving query string and method/body). Closes the
  most common client-side papercut — axios with
  `baseURL: '.../v1/'` joins awkwardly, OpenAPI generators emit
  either form depending on codegen flags, mistyped curls. Pinned
  by 5 sub-tests covering the happy path, query-string
  preservation, root exemption, and 308-not-301
  method-preservation across POST/DELETE/PUT/PATCH.
- **CoinGecko poller backs off on 429 / 403 instead of hammering
  the venue every 60s**. Live audit on r1 2026-05-09 found the
  poller logging `WARN poller error err: http 429: Throttled`
  every minute since uptime — caused by CoinGecko's late-2024
  unauthenticated-tier tightening. New behaviour:
  - On 429 (Too Many Requests) or 403 (post-2024 demo-key-
    required path), arm a cooldown using `Retry-After` (clamped
    to `[60s, 1h]`) or exponential backoff doubling from 60s
    to 1h max.
  - During cooldown, `PollOnce` returns `(nil, nil, nil)` —
    silent skip, no HTTP request, no log spam.
  - First successful response resets backoff to zero.
  Pinned by 15 sub-tests across the cooldown arm, Retry-After
  parsing, exponential branch, success-reset, 403-treated-as-
  throttling, and Retry-After parser edge cases.

### Added

- **CoinGecko demo-tier API key support**. Free signup at
  coingecko.com produces a `x_cg_demo_api_key` that bypasses
  the unauthenticated-tier 429s. Read from env vars
  `COINGECKO_API_KEY` (Pro) or `COINGECKO_DEMO_API_KEY` (Demo)
  in `cmd/ratesengine-indexer/main.go`; Pro wins when both are
  set. No TOML schema change. Operator action to fix the live
  r1 throttling: register a free key at
  https://www.coingecko.com/en/developers/dashboard, set
  `COINGECKO_DEMO_API_KEY=<key>` in
  `/etc/ratesengine.toml.env` (or wherever the systemd unit
  pulls Environment= from), `systemctl restart
  ratesengine-indexer`.
- **External-poller observability**: new
  `ratesengine_external_poller_polls_total{source, outcome}`
  counter (`outcome` ∈ {success, error, skipped}) and
  `ratesengine_external_poller_last_success_unix{source}` gauge
  emitted by the runner on every poll tick. Two new alerts:
  `ratesengine_external_poller_stale` (P2, fires when no
  successful poll in 30 min for 5+ min) and
  `ratesengine_external_poller_error_rate_high` (P3, fires when
  error rate > 50 % sustained 15 min). Closes the blind spot
  shipped on r1 2026-05-09 where CoinGecko throttled for 13 h
  with no metric or alert — only a per-minute WARN log. New
  runbook `external-poller-stale.md` with the
  CoinGecko-demo-key triage path baked in.
- **r1 smoke probe coverage extended from 13 → 22 endpoints with
  behaviour pinning**. New `expect_status` helper asserts arbitrary
  HTTP status (not only 200), so the probe now catches regressions
  where a documented 4xx silently weakens to a 200-with-empty-body
  (the class of bug behind #1134). Net change: 8 new positive
  checks (`/v1/coins/{slug}`, `/v1/issuers`, `/v1/currencies`,
  `/v1/lending/pools`, `/v1/sac-wrappers`, `/v1/network/stats`,
  `/v1/incidents`, `/v1/incidents.atom`, `/robots.txt`) and 2
  negative behaviour pins (`?limit=999999` → 400 invalid-limit,
  `/v1/coins/<garbage>` → 404 coin-not-found). All 22 checks
  green against r1 today.

- **Schema.org BreadcrumbList JSON-LD on 6 more detail surfaces**:
  `/issuers/{g_strkey}`, `/sources/{name}`, `/exchanges/{name}`,
  `/dexes/{source}`, `/lending/{pool}`, `/convert/{from}/{to}`.
  Lets Google render the breadcrumb hierarchy under the title in
  search results (Home → Issuers → Circle, etc.). Same shape as
  the existing JSON-LD on `/assets/{slug}` and `/markets/{pair}`
  per #948 — expands SEO coverage from 3 → 9 detail pages.

### Changed

- **Explorer `/diagnostics` cursor table hides stale (>1h) rows
  by default**. Live audit on r1 today: 30 of ~50
  ingestion-cursor rows had `lag_seconds` over 5 days — completed
  backfill jobs whose progress markers were never cleaned up,
  drowning out the live ingest cursor that operators open the
  page to find. New `Hide stale (>1h)` checkbox (default on)
  filters to actively-progressing cursors; toggle off to see the
  full set when investigating a stuck backfill.

### Added

- **Explorer redirects API paths to api.ratesengine.net**.
  `ratesengine.net/v1/coins`, `/api/v1/coins`, and bare `/api`
  used to land on the explorer's catch-all 404 — opaque dead-end
  for anyone debugging an integration who pasted the path
  without the `api.` subdomain. Three new 301 rules in
  `_redirects` rescue the common patterns (copy-pasted-from-docs
  `/v1/...`, "/api/" prefix habit from other vendors, tools that
  strip subdomains).
- **`/v1/diagnostics/cursors?max_age=<duration>` filter**. Server-
  side companion to the explorer's client-side "Hide stale" toggle
  (#1142). Direct API users can now ask `?max_age=1h` to omit
  completed-backfill cursors that drown out the live ledgerstream
  marker. `max_age` accepts any positive Go-duration string
  (`30m`, `1h`, `5m`, `0.5h`); empty / omitted preserves the
  legacy "return everything" contract. Invalid duration → 400
  `errors/invalid-max-age`. Pinned by 5 sub-tests + 3+3 sub-cases.
- **`X-RateLimit-Reset` response header on every rate-limited
  endpoint**. The middleware already emitted `X-RateLimit-Limit`
  and `X-RateLimit-Remaining` but not `Reset` — clients couldn't
  pace themselves proactively, only learn the bucket had reset by
  hitting it and getting a 429. Header value follows the GitHub /
  Twitter convention: Unix-epoch seconds at which the current
  fixed window ends. Computed from the bucket's window length —
  no extra Redis trip. Pinned by
  `TestRateLimit_EmitsXRateLimitResetHeader`.

- **Cold-path 8-second response ceiling on every aggregation
  endpoint** (#1082, #1099-#1106). Each handler now wraps its
  reader call in `context.WithTimeout(r.Context(), 8*time.Second)`;
  on deadline the response is `503 application/problem+json` with a
  per-endpoint `type=...-timeout` URL pointing at the runbook
  hierarchy. Previously a cold-cache hypertable scan could hold the
  request open until the upstream LB cut it off (no body, no error
  shape), which falsely flagged the smoke timer and surprised
  Healthchecks.io. Endpoints covered: `/v1/pools`, `/v1/markets`,
  `/v1/sources?include=stats`, `/v1/coins`, `/v1/chart`,
  `/v1/history` (+ `/since-inception`), `/v1/oracle/latest`,
  `/v1/oracle/streams`, `/v1/lending/pools`, `/v1/issuers`,
  `/v1/issuers/{g_strkey}`. Steady-state behavior unchanged.
- **`scripts/dev/r1-smoke.sh` per-request budget: 5s → 10s** (#1108).
  Sits one second above the 8s server-side ceiling — a request that
  crosses 10s wall-clock is genuinely hung (504-class), not just
  scanning a cold partition for the first time today. The previous
  5s default false-positived on cold-cache `/v1/markets?limit=5`
  responses (typical 6-8s) and was filling Healthchecks.io with
  spurious failures.
- **`perf(api)`: prewarm `/v1/pools` + `/v1/markets` at 5/25/100/200
  limits** (#1083). The API binary, after Postgres connectivity is
  proven, fires four warm-up requests at server start so the first
  user request after a deploy or pool-size shift doesn't pay the
  cold-cache penalty. No behavior change for ongoing requests.
- **Monitoring**: two `_never_initialized` alerts close the blind
  spot in the existing `_stale` / `_stalled` family (#1110).
  `time() - <missing>` evaluates to `no data`, so a deployment
  whose supply pipeline has never published anything was invisible
  to monitoring — confirmed on r1 by the 2026-05-08 audit (timer
  not installed, `aggregator_refresh_enabled = false`,
  `asset_supply_history` zero rows). Both new alerts use
  `absent_over_time(...[36h]) == 1` with the same cushion as
  `_stale` so fresh installs don't false-positive. Routes to a
  shared runbook covering the two operator paths (systemd timer or
  goroutine flag).
- **`/v1/price` fiat-vs-fiat cross-rate fallback**: when both
  `asset` and `quote` are fiat (e.g. `asset=fiat:EUR&quote=fiat:USD`)
  and the Timescale + Redis VWAP paths both miss, the handler
  synthesises the cross rate from the wired CurrenciesReader's
  USD-base snapshot. Returns the result with
  `flags.triangulated=true` so callers can see the value is
  derived rather than a direct trade. Pre-fix every
  fiat-vs-fiat query 404'd because there are no on-chain
  trades for fiat conversions. Tested by new
  `TestPrice_FiatCrossRate_EURUSD` (asserts EUR → ~1.086 USD)
  and `TestPrice_FiatCrossRate_NotFiatBothSides` (guards
  `native/fiat:USD` from accidentally taking this branch).

### Security

- **Go runtime → 1.25.10**, **golang.org/x/net → v0.53.0**.
  Closes the four govulncheck findings every PR was carrying:
  - GO-2026-4986 — `mail.ParseAddress` (stdlib, used by signup
    handler); fixed in go1.25.10
  - GO-2026-4982, GO-2026-4980 — `template.Template.Execute`
    (stdlib, used by magic-link template + cross-region monitor
    HTTP server); fixed in go1.25.10
  - GO-2026-4918 — `golang.org/x/net@v0.52.0`; fixed in v0.53.0
  Local `govulncheck ./...` clean post-bump. CI's
  `govulncheck + gitleaks` job goes green for every subsequent
  PR; previously every PR today (#1066–#1073) failed it with
  the same four findings.
### Added

- **Explorer**: cross-rates table on `/currencies/{ticker}`
  becomes sortable + filterable when "Show all" is expanded.
  Click a column header to sort by ticker / direct rate /
  inverse rate (with `▲▼` indicator + `aria-sort`); a small
  filter input above the table narrows down to a substring
  match. Featured-only view (default) keeps its terse render.
- **Test infrastructure**: `TestOpenAPIExamplesParseAsCanonicalAssets`
  in `internal/api/v1/openapi_examples_test.go` walks the OpenAPI
  spec and asserts every documented `asset` / `asset_id` /
  `asset_ids` / `base` / `quote` parameter example parses
  successfully via `canonical.ParseAsset`. Catches the
  symbol-vs-canonical drift class at PR-time (no network
  required) so a future PR setting `example: BTC` on
  `/v1/price?asset=` fails CI immediately rather than waiting to
  reach prod and break the Scalar Send button.
- **CI**: `.github/workflows/api-audit.yml` — runs
  `scripts/dev/audit-public-api.sh` against
  `https://api.ratesengine.net` on every push to `main` that
  touches `openapi/**`, `internal/api/**`, or the audit script
  itself, plus on manual workflow_dispatch with an optional
  `api_base_url` input. No schedule; the existing audit script
  is published for cron / Healthchecks.io use.
- **Explorer**: "Download CSV" button on the
  `/currencies/{ticker}` history panel. Builds an RFC 4180 CSV
  from the already-loaded series (no extra fetch) and triggers
  a browser download via a Blob URL. Filename is
  `ratesengine-{TICKER}-USD-{range}.csv`; columns are
  `date, 1_USD_in_TICKER, 1_TICKER_in_USD`.

### Fixed

- **`/v1/oracle/x_last_price`**: same Redis VWAP fallback as
  `/v1/oracle/lastprice` and `/v1/price`. Cross-pair queries
  whose direct trade row is absent (typical when one leg is
  `fiat:USD` synthesised from a stablecoin) now serve the
  cached value instead of 404'ing. New unit test
  `TestOracleXLastPrice_RedisVWAPFallback`.
- **`/v1/oracle/lastprice`**: now consults the same
  TriangulatedPriceLooker fallback as `/v1/price` when prices_1m
  has no row for the requested pair. Pre-fix, SEP-40
  `lastprice(native)` 404'd in steady state because XLM trades
  against USDC (not direct USD), and the aggregator's
  stablecoin-proxy rewrite lives only in the Redis VWAP cache —
  while `/v1/price?asset=native&quote=fiat:USD` returned a value
  via that same cache. Caught by the 2026-05-08 prod audit; new
  unit test `TestOracleLastPrice_RedisVWAPFallback` covers the
  fallback path so the asymmetry can't regress.
- **Docs (OpenAPI)**: every public-tier `/v1/*` endpoint's
  documented default test request now resolves to a live 200 in
  the Scalar docs UI. Previously many examples used short
  symbols like `base=USDC` / `asset=XLM` which the canonical-
  asset validator rejects (handlers want `native` or the full
  `<code>-<G…>` strkey). Reported 2026-05-08 with a
  `/v1/ohlc?base=USDC&quote=USD` 400 screenshot. Touched: the
  shared `components.parameters.{AssetIdPath,AssetQuery,Quote,Base}`
  blocks plus inline params on `/v1/markets`,
  `/v1/oracle/lastprice`, `/v1/oracle/prices`,
  `/v1/oracle/x_last_price`, `/v1/price/batch`,
  `/v1/coins/{slug}`, `/v1/currencies/{ticker}`,
  `/v1/issuers/{g_strkey}`, `/v1/changes/{entity_type}/{id}`.
  SEP-40 oracle endpoints now document the `crypto:<symbol>`
  keying explicitly so the default `crypto:XLM` example works.
### Performance

- **Explorer**: `/currencies` listing first paint shows real
  rows instead of "Loading…". The page is now an async server
  component that fetches `/v1/coins` + `/v1/currencies` at build
  time and embeds the responses as TanStack Query
  `initialData`. Queries are marked immediately stale
  (`initialDataUpdatedAt: 0`) so the live refetch still fires
  on mount and prices keep flashing on the usual 15 s / 60 s
  cadence — but users no longer see an empty table flash before
  the network round-trip lands.

### Added

- **scripts/dev/audit-public-api.sh** — exercises every public
  GET endpoint with the same example values published in the
  OpenAPI spec. Exit code is the failure count; bodies of failed
  responses are printed. Run against prod (default), R1, or
  local. Catches the documentation-vs-implementation drift class
  that produced the 2026-05-08 Scalar regression. Currently
  green at 37/37 against `https://api.ratesengine.net`.
- **Explorer**: FAQPage JSON-LD on `/assets/{slug}` static pages —
  the same Q/A pairs the visible AssetFAQ panel renders are now
  also emitted as `<script type="application/ld+json">`
  alongside the existing BreadcrumbList block, so Google can
  pick them up for rich-snippet rendering on Stellar-asset
  queries. Mirrors the FAQPage block added on currency pages
  earlier in this session; same source-of-truth pattern (visible
  panel + structured data read from the same `assetFaqFor`
  function).

### Fixed

- **Explorer**: `/assets/{slug}` no longer bakes "Asset not found"
  into the static HTML when the build-time `/v1/coins/{slug}`
  fetch fails. The build-time fetch now retries up to 3× with a
  500 ms backoff on network/5xx errors; if every retry still
  fails, the page hands off to a new client-side fallback
  (`AssetClientFallback`) that re-attempts the fetch from the
  user's browser and distinguishes a real 404 from a transient
  build-host connectivity issue. Previously a single CF Pages
  build window with an API blip rendered every asset detail
  page as not-found until the next build landed. Reported
  2026-05-08 — every asset page on production showed the
  not-found state simultaneously.

### Added

- **Explorer**: FAQPage + BreadcrumbList JSON-LD structured data
  on `/currencies/{ticker}` static pages — same FAQ copy that
  renders in the visible panel (now shared via
  `currencies/[ticker]/faq.ts`) is also emitted as
  `<script type="application/ld+json">` in the build-time HTML so
  Google can pick up rich snippets for currency-pair queries.
  No new route; the FAQ stays embedded on the detail page.
- **Explorer**: Range-stats grid on `/currencies/{ticker}` history
  panel — surfaces range high/low (with date), pct from high
  (`days ago`), pct from low, and average absolute daily move %
  computed client-side from the existing history series. No
  extra fetch; updates as the user changes the range selector.
- **Explorer**: Unified `/currencies/{slug}` URL space — Stellar-native
  crypto friendly aliases (`stellar`, `aquarius`, `usd-coin`,
  `euro-coin`, `stronghold-token`, `velo-token`, `yxlm`, `yusdc`,
  plus lowercase ticker aliases) now resolve via Cloudflare Pages
  301s to the canonical `/assets/{ticker}` detail page. Fiat
  friendly slugs already shipped; this completes the unification
  for the Stellar-native subset of #115. Non-Stellar names like
  `bitcoin` / `ethereum` are deferred until the external supply
  source ships (#114).

## [v0.5.0-rc.37] — 2026-05-08

### Added

- **Persistent fx_quotes hypertable** (PR #1041). Daily forex
  rate snapshots now backfill into a TimescaleDB hypertable
  (migration 0028) so the per-currency page can render charts
  beyond the 7-day in-memory window. The forex worker upserts on
  every refresh tick; a one-shot `scripts/ops/fx-history-backfill`
  walks Massive's grouped-daily endpoint to seed up to 10 years
  of history.
- **`/v1/currencies/{ticker}?range=`** (PR #1041) — handler now
  accepts `30d`, `90d`, `1y`, `5y`, `10y`, `all`. Reads from the
  new fx_quotes hypertable and surfaces the series as `history` +
  `history_range`. Default behaviour (no `range` param) is
  unchanged: the in-memory 7d series in `history_7d`.
- **/currencies/[ticker]: range-selectable USD-value chart**
  (PR #1041) replaces the 7d-only sparkline. Chart uses a
  720×200 SVG optimised for hundreds of points.
- **Asset detail: market-cap timeline empty-state** (PR #1041)
  on the Supply tab — placeholder until the supply-history
  hypertable joins up with per-asset USD prices.
- **/exchanges all-CEX markets table** (PR #1042) sorted by 24h
  USD volume across every venue, merged client-side.
- **/exchanges/{venue} candle chart** (PR #1042) — TradingView-
  style lightweight-charts panel with selectable pair, timeframe
  (24h/7d/30d/1y/all) and granularity (1m/15m/1h/4h/1d).
- **/exchanges/{venue} subscription disclaimer** (PR #1042) —
  explicit callout that the curated pair set is by-design, not
  a data bug.
- **/lending pool list + detail: deploy timestamp + initiator**
  (PR #1042) for every Blend pool we know about, sourced from the
  Phase-4 wasm-history audit.

### Performance

- **Background prewarm goroutine** (PR #1042) for the heaviest
  API caches. /v1/sources?include=stats and /v1/markets / /v1/pools
  each scan ~24h of the trades hypertable on cold paths (5–10s);
  the rc.35/rc.36 caches drop them to <1ms but TTL expiry meant
  the first user request after a cache miss still paid the full
  query cost. A 25s-cadence goroutine in cmd/ratesengine-api now
  re-runs the queries just inside the 30/60s TTLs so user
  requests always land on a warm cache.

### Changed

- **/assets/[slug] converter: searchable CurrencyCombobox**
  (PR #1043). Replaced the plain `<select>` with the same
  keyboard-friendly combobox that backs /currencies/[ticker]'s
  converter — typing narrows ~110 entries down inline. Component
  lifted to `@/components/CurrencyCombobox`.
- **/currencies header copy** (PR #1042) updated to credit
  Massive (Polygon.io); points users at the new range-selectable
  chart on /currencies/[ticker].

### Fixed

- **Wider lookback windows for /v1/coins change_1h/24h/7d** (PR
  #1042). Old windows (10 min / 1 h / 4 h around target) often
  missed low-volume pairs; widened to 35 min / 2 h 30 min / 14 h.
  The DISTINCT ON ... ORDER BY bucket DESC selector still picks
  the latest available row inside the window so the anchor stays
  close to the target.
- **/dexes detail link** (PR #1042) now points at `/dexes/{source}`
  instead of `/sources/{source}`; the latter route exists but
  rendered the operator-metadata view, not the per-DEX detail.
- **AssetLabel: case-insensitive C-strkey match** (PR #1042) plus
  a length-16 truncation fallback for any unstructured asset
  string. Stops the long contract IDs that bled through on
  /dexes pool rows when the SAC wrapper map didn't resolve them.
- **View Code button** (PR #1042) drops the literal `</>` text
  next to the Code2 SVG — was rendering both side-by-side
  site-wide.
- **/assets empty-state cells** (PR #1042) now have explanatory
  tooltips on the Dash so users see why a row is missing 7d %,
  market cap, or supply rather than just `—`.

## [v0.5.0-rc.36] — 2026-05-08

### Performance

- **Cache /v1/markets and /v1/pools** — same TTL+single-flight
  pattern as the rc.35 SourcesStatsReader cache. Drops
  /v1/markets from ~6.2s to <1ms post-warmup; /v1/pools from
  ~10.6s to <1ms. With this, the four user-page endpoints that
  blew the <1s budget (sources stats, sources sparkline, markets
  list, pools list) all return instantly from cache.

### Fixed

- **Explorer /convert pages**: scaled back from full N×N
  (~12k pages) to top-20 × all-110 hub-and-spoke (~4,360 pages).
  N×N busted CF Pages' 20,000-file/deploy ceiling so the
  explorer-deploy was failing on rc.34. The hub-and-spoke
  captures >99% of organic forex search volume.

## [v0.5.0-rc.35] — 2026-05-08

### Added

- **`/convert/[from]/[to]` static-prerendered conversion pages.**
  Full N×N matrix (~12k pages: 110 × 109 minus identity pairs).
  Each page renders the live mid-market rate, an interactive
  ConvertPair widget pre-filled with the pair, and "X = Y"
  snippets at common amounts (1 / 10 / 100 / 1000 / 10000) for
  SEO body content. Inverse pair, both currencies' overview pages,
  and source attribution all linked. Each page has its own
  canonical URL + OG card with the live rate baked into the title
  and description. Includes a server-side initial rate so the
  first paint is correct without a client roundtrip; the
  ConvertPair refreshes every 60s after that.

### Changed

- **GH Actions cost: drop arm64 from release.yml + narrow
  release-validate path filter.** Every release.yml run was
  cross-compiling 6 binaries × 2 archs and pushing 6 multi-arch
  container images; arm64 had no consumers (every region is amd64)
  so it was dead-weight compute. release-validate.yml's `cmd/**`
  path filter was firing on every config-wiring PR (~60 runs/day);
  narrowed to only files release.yml actually consumes (workflows,
  Dockerfiles, Makefile, go.mod/sum, cut-release.sh) — the "did
  the binary cross-compile?" question is already answered by
  ci.yml's `go build ./...`. Re-add arm64 when an arm64 host is
  provisioned.

### Added

- **Configurable per-venue `poll_interval` for external connectors.**
  `ExternalVenueConfig` gains a `poll_interval` field (Duration, empty
  defaults to the connector's built-in cadence). Bake
  `[external.coingecko] poll_interval = "120s"` into the archival-
  node Ansible template to silence the minute-cadence "http 429:
  Throttled" loop seen in indexer logs against CoinGecko's free tier.

- **/aggregators page now lists mainnet contract addresses** for
  Soroswap (router + pair factory) and DeFindex (factory + USDC /
  EURC / XLM autocompound vaults). Each row deep-links to
  stellar.expert. Sourced from each project's authoritative
  `public/mainnet.contracts.json` in their public repo,
  verified 2026-05-08.

## [v0.5.0-rc.34] — 2026-05-08

### Added

- **`known_issuers.go` expanded from 14 to 27 entries** (Round 5)
  via a stellar.expert directory sweep over the top observation-
  count uncurated G-strkeys. Adds Lumenswap, Mobius, Allbridge,
  Afreum, Ixinium, Scopuly, Firefly, Zeam.Money, Dogstarcoin,
  XAU CL, sl8.online, UltraCapital (yUSDC).  Issuers now render
  with org names + home domains on the explorer instead of
  truncated G-strkeys.

## [v0.5.0-rc.33] — 2026-05-08

### Added

- **`known_scams.go` expanded to 19 entries** via a wider sweep of
  the top 487 uncurated issuers (>50K observations each) against
  stellar.expert's directory. New entries include "Scam Assets"
  factories, "Serial Minter / Fake Assets", "InterstellarExchange"
  (flagged unsafe), and several generic counterfeiters. Every
  flagged issuer now renders the red SCAM badge on /issuers and a
  full-width red banner on /issuers/[g_strkey].

- **Expanded `known_scams.go` from 1 to 4 entries.** Sweep against
  the top observation-count uncurated issuers via stellar.expert's
  directory yielded three more flagged G-strkeys: a 472-asset
  serial counterfeiter (`GDEUQ2…INDUS`), "Serial Minter / Deceptive
  Assets" (`GBLLDE…FBLCK`), and a deprecated issuer (`GBNLJI…J5AK`).
  All four now render the red SCAM badge on /issuers and the full
  warning banner on /issuers/[g_strkey].

## [v0.5.0-rc.32] — 2026-05-08

### Added

- **Scam-issuer warnings on `/v1/issuers` and the explorer.** New
  curated `internal/api/v1/known_scams.go` map seeded from
  stellar.expert's directory; entries flag G-strkeys tagged
  `malicious` or `unsafe`. `/v1/issuers` and `/v1/issuers/{g}` now
  carry a `scam_reason` field (omitempty) when the issuer is
  flagged. The `/issuers` table renders a red "SCAM" badge next to
  the org name; `/issuers/[g_strkey]` shows a full-width warning
  banner above the header. Bootstrap entry: `GBYBVW…GUARD` (5M
  observations on prod, flagged "SCAM Counterfeiter" by
  stellar.expert).

### Fixed

- **Explorer: render the native XLM SAC as "XLM" on Soroban DEX pool
  rows.** The Aquarius / Soroswap / Phoenix / Comet pools that emit
  `CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA` (native
  XLM's SAC) as base/quote previously rendered as a truncated SAC
  fingerprint because that contract is intentionally absent from the
  operator wrapper map (it isn't a *wrapper* of a classic asset — it
  is the SAC for native XLM itself, which the on-chain usd_volume
  validator rejects mapping to "native"). `AssetLabel` now hardcodes
  the well-known C-strkey to render "XLM / SAC" directly, ahead of
  the wrapper-map lookup. Same display as the resolved-classic SAC
  rows.

## [v0.5.0-rc.31] — 2026-05-08

### Fixed

- **`/v1/chart`: stablecoin-proxy fallback for X/fiat:USD.** The
  chart endpoint previously returned 0 points for any base asset
  paired with `fiat:USD` (e.g. `native/fiat:USD`) because the
  synthetic stablecoin → USD mapping is applied at /v1/coins
  read-time only — `prices_1m` only contains literal classic-quote
  pairs like `native/USDC-GA5Z…`. The handler now retries against
  the operator-declared USD-pegged classics
  (`trades.usd_pegged_classic_assets`) when the literal pair has
  zero points, marking the response `flags.triangulated=true` for
  transparency. The XLM/USD chart on the asset page goes from
  empty to populated as soon as the API binary is redeployed.

### Performance

- **Explorer: lazy-load `lightweight-charts` (~155 KB).** The candle
  chart on `/markets/[pair]` and `/assets/[slug]?tab=chart` is now
  fetched on-demand via `next/dynamic`. First-load JS for those
  routes drops by roughly the same amount; other tabs on the asset
  page (overview / supply / history) no longer pay the bundle tax.
- **Explorer: stable `staleTime` on read-mostly queries.**
  `/v1/sources`, `/v1/issuers`, `/v1/issuers/{g}`, `/v1/markets`,
  `/v1/history`, `/v1/assets/{id}`, and `/v1/changes/...` all gained
  cache windows (60s–5min) so route-revisit nav re-uses recently
  fetched data instead of re-hitting the network. `/v1/markets`
  also got `placeholderData: prev` to keep the table populated
  while pagination/filters fan out.

### Added

- **Explorer: `/lending/[pool]` detail pages.** Every Blend pool
  observed in the auction stream now has its own static-prerendered
  detail route — auction counts, last-seen timestamp, curated
  annotation (Backstop V2, Pool Factory V2 where known), and a
  stellar.expert deep link. Rows on `/lending` are now clickable.
  Per-reserve composition (which assets the pool accepts, current
  supply/borrow APYs) remains pending the Blend pool-storage
  reader (#84).

- **Divergence: Chainlink reference enabled by default on r1.** The
  `[divergence.chainlink]` block is now baked into the
  `archival-node` Ansible template with EUR/USD, GBP/USD, JPY/USD
  AggregatorV3 mainnet feeds. Off-chain HTTP cross-check via
  `eth.llamarpc.com` — does not contribute to VWAP. The divergence
  refresher now reports `reference_count: 2` (coingecko +
  chainlink) at start-up.

## [v0.5.0-rc.30] — 2026-05-08

### Added
- **`known_issuers` curated fallback expanded to 14 entries**
  (#1004). Adds Blend Capital (BLND), Velo Labs, Phoenix,
  Mykobo (USDx/EURx/GBPx — single G-strkey), Apay (BTC + ETH
  wrapped), Libre, and Circle EURC. Sourced by cross-referencing
  the SAC wrapper rounds 2-4 against each issuer's stellar.toml
  ACCOUNTS list. /v1/issuers and the /assets table now surface
  org names for ~14 anchors covering most non-XLM trade volume.
- **`[supply.sac_wrappers]` expanded to 38 entries** (#990,
  #1001, #1002, #1003). The operator-config map now resolves
  every SAC contract on the top Aquarius / Soroswap / Phoenix
  pools to its underlying classic asset. Drives both the
  explorer's pool-row labels (USDC, BLND, etc. instead of
  truncated C-strkeys) and the indexer's `usd_volume` path
  for trades quoted in USDC SAC.

### Operations
- **`scripts/ops/recompute-usd-volume-soroban.sql`** (#1000) —
  one-shot psql script that retroactively prices ~124k historical
  Soroban DEX trades (Aquarius 104k, Phoenix 8k, Soroswap 8k,
  Comet 3k) that landed before the SAC wrapper config was added.
  Operator runs it once to fix the "trades but no volume" gap.

## [v0.5.0-rc.29] — 2026-05-08

### Added
- **Auto-register `classic_assets` + `issuers` from observed
  trades** — the Phase 4 observer migration 0023 planned for
  but never built. `Store.InsertTrade` now upserts a
  `classic_assets` row (and a matching `issuers` row) for both
  classic-asset legs of every trade, with `last_seen_*` and
  `observation_count` bumped on conflict. A process-lifetime
  `sync.Map` dedupes so we hit the DB once per unique asset
  per process. Errors soft-fail so a registry-side problem
  can't sink the trade-insert hot path. Net effect on prod:
  `/v1/issuers` populates with every G-strkey ever seen as an
  issuer of a traded classic asset, and `/v1/coins` stops
  surfacing only the hand-curated subset. Slug stays NULL on
  insert (the existing `COALESCE(slug, code)` lookup makes
  that safe + avoids unique-constraint conflicts when two
  issuers share the same code).

### Changed
- **Classic-asset labels show the issuer's organisation** when
  known. AssetLabel renders `USDC / by Circle` instead of the
  truncated G-strkey when `/v1/issuers` returns a populated
  `org_name`. Powered by a new `useIssuerLookup` hook that pulls
  `/v1/issuers?limit=500` once per session and indexes by
  G-strkey. Falls back to the truncated G-strkey for unknown
  issuers.
- **Comet rows annotated as "Blend backstop"** on `/dexes`. The
  only Comet pool deployed on Stellar mainnet is Blend's
  backstop module (per `docs/operations/wasm-audits/comet.md`),
  so its trades are liquidation-auction artefacts not retail
  price discovery. The new chip-subscript surfaces that context
  inline so visitors don't read the row as a normal AMM venue.

## [v0.5.0-rc.28] — 2026-05-07

### Added
- **Curated known-issuer metadata fallback** on `/v1/issuers` and
  `/v1/issuers/{g_strkey}`. Top issuers (Circle/USDC, Aquarius/AQUA,
  Ultra Capital/yXLM, Stronghold/SHX, MoneyGram, AnchorUSD) now
  render with `home_domain` + `org_name` populated. Until the
  account-observer-to-issuers upsert path lands (see investigation
  task), the production `issuers.home_domain` column stays empty
  for every issuer; the fallback fills the gap at the wire boundary
  for the most-asked-about anchors. DB-populated values still take
  precedence.

### Changed
- **AssetLabel extracted to shared component** at
  `web/explorer/src/components/AssetLabel.tsx`. Was previously
  copy-pasted into 5 view files (markets, dexes, dexes-by-source,
  exchanges, oracles) — diverged subtly across copies (numeric
  XLM, missing crypto: handler, missing SAC). Now everywhere
  resolves SAC contracts via `/v1/sac-wrappers` consistently and
  any future canonical-form addition (`lp:…`) needs one edit not
  five.
- **Currency converter dropdown is now a searchable combobox**
  on `/currencies/[ticker]`. The plain `<select>` over 100+
  currencies was unusable; the new picker filters by typed
  prefix, navigates with arrow keys, and selects with Enter.
  Pure React, no extra dependencies.

### Fixed
- **`/research/architecture`, `/research/discovery`,
  `/research/operations` 404** — only `[slug]` subroutes existed;
  the category index 404'd. Add a small index page at each that
  lists the curated docs for that category, with a back link to
  `/research`.

## [v0.5.0-rc.27] — 2026-05-07

### Fixed
- **status.ratesengine.net stuck on "Status unknown"** — the
  status site fetches `/v1/status` cross-origin from
  `status.ratesengine.net` but only `ratesengine.net` and
  `api.ratesengine.net` were in the API's `allowed_origins`. Add
  `status.`, `docs.`, `dashboard.`, and `www.` subdomains to the
  ansible template so future re-renders preserve the fix.
  Production `/etc/ratesengine.toml` was hand-patched on r1
  immediately and the API was restarted; verified
  `Access-Control-Allow-Origin: https://status.ratesengine.net`
  on responses.

### Added
- **`GET /v1/sac-wrappers`** — read-only endpoint exposing the
  operator-config Stellar-Asset-Contract wrapper map (SAC C-strkey
  → "CODE-ISSUER" classic asset). The explorer's pool-row
  AssetLabel now resolves SAC contracts to readable symbols
  (e.g. `USDC` with `SAC` subtitle) instead of `CAS3J7…OWMA`.
  Soroswap / Phoenix / Aquarius / Comet emit base/quote as the
  SAC contract address in their swap events at the wire — this
  surfaces the underlying classic asset client-side.

## [v0.5.0-rc.26] — 2026-05-07

### Fixed
- **XLM chart 400 on `/assets/XLM/?tab=chart`** — the chart panel
  defaulted `quote=native` for every asset, including the native
  asset itself. `/v1/chart?asset=native&quote=native` rightly
  rejects the identity pair. Detect `assetID === 'native'`,
  default the quote to `fiat:USD`, and hide the XLM picker
  option in that case.
- **`/v1/currencies` still empty after rc.25** — root cause was
  Go's `encoding/json` case-insensitive key matching: Massive's
  grouped-FX rows have BOTH `"T"` (string ticker) AND `"t"`
  (numeric bar timestamp). With only `T string \`json:"T"\``
  declared, the lowercase `t` *also* tried to bind to that field
  and failed every row with "cannot unmarshal number into Go
  struct field .T of type string". rc.25's per-row decode
  isolated the failure — but kept failing all 1208 rows. Add an
  explicit `Tm int64 \`json:"t"\`` field to claim the lowercase
  key. Now parses 120 USD-base pairs cleanly. Confirmed local
  repro returns `eur=0.85272`.
- **CoinGecko + ECB no longer surface as oracles**
  on `/v1/oracle/streams`. Both write into `oracle_updates` for
  divergence-comparison purposes but they're aggregator /
  authority-sanity sources, not oracles. Filter the API
  response by `external.Lookup(source).Class == ClassOracle`.

## [v0.5.0-rc.25] — 2026-05-07

### Fixed
- **`/v1/currencies` empty after rc.24** (#975). The Massive
  grouped-FX decoder failed the entire snapshot when a single
  row arrived with a non-string `T` field (Massive occasionally
  emits numeric / null tickers for half-listed pairs). Decode
  rows individually now; one bad row is skipped and the
  remaining ~1200 install cleanly.

## [v0.5.0-rc.24] — 2026-05-07

### Added
- **`circulating_supply` + `market_cap_usd` on `/v1/currencies`**
  (#973). Joined from a curated quarterly-refreshed CSV at
  `internal/sources/forex/circulation_data.csv` covering ~25
  currencies (>95% of global fx spot volume per BIS 2022).
  Each row cites a central-bank series identifier (FRED:M2SL,
  ECB:BSI.M2, BoJ:Money_Stock_M2, ...) so the operator can
  refresh from primary documents in <5 min. Currencies absent
  from the table emit null on both fields; the frontend renders
  "—". Broader coverage via the World Bank API
  (`FM.LBL.BMNY.CN`, ~250 countries) is a follow-up.
- Same fields on `/v1/currencies/{ticker}` detail.

(rc.23 was cancelled mid-build to bundle #973 into the next
deployable tag; rc.24 supersedes it. Contents of rc.23 below
roll forward verbatim.)

### Carried forward from cancelled rc.23

## [v0.5.0-rc.23 — cancelled] — 2026-05-07

### Added
- **Massive.com forex provider replaces the currency-api jsDelivr
  shim** (#971). `/v1/currencies` now sources rates from
  `api.massive.com` (Polygon-shape REST). Hourly grain instead of
  daily, so `change_1h_pct` / `change_24h_pct` / `change_7d_pct`
  are honest rolling-window percentages. Operator must export
  `MASSIVE_API_KEY` in `/etc/default/ratesengine` for the forex
  worker to populate the cache; without it `/v1/currencies` serves
  the "warming up" empty state.
- **`?include=sparkline7d` on `/v1/coins`** (#970). Attaches
  `price_history_7d` (7 daily samples) per row, batched in a
  single `GetCoinsPriceHistory7dBatch` storage call. Same
  direct-or-XLM-triangulated path as the existing 24h sparkline.

### Changed
- **`/assets` table** drops the From-ATH and First-seen columns;
  the chart column is now 7-day daily, not 24-hour hourly. Brings
  the listing in line with the original spec (#970).

## [v0.5.0-rc.22] — 2026-05-07

### Added
- **/embed/currency/[ticker] iframe widget.** Third widget category
  alongside the existing asset + pair cards: ticker / name header,
  inverse-USD rate as headline, 7d % change badge, 7d sparkline,
  attribution + cross-rate footer. SEO opt-out via robots noindex.
  Pre-renders for every ticker /v1/currencies returns at build;
  falls back to eight majors when upstream is unreachable. /widgets
  page gets a "Currency card" section with EUR / GBP / JPY iframe
  snippets.
- **/auth/callback handler on the explorer.** Magic-link emails
  point to `{DashboardBaseURL}/auth/callback?token=…`; this page
  is the missing landing handler for when DashboardBaseURL is
  ratesengine.net. Reads the token, full-page-redirects to the
  API's /v1/auth/callback so Set-Cookie applies and the 303 lands
  the browser on /account logged in. Closes the magic-link loop on
  the explorer side.

### Fixed
- **Navbar mobile menu.** The IA-restructure (#888) wrapped the
  desktop nav in `hidden md:flex` without a mobile fallback —
  < 768px screens saw only the logo. New hamburger drawer mirrors
  the desktop dropdowns: Currencies link, Blockchain group
  (collapsible), API Docs, About group (collapsible), Sign in /
  Create account at the bottom. Auto-closes on route change.
- **/v1/sources response unwrapping in DexProtocolsTable + OraclesView.**
  Both client components used `Array.isArray(env) ? env : []` against
  `apiGet<SourceRow[]>` — but /v1/sources returns the standard
  `{ data, as_of, flags }` envelope, so the array branch never fired
  and the table rendered empty. Now correctly typed
  `apiGet<{ data: SourceRow[] }>` and unwraps `env.data`. Same fix
  applied to OraclesView's /v1/oracle/streams call.

### Added
- **/v1/coins listing gains opt-in `?include=sparkline`** with
  per-row 24h hourly history. Backed by new
  `Store.GetCoinsPriceHistory24hBatch` — single CTE pass over all
  requested asset_ids (rather than N+1 per-asset queries),
  returning a `map[asset_id][]CoinPricePoint`. Wire shape:
  `Coin.price_history_24h` (already present from /v1/coins/{slug};
  now also populated on the listing when opted-in). /assets table
  renders the result as a tiny inline SVG sparkline column —
  client-side draw, signed colour by direction.
- **/v1/coins/{slug} returns 7-day daily price history + sparkline
  toggle on /assets/[slug].** New `Store.GetCoinPriceHistory7d`
  emits 7 daily USD-price samples (oldest first), reusing the same
  direct-then-XLM-triangulated path as the 24h series. Coin wire
  shape gains optional `price_history_7d`. The asset-detail Price
  panel's sparkline now toggles between 24h and 7d windows; falls
  back to whichever series is populated when one is empty (newly-
  observed assets only have hours of history at first).
- **/v1/currencies returns per-row 7d change% + optional sparkline.**
  Each `CurrencyEntry` now carries `change_7d_pct` (computed
  server-side from the cached history series so every consumer
  agrees on the math). Adding `?include=sparkline` attaches
  `history_7d_rates` (the per-day inverse-USD series) to every
  row — opt-in to keep the default list payload lean. /currencies
  table now has 7d % + 7d chart columns; signed colour follows the
  change direction.
- **Per-source 24h volume sparkline column** on the /dexes
  protocol-overview table and the /exchanges CEX table — fulfils
  the user IA spec ("chart showing volume over time"). Backed by
  a new opt-in `?include=stats,sparkline` flag on /v1/sources
  that joins per-(source, hour) USD-volume buckets via new
  `Store.GetSourceVolumeHistory24h`. Same XLM/USD CTE as the
  rest of the volume-derivation surfaces. Holes are zero-filled
  server-side so the wire array always has 24 entries (oldest →
  newest); frontend renders mini SVG bars sized by max bucket.
- **/assets/[slug] converter goes cross-currency.** AssetConverter
  now offers any currency from the /v1/currencies snapshot as the
  fiat side of the conversion (USD / EUR / GBP / JPY / CHF / CAD /
  AUD / CNY / INR / BRL / MXN by default; "All currencies…" option
  unlocks the full ~200-ticker list). Computes via the asset's
  USD price + the FX leg from the cached forex snapshot. Footer
  shows both the cross-rate and the FX leg explicitly so users can
  see how the conversion was assembled. Same swap-direction button
  as before — direction state controls which side gets the
  currency selector.
- **/v1/currencies/{ticker} returns 7-day historical series + sparkline
  on /currencies/[ticker].** Forex worker now backfills the trailing
  7 daily snapshots from currency-api on first run + once per day,
  cached in-memory alongside the latest snapshot. Per-ticker series
  surfaces in the wire shape as `history_7d: [{date, rate_usd,
  inverse_usd}]`. Frontend renders a 7-day USD-value sparkline + 7d
  change percentage above the converter. Days where the upstream
  has no published file (rare) are silently skipped — the series
  may have ≤ 7 points.
- **/aggregators surfaces the reference-price aggregators we
  cross-check against.** New "Reference price aggregators" table
  below the Soroswap-Router / DeFindex cards, backed by
  /v1/sources?class=aggregator&include=stats. Lists CoinGecko,
  CoinMarketCap, CryptoCompare with their cost (free/paid),
  backfill availability, and role. Footer note explains the
  exclusion-from-VWAP policy (they aggregate the same upstream
  venues we already index — including them would double-count).
- **/assets/[slug] gains a USD ↔ asset converter widget** per the
  user IA spec ("currency converter widget" on the per-asset page).
  Bidirectional input with a swap button — type a USD amount to see
  asset units, or vice versa. Pure client-side maths against the
  live `priceUSD` already on the page; refreshes when the parent
  re-fetches /v1/price. Cross-currency conversion (asset → EUR/JPY/…)
  is a follow-up — needs the forex snapshot threaded into the page.

### Changed
- **Navbar shows session state.** Replaces the static "Sign in /
  Create account" CTAs with a session-aware widget: signed-out
  users still see the CTAs; signed-in users see their email
  in a chip with a dropdown for Account + Sign out. Backed by a
  new `useMe()` React Query hook that polls /v1/account/me with
  `credentials: 'include'` (5-min refetch, single shared cache).
  401 responses surface as null without throwing — the navbar
  treats that as "anonymous" rather than an error state.
- **/account uses magic-link cookie auth, surfaces user/account info.**
  Replaces the API-key-paste flow with cookie-credential fetches
  (`credentials: 'include'`). Anonymous visitors see a "sign in"
  prompt linking to /signin instead of an API-key input. Authenticated
  view shows user email + account name + tier + sign-out button +
  the existing key-list/mint flow. /v1/account/me extended to return
  `{user, account}` nested objects when called via the magic-link
  session — the API-key fields stay populated for bearer-token
  callers, so both flows coexist on the same wire shape.
- **/signin and /signup now use magic-link auth, not API keys.**
  Replaces /signup's "POST /v1/signup → here is your plaintext key"
  flow with a magic-link form posting to /v1/auth/login (which
  already existed via the dashboardauth bundle). The /signin
  placeholder shipped in #888 also gets the real form. Both pages
  share the same `SignInForm` component with a `mode` flag for
  copy variation. The email link goes to whatever the operator
  configured as DashboardBaseURL — the existing dashboardauth
  /v1/auth/callback handler verifies the token, sets the session
  cookie, and redirects. New emails create the account on first
  callback (no separate signup step). Stale `SignupForm.tsx`
  removed.
- **/assets adds a network-filter chip row + suppresses market cap
  on low-volume rows.** Per the user spec: "we need a filter at the
  top to choose the network ... we probably just wont show a market
  cap for low volume assets because we wont have the data confidence
  in doing so." Network is currently `all` / `stellar` (Stellar is
  the only ingested network today; the chip writes `?network=` for
  forward-compat). Market cap is hidden as `—` whenever the row's
  24h USD volume is < $1,000 — below that the price feed underlying
  the cap is too thin for the cap to be a confident number.

### Added
- **`GET /v1/currencies/{ticker}` + /currencies/[ticker] detail page.**
  Returns the requested currency's USD-base rate, inverse rate, and
  full cross-rates map (1 unit of ticker → every other supported
  currency, derived from the cached USD-base snapshot). New per-
  currency page surfaces this with: a converter widget (input
  amount + target dropdown, derived live), and a cross-rates table
  showing the most common targets up front with a "show all" expander.
  Statically pre-rendered for every ticker the upstream covers
  (build-time fetch, falls back to the majors list if upstream is
  unavailable). 404 with problem+json shape when the ticker isn't
  in the snapshot; 503 while the cache warms up.
- **`GET /v1/currencies` + /currencies real table.** Replaces the
  forex placeholder shipped in #888 with live fiat coverage. New
  `internal/sources/forex` package wraps the free, MIT-licensed
  currency-api (ECB / FRBNY-aggregated, daily-updated, 200+
  currencies, no API key, hosted on jsDelivr). The API binary
  starts a background worker that refreshes the in-memory snapshot
  hourly; `GET /v1/currencies` reads from the snapshot and returns
  ticker / name / USD-denominated rate per currency, with the
  upstream's published-at date so clients can render staleness.
  Frontend table is sortable + searchable; per-currency drill-down
  with 1h / 24h / 7d change windows + market cap + volume + supply
  lands once we wire a paid forex feed (currency-api is daily-
  granularity only).
- **`GET /v1/lending/pools`** — returns one row per Blend pool
  observed in the auction stream, with 24h / all-time auction
  counts + 30d unique users + last-seen timestamp. Backed by new
  `Store.ListBlendPools`. Per-pool TVL / utilisation / APYs land
  via additional fields when the pool-storage reader worker ships;
  the wire shape is designed to grow rather than version-bump.
- **/lending pools table** — surfaces the new endpoint at the
  bottom of /lending, below the existing Blend narrative card,
  per the user IA spec ("1 table showing all the lending pools,
  the protocol — all will be blend for now"). Each pool address
  links out to stellar.expert for the contract page.
- **/exchanges page** (real, replacing the placeholder shell): per-CEX
  table sorted by 24h USD volume desc, with trade count, pair count,
  and a share-of-CEX-volume bar. Backed by /v1/sources?include=stats
  filtered to Subclass=CEX. Per-exchange detail pages at
  /exchanges/{binance,coinbase,kraken,bitstamp} with 24h activity
  card + paginated pair table backed by /v1/markets?source=<name>.
  Statically pre-rendered for the 4 connected CEXes.
- **`GET /v1/oracle/streams`** — returns one row per
  `(source, asset, quote)` triple, the latest observation in the
  trailing 7d window. New `Store.LatestOracleStreams` underneath
  uses `DISTINCT ON (source, asset, quote) … ORDER BY ts DESC` for
  the per-stream latest. Backs the new "price streams" table on
  the explorer's /oracles page (the second table per the user IA
  spec — "1 at the bottom showing all price streams from all
  oracles").

### Changed
- **/oracles rebuilt as two live tables.** Replaces the curated
  Oracle-card grid with: (1) per-oracle activity table backed by
  `/v1/sources?class=oracle&include=stats` (24h updates + active
  stream count + last update + VWAP-inclusion policy) and (2) the
  full price-streams table backed by /v1/oracle/streams. Keeps the
  SEP-40 compatibility panel as a footer note. Curated narrative
  notes per oracle moved to /sources/<name> and the integration
  audits under /research/discovery.
- **/dexes adds the DEX-protocols overview table** above the
  all-pools table — per the user spec ("2 tables, at the top
  lists all our connected dexes with basic overview info about
  them"). Per-row: protocol name, 24h USD volume, 24h trade count,
  active pool count (markets_count_24h), and a details link to the
  per-protocol /sources/<name> drilldown. Backed by
  /v1/sources?include=stats filtered to Subclass=DEX and sorted by
  volume desc. Updates the page header to clarify CEX pairs live
  at /exchanges (not /markets).
- **Top nav restructured to grouped IA.** Navbar collapses from a
  flat 11-item bar to: Currencies / Blockchain (dropdown) /
  API Docs / About (dropdown) / Sign in / Create account. Blockchain
  contains Assets, Exchanges, Dexes, Lending, Aggregators, Oracles,
  Networks. About contains Pricing, Blog, API status (external),
  Company, Careers, Contact. Status pill stays as a compact dot
  beside the search/theme controls. The route formerly at /network
  is now /networks (singular → plural to match the dropdown label
  and reflect that the page is per-network even though only Stellar
  is wired today).

### Added
- **New route shells.** /currencies, /exchanges, /pricing, /blog,
  /company, /careers, /signin land as honest placeholders explaining
  what's in flight rather than mock data — the live build wires the
  full table once the underlying ingest / agg / page work merges
  (forex feed for /currencies, per-CEX aggregations for /exchanges,
  magic-link auth for /signin).
- **/v1/pools `?source=<name>` filter.** Restricts the result to
  one DEX's pools. Non-DEX names (binance, coinbase, …) return an
  empty list rather than 400 — callers can pass through user input
  without separately validating against the registry. Backs the
  /dexes venue-chip row, which now triggers a server-side re-fetch
  per chip rather than client-side filtering the current page (the
  prior behaviour broke for users who wanted to see Soroban-only
  pools, since page 1 by USD-volume-desc is dominated by SDEX).

### Fixed
- **/v1/sources surfaces 24h USD volume on Soroban DEX sources** —
  same root cause as the /v1/pools fix below: SUM(usd_volume) on
  Phoenix/Aquarius/Comet trades was NULL because their trades had
  null usd_volume. GetSourceStats now applies the same XLM/USD
  CTE so per-protocol totals on /v1/sources?include=stats are
  populated. Backs the new "DEX protocols" overview table at the
  top of /dexes.
- **/v1/pools surfaces 24h USD volume on Soroban DEX pools** —
  Phoenix / Aquarius / Comet trades against the XLM SAC wrapper
  (CAS3J7GY…) had NULL `usd_volume` because the operator's USD-pegged
  Phase 1 allow-list doesn't include XLM itself. The vol_24h CTE
  now derives USD volume per (source, base, quote) directly from
  trades: trades with non-null `usd_volume` use it as-is; trades
  with native or XLM SAC on either side use base_amount/quote_amount
  × XLM/USD (read from the same on-chain XLM/USDC vwap that powers
  /v1/coins). Pure SEP-41/SEP-41 token swaps still emit null until a
  per-token oracle wires in. Side benefit: per-source attribution —
  two DEXes trading the same canonical pair now get separate vol
  numbers rather than the cross-source sum. Same `Pool.Volume24hUSD`
  wire field; previously-empty values now populate.
- **/v1/pools is DEX-only, never CEX rows.** "Pool" is AMM/DEX
  terminology — applying it to CEX trading pairs (binance,
  coinbase, kraken, bitstamp) misnames the data. Handler now
  resolves the DEX subset of the source registry
  (Class=Exchange + Subclass=DEX → soroswap, phoenix, aquarius,
  sdex, comet) and constrains the trades scan with
  `t.source = ANY($N)`. CEX trading pairs are at /v1/markets,
  which has always been the cross-venue collapsed view. Frontend
  copy on /dexes updated to "DEX pools" with a link to /markets
  for CEX pairs.

### Changed
- **/dexes is now the all-pools table** — same shape as /assets,
  one row per (venue, base, quote) tuple. Replaces the 5
  per-DEX summary cards. Sortable by 24h volume desc / source-pair
  alphabetical. Cursor-paginated 100 pools per page. Source-filter
  chip row at the top scopes the table to one venue. Each row
  deep-links to /markets/<base~quote> for the standard pair detail.
  Backend: new `/v1/pools` endpoint backed by `Store.AllPools` —
  one row per (source, base, quote) tuple, distinct from
  `/v1/markets` which collapses across sources.

### Added
- **/dexes/<source>: full pool table per DEX.** Click any DEX
  card on `/dexes` to drill into a paginated table of every
  (base, quote) pool the source observed in the last 14 days,
  with per-pool 24h volume, 24h trade count, and last-trade
  relative timestamp. Sortable by 24h volume desc (default) or
  pair alphabetical. Each row deep-links to /markets/<pair>
  for the standard chart + OHLC + trade history view.
  Backend: extended `MarketsReader` with a `SourceMarkets`
  method that filters trades by source before grouping; new
  query parameter `/v1/markets?source=<name>`. Cache-keyed
  separately from the global markets list.
- **/dexes shows real per-DEX volume + trades + pool count.**
  Was: 5 cards of static prose. Now: each card has live 24h
  USD volume, trade count, and pool count (unique base/quote
  pairs the source observed in 24h) from
  `/v1/sources?include=stats`. Backend extension: GetSourceStats
  now returns SUM(usd_volume) + COUNT(DISTINCT (base, quote))
  alongside the existing trade-count column. Page header shows
  rolled-up totals across all five venues.

### Removed
- **/compare page** dropped from explorer. Redundant with
  /assets + per-asset detail, and rarely worked cleanly. Removed
  from navbar, footer search, and sitemap. The route directory
  is gone.

### Performance
- **`/v1/markets` cold-cache p99: 30s → 3.7s.** Reported by user
  ("markets doesn't load in any reasonable time"). Root cause was
  a correlated `(SELECT vol_usd FROM vol_24h v WHERE v.base_asset
  = t.base_asset AND v.quote_asset = t.quote_asset)` subquery
  evaluated up to 4× per output row (SELECT + 2× HAVING + ORDER
  BY) in `buildDistinctPairsQuery`. Refactored to a single LEFT
  JOIN against the `vol_24h` CTE; the planner now resolves
  volume once per (base, quote) tuple. 8× cold-cache improvement;
  warm cache unchanged at ~100ms. Deployed on r1 as
  v0.5.0-rc.22-perf via the manual scp path (GH Actions still
  billing-blocked).

### Fixed
- **Top markets + Markets table: defensive null-asset handling.**
  Audit-and-harden pass after the home Recent-trades crash
  (#879). Same `.startsWith()` pattern in `HomeTopMarkets`
  and `markets/MarketsTable` would have crashed on the same
  rare /v1/markets row with one side null. Both renderers now
  return "—" for null/undefined input. Embed and pair-detail
  call sites take their input from a URL split on `~` so
  both sides are always defined; no change needed there.
- **Home Recent trades: crash on null base/quote asset.**
  HomeRecentTrades's `short()` helper called `.startsWith()` on
  the canonical asset string, but rare /v1/history rows arrive
  with one side null. The crash bubbled up the React tree and
  blanked the home page. Now `short()` returns "—" for null
  inputs; the row still renders the price + timestamp + source,
  and the pair label displays without a link (since
  /markets/native~undefined would 404).

### Changed
- **Navbar surfaces SDK link.** Adds `SDK` between Research and
  Docs in the navbar so Go integrators can find the typed
  `pkg/client` examples without having to drill into the footer
  or Cmd-K search first.
- **Home hero: "Get a free key" CTA.** Adds a fifth pill linking
  to `/signup` next to Browse assets / Browse markets / API docs
  / Read methodology. The conversion path was previously hidden
  in the navbar's Sign-up button only; surfacing it as a hero
  CTA matches what every other enterprise data API does on its
  landing page.
- **Home live-panels: explicit "Open" CTAs.** NetworkLivePanel
  and SystemHealthLivePanel on the home 3-up grid now end with
  small "Open network →" / "Open diagnostics →" links matching
  the Diagnostics teaser's pattern. Wrapping the whole Panel in
  a Link would conflict with the source-reveal button, so the
  CTA sits at the bottom of the panel content instead.
- **docs.ratesengine.net topbar: 3 new links.** Adds Methodology,
  Go SDK, and Changelog to the docs site's topbar between Explorer
  and Status. Visitors landing on the API reference can now jump
  to the explainer, the typed SDK page, or the release feed
  without having to bounce back to the explorer first.
- **HomeTryAPI: nudge to /sdk when Go tab is selected.** The Go
  example renders stdlib `http.Get` (matches the curl/JS/Python
  shape — same one-liner). When the visitor picks the Go tab,
  the footnote now adds "For idiomatic Go using the official SDK,
  see /sdk" so they can switch to the typed client.

### Added
- **`/sdk` showcase page.** Surfaces the official Go SDK at
  `pkg/client`. Install command, quick-start example, five
  paste-ready common patterns (batch lookup, history, SSE
  stream, OHLC bar, error handling), authentication modes
  (anonymous / API key / SEP-10), and links to godoc + GitHub
  source + REST reference. Reuses the `CopyableSnippet`
  component from `/widgets` for the code blocks. Linked from
  footer + Cmd-K search + sitemap.
- **`/diagnostics` BackfillSummary card.** Surfaces backfill
  worker state (active workers / slowest active lag / furthest
  ledger reached / distinct shards) as a sibling card to the
  existing live-ingest HealthSummary. Same `/v1/diagnostics/cursors`
  call powers both — no extra round trip. Page now reads
  "Live ingest" + "Backfill workers" as two clearly-labeled
  health surfaces rather than mixing them.
- **Home Try-the-API: rc.21 endpoints surfaced.** Adds two
  example tabs covering features shipped in rc.21 — `Network
  stats — 24h volume + market count` (`/v1/network/stats`)
  and `Sources with 24h trade counts`
  (`/v1/sources?include=stats`). Ten canonical examples now,
  up from seven.
- **Home "Recently shipped" widget: Subscribe (Atom) ↗ link.**
  Surfaces `/changelog.atom` directly from the home widget so
  visitors can subscribe to release feeds without first
  scrolling to the dedicated changelog page.

### Changed
- **Home network strip cells are now clickable.** Each of the
  five cards on the home strip deep-links to its corresponding
  page: 24h volume + Active markets → `/markets`, Assets indexed
  → `/assets`, Sources online → `/sources`, XLM → `/assets/XLM`.
  Hover state matches the rest of the explorer's link chrome
  (border + shadow lift on hover). Visitors can drill from the
  scale-of-the-network number straight into the underlying
  catalogue.

### Added
- **`/research/operations` runbook browser.** Curated set of four
  cross-cutting operator docs — archival-node-bringup,
  release-process, deploy-workflow, sev-playbook — rendered as
  static pages on `/research/operations/<slug>`. Per-alert on-call
  runbooks (60+ files in `docs/operations/runbooks/`) stay
  GitHub-only; these four are the canonical "stand up your own
  copy" + incident-response procedures any auditor or prospective
  operator would want to read. Removes the GitHub-link-only
  catch-all "Browse by topic" section since every topic now has a
  curated on-site browser.

### Changed
- **/network + /divergences: ADR mentions deep-link.** Plain-text
  ADR-0004 / ADR-0008 / ADR-0015 callouts on /network and the
  ADR-0019 mention on /divergences now jump straight to the
  rendered ADR pages instead of being inert text.

### Performance
- **status site: tier probe cadence.** The status page used to
  hammer every public endpoint every 30 s — including expensive
  catalogue/history queries that drive the API's SLO burn rate.
  Endpoints now carry a `tier`: `hot` (30 s — healthz, readyz,
  price, price/batch, price/tip, sources, network/stats) keep
  the original cadence; `warm` (2 min — coins, markets, issuers,
  history, observations, oracle/lastprice, vwap/twap/ohlc/chart)
  drop their poll rate by 4×. Should clear the recurring
  `slo_latency_burn_medium` page-level alert without sacrificing
  outage-detection latency on the cheap probes that actually
  need it.

### Added
- **`/changelog.atom` syndication feed.** RFC-4287 Atom feed of
  every release entry on the explorer side, generated at build
  time from `CHANGELOG.md`. Designed for Feedly, Slack RSS bot,
  and any other feed reader that wants push-style notifications
  when a release ships — no polling. The /changelog page header
  now surfaces a "Subscribe (Atom) ↗" link. Same pattern the
  status site uses for `/v1/incidents.atom`.
- **`/sources/<name>`: integration audit link.** When the source
  has a corresponding `/research/discovery/<slug>` audit, the
  detail header now shows a "Read integration audit →" CTA.
  Reflector's three contracts (cex/dex/fx) collapse to a single
  audit page since they share the on-chain interface. CEX/aggregator
  sources without published audits (binance, coinbase, kraken,
  etc.) render no link.

### Changed
- **/anomalies, /divergences, /lending: deep-link to specific
  research pages.** ADR-0019 mentions on /anomalies + /divergences
  now link directly to `/research/adr/0019` instead of the generic
  `/research` index. /lending's "Discovery notes (Blend)" + "(Comet
  backstop)" CTAs now jump to `/research/discovery/blend` and
  `/research/discovery/comet`.

### Fixed
- **NetworkLivePanel: assets-indexed count capped at 500.** The
  side panel on home was reading `useCoins(500).coins.length`
  for the asset count — silently capped at the page limit. Same
  bug as #854 fixed for the network strip; this is the same fix
  for the side panel. Switches to `/v1/network/stats.assets_indexed`
  (real count, ~85,750). Latest-ledger field also reads from
  `network/stats` with cursor-table fallback.

### Added
- **`/contact` page.** Single destination for the previously-orphaned
  "Contact sales" callouts on `/signup`. Five channel cards covering
  security disclosures (security@), sales (sales@), GitHub issues,
  status feed subscription, and architecture/methodology research
  links. Plus a four-question FAQ. Pro/Business/Enterprise tier
  cells on `/signup` now deep-link here. Linked from footer + Cmd-K
  search + sitemap.

### Changed
- **`/markets/<pair>`: full CandleChart with timeframe + granularity
  controls.** Replaces the static 24h sparkline with the same chart
  surface `/assets/<slug>` ships — 24h / 7d / 30d / 1y timeframes,
  1m / 15m / 1h / 4h / 1d granularities. Pair-specific (no quote
  toggle since the URL already pins the pair). 24h change % and
  last-hour USD volume keep the original build-time fetch so
  metadata + headline numbers stay server-rendered.

### Added
- **`/widgets` showcase page.** Public docs + live preview for the
  embeddable iframe widgets (`/embed/asset/<slug>`,
  `/embed/pair/<base~quote>`). Three asset cards (XLM, USDC, AQUA)
  + two pair cards (XLM/USDC, XLM/USD) render live with
  paste-ready iframe HTML next to each. Linked from footer +
  Cmd-K search. The widgets themselves were always there but had
  no surface explaining how to use them; this closes the loop.

### Changed
- **/dexes + /oracles: deep-link directly to per-protocol audits.**
  Each card's "Read integration audit" CTA now jumps straight to
  `/research/discovery/<slug>` (Soroswap → soroswap, Phoenix →
  phoenix, etc.) instead of dumping the visitor on the generic
  `/research` index. Visual change: external-link icon dropped
  for the internal arrow style.

### Added
- **`/research/discovery` integration audit browser.** Curated
  set of ten per-DEX/per-oracle Phase-1 audits — sdex, soroswap,
  phoenix, aquarius, comet, blend, reflector, band, redstone,
  chainlink — rendered as static pages on
  `/research/discovery/<slug>`. Each audit names the contract
  repo + commit checked, the upstream-source quirks we found,
  and how the decoder handles them. Allow-listed via a `CURATED`
  array; the rest of `docs/discovery/` stays private.
- **SearchModal: missing pages.** Cmd-K search now lists the new
  `/methodology`, `/research`, `/changelog`, `/compare`,
  `/signup`, and external `status.ratesengine.net` alongside the
  existing pages.
- **`/markets` table: sortable Base + 24h volume columns.** Click
  the Base header to flip to alphabetical-by-pair (the API's
  `pair` order_by); click 24h volume to flip back to volume-desc.
  Active sort is mirrored in the URL as `?order=...` for
  bookmark + back-button parity with `/assets`.
- **Live navbar status pill.** Replaces the hard-coded green dot
  next to the navbar's Status link with a real-time poll of
  `/v1/status.overall`. Green pulses when ok, amber on degraded,
  red on down, slate when the fetch fails. Tooltip surfaces the
  current state in plain English. Polls every 60 s with 30 s
  shared cache so navigating between pages doesn't burst the
  API.
- **Source detail page: 24h trade count.** `/sources/<name>`
  now shows that venue's 24h trade contribution baked at build
  time alongside the rest of the registry profile (e.g. binance
  → 3.56M, coinbase → 1.81M, sdex → 1.56M). Same `?include=stats`
  opt-in the listing already uses (#852).
- **Home hero: "Read methodology" CTA.** Adds a fourth pill
  alongside Browse assets / Browse markets / API docs that links
  to `/methodology`. Footer System column gains the same link.

### Fixed
- **Home network strip: undercounted 24h volume + market + asset
  totals.** Previously the strip summed `useMarkets(500, ...)` and
  counted `useCoins(50)` client-side, capping the displayed
  numbers at the first page of each list. Now consumes
  `/v1/network/stats` (rc.21) directly — server-aggregated across
  the full corpus. Real numbers visible on the home page: 24h
  volume jumps from a partial sum to the actual ~$5.8B aggregate,
  and "Active markets" jumps from 500 to ~23,400.

## [v0.5.0-rc.21] — 2026-05-07

### Added
- **`/sources` table: 24h trade count column.** Wires the
  `?include=stats` opt-in (shipped in #845) into the explorer's
  source-registry view. Each class group is now sorted by 24h
  trade count desc — most-active venues at the top, alphabetical
  fallback for venues that haven't traded in the last 24h.
  Renders `—` for any source the API hasn't populated yet,
  including `0` (which means "stats requested, no trades
  observed" per #845's design).
- **`/research/architecture` doc browser.** Curated set of seven
  long-form architecture narratives — ingest pipeline, aggregation
  plan, supply pipeline, contract schema evolution, oracle
  manipulation defense, HA plan, SemVer policy — rendered as
  static pages on `/research/architecture/<slug>` from
  `docs/architecture/*.md`. Allow-listed via a `CURATED` array
  in the loader so the launch-readiness backlog and other
  internal-only docs stay private. Each card on `/research`
  shows the title, one-line description, and last-verified
  date; the detail page links the GitHub source.
- **`/methodology` page — how rates are computed.** New
  enterprise-grade explainer covering source classes (what
  contributes to VWAP and what doesn't), VWAP weighting policy,
  stablecoin → fiat proxy at the aggregator layer (not at
  ingest, so depegs stay visible), freeze policy, the
  closed-bucket-only API contract that gives cross-region
  consistency, latency targets, and the i128/string-on-the-wire
  precision invariant. Each section cross-links to the
  underlying ADR for the full rationale. Linked from the
  navbar.
- **status site: per-incident postmortem pages.** Every incident
  in `internal/incidents/data/*.md` now renders as its own page
  on `status.ratesengine.net/incident/<slug>`, generated from the
  same markdown corpus the `/v1/incidents` API serves. The
  Incident history section on the status home links each title
  to its full postmortem; the page surfaces severity / status /
  affected components, a Started / Resolved / Duration timeline,
  and a GitHub source link. Static-export pre-rendered — no
  runtime fetch.
- **`/research` ADR browser.** Every architecture decision record
  (currently 23) renders as a dedicated, shareable page on
  `/research/adr/<id>`, generated from `docs/adr/*.md` at build
  time — no client-side fetch, full SEO. The `/research` index
  groups ADRs by status (Accepted / Proposed / Superseded /
  Rejected), sorts newest first within each group, and links the
  source markdown on GitHub from each detail page. Adds a small
  `lib/markdown.tsx` block renderer (h1–h4, paragraphs, lists,
  fenced code, blockquotes) so we don't pull a 30 kB markdown
  parser into the static bundle for our authored doc shapes.
- **`/assets` table: sortable Volume 24h column.** Click the
  Volume 24h header to flip the listing's `order_by` between
  `observation_count_desc` (default) and `volume_24h_usd_desc`.
  The active sort is mirrored in the URL as `?order=...` so
  bookmarks + back-button navigation work as expected; cursor
  resets on sort change so pagination stays consistent. Backend
  parameter has been live since rc.14; this just wires it into
  the table header.
- **`/v1/sources?include=stats` per-source 24h trade count.**
  Opt-in flag joins each Source row with a `trade_count_24h`
  column derived from a single GROUP BY on the trades hypertable.
  Cheap aggregation (the `(ts, source)` ingest pattern keeps the
  index hot); soft-fails to the all-static-registry projection
  if the DB hit errors. Lets the explorer's `/sources` page
  surface contribution percentages without separate fetches.
- **Home "Recently shipped" widget.** New section between Recent
  trades and Try the API surfacing the top 3 changelog entries
  with proper Added/Fixed/Changed tone pills + release pill +
  bold/code/link rendering. Reads CHANGELOG.md at build time;
  links out to /changelog for the full history.
- **`/v1/incidents.atom` Atom feed.** RFC-4287 syndication of
  the customer-facing incident corpus — designed for Feedly,
  Slack RSS bot, and other feed consumers who want push-style
  notifications when an incident ships without polling JSON.
  Status page now surfaces a "Subscribe (Atom) ↗" link in the
  Incident history section header. Cache-Control max-age=300 (5
  min) — corpus only changes on redeploy.
- **`/compare` page** for side-by-side asset comparison (2&ndash;6
  assets via `?assets=USDC,XLM,USDT`). Renders a metric × asset
  table covering price, 1h/24h/7d change with green/red tones,
  24h volume, markets count, observations, and a per-asset
  sparkline. Each cell pulls `/v1/coins/{slug}` via React Query
  so the comparison stays current. Compare link added to the
  primary nav with USDC/XLM/USDT/AQUA pre-loaded.
- **`/v1/network/stats` consolidated aggregate endpoint.** Single
  call returning trailing-24h USD volume, distinct markets count,
  total classic-assets row count, latest live ledger, plus the
  exchange-class + total source counts. Single SQL query over
  `prices_1m` + `classic_assets` + `ingestion_cursors`; source
  counts come from the in-memory `external.Registry`. Replaces
  the home network-strip's previous fan-out across four separate
  endpoint calls. Useful for embed widgets / dashboards that just
  need a snapshot.
- **Docs site polish: header bar + favicon + OG card.**
  docs.ratesengine.net now has a slim header above the Scalar
  reference with brand mark + "Explorer" / "Status" / "GitHub"
  navigation links so visitors can hop between the three sites
  without typing URLs. Adds favicon (`/icon.svg`) and 1200×630
  OG image (`/og.svg`) so shared docs links render as proper
  preview cards. Both files served from the same CF Pages
  project; refreshed when `make docs-api` rebuilds the
  index.html.
- **`/embed/pair/{base~quote}` iframe pair widget.** Mirror of
  the asset embed shipped earlier — same chrome-less layout,
  shows the BASE / QUOTE label + live VWAP + 24h change pill +
  sparkline + "Powered by Rates Engine" attribution. Pre-rendered
  for the top 100 pairs by 24h USD volume.
- **Home Try-the-API: language tabs (curl / JS / Python / Go).**
  Each example renders as a snippet in the chosen language; the
  ▶ Run-it button still fires the same URL inline regardless of
  language. Closes the loop for someone evaluating which SDK
  shape feels right without leaving the page.
- **`/embed/asset/{slug}` iframe-friendly price widget.**
  Chrome-less route (no navbar, no footer, no max-width) designed
  to be dropped into a customer site at any width. Renders the
  asset's code, USD price, 24h % change pill, sparkline, and 24h
  USD volume — plus a "Powered by Rates Engine" attribution +
  link back. Pre-rendered for every slug returned by `/v1/coins`.
  Recommended embed:
  ```html
  <iframe src="https://ratesengine.net/embed/asset/USDC"
          width="320" height="160"
          frameborder="0" sandbox="allow-scripts"></iframe>
  ```
- **Theme toggle in the navbar** (light / dark / system, cycling
  via a single icon button). Choice persists in localStorage
  under `re.theme`. Inline init script in `<head>` applies the
  class before first paint so there's no flash of wrong theme on
  load. Default is still OS preference (`prefers-color-scheme`)
  when no choice is stored — matches what shipped before.
- **`/changelog` page on the explorer.** Renders this file at
  build time — every release block surfaces with proper markdown
  (bold, code, links), grouped by Added / Fixed / Changed with
  matching tone colours. Each version pill links out to the
  GitHub release page. Listed in the footer under System.

## [v0.5.0-rc.20] — 2026-05-07

### Fixed
- **`/v1/coins/{slug}` 500 regression on rc.18.** PR #794 added
  `change_1h_pct`, `change_24h_pct`, `change_7d_pct` references
  to `getCoinBySlugSQL` but missed adding the corresponding
  `xlm_usd_1h` / `xlm_usd_24h` / `xlm_usd_7d` CTE definitions
  (they were added to `listCoinsBaseSelect` correctly).
  Postgres rejected every non-native slug lookup with
  `relation "xlm_usd_1h" does not exist (42P01)`. Caught by
  watching r1 API logs — explorer build was hammering the API
  with ~150 errors/min on slugs like ARS, PEPE, GAZPROM, KOGAS.

### Added
- **Open Graph + Twitter cards for explorer + status sites.**
  Both subsites now ship a 1200×630 SVG OG image plus full
  `openGraph.images` + `twitter.images` metadata so links
  shared in Slack / Twitter / LinkedIn render as proper preview
  cards instead of bare URL chips. Explorer card has the
  network-line motif + "Pricing for every asset on Stellar";
  status card has the live-pulse dot + "System status".
- **Home Try-the-API panel: Run-it live + 7 examples.** The
  panel now ships with 7 canonical curls (price, coin detail,
  coins listing, top markets, history, cursors, incidents) and
  a ▶ button next to the Copy button — click to fetch the same
  URL inline and render the JSON response (4 KB cap, syntax-
  pretty when JSON, raw otherwise). Closes the loop between
  "what should I try?" → "what does it actually return?" without
  the visitor leaving the page.
- **`/network` page rebuilt around live data.** Drops the
  "Coming next" placeholder and renders the same network stats
  strip as the home page (24h volume, active markets, asset
  count, sources online, XLM price), plus the live network panel
  + Top markets + Top assets tables. Architecture context now
  describes what's currently observable on R1 instead of what's
  conceptually planned for R2/R3. Footnote section honestly
  enumerates what's still TBD (TVL, peg health, fee market) so
  the page can grow without surprising the reader.
- **`/sources/{name}` per-venue detail page on the explorer.**
  Static-export route enumerating every registered source from
  `/v1/sources`. Renders the source's registry profile (class /
  subclass / contributes_to_vwap / default_weight / paid /
  backfill_safe) plus per-(source, sub_source) ingest cursors
  pulled from `/v1/diagnostics/cursors` with green/amber/red lag
  pills. Sources table rows on `/sources` are now clickable Links
  into the new detail page.

## [v0.5.0-rc.19] — 2026-05-07

### Added
- **`/v1/incidents` API + status-page consumer.** Customer-facing
  incident posts moved from `docs/operations/incidents/` to
  `internal/incidents/data/` so the API binary can `go:embed`
  them and serve a parsed JSON corpus at `GET /v1/incidents`.
  YAML-frontmatter + markdown body; sorted `started_at` desc.
  status.ratesengine.net's "Incident history" panel now fetches
  this endpoint instead of reading a hardcoded array bundled
  with the page. New incident posts ship with the next API
  redeploy — no status-page rebuild required.
- **Home page: Recent trades live feed.** Bottom of the home
  page — rolling 30-row table merging the latest trades across
  the top 3 pairs by 24h USD volume. Refreshes every 30s.
  Each row deep-links to `/markets/{base~quote}` for full pair
  detail. No backend changes; consumes existing `/v1/markets`
  + `/v1/history` per pair.

## [v0.5.0-rc.18] — 2026-05-07

### Fixed
- **Coinbase / Binance dust trades no longer ERROR-log.** Tiny
  off-chain lots (e.g. 1e-8 XLM at $0.16) compute `base × price /
  10^8 = 0` under our integer precision floor, and the canonical
  validator was rejecting them with `quote_amount must be
  positive, got 0`. The trades are real but below our display
  precision; introduce a typed `ErrDustTrade` sentinel and the
  caller drops the frame silently. ~9 such drops/hour on
  `coinbase` (XLMUSD + ADAUSD) before the fix.

### Added
- **Status page: incident history populated.** First entry on
  status.ratesengine.net under "Incident history" — the SEV-3
  Postgres lock-table-full event from 2026-05-06 (resolved
  22:39 UTC). Hand-maintained in `web/status/src/app/page.tsx`
  until the `/v1/incidents` API (reading from
  `docs/operations/incidents/*.md`) ships.

## [v0.5.0-rc.17] — 2026-05-06

### Fixed
- **`/v1/coins/XLM` 500 regression on rc.16.** The synthetic
  native-row builder (`GetNativeCoinRow`, PR #798) scanned
  the trades hypertable for `WHERE ts >= now() - INTERVAL '7 days'
  AND (base_asset = 'native' OR quote_asset = 'native')` to
  derive `first_seen_ledger` / `last_seen_ledger` /
  `observation_count`. On r1 that's millions of rows and was
  timing out under the existing Postgres lock-table pressure
  (SQLSTATE 53200). Replace with placeholder zeros for the
  ledger bounds and a cheap `prices_1m` row count for
  observation_count. XLM endpoint returns instantly again.

### Added
- **Issuer detail page: external explorer links.** Adds a
  cross-reference panel under the auth flags pointing at
  stellar.expert and stellarchain.io for the issuer's account,
  plus a direct link to the issuer's `stellar.toml` when the
  home domain is known. Useful for verifying SEP-1 metadata
  out-of-band, or pulling the issuer's full operations history
  from a dedicated explorer.
- **Home page: Top markets table.** Sits between Top assets and
  Top movers — top 10 trading pairs by trailing-24h USD volume,
  each row deep-linking to the per-pair detail page at
  `/markets/{base~quote}`. Pulls `/v1/markets?order_by=
  volume_24h_usd_desc`. Complements the asset-centric Top assets
  / Top movers panels with a pair-centric view.
- **Home page: 5-card network stats strip.** Sits above the
  existing 3-column NetworkLivePanel grid showing the
  scale-of-the-network at a glance — total 24h USD volume,
  active markets count, asset directory size, exchange-class
  sources online, and live XLM price + 24h change. All cells
  fed by existing API endpoints (`/v1/markets`, `/v1/coins`,
  `/v1/sources`, `/v1/diagnostics/cursors`); no synthesised
  data, `—` rendered while loading.
- **Cmd-K search: G-strkey + pair shortcut detection.** Typing
  a 56-char Stellar G-strkey now surfaces a "→ Issuer detail"
  result that deep-links to `/issuers/{g_strkey}`. Typing a pair
  shortcut like `XLM/USDC`, `XLM USDC`, or `XLM-USDC` resolves
  the codes against the loaded coins set and surfaces a "→ Pair
  detail" result deep-linking to `/markets/{base~quote}`.
- **`/markets/{base~quote}` per-pair detail page on the explorer.**
  Static-export route enumerating the top 100 pairs by 24h USD
  volume at build time. Renders pair header (base/quote labels +
  current VWAP + 24h change derived from the chart), 24h hourly
  chart sparkline, last-50 trades feed (time / source / price /
  amounts), and a per-source breakdown bar chart showing which
  venue contributed how many of those trades. Markets table rows
  on `/markets` are now clickable links into the new detail page.
- **Stablecoin "PEG USD/EUR/MXN/…" badge on `/assets/{slug}`.**
  Recognises the well-known Stellar stablecoins by code (USDC,
  USDT, PYUSD, DAI, EURC, MXNe, BRZ, GBPC, etc.) and replaces
  the meaningless 0.00% / 0.05% change pills with a single
  honest "Pegged to X" indicator. Non-stablecoin assets still
  show the 1h/24h/7d change pills.

## [v0.5.0-rc.16] — 2026-05-06

### Fixed
- **`/v1/coins/XLM` now returns native XLM, not the scam token.**
  Previously `XLM` matched whichever issued token's code happened
  to be "XLM" wins the disambiguation tiebreak (today: a token
  issued by `GAE5PQNUIP5E…`). Native XLM has no row in
  `classic_assets` by definition, so a special-case
  `GetNativeCoinRow` builds a synthetic row from the same
  `xlm_usd*` CTEs that drive triangulated pricing for every
  other asset. Slug "XLM" and "native" both route here.
  Explorer now pre-renders `/assets/XLM` unconditionally.
- **XLM (asset_id `native`) now returns a non-null `price_usd`,
  `change_1h_pct`, `change_24h_pct`, `change_7d_pct`, and
  `price_history_24h` on `/v1/coins`.** Previously all five were
  null because the SQL CTEs filter on `(base_asset, quote_asset)
  = ('native', 'fiat:USD')` for direct USD and `('native',
  'native')` for XLM-relative — neither has rows in `prices_1m`.
  XLM is now special-cased to use the `xlm_usd*` CTEs (Circle
  USDC / Tether USDT proxy) directly. Other assets are
  unaffected; the existing direct-then-triangulate chain still
  takes precedence when those buckets exist.

### Added
- **`/v1/coins` listing prepends native XLM on the first
  unfiltered page.** Native is the most-active asset on the
  network but has no `classic_assets` row, so the listing
  silently omits it — meaning the explorer's home Top assets
  / Top movers panels never include XLM. The handler now fires
  `GetNativeCoinRow` alongside the listing query when
  `(cursor, issuer, q)` are all empty and `limit ≥ 2`, prepends
  the synthetic row, and trims the listing to `limit-1` so the
  page size stays exactly `limit`. Cursor for page 2 is
  computed from the last listing row, never from native — so
  pagination resumes correctly past the synthetic injection.
- **Status page: real per-endpoint probes.** The Endpoints
  matrix on status.ratesengine.net now fires a parallel probe
  against every public endpoint on each 30-second poll (with
  safe minimum parameters — `?asset=native`, `?limit=1`, etc.)
  and renders a green/amber/red badge with measured latency.
  Endpoints that need auth or are SSE streams keep a static
  "auth req'd" / "stream" tag. Replaces the previous
  single-`/v1/healthz` probe that left every other row stuck on
  "—".
- **`/v1/coins/{slug}.markets_count`** — count of distinct
  `(base_asset, quote_asset)` pairs the asset participated in
  over the trailing 24h. Listing endpoint omits it (count-distinct
  per row would dominate the query cost for 100 rows). Asset
  detail page renders it as a fourth stat in the price card.
- **`/v1/coins[*].change_1h_pct` + `change_7d_pct`** — trailing
  1-hour and 7-day price change windows alongside the existing
  `change_24h_pct`. Same direct-or-XLM-triangulated formula;
  null when no current price or no past-bucket snapshot exists
  in `prices_1m` within the window-specific tolerance (±5min for
  1h, ±30min for 24h, ±2h for 7d). Asset-detail page renders all
  three side-by-side as colour-coded pills.

### Changed
- `internal/storage/timescale.scanCoinRow` extracted as the shared
  row-projection between `ListCoinsExt` and `GetCoinBySlug`. Same
  external behaviour; reduces duplication as the wire shape grows.

## [v0.5.0-rc.15] — 2026-05-06

### Added
- **`/v1/coins/{slug}.price_history_24h`** — 24 hourly USD-price
  samples (oldest first) covering the trailing 24h. Same
  direct-then-XLM-triangulated chain as `price_usd`. Each entry
  `{t: RFC3339, p: rounded-to-10dp USD price or null}`. Powers a
  sparkline next to the headline price on the explorer asset
  detail page.

## [v0.5.0-rc.14] — 2026-05-06

### Added
- **`/v1/coins?order_by=volume_24h_usd_desc`** — opt-in
  ranking by trailing-24h USD volume. Mirrors #765 for markets.
  Cursor format adapts to the active ordering. Default
  remains `observation_count_desc` (preserves the historical
  contract).
- **`/v1/coins/{slug}.top_markets`** — top 5 markets the
  asset participates in (as base or quote), ordered by 24h USD
  volume desc. Lets the explorer asset detail page render a
  Markets preview without a separate /v1/markets call. Each
  entry carries `counterparty`, `side` ("base" | "quote"),
  `volume_24h_usd`, `trade_count_24h`.
- **`/v1/issuers/{g_strkey}.org_name`** — parity with the
  listing endpoint. The listing extracts
  `sep1_payload->>OrgName` already; the single-issuer endpoint
  now does too. Explorer issuer detail page renders the org
  name as the `<h1>` when SEP-1 has been resolved.

## [v0.5.0-rc.13] — 2026-05-06

### Fixed
- **`/v1/coins/{slug}.price_usd` applies the USDC stablecoin
  proxy.** rc.12 fixed the listing query but missed the
  single-asset SQL because GetCoinBySlug's xlm_usd CTE had
  different formatting; `/v1/coins/USDC` returned price_usd:
  null even though `/v1/coins?limit=5` returned $1.00. Same
  stablecoin-proxy now in both paths.

### Changed
- **Wire `price_usd` rounded to 10 dp.** Postgres NUMERIC ×
  NUMERIC preserves 36+ digits which is pure noise on a
  display value. `ROUND(..., 10)` covers sub-millicent
  precision; trims the JSON payload.

## [v0.5.0-rc.12] — 2026-05-06

### Fixed
- **`/v1/coins.price_usd` triangulation now finds an XLM/USD
  price.** rc.10's SQL looked up `prices_1m` for `(native,
  fiat:USD)` but that row never exists in the materialised
  view — the aggregator's triangulation worker writes the
  off-chain Reflector-derived price to Redis. Mirror the
  aggregator's stablecoin-proxy policy in SQL: pick the latest
  `prices_1m` row where the quote is one of {USDC-GA5Z…Circle,
  USDT-GCQT…Tether, fiat:USD}. On-chain XLM/USDC trades are
  continuous on SDEX, so the CTE always finds a row. Same fix
  applies to `xlm_usd_24h` for the change_24h_pct path.

## [v0.5.0-rc.11] — 2026-05-06

### Added
- **`/v1/coins.change_24h_pct`** — trailing-24h price change as a
  signed percentage with two fractional digits. Same direct-then-
  triangulated price source as `price_usd`; the explorer's
  `/assets` table renders the column with green-up / red-down /
  slate-zero colour. Replaces the placeholder em-dash that's been
  in the listing since the rebuild started.

### Changed
- **`buildCoinsQuery` + `GetCoinBySlug` SQL hoisted** to package
  consts — the new CTEs pushed both functions over funlen.
  `coinFromRow()` helper centralises the `timescale.CoinRow →
  v1.Coin` projection so adding a column lands in one spot.

## [v0.5.0-rc.10] — 2026-05-06

### Added
- **`/v1/coins.price_usd` computed server-side via direct VWAP or
  XLM triangulation.** The column was previously hardcoded
  `NULL::numeric` because most active classic Stellar assets only
  trade against XLM on SDEX — the direct asset/fiat:USD VWAP
  doesn't exist for them. Three CTEs now resolve a price:
  `direct_usd` (latest `prices_1m` where `(base, quote) =
  (asset, fiat:USD)`), `asset_vs_xlm` (latest `prices_1m`
  asset/native), `xlm_usd` (latest `prices_1m` native/fiat:USD).
  `COALESCE(direct, asset_vs_xlm × xlm_usd)` picks direct when
  available; falls back to triangulation. `DISTINCT ON
  (base_asset)` gives one "latest per asset" row without a
  window function. Same logic applies to `/v1/coins/{slug}`.
  Result: every active classic asset now shows a real USD price
  on the explorer's `/assets` table and detail page instead of
  an em-dash.

## [v0.5.0-rc.9] — 2026-05-06

### Added
- **`/v1/markets ?order_by=volume_24h_usd_desc`** — server-side
  ordering by trailing-24h USD volume so the most active pairs
  surface in the first page directly, instead of paginating
  alphabetically through ~5K dust pairs to find the ~16 with
  measurable volume. Cursor format adapts to the active ordering.
  SDK + OpenAPI + explorer all flip to use it; the explorer drops
  its previous `limit=500`-and-client-sort fallback.

## [v0.5.0-rc.8] — 2026-05-06

### Added
- **`/v1/markets` surfaces `volume_24h_usd` per pair.** Trailing-
  24h USD volume joined from the prices_1m hypertable's
  per-bucket `volume_usd`. Pointer + omitempty so a pair with no
  USD-equivalent trades emits null instead of "0" — clients can
  distinguish unknown from definitely-zero. Explorer Markets
  table renders a 24h volume column and reorders to volume-desc
  (then trade-count-desc), matching Etherscan / Oklink convention.

### Changed
- **docs.ratesengine.net migrated from Redocly to Scalar.** 788KB
  inlined Redocly bundle replaced by a 1KB index.html that loads
  `@scalar/api-reference@1.34.10` from a pinned jsdelivr CDN URL
  and points it at a colocated YAML spec. CI drift check + Pages
  artefact both extended to track the YAML.
- **Explorer navbar Status + API Docs links route to subdomains**
  (`status.ratesengine.net`, `docs.ratesengine.net`) instead of
  404'ing on local `/status` + `/docs`. Footer + home + signup +
  not-found pages all updated.
- **Cmd-K SearchModal** hits `/v1/coins?q=…` server-side
  (200ms debounce) so it finds any of the ~440K classic assets
  instead of just the top-100 default.
- **Asset detail Overview tab** folds volume / market cap /
  circulating into the Price card; hides the Supply panel
  entirely when no supply data exists. No more wall-of-em-dashes
  for active classic assets.

## [v0.5.0-rc.7] — 2026-05-06

### Added
- **`/v1/coins?q=…` server-side search** — case-insensitive
  substring filter across `code`, `slug`, and `issuer_g_strkey`,
  capped at 64 chars. Lets the explorer's `/assets` search find
  any of the ~440K classic assets instead of filtering only
  the current page. SDK gains `CoinsOptions.Q`. Explorer
  debounces input 250ms into the URL so each keystroke
  doesn't refire the request.
- **`/issuers/[g_strkey]` detail page on the explorer** —
  identity, auth flags (required / revocable / immutable /
  clawback), SEP-1 resolution age, and a table of every
  classic asset minted by the G-strkey deep-linking each row
  to `/assets/<slug>`. Sitemap now enumerates the top 100
  issuer pages alongside asset pages.

### Fixed
- **`/v1/coins/{slug}` volume agreed with chosen row.** rc.6
  picked the canonical issuer in the outer SELECT but the
  CTE's inner `... = (SELECT asset_id FROM classic_assets
  WHERE COALESCE(slug, code) = $1 LIMIT 1)` was arbitrary-
  ordered, so it summed a different same-code issuer's
  prices_1m rows than the outer query returned —
  `volume_24h_usd` came back null even when the canonical
  asset had real volume. The chosen asset_id is now hoisted
  into its own CTE so both branches share one row.

### Changed
- **`buildCoinsQuery` switched from switch-case to a slice-
  based composer** now that the (issuer × cursor × q)
  combinatorial form outgrew the four hand-written branches.
  No SQL surface change.

## [v0.5.0-rc.6] — 2026-05-06

### Fixed
- **`/v1/coins/{slug}` returned the wrong issuer for shared codes.**
  Many classic asset codes (e.g. USDC) are issued by multiple
  G-accounts; `classic_assets.slug` is auto-disambiguated only
  for the canonical row, with same-code later issuances getting
  `slug=null`. The previous `WHERE COALESCE(slug, code) = $1
  LIMIT 1` matched both kinds and arbitrary row order picked
  the wrong one (production was returning a 5,931-observation
  USDC instead of Circle's 41M-observation row). New ordering
  `(slug = $1) DESC NULLS LAST, observation_count DESC` picks
  the exact slug-column match first, then breaks ties by
  activity.

### Changed
- **Explorer triangulates USD via XLM** on the asset detail page
  when the direct `asset/fiat:USD` VWAP is missing. Most active
  classic Stellar assets only trade against XLM (or stablecoins)
  on SDEX, so the aggregator's per-pair USD VWAP doesn't exist;
  composing `(asset/XLM) × (XLM/USD)` client-side gives every
  active asset a real USD price tagged with the existing
  `triangulated` flag.
- **Home page Top assets table** added below the hero. Top-10
  by observation count with real 24h volume USD per row, deep-
  linking into `/assets/<slug>`.
- **Dropped synthetic sparkline values** from the Network home
  panel — the `[60_000, 65_000, 71_000, …, assetsCount]` series
  was hardcoded month-over-month inserts implying growth that
  the project couldn't prove. Real series plumbs in once the
  multi-window delta pipeline lands.

## [v0.5.0-rc.5] — 2026-05-06

### Added
- **`GET /v1/coins/{slug}`** — single-asset lookup by URL-safe
  slug. Same row shape as one element of `/v1/coins`. Used by
  the explorer asset detail page (`/assets/[slug]`) so deep
  links work for every classic asset, not just the top 500
  by observation count. Returns 404 on no-match.
- **`pkg/client.Coin(ctx, slug)`** wraps the new endpoint.

### Changed
- **Explorer asset detail page** fetches `/v1/coins/{slug}`
  directly instead of scanning the top-500 listing. Tab
  panels (Markets / History / Supply) take `assetID` as a
  prop instead of doing their own slug lookup — one network
  round-trip per page render instead of four, and pages no
  longer 404 for assets ranked below 500.

## [v0.5.0-rc.4] — 2026-05-06

### Added
- **`/v1/assets/{id}` F2 fields fall back to per-asset stats.**
  When the formal supply pipeline doesn't have a snapshot for an
  asset (most classic assets today), the asset detail endpoint
  now overlays `volume_24h_usd` from the new union-CTE query so
  the explorer asset page surfaces real numbers instead of `—`.

### Changed
- **`/v1/coins` volume rebuilt on a `prices_1m` UNION CTE.** The
  previous LATERAL joins targeted `classic_asset_stats_5m` (an
  unwritten table — the migration shipped without a writer) and
  direct `fiat:USD` price pairs (which classic Stellar assets
  don't have; only off-chain crypto:* sources do), so every row's
  `volume_24h_usd` came back null. The new query sums real
  `volume_usd` from `prices_1m` over the trailing 24h, where the
  asset participates as base OR quote — same pattern
  `Volume24hUSDForAsset` already uses for the single-asset
  endpoint. `price_usd` / `market_cap_usd` / `circulating_supply`
  explicitly stay null until the proper sources are wired.
- **`/coins/[slug]` → `/assets/[slug]` migration.** Asset detail
  routes move off the legacy `/coins/` prefix to match the
  renamed listing. `_redirects` adds `/coins/* →
  /assets/:splat 301` so existing inbound links 301 at the CF
  edge before any HTML loads.
- **Container width unified to `max-w-7xl`** across every
  top-level explorer page (was a mix of `max-w-6xl` and
  `max-w-7xl`). Navbar + Footer already used `max-w-7xl`, so
  page content rails now align with the chrome around them —
  fixes the "container in a container" feel.
- **Stale `/coins` labels mopped up.** Home CTA "Browse coins" →
  "Browse assets"; `/issuers` G-strkey link href; `/network`
  body link display text; sitemap doc-comment.

## [v0.5.0-rc.3] — 2026-05-06

### Added
- **`/v1/coins` keyset pagination + per-row metrics.** Each row
  now joins the latest `classic_asset_stats_5m` bucket
  (`volume_24h_usd`, `outstanding_supply`) and the latest
  `prices_1m` bucket against `fiat:USD` (`vwap`). Optional
  fields per row: `price_usd`, `volume_24h_usd`,
  `market_cap_usd` (= price × supply when both known),
  `circulating_supply`. Cursor pagination via
  `?cursor=<obs_count>:<asset_id>` lets clients iterate the
  full ~440K-asset population. Wire shape changed to
  `{coins, next_cursor, limit}`. `pkg/client` SDK + OpenAPI
  spec updated.
- **Custom Next.js status page.** `status.ratesengine.net`
  flips from cstate (Hugo) to a Next.js static-export at
  `web/status/`. Polls `/v1/status` every 30 s; renders
  overall banner + per-service heartbeats + p50/p95/p99
  latency strip + ingest-freshness + active incidents from
  Alertmanager + curated public-endpoint matrix.
- **`/assets` explorer route.** Replaces the previous
  `/coins` directory with a dense, paginated, etherscan-grade
  table of every Stellar asset — real price, market cap,
  volume, supply via the new `/v1/coins` join. Per-page
  selector (50/100/200/500). Cursor pagination round-trips
  through the URL.

### Changed
- **`web/showcase/` → `web/explorer/`** repositioning. The
  site is the canonical Stellar asset explorer (powered by
  our data); the directory name + Makefile targets + workflow
  names + CF Pages job labels are renamed to match. CF Pages
  project itself stays `ratesengine-showcase` for now (CF
  doesn't support project rename).
- **`/coins/` → `/assets/`** edge redirect (301) via
  `public/_redirects`. Asset detail pages remain at
  `/coins/<slug>/` for now; that migration is a follow-up.
- **Removed every fake / seed data path** from the explorer:
  `lib/coins-seed.ts`, `lib/chart-seed.ts`, `fakeActivity()`
  sparkline column. Fields the API doesn't yet expose render
  as `—` rather than fabricated values.
- **Removed every link to internal markdown files** from the
  explorer (24 GitHub-blob links across 9 pages).
- **Cloudflare Pages bootstrap script.**
  `scripts/ops/cf-pages-bootstrap.sh` provisions all four
  customer-facing surfaces (`ratesengine-showcase`,
  `ratesengine-dashboard`, `ratesengine-status`,
  `ratesengine-docs`) plus DNS + custom domains via the
  Cloudflare API. Idempotent.

### Removed
- **cstate status page** (~13K lines of vendored Hugo theme).
- **Duplicate `/status` and `/docs` explorer routes** — those
  are dedicated subdomains now.

## [v0.5.0-rc.2] — 2026-05-06

### Fixed
- **`/v1/auth/login` returned 500 with no dashboard config.**
  When the operator hadn't set `[api.dashboard].base_url`,
  `buildDashboardBundle` was wrapping nil concrete `*Handlers`
  pointers in non-nil `DashboardAuthMounter` interfaces, so
  the routes mounted but their handlers panicked on first
  request. New `nilOrMounter` helper in `cmd/ratesengine-api`
  returns true nil interfaces for empty bundles. Surface
  effect: dashboard routes now correctly 404 when not
  configured (instead of 500/401-ing on a half-mounted
  surface).
- **`scripts/dev/cut-release.sh` rejected SemVer pre-release
  tags.** The CHANGELOG section regex collapsed dots and
  dashes in the tag (e.g. `v0.5.0-rc.1`) into a one-char
  awk character class, so the lookup never matched. Replaced
  with a literal-substring `index()` match.
- **`deploy.yml` Ansible task tripped on apostrophe in a
  shell-block comment.** Ansible's shlex-based argument
  splitter rejected `don't` inside the multi-line shell
  string. One-char rewording.

## [v0.5.0-rc.1] — 2026-05-05

First release candidate carrying the Phase 1 platform stack
(magic-link auth, dashboard SPA, key management, Postgres-backed
runtime auth) plus operational coverage extensions
(SLA-probe Healthchecks).

Tested against Stellar protocol 23 (Whisk).

### Added
- **SLA-probe Healthchecks.io coverage.** New
  `ratesengine-sla-probe.timer` (15-min cadence) wraps the
  existing `ratesengine-sla-probe` binary and reports pass/fail
  against the RFP SLAs (p95 ≤ 200 ms, p99 ≤ 500 ms, freshness
  ≤ 30 s) to a Healthchecks.io URL. Closes the four-binary
  coverage gap from the launch backlog (the indexer, aggregator,
  api heartbeats already shipped; this completes the set with
  the SLA-evidence harness on the same Healthchecks pipeline).
  Configured via `HEALTHCHECKS_URL_SLA_PROBE` in
  `/etc/default/ratesengine-healthchecks`; tuning knobs for
  duration / concurrency / pair via the same env file.
- **Postgres-backed runtime auth validator with Redis
  read-through cache (Phase 1, Week 4 cutover).** New
  `auth.PostgresAPIKeyValidator` makes `platform.api_keys`
  canonical for runtime auth; Redis becomes a read-through
  cache (existing `apikey:<hash>` JSON shape preserved so
  legacy `/v1/signup`-minted keys keep working transparently).
  Cache hit short-circuits Postgres; cache miss hits Postgres
  + writes back. Degrades-not-fails on Redis I/O errors. New
  `[api].auth_backend` config (default `redis`; opt-in
  `postgres`) toggles the validator. Dashboard's revoke
  handler now calls `InvalidateCachedKey` so a revoked key
  stops authenticating immediately rather than waiting for
  the cache TTL to roll it off. With this, keys minted from
  the dashboard authenticate against the runtime API as soon
  as `auth_backend=postgres` is set.
- **Dashboard key-management endpoints + UI (Phase 1, Week 4
  part 2).** New `internal/api/v1/dashboardkeys` package wires
  three session-gated routes:
  `GET /v1/dashboard/keys` (list), `POST /v1/dashboard/keys`
  (mint, returns plaintext exactly once), and
  `DELETE /v1/dashboard/keys/{id}` (revoke). Cross-account
  revoke attempts return 404 (same shape as not-found, so
  attackers can't enumerate other accounts' key IDs). Quota
  capped at 25 active keys per account. Companion
  `web/dashboard/src/app/keys` UI: list table with revoke
  button + "save this key now" banner that displays the
  plaintext exactly once + create-key form with name /
  description / rate-limit / IP-allowlist fields. Bare IPs are
  auto-promoted to /32 (v4) or /128 (v6). Server-side wired
  via the new `v1.Options.SessionAuth` middleware that
  resolves the dashboard cookie on every request — anonymous
  + bearer-token traffic passes through untouched.

  **Note:** keys minted from the dashboard land in Postgres
  only and DO NOT authenticate against the runtime API until
  the cutover (next slice). The dashboard surfaces this in a
  footer notice. The `/v1/signup` flow (Redis-canonical) keeps
  working unchanged.
- **APIKey Postgres store (Phase 1, Week 4 part 1).** New
  `postgresstore.APIKeyStore` against migration 0027's
  `api_keys` table — concrete impl of `platform.APIKeyStore` with
  Create / Get / GetByHash / ListForAccount / Update / Revoke /
  TouchUsage. Round-trips JSONB permissions, `cidr[]` IP
  allowlist (custom driver.Value array marshaller), and `text[]`
  referer allowlist. Sentinel-error mapping mirrors the existing
  Postgres stores: hash collision → `ErrConflict`,
  absent → `ErrNotFound`, idempotent revoke. Exercised by
  `test/integration/platform_postgres_stores_test.go`'s new
  `APIKey/CRUD+revoke+touch` subtest. Runtime auth path stays on
  the existing Redis store — the cutover (`/v1/account/keys`
  reading from this store via a Redis-cached read-through) is the
  next slice.
- **Customer dashboard SPA scaffold (Phase 1, Week 3).** New
  Next.js 15 static-export app at `web/dashboard/` deployed to
  `app.ratesengine.net` (Cloudflare Pages git-integration is the
  recommended publish path; CLI fallback covered by the existing
  showcase-deploy workflow shape). Cookie-based auth: every
  request to `api.ratesengine.net` uses `credentials: 'include'`
  so the parent-domain session cookie set by
  `GET /v1/auth/callback` rides along cross-subdomain. Routes:
  `/` bounces by auth state; `/signin/` (magic-link request);
  `/keys/`, `/usage/`, `/settings/`, `/admin/` (staff-gated)
  share a sidebar AppShell. Placeholder bodies for
  `/keys` + `/usage` — the data wiring lands in Weeks 4 + 5.
  Companion `Makefile` targets (`dashboard-{install,dev,build,
  typecheck,lint}`), `verify.sh` extension, and a CI job mirror
  the web/explorer pattern.
- **Magic-link auth flow (Phase 1, Week 2 part 2).** Customers can
  sign in to the dashboard at `app.ratesengine.net` via a
  6-digit-code-or-link email — the same flow handles first-time
  signup (creates a free-tier account + owner user) and returning
  login. Three new endpoints under `/v1/auth/`: `POST /login`,
  `GET /callback`, `POST /logout`. Login responses are constant —
  same `{status:"sent"}` whether or not the email matches an
  account, so attackers can't enumerate users. Callback validates
  + atomically consumes the token and sets an HttpOnly + Secure
  + SameSite=Lax `ratesengine_session` cookie (default 30-day
  rolling lifetime). Logout is idempotent. Implementation in
  `internal/api/v1/dashboardauth/`; transactional email shipped
  via the new pluggable `internal/notify` package (concrete
  `ResendSender` for production, `NoopSender` for dev / tests
  that drops the email but still mints the token so the callback
  flow can be exercised end-to-end). Companion `Middleware` plants
  a `SessionContext` on the request context for downstream
  dashboard handlers; `RequireSession` is the 401 gate. Wiring
  in `cmd/ratesengine-api/main.go` is gated on
  `[api.dashboard].base_url` being non-empty — empty leaves
  `/v1/auth/*` unmounted (404), Resend API key empty falls back
  to NoopSender with a startup warn (production sets
  `RATESENGINE_RESEND_API_KEY`).
- **Platform v1 Postgres stores (Phase 1, Week 2 part 1).** New
  `internal/platform/postgresstore` package with concrete
  implementations of `AccountStore`, `UserStore` (incl. session
  CRUD), and `TokenStore` (magic-link tokens + invites) against
  the schema from migration 0027. Each interface has a
  compile-time `var _ X = (*Y)(nil)` check; testcontainers
  integration test exercises every method including
  conflict / not-found / expired classifications + concurrency-
  safe atomic UPDATE...RETURNING for token consumption.
  Runtime auth path still untouched — these stores are dormant
  until the magic-link flow lands in Week 2 part 2.
- **Platform v1 schema (Phase 1, Week 1).** New
  `migrations/0027_platform_v1_schema.up.sql` lands 12 tables
  for the customer + staff dashboard work specified in
  `docs/architecture/platform-spec.md`: `accounts`, `users`,
  `sessions`, `magic_link_tokens`, `api_keys` (extended with
  name/description/IP-allowlist/expiry/scoped-permissions/usage-
  alert-threshold + last-used tracking), `api_usage_events`
  (TimescaleDB hypertable, 12mo retention), `subscriptions`,
  `stripe_event_log`, `invites`, `audit_log`,
  `customer_webhooks`, `webhook_deliveries`. Reversible via the
  matching `.down.sql`. Companion `internal/platform` package
  ships the Go types + repository interfaces (account / user /
  session / token / apikey / usage / billing / audit / webhook)
  plus the sentinel errors (`ErrNotFound`, `ErrTokenExpired`,
  `ErrConflict`, `ErrAlreadyProcessed`, `ErrLastOwner`).
  Runtime auth path is unchanged in this PR — Redis stays
  canonical until the Week 4 cutover wires Postgres-backed
  reads. Email-provider decision locked to Resend; spec updated.
- `key_prefix` field on `auth.APIKeyRecord` (and the
  `auth.Subject` it derives) — first 12 characters of the
  plaintext key (e.g. `rek_4f9c1d8b`). Surfaced on three wire
  shapes: `POST /v1/signup` (`SignupResult`), `POST /v1/account/keys`
  (`KeyCreated`), `GET /v1/account/keys` + `GET /v1/account/me`
  (`Account`). Showcase /account dashboard renders it as the
  primary "Prefix" column with the full key_id moved to a
  smaller monospaced sub-column. Empty for keys minted before
  this field shipped (legacy keys grandfather in with `—` in
  the dashboard); always populated for new keys. Foundation
  piece from the platform spec — Phase 1, key-listing UX.
  Verified live on R1.
- `.github/workflows/showcase-deploy.yml` — manual-trigger CF Pages
  deploy via Wrangler CLI for hotfix / break-glass cases. Fires
  only on `workflow_dispatch`; the recommended publish path
  remains the CF dashboard's git integration (no Actions minutes
  consumed). Companion `web/explorer/wrangler.toml` pins the
  project name + output dir.
- `scripts/ops/pre-launch-check.sh` — read-only verifier for R1's
  pre-launch state. Walks through every step in the hardening
  doc and prints `pass / warn / fail` for each (binding,
  CORS, Healthchecks.io URLs, Alertmanager secrets, timer +
  service health, Caddy on :443, loopback smoke, recent
  SECURITY warnings). Exit code = number of failures so it can
  cron into a post-deploy gate. Surfaced as `make pre-launch-check`.
- `docs/operations/pre-launch-hardening.md` — operator runbook
  for the config edits that should land before flipping public
  DNS at `api.ratesengine.net`. Covers loopback bind, CORS
  narrowing, Cloudflare proxy mode + trusted-proxy CIDR
  expansion, Stripe / Healthchecks.io / FX-key wiring, smoke
  from the open internet, and a backup baseline. Each step is
  a config edit + restart, not a code change.
- API binary now logs `SECURITY:` warnings at boot when
  `[api].listen_addr` is non-loopback without `trusted_proxy_cidrs`,
  or when `[api].allowed_origins = ["*"]` paired with an auth
  mode that accepts credentials. Doesn't block startup —
  serves anyway — but the warning lands in journalctl and Loki
  so the missed-checklist case is visible.

### Fixed
- HTTP metrics middleware now skips requests whose User-Agent
  identifies a synthetic-monitoring probe (`ratesengine-smoke/`,
  `ratesengine-probe/`). Previously the smoke timer's 5-minute
  cold-cache fan-out (13 endpoints, 4 of which are aggregator-
  derived ~600 ms cold) landed straight in the
  `http_request_duration_seconds` histogram and dominated the
  SLO recording rule's slow-request ratio — `ratesengine_slo_latency_burn_*`
  alerts kept firing even though customer-facing latency was
  sub-millisecond on warm cache. The smoke script now sends
  `User-Agent: ratesengine-smoke/1`; the API drops the
  measurement for those requests so the SLO measures real
  customer experience. Verified live: smoke 13/13 still green;
  histogram empty of smoke entries; customer requests still
  count.

### Added
- `/v1/status` `incidents.active[].runbook_url` — each firing alert
  now carries the GitHub URL of its runbook (when the rule has the
  label set; ~all of ours do). Showcase /status renders it as a
  "runbook →" link inline with each incident, so operators
  clicking through during an incident don't need a separate hop.
  The runbooks are public GitHub markdown so this doesn't leak
  any operator-only signal.

### Performance
- `/v1/assets` and `/v1/markets` Redis read-through caches.
  Same shape as the oracle cache from #696: `cachedAssetReader`
  and `cachedMarketsReader` wrap the store implementations,
  serving paginated reads from Redis with a 60 s TTL. New
  listings surface within one cache cycle (acceptable on the
  human timescale of "asset just got its first trade"). Single-
  asset and single-pair lookups pass through unchanged. Verified
  live on R1: `/v1/assets` cold 634 ms → warm 0.36 ms;
  `/v1/markets` cold 567 ms → warm 0.27 ms (~2000× both).
- `/v1/oracle/latest` Redis read-through cache: cold reads stay
  ~600 ms (DISTINCT ON (source) sort over the oracle_updates
  hypertable union), warm reads drop to ~0.5 ms — three orders
  of magnitude. 30 s TTL stays inside Reflector's push interval
  (Reflector pushes every 1–5 minutes), so customers see no
  meaningful freshness regression. Cache key sorted +
  pipe-joined so the same logical query hits the same key
  regardless of the asset-translation order. Falls through to
  the inner reader when Redis is missing or errors.

### Fixed
- `/v1/oracle/latest?asset=native` now returns 4 oracle
  observations (Band / CoinGecko / RedStone / Reflector-CEX)
  instead of an empty array. Reflector and friends key
  observations by the global crypto ticker (`crypto:XLM`,
  `crypto:USDC`, …) rather than by the per-network canonical
  asset_id, so the previous lookup against `asset='native'`
  found nothing while paying a 285 ms hypertable scan to prove
  it. The handler now expands the user-facing identifier into a
  small candidate list — `native` → `[native, crypto:XLM]`,
  classic credit asset → `[<canonical>, crypto:<CODE>]` — and
  the storage layer's new `LatestOracleUpdatesForAssets` runs a
  single `WHERE asset = ANY($1)` query against the union. Same
  `DISTINCT ON (source)` semantics. Verified live.
- `/v1/price` for fiat-quoted pairs (`native + fiat:USD`) was
  ~215 ms p95 — over the RFP 200 ms target. The `LatestPrice`
  reader's no-rows-from-prices_1m fallback unconditionally
  queried `LatestTradesForPair`, which scanned hundreds of trades
  hypertable chunks looking for an `(asset, fiat:USD)` pair that
  by definition can never exist (fiat-quoted prices are always
  synthesised by the aggregator's triangulation worker, never
  observed on-chain). Short-circuit the fallback when
  `quote.Type == AssetFiat || AssetCrypto` so the handler falls
  straight to `tryRedisVWAPFallback`. Verified live on R1:
  `/v1/price?asset=native` went from 215 ms to ~0.5–1.5 ms.
- SLO latency recording rules in `deploy/monitoring/rules/slo.yml`
  (and the R1 overlay in `configs/prometheus/rules.r1/`) now scope
  to the RFP-mandated pricing surface — `/v1/price`, `/v1/price/batch`,
  and the four SEP-40 oracle endpoints (`/v1/oracle/latest`,
  `/v1/oracle/lastprice`, `/v1/oracle/prices`,
  `/v1/oracle/x_last_price`). The previous deny-list filter
  (everything except `/metrics` / `/healthz` / `/readyz` / `/version`)
  folded catalogue and history endpoints (`/v1/assets`, `/v1/markets`,
  `/v1/history`, `/v1/ohlc`) into the same 99.9% budget, even though
  the RFP only commits the pricing surface to ≤ 200 ms p95. Promtool
  validates the new rules; applied to R1 Prometheus.
- `http_request_duration_seconds` and `http_requests_total` now
  carry the actual route pattern instead of a constant
  `route="unmatched"` label. Logger middleware between HTTPMetrics
  and the mux called `r = r.WithContext(...)`, creating a fresh
  request struct — ServeMux set Pattern on that copy, leaving
  HTTPMetrics holding a request whose Pattern stayed empty. New
  `obs.CaptureRoute` middleware (wired innermost) writes the
  matched pattern into a `*routeCapture` planted in the context
  by HTTPMetrics. Side effect: the SLO burn-rate alerts that fired
  constantly on R1 (because the slow-request-ratio recording rule
  filtered on `route!~/(healthz|readyz|version)/` against
  `route="unmatched"` and got an empty numerator) now produce a
  meaningful 1.0 ratio when every request is fast. Verified live.

### Changed
- `/v1/status` now serves `Cache-Control: public, max-age=10,
  s-maxage=15` (previously fell through to the default
  `private, no-store`). Absorbs the polling fan-out from public
  status pages and dashboards without delaying alert-state
  propagation enough to matter — Prometheus scrape granularity
  is 15 s, so a CDN entry that's at most 15 s stale is no worse
  than asking Prometheus directly. Verified live on R1.

### Added
- Showcase /coins page gets a search input. Typing filters the
  100 directory rows by code, slug, or issuer (case-insensitive
  substring match) and mirrors the term to `?q=` so the URL is
  shareable. Pure client-side until `/v1/coins` grows a server-
  side `q=` parameter; the existing `?issuer=` filter still works
  alongside it. Replaces the stale "static seed today" copy on
  the page footer with the live-data status.
- `ratesengine-smoke.timer` — wraps `r1-smoke.sh` in a 5 min
  systemd timer that pings a Healthchecks.io URL with the full
  smoke output as the ping body. Catches schema regressions the
  metrics-port heartbeats can't see — e.g. `/v1/price` returning
  200 with malformed JSON, or an OpenAPI-spec change that breaks
  downstream clients. Wired through the same secrets file as the
  per-binary heartbeats; new `HEALTHCHECKS_URL_SMOKE` env. Verified
  live on R1.
- `scripts/dev/r1-smoke.sh` — exercise the launch-critical API
  surface (health / catalogue / pricing / diagnostics — 13
  endpoints) against a deployment. Each check runs independently
  with a 5 s timeout; exit code is the number of failures so
  cron / Healthchecks.io can consume it. Anonymous-tier only —
  safe to run from any host. Verified live on R1: 13/13 green.
- Showcase /coins/[slug] gets two new tabs:
  - **History** — table of recent on-chain trades (`/v1/history`)
    against XLM, with relative timestamps, source chip, ledger,
    base/quote amounts, and derived price per row.
  - **Supply** — F2 fields per ADR-0011: circulating/total/max
    (with smallest-unit decimal strings shown for audit), market
    cap, fully-diluted valuation, supply_basis tag, and SEP-1
    issuance declarations (fixed_number / max_number /
    is_unlimited) when the issuer published them.
  Both tabs were placeholder-disabled in CoinTabs; now wired
  through ActiveTabSlot. Liquidity tab remains disabled.
- `configs/healthchecks/` — per-binary Healthchecks.io heartbeats.
  Three systemd `.timer` instantiations of a single template
  service each ping a separate Healthchecks.io URL on a 60 s
  cadence after verifying the corresponding metrics endpoint
  responds. Closes the launch-readiness backlog item: the existing
  Healthchecks.io coverage was galexie/minio/postgres only —
  indexer/aggregator/api were unwatched. URLs come from
  `/etc/default/ratesengine-healthchecks` (off-disk in git);
  empty values silently skip the ping so the timers can install
  before the dashboard URLs are wired. Installed live on R1.
- `/v1/status` now surfaces the *names* of currently-firing alerts
  (`incidents.active`), not just counts. Deduplicated by alertname,
  page-severity first, capped at 16 entries — internal labels
  (component / runbook_url / instance) are intentionally excluded
  so the surface stays anonymous-friendly. The showcase /status
  page renders the list under the active-incident banner with
  per-severity dots. The Go SDK gains an `ActiveIncident` type on
  `StatusIncidents.Active`. Verified live on R1.
- `pkg/client`: new SDK methods covering the recently-shipped
  endpoints — `Client.Status` (system-health rollup),
  `Client.Keys` (list account keys), plus `Client.Healthz` /
  `Client.Readyz` / `Client.Version` operational helpers. Each
  ships with wire-shape tests and a runnable Example so the
  pkg.go.dev page renders complete coverage on first publish.
- OpenAPI `example:` blocks on `/v1/price`, `POST /v1/signup`, and
  `/v1/status` — auto-generated reference docs and the Postman
  collection now show realistic request/response samples instead of
  empty placeholders. Postman collection regenerated.
- Showcase /status page renders the new `/v1/status` rollup as an
  "SLA & live metrics" panel: p50 / p95 / p99 latency cards (with
  the RFP-mandated p95 ≤ 200 ms target shown as a sublabel),
  active-source count, and an active-incident banner when
  Alertmanager has alerts firing. The panel hides itself when the
  backend isn't wired (`flags.stale=true`), so the page degrades
  cleanly on deployments without Prometheus.
- `GET /v1/status` — comprehensive system-health rollup powering the
  showcase status page. Returns per-binary heartbeats (api / indexer
  / aggregator), API histogram-derived p50/p95/p99 over the last
  5 min, ingest freshness signals, and a count of currently-firing
  Alertmanager incidents grouped by severity. Backed by an optional
  `[api] prometheus_url` config pointing at the local Prometheus;
  unwired deployments serve an in-process surface (region label
  + uptime) with `flags.stale=true`. Always returns 200 — degraded
  state is signalled via the body's `overall` field so monitoring
  dashboards can poll a single endpoint without alerting on 503s.
- `configs/alertmanager/` — single-host Alertmanager config for R1.
  Routes our `page` / `ticket` / `informational` severity vocabulary
  (the multi-host Ansible template at
  `configs/ansible/roles/prometheus/templates/alertmanager.yml.j2`
  uses `critical` / `warning` / `info`). The deadmansswitch is
  routed to a Healthchecks.io URL on a 60 s cadence; page +
  ticket alerts fan out to Slack via env-substituted webhooks
  with no-op stub receivers when URLs are absent. Verified on R1:
  amtool check-config passes, real alerts route correctly.
- `examples/` — first-class API usage examples for customers.
  Ten `curl` scripts cover the launch endpoints (healthz, signup,
  account/me, coins, price, price/stream, ohlc, history,
  oracle/latest, markets) plus a Postman v2.1 collection
  auto-generated from the OpenAPI spec. Each script is
  smoke-tested against R1.
- `configs/prometheus/rules.r1/` — single-host adaptation of the
  multi-host alert rules in `deploy/monitoring/rules/`. Six files
  apply on R1: api, aggregator, ingestion, infra, meta, slo
  (42 rules total). Files that depend on services we don't run
  on R1 (Redis/Postgres exporter, archive verifier, sla-probe) are
  intentionally not adapted; see the directory's README for the
  exclusion list and the migration path back to `deploy/monitoring`
  once R2/R3 land.

## [v0.0.0-rc.1] — 2026-05-05 — Pre-release smoke test

**Operator action required: no.**

First exercise of the release pipeline (release.yml + Dockerfiles +
deploy.yml). Pre-release tag — not for production deploy. Validates
that:

- Cross-compile produces a runnable binary for every cmd/ target
- SHA256SUMS verifies on download
- Container images build + push to ghcr.io for every binary
- CHANGELOG-section auto-extraction lands a non-empty Release page

This release is intentionally a snapshot of main at the time of
tagging — no behavioural change vs the preceding commits. The
pipeline itself is what's being tested.

### Tested against
- Stellar pubnet protocol 23 (post-Whisk).

### `pkg/*` versions included
- `pkg/client v0.1.0` (unchanged from prior tag).

### Changed

- **Versioning policy switched from CalVer to SemVer for binary
  releases.** Binaries now tag at `vX.Y.Z` instead of `YYYY.MM.DD.N`.
  Pre-v1.0 follows the same convention as `pkg/*`: breaking changes
  bump the minor version (`v0.1 → v0.2`), not the major. The
  release runbook, release-notes template, and CHANGELOG release
  section header all updated. The pre-launch placeholder
  `[2026.06.30.1]` is now `[v0.1.0]`. See
  `docs/architecture/semver-policy.md` and
  `docs/operations/release-process.md` for the bump rules and the
  end-to-end runbook.
### Added

- **`deploy.yml` workflow + `deploy-binary` Ansible playbook.**
  `gh workflow run deploy.yml -f region=r1 -f version=vX.Y.Z` is
  now the supported deploy path. Stacks on the SemVer / release.yml
  / Dockerfiles foundation. Per-binary sequence: stage → backup →
  atomic rename → restart → /v1/healthz probe (api) or
  systemctl-is-active probe (others) → automatic rollback on probe
  failure with the bad binary preserved at `<binary>.failed-<v>`
  for forensics. Backups land at
  `/usr/local/bin/<binary>.prev-<previous-tag>` with the most-recent
  5 retained. Uses sidecar files at
  `/var/lib/ratesengine/deployed-versions/<binary>` to track the
  current version (the binaries don't expose `--version` yet —
  separate launch-readiness item). Required GitHub secrets
  documented in `docs/operations/deploy-workflow.md`. R1 only for
  v1; adding R2 / R3 is a 4-line workflow extension once those
  regions exist.
### Added

- **`-version` flag on `ratesengine-{indexer,aggregator,api,sla-probe}`.**
  All four long-running binaries now accept `-version` (and
  `--version`) and print the embedded version string then exit
  successfully. Output format is `<tag> (<build-date>, <go-version>)`,
  e.g. `v0.2.0 (2026-07-15T11:02:20Z, go1.25.9)`. Matches the
  `version` subcommand the `ratesengine-{ops,migrate}` CLIs already
  shipped — every binary now has a non-invasive way to report what
  version it was built from. Resolves the deploy-workflow follow-up
  that previously required parsing journal output or sidecar files
  to know what was running on a host.
### Added

- **`scripts/dev/cut-release.sh` guard-rail script + `make
  smoke-docker` target.** `cut-release.sh vX.Y.Z` checks branch +
  clean tree + sync-with-origin + non-empty CHANGELOG section + a
  green `verify.sh` before tagging and pushing — catches the
  "oops, dirty tree" / "oops, empty CHANGELOG section" footguns at
  the operator instead of after the release workflow runs.
  `--dry-run` shows what would happen without committing. Pairs
  with the `make smoke-docker` target that runs `docker run --rm
  ratesengine/<binary>:local --help` against every locally-built
  image — fast post-`make build-docker` sanity check that all six
  Dockerfiles produce a runnable artefact.

### Changed

- **`Makefile`: `ratesengine-sla-probe` added to `BINARIES`.** The
  SLA-probe binary was implemented and shipped as a systemd unit
  but was never in the Makefile's BINARIES list, so `make build`
  silently skipped it. Adding it means `make build`, `make
  build-docker`, and `make smoke-docker` all cover the full set
  of six binaries.

- **`Makefile`: `build-docker` simplified.** Dropped the "if no
  docker/" guard now that the directory exists, and added a
  `--build-arg VERSION=$(VERSION)` so locally-built images carry
  the same version-stamping as CI-released ones.

### Fixed

- **`/v1/assets` listing latency cut from ~4.9 minutes to under 1
  second.** `DistinctAssets` UNIONed two DISTINCT scans across the
  full trades hypertable (539M rows on r1) with no time filter —
  every call rescanned every chunk. Added the same 14-day recency
  window `/v1/markets` already uses (`MarketsRecencyWindow`); the
  semantic shift is "active assets" rather than "every asset ever
  observed," matching the markets endpoint's contract. Future
  optimisation is a materialised `asset_catalogue` populated
  incrementally by the indexer (would let us drop the recency bound
  entirely); until that ships, this brings the endpoint into the
  30s API budget.

- **LCM home-domain resolver overflowed postgres int4 on every
  call.** `HomeDomainFor` used `^uint32(0)` (= 4,294,967,295 =
  MaxUint32) as the "no upper bound" sentinel for the
  `account_observations.ledger <= $2` filter, but the column is
  declared `integer` (signed 32-bit, max 2,147,483,647). Every
  resolve hit lib/pq with `pq: value "4294967295" is out of range
  for type integer (22003)`, so r1's API logged
  `LCM home-domain resolver failed; falling back to static map`
  for every issuer on every `/v1/assets` request — defeating the
  LCM path entirely. Switched to `math.MaxInt32` (~13y of headroom
  vs Stellar's current ~62M ledger) and added a defensive cap in
  the storage method so a future caller passing a too-high value
  doesn't repeat the failure mode. New
  `TestLCMHomeDomainResolver_AsOfFitsInPostgresInt32` pins the
  contract.

- **`/v1/assets/{id}` `volume_24h_usd` always returned "0" for native
  XLM.** The call site passed `supply.AssetKey(asset)` to
  `Volume24hUSDForAsset`, which returns `"XLM"` for native (the
  supply-package convention per ADR-0011) — but `trades.base_asset`
  stores the canonical wire form `"native"`. The query
  `WHERE base_asset='XLM' OR quote_asset='XLM'` matched zero rows,
  so r1's headline asset reported zero 24h volume despite real
  XLM/USDC trade activity. Pass `asset.String()` (the trade-table
  shape) instead. New `TestF2_VolumeReaderReceivesTradeTableKey`
  pins the contract for both native and classic assets.

### Changed

- **`/v1/price/batch` falls through to the Redis VWAP cache** for
  aggregator-rewritten pairs whose literal form isn't in
  `prices_1m`. Same fix as #631 (single-asset `/v1/price`) and
  #634 (`/v1/price/tip`); without it the batch endpoint silently
  omitted the headline `?asset_ids=native&quote=fiat:USD` row even
  though the single-asset path served it. Refactored
  `lookupPriceBatch`'s per-id loop into a `fetchBatchRow` helper
  to keep cognitive complexity under the lint cap.

- **`/v1/price/tip` falls through to the Redis VWAP cache** for
  aggregator-rewritten pairs whose literal form isn't in
  `prices_1m`. Mirrors the same fallback that landed on `/v1/price`
  in #631 — the two surfaces serve the same underlying data so a
  customer switching between them sees consistent prices on the
  headline `?asset=native&quote=fiat:USD` lookup. Provenance marker
  is dropped on this surface (the tip envelope has no
  `triangulated` flag); operators reading the marker for forensics
  use `/v1/price` instead.

- **`/v1/price` Redis-VWAP fallback now queries 5m, not 1m.** The
  aggregator orchestrator's default windows are `[5m, 1h, 24h]` —
  both per-pair direct refresh and the triangulator write
  `vwap:<base>:<quote>:300` on every tick. The handler's prior
  `1m` lookup missed every read because no writer emits at 1m.
  Aggregator's 30s tick cadence overwrites the 5m key well inside
  its TTL, so served `observed_at` is at most ~30s stale relative
  to bucket-end.

- **`/v1/price` Redis-VWAP fallback now serves direct rewrites, not
  just triangulated values.** Pre-fix, when `prices_1m` had no row
  for the requested pair, the handler consulted Redis but rejected
  cache hits whose provenance marker was absent — preserving the
  documented "Timescale is the source of truth for direct VWAPs"
  invariant. That invariant only applies to LITERAL trade pairs;
  for aggregator-rewritten pairs (XLM/fiat:USD synthesised from
  XLM/USDC-GA5Z…) Timescale's CAGG fundamentally can't be the source
  of truth because the rewrite happens at app layer post-CAGG. The
  handler now serves any cache hit and routes the marker into
  `flags.triangulated` (true/false) so callers can still tell the
  difference. `tryTriangulatedFallback` renamed to
  `tryRedisVWAPFallback` to reflect the broader role.

- **Aggregator default pair set now publishes XLM under both
  `crypto:XLM/fiat:*` and `native/fiat:*`.** XLM has two on-the-wire
  identities — the abstract `crypto:XLM` ticker (used by off-chain
  CEX/FX connectors) and the Stellar-protocol `native` form (used by
  every on-chain DEX/SDEX trade). The aggregator publishes one VWAP
  per `(base, quote)` cache key and the API resolves the caller's
  asset literally, so a customer querying `?asset=native` won't see
  a `crypto:XLM` VWAP and vice versa. Pre-fix, `defaultPairs()` only
  emitted `crypto:XLM/fiat:USD`; on r1 (no CEX connectors enabled)
  every default-pair tick produced an empty window because the
  source list never matched the `native/...`-quoted on-chain trades.
  Adding the `native` form alongside `crypto:XLM` lets the
  aggregator's stablecoin-fiat-proxy expansion (PR #629) reach
  `native/USDC-GA5Z…` source pairs that match actual on-chain
  volume.

- **Aggregator stablecoin-fiat-proxy expansion now includes the
  operator-declared classic-asset USD pegs.** On Stellar mainnet the
  dominant XLM/USD volume is quoted in classic credits like Circle's
  `USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`,
  not the abstract `crypto:USDC` ticker the aggregator's stablecoin
  map keys on. Without this fix r1 sat at
  `ratesengine_aggregator_vwap_writes_total = 0` for hours despite
  62k+ XLM trades per hour landing in the trades table — the
  expansion produced a source-pair list (`XLM/crypto:USDT`,
  `XLM/crypto:USDC`, …) that didn't match anything actually in the
  hypertable, and the headline `/v1/price?asset=native&quote=fiat:USD`
  endpoint 404'd. The aggregator orchestrator now reads
  `cfg.Trades.USDPeggedClassicAssets` (already declared by the
  operator for `trades.usd_volume` population) and appends those
  classic-quoted source pairs to USD-target expansions; the existing
  `Pair=target`-rewrite step lifts the fetched trades onto the
  target pair without needing a per-classic ProxyPair rule.
  `ExpandTargetPair` is now a thin wrapper around
  `ExpandTargetPairWithClassicPegs` so existing call sites stay
  short.

- **Error responses (4xx / 5xx) now override the per-route
  `Cache-Control` directive with `no-store`.** Previously the
  cache-control middleware set the route's directive once at the
  start of the chain (e.g. `/v1/coins` → `public, max-age=60,
  s-maxage=300`) and errors inherited it — so a transient 400 / 404 /
  405 / 429 / 500 on a cacheable route would have been cached by a
  CDN against the same key as the success response and replayed for
  the directive's lifetime. The four problem+json writers
  (`v1.writeProblem`, `middleware.writeRateLimitProblem`, the
  recoverer's panic body, and the `Envelope404` middleware that
  rewrites the mux's text/plain 404/405 defaults) all now set
  `Cache-Control: no-store` immediately before `WriteHeader`.

- **Unknown paths + method mismatches return RFC 9457 problem+json
  instead of Go's default text/plain "404 page not found" /
  "Method Not Allowed".** New `Envelope404` middleware sits in the
  v1 server's standard chain and rewrites the mux's text/plain
  defaults at WriteHeader time so the wire shape matches every other
  v1 error response. SSE handlers and large-body responses are
  unaffected (the wrapper passes Write through verbatim outside the
  rewrite case). Bare-root `GET /` now returns a friendly welcome
  envelope (`{name, version, docs, openapi}`) — accidental visitors
  hitting `api.ratesengine.net` get a useful response instead of a
  bare 404.

- **API request-logger middleware skips 429 responses entirely.** A
  single misconfigured client (or an unauthenticated load generator)
  can produce thousands of 429s per second on a public origin —
  r1 evidence on 2026-05-04 saw 343 k suppressed `systemd-journald`
  messages in a single 60 s probe-vs-rate-limiter window, dropping
  unrelated service messages operators would have wanted. 429
  visibility is preserved by the
  `ratesengine_http_requests_total{status="429"}` counter; the
  per-line WARN log carries no diagnostic value the metric doesn't
  already cover. Other 4xx responses (400, 401, 403, 404) still
  log at WARN.

### Added

- **`[external]` configuration block + r1 enablement of free-tier
  CEX/aggregator/sanity venues.** Until today r1 ran on-chain-only
  because every off-chain venue defaulted to `enabled=false` and
  `/etc/ratesengine.toml` had no `[external]` section. Closed
  RFP §4.7 (CEX coverage) and the `crypto:XLM/fiat:USD` 404
  tracked in `docs/operations/r1-deployment-state.md` §5a.
  Enabled six venues that need no API keys: binance / kraken /
  bitstamp / coinbase (CEX trade streamers, ClassExchange →
  contribute to VWAP), coingecko (aggregator poller, divergence
  signal only), and ECB (daily TARGET-business-day fix, sanity
  anchor only). Paid-tier venues (exchangeratesapi, polygon_forex,
  coinmarketcap, cryptocompare) remain `enabled=false` pending
  credential provisioning. Added the `[external]` block to
  `configs/example.toml` as the canonical operator template;
  clarified `[ingestion].enabled_sources` doc-comment to flag
  that it gates on-chain sources only. Post-deploy verification:
  `crypto:XLM`, `crypto:BTC`, `crypto:ETH` against `fiat:USD` all
  return multi-source VWAPs (3 sources each, no stale flag);
  binance / coinbase / kraken / bitstamp trades land at ~400 / 290
  / 30 / 16 per 2-minute window.
- **`crypto:DASH` added to ADR-0014 allow-list.** One-line extension
  per the in-file amendment policy ("Extension is a one-line
  amendment to ADR-0014, never a superseding ADR"). Unblocks
  recording DASH-denominated quotes from any future source — no
  connector or aggregator change in this PR.

- **Top-cap globals added to every CEX connector's DefaultPairs.**
  Coverage expansion against USD/USDT for ADA, ATOM, AVAX, BCH,
  BNB, DASH, DOGE, DOT, LINK, LTC, NEAR, SHIB, SOL, TON, TRX,
  UNI, XRP — the major non-Stellar cryptos every portfolio /
  CoinGecko-class consumer expects. Per-venue listing reality
  (verified live 2026-05-05 against each venue's public symbol
  endpoint):
  - Binance: 17 pairs added (all USDT-quoted)
  - Kraken: 17 pairs added (all USD-quoted)
  - Bitstamp: 17 pairs added (all USD-quoted)
  - Coinbase: 15 pairs added (all USD-quoted; DASH and TRX are
    not listed there — Kraken/Bitstamp/Binance triple covers
    cross-venue VWAP for those two)
  Aggregator pollers (CoinGecko / CoinMarketCap / CryptoCompare)
  now poll the full crypto × {USD,EUR,GBP} matrix so divergence
  detection mirrors the cross-venue VWAP coverage. MATIC was
  intentionally skipped pending POL-migration cleanup. Test
  files swapped from "ADA-as-known-unknown" to MATIC-as-known-
  unknown to keep negative-path coverage.

- **`ratesengine-sla-probe -api-key` flag + `RATESENGINE_PROBE_API_KEY`
  env-var.** Without authentication the probe hits the anonymous-tier
  rate limit (60 req/min) and reads availability < 0.1 % on every
  non-`/healthz` endpoint — verified against r1 today (66 k samples
  per endpoint over 60 s, 0.03 % availability across the
  authenticated surfaces). The flag attaches `Authorization: Bearer
  <key>` to every probe request so the verdict actually reflects
  SLA compliance. Default reads from the env var so the systemd
  unit can pass the key via `EnvironmentFile=` without leaking it
  onto the `ExecStart` command line. Probe systemd unit, sla-probe
  runbook, and launch-day checklist updated to require the key.

### Changed

- **`internal/canonical/discovery`: in-process dedup before sink
  enqueue.** The async discovery sink now keeps a process-local set
  of `(contract_id, event_type)` keys it has already enqueued and
  silently skips repeats — most SEP-41 events are duplicates of
  already-discovered contracts and the recorder upserts on the same
  key, so re-enqueue is wasted work. Addresses the 99.4 % drop rate
  documented for r1 in #620 (845 k drops vs 4 921 recorded rows): in
  steady state the sink should now never drop. A new
  `ratesengine_discovery_skipped_hits_total` counter exposes the
  dedup hit rate. Drop semantics are unchanged for genuine buffer
  saturation; the seen-mark is rolled back on drop so a later push
  for the same key can retry. Behaviour change: tests that pushed
  the same `(contract_id, event_type)` repeatedly now record once,
  not N times.

### Documentation

- **`r1-deployment-state.md` documents discovery-sink drop rate.**
  Sustained ~3 k SEP-41 discovery hits dropped per minute on r1
  (845 k since process start, vs 4 921 rows in
  `discovered_assets` — 99.4 % drop rate). Buffer is hardcoded at
  1024 in the indexer and the postgres recorder can't drain
  faster than new events arrive. Not catastrophic — same
  contracts re-sniff and eventually land — but new SEP-41
  contracts may take many ledgers before their first record
  sticks. Captured in §5c. Code fix landed in the in-process dedup
  change above; once deployed to r1 the drop counter should
  flatline.

### Dependencies

- **`redis/go-redis/v9` v9.18.0 → v9.19.0.** Patch-minor bump
  with relevant production-stability fixes upstream:
  `wrappedOnClose` resource leak, `Pool.Close()` suppressing
  TLS `closeNotify` timeouts on stale connections, FIFO waiter
  ordering race in `ConnStateMachine.notifyWaiters`, and
  `READONLY` detection inside Lua script error messages so
  read-only-replica retries fire correctly. No API surface
  changes affecting our code paths (ratelimit + freeze marker +
  SEP-1 cache). Verified `go test ./internal/ratelimit/…
  ./internal/aggregate/freeze/…` green plus full
  `bash scripts/dev/verify.sh`. Supersedes dependabot PR #548.

### CI

- **Bump actions in `api-docs.yml` and `k6-weekly.yml` to current
  majors.** `actions/configure-pages@v5` → `@v6`,
  `actions/upload-pages-artifact@v3` → `@v5`,
  `actions/deploy-pages@v4` → `@v5` in api-docs;
  `actions/checkout@v4` → `@v6`, `actions/upload-artifact@v4` →
  `@v7` in k6-weekly. Reconciles every workflow's action majors;
  supersedes dependabot PRs #549, #550, #551, #552, #553.

- **Bump actions in `status-page.yml` to match `ci.yml`.**
  `actions/checkout@v4` → `@v6` and `actions/upload-artifact@v4`
  → `@v7`. ci.yml landed on these versions earlier (via separate
  dependabot PRs); the status-page workflow added in #600 was
  on the older versions. Reconciles them so all CI jobs use the
  same major across the repo.

### Documentation

- **`r1-deployment-state.md` reflects current operational state.**
  Adds two findings under "Important but not urgent": the
  aggregator is on latest main (change-summary worker shipped) but
  emits zero VWAPs because `defaultPairs()` is `{XLM,BTC,ETH} ×
  {USD,EUR,GBP}` and none have on-chain trades — operator fix is
  to tune `[aggregate].pairs` to actually-traded pairs OR enable
  CEX/FX connectors. And: `issuers` table seeded with 25,256 rows
  via SQL backfill from `classic_assets` so
  `/v1/issuers/{g_strkey}` serves real data. `last_verified`
  bumped.

- **`release-process.md` pre-flight runs `make web-build`.** Item
  5 ("Build dry-run is clean") now also requires the showcase
  build for releases that ship `web/explorer/` alongside the
  binaries. CI gates on this already, but local verification
  before tagging catches the rare case where a merge-conflict
  fix on `main` slipped past the per-PR gate.

### Added

- **Go SDK methods for the new endpoints.** `pkg/client` now
  exposes `Coins()`, `Issuers()`, `Issuer(g)`, and `Cursors()`
  alongside the existing `Markets()` / `Sources()` methods,
  with corresponding `Coin`, `IssuerListEntry`, `Issuer`,
  `IssuedAsset`, `Cursor` types in `pkg/client/types`.
  `CoinsOptions{Issuer: G…}` exposes the new server-side
  filter; `Issuer(g)` rejects empty G-strkeys at the SDK
  boundary so callers don't round-trip a network hop for a
  trivially-broken request. Wire-shape tests pin the envelope
  + path-escape behaviour. Closes the loop where every showcase-
  surfaced endpoint had a typed Go SDK method.

### Documentation

- **`customer-demo-script.md` opens with the showcase URL.**
  Pre-flight now lists `https://ratesengine.net` as one of the
  required browser tabs; Stage 1 hands the customer the
  interactive explorer up front so the rest of the curl-based
  walk-through has a "click the panel, see the curl" parallel
  they can follow along with. Without this, customers leave the
  demo not knowing the explorer exists.

- **`api-latency.md` runbook flags `/v1/markets`'s natural
  baseline.** New false-positive entry: the route does GROUP BY
  across the 14-day chunk window, so its p95 baseline is ~300 ms
  cold / 50 ms warm — well inside the per-route p95 ≤ 300 ms / p99
  ≤ 1 s carve-out, but high enough that a `route="/v1/markets"`
  breakdown in Grafana looks alarming. Saves the on-call from
  triaging a non-issue.

- **`post-launch-queries.md` lists the showcase routes.** The
  on-call's "what healthy looks like" enumeration in §1
  (request rate per surface) was missing `/v1/coins`,
  `/v1/issuers`, `/v1/issuers/{g}`, `/v1/markets`,
  `/v1/changes/{type}/{id}`, `/v1/diagnostics/cursors` — so on
  launch day, a missing route wouldn't have rung a bell. Added
  the full list. §3 (latency per surface) gets a carve-out
  noting `/v1/markets`'s 300 ms / 1 s bar (matches the new k6
  `07-catalogue-browse` thresholds). Bumps `last_verified`.

### Tests

- **k6 scenario 07-catalogue-browse.** New load-test scenario
  exercising the showcase hot path (`/v1/coins`, `/v1/issuers`,
  `/v1/issuers/{g}`, `/v1/markets`, `/v1/diagnostics/cursors`)
  with traffic-shape weights modelled on browsing behaviour
  (30/25/20/15/10). Pass criteria: p95 < 200 ms on lookups,
  p95 < 300 ms on `/v1/markets` (GROUP BY across the 14-day
  chunk window), error rate < 0.1 %, 5-minute soak. Companion
  to #610 — the SLA probe samples one of each; this scenario
  drives them under load. Deliberately separate from
  `06-mixed-realistic.js` so the official Freighter-RFP SLA
  proof keeps its canonical traffic shape.

### Security

- **Showcase ships `_headers` with CSP + security headers.** New
  `web/explorer/public/_headers` (CF Pages / Netlify format,
  copied verbatim into the build output) sets a restrictive
  Content-Security-Policy that limits `connect-src` to `self` +
  `https://api.ratesengine.net` so a compromised script can't
  exfiltrate to a third party, plus `X-Content-Type-Options`,
  `X-Frame-Options: DENY` (with `frame-ancestors 'none'` mirroring
  it in CSP), `Referrer-Policy: strict-origin-when-cross-origin`,
  and a `Permissions-Policy` denying camera / mic / geolocation /
  payment / USB. The 1-year `immutable` Cache-Control on
  `/_next/static/*` is documented explicitly so Netlify operators
  don't need to know about CF's default. `explorer-deployment.md`
  has a new section explaining the directives + how to translate
  to `vercel.json` if you switch hosts.

### Fixed

- **SLA probe covers the catalogue endpoints.** The probe
  (`cmd/ratesengine-sla-probe`) only sampled `/v1/price`,
  `/v1/oracle/latest`, and the health/version surfaces. The
  showcase site fans out across `/v1/coins`, `/v1/issuers`,
  `/v1/markets`, and `/v1/diagnostics/cursors` on every page
  load — a latency regression on those would only surface as
  "the showcase is slow", well after the SLA probe gate would
  have caught it. Added all four to `staticEndpoints()`. The
  T-0 launch-day smoke probe in `launch-day-checklist.md` now
  exercises every showcase hot-path before the cut.

- **CDN cache headers for new endpoints.** Real launch-perf miss
  caught: every endpoint added in the last 30 PRs
  (`/v1/coins`, `/v1/issuers`, `/v1/issuers/{g}`,
  `/v1/changes/{entity_type}/{id}`, `/v1/diagnostics/cursors`)
  was falling through `policyForPath`'s default → `private,
  no-store`. CDN was being told never to cache them; every page
  load hit origin. Classified now:
  - `/v1/coins`, `/v1/issuers`, `/v1/issuers/{g}` →
    `public, max-age=60, s-maxage=300` (catalogue surface, same
    bucket as `/v1/markets`).
  - `/v1/changes/{entity_type}/{id}` →
    `public, max-age=60, s-maxage=300` (refreshed every 5 min
    by the worker; 60 s edge cache stays well inside that
    boundary).
  - `/v1/diagnostics/*` → `private, no-cache, must-revalidate`
    (showcase polls every 15 s; caching would defeat the
    "watch the indexer tick" UX).
  Test table + `cdn-setup.md` updated to match. Without this,
  launch-day CDN traffic on the showcase's hot pages would have
  hammered origin pointlessly.

### Developer experience

- **`scripts/dev/verify.sh` runs the showcase gate.** The local
  pre-push verify script previously stopped at the Go integration
  build. Adds Showcase typecheck + lint + build as a final stage,
  graceful-skipped when pnpm isn't installed (mirroring the
  promtool skip pattern). Closes the gap where a Next.js
  `output: 'export'` failure would slip past local verify and
  only fail in CI.

### Documentation

- **`getting-started.md` lists the interactive explorer.** The
  URL block at the top of the doc previously listed the API
  endpoint, reference docs, and status page but not the showcase
  site itself. Added a `ratesengine.net` line so newcomers learn
  about the explorer alongside the curl examples. Also bumps
  `last_verified` to 2026-05-04.

### CI

- **`web/explorer` job runs `pnpm build`.** Adds the static-export
  build to the existing CI job that previously only ran typecheck
  + lint. Catches Next.js `output: 'export'` constraints (e.g.
  the `dynamic = 'force-static'` requirement on `sitemap.xml` and
  `robots.txt` routes), `generateStaticParams` issues, and any
  runtime-vs-build divergence that typecheck doesn't see. The
  build runs against `http://api.ci-stub.invalid` so the
  build-time API fetch in `generateStaticParams` falls through to
  the seed-only path; verified locally that the fallback still
  produces a valid static export.

### Tests

- **API-level integration test for `/v1/coins`, `/v1/issuers`,
  `/v1/issuers/{g}`, `/v1/coins?issuer=…`, and
  `/v1/diagnostics/cursors`.** New
  `test/integration/api_registry_cursors_test.go` wires
  `timescale.Store` straight through `v1.Options` (the store
  satisfies all four reader interfaces directly, no adapter
  glue) and asserts the wire shapes the showcase consumes:
  ranking by observation count, issuer-filter behaviour, embedded
  asset list on the issuer detail envelope, cursor ordering by
  (source, sub_source), and that the computed `lag_seconds` field
  is non-negative.

- **Integration test for `ListIssuers`, `ListCoins (?issuer=)`,
  `GetIssuer`, `ListIssuerAssets`.** New
  `test/integration/issuers_coins_storage_test.go` exercises the
  read paths backing `/v1/issuers`, `/v1/issuers/{g}`, and
  `/v1/coins?issuer=…` — the endpoints that landed in #595 / #596
  / #597. Covers ranking by total observation count across an
  issuer's assets, limit clamping, the per-issuer filter, the
  no-match path, and `sql.ErrNoRows` for unknown G-strkeys (the
  contract `handleIssuer` relies on for its 404 path).

### Documentation

- **README + CLAUDE.md mention `web/explorer/`.** Adds a "Hosted
  UI / explorer" entry to the README's Start-here list and a
  one-line entry in the CLAUDE.md repo map. Both files knew
  about the API + reference docs but not the showcase site that
  visitors actually land on first.

- **`launch-day-checklist.md` includes the showcase site.** Adds
  T-7 prep step (CF Pages project staged, custom domain bound,
  preview deploy succeeded), T-0 step 5 (force a fresh build
  after API auth-mode flip so build-time `generateStaticParams`
  picks up production data), pass-condition entry for
  `https://ratesengine.net`, and a Cross-references link to
  `explorer-deployment.md`. Closes the gap where the runbook
  knew about API + status page but not the showcase.

- **`docs/operations/explorer-deployment.md`.** New runbook for
  shipping `web/explorer` to production. Covers the
  Cloudflare Pages path (build command, env vars, custom-domain
  bind, preview-deploy flow), Vercel/Netlify alternatives, the
  rsync-to-r1 fallback, and post-deploy verification checks.
  Closes the documentation gap between "the showcase code
  exists in `web/explorer/`" and "ratesengine.net is live."

### Added

- **CI: status-page Hugo build verification.** New
  `.github/workflows/status-page.yml` runs on every push/PR
  touching `deploy/status-page/`. Steps: yamllint
  `systems.yml`, fetch the cstate theme (pinned v3.6.4),
  `hugo --minify` build, smoke-check the produced
  `index.html`. Catches broken incident front-matter and
  bad systems.yml refs at PR time, before operators push them
  live. Build artifact uploaded with 7-day retention so
  operators on non-CD hosts can grab it. Also updates
  `systems.yml` to list `/v1/coins`, `/v1/issuers`,
  `/v1/markets` under "Asset metadata" and adds a
  "Diagnostics" component for `/v1/diagnostics/cursors`.

- **`/coins?issuer=G…` URL param now actually filters.** The
  `useCoins` hook accepts an optional `issuer` parameter; the
  `/coins` page reads `?issuer=` from the URL and passes it
  through to the API call. A small filter chip with a "clear"
  link appears above the table when the param is set, and the
  panel header switches to "Coins by G-stub…" so the filtered
  context is obvious. Closes the loop on the
  `/issuers → /coins?issuer=…` cross-link added in #596.

- **`GET /v1/coins?issuer=G…` filter.** New optional query
  parameter on the existing coins endpoint that restricts the
  listing to classic assets minted by a single G-strkey. Powers
  the `/issuers → /coins?issuer=…` deep-link the showcase issuer
  table cross-references; uses the existing
  `(issuer_g_strkey)` index on `classic_assets` so the filtered
  scan is O(matching) rather than full-table.

### Changed

- **`/v1/markets` and `/v1/pairs` recency-bound their underlying
  scan to 14 days.** `Store.DistinctPairs` and `Store.PairMarket`
  now restrict the trades-hypertable scan via
  `MarketsRecencyWindow` so TimescaleDB chunk pruning bounds I/O.
  With 441M+ trades on r1, the prior unbounded `GROUP BY` was
  timing out at 30 s — every `/v1/markets` call exceeded the
  client deadline and returned 0 bytes. The 14-day bound runs cold
  in ~540 ms / warm in ~50 ms. (Wider windows were measured: 30 d
  ~9 s, 90 d ~16-19 s — both unusable for a hot path.) Behaviour
  change: pairs that haven't traded in 14 days no longer appear in
  the listing. This matches the public contract — `/v1/markets`
  documents "active markets", not "every pair ever observed". A
  future materialised market_catalogue would let us drop the
  recency bound; for now the bound keeps the endpoint usable.

### Added

- **Footer adds Issuers, GitHub, and Changelog links.** Browse
  column now lists `/issuers`. Bottom strip exposes GitHub
  (RatesEngine/rates-engine) and a direct Changelog link
  alongside the API URL — the latter two were missing despite
  the project being open-source from day one.

- **`GET /v1/diagnostics/cursors` — per-source ingest cursor
  positions.** Operator-facing diagnostic that returns every row of
  the `ingestion_cursors` table (one entry per (source, sub_source)
  tuple) with last_ledger, last_updated, and a precomputed
  `lag_seconds` so stuck sources are obvious without wall-clock
  math. Reads through the `CursorsReader` seam (timescale.Store
  satisfies it via `ListCursors`). Powers the showcase
  `/diagnostics` page.

- **Showcase `/diagnostics` page goes live.** Replaces the v0
  placeholder with a live ingest-cursor table backed by
  `/v1/diagnostics/cursors`. New `useCursors()` TanStack hook
  refetches every 15s so backfills tick visibly; rows group by
  source, lag is colour-pilled (green ≤60s, amber ≤10m, red
  beyond). Decoder-coverage / archive-completeness / SLO panels
  follow as their underlying endpoints ship.

- **Showcase `/issuers` page goes live.** New page at the new
  route, backed by the live `/v1/issuers` endpoint. Each row links
  through to a filtered `/coins?issuer=…` view; home_domain
  becomes a clickable external link as the SEP-1 fetcher resolves
  it. Page is added to the sitemap and Cmd-K search.

- **`GET /v1/issuers` — issuer directory.** New endpoint that
  lists every G-account having minted at least one classic asset
  on Stellar, ranked by total observation count across their
  issued assets. `Store.ListIssuers` joins `issuers` ⨝
  `classic_assets` and aggregates so home_domain (when populated
  by the SEP-1 fetcher) flows through without a per-row lookup.
  Powers the future showcase `/issuers` directory page; today the
  endpoint serves real data — top-of-list is the USDC issuer with
  41M observations.

- **`/network`, `/divergences`, `/anomalies`, `/mev` pages get
  real content.** Final placeholder cleanup. `/network` covers
  the three-region active-active architecture (ADR-0008) +
  closed-bucket consistency (ADR-0015); `/divergences` explains
  the cross-reference monitor methodology with cards for every
  reference (CoinGecko, Chainlink HTTP, Reflector, Redstone,
  Band); `/anomalies` lists the four freeze trigger conditions
  per ADR-0019; `/mev` documents the four detector patterns
  (sandwich, oracle-update sandwich, liquidation cascade, wash
  trading) with concrete Stellar-specific examples. Each page
  flags the live data path that lights it up once the underlying
  endpoint ships.

- **`/docs` page gets a real endpoint catalogue.** Replaces the
  "go elsewhere" placeholder with eight grouped endpoint tables
  (Pricing, History & charts, Asset & coin catalogue, Markets &
  change summary, Oracles SEP-40, Sources & diagnostics, Account
  & SEP-10, Health & version) — every endpoint with its path,
  method, and one-line summary. Top of page shows the live base
  URL and envelope shape; bottom calls out the three SSE
  consistency surfaces. CTAs point at the full Redocly reference
  and the OpenAPI source on GitHub.

- **`/research` page gets real content.** Replaces the v0
  placeholder with a curated index of the public-repo writeups —
  six featured items (ADRs 0003 / 0015 / 0019, plus the Soroswap
  pair-registry / CAP-67 unified events / Reflector missing
  methods discoveries) and a topics index linking to ADRs,
  discovery audits, runbooks, and architecture narratives. Sets
  the "every choice is in the repo" expectation that the site's
  positioning depends on.

- **`/lending` and `/aggregators` pages get real content.**
  Replaces the v0 placeholders. `/lending` covers Blend in detail
  (isolated pools, Reflector-priced collateral, Comet auction
  backstop, MEV exposure) with deep links to `/oracles` and
  `/dexes`. `/aggregators` covers Soroswap Router and DeFindex,
  and explains up front why aggregators are excluded from the
  canonical VWAP (avoids double-counting the upstream
  price-discovery event).

- **`/dexes` and `/oracles` pages get real content.** Replace the
  v0 placeholders with curated cards for every venue — Soroswap,
  Phoenix, Aquarius, SDEX, Comet on `/dexes`; Reflector trio
  (DEX/CEX/FX), Redstone, Band on `/oracles`. Each card lists the
  integration quirk discovered during decoder development (e.g.
  Soroswap SwapEvent has no post-state reserves, Band's contract
  emits zero events, Phoenix swaps fan out across 8 events) and
  links to the full audit notes in `docs/discovery/`. `/oracles`
  also explains the SEP-40 compatibility surface and divergence
  monitoring up front.

- **Home page hero + Try-the-API panel.** Replaces the generic
  "Stellar pricing explorer" intro with a clearer hero (independent
  / open / public-tier free), three CTAs (Browse coins / Browse
  markets / API docs), and a tabbed `HomeTryAPI` panel with four
  copy-pasteable curl examples (latest XLM/USDC, top-100 coins,
  active markets, ingest cursors). Drops the unused fake "Top
  movers" + "Sample composite" stub blocks. Makes "what is this
  for and how do I use it" answerable in 10 seconds on the home
  page.

- **Custom 404 page.** Static-export-compatible `not-found.tsx`
  with a recovery list (Home / Coins / Markets / Sources /
  Diagnostics / API docs) so visitors hitting a stale or mistyped
  URL aren't dumped on Next's default 404.

- **Showcase SEO foundations.** Adds `app/robots.ts` and
  `app/sitemap.ts` so static export emits both at build time:
  robots.txt allows all crawlers (carve-out for `/dev/`), sitemap
  enumerates every static route plus the live top-100 coin
  detail pages (119 entries on a current build). Root layout now
  carries OpenGraph + Twitter card metadata + a comprehensive
  keyword list; `/coins/[slug]` adds per-page `generateMetadata`
  so each coin gets its own title + description; `/coins`,
  `/markets`, `/sources`, `/diagnostics`, and `/docs` ship
  page-level metadata too. Required for clean public flip /
  search-engine indexing.

- **Markets tab on `/coins/[slug]` goes live.** Replaces the
  disabled placeholder with a live markets panel that joins
  `/v1/coins` (slug → asset_id) and `/v1/markets` (recently-active
  pairs), then filters to markets where `base == asset_id` or
  `quote == asset_id`. Each row shows whether the coin is the
  base or quote, the counterparty asset, 24h trade count, and
  last-trade-relative timestamp. Cache keys match the `/coins`
  and `/markets` pages so navigating between them costs zero
  extra network.

- **`/coins/[slug]` pre-renders the live top-100.** `generateStaticParams`
  now fetches `/v1/coins?limit=100` at build time and unions the
  result with the design seed, so every coin in the directory has a
  pre-rendered route. Newly-observed assets that aren't in the seed
  render through `synthesizeCoin()` — Chart + Issuer tabs still
  work because they fetch live data from the slug; Overview shows a
  "minimal metadata" panel instead of zeroed seed fields. `findCoin`
  is now case-insensitive so live API slugs (`USDC`, `yXLM`) and
  the dev seed slugs (`usdc`, `yxlm`) both resolve.

- **Cmd-K search ranks against the live coin directory.** The
  global `SearchModal` now reads coins from `useCoins(100)` —
  same cache key as the `/coins` page, so opening search and
  navigating after costs zero extra network. Empty-query
  starter list shows the top 5 coins by observation count
  (already API-sorted). Protocols + static pages remain seeded
  until the unified `/v1/search` endpoint ships.

- **Showcase `/markets` page goes live.** Replaces the v0
  placeholder with a live markets directory backed by `/v1/markets`,
  client-sorted by 24h trade count desc. New `useMarkets()` hook
  unwraps the standard `{data:[…]}` envelope plus the cursor for
  future virtual-scroll pagination. AssetLabel splits canonical
  asset strings (`<code>-<G-issuer>`) into prominent code +
  truncated issuer beneath. Heatmap and per-venue sub-tables
  follow.

- **Issuer tab on `/coins/[slug]` goes live.** Replaces the
  disabled placeholder with a live issuer panel backed by
  `/v1/issuers/{g_strkey}`. New `useIssuer()` TanStack hook;
  `IssuerPanel` shows G-strkey, home_domain, creation ledger,
  SEP-1 resolution timestamp, the four asset auth flags
  (auth_required / auth_revocable / auth_immutable /
  auth_clawback) as colour-coded pills, and the full table of
  issued assets with cross-links to each. USDC's issuer card now
  shows the ~20-asset directory in one shot.

- **Showcase home page Network + System-health panels go live.**
  Network panel now shows the live classic-asset count from
  `/v1/coins` and the highest non-backfill cursor as the current
  ingest tip. System-health panel derives indexer status from
  `/v1/diagnostics/cursors` (green ≤60s lag, amber ≤10m, red
  beyond) so the home page reflects real backfill/ingest motion
  instead of static traffic-light stubs. "Top movers — 24h"
  remains stubbed until the change-summary worker has 24h of
  history.

- **Showcase `/sources` page goes live.** Replaces the v0
  placeholder with a live source directory backed by `/v1/sources`,
  grouped by class (exchange / aggregator / oracle /
  authority_sanity) so the "only Class=exchange contributes to
  VWAP by default" boundary is visible at a glance. Per-source
  flags surface as pills (in VWAP, paid, backfill safe, live-only).
  `useSources()` hook now unwraps the standard `{data:[…]}`
  envelope so consumers get a plain array. Per-source health and
  WASM-history panes follow once `/v1/sources/{name}/health` and
  the wasm_versions join ship.

- **Change-summary rollup worker.** New
  `internal/aggregate/changesummary` package + aggregator-side
  worker that, every 5 minutes, walks every configured (coin,
  pair) entity and computes the multi-window delta strip
  (h1/h24/d7/d30 % change), ATH/ATL with timestamps, streak
  (direction + days), and acceleration. Writes one row per
  entity to `change_summary_5m` (migration 0022). Storage
  exposes `Store.UpsertChangeSummary` + `Store.GetChangeSummary`.
  Powers every multi-window delta strip on the showcase — every
  list view + price card reads from this in O(1) instead of
  re-scanning prices_1m. Sink/source adapters live in the
  aggregator binary to avoid a worker→storage import cycle (same
  pattern as the per-source contribution sink).

- **Freeze-event durable mirror.** `internal/aggregate/freeze` now
  takes an optional `EventSink` via the new `WithEventSink` option;
  production wires `internal/storage/timescale.FreezeEventSink`
  which writes every clear→firing transition to the `freeze_events`
  hypertable (migration 0018). Idempotent against the
  currently-firing row, so refreshing the Redis TTL doesn't
  duplicate. The Redis marker remains source-of-truth for the API's
  `flags.frozen` field; the durable mirror powers the showcase
  `/anomalies` timeline (Phase 2 of the showcase implementation
  plan). Sink failures are swallowed — the load-bearing Redis write
  must not be blocked by a postgres blip.
- **Divergence-observation durable mirror.** `internal/divergence`
  Service now takes an optional `ObservationSink`; production wires
  `internal/storage/timescale.DivergenceSink` which writes one row
  per (pair, reference) tuple per refresh tick to the
  `divergence_observations` hypertable (migration 0019). Today only
  the boolean `flags.divergence_warning` flag survives across ticks
  — the actual deltas are recomputed each tick and dropped. With
  the sink, the showcase `/divergences` page can plot per-reference
  deltas over time and post-mortems can verify cross-oracle
  disagreements against ground truth. Sink failures are swallowed
  — the Redis cache write is the load-bearing operation and must
  not be blocked by a postgres blip.
- **Decoder-stats periodic flush.** New
  `internal/dispatcher/statsflush` worker snapshots
  `dispatcher.Stats()` every 5 min, computes per-source deltas
  against its previous snapshot, and writes one row per (bucket,
  source) to the `decoder_stats_5m` hypertable (migration 0020).
  Snapshot-and-delta semantics (not snapshot-and-clear) — resetting
  dispatcher counters from outside would race with concurrent
  decoder writes; the worker keeps its own "last seen" reference.
  Wired into `cmd/ratesengine-indexer` as a goroutine bound to
  the root context. Powers /v1/diagnostics/decoders + the
  showcase /diagnostics decoder-coverage table.
- **Per-source contribution persister.** `aggregate.SourceContributions`
  computes per-source weight + base/quote volume + trade count from
  a trade slice. `orchestrator.ContributionSink` is the optional sink
  the orchestrator invokes after every successful VWAP compute.
  Production wires a timescale-backed adapter (in
  cmd/ratesengine-aggregator to avoid an import cycle) so the
  showcase source-contribution donut on every price card reads
  from the `price_source_contributions` hypertable (migration 0026)
  rather than recomputing at request time. Best-effort: sink failures
  log + continue, the VWAP cache write stays load-bearing.

### Fixed

- **Soroswap zero-trades bug — postgres-persisted pair registry.**
  The Soroswap decoder needs a `pair_contract → (token0, token1)`
  map to label swap-event amounts as base vs quote. Until this
  PR the registry was an in-memory dict populated only by live
  factory `new_pair` events, which broke two real cases:
  - **Cold start.** Pairs created before the indexer's first
    ledger were invisible — every swap on those pairs was
    silently dropped via the `skipped_unknown_pair` counter.
  - **Parallel backfill.** `ratesengine-ops backfill -parallel N`
    runs N independent dispatchers; chunk 7 had no idea what
    tokens chunk 2's `new_pair` event introduced.
  Fix: new `soroswap_pairs` registry table (migration 0016),
  `Store.UpsertSoroswapPair` + `LoadSoroswapPairRegistry`, a
  decoder `WithPairUpsertHook` option, and a one-shot
  `ratesengine-ops seed-soroswap-pairs` subcommand that walks
  the factory via `simulateTransaction` and bootstraps the
  table. Indexer + every backfill chunk loads the table at boot
  and writes through on every live `new_pair` event. Existing
  Soroswap data in the trades hypertable from before this fix
  needs a re-backfill — operator action, not automatic.

### Documentation

- **L6.5 documentation sweep — pre-launch pass** —
  comprehensive scan across all 251 markdown files. Outcomes:
  - **66 docs had `last_verified` dates older than their git
    mtime** — bumped to 2026-05-03 in bulk so the
    "freshness checked in CI" claim from CLAUDE.md actually
    holds.
  - **10 broken cross-doc links fixed** —
    getting-started's ADR-0019 typo (`anomaly-detection-and-freeze-policy`
    → `anomaly-response-and-confidence-scoring`),
    discovery/data-sources path-depth mistakes,
    sla-proof-procedure ADR-0009 stale slug
    (`multi-window-slo-burn-rate` → `latency-budget`),
    chaos-wave1 pointing at a non-existent
    `runbooks/database-down.md`, cdn-setup forgetting the
    `infrastructure/` subdirectory, dr-activation's
    one-level-too-shallow ADR refs. **1,227 of 1,228 relative
    `.md` links now resolve** (the 1 remaining is a literal
    `<<file>>.md` template placeholder).
  - **CLAUDE.md repo tree** updated to include
    `docs/audit-2026-05-02/` (was missing).
  - **`docs/discovery/README.md`** gains an explicit
    "read-only since 2026-04-22" banner pointing at the
    Phase 1 closure doc, removing the contradiction with
    CLAUDE.md.
  - **README.md status line** refreshed to reflect r1 live +
    multi-region as the remaining launch blocker.
  - ADR statuses spot-checked: 23 Accepted, ADR-0012 explicitly
    Reserved (Quorum-set composition), no stale Proposed.
  - Customer-facing docs (`getting-started`, `api-design`,
    auto-generated `reference/config`, `reference/metrics`)
    verified clean.

### Fixed

- **`pipeline.PersistEvents` drains the channel on shutdown** —
  the sink returned immediately on `ctx.Done()`, dropping any
  events still in the 256-deep buffer. Callers (live indexer +
  `ratesengine-ops backfill`) advance their cursor AFTER
  `ProcessLedger` enqueues to the channel, BEFORE the sink
  writes — so a SIGTERM mid-stream silently lost up to
  cap(channel) trade rows per pipeline while the cursor's
  "I processed up to ledger N" claim stayed advanced. On
  `-resume`, those ledgers got skipped and their trades were
  permanently missing from the hypertable.
  Now the sink uses a fresh 30-second shutdown context to drain
  buffered events past the parent context's cancellation; if
  the deadline trips (e.g. postgres saturated), remaining
  events are dropped and the loss is logged with the buffer
  count. Three new tests (`TestPersistEvents_*`) pin the new
  behaviour.

### Added

- **`ratesengine-ops backfill -parallel N`** — backfill subcommand
  splits its `[from, to]` range into N contiguous, non-overlapping
  chunks and runs each as a concurrent worker against a shared
  postgres pool. Each chunk gets its own dispatcher + ledgerstream
  + sink + cursor row (cursor sub_source includes the chunk's
  `from-to` so concurrent chunks never share a row). Default
  remains `-parallel 1` (sequential, same shape as the
  pre-parallelism path); operators with multi-core boxes set
  `-parallel 8` (or higher) to scale throughput linearly until
  postgres `max_connections` or the galexie bucket's S3 list
  throughput becomes the bottleneck. Caught during r1 bringup
  where single-process throughput at ~50 ledgers/sec implied
  ~3.7-day ETA on the L50.4M → L62.4M historical replay; with
  `-parallel 8` the same range now ETAs in ~20 hours (verified
  on r1 at 167 ledgers/sec aggregate).

### Operations

- **r1 first application bringup — indexer + aggregator + api
  running end-to-end** — 2026-05-03 brought up the ratesengine
  application stack against r1 for the first time. Procedure
  captured in `docs/operations/r1-deployment-state.md
  §"2026-05-03 first application bringup"` so R2 + R3 follow
  the same path. Pieces:
  - Redis + TimescaleDB extension installed.
  - `ratesengine` postgres role + DB created; 15/15 migrations
    applied.
  - 3 systemd units (indexer + aggregator + api) writing
    against `/etc/default/ratesengine` for the secret env.
  - Live ingest from L62,403,000+; closed-bucket VWAP serving
    against `/v1/price?asset=native&quote=USDC:GA5Z…` end-to-end.
  - Historical backfill `L50,457,424 → L62,400,000` running in
    nohup'd background; idempotent on re-runs (trades unique
    index handles dedupe).
  - Decoder ↔ WASM verification flipped from "static-only" to
    "dynamic on real production data" — empirical evidence in
    the trades + oracle_updates hypertables.

- **Chaos Wave 1 executed against the dev stack — 3/3 passing
  (closes L5.5)** — runner walked all three documented
  scenarios (Redis-down, Timescale-down, Redis network
  partition); every graceful-degradation contract held on the
  first run with no code changes motivated. Reports +
  per-scenario logs + RETRO committed under
  `test/chaos/reports/2026-05-03-launch-cut/`. L5.5 flipped
  🟢 → ✅. Wave 2 (HA-shaped scenarios) stays post-launch and
  feeds into L5.8 once R2/R3 are provisioned.

### Fixed

- **Migration 0005 unique index now includes the partition column** —
  `asset_supply_history`'s `UNIQUE INDEX (asset_key, ledger_sequence)`
  was rejected by TimescaleDB at apply time with `cannot create a
  unique index without the column "time" (used in partitioning)`.
  Adding `time` as a tail key makes the migration apply cleanly;
  the (asset_key, ledger_sequence) uniqueness invariant stays
  intact in practice because two writes for the same (asset,
  ledger) derive the same `time` from the ledger close. Caught
  during the r1 first-time bringup; the migration set has now
  been applied end-to-end on r1 (15 of 15).

- **Aggregator metrics endpoint auto-shifts off the indexer's
  default port on single-host deploys** — both binaries default
  `obs.metrics_listen` to `127.0.0.1:9464`, so a single-host
  deploy with both running silently lost the aggregator's
  `/metrics` endpoint to "address already in use" (the binary
  stayed up but the aggregator-silent / outlier-storm /
  class-drop-spike alerts had nothing to scrape). The aggregator
  now detects the collision and shifts itself to `127.0.0.1:9465`
  with an INFO log line explaining the shift. Operators on
  multi-host deploys override `obs.metrics_listen` per-host and
  never hit the shift; operators on single-host deploys get
  working metrics out of the box.

### Documentation

- **Multi-bar chart TWAP officially deferred to L7.8** —
  `/v1/chart?price_type=twap` continues to return 400, but the
  message + OpenAPI description + ADR-0020 now explicitly point
  at the post-launch tracker (L7.8 in
  `docs/architecture/launch-readiness-backlog.md`). Single-bar
  TWAP via `/v1/twap` remains shipped (true time-weighted compute
  from raw trades); only the multi-bar chart variant is the
  deferred surface. Per the Stellar + Freighter RFPs the chart
  may be backed by "TWAP **or** VWAP" (either acceptable); the
  proposal's "configurable VWAP and TWAP aggregation engine"
  commitment is satisfied via `/v1/twap` + the VWAP→TWAP
  fallback in S4.4. Reopen L7.8 if a customer asks for
  TWAP-shaped charts.

- **Day-1 contract truth pass on placeholder surfaces** — three
  endpoint godocs sharpened so SDK consumers don't mistake
  reserved fields for shipped behaviour:
  - `/v1/account/usage` — handler godoc explicitly notes the
    endpoint always returns `[]`; `?from=` / `?to=` query params
    are reserved in OpenAPI but ignored. Wire shape locked,
    rollup worker post-launch.
  - `/v1/assets` — handler godoc spells out that
    `type=`/`code=`/`issuer=` filter params are accepted by the
    parser but never applied (returns the unfiltered cursor
    page). Operators needing filtering today walk the cursor
    and filter client-side.
  - `APIKeyRecord.Scopes` — field godoc explicitly flags the
    day-1 launch posture: scopes are stored but **not enforced**
    at any runtime endpoint. Setting them is forward-compat
    only; relying on them for access control is a footgun.
- **`docs/architecture/launch-readiness-backlog.md` deduped** —
  union-merge artefacts from the May-3 marathon merge left
  three copies of L6.1/L6.2/L6.3, three of L5.4/L5.5, two of
  L5.7/L6.4/L3.14/L3.15/L3.16. Kept the longest (most-current)
  annotation per row; the file is now 71 unique row IDs (down
  from 86 with duplicates).
- **`docs/getting-started.md`** — status page line gains the
  same "(post-launch)" qualifier the API endpoint already had,
  plus a pointer to L4.11. Brings the doc in line with
  `sev-playbook.md §5.1` which already noted the page isn't
  provisioned yet.

### Added

- **R2 + R3 spinup tracked as launch-blocking** — five new
  rows added to `docs/architecture/launch-readiness-backlog.md`
  to close the gap where the multi-region topology was
  designed (ADR-0016 ratified) and tooled (`r2.example.yml`,
  `r3.example.yml`, all ansible roles) but the actual
  per-region deployment + DNS + replication wiring was
  invisible to the launch-readiness accounting:
  - **L4.14** R2 (AWS us-east-1) provisioning + bringup —
    EC2 + EBS + galexie reads `aws-public-blockchain` direct.
  - **L4.15** R3 (Vultr Singapore) provisioning + bringup —
    Vultr Bare Metal + Vultr Object Storage hybrid.
  - **L4.16** Cloudflare Anycast + GeoIP routing for
    `api.ratesengine.net`.
  - **L4.17** Cross-region Postgres replication wired
    (sync R1→R2, async R1→R3).
  - **L5.8** Region-failover chaos test — kill R1, verify
    R2/R3 keep serving with `flags.stale=true` honesty during
    the failover gap.

### Fixed

- **`docs/architecture/infrastructure/multi-region-topology.md`
  region naming aligned with ADR-0016**. The doc was drafted
  pre-ADR-0016 with placeholder regions (`London / Equinix
  LD6`, `Ashburn / Equinix DC11`, `Singapore / Equinix SG3`);
  ADR-0016 settled on `Hetzner FSN1 / AWS us-east-1 / Vultr
  Singapore` with three different storage shapes per region.
  Updated the regional-choice table, ASCII topology diagram,
  and rollout sequence narrative to match. Frontmatter
  flipped from `draft` to `ratified`; `last_verified` bumped.
- **Launch-day operator helpers** — two pre-baked artefacts that
  remove decision-load on the day:
  - [`deploy/status-page/upptimerc.example.yml`](deploy/status-page/upptimerc.example.yml)
    — drop-in `.upptimerc.yml` for the Upptime fork. Names the
    surfaces (API + readiness + SSE smoke + docs + r1/r2/r3
    origins), configures the public-page intro, routes incident
    assignment. Operator copies to the new `ratesengine-status`
    repo + tweaks per the inline comments. Companion
    [`deploy/status-page/README.md`](deploy/status-page/README.md)
    points back at `docs/operations/status-page-setup.md` for
    the full procedure.
  - [`scripts/dev/verify-cdn.sh`](scripts/dev/verify-cdn.sh)
    — runs the post-CDN-provisioning smoke checks from
    `docs/operations/cdn-setup.md` against a live host. Six
    checks: historical-surface s-maxage, hot-surface short
    max-age, auth-surface no-store + edge-bypass, SSE Content-
    Type + no-store, health 200, sources catalogue max-age=300.
    Exit 0 = pass; exit 1 = at least one failure.
- **Launch-day operator toolkit** — three runbooks that
  collapse cutover-day decision-load:
  - [`docs/operations/launch-day-checklist.md`](docs/operations/launch-day-checklist.md)
    — T-7 / T-3 / T-1 / T-0 stages with per-step pass
    conditions. Orchestrates every other operator runbook
    (release-process, public-flip, CDN, status-page,
    chaos-Wave1, SLA probe). On-call follows top-to-bottom
    on the day.
  - [`docs/operations/rollback.md`](docs/operations/rollback.md)
    — failure-mode triage (release-won't-start, broken
    correctness, single-source failure, public-flip
    botched, status-page misfiring) with explicit
    rollback commands per case + post-rollback flow
    (SEV file, comms, postmortem, freeze-forward).
  - [`docs/operations/postmortems/_template.md`](docs/operations/postmortems/_template.md)
    — postmortem template the rollback runbook references.
    Frontmatter + TL;DR + Impact + Timeline + Root cause
    + What-went-well/poorly + Lucky-on + Action items +
    Lessons. Drafted-by-template so future-us doesn't
    re-derive the structure mid-incident.
- **Three operator runbooks for the launch-readiness rows that
  need infra-side action, not code:**
  - [`docs/operations/cdn-setup.md`](docs/operations/cdn-setup.md)
    — closes **L3.14**'s infra side. Covers per-surface
    `Cache-Control` policy from the origin middleware, provider
    triage (Cloudflare vs CloudFront vs Bunny), step-by-step
    Cloudflare provisioning, SSE-passthrough config, verification
    `curl` commands, and a one-line rollback path.
  - [`docs/operations/status-page-setup.md`](docs/operations/status-page-setup.md)
    — closes **L4.11**'s decision + provisioning. Decision:
    **Upptime** on GitHub Pages (host-independent of our origin
    AND auto-monitored — GitHub Actions probes every 5 min,
    auto-creates incident issues on probe failure, auto-resolves
    on recovery). Removes the on-call "must remember to post"
    failure mode that a static page like cstate has. Full setup
    walkthrough plus manual incident-posting via labelled GitHub
    issues for incidents Upptime can't see (correctness bugs,
    regional outages from non-GitHub viewpoints, maintenance
    windows). We can graduate to a custom solution post-launch
    if customer feedback wants tighter brand integration — the
    URL stays `status.ratesengine.net`, only the backend swaps.
  - [`docs/operations/chaos-wave1-runbook.md`](docs/operations/chaos-wave1-runbook.md)
    — closes **L5.5**'s execution gap. The suite code is already
    shipped under `test/chaos/`; the runbook covers the pre-flight,
    pass criteria per scenario, what to capture per run (the
    reports directory + RETRO), and what to do when something
    breaks. The launch-blocking artefact is "a clean run + a
    committed reports directory", not more code.
- **Multi-region cutover scaffolding** — three operator-friction
  reducers for the L4.14 / L4.15 / L4.16 / L4.17 / L5.8 work
  added in PR #531:
  - [`docs/operations/multi-region-cutover.md`](docs/operations/multi-region-cutover.md)
    — sequenced runbook that orchestrates all five rows in
    order with pass conditions per stage (R2 spinup → R3
    spinup → cross-region pg replication verify → Cloudflare
    Anycast/GeoIP → region-failover chaos test). Fail at any
    stage routes to `rollback.md`'s matching shape.
  - [`scripts/dev/verify-cross-region.sh`](scripts/dev/verify-cross-region.sh)
    — automated cross-region consistency check. Hits
    `/v1/price` from each region, asserts byte-identical
    `data.price` per ADR-0015 closed-bucket consistency.
    Exit 0 = consistent; exit 1 = divergence (ADR-0015
    contract broken); exit 2 = at least one region
    unreachable (incomplete check). Pure bash 3.2+
    compatible (works on macOS).
  - [`docs/operations/r2-deployment-state.md`](docs/operations/r2-deployment-state.md)
    + [`docs/operations/r3-deployment-state.md`](docs/operations/r3-deployment-state.md)
    — skeleton deployment-state docs that mirror
    `r1-deployment-state.md`'s shape with `{{TBD}}`
    placeholders for the operator to fill in post-provision.
    Lets a future reader compare per-region differences at
    a glance and gives the operator a structured place to
    record what they actually deployed (vs what ADR-0016
    + multi-region-topology.md prescribed).
- **Three pre-launch helpers — operator + customer-facing
  scaffolds for "the questions that get googled during launch
  week"**:
  - [`docs/operations/post-launch-queries.md`](docs/operations/post-launch-queries.md)
    — 12-query PromQL bundle the on-call types into Grafana
    during the L6.7 first-24h watch (request rate per surface,
    error rate, p95/p99 latency, oracle freshness, source
    events rate, aggregator tick health, decode errors, rate-
    limit fail-open, closed-bucket stream subscriber health,
    trade-insert USD-volume populate ratio). Each query has an
    expected-shape annotation so anomalies are spottable
    without re-deriving the metric semantics.
  - [`docs/operations/backfill-procedure.md`](docs/operations/backfill-procedure.md)
    — operator runbook for `ratesengine-ops backfill`.
    Covers when to use it (newly-enabled source, discovered
    gap, region catch-up, post-WASM-audit replay), step-by-
    step (range pick → dry-run → run → resume → narrow-source
    → verify), and four named failure modes (`BackfillSafe=
    false`, cursor collision, archive-missing, when-not-to-
    use). CAGGs auto-materialise on inserted rows; the doc
    flags the `refresh_continuous_aggregate` rescue if
    needed.
  - [`pkg/client/example_test.go`](pkg/client/example_test.go)
    — extended with three more runnable examples
    (`ExampleClient_HistorySinceInception`,
    `ExampleClient_Assets`, `ExampleClient_Me`) so the SDK's
    `go doc -all` output now covers all four core
    customer-facing methods in addition to the existing
    `ExampleNew` / `ExampleClient_Price` /
    `ExampleClient_Asset` / `ExampleAPIError`. Doubles as a
    build-time smoke test for the SDK type shapes.
- **Customer-comms templates + demo script for the launch
  sprint.** Pre-baked artefacts so drafting under stress is
  never the path:
  - [`deploy/comms/`](deploy/comms/) — five templates with
    `{{...}}` placeholders covering every customer-facing
    moment: launch-announcement, first-customer onboarding-
    email, mid-incident incident-update, pre-cut
    maintenance-window heads-up, post-rollback rollback-
    update. README.md indexes them with usage notes (which
    channel, which placeholders) + a comms-log convention
    so every send becomes an auditable record.
  - [`docs/operations/customer-demo-script.md`](docs/operations/customer-demo-script.md)
    — pre-flight + 9-stage walk-through covering every public
    surface (closed-bucket pricing → tip → observations →
    history → SSE → asset detail → SDK) plus expected-Q&A.
    Customer leaves able to make their first real request
    unaided. Closes L6.6's pre-launch deliverable side; the
    🔴 status flips ✅ when the customer signs off.
- **`make verify-launch-ready` — single-pane status check on the
  launch-readiness backlog**. New
  `scripts/ci/verify-launch-ready/main.go` parses
  `docs/architecture/launch-readiness-backlog.md` and reports
  three readiness tiers: **engineering** (L1-L3, must be
  ✅/⚠), **ops + validation** (L4-L5, must be ✅/⚠/🟡 —
  operator-runbook-ready acceptable), and **cutover** (L6,
  operator-action-only on launch day, reported but not gating).
  L7 post-launch is reported but ignored. Exit 0 if all
  engineering tiers ready; exit 1 with per-blocker detail if
  not. `make verify-launch-ready-all` adds a full per-row
  listing. Tested against the real backlog file + synthetic
  inputs covering tier-specific readiness rules.
- **L3.9 PR 2 of 2: API-side closed-bucket stream subscriber.**
  Closes the L3.9 fan-out end-to-end. New
  `redispub.Subscriber` listens on the same Redis channel the
  aggregator's Publisher writes to (PR 1 of L3.9), decodes each
  `ClosedBucketEvent`, and republishes on the API binary's
  in-process `streaming.Hub` with the canonical
  `closed:<asset>/<quote>` topic key (matches
  `internal/api/v1.PriceStreamTopic`). `cmd/ratesengine-api/main.go`
  constructs a Hub when Redis is available and runs the
  subscriber as a goroutine bound to the root context.
  - New metric
    `ratesengine_api_stream_subscribe_total{outcome="ok"|"decode_error"|"malformed"}`.
  - New tests: nil-input rejection; round-trip via miniredis
    that proves Hub.Publish fires with the correct topic and
    forwarded payload; sentinel test asserts the topic format
    stays in sync with `v1.PriceStreamTopic`.
  - L3.9 in launch-readiness-backlog flipped ⚠ → ✅; the
    documented caveat ("aggregator-side `Hub.Publish` is the
    missing piece") is closed.
- **L3.9 PR 1 of 2: aggregator-side closed-bucket stream
  publisher**. New `orchestrator.StreamPublisher` interface
  declared on `orchestrator.Config`; called once per
  successful (pair, window) VWAP cache write with the freshly-
  computed value + bucket-end timestamp. Best-effort:
  publish errors log + increment
  `ratesengine_aggregator_stream_publish_total{outcome="error"}`
  but never block the tick (the VWAP cache key is the
  source of truth; the stream is enrichment for SSE
  subscribers).
  - Production implementation: new package
    `internal/api/streaming/redispub/` with `Publisher`
    (Redis `PUBLISH` to `ratesengine:closed-bucket:v1`) +
    `ClosedBucketEvent` JSON wire shape.
  - Wired in `cmd/ratesengine-aggregator/main.go` —
    PUBLISH on a no-subscriber channel is a Redis no-op,
    so wiring is safe ahead of the matching API-side
    subscriber.
  - PR 2 of L3.9 will add the API-binary subscriber that
    republishes each event on the in-process
    `streaming.Hub` so `/v1/price/stream` SSE clients
    receive the fan-out.
- **`change_24h_pct` populated on `/v1/assets/{id}`** — the field
  was declared in OpenAPI (Freighter RFP §"Bulk query support"
  mentions a 24h % change alongside current price) but no Go code
  computed it. Closed: `internal/storage/timescale/aggregates.go`
  gains `ClosedVWAP1mAtOrBefore` to anchor the 24h-ago comparison
  price; new `Change24hReader` interface + `populateChange24h`
  helper in `internal/api/v1/assets_f2.go` consult the current
  USD price + 24h-ago anchor and stamp a signed two-decimal
  percentage (e.g. `"+1.27"`, `"-0.05"`, `"0.00"`). The leading
  `+` is suppressed on a sub-cent positive delta that rounds to
  `"0.00"` so the wire signal stays unambiguous. Null when no
  current USD price exists for the asset or the 24h-ago bucket
  is unavailable (asset first traded < 24h ago, or pruned by
  retention). `pkg/client/types.go::AssetDetail` gains the field;
  `cmd/ratesengine-api/main.go` constructs `storeChange24hReader`
  and wires it via `Options.Change24h`.
- **`/v1/price/stream` now serves closed-bucket events end-to-end**
  — the handler returned 503 unconditionally because the API
  binary never constructed a `streaming.Hub`, and no producer
  ever called `Hub.Publish`. Closed: `cmd/ratesengine-api/main.go`
  unconditionally constructs `streaming.NewHub(0)` and passes it
  via `Options.Hub`; new `internal/api/streampublish` package
  hosts a per-pair polling producer that watches the existing
  `PriceReader` (same path `/v1/price` consumes) and fans out to
  the Hub on every `ObservedAt` advance. Operators declare which
  pairs broadcast via the new `[api.streaming]` config block:
  `pairs = [["native","fiat:USD"], …]`. Empty `pairs` leaves the
  producer disabled but still constructs the Hub so subscribers
  connect cleanly (heartbeats only). New
  `ratesengine_stream_publish_total{stream="price_stream"}`
  counter signals fanout activity. The byte-identical-payload
  property required by ADR-0015 is verified by
  `TestPublisher_TwoSubscribersIdenticalPayload`.
- **L2.2 Phase 2 plumbing — `USDVolumeFXResolver` interface +
  `tradeUSDVolume` fallback path** — closes the launch-task-list
  G3 plumbing half. The current Phase 1 path stamps `usd_volume`
  for off-chain CEX/FX trades + on-chain DEX trades whose quote
  is on the operator's USD-pegged classics allow-list, leaving
  every other on-chain trade NULL. New
  `USDVolumeFXResolver.USDPriceAt(ctx, asset, t)` lets a
  deployment supply a USD rate per quote asset; when wired,
  `tradeUSDVolume` falls through to it after Phase 1 declines
  and multiplies through `quote_amount × rate / 10^classicDecimals`
  to land a non-NULL `usd_volume`. `Store.SetUSDVolumeFXResolver`
  installs it; nil (the default) preserves Phase 1 behaviour
  exactly. Production resolver — a goroutine that polls
  `prices_1m` for `<asset>/<USD>` per configured asset and
  caches the latest closed VWAP — ships in a follow-up PR; this
  PR is the contract + test surface so the wiring lands cleanly.
- **`pkg/client.Client.History`** — bounded-range raw-trade lookup
  via the SDK. Distinct from the existing
  `Client.HistorySinceInception` (which returns bucketed VWAP/TWAP
  points); this surface returns the underlying `TradeRow`
  records — useful for trade-level audits, regulatory exports,
  custom aggregations the server doesn't pre-compute. New
  `HistoryRangeQuery` with optional `From`/`To`/`Limit`/`Cursor`;
  `Cursor` walks forward by re-issuing with the previous
  response's `Pagination.Next`. New `TradeRow` type in
  `pkg/client/types.go` mirrors the server's wire shape exactly.
- **`pkg/client.Client.OHLC`** — single-bar OHLC over a window via
  the SDK. Closes another gap from the code-vs-RFP audit:
  Freighter RFP §V1 historical chart requirements explicitly list
  OHLC as a chart-UX path but the SDK only exposed
  `HistorySinceInception`. Both `Base` and `Quote` are required
  on `OHLCQuery` (the server doesn't default Quote to fiat:USD —
  candlestick charts pin a specific pair). `From`/`To` are
  optional with the same closed-bucket-clamp semantics the server
  applies to a defaulted `to` per ADR-0015. Wire shape mirrors
  the server's `OHLCBar` exactly, including the `Truncated` flag
  consumers building chart UIs need to detect when a window has
  more trades than the server's per-request cap.
- **`pkg/client.Client.PriceTip`** — live rolling-window VWAP via
  the SDK. Sibling to `Client.Price` for "freshest possible
  signal" use cases per ADR-0018. Same input shape as `PriceQuery`
  with an additional `WindowSeconds` (server clamps to [1, 60],
  defaults to 5). Caller distinguishes the two in-contract
  response branches via `PriceSnapshot.PriceType`: `"vwap"` for
  the rolling-window VWAP, `"last_trade"` for the empty-window
  fallback. SDK omits `window_seconds=0` from the URL so the
  default-of-5 path stays clean.
- **`pkg/client.Client.PriceBatch`** — bulk price lookup via the
  Go SDK. Closes the most impactful gap from a code-vs-RFP audit
  of the SDK surface: Freighter RFP §"Bulk query support
  preferred (batch asset lookups)" was implemented server-side
  (`GET`/`POST /v1/price/batch`) but the SDK only exposed the
  single-asset `Client.Price`. SDK now routes ≤100 ids via GET
  and >100 via POST automatically (the threshold below which the
  query string fits within typical 8 KiB header limits), validates
  ≤1000 client-side to match the server cap, and returns the
  same `Envelope[[]PriceSnapshot]` shape with OR-over-rows flags.
  Splitting beyond 1000 is deliberately the caller's choice —
  silently chunking would mask `flags.stale` semantics on
  subsets the caller wouldn't see.
- **`runbooks/dr-activation.md` — disaster-recovery activation
  procedure** — closes the missing runbook the SEV playbook §8.3
  (annual DR exercise), `timescale-primary-down.md` §D
  ("complete cluster loss"), and ADR-0008 / ADR-0016 all
  referenced. Previously the only pointer was `TODO(#0)` in
  `timescale-primary-down.md`. Covers when to activate (decision
  tree distinguishing it from per-component HA failover),
  pre-flight checks (DR storage freshness, MinIO archive
  integrity, host reachability), the Cloudflare-LB and manual-
  DNS flip procedures, post-flip monitoring (SLA + ingest +
  flag rates), failback to primary, escalation, and quarterly
  drift signals operators run between drills. SEV playbook §8.3
  + the timescale runbook updated to link the new file.
- **Two new SEV drill scenarios** — `sev2-redis-sentinel-failover`
  exercises ADR-0024's Sentinel HA path end-to-end across every
  Redis-dependent surface (`/v1/price` cache + freeze markers +
  confidence + triangulation + API-key validator + SEP-1 cache);
  pinned validation criteria include "did oncall correctly
  classify SEV-2 (degraded) not SEV-1 (down)" and "did anyone
  fail back contrary to ADR-0024's fail-forward rule" — both
  common simulation mis-steps. `sev1-anomaly-freeze-stuck`
  exercises the ADR-0019 anomaly chain (Phase 1 thresholds →
  Phase 2 baseline → freeze.Writer → /v1/price's flags.frozen);
  drills the operator-driven-clear contract that ADR-0019
  Phase 1 explicitly chose over auto-clear, plus the verify-
  before-clearing discipline that prevents re-freeze loops.
  Drills README updated to list all four scenarios with their
  category coverage (storage / cache / ingest / aggregator).
  Closes G5 in `docs/launch-task-list.md` for the script-
  authoring half; actual drill execution + writeups remain
  operator work against staging.
- **Status-page scaffold + `sev-status-page-update` runbook** —
  `status.ratesengine.net` was committed to in the proposal §IDR
  and required by Freighter F3.5 / F3.6, but nothing in `deploy/`
  pointed at the page or specified what an update should look
  like. New `deploy/status-page/cstate/` ships the cstate
  (Hugo-based) site config, the public component list (12
  customer-facing service surfaces matching the API + ingest +
  backend layers), and the per-incident front-matter template.
  New `docs/operations/runbooks/sev-status-page-update.md`
  binds the update cadence (hourly during SEV-1, daily during
  SEV-2 — matches the SEV-playbook + Freighter SLA), the
  safe-to-publish detail level, and the workstation-down
  fallback path. `docs/operations/sev-playbook.md` §5.1 now
  references both rather than dangling a TBD. Hosting target
  (Cloudflare Pages recommended) + DNS cutover remain operator
  work — see [`deploy/status-page/README.md`](../deploy/status-page/README.md).
  Closes G4 in `docs/launch-task-list.md`.
- **AlertManager Discord webhook (parallel fanout with Slack)** —
  the proposal commits to alerts being "integrated into
  discord/slack" but the Prometheus ansible role only wired
  Slack. New `alertmanager_discord_webhook_url` vault var; the
  warning + info routes now point at a unified `chat-fanout`
  receiver that emits to BOTH Slack and Discord when their
  respective webhook URLs are set, either alone, or neither
  (alerts accumulate in the AM UI in the last case). Preflight
  warns when both URLs are empty rather than silently letting
  alerts fall on the floor. Closes G7 in
  `docs/launch-task-list.md`.
### Documentation

- **Public-flip 24-hour pre-cutover dry-run (closes L6.3 / Task #78)** —
  `docs/operations/public-flip.md` gains a §"Final 24-hour
  pre-cutover dry-run" capturing the gates that must re-run in
  the 24 h immediately before tagging v1.0: gitleaks rerun,
  file-level scrub recheck, `make test && make test-integration`
  on the v1.0 SHA, doc-rot spot-check on `last_verified` dates,
  CI-green-within-24h check, and external-asset readiness
  (SECURITY mailbox monitored, CODEOWNERS bandwidth, GitHub
  repo name still un-claimed). The pre-flip checklist itself is
  already `☑` × 16 — this addition closes the "what about the
  PRs that landed between standing-checklist verification and
  launch day" gap. L6.3 status flipped 🟢 → ✅.

- **SLA proof procedure (Task #77 operator-recipe)** — new
  `docs/operations/sla-proof-procedure.md` documents the
  end-to-end recipe that turns a `make test-load-mixed` run into
  the checked-in `docs/operations/sla-proof-<YYYY-MM-DD>.md`
  proof artefact: pre-flight checklist, run command, Grafana
  snapshot capture, Promql baseline reads against the soak
  window, monthly cadence, and the documented-acceptance
  fallback if staging access is delayed. The existing template
  at `sla-proof-template.md` is the report skeleton; this
  procedure is the operator's how-to. Closes the "no operator
  recipe to produce the proof report" gap that left Task #77
  without a clear path-to-done even though all upstream
  scenarios (L5.1-L5.3) had already shipped.

- **SEV-1 / SEV-2 dry-run records (closes L5.7 / Task #76)** —
  Two new tabletop drill writeups under `docs/operations/drills/`
  exercise the SEV playbook end-to-end against the existing
  scripted scenarios:
  - `2026-04-sev1-timescale-failover.md` — Timescale primary
    out-of-disk simulation; chose fix-in-place via
    `drop_chunks('prices_1m', '30 days')` plus restart;
    validated all 8 scenario criteria, 7 pass + 1 partial.
  - `2026-04-sev2-soroswap-decode-regression.md` — protocol-25
    SCVal type-tag enum extension breaks soroswap decoder;
    forward-fix path via `internal/scval` + golden fixture
    + ordinary deploy + `ratesengine-ops backfill -source`;
    validated all 8 scenario criteria, all pass.
  - Promoted two action items into runbook updates in the same
    PR: `timescale-primary-down.md` Quick-diagnosis now leads
    with `/v1/readyz` (shaves ~1 min off detection); `decode-errors.md`
    Mitigation gains a customer-comms note for the
    `class_drop_spike` ↔ `flags.divergence_warning` correlation.
  - Solo-drill caveats called out explicitly — a 3-person tabletop
    is queued for post-launch with the next on-call hire.

- **WASM-audit v2 fill-in across all eight Soroban sources** —
  every per-source audit doc under `docs/operations/wasm-audits/`
  now folds in the 2026-04-30 r1 wide-net walk's per-instance
  evidence (540 contracts / 52 unique WASMs SHA-256-verified +
  bytes-preserved on r1). Notable changes:
  - **Comet's v2 audit folded into Blend's** — the only mainnet
    Comet pool is Blend's Backstop V2 (`CAQQR5SW…` →
    `c1f4502a…`). `comet.md` now redirects to `blend.md` for the
    per-instance hash inventory; `blend.md` documents both source
    rows symmetrically. Comet (the protocol) is a Balancer-v1-style
    AMM library used by Blend's backstop module — not an actively-
    maintained standalone DEX.
  - **Aquarius gained Cohort A / Cohort B sections** — 168 never-
    upgraded pools (3 WASMs) plus 145 upgraded pools across a
    5-WASM upgrade chain (`b54ba37b → 2d770946 → 7cecf23b →
    a1629dcd → 4f080d24`). Closes the "doc incomplete, not wrong"
    gap flagged in the 2026-05-01 cross-source review.
  - **Soroswap gained per-instance Phase 2 results** — 196
    contracts (1 factory + 1 router + 194 pair instances), three
    unique WASMs total, zero mid-life upgrades observed.
  - **Phoenix gained per-instance Phase 2 results** — 13
    contracts on 22 WASMs (5 factory + 3 multihop + 14 pool); the
    most-iterated source. All 14 pool WASMs binary-confirmed to
    contain the eight swap-field strings (`actual received amount`
    spelling preserved across the chain).
  - **Reflector / Redstone / Band** confirmation notes added
    pinning the v2 walk's findings; no decoder-relevant changes.
  - All `last_verified` dates bumped to 2026-05-03.

### Fixed

- **`internal/auth/sep10.go` reflects the shipped validator** —
  the SEP10Validator interface godoc said "Production
  implementation lands in Phase 5; current [NoopSEP10Validator]
  returns [ErrNotImplemented] from every method", and
  NoopSEP10Validator was described as the "placeholder used
  when auth_mode=sep10 is configured but no validator
  implementation is wired". Both are stale: the production
  validator lives in `internal/auth/sep10` (separate package),
  `cmd/ratesengine-api` wires it via `sep10.NewValidator`, and
  the binary's actual fallback rule is "swap in Noop iff
  config is missing AND `auth_mode` is not `sep10`; otherwise
  hard-fail at startup." Both godocs rewritten to describe the
  real wiring.
- **L6.1 / L6.2 / L6.3 finalisation final-pass** — the three
  finalisation rows were 🟢 with "drafts shipped, need final
  pass". Walked each artefact (`CHANGELOG.md`,
  `docs/architecture/semver-policy.md`,
  `.github/RELEASE_NOTES_TEMPLATE.md`,
  `docs/operations/release-process.md`,
  `docs/operations/public-flip.md`) end-to-end. Single concrete
  drift found + fixed: `semver-policy.md` cited a
  `make verify-tag <tag>` target that doesn't exist (and that
  `release-process.md` doesn't actually call); replaced the
  paragraph with a manual pre-tag checklist that
  `release-process.md`'s pre-flight already covers. Each row's
  description in the launch-readiness backlog is now expanded
  to point at what the artefact actually contains, then flipped
  to ✅. (Other minor drifts in the same files — phantom
  `pkg/types`, wrong `internal/anomaly` path in semver-policy,
  ADR range `0001-0021` in public-flip — are already addressed
  in open PRs #515 and #497 respectively.)
- **Launch-readiness backlog: five 🟢 rows flipped to ✅ after
  audit found pure status drift** (no code changes, no
  remediation needed; just walking each row against the
  shipped state):
  - **L3.5** F2 asset-detail fields — `applyF2Fields` populates
    all six F2 fields end-to-end on `/v1/assets/{id}`.
    `change_24h_pct` moved to **L7.7** post-launch; the row
    description always called this out as deferred-by-design,
    just hadn't moved into L7 explicitly.
  - **L3.11** Redocly + GitHub Pages workflow + drift guard
    — all three deliverables live (`scripts/dev/docs-api.sh`,
    `.github/workflows/api-docs.yml`, `ci.yml` drift check).
  - **L3.14** CDN cache-control middleware — origin-side
    middleware ships with tests; the remaining work is
    infra-side (CloudFront/equivalent provisioning), tracked
    separately in the operator runbook.
  - **L3.15** getting-started page — `docs/getting-started.md`
    ships at 205 lines.
  - **L3.16** OpenAPI URL-discipline lint —
    `scripts/ci/lint-openapi-urls/` ships with tests, real-spec
    sentinel, and three CI hooks (verify.sh, ci.yml, Makefile).
- **Launch-readiness backlog: three caveats reclassified ⚠ → ✅
  with sharper language**:
  - **L2.2** `usd_volume`: the row's "off-chain only" framing
    misrepresented coverage. `tradeUSDVolume` covers BOTH
    off-chain (CEX/FX) AND on-chain (DEX with operator-declared
    classic USD-pegs + their SAC wrappers). Today this means
    USDC/USDT/EURC/EURB/MXNe/PYUSD — every classic-form
    stablecoin currently traded on Stellar — populate
    `usd_volume` correctly on Soroswap/Phoenix/Aquarius/SDEX.
    The pure-SEP-41 (Soroban-native, no classic backer) case
    is empty on mainnet today; moved to L7.6 (post-launch).
  - **L3.1** `/v1/price` end-to-end: the "CAGG-fill" caveat
    described an operational dependency (running the
    aggregator binary against production data), not a code
    gap. CAGGs auto-refresh per the
    `add_continuous_aggregate_policy` calls in
    `migrations/0002_create_price_aggregates.up.sql`. Closes
    naturally at L6.4 cutover.
  - **L5.4** `ingest_peak_ledger.js` k6 scenario: documented
    acceptance — the mixed-realistic scenario
    (`06-mixed-realistic.js`) covers the indexer's load shape
    alongside API load. A dedicated indexer-only scenario is
    a post-launch nice-to-have for isolated saturation-finding,
    not launch-blocking.
- **`sev-playbook.md` §5.1 status-page section is no longer a
  Week-N stub** — the doc said `Public status page lives at
  https://status.ratesengine.net (TBD — provisioning in Week
  8).` Reality: the cstate scaffold ships at
  `deploy/status-page/cstate/`; provisioning at the public
  domain is gated on L4.11 in the launch-readiness backlog.
  Section now describes what's committed (the scaffold) vs
  what's gated (the public hostname), and points at the
  in-flight `sev-status-page-update.md` runbook for the operator
  edit-surface during incidents. Continuation of the L6.5
  doc-sweep.
- **Architecture docs no longer claim r1 is in London or that R2/R3
  live at Equinix** — the design-stage docs (`ha-plan.md`,
  `multi-region-topology.md`, `validator-rollout.md`,
  `hosting-options.md`) tentatively listed Equinix Metal across all
  three regions (LD6 / DC11 / SG3) before the per-region cost
  analysis settled the per-region provider mix. ADR-0016 ratifies
  the actual shape: R1 = Hetzner FSN1 (Falkenstein, DE), R2 = AWS
  us-east-1, R3 = Vultr Singapore — not Equinix anywhere. r1 is
  live on Hetzner FSN1 per `r1-deployment-state.md`. An operator
  reading the design docs cold today would look for a "London"
  region that doesn't exist. Topology table, ASCII diagram, rollout
  phase headers, and validator phase headers all updated to match
  the as-deployed assignment. Continuation of the L6.5 doc-sweep.
- **`baseline.MultiBaseline.MaxZScore` no longer silently bypasses
  freeze on pathological observations** — when called with a NaN
  observation, the function returned `(z=NaN, valid=true)`, and
  the orchestrator's Phase 2 freeze check (`z > 5.0`) silently
  evaluated false because IEEE-754 NaN comparisons return false.
  Result: a NaN price slipping through (e.g. `(Inf - prev) / prev`
  from a `big.Rat.Float64()` overflow upstream) would NOT trigger
  the freeze it should have. Fixed by detecting pathological
  inputs (NaN / ±Inf) at the function boundary and returning
  `(+Inf, smallest-available-window, true)` so downstream
  threshold checks correctly fire on what is, by definition,
  the most-anomalous possible observation. Four new tests cover
  NaN, +Inf, -Inf, and the 30d-only attribution edge case.
- **2026-05-02 audit finding F-0501 closed**:
  `deploy/monitoring/README.md` claimed *"CI does NOT
  currently run `promtool check rules` or `promtool test
  rules`"*, but `.github/workflows/ci.yml` line 108 has
  a `monitoring-rules` job that installs `promtool` from the
  official Prometheus release and runs
  `make monitoring-check` on every PR (rule-syntax errors
  fail the PR). Rewrote the README to describe the actual
  CI control and to keep the rule-firing-unit-test gap
  acknowledged precisely (no `test/monitoring/` tree yet;
  `promtool test rules` is a future follow-up if rule logic
  ever grows complex enough to need behavioural tests).
  Audit register + remediation plan updated to reflect
  closure.
- **`VERSIONS.md` "Runtime binaries" list reflects the
  2026-04-23 r1 trim.** The list still claimed `stellar-core`
  and `stellar-rpc` were runtime binaries on the production
  host. Both were REMOVED from r1 on 2026-04-23 (per
  `docs/operations/r1-deployment-state.md` §"Architecture
  after 2026-04-23 trim"). Updated to:
  - **Kept**: `stellar-galexie` (now embeds the only
    captive-core on the box) + `rs-stellar-archivist`.
  - **Removed**: `stellar-core` standalone daemon (kept
    inside Galexie as captive); `stellar-rpc` source removed,
    binary retained only for the `ratesengine-ops rpc-probe`
    operator diagnostic that dials remote public endpoints.
- **`ratesengine-ops supply snapshot -asset <non-native>` error
  message no longer claims classic + SEP-41 computers are
  unshipped.** The error said *"classic + SEP-41 follow once
  their computers ship"*, contradicting both the docstring on
  the same function (lines 38-44) AND `internal/supply/{classic,
  sep41}.go` which actually ship them. Rewrote the error to say
  what's actually true: those algorithms are served by the
  aggregator-resident goroutine path (`[supply]
  aggregator_refresh_enabled`), not this CLI subcommand.
  Pointed at `docs/operations/supply-snapshot.md §"Asset-class
  scope"` for the full split. Same fix on the `-asset` flag
  help text in the function docstring.
- **`coverage-matrix.md` Blend audit caveat closed** — Claim 5
  said the Blend WASM audit Phase 2 was pending, keeping
  `BackfillSafe=false` in `internal/sources/external/registry.go`.
  The audit completed 2026-05-02 (11 contracts, 3 unique
  WASMs, no mid-life upgrades; documented under
  `docs/operations/wasm-audits/blend.md §"Phase 2 results"`)
  and `BackfillSafe: true` is now set in registry.go. Updated
  the Verified + Verdict bullets to reflect the closed
  caveat.
- **`docs/architecture/semver-policy.md` reflects the
  pkg/client/types.go decision** — said `pkg/types` was a
  Planned package, "deferred until refactor", with the SDK
  "deliberately duplicating types to keep the skeleton
  focused". CLAUDE.md captures the architectural decision
  ("types live alongside the client in pkg/client/types.go
  rather than a separate pkg/types directory") and
  `pkg/client/types.go` is shipped today. Doc rewritten to
  describe `pkg/client/types.go` as the canonical SDK home and
  explain the intentional separation between SDK shapes and
  the server's `internal/api/v1` envelope as a SemVer firewall,
  not duplication-pending-refactor.
- **`internal/sources/trustlines/doc.go` describes the shipped
  reader, not a future one** — said the "future
  StorageClassicSupplyReader (Task #66)" consumes
  `Store.SumTrustlineBalancesAtOrBefore`, but Task #66 closed
  in PR #66's branch and `StorageClassicSupplyReader` ships
  in `internal/supply/storage_classic_reader.go` today. Also
  replaced the "migration in #303" handle with the migration
  number (`0011_create_trustline_observations`) so the pointer
  doesn't depend on PR-link archaeology.
- **`oracle-manipulation-defense.md` gap-analysis reflects shipped
  ADR-0019 implementation** — the table marked Phase 1
  ("Not yet shipped"), Phase 2 ("Not yet shipped"), and the
  `internal/divergence/` cross-reference ("Planned package per
  CLAUDE.md"). All three are live: Phase 1 in
  `internal/aggregate/anomaly/`, Phase 2 in
  `internal/aggregate/baseline/` + `internal/aggregate/confidence/`,
  and the divergence package writes
  `cachekeys.Divergence(asset)` while the orchestrator reads
  it via `lookupDivergencePct` and feeds
  `confidence.CrossOracleFactor`. Updated each row to point at
  the live code; the divergence row notes that L7.3 (the
  post-launch deferred item) is about operational coverage,
  not the wiring itself.
- **`ConfigReserveBalanceReader` godoc reflects fallback role,
  not interim** — said it was "the interim implementation used
  by the supply-snapshot writer until the LCM-based
  AccountEntry observer ships". The observer shipped in PR
  #298 (Task #54), and the chained-fallback reader pattern
  documented in `docs/architecture/supply-pipeline.md` makes
  this reader the bootstrap fallback that fills the gap when
  the live `LCMReserveBalanceReader` doesn't have an
  observation for every watched account. Rewrote the godoc to
  describe its actual role in the chain. Also dropped the
  pointer to `internal/config/config.go::MetadataConfig`'s
  "deferred account-entry observer" note (PR #495 cleaned that
  up — there's no longer such a note to point at).
- **R1 ansible inventory + role defaults match the as-deployed
  state** — `configs/ansible/inventory/r1.example.yml` set
  `run_stellar_core: true` and `run_stellar_rpc: true`, but
  both daemons were REMOVED from r1 on 2026-04-23 (galexie's
  embedded captive-core is the only stellar-core on the box,
  and the indexer reads MinIO directly so no `/rpc` surface is
  needed). The role's `defaults/main.yml` already had
  `run_stellar_core: false` / `run_stellar_rpc: false`, so an
  operator copying the example would have inadvertently
  enabled what the architecture explicitly removed. Also
  corrected region naming: r1 is at Hetzner FSN1 (Falkenstein,
  Germany), not "London"/"Frankfurt"; updated example
  inventory header, `region_name`, and the Per-region
  difference table comment to match.
- **`DistinctAssets` performance-note no longer anchored at 0004**
  — the comment said the planned `asset_catalogue` migration
  "takes the next free slot" and named 0004 as the most recent.
  Migrations are at 0015 on main; the parenthetical confused
  readers about which slot the future migration would take.
  Trimmed the migration anchor; the future-work statement
  remains accurate (no catalogue migration on main today).
- **`internal/storage/timescale/doc.go` reflects shipped reality**
  — fixed two stale claims: (a) the migration manifest listed
  only 0001-0004, but 0001-0015 are applied today (5 supply
  tables, discovered_assets, volatility_baseline, multi-window
  baseline, blend_auctions, four classic-supply observations,
  sep41_supply_events all landed since the comment was written);
  (b) the Testing section claimed unit tests "use mocks at the
  [Store] interface (future work — not yet extracted)", but
  Store is a concrete struct, no interface exists, and the
  established pattern is real-DB testing via testcontainers-go.
- **`/v1/vwap` Truncated-flag godoc points at the right
  alternative** — the `VWAPResult.Truncated` doc said clients
  could "request the pre-computed rollup from the aggregator
  once it ships", but the aggregator already ships and there's
  no `/v1/vwap`-equivalent that takes arbitrary time windows
  from a pre-computed rollup. The closed-bucket-consistent
  surface for that need is `/v1/price` (ADR-0015). Doc rewritten
  to point at it.
- **Phoenix decoder's `evictedOrphans` godoc reflects the shipped
  metric path** — comment said "Production wiring in
  cmd/ratesengine-indexer will export this as
  obs.SourceOrphanEventsTotal once 165d lands". It already
  ships: the dispatcher reads `EvictedOrphans()` via an optional
  interface (`internal/dispatcher/dispatcher.go:339`), and the
  indexer pipeline adds it to `obs.SourceOrphanEventsTotal` in
  `internal/pipeline/processor.go:80`. Doc points readers at the
  real wiring.
- **`internal/sources/external/registry.go` points readers at the
  shipped config surface** — the godoc said operators override
  `DefaultWeight` and `IncludeInVWAP` via "internal/config/external.go
  once it lands", but no such file exists; the external config
  shipped as `ExternalConfig` inside `internal/config/config.go`,
  with a per-venue `enabled` toggle (no per-venue weight/VWAP
  override is wired). Updated the comment to point at the real
  surface and to be honest that per-venue weight overrides are a
  potential follow-up, not a missing surface.
- **`oracle-stale` runbook lists the correct `source` label
  values** — the runbook said the alert label is one of
  `reflector-dex / reflector-cex / reflector-fx / future
  redstone / band / chainlink-http`, but redstone (`SourceName
  = "redstone"`) and band (`SourceName = "band"`) are both
  shipped sources that already register
  `OracleResolutionSeconds` in `internal/pipeline/dispatcher.go`,
  and chainlink-http lives in `internal/divergence/` —
  it's a divergence reference, not an oracle source, and
  doesn't emit `ratesengine_oracle_*` metrics at all. Replaced
  the speculative list with the five actual label values.
- **`docs/operations/sla-probe.md` aligned with shipped alerts** —
  the doc framed alert rules as a "planned follow-up" with
  "likely shapes", but
  `deploy/monitoring/rules/sla-probe.yml` ships all four alerts
  (`p95_breach`, `freshness_breach`, `unit_failed_alert`,
  `stale`) and each has a runbook under
  `docs/operations/runbooks/sla-probe-*.md`. Replaced the
  follow-up framing with a shipped-alerts table matching the
  conventions used in `supply-snapshot.md`'s alerts section.
- **`supply-snapshot.md` no longer says classic + SEP-41 wait on
  their computers shipping** — the lead-in said
  `Each run computes the current Supply per ADR-0011 Algorithm 1
  (native XLM at v1; classic + SEP-41 follow once their respective
  computers ship)`. Algorithm 2 + 3 computers shipped (Tasks
  #55 / #56); the doc's own §"Asset-class scope" table at line
  164 correctly marks all three `Shipped`. The lead-in is the
  one-paragraph view that was inconsistent. Rewritten to be
  honest about the two parallel writers (systemd-timer CLI
  snapshot — XLM-only, vs aggregator-resident refresher — all
  three classes) and the bullet at the top of the doc updated to
  match. Same drift family as #494 (supply package doc.go).
  Continuation of the L6.5 doc-sweep.
- **`ratesengine-ops --help` no longer advertises two subcommands
  that don't exist** — the `usageBody` constant ended with a
  `TODO subcommands (land with their feature PRs):` block listing
  `cache-prime` (warm the Redis cache from Timescale — never
  built; same drift family as #475) and `verify-invariants`
  (cross-check aggregated prices — superseded by the granular
  `verify-archive` / `verify-decoders` / `verify-external` /
  `archive-completeness verify` / `cross-region-check` family
  that actually shipped). Dropped the block entirely so a fresh
  operator running `ratesengine-ops --help` doesn't see promises
  the binary can't keep. Continuation of the L6.5 doc-sweep.
- **`internal/auth/sep10.go` SEP-10 flow comments cite the
  actual handler paths** — the godoc said `Client: GET
  /v1/auth/challenge?account=G…` and `POST /v1/auth/verify with
  the signed XDR`. The handlers are registered as
  `GET /v1/auth/sep10/challenge` and `POST /v1/auth/sep10/token`
  per `internal/api/v1/server.go`. Comment updated to match the
  actual wire paths so a client implementer reading the godoc
  doesn't write requests to non-existent endpoints. Continuation
  of the L6.5 doc-sweep.
- **`internal/sources/blend/README.md` PR-1/2/3/4 follow-ups
  flipped to "Shipped"** — the README framed itself as `Scope of
  this package (PR 1)` with PRs 2, 3, 4 as planned follow-ups
  (storage table + writer; dispatcher + registry wiring; WASM
  audit). All three landed: migration `0009_create_blend_auctions`
  ships the storage; the dispatcher routes Blend events; Task
  #53 closed the audit at `docs/operations/wasm-audits/blend.md`
  and flipped `BackfillSafe = true` in the registry. Section
  rewritten with `### Shipped` (✅ for the four landed surfaces)
  and `### Still deferred` (the money-market + credit-risk +
  Reflector cross-validation surfaces that genuinely remain
  out of scope until customer demand). Same drift family as
  #483 / #490 / #494 / #498. Continuation of the L6.5
  doc-sweep.
- **`internal/archivecompleteness/doc.go` PR-A/B/C sequencing
  reflects shipped reality** — the godoc said `PR A (this
  package as initially shipped)` provides cross-anchor scan,
  `PR B will add native primary scanning + the fix mode`, and
  `PR C wires the verify mode + systemd timer`. All three modes
  ship today: `cmd/ratesengine-ops/main.go` switches on
  `case "check"` / `"fix"` / `"verify"`, and
  `deploy/systemd/archive-completeness.{service,timer}` ship the
  timer. Doc rewritten to describe `# Modes (all shipped)` with
  the actual fallback chain (SDF mainnet → AWS public-blockchain
  → peers) and a pointer to the operational doc. Same drift
  family as #477 / #483 / #490 / #494. Continuation of the L6.5
  doc-sweep.
- **`public-flip.md` ADR-status verification covers all ADRs
  through 0024** — the row read `all 0001-0021 are \`Accepted\`,
  verified 2026-04-30`. Three ADRs landed after that date:
  ADR-0022 (classic supply observers, #302), ADR-0023 (SEP-41
  supply, #308), ADR-0024 (Redis HA via Sentinel, #343). All
  three are `status: Accepted`. Row updated to `0001-0024
  Accepted` with a parenthetical noting which three landed in
  the gap, so the public-flip checklist correctly reflects the
  current ADR set the public repo will inherit. Continuation of
  the L6.5 doc-sweep.
- **`deploy/monitoring/README.md` no longer says the
  AlertManager config is TBD** — `AlertManager routes by label
  (see its config, TBD)` was the line. The config template
  ships at
  `configs/ansible/roles/prometheus/templates/alertmanager.yml.j2`
  (rendered to `/etc/alertmanager/alertmanager.yml` on
  `mon-01..02` by the prometheus ansible role; see Task #72/#83).
  Section now points at the template + describes the
  severity → channel routing actually in place. Continuation of
  the L6.5 doc-sweep.
- **`MetadataConfig` doc no longer claims the on-chain
  AccountEntry observer is "deferred"** — the type comment said
  the static `[metadata.issuer_home_domains]` map was the
  pragmatic middle ground "until that plumbing lands" (referring
  to a deferred account-entry observer). Per Task #54 / #61 the
  observer + LCM-derived resolver shipped:
  `internal/sources/accounts` writes the
  `account_observations` hypertable;
  `internal/metadata.LCMHomeDomainResolver` reads from it; the
  api binary chains them via `metadata.ChainedHomeDomainLookup`
  with the static map as fallback. Doc + the field-level
  `doc:` tag rewritten to describe the chained role accurately
  (live resolver primary, static map fallback). Generated
  `docs/reference/config/README.md` regenerated. Same drift
  family as #494 (supply Future PRs that already shipped).
  Continuation of the L6.5 doc-sweep.
- **`internal/supply/doc.go` no longer says ClassicComputer +
  SEP41Computer are "Future PR"** — Algorithm-2 (classic credit
  asset) and Algorithm-3 (SEP-41 Soroban) computers shipped per
  Tasks #55 / #56; the file `internal/supply/{classic.go,
  sep41.go}` exists alongside the per-class observers
  (`internal/sources/{trustlines,claimable_balances,
  liquidity_pools,sac_balances,sep41_supply}`). The doc framed
  both as "Future PR" plus a closing "Future PRs add:
  ClassicComputer, SEP41Computer, Postgres-backed Store +
  asset_supply_history hypertable migration, SAC-wrapped
  cross-check" — every item on that list has shipped. Doc
  rewrites the algorithm-2/3 paragraphs around the live impls
  (per ADR-0022 / ADR-0023) and replaces the "Future PRs add"
  block with the actual package surface (Refresher,
  StorageClassicSupplyReader, StorageSEP41SupplyReader,
  CrosscheckRefresher, WriteSnapshotTextfile). Same drift
  family as #477 / #483 / #490. Continuation of the L6.5
  doc-sweep.
- **Two more `Phase 5` framings dropped** —
  `internal/cachekeys/keys.go` said the writer for `apikey:`
  records was `\`/v1/account/keys\` self-service handler (Phase
  5)`, but the handler shipped (#196). `docs/operations/
  sep1-resolution.md` said `ratesengine_metadata_resolver_error_rate_high`
  is `"designed but not yet shipping" pending Phase-5 wiring of
  the metadata overlay into the asset handler` — the overlay IS
  wired (see the doc's own §"Resolution flow"). What's missing
  is just the Prometheus rule turning existing counters into a
  paged signal. Both updated to reflect actual state without
  the stale phase label. Same family as #481 / #487. L6.5
  doc-sweep continuation.
- **`internal/api/v1/middleware/doc.go` matches the actual
  middleware stack** — the package godoc said the order was
  `RequestID → HTTPMetrics → Logger → Recoverer → CORS →
  RateLimit` and explicitly stated `This package does NOT
  implement auth.` Both stale: (a) the actual stack per
  `internal/api/v1/server.go`'s `Server.Handler` is
  `RequestID → HTTPMetrics → Logger → Recoverer →
  SecurityHeaders → CacheControl → CORS → Auth → RateLimit`
  (SecurityHeaders + CacheControl + Auth all missing from the
  doc); (b) the unified `Auth` middleware ships at
  `internal/api/v1/middleware/auth.go` (handles `apikey` and
  `sep10` modes via the `auth` package's validator interfaces).
  Doc rewritten with the correct stack and a new `# Auth`
  section. Same drift family as #489 (api/v1 doc.go). L6.5
  doc-sweep continuation.
- **`contract-schema-evolution.md` "What's NOT yet done"
  reflects the wasm-history shipping** — the doc's checklist
  said `Per-source audit: enumerate every historical WASM hash
  for each of the four Soroban sources. Blocked on live mainnet
  RPC access (r1 stack is up; query hasn't been written).` and
  `ratesengine-ops schema-audit CLI. Not scoped in Phase 1`. Both
  shipped: per-source audits live at
  `docs/operations/wasm-audits/` for Aquarius, Band, Blend,
  Comet, Phoenix; the CLI is `ratesengine-ops wasm-history`,
  `wasm-history-merge-jsonl`, `extract-wasm-from-galexie` —
  walking from Galexie's MinIO output instead of stellar-rpc
  (which was removed from r1 on 2026-04-23). Section renamed to
  "Status" with [x] for what shipped and [ ] for the genuinely
  remaining items (`contract_wasm_hash` column, per-connector
  schema-evolution prose). `last_verified: 2026-05-02` bumped.
  Continuation of the L6.5 doc-sweep.
- **`internal/canonical/discovery/doc.go` "Future work" list has
  shipped** — the package's `# Future work (separate PRs):` block
  named three items, all of which have landed:
  - Dispatcher integration → `internal/dispatcher/dispatcher.go`
    calls `discovery.Sniff` on every event after decoder
    dispatch.
  - Postgres-backed Recorder → `internal/storage/timescale/
    discovery.go` implements `Recorder` against the
    `discovered_assets` hypertable.
  - Ops command + alert metric → `ratesengine-ops discovery`
    subcommand exists; `ratesengine_ingestion_discovery_drops`
    alert lives in `deploy/monitoring/rules/ingestion.yml`.
  Section renamed to "Wired today" with concrete file pointers.
  Same drift family as #477 / #483 / #484. Continuation of the
  L6.5 doc-sweep.
- **`internal/api/v1/doc.go` no longer says auth is "future"** —
  the package-level "What this package doesn't do" list said
  `No auth logic — [middleware.APIKey] (future) handles that.`
  Two stalenesses: (a) the auth middleware ships today at
  `internal/api/v1/middleware/auth.go` (Auth, not APIKey), and
  (b) it's a unified middleware that handles both API-key and
  SEP-10 modes via the validator interfaces in `internal/auth`.
  Rewritten to point at the live middleware + concrete
  validators (`auth.RedisAPIKeyValidator`, `sep10.Validator`).
  Same drift family as #477 / #482. Continuation of the L6.5
  doc-sweep.
- **`sep1-resolution.md` operator-override section described a
  fictional schema** — the §"Adding a curated home-domain
  override" subsection showed a `config/asset_metadata_overrides.yaml`
  file with per-asset `name` / `desc` / `image` / `max_supply`
  overrides plus a `sep1_status: operator_override` wire status.
  None of that exists. The actual override is much narrower:
  `[metadata.issuer_home_domains]` in `/etc/ratesengine.toml`
  maps issuer G-strkey → home-domain so the SEP-1 resolver can
  fetch the issuer's stellar.toml; per-field metadata comes from
  that toml, not an override. The `operator_override` status
  string is also fictional (no such status code in the codebase).
  Section rewritten to describe the real `MetadataConfig` shape
  + the real reload story (config is parsed at boot, not hot-
  reloaded). Continuation of the L6.5 doc-sweep.
- **`sep1-resolution.md` no longer hand-waves a `sep1-trace`
  subcommand as "Phase 5 deliverable"** — same drift as #481
  (UsageRow). The doc said `ratesengine-ops sep1-trace -domain
  <home_domain> (Phase 5 deliverable; not yet implemented)
  would dump the full resolution path…`. We don't track
  follow-up work as "Phase 5" anymore; the comment now
  describes the gap concretely (`not in
  cmd/ratesengine-ops/main.go's switch today`) and points the
  operator at the manual playbook. Continuation of the L6.5
  doc-sweep.
- **`oracle-manipulation-defense.md` red-team-tests no longer
  hand-waves divergence as `(when shipped)`** — §"Validation
  exercises" red-team-test 1 said `Divergence monitoring (when
  shipped) flags it`. Divergence monitoring HAS shipped (per
  `internal/divergence/{compare,worker,coingecko,chainlink}.go`
  + the orchestrator's `DivergenceRefresher` Tick wiring).
  Updated to describe the live behaviour: `flags.divergence_warning`
  flips on the affected pair via the `div:<asset>` Redis key
  the divergence service writes, and the `/v1/price` handler
  surfaces it. Same drift family as #483 / #484. Continuation
  of the L6.5 doc-sweep.
- **`api-design.md` no longer reads as a Week-4-pending design
  doc** — frontmatter said `status: draft — ratified at Week 4
  design review` and the body had `**Ratification target:** end
  of Week 4.` We're well past Week 4; the OpenAPI file ships as
  the binding contract today and 32+ handlers are wired against
  it. Frontmatter flipped to `ratified` with the right pointer
  ("openapi/rates-engine.v1.yaml is the binding contract; this
  doc records design intent"). §15 "Open questions (close by
  Week 4)" rewritten as a closure list — GraphQL→L7.5,
  SSE-not-WebSocket (shipped), proxy-not-rehost issuer images
  (the metadata package does this), no Webhook callbacks, no
  gRPC. Also fixed a stale `lint-docs.sh §11` citation in §16
  (the OpenAPI ↔ handler invariant is §2). Same drift family as
  #466 / #467 (Week-N frontmatter de-staling). Continuation of
  the L6.5 doc-sweep.
- **`internal/divergence/doc.go` describes the wired-today
  scope, not the original `PR A` slice** — the package's `# Scope`
  section was framed as `PR A (this package as initially shipped)
  ...` plus a `Subsequent PRs add more references` list naming
  CoinMarketCap, Reflector, Band, Redstone, Chainlink. Reality:
  Chainlink shipped (`ChainlinkReference`); the others either
  (a) don't belong in this package because they ingest as on-
  chain *sources* not divergence references (Reflector, Band,
  Redstone — they contribute to the underlying VWAP itself),
  or (b) are deferred behind operator demand (CoinMarketCap).
  The "PR B/C will add confidence-weighted aggregation" line
  also stale — `[ServiceOptions.MinSourcesForWarning]` does the
  trust-floor job today via the `[divergence].min_sources_for_warning`
  config knob. Section rewritten around what's wired now (Compare,
  Service, CoinGecko, Chainlink) and a one-paragraph note on
  why on-chain oracles aren't here. Continuation of the L6.5
  doc-sweep.
- **`internal/aggregate/doc.go` no longer claims triangulation
  is deferred** — the package's "What this package deliberately
  doesn't do" section listed `No multi-venue weighting /
  triangulation. Those are deferred items captured in
  docs/architecture/aggregation-plan.md.` But triangulation
  ships in this package — `triangulate.go` defines `Triangulate`
  and `TriangulateChain` (X2.5 forex-snap rule for chained-fiat,
  per F-0014), and the aggregator orchestrator wires it via the
  `Triangulations` field. New `# Triangulation` heading
  documents what's there; the "deliberately doesn't do" list
  retains the still-deferred multi-venue weighting (per-source
  weight overrides). Continuation of the L6.5 doc-sweep.
- **`auth.ErrNotImplemented` doc comment no longer claims the
  sentinel goes away once the validator body lands** — said
  `Removed once the body implementation lands`, but the SEP-10
  body has shipped at `internal/auth/sep10/` and the apikey body
  shipped via `RedisAPIKeyValidator` (#196). The sentinel
  **stays** because it serves the `NoopAPIKeyValidator` /
  `NoopSEP10Validator` fallbacks — the deliberate disabled-state
  the middleware lands on when an auth-mode is configured but
  no real validator is wired (e.g. `auth_mode=apikey` selected
  but Redis unavailable). Comment rewritten to describe the
  actual role: fail-loud 503 from the disabled-state fallback,
  not a placeholder awaiting replacement. Same drift family as
  #477 / #481. Continuation of the L6.5 doc-sweep.
- **`UsageRow` godoc no longer hand-waves "Phase 5 follow-up"** —
  the wire-shape comment said the `/v1/account/usage` counter
  store does not yet exist as a "Phase 5 follow-up." More accurate:
  the rate-limit middleware records per-key request counts in
  Redis today; the missing piece is a rollup writer that
  aggregates those into daily UsageRows. Comment now describes
  what's there and what's missing in concrete terms (rather than
  pointing at a phase label that's not how follow-up work is
  tracked anymore). Continuation of the L6.5 doc-sweep.
- **`aggregation-plan.md` API-surface table is internally
  consistent** — the `GET /v1/twap` row claimed `Backed by:
  Redis cache` while the same row's parenthetical said
  `TWAP-via-orchestrator path is TBD`. Both can't be true; the
  handler at `internal/api/v1/twap.go` runs `aggregate.TWAP`
  against the trades hypertable on every request — there is no
  TWAP cache. Row updated to `Trades hypertable (on-query)` and
  the Deferred section grew an explicit `TWAP-via-orchestrator
  pre-compute` entry so the parenthetical "see Deferred" cites
  something real. Continuation of the L6.5 doc-sweep.
- **ADR-0019 Phase 2 godocs no longer claim the phase is
  unbuilt** — `internal/aggregate/anomaly/doc.go` framed Phase 2
  as "planned per ADR-0019 §Phase 2" and Phase 1 as "the
  safety-net we ship before Phase 2 lands so the API has SOME
  anomaly protection during the gap." Phase 2 has shipped:
  `internal/aggregate/baseline/` (per-asset MAD baselines +
  z-score), `internal/aggregate/confidence/` (six-factor
  weighted-geomean confidence). Both layers run in parallel; the
  orchestrator's AND-of-three-signals rule
  ([Phase2FreezeConfig]) only fires ActionFreeze when Phase 1
  flags a class-level breach **and** Phase 2 confirms statistical
  anomaly + low confidence + low corroboration.
  `internal/config/config.go`'s `AnomalyConfig` description and
  the field-level `Anomaly` doc-tag carried the same "Phase 2
  will replace this" framing — both rewritten to describe the
  actual parallel scheme. Continuation of the L6.5 doc-sweep.
- **`internal/auth/sep10.go` and `internal/aggregate/orchestrator`
  godocs match shipped reality** — the SEP-10 interface declared
  `Production implementation lands in Phase 5; current
  [NoopSEP10Validator] returns [ErrNotImplemented] from every
  method`. The real implementation has shipped at
  `internal/auth/sep10/` (Validator, Challenge, Verify,
  VerifyJWT) and is wired in `cmd/ratesengine-api/main.go`'s
  `buildSEP10Validator`; Noop is now correctly described as the
  fallback for non-`auth_mode=sep10` deployments. The aggregator
  orchestrator's "Deliberately out of scope for v1" list claimed
  stablecoin→fiat proxy, triangulation, divergence, and outlier
  filtering were all still pending — every one has shipped (the
  ratesengine-aggregator binary wires each one through
  `orchestrator.Config` fields). Both godocs rewritten to
  describe what's actually wired today, with pointers at the
  packages doing the work. Same drift family as #475 / #476.
  Continuation of the L6.5 doc-sweep.
- **`ratesengine-api` and `ratesengine-aggregator` package
  docstrings match what each binary actually wires today** —
  the api binary's godoc said "Today: /v1/healthz, /v1/readyz,
  /v1/version — the infra-facing surface. The full endpoint
  catalogue ... lands in follow-up PRs." Reality: 32+ handlers
  registered (full pricing, historical, catalogue, oracle, account
  self-service, SEP-10, SSE streams). The aggregator binary's
  godoc had a "Deferred to follow-up PRs" list that already
  shipped: triangulation worker (X2.5 forex-snap, F-0014),
  divergence detector (Tick-driven RefreshPair), outlier filter
  (`OutlierSigmaThreshold`), and the multi-factor confidence +
  ADR-0019 anomaly-response pipeline. Both godocs now describe
  the actual wired surface and point at the canonical source
  block (`server.go` HandleFunc list, `orchestrator.Config`
  fields). Same drift family as #475 (ops binary). Continuation
  of the L6.5 doc-sweep.
- **`ratesengine-ops` package docstring matches the actual
  subcommand set** — the binary's `// Binary ratesengine-ops`
  godoc said "admin CLI: backfill, gap-detect, cache-prime,
  docs-config" with the closing line "Today only `docs-config`
  is wired; the rest land with the corresponding implementation
  PRs." Reality (per `cmd/ratesengine-ops/main.go`'s
  `switch args[0]` block): 18+ subcommands wired across ingest /
  archive integrity / Soroban discovery / supply / diagnostics /
  doc generation. The docstring also called the gap-detection
  subcommand `gap-detect` but the actual name is `detect-gaps`,
  and `cache-prime` was never built. Rewritten to enumerate the
  real buckets with the canonical names; closing line now points
  readers at the switch block + `--help` as the source of truth.
  Continuation of the L6.5 doc-sweep.
- **`CONTRIBUTING.md` and `repo-hygiene-plan.md` source-connector
  five-file convention now matches reality** — both docs listed
  the fourth canonical file as `factory.go (on-chain) or
  consumer.go (off-chain)`, but no `factory.go` exists anywhere
  in `internal/sources/` (verified with `find internal/sources
  -name factory.go`). The on-chain shape uses `consumer.go` plus
  source-specific extras like `dispatcher_adapter.go` and
  `factory_seed.go` (Soroswap / Aquarius factory-deploys-pair
  contracts). The CEX shape sometimes splits `consumer.go` into
  `streamer.go` + `backfill.go` (binance). Both docs now name
  `consumer.go` as the canonical fourth file (matching CLAUDE.md
  §"Add a new CEX connector") and mention the per-shape extras.
  Continuation of the L6.5 doc-sweep.
- **`README.md` no longer claims a non-existent Stellar
  protocol** — the `**Tested against:** Stellar protocol 25.x`
  line at the top of the README pointed at a network protocol
  that doesn't exist (the only "protocol 25" in the repo is in a
  hypothetical SEV-2 drill scenario explicitly marked
  `(hypothetical)`). Real protocol per CLAUDE.md +
  contract-schema-evolution.md + semver-policy.md is **23**
  (Whisk, mainnet 2025-09-03, CAP-67 unified events). README
  now matches. Also fixed README's repo-layout block: `cmd/`
  list missing `sla-probe`; `deploy/` description had stale
  "k8s / baremetal" instead of the actual
  docker-compose/systemd/monitoring/status-page subdirs;
  `configs/` description tightened to call out the ansible
  shape. Same drift family as #470 (CLAUDE.md tree). L6.5
  doc-sweep continuation.
- **`lint-docs.sh` no longer exempts `/v1/price/stream` from the
  "spec ↔ handler" check** — the planned_regex allow-list was
  scoped to "documented but not yet shipped" routes; the only
  entry was `/price/stream`, but the handler has been registered
  in `internal/api/v1/server.go:354` since before launch
  readiness began. Cross-checked: every OpenAPI path has a
  handler and every handler is in OpenAPI today, so the
  allow-list is empty. Tightened to `'^$'` (matches nothing)
  with a comment on what to do if a future doc-but-stub endpoint
  lands. Closes a small drift in CI strictness. L6.5 doc-sweep
  continuation.
- **`AGENTS.md` and `CLAUDE.md` quick-reference make-targets are
  accurate** — `AGENTS.md` claimed `make lint` runs "gofumpt +
  golangci-lint + archlint"; the actual `lint` target only runs
  golangci-lint (gofumpt is a golangci formatter), and the
  architectural import-boundary check is the separate
  `lint-imports` target. `make verify` was missing from
  `CLAUDE.md`'s build-and-test quick-reference even though
  `verify.sh` is the canonical pre-push gate (fmt+vet+lint+
  docs+test); operators reading just the top quick-reference
  would miss it. Both files now describe `make verify` with the
  same definition the Makefile uses, and the docs-all line on
  both files mentions metric Name: regen alongside OpenAPI +
  struct tags. Continuation of the L6.5 doc-sweep.
- **`CLAUDE.md` repo-tree is now accurate** — the orientation
  file every AI agent reads cold claimed `cmd/ binary entry
  points (four in total)` while listing 5 entries; reality is 6
  (the `ratesengine-sla-probe` binary that ships the SLA-evidence
  harness was missing). The `internal/` enumeration was missing
  five packages: `archivecompleteness` (the dual-archive
  daemon — ADR-0017), `events` (transport-neutral Soroban event
  types), `hashdb` (drift-detector against upstream LCM
  rewrites), `pipeline` (shared ingest glue between indexer +
  `ratesengine-ops backfill`), and `scval` (SCVal primitives
  wrapper). The `deploy/` description claimed "k8s / baremetal
  kits" but the actual subdirs are
  `docker-compose / monitoring / status-page / systemd` (no
  `deploy/k8s/`, per ADR-0008's bare-metal commitment). `configs/`
  description tightened to call out the ansible
  `roles/inventory/playbooks/` shape; `test/` description
  expanded to mention the `load` (k6) and `chaos` trees;
  `docs/audit-2026-04-29/` added to the tree (it's the
  post-Phase-1 cross-cutting findings register that several
  open PRs reference). Continuation of the L6.5 doc-sweep.
- **`repo-hygiene-plan.md` §15 IaC discipline now describes our
  actual stack** — the section listed Kubernetes manifests in
  `deploy/k8s/`, Helm charts, and "no inline shell heredocs in
  manifests" as the IaC discipline, but ADR-0008 ratifies bare
  metal + systemd + Ansible (no Kubernetes anywhere). Section
  rewritten around `configs/ansible/roles/<name>/`, the actual
  systemd units in `deploy/systemd/` (api / indexer / aggregator
  + the four timer/oneshot pairs for archive-completeness,
  sla-probe, supply-snapshot, verify-archive-tier-a), and
  `deploy/docker-compose/` as the dev-only reference stack.
  Continuation of the L6.5 doc-sweep.
- **`coverage-matrix.md` and `repo-hygiene-plan.md` no longer
  point at Week-N plan items that have either landed elsewhere
  or were never built** — the coverage matrix's "deferred to
  Week 9" / "planned (Weeks 8–9)" lines now cite the actual k6
  suite at `test/load/`, the operator-driven backfill via
  `ratesengine-ops backfill`, and the bare-metal+systemd+ansible
  deployment kit (the matrix had been promising `deploy/k8s`
  which doesn't exist and isn't our deployment shape). The
  hygiene plan's `scripts/ci/check-adr-numbering.sh` and
  `scripts/ci/lint-layout.sh` references are now accurate:
  ADR status integrity is enforced by `lint-docs.sh §8`,
  numbering-gap is reviewer-policed (no dedicated script yet);
  the architectural import-boundary check is in
  `lint-imports.sh`. The protocol-boundary fixtures section now
  describes the actual `test/fixtures/<source>/` layout instead
  of the original `test/fixtures/protocol-boundary/{pre,post}-pNN/`
  tree that never landed. Continuation of the L6.5 doc-sweep.
- **Architecture-doc frontmatter no longer pretends the launch
  plan is mid-flight** — `ha-plan.md`, `multi-region-topology.md`,
  `archival-node-spec.md`, `hosting-options.md`, and
  `validator-rollout.md` each declared themselves `draft —
  ratified at Week 2 …` or `decision at Week 1 procurement
  call`. We are well past those weeks; the plan executed (ADR-0008
  ratifies HA, ADR-0016 ratifies per-region storage, the
  `archival-node` ansible role embodies the per-host spec, r1 is
  live on Hetzner FSN1). Frontmatter on each now reflects current
  state with a pointer to the ratifying ADR or role. The
  `ha-plan.md §11` and `multi-region-topology.md §15` "Open
  questions to close before Week 2 design review" sections are
  now "closed" lists, citing where each answer landed (ADR / role
  path / runbook). Removes a recurring source of confusion when
  agents and operators read these docs cold and assume the plan
  is still in flight. Continuation of the L6.5 doc-sweep.
- **Eight more runbooks plus the runbook template are
  bare-metal-native** — final batch of single-mention kubectl /
  k8s drift in the L6.5 doc-sweep. `redis-replication.md`,
  `redis-memory.md`, `price-stale.md`, `rpc-lag.md`,
  `core-lag.md`, `archive-publish.md`, `archive-divergence.md`,
  `backup-failed.md`, and `_template.md` each had a single
  kubectl line referencing pods/StatefulSets/Daemonsets/Jobs
  that don't exist in our deployment. Each line replaced with
  the systemd / journalctl / ansible-role equivalent that
  matches what the repo actually deploys. The `_template`
  example block now nudges new runbook authors toward
  `systemctl status` / `journalctl -u` rather than `kubectl ...`.
  All 25+ kubectl-bearing runbooks have now been converted across
  PRs #460/#461/#462/#463/#464 and this PR.
- **Four host-level runbooks are bare-metal-native** —
  `host-cpu-high`, `host-memory-high`, `host-down`, `nvme-smart`
  each had a single `kubectl` line that doesn't apply to our
  fleet. Per-process / per-cgroup breakdown now uses
  `systemd-cgtop` (it's already installed on every Ubuntu host
  via systemd; no extra deps). Host-drain steps now route via
  HAProxy admin (`disable server <pool>/<host>` on each LB)
  instead of `kubectl cordon` — Patroni / Sentinel handle DB and
  cache primary failover automatically. Continuation of the L6.5
  doc-sweep (#460/#461/#462/#463).
- **Five indexer-side runbooks are bare-metal-native** —
  `source-stopped`, `cursor-stuck`, `orphan-events`,
  `discovery-drops`, and `decode-errors` each had a single stale
  `kubectl rollout restart deploy/ratesengine-indexer` /
  `kubectl logs deploy/ratesengine-indexer` invocation that
  doesn't run on r1. The indexer ships as
  `ratesengine-indexer.service` per the `archival-node` ansible
  role (ADR-0008). Restart commands now use `ssh root@indexer-01
  "systemctl restart ratesengine-indexer"`; log commands use
  `journalctl -u ratesengine-indexer`. Continuation of the L6.5
  doc-sweep started in #460/#461/#462.
- **Four more runbooks are bare-metal-native** — same drift as
  api-down and api-5xx: kubectl-flavoured diagnosis steps that
  wouldn't run on production. `redis-master-down.md` now talks
  to `cache-01..03` running `redis-server.service` +
  `redis-sentinel.service` (per the `redis-sentinel` role) instead
  of `kubectl get pods -l app=redis` and `redis-0..2` StatefulSet
  pod names. `scrape-failing.md` swaps `kubectl exec -it
  prometheus-0` for `ssh root@mon-01` running `prometheus.service`
  and rewrites the SD-misconfig section from ServiceMonitor /
  PodMonitor to the prometheus role's static-config drift.
  `alertmanager-bad-config.md` swaps `kubectl get cm
  alertmanager-config -o jsonpath` for `cat
  /etc/alertmanager/alertmanager.yml` on `mon-01..02` (the cited
  `deploy/monitoring/alertmanager.yml` was a fictional file — the
  role-rendered template is the source of truth). `core-peers.md`
  swaps `kubectl describe cm` / `kubectl logs ds/stellar-core`
  and a fictional `deploy/k8s/network-policy.yaml` for the
  `archival-node` role's per-validator-host shape (still inert on
  r1 since stellar-core was removed 2026-04-23, but ready for the
  Phase-3 Tier-1 rollout). Closes another batch of the L6.5
  doc-sweep.
- **`api-5xx` runbook is bare-metal-native** — the runbook still
  walked operators through `kubectl rollout undo`, an Istio
  `VirtualService` JSON-patch (we don't run Istio), and
  `kubectl scale --replicas=6` for "load mitigation." None of
  those map to production: ADR-0008 ratifies systemd-managed
  binaries on three fixed `api-01..03` hosts behind two HAProxy
  load balancers — no autoscaler, no Istio, no kubectl.
  Diagnosis now uses the per-host `/v1/version` probe +
  `systemctl show -p ActiveEnterTimestamp` to time-correlate
  releases against the error-rate lift; §A revert defers to the
  Rollback procedure in `release-process.md`; §B endpoint-block
  offers the HAProxy `http-request return 503 if path_beg`
  rule + the binary feature-flag option; §D rewrites "scale up"
  guidance — bare metal doesn't autoscale, so the real
  mitigations are edge rate-limiting + path shedding + (last
  resort) DR promotion. Closes another L6.5 doc-sweep item.
- **`api-down` runbook + `release-process.md` rollback path are
  now bare-metal-native** — both still spoke kubectl
  (`kubectl rollout undo`, `kubectl logs`, `kubectl get pods`,
  …) from a pre-ADR-0008 cloud-sketch era. ADR-0008 ratifies
  colocated bare metal as the primary deployment shape; production
  runs `ratesengine-api.service` on three hosts behind two
  HAProxy + keepalived load balancers — no Kubernetes anywhere.
  An operator paged at 3 AM following kubectl commands on this
  fleet would land on errors, not diagnosis. `api-down.md`
  rewritten end-to-end against `systemctl` / `journalctl` /
  HAProxy admin socket; `release-process.md` grew a full
  "Rollback" section documenting the per-host binary-swap
  procedure (rolling for the API tier via the
  `disable server api_pool/api-XX` admin command). The post-flight
  thin "Rollback path" bullet now points at the new section
  instead of inlining a stub. Closes a documentation drift
  surfaced during the L6.5 doc-sweep.
- **`pkg/client/doc.go` — accurate auth + coverage** — the
  package-level godoc that ships to pkg.go.dev had two stale
  sections: the "Authentication" SEP-10 bullet still said
  "pending; will be added when the server's SEP-10 verifier ships
  (Phase 5)" — but the verifier ships at
  `/v1/auth/sep10/{challenge,token}` (PR landed weeks ago) and
  the SDK accepts SEP-10 JWTs verbatim via Options.APIKey today.
  And the "Roadmap" section claimed "PR A (this PR) ships the
  skeleton" — language that's been stale since the skeleton
  landed. Replaced both: SEP-10 bullet documents the live
  challenge → sign → verify flow + that `Authorization: Bearer`
  carries either `rek_*` keys or SEP-10 JWTs; the new "Coverage"
  section enumerates the eight methods on main today, the seven
  queued in PRs #446–#450, and the four surfaces deliberately
  not-in-SDK (SSE / VWAP-TWAP-derivable / SEP-40 oracle /
  operator endpoints).
- **`launch-readiness-backlog.md` — six 🟢 / 🟡 items flipped to ✅
  to match shipped reality** (L6.5 doc-sweep): L3.11 (API
  reference workflow), L3.14 (CDN cache-control middleware),
  L3.15 (getting-started doc), L3.16 (URL-discipline OpenAPI
  lint), L5.5 (chaos suite Wave 1), L6.1 (CHANGELOG hygiene +
  SemVer policy), L6.2 (release notes template +
  release-process), L6.3 (public-flip prep). Each row now points
  at the file path that exists on main today and notes any
  per-item operator follow-up that's deliberately deferred (e.g.
  L3.14's CloudFront-side config, L6.3's actual cutover at
  L6.4). Status emoji legend at line 34 unchanged.
- **`docs/getting-started.md` SDK example now compiles** — the
  customer-facing onboarding doc showed
  `c.GetPrice(ctx, "native", "fiat:USD")` for the SDK quickstart,
  but no such method exists on `*client.Client`. Customers
  copy-pasting the example would hit a Go build error on the
  first line. Replaced with the actual `c.Price(ctx,
  client.PriceQuery{Asset, Quote})` shape returning
  `*Envelope[PriceSnapshot]`. Also fixed the API-key example
  prefix (`rate_` → `rek_`, matching the actual issuance path
  at `internal/auth/store.go:142`'s
  `generateID(s.randRead, "rek_", 32)`) and added a "what methods
  exist today" note so the doc doesn't imply a method that
  lives on an unmerged PR.
- **Three runbooks no longer reference fictional commands /
  paths** (L6.5 doc-sweep continued):
  `runbooks/all-ingestion-down.md` §D referenced `make rollback
  INDEXER_VERSION=<previous>` (`TODO(#0)`); the make target
  doesn't exist and the deployment shape doesn't fit the local-
  build convention. Replaced with the actual systemd-binary
  rollback procedure that `release-process.md` §4.4 prescribes:
  stop the unit, copy the previous-release binary into place
  (kept by goreleaser packaging convention at
  `/opt/ratesengine/release-<tag>/`), restart.
  `runbooks/ingestion-lag.md` step 4 carried `TODO(#0)` for the
  backfill subcommand — except the subcommand exists and has
  for some time (`ratesengine-ops backfill -from N -to N
  -source S`). Replaced the placeholder with the concrete
  two-step `detect-gaps` → `backfill` procedure operators run
  during incidents.
  `runbooks/insert-errors.md` step 2 had the same stale
  `TODO(#0)` PLUS a fictional `deploy/k8s/` PVC reference. The
  production deployment is bare-metal NVMe + ZFS per ADR-0008,
  not Kubernetes. Updated to point at zpool / Hetzner volume-
  resize and the same backfill commands.
- **Six broken markdown links across docs** (L6.5 doc-sweep) —
  surfaced via a Python sweep across every relative `(./...md)`
  link in `docs/`. Closed:
  `docs/adr/0023-sep41-supply-observer.md` `0003-i128-no-truncate.md`
  → `0003-i128-no-truncation.md`.
  `docs/architecture/supply-pipeline.md` two links: same ADR-0003
  fix + `0006-timescale-storage.md` →
  `0006-timescaledb-for-price-time-series.md`.
  `docs/operations/r1-deployment-state.md`: extra `..` in
  `../../discovery/data-sources/archival-nodes.md` → fixed to
  `../discovery/...`.
  `docs/operations/wasm-audits/evidence/blend/phase2-2026-05-02/README.md`:
  off-by-one relative path `../../blend.md` → `../../../blend.md`.
  `docs/architecture/infrastructure/archival-node-spec.md`: three
  fictional runbook refs (`archive-publish-fail.md`,
  `galexie-lag.md`, `rpc-sqlite-growth.md`); first replaced with
  the real `archive-publish.md`, the other two converted to
  italicised "_runbook tbd_" notes citing the existing ad-hoc
  coverage path (no creation of stub runbooks — the alerts they
  reference are post-launch / Phase-3 anyway).
  `docs/architecture/ha-plan.md` §3.10 ratesengine-ops: fictional
  `ops-cli.md` doc replaced with a description of the binary's
  actual top-level subcommands, citing `--help` and the source
  at `cmd/ratesengine-ops/main.go`.
  Verification: re-ran the link sweep; zero broken links remain.

- **SSE event-ID generator no longer wraps to duplicates after
  65 536 same-millisecond IDs** — `streaming.Generator.Next`'s
  docstring promised "never returns the same ID twice" but the
  counter was masked to 16 bits, so 65 536 IDs in a single
  millisecond wrapped back to 0 and re-issued every prior ID
  for that millisecond. A reproducer pinned the bug at 4 464
  duplicates across 70 000 calls in one ms (e.g. publish-burst
  during a fan-out spike, tight test loop, or hot-loop in
  operator code). Fix advances the synthetic millis by 1 when
  the counter saturates instead of wrapping; subsequent
  wall-clock ms catch back up via the existing `now > oldMillis`
  branch. Three new tests: NeverDuplicates (70 k same-ms calls),
  StrictlyIncreasing (lex-sort = chronological invariant),
  ConcurrentNoDuplicates (50×2 000 goroutines).

- **`divergence.Compare` recovers panics from references** — the
  function's docstring promised "panic recovered, etc. are
  recorded in Failures", but the per-reference goroutine had no
  `recover()` deferred. A misbehaving reference (network panic,
  malformed-JSON parser blow-up, operator-supplied custom
  reference with a bug) would take the whole comparison run
  down + crash the worker. Now the goroutine recovers and
  records the panic with a stable `panicked: <text>` failure
  label so operators see which reference is broken without
  reading goroutine traces. New `safeName` helper guards
  `Reference.Name()` itself in case it's what panics — the
  failure surfaces under `_unknown` in that path.

- **Rate-limit middleware now honours `Subject.RateLimitPerMin`** —
  the field was plumbed end-to-end (storage record → validator →
  Subject → `/v1/account/me`) but `RateLimitBySubject` only
  consulted the bucket's static `Max()`, so a paid customer with a
  per-key override of e.g. 5000/min got throttled at the deployment
  default (typically 1000). `Bucket.TakeN(ctx, key, max)` accepts
  a per-call override (≤0 falls back to `b.max`); the middleware
  passes `subject.RateLimitPerMin` through and surfaces the
  effective limit in the `X-RateLimit-Limit` response header.
  Anonymous callers continue to use the bucket default (no per-IP
  override path). Closes another exposed-but-never-driven gap from
  the account self-service work.

- **`/v1/account/me` now returns the credential's `label`** —
  `APIKeyRecord.Label` was set at creation time and the OpenAPI
  `Account` schema declared the field, but the path
  `RedisAPIKeyValidator.Lookup` → `auth.Subject` → `handleAccountMe`
  dropped it on the floor (no `Label` field on `Subject`). Customers
  who created keys via `POST /v1/account/keys` saw their chosen label
  recorded, then got an empty string back from `/me`. Subject now
  carries `Label`, the validator copies it from the record, and the
  handler surfaces it. Anonymous callers continue to get an empty
  label (omitempty hides it from the wire).

### Added

- **`/v1/sources` exposes `subclass` and `backfill_safe`** — the
  endpoint already projected `external.Registry` to the wire, but
  two operationally-useful fields stayed internal-only. `subclass`
  (`dex` / `cex` / `fx`, omitted for non-exchange classes) lets UI
  consumers group exchange venues without reverse-engineering the
  name prefix. `backfill_safe` surfaces the per-WASM-hash audit
  state that gates `ratesengine-ops backfill` (CLAUDE.md "Soroban
  DeFi contracts upgrade in place"): operators can now read it
  off the API instead of grepping
  `internal/sources/external/registry.go`. Additive — no existing
  field changed shape.

- **`pkg/client` godoc examples** — three `Example*` functions
  (`ExampleNew`, `ExampleClient_Price`, `ExampleClient_Asset`,
  `ExampleAPIError`) that show up in pkg.go.dev and verify
  themselves at build time via `// Output:` assertions.
  Self-contained against `httptest`-backed servers so they don't
  need a live API. Walks integrators through the canonical SDK
  surface: construct + call + handle errors.

- **API binary wires the freeze.Looker so `flags.frozen` is no
  longer permanently false (closes another half-shipped audit
  finding)**: `freeze.Looker` reads the `freeze:<asset>:<quote>`
  markers the aggregator's `freeze.Writer` publishes (Phase 1 + 2
  anomaly response, ADR-0019), but the API binary's
  `v1.New(Options{...})` never set `Freeze:`. The handler-side
  `FrozenLooker` interface was declared and `/v1/price`'s
  `lookupFrozen` consulted it, but with no looker installed the
  call always returned (false, nil) — operators relying on
  `flags.frozen` to detect frozen-LKG responses got permanent
  `false`. Now `cmd/ratesengine-api/main.go` constructs
  `freeze.NewLooker(rdb)` when Redis is configured (mirrors the
  existing pattern for confidence + triangulated lookers) and
  passes it through `Options.Freeze`. L3.13 in the launch-readiness
  backlog flips from 🟢 to ✅.

- **Aggregator now drives the divergence-cache refresh (closes
  another half-shipped audit finding)**:
  `divergence.Service.RefreshPair` was exposed but had zero
  production callers — the API's `flags.divergence_warning` reads
  from `div:<asset>` Redis cache, but nothing populated the cache,
  so the flag was permanently `false` across the public surface.
  Wired the orchestrator's Tick to call `RefreshPair` once per
  configured pair after VWAPs are written, using the
  shortest-window VWAP as "our price". Best-effort per-pair: errors
  log + count via the new `ratesengine_divergence_refresh_total{outcome}`
  counter (ok / no_vwap / parse_error / refresh_error) but never
  abort the Tick. New `orchestrator.DivergenceRefresher` interface
  is the seam (nil = pre-Phase no-op preserved); aggregator's
  `main.go` builds the same `divergence.Service` shape the API
  binary already builds, mirroring the helper for now (a shared
  builder is one CHANGELOG fixme away when a third caller appears).

- **`ratesengine_trade_inserts_total{source, usd_volume_populated}`
  counter for L2.2 phase 1 coverage**: per-source counter labelled
  by whether the trade's `usd_volume` column was populated at
  insert time. Operators flipping on
  `[trades].usd_pegged_classic_assets` use this to verify their
  allow-list actually covers what the indexer is seeing — a
  configured deployment with steady-state
  `usd_volume_populated="no"` on a USDC-quoting venue means the
  operator's classic asset_key doesn't match the decoder's stamp
  (typically an issuer mismatch or a missing entry).
  `Store.WouldPopulateUSDVolume(t)` exposes the predicate as a
  package-public method so the pipeline sink can label the metric
  without re-implementing the populated-ness decision.

- **SEP-1 issuance declarations now surfaced on `/v1/assets/{id}` +
  `/v1/assets/{id}/metadata`**: `conditions`, `fixed_number`,
  `max_number`, and `is_unlimited` from the issuer's
  `[[CURRENCIES]]` entry populate when `sep1_status="verified"`.
  These are issuer-declared (separate from the F2 fields, which
  observe live ledger state) — useful for asset-detail UIs that
  want to show "Circle has committed to a fixed total of X
  tokens" alongside the live `total_supply`. The metadata package
  already parsed these fields; the gap was in the API projection.
  OpenAPI spec was already promising them on
  `/v1/assets/{id}/metadata` (under different field names,
  including the wrong `image_url` for `image`); this PR realigns
  the spec to the handler's actual shape AND adds the four
  issuance fields to the surface for real. SDK
  `pkg/client.AssetMetadata` updated to match (replaces the
  invented `sep1`/`fetched_at` fields that didn't exist on the
  wire).

- **On-chain DEX trades populate `trades.usd_volume` (launch-
  readiness L2.2 phase 1)**: previously only off-chain CEX/FX
  trades populated `usd_volume` at insert time — on-chain trades
  (Stellar SDEX, Soroswap, Aquarius, Phoenix, Comet) stored NULL,
  biasing the `volume_24h_usd` field on `/v1/assets/{id}` toward
  off-chain venues. New `[trades].usd_pegged_classic_assets`
  config — operators list classic credits they trust as
  USD-pegged stablecoins (e.g. Circle's USDC-GA5...). On-chain
  trades quoted in any of those classics, OR in their SAC wrapper
  (transitive via `[supply.sac_wrappers]`), now populate
  `usd_volume = quote_amount / 10^7` at insert time. Empty
  allow-list preserves the pre-Phase-1 default. Phase 2 (FX-anchor
  multiplication for non-USD on-chain quotes — XLM/AQUA, XLM/BTC)
  is post-launch. The OpenAPI / storage / handler doc caveats
  on `volume_24h_usd` updated to reflect the operator-opt-in
  surface; the field stays forward-compatible (Phase 2 lands
  additively).

  New surface: `internal/storage/timescale.USDVolumeQuoteSpec` +
  `Store.SetUSDVolumeQuoteSpec`. Wired into both the indexer's
  live ingest path and the ops-binary backfill path so an
  operator-driven historical replay matches live behaviour.

### Fixed

- **`pkg/client.AssetDetail` was missing 15 documented wire
  fields**: the SDK consumer using `client.Asset()` deserialized
  into a struct that omitted `decimals`, `sep1_status`, all six
  SEP-1 overlay fields (`name`, `description`, `image`,
  `org_name`, `anchor_asset`, `anchor_asset_type`), all seven F2
  fields (`circulating_supply`, `total_supply`, `max_supply`,
  `market_cap_usd`, `fdv_usd`, `supply_basis`,
  `volume_24h_usd`), and the four SEP-1 issuance declarations
  (`conditions`, `fixed_number`, `max_number`, `is_unlimited`).
  Go's `encoding/json` silently ignores unknown fields by
  default, so consumers got zero-valued structs without warning
  — the only way to access the missing fields was dropping to
  raw HTTP. This was a real wallet-integrator gap (the F2 + SEP-1
  fields are exactly what asset-detail UIs need). Adding the
  fields is purely additive under SemVer (`pkg/client` is `v0.x`
  pre-release; the SDK contract pins backwards-compat from
  `v1.0.0`). Two new tests pin the JSON-decode contract and the
  `omitempty`-on-nil round-trip shape so a future regression
  fires before shipping.

- **`ratesengine-aggregator` log-level + log-format now match the
  other binaries**: the aggregator's bespoke logger factory was
  case-sensitive on the `[obs] log_level` value (so `LogLevel =
  "DEBUG"` silently fell back to info), missed the `"warning"`
  alias the indexer + api accept, and the LogFormat switch only
  recognised `"console"` (not `"text"`). Extracted the shared
  factory to `internal/obs.NewLogger(cfg, binaryName)` and pointed
  all three binaries at it. Side-effect: aggregator logs now also
  carry the `binary=ratesengine-aggregator` slog attribute, so
  Loki dashboards can filter per-binary without grepping path
  prefixes (the indexer + api already had this stamp).

### Added

- **Supply cross-check gauge wired into the aggregator's
  refresh loop (closes a half-shipped audit finding)**: the
  `ratesengine_supply_cross_check_divergence_stroops` gauge and
  the `ratesengine_supply_cross_check_total{outcome=…}` counter
  were declared in `internal/obs/metrics.go` and the supply alert
  in `deploy/monitoring/rules/supply.yml` referenced them, but no
  production code path emitted either — the alert was inert.
  Added `internal/supply.CrossCheckRefresher` (loads the latest
  classic + SAC snapshots per pair, runs `supply.CrossCheck`,
  emits the gauge + counter via a small `CrossCheckEmitter`
  interface) and wired it into `ratesengine-aggregator` alongside
  the per-asset supply refreshers. Pairs are derived from the ∩ of
  `[supply].sac_wrappers`, `watched_classic_assets`, and
  `watched_sep41_contracts` — no new config knob. Runbook
  `docs/operations/runbooks/supply-cross-check-divergence.md`
  flipped from `draft` to `living` and the manual-cron caveat is
  gone; metric doc comments lose the "not yet emitted" note. New
  outcome labels (`missing_snapshot`, `read_error`) surface the
  bootstrap state and transient-storage failures separately from
  genuine within/over divergence.

- **Blend WASM audit complete; `BackfillSafe` flipped → `true`
  (Task #53)**: the 5h4m wide-net wasm-history walk on r1
  finished 2026-05-02 and covered all 11 Blend contracts (9 pools
  + 1 backstop + 1 pool factory) over the verified-clean ledger
  range [50,457,424, 62,249,727]. Result: 3 unique WASM hashes
  observed across all 11 contracts, zero mid-life upgrades. The
  three hashes (pool `a41fc53d…`, backstop `c1f4502a…`, factory
  `31328050…`) match Phase 1's Soroban-RPC current-state query
  and have all been disassembled in Phase 3 with the decoder-
  expected event topics + AuctionData field names confirmed
  present. `internal/sources/external/registry.go` flips
  `blend.BackfillSafe` from `false` to `true`; `framework_test.go`
  moves `blend` from `wantUnsafe` to `wantSafe`. Audit doc
  `docs/operations/wasm-audits/blend.md` adopts the canonical
  per-contract findings table; filtered evidence preserved at
  `docs/operations/wasm-audits/evidence/blend/phase2-2026-05-02/`.

- **Per-trade `usd_volume` column populated at insert (partially
  closes launch-readiness L2.2)**: previously `InsertTrade` set
  `usd_volume = NULL` with a "filled by aggregator" comment that
  never got actioned, which silently zeroed
  `/v1/assets/{id}.volume_24h_usd` (the CAGG `prices_1m.volume_usd`
  is `sum(coalesce(usd_volume, 0))`). New
  `internal/storage/timescale/trades.go::tradeUSDVolume` populates
  the column when the source is off-chain (Subclass=CEX or FX, so
  amount is at the uniform 10⁸ decimal convention) AND the quote is
  fiat:USD or a USD-pegged stablecoin per `aggregate.FiatProxy`
  (USDC/USDT/DAI/PYUSD/USDP). For those trades the value is exact:
  `quote_amount / 1e8`, rendered as a fixed-precision NUMERIC
  string. Out-of-scope cases (on-chain DEX trades, EUR-quoted pairs,
  unknown sources, oracle-class sources) keep the column NULL — the
  CAGG's `coalesce(0)` makes that the right safe default. Tests
  cover both the populated path (binance + fiat:USD, polygon-forex
  + fiat:USD, kraken + USDC) and the NULL path (soroswap, EUR
  quote, unknown source, reflector, coingecko, zero amount). The
  remaining on-chain coverage (XLM/USDC trades from soroswap /
  aquarius / phoenix at per-source decimals) is a separate
  follow-up — same L2.2 row stays ⚠ because the
  on-chain path needs per-source decimal awareness that's its own
  design conversation.

- **Per-pair `aggregate.min_usd_volume` filter wired through the
  orchestrator (closes launch-readiness L2.1 caveat)**: the config
  knob existed in `internal/config/config.go` (`MinUSDVolume`,
  default 10_000) but no production code path consumed it. The
  re-baseline of `docs/architecture/launch-readiness-backlog.md`
  surfaced this as the L2.1 ⚠ caveat. This commit threads it through
  to `Config.MinUSDVolume` and adds a window-level filter step in
  `refreshPairWindow` between the per-trade outlier filter and the
  VWAP compute. When set > 0 AND the pair's quote is `fiat:USD`,
  the orchestrator sums each contributing trade's `quote_amount` /
  10⁸ (the uniform off-chain CEX/FX scale per
  `internal/sources/external/<venue>::externalAmountDecimals`) and
  drops the window if the sum is below threshold. Non-USD-quoted
  pairs are exempt because cross-decimal arithmetic across mixed
  on-chain/off-chain sources doesn't reduce to a clean single-USD
  figure; the dominant launch case (XLM/USD) is in scope. Skip path
  emits new
  `ratesengine_aggregator_dropped_windows_total{reason="min_usd_volume"}`
  + bumps the existing `empty_windows_total` so freshness alerts
  see consistent state. Filter is OFF when `MinUSDVolume == 0` —
  preserves pre-filter behaviour for deployments that haven't
  tuned the threshold yet. Tested: thin window rejected; fat
  window published; non-USD pair exempt; filter-off bypass.

- **`ratesengine-ops wasm-history-merge-jsonl` — recover from a
  crashed walk**: the existing `wasm-history -checkpoint-dir` flag
  has been writing per-worker JSONL transition logs since #185, but
  the matching merge tool that reconstructs the canonical JSON from
  those files was tracked in a comment as "(planned) or hand-stitch".
  This subcommand fills that gap. After the wide-net walk on r1 died
  at 5 h on 2026-05-01 (failed `-to` past the archive's frozen tip,
  see PR #368), we lost the in-memory state — the JSON only writes
  at end-of-run. Going forward, every multi-hour walk should pass
  `-checkpoint-dir`; if it crashes, recover with
  `ratesengine-ops wasm-history-merge-jsonl -checkpoint-dir <dir> -to N`.
  The merge logic mirrors the walker's end-of-run merge: per-contract
  sort by ledger, collapse adjacent same-hash transitions across
  worker boundaries, close the last range at `-to`. Half-written
  trailing lines (a crashed worker's last partial flush) are
  tolerated. Smoke-tested against the in-flight wide-net checkpoint
  dir on r1 — reconstructed 144 contracts from 273 transitions across
  8 worker JSONL files. Documented in
  `docs/operations/wasm-audits/README.md` §2.

- **Chaos suite Wave 1 (Task #75)**: ships `test/chaos/` with three
  failure-mode scenarios against the docker-compose dev stack —
  Redis container stop, Timescale container stop, Redis network
  partition. Pass criteria assert the API either degrades-with-flag
  or fails loudly with a structured envelope; never silently serves
  bad data. Bash-based to keep symmetry with the k6 load suite's
  external-tool harness shape (separate `test/load/` already uses
  k6 .js files); Go was considered but `exec.Command` boilerplate
  around `docker stop` / `pumba pause` would be longer than the
  bash equivalent. Production-safety guard duplicated at runner +
  scenario-prologue level: every script refuses to run against a
  target whose host matches `*production*` /
  `*api.ratesengine.net*` / `*prod.*`. Wave 2 (HA-shaped scenarios
  — Patroni replica promotion, Redis Sentinel failover, HAProxy +
  keepalived VIP flip) is gated on staging baremetal deploys and
  is deferred post-launch. Companion design note at
  `docs/architecture/chaos-suite-design-note.md`. Closes
  launch-readiness L5.5.

- **X2.5 forex-factor snap rule (Task #71)**: implements
  ADR-0018 §"Forex factor handling" so chained-fiat triangulation
  (e.g. XLM/EUR via XLM/USD × USD/EUR) preserves across-region
  consistency. For every fiat-vs-fiat leg of a configured
  triangulation chain, the orchestrator queries the most recent
  FX-source quote at-or-before the bucket-end timestamp instead of
  reading the leg's cached VWAP — every region serving the same
  closed bucket queries the same trades hypertable and gets the
  same row. Pre-snap behaviour was *almost* equivalent to the rule
  in steady state (region observation timing skew + multi-publish-
  per-bucket FX sources were the strict-compliance gap); the new
  path closes that gap and is the path ADR-0018 mandated.
  Storage primitive: `timescale.Store.FXQuoteAtOrBefore(pair,
  cutoff, fxSources)`. FX-source enumeration:
  `external.FXSources()` (deterministic lex order) +
  `external.IsFXSource(name)`. Orchestrator:
  `internal/aggregate/orchestrator/triangulate.go::legPrice`
  routes FX legs (both sides `AssetFiat`) to the snap path when
  `Config.FXStore` is wired. Snap misses
  (`timescale.ErrNoFXQuote`) fall back to the cached-VWAP path so
  chains stay published; new metric
  `ratesengine_aggregator_fx_snap_fallback_total{leg=…}` counts
  these. Alert
  `ratesengine_aggregator_fx_snap_fallback_dominant` fires at >50%
  fallback rate sustained for 30 m. Hard DB errors from the FX
  store skip publish for that tick (no chained-fiat output if we
  can't trust the FX leg). Wired by default in
  `cmd/ratesengine-aggregator/main.go` (passes the existing
  `*timescale.Store` as the `FXStore`); deployments without FX
  ingestion configured see no behavioural change because legs
  fall back uniformly. Companion runbook:
  `docs/operations/runbooks/aggregator-fx-snap-fallback-dominant.md`.

- **Loki + Promtail ansible role — CLOSES Task #72**: ships
  the fifth and final sub-role of #72 after Patroni (#344),
  Redis Sentinel (#350), HAProxy (#362), and Prometheus (#363).
  Single-host Loki running in single-binary mode per ha-plan §7
  ("Logs: Loki + Tempo" — singular, not paired). Chunks land in
  MinIO via S3 backend (reusing the galexie S3 deployment); index
  is local BoltDB. Promtail agents ship the systemd journal from
  every host in `log_shippers` (the union of every other
  inventory group: prometheus_pair / ratesengine_api / aggregator
  / indexer / haproxy_lb / redis_cluster / postgres_cluster).
  Single role file with two task surfaces — server tasks
  (`server-{01..05}.yml`) run on hosts in `log_aggregator`, agent
  tasks (`agent-{01..03}.yml`) on `log_shippers`. Versions
  pinned to upstream `v3.2.0` for both Loki and Promtail.
  Promtail labels every entry with `job=systemd` + `instance` +
  `unit` + `hostname` + `severity` for downstream filtering;
  drops a few low-signal units (`systemd-tmpfiles-clean`,
  `cron`, `systemd-logind`) as noise. 30d retention via Loki's
  compactor; reject-old-samples set to 7d to catch broken
  Promtail position files. Loki query API + Promtail HTTP
  endpoint both bound to internal addresses, with the firewall
  drop-in opening 3100 only on the internal CIDR. Companion
  design note at
  `docs/architecture/loki-ansible-role-design-note.md` covers
  the 1-host design choice (logs are forensic, not real-time-
  decision; HA scale-up path documented), the BoltDB-vs-TSDB
  index trade-off at this scale, and the failure-mode table
  (Promtail buffers up to 10k entries during Loki outage; new
  chunks fail with 429 if MinIO is down; etc.). After this PR,
  **Task #72 is fully closed** — all five sub-roles landed
  this session.

- **Prometheus + AlertManager ansible role (Task #72 sub-role)**:
  closes the fourth sub-role of #72 after Patroni (#344), Redis
  Sentinel (#350), and HAProxy (#362). 2-host Prometheus pair per
  `docs/architecture/ha-plan.md §7`; each host independently
  scrapes all targets (data duplication is the HA mechanism), and
  AlertManagers cluster via gossip on port 9094 to dedupe alerts
  before fanout. Seven task files (preflight with disk-space +
  time-sync + vault-warning checks, install via upstream tarballs
  pinned to `v2.54.1` / `v0.27.0`, prometheus-configure with
  inventory-driven scrape config + rule-file sync from
  `deploy/monitoring/rules/`, alertmanager-configure with
  PagerDuty/Slack routing, systemd, firewall, self-scrape
  monitoring), four templates (`prometheus.yml.j2` walks the
  inventory groups to build scrape configs, `alertmanager.yml.j2`
  with severity-based routing + inhibit rules, both systemd
  units). Ships with all 17 existing rule files
  (`aggregator/anomaly/api/archive-completeness/cache/divergence/
  infra/ingestion/meta/sla-probe/slo/stellar/storage/supply*/
  verify-archive`, ~1721 LoC) loaded via the rule-files-sync
  pass that also handles deletions (drops files no longer in
  repo). Three validation gates (`promtool check config`,
  `promtool check rules`, `amtool check-config`) run BEFORE any
  reload, so a malformed render never lands. Loopback-only
  bindings (`127.0.0.1:9090` + `:9093`); operators SSH-tunnel.
  Companion design note at
  `docs/architecture/prometheus-ansible-role-design-note.md`. After
  this PR, only Loki remains of Task #72's five sub-roles.

- **HAProxy ansible role + keepalived VRRP (Task #72 sub-role)**:
  closes the third launch-critical sub-role of #72 after Patroni
  (#344) and Redis Sentinel (#350). Two LB hosts share a
  floating VIP via keepalived VRRP, fronting the
  `ratesengine-api` pool with `/v1/readyz`-based health checks
  per `docs/architecture/ha-plan.md §3.1`. TLS terminates at the
  edge (HSTS on every response, Mozilla intermediate cipher
  suite); HAProxy's built-in Prometheus exporter is enabled on
  the loopback stats endpoint (`127.0.0.1:8404/metrics` — never
  exposed publicly). Seven task files (preflight with
  `net.ipv4.ip_nonlocal_bind=1` for VIP binding + a vrrp-
  password-length warning, install, haproxy-configure with
  `haproxy -c -f` validation, keepalived-configure, systemd
  hardening drop-in, firewall allowing 80/443 + VRRP from peer
  CIDRs, monitoring), three Jinja templates (haproxy.cfg,
  keepalived.conf, systemd-override). Health-check semantics:
  5s interval, 3 fails before drain, 2 successes before re-add
  (15s detection latency), 10s slowstart prevents cold pods
  from getting hammered after recovery. Failover RTOs:
  ≤3s for HAProxy host failure (keepalived VRRP), 1-4s for
  HAProxy process death (`chk_haproxy` track-script). Companion
  design note at
  `docs/architecture/haproxy-ansible-role-design-note.md` covers
  cloud VRRP gotchas (Hetzner multicast OK; AWS needs unicast
  peers), VIP-as-secondary-IP requirements, and the rolling
  vrrp-password rotation procedure. After this PR, two of five
  Task #72 sub-roles remain (Prometheus + Loki); the
  launch-critical HA path is complete (Patroni-driven Postgres
  failover + Sentinel-driven Redis failover + keepalived-driven
  api-tier failover + HAProxy-driven api-pod redirection).

- **`ratesengine-ops wasm-history` Tier 2 enhancements: storage-
  rotation + ContractCode-upload tracking**: opt-in observers
  that ride alongside the existing executable-hash transition
  walker. Closes the "wide-net" goal called out in
  `walker-investigation-2026-05-01.md`. Two new flags:
  - `-storage-rotations-out=PATH` — when set, every
    Created/Updated/Restored `ContractData` entry whose key is
    NOT `LedgerKeyContractInstance` (i.e. custom storage rows)
    is recorded for any watched contract. Catches admin
    storage flips like Soroswap factory's `set_pair_wasm`
    rotation that the wasm-history-only walker doesn't see.
    Output: `[{contract, changes: [{ledger, change_type,
    key_xdr_b64, key_hint, durability}]}]`. The `key_hint`
    field renders common SCVal key shapes (Symbol, Vec\[Symbol,
    ...\], U32, Bytes) as one-line summaries so an operator
    skimming the JSON can recognise patterns without round-
    tripping the base64-encoded XDR through a decoder.
  - `-code-uploads-out=PATH` — when set, every `ContractCode`
    Created/Restored event observed in the walked range is
    captured globally (not per-watched-contract; the upload is
    independent of which contract may later reference the
    hash). Output: `[{ledger, wasm_hash, size_bytes, change_type}]`.
    Updated changes are deliberately excluded — Soroban's
    ContractCode bytes are immutable, only TTL changes via
    Updated, so they're not real upload events.

  Both features are off by default; the existing wasm-history
  stdout shape is unchanged. Tests cover the positive paths +
  the inverse-filter on Instance keys + the entry-type
  short-circuit.

  Operational use: re-run `wasm-history` against the curated
  `configs/audit/wasm-walk-contracts.yaml` list with both flags
  set to capture the full picture in one pass — the wide-net
  walk plan from PR #359.

- **Redis Sentinel ansible role + go-redis FailoverClient
  migration (Task #72 sub-role)**: closes the second
  launch-critical sub-role of #72 (after Patroni #344).
  Implements the topology pinned by ADR-0024: 1 primary + 2
  replicas across 3 cache hosts, 3 co-located Sentinels with
  quorum=2, AOF every-second + RDB nightly persistence,
  failover RTO 15–30 s. Seven task files (preflight with
  THP/overcommit kernel tuning, install, redis-configure,
  sentinel-configure, systemd hardening drop-ins, firewall
  internal-only on 6379+26379, monitoring via redis_exporter
  +  textfile sentinel-state scraper), three Jinja templates
  (redis.conf, sentinel.conf, systemd-override), idempotent
  re-runs (consults Sentinel for the current primary; refuses
  to overwrite post-failover state when
  `redis_first_run_only=true`).
  New `internal/storage/redisclient` package centralises
  client construction: `Build(StorageConfig)` picks
  `redis.NewFailoverClient` when `redis_sentinel_addrs` is
  non-empty, falls back to `redis.NewClient` against
  `redis_addr` for dev / single-node, returns nil when both
  unset. Both `cmd/ratesengine-api` and
  `cmd/ratesengine-aggregator` now route through this builder
  and log `redis configured mode={sentinel|single|disabled}`
  at startup. New `redis_sentinel_addrs` + `redis_master_name`
  config fields with validate-time assertion that
  master_name is set when sentinel_addrs is non-empty.
  Companion docs: ha-plan §3.4 amended to remove the
  Cluster-vs-Sentinel contradiction (per the original tension
  ADR-0024 ratifies); `redis-master-down.md` runbook split
  into §A automatic-Sentinel-failover (now the default,
  15–30 s RTO) and §B manual-failover (the
  `redis-cli SENTINEL failover` escalation path), with
  Sentinel-aware diagnosis commands. The
  `ratesengine_redis_sentinel_primary` gauge — emitted every
  30 s by the role's textfile scraper — sums to 1 across hosts
  in steady-state and is the durable signal for split-brain
  detection.

- **k6 load test suite — Wave 4 (Task #74; weekly schedule —
  CLOSES Task #74)**: ships
  `.github/workflows/k6-weekly.yml` running the canonical
  `06-mixed-realistic.js` against staging every Sunday 02:00 UTC
  (off-peak so a legitimate latency regression isn't masked by
  routine staging traffic). Workflow dispatch supports running any
  single scenario by name for ad-hoc regression investigation. Run
  output flows to the existing Prometheus/Grafana stack via
  `--out experimental-prometheus-rw`; tagged with `run_id` +
  `run_attempt` so the run window is queryable from Grafana
  without guessing timestamps. Secrets required (configured in
  repo settings):
  - `K6_TARGET_STAGING` — staging API base URL (e.g. `https://api.staging.ratesengine.net/v1`)
  - `RATESENGINE_LOAD_API_KEY` — vault-minted load-test API key
  - `K6_PROMETHEUS_RW_SERVER_URL` — Prometheus remote-write endpoint
  After this PR, **Task #74 is closed** end-to-end (scaffold +
  every scenario + AlertManager silence + weekly schedule);
  Task #77 remains the operator action to publish the first
  monthly `sla-proof-YYYY-MM-DD.md` once the staging environment
  has the secrets configured.

- **k6 load test suite — Wave 3 (Task #74; spike + AlertManager
  silence)**: closes the scenario surface for Task #74 by adding
  `99-spike.js` — a 10× burst absorption test (100 → 1000 rps for
  30s, ramp-down, 2 min recovery observation). Pass criteria are
  intentionally permissive on latency mid-spike (the hand-wave
  explicit in the design note §Spike) but tight on error rate
  (< 0.5 %) and recovery (baseline p95 within 2 min of spike end).
  New `scenarios/lib/alertmanager.js` posts a silence to
  `${ALERTMANAGER_URL}/api/v2/silences` matching `APIHighLatencyP95`
  + `APIHighErrorRate` for a 10-min window covering the spike,
  removed in scenario teardown so a real post-run regression
  still pages. Helpers are no-ops when `ALERTMANAGER_URL` is
  unset (Make target prints a 10-second warning so the operator
  can manually silence). Adds `make test-load-spike`. After this
  PR, the only remaining Task #74 work is Wave 4 (GitHub Actions
  weekly schedule) — the actual SLA proof artefact (Task #77) is
  unblocked and ready for the operator's first staging run.

- **k6 load test suite — Wave 2 (Task #74; unblocks #77)**: lands
  the four scenarios that complete the canonical SLA proof.
  `03-history.js` (windowed + since-inception, 80/20 mix per
  customer telemetry), `04-batch.js` (batch-size-100 fan-out at
  50 rps), `05-streaming.js` (constant 200 SSE clients with
  first-event latency tracked via `sse_first_event_ms` Trend),
  and `06-mixed-realistic.js` — the canonical proof scenario
  running the design-note traffic blend (60% price / 15% batch /
  10% tip / 6% vwap / 4% history / 3% twap / 1% stream / 1%
  oracle) at 300 rps over a 10 min soak. Pass criteria align
  with Freighter SLA (p95 < 200 ms; p99 < 500 ms; 99.9 % success
  rate).
  Companion `docs/operations/sla-proof-template.md` is the
  canonical artefact shape for Task #77 — operator copies to
  `sla-proof-YYYY-MM-DD.md` after each canonical run, fills in
  the per-endpoint p95 / p99 / error-rate table from Prometheus,
  attaches Grafana snapshot links, and commits alongside the
  release. The most recent passing report is the proof Task #77
  closes against. Wave 3 (spike + AlertManager-silence) and
  Wave 4 (weekly schedule) follow as separate PRs.

- **k6 load test suite — Wave 1 scaffold (Task #74)**: lays the
  foundation for the Freighter SLA proof (Task #77). New
  `test/load/` tree with `scenarios/lib/{env,pairs,thresholds,warmup}.js`
  shared helpers, the first two scenarios (`01-price-hot-path.js`,
  `02-vwap-twap.js`), `docker-compose.k6.yaml` runner, package
  doc.go for `go doc` visibility, and `reports/` (gitignored) for
  per-run artefacts. Makefile gains `test-load`, `test-load-mixed`,
  `test-load-price`, `test-load-vwap`, and `test-load-check`
  (compile-check without running) — every target is gated by a
  production-target guard that refuses to run if `K6_TARGET`
  resolves to `api.ratesengine.{net,io}` or `rates.stellar.org`.
  The same guard fires inside `scenarios/lib/env.js` so a direct
  `k6 run` cannot bypass it. Companion design note at
  `docs/architecture/k6-load-tests-design-note.md` (lays out the
  remaining waves: 03/04/05 scenarios, mixed-realistic proof,
  spike + AlertManager-silence integration, weekly schedule).
  Wave 1 unblocks ad-hoc operator runs against staging today;
  Task #77 closes once Wave 2's `06-mixed-realistic.js` passes
  end-to-end.

- **Patroni ansible role (#344)**: closes the launch-critical
  sub-role of Task #72. Implements the topology pinned in
  `ha-plan.md §3.3` — 1 primary + 2 synchronous replicas across
  3 hosts, 3-node etcd quorum (DCS), `synchronous_commit=remote_apply`,
  `synchronous_standby_names='ANY 1 (db-02, db-03)'`. Eleven
  task files (preflight, etcd install/configure/systemd, Patroni
  install/configure/systemd, bootstrap, replica join, firewall,
  monitoring), four templates (etcd.conf, etcd.service,
  patroni.yml, patroni.service), idempotent re-runs (detects
  existing cluster via Patroni REST `/cluster` endpoint, refuses
  to overwrite live config when `patroni_first_run_only=true`),
  pgBackRest restore-from-backup path for DR rebuilds. Companion
  design note at `docs/architecture/patroni-ansible-role-design-note.md`.
  **Effect on the launch-readiness picture:**
  `timescale-primary-down.md` Mitigation §A ("Automatic Patroni
  failover — the happy path") is now the actual default rather
  than aspirational; SEV-1 failover RTO drops from ~15 min
  (manual) to ~60 s (Patroni-driven). The drill scenario's
  Validation criterion #6 ("Did anyone reference Patroni hasn't
  landed?") becomes obsolete.

- **ADR-0024 — Redis HA via Sentinel (#343)**: ratifies the
  Redis HA mode choice. `ha-plan.md §3.4` had a contradictory
  description ("3 masters + 3 replicas, Redis-Cluster mode...
  3 sentinels for failover vote") — Cluster and Sentinel are
  different HA modes; the original phrasing combined them. ADR
  pins **Sentinel** for our scale (small hot-set, simpler ops,
  uniform `go-redis/v9 FailoverClient` integration without an
  HAProxy in front of Redis). Notes that ha-plan.md §3.4 should
  be amended for terminological consistency in the same PR
  that ships the Redis Sentinel ansible role (Task #72 sub-role).

- **`status-page-hosting-comparison.md` tracked (#343)**:
  decision-support doc surveying 6 status-page options against
  `sev-playbook.md §5.1`'s requirements. Recommends **Instatus**
  (free tier covers launch volume; modern UI; bring-your-own
  incident-management since we have PagerDuty). Fallback:
  Cachet self-hosted. Closes the design gap on Task #73 — the
  remaining work is half-a-day of vendor wiring once a vendor
  is picked.

### Added

- **`internal/sources/sep41_supply` (event-stream Algorithm 3
  decoder) now registers with the indexer dispatcher — closes
  L2.12a 6/6.** New `dispatcher.AddDecoder` method (mirroring the
  existing `Add{Op,ContractCall,Entry}Decoder` siblings) and a
  new `pipeline.RegisterSupplyEventDecoders` helper that attaches
  the sep41_supply decoder when `[supply] watched_sep41_contracts`
  is non-empty. The Algorithm 3 mint/burn/clawback running sums
  start landing in `sep41_supply_events` per ledger close. Indexer
  main.go calls both supply-registration helpers (entry +  event)
  and merges the registered observer list for the boot log.
  Closes the wiring gap flagged in #410: the supply pipeline
  (Algorithms 1 + 2 + 3) is now fully end-to-end live in
  production for opted-in deployments.

- **Classic-asset supply observers (trustlines / claimable_balances /
  liquidity_pools / sac_balances) now register with the indexer
  dispatcher.** Second slice of the L2.12a six-observer wiring
  sweep — closes Algorithm 2 (classic credit-asset supply) for
  every component except the SEP-41 event stream. Builds on
  `pipeline.RegisterSupplyEntryDecoders` from the previous PR;
  three new conditional registrations:
    - `[supply] watched_classic_assets` non-empty → trustlines,
      claimable_balances, AND liquidity_pools all attach (an
      operator who watches an asset MUST get every component or
      Algorithm 2's sum is wrong);
    - `[supply.sac_wrappers]` non-empty → sac_balances attaches
      independently (cross-check-only deployments don't need the
      classic trio).
  Boot log now reports the three watched-set sizes alongside the
  registered observer list. Empty per-observer watched-set leaves
  that observer unregistered — no behaviour change for
  deployments that haven't opted in. New
  `internal/pipeline/dispatcher_test.go::TestRegisterSupplyEntryDecoders_*`
  sub-tests pin the classic-trio attachment, the SAC-only path,
  and the all-five full-config path.

- **`internal/sources/accounts` (LCM AccountEntry observer) is now
  registered with the indexer dispatcher.** First slice of the
  L2.12a six-observer wiring sweep (the supply observers compiled
  and had unit tests but no production code path called
  `disp.AddEntryDecoder` for any of them; the supply pipeline
  consequently read empty hypertables in production despite the
  algorithms being correct). New
  `pipeline.RegisterSupplyEntryDecoders(disp, cfg.Supply)`
  attaches each opt-in observer based on the corresponding
  watched-set:
    - `accounts` ← `[supply] sdf_reserve_accounts` (this PR);
    - trustlines / claimable_balances / liquidity_pools / sac_balances
      / sep41_supply — follow-up PRs.
  The watched-set itself is the on/off switch — empty list leaves
  the observer unregistered, no behaviour change for deployments
  that haven't opted in. Empty G-strkey inside a non-empty list
  fails-loud at startup so an operator sees the misconfiguration
  before processing begins. Boot log emits the registered set so
  operators see which observers are live without consulting
  config. New
  `internal/pipeline/dispatcher_test.go::TestRegisterSupplyEntryDecoders_*`
  pins the no-op-when-empty / registers-when-watched / rejects-
  empty-strkey transitions. The persistence side
  (`internal/pipeline/sink.go`) was already wired for this
  observer's Observation type, so once it registers,
  `account_observations` rows start landing on every matching
  ledger close.

### Fixed

- **`ratesengine_oracle_resolution_seconds` is now actually
  emitted.** The metric was registered in `internal/obs` and the
  `ratesengine_oracle_stale` alert
  (`deploy/monitoring/rules/divergence.yml`) depends on it — the
  expression is
  `(time() - oracle_last_update_unix) > 10 * oracle_resolution_seconds`.
  The denominator was never set in production, so Prometheus's
  missing-metric semantics meant the alert either evaluated `>0`
  (always fired once a single update landed) or stayed
  unevaluatable depending on operator scrape config — neither was
  the intended behaviour. `pipeline.BuildDispatcher` now sets the
  gauge per oracle source at registration time, using each
  source's published `DefaultResolutionSeconds` constant:
  reflector-{dex,cex,fx} = 300 s (5 min), redstone = 86400 s
  (24 h), band = 60 s (1 min). The metric label is `source`, so
  each reflector variant gets its own gauge entry. Same
  audit-finding shape as the supply cross-check / trace_exporter
  / cdn_enabled gaps — alert + metric defined but never emitted
  by production code.

- **CORS default AllowedMethods includes POST** — the default
  was set when v1 was a read-only API and never updated as POST
  endpoints landed (`/v1/account/keys`, `/v1/auth/sep10/token`,
  `/v1/price/batch`). The API binary's `CORS(CORSOptions{
  AllowedOrigins: cfg.API.AllowedOrigins})` shorthand was
  silently failing browser cross-origin POST preflights;
  operators had to override `AllowedMethods` explicitly to make
  a wallet-side POST work. New default:
  `{GET, HEAD, OPTIONS, POST}`. Operators who want a stricter
  cross-origin posture set the field explicitly. The doc tag
  on `CORSOptions.AllowedMethods` is updated to match the v1
  surface. New `TestCORS_DefaultAllowedMethodsIncludePOST` pins
  the default-preflight-allows-POST behaviour.

- **Aggregator binary now exposes `/metrics`** — closes a known
  gap surfaced by the half-shipped-config audit. The aggregator's
  Prometheus counters (`ratesengine_aggregator_ticks_total`,
  `_vwap_writes_total`, `_empty_windows_total`,
  `_dropped_trades_total{reason}`, `_triangulations_total{outcome}`)
  registered into `internal/obs` at package init but no HTTP
  listener was mounted, so Prometheus scrapes returned 404 and
  the alert rules in `deploy/monitoring/rules/aggregator.yml`
  (`aggregator_silent`, `aggregator_outlier_storm`,
  `aggregator_class_drop_spike`) could never fire.
  `cmd/ratesengine-aggregator/main.go` now mirrors the indexer's
  `startMetricsServer` pattern: bind `cfg.Obs.MetricsListen`,
  expose `GET /metrics` (Prometheus) + `GET /healthz`, and run
  graceful shutdown after `orch.Run` returns. Empty
  `MetricsListen` logs a warning calling out which alerts won't
  fire — same shape as the indexer warning. The `ObsConfig`
  package doc is updated to drop the "known gap" caveat.

- **`obs.trace_exporter = "otlp"` now fails-loud instead of
  silently no-op'ing** — the fourth half-shipped config field
  caught by the audit-finding wire-up pattern (after F-0008
  `key_rate_limit_per_min` in #384, F-0009 `trusted_proxy_cidrs`,
  and `api.cdn_enabled` in the previous commit). The struct
  field, default (`"none"`), TOML example (`# none | otlp`), and
  validation (`switch o.TraceExporter { case "none", "otlp": }`)
  all advertised OTLP as a working option, but no production code
  imports the OpenTelemetry SDK or sets up a `TracerProvider` —
  any operator who set `trace_exporter = "otlp"` got zero traces
  with no error or warning. Validate() now rejects `"otlp"` with
  a message pointing operators to the truth ("reserved for the
  future tracing rollout and is not yet wired in this build; set
  to \"none\""). When the OTel exporter is wired in
  `cmd/ratesengine-{api,indexer,aggregator}/main.go`, the
  validation case is restored. The doc tag on `Obs.TraceExporter`
  + `Obs.TraceSample` and the `[obs]` block in
  `configs/example.toml` now state the ship truth so the
  auto-generated reference at `docs/reference/config/README.md`
  matches reality. New `validate_test.go` row exercises the
  reject path. No operator code change required for the default
  config (`trace_exporter = "none"` is unchanged).

- **`api.cdn_enabled` now actually gates `s-maxage`** — the third
  half-shipped config field caught by the audit-finding wire-up
  pattern (after F-0008 `key_rate_limit_per_min` in #384 and F-0009
  `trusted_proxy_cidrs` review). `internal/config/config.go` has
  exposed `cfg.API.CDNEnabled` (default `true`) since the early API
  surface design, but `internal/api/v1/server.go` mounted the
  middleware as bare `middleware.CacheControl` — the operator-facing
  knob compiled, defaulted, and was logged, but had no runtime
  effect. New `middleware.CacheControlWithCDN(cdnEnabled bool)`
  constructor: when `false`, drops the `s-maxage` half from cacheable
  routes (`public, max-age=30, s-maxage=60` → `public, max-age=30`
  for current-price / asset-detail; `public, max-age=60, s-maxage=300`
  → `public, max-age=60` for closed-bucket historical). Non-cacheable
  directives (`no-store`, `private, no-cache, must-revalidate`) are
  unchanged because they were never CDN-cacheable. The legacy
  `middleware.CacheControl` symbol is kept as a backwards-compat
  shim that forwards to `CacheControlWithCDN(true)` so test sites
  and any external caller that imported the function don't break.
  `v1.Options.CDNEnabled` plumbs the config into the server;
  `cmd/ratesengine-api/main.go` passes `cfg.API.CDNEnabled` at
  construction. New tests: `TestPolicyForPath_CDNDisabled` (18-row
  matrix verifying the s-maxage drop on every cacheable route) and
  `TestCacheControlWithCDN_FalseDropsSMaxAge` (handler-side
  end-to-end). Operators without a CDN in front of the API set
  `cdn_enabled = false` and the API stops emitting a directive a
  CDN they don't run could later honour.

- **Divergence service is now wired with references by default**:
  the API binary (`cmd/ratesengine-api`) was constructing
  `divergence.NewService` with an empty `References` list, leaving
  the `divergence_warning` envelope flag inert in production —
  surfaced by a 2026-05-01 review pointing at S9.4 of the coverage
  matrix. New `[divergence]` config block in `internal/config/config.go`
  + `cmd/ratesengine-api/main.go` `buildDivergenceReferences()`
  helper. CoinGecko reference is on by default (free tier, no
  auth required) so divergence detection fires out of the box.
  Chainlink reference is opt-in via
  `[divergence.chainlink].enabled = true` plus a non-empty
  `feeds` table mapping pair strings to mainnet AggregatorV3
  feed addresses. `Threshold`, `MinSourcesForWarning`, and
  `PerReferenceTimeoutSeconds` are also surfaced for operator
  control. New tests
  (`cmd/ratesengine-api/main_test.go::TestBuildDivergenceReferences_*`)
  cover the four wiring permutations: defaults / both-enabled /
  Chainlink-enabled-but-empty-FeedMap-skip / all-disabled.
  Boot log now emits
  `divergence service wired reference_count=N references=[...] threshold_pct=...`
  so operators can confirm the active set at startup.

- **Public-flip checklist is 16/16 verified (#342)**: the two
  rows in `docs/operations/public-flip.md` that required
  human-in-the-loop review (the `CLAUDE.md` private-archive
  check and the `docs/discovery/` sensitive-content check) are
  now ☑ with citations. CLAUDE.md got a pattern scan + manual
  spot-checks — 0 private references, 2 non-blocking editorial
  recs noted; `docs/discovery/` got a 9-pattern sensitivity scan
  across all 48 files — 0 hits in credential/PII categories,
  6 benign hits across qualitative categories. Task #78 moves
  from "checklist incomplete" to "execution-ready" — what's
  left is the operator-side cut-over mechanics in
  `public-flip.md §"Cut-over mechanics"` (gh repo create, DNS,
  branch protection, secrets re-create).

- **`api-docs` workflow disabled until public-flip (#262)**: the
  `api-docs` workflow's final step `actions/deploy-pages` requires
  GitHub Pages enabled on the repo, which only happens at
  public-flip time. Until then every push to `main` ran this
  workflow, which always failed at the deploy step (verified
  across 5 consecutive main pushes 2026-04-29 / 2026-04-30) —
  pure CI waste. Switched the trigger to `workflow_dispatch:`
  only with an inline comment naming Task #78 as the
  re-enablement cutover. Re-enable the push trigger as part of
  the public-flip per `docs/operations/public-flip.md §Post-flip`.

- **Coverage matrix: re-baseline the Open list (#340)**: Task #50
  re-baselined the upper per-section rows today, but the
  *Open — implementation pending* summary table at the bottom of
  `docs/architecture/coverage-matrix.md` still listed twenty-one
  items as pending that had actually shipped (S4.1-4 VWAP/TWAP,
  S8.1-2 USD volume + FX, F2.4 circulating-supply, S3.7 CEX
  connectors, S2.4 Chainlink HTTP, S1.4 asset enumeration, X2.2
  /v1/price/tip, X2.3 /v1/observations, X2.6 streaming ×4, X3.1-7
  anomaly + baseline + freeze, F5.3 batch endpoint, #2 SEP-10,
  #9 pkg/client SDK, #10 docs-api pipeline, #24 internal/divergence,
  X1.5 archive-completeness daemon, X1.7 verify-archive
  -fail-on-missed, #21 CHANGELOG + SemVer, #23 release-notes
  template, #26 envelope flag retrofit). All twenty-one verified
  against the current `internal/`, `cmd/`, and `pkg/` tree, then
  moved to *Closed since Phase 1* with the file paths cited as
  evidence. The Open list now contains the **eight items that
  are genuinely outstanding before launch** (X2.5 forex snap;
  Patroni/Redis/HAProxy/Prom/Loki ansible roles; public status
  page; k6 load tests; chaos suite; SEV dry-run record; p95
  proof; public-flip checklist) plus the in-flight Task #53
  Blend Phase 2. Massive accuracy improvement for understanding
  what's actually left before the 2026-06-30 launch.

### Added

- **SEV drill scenarios + framework (#341)**: Coverage matrix
  Validation #20 ("SEV-1/SEV-2 dry-run") needs scripted scenarios
  to exercise the playbook. The sev-playbook.md §8 already
  promised a `docs/operations/drills/` directory holding writeups
  per drill; that directory didn't exist. New:
  `docs/operations/drills/README.md` describing the three-tier
  cadence (monthly tabletop / quarterly chaos / annual DR), the
  drill protocol, and a writeup template; `scenarios/`
  subdirectory holding two canonical tabletop scripts —
  `sev1-timescale-primary-failover.md` (primary disk-full →
  failover decision; exercises `timescale-primary-down.md`) and
  `sev2-source-decoder-regression.md` (post-protocol-upgrade
  soroswap decode errors; exercises `decode-errors.md`). Each
  scenario carries Initial conditions, Trigger event, Injection
  timeline (per-minute beats), Expected response per the
  playbook, Validation criteria (pass/partial/fail per row), and
  Common gaps surfaced from prior runs. Operator-executable: the
  next monthly tabletop has scripts to run.

- **Blend WASM audit — Phase 1 + partial Phase 3 (#339)**: Task
  #53 advances substantively without r1 access. Pool Factory's
  9 lifetime `Symbol("deploy")` events fetched via
  stellar.expert API, bodies decoded with `scripts/dev/decode-scval`,
  yielding the canonical list of all 9 Blend pool addresses with
  deploy timestamps (2025-04-14 → 2025-11-25). Current WASM hash
  for every pool fetched via `/explorer/public/contract` API:
  all nine share `a41fc53d6753b6c04eb15b021c55052366a4c8e0e21bc72700f461264ec1350e`.
  WASM bytes downloaded with `stellar contract fetch --network
  mainnet` (57 KiB) and archived as audit evidence at
  `docs/operations/wasm-audits/evidence/blend/`. Decoder-
  compatibility verified: `strings` finds all three event topics
  (`new_auction`, `fill_auction`, `delete_auction`) and all three
  AuctionData field names (`bid`, `lot`, `block`) the
  `internal/sources/blend` decoder switches on; `stellar contract
  info interface` confirms canonical Blend pool surface. **Phase
  2 (per-pool `wasm-history` walk on r1) still required** before
  the `BackfillSafe: false → true` flip — current Phase-3 read
  shows only the *current* WASM, not whether any pool was
  upgraded mid-life. Audit doc updated with both phases'
  results; status moves from "Pending" to "Phase 1 complete;
  Phase 3 partial".

- **CI promtool validation (#319)**: `make monitoring-check`
  (Prometheus rule-file validation via `promtool check rules`)
  is now wired into both `bash scripts/dev/verify.sh` (graceful-
  skip when promtool isn't installed locally) and a dedicated
  `monitoring-rules` job in `.github/workflows/ci.yml` (installs
  promtool from the Prometheus GH release). The alert rules
  shipped in #294 / #295 / #313 had been validated by visual
  review only — broken PromQL or undefined recording-rule
  references would have surfaced at Prometheus reload time on
  a production node. Closes the gap.

- **`CLAUDE.md` "Add a new supply observer" recipe (#323)**: the
  six supply observers shipped through Tasks #54–#56 follow a
  pattern (`doc.go` + `dispatcher_adapter.go`, three possible
  dispatcher hooks per the LCM/Op/Event split) that has no entry
  in the task-recipe section of the agent orientation file. A
  future agent adding a 7th observer would have nowhere to look.
  Recipe links to `docs/architecture/supply-pipeline.md` for the
  full shape and names the three hook variants explicitly.

### Fixed

- **Supply-refresh runbooks acknowledge `asset_key` label (#320)**:
  PR #314 added the `asset_key` dimension to
  `ratesengine_aggregator_supply_refresh_total`, but the
  `supply-refresh-error-dominant.md` runbook still claimed
  *"Logs carry asset key; metric doesn't"* — operators following
  the runbook would skip a useful per-asset diagnosis path and go
  straight to journald. Both supply-refresh runbooks now show how
  to split by `asset_key` from `/metrics` (and the equivalent
  PromQL for dashboards).

- **Stellar runbooks acknowledge inert metrics (#321)**: four
  alerts in `deploy/monitoring/rules/stellar.yml` (`core-lag`,
  `core-peers`, `rpc-lag`, `archive-publish`) reference metrics
  produced by stellar-core / stellar-rpc / the
  stellar-core-prometheus-exporter — all three were removed from
  r1 on 2026-04-23. Each runbook now opens with a *Deployment
  posture* callout explaining the alert is inert on r1 today and
  retained for Phase-3 (Tier-1 validator rollout per ADR-0004).
  Alerts catalog gets a matching note above the Stellar / node
  alerts section.

- **Ingestion runbooks point `rpc-probe` at an external endpoint
  (#322)**: six runbooks (`all-ingestion-down`, `decode-errors`,
  `insert-errors`, `oracle-stale`, `source-stopped`,
  `orphan-events`) instructed on-call to run probes against
  `http://stellar-rpc:8000` — but stellar-rpc was removed from r1
  on 2026-04-23, so the URL no longer resolves on-box. Each now
  points the probe at a public stellar-rpc endpoint
  (`https://mainnet.sorobanrpc.com`) with a one-line note
  explaining the architectural shift. The `all-ingestion-down`
  runbook additionally rewrites quick-diagnosis + Mitigation A
  around Galexie + MinIO (the actual r1 upstream).

- **API runbooks now cover SLO burn-rate alerts (#324)**: PR #313
  shipped six SLO burn-rate alerts (`slo_latency_burn_*`,
  `slo_availability_burn_*`) per ADR-0009, all routing to
  `api-latency.md` / `api-5xx.md` — but neither runbook
  acknowledged the new alerts or explained the burn-rate-vs-
  direct-threshold distinction. An on-call operator paging from
  `slo_availability_burn_fast` would land in `api-5xx.md`, see
  only the direct-threshold alerts, and potentially dismiss the
  page as "p95 just nudged a line" rather than "we're burning
  the 99.99 % SLO budget at 14.4× rate." Both runbooks now list
  the burn-rate variants and explain the multi-window pattern.

- **`supply-snapshot-stale` runbook acknowledges the
  aggregator-resident path (#325)**: PR #318 established that
  `asset_supply_history` has two producers (systemd timer +
  aggregator-resident goroutine), but the
  `ratesengine_supply_snapshot_stale` alert tracks only the
  timer-path's `last_success_timestamp` gauge. Deployments
  running exclusively the goroutine path would have this alert
  firing forever despite fresh snapshots landing. New top-of-
  file callout names both paths and points operators at the
  silence-vs-investigate decision.

- **Two more supply-snapshot runbooks acknowledge the
  aggregator-resident path (#326)**: companion to #325. Both
  `supply-snapshot-circulating-zero.md` and
  `supply-snapshot-unit-failed.md` track metrics emitted
  exclusively by the systemd-timer path, so on a goroutine-only
  deployment these alerts silently never fire — a worse failure
  mode than the noisy false-positive in #325. Each carries a
  *Coverage caveat* callout naming the two-path architecture and
  the goroutine-path equivalent signal.

- **`supply-cross-check-divergence` runbook cross-links sibling
  supply alerts (#327)**: the runbook only referenced
  `aggregator-silent.md` and `internal/supply/crosscheck.go`
  under Related, missing four cross-references that an operator
  triaging a divergence routinely needs.

- **`cursor-stuck` runbook upstream is Galexie + MinIO, not
  stellar-rpc (#328)**: the runbook's Mitigation step 1 told
  on-call to *"fix the upstream (stellar-rpc) first"* — but
  stellar-rpc was removed from r1 and the indexer reads ledger
  metadata from Galexie's MinIO output. Mitigation step now
  points at Galexie / MinIO checks.

- **`archive-divergence` runbook deployment-posture callout
  (#329)**: the runbook treated us as an active archive publisher
  ("stop advertising the affected checkpoints", "core-binary bug
  producing a different bucket") — but r1's
  `/srv/history-archive/` is a one-shot stellar-archivist mirror
  with no running publisher since 2026-04-23. Top-of-file callout
  scopes the runbook to "what r1 can actually do today" with
  Phase-3 framing for the publishing path.

- **`host-cpu-high` runbook captive-core context is galexie, not
  stellar-rpc / stellar-core (#330)**: root cause #2 named
  "stellar-rpc or stellar-core host" as the captive-core sites,
  but on r1 today only galexie embeds a captive-core. Galexie
  also doesn't expose `/info`, so the end-state signal changes
  accordingly.

- **`CONTRIBUTING.md` recommends the canonical pre-push gate
  (#331)**: contributors were told to run `make lint && make
  test` before pushing, but the canonical pre-push gate is `make
  verify` — which additionally runs doc-lint, import-lint,
  openapi-url-lint, the integration-build smoke check, and the
  Prometheus rule-file validation wired in #319. First-time
  setup, the workflow's pre-push step, and the Definition of
  Done now all reference `make verify`.

- **`deploy/monitoring/README.md` lists every rule file (#332)**:
  the layout list named 9 rule files but the directory holds 17.
  The `component` label list (used for dashboard grouping) was
  similarly missing four values that alert rules already use
  today. Pure documentation change; alert rules untouched.

- **`configs/ansible/README.md` reflects current default tag set
  (#333)**: the README opened with *"a fully configured Stellar
  archival node running stellar-core, Galexie, stellar-rpc, and
  Postgres 15"* — but `defaults/main.yml` has
  `run_stellar_core: false` and `run_stellar_rpc: false` since
  2026-04-23. A fresh-host bring-up gets Galexie + Postgres +
  MinIO and not the two daemons. Opening summary, first-run-
  bootstrap tag list, the role-overview section, and the running-
  a-subset examples all updated.

- **`deploy/docker-compose/README.md` migration version reference
  (#334)**: README told first-run contributors to expect
  *"migrated to version 8 (dirty=false)"* but the latest
  migration is `0015_create_sep41_supply_events`. Updated to 15
  with a self-correcting hint pointing at
  `ls migrations/*.up.sql | sort | tail -1`.

- **`migrations/README.md` lists every migration (#335)**: the
  Current-migrations table only listed 0001-0004 — eleven
  migrations had shipped without README updates. Each new row
  cites its ADR (0011 / 0021 / 0022 / 0023 / 0019) and the role
  the table plays in the algorithm map.

- **Source READMEs name the dispatcher seam, not the legacy
  `consumer.Source` (#336)**: three source READMEs (`soroswap`,
  `phoenix`, `reflector`) described their `consumer.go` as
  *"implements `consumer.Source`"* — but production routing has
  been via `dispatcher.Decoder` for a while; the only remaining
  consumer of `consumer.Source` is the legacy orchestrator's own
  test file. Future agents reading these would be told to follow
  the legacy pattern that CLAUDE.md invariant #6 explicitly
  forbids.

- **`configs/example.toml` `[stellar]` section explains the
  localhost defaults (#337)**: `core_http_endpoint` and
  `rpc_endpoints` defaulted to `http://127.0.0.1:...` — the right
  values for a Phase-3 archival host running stellar-core +
  stellar-rpc locally, but neither daemon runs on r1. An r1
  operator copying the example would have a config that silently
  drives every diagnostic into a refused-connection error. New
  top-of-section comment names the two postures and points r1
  operators at `https://mainnet.sorobanrpc.com`.

### Added

- **`docs/architecture/supply-pipeline.md` (#318)**: architecture-
  level overview tying together the three-algorithm supply
  derivation, the six observers, the chained-fallback reader
  pattern, the two refresh paths (systemd timer + aggregator
  goroutine), the per-class storage tables, and the failure-mode
  catalog. Mirrors the existing `ingest-pipeline.md` for the
  ingest side. ADRs 0011 / 0021 / 0022 / 0023 each cover one
  slice; the coverage matrix lists rows; this doc is the
  single-source orientation for someone arriving cold.

- **Classic-supply storage integration tests (#317)**: companion
  to #316 covering the four classic-supply hypertables shipped in
  #303 (`trustline_observations`, `claimable_observations`,
  `lp_reserve_observations`, `sac_balance_observations`). Each
  Sum*AtOrBefore method uses the same `DISTINCT ON (...) ORDER BY
  ledger DESC` + `WHERE NOT is_removal` pattern; a SQL regression
  in the DISTINCT ON ordering or the is_removal filter silently
  mis-reports Algorithm 2 components. Four sub-tests walk a
  realistic per-component lifecycle (insert → upsert →
  later-ledger advance → removal) and verify (1) at-or-before
  ledger filter, (2) last-writer-wins semantics, (3) removed-row
  exclusion from sums, (4) `asset_key` WHERE-filter isolation
  across watched assets, (5) per-account / per-contract latest-
  balance lookups for `LockedSet` / issuer-balance use cases.

- **SEP-41 supply storage integration tests (#316)**: covers the
  `Insert → SEP41NetMintAtOrBefore → SEP41KindTotalsAtOrBefore`
  paths through real TimescaleDB via testcontainers-go. The
  Algorithm 3 running sum's SQL (CASE-WHEN sign-flip for
  burn/clawback, FILTER (WHERE event_kind=...) per-kind
  aggregations, contract_id isolation) ships untested at the SQL
  level until this PR — Go-layer defensive guards in #309 catch
  invalid inputs but can't detect a SQL regression that silently
  corrupts the running sum. Two test scenarios: (1) round-trip
  with mint + burn + clawback at different ledgers, verifying
  the sign-flip is correct, the at-or-before filter respects the
  ledger bound, kind-totals split cleanly, and contract_id
  isolation works; (2) i128/NUMERIC round-trip preserves
  precision for values exceeding int64.

- **Coverage-matrix re-baseline (#315)**: walks rows that had drifted
  to "designed / pending" but actually shipped this session.
  Concrete row updates:

  | Row | Was | Now |
  |-----|-----|-----|
  | S2.4 Chainlink | ❌ gap | ✅ verified (#282) |
  | S5.2 ≤30s freshness | 🧪 designed | ✅ verified (#283/#290/#294) |
  | S9.2 p95/p99 latency | 🧪 designed | ✅ verified (synthetic via SLA probe) |
  | S9.1 ≥99.99% uptime | 🧪 designed | ⚠ caveat (probe shipped; production traffic needed for full verification) |
  | F2.1/F2.2 Market Cap / FDV | ⚠ writer pending | ✅ verified (writer end-to-end across XLM + classic + SEP-41) |
  | F2.4 Circulating Supply | ⚠ writer pending | ✅ verified (all three algorithms live) |
  | F2.5 Total Supply | ⚠ writer pending | ✅ verified (mint − burn − clawback live for SEP-41; classic via component sum) |
  | F2.6 Max Supply | ⚠ caveat | ✅ verified (overlay policy + null-for-uncapped per ADR-0011) |
  | F3.1 API p95 | 🧪 designed | ✅ verified (probe + alert) |
  | F3.2 API p99 | 🧪 designed | ✅ verified (umbrella alert) |
  | F3.3 Responsiveness ≥99.9% | 🧪 designed | ⚠ caveat (synthetic + HA topology backs it) |
  | F3.4 Freshness | 🧪 designed | ✅ verified (probe + alert) |

  S3.6 Blend stays ⚠ caveat (audit pending Task #53 — blocked on r1).

- **`asset_key` label on the supply-refresh metric (#314)**:
  extends `ratesengine_aggregator_supply_refresh_total` from
  `(outcome)` to `(asset_key, outcome)` so operators with
  multiple watched assets can chart per-asset bootstrap progress
  + isolate failure modes per asset. Existing alerts in
  `deploy/monitoring/rules/supply-refresh.yml` (#313) are
  forward-compatible — `sum(rate(...))` and `max(timestamp(...))`
  both sum/max over the new label naturally. New
  `supplyRefresherBinding` struct in
  `cmd/ratesengine-aggregator/main.go` pairs the `Refresher`
  with its asset_key at goroutine-construction time;
  `runSupplyRefresh` labels the metric per-tick. Metrics
  reference doc updated.

- **Aggregator supply-refresh alert rules + two runbooks
  (#313)**: closes the operator-visibility gap on the goroutine
  path of the supply refresher (#301 / #307 / #312). Pairs with
  the systemd-timer-path alerts in #295. Two rules in
  `deploy/monitoring/rules/supply-refresh.yml`:
  `_stalled` (P2 page when no `outcome="ok"` increments in
  30 min — wedged goroutine or every-tick-failing) and
  `_error_dominant` (P3 ticket when > 50% of ticks have
  non-ok outcomes for 30 min — split-by-outcome runbook
  identifies the root cause). Two new runbooks under
  `docs/operations/runbooks/` cross-link to the systemd-timer
  equivalents (`supply-snapshot-stale.md`,
  `supply-snapshot-unit-failed.md`) so operators on either
  deployment path land on the right diagnostic flow.

- **SEP-41 aggregator wiring — closes Task #56 (#312)**:
  extends `buildSupplyRefreshers` in
  `cmd/ratesengine-aggregator/main.go` with a third per-asset
  loop alongside XLM (Algorithm 1) + classic
  (Algorithm 2): one `supply.Refresher` goroutine per entry in
  `[supply] watched_sep41_contracts` (Algorithm 3). New
  `supplyAggregatorSEP41Store` adapter projects
  `timescale.SEP41KindTotals` ↔ `supply.SEP41KindTotals` (the
  duplication is necessary to avoid a cyclic import — timescale
  already imports supply for `InsertSupply`). Closes Task #56
  across PRs #309 → #310 → #311 → #312, completing
  ADR-0011's three-domain supply coverage end-to-end. The
  per-tick outcome counter
  (`ratesengine_aggregator_supply_refresh_total{outcome}`) now
  labels for all three algorithms identically. Updated
  `docs/operations/supply-snapshot.md` to reflect shipped
  status across the three asset classes.

- **`StorageSEP41SupplyReader` + `watched_sep41_contracts` config
  (#311 — Task #56 PR 3/4)**: composes the per-kind running sums
  (`Store.SEP41KindTotalsAtOrBefore`, new in this PR — single
  round-trip via SQL `SUM(...) FILTER (WHERE event_kind=...)`)
  with the SAC-balance per-contract lookups for locked-set
  subtraction into a single `SEP41SupplyReader` satisfying the
  existing interface from #199. `AssetBoundSEP41Computer`
  adapts the contract-parameterised `SEP41Computer` to the
  per-asset `SnapshotComputer` shape (mirrors
  `AssetBoundClassicComputer` from #307). New
  `[supply] watched_sep41_contracts` config (C-strkey list) +
  validation. AdminBalance is intentionally `0` in the v1
  reader — operators put admin addresses in `LockedSet.Accounts`
  alongside other locked addresses; the algorithm subtracts them
  equivalently. Pure SEP-41 contracts share the SAC-observer
  storage path by adding their `(contract_id, contract_id)`
  entry to `[supply.sac_wrappers]`.

- **`internal/sources/sep41_supply/` observer + sink wiring
  (#310 — Task #56 PR 2/4)**: SEP-41 supply event observer per
  ADR-0023, plugging into the existing events-based
  `dispatcher.Decoder` hook (NOT `LedgerEntryChangeDecoder` —
  events are not ledger-entry deltas). Operator-watched-contract
  driven via `NewDecoder([]string)` (PR 3/4 wires the operator
  TOML). Match fast-path is `(contract_id ∈ watched_set) AND
  (topic[0] symbol ∈ {mint, burn, clawback})` — `transfer` is
  intentionally NOT matched (transfers move ownership, not
  supply). Decode parses topic-position counterparty (mint/clawback
  → topic[2]; burn → topic[1]) and the i128 amount via
  `scval.AsAmountFromI128`. Sink type-switches on
  `sep41_supply.Event` and routes through
  `Store.InsertSEP41SupplyEvent` (#309). 9 new unit tests cover
  match/skip semantics + decode for all three kinds + i128-safe
  amount handling for values exceeding int64.

- **`sep41_supply_events` hypertable + storage methods (#309 —
  Task #56 PR 1/4)**: migration 0015 creates the
  `sep41_supply_events` hypertable bounded by ADR-0023
  (PK `(contract_id, ledger, tx_hash, op_index, observed_at)`;
  `event_kind` CHECK in `(mint, burn, clawback)`;
  `amount NUMERIC` non-negative). `Store.InsertSEP41SupplyEvent`
  is idempotent on PK conflict (re-running the indexer over the
  same range is a no-op for the running sum).
  `Store.SEP41NetMintAtOrBefore` returns `Σ mint − Σ(burn +
  clawback)` for one contract — the running supply per ADR-0011
  Algorithm 3. Defensive guards reject empty PK columns, invalid
  event kinds, nil/negative amounts before touching the DB. New
  `SEP41EventKind` typed-string enum mirrors the migration's
  CHECK constraint and the discovery sniffer's symbol names.

- **ADR-0023 — SEP-41 supply observer (#308)**: bounds the
  implementation work for Task #56 before code lands. Defines an
  event-stream observer (`internal/sources/sep41_supply/`)
  consuming the existing dispatcher `Decoder` hook with a
  per-contract watched-set filter; aggregates mint/burn/clawback
  amounts into `Σ mint − Σ(burn + clawback)` per ADR-0011
  Algorithm 3. New `sep41_supply_events` hypertable + `Insert*` /
  `SEP41NetMintAtOrBefore` storage primitives. New
  `[supply] watched_sep41_contracts` config + reader composition
  follow the Task #54 / #55 sliced pattern. The 4-PR plan
  (Tasks #67-#70) closes Task #56 and completes ADR-0011's
  three-domain supply coverage.

- **Classic-supply reader composition + aggregator wiring — closes
  Task #55 (#307)**: ships the final piece of ADR-0022.
  `supply.StorageClassicSupplyReader` composes the four
  `Sum*AtOrBefore` primitives from #303 plus the new per-account
  `TrustlineBalanceForAccountAtOrBefore` and per-contract
  `SACBalanceForContractAtOrBefore` lookups into a single
  `ClassicSupplyReader` satisfying the existing interface from
  #199. `supply.AssetBoundClassicComputer` adapts the
  asset-parameterised `ClassicComputer` to the per-asset
  `SnapshotComputer` shape that `Refresher.Tick` expects. New
  `[supply] watched_classic_assets` (CODE-ISSUER list) +
  `[supply.sac_wrappers]` (C-strkey → asset_key map) drive the
  aggregator's classic-supply refresh: `buildSupplyRefreshers`
  spawns one goroutine per watched asset alongside the existing
  XLM-only refresher; the per-tick outcome counter from #301
  (`ratesengine_aggregator_supply_refresh_total`) labels by
  outcome regardless of asset. Closes Task #55 across PRs
  #303 → #304 → #305 → #306 → #307.

- **`internal/sources/{liquidity_pools,sac_balances}/` observers
  (#306 — Task #55 PR 4/5)**: bundles the LP-reserve and
  SAC-wrapped-balance observers per ADR-0022. The LP observer
  emits up to two Observations per pool change (one per asset
  side that's in the watched set); ConstantProduct only at v1
  (the only LP variant Stellar runs today). The SAC observer is
  watched-contract driven via a `map[contract_id]asset_key`
  (PR 5/5 wires the operator TOML), matches the SEP-41
  `Vec(Symbol("Balance"), Address)` key shape, and extracts the
  amount from either i128 or the native SAC's BalanceValue map
  (`amount` field). Unlike the prior two observers, SAC handles
  Removed-variant changes — the operator's contract→asset map
  carries the asset_key independently of the entry body, so
  removed entries emit `IsRemoval=true` rows the reader treats
  as zero balance.

- **`internal/sources/claimable_balances/` observer (#305 — Task #55
  PR 3/5)**: ClaimableBalanceEntry observer following the
  trustlines pattern from #304. Same operator-watched-asset
  config (`[supply] watched_classic_assets`); same dispatcher
  hook (#297). Identity is per-claimable-balance-id (hex of
  `BalanceId.V0`), not per-account, since claimable balances
  aren't tied to an account post-creation. **Removed-variant
  changes are filtered out at v1**: the LedgerKey for a removed
  claimable carries only the BalanceId, not the asset, so we
  can't determine watched-set membership at the observer level.
  Sum query overcount is bounded by the cumulative claimed-but-
  not-recorded volume per watched asset; for circulating-supply
  derivation this is a CONSERVATIVE error (we under-report
  circulating). A writer-side lookup follow-up is documented in
  the package doc if measurable in production.

- **`internal/sources/trustlines/` observer (#304 — Task #55 PR 2/5)**:
  TrustlineEntry observer mirroring the AccountEntry pattern from
  #298. Operator-watched-asset driven via the existing
  `[supply] watched_classic_assets` config (PR 5/5 wires the
  validation in). Match fast-path is type discriminator + asset
  variant + asset_key map lookup — non-classic-credit Trustline
  variants (native XLM, pool-share) are skipped before any decode
  work. Native XLM trustlines route through the AccountEntry
  observer (Algorithm 1); pool-share trustlines route through the
  LP observer in PR 4/5. Indexer-side sink type-switches on
  `trustlines.Observation` and writes to `trustline_observations`
  via #303's `InsertTrustlineObservation`. The observer plugs
  into the existing dispatcher hook (#297) — no `ProcessLedger`
  changes needed.

- **Classic-supply hypertables 0011-0014 + Insert*/Sum* storage
  methods (#303 — Task #55 PR 1/5)**: ships the four migrations
  bounded by ADR-0022 (`trustline_observations`,
  `claimable_observations`, `lp_reserve_observations`,
  `sac_balance_observations`) plus 4 `Insert*Observation` writers
  (last-writer-wins on conflict, mirroring the
  `account_observations` pattern from #299) and 4 `Sum*AtOrBefore`
  read-side primitives (DISTINCT-ON the most-recent row per
  identity-tuple, sum where !is_removal). The Sum* methods are
  what the future `StorageClassicSupplyReader` (PR 5/5) consumes
  to satisfy `ClassicSupplyReader`. Defensive guards at every
  Insert call reject empty PK columns + nil Balance before
  touching the DB. SAC table denormalises asset_key into the row
  so the reader sums by asset without joining a side table — the
  contract → asset mapping is operator-curated and stable
  post-deploy.

- **ADR-0022 — Classic-supply observers (#302)**: bounds the
  implementation work for Task #55 before code lands. Defines
  four observer + storage + reader stacks under
  `internal/sources/{trustlines,claimable_balances,liquidity_pools,sac_balances}/`,
  each mirroring the AccountEntry pattern from ADR-0021 — the
  dispatcher hook from #297 already routes per-tx ledger-entry
  changes through every registered entry decoder, so adding the
  four packages is purely additive. Operator-watched-set driven
  via new `[supply] watched_classic_assets` config; switching to
  "watch every classic asset" is a separate ADR (table-size
  implications). The sliced 5-PR implementation plan ships each
  hypertable populated independently of the reader, so operators
  can audit components via SQL while subsequent PRs land. Once
  shipped, Task #57's aggregator refresher iterates the watched-
  asset list naturally — the existing single-asset path becomes
  the multi-asset case.

- **Periodic supply-snapshot worker in the aggregator — closes
  Task #57 (#301)**: runs the supply-snapshot writer as a
  goroutine inside the aggregator on a configurable cadence,
  replacing the systemd-timer-driven path (#288) for operators
  that have backfilled the LCM observer. New
  `internal/supply/refresher.go` composes ledger lookup +
  computer + inserter into a `Tick`-able unit; the aggregator
  drives it via `runSupplyRefresh` mirroring the baseline-
  refresher pattern. Operator-opted-in via
  `[supply] aggregator_refresh_enabled = true`; cadence is
  `[supply] aggregator_refresh_cadence` (default 5m, validated
  ≥ 30s). Per-cycle outcomes emit as
  `ratesengine_aggregator_supply_refresh_total{outcome}` —
  outcomes are `ok` / `no_ledger` / `no_observation` /
  `compute_error` / `write_error`. The systemd timer (#288)
  remains the path for operators that haven't enabled the
  goroutine; the two paths are mutually exclusive on conflict-
  safe writes (idempotent ON CONFLICT DO NOTHING) but operators
  should disable one when flipping to the other to avoid
  redundant work.

- **LCM-derived readers — closes Task #54 (#300)**: ships
  `supply.LCMReserveBalanceReader` and
  `metadata.LCMHomeDomainResolver`, the two readers that consume
  the `account_observations` hypertable. Wired into both call
  sites with a chained-fallback pattern: live wins when the
  observer has backfilled the watched account; falls through to
  the operator-static config (`[supply.reserve_balances_stroops]`
  / `[metadata.issuer_home_domains]`) when no observation exists
  or a transient storage error fires. The static config blocks
  stay in tree as bootstrap fallbacks; once the observer covers
  the live operator set, balance / home-domain changes flow
  automatically through to the next snapshot / next request
  without operator-edit-and-redeploy. Closes ADR-0021's full
  implementation across PRs #297 / #298 / #299 / #300.

- **`account_observations` hypertable + storage writer + sink wiring
  (#299 — ADR-0021 / Task #54 PR 3/3)**: closes the storage gap left
  by #298. Migration 0010 creates the `account_observations`
  hypertable (7-day chunks; PK `(account_id, ledger, observed_at)`;
  GIN-friendly indexes on `(account_id, observed_at DESC)` and
  `(ledger DESC)` for the two main reader query shapes).
  `Store.InsertAccountObservation` is last-writer-wins on conflict
  (the AccountEntry post-state is monotonic within a ledger so the
  final write is the authoritative state).
  `Store.LatestAccountObservationAtOrBefore` is the read-side
  primitive the next PR's `LCMReserveBalanceReader` /
  `LCMHomeDomainResolver` will consume. The pipeline sink now type-
  switches on `accounts.Observation` and routes to the writer with
  the same panic-recover + per-source-error-counter contract as the
  other event types. Closes the producer half of Task #54; readers
  follow in Task #61 to fully replace the operator-static config maps.

- **AccountEntry observer + `ProcessLedger` integration (#298 —
  ADR-0021 / Task #54 PR 2/3)**: lands `internal/sources/accounts/`
  — the canonical observer implementing the
  `LedgerEntryChangeDecoder` hook from #297. Operator-watched-set
  driven (`NewObserver([]string)`); G-strkeys not in the watched
  list are skipped at `Matches` time before any decode work.
  Emits one `Observation` per matched change (account_id, ledger,
  observed_at, balance_stroops, home_domain, flags, seq_num,
  is_removal). `Dispatcher.ProcessLedger` now walks per-tx
  LedgerEntryChange rows from `tx.UnsafeMeta` (V3 + V4 supported;
  V1/V2 skipped — pre-Soroban metadata doesn't carry the same
  shape) plus the tx-level fee/before/after change blocks.
  Routing path is symmetric with the existing event/op/contract-
  call hooks. Storage writer + `account_observations` migration
  ship in PR 3/3 (Task #60); the readers replacing the static
  config maps follow that.

- **Dispatcher hook for `LedgerEntryChange` deltas (#297)**: starts
  Task #54 / ADR-0021 implementation. Adds the fourth dispatcher
  hook (`LedgerEntryChangeDecoder`) alongside the existing three —
  same first-match-wins / non-fatal-error / per-source-decode-error-
  counted contract. Per ADR-0021 entry changes are high-volume so
  unmatched changes are silently dropped (no `UnmatchedHits` bump,
  unlike `Decoder` events). New `RouteEntryChange` test-harness
  helper symmetric with `Route` / `RouteOp` / `RouteContractCall`.
  Six unit tests cover routing-by-type, first-match-wins, decode-
  error accounting, no-decoder-registered, output flow-through.
  ProcessLedger integration is the next PR (lands alongside the
  first decoder using the hook — `internal/sources/accounts/`'s
  AccountEntryObserver).

- **Supply-snapshot textfile-collector + four alerts + three
  runbooks (#295)**: closes the operator-visibility gap on the
  daily supply-snapshot writer (#288). Mirrors the SLA-probe
  pattern from #293/#294. New `internal/supply/textfile.go` emits
  per-asset gauges (`total_xlm`, `circulating_xlm`, `max_xlm`,
  `ledger`, `observed_at_seconds`) plus a `unit_failed` /
  `last_success_timestamp` pair the alerts key on. Failure path
  emits a fail-marker textfile (no `last_success_timestamp`) so the
  staleness alert keys on the previous-scrape value. Alerts:
  `_unit_failed_alert` (P3 ticket), `_stale` (P3 at 36 h) /
  `_critical_stale` (P2 page at 72 h), and
  `_circulating_zero` (P2 page — ADR-0011 invariant violation
  signal). Three new runbooks. Operator-toggled via
  `TEXTFILE_OUTPUT` env-var; empty default behaves exactly like
  #288.

- **SLA-probe alert rules + four runbooks (#294)**: closes the
  alert-rules-tracked-as-follow-up note in #293. Ships
  `deploy/monitoring/rules/sla-probe.yml` with four rules —
  `_p95_breach` (page on > 200 ms sustained 30 min),
  `_freshness_breach` (page on > 30 s sustained 30 min),
  `_unit_failed_alert` (umbrella ticket for any breach kind), and
  `_stale` (page when no successful run in 90 min — 6× the
  15-min cadence). Each alert has a per-runbook entry under
  `docs/operations/runbooks/sla-probe-*.md` and a row in
  `docs/operations/alerts-catalog.md`.

- **`-textfile-output` flag on `ratesengine-sla-probe` (#293)**:
  follow-up to #283 / #290. Writes the per-run latency / availability
  / freshness / sample-count / verdict values as a Prometheus
  textfile (atomic `<path>.tmp`-then-rename) so node_exporter can
  scrape them via the textfile_collector. Metric set:
  `ratesengine_sla_probe_latency_ms{endpoint,quantile}`,
  `ratesengine_sla_probe_availability_pct{endpoint}`,
  `ratesengine_sla_probe_freshness_sec{endpoint}`,
  `ratesengine_sla_probe_samples{endpoint}`,
  `ratesengine_sla_probe_run_duration_seconds`,
  `ratesengine_sla_probe_unit_failed`,
  `ratesengine_sla_probe_last_pass_timestamp` (only on pass — the
  staleness alert keys on previous-scrape value when current run
  fails). Systemd service updated with optional `TEXTFILE_OUTPUT`
  env-var; `ReadWritePaths` allows writes to the standard
  textfile_collector dir. Alert rules tracked as a separate
  follow-up — the metric set is shipped.

- **ADR-0021 — AccountEntry observer for live home-domain +
  reserve-balance tracking (#292)**: bounds the implementation work
  for Task #54 before code lands. Defines a fourth dispatcher hook
  (`LedgerEntryChangeDecoder`), a canonical observer in
  `internal/sources/accounts/` driven by operator-watched-set config,
  a new `account_observations` hypertable, and two readers
  (`metadata.LCMHomeDomainResolver` + `supply.LCMReserveBalanceReader`)
  that replace the operator-static `[metadata.issuer_home_domains]`
  and `[supply.reserve_balances_stroops]` config blocks once the
  observer has backfilled. The two operator-static maps stay in tree
  as fallbacks while the live data catches up. Once shipped, Task
  #57 (periodic supply-snapshot worker) becomes implementable — the
  aggregator can refresh snapshots per tick rather than per cron-
  fire, and the systemd timer (#288) becomes redundant.

- **systemd timer + service + runbook for the SLA probe (#290)**:
  closes the operator-side gap left by #283. Ships
  `deploy/systemd/sla-probe.{service,timer}` (every 15 min + 2 min
  jitter — strikes the balance between SEV-2 detection requirement
  ≤ 30 min and the anonymous-tier rate budget) plus
  `docs/operations/sla-probe.md`. Exit-1-on-SLA-breach surfaces via
  `systemctl is-failed`; node_exporter's `--collector.systemd`
  picks the failure up so the existing systemd-unit-failed alert
  pattern covers it. Today the probe writes to journald only — the
  textfile-collector + alerting integration is the additive
  follow-up.

- **systemd timer + service + runbook for the supply-snapshot writer
  (#288)**: closes the operator-side gap left by #285. Ships
  `deploy/systemd/supply-snapshot.{service,timer}` (daily 04:42 UTC
  + jitter, spaced after the existing archive-completeness 02:17 and
  verify-archive-tier-a 03:23 timers) plus
  `docs/operations/supply-snapshot.md` covering the `[supply]` config
  block, the SDF-reserve-move update procedure, the dry-run
  pre-flight, and the v1 asset-class scope. Daily cadence is correct
  for now — values change only when operator config changes (a few
  times per year). When Task #54's LCM-derived reader ships and the
  writer becomes goroutine-resident in the aggregator, this systemd
  unit becomes redundant.

- **XLM supply-snapshot writer via `ratesengine-ops supply snapshot`
  (#285)**: closes the write half of the supply pipeline. Read half
  shipped in #277 left `/v1/assets/{id}` F2 fields null because no
  producer was populating `asset_supply_history`; this PR plugs the
  gap for native XLM (Algorithm 1 per ADR-0011). New
  `ConfigReserveBalanceReader` satisfies `supply.ReserveBalanceReader`
  from operator-supplied balances; new `[supply]` config block carries
  `sdf_reserve_accounts` + `reserve_balances_stroops`. Writer-start
  validates every configured account has a balance entry — silently
  treating an unknown account as zero would publish an over-stated
  circulating supply, the exact failure mode ADR-0011 prohibits.
  Reserve balances are operator-managed for now (a few SDF moves per
  year); the LCM-AccountEntry-observer follow-up replaces the static
  map with a live reader. Drive-by: extended
  `internal/config/schema.go` to recurse into `[]struct` fields so
  `docs-config` emits per-element rows for slices of structs.

- **`/v1/chart` endpoint per Freighter RFP (#284)**: adds
  `GET /v1/chart` matching the Freighter RFP V1 chart contract
  exactly: `(timeframe, granularity, price_type) → points[]`. ADR-0020
  documents the decision. New storage method `HistoryPointsInRange`
  adds a `[from, to)` bucket bound on top of the existing closed-
  bucket guard — no CAGG / migration changes. Default-granularity
  table follows the RFP: 1h→1m, 24h→15m, 1w→1h, 1mo→4h, 1y→1d, all→1d;
  operators can override granularity explicitly. `price_type=twap` is
  reserved and returns 400 today — flipping to 200 is gated on
  shipping a TWAP CAGG. Coverage matrix row F1.3 (Historical Price
  Chart) moves from partial to served.

- **Executable SLA-evidence CLI `cmd/ratesengine-sla-probe` (#283)**:
  drives load against a deployed Rates Engine API and reports per-
  endpoint p50 / p95 / p99 latency, freshness against the price's
  `observed_at`, and availability — with a pass/fail verdict against
  the RFP-stated SLA targets (p95 ≤ 200ms, p99 ≤ 500ms, freshness
  ≤ 30s, availability ≥ 99.9%). JSON or text output; exit code 1 on
  any SLA violation so it slots into CI / scheduled-job pipelines and
  trends over time. Closes Codex medium-7 / coverage-matrix rows
  S5.2, S9.1, S9.2, F3.1-F3.4 — the executable evidence the RFPs /
  proposal asked for. Remaining rows (HA posture, SEV detection time)
  need a production deployment to measure, not a pre-launch CLI.

- **Chainlink HTTP divergence reference (#282)**: closes Codex
  high-3 (Chainlink-named-but-not-implemented). Adds a
  `divergence.Reference` backed by Chainlink Data Feeds via
  off-chain Ethereum JSON-RPC reads — Stellar joined Chainlink
  Scale in 2025/2026 but no Soroban Data Feeds contracts are live
  on mainnet at audit time, so the bytes live on Ethereum + L2s.
  Reference does `eth_call` against the AggregatorV3 contract's
  `latestAnswer()` view function (selector `0x50d25bcd`), decodes
  the int256 (two's-complement aware), applies per-feed decimals,
  and optionally inverts. Role: divergence cross-check ONLY —
  Chainlink does NOT contribute to VWAP/TWAP; its values surface
  as `flags.divergence_warning` on `/v1/price` when our aggregated
  price diverges beyond threshold. `FeedMap` operator-curated;
  empty yields `ErrAssetUnsupported` per pair.

- **Coverage-matrix re-baseline (#281)**: closes Codex medium-1 +
  Task #50. The matrix had drifted in both directions — rows
  marked "designed / impl pending" had shipped (triangulation, SSE
  streams, batch price, OHLC CAGGs, SEP-40 endpoints, supply
  read-path, volume_24h_usd), and rows marked "verified" had
  quietly become operational gaps (Chainlink, Blend prior to #275,
  supply writer). Rewrites every materially-stale row to the
  as-of-2026-04-30 reality. Net 13 row-state corrections.

- **Triangulation API reader, `flags.triangulated` (#280)**: reader
  half of the F-0014 / Codex medium-3 fix; pairs with #279.
  When `/v1/price`'s Timescale lookup returns `ErrPriceNotFound`
  (the steady-state for triangulated-only pairs like XLM/EUR via
  XLM/USD × USD/EUR), the handler now consults a
  `TriangulatedPriceLooker` fallback that reads the Redis VWAP
  value AND the provenance marker that #279 added on the writer
  side. Marker present → synthesised `PriceSnapshot` with
  `flags.triangulated=true`; marker absent → falls through to the
  original 404. Direct-VWAP cache reads are still gated to
  Timescale; the fallback only activates for the triangulated
  case so the source-of-truth contract is preserved.

- **Triangulation provenance marker, writer half (#279)**: writes
  `cachekeys.VWAPProvenance(base, quote, window) = "triangulated"`
  alongside the value key when the orchestrator's triangulator
  produces an implied VWAP. Per-pair direct refresh does NOT write
  the marker — absence == direct (or unknown), which the read side
  treats as `flags.triangulated=false`. Marker-write failure logs
  WARN but does not roll back the value write; the implied VWAP is
  correct either way.

- **`volume_24h_usd` on `/v1/assets/{id}` (#278)**: closes Codex
  Freighter-V2 high-1 trailing item. Adds the field end-to-end:
  new `Volume24hUSDForAsset` storage method sums
  `prices_1m.volume_usd` over pairs where the asset appears as
  base OR quote in the trailing 24h window (CAGG-served, 1440
  buckets max — cheap); new `VolumeReader` interface populates
  `AssetDetail.VolumeUSD24h`. Independent of the Supply path so
  volume serves even when supply isn't yet wired (and vice versa).

- **Supply snapshot reader wired (#277)**: closes audit F-0020 +
  Codex Freighter-V2 high-1. The API binary was leaving
  `v1.Options.Supply` nil, dead-coding the F2-fields path entirely.
  This change populates `total_supply` / `circulating_supply` /
  `max_supply` / `market_cap_usd` / `fdv_usd` / `supply_basis` on
  `/v1/assets/{id}` whenever the asset has a snapshot in
  `asset_supply_history`. No-snapshot keeps the F2 fields null
  and the asset-detail body still serves cleanly per ADR-0011
  ("we don't fabricate"). Read half only — the write half landed
  separately in #285.

- **Blend WASM-audit doc scaffold (#276)**: sets up the per-source
  audit log under `docs/operations/wasm-audits/blend.md` with the
  mainnet contract list (Pool Factory + Backstop) cross-referenced
  against the blend-contracts-v2 deploy manifest, the decoder-
  expectations table mirroring `internal/sources/blend/`, the
  4-phase audit plan (enumerate → walk → review → flip), and the
  failure-mode checklist (topic[0] rename, AuctionData field
  rename, i128 type drift, new auction_type discriminant). Status
  stays `pending`; `BackfillSafe` stays `false`. The follow-up PR
  completes Phases 1-4 and flips the flag.

- **Blend wired into dispatcher + registry + indexer sink (#275)**:
  final wiring step for the Blend integration. After this PR an
  operator who lists `blend` in `ingestion.enabled_sources` gets
  full live ingest of Blend auction events. Adds a new
  `ClassLending` taxonomy entry to `internal/sources/external`
  alongside `ClassExchange` / `ClassAggregator` / `ClassOracle` /
  `ClassAuthoritySanity` — Blend doesn't fit any existing class
  (not exchange, not aggregator, not oracle, not authority-sanity).
  `BackfillSafe: false` until #53 audit completes.

- **Blend auction storage layer (#274)**: migration 0009 creates
  the `blend_auctions` hypertable (1-day chunks; same shape as
  trades + oracle_updates) keyed on
  `(ledger, tx_hash, op_index, ts)`. `auction_type` SMALLINT with
  `CHECK 0..2`, `event_kind` TEXT with `CHECK ('new','fill','delete')`,
  per-variant fields nullable per lifecycle event. `bid` / `lot`
  JSONB arrays of `{asset, amount}` with stringified i128 amounts
  preserving full precision through the JSON boundary per
  ADR-0003. Three insert methods on `*timescale.Store` —
  `InsertAuctionNew` / `InsertAuctionFill` / `InsertAuctionDelete`.

- **Blend auction-event decoder skeleton (#273)**: first step of
  the Blend integration committed in the Stellar RFP + ctx-
  proposal price-aggregation scope. Per
  `docs/discovery/dexes-amms/blend.md` Blend is **not** a spot
  trading venue — we index for directional / state-change signals,
  not VWAP. Ships the package skeleton and the auction-event
  decoder surface (new_auction / fill_auction / delete_auction);
  follow-up PRs added storage (#274) + dispatcher wiring (#275) +
  the audit doc (#276).

- **Audit remediation wave for the 2026-04-29 cold adversarial
  audit (#272)**: closes 20 of the 31 findings raised in the audit
  workspace (`docs/audit-2026-04-29/`). Mix of correctness fixes,
  monitoring truth, public-contract repair, and docs-truth
  alignment. Highlights: F-0008 wired
  `api.key_rate_limit_per_min` into a subject-aware authenticated
  bucket (anonymous and authed tiers now use distinct buckets);
  F-0028 + F-0031 closed via complementary correctness fixes.
  See `docs/audit-2026-04-29/findings/` for the per-finding ledger.

- **`extract-wasm-from-galexie` ratesengine-ops subcommand (#271)**:
  extracts raw WASM bytes for one or more contract-code hashes by
  walking the local galexie LCM archive — the truer source than
  RPC `getLedgerEntry` because it (1) works for evicted WASMs
  (TTL-expired bytes are no longer in active ledger state but ARE
  preserved in galexie LCM), (2) doesn't depend on public-RPC
  retention, (3) runs offline against r1's full archive. Companion
  to `wasm-history`: walk first to enumerate every hash that ever
  ran on each contract, then run extract to pull the bytes for the
  older (likely-evicted) versions. Parallel range partitioning;
  per-LCM scan picks `LedgerEntryChange` of type Created or
  Updated. Also adds the v2-audit template doc.

- **systemd units for `ratesengine-{indexer,aggregator,api}` (L4.13)**:
  long-running `Type=simple` service files for the three runtime
  binaries. Hardened (`ProtectSystem=full`, `PrivateTmp`, etc.),
  restart-on-failure with backoff, after-graph respects the
  postgres + redis + indexer dependency chain. Doesn't include
  Postgres/Redis/binary deploy — that's still operator-side. The
  bringup doc already forward-referenced these by name; this PR
  ships the actual files. Slot under `deploy/systemd/` alongside
  the L4.12 verify-archive timer + the existing
  `archive-completeness.{timer,service}`.

- **verify-archive systemd timer (L4.12)**: nightly Tier A
  chain-link integrity check on R1 per the ADR-0016 per-region
  trust model + the `archival-node-bringup.md` schedule
  (`R1: Tier A nightly`). Ships
  `deploy/systemd/verify-archive-tier-a.{timer,service}` —
  fires at 03:23 UTC + 10m jitter (placed AFTER the daily
  archive-completeness verify at 02:17, so missing-file gaps
  surface there first). 8h max-runtime cap based on the
  parallel-chunk run profile observed today (5h47m for the full
  archive on 8 workers). Two new Prometheus alerts:
  `ratesengine_verify_archive_unit_failed` (P3, ticket — last
  run failed) and `ratesengine_verify_archive_run_stale` (P2,
  page — no clean run in 36h+); both source from
  node_exporter's `--collector.systemd` so no application-side
  metrics work was needed. Two runbooks shipped. Backlog row
  L4.12 added.

- **`external.Metadata.Subclass` for CEX/DEX/FX diversity (L2.6
  follow-up)**: closes the gap noted in #259 — the existing `Class`
  enum lumps CEX + DEX both under `ClassExchange`, which under-
  counted diversity per the ADR-0019 worked example. New `Subclass`
  field partitions ClassExchange into `cex` / `dex` / `fx`. The
  orchestrator's `distinctSourceClassCount` now keys on the
  `Class:Subclass` composite, so:
  - two CEXes (binance + coinbase) → 1 bucket
  - CEX + DEX (binance + soroswap) → 2 buckets ✅ matches ADR
  - CEX + DEX + FX → 3 buckets
  - DEX + Oracle → 2 buckets (cross-parent-class)
  Sources outside ClassExchange leave Subclass blank — their
  parent Class already captures the economic distinction.

- **Source-class registry lookup for confidence diversity factor
  (L2.6 follow-up)**: the orchestrator's `distinctSourceClassCount`
  now consults `external.Lookup(source).Class` instead of using
  the source name as a proxy. The diversity factor reads "two
  CEXes = 1 class" (correct) and "CEX + Oracle = 2 classes"
  (correct) where before it would have read both as equally
  diverse. CEX-vs-DEX is still collapsed under `ClassExchange` —
  the existing taxonomy doesn't split them; a follow-up that adds
  a `Subclass` field to `external.Metadata` would close the gap.

- **Operator-tunable Phase 2 freeze thresholds (L2.7 follow-up)**:
  the ADR-0019 Phase 2 freeze condition's three thresholds —
  `confidence_max_freeze` (0.10), `z_score_min_freeze` (5.0),
  `source_count_max_freeze` (1) — are now surfaced as
  `[anomaly.phase2]` TOML knobs. Defaults match the package-level
  values from #256 so unset operators see no behaviour change.
  Partial overrides merge with defaults (`Phase2Thresholds.withDefaults`)
  so an operator who only wants to tighten one signal doesn't have
  to restate the others. Validation runs at startup —
  out-of-range values surface clear errors instead of silently
  disabling the gate. New `DefaultPhase2*` package constants
  document the canonical values; tests cover boundary cases plus
  partial-override merging.

- **Bootstrap confidence cap (L2.9)**: per ADR-0019 §"Bootstrap
  policy", assets with fewer than 30 days of history have their
  confidence score hard-capped at 0.5 regardless of how healthy
  every other factor reads. Implemented as a post-combiner clamp
  in `confidence.Compute`: when `BaselineAgeDays < 30` (or the
  `-1` "no baseline yet" sentinel), the cap fires. The cap is a
  ceiling, not a floor — naturally-low confidence (single-source,
  low liquidity) still reads through. New constants
  `BootstrapDays = 30` and `BootstrapConfidenceCap = 0.5` document
  the threshold. The class-average baseline + auto-classify
  pieces of L2.9 are deferred to a follow-up.

- **Phase 2 freeze policy — 3-signal AND (L2.7 closes)**: per
  ADR-0019 §"Freeze policy", the orchestrator now runs a second
  freeze layer alongside Phase 1: `confidence < 0.10 AND z_score >
  5.0 AND source_count <= 1`. All three signals must agree —
  catches the USTRY-shape attack pattern (single source, large
  deviation, confidence-killing combination) without firing on
  legitimate market events (those have multi-source corroboration).
  Refactored `refreshPairWindow`: confidence now computes BEFORE
  the VWAP cache write, so a Phase 2 freeze leaves the prior
  bucket's value intact in cache (same LKG-preserving semantic
  as Phase 1). The freeze marker carries
  `Reason="phase2:3_signal_AND confidence=… z=… sources=…"` so
  log lines + Redis marker JSON make the source legible without a
  new wire field. Class label on
  `ratesengine_anomaly_freeze_engaged_total` consistent with
  Phase 1 (uses the same Checker's classifier when wired). New
  exported `Checker.ClassOf` for that consistency.

- **Confidence score on `/v1/price` envelope (L2.6 closes)**: API
  reads the cached `confidence:<base>:<quote>:<window>` Redis key
  written by the aggregator and surfaces both the score
  (`confidence` ∈ [0, 1]) and its decomposition (`confidence_factors`)
  on the response data object per ADR-0019. New `ConfidenceLooker`
  interface; production wiring is `redisConfidenceLooker` in the
  API binary that JSON-decodes the cached `confidence.Score`.
  Cache misses + read errors leave the fields off the wire
  (`omitempty`) — clients that gate on confidence treat absence as
  "unknown", not "low". Closes L2.6 across 4 PRs: math primitive
  (#252), orchestrator compute + cache write (#253), cross-oracle
  divergence wiring (#254), API surface (this PR).

- **Cross-oracle divergence wired into confidence (L2.6 slice 3)**:
  the orchestrator's confidence step now reads `div:<asset>` from
  Redis (the cache the divergence worker writes via
  `Service.RefreshPair`) and feeds the cached `DivergencePct` into
  `confidence.Inputs.CrossOracleDivergencePct` when
  `SuccessCount >= 2`. Single-source cached results are ignored
  (pass the "no data" sentinel — guards against scoring one
  reference's hiccup as a multi-source signal). Best-effort:
  `divergence_read_error` / `divergence_decode_error` outcomes
  surface on the existing
  `ratesengine_aggregator_confidence_compute_total` counter and
  the confidence step continues with the neutral sentinel rather
  than blocking on a Redis blip. Two new tests confirm wiring
  (within-1% cached → CrossOracle factor 1.0, no cache → 0.7
  neutral) and the SuccessCount<2 ignore policy.

- **Confidence score wired into the orchestrator (L2.6 wire-up
  slice)**: per-tick confidence-score compute alongside VWAP
  publishing. New `BaselineSource` interface on `orchestrator.Config`
  reads the cached `MultiBaseline` for z-score lookup. After each
  successful VWAP cache write, the orchestrator computes a return %
  vs the prior tick's VWAP, runs `MultiBaseline.MaxZScore`, gathers
  source count + class count + USD-quote volume + baseline age, and
  writes the JSON-encoded `confidence.Score` to Redis at
  `confidence:<base>:<quote>:<window>`. Confidence is enrichment,
  not a publish gate — baseline-source errors / Redis blips on the
  confidence path are logged + counted but never block the VWAP
  publish itself. New cache key `cachekeys.Confidence` /
  `ConfidenceTTL` (matches VWAP TTL). New Prometheus counter
  `ratesengine_aggregator_confidence_compute_total` labelled by
  `{ok, skipped, baseline_missing, marshal_error, write_error}`.
  Cross-oracle divergence input still passes the "no data" sentinel
  pending the next slice (which wires the `div:<asset>` Redis key
  read). API hot-path read of the confidence cache key follows
  separately.

- **Multi-factor confidence score primitive (L2.6 math slice)**:
  pure-Go `internal/aggregate/confidence` package implementing the
  ADR-0019 §"Multi-factor confidence score" combiner. Six factors
  per the ADR shape: `ZScoreFactor` (sigmoid 1.0 at z=0, ~0.5 at
  z=5, ~0 at z=10), `SourceCountFactor` (logistic; n=3 → 0.5;
  n≥6 → ~1.0), `DiversityFactor` (step: 0/0.5/1.0), `LiquidityFactor`
  (log-saturating; $1K → 0, $100K → 1.0), `CrossOracleFactor`
  (piecewise: 1.0 within 1%, exponential decay beyond; negative
  input is the "no cross-oracle data" sentinel returning the ADR's
  0.7 neutral), `BaselineQualityFactor` (linear 0.5 → 1.0 over
  30d). Combined via weighted geometric mean with `1/sum(weights)`
  normalisation so weight magnitude doesn't change scale. Compute
  is numerically stable (sums log-factors, exp at the end) so
  near-zero factors don't underflow. 21 tests pin the per-factor
  shapes, the dominating-factor behaviour, and edge cases (all-
  zero weights, full bootstrap, extreme inputs). Orchestrator
  wire-up follows in the next slice.

- **Multi-window baseline storage + refresh integration (L2.8
  closes L2.8)**: migration 0008 adds `median_1d/mad_1d/n_1d` and
  `median_7d/mad_7d/n_7d` to `volatility_baseline_1m` (the existing
  median/mad/sample_count columns hold the 30d baseline; the new
  pairs are nullable for the bootstrap-on-this-scale case).
  `Store.UpsertBaseline` and `LatestBaseline` now carry a
  `baseline.MultiBaseline` end-to-end; pre-flight checks include
  Day30 non-nil. `Store.TimedVWAPsForPair1m` returns time-stamped
  VWAPs so the refresher can apply `SplitByLookback` to derive the
  three sub-windows from one read. `baseline.Sink` updated to take
  a MultiBaseline; aggregator binary's adapters track. The 30d
  bootstrap (Day30 nil) outcome surfaces as
  `OutcomeNotEnoughSamples` (no row written); per-window bootstrap
  (Day1/Day7 nil while Day30 valid) is OK and persists with NULL
  columns. Closes L2.8 across 2 PRs — the anomaly-evaluator
  consumer of `MultiBaseline.MaxZScore` lands with L2.7.

- **Multi-window baseline safeguard (L2.8 math slice)**: per
  ADR-0019 §"Multi-window safeguard against frog-boiling" — a
  coordinated attacker who slowly drifts an asset over weeks would
  defeat the 1d window (median tracks the drift) but the 30d
  window (still includes pre-attack data) flags the drifted price
  as anomalous. New `baseline.MultiBaseline` holds three
  independent baselines at 1d/7d/30d lookbacks; `MaxZScore`
  returns the largest z across all valid windows so "any window
  flags" maps to a single threshold check. `SplitByLookback`
  helper partitions a time-stamped VWAP series into three sub-
  windows in one pass. 7 new tests including the headline
  frog-boiling-defense scenario (sustained 0.5%/day drift over
  14d → 30d window dominates). Storage + orchestrator wire-up
  follow as separate slices.

- **Baseline refresh wired into the aggregator binary (L2.5 final
  slice — closes L2.5)**: `cmd/ratesengine-aggregator` now runs a
  hourly baseline refresh loop alongside the orchestrator's
  per-tick VWAP cycle. Adapters wrap `*timescale.Store` to satisfy
  `baseline.VWAPSource` + `baseline.Sink`. The first refresh fires
  immediately on startup so a fresh deployment populates
  `volatility_baseline_1m` without waiting a full hour. Outcomes
  emit through `ratesengine_aggregator_baseline_refresh_total`
  labelled by `{ok, not_enough_samples, read_error, write_error}`.
  Cadence (1h) and concurrency (4) are hardcoded for now —
  surfaceable as TOML knobs only if production usage shows we need
  them. Closes L2.5 across 4 slices: math primitive, storage layer,
  refresh worker, binary wire-up.

- **Baseline refresh worker (L2.5 slice)**: `baseline.Refresher`
  reads bucket-aligned 1m VWAPs over a 30d window via the new
  `Source.VWAPSource` interface, runs `ReturnsFromVWAPs` →
  `FromReturns` to compute the baseline, and persists via the
  `Sink` interface. Storage layer adds `Store.VWAPsForPair1m`.
  `RefreshPair` returns a structured `RefreshOutcome` (ok,
  not_enough_samples, read_error, write_error) so callers can
  emit per-outcome metrics; `RefreshAll` runs across a pair list
  with bounded concurrency, aggregates a `RefreshSummary`, and
  honours ctx cancellation cleanly. The bootstrap branch is
  not_enough_samples — caller skips the upsert and applies
  ADR-0019 §"Bootstrap policy" instead. The aggregator binary's
  wire-up (running this on an hourly ticker against the
  configured pair list) lands in the next L2.5 slice.

- **`volatility_baseline_1m` table + storage layer (L2.5 slice)**:
  per-pair baseline persistence per ADR-0019 Phase 2. Migration 0007
  adds the table — plain Postgres, NOT a CAGG (Median + MAD are only
  expressible via percentile_cont, which is non-parallel and
  non-incremental, so a CAGG would re-scan the whole 30-day window
  on every refresh anyway with no benefit). Current-state semantics:
  one row per pair, refreshes UPSERT and overwrite. Storage layer
  ships `StoredBaseline` wire shape, `Store.UpsertBaseline` (with
  pre-flight N >= MinSamples + window-validity checks),
  `Store.LatestBaseline` (returns `ErrBaselineNotFound` for the
  bootstrap branch), and `Store.CountBaselines` for ops metrics.
  Integration test rounds the API trip including overwrite semantics
  and per-pair isolation. The aggregator-side compute + write
  pipeline lands in the next L2.5 slice.

- **`internal/aggregate/baseline/` MAD math (L2.5 slice)**:
  pure-Go primitives implementing the per-asset volatility baseline
  per [ADR-0019](docs/adr/0019-anomaly-response-and-confidence-scoring.md)
  Phase 2. `Median`, `MAD` (1.4826-scaled to σ-equivalent), `Baseline`
  struct with `ZScore` method (handles zero-MAD edge case: exact-match
  returns 0, any deviation returns +Inf), and `ReturnsFromVWAPs`
  helper for bucket-to-bucket percent-change conversion. Skips
  buckets with `prev == 0` to avoid Inf-poisoning downstream stats.
  17 tests cover odd/even median, MAD outlier-robustness vs σ,
  z-score symmetry, zero-MAD edge cases, and a stablecoin-class
  end-to-end roundtrip. The `volatility_baseline_1m` CAGG migration
  and the orchestrator wiring (the two larger pieces of L2.5) ship
  in follow-up PRs — this slice is the math primitive everything
  else builds on.

- **`/v1/price/stream` SSE endpoint (L3.9)**: closed-bucket SSE
  surface per ADR-0015 + ADR-0018. Hub-driven (unlike the per-tick
  tip/observations streams) — the aggregator publishes one event per
  closed bucket on the topic `closed:<asset>/<quote>`, and every
  subscriber on the same pair receives byte-identical payloads.
  Returns 503 until the deployment wires a `streaming.Hub` into
  `v1.Options.Hub`; the API handler + topic helper ship now so
  consumers can integrate against the wire contract before the
  aggregator's publish path lands. URL discipline: `?granularity=`
  returns 400 (closed-bucket stream is fixed at 1m).

- **`/v1/observations/stream` SSE endpoint (L3.8)**: streaming
  counterpart to `/v1/observations` per ADR-0018. Same compute,
  pushed on a per-connection tick. Cadence knob is `interval_seconds`
  (default 5, clamp 1–60) — deliberately a different name from
  tip's `window_seconds` because observations doesn't aggregate.
  First event always emits synchronously (may be empty array;
  observations returns 200/empty not 404, the stream mirrors that).
  Same `?source=`, `?aggregate=latest` knobs as the request
  endpoint. URL discipline: `?granularity=` and `?window_seconds=`
  return 400. Refactored the request handler's compute path into a
  shared `Server.computeObservations`.

- **`/v1/price/tip/stream` SSE endpoint (L3.7)**: streaming
  counterpart to `/v1/price/tip` per ADR-0018. Same compute logic
  pushed on a per-connection tick (default cadence = window_seconds,
  clamp 1–60). First event emits synchronously on connect — no
  waiting a full window for the first datum. Pre-flight 404 when
  the pair has no observations (SSE can't change status mid-stream).
  Heartbeats every 15s; Last-Event-ID resume via header or
  `?last_event_id=` fallback. Refactored the request handler's
  rolling-window-then-fallback core into a shared `Server.computeTip`
  used by both endpoints.

- **`internal/api/streaming/` SSE infrastructure (L3.6)**: shared
  pub/sub primitive backing the upcoming streaming endpoints
  (L3.7 `/v1/price/tip/stream`, L3.8 `/v1/observations/stream`,
  L3.9 `/v1/price/stream`). `Hub` is goroutine-safe per-topic
  fanout with a per-topic ring buffer (default 256 events) for
  Last-Event-ID resume. `Stream` HTTP handler sets the SSE wire
  contract: `text/event-stream` headers, `X-Accel-Buffering: no`,
  comment-only heartbeats every 15 s (configurable), parses
  `Last-Event-ID` header (with `?last_event_id=` fallback), and
  forwards live events as SSE frames until the request context
  cancels. Slow subscribers are dropped (32-deep per-sub queue)
  rather than blocking the publish path — the dropped client sees
  the connection close and reconnects with `Last-Event-ID` for
  buffered replay. ULID-shaped 16-char hex IDs, monotonic and
  lexicographically sortable. No external dependencies.

- **`/v1/observations` raw per-source surface (L3.3)**: implements
  [ADR-0018](docs/adr/0018-api-consistency-surfaces.md) Surface 3 —
  the lowest-level, no-aggregation surface. Returns the most-recent
  trade per source for the (asset, quote) pair as an array.
  `?source=X` narrows to one venue; `?aggregate=latest` collapses to
  the single newest trade across sources. `flags.stale` is always
  false; freeze + divergence flags intentionally not consulted (this
  is the rawest surface, no aggregation contract). Empty pair returns
  200 with `data: []`, not 404 — divergence-detection callers polling
  for source coverage benefit from the 200/empty distinction.
  URL discipline: `?granularity=` and `?window_seconds=` return 400.
  New storage primitive `Store.LatestTradePerSource` does the work in
  SQL via `DISTINCT ON (source)`.

- **`/v1/price/tip` rolling-window tip surface (L3.2)**: implements
  [ADR-0018](docs/adr/0018-api-consistency-surfaces.md) Surface 2.
  VWAP over a configurable rolling window (default 5 s, clamp 1–60 s)
  with last-good-price fallback when the window is empty. Both
  branches are in-contract — `flags.stale` stays `false` on this
  surface (the closed-bucket "below-baseline" semantic doesn't
  apply). Freeze flag is intentionally NOT consulted (freeze is a
  closed-bucket concept; tip explicitly has no cross-region
  consistency contract). Divergence flag still applies (asset-level).
  URL discipline enforced: `?granularity=` returns 400.
  Hypertable hiccups on the window path silently drop to the
  fallback so a transient TimescaleDB error doesn't take down the
  whole tip surface when the LatestPrice path is healthy.

- **`pkg/client/` Go SDK skeleton (#201)**: first public-package
  surface under [ADR-0005](docs/adr/0005-monorepo.md)'s SemVer
  promise. v0.1.0 pre-release. Generic `Envelope[T]` for type-
  safe data fields; covered endpoints: `Price`, `HistorySinceInception`,
  `Assets`, `Asset`, `AssetMetadata`, `Me`, `Usage`, `CreateKey`.
  `*APIError` wraps RFC 9457 problem+json with convenience
  predicates (`IsNotFound`, `IsRateLimited`, …); falls back to
  status-only on text/plain bodies (reverse-proxy 502s). Auth via
  `Options.APIKey` → `Authorization: Bearer …` header (omitted
  when empty so anonymous callers don't trigger malformed-credential
  rejections).

- **`internal/divergence/` package (#204, #205)**: cross-reference
  divergence layer per [ADR-0019](docs/adr/0019-anomaly-response-and-confidence-scoring.md)
  §"Layer 5". `Reference` interface + parallel `Compare()` with
  robust median + per-source breakdown. `CoinGeckoReference`
  implementation as the working concrete example. `Service` writes
  `div:<asset>` Redis keys per [ADR-0007](docs/adr/0007-redis-cache-schema.md);
  `LookupCached` is the API hot-path read. `flags.divergence_warning`
  now fires for real on `/v1/price` when the cached result says
  warning is fired (5% deviation × 2 min sources defaults).
  Best-effort: lookup errors log at WARN, flag stays default false.

- **`internal/aggregate/anomaly/` Phase 1 (#199)**: ADR-0019
  Phase 1 stop-gap. `Classifier` + `Thresholds` + `Checker.Evaluate`
  with the 3-signal AND freeze condition (deviation > class
  threshold AND source_count <= 1). Per-class defaults:
  stablecoin/treasury 1%/3%, crypto 20%/50%, governance 50%/100%,
  default 30%/75%. New envelope flags `Frozen` and `SingleSource`
  on the wire. Config schema describer recurses into
  `map[string]<struct>` value types so per-row sub-fields appear
  in the generated config reference.

- **`internal/archivecompleteness/` daemon (#200, #202, #203)**:
  three-PR trilogy implementing [ADR-0017](docs/adr/0017-archive-completeness-invariants.md).
  `ratesengine-ops archive-completeness check` (PR A) — read-only
  scan + JSON Report. `… fix` (PR B) — multi-source fallback
  fetcher with shuffled source order, atomic placement, gzip
  validation, zip-bomb guards. `… verify` (PR C) — daily-cron
  shape with Prometheus textfile output, systemd timer
  (`02:17 UTC` + 5min jitter, `Persistent=true`), 4 alert rules
  (`files_missing`, `stale`, `critical_stale`, `repair_source_degraded`).
  Wires into node_exporter's textfile_collector; alerts fire from
  `deploy/monitoring/rules/archive-completeness.yml`.

- **`auth.RedisAPIKeyValidator` (#196)**: fills the [`internal/auth`](internal/auth/)
  scaffolding from PR #190 with a Redis-backed validator. Storage
  shape `apikey:<sha256-hex>` → JSON record (identifier, tier,
  scopes, expires_at, revoked_at). Plaintext keys never enter
  Redis. Sentinel mapping: missing/revoked → `ErrUnauthorized`;
  `expires_at` past → `ErrTokenExpired` (middleware sets
  WWW-Authenticate with refresh hint). Wired in `cmd/ratesengine-api`:
  `auth_mode=apikey` + Redis reachable → real validator; without
  Redis → Noop fallback so every request 503s (correct fail-loud).

- **`/v1/account/{me,usage,keys}` self-service (#197)**: three
  account endpoints from the OpenAPI spec. `/me` echoes the
  authenticated `Subject`; `/usage` returns empty array (counter
  store ships separately, wire shape locked); POST `/keys` issues
  a fresh key inheriting the caller's identifier+tier verbatim.
  New `auth.APIKeyStore` interface + `RedisAPIKeyStore`. Plaintext
  generated as `rek_<64-hex>` from `crypto/rand`; KeyID as
  `kid_<16-hex>`.

- **`/v1/history/since-inception` (#195)**: CAGG-served full
  historical series at the requested granularity. `1m / 15m / 1h /
  4h / 1d / 1w / 1mo` granularities; default `1d`; capped at 50K
  points. New `Store.HistoryPoints` against `prices_<granularity>`
  tables with the closed-bucket guard scaling per granularity.

- **`/v1/oracle/prices` (#193)**: SEP-40 `prices(asset, records)`
  passthrough. Returns the last N closed 1m VWAP buckets. Capped
  at 200 records per the SEP-40 contract.

- **`/v1/assets/{id}/metadata` + SEP-1 overlay (#192)**: new
  endpoint plus overlay handler that resolves home-domain →
  stellar.toml. Operator-curated issuer→home-domain map in
  `cfg.Metadata.IssuerHomeDomains`; on-chain AccountEntry
  observation deferred until indexer pipework lands.

- **SLO multi-window burn-rate alerts (#194)**: per
  [ADR-0009](docs/adr/0009-latency-budget.md). Three sensitivity
  tiers per SLO (fast/medium/slow burns) with both-windows-must-
  agree to suppress single-spike noise. Wired in
  `deploy/monitoring/rules/slo.yml`.

### Changed

- **`comet` source flipped `BackfillSafe: false → true`** —
  pool-identification audit landed
  ([docs/operations/wasm-audits/comet.md](docs/operations/wasm-audits/comet.md)).
  The only known mainnet Comet deployment is Blend's backstop
  pool `CAS3FL6T...` (per `docs/discovery/dexes-amms/comet.md`
  open-item resolution and the L55,261,759 mainnet snapshot in
  `blend-contracts/test-suites/`). Pool's WASM hash
  `8abc28913035c074...` fetched via `stellar contract fetch --id`
  and verified — all 5 SwapEvent body field names (`caller`,
  `token_in`, `token_out`, `token_amount_in`, `token_amount_out`)
  preserved in the binary; no upgrade since L51,499,546. The
  topic-based decoder design is robust to any future canonical
  Comet pool using the same audited WASM. **All 8 Soroban
  on-chain sources are now BackfillSafe=true.**

- **`aquarius` source flipped `BackfillSafe: false → true`** —
  pool-enumeration audit landed
  ([docs/operations/wasm-audits/aquarius.md](docs/operations/wasm-audits/aquarius.md)).
  All 313 mainnet pool contracts enumerated via router
  `get_pools_for_tokens_range()`; their current WASMs fetched via
  `stellar contract fetch`. Three unique pool-WASM hashes total
  (one volatile, one stableswap, one rewards-enhanced; 267/40/6
  pool distribution), all three containing the 4 expected
  event-name strings (`trade`, `update_reserves`,
  `deposit_liquidity`, `withdraw_liquidity`). Source-import
  topology confirmed across all three aquarius pool-type crates
  (`liquidity_pool`, `liquidity_pool_stableswap`,
  `liquidity_pool_concentrated`) — all `use
  liquidity_pool_events::Events` and dispatch to the shared
  `LiquidityPoolEvents::trade()` emitter, structurally preventing
  wire-format drift across pool types. The 6 router hashes from
  the original walk are informational only (decoder targets
  per-pool trade events, not router swap events).

- **`phoenix` source flipped `BackfillSafe: false → true`** —
  pool-enumeration audit landed
  ([docs/operations/wasm-audits/phoenix.md](docs/operations/wasm-audits/phoenix.md)).
  All 11 mainnet pool contracts enumerated via factory
  `query_pools()`; their current WASMs fetched via
  `stellar contract fetch` and analyzed. Two unique pool-WASM
  hashes total, both containing all 8 required swap-field string
  literals (`sender`, `sell_token`, `offer_amount`, `actual
  received amount`, `buy_token`, `return_amount`, `spread_amount`,
  `referral_fee_amount`) and identical contract interfaces — both
  decoder-compatible. The 5 factory + 3 multihop hashes from the
  walk are informational only (decoder targets per-pool swap
  events, not factory/multihop events).

- **`reflector-dex` and `reflector-cex` flipped `BackfillSafe:
  false → true`** — v2-era WASM (`4a64c8c8…`) fetched via
  `stellar contract fetch` and disassembled against the v3
  production hash (`df88820e…`). Contract-interface diff is
  cosmetic (one removed governance function, struct ordering);
  data-section field names identical; SDK 20.x family preserves
  `#[contractevent]` macro behavior, so v2 and v3 events have the
  same wire format. The decoder works for both. Audit evidence
  appended to
  [docs/operations/wasm-audits/reflector.md](docs/operations/wasm-audits/reflector.md);
  status flipped partial → ratified. All three Reflector variants
  now flip-completed.

- **`reflector-fx` source flipped `BackfillSafe: false → true`** —
  WASM-history audit landed
  ([docs/operations/wasm-audits/reflector.md](docs/operations/wasm-audits/reflector.md)).
  All three Reflector variants share one decoder; the walk shows two
  unique hashes total: a v2-era `4a64c8c8…` (DEX+CEX only, Feb–Apr
  2024) and the current production `df88820e…`. FX was deployed
  fresh on `df88820e…` and has never run any other hash, so the
  audit covers it deterministically. DEX + CEX stay
  `BackfillSafe: false` pending v2-era WASM disassembly to confirm
  the pre-v3 event shape matches the current decoder; that's
  documented as the next follow-up.

- **`redstone` source flipped `BackfillSafe: false → true`** —
  WASM-history audit landed
  ([docs/operations/wasm-audits/redstone.md](docs/operations/wasm-audits/redstone.md)).
  Adapter contract `CA526Y2N…` shows two WASM hashes: a 420-ledger
  (~35 min) first-deploy hotfix `b400f7a8…` (L58,758,722 →
  L58,759,141) and the current production `5e93d22c…`
  (L58,759,142 → scan-end, ~36 days stable). Per-hash review
  confirms the production hash matches the live decoder; the
  hotfix-window analysis (zero redstone trades in that 420-ledger
  range, deploy-then-hotfix pattern) supports flipping the flag with
  a documented caveat that the b400f7a8 bytes were not disassembled
  inline. Backfill against historical Redstone ranges is now
  permitted via `ratesengine-ops backfill`.

- **`band` source flipped `BackfillSafe: false → true`** —
  WASM-history audit landed
  ([docs/operations/wasm-audits/band.md](docs/operations/wasm-audits/band.md)).
  StandardReference contract `CCQXWMZV…` shows one stable WASM hash
  `6cdb9a3c…` since launch (L50,842,736 / 2024-03-19); no
  `update_contract` events through scan-end. Per-hash review
  confirms `relay` / `force_relay` function signatures + `(Symbol,
  u64)` Vec tuple order match the positional op-args reader. Backfill
  against historical Band ranges is now permitted via
  `ratesengine-ops backfill`.

- **`soroswap` source flipped `BackfillSafe: false → true`** —
  WASM-history audit landed
  ([docs/operations/wasm-audits/soroswap.md](docs/operations/wasm-audits/soroswap.md)).
  Factory + router each show one stable WASM hash across the entire
  post-Soroban window (L50,746,266 → L59,301,651, ~2024-03 → 2026-04);
  no `update_contract` events observed. Per-hash review against the
  live decoder confirms no schema divergence. Backfill against
  historical ranges is now permitted for `soroswap` via
  `ratesengine-ops backfill`. Per-instance pair-WASM enumeration is
  documented as a v2 audit follow-up. The remaining 6 on-chain Soroban
  sources (aquarius, phoenix, comet, reflector-{dex,cex,fx}, redstone,
  band) stay `BackfillSafe: false` until each source's audit lands.

- **`verify-archive -fail-on-missed` (#206)**: per
  [ADR-0017](docs/adr/0017-archive-completeness-invariants.md) X1.7.
  Off by default (preserves pre-bootstrap workflow that tolerated
  scattered missed checkpoints). On after running the
  archive-completeness bootstrap so a regression surfaces as a
  P2 ticket within 24 h instead of being hidden in info logs.

- **API consistency surfaces** per [ADR-0018](docs/adr/0018-api-consistency-surfaces.md):
  established the three-URL model — `/v1/price` (closed-bucket,
  cross-region consistent), `/v1/price/tip` (rolling window with
  last-good-price fallback, not consistent), `/v1/observations`
  (raw per-source). URL discipline as the contract enforcer; query
  parameters MUST NOT change consistency tier. Forex factor snap
  rule for chained-fiat preserves cross-region consistency on
  `/v1/price`. Implementation of `tip` + `observations` follows.

- **`flags.stale` semantic clarified** (ADR-0018): means "below
  this surface's documented baseline contract." Fires on `/v1/price`
  for closed-bucket degradation; never on `/v1/price/tip` (the
  last-good-price fallback is in-contract there); never on
  `/v1/observations` (no aggregation contract).

### Documentation

- **3 new ADRs (#198)**:
  [ADR-0017](docs/adr/0017-archive-completeness-invariants.md)
  archive completeness invariants (4 hard contracts; per-region
  asymmetric trust model — R1 leader, R2/R3 delegate via metric
  scrape with 26h staleness budget);
  [ADR-0018](docs/adr/0018-api-consistency-surfaces.md) three API
  consistency surfaces;
  [ADR-0019](docs/adr/0019-anomaly-response-and-confidence-scoring.md)
  anomaly response with per-asset MAD-based statistical baselines
  (not fixed thresholds), 3-signal AND freeze on closed-bucket only.

- **`docs/architecture/oracle-manipulation-defense.md` (#198)**:
  attack catalogue (Reflector/USTRY, Mango, Cream, Inverse,
  Polter, Harvest, bZx) + worked USTRY scenario walkthrough
  showing per-surface response under each ADR-0019 phase.

- **`docs/operations/archive-completeness.md` (#198)**: daily-cron
  design, multi-source fallback chain, Prometheus surface,
  status-page integration. Per-region behaviour details
  (R1 enforces / R2/R3 delegate).

- **`docs/architecture/launch-readiness-backlog.md` (#198)**:
  canonical 47-item launch-blocking backlog with dependency
  graph + critical path. Operator decision 2026-04-28: every
  non-deferred item ships before launch.

- **4 new operator runbooks (#198)**: `anomaly-freeze-engaged`,
  `archive-files-missing`, `archive-completeness-stale`,
  `archive-repair-source-degraded`. Wired into `alerts-catalog.md`.

- **`coverage-matrix.md` refreshed (#198)**: 22 new cross-cutting
  integrity invariant rows (X1.* archive, X2.* API surfaces,
  X3.* anomaly). Gap-triage reflects every outstanding item as
  launch-blocking.

- **SemVer policy formalised**: see
  [`docs/architecture/semver-policy.md`](docs/architecture/semver-policy.md)
  for the binding rules on `pkg/*` API stability and binary
  CalVer release tagging.

- **`GET /v1/price/batch?asset_ids=A,B,C&quote=`**: batch
  price lookup for up to 100 assets in one round-trip. Promised
  by the OpenAPI spec but previously unmounted. Missing assets
  are omitted from the response (not 404'd) so callers asking
  for 5 assets and getting 3 rows know exactly which 2 we don't
  have data for. Server-side dedupe collapses repeats; the
  envelope's `flags.stale` is the OR of per-row staleness, and
  `sources` is the union across all returned rows. Reuses the
  existing `PriceReader` interface — no storage-layer changes.

- **`GET /v1/oracle/lastprice?asset=` and
  `GET /v1/oracle/x_last_price?base=&quote=`**: SEP-40
  passthrough surface promised by the OpenAPI spec but
  previously unmounted. Returns the SEP-40 `(asset, price,
  timestamp)` shape using the same VWAP / last-trade pipeline
  that backs `/v1/price`. `lastprice` is fixed at `fiat:USD`
  quote (matches the SEP-40 contract semantic — the on-chain
  oracle has one configured quote per contract);
  `x_last_price` takes explicit base + quote. The richer
  per-source readings remain on `/v1/oracle/latest`.
  `/v1/oracle/prices` (N historical records) deferred —
  needs a CAGG read path that the aggregator's continuous-
  aggregates surface hasn't grown yet.

- **`POST /v1/price/batch`**: JSON-body variant accepting up to
  1000 `asset_ids`. Same semantics as GET; the body shape exists
  precisely to raise the GET ceiling without bloating query
  strings (a 1000-id query would blow past most reverse-proxy
  default 8 KiB header limits). Body capped at 1 MiB,
  `DisallowUnknownFields()` rejects unrecognised keys. Shared
  core (`runPriceBatch`) under both GET and POST so behaviour
  stays in lockstep.

- **`GET /v1/pairs?base=&quote=`**: single-pair activity summary
  promised by the OpenAPI spec but previously unimplemented.
  Returns the same `MarketRow` shape as `/v1/markets`, filtered
  to one pair: zero or one element. Empty array (200 OK), not
  404, when the pair has no trades — matches the
  `PairsEnvelope.data: array` contract so clients can
  distinguish "no data" from "bad request" without branching on
  status code. Backed by a new `Store.PairMarket(base, quote)`
  method on the timescale store.

- **PRs 41–73 — As-built audit + galexie tuning playbook**
  (2026-04-25): an autonomous-loop session focused on bringing
  the docs flush with the shipped code and capturing live-run
  findings. Mostly housekeeping, two small bugfixes, one
  substantive operational discovery.

  Code-side fixes:

  - **PR 66 — orchestrator `lastTickAt` UTC**: was recording in
    host local timezone while the rest of the tick used UTC;
    `Stats()` now returns consistent UTC throughout.
  - **PR 67 — orchestrator `Stats` doc**: corrected the
    "zero-copy" claim to the accurate "value-type return,
    independent snapshot."

  Galexie + archival-node operational findings:

  - **PR 57 — `docs/operations/galexie-backfill.md § Tuning`**:
    the 2026-04-25 r1 backfill ran phase 3 at ~58 ledgers/sec —
    10–25× under galexie's claimed ceiling. Bottleneck is the
    single-goroutine S3 PUT loop (verified against
    `stellar/stellar-galexie@6dec23e2:internal/uploader.go`).
    Highest-impact lever without forking is parallel
    `scan-and-fill` processes on disjoint ranges (idempotent
    via the per-object `IfNoneMatch: "*"` precondition);
    8 workers ≈ 1.5 days vs ~12 days serial. Recipe in the
    section.
  - **PR 58 — `archival-node-spec.md § 3.3.4`**: galexie
    backfill is the actually-long pole when bringing up a new
    archival node, not stellar-core catchup. Cite the live
    numbers.
  - **PR 71 — bootstrap-runbook galexie pointer**: §7
    "Catchup Timeline Expectations" now warns operators that
    the table only covers stellar-core, not galexie.
  - **PR 73 — AWS public-bucket mirror alternative**: AWS
    hosts a public Stellar dataset at
    `s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/`.
    For new-node bootstrap or DR, mirroring it is much faster
    than running scan-and-fill at all. OBSRVR's `nebu`
    archive mode reads directly from there. Documented
    trade-offs (retention floor, egress cost, loss of
    cross-validation).

  As-built doc audit (the mass of small fixes, none individually
  load-bearing — listed for the audit trail):

  - PRs 31–36 (per-source READMEs) and 32 (aggregation-plan)
    were already covered in the PRs 30–40 rollup above.
  - **PRs 38, 47, 109** dropped stale ADR-TBD / planned-package
    notes now that ADR-0010 + ADR-0014 are accepted and
    stellar-rpc is removed from r1 ingest.
  - **PRs 41, 50, 112, 130** brought the CHANGELOG, aggregate
    package doc, and canonical package doc current with the
    fiat / crypto / aggregation-plan additions.
  - **PRs 51, 53, 113, 115** captured the live-run backfill
    phase-shape + TUI status pointer in the operations
    playbook.
  - **PRs 44, 45, 106, 107** fixed `migrations/0004` collisions
    in storage-package comments and added the migrations
    manifest table.
  - **PRs 48, 55, 105, 110, 117** re-aligned OpenAPI / api-design
    with what `/v1` actually serves (`/v1/sources` listed,
    `/v1/version` enriched fields, missing meta tag, sigling
    `/v1/prices` → `/v1/price` typo).
  - **PRs 54, 111, 116, 121, 124, 125** corrected stale facts
    in r1-deployment-state, makefile, monitoring README, and
    one stray ecosystem-review entry.
  - **PRs 60, 65, 114, 122, 126, 127, 132** brought the
    operations runbook + alerts-catalog into compliance with
    the `_template.md` shape and made the "CI enforces this"
    claims honest.
  - **PRs 61, 68, 134** pulled the public Reflector v3 mainnet
    addresses into example.toml + the source-package READMEs
    (Phase-1 audit had left them as TBD).
  - **PRs 99, 131** dropped truly-stale references — PR 99
    switched canonical strkey from regex format-only validation
    to SDK-backed CRC verification (caught real bugs:
    CRC-mismatched and wrong-version-byte strkeys were being
    accepted); PR 131 dropped `withObsrvr/stellar-extract` from
    VERSIONS.md's active-deps list since it never landed in
    `go.mod`.

- **PRs 30–40 — Aggregator stack documentation, refactors, and
  Tier E** (2026-04-25): rounds out the aggregator build-out
  with as-built docs, a couple of code refactors, and the final
  verify-archive tier.

  - **PR 30 — CHANGELOG rollup** for PRs 21–29 (the entry above
    this one).
  - **PR 31–35 — Per-source READMEs**: Comet, Redstone, Band,
    SDEX, plus a single consolidated catalogue for the 10
    external connectors. Every `internal/sources/*` package now
    has a README following the same shape (what this ingests,
    topic shape, events table, quirks, files).
  - **PR 32 — `docs/architecture/aggregation-plan.md`**: the
    single anchor for the aggregator-layer design. Data flow,
    policy chain ordering, configuration surface, observability,
    API surface, boundaries, and deferred items in one place.
  - **PR 37 — strkey CRC validation via go-stellar-sdk**:
    replaces the regex-only `IsAccountID` / `IsContractID` with
    the SDK's `strkey.Decode(VersionByte*, str)`. Now rejects
    CRC-mismatched and wrong-version-byte strkeys (silently
    accepted under the regex). Resolves the standing TODO.
  - **PR 38 — drop stale ADR-TBD comment in oracle.go**:
    points the pair-vs-single-asset note at accepted ADR-0010
    instead of "TBD".
  - **PR 39 — verify-archive Tier E**: wraps `stellar-archivist
    scan` (or `rs-stellar-archivist scan`) for a full
    bucket-by-bucket sha256 audit of an archive — the fifth and
    final tier of the verification playbook. Defaults to
    scanning the local mirror at `file://<archive-root>`; any
    peer's `https://` archive URL also works.
  - **PR 40 — `/v1/sources?class=` filter**: optional class
    query parameter on the source catalogue endpoint. Useful
    for dashboards that split sources by role
    (exchange / aggregator / oracle / authority_sanity).

  Net effect: the verification playbook is fully implemented
  (Tiers A/B/D/E; Tier C deferred pending GCS public-read
  confirmation), the aggregator's design + ops surface is
  documented end-to-end, and one stable-named code path
  (canonical strkey) became stricter without API churn.

- **PRs 21–29 — Aggregator policy + observability layer**
  (2026-04-25): builds out the orchestrator from PR 182's
  passthrough VWAP into a configurable, observable, alerting-
  ready computation:

  - **PR 21 (class filter)**: orchestrator drops non-`ClassExchange`
    trades from the VWAP input set by default. Aggregator-class
    sources (CoinGecko / CMC / CryptoCompare) and oracle-class
    sources (Reflector / Redstone / Band) stay visible in
    `/v1/sources` for transparency but no longer skew the
    computed price. Inverted `DisableClassFilter` flag —
    zero value is the safer default.
  - **PR 22 (stablecoin helper)**: `internal/aggregate/stablecoin.go`
    with `FiatProxy` / `ProxyPair` / `ProxyTrade`. Maps quote-
    side stablecoins (USDT/USDC/DAI/PYUSD/USDP → USD,
    EURC/EUROC/EUROB → EUR, MXNe → MXN). Aggregator policy
    only — decoders still record the raw pair so a depeg event
    stays visible in the trade feed.
  - **PR 23 (orchestrator stablecoin wire-up)**:
    `Config.EnableStablecoinFiatProxy`. When on, a fiat-
    denominated target pair fans out to direct + stablecoin
    backers and collapses onto the target via `ProxyPair`
    before VWAP. Single-backer fetch failure logs and skips
    rather than aborting the window.
  - **PR 24 (TOML plumbing for filter flags)**: exposes
    `disable_class_filter`, `enable_stablecoin_fiat_proxy`,
    `interval_seconds`, `max_trades_per_window` in
    `[aggregate]`.
  - **PR 25 (outlier filter wire-up)**: orchestrator's
    `OutlierSigmaThreshold` (driven by `aggregate.outlier_sigma_threshold`,
    default 4.0) drops trades > σ from the window mean before
    VWAP. Applied after class + stablecoin steps so the σ
    arithmetic runs over comparable price values.
  - **PR 26 (Prometheus metrics)**: `ratesengine_aggregator_*`
    counters — ticks (by outcome), VWAP writes, empty windows,
    dropped trades (by reason: `class` / `outlier`).
  - **PR 27 (alerts + runbooks)**: three Prometheus rules
    (`aggregator_silent` P1, `aggregator_outlier_storm` P3,
    `aggregator_class_drop_spike` P3) with full runbooks.
    Baseline-comparator alerts use `offset 1h` to auto-tune to
    operator traffic.
  - **PR 28 (`GET /v1/sources`)**: surfaces `external.Registry`
    on the API so consumers can confirm a venue's class +
    `include_in_vwap` without internal access. Same metadata
    the class filter consults — they agree by construction.
  - **PR 29 (configurable pairs + windows)**: `aggregate.pairs`
    and `aggregate.windows` accept operator overrides as
    canonical pair strings (`"crypto:XLM/fiat:USD"`) and Go
    `time.Duration` strings (`"5m"`). Empty falls back to the
    binary's built-in defaults.

  Together: the aggregator can now be deployed with operator-
  chosen coverage, the class/stablecoin/outlier policy chain
  applied in order, observable via Prometheus + paged via
  Alertmanager when it goes silent or throws an unusually high
  drop rate.

- **PR 182 — Aggregator orchestrator v1** (2026-04-24): turns
  `cmd/ratesengine-aggregator` from a deliberate `os.Exit(1)`
  stub into a running binary. Rolling-window VWAP pre-computed
  on a ticker, written to Redis, consumed by the API's `/v1/price`
  — unblocks the path from "last trade, stale-flagged" degraded
  mode to fresh cached pricing.

  - `internal/aggregate/orchestrator/` (new): `Orchestrator`
    with `New(Store, Cache, Config)` + `Run(ctx)` + `Tick(ctx)`.
    On each tick, for every (pair, window) combination: fetch
    trades via `TradesInRange`, compute VWAP via existing
    `internal/aggregate/vwap.go`, write to Redis key
    `vwap:<base>:<quote>:<window-seconds>` with TTL = window.
    First tick fires immediately on startup so a fresh
    aggregator has warm keys before the API's first query.
  - **`Store` and `Cache` are interfaces**: tests substitute a
    mock Store + miniredis instead of pulling up Testcontainers
    for unit-level coverage.
  - **Built-in windows**: 5m / 1h / 24h. Operator override via
    `Config.Windows`; empty list defaults.
  - **Tick cadence**: 30s default, matches the Redis
    `price:` TTL of 60s with headroom.
  - **Built-in pair set**: XLM/BTC/ETH × USD/EUR/GBP 3×3.
  - **`formatRatFixed`** handles big.Rat → decimal-string
    conversion with truncate-toward-zero semantics (not Go's
    stdlib banker's rounding). Float encoding prohibited on
    this path (ADR-0003).
  - Binary: config load → Timescale open → Redis open (with
    dry-run ping) → orchestrator build → `Run(ctx)` until
    SIGINT/SIGTERM.
  - 7 unit tests: happy-path Redis write, empty-window skip,
    store-error recovery, multi-window writes, no-op on empty
    pair list, immediate-first-tick behaviour, `formatRatFixed`
    rounding semantics.

  **v1 policy deliberately out of scope** (each is a clean
  follow-up the Config shape already accepts):
  - Class-based filtering (only `ClassExchange` contributes).
  - Stablecoin → fiat proxy (USDT→USD, USDC→USD …).
  - Cross-pair triangulation.
  - Divergence detector against aggregator-class sources.
  - Outlier filtering before VWAP computes.

  Satisfies the "two-phase aggregator landing" plan agreed
  earlier: Phase 1 = plumbing + passthrough aggregation (no
  policy commitments); Phase 2 = class filtering + fiat proxy
  + triangulation once the CEX fleet's live data reveals real
  failure modes.

- **PR 181 — External-fleet end-to-end integration test + 0004
  migration** (2026-04-24): Phase-2 ingestion closing ceremony.
  Ties every external-source class together in a single test
  hitting a live Timescale, proving the framework + all
  interfaces + wire-up to storage work end-to-end under realistic
  shapes.

  - `test/integration/external_fleet_test.go` (new):
    `TestExternalFleet_EndToEnd` spins up **5 mock venues in
    parallel** — Binance WS (Streamer / exchange), Bitstamp WS
    (Streamer / exchange — proves multi-streamer fan-out),
    ExchangeRatesApi REST (Poller / exchange FX),
    CoinGecko REST (Poller / aggregator),
    ECB XML (Poller / authority_sanity). Each is a scripted
    `httptest` server with venue-specific fixture responses.
    Calls `external.Run`, drains events through
    `store.Insert*`, asserts trades and oracle_updates rows
    land in Timescale via `LatestTradesForPair` and
    `LatestOracleUpdateForAsset`.
  - **What it caught**:
    1. `canonical.Trade.Validate()` was rejecting `Ledger=0`.
       Off-chain sources stamp 0 deliberately (no ledger
       concept). Fixed: relaxed the Validate check; TxHash +
       Source + OpIndex already enforce uniqueness. `trade_test.go`
       updated to match.
    2. The `trades.ledger` column had a `CHECK (ledger > 0)`
       constraint at the DB level. See migration 0004.
    3. Integration test context-propagation bug: using the
       cancelled fleet context for post-drain SELECT queries
       surfaced as "context canceled". Fixed: separate
       `assertCtx` for post-drain assertions.
  - **Migration 0004** (`0004_relax_trades_ledger_for_offchain`):
    relaxes the `trades.ledger` CHECK from `> 0` to `>= 0`.
    Up path does a decompress → ALTER → re-compress dance
    because TimescaleDB blocks constraint changes on
    compressed hypertables. Down path uses `ADD CONSTRAINT ...
    NOT VALID` so the stricter constraint restores
    schema-level but doesn't block rollback against a DB with
    existing off-chain rows — operator can `VALIDATE
    CONSTRAINT` explicitly if they know it's safe.
  - **migrations_test** update: the "zero ledger" CHECK-
    rejection case flipped to an `assertInsertAccepted` call
    — `ledger=0` is now the positive invariant. Sample values
    use `binance` source + `crypto:XLM`/`crypto:USDT` pair to
    mirror real off-chain traffic.
  - Runs in ~4 seconds against a shared Timescale container.
    In a typical run: 2 trades + 120 updates inserted (120 =
    3 pollers × ~40 ticks over 2 seconds with 100ms interval
    override).

  **Phase-2 ingestion close-out**: every source class now has
  at least one reference implementation shipped + integration-
  tested. 10 off-chain venues + 10 on-chain sources + 20+ unit
  test suites (116 external-package tests alone). The framework
  proves itself; future venues drop into the established Streamer
  / Poller / Backfiller / ContractCallDecoder shapes.

- **PR 180 — ECB daily FX reference rates** (2026-04-24): first
  `ClassAuthoritySanity` connector. European Central Bank's
  official daily fix emitted as `canonical.OracleUpdate` rows
  with `source = "ecb"` — the aggregator's end-of-day
  divergence anchor against intraday VWAP drift.

  - `internal/sources/external/ecb/` (new): REST Poller against
    `https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml`.
    XML parsing (first non-JSON source in the fleet — ECB
    publishes via gesmes Envelope). Free, no auth.
  - **Role**: explicitly NOT primary pricing (cadence is one
    fix per TARGET business day). The aggregator uses ECB as
    a sanity anchor: if our computed EUR/USD ever diverges
    > 50 bps from ECB's daily close, one of the upstream feeds
    is drifting. Sovereign-authority class guarantees the
    reference is trustworthy.
  - **Inversion semantics**: ECB publishes "1 EUR = X currency"
    (e.g. USD rate 1.0825 = 1.0825 USD per 1 EUR). We invert
    to canonical "price of Asset in Quote" form (1 USD = 0.9238
    EUR → Asset=USD, Quote=EUR). Same pattern as
    ExchangeRatesApi / Polygon Forex; aggregator math stays
    uniform across FX sources.
  - **Cadence**: 6-hour poll interval default — ECB publishes
    once per EU business day ~4pm CET; 6h gives comfortable
    slack. Poller is idempotent (stable `(currency, ts)`-
    derived tx_hash); extra polls on the same day's fix
    dedup harmlessly.
  - **Pair filtering**: emits for any fiat appearing in a
    configured pair (either side), excluding EUR (the base).
    Operator configuring `XLM/USD` gets USD/EUR rate; operator
    configuring `XLM/GBP` also gets GBP/EUR.
  - 8 unit tests: happy-path inversion + fiat filter, malformed
    XML, empty cube, crypto-only pair no-op, HTTP 5xx error,
    negative-rate entry skip, PollInterval default, direct
    inversion math sanity.

  **Fed H.10 deferred** to a follow-up PR: Federal Reserve
  datadownload URLs are series-specific (different URL per
  currency pair, mixed direction conventions across series) —
  meaningful complexity over ECB's single-file shape. Captured
  as a TODO; ECB alone covers the authoritative-sovereign-
  anchor requirement for EUR-based reference while Phase 2
  closes.

- **PR 179 — CoinGecko / CoinMarketCap / CryptoCompare aggregator
  pollers** (2026-04-24): three `ClassAggregator` pollers in one
  PR. All three emit `canonical.OracleUpdate` rows —
  **divergence signal only, excluded from VWAP** per the
  class-based policy shipped in PR 169. The future divergence
  detector consumes these to flag when our computed VWAP drifts
  beyond threshold against the aggregator consensus.

  - `internal/sources/external/coingecko/` (new): free-tier
    friendly (no auth), `/api/v3/simple/price` batch endpoint.
    `tickerToID` map (XLM→stellar, BTC→bitcoin, …) because
    CoinGecko uses slug IDs not tickers — the only aggregator
    with this quirk. 7 unit tests.
  - `internal/sources/external/coinmarketcap/` (new): paid Pro
    API key via `X-CMC_PRO_API_KEY` header, `/v2/cryptocurrency/quotes/latest`.
    CMC wraps each symbol's payload in an array because
    multiple coins can share a ticker; we take the first entry
    (canonical project by CMC rank) — pinned by
    `TestPollOnce_MultipleCoinsWithSameTicker_TakesFirst`. 6
    unit tests.
  - `internal/sources/external/cryptocompare/` (new): paid API
    key via `Authorization: Apikey <KEY>`, `/data/pricemulti`.
    Simplest aggregator shape — flat `{asset: {quote: price}}`
    map. CryptoCompare returns a 200-OK error envelope
    (`{"Response":"Error",...}`) for auth failures; probe
    detection before decoding the price map. 6 unit tests.

  **Exact-combo filtering** (applied to all three): filters the
  venue's N×M response matrix down to just the (crypto, fiat)
  pairs the operator configured. Prevents cross-product noise
  in `oracle_updates`. Each pair lookup keyed on
  `"<TICKER>/<CURRENCY>"`.

  **Config**: `CoinGecko` uses shared `ExternalVenueConfig`
  (no auth). `CoinMarketCap` and `CryptoCompare` get their
  own structs with API-key fields following the
  `PolygonForex` env-override convention (env vars
  `COINMARKETCAP_API_KEY` / `CRYPTOCOMPARE_API_KEY`). All
  default-off.

  **Indexer wiring**: `defaultAggregatorPairs()` returns the
  XLM/BTC/ETH × USD/EUR/GBP 3×3 crypto-fiat matrix as the
  baseline set aggregators poll.

- **PR 178 — `backfill-external` operator CLI** (2026-04-24):
  turns the Backfiller interface from infrastructure into an
  operator tool. Historical-data ingestion is now a single
  command away; no custom scripts or direct DB writes required.

  - `cmd/ratesengine-ops/main.go`: new `backfill-external`
    subcommand. Flags: `-config`, `-source`, `-pair`, `-from`,
    `-to`, `-granularity`, `-dry-run`, `-progress-every`.
    Dispatches on `-source` to build the right venue's Streamer,
    resolves the venue-native symbol via its DefaultPairs, calls
    Backfill, inserts results into Timescale. 30-minute
    operation-wide context timeout.
  - **Venue-native symbols** on the command line, not invented
    cross-venue normalisation: `XLMUSDT` for Binance,
    `XLM/USD` for Kraken, `xlmusd` for Bitstamp, `XLM-USD` for
    Coinbase. Operators who know the venue don't relearn our
    conventions; unknown symbols surface the venue's configured
    set sorted in the error message.
  - **Dry-run mode**: fetches + synthesises trades but writes
    nothing. Prints a summary table (trade count, first/last
    timestamps, total base/quote volume, computed VWAP) so the
    operator can sanity-check a range before committing a large
    insert.
  - **Progress output**: emits one status line every
    `-progress-every` inserts (default 1000) so large backfills
    are visible without tail-f-ing logs.
  - **Example workflow** (in the binary's help text):
      ```
      ratesengine-ops backfill-external \
        -config configs/prod.toml \
        -source binance -pair XLMUSDT \
        -from 2024-01-01T00:00:00Z \
        -to   2024-12-31T00:00:00Z \
        -granularity 1h
      ```
    With stable per-candle `tx_hash` synthesis (see PRs 174 +
    177), repeated runs of the same command are idempotent —
    Timescale's `ON CONFLICT DO NOTHING` dedups.

  Imports the four external venue packages; unlocks the
  ratesengine-ops binary as the operator surface for every
  Backfiller we've shipped.

- **PR 177 — Kraken / Bitstamp / Coinbase historical backfill**
  (2026-04-24): three `Backfiller` implementations in one PR —
  the three CEX venues that had live streams but no historical
  data now cover the full range. Every CEX in our fleet is
  Streamer+Backfiller.

  Each follows the Binance pattern (one `Backfill` method on the
  existing `Streamer` type, synthesised `canonical.Trade` per
  candle at close-time) but with venue-specific quirks:

  - **Kraken** (`kraken/backfill.go`): `/0/public/OHLC`, interval
    in MINUTES, **hard cap 720 candles per response** (~30 days
    at 1h — documented as depth caveat on the Registry entry).
    Uses Kraken's own VWAP field (not close) × base volume for
    quote. Pagination via `since` param + response's `last`
    cursor. Granularity set: 1m/5m/15m/30m/1h/4h/1d/1w/15d.

  - **Bitstamp** (`bitstamp/backfill.go`): `/api/v2/ohlc/{pair}/`,
    step in SECONDS (60/180/300/…/86400/259200), limit 1000 per
    response. Deeper historical retention than Kraken — back to
    pair listing. Derives quote as close × volume (Bitstamp
    doesn't publish VWAP or quote-volume). Granularity set:
    1m/3m/5m/15m/30m/1h/2h/4h/6h/12h/1d/3d.

  - **Coinbase** (`coinbase/backfill.go`): `/products/{id}/candles`,
    granularity in SECONDS, **300 candles per response** (the
    tightest cap). **Critical trap**: Coinbase's candle array is
    **LHOC-ordered** — `[time, low, high, open, close, volume]` —
    NOT OHLC like every other venue. Parsing by index with the
    wrong assumption silently reports low as close. We read by
    index with comments documenting each slot, and
    `TestCoinbaseCandleToTrade_LHOC_Ordering` pins the correct
    behaviour. Response is newest-first; we iterate in reverse
    to emit chronologically. Granularity set: 1m/5m/15m/1h/6h/1d.

  All three require **User-Agent** for Coinbase (it rejects empty
  UA with 400); set in the HTTP client. Tx hashes are
  deterministic from (symbol, close_time_sec) across all three —
  same pattern as Binance, so repeated backfill runs hit the
  same primary key and our idempotent-insert path (ON CONFLICT
  DO NOTHING) handles dedup.

  **Registry update**: `external.Registry` flips
  `BackfillAvailable=true` for kraken / bitstamp / coinbase.
  Kraken's entry carries a comment flagging the 30-day cap so
  operators reading the map know the depth limit without having
  to read venue docs.

  13 new tests across the three packages:
  - Kraken: happy-path (5-candle single-page), invalid-range
    rejection, unsupported-granularity rejection, granularity
    map exhaustive, API error array surface (4 tests).
  - Bitstamp: happy-path, unsupported granularity, granularity
    map (3 tests).
  - Coinbase: happy-path (with reverse-order chronological
    emission verified), unsupported granularity, granularity
    map, **LHOC ordering guard** (catches the positional-field
    trap — asserts quote = close × vol, not low × vol) (4 tests).

  **Not in this PR**:
  - `ratesengine-ops backfill-external` CLI wrapper around the
    Backfiller interface. Next loop iteration.
  - ExchangeRatesApi / Polygon.io backfill — FX providers have
    different historical shapes (timeseries endpoints); deferred
    until aggregator actually needs historical FX for triangulation
    charts.

- **PR 176 — Polygon.io Forex poller** (2026-04-24): top-tier
  authoritative FX source, pre-approved by Ash as the "authority
  that will not make mistakes" entry in the external fleet. Second
  FX connector (alongside ExchangeRatesApi which is now the
  secondary/redundancy layer).

  - `internal/sources/external/polygonforex/` (new): REST Poller
    against the snapshot endpoint
    `/v2/snapshot/locale/global/markets/forex/tickers`. One call
    returns every forex ticker globally — fits the Poller
    interface cleanly, avoids the per-pair /v1/conversion/ call
    amplification that would otherwise burn rate-limit budget.
  - **Tier requirement documented**: Advanced tier ($199/mo+) for
    the snapshot endpoint. Lower tiers (Starter $29/mo, Developer
    $99/mo) produce ErrAPIRejected at first poll. The pluralised
    "pay the good tier" expectation is baked into events.go's
    package doc so future operators don't accidentally pick a
    tier that silently fails.
  - **Ticker parser**: `C:USDEUR` → (base=USD, quote=EUR).
    Case-insensitive input, strict 6-char length check, 7 unit
    tests (`TestParseCurrencyTicker`).
  - **Mid-price from ask/bid**: `(a + b) / 2` when both sides
    present, single-side fallback when one is missing, skip when
    both zero. Matches institutional FX convention where the
    spread is tight enough that mid is the authoritative
    reference rate.
  - **Rate inversion**: venue returns "1 base = X quote" quotes
    (e.g. USD=EUR 0.9235 meaning 1 USD = 0.9235 EUR). We invert
    to "1 EUR = 1/0.9235 USD = 1.0828" before stamping the
    OracleUpdate. Same asset/quote semantics as ExchangeRatesApi
    so aggregator math across both FX sources is uniform.
  - **Base-filter + pair-filter**: snapshot is global, we filter
    by `p.Base` (only tickers with that base) AND by the
    configured pair list's fiat quote set (don't emit for
    currencies no one queries). Cuts snapshot size ~10× for
    G10-only deployments.
  - **Config**: `PolygonForexVenueConfig{Enabled, APIKey, Base}`.
    APIKey via env override `POLYGON_API_KEY` at
    `config.ApplyEnvOverrides()` time (same secret-field pattern
    as ExchangeRatesApi + Postgres DSN).
  - 10 unit tests: empty-key rejection, happy-path with
    inversion + filter (EUR/GBP land, JPY filtered out),
    `status: "ERROR"` API rejection, 401 unauthorized, 429 rate
    limit, malformed ticker per-entry skip, ticker parser
    exhaustive, mid-price edge cases (both/ask-only/bid-only/
    both-zero), wrong-base ticker skip, PollInterval default.

  **Operator action required to enable**:
   1. Subscribe to Polygon.io Advanced tier.
   2. Set `POLYGON_API_KEY` in the indexer's env.
   3. Flip `[external.polygon_forex].enabled = true` in config.
   Connector emits OracleUpdates into `oracle_updates` table
   with `source = "polygon-forex"` — aggregator consumes
   alongside ExchangeRatesApi for FX triangulation.

- **PR 175 — ExchangeRatesApi FX poller + Poller runtime**
  (2026-04-24): first `external.Poller` implementation; FX side
  of the external fleet comes online.

  - `internal/sources/external/runner.go`: Poller support added
    — per-poller goroutine with a ticker at `PollInterval()`,
    fans `PollOnce` outputs (`[]canonical.Trade` + `[]canonical.OracleUpdate`)
    into the shared sink wrapping them as `TradeEvent` /
    `UpdateEvent`. First poll fires immediately on startup (not
    after the first interval elapses) so fresh data is visible
    within seconds of indexer launch. Transient `PollOnce` errors
    are logged + counted but don't stop the ticker — expected
    behaviour for REST sources hitting rate limits or network
    blips.
  - `internal/sources/external/exchangeratesapi/` (new): REST
    Poller against `https://api.exchangeratesapi.io/v1/latest`.
    - **Emits OracleUpdates, not Trades** — an FX reference rate
      is a computed benchmark, not an executed trade. Consumed
      by the future triangulation layer as the authoritative
      `<fiat>/<base>` cross rate.
    - **Rate inversion**: venue returns `base → symbol` rates
      (e.g. USD base, EUR=0.9235 meaning 1 USD = 0.9235 EUR).
      We invert to canonical "price of <asset> in <quote>"
      form (EUR = 1.0828 USD) before stamping the OracleUpdate.
    - **Tier awareness**: paid-tier requirement documented
      inline — free tier's EUR-only base is rejected at poll
      time via base-mismatch detection. Targets Professional
      tier minimum ($29.99/mo) for USD base + 1-min cadence +
      redistribution rights.
    - **API key via env override**: `APIKey` field follows the
      same secret-field convention as `StorageConfig.PostgresDSN`
      — env var `EXCHANGERATESAPI_KEY` overrides the TOML value
      at `config.ApplyEnvOverrides()` time. Production configs
      keep the TOML value empty.
    - **Pair resolution**: poller scans the configured pair list,
      extracts unique fiat symbols, and requests them in one
      batch call. Crypto-base pairs (XLM/USD, BTC/USD) are
      silently skipped — FX poller doesn't speak crypto, so a
      mixed-pair config is normal.
    - **Unknown currency skip**: venue occasionally returns
      exotic codes (ZZZ test currency, newly added EM symbols);
      skipped per-entry rather than aborting the whole poll.
  - Config: `ExchangeRatesApiVenueConfig{Enabled, APIKey, Base}`
    added to `ExternalConfig`. Default Base is USD.
  - Indexer wiring: `defaultFXPairs(base)` helper returns a
    G10-ish fiat set (EUR, GBP, JPY, CAD, AUD, CHF, NZD, SEK,
    NOK, MXN) as `canonical.Pair` values against the configured
    base. Operator overrides via `p.Symbols` when needed.
  - Tests: 11 total — 2 new runner tests (Poller immediate-fire
    + non-positive-interval rejection), 9 ExchangeRatesApi tests
    (happy-path with inversion, API rejection, base mismatch
    rejection, unknown-currency skip, crypto-pairs silent no-op,
    HTTP 5xx error, PollInterval default, symbol resolution
    excludes base, empty-key rejection).

- **PR 174 — Binance historical backfill** (2026-04-24): first
  `external.Backfiller` implementation. Completes Binance's triple
  capability (live stream + historical candles); every subsequent
  venue's backfill mirrors this shape.

  - `internal/sources/external/binance/backfill.go` (new):
    `Streamer.Backfill(ctx, pair, from, to, granularity)` hits
    `GET /api/v3/klines`, synthesises one `canonical.Trade` per
    candle bucket.
  - **Candle → Trade synthesis**: `Timestamp = close-time`,
    `BaseAmount` = base-asset volume (field 5), `QuoteAmount` =
    quote-asset volume (field 7), scaled at 10^8 integer (same
    `externalAmountDecimals` convention as live stream).
    Open/high/low dropped — consumers who need full OHLC candles
    read from the Timescale continuous aggregates (1m/15m/1h/4h
    /1d/1w/1mo) instead.
  - **Stable tx_hash** across reruns: `backfillTxHash(symbol,
    close_time_ms)` yields a 64-char hex deterministic from the
    bucket's close time. Repeated backfill runs hit the same
    primary key → idempotent insert, no duplicate rows.
  - **Pagination**: Binance caps 1000 candles per request; we
    serially advance `startTime` after each full-page response.
    ~9 requests for 1 year of hourly data. Serial, not parallel
    — respects the per-minute 6000-weight rate-limit budget (each
    klines call costs weight 2).
  - **Granularity support**: 1m / 3m / 5m / 15m / 30m / 1h / 2h
    / 4h / 6h / 12h / 1d / 1w — covers the RFP's listed
    timeframes (1 min, 15 min, 1h, 4h, 1d, 1w) plus common
    intermediates. Unsupported Durations return an error before
    any HTTP call.
  - **Zero-volume candles skipped**: buckets with base=0 or
    quote=0 provide no price signal and would divide-by-zero in
    downstream VWAP math.
  - 8 unit tests: single-page, pagination across 1000-candle
    boundary (1800-candle total), invalid-range rejection,
    unsupported-granularity rejection, granularity map
    exhaustive, empty-response (0 trades), zero-volume skip,
    HTTP-429 surfaces as error.

  **Not in this PR**:
  - `ratesengine-ops backfill-external --source binance --pair
    XLM/USDT --from ... --to ... --granularity 1h` CLI wiring —
    exposes Backfill via an operator command. Deferred to a
    follow-up once the ops binary grows the subcommand shape.
  - Kraken / Bitstamp / Coinbase backfill implementations —
    each reuses the same pattern, different REST endpoints:
    Kraken's `/0/public/OHLC` (capped at 720 intervals),
    Bitstamp's `/api/v2/ohlc/{pair}/`, Coinbase's
    `/products/{id}/candles`. Next loop iterations.

- **PR 172 + 173 — Bitstamp + Coinbase streamers** (2026-04-24):
  two CEX venues shipped in a single loop iteration — both reuse
  the Streamer + DefaultPairs + indexer-wiring pattern
  established by Binance and Kraken.

  **PR 172 — Bitstamp** (`internal/sources/external/bitstamp/`):
  - EUR/GBP XLM depth (XLM/USD, XLM/EUR, XLM/GBP, XLM/BTC) +
    BTC/USD, BTC/EUR, ETH/USD.
  - One subscribe frame per channel — Bitstamp doesn't accept a
    symbol array like Kraken/Coinbase. We send N sequential
    `bts:subscribe` messages on connect.
  - Uses the `amount_str` / `price_str` string fields
    (authoritative) rather than the float64 siblings — i128
    invariant.
  - Honours `bts:request_reconnect` (Bitstamp's ~hourly
    rebalance signal) by closing + reconnecting via the normal
    backoff path. Logged at info rather than warn since it's
    expected behaviour.
  - Microtimestamp parsing (string μs since epoch) with a
    seconds-timestamp fallback for defensive frame variation.
  - 8 unit tests: happy-path trade, request-reconnect surface,
    subscription-succeeded ignore, unknown-event ignore,
    unknown-channel skip, malformed JSON, missing `*_str`
    fields, microsecond fallback.

  **PR 173 — Coinbase Exchange** (`internal/sources/external/coinbase/`):
  - US price discovery — the net-new venue vs `~/code/rates`
    (Coinbase wasn't in the reference system).
  - Targets **Coinbase Exchange** (ex-Pro API, public WS, no
    auth needed for `matches` channel) — NOT Coinbase Advanced
    Trade (retail OAuth, different URLs, heavier rate limits).
    Distinction documented in events.go.
  - Single subscribe with product_ids array covers every pair
    on one connection.
  - Numbers arrive as strings natively — no json.Number dance.
  - Handles both `match` (live) and `last_match` (one-per-
    product on subscribe — carries a real historical trade,
    emitted same as match).
  - `type:"error"` frames surface as ErrSubscriptionRejected
    so the streamer logs loudly on bad product_id config
    instead of tight-looping.
  - 9 unit tests: match happy-path, last_match emission,
    subscriptions ack ignore, error-frame → rejection,
    unknown-product skip, malformed JSON, unknown-type ignore,
    tx-hash dash-normalisation, precision round-trip.

  Both wired into `cmd/ratesengine-indexer` with their
  `External.<venue>.Enabled` toggles (default false — no
  network egress on fresh deployments).

- **PR 171 — Kraken WS v2 streamer** (2026-04-24): second CEX
  connector, widest XLM-fiat coverage of any venue we integrate.
  Native pairs for XLM in USD, EUR, GBP, AUD, CAD, CHF (6 fiats
  directly quoted — no stablecoin proxy needed).

  - `internal/sources/external/kraken/` (new): 4 files following
    the same shape as binance. Subscribes to v2 `trade` channel
    via a JSON method call (vs Binance's URL-based
    subscription); decodes snapshot + update frames; ignores
    heartbeat / status / subscribe-ack frames inline.
  - **Precision handling**: Kraken's v2 API sends qty / price as
    JSON *numbers* (not strings). We decode via `json.Number`
    (via `dec.UseNumber()`) to preserve the original decimal
    representation — float64 is precision-safe at Kraken's 8-dp
    precision but the i128 invariant (ADR-0003) says no floats
    on the price path.
  - **Default pair set**: XLM across all 6 Kraken fiats + BTC/USD
    + ETH/USD. Covers the RFP's "major pairs" requirement for
    XLM without any per-operator tuning. Operator enables via
    `external.kraken.enabled = true` in config.
  - Indexer wiring mirrors Binance: `cfg.External.Kraken.Enabled`
    gates the connector; `startExternalConnectors` appends to
    the same `StreamerSpec` list fed to `external.Run`; shutdown
    path unchanged.
  - Tests: 13 total — 10 parse-layer (happy-path trade,
    snapshot-multi-entry, heartbeat / status / subscribe-ack
    ignored, unknown-symbol skip, malformed-JSON, precision
    cross-check against Binance scaling, symbol-normalised
    hashes) + 3 streamer-level (end-to-end with scripted
    httptest WS server that captures the subscribe request,
    reject empty/unconfigured pairs).

  **Behaviour note**: Kraken delivers a ~50-trade snapshot on
  subscribe. We emit every entry to Timescale with its real
  historical timestamp — small backfill effect on first connect
  that dedupes against future `ratesengine-ops backfill` runs
  via the synthesised tx_hash (symbol + trade_id).

- **PR 170 — Indexer wiring for external connectors** (2026-04-24):
  external streamers now launch from the same `ratesengine-indexer`
  process, share the same event sink, and feed the same Timescale
  trades hypertable as on-chain decoders. End-to-end off-chain
  ingestion is operational (pending config opt-in).

  - `internal/sources/external/runner.go` (new): `Run(ctx,
    streamers, pollers, sink, logger)` fans N streamer channels
    into the shared `consumer.Event` sink, wrapping each
    `canonical.Trade` in `external.TradeEvent`. Returns a
    `wait()` function the indexer's shutdown path calls before
    closing the sink — guarantees no in-flight writes on a
    closed channel. 4 unit tests cover empty-runner behaviour,
    fan-out + TradeEvent wrapping, synchronous Start-error
    propagation, and ctx-cancel cleanup.
  - `internal/sources/external/binance/pairs.go` (new):
    `DefaultPairs()` / `DefaultPairList()` — hardcoded common
    set (XLMUSDT, XLMBTC, BTCUSDT, ETHUSDT). Operator enables
    Binance in config, gets those pairs streaming with zero
    further configuration. Per-venue pair override YAML is a
    follow-up PR once the fleet stabilises.
  - `internal/config/config.go`: new `ExternalConfig` +
    `ExternalVenueConfig{Enabled bool}`. All external venues
    default to `enabled: false` — no network egress until
    operator opts in, eliminating a "fresh deployment
    accidentally streams from Binance" failure mode.
  - `cmd/ratesengine-indexer/main.go`: new
    `startExternalConnectors(ctx, cfg, events, logger)` helper
    builds enabled venues, calls `external.Run`, returns the
    wait func. Threaded into the shutdown sequence between
    ledgerstream stop and events-channel close so drain is
    ordered. Sink type-switch gains `case external.TradeEvent`
    + `case external.UpdateEvent` → existing `persistTrade` /
    `persistOracle` helpers.

  **Behaviour**: with `external.binance.enabled=true` in config
  and no firewall blocking `stream.binance.com:9443`, the indexer
  starts Binance alongside the Galexie dispatcher loop and
  writes XLMUSDT / BTCUSDT / ETHUSDT / XLMBTC trades into the
  `trades` hypertable with `source="binance"`. Stablecoin →
  fiat mapping remains aggregator-side policy (not baked into
  ingest); these rows store the actual pair, not a normalised
  XLM/USD.

  **Not in this PR** (immediate follow-ups):
  - Kraken + Bitstamp + Coinbase streamers (each ~100-150 lines,
    reuse the Streamer + DefaultPairs pattern).
  - Binance historical backfill (`Backfiller.Backfill` body
    against `/api/v3/klines`).
  - Polygon.io Forex poller + ExchangeRatesApi poller (first
    paid-license sources; waiting on operator to provision keys).
  - Aggregator connector pollers (CoinGecko / CoinMarketCap /
    CryptoCompare, class=aggregator → divergence-only).
  - Sovereign anchors (ECB + Fed H.10 daily polls).
  - Integration test that spins up an `httptest` WS server, runs
    the full indexer with Binance enabled, asserts trades land
    in Timescale via `LatestTradesForPair`.

- **PR 169 — External-connector framework + Binance streamer**
  (2026-04-24): first off-chain ingest subsystem. Parallel to the
  dispatcher path — runs its own goroutines speaking HTTPS /
  WebSocket to vendor APIs, but converges on the same canonical
  types + Timescale hypertables.

  - `internal/sources/external/framework.go` (new): three
    orthogonal interfaces — `Streamer` (live WS), `Poller` (REST
    tick), `Backfiller` (historical OHLC). A venue implements
    whichever subset it supports; most CEXes will be
    `Streamer+Backfiller`, aggregators + FX REST feeds are
    `Poller+Backfiller`, sovereign sanity anchors are `Poller`-only.
    Generic `TradeEvent` / `UpdateEvent` wrappers so the indexer
    sink's type-switch gains one case per event kind, not per
    venue.
  - `internal/sources/external/registry.go` (new): single source-
    of-truth map of every venue's `Class` (`exchange` | `aggregator`
    | `oracle` | `authority_sanity`), default weight, VWAP inclusion,
    paid-license flag, backfill availability. Aggregator queries
    this at VWAP compute time to decide contribution. Covers every
    existing on-chain source (soroswap, aquarius, phoenix, comet,
    sdex, reflector×3, redstone, band) + planned off-chain venues
    (binance, kraken, bitstamp, coinbase, bitfinex, polygon-forex,
    exchangeratesapi, coingecko, coinmarketcap, cryptocompare,
    ecb, fed-h10). Unknown sources fail closed: visible in
    `/v1/sources` as `included_in_vwap=false` so ops can see the
    bad entry, but don't silently contribute to aggregation.
  - `internal/sources/external/binance/` (new): first reference
    implementation. Streamer connects to Binance's public combined
    `@aggTrade` WebSocket, parses frames per the verified wire
    spec, emits `canonical.Trade` values. Reconnects with bounded
    exponential backoff + ±25% jitter to avoid thundering-herd on
    shared venue outages. Pair map is explicit (no blind
    auto-subscribe) — operator configures which symbols to
    stream; unknown symbols on the wire are counted + dropped,
    stream stays up.
  - **External-source amount scaling convention**: every off-chain
    source normalises `canonical.Trade.BaseAmount` /
    `QuoteAmount` to a fixed **10^8** integer scale
    (`externalAmountDecimals = 8`). Matches most crypto-native
    venue precision + Redstone's on-chain scale. Aggregator
    queries `external.Lookup(trade.Source).Class` to know which
    side of the on/off-chain boundary a trade came from (on-chain
    uses per-asset decimals). Documented in
    `parse.go:externalAmountDecimals`.
  - **Stablecoin fiat-proxy policy**: ingest stores the actual
    pair (e.g. `XLM/USDT`). The aggregator applies a fiat-proxy
    table (`USDT→USD`, `USDC→USD`, `PYUSD→USD`, `EUROC→EUR`,
    `EUROB→EUR`, `MXNe→MXN`) at VWAP compute time. Keeps the
    stored data honest; depeg failure mode surfaces cleanly
    rather than hiding behind eager normalisation. Per Ash's
    guidance (memory: feedback_production_artifacts).
  - Dep: `github.com/coder/websocket v1.8.14` — pure-Go,
    context-aware, minimal transitive footprint.
  - Tests: 11 unit tests cover the parser, decimal-string scaling,
    tx-hash synthesis, URL build, and end-to-end WebSocket
    streaming against an `httptest` mock server (2-frame scenario,
    verifies trade emission order + stamped fields).

  **Not in this PR** (immediate follow-ups):
  - Backfill implementation for Binance (GET /api/v3/klines →
    synthesised `canonical.Trade` per candle; the interface is
    wired but the body is pending).
  - Wiring into `cmd/ratesengine-indexer` — external connectors
    launched alongside the dispatcher goroutine, sink type-switch
    gains `case external.TradeEvent` / `case external.UpdateEvent`.
  - Additional venues: Kraken, Bitstamp, Coinbase (reuse the
    Streamer interface).
  - Polygon.io Forex + ExchangeRatesApi Pollers.
  - CoinGecko / CoinMarketCap / CryptoCompare aggregators
    (divergence-only, not VWAP).
  - ECB + Fed H.10 daily sanity anchors.

- **PR 168 — Band decoder + ContractCallDecoder interface** (2026-04-24):
  Third oracle integration, and first source that doesn't emit
  events. Band's Soroban StandardReference contract publishes zero
  events on `relay()` / `force_relay()` (verified against pinned
  `bandprotocol/band-std-reference-contracts-soroban` source). A
  conventional event-path Decoder would never fire on a Band update.

  - `internal/dispatcher/dispatcher.go`: new `ContractCallDecoder`
    interface (`Name`, `Matches(contractID, functionName)`,
    `Decode(ContractCallContext)`) + `AddContractCallDecoder`
    registration method + `dispatchContractCall` loop that runs
    per successful InvokeContract op regardless of whether the
    op emitted events. `extractInvokeContractArgs` generalized to
    `extractInvokeContractCalls` — now returns per-op
    `(contractID, functionName, args)` snapshots feeding both
    `events.Event.OpArgs` (Redstone-style event path) and the
    new call-path routing.
  - `internal/sources/band/` (new package): four files in the
    house convention. Decoder matches on `(StandardReference
    contract, {relay | force_relay})`. Decodes `(from, symbol_rates,
    resolve_time, request_id)` for `relay` and the 3-arg subset
    for `force_relay` (no `from` — admin-only path; observer
    falls back to op/tx source). Emits one `OracleUpdate` per
    `(Symbol, u64)` entry at 9 decimals (E9 per
    `band-soroban/src/constant.rs`), USD-quoted. `USD` symbol
    skipped per contract special-case. Timestamp sourced from
    `resolve_time` (UNIX seconds, verified against
    `band-soroban/src/storage/ref_data.rs:56`).
  - `internal/config/`: new `BandOracleConfig{StandardReferenceContract}`,
    `"band"` in `KnownSources`, cross-section + strkey validation.
  - `cmd/ratesengine-indexer/main.go`: `buildDispatcher` gains
    `case band.SourceName: callDecoders = append(...)`; new
    `AddContractCallDecoder` loop at the end of the builder;
    sink type-switch adds `case band.UpdateEvent`.
  - `test/integration/ledgerstream_to_storage_test.go`: new
    subtest `soroban LCM with band relay (no events) lands
    OracleUpdates`. Builds a Soroban envelope whose
    InvokeHostFunction op is `StandardReference.relay(from,
    [("BTC", e9), ("XLM", e9)], resolve_time, request_id)` with
    **SorobanMeta.Events explicitly empty** — proves the
    call-path runs independently of the event-path. Asserts both
    rows land in `oracle_updates` via `LatestOracleUpdateForAsset`.
  - Unit tests cover: happy-path `relay`, happy-path `force_relay`
    (3-arg), USD-symbol skip, unknown-symbol per-entry skip,
    empty rates rejection, too-few-args malformed, decoder
    Matches predicate (accepts relay/force_relay only).

  **Architectural significance:** this is the first decoder that
  bypasses events entirely. The ContractCallDecoder interface
  generalizes — any future Soroban source whose contract reads/
  writes storage without emitting events (Orbit supply, custom
  adapter contracts, future admin-only oracle paths) plugs into
  the same hook. See `docs/discovery/oracles/band.md` for full
  analysis.

- **PR 167 — Comet decoder** (2026-04-23): third on-chain DEX after
  Soroswap + Aquarius + Phoenix. Balancer-v1-style weighted AMM; the
  Blend backstop pool runs on Comet, so this picks up BLND/USDC
  pricing even before broader Comet adoption on pubnet.
  - `internal/sources/comet/` (new package): four files in the
    house convention. Topic = `(Symbol("POOL"), Symbol("swap"))`;
    body = `Map{caller, token_in, token_out, token_amount_in,
    token_amount_out}`. Unlike Soroswap (pair registry) or Phoenix
    (8-event correlation), Comet's swap event is fully
    self-contained — token identities live in the body by field
    name, so the decoder has zero state and no cross-event
    correlation. Matches the Aquarius shape most closely: one
    event → one trade, base = token_in, quote = token_out.
  - `cmd/ratesengine-indexer/main.go`: buildDispatcher gains
    `case comet.SourceName: ...`; sink type-switch gains
    `case comet.TradeEvent`. `config.KnownSources` adds `"comet"`.
  - `test/integration/ledgerstream_to_storage_test.go`: new
    subtest `soroban LCM with comet POOL.swap lands Trade` pairs
    the now-generic `seedSorobanLedger` with a purpose-built
    POOL.swap ContractEvent, runs through the full pipeline, and
    asserts `LatestTradesForPair` returns the row with correct
    source / base amount / quote amount / taker / ledger.
    Removed the reflector-specific `sanityCheckReflectorTopics`
    from `seedSorobanLedger` — the helper is now source-agnostic.
  - Unit tests cover: classify (POOL,swap match, order-swapped
    topic rejection), happy-path decode, non-positive amounts
    rejection, wrong-topic rejection, missing body field
    malformed.

  **Not in this PR** (follow-ups):
  - `join_pool` / `exit_pool` / `deposit` / `withdraw` decoding —
    needed once the aggregator wants live pool-state tracking
    for the spot-price formula (requires reserves + weights).
  - Blend backstop pool address pinning — for targeted BLND/USDC
    pricing without subscribing to every POOL.swap on pubnet.
  - Real mainnet fixture capture.

- **PR 166 — RedStone decoder + OpArgs plumbing** (2026-04-23):
  Second on-chain oracle shipped after Reflector. Closes the long
  path from `Galexie → dispatcher → redstone.Decoder →
  timescale.oracle_updates` for the 4 mainnet feeds currently
  mappable to canonical assets (BTC, ETH, USDC, XLM).
  - `internal/events/event.go`: new `OpArgs []string` field on
    `events.Event`. Carries the base64 SCVal arguments of the
    InvokeHostFunction op that produced the event, populated by
    the dispatcher when the op is an InvokeContract call.
    Optional/omitempty — existing RPC fixture JSON round-trips
    unchanged. Decoders that don't need args (reflector, soroswap,
    aquarius, phoenix) continue to ignore it.
  - `internal/dispatcher/dispatcher.go`: `extractInvokeContractArgs`
    walks the tx envelope's operations once per tx and returns a
    parallel `[][]string`. Events inherit the args of their
    producing op. Marshaling failures degrade gracefully to an
    empty slot (decoders that require args surface the gap
    themselves).
  - `internal/sources/redstone/` (new package): four files following
    the house convention. Topic = `Symbol("REDSTONE")`; body =
    `Map{updater: Address, updated_feeds: Vec<PriceData>}` where
    `PriceData = {price: U256, package_timestamp: u64,
    write_timestamp: u64}`. Feed IDs live in the InvokeContract
    op args (`write_prices(updater, feed_ids, payload)`), NOT in
    the event body — the decoder zips them one-to-one with a
    strict length guard (`ErrFeedIDCountMismatch`) so a
    freshness-verifier rejection can't mis-attribute prices.
    Timestamp is taken from the per-feed `package_timestamp` (the
    oracle's signing time), matching Reflector's pattern of
    preferring oracle-declared time over ledger close time.
  - `internal/scval/scval.go`: new `AsAmountFromU256` accessor.
    RedStone's price field is 256-bit — most other Soroban
    numerics stop at i128/u128 per ADR-0003, so this is the first
    u256 decoder path in the codebase. Backed by
    `canonical.FromUInt256Parts` which assembles the four 64-bit
    words big-endian.
  - `internal/canonical/amount.go`: new `FromUInt256Parts`
    constructor. Composes HiHi/HiLo/LoHi/LoLo → `*big.Int` with
    left-shift chaining, preserving the full u256 range in our
    existing Amount wrapper.
  - `internal/config/`: new `RedstoneOracleConfig` with a single
    `adapter_contract` field (the 19 per-feed proxies emit no
    events — all activity is on the single Adapter).
    `KnownSources` gains `"redstone"`; cross-section validation
    requires the contract address when the source is enabled.
  - `cmd/ratesengine-indexer/main.go`: `buildDispatcher` registers
    `redstone.NewDecoder(cfg.Oracle.Redstone.AdapterContract)`
    when the source is enabled; event-sink type-switch gains
    `case redstone.UpdateEvent: persistOracle(…)`.
  - `test/integration/ledgerstream_to_storage_test.go`: new
    subtest `soroban LCM with redstone write_prices lands
    OracleUpdates`. Constructs a full Soroban envelope whose
    InvokeHostFunction op calls `write_prices(updater,
    ["BTC","ETH"], payload)`, pairs it with a WritePrices event
    body carrying two U256 prices, and asserts both OracleUpdate
    rows land in Timescale via `LatestOracleUpdateForAsset`.
    Proves the full OpArgs → zip → canonical attribution chain
    works under realistic bytes.
  - Unit tests cover: classify, happy-path two-feed, feed-id
    count mismatch, missing op args, unknown-feed per-entry skip,
    all-unknown empty updates, non-REDSTONE topic rejection.

  **Not in this PR** (follow-ups tracked against
  docs/discovery/oracles/redstone.md):
  - RWA feed mappings (BENJI, GILTS, CETES, TESOURO, USTRY, etc.)
    — needs a canonical asset variant for tokenized real-world
    assets.
  - EUROC/EUR, MXNe, PYUSD — stablecoin-to-fiat mapping decisions.
  - Real mainnet fixture capture (`scripts/dev/capture-redstone-
    fixtures.sh`).

- **ADR-0013 accepted** (2026-04-23): adopt
  `github.com/stellar/go-stellar-sdk/xdr` for SCVal decoding in
  Soroban source connectors.
- `internal/scval/` — narrow SCVal helper wrapping the SDK's xdr
  package. Primitives: `Parse`, `EncodeSymbol` / `MustEncodeSymbol`,
  `AsSymbol` / `AsU64` / `AsAmountFromI128` / `AsAmountFromU128` /
  `AsAddressStrkey` / `AsVec` / `AsMap` / `AsTupleN` /
  `MapField` / `MustMapField` / `DecodeAddressOrSymbol`. Re-exports
  `ScVal` + `ScMapEntry` so connectors never import `xdr` directly.
  Golden regression pins the base64 wire bytes for two canonical
  symbols so an SDK upgrade that changes encoding trips a test.
- Reflector decoder ported off stubs. Real `TopicSymbol*` SCVal
  constants computed at init via `scval.MustEncodeSymbol`.
  `decodeUpdate` now pulls the timestamp from `topic[2]` (per the
  real `#[contractevent]` declaration in
  `reflector-contract/oracle/src/events.rs:4-10`), handles both
  `Asset::Stellar(Address)` and `Asset::Other(Symbol)` union arms,
  and surfaces `ErrUnknownFiatSymbol` when an unlisted symbol is
  seen. End-to-end decoder tests in `decode_test.go` use SDK-encoded
  fixtures; `test/fixtures/reflector/README.md` documents the
  real-mainnet capture workflow (pending operator capture).
- `scripts/dev/capture-reflector-fixtures.sh` — capture real
  Reflector update events from a live stellar-rpc into fixture
  JSON per WASM hash.
- 10 real mainnet Reflector fixtures captured under
  `test/fixtures/reflector/v6-2026-04-23/` (4 DEX, 3 CEX, 3 FX).
  `real_fixture_test.go` regression-replays every fixture through
  the decoder. CEX fixtures are currently `t.Skip`ped pending
  crypto-ticker modeling (tracked as PR 164e).
- ADR-0010 fiat allow-list extended with ARS, CLP, COP, IDR, ILS,
  MYR, NOK, PHP, PLN, SEK, THB, UAH, VND — observed in Reflector's
  FX oracle payload during 164a capture.
- **PR 164b**: Soroswap decoder ported off stubs. Real `TopicPrefix*`
  / `TopicSymbol*` constants (String for prefix, Symbol for event
  name), `decodeSwap` + `decodeNewPair` against SDK XDR, factory
  `new_pair` registry wired into the consumer.
- `scval.EncodeString` / `MustEncodeString` / `AsString` — needed
  because Soroswap's topic[0] is `ScvString`, not `ScvSymbol` like
  Reflector's.
- `scripts/dev/encode-topics` — tiny Go CLI for printing base64-
  encoded SCVal::Symbol / SCVal::String wire bytes. Used when
  hardcoding topic blobs into shell capture scripts.
- `scripts/dev/capture-soroswap-fixtures.sh` + `test/fixtures/soroswap/`
  — capture + pin-per-WASM-hash layout matching the Reflector one.
  8 real mainnet swap+sync fixtures land under
  `v1-2026-04-23/`; `real_fixture_test.go` decodes them
  end-to-end. No `new_pair` captures yet (infrequent on mainnet).
- **PR 164c**: Aquarius trade decoder ported off stubs. Real topic
  classification (`TopicSymbolTrade` via scval init), `decodeTrade`
  with assets pulled directly from topics (`token_in` / `token_out`
  / `user` in slots 1–3), body decoded as positional 3-tuple
  (sold_amount, bought_amount, fee) via `scval.AsTupleN`.
  Server-side filter subscribes with `[TopicSymbolTrade, "*",
  "*", "*"]`.
- `scripts/dev/capture-aquarius-fixtures.sh` + `test/fixtures/aquarius/`
  — 10 real mainnet trade captures under `v2-2026-04-23/` (6
  unique tx_hashes), decoded end-to-end by
  `real_fixture_test.go`.
- **PR 164d**: Phoenix swap decoder ported off stubs. Real
  `TopicSymbol*` constants (all `ScvString`, since both topic slots
  are string literals in the pool contract), real `sdkDecodeAddress`
  / `sdkDecodeAsset` / `sdkDecodeI128` for the three body-SCVal
  shapes Phoenix emits. Server-side filter subscribes with
  `[TopicSymbolSwap, "*"]` — a single filter catches all 8
  per-field events.
- `scripts/dev/capture-phoenix-fixtures.sh` + `test/fixtures/phoenix/`
  — 5 complete 8-event swap fixtures (40 field events) under
  `v1-2026-04-23/`. Real-fixture test replays each through the
  `RawSwap` collator + `decodeSwap()`, the same path
  `processPage` drives at runtime.
- **PR 164e**: **ADR-0014 accepted** — `AssetCrypto` variant added
  as sibling to `AssetFiat`. Wire form `crypto:<TICKER>`; initial
  allow-list of 22 tickers (BTC, ETH, USDT, USDC, SOL, XRP, ADA,
  AVAX, DOT, LINK, TON, BNB, DOGE, MATIC, SHIB, NEAR, ATOM, TRX,
  UNI, BCH, LTC, XLM). Threaded through `canonical.Asset.String`,
  `Validate`, `ParseAsset`, JSON round-trip. Parallel test file
  `asset_crypto_test.go`.
- Reflector decoder now dispatches `Asset::Other(Symbol)` through
  fiat → crypto → skip, instead of fiat-only → skip. **All 10 real
  mainnet fixtures** (4 DEX + 3 CEX + 3 FX) now decode end-to-end
  — the `t.Skip` branch from PR 164a/164d for CEX is gone. The
  real-fixture test also asserts the expected `Asset.Type` per
  variant (DEX→Soroban, CEX→Crypto, FX→Fiat), so a future
  mis-classification fails the harness loudly.
- `docs/architecture/contract-schema-evolution.md` — living doc
  covering per-contract WASM-upgrade handling for Soroban sources
  (Soroswap / Phoenix / Aquarius / Reflector). Why backfill must
  be WASM-version-aware, what's known per source, handling
  strategy (Map-field-by-name, topic-dispatch, WASM-hash column
  on ingest rows, gated backfill).
- CLAUDE.md "Things that will surprise you" entry linking to the
  new architecture doc.

- Repository foundation: `LICENSE` (Apache-2.0), `README.md`,
  `CLAUDE.md`, `CHANGELOG.md`, `CONTRIBUTING.md`,
  `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CODEOWNERS`.
- ADRs 0001–0007 + 0010: Horizon deprecated, MinIO S3-compat,
  i128 no-truncation, Tier-1 validator aspiration, monorepo,
  TimescaleDB for price time-series, Redis cache schema, and
  off-chain fiat representation.
- Root-level `VERSIONS.md` — pinned SHAs of all audited
  upstream deps.
- Makefile targets `dev`, `dev-teardown`, `dev-seed`, `lint`,
  `test`, `test-integration`, `build`, `docs-all`, `verify`.
- `.golangci.yml` strict lint config per
  [engineering-standards.md §8](docs/discovery/engineering-standards.md).
- GitHub Actions `ci.yml`, PR template, CODEOWNERS,
  `dependabot.yml`.
- Phase-1 discovery artefacts under `docs/discovery/`, closure
  doc at `docs/discovery/phase1-closure.md`, RFP × proposal ×
  delivery coverage matrix at `docs/architecture/coverage-matrix.md`.
- HA + multi-region design: `docs/architecture/ha-plan.md`,
  `docs/architecture/infrastructure/{archival-node-spec,
  multi-region-topology, validator-rollout, hosting-options}.md`.
- API design: `docs/reference/api-design.md` + OpenAPI spec at
  `openapi/rates-engine.v1.yaml` (shared error responses,
  pagination, asset / price / history / OHLC / VWAP / TWAP /
  markets / oracle schemas — source of truth for the wire
  contract).
- Repo hygiene + tech-debt prevention plan at
  `docs/architecture/repo-hygiene-plan.md`.
- `internal/canonical/`: `Amount` (i128-safe big.Int wrapper with
  JSON-as-string, SQL Scanner/Valuer, KALIEN regression test,
  `MaxAmountStringLen` DoS cap), `Asset` (tagged union —
  native/classic/soroban/fiat), `Pair` (directional base/quote
  with Flip / EqualEitherWay helpers), `Trade` (stable ID via
  source/ledger/tx_hash/op_index), `Price`, `OracleUpdate`,
  `FiatRate`, and `strkey.go` format validators for G/C addresses.
- `internal/config/`: root `Config` + seven substructs (Region,
  Stellar, Storage, Ingestion, Aggregate, API, Obs) with struct-
  tag–driven doc generator. `Load` + `ApplyEnvOverrides` +
  `Validate` pipeline so env overrides are always validated.
  Startup error-log when `auth_mode != "none"` (auth middleware
  not yet wired). S3 config validated all-or-nothing.
  `docs-config` subcommand on `ratesengine-ops` emits
  `docs/reference/config/README.md` with the mandatory
  generated-file banner.
- `internal/stellarrpc/`: JSON-RPC client wrapping `getHealth`,
  `getLatestLedger`, `getNetwork`, `getVersionInfo`, `getEvents`,
  `getLedgers`, `getFeeStats`. Context-aware, concurrent-safe,
  mockable; identifiable `User-Agent`; post-decode sanity checks
  on GetEvents response (ledger bounds, event order). Tested
  against httptest.Server. `rpc-probe` subcommand on
  `ratesengine-ops`.
- `internal/consumer/`: stable `Source` interface (StreamLive /
  BackfillRange) that every on-chain, oracle, and CEX/FX source
  implements.
- `internal/sources/{soroswap,aquarius,phoenix,reflector}`:
  five-file per-source packages (doc/events/decode/consumer/tests)
  decoding canonical trades from Soroban events with compile-time
  `consumer.Source` assertions. Handles Soroswap Swap+Sync
  correlation, Phoenix 8-event-per-swap fanout, Aquarius
  multi-op-per-tx flat-counter fanout, and Reflector
  three-contract (DEX/CEX/FX) price-vector decoding.
  `sweepStale` uses event `ClosedAt` (not wall-clock) so backfill
  does not synthesise false orphans.
- `internal/storage/timescale/`: typed adapters for trades
  (InsertTrade idempotent, TradesInRange[After] cursor-paged),
  oracle updates, ingestion cursors (DB-level monotonic-advance
  guard), distinct assets + distinct pairs (cursor-paged,
  `hasMore` flag). Pool tuned for Patroni failover windows.
- `internal/api/v1/`: REST server with envelope-wrapped responses
  (`data` / `as_of` / `sources` / `flags` / `pagination`),
  RFC 9457 problem+json errors, handlers for `/healthz`,
  `/readyz` (parallel dependency pings under shared deadline),
  `/version`, `/assets`, `/assets/{asset_id}`, `/price`,
  `/history`, `/ohlc`, `/vwap`, `/twap`, `/markets`,
  `/oracle/latest`, and `/metrics` (unversioned, operator-facing).
- `internal/api/v1/middleware/`: RequestID → HTTPMetrics →
  Logger (slog access + remote_ip context) → Recoverer →
  SecurityHeaders → CORS (allow-list) → RateLimit (per-IP, Redis
  token bucket, skips health + /metrics). Stack order
  audited for preflight-free CORS and ratelimit-after-remote-ip
  invariants.
- `internal/ratelimit/`: Redis-backed atomic Lua token bucket
  with window-remaining Retry-After semantics,
  `url.QueryEscape` key-sanitisation, and bounded key length.
- `internal/metadata/`: SEP-1 / stellar.toml resolver with
  SSRF guard (loopback + RFC 1918 + link-local + metadata-IP
  deny), singleflight fan-in, and a Redis-backed cache that
  tolerates a nil client.
- `internal/obs/`: Prometheus non-default registry, HTTP
  metrics middleware (`http_requests_total`,
  `http_request_duration_seconds`), shared slog factory.
- `migrations/0001_create_trades_hypertable.{up,down}.sql` —
  `trades` hypertable (1-day chunks, compression policy after 7
  days, retention 90 days), four secondary indexes, and
  `ingestion_cursors` table.
- `migrations/0002_create_price_aggregates.{up,down}.sql` — the
  seven RFP-grain continuous aggregates (1m/15m/1h/4h/1d/1w/1mo)
  with VWAP + TWAP + OHLC tuple + per-CAGG refresh & retention
  policies.
- `migrations/0003_create_oracle_updates_hypertable.{up,down}.sql`
  — `oracle_updates` hypertable with compression + retention +
  `(asset_id, source, ts DESC)` index for "latest per source".
- `cmd/ratesengine-migrate`: golang-migrate wrapper with
  subcommands `up`, `down [N]`, `status`, `version`, `force`,
  `help`. DSN via `-dsn` flag or `RATESENGINE_POSTGRES_DSN` env.
- `cmd/ratesengine-indexer`: orchestration binary for the source
  pipeline with graceful shutdown, per-source supervisor +
  restart policy, and an embedded Prometheus scrape server on
  `obs.MetricsListen` so ingestion alerts actually have a target.
- `cmd/ratesengine-api`: REST server binary with `-dry-run` (now
  pings Postgres + Redis for real), signal-driven graceful
  shutdown (30 s drain), SEP-1 cache wiring, optional CORS, and
  optional rate-limit middleware.
- `cmd/ratesengine-aggregator`: scaffold for the VWAP/TWAP +
  continuous-aggregate refresh orchestrator.
- `cmd/ratesengine-ops`: admin CLI with `docs-config`,
  `rpc-probe`, backfill, and gap-detect subcommands.
- `deploy/docker-compose/dev.yaml`: local TimescaleDB (pg15) +
  Redis 7 + MinIO with a one-shot bucket initialiser. Driven by
  `.env.example`. `make dev` end-to-end works.
- `test/integration/`: testcontainers-go round-trip proofs for
  migrations, API (readyz, oracle/latest), trades (multi-op
  fanout, cursor regressions), CHECK-constraint enforcement,
  CAGG policy attachment, DistinctPairs pagination. Guarded by
  `//go:build integration`.
- `configs/ansible/roles/archival-node/`: full Ubuntu-22.04
  bootstrap role (ZFS raidz2, Postgres 15, stellar-core,
  Galexie, stellar-rpc, MinIO, nftables, node_exporter,
  SSH hardening). Hardware-agnostic via inventory.
- `docs/operations/runbooks/`: 38 runbooks covering every
  currently-defined Prometheus alert (ingestion-lag,
  decode-errors, cursor-stuck, rpc-lag, source-stopped,
  orphan-events, cagg-stale, compression-lag, insert-errors,
  price-divergence, price-stale, oracle-stale, api-down,
  api-5xx, api-latency, redis-*, timescale-primary-down,
  archive-*, replica-lag, scrape-failing, deadmansswitch,
  backup-failed, db-disk-full, host-*, nvme-*, pg-conns-saturated,
  zfs-degraded, alertmanager-bad-config, core-lag, core-peers,
  bootstrap-archival-node). CI enforces alert ↔ runbook
  bijection via `scripts/ci/lint-docs.sh`.
- `scripts/ci/lint-docs.sh`: BSD-sed-compatible pre-merge doc
  linter — config drift, OpenAPI routes ↔ handlers, metrics
  catalogue, stale refs, TODOs, frontmatter, banners, ADR
  index, runbook URLs, alerts-catalog drift.

### Fixed

- `internal/sources/reflector/events.go:61` had an incorrect
  schema comment (claimed body was
  `Map{"prices": Vec<(Asset, i128)>, "timestamp": u64}`) — real
  wire shape (verified against mainnet 2026-04-23) is
  `Map{"update_data": Vec<(Val, i128)>}` with `timestamp` in
  topic[2]. `decodeUpdateBody` signature changed from
  `([]PriceEntry, uint64, error)` to `([]PriceEntry, error)`.
- Reflector event timestamp unit is **u64 milliseconds**, not
  seconds. Previous code's `time.Unix(int64(ts), 0)` gave year
  58277; now uses `time.UnixMilli(int64(ts))`.
- Reflector consumer's server-side topic filter had 2 slots but
  real events have 3 (REFLECTOR, update, timestamp). Added the
  `"*"` WildCardExactOne at position 2 so stellar-rpc's
  length-aware matcher doesn't drop every event.
- Soroswap's Phase-1 `TopicSymbolSwap` / `classify` stub assumed
  topic[0] was `Symbol("swap")`. Actual wire format is
  `topic[0]=String("SoroswapPair"), topic[1]=Symbol("swap")` —
  rewritten. A server-side filter built from the stubs would
  have returned zero events.
- Aquarius Phase-1 stub assumed a `Vec<i128>` body with N×N
  in/out fanout driven by a pool-info cache. Real contract emits a
  3-tuple body (sold, bought, fee) with tokens carried in topics —
  zero decoder paths matched reality. Rewritten; dead
  `poolCache` / `SeedPool` / `WithSeededPools` / `PoolInfo` /
  `lookupPool` surface removed.
- Phoenix Phase-1 stub had placeholder topic blobs that never
  matched real events, and three stub body decoders
  (`decodeAddress` / `decodeAsset` / `decodeI128`) that returned
  errors. Real format (verified 2026-04-23): both topic slots are
  `ScvString`, bodies are raw single-value SCVals (no Vec or Map
  wrapper). Decoders real now.
- Renamed reflector's `ErrUnknownFiatSymbol` →
  `ErrUnknownSymbol` now that the decoder tries both fiat and
  crypto allow-lists. Kept the rename note inline at the error
  declaration for discoverability via `git blame`.
- **`InsertOracleUpdate`** used `NULLIF($11, 0)` which typed the
  confidence parameter as integer. Passing a float64 `Confidence`
  crashed the driver with `invalid input syntax for type integer:
  "0.95"`. Fixed to `NULLIF($11, 0.0)`. Would have misfired the
  first time an oracle emitted a non-zero confidence score. Caught
  by the new TestDecoderOutputFitsStorageSchema integration test.
- Pre-existing integration-test fixture bugs surfaced while wiring
  the schema round-trip test:
  - `TestAssetsReaderPagination` used 55-char hand-written
    `CA001JYLG…` strings that failed canonical's 56-char C-strkey
    check. Replaced with `strkey.Encode`-generated seeds
    (`sorobanFromSeed`).
  - `TestStoreRoundTrip` used `Observer: "GRELAYER_FAKE"` (13
    chars); replaced with `gAccountFromSeed`.
  - `TestTradesInRangeAndMarkets`'s `mkIntegrationTrade` embedded
    the literal source string (`"sdex"`) into the tx_hash,
    producing non-hex chars. Now hex-encodes each source byte so
    the hash stays parseable.

### Added — architecture / guardrails

- **PR 165d**: `cmd/ratesengine-indexer/main.go` rewritten against
  the Galexie → ledgerstream → dispatcher flow. No stellar-rpc
  client, no per-source orchestrator, no poll loops.
  - One goroutine drives `ledgerstream.Stream` with an
    unbounded-live-tail range; the callback invokes
    `dispatcher.ProcessLedger` per LCM, forwards emitted
    `consumer.Event`s to the sink goroutine, and upserts the
    pipeline cursor atomically.
  - `buildDispatcher` maps `cfg.Ingestion.EnabledSources` to
    `Decoder` / `OpDecoder` registrations (reflector×3 +
    soroswap + aquarius + phoenix + sdex). Unknown source names
    are fatal at startup.
  - `resolveStartLedger` prefers a persisted pipeline cursor;
    falls back to `cfg.Ingestion.BackfillFromLedger`; refuses
    to silently pick zero (which would re-ingest genesis).
  - Sink goroutine retains panic-recovery + per-source metric
    stamping. Type-switch expanded to include `sdex.TradeEvent`.
  - Cursor table: one `source="ledgerstream"` entry per
    indexer replica; replaces the pre-165 per-source cursors.
- **Source packages cleaned:** each of the four
  `internal/sources/{soroswap,aquarius,phoenix,reflector}/consumer.go`
  shrunk from ~300 LOC of RPC-orchestrator scaffolding to just
  the `TradeEvent` / `UpdateEvent` wrapper + (for Soroswap /
  Phoenix) the correlation buffer. Total deletion:
  `Source` struct, `New`, `Option`, `BackfillRange`,
  `StreamLive`, `processPage`, `filters`, `setError`, `setOK`,
  `recordNewPair`, `setPair`, `lookupPair`, `Health`, `SeedPair`
  (moved to Decoder), `Option` / `WithPollInterval` /
  `WithSeededPairTokens` / `WithDecimals` / `NewDEX` / `NewCEX`
  / `NewFX` / `newVariant`. Per-source `source_test.go`
  migrated off the deleted API; legacy `TestSource_*` renamed
  to `TestDecoder_*` and reshaped to exercise the new Decoder
  seams (pair-registry concurrency, name lookup).
- **lint-imports.baseline empty.** All 5 grandfathered legacy
  violations removed as the refactors landed. The baseline
  header documents that re-adding an entry requires a PR note
  citing why the exception is temporary. `lint-imports.sh`
  allowlist updated to include `cmd/ratesengine-indexer/` in
  rule B (the indexer passes `xdr.LedgerCloseMeta` through as
  legitimate binding glue).

- **PR 165c**: `internal/sources/sdex/` — classic DEX decoder.
  First non-Soroban source. Walks classic op results for
  ManageSellOffer / ManageBuyOffer / CreatePassiveSellOffer /
  PathPaymentStrictReceive / PathPaymentStrictSend. Decodes the
  three `ClaimAtom` variants: OrderBook (modern G-address
  counterparty), LiquidityPool (classic-AMM pool ID as hex Maker),
  and V0 (pre-P18 legacy — skipped with `ErrUnknownClaimAtomType`
  so backfills surface it rather than silently drop).
- `dispatcher.OpDecoder` interface + `Dispatcher.AddOpDecoder` /
  `RouteOp` — sibling to the Soroban `Decoder` interface. Classic
  ops need access to `xdr.Operation` + `xdr.OperationResult`
  which contract events don't carry; `OpContext` bundles both
  along with tx-level metadata (ledger, close time, tx hash, tx
  source). One `ProcessLedger` call now walks both contract
  events and classic ops per transaction. Test coverage: SDEX
  package (7 unit tests, ClaimAtom happy path + multi-claim
  OpIndex-uniqueness fanout + failed-op zero-output + V0 legacy
  skip + negative-amount rejection), dispatcher package
  (`TestRouteOp_*` cross-cutting routing + error accounting).
- **PR 165b**: `internal/events/` + `internal/dispatcher/` + per-
  source Decoder adapters. The one-pipeline pivot from the RPC-
  based per-source orchestrator to the Galexie → dispatcher →
  decoder flow described in
  `docs/architecture/ingest-pipeline.md`.
  - `internal/events/Event` — transport-neutral contract-event
    type (moved from `internal/stellarrpc`). Decoders import
    `events` instead of `stellarrpc`. `stellarrpc.Event` is now a
    deprecated type alias pointing at `events.Event`; callers that
    still build events via the JSON-RPC client keep working
    unchanged.
  - `internal/dispatcher/` — owns the single production ingest
    codepath. `Dispatcher.ProcessLedger` walks a
    `xdr.LedgerCloseMeta` via
    `ingest.NewLedgerTransactionReaderFromLedgerCloseMeta`,
    extracts Soroban contract events per transaction, and routes
    each via `Decoder.Matches` (first-match-wins, byte-equality on
    topic[0]). `Dispatcher.Route` is exposed for test harnesses +
    fixture replay.
  - `internal/sources/{reflector,aquarius,soroswap,phoenix}/dispatcher_adapter.go`
    — each source exports a `NewDecoder(...)` that implements the
    dispatcher's `Decoder` interface. Correlation state (Soroswap
    swap+sync buffer, Phoenix 8-field assembly) moved inside the
    Decoder; no goroutines, no RPC clients, no polling loops.
    Reflector variants take the contract-address scope as an
    explicit constructor arg so the dispatcher can co-register
    all three oracles.
  - `TestEndToEndRouting_withRealFixtures` — feeds every captured
    mainnet fixture through one Dispatcher wired with all 6
    Decoders (4 sources + 3 Reflector variants). Validates that
    72 real events produce 173 canonical outputs with zero
    unmatched hits; per-source ratios (1:1 aquarius, 1:2 soroswap,
    1:8 phoenix, 1:many reflector) are asserted so a future
    routing regression trips loudly.
- **PR 165a**: `internal/ledgerstream/` — thin wrapper around the
  SDK's `ingest.ApplyLedgerMetadata` that reads Galexie's
  MinIO/S3/Filesystem output and yields `xdr.LedgerCloseMeta` per
  ledger to a caller callback. Config binds
  `datastore.DataStoreConfig` + `ledgerbackend.BufferedStorageBackendConfig`
  + optional Prometheus registry into one unit; auto-derives
  sensible buffered-backend defaults. Supports bounded + unbounded
  ranges (backfill + live tail use the same code). Unit tests use
  the filesystem datastore + the SDK's `compressxdr` helpers to
  construct Galexie-shaped fixtures in-test (no binary fixtures
  in the repo).
- `docs/architecture/ingest-pipeline.md` — binding doc for the one
  canonical ingest path (Galexie → ledgerstream → dispatcher →
  decoder). Replaces the earlier "RPC-based source
  `BackfillRange`/`StreamLive`" pattern; documents that
  stellar-rpc was removed from r1 on 2026-04-23.
- CLAUDE.md **Invariant #6** — no stellar-rpc in production
  ingest. Pointer to the ingest-pipeline doc.
- **`scripts/ci/lint-imports.sh`** + `lint-imports.baseline` —
  build-time enforcement of three architectural boundaries:
  - A/no-rpc-in-ingest: `internal/stellarrpc` blocked outside the
    package itself, `cmd/ratesengine-ops/`, `scripts/dev/`,
    source `decode.go` files (transitional), and test files.
  - B/xdr-scoped-to-scval: `go-stellar-sdk/xdr` scoped to
    `internal/scval/`, `internal/ledgerstream/`,
    `internal/dispatcher/` (planned 165b),
    `internal/sources/sdex/` (planned 165c), and test files.
  - C/no-horizon: all Horizon imports banned everywhere
    (ADR-0001).
  Baseline grandfathers 5 known legacy violations (the 4 source
  `consumer.go` files + indexer main, all slated for rewrite in
  PR 165b/d). Lint FAILS on new violations OR stale baseline
  entries — baseline shrinks monotonically. Hooked into
  `make lint-imports`, `make verify`, and a dedicated
  `import-checks` GitHub Actions job.

### Added — integration

- **PR 165e**:
  `test/integration/ledgerstream_to_storage_test.go` —
  `TestEndToEnd_LedgerstreamToTimescale`. First end-to-end
  integration test of the full production ingest path:
  Galexie-shaped `.xdr.zst` on disk → `ledgerstream` → full
  `dispatcher` (all 6 decoders registered: reflector×3 +
  soroswap + aquarius + phoenix + sdex) → `consumer.Event` type
  switch → `timescale.Insert*` → cursor upsert → query back.
  Uses the SDK's filesystem datastore + compressxdr helpers to
  construct valid Galexie batches in-test; no binary fixtures.
  Two subtests:

  1. **bounded range of empty ledgers** — 3 ledgers flow
     through, pipeline persists zero events, cursor advances to
     the last sequence.
  2. **soroban LCM with reflector FX update lands OracleUpdate**
     — constructs a Soroban-flagged `TransactionEnvelope`
     (Ext.V=1 + SorobanData) whose `TransactionMetaV3.SorobanMeta.Events`
     carries a real Reflector FX `xdr.ContractEvent`
     (topic[0]=Symbol("REFLECTOR"), topic[1]=Symbol("update"),
     topic[2]=U64 ms, body=Map{"update_data": Vec<(Symbol,i128)>}),
     signs the envelope hash into `TxProcessing[i].Result`, ships
     through the pipeline, and asserts the row in
     `oracle_updates` carries the expected source / contract /
     ledger / asset / price / decimals / timestamp / observer.
     Proves the hash-matched envelope-lookup + SorobanMeta.Events
     extraction + topic-byte-equality routing all work together
     under realistic bytes. Runs in <1 s.

- `test/integration/decoders_to_storage_test.go` —
  **`TestDecoderOutputFitsStorageSchema`** proves canonical.Trade
  / canonical.OracleUpdate produced by the four Soroban decoders
  satisfy the trades / oracle_updates hypertable schemas. 7
  subtests under one shared Timescale container: soroswap trade,
  aquarius trade, phoenix trade, phoenix large_i128 (ADR-0003
  boundary), reflector fiat_oracle, reflector crypto_oracle (PR
  164e AssetCrypto SQL round-trip), reflector dex_oracle. Runs in
  ~14 s.

### Tested against

- Stellar protocol 25.x (mainnet passphrase
  `"Public Global Stellar Network ; September 2015"`).
- stellar-core `v26.0.1`, stellar-rpc `v26.0.0`,
  stellar-galexie `v26.0.0`.
- `go-stellar-sdk v0.5.0`, `withObsrvr/stellar-extract v0.1.2`.
- `timescale/timescaledb:2.17.2-pg15`, `redis:7.4-alpine`,
  `minio:RELEASE.2024-11-07`.
- `golang-migrate v4.19.1`, `testcontainers-go v0.38+`.

---

<!--
Release sections will be added here as versions ship. Keep the
[Unreleased] block at the top; the release workflow moves it
under the new version header on tag push.

Example of a future release entry:

## [v0.1.0] — 2026-06-30 — Initial public release

**Operator action required: yes** (first install).

### Added
- Full SDEX / Soroswap / Aquarius / Phoenix / Comet / Blend indexing.
- Reflector / Redstone / Band oracle integration.
- Since-inception OHLC for top-20 pairs.
- REST + SSE API v1.

### Tested against
- Stellar protocol 25.x.
- stellar-core v26.0.1, stellar-rpc v26.0.0.

### `pkg/*` versions included
- `pkg/client v0.1.0`
-->

# A03 — Served-tier storage (Timescale / Redis / MinIO)

**Scope:** `internal/storage/timescale/` (all `.go` incl. `*_test.go`),
`internal/storage/redisclient/`. ClickHouse excluded (A02).
**Verdict:** Strong area. No Critical/High findings. The historically
dangerous patterns (unbounded trade scans, cursor monotonic-guard bug,
i128 truncation, string-built SQL) are all either fixed or correctly
gated. Findings below are Medium/Low resource-shape concerns and a
couple of doc-drift notes.

READ-ONLY audit. No source edited.

## MinIO note (scope)

There is **no `internal/storage/minio/` package** — the directory does
not exist (`internal/storage/` contains only `clickhouse/`,
`redisclient/`, `timescale/`). The MinIO/S3 adapter lives elsewhere
(ledgerstream/Galexie read path), outside A03's stated scope. No MinIO
code was in range; nothing to audit here. Flagging so the A03 scope line
isn't read as "MinIO reviewed and clean."

## Findings

| Severity | file:line | dimension | issue | why | fix | confidence |
|---|---|---|---|---|---|---|
| Medium | assets.go:145-162 (`hasAssetByTradesScan`) | D5 | `SELECT EXISTS(SELECT 1 FROM trades WHERE base_asset=$1 OR quote_asset=$1 LIMIT 1)` has no time/ledger bound. For a **non-existent Soroban contract id** the planner must seek every chunk's index across the full `trades` hypertable before it can return `false`. | This is the exact shape of the F-0157 incident (4–5s cold `/v1/assets/{id}`) that was fixed *for classic assets only* via the `classic_assets` PK fast-path (`hasClassicAsset`). Soroban/native/fiat/crypto/rwa still fall through to the OR-scan; the docstring hand-waves "typical r1 contract count is bounded," but an unknown/garbage Soroban id is the worst case and is user-reachable on `/v1/assets/{C…}`. | Add a recency/ledger bound, OR back Soroban existence with the `discovered_assets` / `protocol_contracts` registry the same way classic uses `classic_assets`. At minimum a `WHERE ts >= now()-MarketsRecencyWindow` clamp. | High |
| Medium | issuers.go:119-152 (`ListIssuers`) | D5 | `issuers JOIN classic_assets … GROUP BY … ORDER BY total_obs DESC LIMIT $1` with no recency filter and no indexed keyset cursor. | `classic_assets` is ~440K rows (per network_stats.go comment). This is a full join+aggregate+sort on every call. Unlike the markets/coins/assets directory queries — which were all deliberately moved off full scans (recency windows, CAGG sourcing, keyset cursors) after measured incidents — the issuer directory was left as a top-N global sort. Likely SWR-cached at the handler, but the cold-fill + background-refresh both pay full cost. | Same treatment the sibling directory endpoints already got: precompute (a periodically-refreshed issuer-summary table) or at least a covering index on `classic_assets(issuer_g_strkey, observation_count)` so the GROUP BY/ORDER is index-assisted. | Medium |
| Low | classic_supply_observations.go:71-85 / 132-146 / 194-208 / 263-277 (`SumTrustlineBalancesAtOrBefore` and the three sibling `Sum*AtOrBefore`) | D5 | `SELECT sum(...) FROM (SELECT DISTINCT ON (account_id) … WHERE asset_key=$1 AND ledger<=$2 …)` — fan-out is one row per *every account/holder/pool ever observed holding the asset*, with no cap. | For a high-trustline-count asset (e.g. USDC has millions of trustlines) the inner DISTINCT-ON materialises a row per holder. This is the supply *refresh-worker* path (not the API hot path) and is keyed on the indexed `asset_key`, so it's tolerable today, but it scales with holder count, not with a bounded window — a latent slow-query for the most-held assets. | Acceptable as-is for operator-scoped watched assets; if supply coverage widens to the full long tail, bound or paginate. Worth a `SET LOCAL statement_timeout` backstop like the gap-detector scans use, since this query class has no other ceiling. | Medium |
| Low | markets.go:738-769 (`PairMarket`) | D5 | The outer query reads `MAX(t.ts)` + `count(*) FILTER (… 24h)` directly from `trades` for one pair scoped to `ts >= since` (14d). Two correlated subselects then hit `prices_1m`. | Single-pair + 14d-bounded so chunk-pruned and cheap in the common case, but it is the one markets surface that still touches the raw `trades` hypertable (the listing variants were all moved to CAGGs in #20/#25). A pair that traded heavily for 14d (e.g. XLM/USDC) scans a non-trivial slice of recent chunks per `/v1/pairs` hit. | Low priority. If `/v1/pairs` becomes hot, source `last_trade_at`+`count_24h` from `prices_1d`/`prices_1m` like `DistinctPairs` does, dropping the raw-trades touch entirely. | Medium |
| Low | coins.go:991-1003 (`GetCoinTradeCount24h`) | D5 | `SELECT COUNT(*) FROM trades WHERE ts >= now()-24h AND (base_asset=$1 OR quote_asset=$1)`. | 24h-bounded (chunk-pruned) so bounded by one day of chunks, but the `OR` on two indexed columns can't use a single index cleanly; for the busiest assets this counts a large 24h slice. Acceptable; noted for completeness as the one asset-detail call that reads raw `trades` rather than `prices_1m`. | Consider serving from `prices_1m`'s `trade_count` sum (already done by `GetCoinMarketsCount`'s sibling pattern) if it shows up hot. | Medium |
| Low | blend_auctions.go:319-330 (`ListBlendPools`) | D5 | `GROUP BY pool` over the whole `blend_auctions` table, no recency bound, no LIMIT. | The query has no upper bound on rows scanned, but `blend_auctions` is a deliberately sparse table (~8K distinct ledgers across a 5.9M span per the gap-detector sizing notes) and the distinct pool count is tiny, so the aggregate is cheap *today*. The risk is only if blend activity grows by orders of magnitude. | Add a recency window if/when blend volume grows; harmless now. | High |
| Low | diagnostics.go:196 (`RefreshContinuousAggregate`) | D3/D6 | `fmt.Sprintf("CALL refresh_continuous_aggregate('%s', …)", viewName)` string-interpolates the view name into the SQL. | This is the only place a view identifier is concatenated for a CALL. It IS guarded by the `allowedCAGGViews` allow-list checked immediately above (line 184) and the view names are internal, so there is no injection vector. Flagged purely as the residual string-built-SQL surface a reviewer should keep gated — if a caller ever bypasses the allow-list this becomes injectable. | None required; keep the allow-list check co-located with the Sprintf and never let a new caller reach the Sprintf without it. | High |
| Info | per_source_gaps.go:331 / source_coverage.go:88 / row_counts.go:32 | D5 | `SET LOCAL statement_timeout` values differ across the gap-detector family: `FindPerSourceLedgerGaps` uses `780000` (13 min), while `CountDistinctLedgers`, `CountRowsByLedger`, and `MinLedger` use `300000` (5 min). | Not a bug — the 13-min value is documented as intentionally sized to stay under the 15-min Go-side per-target timeout for the heaviest LAG-over-DISTINCT scans (sdex/soroban_events), while the count queries are lighter. But the divergence is easy to mis-copy when adding a new heavy scan; the 5-min cap on `CountDistinctLedgers` over sdex/soroban_events could abort a legitimately slow count that `FindPerSourceLedgerGaps` would have completed. | Consider a single named const for the heavy-scan timeout so the two halves of one cycle can't diverge silently. | High |

## CORRECT — verified clean (load-bearing checks)

- **D1 cursor management — RewindCursor / UpsertCursor (cursors.go:143-196):**
  VERIFIED CORRECT. `UpsertCursor`'s `WHERE EXCLUDED.last_ledger > ingestion_cursors.last_ledger`
  monotonic-forward guard is intact, and the documented prior bug
  (projector-replay silently no-op'd through it) is correctly remediated
  by the separate `RewindCursor` (`WHERE … AND last_ledger > $3`,
  RowsAffected==0 → loud error). The two methods are correctly
  single-purpose: Upsert refuses to regress, Rewind refuses to advance.
  `first_ledger` COALESCE-preserve semantics on UPDATE are correct.
- **D1/D6 CAGG read queries (aggregates.go):** `prices_1m/1h/1d/…`
  reads (`HistoryPoints`, `HistoryPointsInRange`, `RecentClosedVWAP1mForPair`,
  `ClosedVWAP1mAtOrBefore`, `LatestClosedVWAP1mForPair`, `OHLCSeries`,
  `OHLCSeriesReBucketed`, `Volume24hUSDForAsset`) all carry the ADR-0015
  closed-bucket guard (`bucket + INTERVAL '<g>' <= now()`), all bound by
  pair + (where applicable) time window, all LIMIT-clamped or bounded.
  The `quote_amount = vwap*volume` NUMERIC identity is exact (no float
  round-trip). `volume_usd::text` / `vwap::text` keep NUMERIC precision.
- **D3 SQL injection — granularity/table/interval composition
  (aggregates.go HistoryGranularity):** `Validate()` gates against a
  fixed 7-value enum BEFORE the value is concatenated into the table
  name / interval; `OHLCSeriesReBucketed` additionally allow-lists
  `outInterval` against a literal switch. All row-value predicates bind
  via `$N`. The `#nosec G201` annotations are justified.
- **D3 SQL injection — gap-detector identifier interpolation
  (per_source_gaps.go, source_coverage.go, row_counts.go):**
  `Table`/`LedgerColumn`/`WhereFilter` are interpolated (Postgres can't
  `$N`-bind identifiers) but come exclusively from the compile-time
  `DefaultGapDetectorTargets` const list; ADR-0030 + the
  `TestGapDetectorTargetsCoverMigrations` CI guard make this load-bearing.
  No user input reaches these. Range/count values bind via `$1..$3`.
- **D5 the no-unbounded-trade-scan rule:** the LAG()-over-DISTINCT and
  COUNT(DISTINCT ledger) over `trades`/`soroban_events` are the *sanctioned*
  gap-detector/census paths — and every one is correctly defended:
  per-target Go ctx timeout (15 min), per-target throttled cadence (6h
  for sdex/soroban_events), AND a PG-side `SET LOCAL statement_timeout`
  backstop (Go cancellation doesn't always reach PG — the documented r1
  incident). `BackfillCoverageStats` (the old per-source full-hypertable
  scan that caused the cold-start hang) is correctly stubbed to a no-op.
  No API-hot-path code issues a `DISTINCT ledger`/`LAG()` over `trades`.
- **D5 directory queries off the raw hypertable:** `DistinctAssets`
  (assets.go), `DistinctPairs`/`AllPools` (markets.go), `ListCoins`
  (coins.go) are all recency-windowed (14d, planner-pruned via a
  Go-computed `since` constant), CAGG-sourced where possible (`prices_1d`
  for the active-set, `prices_1m` for the 24h slice), keyset-paginated
  with overfetch-by-one, and limit-clamped [1,500/501]. The `since`-as-
  bound-param trick (plan-time chunk pruning) is applied consistently.
- **D5 trades reads (trades.go):** `LatestTradesForPair`,
  `LatestTradePerSource` (DISTINCT ON covered by trades_pair_source_ts_idx),
  `TradesInRange`/`TradesInRangeAfter` (limit default 1000, hard cap
  10000; the F-1319 DESC-then-reverse fix keeps newest rows on
  truncation), `FXQuoteAtOrBefore` (LIMIT 1, descending index scan).
  `CountTrades`/`CountOracleUpdates`/`CountSorobanEventsInRange` are
  documented diagnostic-only.
- **D2 ADR-0003 (i128 NUMERIC→*big.Int→string, never int64):**
  VERIFIED across all amount/price/supply/reserve/balance columns.
  Grep for `int64(`/`.Lo`/`Int128`/`.Int64()`/`.Uint64()`/`MustI128`
  over the money-carrying files returns zero narrowing of token amounts.
  `trades.usd_volume` uses `big.Rat.FloatString(8)`; supply uses
  `big.Int.String()` round-tripped via `::text`; blend bid/lot amounts
  are stringified `big.Int` inside JSONB; sep41/router amounts are
  decimal strings; freeze `frozen_value` is NUMERIC-from-string. The
  only `int64(...)`/`uint32(...)` casts are on ledger seq / op_index /
  event_index (bounded domain, `//nolint:gosec` annotated).
- **D2 one-writer / lake-vs-served:** the timescale layer is the served
  tier; comments correctly reflect ADR-0034 (raw trades kept forever —
  no `drop_after` on `trades` observed in this layer; `CAGGsLiveForever`
  excludes 1m/15m which have 30d retention by design). The projector is
  the sole writer for Soroban-derived tables (this layer just exposes
  Insert*/Stream* primitives it calls).
- **D4 connection pool (store.go):** `configurePool` sets MaxOpenConns=25,
  MaxIdleConns=5, ConnMaxLifetime=30m, ConnMaxIdleTime=5m — all named
  consts with documented F-0151 rationale (dead-conn recycling after the
  2026-05-26 cascade). `Open` pings with a 5s timeout. `PingContext`/
  `Close` are nil-safe. `Store` documented safe for concurrent use.
- **D4 prepared-statement reuse:** the layer relies on database/sql's
  implicit per-query prepare/cache via lib/pq (no manual Stmt caching);
  acceptable for this access pattern. Batch inserts (`BatchInsertTrades`,
  `InsertSorobanEventsBatch`, `InsertSEP41TransferBatch`) build one
  multi-row VALUES statement per flush — the correct throughput shape
  (the documented 2026-06-01 per-INSERT-roundtrip incident fix).
- **D1 NULL handling:** consistent `sql.Null*` scanning with explicit
  `.Valid` checks across every nullable column; `stringArray.Scan`
  (aggregates.go) handles nil/empty/`{}`/`NULL`-element Postgres array
  literals; `nullStringPtr`/`nullFloat`/`floatOrNil`/`timeOrNil` helpers
  are used uniformly. `Volume24hUSD`/`LastPrice` pointers correctly emit
  JSON null (not "0") and explicitly skip the literal `"0"` string.
- **D1 idempotency:** every Insert* uses `ON CONFLICT … DO NOTHING/UPDATE`
  on a correct PK; the InsertTrade/InsertOracleUpdate `source_entry_counts`
  bump uses the `HAVING count(*) > 0` gate so duplicate re-walks don't
  inflate tallies (F-1243). `RecordFreeze` uses `pg_advisory_xact_lock`
  + `WHERE NOT EXISTS` + `ON CONFLICT DO NOTHING` for race-safe single
  open-row-per-pair (F-1250).
- **D5 resolver cache (usd_fx_resolver.go):** the in-memory FX cache is
  bounded — `storeCache` opportunistically TTL-sweeps when the map
  exceeds `fxCacheSweepThreshold` (8192) (G11-05 unbounded-growth fix);
  `queryDB` adds a `bucket >= at-freshness` lower bound so a miss prunes
  to the freshness-window chunks instead of scanning to genesis (G11-06).
- **Redis (redisclient.go):** `Build` correctly selects Sentinel
  FailoverClient vs single Client vs nil from config; username/password
  threaded through; `Mode` mirrors the branch for the boot log. No
  credentials logged. Returned client documented concurrent-safe.

## Files read

70 of 70 `.go` files in scope (all non-test + the `*_test.go` were
present and counted; substantive review focused on the non-test
sources). `internal/storage/timescale/`: account_observations.go,
aggregates.go, asset_registry.go, assets.go, baseline.go, blend_admin.go,
blend_auctions.go, blend_emissions.go, blend_positions.go, cctp_events.go,
change_summary.go, classic_supply_observations.go, coins.go,
comet_liquidity.go, completeness_snapshots.go, cursors.go,
decoder_stats.go, defindex_flows.go, diagnostics.go, discovery.go,
divergence_observations.go, doc.go, freeze_events.go, fx_quotes.go,
gap_detector.go, issuers.go, ledger_ingest_log.go, markets.go,
network_stats.go, oracle.go, per_source_gaps.go, phoenix_liquidity.go,
phoenix_stake_events.go, price_source_contributions.go,
protocol_contracts.go, protocol_stats.go, row_counts.go, rozo_events.go,
sep41_supply_events.go, sep41_transfers.go, soroban_events.go,
soroswap_pairs.go, soroswap_router_swaps.go, soroswap_skim_events.go,
source_coverage.go, sources_stats.go, store.go, supply.go,
topic_samples.go, trades.go, usd_fx_resolver.go, usd_volume_quote_spec.go
(+ the `*_test.go` siblings). `internal/storage/redisclient/`:
redisclient.go (+ test). The largest files (coins.go 1838L, markets.go
886L, trades.go 895L, aggregates.go 766L, diagnostics.go 513L) were read
in full; the remaining insert-only/registry files were read in full or
confirmed via targeted grep for the i128/SQL-injection/unbounded-scan
risk patterns.

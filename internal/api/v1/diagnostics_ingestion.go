package v1

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/currency/marketcap"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/version"
)

// FXCoverageReader is the seam the ingestion diagnostics reads
// fx_quotes coverage stats through. timescale.Store satisfies it
// via FXCoverageStats.
type FXCoverageReader interface {
	FXCoverageStats(ctx context.Context) (timescale.FXCoverage, error)
}

// SupplyCoverageReader is the seam the ingestion diagnostics reads
// asset_supply_history coverage stats through. timescale.Store
// satisfies it via SupplyCoverageStats.
type SupplyCoverageReader interface {
	SupplyCoverageStats(ctx context.Context) (timescale.SupplyCoverage, error)
}

// CAGGCoverageReader is the seam the ingestion diagnostics reads
// prices_1h coverage stats through. timescale.Store satisfies it
// via CAGGCoverageStats.
type CAGGCoverageReader interface {
	CAGGCoverageStats(ctx context.Context) (timescale.CAGGCoverage, error)
}

// SourceEntryCountReader is the seam the ingestion diagnostics reads
// the per-source entry tally through. timescale.Store satisfies it
// via SourceEntryCounts. Cheap ~20-row tally read — available even
// during an all-time backfill, which is the whole point.
type SourceEntryCountReader interface {
	SourceEntryCounts(ctx context.Context) (map[string]int64, error)
}

// IngestionDiagnostics is the wire shape for
// /v1/diagnostics/ingestion. One snapshot of the region's ingest
// state — region label, version, ledger tip, per-decoder backfill,
// FX coverage, market-cap cache state, supply coverage, and the
// full source registry projected onto live stats. Designed to be
// the only call the public status page makes for the ingestion
// section so it can render the whole region panel without scraping
// five separate endpoints.
type IngestionDiagnostics struct {
	Region   RegionInfo             `json:"region"`
	Version  IngestionVersionInfo   `json:"version"`
	Ledger   LedgerTip              `json:"ledger"`
	Backfill []BackfillDecoderState `json:"backfill"`
	// BackfillCoverage answers "what fraction of genesis→tip have we
	// actually processed?" per source. The headline DensityPct (and
	// covered/expected/earliest/latest) is derived CURSOR-FIRST from
	// the union of completed backfill-cursor intervals — no trades
	// scan — so it stays live even mid-backfill when the trades
	// coverage query is too IO-contended to finish. trade_count is
	// best-effort enrichment from the background trades-scan cache.
	// SECONDARY to Backfill: `Backfill` shows what backfill *is
	// doing*; `BackfillCoverage` shows what we've actually walked.
	BackfillCoverage   []BackfillCoverageRow `json:"backfill_coverage"`
	BackfillCoverageAt string                `json:"backfill_coverage_as_of,omitempty"`
	// rawCursors is stashed by fillIngestionBackfill so the cursor-
	// first buildBackfillCoverage step has access without re-issuing
	// ListCursors. Unexported + json:"-" so it never leaks to the
	// wire — purely an in-process scratchpad.
	rawCursors []timescale.Cursor `json:"-"`
	// entryCounts is stashed by fillIngestionEntryCounts: the
	// always-on per-source tally (source_entry_counts) that backs
	// the `entries` column. Unexported scratchpad, never on the wire.
	entryCounts map[string]int64 `json:"-"`
	// CAGGCoverage is the time range of prices_1h — the canonical
	// "long-lived" continuous aggregate. The raw trades hypertable
	// has a 90-day retention so its MIN(ledger) only reports the
	// recent window; prices_1h is retained forever (migration 0002)
	// so its MIN(bucket) is the real "do we have historical OHLC
	// since genesis?" answer. Powers /v1/chart and the since-
	// inception history endpoint.
	CAGGCoverage CAGGCoverageView  `json:"cagg_coverage"`
	FXBackfill   FXBackfillState   `json:"fx_backfill"`
	MarketCap    MarketCapState    `json:"market_cap"`
	Supply       SupplyStateView   `json:"supply"`
	Sources      []SourceHealthRow `json:"sources"`
}

// CAGGCoverageView is the wire shape of the prices_1h coverage
// summary. EarliestBucket / LatestBucket are RFC3339; empty
// strings when the CAGG has not been materialised yet.
type CAGGCoverageView struct {
	EarliestBucket string `json:"earliest_bucket,omitempty"`
	LatestBucket   string `json:"latest_bucket,omitempty"`
	BucketCount    int64  `json:"bucket_count"`
}

// BackfillCoverageRow is the per-source coverage projection.
//
// For on-chain (mapped) sources EarliestLedger / LatestLedger are
// the min/max of the MERGED backfill-cursor union — the actual
// processed span, not a trades MIN/MAX. They're display context
// only; DensityPct is the gap-aware number (a wide earliest..latest
// with interior gaps still yields a low density). CEX/FX sources
// report 0 / 0 (no Stellar ledger); `applies` distinguishes the two
// cases for the UI (no point drawing a "coverage bar" for binance).
//
// GenesisLedger is the source's known start point — 1 for SDEX
// (Stellar pubnet genesis), the contract deploy ledger for
// Soroban contracts, 0 ("not applicable") for CEX/FX. Hardcoded
// in `sourceGenesisLedger`; when an operator deploys a new source
// add a row there.
//
// DensityPct is the fraction of (genesis → tip) ledgers we've
// SUCCESSFULLY PROCESSED for this source, measured via the union
// of completed portions of backfill cursor ranges. When backfill
// fully covers [genesis, tip], DensityPct = 1.0.
//
// Why cursor-based, not row-based: a sparse source like Comet
// (~16k trades over 10.7M ledgers) naturally has ≥1 trade on only
// 0.15% of its ledgers. A row-COUNT(DISTINCT ledger) metric would
// peg at 0.15% even with perfect backfill, useless as a "are we
// done" signal. Cursor coverage measures "did the indexer walk
// this ledger?" — which is the question the operator actually
// wants to answer.
//
// CoveragePct (deprecated 2026-05-14) is the prior endpoint-span
// metric — `(LatestLedger - max(EarliestLedger, GenesisLedger) + 1)
// / (tip - GenesisLedger + 1)`. Misleading: a source with one
// trade at ledger 1 and one trade at tip with 99% gap in between
// scored 100%. Kept as a transitional field; the status page reads
// DensityPct instead.
type BackfillCoverageRow struct {
	Source         string `json:"source"`
	Applies        bool   `json:"applies"`
	GenesisLedger  int64  `json:"genesis_ledger,omitempty"`
	EarliestLedger int64  `json:"earliest_ledger,omitempty"`
	LatestLedger   int64  `json:"latest_ledger,omitempty"`
	// Entries is the always-on per-source running tally of ingested
	// rows — `trades` for exchange/DEX/CEX sources, `oracle_updates`
	// for oracle sources (migration 0035 / source_entry_counts).
	// Read from a tiny ~20-row tally table, so it's exact and
	// available EVEN during an all-time backfill — unlike the old
	// `trade_count`, which came from the IO-contended trades scan
	// and collapsed to a misleading 0 mid-backfill (and was
	// structurally always-0 for oracle sources, which never write
	// to `trades`). Renamed trade_count → entries 2026-05-15.
	Entries int64 `json:"entries"`
	// CoveragePct — see godoc on the type. Endpoint-span metric.
	// Deprecated; retained as a transitional field. Status page
	// renders DensityPct.
	CoveragePct float64 `json:"coverage_pct,omitempty"`
	// DensityPct is the honest "what fraction of ledgers have we
	// processed" measurement based on the union of backfill cursor
	// intervals. 1.0 = fully backfilled. See godoc.
	DensityPct float64 `json:"density_pct,omitempty"`
	// CoveredLedgers is the absolute count of ledgers covered by
	// successful backfill ranges between genesis and tip. The
	// numerator of DensityPct.
	CoveredLedgers int64 `json:"covered_ledgers,omitempty"`
	// ExpectedLedgers is tip - genesis + 1 — the denominator of
	// DensityPct. Exposed so the UI can render absolute "X / Y
	// ledgers covered" rather than just a percentage.
	ExpectedLedgers int64 `json:"expected_ledgers,omitempty"`
}

// sourceGenesisLedger is the operator-curated map of "what's the
// earliest ledger this source can possibly have data for". Values:
//   - 1                : SDEX (Stellar pubnet genesis 2015-08-19).
//   - <first deploy>    : the EXACT ledger the source's first
//     contract WASM was installed on mainnet — the minimum
//     create_contract ledger across ALL of that protocol's
//     contracts (factory + every instance; multi-contract,
//     upgrade-in-place aware). Zero slack: this is the denominator
//     of DensityPct, so a value before the real deploy makes 100%
//     unreachable (counts pre-existence ledgers) and a value after
//     it silently hides genuine early-history gaps. Sourced from
//     the per-source WASM-audit walk evidence
//     (docs/operations/wasm-audits/, r1-walk-2026-05-01).
//   - 0 (default)      : not applicable (CEX/FX/aggregator/oracle —
//     these sources don't have a Stellar-ledger genesis concept).
//
// When a new on-chain source ships, add its exact first-deploy
// ledger here from its WASM audit (not a rounded estimate). The
// list intentionally sits next to the projection so a reviewer
// notices it during PR review.
var sourceGenesisLedger = map[string]int64{
	"sdex": 1,
	// Soroban contracts — exact first-deploy ledgers, MIN across
	// every contract the source routes, from the per-source WASM
	// audits (docs/operations/wasm-audits/<src>.md +
	// evidence/r1-walk-2026-05-01/per-source-final/<src>.json).
	"soroswap":        50_746_266, // factory first-deploy (soroswap.md:242)
	"soroswap-router": 50_746_272, // router, +6 ledgers after factory (soroswap.md:236)
	"aquarius":        52_728_375, // MIN across 313 pools + router
	"phoenix":         51_572_016, // factory first-deploy
	"comet":           51_499_545, // Blend backstop pool (only mainnet Comet) (comet.md:158)
	"blend":           51_499_545, // factory/backstop mainnet rollout
	"reflector-dex":   50_644_229, // v2 WASM deploy (reflector.md:186)
	"reflector-cex":   50_644_239, // v2 WASM deploy, +10 after DEX (reflector.md:186)
	"reflector-fx":    56_733_481, // deployed fresh on v3, no prior history (reflector.md:195)
	"band":            50_842_736, // single stable WASM since 2024-03-19 (band.md:198)
	"redstone":        58_758_722, // first-deploy hotfix, replaced +420 ledgers (redstone.md:179)
	// PROVISIONAL — defindex per-WASM walk not yet captured
	// (BackfillSafe=false, audit in_progress: defindex.md, Task
	// #6). Anchored to the Blend-ecosystem rollout window its
	// vaults launched in (2025-04-14, ~L51,499,545); erring early
	// keeps density honest (reads <100% rather than hiding
	// history) until the exact value lands.
	"defindex": 51_499_545,
}

// RegionInfo identifies which deployment generated this snapshot.
// Today r1/production only; r2/r3 will join when their playbooks
// land. Mirrors the Region shape on /v1/status — a clean rename
// here would mean cross-endpoint drift, so we lift the same one.
type RegionInfo struct {
	Name       string `json:"name"`
	Deployment string `json:"deployment"`
}

// IngestionVersionInfo is the same five fields /v1/version returns.
// Duplicated onto the ingestion response so an operator screen can
// render "what's running here" without a second fetch — matters
// during cross-region drift investigations when comparing r1 vs r2
// without a SSH window open.
type IngestionVersionInfo struct {
	Version   string `json:"version"`
	BuildDate string `json:"build_date"`
	Commit    string `json:"commit"`
	Dirty     string `json:"dirty"`
	GoVersion string `json:"go_version"`
}

// LedgerTip summarises live-network ingest progress. LatestLedger
// comes from prices_1m's MAX(ledger_sequence); LagSeconds is the
// wall-clock age of the most recent ledgerstream cursor update.
// Volume / markets / assets come from the same source as
// /v1/network/stats so the two endpoints agree.
type LedgerTip struct {
	LatestLedger    int64  `json:"latest_ledger"`
	LagSeconds      int64  `json:"lag_seconds"`
	Volume24hUSD    string `json:"volume_24h_usd,omitempty"`
	MarketsCount24h int64  `json:"markets_count_24h"`
	AssetsIndexed   int64  `json:"assets_indexed"`
}

// BackfillDecoderState is one row of the per-decoder backfill
// summary. The aggregator runs many backfill ranges concurrently
// (one per worker × decoder set); this struct collapses them into
// the per-decoder view operators actually want: how many ranges
// are still running, which is oldest, when did the slowest one
// last advance.
//
// Decoder = the comma-separated decoder set from the cursor's
// sub_source after the range prefix (e.g.
// "50534290-51275895:sdex,soroswap" → decoder "sdex,soroswap").
type BackfillDecoderState struct {
	Decoder string `json:"decoder"`
	// RangesTotal is unchanged for back-compat: total cursor
	// rows for this decoder set. Three new fields decompose it
	// for the status page so operators can immediately see how
	// many are done vs stuck.
	RangesTotal int `json:"ranges_total"`
	// RangesComplete: cursor's last_ledger == range_end.
	RangesComplete int `json:"ranges_complete"`
	// RangesRunning: last_ledger < range_end AND last_updated
	// in the recent past (≤ 10 min — the same threshold the
	// /diagnostics/cursors `?status=active` filter uses).
	RangesRunning int `json:"ranges_running"`
	// RangesStalled: last_ledger < range_end AND last_updated
	// is older than 10 min — the cursor advanced once but
	// hasn't moved since. Almost always means an operator
	// killed the backfill mid-run; needs a `-resume` restart.
	RangesStalled int `json:"ranges_stalled"`
	// RangesActive is RangesRunning + RangesStalled. Kept for
	// back-compat with the v0 wire shape — UIs that just want
	// "incomplete count" can keep using it.
	RangesActive    int    `json:"ranges_active"`
	OldestUpdatedAt string `json:"oldest_updated_at,omitempty"`
	OldestLagSecs   int64  `json:"oldest_lag_seconds"`
	NewestLedger    int64  `json:"newest_ledger"`
}

// stalledThreshold is the wall-clock age above which an in-progress
// backfill cursor is considered stalled rather than actively
// processing. Mirrors the `statusActiveMaxAge` in
// diagnostics_cursors.go (the `?status=active` filter on
// /v1/diagnostics/cursors uses the same boundary).
const stalledThreshold = 10 * time.Minute

// FXBackfillState describes how much fiat-rate history we have in
// fx_quotes. Earliest/Latest are RFC3339 dates (truncated to day
// boundaries by the upstream daily snapshot cadence). Empty when
// the table is empty.
type FXBackfillState struct {
	EarliestQuote   string `json:"earliest_quote,omitempty"`
	LatestQuote     string `json:"latest_quote,omitempty"`
	TotalQuotes     int64  `json:"total_quotes"`
	CurrenciesCount int    `json:"currencies_count"`
}

// MarketCapState is a slim view of the marketcap.Cache. Populated
// at request time from cache.All() — small map (~22 catalogue
// entries today), so no incremental cost. OldestFetchedAt is the
// LRU age of the oldest entry (if any); a stale OldestFetchedAt
// signals the CG refresher is wedged.
type MarketCapState struct {
	EntriesCount    int    `json:"entries_count"`
	OldestFetchedAt string `json:"oldest_fetched_at,omitempty"`
	NewestFetchedAt string `json:"newest_fetched_at,omitempty"`
}

// SupplyStateView projects timescale.SupplyCoverage onto the wire.
// Splits "classic" (XLM + CODE:G…) from "SEP-41" (C-strkey
// contract addresses) so operators can spot a stalled observer
// per asset domain — the supply observers run independently and
// can wedge separately.
type SupplyStateView struct {
	ClassicAssets  int    `json:"classic_assets_with_supply"`
	SEP41Assets    int    `json:"sep41_assets_with_supply"`
	LastSnapshotAt string `json:"last_snapshot_at,omitempty"`
	LatestLedger   int64  `json:"latest_ledger,omitempty"`
}

// SourceHealthRow joins the static external.Registry metadata
// (class, subclass, VWAP-eligibility, backfill-safety) with the
// live 24h stats from sourcesStats (trades, volume, markets) so
// operators can see at a glance which sources are silent vs
// actively ingesting. Sorted by name for deterministic responses.
type SourceHealthRow struct {
	Name            string `json:"name"`
	Class           string `json:"class"`
	Subclass        string `json:"subclass,omitempty"`
	IncludeInVWAP   bool   `json:"include_in_vwap"`
	BackfillSafe    bool   `json:"backfill_safe"`
	TradeCount24h   int64  `json:"trade_count_24h"`
	VolumeUSD24h    string `json:"volume_24h_usd,omitempty"`
	MarketsCount24h int64  `json:"markets_count_24h"`
}

// handleDiagnosticsIngestion serves GET /v1/diagnostics/ingestion.
//
// Composes the ingestion snapshot from existing readers + the
// in-memory marketcap cache + the external.Registry. Every reader
// fans out under a single 6s deadline; per-reader soft-fail means
// one stuck dependency degrades that section to an empty / default
// value rather than failing the whole response.
//
// Cache: short, public, max-age=15s. The data underneath changes
// at most every few seconds (cursor updates, supply observer ticks)
// so 15s smooths the load from a refreshing status page without
// hiding live degradation.
func (s *Server) handleDiagnosticsIngestion(w http.ResponseWriter, r *http.Request) {
	// Per-handler ceiling — 30s. Each filler uses its own
	// sub-context (5-10s each) so one slow reader doesn't starve
	// the others. Pre-2026-05-14 the parent ctx was 6s and the
	// fillers were sequential with no per-call timeout — when one
	// reader exceeded its share, every subsequent filler aborted
	// with `context deadline exceeded` and the response showed
	// 0% coverage on every source. Caught live on r1 12:45 UTC.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	out := IngestionDiagnostics{
		Region: RegionInfo{
			Name:       s.regionName,
			Deployment: s.regionDeployment,
		},
		Version: IngestionVersionInfo{
			Version:   version.Version,
			BuildDate: version.BuildDate,
			Commit:    version.Commit,
			Dirty:     version.Dirty,
			GoVersion: version.GoVersion,
		},
	}
	// Defensive empty-slice init: Go marshals nil slices as `null`,
	// which crashes naïve clients that do `rows.length`.
	out.Backfill = []BackfillDecoderState{}
	out.BackfillCoverage = []BackfillCoverageRow{}
	out.Sources = []SourceHealthRow{}

	// Run independent fillers concurrently. Each has its own per-
	// call timeout so a slow reader can't block the others. The
	// in-memory cached/projected sections (BackfillCoverage,
	// MarketCap) run inline since they don't touch the DB.
	out.MarketCap = projectMarketCapState(s.marketCaps)
	// The background-refreshed trades-scan snapshot is now ONLY a
	// best-effort enrichment source (per-source trade_count + the
	// off-chain CEX/FX rows). The authoritative coverage/density is
	// derived cursor-first after the parallel fillers run — see
	// buildBackfillCoverage. Fetching it here (cheap RLock) keeps
	// the read off the request critical path; an empty/stale cache
	// no longer blanks the whole snapshot during an all-time
	// backfill when the trades scan is too IO-contended to finish.
	var cacheRows []timescale.BackfillCoverage
	if s.backfillCoverage != nil {
		cacheRows, _ = s.backfillCoverage.Snapshot()
	}

	type filler struct {
		name    string
		fn      func(context.Context)
		timeout time.Duration
	}
	fillers := []filler{
		{"sources", func(c context.Context) { out.Sources = buildSourceHealth(c, s) }, 8 * time.Second},
		{"ledger", func(c context.Context) { s.fillIngestionLedger(c, &out) }, 6 * time.Second},
		{"backfill", func(c context.Context) { s.fillIngestionBackfill(c, &out) }, 5 * time.Second},
		{"fx_coverage", func(c context.Context) { s.fillIngestionFXCoverage(c, &out) }, 5 * time.Second},
		{"supply_coverage", func(c context.Context) { s.fillIngestionSupplyCoverage(c, &out) }, 5 * time.Second},
		{"cagg_coverage", func(c context.Context) { s.fillIngestionCAGGCoverage(c, &out) }, 5 * time.Second},
		{"entry_counts", func(c context.Context) { s.fillIngestionEntryCounts(c, &out) }, 5 * time.Second},
	}
	var wg sync.WaitGroup
	for _, f := range fillers {
		wg.Add(1)
		go func(f filler) {
			defer wg.Done()
			subCtx, subCancel := context.WithTimeout(ctx, f.timeout)
			defer subCancel()
			f.fn(subCtx)
		}(f)
	}
	wg.Wait()

	// Build the coverage rows cursor-first now that the parallel
	// fillers have populated the tip (fillIngestionLedger) and the
	// raw cursors (fillIngestionBackfill). This is the authoritative
	// path: density / covered / expected / earliest / latest for
	// every on-chain source come from the union of completed
	// backfill cursor intervals — no trades scan — so the snapshot
	// populates DURING an all-time backfill instead of waiting for
	// the IO-contended trades-coverage query to finish. cacheRows
	// now only carries the off-chain row presence; the `entries`
	// count comes from the always-on tally (out.entryCounts).
	tip := out.Ledger.LatestLedger
	out.BackfillCoverage = buildBackfillCoverage(out.rawCursors, cacheRows, out.entryCounts, tip)
	if len(out.BackfillCoverage) > 0 {
		// Assembled this request from live cursors — the headline
		// density is as-of-now, not the (possibly stale/failed)
		// trades-scan cache time.
		out.BackfillCoverageAt = time.Now().UTC().Format(time.RFC3339)
	}

	w.Header().Set("Cache-Control", "public, max-age=15, s-maxage=15")
	writeJSON(w, out, Flags{})
}

// fillIngestionLedger reads network-stats and copies the four
// numeric fields onto out.Ledger. Soft-fail: a stuck reader leaves
// the section at zero-valued defaults rather than erroring the
// whole response.
func (s *Server) fillIngestionLedger(ctx context.Context, out *IngestionDiagnostics) {
	if s.networkStats == nil {
		return
	}
	ns, err := s.networkStats.GetNetworkStats(ctx)
	if err != nil {
		s.logger.Warn("diagnostics/ingestion: network_stats", "err", err)
		return
	}
	out.Ledger.LatestLedger = ns.LatestLedger
	out.Ledger.MarketsCount24h = ns.MarketsCount24h
	out.Ledger.AssetsIndexed = ns.AssetsIndexed
	if ns.Volume24hUSD != nil {
		out.Ledger.Volume24hUSD = *ns.Volume24hUSD
	}
}

// fillIngestionBackfill reads the cursors table and projects two
// outputs: the per-decoder backfill state on out.Backfill, and the
// live-stream cursor age on out.Ledger.LagSeconds. Done in one
// helper because both derive from the same fetch.
func (s *Server) fillIngestionBackfill(ctx context.Context, out *IngestionDiagnostics) {
	if s.cursors == nil {
		return
	}
	rows, err := s.cursors.ListCursors(ctx)
	if err != nil {
		s.logger.Warn("diagnostics/ingestion: cursors", "err", err)
		return
	}
	out.Backfill = aggregateBackfill(rows)
	out.Ledger.LagSeconds = ledgerStreamLagSeconds(rows)
	// Stash for the post-fillers density-projection step. Cheap —
	// just a slice of pointers; the recomputation reads it once.
	out.rawCursors = rows
}

// fillIngestionFXCoverage type-asserts that the wired FX reader
// also implements FXCoverageReader. Production wiring (Store) does;
// test fakes may not, in which case the section stays empty.
func (s *Server) fillIngestionFXCoverage(ctx context.Context, out *IngestionDiagnostics) {
	reader, ok := s.fxHistory.(FXCoverageReader)
	if !ok || reader == nil {
		return
	}
	cov, err := reader.FXCoverageStats(ctx)
	if err != nil {
		s.logger.Warn("diagnostics/ingestion: fx_coverage", "err", err)
		return
	}
	out.FXBackfill = FXBackfillState{
		TotalQuotes:     cov.TotalQuotes,
		CurrenciesCount: cov.CurrenciesCount,
	}
	if !cov.EarliestQuote.IsZero() {
		out.FXBackfill.EarliestQuote = cov.EarliestQuote.Format("2006-01-02")
	}
	if !cov.LatestQuote.IsZero() {
		out.FXBackfill.LatestQuote = cov.LatestQuote.Format("2006-01-02")
	}
}

// fillIngestionCAGGCoverage reads prices_1h's MIN/MAX bucket — the
// real "do we have historical aggregates" answer (the raw trades
// table only retains 90 days, but prices_1h is retained forever).
// Type-asserts through fxHistory since timescale.Store satisfies
// every reader interface; the assertion gracefully no-ops on test
// fakes that don't implement CAGGCoverageReader.
func (s *Server) fillIngestionCAGGCoverage(ctx context.Context, out *IngestionDiagnostics) {
	reader, ok := s.fxHistory.(CAGGCoverageReader)
	if !ok || reader == nil {
		return
	}
	cov, err := reader.CAGGCoverageStats(ctx)
	if err != nil {
		s.logger.Warn("diagnostics/ingestion: cagg_coverage", "err", err)
		return
	}
	out.CAGGCoverage = CAGGCoverageView{BucketCount: cov.BucketCount}
	if !cov.EarliestBucket.IsZero() {
		out.CAGGCoverage.EarliestBucket = cov.EarliestBucket.UTC().Format(time.RFC3339)
	}
	if !cov.LatestBucket.IsZero() {
		out.CAGGCoverage.LatestBucket = cov.LatestBucket.UTC().Format(time.RFC3339)
	}
}

// fillIngestionEntryCounts stashes the always-on per-source entry
// tally (source_entry_counts) onto out for buildBackfillCoverage.
// Type-asserts through fxHistory like the other coverage readers;
// no-ops on test fakes that don't implement SourceEntryCountReader.
// Soft-fail: a reader error leaves entryCounts nil → the `entries`
// column reads 0 rather than erroring the whole response.
func (s *Server) fillIngestionEntryCounts(ctx context.Context, out *IngestionDiagnostics) {
	reader, ok := s.fxHistory.(SourceEntryCountReader)
	if !ok || reader == nil {
		return
	}
	counts, err := reader.SourceEntryCounts(ctx)
	if err != nil {
		s.logger.Warn("diagnostics/ingestion: entry_counts", "err", err)
		return
	}
	out.entryCounts = counts
}

// buildBackfillCoverage produces the per-source coverage rows
// CURSOR-FIRST.
//
// For every on-chain source with a known genesis (sourceGenesisLedger)
// it derives density / covered / expected / earliest / latest purely
// from the union of completed backfill-cursor intervals. This path
// needs NO trades scan, so it always populates — including during an
// all-time backfill when the trades-hypertable coverage query is too
// IO-contended to finish within its timeout. earliest/latest here are
// the *processed* span (min/max of the merged cursor union), not a
// trades MIN/MAX — honest for "what have we actually walked", and it
// can never claim a gap is covered.
//
// entryCounts (source_entry_counts, the always-on tally) supplies
// the `entries` column for every row — exact and available even
// mid-backfill. cacheRows (the background-refreshed trades-scan
// snapshot) now ONLY carries off-chain CEX/FX row presence (those
// sources have no Stellar-ledger genesis and thus no cursor concept).
// An empty/stale cache just drops the off-chain context rows — it
// can no longer blank the whole snapshot, and the `entries` numbers
// are unaffected (different source). Any cache source not in
// sourceGenesisLedger keeps the legacy endpoint-span behaviour
// (Applies + deprecated CoveragePct) so an un-mapped on-chain source
// doesn't silently vanish; DensityPct stays 0 for it (no genesis →
// no honest density, the signal to add it to sourceGenesisLedger).
func buildBackfillCoverage(cursors []timescale.Cursor, cacheRows []timescale.BackfillCoverage, entryCounts map[string]int64, tip int64) []BackfillCoverageRow {
	out := make([]BackfillCoverageRow, 0, len(sourceGenesisLedger)+len(cacheRows))

	sources := make([]string, 0, len(sourceGenesisLedger))
	for src := range sourceGenesisLedger {
		sources = append(sources, src)
	}
	sort.Strings(sources)
	for _, src := range sources {
		genesis := sourceGenesisLedger[src]
		row := BackfillCoverageRow{
			Source:        src,
			Applies:       true,
			GenesisLedger: genesis,
			Entries:       entryCounts[src],
		}
		if genesis > 0 && tip > 0 {
			covered, density, earliest, latest := computeSourceCoverage(cursors, src, genesis, tip)
			row.CoveredLedgers = covered
			row.ExpectedLedgers = tip - genesis + 1
			row.DensityPct = density
			row.EarliestLedger = earliest
			row.LatestLedger = latest
			row.CoveragePct = computeCoveragePct(genesis, earliest, latest, tip)
		}
		out = append(out, row)
	}

	// Cache-only rows: off-chain CEX/FX (no genesis → no cursors)
	// plus any on-chain source not yet mapped. Best-effort context;
	// absent entirely when the cache is cold.
	for _, r := range cacheRows {
		if _, mapped := sourceGenesisLedger[r.Source]; mapped {
			continue
		}
		row := BackfillCoverageRow{Source: r.Source, Entries: entryCounts[r.Source]}
		if r.EarliestLedger > 0 && r.LatestLedger > 0 {
			row.Applies = true
			row.EarliestLedger = r.EarliestLedger
			row.LatestLedger = r.LatestLedger
			row.CoveragePct = computeCoveragePct(0, r.EarliestLedger, r.LatestLedger, tip)
		}
		out = append(out, row)
	}
	return out
}

// computeCoveragePct returns the fraction of the
// (genesis → tip) interval we have any data for. Returns 0 if
// genesis or tip aren't usable (cold start, missing config).
// Capped at 1.0; any LatestLedger ≥ tip → 1.0 (covered to head).
func computeCoveragePct(genesis, earliest, latest, tip int64) float64 {
	if tip <= 0 || genesis <= 0 {
		return 0
	}
	expectedSpan := tip - genesis + 1
	if expectedSpan <= 0 {
		return 0
	}
	covStart := earliest
	if covStart < genesis {
		covStart = genesis
	}
	covEnd := latest
	if covEnd > tip {
		covEnd = tip
	}
	if covEnd < covStart {
		return 0
	}
	covered := covEnd - covStart + 1
	pct := float64(covered) / float64(expectedSpan)
	if pct > 1 {
		pct = 1
	}
	return pct
}

// fillIngestionSupplyCoverage type-asserts that the wired
// supply reader also implements SupplyCoverageReader.
func (s *Server) fillIngestionSupplyCoverage(ctx context.Context, out *IngestionDiagnostics) {
	reader, ok := s.supply.(SupplyCoverageReader)
	if !ok || reader == nil {
		return
	}
	cov, err := reader.SupplyCoverageStats(ctx)
	if err != nil {
		s.logger.Warn("diagnostics/ingestion: supply_coverage", "err", err)
		return
	}
	out.Supply = SupplyStateView{
		ClassicAssets: cov.ClassicAssets,
		SEP41Assets:   cov.SEP41Assets,
		LatestLedger:  cov.LatestLedger,
	}
	if !cov.LastSnapshotAt.IsZero() {
		out.Supply.LastSnapshotAt = cov.LastSnapshotAt.UTC().Format(time.RFC3339)
	}
}

// aggregateBackfill collapses the per-range backfill cursor rows
// into one row per decoder set. The cursor sub_source format is
// "<start>-<end>:<decoder-set>" (e.g.
// "50534290-51275895:sdex,soroswap"); a single decoder-set string
// becomes one BackfillDecoderState row. RangesActive counts ranges
// whose last_ledger < range_end — i.e. still catching up.
func aggregateBackfill(rows []timescale.Cursor) []BackfillDecoderState {
	type group struct {
		total       int
		complete    int // last_ledger == range_end
		running     int // incomplete AND updated_at within stalledThreshold
		stalled     int // incomplete AND updated_at older than stalledThreshold
		newestLedge int64
		oldestAt    time.Time
	}
	groups := map[string]*group{}
	now := time.Now().UTC()
	for _, c := range rows {
		if c.Source != "backfill" {
			continue
		}
		decoder, rangeEnd := parseBackfillSub(c.Sub)
		if decoder == "" {
			continue
		}
		g := groups[decoder]
		if g == nil {
			g = &group{}
			groups[decoder] = g
		}
		g.total++
		incomplete := rangeEnd > 0 && int64(c.LastLedger) < rangeEnd
		if !incomplete {
			g.complete++
		} else if now.Sub(c.UpdatedAt) <= stalledThreshold {
			g.running++
		} else {
			g.stalled++
		}
		if int64(c.LastLedger) > g.newestLedge {
			g.newestLedge = int64(c.LastLedger)
		}
		if g.oldestAt.IsZero() || c.UpdatedAt.Before(g.oldestAt) {
			g.oldestAt = c.UpdatedAt
		}
	}
	out := make([]BackfillDecoderState, 0, len(groups))
	for decoder, g := range groups {
		state := BackfillDecoderState{
			Decoder:        decoder,
			RangesTotal:    g.total,
			RangesComplete: g.complete,
			RangesRunning:  g.running,
			RangesStalled:  g.stalled,
			RangesActive:   g.running + g.stalled,
			NewestLedger:   g.newestLedge,
		}
		if !g.oldestAt.IsZero() {
			state.OldestUpdatedAt = g.oldestAt.UTC().Format(time.RFC3339)
			state.OldestLagSecs = int64(now.Sub(g.oldestAt).Seconds())
		}
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Decoder < out[j].Decoder })
	return out
}

// parseBackfillSub splits a cursor sub_source string of the form
// "<start>-<end>:<decoder-set>" into its decoder-set tail and the
// numeric range end. Returns ("", 0) when the format doesn't match
// (defensive — a malformed cursor doesn't crash aggregation).
func parseBackfillSub(sub string) (decoder string, rangeEnd int64) {
	colonIdx := strings.IndexByte(sub, ':')
	if colonIdx <= 0 || colonIdx == len(sub)-1 {
		return "", 0
	}
	rangePart := sub[:colonIdx]
	decoder = sub[colonIdx+1:]
	dashIdx := strings.IndexByte(rangePart, '-')
	if dashIdx <= 0 || dashIdx == len(rangePart)-1 {
		return decoder, 0
	}
	endStr := rangePart[dashIdx+1:]
	rangeEnd = parseInt64(endStr)
	return decoder, rangeEnd
}

// parseBackfillSubFull splits "<start>-<end>:<decoder-set>" into all
// three pieces. parseBackfillSub returns end-only because that's the
// only piece aggregateBackfill needs; density projection needs the
// start too. Returns (0, 0, "") on malformed input.
func parseBackfillSubFull(sub string) (rangeStart, rangeEnd int64, decoder string) {
	colonIdx := strings.IndexByte(sub, ':')
	if colonIdx <= 0 || colonIdx == len(sub)-1 {
		return 0, 0, ""
	}
	rangePart := sub[:colonIdx]
	decoder = sub[colonIdx+1:]
	dashIdx := strings.IndexByte(rangePart, '-')
	if dashIdx <= 0 || dashIdx == len(rangePart)-1 {
		return 0, 0, decoder
	}
	rangeStart = parseInt64(rangePart[:dashIdx])
	rangeEnd = parseInt64(rangePart[dashIdx+1:])
	return rangeStart, rangeEnd, decoder
}

// coverageInterval is [Start, End] inclusive on both ends.
type coverageInterval struct {
	Start, End int64
}

// mergeCoverageIntervals takes any set of intervals (possibly
// overlapping, in any order) and returns a minimal sorted set of
// non-overlapping intervals covering the same point set. Adjacent
// intervals (End+1 == next.Start) are joined.
//
// Standard sweep-line merge, O(n log n) sort + O(n) walk. Fine for
// the ~1000s of backfill cursors r1 carries today; an operator
// with a million cursors would want something fancier.
func mergeCoverageIntervals(intervals []coverageInterval) []coverageInterval {
	if len(intervals) == 0 {
		return nil
	}
	sorted := make([]coverageInterval, len(intervals))
	copy(sorted, intervals)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	out := []coverageInterval{sorted[0]}
	for _, iv := range sorted[1:] {
		last := &out[len(out)-1]
		if iv.Start <= last.End+1 {
			if iv.End > last.End {
				last.End = iv.End
			}
		} else {
			out = append(out, iv)
		}
	}
	return out
}

// sumCoverageIntervals returns the total ledger count in a
// pre-merged interval set. Each interval contributes (End - Start
// + 1) because the bounds are inclusive.
func sumCoverageIntervals(intervals []coverageInterval) int64 {
	var total int64
	for _, iv := range intervals {
		total += iv.End - iv.Start + 1
	}
	return total
}

// decoderSetContains reports whether the comma-separated decoder
// list `set` contains the exact source name `source`. Substring
// match would false-positive on prefixes (e.g. "reflector-dex" in
// "reflector-dex-extended").
func decoderSetContains(set, source string) bool {
	for {
		idx := strings.IndexByte(set, ',')
		var part string
		if idx == -1 {
			part = set
		} else {
			part = set[:idx]
		}
		if part == source {
			return true
		}
		if idx == -1 {
			return false
		}
		set = set[idx+1:]
	}
}

// computeSourceCoverage returns the cursor-based coverage for one
// source: the union of completed portions of all backfill cursors
// that include `source` in their decoder set, PLUS the live-ingest
// tail, clamped to [genesis, tip].
//
// The "completed portion" of a range cursor `<start>-<end>` is
// [start, min(last_ledger, end)] — if the range cursor's worker
// only got partway through, only the partway portion counts.
//
// Live-ingest tail: the `ledgerstream` cursor tracks the network tip
// in real time and live ingest is gap-free from its low-water to tip
// (sequential walker + archivecompleteness daemon, ADR-0017). We
// don't persist that low-water — see extendWithLiveTail. This closes
// the "head band" between the top of the backfill union and the live
// tip (so a fully-caught-up source reads ~100% and STAYS there as
// the tip advances) AND interior gaps that lie entirely at/below the
// live cursor and are bracketed by backfill coverage on both sides
// (provably walked by the gap-free live tail — e.g. a stalled
// backfill range, or a disjoint high gap-backfill island that
// fragmented the union). It still never credits the
// [genesis, firstBackfillStart] lower boundary and never gives a
// never-backfilled source false credit.
//
// Returns (covered_ledger_count, density_pct, earliest, latest).
// earliest/latest are the min Start / max End of the MERGED union —
// the actual processed span, so they cannot imply a gap is covered
// (density is the gap-aware number; earliest/latest are display
// context). Density is covered / (tip - genesis + 1) capped at 1.0.
// Zero genesis or non-positive expected range → all zero.
func computeSourceCoverage(cursors []timescale.Cursor, source string, genesis, tip int64) (covered int64, density float64, earliest, latest int64) {
	if genesis <= 0 || tip <= 0 || tip < genesis {
		return 0, 0, 0, 0
	}
	expected := tip - genesis + 1

	intervals := make([]coverageInterval, 0, len(cursors))
	for _, c := range cursors {
		iv, ok := cursorCoverageInterval(c, source, genesis, tip)
		if !ok {
			continue
		}
		intervals = append(intervals, iv)
	}
	merged := mergeCoverageIntervals(intervals)
	merged = extendWithLiveTail(merged, cursors, tip)
	covered = sumCoverageIntervals(merged)
	if len(merged) > 0 {
		earliest = merged[0].Start
		latest = merged[len(merged)-1].End
	}
	density = float64(covered) / float64(expected)
	if density > 1.0 {
		density = 1.0
	}
	return covered, density, earliest, latest
}

// liveLedgerstreamTop returns the high-water ledger of the live
// `ledgerstream` ingest cursor. 0 when no live cursor is present
// (e.g. a test fixture or a region whose live ingest hasn't started).
func liveLedgerstreamTop(cursors []timescale.Cursor) int64 {
	var top int64
	for _, c := range cursors {
		if c.Source != "ledgerstream" {
			continue
		}
		if l := int64(c.LastLedger); l > top {
			top = l
		}
	}
	return top
}

// extendWithLiveTail credits the live-ingest tail on top of the
// backfill union. Live ingest is gap-free from its low-water to tip
// (sequential walker + archivecompleteness daemon, ADR-0017), so two
// region classes are creditable to it:
//
//   - Head band: from the top of the backfill union up to
//     min(liveTop, tip) — the always-advancing caught-up frontier,
//     so a fully-synced source reads ~100% and stays there.
//   - Interior sub-tip gaps: a gap BETWEEN two merged backfill
//     intervals whose upper neighbour starts at/below liveTop. The
//     gap is bracketed by backfill coverage on both sides and lies
//     wholly within the gap-free live span, so live ingest provably
//     walked it (a stalled backfill range later covered live, or a
//     disjoint high gap-backfill island that fragmented the union —
//     the latter was silently capping density at ~96%). It is NOT
//     the [genesis, firstBackfillStart] region: that's the lower
//     boundary, never an adjacent-pair interior gap, so it is left
//     uncovered and a never-backfilled-low source still reads
//     honestly.
//
// Honest-by-construction guards retained:
//   - merged empty (never backfilled, e.g. BackfillSafe=false
//     Soroban source) → unchanged, stays 0%.
//   - no live cursor (liveTop ≤ 0: test fixture / region without
//     live ingest) → unchanged.
//   - nothing is ever credited above min(liveTop, tip).
func extendWithLiveTail(merged []coverageInterval, cursors []timescale.Cursor, tip int64) []coverageInterval {
	if len(merged) == 0 {
		return merged
	}
	liveTop := liveLedgerstreamTop(cursors)
	if liveTop > tip {
		liveTop = tip
	}
	if liveTop <= 0 {
		return merged
	}

	out := make([]coverageInterval, 0, len(merged)+1)
	out = append(out, merged...)

	// Interior sub-tip gaps: bridge any gap between consecutive
	// backfill intervals whose upper neighbour starts ≤ liveTop.
	// Bounds are inclusive and mergeCoverageIntervals joins on
	// Start ≤ End+1, so Start:gapLo / End:gapHi overlaps both
	// neighbours by one ledger and collapses cleanly on re-merge —
	// same convention as the head band below.
	for i := 0; i+1 < len(merged); i++ {
		gapLo, gapHi := merged[i].End, merged[i+1].Start
		if gapLo < gapHi && gapHi <= liveTop {
			out = append(out, coverageInterval{Start: gapLo, End: gapHi})
		}
	}

	// Head band: extend the union top up to liveTop.
	if backfillTop := merged[len(merged)-1].End; liveTop > backfillTop {
		out = append(out, coverageInterval{Start: backfillTop, End: liveTop})
	}

	return mergeCoverageIntervals(out)
}

// computeSourceDensity is the (covered, density)-only view of
// computeSourceCoverage, kept for callers/tests that don't need the
// processed-span endpoints.
func computeSourceDensity(cursors []timescale.Cursor, source string, genesis, tip int64) (int64, float64) {
	covered, density, _, _ := computeSourceCoverage(cursors, source, genesis, tip)
	return covered, density
}

// cursorCoverageInterval extracts the [start, min(last, end)]
// completed portion of one backfill cursor, clamped to
// [genesis, tip] and gated on the decoder set containing `source`.
// Returns ok=false for non-backfill cursors, malformed sub_source,
// decoder mismatch, or completed-portion below the start.
//
// Split out from computeSourceDensity to keep that function's
// cognitive complexity below the linter ceiling — the per-cursor
// logic is naturally branchy.
func cursorCoverageInterval(c timescale.Cursor, source string, genesis, tip int64) (coverageInterval, bool) {
	if c.Source != "backfill" {
		return coverageInterval{}, false
	}
	rangeStart, rangeEnd, decoder := parseBackfillSubFull(c.Sub)
	if decoder == "" || rangeStart == 0 || rangeEnd == 0 {
		return coverageInterval{}, false
	}
	if !decoderSetContains(decoder, source) {
		return coverageInterval{}, false
	}
	covEnd := int64(c.LastLedger)
	if covEnd > rangeEnd {
		covEnd = rangeEnd
	}
	if covEnd < rangeStart {
		return coverageInterval{}, false
	}
	if rangeStart < genesis {
		rangeStart = genesis
	}
	if covEnd > tip {
		covEnd = tip
	}
	if covEnd < rangeStart {
		return coverageInterval{}, false
	}
	return coverageInterval{Start: rangeStart, End: covEnd}, true
}

// parseInt64 returns 0 on parse failure — defensive default.
func parseInt64(s string) int64 {
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

// ledgerStreamLagSeconds finds the live-stream cursor (source =
// "ledgerstream") and reports its age in seconds. Returns 0 when
// no live cursor exists (cold start / catastrophic stall).
func ledgerStreamLagSeconds(rows []timescale.Cursor) int64 {
	now := time.Now().UTC()
	for _, c := range rows {
		if c.Source == "ledgerstream" {
			return int64(now.Sub(c.UpdatedAt).Seconds())
		}
	}
	return 0
}

// projectMarketCapState reads cache.All() once and reduces it to
// the EntriesCount / Oldest / Newest summary. Iterates the map at
// most once; no allocs beyond the output. Nil cache → empty state.
func projectMarketCapState(c *marketcap.Cache) MarketCapState {
	if c == nil {
		return MarketCapState{}
	}
	all := c.All()
	if len(all) == 0 {
		return MarketCapState{}
	}
	var oldest, newest time.Time
	for _, snap := range all {
		if snap.FetchedAt.IsZero() {
			continue
		}
		if oldest.IsZero() || snap.FetchedAt.Before(oldest) {
			oldest = snap.FetchedAt
		}
		if newest.IsZero() || snap.FetchedAt.After(newest) {
			newest = snap.FetchedAt
		}
	}
	out := MarketCapState{EntriesCount: len(all)}
	if !oldest.IsZero() {
		out.OldestFetchedAt = oldest.UTC().Format(time.RFC3339)
	}
	if !newest.IsZero() {
		out.NewestFetchedAt = newest.UTC().Format(time.RFC3339)
	}
	return out
}

// buildSourceHealth projects the static external.Registry onto
// the wire shape, joining each row with the live 24h stats from
// sourcesStats. Sources with no recent trades render as 0/empty
// rather than absent — operators want to see "binance had 0
// trades in 24h", which is a signal not a hidden row.
func buildSourceHealth(ctx context.Context, s *Server) []SourceHealthRow {
	statsBySource := map[string]timescale.SourceStats{}
	if s.sourcesStats != nil {
		if rows, err := s.sourcesStats.GetSourceStats(ctx); err == nil {
			for _, r := range rows {
				statsBySource[r.Source] = r
			}
		}
	}
	out := make([]SourceHealthRow, 0, len(external.Registry))
	for name, meta := range external.Registry {
		row := SourceHealthRow{
			Name:          name,
			Class:         string(meta.Class),
			Subclass:      string(meta.Subclass),
			IncludeInVWAP: meta.IncludeInVWAP,
			BackfillSafe:  meta.BackfillSafe,
		}
		if st, ok := statsBySource[name]; ok {
			row.TradeCount24h = st.TradeCount24h
			row.MarketsCount24h = st.MarketsCount24h
			if st.VolumeUSD24h.Valid {
				row.VolumeUSD24h = st.VolumeUSD24h.String
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

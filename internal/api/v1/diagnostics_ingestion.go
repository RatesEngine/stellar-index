package v1

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	// degraded reports whether one or more critical readers failed
	// (or were not wired) during the snapshot build, so the response
	// is missing fields its consumers expect. The handler propagates
	// this to the envelope's Flags.Stale so the client can react —
	// pre-fix the response served zero-valued struct fields with
	// flags.stale:false, which is a lie when every counter is zero
	// (F-0095). Unexported scratchpad, never on the wire as data.
	//
	// Fillers don't write this directly: the parallel-fillers pipeline
	// uses an atomic.Bool sidecar and copies the resolved value here
	// after wg.Wait(), to avoid a data race on this single shared
	// field across the filler goroutines.
	degraded bool `json:"-"`
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
// GenesisLedger is the source's earliest-possible-data ledger —
// 2 for SDEX (Stellar's genesis ledger 1 carries no operations so
// no SDEX trade can live in it; ledger 2 is the first that can),
// the contract deploy ledger for Soroban contracts, 0 ("not
// applicable") for CEX/FX. Hardcoded in `sourceGenesisLedger`;
// when an operator deploys a new source add a row there.
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
//   - 2                : SDEX. Stellar pubnet's network-genesis is
//     ledger 1, but ledger 1 carries zero operations by design —
//     it's the genesis spec record — so no SDEX trade can ever
//     live in it. The earliest ledger an SDEX trade can occupy is
//     ledger 2. Setting this to 1 would lock DensityPct at
//     99.99999...% no matter how complete the indexer is (verified
//     via #51's gap-fill: 62,688,969 / 62,688,970 with the
//     residual = ledger 1). The 100%-density mission needs 100%
//     reachable; this is the minimum-honest denominator floor.
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
	"sdex": 2,
	// Soroban contracts — exact first-deploy ledgers, MIN across
	// every contract the source routes, from the per-source WASM
	// audits (docs/operations/wasm-audits/<src>.md +
	// evidence/r1-walk-2026-05-01/per-source-final/<src>.json).
	"soroswap":        50_746_266, // factory first-deploy (soroswap.md:242)
	"soroswap-router": 50_746_272, // router, +6 ledgers after factory (soroswap.md:236)
	"aquarius":        52_728_375, // MIN across 313 pools + router
	"phoenix":         51_572_016, // factory first-deploy
	// comet ≡ blend genesis is a REAL shared origin, not a
	// copy-paste: there is no standalone mainnet Comet — the only
	// mainnet Comet deployment IS Blend's backstop pool, instantiated
	// in the SAME ledger as Blend's Pool Factory V2 during Blend's
	// mainnet rollout. Exact instantiation ledger L51,499,546 per
	// comet.md:157 ("first instantiated by Blend's deploy") and
	// blend.md:90 ("the factory's deploy ledger (L51,499,546)").
	// (Was 51_499_545 — that was the walk-JSON from_ledger /
	// ContractCode-upload boundary, off by one vs the actual
	// ContractInstance create; corrected under #10 "exact, zero
	// slack".)
	"comet":         51_499_546, // Blend backstop pool instantiation (comet.md:157)
	"blend":         51_499_546, // Pool Factory V2 deploy, same ledger (blend.md:90)
	"reflector-dex": 50_644_229, // v2 WASM deploy (reflector.md:186)
	"reflector-cex": 50_644_239, // v2 WASM deploy, +10 after DEX (reflector.md:186)
	"reflector-fx":  56_733_481, // deployed fresh on v3, no prior history (reflector.md:195)
	"band":          50_842_736, // single stable WASM since 2024-03-19 (band.md:198)
	"redstone":      58_758_722, // first-deploy hotfix, replaced +420 ledgers (redstone.md:179)
	// defindex is paltalabs' yield aggregator, a separate 2025
	// protocol. EXACT first-deploy from the 2026-05-19 r1 wasm-history
	// walk (merged.json): factory CDKFHFJI... first observed at
	// L57,056,338 — staggered ahead of its three vaults (CDB2WMKQ
	// L57,056,388 / CC5CE6MW L57,056,390 / CDPWNUW7 L57,056,392),
	// which confirms these are genuine deploy ledgers, not the walk
	// window's lower bound. MIN across every contract the source
	// routes = the factory = 57,056,338. (Was a provisional
	// 51_499_545 placeholder, deliberately distinct from comet/blend
	// while the walk was pending; #10 "exact, zero slack".) NOTE:
	// defindex BackfillSafe stays false — the decoder↔deployed-WASM
	// mismatch (Task #28, defindex.md) is orthogonal to genesis
	// precision; an honest genesis here makes density read correctly,
	// not falsely.
	"defindex": 57_056_338,

	// TODO(#40, #41): add `cctp` + `rozo` genesis ledgers once their
	// per-source WASM-history walks complete. Both are brand-new
	// cross-chain bridges on Stellar with very short on-chain history
	// (BackfillSafe=false until audited; see registry.go). Until the
	// walks land at docs/operations/wasm-audits/{cctp,rozo}.md they
	// stay out of this map — the buildBackfillCoverage cache-row
	// fallback still surfaces them as "no genesis → no density",
	// which is the honest signal that the audit is owed.
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
	// #16: serve from the background-refreshed snapshot when present —
	// sub-millisecond instead of the 200-500ms inline build. Falls back
	// to inline-build when the refresher hasn't fired yet (process just
	// booted) so first-request-after-restart is never stuck.
	if entry := s.ingestionSnapshot.Load(); entry != nil {
		w.Header().Set("Cache-Control", "public, max-age=15, s-maxage=15")
		writeJSON(w, entry.snap, ingestionFlags(entry.snap))
		return
	}

	// Per-handler ceiling — 30s. Each filler uses its own
	// sub-context (5-10s each) so one slow reader doesn't starve
	// the others. Pre-2026-05-14 the parent ctx was 6s and the
	// fillers were sequential with no per-call timeout — when one
	// reader exceeded its share, every subsequent filler aborted
	// with `context deadline exceeded` and the response showed
	// 0% coverage on every source. Caught live on r1 12:45 UTC.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	out := s.buildIngestionSnapshot(ctx)
	w.Header().Set("Cache-Control", "public, max-age=15, s-maxage=15")
	writeJSON(w, out, ingestionFlags(out))
}

// ingestionFlags maps a built IngestionDiagnostics onto the wire
// Flags. flags.stale fires when any critical filler soft-failed OR
// when LatestLedger == 0 — both signal "we're serving zero-valued
// fields that look fresh." Pre-fix (F-0095) the handler always wrote
// Flags{}, so an all-zeros response under the F-0039 cascade looked
// indistinguishable from "the network has zero ledgers and zero
// trades." Mirrors the principle of /v1/network/stats returning a
// real error instead of zero-valued success on storage failure.
func ingestionFlags(snap IngestionDiagnostics) Flags {
	stale := snap.degraded || snap.Ledger.LatestLedger == 0
	return Flags{Stale: stale}
}

// ingestionSnapshotEntry wraps a computed IngestionDiagnostics for
// atomic storage. Kept separate from the response type so future
// per-snapshot metadata (computedAt for staleness checks, build
// duration for an SLI) lands here without bloating the wire shape.
type ingestionSnapshotEntry struct {
	snap IngestionDiagnostics
}

// StartIngestionSnapshotRefresh launches a background goroutine that
// refreshes [Server.ingestionSnapshot] every 15s (matches the
// existing `Cache-Control: max-age=15`) by running the same build
// path the inline-fallback handler uses. Stops on ctx cancellation.
// Safe to call once at server start; calling twice would double the
// work but not corrupt state (atomic.Pointer.Store semantics). One
// shoot-and-store per cycle: a slow build doesn't block subsequent
// reads — they keep serving the previous snapshot until store.
//
// Cadence rationale: 15s matches the explorer's status-tile poll
// cadence + the existing browser cache-control header. Tighter is
// wasted DB calls; looser would surface a 1-cycle lag spike during
// post-restart cold-cache windows that the cache header already
// permits anyway.
func (s *Server) StartIngestionSnapshotRefresh(ctx context.Context) {
	const cadence = 15 * time.Second
	// Fire once immediately so the first user request hits the warm
	// path. Build under its own per-call ctx — independent of the
	// caller's parent ctx — for the same outlive-request-lifetime
	// rationale as the SWR detached-fill pattern in CachedHistoryReader.
	doRefresh := func() { //nolint:gosec,contextcheck // G118/contextcheck — intentional detached build; the parent ctx is the LIFETIME of the api process, not any individual request, so the closure deliberately doesn't accept the caller's ctx + uses context.Background+timeout instead.
		buildCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out := s.buildIngestionSnapshot(buildCtx)
		// Preserve last-known-good when the new build is degraded AND
		// effectively empty (LatestLedger == 0). Stomping a fresh
		// snapshot with an all-zeros one is how F-0095 stayed
		// invisible: every counter showed 0 with flags.stale:false
		// even though /v1/network/stats (same reader, no cache layer)
		// was returning real numbers in the same probe window. The
		// handler will still mark flags.stale:true for the preserved
		// snapshot via ingestionFlags so the response is honest about
		// being a fallback, not a fresh read.
		if out.degraded && out.Ledger.LatestLedger == 0 {
			if prev := s.ingestionSnapshot.Load(); prev != nil && prev.snap.Ledger.LatestLedger > 0 {
				// Mark the preserved snapshot degraded so the wire
				// flag fires regardless of the prior build's flag.
				keep := prev.snap
				keep.degraded = true
				s.ingestionSnapshot.Store(&ingestionSnapshotEntry{snap: keep})
				return
			}
		}
		s.ingestionSnapshot.Store(&ingestionSnapshotEntry{snap: out})
	}
	doRefresh()
	t := time.NewTicker(cadence)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			doRefresh()
		}
	}
}

// buildIngestionSnapshot runs the parallel-fillers pipeline that
// composes one IngestionDiagnostics. Called by both the inline-
// handler fallback (when the atomic snapshot isn't populated yet)
// and the background refresher. Self-contained — no response
// writes, no header sets; returns the snapshot only.
func (s *Server) buildIngestionSnapshot(ctx context.Context) IngestionDiagnostics { //nolint:funlen,gocognit,gocyclo // linear filler-orchestration; splitting would obscure the parallel pattern.
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

	// Each filler is a closure over &out's per-field write target;
	// `degraded` is the SHARED scratchpad across fillers, so it can't
	// live on `out` (data race) — instead it's an atomic.Bool here
	// and copied onto out.degraded after wg.Wait(). Fillers signal
	// via degraded.Store(true) when their critical reader soft-fails.
	var degraded atomic.Bool
	type filler struct {
		name    string
		fn      func(context.Context)
		timeout time.Duration
	}
	fillers := []filler{
		{"sources", func(c context.Context) { out.Sources = buildSourceHealth(c, s) }, 8 * time.Second},
		{"ledger", func(c context.Context) { s.fillIngestionLedger(c, &out, &degraded) }, 6 * time.Second},
		{"backfill", func(c context.Context) { s.fillIngestionBackfill(c, &out, &degraded) }, 5 * time.Second},
		{"fx_coverage", func(c context.Context) { s.fillIngestionFXCoverage(c, &out) }, 5 * time.Second},
		{"supply_coverage", func(c context.Context) { s.fillIngestionSupplyCoverage(c, &out) }, 5 * time.Second},
		{"cagg_coverage", func(c context.Context) { s.fillIngestionCAGGCoverage(c, &out) }, 5 * time.Second},
		{"entry_counts", func(c context.Context) { s.fillIngestionEntryCounts(c, &out, &degraded) }, 5 * time.Second},
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
	// Hoist the atomic-shared degraded sidecar onto the snapshot
	// struct now that every goroutine has finished. Safe single
	// writer here (we're back on the calling goroutine).
	if degraded.Load() {
		out.degraded = true
	}

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
	return out
}

// fillIngestionLedger reads network-stats and copies the four
// numeric fields onto out.Ledger. Soft-fail: a stuck reader leaves
// the section at zero-valued defaults rather than erroring the
// whole response — but signals the shared `degraded` atomic.Bool so
// the response is marked flags.stale:true rather than passing the
// zero defaults off as a fresh successful read (F-0095). The atomic
// is a separate sidecar from out.degraded because the parallel
// fillers would otherwise race on the shared field; the parent
// goroutine hoists the resolved value onto out after wg.Wait().
func (s *Server) fillIngestionLedger(ctx context.Context, out *IngestionDiagnostics, degraded *atomic.Bool) {
	if s.networkStats == nil {
		degraded.Store(true)
		return
	}
	ns, err := s.networkStats.GetNetworkStats(ctx)
	if err != nil {
		s.logger.Warn("diagnostics/ingestion: network_stats", "err", err)
		degraded.Store(true)
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
func (s *Server) fillIngestionBackfill(ctx context.Context, out *IngestionDiagnostics, degraded *atomic.Bool) {
	if s.cursors == nil {
		degraded.Store(true)
		return
	}
	rows, err := s.cursors.ListCursors(ctx)
	if err != nil {
		s.logger.Warn("diagnostics/ingestion: cursors", "err", err)
		degraded.Store(true)
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
func (s *Server) fillIngestionEntryCounts(ctx context.Context, out *IngestionDiagnostics, degraded *atomic.Bool) {
	reader, ok := s.fxHistory.(SourceEntryCountReader)
	if !ok || reader == nil {
		// Not a test fake (those inject a discard logger): in
		// production this means the fxHistory adapter is missing its
		// SourceEntryCounts delegate, which silently zeroes the
		// `entries` column for every source. Warn so this class of
		// wiring regression is visible instead of invisible — it
		// shipped unnoticed in rc.55.
		s.logger.Warn("diagnostics/ingestion: entry_counts reader unavailable — entries will read 0 for all sources")
		return
	}
	counts, err := reader.SourceEntryCounts(ctx)
	if err != nil {
		s.logger.Warn("diagnostics/ingestion: entry_counts", "err", err)
		degraded.Store(true)
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
	merged = extendWithLiveTail(merged, cursors, genesis, tip)
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

// liveLedgerstreamRange returns the live cursor's persisted coverage
// span [first_ledger, last_ledger] (post-migration 0046). first=0
// signals "first_ledger was NULL on disk" (pre-migration rows that
// haven't yet been touched by an UpsertCursor UPDATE — the UPDATE
// branch COALESCE-populates first_ledger on the first write after
// deploy, so this transient window typically lasts one live tick).
// extendWithLiveTail contributes nothing to coverage in that case
// — honest about "we don't yet know how far back this cursor
// reaches" rather than the pre-2026-05-28 behaviour of falling
// back to sourceGenesisLedger and silently crediting genesis-onward
// coverage. hasCursor=false when no live cursor row exists at all.
func liveLedgerstreamRange(cursors []timescale.Cursor) (first, last int64, hasCursor bool) {
	for _, c := range cursors {
		if c.Source != "ledgerstream" {
			continue
		}
		if l := int64(c.LastLedger); l > last {
			last = l
			first = int64(c.FirstLedger)
			hasCursor = true
		}
	}
	return first, last, hasCursor
}

// extendWithLiveTail credits the live-ingest tail on top of the
// backfill union. Live ingest is gap-free from its low-water to
// tip (sequential walker + archivecompleteness daemon, ADR-0017)
// — but **only for sources actively enabled in the indexer's
// decoder set during that live walk**. The ledgerstream cursor
// itself doesn't carry per-source attribution: it advances
// regardless of which decoders were running. So we can only
// credit the live cursor for a source's coverage in regions
// where live ingest provably decoded it.
//
// Credit awarded (post-migration-0046, 100% density mission):
//
//   - **Persisted live span.** From the live cursor's
//     `first_ledger` to min(liveTop, tip). first_ledger is the
//     first ledger this region's live indexer wrote a cursor for
//     (captured on the INSERT branch of UpsertCursor and
//     preserved by ON CONFLICT DO UPDATE). The live cursor is
//     gap-free from first_ledger to last_ledger by construction
//     — sequential walker + archivecompleteness daemon — so the
//     whole band is creditable. This closes the
//     [genesis, backfill_low] sub-band that backfill cursors
//     don't cover when live ingest has been running since
//     before the first backfill range started, lifting the
//     density ceiling from ~98% to 100% on a fully-caught-up
//     source (project_density_100pct_goal).
//
//   - **NULL first_ledger handling (2026-05-28 honest-density fix).**
//     When the live cursor row's first_ledger is NULL (pre-migration-0046
//     row that hasn't been UPDATE'd yet) we now **decline to credit**
//     any live span. The pre-2026-05-28 behaviour was to fall back to
//     `[genesis, last_ledger]`, which silently inflated density to
//     100% for every source. The fallback's stated premise ("the
//     UPDATE branch flips first_ledger on next write") was wrong:
//     the UPDATE branch left first_ledger untouched, so the NULL
//     persisted forever and the fallback became a permanent lie that
//     hid the F-0020 cascade-window ingest gap on r1.
//     UpsertCursor now COALESCE-populates first_ledger on the first
//     UPDATE after a NULL row, so this branch only applies in the
//     seconds between deploy and the live indexer's first tick.
//

// Credit deliberately NOT awarded:
//
//   - **Interior sub-tip gaps via live-bridging (removed
//     2026-05-20).** A gap between two merged backfill
//     intervals USED to be bridged when its upper neighbour
//     started ≤ liveTop. That bridging was over-eager (e.g.
//     soroswap-router / defindex added to enabled_sources at
//     rc.5x with live cursor already at ~62.5M — interior
//     [60M, 62.5M] gap incorrectly credited). The fix in
//     migration 0046 + persisted first_ledger is the
//     RIGHT-WAY-AROUND replacement: a late-enabled source
//     gets its live cursor's first_ledger set to that
//     late-enable ledger on first write, so the live span
//     starts at the late-enable ledger and the [60M, 62.5M]
//     interior gap stays UNcovered. Operators close it via an
//     explicit backfill, same as before.
//
// Honest-by-construction guards retained:
//
//   - merged empty (never backfilled, e.g. BackfillSafe=false
//     Soroban source) → unchanged, stays 0%. The live cursor
//     alone doesn't credit a never-backfilled source, because
//     we still gate the live-span credit on the existence of
//     at least one backfill anchor: the
//     `decoderSetContains`-filter that built `merged` proves
//     the source was an active decoder target at some point.
//     A source that's never had a backfill range claimed for
//     it has `merged == nil` and reads 0% — the right answer.
//   - no live cursor (hasCursor=false: test fixture / region
//     without live ingest) → unchanged.
//   - nothing is ever credited above min(liveTop, tip).
//   - nothing is ever credited below the source's genesis ledger
//     (mergeCoverageIntervals + the clamp on liveStart).
func extendWithLiveTail(merged []coverageInterval, cursors []timescale.Cursor, genesis, tip int64) []coverageInterval {
	if len(merged) == 0 {
		return merged
	}
	liveStart, liveTop, hasCursor := liveLedgerstreamRange(cursors)
	if !hasCursor {
		return merged
	}
	if liveTop > tip {
		liveTop = tip
	}
	if liveTop <= 0 {
		return merged
	}

	// NULL first_ledger handling (2026-05-28 honest-density fix):
	// previously this branch fell back to `liveStart = genesis`, which
	// silently inflated every source's density to 100% for live cursor
	// rows that pre-dated migration 0046 (the rollback case the audit
	// observed on r1 post-rc.83). The fallback assumed
	// `UpsertCursor`'s UPDATE branch would soon populate first_ledger
	// — but the UPDATE branch did not touch the column, so the live
	// cursor stayed NULL forever and the fallback became a permanent
	// lie. UpsertCursor now COALESCE-populates first_ledger on the
	// first UPDATE after deploy, so this branch should fire only for
	// the seconds between deploy and the first live tick. Until then,
	// don't credit any historical live span: backfill cursors are the
	// only credit source, which is the honest answer.
	if liveStart <= 0 {
		return merged
	}
	if liveStart < genesis {
		liveStart = genesis
	}
	if liveStart > liveTop {
		// Defensive: a non-sensical persisted span (first_ledger >
		// last_ledger) shouldn't credit anything.
		return merged
	}

	out := make([]coverageInterval, 0, len(merged)+1)
	out = append(out, merged...)
	out = append(out, coverageInterval{Start: liveStart, End: liveTop})

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

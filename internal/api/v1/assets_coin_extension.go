package v1

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// coinExtensionTimeout caps the total wall time for the
// coin-equivalence overlay on /v1/assets/{id}. Five reader calls
// run in parallel; each is bounded by this shared deadline.
const coinExtensionTimeout = 4 * time.Second

// applyCoinExtensionFields lifts the trailing-window activity +
// history fields from the coins catalogue onto AssetDetail. Skipped
// for fiat:* / external:* assets (no coin row) and when no
// CoinsReader is wired.
//
// All sub-fetches run in parallel and are best-effort: individual
// failures log at Debug level and leave the affected field nil. A
// missing coin row (the asset has never traded) is the common case
// and isn't an error — it just leaves all the extension fields
// nil/empty.
//
// Same readers /v1/coins/{slug} uses — wiring is identical, the
// only difference is the lookup key (asset_id from the URL path
// here vs slug there).
// coinExtensionResults holds the parallel-fetch results. Separated
// from applyCoinExtensionFields to keep that function's cognitive
// complexity under the gocognit ceiling.
type coinExtensionResults struct {
	topMarkets    []timescale.CoinTopMarket
	topMarketsErr error
	hist24        []timescale.CoinPricePoint
	hist24Err     error
	hist7d        []timescale.CoinPricePoint
	hist7dErr     error
	marketsCount  int64
	marketsErr    error
	tradeCount    int64
	tradeCountErr error
	ath           *timescale.CoinATH
	athErr        error
}

func (s *Server) applyCoinExtensionFields(ctx context.Context, detail *AssetDetail, asset canonical.Asset) {
	if s.coins == nil || asset.Type == canonical.AssetFiat {
		return
	}
	assetID := asset.String()

	cctx, cancel := context.WithTimeout(ctx, coinExtensionTimeout)
	defer cancel()

	row, rowErr := s.lookupCoinRow(cctx, asset, assetID)
	results := s.fetchCoinExtensionResults(cctx, assetID)

	s.applyCoinRowToDetail(detail, row, rowErr, assetID)
	applyCoinExtensionResults(detail, results)
	s.logCoinExtensionFailures(assetID, results)
}

// fetchCoinExtensionResults runs the 6 reader calls in parallel.
func (s *Server) fetchCoinExtensionResults(ctx context.Context, assetID string) coinExtensionResults {
	var (
		r  coinExtensionResults
		wg sync.WaitGroup
	)
	wg.Add(6)
	go func() {
		defer wg.Done()
		r.topMarkets, r.topMarketsErr = s.coins.GetCoinTopMarkets(ctx, assetID, 5)
	}()
	go func() {
		defer wg.Done()
		r.hist24, r.hist24Err = s.coins.GetCoinPriceHistory24h(ctx, assetID)
	}()
	go func() {
		defer wg.Done()
		r.hist7d, r.hist7dErr = s.coins.GetCoinPriceHistory7d(ctx, assetID)
	}()
	go func() {
		defer wg.Done()
		r.marketsCount, r.marketsErr = s.coins.GetCoinMarketsCount(ctx, assetID)
	}()
	go func() {
		defer wg.Done()
		r.tradeCount, r.tradeCountErr = s.coins.GetCoinTradeCount24h(ctx, assetID)
	}()
	go func() {
		defer wg.Done()
		r.ath, r.athErr = s.coins.GetCoinATH(ctx, assetID)
	}()
	wg.Wait()
	return r
}

// applyCoinRowToDetail mirrors scalar fields from CoinRow onto
// AssetDetail. sql.ErrNoRows is the expected "no coin row" case —
// silent skip. Other errors are logged at Debug.
func (s *Server) applyCoinRowToDetail(detail *AssetDetail, row timescale.CoinRow, err error, assetID string) {
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.logger.Debug("coin extension: row lookup failed",
				"asset_id", assetID, "err", err)
		}
		return
	}
	if row.PriceUSD != nil {
		detail.PriceUSD = row.PriceUSD
	}
	if row.Change1hPct != nil {
		detail.Change1hPct = row.Change1hPct
	}
	if row.Change7dPct != nil {
		detail.Change7dPct = row.Change7dPct
	}
	if reason := scamReason(row.IssuerGStrkey); reason != "" {
		detail.IssuerScamReason = reason
	}
	// Identity + activity metadata. Mirrors CoinSummary scalars so
	// the explorer's asset-detail page can drop its parallel
	// /v1/coins/{slug} fetch (R-018 finish — consumer migration).
	if row.Slug != "" {
		detail.Slug = row.Slug
	}
	if row.FirstSeenLedger != 0 {
		v := row.FirstSeenLedger
		detail.FirstSeenLedger = &v
	}
	if row.LastSeenLedger != 0 {
		v := row.LastSeenLedger
		detail.LastSeenLedger = &v
	}
	if row.ObservationCount != 0 {
		v := row.ObservationCount
		detail.ObservationCount = &v
	}
}

// applyCoinExtensionResults populates the array-shaped fields from
// the parallel-fetch results. Each field is independent — one
// failure doesn't fail the others.
func applyCoinExtensionResults(detail *AssetDetail, r coinExtensionResults) {
	if r.topMarketsErr == nil && len(r.topMarkets) > 0 {
		detail.TopMarkets = topMarketsToWire(r.topMarkets)
	}
	if r.hist24Err == nil && len(r.hist24) > 0 {
		detail.PriceHistory24h = coinPointsToWire(r.hist24)
	}
	if r.hist7dErr == nil && len(r.hist7d) > 0 {
		detail.PriceHistory7d = coinPointsToWire(r.hist7d)
	}
	if r.marketsErr == nil {
		v := r.marketsCount
		detail.MarketsCount = &v
	}
	if r.tradeCountErr == nil {
		v := r.tradeCount
		detail.TradeCount24h = &v
	}
	if r.athErr == nil && r.ath != nil {
		detail.ATH = &CoinATH{USD: r.ath.USD, At: r.ath.At}
	}
}

// logCoinExtensionFailures emits one Debug line per failed sub-fetch
// so an operator can correlate cold-cache spikes without 6 separate
// log helpers inside the parallel-fetch goroutines.
func (s *Server) logCoinExtensionFailures(assetID string, r coinExtensionResults) {
	for _, e := range [...]struct {
		err error
		tag string
	}{
		{r.topMarketsErr, "top_markets"},
		{r.hist24Err, "price_history_24h"},
		{r.hist7dErr, "price_history_7d"},
		{r.marketsErr, "markets_count"},
		{r.tradeCountErr, "trade_count_24h"},
		{r.athErr, "ath"},
	} {
		if e.err != nil {
			s.logger.Debug("coin extension: "+e.tag+" failed",
				"asset_id", assetID, "err", e.err)
		}
	}
}

// topMarketsToWire projects storage rows onto the API shape.
func topMarketsToWire(in []timescale.CoinTopMarket) []CoinTopMarket {
	out := make([]CoinTopMarket, len(in))
	for i, m := range in {
		out[i] = CoinTopMarket{
			Counterparty:  m.Counterparty,
			Side:          m.Side,
			Volume24hUSD:  m.Volume24hUSD,
			TradeCount24h: m.TradeCount24h,
		}
	}
	return out
}

// lookupCoinRow picks the right CoinsReader method based on the
// asset shape: native short-circuits to GetNativeCoinRow; everything
// else uses GetCoinByAssetID.
func (s *Server) lookupCoinRow(ctx context.Context, asset canonical.Asset, assetID string) (timescale.CoinRow, error) {
	if asset.Type == canonical.AssetNative {
		return s.coins.GetNativeCoinRow(ctx)
	}
	return s.coins.GetCoinByAssetID(ctx, assetID)
}

// coinPointsToWire projects the storage-layer price points onto the
// API wire shape — same field rename (Bucket → T, USDPrice → P) as
// /v1/coins uses.
func coinPointsToWire(pts []timescale.CoinPricePoint) []CoinPricePoint {
	out := make([]CoinPricePoint, len(pts))
	for i, p := range pts {
		out[i] = CoinPricePoint{T: p.T, P: p.P}
	}
	return out
}

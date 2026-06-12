package v1

import (
	"context"

	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// CoinsReader is the seam the /v1/assets handlers read through to
// surface coin-catalogue data (price / volume / market_cap /
// sparkline / ATH). The legacy /v1/coins HTTP surface has been
// removed (no production consumers); this interface stays because
// /v1/assets sources the same data through it.
//
// timescale.Store satisfies it via ListCoinsExt + GetCoinBySlug
// + GetNativeCoinRow + GetCoinTopMarkets + GetCoinPriceHistory24h
// + GetCoinMarketsCount.
type CoinsReader interface {
	ListCoinsExt(ctx context.Context, opts timescale.ListCoinsOptions) ([]timescale.CoinRow, error)
	GetCoinBySlug(ctx context.Context, slug string) (timescale.CoinRow, error)
	// GetCoinByAssetID looks up a classic asset by its canonical
	// asset_id (CODE-ISSUER form). Distinct from [GetCoinBySlug]
	// which looks up by the friendly short slug (USDC, AQUA).
	GetCoinByAssetID(ctx context.Context, assetID string) (timescale.CoinRow, error)
	GetNativeCoinRow(ctx context.Context) (timescale.CoinRow, error)
	GetCoinTopMarkets(ctx context.Context, assetID string, limit int) ([]timescale.CoinTopMarket, error)
	GetCoinPriceHistory24h(ctx context.Context, assetID string) ([]timescale.CoinPricePoint, error)
	GetCoinPriceHistory7d(ctx context.Context, assetID string) ([]timescale.CoinPricePoint, error)
	GetCoinsPriceHistory24hBatch(ctx context.Context, assetIDs []string) (map[string][]timescale.CoinPricePoint, error)
	GetCoinsPriceHistory7dBatch(ctx context.Context, assetIDs []string) (map[string][]timescale.CoinPricePoint, error)
	GetCoinMarketsCount(ctx context.Context, assetID string) (int64, error)
	GetCoinATH(ctx context.Context, assetID string) (*timescale.CoinATH, error)
	GetCoinsATHBatch(ctx context.Context, assetIDs []string) (map[string]timescale.CoinATH, error)
	GetCoinTradeCount24h(ctx context.Context, assetID string) (int64, error)
}

// CoinATH is the all-time-high USD price + bucket-day pair. Embedded
// in AssetDetail.ATH.
type CoinATH struct {
	USD string `json:"usd"`
	At  string `json:"at"`
}

// CoinTopMarket is one entry in the per-asset top-markets preview.
// `Counterparty` is the OTHER side of the pair (the asset that's
// not the one being queried). `Side` is "base" or "quote".
type CoinTopMarket struct {
	Counterparty  string  `json:"counterparty"`
	Side          string  `json:"side"`
	Volume24hUSD  *string `json:"volume_24h_usd,omitempty"`
	TradeCount24h int64   `json:"trade_count_24h"`
}

// CoinPricePoint is one hourly/daily USD-price sample in
// AssetDetail.PriceHistory24h / PriceHistory7d. `T` is the bucket
// end as an RFC3339 timestamp; `P` is the rounded-to-10dp USD price
// or nil when no trades that bucket produced a VWAP.
type CoinPricePoint struct {
	T string  `json:"t"`
	P *string `json:"p,omitempty"`
}

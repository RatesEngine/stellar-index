package v1

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// CoinsReader is the seam the /v1/coins handlers read through.
// timescale.Store satisfies it via ListCoinsExt + GetCoinBySlug
// + GetNativeCoinRow + GetCoinTopMarkets + GetCoinPriceHistory24h
// + GetCoinMarketsCount.
type CoinsReader interface {
	ListCoinsExt(ctx context.Context, opts timescale.ListCoinsOptions) ([]timescale.CoinRow, error)
	GetCoinBySlug(ctx context.Context, slug string) (timescale.CoinRow, error)
	// GetCoinByAssetID looks up a classic asset by its canonical
	// asset_id (CODE-ISSUER form). Distinct from [GetCoinBySlug]
	// which looks up by the friendly short slug (USDC, AQUA).
	// Used by /v1/coins/{slug} when the URL contains a canonical
	// asset_id rather than a friendly slug.
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

// Coin is the wire shape of one entry in the /v1/coins response.
//
// PriceUSD / Volume24hUSD / MarketCapUSD / CirculatingSupply are
// nullable strings — emitted as `null` in JSON when the
// aggregator hasn't yet produced a value (newly-observed asset,
// no off-chain peg, etc.). All numeric fields are strings to
// preserve precision per ADR-0003.
type Coin struct {
	Slug              string  `json:"slug"`
	AssetID           string  `json:"asset_id"`
	Code              string  `json:"code"`
	Issuer            string  `json:"issuer"`
	FirstSeenLedger   uint32  `json:"first_seen_ledger"`
	LastSeenLedger    uint32  `json:"last_seen_ledger"`
	ObservationCount  int64   `json:"observation_count"`
	PriceUSD          *string `json:"price_usd,omitempty"`
	Volume24hUSD      *string `json:"volume_24h_usd,omitempty"`
	MarketCapUSD      *string `json:"market_cap_usd,omitempty"`
	CirculatingSupply *string `json:"circulating_supply,omitempty"`
	// Change1hPct / Change24hPct / Change7dPct are the trailing
	// price changes for those windows, signed percentages with
	// two fractional digits (e.g. "+1.27", "-0.05", "0.00"). Null
	// when the asset has no current price or when no past-bucket
	// snapshot exists in prices_1m within the window-specific
	// tolerance (±5min for 1h, ±30min for 24h, ±2h for 7d).
	Change1hPct  *string `json:"change_1h_pct,omitempty"`
	Change24hPct *string `json:"change_24h_pct,omitempty"`
	Change7dPct  *string `json:"change_7d_pct,omitempty"`
	// TopMarkets is a preview of the asset's top markets by 24h
	// USD volume. Populated only on /v1/coins/{slug} (the
	// listing endpoint omits it to keep payload sizes manageable).
	TopMarkets []CoinTopMarket `json:"top_markets,omitempty"`
	// PriceHistory24h is 24 hourly USD-price samples (oldest
	// first) covering the trailing 24h. Populated only on
	// /v1/coins/{slug}. Each entry: {t: RFC3339 bucket end,
	// p: rounded-to-10dp USD price or null when no trades that
	// hour}. Powers asset detail sparkline + chart preview.
	PriceHistory24h []CoinPricePoint `json:"price_history_24h,omitempty"`
	// PriceHistory7d is 7 daily USD-price samples (oldest first)
	// covering the trailing 7 days. Populated only on
	// /v1/coins/{slug}. Same shape as PriceHistory24h; powers a
	// 7-day mini chart on the asset detail page.
	PriceHistory7d []CoinPricePoint `json:"price_history_7d,omitempty"`
	// MarketsCount is the count of distinct (base, quote) pairs
	// the asset participated in over the trailing 24h. Populated
	// only on /v1/coins/{slug}. Pointer so 0 (asset went silent
	// in the last 24h) is distinguishable from "not computed"
	// (lookup error) — both render as "—" in the UI but behave
	// differently in alerting.
	MarketsCount *int64 `json:"markets_count,omitempty"`
	// TradeCount24h is the count of trades the asset participated
	// in (as base OR quote) over the trailing 24h. Populated only
	// on /v1/coins/{slug}. Read from the trades hypertable directly
	// — accurate down to the individual trade rather than
	// MarketsCount's distinct-pair aggregation. Companion to the
	// all-time `observation_count`.
	TradeCount24h *int64 `json:"trade_count_24h,omitempty"`
	// ATH is the asset's all-time-high USD price plus the day it
	// was set. Populated only on /v1/coins/{slug}. Sourced from
	// `prices_1d` filtered to USD-denominated quotes — triangulated
	// paths excluded (a single bad XLM/USD reading on a thin day
	// could fabricate an ATH). Null when the asset has no
	// USD-quoted history.
	ATH *CoinATH `json:"ath,omitempty"`
	// IssuerScamReason is non-empty when this asset's `issuer`
	// G-strkey appears in the curated `known_scams.go` map sourced
	// from stellar.expert's directory. Mirrors the same field
	// served on /v1/issuers and /v1/issuers/{g_strkey}; clients
	// should render a prominent warning ("known scam asset — do
	// not trust") when present. Always omitted for native XLM
	// (issuer is empty) and for issuers we have no scam record on.
	IssuerScamReason string `json:"issuer_scam_reason,omitempty"`
}

// CoinATH is the all-time-high USD price + bucket-day pair on
// the /v1/coins/{slug} response.
type CoinATH struct {
	USD string `json:"usd"`
	At  string `json:"at"`
}

// CoinTopMarket is one entry in the per-asset top-markets preview.
// `Counterparty` is the OTHER side of the pair (the asset that's
// not the one being queried). `Side` is "base" or "quote"
// depending on which side the queried asset took.
type CoinTopMarket struct {
	Counterparty  string  `json:"counterparty"`
	Side          string  `json:"side"`
	Volume24hUSD  *string `json:"volume_24h_usd,omitempty"`
	TradeCount24h int64   `json:"trade_count_24h"`
}

// CoinPricePoint is one hourly USD-price sample in
// `Coin.PriceHistory24h`. `T` is the bucket end as an RFC3339
// timestamp; `P` is the rounded-to-10dp USD price or nil when no
// trades that hour produced a VWAP.
type CoinPricePoint struct {
	T string  `json:"t"`
	P *string `json:"p,omitempty"`
}

// CoinsPage wraps the rows + cursor pagination metadata. The
// `next_cursor` is empty when the last page has been returned;
// clients iterate while it's non-empty.
type CoinsPage struct {
	Coins      []Coin `json:"coins"`
	NextCursor string `json:"next_cursor,omitempty"`
	Limit      int    `json:"limit"`
}

// handleCoins serves GET /v1/coins.
//
// Query parameters:
//
//	limit   1..500 (default 100). Number of rows per page.
//	cursor  opaque keyset cursor; pass back the value from
//	        `next_cursor` in the previous response to fetch
//	        the next page. Empty for the first page.
//	issuer  optional G-strkey filter.
//
// Response shape:
//
//	{
//	  "data": {
//	    "coins": [...],
//	    "next_cursor": "<opaque>",   // empty when no more pages
//	    "limit": 100
//	  },
//	  ...envelope...
//	}
func (s *Server) handleCoins(w http.ResponseWriter, r *http.Request) { //nolint:gocognit,gocyclo,funlen // option parsing + native-prepend + sparkline opt-in are linear & cohesive; splitting would just spread the request lifecycle across helpers
	if s.coins == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-unavailable",
			"Coins listing unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the coins reader yet.")
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be 1-500")
			return
		}
		limit = n
	}

	issuer := r.URL.Query().Get("issuer")
	cursor := r.URL.Query().Get("cursor")
	q := r.URL.Query().Get("q")
	if len(q) > 64 {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-query",
			"Invalid q", http.StatusBadRequest,
			"q must be 64 chars or fewer")
		return
	}
	var order timescale.CoinsOrder
	switch r.URL.Query().Get("order_by") {
	case "", "observation_count_desc":
		order = timescale.CoinsOrderObservationCountDesc
	case "volume_24h_usd_desc":
		order = timescale.CoinsOrderVolume24hUSDDesc
	default:
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-order",
			"Invalid order_by", http.StatusBadRequest,
			"order_by must be 'observation_count_desc' or 'volume_24h_usd_desc'")
		return
	}

	if err := timescale.ValidateCoinsCursor(cursor, order); err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-cursor",
			"Invalid cursor", http.StatusBadRequest,
			"cursor: "+err.Error()+". Pass back the next_cursor returned by a prior /v1/coins response, or omit the parameter to start at page 1.")
		return
	}

	// Native XLM has no classic_assets row but is the most-active
	// asset on the network — prepend it at the top of the first
	// unfiltered page so /v1/coins (and the explorer's home page
	// that consumes it) never silently omits XLM. We fetch
	// `limit-1` rows from the listing in this mode so the page
	// size stays exactly `limit`. When any filter is active the
	// user has narrowed the listing on purpose, so don't inject.
	prependNative := cursor == "" && issuer == "" && q == "" && limit >= 2
	listingLimit := limit
	if prependNative {
		listingLimit = limit - 1
	}

	// 8s ceiling on the listing read + every downstream batch
	// (sparkline / ATH / native-row). Same pattern as #1082 /
	// #1099 / #1100; the cached reader keeps p50 < 100ms but
	// cold-cache paths can take 5-10s scanning the
	// classic_assets + prices_1m hypertables. The downstream
	// optional-include fan-outs already soft-fail on error
	// (warn log + omit the field), so a deadline degrades
	// gracefully across the chain.
	listCtx, listCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer listCancel()
	rows, err := s.coins.ListCoinsExt(listCtx, timescale.ListCoinsOptions{
		Limit: listingLimit, Issuer: issuer, Cursor: cursor, Q: q, Order: order,
	})
	if err != nil {
		// Client closed the connection mid-flight — pq returns
		// "canceling statement due to user request (57014)" via the
		// Go driver, which doesn't unwrap as context.Canceled but
		// the request context IS cancelled. Detect via the request
		// context, not the error chain. Don't 500 (the response
		// never reaches the client) and don't WARN (it's a normal
		// client behaviour, not a server error).
		if clientAborted(r, err) {
			return
		}
		if handlerTimedOut(listCtx, err) {
			s.logger.Warn("coins list deadline exceeded", "limit", listingLimit)
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/coins-timeout",
				"Coins listing timed out", http.StatusServiceUnavailable,
				"the underlying classic_assets + prices_1m scan didn't return in 8s; cache may still be warming. Retry in a few seconds.")
			return
		}
		s.logger.Warn("coins list", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-error",
			"Coins listing failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}

	// Compute next-cursor from the listing's last real row BEFORE
	// we prepend native — page 2 must resume past the listing
	// tail, never past the synthetic XLM row.
	nextCursor := nextCoinCursor(rows, listingLimit, order)

	out := make([]Coin, 0, limit)
	if prependNative {
		if nativeRow, nErr := s.coins.GetNativeCoinRow(r.Context()); nErr != nil {
			s.logger.Warn("coins list: native row lookup failed; emitting without it",
				"err", nErr)
		} else if nativeRow.AssetID != "" {
			out = append(out, coinFromRow(nativeRow))
		}
	}
	for _, row := range rows {
		out = append(out, coinFromRow(row))
	}

	// Optional opt-ins via ?include=. Default off — payload bloat on
	// the typical /v1/coins call would be wasted bytes for SDK
	// consumers that don't render charts or ATH context.
	//   sparkline   → attach 24h hourly price history per row
	//   sparkline7d → attach 7d daily price history per row
	//   ath         → attach all-time-high USD price + day per row
	includeSparkline := false
	includeSparkline7d := false
	includeATH := false
	for _, f := range strings.Split(r.URL.Query().Get("include"), ",") {
		switch strings.TrimSpace(f) {
		case "sparkline":
			includeSparkline = true
		case "sparkline7d":
			includeSparkline7d = true
		case "ath":
			includeATH = true
		}
	}
	if includeSparkline && len(out) > 0 {
		ids := make([]string, len(out))
		for i, c := range out {
			ids[i] = c.AssetID
		}
		if hist, hErr := s.coins.GetCoinsPriceHistory24hBatch(r.Context(), ids); hErr != nil {
			s.logger.Warn("coins list: sparkline batch failed", "err", hErr)
		} else {
			for i, c := range out {
				series := hist[c.AssetID]
				if len(series) == 0 {
					continue
				}
				converted := make([]CoinPricePoint, len(series))
				for j, p := range series {
					converted[j] = CoinPricePoint{T: p.T, P: p.P}
				}
				out[i].PriceHistory24h = converted
			}
		}
	}
	if includeSparkline7d && len(out) > 0 {
		ids := make([]string, len(out))
		for i, c := range out {
			ids[i] = c.AssetID
		}
		if hist, hErr := s.coins.GetCoinsPriceHistory7dBatch(r.Context(), ids); hErr != nil {
			s.logger.Warn("coins list: sparkline7d batch failed", "err", hErr)
		} else {
			for i, c := range out {
				series := hist[c.AssetID]
				if len(series) == 0 {
					continue
				}
				converted := make([]CoinPricePoint, len(series))
				for j, p := range series {
					converted[j] = CoinPricePoint{T: p.T, P: p.P}
				}
				out[i].PriceHistory7d = converted
			}
		}
	}
	if includeATH && len(out) > 0 {
		ids := make([]string, len(out))
		for i, c := range out {
			ids[i] = c.AssetID
		}
		if aths, aErr := s.coins.GetCoinsATHBatch(r.Context(), ids); aErr != nil {
			s.logger.Warn("coins list: ath batch failed", "err", aErr)
		} else {
			for i, c := range out {
				if a, ok := aths[c.AssetID]; ok {
					out[i].ATH = &CoinATH{USD: a.USD, At: a.At}
				}
			}
		}
	}

	writeJSON(w, CoinsPage{
		Coins:      out,
		NextCursor: nextCursor,
		Limit:      limit,
	}, Flags{})
}

// handleCoin serves GET /v1/coins/{slug}.
//
// Returns one coin row by slug. The slug is the URL-safe
// identifier from `classic_assets.slug` (or `classic_assets.code`
// when slug is null). Used by the explorer asset-detail page
// (/assets/{slug}) so a deep link doesn't require scanning the
// top-N listing first.
//
// Returns 404 when the slug doesn't match any classic asset.
func (s *Server) handleCoin(w http.ResponseWriter, r *http.Request) { //nolint:gocognit,gocyclo // dispatch fans across optional reader calls + 4-shape input parsing (XLM/native intercept, canonical asset_id, friendly slug, case-insensitive retry); collapsing would lose call-site context
	if s.coins == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-unavailable",
			"Coins lookup unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the coins reader yet.")
		return
	}

	slug := r.PathValue("slug")
	if slug == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-slug",
			"Invalid slug", http.StatusBadRequest,
			"slug path parameter is required")
		return
	}

	// Native XLM has no classic_assets row (that table only tracks
	// issued classic assets). Without this intercept the slug "XLM"
	// matches whichever issued token's code happens to be "XLM"
	// wins the disambiguation tiebreak, and "native" 404s outright.
	// The synthetic row is built from the same xlm_usd CTEs that
	// drive triangulated pricing for every other asset.
	//
	// Case-insensitive match: a user typo of `/v1/coins/xlm`
	// (lowercase) was previously routing to a real classic asset
	// in classic_assets with code='xlm' — a scam token issued by
	// `xlm-GBV7ORCO…`. The same security pattern that motivated
	// the XLM-vs-scam-token disambiguation in #45, applied
	// case-insensitively so `xlm`/`Xlm`/`XLm`/etc. all land on
	// native XLM. Confirmed on prod 2026-05-09: /v1/coins/xlm
	// was returning the GBV7ORCO scam asset until this fix.
	var (
		row timescale.CoinRow
		err error
	)
	if strings.EqualFold(slug, "XLM") || strings.EqualFold(slug, "native") {
		row, err = s.coins.GetNativeCoinRow(r.Context())
	} else {
		// Canonical asset_id form (CODE-ISSUER like
		// USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN)
		// is what every other API surface accepts as the wire form.
		// /v1/coins/{slug} historically only accepted the friendly
		// short form (USDC, AQUA, EURC) — anyone copying the
		// canonical asset_id from another endpoint's response into
		// /v1/coins/<id> got a 404. Probe the canonical form first
		// when the slug looks like one (contains a `-` followed by
		// a G-strkey); fall back to the friendly-slug path otherwise.
		// classic_assets.asset_id is the canonical-form column, so
		// look up by exact equality there.
		if a, parseErr := canonical.ParseAsset(slug); parseErr == nil &&
			a.Type == canonical.AssetClassic && a.Issuer != "" {
			row, err = s.coins.GetCoinByAssetID(r.Context(), a.String())
		} else {
			row, err = s.coins.GetCoinBySlug(r.Context(), slug)
			// Case-insensitive fallback: classic_assets.slug is uppercase
			// by convention (USDC, AQUA, EURC, etc.) but URL clients
			// frequently lowercase. Retry once with strings.ToUpper when
			// the literal slug missed AND the upper form differs —
			// preserves case-significance for the rare issued asset that
			// intentionally uses lowercase (Stellar protocol allows it)
			// while rescuing the common /v1/coins/usdc → /v1/coins/USDC
			// typo. Pre-fix the retry was missing and lowercase variants
			// 404'd.
			if errors.Is(err, sql.ErrNoRows) {
				if upper := strings.ToUpper(slug); upper != slug {
					row, err = s.coins.GetCoinBySlug(r.Context(), upper)
				}
			}
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coin-not-found",
			"Coin not found", http.StatusNotFound,
			"No classic asset matches that slug.")
		return
	}
	if err != nil {
		s.logger.Warn("coin by slug", "slug", slug, "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-error",
			"Coin lookup failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}

	out := coinFromRow(row)

	// Populate top_markets + price_history_24h — both soft-fail;
	// the asset detail card still renders without the previews if
	// either lookup errors. A failure here usually means the
	// prices_1m chunk the query needs is being column-store-
	// compressed.
	if topMarkets, mErr := s.coins.GetCoinTopMarkets(r.Context(), row.AssetID, 5); mErr != nil {
		s.logger.Warn("coin top markets", "asset_id", row.AssetID, "err", mErr)
	} else {
		out.TopMarkets = make([]CoinTopMarket, len(topMarkets))
		for i, m := range topMarkets {
			out.TopMarkets[i] = CoinTopMarket{
				Counterparty:  m.Counterparty,
				Side:          m.Side,
				Volume24hUSD:  m.Volume24hUSD,
				TradeCount24h: m.TradeCount24h,
			}
		}
	}
	if history, hErr := s.coins.GetCoinPriceHistory24h(r.Context(), row.AssetID); hErr != nil {
		s.logger.Warn("coin price history 24h", "asset_id", row.AssetID, "err", hErr)
	} else {
		out.PriceHistory24h = make([]CoinPricePoint, len(history))
		for i, p := range history {
			out.PriceHistory24h[i] = CoinPricePoint{T: p.T, P: p.P}
		}
	}
	if history7d, hErr := s.coins.GetCoinPriceHistory7d(r.Context(), row.AssetID); hErr != nil {
		s.logger.Warn("coin price history 7d", "asset_id", row.AssetID, "err", hErr)
	} else {
		out.PriceHistory7d = make([]CoinPricePoint, len(history7d))
		for i, p := range history7d {
			out.PriceHistory7d[i] = CoinPricePoint{T: p.T, P: p.P}
		}
	}
	if n, cErr := s.coins.GetCoinMarketsCount(r.Context(), row.AssetID); cErr != nil {
		s.logger.Warn("coin markets count", "asset_id", row.AssetID, "err", cErr)
	} else {
		out.MarketsCount = &n
	}
	if ath, aErr := s.coins.GetCoinATH(r.Context(), row.AssetID); aErr != nil {
		s.logger.Warn("coin ath", "asset_id", row.AssetID, "err", aErr)
	} else if ath != nil {
		out.ATH = &CoinATH{USD: ath.USD, At: ath.At}
	}
	if tc, tErr := s.coins.GetCoinTradeCount24h(r.Context(), row.AssetID); tErr != nil {
		s.logger.Warn("coin trade count 24h", "asset_id", row.AssetID, "err", tErr)
	} else {
		out.TradeCount24h = &tc
	}

	writeJSON(w, out, Flags{})
}

// nextCoinCursor builds the opaque keyset cursor for the next
// page or returns "" when the just-emitted page is the last one.
// Pulled out of the handler so handleCoins stays under the
// gocognit threshold; the cursor format itself is unchanged.
func nextCoinCursor(rows []timescale.CoinRow, pageSize int, order timescale.CoinsOrder) string {
	if len(rows) != pageSize || len(rows) == 0 {
		return ""
	}
	last := rows[len(rows)-1]
	if order == timescale.CoinsOrderVolume24hUSDDesc {
		vol := ""
		if last.Volume24hUSD != nil {
			vol = *last.Volume24hUSD
		}
		return vol + ":" + last.AssetID
	}
	return timescale.EncodeCoinCursor(last.ObservationCount, last.AssetID)
}

// coinFromRow projects a timescale.CoinRow onto the wire Coin
// struct. Pulled out of the two handlers so they stay under the
// funlen threshold and any future field is added in one spot.
func coinFromRow(row timescale.CoinRow) Coin {
	return Coin{
		Slug:              row.Slug,
		AssetID:           row.AssetID,
		Code:              row.Code,
		Issuer:            row.IssuerGStrkey,
		FirstSeenLedger:   row.FirstSeenLedger,
		LastSeenLedger:    row.LastSeenLedger,
		ObservationCount:  row.ObservationCount,
		PriceUSD:          row.PriceUSD,
		Volume24hUSD:      row.Volume24hUSD,
		MarketCapUSD:      row.MarketCapUSD,
		CirculatingSupply: row.CirculatingSupply,
		Change1hPct:       row.Change1hPct,
		Change24hPct:      row.Change24hPct,
		Change7dPct:       row.Change7dPct,
		IssuerScamReason:  scamReason(row.IssuerGStrkey),
	}
}

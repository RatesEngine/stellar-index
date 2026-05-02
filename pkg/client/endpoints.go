package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// PriceQuery selects the asset / quote pair for a [Client.Price]
// call. Asset is required; Quote defaults to "fiat:USD" server-side
// when empty.
type PriceQuery struct {
	Asset string
	Quote string // optional; server defaults to fiat:USD
}

// Price fetches the current closed-bucket VWAP for asset/quote.
// Cross-region consistent per ADR-0015.
func (c *Client) Price(ctx context.Context, q PriceQuery) (*Envelope[PriceSnapshot], error) {
	if q.Asset == "" {
		return nil, &APIError{Status: 400, Title: "asset required"}
	}
	v := url.Values{}
	v.Set("asset", q.Asset)
	if q.Quote != "" {
		v.Set("quote", q.Quote)
	}
	var env Envelope[PriceSnapshot]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/price", v, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// HistoryQuery selects the range for a [Client.HistorySinceInception]
// call. Asset is required.
type HistoryQuery struct {
	Asset       string
	Quote       string // optional
	Granularity string // 1m / 15m / 1h / 4h / 1d / 1w / 1mo; default 1d
}

// HistorySinceInception fetches the full historical series for an
// asset/quote at the chosen granularity. CAGG-served per PR #195.
// Long-running for fine-grained granularities; pass a context with
// an appropriate deadline.
func (c *Client) HistorySinceInception(ctx context.Context, q HistoryQuery) (*Envelope[HistorySeries], error) {
	if q.Asset == "" {
		return nil, &APIError{Status: 400, Title: "asset required"}
	}
	v := url.Values{}
	v.Set("asset", q.Asset)
	if q.Quote != "" {
		v.Set("quote", q.Quote)
	}
	if q.Granularity != "" {
		v.Set("granularity", q.Granularity)
	}
	var env Envelope[HistorySeries]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/history/since-inception", v, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// OHLCQuery is the input for [Client.OHLC]. Both Base and Quote
// are required (unlike [PriceQuery], which defaults Quote to
// fiat:USD — the OHLC endpoint accepts no implicit USD because
// candlestick charts pin a specific pair). From + To are
// optional; defaults match the server's `now-1h .. now` with the
// closed-bucket clamp applied to a defaulted To per ADR-0015.
type OHLCQuery struct {
	Base  string
	Quote string
	From  time.Time // optional
	To    time.Time // optional
}

// OHLC fetches a single open/high/low/close bar over the
// [From, To) window. Per the Freighter RFP §V1 historical chart
// requirements, this is the surface backing candlestick UIs.
//
// Window semantics:
//   - Both From + To zero: server defaults to now-1h .. now,
//     clamped to a closed-bucket boundary (every region answers
//     the same window per ADR-0015).
//   - From zero, To set: server uses To-1h .. To, no clamp
//     (caller pinned an explicit end).
//   - From set, To zero: server uses From .. now (clamped).
//   - Both set: server uses [From, To) verbatim; caller asserts
//     a specific historical range.
//
// Returns ErrNoTrades / 404 (translated to APIError 404) when no
// trades fell in the window.
//
// Truncation: when the window holds more trades than the server's
// cap (10000 today), the response's `Truncated` flag is true and
// High / Low may not be the actual extremes. Narrow the range to
// reach an untruncated bar.
func (c *Client) OHLC(ctx context.Context, q OHLCQuery) (*Envelope[OHLCBar], error) {
	if q.Base == "" {
		return nil, &APIError{Status: 400, Title: "base required"}
	}
	if q.Quote == "" {
		return nil, &APIError{Status: 400, Title: "quote required"}
	}
	v := url.Values{}
	v.Set("base", q.Base)
	v.Set("quote", q.Quote)
	if !q.From.IsZero() {
		v.Set("from", q.From.UTC().Format(time.RFC3339))
	}
	if !q.To.IsZero() {
		v.Set("to", q.To.UTC().Format(time.RFC3339))
	}
	var env Envelope[OHLCBar]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/ohlc", v, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// AssetsOptions paginates through the asset catalogue. Empty
// Cursor starts from the beginning; pass the previous response's
// Pagination.Next to walk forward.
type AssetsOptions struct {
	Cursor string
	Limit  int // 0 → server default (typically 100)
}

// Assets lists the asset catalogue, paginated.
func (c *Client) Assets(ctx context.Context, opts AssetsOptions) (*Envelope[[]AssetDetail], error) {
	v := url.Values{}
	if opts.Cursor != "" {
		v.Set("cursor", opts.Cursor)
	}
	if opts.Limit > 0 {
		v.Set("limit", strconv.Itoa(opts.Limit))
	}
	var env Envelope[[]AssetDetail]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/assets", v, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Asset fetches one asset's detail by its canonical asset_id.
func (c *Client) Asset(ctx context.Context, assetID string) (*Envelope[AssetDetail], error) {
	if assetID == "" {
		return nil, &APIError{Status: 400, Title: "asset_id required"}
	}
	var env Envelope[AssetDetail]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/assets/"+url.PathEscape(assetID), nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// AssetMetadata fetches the SEP-1 overlay (home-domain / stellar.toml
// resolution) for an asset.
func (c *Client) AssetMetadata(ctx context.Context, assetID string) (*Envelope[AssetMetadata], error) {
	if assetID == "" {
		return nil, &APIError{Status: 400, Title: "asset_id required"}
	}
	var env Envelope[AssetMetadata]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/assets/"+url.PathEscape(assetID)+"/metadata", nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Me returns the authenticated caller's account info. Requires an
// API key on the client (anonymous calls receive 401).
func (c *Client) Me(ctx context.Context) (*Envelope[Account], error) {
	var env Envelope[Account]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/account/me", nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Usage returns the authenticated caller's usage rollup. Currently
// returns an empty array — server-side usage tracking lands in
// follow-up PRs (the wire shape is locked already).
func (c *Client) Usage(ctx context.Context) (*Envelope[[]UsageRow], error) {
	var env Envelope[[]UsageRow]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/account/usage", nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// CreateKeyRequest is the body for [Client.CreateKey].
type CreateKeyRequest struct {
	Label string `json:"label"`
}

// CreateKey issues a new API key. The new key inherits the
// authenticated caller's identifier + tier; the plaintext secret
// appears in the response exactly once and is unrecoverable
// thereafter.
func (c *Client) CreateKey(ctx context.Context, req CreateKeyRequest) (*Envelope[KeyCreated], error) {
	if req.Label == "" {
		return nil, &APIError{Status: 400, Title: "label required"}
	}
	var env Envelope[KeyCreated]
	if err := c.doJSON(ctx, http.MethodPost, "/v1/account/keys", nil, req, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

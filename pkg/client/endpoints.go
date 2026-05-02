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

// HistoryRangeQuery is the input for [Client.History]. Both Base
// and Quote are required. From + To are optional (server defaults
// to `now-1h .. now`); Limit clamps to [1, 10000] server-side
// (default 1000). Cursor paginates — pass the previous response's
// `Pagination.Next` to walk forward.
//
// Note: distinct from [HistoryQuery] (which targets the
// since-inception bucketed series). This surface returns RAW
// trades within a window — useful for trade-level audits,
// regulatory exports, custom aggregations.
type HistoryRangeQuery struct {
	Base   string
	Quote  string
	From   time.Time // optional
	To     time.Time // optional
	Limit  int       // optional; server defaults to 1000, max 10000
	Cursor string    // optional; opaque from prior Pagination.Next
}

// History fetches raw trades for [Base, Quote] within the
// [From, To) window. Distinct from
// [Client.HistorySinceInception] which returns bucketed VWAP/TWAP
// points; this surface returns the underlying trades themselves
// — same data the aggregator consumes.
//
// Use cases: trade-level audits, regulatory exports, custom
// aggregations the server doesn't pre-compute. Pagination via
// `Cursor` (opaque base64); the walker collects pages by
// re-issuing with `Cursor: prev.Pagination.Next` until the
// returned cursor is empty.
//
// `flags.stale` doesn't apply here (this surface returns raw
// stored trades, not a VWAP). Other envelope flags propagate
// per-trade where meaningful.
func (c *Client) History(ctx context.Context, q HistoryRangeQuery) (*Envelope[[]TradeRow], error) {
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
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Cursor != "" {
		v.Set("cursor", q.Cursor)
	}
	var env Envelope[[]TradeRow]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/history", v, nil, &env); err != nil {
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

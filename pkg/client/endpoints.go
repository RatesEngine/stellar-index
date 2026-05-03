package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// PriceBatchQuery is the input for [Client.PriceBatch]. AssetIDs
// is required and non-empty; Quote defaults to "fiat:USD"
// server-side when empty.
//
// Server-side limits:
//   - 1..100 ids → routed via GET (`?asset_ids=…`).
//   - 101..1000 ids → routed via POST with a JSON body.
//   - >1000 ids → server returns 400; the SDK splits into ≤ 1000
//     chunks would belong on the caller, not here, because the
//     batch envelope's `flags.stale` semantic is OR-over-the-batch
//     and silently chunking would mask staleness in unrelated
//     subsets.
//
// Per the Freighter RFP §"Bulk query support preferred" — batch
// is the recommended path for portfolio + multi-asset views.
type PriceBatchQuery struct {
	AssetIDs []string
	Quote    string // optional; server defaults to fiat:USD
}

// PriceBatch fetches the current closed-bucket VWAP for many
// assets in a single round-trip. Cross-region consistent per
// ADR-0015 — every returned snapshot is from the same closed
// bucket window the single-asset `/v1/price` would have served.
//
// Routing:
//   - len(AssetIDs) ≤ 100 → GET /v1/price/batch?asset_ids=...
//   - len(AssetIDs) > 100 → POST /v1/price/batch with JSON body
//
// Missing observations (asset has no indexed data) are silently
// omitted from the response array — the envelope's `Data` slice
// can be shorter than `AssetIDs`. Callers that need to detect
// "asset X had no observation" diff the input + output.
//
// `flags.stale` on the envelope is the OR over per-row staleness:
// any stale row sets the envelope flag.
func (c *Client) PriceBatch(ctx context.Context, q PriceBatchQuery) (*Envelope[[]PriceSnapshot], error) {
	if len(q.AssetIDs) == 0 {
		return nil, &APIError{Status: 400, Title: "asset_ids required"}
	}
	if len(q.AssetIDs) > priceBatchPOSTMax {
		return nil, &APIError{
			Status: 400,
			Title:  "too many asset_ids",
			Detail: "the server caps POST /v1/price/batch at " + strconv.Itoa(priceBatchPOSTMax) + " ids",
		}
	}

	var env Envelope[[]PriceSnapshot]
	if len(q.AssetIDs) <= priceBatchGETMax {
		v := url.Values{}
		v.Set("asset_ids", strings.Join(q.AssetIDs, ","))
		if q.Quote != "" {
			v.Set("quote", q.Quote)
		}
		if err := c.doJSON(ctx, http.MethodGet, "/v1/price/batch", v, nil, &env); err != nil {
			return nil, err
		}
		return &env, nil
	}

	// >100 ids → POST. Body shape mirrors the server's POST handler.
	body := struct {
		AssetIDs []string `json:"asset_ids"`
		Quote    string   `json:"quote,omitempty"`
	}{
		AssetIDs: q.AssetIDs,
		Quote:    q.Quote,
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v1/price/batch", nil, body, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// priceBatchGETMax / priceBatchPOSTMax mirror the server's
// `priceBatchMaxAssets` / `priceBatchMaxAssetsPOST` constants
// in `internal/api/v1/price.go`. Duplicating them here is
// deliberate — the SDK ships SemVer-stable per ADR-0005, so
// importing the unexported server-side constants would couple
// SDK consumers to internal/.
const (
	priceBatchGETMax  = 100
	priceBatchPOSTMax = 1000
)

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

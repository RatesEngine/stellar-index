package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

// PriceTipQuery is the input for [Client.PriceTip]. Asset is
// required; Quote defaults to "fiat:USD" server-side. WindowSeconds
// is the rolling-window size; the server clamps to [1, 60] and
// defaults to 5 when zero.
type PriceTipQuery struct {
	Asset         string
	Quote         string // optional; server defaults to fiat:USD
	WindowSeconds int    // optional; server clamps to [1, 60], default 5
}

// PriceTip fetches the live "rolling-window" price per ADR-0018.
// Two in-contract branches the caller distinguishes via
// `PriceSnapshot.PriceType`:
//
//   - "vwap" with `WindowSeconds=N` — at least one trade in the
//     last N seconds; rolling-window VWAP.
//   - "last_trade" — window was empty; the most recent observation
//     as-is. Caller reads `ObservedAt` to decide if it's fresh
//     enough for their use case.
//
// Unlike `/v1/price` (closed-bucket, ADR-0015), the tip surface has
// no cross-region consistency contract — two clients in different
// regions may see different rolling-window VWAPs depending on which
// trades have replicated. Use Price for "every consumer sees the
// same number"; use PriceTip for "freshest possible signal."
//
// `flags.stale` on the envelope is ALWAYS false here per ADR-0018:
// both branches are in-contract on this surface. `flags.frozen`
// also stays unset (freeze is a closed-bucket concept).
// `flags.divergence_warning` and `flags.single_source` apply.
func (c *Client) PriceTip(ctx context.Context, q PriceTipQuery) (*Envelope[PriceSnapshot], error) {
	if q.Asset == "" {
		return nil, &APIError{Status: 400, Title: "asset required"}
	}
	v := url.Values{}
	v.Set("asset", q.Asset)
	if q.Quote != "" {
		v.Set("quote", q.Quote)
	}
	if q.WindowSeconds > 0 {
		v.Set("window_seconds", strconv.Itoa(q.WindowSeconds))
	}
	var env Envelope[PriceSnapshot]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/price/tip", v, nil, &env); err != nil {
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

// SourcesOptions filters [Client.Sources]. Class is one of the
// canonical class strings ("exchange" / "aggregator" / "oracle"
// / "authority_sanity"); empty returns the full registry.
type SourcesOptions struct {
	Class string // optional; empty = all classes
}

// Sources lists the source registry — venues + oracles +
// aggregators the deployment can ingest from. Read-only and
// catalogue-shaped (no pagination needed; ~25 entries today).
//
// Operators use the registry to drive admin tooling
// (`ratesengine-ops verify-decoders`, the source-class lint in
// CI). Customers use it to render "where do these prices come
// from?" UIs.
func (c *Client) Sources(ctx context.Context, opts SourcesOptions) (*Envelope[[]Source], error) {
	v := url.Values{}
	if opts.Class != "" {
		v.Set("class", opts.Class)
	}
	var env Envelope[[]Source]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/sources", v, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// MarketsOrderBy controls server-side sort + cursor scheme.
type MarketsOrderBy string

const (
	// MarketsOrderByPair sorts by `<base>|<quote>` lex order
	// ascending. Stable for paginating the full set. Default.
	MarketsOrderByPair MarketsOrderBy = "pair"
	// MarketsOrderByVolume24hUSDDesc sorts by 24h USD volume
	// descending (NULLS LAST), with `<base>|<quote>` as the
	// tie-breaker. Surfaces high-activity pairs first.
	MarketsOrderByVolume24hUSDDesc MarketsOrderBy = "volume_24h_usd_desc"
)

// MarketsOptions paginates through the active-pair catalogue.
// Same Cursor + Limit semantics as [AssetsOptions].
type MarketsOptions struct {
	Cursor string
	Limit  int // 0 → server default (typically 100); max 500
	// OrderBy controls sort + cursor scheme. Empty → server default
	// (alphabetic by `<base>|<quote>`).
	OrderBy MarketsOrderBy
}

// Markets lists the (base, quote) pairs the deployment has
// observed at least one trade for, paginated. Each Market entry
// includes a 24h activity summary (last-trade timestamp + 24h
// trade count + 24h USD volume).
func (c *Client) Markets(ctx context.Context, opts MarketsOptions) (*Envelope[[]Market], error) {
	v := url.Values{}
	if opts.Cursor != "" {
		v.Set("cursor", opts.Cursor)
	}
	if opts.Limit > 0 {
		v.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.OrderBy != "" {
		v.Set("order_by", string(opts.OrderBy))
	}
	var env Envelope[[]Market]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/markets", v, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Pair returns the activity summary for one specific pair. Both
// `base` and `quote` are required. The response shape is a
// 0-or-1-element `[]Market` array — empty when the pair has no
// observed trades, single-element when it does. Pinning a 0-or-1
// shape (rather than 404 on empty) lets callers distinguish "no
// such pair" from "malformed request" without branching on
// status code.
func (c *Client) Pair(ctx context.Context, base, quote string) (*Envelope[[]Market], error) {
	if base == "" {
		return nil, &APIError{Status: 400, Title: "base required"}
	}
	if quote == "" {
		return nil, &APIError{Status: 400, Title: "quote required"}
	}
	v := url.Values{}
	v.Set("base", base)
	v.Set("quote", quote)
	var env Envelope[[]Market]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/pairs", v, nil, &env); err != nil {
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

// CoinsOptions paginates / filters the classic-asset directory.
// `Limit` is server-side clamped to [1, 500] (default 100).
// `Issuer`, when non-empty, restricts the listing to assets minted
// by that G-strkey. `Cursor` is the keyset cursor returned by a
// previous response's `next_cursor` field — empty for the first
// page; iterate while non-empty.
type CoinsOptions struct {
	Limit  int
	Issuer string
	Cursor string
	// Q is a case-insensitive substring filter against `code`,
	// `slug`, and `issuer_g_strkey`. Server-side limited to 64
	// chars. Empty means "no search filter."
	Q string
}

// Coins lists the registry-aware coin directory ranked by
// observation count desc, joined to the latest 5-minute stats
// + 1-minute VWAP. Each row carries optional price_usd,
// volume_24h_usd, market_cap_usd, and circulating_supply.
//
// Iterate paginated:
//
//	cursor := ""
//	for {
//	    page, err := c.Coins(ctx, CoinsOptions{Limit: 500, Cursor: cursor})
//	    if err != nil { return err }
//	    process(page.Data.Coins)
//	    if page.Data.NextCursor == "" { break }
//	    cursor = page.Data.NextCursor
//	}
func (c *Client) Coins(ctx context.Context, opts CoinsOptions) (*Envelope[CoinsPage], error) {
	v := url.Values{}
	if opts.Limit > 0 {
		v.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Issuer != "" {
		v.Set("issuer", opts.Issuer)
	}
	if opts.Cursor != "" {
		v.Set("cursor", opts.Cursor)
	}
	if opts.Q != "" {
		v.Set("q", opts.Q)
	}
	var env Envelope[CoinsPage]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/coins", v, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Coin returns a single asset row by slug. Same row shape as
// one element of [CoinsPage.Coins]. 404 when the slug doesn't
// match a known classic asset.
func (c *Client) Coin(ctx context.Context, slug string) (*Envelope[Coin], error) {
	var env Envelope[Coin]
	path := "/v1/coins/" + url.PathEscape(slug)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// IssuersOptions paginates the issuer directory. Same `Limit`
// semantics as [CoinsOptions].
type IssuersOptions struct {
	Limit int
}

// Issuers lists every G-account that has minted at least one
// classic asset, ranked by total observation count across the
// issuer's assets. `home_domain` populates as the SEP-1 fetcher
// worker resolves it.
func (c *Client) Issuers(ctx context.Context, opts IssuersOptions) (*Envelope[[]IssuerListEntry], error) {
	v := url.Values{}
	if opts.Limit > 0 {
		v.Set("limit", strconv.Itoa(opts.Limit))
	}
	var env Envelope[[]IssuerListEntry]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/issuers", v, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Issuer returns the per-issuer detail (auth flags, SEP-1
// metadata, embedded assets list). Returns 404 (translated to
// [APIError]) when the G-strkey hasn't been observed as an
// issuer.
func (c *Client) Issuer(ctx context.Context, gStrkey string) (*Envelope[Issuer], error) {
	if gStrkey == "" {
		return nil, &APIError{Status: 400, Title: "g_strkey required"}
	}
	var env Envelope[Issuer]
	path := "/v1/issuers/" + url.PathEscape(gStrkey)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Cursors returns the per-source ingest cursor table from
// `/v1/diagnostics/cursors`. Operator-facing diagnostic — every
// (source, sub_source) tuple the dispatcher persists, with
// `lag_seconds` precomputed server-side so callers don't need a
// clock-sync agreement.
func (c *Client) Cursors(ctx context.Context) (*Envelope[[]Cursor], error) {
	var env Envelope[[]Cursor]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/diagnostics/cursors", nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Keys lists every API key whose identifier matches the
// authenticated caller's. Sorted by CreatedAt ascending — the
// caller's original signup key first, rotated keys later.
//
// Anonymous calls receive 401. The plaintext is NEVER returned —
// it's only available at create time, by design. To rotate, call
// [Client.CreateKey] for a new key and revoke the old one out-of-band.
func (c *Client) Keys(ctx context.Context) (*Envelope[[]Account], error) {
	var env Envelope[[]Account]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/account/keys", nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Status returns the comprehensive customer-facing health rollup
// from `/v1/status` — per-service heartbeats, p50/p95/p99 latency,
// ingest freshness, and Alertmanager incident counts. Anonymous-
// friendly. Always returns 200; degraded state is reported via the
// body's [Status.Overall] field rather than an HTTP error so
// monitoring dashboards can poll a single endpoint.
//
// Deployments without a Prometheus backend wired return only the
// in-process surface (region label + uptime); [Envelope.Flags.Stale]
// is true in that case.
func (c *Client) Status(ctx context.Context) (*Envelope[Status], error) {
	var env Envelope[Status]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/status", nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Healthz is the shallow liveness probe — returns 200 as long as
// the API process is up. Doesn't touch dependencies. Use for
// load-balancer health checks; use [Client.Readyz] when you need
// to verify the dependency stack is responsive.
func (c *Client) Healthz(ctx context.Context) (*Envelope[Health], error) {
	var env Envelope[Health]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/healthz", nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Readyz is the deep readiness probe. Returns 200 only if every
// registered dependency check (Postgres, Redis, etc.) is responsive
// within a 2 s server-side budget. Returns 503 + the same envelope
// shape with [Envelope.Flags.Stale] = true otherwise; the wrapped
// error in that case is an [APIError] with Status=503.
func (c *Client) Readyz(ctx context.Context) (*Envelope[Health], error) {
	var env Envelope[Health]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/readyz", nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// Version reports the build metadata of the API binary serving
// the request — git SHA, build date, Go runtime version. Useful
// for fleet-wide "what's running" checks over the API rather than
// SSH-ing into every host.
func (c *Client) Version(ctx context.Context) (*Envelope[Version], error) {
	var env Envelope[Version]
	if err := c.doJSON(ctx, http.MethodGet, "/v1/version", nil, nil, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

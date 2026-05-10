package v1

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// DexSourceNames returns every source registered with
// Class=Exchange + Subclass=DEX, sorted for stable order. Cached
// implicitly because external.Registry is a package-level
// constant — no need to memoise.
//
// Exported so the prewarm goroutine in cmd/ratesengine-api can
// compute the SAME slice the handler does. Without that, the
// unfiltered /v1/pools prewarm builds `PoolsFilter{Sources: nil}`
// → cache key `[]`, while the unfiltered handler builds
// `PoolsFilter{Sources: DexSourceNames()}` → cache key
// `[aquarius comet phoenix sdex soroswap]`. Different keys, so
// the warmed entry never matches a user request.
func DexSourceNames() []string {
	out := make([]string, 0, len(external.Registry))
	for name, md := range external.Registry {
		if md.Class == external.ClassExchange && md.Subclass == external.SubclassDEX {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// CexSourceNames returns every source registered with
// Class=Exchange + Subclass=CEX, sorted for stable order. Same
// shape and rationale as DexSourceNames — exported so the prewarm
// goroutine in cmd/ratesengine-api can iterate the registered CEXes
// to warm `/v1/markets?source=<name>` cache slots.
func CexSourceNames() []string {
	out := make([]string, 0, len(external.Registry))
	for name, md := range external.Registry {
		if md.Class == external.ClassExchange && md.Subclass == external.SubclassCEX {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// MarketsReader is the storage-side interface for /v1/markets
// and /v1/pairs lookups. Implementations: *timescale.Store
// (DistinctPairsExt + PairMarket), in-memory stubs for tests.
type MarketsReader interface {
	// DistinctPairsExt returns one page of (base, quote) pairs
	// present in the trades store under the requested ordering.
	// Cursor opaque; empty starts at page 1.
	DistinctPairsExt(ctx context.Context, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error)

	// SourceMarkets is DistinctPairsExt narrowed to a single
	// source — the per-DEX pool list backing
	// /v1/markets?source=<name>. Same shape as DistinctPairsExt
	// for paginated drill-down.
	SourceMarkets(ctx context.Context, source, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error)

	// AssetMarkets is DistinctPairsExt narrowed to pairs where the
	// given asset_id appears on either side (base OR quote). Backs
	// /v1/markets?asset=<asset_id> — the explorer's
	// /assets/{slug} Markets tab uses it to surface every market
	// the asset participates in without paying for a global scan +
	// client-side filter.
	AssetMarkets(ctx context.Context, asset, cursor string, limit int, order timescale.MarketsOrder) ([]Market, string, error)

	// AllPools returns every (source, base, quote) tuple — same
	// pair on two venues becomes two rows. When `sources` is
	// non-empty, restricts the result to rows whose source name
	// appears in the slice. Backs /v1/pools (DEX-only) where the
	// handler resolves the DEX subset of the source registry.
	AllPools(ctx context.Context, filter timescale.PoolsFilter, cursor string, limit int, order timescale.MarketsOrder) ([]Pool, string, error)

	// PairMarket returns the activity summary for a single (base,
	// quote) pair. The bool is false when the pair has no trades —
	// the /v1/pairs handler translates that to an empty 200 OK array,
	// not a 404, so the wire shape stays consistent with the
	// PairsEnvelope contract.
	PairMarket(ctx context.Context, base, quote canonical.Asset) (Market, bool, error)

	// GetPairsVolumeHistory24hBatch returns per-pair hourly USD-volume
	// buckets for the trailing 24h. Backs the /v1/markets sparkline
	// column when the caller passes ?include=sparkline.
	GetPairsVolumeHistory24hBatch(ctx context.Context, pairs [][2]string) (map[string][]timescale.PairVolumePoint, error)
}

// Pool is the wire shape for /v1/pools entries. Same fields as
// Market but with a `source` dimension so the same physical pair
// traded on two DEXes shows as two rows.
type Pool struct {
	Source        string    `json:"source"`
	Base          string    `json:"base"`
	Quote         string    `json:"quote"`
	LastTradeAt   time.Time `json:"last_trade_at"`
	TradeCount24h int64     `json:"trade_count_24h"`
	Volume24hUSD  *string   `json:"volume_24h_usd,omitempty"`
	// LastPrice is the most recent quote-per-base price observed
	// for THIS pool — same wire shape as Market.LastPrice but
	// per-source, so two venues trading the same pair surface
	// independent prices.
	LastPrice *string `json:"last_price,omitempty"`
}

// handlePools serves GET /v1/pools — DEX/AMM liquidity pools only.
// One row per (source, base, quote) where source is a DEX
// (Subclass=DEX in the source registry: soroswap, phoenix,
// aquarius, sdex, comet). CEX pairs go through /v1/markets;
// "pool" is AMM/DEX terminology and applying it to centralised
// venues misnames the data.
//
// Query params:
//   - cursor   (optional): opaque, from a prior pagination.next.
//   - limit    (optional): integer 1-500, default 100.
//   - order_by (optional): "volume_24h_usd_desc" (default) or "pair".
//   - source   (optional): single DEX name. Restricts the result to
//     that one DEX's pools. Unknown / non-DEX
//     source names return an empty list.
//   - base     (optional): canonical asset_id. Restricts to pools
//     where this asset is on the base side. AND-combined with
//     `quote` if both are passed (single-pair lookup).
//   - quote    (optional): canonical asset_id. Same as `base` but
//     on the quote side.
//   - asset    (optional): canonical asset_id. Restricts to pools
//     where this asset appears on either side (base OR quote).
//     Mutually exclusive with `base`/`quote` — combining the two
//     filter shapes (AND vs OR) has no well-defined semantics.
//     Backs the explorer's /assets/{slug} Liquidity tab.
func (s *Server) handlePools(w http.ResponseWriter, r *http.Request) { //nolint:gocognit,gocyclo,funlen // option parsing + DEX-source filter + asset/base+quote validation + 8s-timeout guard are linear; splitting fragments the request lifecycle
	cursor := r.URL.Query().Get("cursor")
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be an integer in [1, 500]")
			return
		}
		limit = parsed
	}
	var order timescale.MarketsOrder
	switch r.URL.Query().Get("order_by") {
	case "", "volume_24h_usd_desc":
		order = timescale.MarketsOrderVolume24hDesc
	case "pair":
		order = timescale.MarketsOrderPair
	default:
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-order",
			"Invalid order_by", http.StatusBadRequest,
			"order_by must be 'pair' or 'volume_24h_usd_desc'")
		return
	}

	if err := timescale.ValidateMarketsCursor(cursor, order); err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-cursor",
			"Invalid cursor", http.StatusBadRequest,
			"cursor: "+err.Error()+". Pass back the pagination.next value from a prior /v1/pools response, or omit the parameter to start at page 1.")
		return
	}

	// Resolve DEX source list from the registry. Hard-coded to
	// Subclass=DEX so the endpoint is unambiguously "pools" — no
	// CEX rows ever.
	dexSources := DexSourceNames()
	if reqSource := r.URL.Query().Get("source"); reqSource != "" {
		// Filter to the requested DEX. Non-DEX names get rejected
		// here (empty intersection → empty result list, not a 400)
		// so callers can pass through user input without separately
		// validating against the registry.
		filtered := make([]string, 0, 1)
		for _, s := range dexSources {
			if s == reqSource {
				filtered = append(filtered, s)
				break
			}
		}
		dexSources = filtered
	}

	reader := s.markets
	if reader == nil {
		writeJSON(w, []Pool{}, Flags{})
		return
	}
	if len(dexSources) == 0 {
		// Either the registry has no DEX sources (impossible) or
		// the source= filter didn't match a DEX. Return an empty
		// list rather than scan the trades hypertable for nothing.
		writeJSON(w, []Pool{}, Flags{})
		return
	}
	baseFilter := r.URL.Query().Get("base")
	quoteFilter := r.URL.Query().Get("quote")
	assetFilter := r.URL.Query().Get("asset")
	if assetFilter != "" {
		if _, err := canonical.ParseAsset(assetFilter); err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-asset-id",
				"Invalid asset", http.StatusBadRequest,
				"asset must be a canonical asset_id (e.g. 'native', 'USDC-G…', 'fiat:USD'); got "+assetFilter+" ("+err.Error()+")")
			return
		}
		if baseFilter != "" || quoteFilter != "" {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/conflicting-filters",
				"Conflicting filters", http.StatusBadRequest,
				"asset (base OR quote) cannot be combined with base or quote (AND-shape) on /v1/pools; pick one filter shape.")
			return
		}
	}
	filter := timescale.PoolsFilter{
		Sources: dexSources,
		Base:    baseFilter,
		Quote:   quoteFilter,
		Asset:   assetFilter,
	}
	// Hard 8s ceiling — the AllPools query scans the trades
	// hypertable's 24h window and can take 10s+ on a cold-cache
	// path even with the cache wrapper above (the prewarm covers
	// limit=25 + no filter; long-tail variants warm on first hit).
	// Without this ceiling the user sees a hung request that
	// eventually times out at the ingress; with it, they get a
	// fast 503 they can retry against a now-warm cache.
	pCtx, pCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer pCancel()
	rows, next, err := reader.AllPools(pCtx, filter, cursor, limit, order)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if handlerTimedOut(pCtx, err) {
			s.logger.Warn("AllPools deadline exceeded", "limit", limit, "filter", filter)
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/pools-timeout",
				"Pools query timed out", http.StatusServiceUnavailable,
				"the underlying trades-hypertable scan didn't return in 8s; cache may still be warming. Retry in a few seconds.")
			return
		}
		s.logger.Error("AllPools failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	if rows == nil {
		rows = []Pool{}
	}
	env := Envelope{Data: rows, Flags: Flags{}}
	if next != "" {
		env.Pagination = &Pagination{Next: next}
	}
	writeEnvelope(w, env)
}

// Market is the wire shape for /v1/markets entries.
//
// TradeCount24h may be zero even when LastTradeAt is recent — they
// measure different windows (activity vs most-recent event). The
// fields are designed to let clients sort markets by "current"
// activity vs total history.
//
// Volume24hUSD is the trailing-24h USD volume summed from
// prices_1m's per-bucket volume_usd. Pointer + omitempty so a
// pair with no USD-equivalent trades emits null instead of "0"
// — important for client-side sorting (treat null as "unknown",
// 0 as "definitely zero").
type Market struct {
	Base          string    `json:"base"`
	Quote         string    `json:"quote"`
	LastTradeAt   time.Time `json:"last_trade_at"`
	TradeCount24h int64     `json:"trade_count_24h"`
	Volume24hUSD  *string   `json:"volume_24h_usd,omitempty"`
	// LastPrice is the most recent quote-per-base price observed
	// for this pair (cross-source) within the trailing 24h. Null
	// when no recent prices_1m bucket has a non-null last_price.
	LastPrice *string `json:"last_price,omitempty"`
	// VolumeHistory24h — per-hour USD-volume buckets for the
	// trailing 24h. Populated only when the request sets
	// `?include=sparkline`. 24 entries oldest → newest, zero-
	// filled server-side so the wire array length is stable.
	VolumeHistory24h []MarketVolumeBucket `json:"volume_history_24h,omitempty"`
}

// MarketVolumeBucket — one hourly USD-volume datapoint for the
// /v1/markets sparkline. Hour is RFC 3339; volume_usd is
// numeric-stringified for precision parity.
type MarketVolumeBucket struct {
	Hour      time.Time `json:"hour"`
	VolumeUSD string    `json:"volume_usd"`
}

// handleMarkets serves GET /v1/markets.
//
// Query params:
//   - cursor   (optional): opaque, from a prior response's pagination.next.
//   - limit    (optional): integer 1-500, default 100.
//   - order_by (optional): "pair" (default) or "volume_24h_usd_desc".
//     The latter surfaces high-USD-volume pairs first so clients
//     don't paginate alphabetically through ~5K dust pairs to find
//     the ones with real activity.
//   - source   (optional): single source name (DEX or CEX). Restricts
//     the result to pairs that source observed in the recency window.
//   - asset    (optional): canonical asset_id. Restricts the result
//     to pairs where the asset appears on either side (base OR
//     quote). Mutually exclusive with `source` — combine the two
//     in the client if you need both. Backs the explorer's
//     /assets/{slug} Markets tab.
func (s *Server) handleMarkets(w http.ResponseWriter, r *http.Request) { //nolint:gocognit,gocyclo,funlen // option parsing + source/asset/no-filter dispatch + 8s-timeout guard + sparkline backfill are linear; splitting fragments the request lifecycle
	cursor := r.URL.Query().Get("cursor")
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be an integer in [1, 500]")
			return
		}
		limit = parsed
	}
	var order timescale.MarketsOrder
	switch r.URL.Query().Get("order_by") {
	case "":
		// Default switched from MarketsOrderPair to volume-desc on
		// 2026-05-10. The alphabetical default surfaced spam tokens
		// (`0-…`, `0TAX-…`, `0x1F3D4-…`) at the top of the listing
		// — useless for callers expecting a "what's interesting on
		// Stellar" view, and the explorer always passed
		// `?order_by=volume_24h_usd_desc` explicitly to work around
		// it. Now the implicit default matches what every consumer
		// actually wants. R-014 in `docs/review-2026-05-10.md`.
		// Callers wanting the alphabetical view pass `?order_by=pair`.
		order = timescale.MarketsOrderVolume24hDesc
	case "pair":
		order = timescale.MarketsOrderPair
	case "volume_24h_usd_desc":
		order = timescale.MarketsOrderVolume24hDesc
	default:
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-order",
			"Invalid order_by", http.StatusBadRequest,
			"order_by must be 'pair' or 'volume_24h_usd_desc'")
		return
	}

	if err := timescale.ValidateMarketsCursor(cursor, order); err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-cursor",
			"Invalid cursor", http.StatusBadRequest,
			"cursor: "+err.Error()+". Pass back the pagination.next value from a prior /v1/markets response, or omit the parameter to start at page 1.")
		return
	}

	source := r.URL.Query().Get("source")
	if source != "" {
		// Validate against the in-memory registry so an unknown
		// source name returns 400 instead of an empty page (the
		// silent-empty-page anti-pattern: a typo in `?source=`
		// looks identical on the wire to "this source has no
		// trades", which sends callers chasing nonexistent data).
		// Mirrors the same guard pattern on /v1/coins (PR #1134),
		// /v1/markets cursor (#1135), and /v1/pools.
		if _, ok := external.Registry[source]; !ok {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/unknown-source",
				"Unknown source", http.StatusBadRequest,
				"source must be a registered source name (see /v1/sources for the canonical list); got "+source)
			return
		}
	}

	asset := r.URL.Query().Get("asset")
	if asset != "" {
		// Validate canonical asset_id shape so an unparseable input
		// returns 400 with a useful diagnostic instead of an empty
		// 200 (the silent-empty-page anti-pattern that confuses
		// callers; same guard family as ?source= above).
		if _, err := canonical.ParseAsset(asset); err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-asset-id",
				"Invalid asset", http.StatusBadRequest,
				"asset must be a canonical asset_id (e.g. 'native', 'USDC-G…', 'fiat:USD'); got "+asset+" ("+err.Error()+")")
			return
		}
	}
	if source != "" && asset != "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/conflicting-filters",
			"Conflicting filters", http.StatusBadRequest,
			"source and asset cannot be combined on /v1/markets; pick one. To find a single source's markets involving a specific asset, fetch /v1/markets?source=<src> and filter client-side, or use /v1/pools?source=<src>&base=<asset>.")
		return
	}

	reader := s.markets
	if reader == nil {
		// Feature not wired — empty list is consistent with the
		// contract and doesn't force a 503. Mirrors the /v1/assets
		// degradation pattern.
		writeJSON(w, []Market{}, Flags{})
		return
	}

	var (
		rows []Market
		next string
		err  error
	)
	// Hard 8s ceiling — DistinctPairsExt + SourceMarkets scan the
	// trades hypertable's 24h window and can take 10s+ on a
	// cold-cache path even with the cache wrapper. Companion to
	// the same fix shipped on /v1/pools (#1082); without it the
	// user sees a hung request that eventually times out at the
	// ingress (observed 6.9s for /v1/markets?limit=5 on prod
	// 2026-05-08 because limit=5 missed the prewarm-25-only set).
	mCtx, mCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer mCancel()
	switch {
	case source != "":
		rows, next, err = reader.SourceMarkets(mCtx, source, cursor, limit, order)
	case asset != "":
		rows, next, err = reader.AssetMarkets(mCtx, asset, cursor, limit, order)
	default:
		rows, next, err = reader.DistinctPairsExt(mCtx, cursor, limit, order)
	}
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if handlerTimedOut(mCtx, err) {
			s.logger.Warn("DistinctPairs deadline exceeded",
				"limit", limit, "source", source)
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/markets-timeout",
				"Markets query timed out", http.StatusServiceUnavailable,
				"the underlying trades-hypertable scan didn't return in 8s; cache may still be warming. Retry in a few seconds.")
			return
		}
		s.logger.Error("DistinctPairs failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	// Defensive nil-to-empty: OpenAPI's MarketsEnvelope.data is
	// `type: array`, which means "data": null violates the schema.
	// Mirrors the handleAssetList guard.
	if rows == nil {
		rows = []Market{}
	}

	// Optional opt-in: attach 24h hourly volume history per row
	// for sparkline columns. Default off (avoids ~50KB per page
	// of bloat for SDK consumers that don't render charts).
	includeSparkline := false
	for _, f := range strings.Split(r.URL.Query().Get("include"), ",") {
		if strings.TrimSpace(f) == "sparkline" {
			includeSparkline = true
		}
	}
	if includeSparkline && len(rows) > 0 {
		pairs := make([][2]string, len(rows))
		for i, m := range rows {
			pairs[i] = [2]string{m.Base, m.Quote}
		}
		// Use the same 8s budget as the markets-list query — without
		// this, the sparkline batch ran on r.Context() unbounded and
		// could push total request latency to 10-15s after the markets
		// query had already burned 5-8s. Sharing mCtx caps the total
		// request at 8s; if markets ate most of it, sparkline gets
		// what's left and degrades gracefully (best-effort path: a
		// failure here logs at WARN and the response ships without
		// sparkline data).
		if hist, hErr := reader.GetPairsVolumeHistory24hBatch(mCtx, pairs); hErr != nil {
			s.logger.Warn("markets sparkline batch failed", "err", hErr)
		} else {
			for i, m := range rows {
				key := m.Base + "|" + m.Quote
				series := hist[key]
				if len(series) == 0 {
					continue
				}
				out := make([]MarketVolumeBucket, len(series))
				for j, p := range series {
					out[j] = MarketVolumeBucket{Hour: p.Hour, VolumeUSD: p.VolumeUSD}
				}
				rows[i].VolumeHistory24h = out
			}
		}
	}

	env := Envelope{
		Data:  rows,
		Flags: Flags{},
	}
	if next != "" {
		env.Pagination = &Pagination{Next: next}
	}
	writeEnvelope(w, env)
}

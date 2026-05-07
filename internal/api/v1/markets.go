package v1

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// dexSourceNames returns every source registered with
// Class=Exchange + Subclass=DEX, sorted for stable order. Cached
// implicitly because external.Registry is a package-level
// constant — no need to memoise.
func dexSourceNames() []string {
	out := make([]string, 0, len(external.Registry))
	for name, md := range external.Registry {
		if md.Class == external.ClassExchange && md.Subclass == external.SubclassDEX {
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

	// AllPools returns every (source, base, quote) tuple — same
	// pair on two venues becomes two rows. When `sources` is
	// non-empty, restricts the result to rows whose source name
	// appears in the slice. Backs /v1/pools (DEX-only) where the
	// handler resolves the DEX subset of the source registry.
	AllPools(ctx context.Context, sources []string, cursor string, limit int, order timescale.MarketsOrder) ([]Pool, string, error)

	// PairMarket returns the activity summary for a single (base,
	// quote) pair. The bool is false when the pair has no trades —
	// the /v1/pairs handler translates that to an empty 200 OK array,
	// not a 404, so the wire shape stays consistent with the
	// PairsEnvelope contract.
	PairMarket(ctx context.Context, base, quote canonical.Asset) (Market, bool, error)
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
func (s *Server) handlePools(w http.ResponseWriter, r *http.Request) {
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

	// Resolve DEX source list from the registry. Hard-coded to
	// Subclass=DEX so the endpoint is unambiguously "pools" — no
	// CEX rows ever.
	dexSources := dexSourceNames()

	reader := s.markets
	if reader == nil {
		writeJSON(w, []Pool{}, Flags{})
		return
	}
	rows, next, err := reader.AllPools(r.Context(), dexSources, cursor, limit, order)
	if err != nil {
		if clientAborted(r, err) {
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
func (s *Server) handleMarkets(w http.ResponseWriter, r *http.Request) {
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
	case "", "pair":
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

	source := r.URL.Query().Get("source")

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
	if source != "" {
		rows, next, err = reader.SourceMarkets(r.Context(), source, cursor, limit, order)
	} else {
		rows, next, err = reader.DistinctPairsExt(r.Context(), cursor, limit, order)
	}
	if err != nil {
		if clientAborted(r, err) {
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

	env := Envelope{
		Data:  rows,
		Flags: Flags{},
	}
	if next != "" {
		env.Pagination = &Pagination{Next: next}
	}
	writeEnvelope(w, env)
}

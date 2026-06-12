package v1

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// HistoryReader is the storage-side interface for /v1/history
// lookups.
type HistoryReader interface {
	// TradesInRange returns trades for pair whose close-time is in
	// [from, to). Ordered chronologically (ts ASC). Used by the
	// aggregation endpoints (/v1/vwap, /v1/twap, /v1/ohlc) which
	// consume the whole window at once.
	TradesInRange(ctx context.Context, pair canonical.Pair, from, to time.Time, limit int) ([]canonical.Trade, error)

	// TradesInRangeAfter is TradesInRange with a full-PK cursor.
	// Rows are filtered to (ts, ledger, tx_hash, op_index, source)
	// > (afterTs, afterLedger, afterTxHash, afterOpIndex, afterSource).
	// afterTs = zero time disables the cursor.
	//
	// Full PK as the cursor (not just ts+ledger) avoids dropping
	// rows when a page break falls mid-ledger — multiple trades
	// can share (ts, ledger) on high-volume ledgers.
	TradesInRangeAfter(
		ctx context.Context,
		pair canonical.Pair,
		from, to, afterTs time.Time,
		afterLedger uint32,
		afterTxHash, afterSource string,
		afterOpIndex uint32,
		limit int,
	) ([]canonical.Trade, error)

	// HistoryPoints returns every CLOSED bucket from the requested
	// granularity's CAGG (prices_1m / prices_15m / prices_1h / etc.)
	// for the pair, ordered chronologically. Used by /v1/history/
	// since-inception to serve the full historical series.
	//
	// granularity must be one of the canonical strings: "1m", "15m",
	// "1h", "4h", "1d", "1w", "1mo". The handler translates the
	// query-param string into this argument; unknown granularities
	// return [ErrUnknownGranularity].
	//
	// limit clamps the row count; 0 = unbounded. Empty slice + nil
	// error when the pair has no closed buckets yet.
	HistoryPoints(ctx context.Context, pair canonical.Pair, granularity string, limit int) ([]HistoryPoint, error)

	// HistoryPointsInRange is [HistoryPoints] with an explicit
	// [from, to) bucket bound. Same closed-bucket guard, same
	// granularity validation, same limit semantics. `from` zero
	// disables the lower bound; `to` zero disables the upper bound.
	//
	// Used by /v1/chart to serve a rolling-window series (timeframe
	// → from = now-tf, to = now). Per ADR-0020.
	HistoryPointsInRange(ctx context.Context, pair canonical.Pair, granularity string, from, to time.Time, limit int) ([]HistoryPoint, error)

	// OHLCSeries returns chronologically-ordered OHLC bars from the
	// CAGG matching the requested interval, in [from, to). Bucket is
	// the START of each window; window end = bucket + interval.
	// `interval` is one of: "1m", "5m", "15m", "30m", "1h", "4h",
	// "1d", "1w". Unknown intervals return [ErrUnknownGranularity].
	// `limit` clamps the row count (0 = unbounded). Empty slice +
	// nil error when no closed buckets exist in the window. The
	// storage-side impl re-buckets a finer-grain CAGG when the
	// interval doesn't have a native CAGG (5m / 30m / 4h).
	//
	// Used by /v1/ohlc's multi-bar mode (F-0071, CG/CMC parity).
	OHLCSeries(ctx context.Context, pair canonical.Pair, interval string, from, to time.Time, limit int) ([]OHLCSeriesBar, error)

	// LatestTradePerSource returns the most-recent trade FROM EACH
	// source that has ever recorded a trade on `pair`. Empty slice +
	// nil error when the pair has no trades at all.
	//
	// Optional sourceFilter ("" = no filter) restricts the result to
	// a single source — equivalent to "latest trade for the pair on
	// venue X", returning a 0- or 1-element slice. The filter is
	// applied at the SQL layer so a per-source query is cheap.
	//
	// This is the storage-side primitive for the ADR-0018 Surface 3
	// `/v1/observations` endpoint. The production impl is a
	// `DISTINCT ON (source) … WHERE base=$1 AND quote=$2
	// ORDER BY source, ts DESC` scan with **no time bound**, so
	// TimescaleDB cannot do chunk exclusion and probes every chunk
	// of the `trades` hypertable — multiple seconds on a busy
	// deployment even when the result is empty.
	//
	// NOTE: an earlier revision of this comment claimed a
	// `(base_asset, quote_asset, source, ts DESC)` index made this
	// O(num_sources). That index was never created (r1 has only
	// `(base_asset, quote_asset, ts DESC)`), so the claim was false.
	// Mitigation today: [CachedHistoryReader] SWR-caches this method
	// (#29). The real query-cheapening fix — create that composite
	// index (deferred: it is multi-GB on a 2.7B-row hypertable and
	// r1 is disk-constrained) — is the durable root-cause follow-up.
	LatestTradePerSource(ctx context.Context, pair canonical.Pair, sourceFilter string) ([]canonical.Trade, error)
}

// HistoryPoint is one (timestamp, price, optional usd-volume) row
// from a CAGG, returned by [HistoryReader.HistoryPoints]. The wire
// shape (`{t, p, v_usd?}`) is the OpenAPI HistoryPoint schema; the
// reader returns rich types and the handler does the marshalling.
type HistoryPoint struct {
	Bucket    time.Time
	VWAP      string  // NUMERIC text — pass-through, no float round-trip
	VolumeUSD *string // null when the bucket's underlying trades had no usd_volume
}

// ErrUnknownGranularity is what HistoryReader.HistoryPoints returns
// when the granularity arg isn't one of the seven canonical values.
// Handler translates to HTTP 400 problem+json.
var ErrUnknownGranularity = fmt.Errorf("unknown granularity")

// TradeRow is the wire shape for /v1/history entries.
//
// Numeric amounts ship as decimal strings (ADR-0003). Price is a
// pre-computed decimal for consumer convenience — the storage layer
// never persists a derived price, so we compute at response time.
type TradeRow struct {
	Source      string    `json:"source"`
	Ledger      uint32    `json:"ledger"`
	TxHash      string    `json:"tx_hash"`
	OpIndex     uint32    `json:"op_index"`
	Timestamp   time.Time `json:"ts"`
	BaseAsset   string    `json:"base_asset"`
	QuoteAsset  string    `json:"quote_asset"`
	BaseAmount  string    `json:"base_amount"`
	QuoteAmount string    `json:"quote_amount"`
	Price       string    `json:"price"` // quote/base as decimal
}

// tradeRowFrom converts canonical.Trade → wire shape. Price is
// computed at `decimals` fractional digits (default 10 — generous
// enough for sub-stroop precision without being absurd).
func tradeRowFrom(t canonical.Trade, decimals int) TradeRow {
	if decimals <= 0 {
		decimals = 10
	}
	return TradeRow{
		Source:      t.Source,
		Ledger:      t.Ledger,
		TxHash:      t.TxHash,
		OpIndex:     t.OpIndex,
		Timestamp:   t.Timestamp,
		BaseAsset:   t.Pair.Base.String(),
		QuoteAsset:  t.Pair.Quote.String(),
		BaseAmount:  t.BaseAmount.String(),
		QuoteAmount: t.QuoteAmount.String(),
		Price:       priceRatioDecimal(t, decimals),
	}
}

// ─── Handler ──────────────────────────────────────────────────────

// handleHistory serves GET /v1/history?base=<id>&quote=<id>&from=<rfc3339>&to=<rfc3339>&limit=<int>.
//
// Defaults:
//   - from: to - 1h (1-hour window rolling back from `to`)
//   - to:   now
//   - limit: 1000 (server clamps to ≤ 10000)
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) { //nolint:funlen // option parsing + 8s-timeout guard + range/limit defaults are linear; splitting fragments the request lifecycle
	reader := s.history
	if reader == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/history-unavailable",
			"History serving not configured", http.StatusServiceUnavailable,
			"this deployment has no HistoryReader wired — check binary configuration")
		return
	}

	base, quote, ok := parseBaseQuote(w, r)
	if !ok {
		return
	}
	pair, err := canonical.NewPair(base, quote)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-pair",
			"Invalid pair", http.StatusBadRequest,
			err.Error())
		return
	}

	from, to, ok := parseFromTo(w, r)
	if !ok {
		return
	}

	limit := 1000
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 10000 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be an integer in [1, 10000]")
			return
		}
		limit = parsed
	}

	// Optional cursor (opaque to clients; base64 of
	// "<ts>:<ledger>:<source>:<tx_hash>:<op_index>"). Shadows `from`
	// when present — otherwise paginating callers would re-request
	// duplicate rows on each page.
	var (
		afterTs      time.Time
		afterLedger  uint32
		afterSource  string
		afterTxHash  string
		afterOpIndex uint32
	)
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		c, err := decodeHistoryCursor(raw)
		if err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-cursor",
				"Invalid cursor", http.StatusBadRequest, err.Error())
			return
		}
		afterTs = c.ts
		afterLedger = c.ledger
		afterSource = c.source
		afterTxHash = c.txHash
		afterOpIndex = c.opIndex
	}

	// 8s ceiling on the trades hypertable range query. Same
	// pattern as #1082 / #1099 / #1100 / #1101 / #1102. Long
	// `from` windows (no `from` set, or month-spanning) can take
	// 5–10s on a cold cache scanning per-trade rows.
	hCtx, hCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer hCancel()
	trades, err := reader.TradesInRangeAfter(hCtx, pair,
		from, to, afterTs, afterLedger, afterTxHash, afterSource, afterOpIndex, limit)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if handlerTimedOut(hCtx, err) {
			s.logger.Warn("TradesInRangeAfter deadline exceeded",
				"base", base.String(), "quote", quote.String(),
				"from", from, "to", to, "limit", limit)
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/history-timeout",
				"History query timed out", http.StatusServiceUnavailable,
				"the underlying trades-hypertable scan didn't return in 8s. Try narrowing the from/to window or reducing the limit.")
			return
		}
		s.logger.Error("TradesInRangeAfter failed",
			"err", err,
			"base", base.String(), "quote", quote.String(),
			"from", from, "to", to)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	rows := make([]TradeRow, len(trades))
	for i, t := range trades {
		rows[i] = tradeRowFrom(t, 10)
	}

	// If the page is full, emit a next-cursor pointing at the last
	// row we returned. Clients just re-issue the same request with
	// ?cursor=<next> to drain subsequent pages. When len < limit, the
	// window is exhausted — no cursor, no next.
	env := Envelope{Data: rows, Flags: Flags{}}
	if len(trades) == limit {
		last := trades[len(trades)-1]
		env.Pagination = &Pagination{
			Next: encodeHistoryCursor(historyCursor{
				ts:      last.Timestamp,
				ledger:  last.Ledger,
				source:  last.Source,
				txHash:  last.TxHash,
				opIndex: last.OpIndex,
			}),
		}
	}
	writeEnvelope(w, env)
}

// historyCursor is the decoded cursor payload for /v1/history.
// Full PK as the key avoids mid-ledger page-break data loss.
type historyCursor struct {
	ts      time.Time
	ledger  uint32
	source  string
	txHash  string
	opIndex uint32
}

// encodeHistoryCursor / decodeHistoryCursor are the opaque
// over-the-wire form of a historyCursor. Base64 keeps the cursor
// URL-safe without needing client-side URL encoding.
//
// Format inside the base64:
// "<unix_nanos>:<ledger>:<source>:<tx_hash>:<op_index>"
// Timestamp is nanosecond-precision (future-proof against sub-
// second ledger close times). Source names are [a-z0-9_-] so no
// field-separator collision.
func encodeHistoryCursor(c historyCursor) string {
	raw := fmt.Sprintf("%d:%d:%s:%s:%d",
		c.ts.UnixNano(), c.ledger, c.source, c.txHash, c.opIndex)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeHistoryCursor(s string) (historyCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return historyCursor{}, fmt.Errorf("cursor base64: %w", err)
	}
	parts := strings.SplitN(string(raw), ":", 5)
	if len(parts) != 5 {
		return historyCursor{}, fmt.Errorf("cursor must be <ts_ns>:<ledger>:<source>:<tx_hash>:<op_index>")
	}
	tsNano, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return historyCursor{}, fmt.Errorf("cursor ts: %w", err)
	}
	ledger, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return historyCursor{}, fmt.Errorf("cursor ledger: %w", err)
	}
	source := parts[2]
	if source == "" {
		// An empty source would weaken the full-PK cursor comparison
		// into a partial one, reintroducing the same-ledger page-skip
		// bug the full-PK cursor was designed to fix. Reject rather
		// than silently serve wrong-looking pages.
		return historyCursor{}, fmt.Errorf("cursor source must not be empty")
	}
	txHash := parts[3]
	if !isLowerHex64(txHash) {
		return historyCursor{}, fmt.Errorf("cursor tx_hash must be 64 lowercase hex chars")
	}
	opIndex, err := strconv.ParseUint(parts[4], 10, 32)
	if err != nil {
		return historyCursor{}, fmt.Errorf("cursor op_index: %w", err)
	}
	return historyCursor{
		ts:      time.Unix(0, tsNano).UTC(),
		ledger:  uint32(ledger),
		source:  source,
		txHash:  txHash,
		opIndex: uint32(opIndex),
	}, nil
}

// isLowerHex64 returns true iff s is exactly 64 characters of
// lowercase hex. Same invariant canonical.validTxHash enforces on
// the ingest side; mirrored here (without importing canonical) so
// decodeHistoryCursor doesn't create a cycle.
func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// parseBaseQuote extracts + validates base/quote from the request.
// Returns (base, quote, true) on success; writes a problem response
// and returns ok=false on failure.
//
// `asset=` is accepted as an alias for `base=` (F-0061 closure) so
// clients copying URLs from /v1/price (which uses asset/quote) don't
// hit a 400 on their first try. Passing BOTH `base` and `asset` is
// a 400 — they're conflicting controls for the same value and the
// silent precedence pick was confusing.
func parseBaseQuote(w http.ResponseWriter, r *http.Request) (canonical.Asset, canonical.Asset, bool) {
	rawBase := r.URL.Query().Get("base")
	rawAsset := r.URL.Query().Get("asset")
	if rawBase != "" && rawAsset != "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-parameter",
			"`base` and `asset` are mutually exclusive", http.StatusBadRequest,
			"both query parameters refer to the same value — pick one (this endpoint's canonical form is `base=`; `asset=` is accepted as an alias for /v1/price compatibility)")
		return canonical.Asset{}, canonical.Asset{}, false
	}
	if rawBase == "" {
		rawBase = rawAsset
	}
	if rawBase == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-base",
			"Missing base parameter", http.StatusBadRequest,
			"base query parameter is required (or `asset=` as an alias for /v1/price compatibility)")
		return canonical.Asset{}, canonical.Asset{}, false
	}
	base, err := canonical.ParseAsset(rawBase)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid base identifier", http.StatusBadRequest,
			err.Error())
		return canonical.Asset{}, canonical.Asset{}, false
	}

	rawQuote := r.URL.Query().Get("quote")
	if rawQuote == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-quote",
			"Missing quote parameter", http.StatusBadRequest,
			"quote query parameter is required")
		return canonical.Asset{}, canonical.Asset{}, false
	}
	quote, err := canonical.ParseAsset(rawQuote)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-quote",
			"Invalid quote identifier", http.StatusBadRequest,
			err.Error())
		return canonical.Asset{}, canonical.Asset{}, false
	}
	return base, quote, true
}

// ─── /v1/history/since-inception ────────────────────────────────

// HistorySeries is the wire shape for /v1/history/since-inception.
// Mirrors the OpenAPI HistoryEnvelope.data shape exactly.
type HistorySeries struct {
	AssetID     string             `json:"asset_id"`
	Quote       string             `json:"quote"`
	PriceType   string             `json:"price_type"`  // "vwap" today; TWAP planned
	Granularity string             `json:"granularity"` // "1m" / "15m" / "1h" / etc.
	Points      []HistoryPointWire `json:"points"`
}

// HistoryPointWire is the JSON-tagged shape that marshals as the
// OpenAPI HistoryPoint ({t, p, v_usd?}). Distinct from the
// reader-side [HistoryPoint] which carries rich types — keeps the
// internal type usable by tests + adapters without leaking wire-
// shape assumptions.
type HistoryPointWire struct {
	T    time.Time `json:"t"`
	P    string    `json:"p"`
	VUSD *string   `json:"v_usd,omitempty"`
}

const (
	defaultHistoryGranularity = "1d"

	// historyMaxPoints is the safety cap on a single response. The
	// 1m CAGG can grow to ~32 M rows over 5 years (one per minute);
	// the 1d CAGG is ~1800 rows over 5 years. Cap at 50k so a
	// granularity=1m request doesn't try to ship a 32M-row JSON
	// payload. Operators wanting the full series in 1m grain should
	// paginate (planned cursor surface).
	historyMaxPoints = 50_000
)

// handleHistorySinceInception serves GET /v1/history/since-inception?
// asset=<id>&quote=<id>&granularity=<g>. Returns CLOSED buckets
// from the granularity's CAGG, oldest to newest, capped at
// historyMaxPoints.
//
// 503 when no HistoryReader is wired. 400 on bad asset/quote/
// granularity. 200 with empty points[] when the pair has no closed
// buckets yet — distinct from 404 since the asset itself may be
// known but just hasn't accrued bucketed history.
func (s *Server) handleHistorySinceInception(w http.ResponseWriter, r *http.Request) { //nolint:funlen // option parsing + 8s-timeout guard + grain-default + clamp logic are linear; splitting fragments the request lifecycle
	if s.history == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/history-unavailable",
			"History serving not configured", http.StatusServiceUnavailable,
			"this deployment has no HistoryReader wired — check binary configuration")
		return
	}

	rawAsset := r.URL.Query().Get("asset")
	if rawAsset == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset",
			"Missing asset parameter", http.StatusBadRequest,
			"asset query parameter is required")
		return
	}
	asset, err := canonical.ParseAsset(rawAsset)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid asset identifier", http.StatusBadRequest,
			err.Error())
		return
	}

	quote := defaultPriceQuote
	if rawQuote := r.URL.Query().Get("quote"); rawQuote != "" {
		q, err := canonical.ParseAsset(rawQuote)
		if err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-quote",
				"Invalid quote identifier", http.StatusBadRequest,
				err.Error())
			return
		}
		quote = q
	}

	if asset.Equal(quote) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/identity-pair",
			"Asset is the quote", http.StatusBadRequest,
			"asset and quote must differ")
		return
	}

	gran := defaultHistoryGranularity
	if raw := r.URL.Query().Get("granularity"); raw != "" {
		gran = raw
	}

	pair, err := canonical.NewPair(asset, quote)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-pair",
			"Invalid pair", http.StatusBadRequest,
			err.Error())
		return
	}

	hCtx, hCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer hCancel()
	points, err := s.history.HistoryPoints(hCtx, pair, gran, historyMaxPoints)
	if errors.Is(err, ErrUnknownGranularity) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-granularity",
			"Invalid granularity", http.StatusBadRequest,
			fmt.Sprintf("granularity must be one of: 1m, 15m, 1h, 4h, 1d, 1w, 1mo (got %q)", gran))
		return
	}
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if handlerTimedOut(hCtx, err) {
			s.logger.Warn("HistoryPoints deadline exceeded",
				"asset", asset.String(), "quote", quote.String(), "granularity", gran)
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/history-timeout",
				"History query timed out", http.StatusServiceUnavailable,
				"the underlying CAGG didn't return in 8s; cache may still be warming.")
			return
		}
		s.logger.Error("HistoryPoints failed",
			"err", err, "asset", asset.String(), "quote", quote.String(), "granularity", gran)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	// F-1225 (codex audit-2026-05-12): stablecoin → fiat:USD fallback.
	// The literal X/fiat:USD pair never has rows in the CAGGs on
	// Stellar mainnet because no on-chain trades quote in fiat:USD —
	// every USD-flavoured trade quotes in classic USDC (USDC-GA5Z…)
	// or one of the other operator-declared pegs. The chart + price
	// handlers already implement this fallback; without it, since-
	// inception XLM/USD returns empty while chart/price/VWAP all
	// surface data. Mirrors `chartStablecoinFallback` shape.
	triangulated := false
	if len(points) == 0 {
		if fp, ok := s.historySinceInceptionStablecoinFallback(hCtx, pair, gran); ok {
			points = fp
			// The series was served via the X/<peg> proxy under the
			// peg≈$1 assumption — that's a triangulated value, exactly
			// as the chart/price siblings flag it (G2-13). Pre-fix this
			// path wrote Flags{} and silently hid the proxy.
			triangulated = true
		}
	}

	wire := make([]HistoryPointWire, len(points))
	for i, p := range points {
		wire[i] = HistoryPointWire{T: p.Bucket, P: p.VWAP, VUSD: p.VolumeUSD}
	}

	writeJSON(w, HistorySeries{
		AssetID:     asset.String(),
		Quote:       quote.String(),
		PriceType:   "vwap",
		Granularity: gran,
		Points:      wire,
	}, Flags{Triangulated: triangulated})
}

// historySinceInceptionStablecoinFallback walks the operator's
// USD-pegged classic allow-list when the literal X/fiat:USD pair
// returned no points. Mirrors [chartStablecoinFallback] but uses
// the since-inception variant (no `from` lower bound). Returns
// ok=false when:
//
//   - quote is not fiat:USD,
//   - no operator-declared pegs are wired,
//   - every peg's CAGG read returns empty / errors out.
//
// F-1225 (codex audit-2026-05-12).
func (s *Server) historySinceInceptionStablecoinFallback(
	ctx context.Context, pair canonical.Pair, gran string,
) ([]HistoryPoint, bool) {
	if pair.Quote.Type != canonical.AssetFiat || pair.Quote.Code != "USD" {
		return nil, false
	}
	for _, peg := range s.usdPeggedClassics {
		if peg.Equal(pair.Base) {
			continue
		}
		proxied, err := canonical.NewPair(pair.Base, peg)
		if err != nil {
			continue
		}
		pp, err := s.history.HistoryPoints(ctx, proxied, gran, historyMaxPoints)
		if err != nil || len(pp) == 0 {
			continue
		}
		return pp, true
	}
	return nil, false
}

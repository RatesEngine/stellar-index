package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/lib/pq"

	"github.com/RatesEngine/rates-engine/internal/aggregate"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// usdVolumeScaleDenom is the divisor that converts a scaled-integer
// quote_amount from an off-chain source to its actual USD value.
// External sources (CEX/FX) stamp every amount at the uniform 10^8
// decimal convention per
// `internal/sources/external/<venue>::externalAmountDecimals`. We
// only compute usd_volume when the source is in {CEX, FX} (so the
// 10^8 scale holds) AND the quote is fiat:USD or a USD-pegged
// stablecoin (so peg ≈ $1). On-chain sources use per-asset decimals
// (XLM=7, Soroban variable) and need a per-source conversion that
// lives in a follow-up — see launch-readiness L2.2.
const usdVolumeScaleDenom = 100_000_000

// tradeUSDVolume returns the per-trade USD-equivalent volume as a
// NUMERIC-compatible string, or nil when the trade can't be
// converted cleanly. Returning a *string lets the caller pass the
// value (or sql NULL) straight into the trades.usd_volume column.
//
// Computable when ALL of the following hold:
//   - source is registered with Subclass = SubclassCEX or SubclassFX
//     (uniform 10^8 quote-amount scale per externalAmountDecimals)
//   - quote is fiat:USD OR a USD-pegged stablecoin per
//     `aggregate.FiatProxy` (USDC/USDT/DAI/PYUSD/USDP — all peg to
//     USD; we treat the peg as 1.0 at insert time, the divergence /
//     freeze paths surface depeg events separately)
//
// Everything else returns nil and the column stays NULL — the
// historical default behaviour is preserved for trades where the
// USD value isn't computable without an FX lookup.
func tradeUSDVolume(t canonical.Trade) *string {
	md := external.Lookup(t.Source)
	if md.Subclass != external.SubclassCEX && md.Subclass != external.SubclassFX {
		return nil
	}
	if !quoteIsUSDOrUSDPegged(t.Pair.Quote) {
		return nil
	}
	q := t.QuoteAmount.BigInt()
	if q == nil || q.Sign() <= 0 {
		return nil
	}
	// FloatString(8) gives a fixed-precision decimal — Postgres
	// NUMERIC accepts the form directly with no precision loss for
	// any value that fit in the original big.Int (NUMERIC is
	// arbitrary-precision; FloatString just chooses a render).
	rendered := new(big.Rat).SetFrac(q, big.NewInt(usdVolumeScaleDenom)).FloatString(8)
	return &rendered
}

// quoteIsUSDOrUSDPegged is true when the asset is fiat:USD or a
// stablecoin that aggregate.FiatProxy maps to USD. The peg is
// trusted at insert time — depeg events are observed separately
// via the divergence + anomaly paths and do NOT change the inserted
// usd_volume retroactively (a depegged USDT trade still carries
// its observed quote_amount, which is the right historical record).
func quoteIsUSDOrUSDPegged(a canonical.Asset) bool {
	if a.Type == canonical.AssetFiat && a.Code == "USD" {
		return true
	}
	proxy, ok := aggregate.FiatProxy(a)
	if !ok {
		return false
	}
	return proxy.Type == canonical.AssetFiat && proxy.Code == "USD"
}

// ErrNoFXQuote is returned by [Store.FXQuoteAtOrBefore] when no FX
// observation exists for the requested pair at-or-before the cutoff.
// Callers fall back to the cached VWAP path (degraded but functional)
// and surface the fallback via the AggregatorFXSnapFallbackTotal metric.
var ErrNoFXQuote = errors.New("timescale: no FX quote at or before cutoff")

// InsertTrade writes one trade. Returns nil for a successful insert
// OR a duplicate-key clash (idempotent by storage identity — the
// current conflict target is source+ledger+tx_hash+op_index+ts).
// Other errors propagate.
//
// The trade is validated via [canonical.Trade.Validate] before
// touching the DB; a Validate failure returns [canonical.ErrInvalidTrade].
//
// `usd_volume` is computed via [tradeUSDVolume] when the source is
// off-chain (CEX/FX) AND the quote is fiat:USD or a USD-pegged
// stablecoin; everything else stores NULL. See the helper's docstring
// for the L2.2 caveat surfaced in launch-readiness re-baseline 2026-05-01.
func (s *Store) InsertTrade(ctx context.Context, t canonical.Trade) error {
	if err := t.Validate(); err != nil {
		return err
	}

	const q = `
        INSERT INTO trades (
            source, ledger, tx_hash, op_index, ts,
            base_asset, quote_asset,
            base_amount, quote_amount, usd_volume,
            maker, taker
        ) VALUES (
            $1, $2, $3, $4, $5,
            $6, $7,
            $8, $9, $10,
            NULLIF($11, ''), NULLIF($12, '')
        )
        ON CONFLICT (source, ledger, tx_hash, op_index, ts) DO NOTHING
    `
	var usdVolume any // sql NULL when nil; pq accepts the *string form too
	if v := tradeUSDVolume(t); v != nil {
		usdVolume = *v
	}
	_, err := s.db.ExecContext(ctx, q,
		t.Source, t.Ledger, t.TxHash, t.OpIndex, t.Timestamp.UTC(),
		t.Pair.Base.String(), t.Pair.Quote.String(),
		t.BaseAmount, t.QuoteAmount, usdVolume,
		t.Maker, t.Taker,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertTrade: %w", err)
	}
	return nil
}

// LatestTradesForPair returns up to `limit` most-recent trades for
// the given ordered pair. Returns an empty slice + nil error if the
// pair has no trades.
func (s *Store) LatestTradesForPair(ctx context.Context, p canonical.Pair, limit int) ([]canonical.Trade, error) {
	if limit <= 0 {
		limit = 100
	}
	const q = `
        SELECT source, ledger, tx_hash, op_index, ts,
               base_asset, quote_asset,
               base_amount, quote_amount,
               COALESCE(maker, ''), COALESCE(taker, '')
          FROM trades
         WHERE base_asset  = $1
           AND quote_asset = $2
         ORDER BY ts DESC, ledger DESC
         LIMIT $3
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: LatestTradesForPair: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []canonical.Trade
	for rows.Next() {
		var t canonical.Trade
		var baseAsset, quoteAsset string
		if err := rows.Scan(
			&t.Source, &t.Ledger, &t.TxHash, &t.OpIndex, &t.Timestamp,
			&baseAsset, &quoteAsset,
			&t.BaseAmount, &t.QuoteAmount,
			&t.Maker, &t.Taker,
		); err != nil {
			return nil, fmt.Errorf("timescale: LatestTradesForPair scan: %w", err)
		}
		// Reconstruct Pair via the canonical parse path — this also
		// enforces shape invariants on read.
		base, err := canonical.ParseAsset(baseAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradesForPair base %q: %w", baseAsset, err)
		}
		quote, err := canonical.ParseAsset(quoteAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradesForPair quote %q: %w", quoteAsset, err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradesForPair pair: %w", err)
		}
		t.Pair = pair
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: LatestTradesForPair rows: %w", err)
	}
	return out, nil
}

// LatestTradePerSource returns the most-recent trade from each source
// that has ever traded `pair`. Empty slice + nil error when the pair
// has no trades.
//
// sourceFilter "" returns all sources; a non-empty value restricts to
// that single source (0- or 1-element slice). Filtering at the SQL
// layer means a single-source query is just an index point lookup.
//
// Implementation: DISTINCT ON (source) ordered by ts DESC, ledger DESC
// — cheap when covered by an index on (base_asset, quote_asset,
// source, ts DESC). The cost is ~O(num_sources) per pair rather than
// O(rows_in_pair).
func (s *Store) LatestTradePerSource(ctx context.Context, p canonical.Pair, sourceFilter string) ([]canonical.Trade, error) {
	const q = `
        SELECT DISTINCT ON (source)
               source, ledger, tx_hash, op_index, ts,
               base_asset, quote_asset,
               base_amount, quote_amount,
               COALESCE(maker, ''), COALESCE(taker, '')
          FROM trades
         WHERE base_asset  = $1
           AND quote_asset = $2
           AND ($3 = '' OR source = $3)
         ORDER BY source, ts DESC, ledger DESC
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(), sourceFilter,
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: LatestTradePerSource: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []canonical.Trade
	for rows.Next() {
		var t canonical.Trade
		var baseAsset, quoteAsset string
		if err := rows.Scan(
			&t.Source, &t.Ledger, &t.TxHash, &t.OpIndex, &t.Timestamp,
			&baseAsset, &quoteAsset,
			&t.BaseAmount, &t.QuoteAmount,
			&t.Maker, &t.Taker,
		); err != nil {
			return nil, fmt.Errorf("timescale: LatestTradePerSource scan: %w", err)
		}
		base, err := canonical.ParseAsset(baseAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradePerSource base %q: %w", baseAsset, err)
		}
		quote, err := canonical.ParseAsset(quoteAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradePerSource quote %q: %w", quoteAsset, err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			return nil, fmt.Errorf("timescale: LatestTradePerSource pair: %w", err)
		}
		t.Pair = pair
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: LatestTradePerSource rows: %w", err)
	}
	return out, nil
}

// TradesInRange returns trades for the given pair whose close-time
// falls in [from, to). Ordered by (ts ASC, ledger ASC) — chronological,
// which is what OHLC / VWAP callers want.
//
// limit clamps the returned count to avoid runaway queries; pass 0
// or negative for the default of 1000. The hard ceiling is 10000.
//
// An empty slice + nil error means the pair has no trades in the
// window — not an error. Callers distinguish "empty" from "error"
// by testing len(rows).
func (s *Store) TradesInRange(ctx context.Context, p canonical.Pair, from, to time.Time, limit int) ([]canonical.Trade, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}
	if to.Before(from) {
		return nil, fmt.Errorf("timescale: TradesInRange: to %v < from %v", to, from)
	}
	const q = `
        SELECT source, ledger, tx_hash, op_index, ts,
               base_asset, quote_asset,
               base_amount, quote_amount,
               COALESCE(maker, ''), COALESCE(taker, '')
          FROM trades
         WHERE base_asset  = $1
           AND quote_asset = $2
           AND ts         >= $3
           AND ts          < $4
         ORDER BY ts ASC, ledger ASC
         LIMIT $5
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(),
		from.UTC(), to.UTC(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: TradesInRange: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []canonical.Trade
	for rows.Next() {
		var t canonical.Trade
		var baseAsset, quoteAsset string
		if err := rows.Scan(
			&t.Source, &t.Ledger, &t.TxHash, &t.OpIndex, &t.Timestamp,
			&baseAsset, &quoteAsset,
			&t.BaseAmount, &t.QuoteAmount,
			&t.Maker, &t.Taker,
		); err != nil {
			return nil, fmt.Errorf("timescale: TradesInRange scan: %w", err)
		}
		base, err := canonical.ParseAsset(baseAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRange base %q: %w", baseAsset, err)
		}
		quote, err := canonical.ParseAsset(quoteAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRange quote %q: %w", quoteAsset, err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRange pair: %w", err)
		}
		t.Pair = pair
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: TradesInRange rows: %w", err)
	}
	return out, nil
}

// TradesInRangeAfter is TradesInRange with a full-PK cursor. Rows
// are returned iff their (ts, ledger, tx_hash, op_index, source)
// tuple is strictly greater than the corresponding `after*` values.
//
// Widening from (ts, ledger) to the full PK closes a pagination
// edge case: multiple trades can share (ts, ledger), and the naive
// tuple `(ts, ledger) > (X, Y)` would skip any same-(ts, ledger)
// row that didn't happen to be the last one on the previous page.
// The primary key is unique so the full-PK tuple gives total order.
//
// afterTs = zero time disables the cursor; use TradesInRange for
// that case (shorter form).
func (s *Store) TradesInRangeAfter(
	ctx context.Context,
	p canonical.Pair,
	from, to, afterTs time.Time,
	afterLedger uint32,
	afterTxHash, afterSource string,
	afterOpIndex uint32,
	limit int,
) ([]canonical.Trade, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}
	if to.Before(from) {
		return nil, fmt.Errorf("timescale: TradesInRangeAfter: to %v < from %v", to, from)
	}
	// Full-PK tuple comparison. ORDER BY + WHERE must agree on the
	// column order so the comparison is monotonic with the sort.
	// Source sorts last so the common case (single-source trades)
	// doesn't pay an unnecessary string compare cost on the index.
	//
	// NOTE: the Go function signature declares afterSource BEFORE
	// afterOpIndex (by-type grouping of the two strings), but the
	// SQL tuple expects them in PK order (tx_hash, op_index, source).
	// The parameter BINDING below — not the signature — is what
	// matters; it hands values to the placeholders in PK order.
	// If you reorder the signature, reorder the binding too.
	const q = `
        SELECT source, ledger, tx_hash, op_index, ts,
               base_asset, quote_asset,
               base_amount, quote_amount,
               COALESCE(maker, ''), COALESCE(taker, '')
          FROM trades
         WHERE base_asset  = $1
           AND quote_asset = $2
           AND ts         >= $3
           AND ts          < $4
           AND (ts, ledger, tx_hash, op_index, source) > ($5, $6, $7, $8, $9)
         ORDER BY ts ASC, ledger ASC, tx_hash ASC, op_index ASC, source ASC
         LIMIT $10
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(), // $1, $2
		from.UTC(), to.UTC(), // $3, $4
		// $5..$9 — must match the PK tuple order in the SQL above,
		// NOT the function-signature order.
		afterTs.UTC(), afterLedger, afterTxHash, afterOpIndex, afterSource,
		limit, // $10
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: TradesInRangeAfter: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []canonical.Trade
	for rows.Next() {
		var t canonical.Trade
		var baseAsset, quoteAsset string
		if err := rows.Scan(
			&t.Source, &t.Ledger, &t.TxHash, &t.OpIndex, &t.Timestamp,
			&baseAsset, &quoteAsset,
			&t.BaseAmount, &t.QuoteAmount,
			&t.Maker, &t.Taker,
		); err != nil {
			return nil, fmt.Errorf("timescale: TradesInRangeAfter scan: %w", err)
		}
		base, err := canonical.ParseAsset(baseAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRangeAfter base %q: %w", baseAsset, err)
		}
		quote, err := canonical.ParseAsset(quoteAsset)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRangeAfter quote %q: %w", quoteAsset, err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			return nil, fmt.Errorf("timescale: TradesInRangeAfter pair: %w", err)
		}
		t.Pair = pair
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: TradesInRangeAfter rows: %w", err)
	}
	return out, nil
}

// FXQuoteAtOrBefore returns the most recent FX-source observation
// for `pair` whose `ts <= cutoff`, restricted to sources passed in
// `fxSources` (typically the result of external.FXSources()). When
// multiple FX sources have a quote at-or-before cutoff, the one with
// the largest ts wins; ties are broken by source-name DESC ordering
// (deterministic across regions because every region's source registry
// is identical).
//
// Returns (price, observedAt, source, nil) on hit;
// (nil, time.Time{}, "", [ErrNoFXQuote]) when no FX quote exists at
// or before cutoff. Other DB errors propagate.
//
// `price` is the per-trade ratio QuoteAmount/BaseAmount expressed as a
// *big.Rat (no precision loss — both sides come from NUMERIC columns).
// FX-source trades use a uniform 1e8 scale on each side so the ratio
// is dimensionally clean (the scale cancels). Empty `fxSources`
// returns ErrNoFXQuote without touching the DB.
//
// Implementation notes:
//   - The hypertable index `(base_asset, quote_asset, ts DESC)` makes
//     this a constant-cost descending range scan. Pushing the source
//     filter to SQL keeps the scan bounded to FX rows.
//   - cutoff is rounded to UTC to match the InsertTrade convention.
func (s *Store) FXQuoteAtOrBefore(
	ctx context.Context,
	pair canonical.Pair,
	cutoff time.Time,
	fxSources []string,
) (price *big.Rat, observedAt time.Time, source string, err error) {
	if len(fxSources) == 0 {
		return nil, time.Time{}, "", ErrNoFXQuote
	}

	const q = `
        SELECT source, ts, base_amount, quote_amount
          FROM trades
         WHERE base_asset  = $1
           AND quote_asset = $2
           AND ts         <= $3
           AND source      = ANY($4)
         ORDER BY ts DESC, source DESC
         LIMIT 1
    `
	var (
		gotSource         string
		gotTS             time.Time
		baseAmt, quoteAmt string
	)
	row := s.db.QueryRowContext(ctx, q,
		pair.Base.String(), pair.Quote.String(),
		cutoff.UTC(), pq.Array(fxSources),
	)
	if err := row.Scan(&gotSource, &gotTS, &baseAmt, &quoteAmt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, time.Time{}, "", ErrNoFXQuote
		}
		return nil, time.Time{}, "", fmt.Errorf("timescale: FXQuoteAtOrBefore: %w", err)
	}

	baseInt, ok := new(big.Int).SetString(baseAmt, 10)
	if !ok || baseInt.Sign() == 0 {
		return nil, time.Time{}, "", fmt.Errorf("timescale: FXQuoteAtOrBefore: invalid base_amount %q", baseAmt)
	}
	quoteInt, ok := new(big.Int).SetString(quoteAmt, 10)
	if !ok {
		return nil, time.Time{}, "", fmt.Errorf("timescale: FXQuoteAtOrBefore: invalid quote_amount %q", quoteAmt)
	}
	r := new(big.Rat).SetFrac(quoteInt, baseInt)
	return r, gotTS, gotSource, nil
}

// CountTrades returns the total number of rows in the trades table.
// O(hypertable scan) on TimescaleDB; use sparingly (diagnostics + tests).
func (s *Store) CountTrades(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM trades`).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("timescale: CountTrades: %w", err)
	}
	return n, nil
}

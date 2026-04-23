package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// InsertTrade writes one trade. Returns nil for a successful insert
// OR a duplicate-key clash (idempotent by identity — we re-insert the
// same trade safely). Other errors propagate.
//
// The trade is validated via [canonical.Trade.Validate] before
// touching the DB; a Validate failure returns [canonical.ErrInvalidTrade].
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
	_, err := s.db.ExecContext(ctx, q,
		t.Source, t.Ledger, t.TxHash, t.OpIndex, t.Timestamp.UTC(),
		t.Pair.Base.String(), t.Pair.Quote.String(),
		t.BaseAmount, t.QuoteAmount, nil, // usd_volume filled by aggregator
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

// TradesInRangeAfter is TradesInRange with a `(ts, ledger)` cursor.
// Only rows whose (ts, ledger) tuple is strictly greater than
// (afterTs, afterLedger) are returned. Paired with ts ASC ordering
// this gives stable, duplicate-free cursor pagination.
//
// afterTs = zero time disables the cursor; use TradesInRange for
// that case (shorter form).
func (s *Store) TradesInRangeAfter(ctx context.Context, p canonical.Pair, from, to, afterTs time.Time, afterLedger uint32, limit int) ([]canonical.Trade, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}
	if to.Before(from) {
		return nil, fmt.Errorf("timescale: TradesInRangeAfter: to %v < from %v", to, from)
	}
	// `(ts, ledger) > ($afterTs, $afterLedger)` is the row-tuple
	// comparison that makes compound-key cursors work in Postgres.
	// Combined with the ts lower bound, the planner can use the
	// (base_asset, quote_asset, ts) index for both predicates.
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
           AND (ts, ledger) > ($5, $6)
         ORDER BY ts ASC, ledger ASC
         LIMIT $7
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(),
		from.UTC(), to.UTC(),
		afterTs.UTC(), afterLedger,
		limit,
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

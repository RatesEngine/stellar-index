package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// InsertOracleUpdate writes one oracle observation. Idempotent on
// (source, ledger, tx_hash, op_index, ts).
func (s *Store) InsertOracleUpdate(ctx context.Context, u canonical.OracleUpdate) error {
	if err := u.Validate(); err != nil {
		return err
	}
	const q = `
        INSERT INTO oracle_updates (
            source, contract_id,
            ledger, tx_hash, op_index, ts,
            asset, quote,
            price, decimals,
            confidence, observer
        ) VALUES (
            $1, NULLIF($2, ''),
            $3, $4, $5, $6,
            $7, $8,
            $9, $10,
            NULLIF($11, 0.0), NULLIF($12, '')
        )
        ON CONFLICT (source, ledger, tx_hash, op_index, ts) DO NOTHING
    `
	_, err := s.db.ExecContext(ctx, q,
		u.Source, u.ContractID,
		u.Ledger, u.TxHash, u.OpIndex, u.Timestamp.UTC(),
		u.Asset.String(), u.Quote.String(),
		u.Price, int(u.Decimals),
		u.Confidence, u.Observer,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertOracleUpdate: %w", err)
	}
	return nil
}

// LatestOracleUpdateForAsset returns the most recent observation
// for an asset from the given source. Returns (nil, ErrNotFound) if
// no row matches.
func (s *Store) LatestOracleUpdateForAsset(ctx context.Context, source string, asset canonical.Asset) (*canonical.OracleUpdate, error) {
	const q = `
        SELECT source, COALESCE(contract_id, ''),
               ledger, tx_hash, op_index, ts,
               asset, quote,
               price, decimals,
               COALESCE(confidence, 0),
               COALESCE(observer, '')
          FROM oracle_updates
         WHERE source = $1
           AND asset  = $2
         ORDER BY ts DESC, ledger DESC
         LIMIT 1
    `
	var (
		u        canonical.OracleUpdate
		assetStr string
		quoteStr string
		decimals int
	)
	err := s.db.QueryRowContext(ctx, q, source, asset.String()).Scan(
		&u.Source, &u.ContractID,
		&u.Ledger, &u.TxHash, &u.OpIndex, &u.Timestamp,
		&assetStr, &quoteStr,
		&u.Price, &decimals,
		&u.Confidence,
		&u.Observer,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("timescale: LatestOracleUpdateForAsset: %w", err)
	}

	parsedAsset, err := canonical.ParseAsset(assetStr)
	if err != nil {
		return nil, fmt.Errorf("timescale: asset %q: %w", assetStr, err)
	}
	parsedQuote, err := canonical.ParseAsset(quoteStr)
	if err != nil {
		return nil, fmt.Errorf("timescale: quote %q: %w", quoteStr, err)
	}
	u.Asset = parsedAsset
	u.Quote = parsedQuote
	u.Decimals = uint8(decimals)
	return &u, nil
}

// LatestOracleUpdatesForAsset returns the most-recent observation
// for asset from EVERY source that has observed it. Returns an
// empty slice + nil error when the asset has no observations.
//
// Optional filter: if sourceFilter != "", the result is restricted
// to that single source (equivalent to calling
// [LatestOracleUpdateForAsset] and wrapping in a 1-element slice,
// but with an empty slice instead of ErrNotFound for "none").
//
// Implementation: DISTINCT ON (source) per Postgres idiom, which
// pairs with (source, asset, ts DESC, ledger DESC) for a cheap scan.
//
// Single-key wrapper around [LatestOracleUpdatesForAssets] —
// preserved for callers that haven't switched to the multi-key
// shape yet.
func (s *Store) LatestOracleUpdatesForAsset(ctx context.Context, asset canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error) {
	return s.LatestOracleUpdatesForAssets(ctx, []canonical.Asset{asset}, sourceFilter)
}

// LatestOracleUpdatesForAssets is the multi-key variant — returns
// the most-recent observation per source across the union of the
// supplied asset keys. The DISTINCT ON (source) pick keeps the
// observation with the highest (ts, ledger) per source, regardless
// of which input key it matched.
//
// Use case: the v1 handler calls this with a translation list —
// e.g. user-facing `native` expands to `[native, crypto:XLM]`
// because Reflector publishes XLM under the global crypto ticker
// rather than the per-network "native" form.
func (s *Store) LatestOracleUpdatesForAssets(ctx context.Context, assets []canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error) {
	if len(assets) == 0 {
		return nil, nil
	}
	keys := make([]string, len(assets))
	for i, a := range assets {
		keys[i] = a.String()
	}
	const q = `
        SELECT DISTINCT ON (source)
               source, COALESCE(contract_id, ''),
               ledger, tx_hash, op_index, ts,
               asset, quote,
               price, decimals,
               COALESCE(confidence, 0),
               COALESCE(observer, '')
          FROM oracle_updates
         WHERE asset = ANY($1)
           AND ($2 = '' OR source = $2)
         ORDER BY source, ts DESC, ledger DESC
    `
	rows, err := s.db.QueryContext(ctx, q, pq.StringArray(keys), sourceFilter)
	if err != nil {
		return nil, fmt.Errorf("timescale: LatestOracleUpdatesForAssets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []canonical.OracleUpdate
	for rows.Next() {
		var (
			u        canonical.OracleUpdate
			assetStr string
			quoteStr string
			decimals int
		)
		if err := rows.Scan(
			&u.Source, &u.ContractID,
			&u.Ledger, &u.TxHash, &u.OpIndex, &u.Timestamp,
			&assetStr, &quoteStr,
			&u.Price, &decimals,
			&u.Confidence, &u.Observer,
		); err != nil {
			return nil, fmt.Errorf("timescale: LatestOracleUpdatesForAsset scan: %w", err)
		}
		parsedAsset, err := canonical.ParseAsset(assetStr)
		if err != nil {
			return nil, fmt.Errorf("timescale: asset %q: %w", assetStr, err)
		}
		parsedQuote, err := canonical.ParseAsset(quoteStr)
		if err != nil {
			return nil, fmt.Errorf("timescale: quote %q: %w", quoteStr, err)
		}
		u.Asset = parsedAsset
		u.Quote = parsedQuote
		u.Decimals = uint8(decimals)
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: LatestOracleUpdatesForAsset rows: %w", err)
	}
	return out, nil
}

// CountOracleUpdates returns the row count in oracle_updates.
// Diagnostic helper, not for production hot paths.
func (s *Store) CountOracleUpdates(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM oracle_updates`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("timescale: CountOracleUpdates: %w", err)
	}
	return n, nil
}

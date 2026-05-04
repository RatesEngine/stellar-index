package timescale

import (
	"context"
	"errors"
	"fmt"
)

// SoroswapPair is the read-side projection of one soroswap_pairs row.
// Pure C-strkey strings — the canonical.Asset reconstruction happens
// in the caller (the indexer/backfill main wiring) so the storage
// layer stays decoupled from the canonical types package.
type SoroswapPair struct {
	PairStrkey   string
	Token0Strkey string
	Token1Strkey string
}

// UpsertSoroswapPair inserts or refreshes a (pair, token0, token1)
// mapping. observed_at is bumped to now() on every call. Idempotent
// on pair_strkey — the live indexer's new_pair handler is wired to
// call this on every factory event without checking whether the row
// already exists.
//
// All three inputs are C-strkeys; the caller validated them at decode
// time (canonical.NewSorobanAsset enforces shape).
func (s *Store) UpsertSoroswapPair(ctx context.Context, pairStrkey, token0Strkey, token1Strkey string) error {
	if pairStrkey == "" {
		return errors.New("timescale: UpsertSoroswapPair: empty pair_strkey")
	}
	if token0Strkey == "" || token1Strkey == "" {
		return fmt.Errorf("timescale: UpsertSoroswapPair %s: token0 or token1 empty", pairStrkey)
	}
	const q = `
		INSERT INTO soroswap_pairs (pair_strkey, token0_strkey, token1_strkey, observed_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (pair_strkey) DO UPDATE SET
		    token0_strkey = EXCLUDED.token0_strkey,
		    token1_strkey = EXCLUDED.token1_strkey,
		    observed_at   = EXCLUDED.observed_at
	`
	if _, err := s.db.ExecContext(ctx, q, pairStrkey, token0Strkey, token1Strkey); err != nil {
		return fmt.Errorf("timescale: UpsertSoroswapPair %s: %w", pairStrkey, err)
	}
	return nil
}

// LoadSoroswapPairRegistry returns every row in soroswap_pairs as a
// flat slice. Used by the indexer + every parallel backfill chunk at
// startup to seed the soroswap.Decoder's in-memory pair registry.
//
// Returns an empty slice (not nil + nil error) when the table is
// empty — that's the steady-state for a fresh deployment that hasn't
// run `ratesengine-ops seed-soroswap-pairs` yet.
func (s *Store) LoadSoroswapPairRegistry(ctx context.Context) ([]SoroswapPair, error) {
	const q = `
		SELECT pair_strkey, token0_strkey, token1_strkey
		  FROM soroswap_pairs
	`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("timescale: LoadSoroswapPairRegistry: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]SoroswapPair, 0, 256)
	for rows.Next() {
		var p SoroswapPair
		if err := rows.Scan(&p.PairStrkey, &p.Token0Strkey, &p.Token1Strkey); err != nil {
			return nil, fmt.Errorf("timescale: LoadSoroswapPairRegistry scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: LoadSoroswapPairRegistry rows: %w", err)
	}
	return out, nil
}

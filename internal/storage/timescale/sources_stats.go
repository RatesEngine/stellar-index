package timescale

import (
	"context"
	"fmt"
)

// SourceStats is the per-source 24h activity row.
type SourceStats struct {
	Source        string
	TradeCount24h int64
}

// GetSourceStats returns trailing-24h trade counts grouped by source.
// Cheap aggregation against the trades hypertable; the source
// column is well-covered by the (ts, source, base_asset,
// quote_asset) ingest pattern.
//
// Sources with no trades in 24h are absent from the result —
// callers join against the static external.Registry to fill in.
func (s *Store) GetSourceStats(ctx context.Context) ([]SourceStats, error) {
	const q = `
		SELECT source, COUNT(*)::bigint
		  FROM trades
		 WHERE ts >= now() - INTERVAL '24 hours'
		 GROUP BY source
		 ORDER BY 2 DESC
	`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("timescale: GetSourceStats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SourceStats
	for rows.Next() {
		var ss SourceStats
		if err := rows.Scan(&ss.Source, &ss.TradeCount24h); err != nil {
			return nil, fmt.Errorf("timescale: GetSourceStats scan: %w", err)
		}
		out = append(out, ss)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: GetSourceStats rows: %w", err)
	}
	return out, nil
}

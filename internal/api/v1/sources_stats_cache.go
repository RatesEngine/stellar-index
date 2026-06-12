package v1

import (
	"context"
	"sync"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// CachedSourcesStatsReader wraps a [SourcesStatsReader] with a
// per-process TTL cache. The underlying SQL aggregations scan ~24h
// of the trades hypertable (millions of rows) and take 5-10s; for
// /v1/sources?include=stats they fire on every page load. Caching
// the result for ttl=60s drops p95 from seconds to microseconds
// without making the data measurably less fresh — these are 24h-
// trailing aggregates that don't move materially in 60s.
//
// Single-flight: concurrent callers during a refetch share one
// upstream call, not N. Important when the perf-bug page-load
// triggers two parallel fetches in the explorer (the table + the
// home strip both want stats).
type CachedSourcesStatsReader struct {
	upstream SourcesStatsReader
	ttl      time.Duration

	mu          sync.Mutex
	stats       []timescale.SourceStats
	statsAt     time.Time
	statsFlight chan struct{}

	hist       []timescale.SourceVolumeBucket
	histAt     time.Time
	histFlight chan struct{}
}

// NewCachedSourcesStatsReader wraps `upstream` with a TTL cache.
// ttl=0 disables the cache (every call goes through). 60s is the
// typical production value.
func NewCachedSourcesStatsReader(upstream SourcesStatsReader, ttl time.Duration) *CachedSourcesStatsReader {
	return &CachedSourcesStatsReader{upstream: upstream, ttl: ttl}
}

// GetSourceStats returns the cached value when fresh; otherwise
// triggers exactly one upstream refetch (sharing it across all
// concurrent callers).
func (c *CachedSourcesStatsReader) GetSourceStats(ctx context.Context) ([]timescale.SourceStats, error) {
	if c.ttl <= 0 {
		return c.upstream.GetSourceStats(ctx)
	}

	c.mu.Lock()
	if time.Since(c.statsAt) < c.ttl && c.stats != nil {
		out := c.stats
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("sources_stats", "source_stats", "hit").Inc()
		return out, nil
	}

	// Stale OR empty. If a refresh is already in flight, wait for it
	// and read the freshly-cached result.
	if c.statsFlight != nil {
		ch := c.statsFlight
		c.mu.Unlock()
		select {
		case <-ch:
			c.mu.Lock()
			out := c.stats
			c.mu.Unlock()
			obs.APICacheOpsTotal.WithLabelValues("sources_stats", "source_stats", "hit").Inc()
			return out, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// We're the leader for this refresh.
	done := make(chan struct{})
	c.statsFlight = done
	c.mu.Unlock()
	obs.APICacheOpsTotal.WithLabelValues("sources_stats", "source_stats", "miss").Inc()

	rows, err := c.upstream.GetSourceStats(ctx)

	c.mu.Lock()
	if err == nil {
		c.stats = rows
		c.statsAt = time.Now()
	}
	c.statsFlight = nil
	c.mu.Unlock()
	close(done)
	return rows, err
}

// GetSourceVolumeHistory24h: same shape as GetSourceStats.
func (c *CachedSourcesStatsReader) GetSourceVolumeHistory24h(ctx context.Context) ([]timescale.SourceVolumeBucket, error) {
	if c.ttl <= 0 {
		return c.upstream.GetSourceVolumeHistory24h(ctx)
	}

	c.mu.Lock()
	if time.Since(c.histAt) < c.ttl && c.hist != nil {
		out := c.hist
		c.mu.Unlock()
		obs.APICacheOpsTotal.WithLabelValues("sources_stats", "volume_history_24h", "hit").Inc()
		return out, nil
	}

	if c.histFlight != nil {
		ch := c.histFlight
		c.mu.Unlock()
		select {
		case <-ch:
			c.mu.Lock()
			out := c.hist
			c.mu.Unlock()
			obs.APICacheOpsTotal.WithLabelValues("sources_stats", "volume_history_24h", "hit").Inc()
			return out, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	done := make(chan struct{})
	c.histFlight = done
	c.mu.Unlock()
	obs.APICacheOpsTotal.WithLabelValues("sources_stats", "volume_history_24h", "miss").Inc()

	rows, err := c.upstream.GetSourceVolumeHistory24h(ctx)

	c.mu.Lock()
	if err == nil {
		c.hist = rows
		c.histAt = time.Now()
	}
	c.histFlight = nil
	c.mu.Unlock()
	close(done)
	return rows, err
}

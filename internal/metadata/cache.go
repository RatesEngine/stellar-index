package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/StellarAtlas/stellar-atlas/internal/cachekeys"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
)

// Cache wraps a [Resolver] with Redis-backed value caching +
// in-process singleflight. Read-through + coalesced-miss is the
// access pattern: many concurrent API requests for the same
// home-domain should share one upstream fetch.
//
// TTL is [cachekeys.TOMLTTL] (24h — stellar.toml is slow-changing
// issuer reference data; a short TTL just makes cold
// /v1/assets/{id} requests pay a fresh ~500ms upstream fetch).
// Negative results are NOT cached — a 404 from the issuer is a
// real signal callers should see, and a transient one at that.
//
// Safe for concurrent use.
type Cache struct {
	resolver *Resolver
	rdb      redis.Cmdable
	ttl      time.Duration
	sf       singleflight.Group
}

// NewCache constructs a cache-wrapped resolver.
//
// rdb may be nil for local/test runs that want no caching — the
// cache then has no persistent layer and each new request falls
// through to the resolver. Note: concurrent callers for the same
// domain still share a single upstream fetch via singleflight
// (even with nil rdb); only sequential callers repeat work.
// Production always provides a live redis client.
func NewCache(resolver *Resolver, rdb redis.Cmdable) *Cache {
	return &Cache{
		resolver: resolver,
		rdb:      rdb,
		ttl:      cachekeys.TOMLTTL,
	}
}

// Resolve returns a cached SEP1 or fetches + caches a fresh one.
//
// Flow: Redis GET → hit: decode + return. Miss: acquire singleflight
// slot, re-check Redis (another goroutine may have won the race),
// fetch upstream, SET with [cachekeys.TOMLTTL], return.
func (c *Cache) Resolve(ctx context.Context, domain string) (*SEP1, error) {
	key := cachekeys.TOML(domain)

	if sep, ok := c.getCached(ctx, key); ok {
		obs.Sep1CacheOpsTotal.WithLabelValues("hit").Inc()
		return sep, nil
	}

	// DoChan (not Do) so a waiter whose ctx cancels doesn't block
	// past its own deadline. With Do, if caller A enters the
	// singleflight slot and caller B joins, B waits for A's
	// function to complete — B's ctx cancellation is ignored.
	// DoChan returns a channel; we select (ctx.Done, chan) per
	// caller independently.
	//
	// The function runs in a DETACHED context (context.Background)
	// so the first caller's cancellation doesn't kill an in-flight
	// fetch that would benefit other waiters. The resolver still
	// has its own 8s timeout built in; detaching just means we
	// don't truncate it short when one caller gives up early.
	ch := c.sf.DoChan(key, func() (any, error) {
		// Re-check inside the singleflight slot: if another caller
		// already populated while we were queued, skip the upstream
		// fetch entirely. Counts as a hit — the alternative would
		// under-count hit rate during high contention.
		if sep, ok := c.getCached(ctx, key); ok {
			obs.Sep1CacheOpsTotal.WithLabelValues("hit").Inc()
			return sep, nil
		}

		// Detached fetch context — see comment above.
		fetchCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second) //nolint:contextcheck // singleflight deliberately survives per-caller cancellation
		defer cancel()
		sep, err := c.resolver.Resolve(fetchCtx, domain) //nolint:contextcheck // see above
		if err != nil {
			obs.Sep1CacheOpsTotal.WithLabelValues("upstream_error").Inc()
			return nil, err
		}
		obs.Sep1CacheOpsTotal.WithLabelValues("miss").Inc()
		c.setCached(fetchCtx, key, sep) //nolint:contextcheck // writes use the same detached context as the fetch above
		return sep, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.(*SEP1), nil
	}
}

func (c *Cache) getCached(ctx context.Context, key string) (*SEP1, bool) {
	if c.rdb == nil {
		return nil, false
	}
	b, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		// Any error — key-miss (redis.Nil), connection blip, decode
		// failure — falls through to upstream. Cache is an
		// optimisation, never a hard dependency.
		return nil, false
	}
	sep := &SEP1{}
	if err := json.Unmarshal(b, sep); err != nil {
		return nil, false
	}
	return sep, true
}

func (c *Cache) setCached(ctx context.Context, key string, sep *SEP1) {
	if c.rdb == nil {
		return
	}
	b, err := json.Marshal(sep)
	if err != nil {
		return
	}
	_ = c.rdb.Set(ctx, key, b, c.ttl).Err()
}

// Invalidate drops the cached entry for domain. Called when an
// operator indicates a fresh fetch is needed (e.g., asset metadata
// admin action).
func (c *Cache) Invalidate(ctx context.Context, domain string) error {
	if c.rdb == nil {
		return nil
	}
	if err := c.rdb.Del(ctx, cachekeys.TOML(domain)).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("metadata: invalidate %q: %w", domain, err)
	}
	return nil
}

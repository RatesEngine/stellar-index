// Package cachekeys is the single source of truth for Rates Engine's
// Redis key grammar, per ADR-0007.
//
// Rationale: every caller that writes to or reads from Redis must use
// these helpers rather than constructing key strings directly. That
// way a typo like `"prce:XLM"` is a compile error (the helper
// doesn't exist) rather than a silent cache-miss-forever.
//
// Design:
//
//   - Zero external dependencies. Pure string manipulation.
//     `import "github.com/redis/go-redis/v9"` is a decision we keep
//     out of this package so it can be imported by any layer
//     including code with no Redis knowledge (e.g. config loaders
//     that validate key prefixes at startup).
//
//   - One builder function per key class — never a generic
//     `Key(parts ...string) string`. The types of the parts matter:
//     `Price(asset canonical.Asset)` takes a typed asset, not a raw
//     string. Callers who pass a raw string get a compile error,
//     which is the point.
//
//   - One TTL helper per key class. Most return a constant; a few
//     (VWAP, OHLC-open-candle) take a parameter. Caller code reads
//     clearly: `ttl := cachekeys.PriceTTL`.
//
// Cross-reference:
//
//   - ADR-0007 — defines the 10 key classes, their semantics, and
//     TTL rules.
//   - internal/ratelimit — implements the `rl:*` family and does NOT
//     import this package (it has its own narrowly-scoped key shape).
//     The Test for `RateLimit` here just asserts the prefix matches
//     what internal/ratelimit writes, preventing drift.
//
// Non-goals:
//
//   - This package does NOT talk to Redis. Callers pass the returned
//     strings to `redis.Client` themselves. The
//     `internal/cachekeys/cache` package (future) will be the thin
//     convenience layer that couples both.
//
//   - This package does NOT serialise values. That belongs to the
//     caller who knows the domain type (canonical.Price, Asset etc.).
package cachekeys

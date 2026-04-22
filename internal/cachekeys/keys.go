package cachekeys

import (
	"fmt"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// Prefix is the global namespace for every Rates Engine cache key.
// Nothing else should produce keys under this prefix.
const Prefix = ""

// ─── Price — latest aggregated price per asset ────────────────────
//
// Wire shape: `price:<asset_id>`
// Writer: aggregator
// Reader: api
// TTL: 60 s (refreshed on every aggregation cycle).

// Price returns the cache key for the latest aggregated price of asset.
func Price(asset canonical.Asset) string {
	return "price:" + asset.String()
}

// PriceTTL is the expiry for price: keys.
const PriceTTL = 60 * time.Second

// ─── VWAP — per-pair + window pre-compute ─────────────────────────
//
// Wire shape: `vwap:<base>:<quote>:<window-seconds>`
// TTL matches window.

// VWAP returns the cache key for a rolling VWAP over window for the
// given pair.
func VWAP(base, quote canonical.Asset, window time.Duration) string {
	return fmt.Sprintf("vwap:%s:%s:%d",
		base.String(), quote.String(), int(window.Seconds()))
}

// VWAPTTL is the TTL for a VWAP key — equal to its window. Returns 0
// for zero window (callers should treat as "don't cache").
func VWAPTTL(window time.Duration) time.Duration { return window }

// ─── OHLC — one candle per (pair, granularity, bucket-start) ──────
//
// Wire shape: `ohlc:<base>:<quote>:<granularity>:<bucket-epoch>`
// Where granularity is "1m" / "15m" / "1h" / "4h" / "1d" / "1w" / "1mo"
// and bucket-epoch is the Unix seconds of the candle start.
//
// Closed candles are immutable — cached with NO TTL (CDN-pinned).
// Open candles TTL short (5 s) so the aggregator can refresh.

// OHLC returns the cache key for one OHLC candle.
func OHLC(base, quote canonical.Asset, granularity string, bucketStart time.Time) string {
	return fmt.Sprintf("ohlc:%s:%s:%s:%d",
		base.String(), quote.String(),
		granularity, bucketStart.Unix())
}

// OHLCOpenTTL is the TTL for the currently-open candle at any
// granularity. Short — aggregator overwrites each refresh cycle.
const OHLCOpenTTL = 5 * time.Second

// OHLCClosedTTL is the TTL for a closed (historical) candle.
// Zero = no expiry (the candle is immutable; CDN pins it upstream).
const OHLCClosedTTL = time.Duration(0)

// ─── Rate-limit counters — one per (key, window) ──────────────────
//
// The rl: family is OWNED by internal/ratelimit, which writes keys
// atomically via a Lua script. The functions below are mirrors of
// that shape for read-only access (e.g. admin dashboards showing
// current usage) and CI consistency checks.
//
// Wire shape: `rl:<subject>:<window-bucket>` where subject is an
// API-key hash or IP address.

// RateLimitKey returns the cache key for a rate-limit counter.
// Deliberately named "...Key" not just "RateLimit" because callers
// are usually reading this for display, not as the write-path.
// window is the fixed-window size (typically 60 s).
func RateLimitKey(subject string, now time.Time, window time.Duration) string {
	bucket := now.Unix() / int64(window.Seconds())
	return fmt.Sprintf("rl:%s:%d", subject, bucket)
}

// RateLimitTTL is the TTL set on rl: keys. 2× window, per ADR-0007
// (keys drain naturally; counter resets at window rollover).
func RateLimitTTL(window time.Duration) time.Duration { return 2 * window }

// ─── SEP-1 / home-domain cache ────────────────────────────────────
//
// Wire shape: `toml:<home-domain>`
// Cached stellar.toml parse result. Lazy-populated by API handlers
// on miss; also invalidated when the home-domain field of a
// classic-asset record changes.

// TOML returns the cache key for a SEP-1 home-domain record.
func TOML(homeDomain string) string {
	return "toml:" + strings.ToLower(homeDomain)
}

// TOMLTTL is the expiry for toml: keys.
const TOMLTTL = 15 * time.Minute

// ─── Asset metadata — code/issuer/contract/decimals + SEP-1 overlay─
//
// Wire shape: `meta:<asset_id>`

// Metadata returns the cache key for the per-asset metadata bundle.
func Metadata(asset canonical.Asset) string {
	return "meta:" + asset.String()
}

// MetadataTTL is the expiry for meta: keys.
const MetadataTTL = 5 * time.Minute

// ─── SSE subscriber registry ──────────────────────────────────────
//
// Wire shape: `sub:<channel>:<subscriber-id>`
// Value: "1" (presence marker).
// TTL: renewed by the subscriber's heartbeat every 60 s; key expires
// 60 s after the last heartbeat.

// Subscriber returns the cache key for an SSE subscriber presence
// marker. channel is typically a price-stream channel name; subID
// is the opaque subscriber identifier.
func Subscriber(channel, subID string) string {
	return fmt.Sprintf("sub:%s:%s", channel, subID)
}

// SubscriberTTL is the expiry for sub: keys — matches the
// heartbeat cadence with headroom.
const SubscriberTTL = 60 * time.Second

// ─── Divergence detector output ───────────────────────────────────
//
// Wire shape: `div:<asset_id>`
// Value: JSON with sources compared + max deviation + threshold.
// Written by the divergence worker after each check cycle.

// Divergence returns the cache key for the latest divergence result
// for an asset.
func Divergence(asset canonical.Asset) string {
	return "div:" + asset.String()
}

// DivergenceTTL is the expiry for div: keys.
const DivergenceTTL = 5 * time.Minute

// ─── Per-source freshness gauge ───────────────────────────────────
//
// Wire shape: `health:<source>`
// Value: JSON with last_event_ts + lag_ledgers.
// Written by the indexer on every event; read by the API for
// /readyz + by Prometheus for scrape.

// Health returns the cache key for a source freshness gauge.
func Health(source string) string {
	return "health:" + source
}

// HealthTTL is the expiry for health: keys. 60 s gives us one
// missed update before the gauge disappears.
const HealthTTL = 60 * time.Second

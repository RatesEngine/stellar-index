package cachekeys

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

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

// ─── VWAP Provenance — was this VWAP triangulated? ──────────────────
//
// Wire shape: `vwap:<base>:<quote>:<window-seconds>:provenance`
// TTL: matches the VWAP value key.
//
// Writer: aggregator's triangulation worker writes "triangulated"
// alongside the value key. Per-pair direct refresh does NOT write
// this — absence == direct (or unknown). Reader treats empty / nil
// as "not triangulated".
//
// Used by the API's price handler to set `flags.triangulated`
// (per ADR-0018 + the "triangulation in serving path" remediation
// for audit F-0014). When the API serves from a Redis-fallback
// path (because the pair has no direct prices_1m row but has a
// triangulated implied value), it consults this key to populate
// the flag.

// VWAPProvenance returns the cache key marker for whether a
// `vwap:<base>:<quote>:<window>` value came from the triangulation
// worker (vs. the direct per-pair refresh).
func VWAPProvenance(base, quote canonical.Asset, window time.Duration) string {
	return fmt.Sprintf("vwap:%s:%s:%d:provenance",
		base.String(), quote.String(), int(window.Seconds()))
}

// VWAPProvenanceTriangulated is the value the triangulation worker
// stamps into the [VWAPProvenance] key. The API reader matches by
// byte-equality.
const VWAPProvenanceTriangulated = "triangulated"

// ─── Confidence — multi-factor score per (pair, window) ───────────
//
// Wire shape: `confidence:<base>:<quote>:<window-seconds>`
// Writer: aggregator (alongside the corresponding vwap: key).
// Reader: api (`/v1/price` envelope's confidence field).
// TTL: matches the VWAP key — confidence becomes meaningless once
// the VWAP it scored expires.
//
// Value is a JSON-encoded confidence.Score (Confidence + Factors)
// rather than a bare float so the API can ship the full
// decomposition without a second lookup.

// Confidence returns the cache key for the confidence score on the
// given (pair, window).
func Confidence(base, quote canonical.Asset, window time.Duration) string {
	return fmt.Sprintf("confidence:%s:%s:%d",
		base.String(), quote.String(), int(window.Seconds()))
}

// ConfidenceTTL is the TTL for a confidence: key. Matches VWAPTTL —
// the score is tied to its underlying VWAP and should expire with it.
func ConfidenceTTL(window time.Duration) time.Duration { return window }

// ─── OHLC — one candle per (pair, granularity, bucket-start) ──────
//
// Wire shape: `ohlc:<base>:<quote>:<granularity>:<bucket-epoch>`
// Where granularity is "1m" / "15m" / "1h" / "4h" / "1d" / "1w" / "1mo"
// and bucket-epoch is the Unix seconds of the candle start.
//
// Closed candles are immutable — cached with NO TTL (CDN-pinned).
// Open candles TTL is a safety-net upper bound; in practice the
// aggregator overwrites the key on every refresh cycle (30 s for 1m,
// longer for coarser grains per migration 0002), so the cached value
// is much fresher than the TTL suggests.

// OHLC returns the cache key for one OHLC candle.
func OHLC(base, quote canonical.Asset, granularity string, bucketStart time.Time) string {
	return fmt.Sprintf("ohlc:%s:%s:%s:%d",
		base.String(), quote.String(),
		granularity, bucketStart.Unix())
}

// OHLCOpenTTL is the SAFETY-NET TTL for the currently-open candle at
// any granularity. Matches ADR-0007. The aggregator refreshes each
// candle on a cadence tied to its granularity (sub-1m; sub-15m;
// sub-1h; …), so the cached value rolls well before this TTL fires.
// The TTL exists only so that if the aggregator stops writing, stale
// open-candle data doesn't live indefinitely.
const OHLCOpenTTL = time.Hour

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
//
// Subject is url.QueryEscape'd for parity with the writer in
// internal/ratelimit/bucket.go — IPv6 addresses contain `:` and
// without escaping two distinct subjects could land on the same
// Redis slot. Keep this in lock-step with the writer; the tests
// round-trip a sample subject to detect drift.
func RateLimitKey(subject string, now time.Time, window time.Duration) string {
	bucket := now.Unix() / int64(window.Seconds())
	return fmt.Sprintf("rl:%s:%d", url.QueryEscape(subject), bucket)
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

// ─── Anomaly freeze marker (ADR-0019) ─────────────────────────────
//
// Wire shape: `freeze:<asset_id>:<quote_id>`
// Value: JSON with the underlying anomaly Decision (deviation_pct,
//   reason, expires_at). Presence of the key means the most-recent
//   bucket for the pair was frozen by the anomaly checker; the API
//   reads it via FrozenLooker to set flags.frozen=true.
//
// Writer: aggregator orchestrator at bucket-close, when
// anomaly.Checker.Evaluate returns ActionFreeze.
// Reader: internal/api/v1.FrozenLooker — production wiring is the
// freeze package's RedisLooker.
//
// TTL: 5 minutes — long enough that the next bucket close (1
// minute) sees the marker still in place if the anomaly persists,
// short enough that a transient anomaly clears within a few buckets
// of the underlying signal returning to normal.

// Freeze returns the cache key for the freeze marker on an
// (asset, quote) pair. The marker's presence drives flags.frozen
// on /v1/price; the value carries diagnostic context (which class
// thresholds fired, observed deviation, last-known-good price).
func Freeze(asset, quote canonical.Asset) string {
	return "freeze:" + asset.String() + ":" + quote.String()
}

// FreezeTTL is the expiry for freeze: keys.
const FreezeTTL = 5 * time.Minute

// ─── API-key records ──────────────────────────────────────────────
//
// Wire shape: `apikey:<sha256-hex>`
// Value: JSON record `{identifier, tier, scopes, expires_at?, revoked_at?}`.
// Writer: `/v1/account/keys` POST handler (self-service key
//         issuance) plus operator seeding scripts.
// Reader: `internal/auth/RedisAPIKeyValidator` on every authenticated
//         request when auth_mode=apikey.
//
// Plaintext keys are NEVER stored — the lookup hashes the
// caller-supplied bytes with SHA-256 (32-byte high-entropy keys are
// preimage-safe; HMAC with a server pepper is a future hardening if
// keys are ever shorter or operator-set). A Redis dump leaks
// metadata but not the keys themselves.
//
// No TTL: API keys are long-lived; expiry + revocation are encoded
// in the JSON record, not at the Redis layer. An operator rotating
// keys deletes the record explicitly.

// APIKey returns the cache key for the API-key record identified by
// keyHash. keyHash MUST be hex-encoded SHA-256 of the plaintext key
// (the auth package does the hashing — callers don't construct this
// directly except in admin tooling that already has the hash).
func APIKey(keyHash string) string {
	return "apikey:" + keyHash
}

// APIKeyTTL is the TTL for apikey: records. Zero — keys live until
// explicitly deleted; expiry/revocation are encoded in the JSON
// payload so the lookup can return the right error sentinel
// (ErrTokenExpired vs ErrUnauthorized).
const APIKeyTTL = time.Duration(0)

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

// ─── Oracle latest readings — read-through cache ─────────────────
//
// Wire shape: `oracle:latest:<asset-keys-joined>:<source-filter>`
// Writer: api (read-through; populated on cache miss)
// Reader: api
// TTL: 30 s — Reflector / Band / RedStone push every 1–5 minutes;
// a 30 s cached entry stays inside one push interval, so customers
// see fresh readings without paying the 285–580 ms full DISTINCT ON
// (source) sort on every poll.
//
// Asset list is sorted before joining so the same logical query
// hits the same cache key regardless of input order — the v1
// handler always passes [user-asset, translated-asset] but the
// hash is order-independent.

// OracleLatest returns the cache key for the multi-source latest
// reading of a deduped, sorted list of asset keys with optional
// source filter (empty = "every source").
func OracleLatest(assetKeys []string, sourceFilter string) string {
	// Defensive copy + sort so the cache key is stable regardless
	// of the caller's argument order. The `|` separator can't
	// appear in a canonical asset_id (G-strkey + base32 alphabet,
	// `:` for class prefixes, contract `C…`).
	sorted := append([]string(nil), assetKeys...)
	sortStrings(sorted)
	return "oracle:latest:" + strings.Join(sorted, "|") + ":" + sourceFilter
}

// OracleLatestTTL is the TTL for `oracle:latest:*` cache entries.
const OracleLatestTTL = 30 * time.Second

// ─── Assets list / Markets list — read-through cache ──────────────
//
// Wire shape:
//   `assets:list:<cursor>:<limit>`
//   `markets:list:<cursor>:<limit>`
//
// Writer: api (read-through; populated on cache miss)
// Reader: api
// TTL: 60 s — both endpoints derive from a 14-day rolling
// window over the trades hypertable; new assets/pairs appear on
// the human timescale of new listings (minutes-to-hours), so a
// 60 s entry stays well inside the human freshness expectation.
//
// Invalidation: TTL only — no explicit invalidation on insert.
// 60 s of staleness on a "new asset just got its first trade"
// is acceptable; a fresh listing isn't expected to surface
// instantly.

// AssetsList returns the cache key for one page of /v1/assets.
func AssetsList(cursor string, limit int) string {
	return fmt.Sprintf("assets:list:%s:%d", cursor, limit)
}

// MarketsList returns the cache key for one page of /v1/markets.
func MarketsList(cursor string, limit int) string {
	return fmt.Sprintf("markets:list:%s:%d", cursor, limit)
}

// CatalogueListTTL is the shared TTL for both `assets:list:*` and
// `markets:list:*`. Tighter than the underlying 14-day window but
// loose enough to absorb polling fan-out.
const CatalogueListTTL = 60 * time.Second

// sortStrings is a tiny inline sort to avoid pulling sort into
// every cachekeys consumer.
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}

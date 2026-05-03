package redispub

import "time"

// DefaultChannel is the Redis pub/sub channel the [Publisher]
// writes to and the matching Subscriber listens on by default.
// Operators with multiple deployments sharing one Redis can
// override per-environment to keep streams partitioned.
const DefaultChannel = "ratesengine:closed-bucket:v1"

// ClosedBucketEvent is the JSON wire shape published per
// successful (pair, window) VWAP cache write. Carries the minimum
// the API-side subscriber needs to reconstruct the SSE topic and
// payload.
//
// Versioning: schema additions are non-breaking via JSON's
// "ignore unknown fields" decoding; subscribers tolerant of new
// fields, no schema bump needed. Removals or type changes are
// breaking and require a new channel name (see [DefaultChannel]).
type ClosedBucketEvent struct {
	// Asset + Quote echo the canonical asset strings the
	// aggregator just published a closed bucket for. The
	// API-side subscriber routes by (Asset, Quote) into
	// `closed:<asset>/<quote>` Hub topics.
	Asset string `json:"asset"`
	Quote string `json:"quote"`

	// WindowSeconds is the closed-bucket window expressed as
	// integer seconds. Encoded as a number rather than a Go
	// duration string for cross-language subscriber friendliness.
	WindowSeconds int64 `json:"window_seconds"`

	// ValueDecimal is the VWAP rendered as a fixed-precision
	// decimal string (12 fractional digits) — the same form the
	// aggregator wrote to the Redis cache key. Strings preserve
	// big.Rat precision the JSON `number` type would lose.
	ValueDecimal string `json:"value_decimal"`

	// ObservedAt is the bucket-end timestamp the aggregator
	// attributed to this VWAP. RFC 3339 UTC.
	ObservedAt time.Time `json:"observed_at"`
}

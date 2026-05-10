package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/RatesEngine/rates-engine/internal/auth"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/ratelimit"
)

// MaxRateLimitKeyLen caps the caller-supplied KeyFn output so a
// hostile header (multi-KB X-Forwarded-For) can't blow up the
// Redis key space or the url.QueryEscape allocation inside
// Bucket.Take. 256 bytes fits any legitimate IP (including
// IPv6 + zone) and any realistic API-key / SEP-10 account id
// with headroom.
const MaxRateLimitKeyLen = 256

// RateLimit returns middleware that enforces the given Bucket on
// every request whose KeyFn produces a non-empty key.
//
// Headers added on every response:
//
//	X-RateLimit-Limit:     <bucket max>
//	X-RateLimit-Remaining: <after this request>
//
// On 429, additionally:
//
//	Retry-After: <seconds until window resets>
//
// plus an RFC 9457 problem+json body.
//
// Fail-open: a Redis outage does NOT reject requests. The middleware
// logs the error at debug (so noise doesn't drown real signals) and
// lets the request through. This mirrors the guidance in
// [ratelimit.Bucket.Take]'s doc: the rate limiter is a policy knob,
// not a hard dependency.
//
// KeyFn decides the key: per-IP (default if nil), per-API-key, etc.
// If KeyFn returns "" the request is not rate-limited. Skip allows
// full bypass for infra endpoints (health probes, metrics).
//
// Key length is capped at [MaxRateLimitKeyLen]. A hostile
// X-Forwarded-For can grow up to the server's MaxHeaderBytes
// (1 MB default in net/http); without a cap here that would
// propagate through url.QueryEscape into the Redis key space.
// Oversize keys get truncated to the cap — still bucketed, just
// not uniquely per-caller past the first N bytes.
func RateLimit(bucket *ratelimit.Bucket, keyFn func(*http.Request) string, skip func(*http.Request) bool, logger *slog.Logger) Middleware { //nolint:gocognit // dispatch-heavy; splitting would reduce linearity
	if logger == nil {
		logger = slog.Default()
	}
	if keyFn == nil {
		keyFn = func(r *http.Request) string { return RemoteIPFrom(r) }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip != nil && skip(r) {
				next.ServeHTTP(w, r)
				return
			}
			key := keyFn(r)
			if key == "" {
				// No identifiable subject — let through. This is rare
				// in practice since Logger populates remote_ip.
				next.ServeHTTP(w, r)
				return
			}
			if len(key) > MaxRateLimitKeyLen {
				key = key[:MaxRateLimitKeyLen]
			}

			res, err := bucket.Take(r.Context(), key)
			if err != nil {
				// Log at debug so a Redis outage doesn't flood the
				// error log — the metric below is the alertable
				// signal, the log is for post-mortem detail.
				logger.Debug("ratelimit redis error — failing open",
					"err", err, "key", key, "request_id", RequestIDFrom(r))
				obs.RateLimitFailOpenTotal.Inc()
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(bucket.Max()))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(nextWindowResetUnix(bucket.Window()), 10))

			if !res.Allowed {
				retryAfter := int(res.RetryAfter.Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeRateLimitProblem(w, r, retryAfter)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// nextWindowResetUnix returns the Unix-epoch seconds of when the
// fixed-window bucket's CURRENT window ends. Mirrors GitHub /
// Twitter's `X-RateLimit-Reset` semantics: clients can compute
// `seconds_until_reset = X-RateLimit-Reset - now` to back off
// proactively without waiting for a 429. See:
// https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-ratelimit-headers
//
// Implementation matches the bucket's key derivation:
//
//	bucket key = unix() / window.Seconds()
//	current window ends at = ((unix()/window) + 1) * window
func nextWindowResetUnix(window time.Duration) int64 {
	if window < time.Second {
		// Bucket constructor rejects this; defensive zero stops the
		// formula's divide-by-zero in case a future bucket variant
		// permits sub-second windows without rejecting at New().
		return time.Now().Unix()
	}
	windowSecs := int64(window.Seconds())
	now := time.Now().Unix()
	return ((now / windowSecs) + 1) * windowSecs
}

// RateLimitBySubject enforces separate anonymous and authenticated
// buckets. When an auth middleware has attached a Subject, anonymous
// callers use anonBucket and authenticated callers use authBucket.
//
// Keying semantics:
//   - authenticated with KeyID: per-key bucket
//   - authenticated without KeyID: per-subject Identifier bucket
//   - anonymous with Subject: anonymous Identifier bucket
//   - no Subject attached: fallback to RemoteIPFrom(r)
//
// Per-subject overrides: an authenticated subject with
// `Subject.RateLimitPerMin > 0` (a paid-tier override sourced from
// the APIKey record) replaces authBucket's default max for THIS
// caller's bucket. The override has no effect on anonymous callers —
// anonRateLimitPerMin is a deployment knob, not a per-IP override.
//
// Nil buckets disable rate limiting for that class.
func RateLimitBySubject(anonBucket, authBucket *ratelimit.Bucket, skip func(*http.Request) bool, logger *slog.Logger) Middleware { //nolint:gocognit // dispatch-heavy; splitting would reduce readability
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip != nil && skip(r) {
				next.ServeHTTP(w, r)
				return
			}

			bucket, key, override := bucketKeyAndOverrideForRequest(r, anonBucket, authBucket)
			if bucket == nil || key == "" {
				next.ServeHTTP(w, r)
				return
			}
			if len(key) > MaxRateLimitKeyLen {
				key = key[:MaxRateLimitKeyLen]
			}

			res, err := bucket.TakeN(r.Context(), key, override)
			if err != nil {
				logger.Debug("ratelimit redis error — failing open",
					"err", err, "key", key, "request_id", RequestIDFrom(r))
				obs.RateLimitFailOpenTotal.Inc()
				next.ServeHTTP(w, r)
				return
			}

			effectiveMax := bucket.Max()
			if override > 0 {
				effectiveMax = override
			}
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(effectiveMax))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(nextWindowResetUnix(bucket.Window()), 10))

			if !res.Allowed {
				retryAfter := int(res.RetryAfter.Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeRateLimitProblem(w, r, retryAfter)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// bucketKeyAndOverrideForRequest picks the right bucket + Redis key
// for the inbound request, plus any per-subject limit override.
//
// Override is non-zero ONLY for an authenticated subject whose
// APIKey record carries an explicit `RateLimitPerMin` (paid-tier
// custom plan). Anonymous callers always get `0` (the bucket's
// default applies). Operators can still floor the bucket via
// `cfg.API.KeyRateLimitPerMin` — the override only raises (or
// lowers) the per-key budget, never the global default.
func bucketKeyAndOverrideForRequest(r *http.Request, anonBucket, authBucket *ratelimit.Bucket) (*ratelimit.Bucket, string, int) {
	if subject, ok := auth.SubjectFrom(r.Context()); ok && subject.Identifier != "" {
		if subject.Tier != auth.TierAnonymous && subject.Tier != "" {
			if authBucket == nil {
				return nil, "", 0
			}
			return authBucket, authenticatedRateLimitKey(subject), subject.RateLimitPerMin
		}
		if anonBucket == nil {
			return nil, "", 0
		}
		return anonBucket, "anon:" + subject.Identifier, 0
	}
	if anonBucket == nil {
		return nil, "", 0
	}
	return anonBucket, RemoteIPFrom(r), 0
}

func authenticatedRateLimitKey(subject auth.Subject) string {
	if subject.KeyID != "" {
		return "auth:" + subject.Tier.String() + ":key:" + subject.KeyID
	}
	return "auth:" + subject.Tier.String() + ":id:" + subject.Identifier
}

// SkipHealthAndMetrics is a convenience Skip predicate for operators
// who don't want liveness probes or prometheus scrapes counted.
func SkipHealthAndMetrics(r *http.Request) bool {
	switch r.URL.Path {
	case "/v1/healthz", "/v1/readyz", "/v1/version", "/metrics":
		return true
	}
	return false
}

// rlProblem is a minimised RFC 9457 body duplicated here so the
// middleware package doesn't import internal/api/v1 (which would
// create a cycle — v1 imports middleware).
type rlProblem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func writeRateLimitProblem(w http.ResponseWriter, r *http.Request, retryAfter int) {
	p := rlProblem{
		Type:     "https://api.ratesengine.net/errors/rate-limited",
		Title:    "Rate limit exceeded",
		Status:   http.StatusTooManyRequests,
		Detail:   "Retry after " + strconv.Itoa(retryAfter) + "s",
		Instance: r.URL.RequestURI(),
	}
	w.Header().Set("Content-Type", "application/problem+json")
	// Override the cache-control middleware's per-route directive:
	// never cache a 429. A 429 says "this caller is over budget right
	// now" — caching it would replay the denial to other anonymous
	// clients on the same CDN key well past their own budget reset.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(p)
}

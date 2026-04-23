package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

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
func RateLimit(bucket *ratelimit.Bucket, keyFn func(*http.Request) string, skip func(*http.Request) bool, logger *slog.Logger) Middleware {
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
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(p)
}

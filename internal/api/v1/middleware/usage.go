package middleware

import (
	"log/slog"
	"net/http"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
	"github.com/StellarAtlas/stellar-atlas/internal/usage"
)

// UsageTracker records per-request daily counters keyed on the
// authenticated subject (KeyID for API-key callers; the auth.Subject
// Identifier when KeyID is empty). Anonymous requests are skipped —
// /v1/account/usage is per-account, and there's no account to bill
// for IP-only callers.
//
// Failures are logged at debug and dropped — usage tracking must
// never block a request. The metric below alerts on Redis trouble.
//
// Wire this AFTER the auth middleware (so SubjectFrom returns) and
// AFTER rate-limit (so denied requests don't pollute the counters).
func UsageTracker(counter *usage.Counter, logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if counter == nil {
				return
			}
			subject, ok := auth.SubjectFrom(r.Context())
			if !ok {
				return
			}
			id := usageKeyForSubject(subject)
			if id == "" {
				return
			}
			if err := counter.Increment(r.Context(), id); err != nil {
				logger.Debug("usage: increment failed", "err", err, "subject", id)
			}
		})
	}
}

// usageKeyForSubject picks the stable identifier we count under.
// Order of preference:
//  1. KeyID — distinguishes per-key when one account has many.
//  2. Identifier — group-by stable identifier across keys.
//  3. "" — anonymous; skip.
func usageKeyForSubject(s auth.Subject) string {
	if s.Tier == auth.TierAnonymous || s.Tier == "" {
		return ""
	}
	if s.KeyID != "" {
		return "key:" + s.KeyID
	}
	if s.Identifier != "" {
		return "id:" + s.Identifier
	}
	return ""
}

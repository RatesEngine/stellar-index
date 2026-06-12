// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// MonthToDateReader is the storage-side primitive the
// [MonthlyQuota] middleware uses. Production implementation:
// `*usage.Counter.MonthToDate`. Declared as a narrow interface
// so tests can substitute fakes without standing up Redis.
type MonthToDateReader interface {
	MonthToDate(ctx context.Context, subject string) (int64, error)
}

// MonthlyQuota returns a [Middleware] that enforces the per-key
// `Subject.MonthlyQuota` ceiling. F-1226 (codex audit-2026-05-12):
// the dashboard accepted a per-key `monthly_quota` value and the
// Postgres store persisted it, but no runtime middleware
// enforced it — paid customers on metered plans could spend
// indefinitely past the cap.
//
// Behaviour:
//
//   - Subject with `MonthlyQuota <= 0` (zero / negative): no
//     check; the middleware is a pass-through. Anonymous /
//     un-keyed callers fall in here.
//   - Subject with `MonthlyQuota > 0` and a non-nil reader: read
//     the month-to-date counter for the subject key (matches
//     `UsageTracker`'s `usageKeyForSubject` so the writer and
//     reader stay in lock-step). When the count >= quota, reject
//     with `429 Too Many Requests` + Problem-JSON body listing
//     the cap.
//   - Reader nil or read error: log + pass through. The cap is
//     not a security boundary — it's a billing fairness
//     mechanism, so a transient Redis blip must not 500 paying
//     customers.
//
// Wire AFTER [Auth] (so SubjectFrom returns) and BEFORE
// [RateLimit] so a request rejected by the monthly cap doesn't
// also spend a rate-limit token.
func MonthlyQuota(reader MonthToDateReader, logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			subject, ok := auth.SubjectFrom(r.Context())
			if !ok || subject.MonthlyQuota <= 0 || reader == nil {
				next.ServeHTTP(w, r)
				return
			}
			id := usageKeyForSubject(subject)
			if id == "" {
				next.ServeHTTP(w, r)
				return
			}
			used, err := reader.MonthToDate(r.Context(), id)
			if err != nil {
				logger.Debug("monthly-quota: read failed; failing open",
					"err", err, "subject", id)
				next.ServeHTTP(w, r)
				return
			}
			if used >= subject.MonthlyQuota {
				writeMonthlyQuotaDenied(w, r, subject.MonthlyQuota, used)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeMonthlyQuotaDenied emits a Problem+JSON 429 with enough
// detail for the customer's client to surface "you hit your
// monthly cap" plus the actual cap + observed counter. Kept
// separate from the rate-limit 429 so dashboards can split the
// two failure modes cleanly.
//
// The body is built via `encoding/json.Marshal` so the
// caller-controlled `r.URL.Path` is properly escaped (any quote
// / control char in a maliciously-crafted path can't break out
// of the JSON string).
func writeMonthlyQuotaDenied(w http.ResponseWriter, r *http.Request, quota, used int64) {
	payload := map[string]any{
		"type":          "https://api.ratesengine.net/errors/monthly-quota-exceeded",
		"title":         "Monthly quota exceeded",
		"status":        429,
		"detail":        "The API key's monthly request quota has been reached. Reset on the 1st UTC.",
		"instance":      r.URL.Path,
		"monthly_quota": quota,
		"month_to_date": used,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		// JSON marshal of a map[string]{string,int64} cannot fail
		// under any input the production path produces; treat as
		// a defence-in-depth fallback.
		body = []byte(`{"title":"Monthly quota exceeded","status":429}`)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-RatesEngine-Monthly-Quota", itoa(quota))
	w.Header().Set("X-RatesEngine-Monthly-Used", itoa(used))
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write(body)
}

// itoa is the tiny non-allocating int64-to-decimal-string helper
// the response-header values use. strconv is fine but the header
// is small + on the cap-hit path; this keeps the formatter
// trivial.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
